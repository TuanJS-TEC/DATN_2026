-- Dữ liệu huấn luyện dự báo xu hướng giá: giá + BCTC (định lượng), văn bản + cảm xúc (định tính).
--
-- Ba tầng: thô (giữ nguyên để trích xuất lại) -> đã xử lý -> đặc trưng (materialized view).
-- Point-in-time: đặc trưng của phiên d chỉ dùng dữ liệu công bố trước 15:00 ngày d (sau ATC),
-- nếu không mô hình học được thông tin tương lai (look-ahead bias).
--
-- PostgreSQL 14+. Kiểm tra: psql -d <db> -f db/check.sql

-- ================================================================ Danh mục

CREATE TABLE company (
  symbol      text PRIMARY KEY CHECK (symbol ~ '^[A-Z0-9]{3}$'),
  name        text NOT NULL,
  exchange    text NOT NULL CHECK (exchange IN ('HOSE', 'HNX', 'UPCOM')),
  industry    text,                          -- ngành ICB: ngân hàng không có biên LN gộp, so theo ngành
  aliases     text[] NOT NULL DEFAULT '{}',  -- 'Vinamilk', 'Sữa Việt Nam': gắn tin không nhắc mã
  listed_on   date,
  delisted_on date                           -- giữ cả mã đã hủy niêm yết: tránh survivorship bias
);
-- ponytail: mã CK làm khóa, đổi mã thì ON UPDATE CASCADE; thêm company_id nếu đổi mã thành chuyện thường.

-- ================================================================ Giá

CREATE TABLE price_daily (
  symbol    text NOT NULL REFERENCES company ON UPDATE CASCADE,
  d         date NOT NULL,
  open      numeric(12, 2) NOT NULL,  -- VND, giá thô
  high      numeric(12, 2) NOT NULL,
  low       numeric(12, 2) NOT NULL,
  close     numeric(12, 2) NOT NULL,
  adj_close numeric(14, 4) NOT NULL,  -- điều chỉnh cổ tức/chia tách: đầu vào LSTM; có sự kiện quyền thì tải lại lịch sử
  volume    bigint NOT NULL,
  value     numeric(20, 0),           -- giá trị khớp, VND
  PRIMARY KEY (symbol, d)
);

-- Chỉ số thị trường. VNINDEX đồng thời là lịch ngày giao dịch.
CREATE TABLE index_daily (
  code   text NOT NULL,  -- VNINDEX, VN30, HNX, HNX30, UPCOM (như internal/market)
  d      date NOT NULL,
  close  numeric(10, 2) NOT NULL,
  volume bigint,
  value  numeric(20, 0),
  PRIMARY KEY (code, d)
);

-- ================================================================ Báo cáo tài chính

CREATE TABLE financial_report (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  symbol         text NOT NULL REFERENCES company ON UPDATE CASCADE,
  fiscal_year    smallint NOT NULL,
  fiscal_quarter smallint NOT NULL CHECK (fiscal_quarter BETWEEN 0 AND 4),  -- 0 = cả năm
  period_end     date NOT NULL,
  published_at   timestamptz NOT NULL,  -- mốc point-in-time, không phải period_end (BCTC Q4 cuối tháng 1 mới ra)
  audit_status   text NOT NULL CHECK (audit_status IN ('unaudited', 'reviewed', 'audited')),
  consolidated   boolean NOT NULL,      -- hợp nhất / riêng lẻ
  source         text NOT NULL,         -- 'vietstock', 'cafef', 'hsx'...
  source_url     text,
  -- Một kỳ có nhiều bản (tự lập -> soát xét/kiểm toán): giữ tất cả, bản sau không ghi đè bản trước.
  UNIQUE (symbol, fiscal_year, fiscal_quarter, consolidated, published_at)
);
CREATE INDEX ON financial_report (symbol, period_end DESC, published_at DESC);

-- Dạng dọc: mẫu BCTC doanh nghiệp (TT200), ngân hàng, chứng khoán, bảo hiểm khác nhau,
-- nên chỉ tiêu là dữ liệu chứ không phải cột.
CREATE TABLE financial_item (
  report_id bigint NOT NULL REFERENCES financial_report ON DELETE CASCADE,
  code      text NOT NULL,  -- 'revenue', 'cogs', 'net_income_parent', 'total_assets', 'equity', 'total_debt',
                            -- 'current_assets', 'current_liabilities', 'inventory', 'shares_outstanding'...
  value     numeric(24, 2) NOT NULL,
  PRIMARY KEY (report_id, code)
);

