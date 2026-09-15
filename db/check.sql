-- Kiểm tra point-in-time và gán văn bản vào phiên. Chạy trong schema tạm rồi ROLLBACK, không để lại gì:
--   psql -d postgres -f db/check.sql
\set ON_ERROR_STOP on
BEGIN;
CREATE SCHEMA schema_check;
SET LOCAL search_path = schema_check;
\ir schema.sql

INSERT INTO company (symbol, name, exchange) VALUES ('FPT', 'FPT Corp', 'HOSE');
-- 29/01 thứ Năm, 30/01 thứ Sáu, 02/02 thứ Hai, 30/03 thứ Hai, 31/03 thứ Ba
INSERT INTO index_daily (code, d, close)
SELECT 'VNINDEX', d, 1800 FROM unnest('{2026-01-29,2026-01-30,2026-02-02,2026-03-30,2026-03-31}'::date[]) d;
INSERT INTO price_daily (symbol, d, open, high, low, close, adj_close, volume)
SELECT 'FPT', d, 96000, 96000, 96000, 96000, 96000, 1 FROM index_daily;

INSERT INTO financial_report (symbol, fiscal_year, fiscal_quarter, period_end, published_at, audit_status, consolidated, source) VALUES
  ('FPT', 2025, 3, '2025-09-30', '2025-10-20 17:00+07', 'unaudited', true, 't'),
  ('FPT', 2025, 4, '2025-12-31', '2026-01-30 10:00+07', 'unaudited', true, 't'),  -- trong giờ giao dịch
  ('FPT', 2025, 0, '2025-12-31', '2026-03-30 16:00+07', 'audited',   true, 't');  -- sau 15:00: phiên sau mới dùng
INSERT INTO financial_ratio (report_id, eps_ttm)
SELECT id, CASE fiscal_quarter WHEN 3 THEN 4500 WHEN 4 THEN 5000 ELSE 4800 END FROM financial_report;

INSERT INTO nlp_model (name, version) VALUES ('phobert', 'v1');
INSERT INTO document (source, kind, url, body, published_at, content_hash) VALUES
  ('cafef', 'news', 'a', 'a', '2026-01-30 16:00+07', sha256('a')),  -- tối thứ Sáu -> thứ Hai
  ('cafef', 'news', 'b', 'b', '2026-02-02 14:00+07', sha256('b')),  -- trong phiên thứ Hai
  ('cafef', 'news', 'c', 'c', '2026-01-30 14:59+07', sha256('c'));  -- tin thị trường, trước 15:00
INSERT INTO sentiment (doc_id, model_id, symbol, score)
SELECT id, 1, CASE url WHEN 'c' THEN NULL ELSE 'FPT' END, CASE url WHEN 'a' THEN 0.8 WHEN 'b' THEN -0.4 ELSE 0.5 END
FROM document;

REFRESH MATERIALIZED VIEW feature_daily;
REFRESH MATERIALIZED VIEW sentiment_daily;

DO $$
DECLARE eps numeric;
BEGIN
  eps := (SELECT eps_ttm FROM feature_daily WHERE d = '2026-01-29');
  ASSERT eps = 4500, format('29/01 phải dùng Q3, được %s', eps);
  eps := (SELECT eps_ttm FROM feature_daily WHERE d = '2026-01-30');
  ASSERT eps = 5000, format('30/01 Q4 công bố 10:00 phải dùng được, được %s', eps);
  eps := (SELECT eps_ttm FROM feature_daily WHERE d = '2026-03-30');
  ASSERT eps = 5000, format('30/03 bản kiểm toán 16:00 chưa được dùng, được %s', eps);
  eps := (SELECT eps_ttm FROM feature_daily WHERE d = '2026-03-31');
  ASSERT eps = 4800, format('31/03 phải dùng bản kiểm toán, được %s', eps);
  ASSERT (SELECT pe FROM feature_daily WHERE d = '2026-03-31') = 20, 'P/E = 96000 / 4800';

  ASSERT (SELECT n_docs FROM sentiment_daily WHERE symbol = 'FPT' AND d = '2026-02-02') = 2, 'tin tối thứ Sáu phải sang thứ Hai';
  ASSERT abs((SELECT mean_score FROM sentiment_daily WHERE symbol = 'FPT' AND d = '2026-02-02') - 0.2) < 1e-6, 'trung bình (0.8 - 0.4) / 2';
  ASSERT NOT EXISTS (SELECT FROM sentiment_daily WHERE symbol = 'FPT' AND d = '2026-01-30'), 'không có tin FPT trước 15:00 thứ Sáu';
  ASSERT (SELECT n_docs FROM sentiment_daily WHERE symbol IS NULL AND d = '2026-01-30') = 1, 'tin thị trường 14:59 thuộc phiên thứ Sáu';

  BEGIN
    INSERT INTO sentiment (doc_id, model_id, symbol, score) SELECT id, 1, NULL, 0 FROM document WHERE url = 'c';
    RAISE EXCEPTION 'trùng cảm xúc thị trường (symbol NULL) phải bị chặn';
  EXCEPTION WHEN unique_violation THEN NULL;
  END;
END $$;

\echo 'check.sql: OK'
ROLLBACK;