-- Chỉ số chuẩn hóa, ETL tính từ financial_item; một dòng mỗi bản BCTC (chỉ bản hợp nhất nếu DN có).
-- Dạng ngang vì bộ chỉ số do đề tài cố định; thêm chỉ số = ALTER TABLE ADD COLUMN.
-- P/E không ở đây: nó đổi theo giá mỗi phiên, tính trong feature_daily.
CREATE TABLE financial_ratio (
  report_id          bigint PRIMARY KEY REFERENCES financial_report ON DELETE CASCADE,
  eps_ttm            numeric(14, 2),  -- LNST cổ đông mẹ 4 quý gần nhất / CP lưu hành
  roe                real,            -- LNST TTM / VCSH bình quân
  roa                real,
  debt_to_equity     real,
  gross_margin       real,            -- NULL với ngân hàng
  revenue_growth_yoy real,            -- so với cùng kỳ năm trước
  current_ratio      real,            -- thanh toán hiện hành
  quick_ratio        real             -- thanh toán nhanh
);

-- ================================================================ Văn bản

-- ponytail: chưa partition. Khi F247/Facebook đẩy lên vài chục triệu dòng: partition document và
-- sentiment theo tháng published_at, chuyển cột raw sang object storage.
CREATE TABLE document (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  source       text NOT NULL,  -- 'cafef', 'vnexpress', 'vietstock', 'bloomberg_vn', 'f247', 'facebook', 'ssi_research'...
  kind         text NOT NULL CHECK (kind IN ('news', 'analyst_report', 'forum_post', 'comment')),
  url          text NOT NULL,  -- hoặc id bài đăng khi nguồn không có URL ổn định
  parent_id    bigint REFERENCES document,  -- bình luận -> bài gốc
  lang         text NOT NULL DEFAULT 'vi',  -- chọn PhoBERT (vi) hay FinBERT (en)
  title        text,
  body         text NOT NULL,  -- văn bản sạch, đầu vào NLP
  raw          text,           -- HTML/JSON gốc: parser đổi thì trích xuất lại, không cần cào lại (TOAST tự nén, lưu ngoài dòng)
  author_hash  text,           -- sha256(tác giả): nhận tài khoản spam/thao túng mà không lưu danh tính
  published_at timestamptz NOT NULL,
  crawled_at   timestamptz NOT NULL DEFAULT now(),
  content_hash bytea NOT NULL, -- sha256(body): nhận bài đăng lại giữa các nguồn
  meta         jsonb NOT NULL DEFAULT '{}',  -- chuyên mục, lượt thích, khuyến nghị & giá mục tiêu (báo cáo phân tích)...
  UNIQUE (source, url)         -- crawler chạy lại an toàn: ON CONFLICT DO NOTHING
);
CREATE INDEX ON document (published_at);
CREATE INDEX ON document (content_hash);

-- Văn bản nhắc tới mã nào. method: 'regex' (internal/news/tag.go), 'alias', 'ner', 'llm'.
CREATE TABLE document_symbol (
  doc_id bigint NOT NULL REFERENCES document ON DELETE CASCADE,
  symbol text NOT NULL REFERENCES company ON UPDATE CASCADE,
  method text NOT NULL,
  PRIMARY KEY (doc_id, symbol)
);
CREATE INDEX ON document_symbol (symbol);

-- ================================================================ NLP

-- Mỗi model/checkpoint/prompt là một dòng: FinBERT, PhoBERT, GPT-4o-mini chấm cùng một văn bản để so sánh.
-- Nhãn tay cũng là một "model" (name = 'human'): tập vàng để fine-tune và đánh giá các model kia.
CREATE TABLE nlp_model (
  id      int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name    text NOT NULL,  -- 'phobert-base-v2', 'finbert', 'gpt-4o-mini', 'human'
  version text NOT NULL,  -- hash checkpoint / phiên bản prompt / người gán nhãn
  params  jsonb NOT NULL DEFAULT '{}',
  UNIQUE (name, version)
);

CREATE TABLE sentiment (
  doc_id     bigint NOT NULL REFERENCES document ON DELETE CASCADE,
  model_id   int NOT NULL REFERENCES nlp_model,
  symbol     text REFERENCES company ON UPDATE CASCADE,  -- NULL = cảm xúc chung (thị trường) của văn bản
  aspect     text NOT NULL DEFAULT 'overall',  -- 'overall', 'revenue', 'profit', 'debt', 'dividend',
                                               -- 'management', 'valuation', 'macro'...
  score      real NOT NULL CHECK (score BETWEEN -1 AND 1),  -- -1 tiêu cực .. 1 tích cực
  confidence real CHECK (confidence BETWEEN 0 AND 1),
  created_at timestamptz NOT NULL DEFAULT now()
);
-- coalesce: UNIQUE thường coi các NULL là khác nhau, sẽ cho trùng dòng cảm xúc thị trường.
CREATE UNIQUE INDEX sentiment_key ON sentiment (doc_id, model_id, coalesce(symbol, ''), aspect);
CREATE INDEX ON sentiment (symbol, model_id);

-- ================================================================ Đặc trưng
-- Sau mỗi lần ETL: REFRESH MATERIALIZED VIEW feature_daily; REFRESH MATERIALIZED VIEW sentiment_daily;

-- Giá + chỉ số tài chính point-in-time: kỳ BCTC gần nhất, bản mới nhất đã công bố trước 15:00 ngày d.
CREATE MATERIALIZED VIEW feature_daily AS
SELECT p.symbol, p.d, p.close, p.adj_close, p.volume, p.value,
       p.close / nullif(r.eps_ttm, 0) AS pe,
       r.eps_ttm, r.roe, r.roa, r.debt_to_equity, r.gross_margin,
       r.revenue_growth_yoy, r.current_ratio, r.quick_ratio,
       r.published_at AS fin_published_at
FROM price_daily p
LEFT JOIN LATERAL (
  SELECT fr.*, f.published_at
  FROM financial_report f
  JOIN financial_ratio fr ON fr.report_id = f.id
  WHERE f.symbol = p.symbol
    AND f.published_at < (p.d + time '15:00') AT TIME ZONE 'Asia/Ho_Chi_Minh'
  ORDER BY f.period_end DESC, f.published_at DESC
  LIMIT 1
) r ON true;
CREATE UNIQUE INDEX ON feature_daily (symbol, d);

-- Cảm xúc gộp theo (mã, phiên, model, khía cạnh, loại văn bản); pipeline pivot thành cột.
-- Văn bản sau 15:00 hoặc vào ngày nghỉ tính cho phiên kế tiếp. symbol NULL = cảm xúc thị trường.
CREATE MATERIALIZED VIEW sentiment_daily AS
SELECT s.symbol, t.d, s.model_id, s.aspect, doc.kind,
       count(*) AS n_docs,
       avg(s.score) AS mean_score,
       sum(s.score * coalesce(s.confidence, 1)) / nullif(sum(coalesce(s.confidence, 1)), 0) AS weighted_score
FROM sentiment s
JOIN document doc ON doc.id = s.doc_id
CROSS JOIN LATERAL (
  SELECT i.d FROM index_daily i
  WHERE i.code = 'VNINDEX'
    AND i.d >= ((doc.published_at AT TIME ZONE 'Asia/Ho_Chi_Minh') + interval '9 hours')::date  -- 15:00 + 9h = sang ngày
  ORDER BY i.d
  LIMIT 1
) t
GROUP BY s.symbol, t.d, s.model_id, s.aspect, doc.kind;
CREATE INDEX ON sentiment_daily (symbol, d);

-- ================================================================ Thí nghiệm

-- Ảnh chụp tập huấn luyện: tham số đủ để dựng lại + file đã xuất, vì dữ liệu nguồn còn được sửa/tính lại.
CREATE TABLE dataset (
  id         int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name       text NOT NULL UNIQUE,
  -- {"symbols": [...], "from": "2018-01-01", "train_end": "2024-06-30", "val_end": "2025-06-30",
  --  "window": 30, "horizon": 5, "up_threshold": 0.02, "sentiment_model_id": 3}
  -- Chia theo thời gian, không xáo trộn ngẫu nhiên.
  params     jsonb NOT NULL,
  uri        text NOT NULL,  -- data/datasets/vn30_h5_v1.parquet
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Mỗi lần train: ablation (chỉ giá / +tài chính / +cảm xúc), fusion concat vs attention,
-- chỉ số trên tập test và đóng góp SHAP theo nhóm đặc trưng.
CREATE TABLE model_run (
  id           int GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  dataset_id   int NOT NULL REFERENCES dataset,
  arch         text NOT NULL,  -- 'gru+phobert/attention'
  hparams      jsonb NOT NULL DEFAULT '{}',
  metrics      jsonb NOT NULL, -- {"accuracy": .., "precision": .., "recall": .., "f1": .., "auc_roc": ..}
  shap_groups  jsonb,          -- {"price": .., "financial": .., "sentiment": ..}
  artifact_uri text,           -- checkpoint
  created_at   timestamptz NOT NULL DEFAULT now()
);
