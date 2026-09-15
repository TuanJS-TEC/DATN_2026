"""Nạp giá rổ VN30 và chỉ số VNINDEX, VN30 từ SSI vào company, price_daily, index_daily.

    python3 -m venv .venv && .venv/bin/pip install -r etl/requirements.txt
    .venv/bin/python etl/prices.py                          # 01/01/2026 -> hôm nay
    .venv/bin/python etl/prices.py --from 2026-06-01 --to 2026-06-30

Chạy lại an toàn: ghi đè theo khóa (mã, ngày). Chạy trong giờ giao dịch thì phiên hôm nay chưa chốt,
lần chạy sau sẽ sửa lại. Kết nối qua DATABASE_URL, mặc định postgresql://localhost/datn.
"""
import argparse
import datetime as dt
import json
import os
import urllib.parse
import urllib.request
from decimal import Decimal

import psycopg

GROUP = "https://iboard-query.ssi.com.vn/stock/group/VN30"
HISTORY = "https://iboard-api.ssi.com.vn/statistics/company/ssmi/stock-info"
INDEXES = ["VNINDEX", "VN30"]  # VNINDEX cũng là lịch phiên của sentiment_daily
ICT = dt.timezone(dt.timedelta(hours=7))


def fetch(url):
    # SSI chặn User-Agent mặc định, như internal/httpx.
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0", "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=20) as r:
        return json.load(r)


def history(symbol, start, end):
    """Mọi phiên của symbol trong [start, end]. SSI cắt pageSize ở 40 nên phải lật trang."""
    rows, page = [], 1
    while True:
        q = urllib.parse.urlencode({"symbol": symbol, "page": page, "pageSize": 40,
                                    "fromDate": f"{start:%d/%m/%Y}", "toDate": f"{end:%d/%m/%Y}"})
        j = fetch(f"{HISTORY}?{q}")
        if j.get("code") != "SUCCESS":
            raise RuntimeError(f"{symbol}: {j.get('message')}")
        rows += j["data"]
        if not j["data"] or len(rows) >= j["paging"]["total"]:
            return rows
        page += 1


def day(r):
    return dt.datetime.strptime(r["tradingDate"], "%d/%m/%Y").date()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--from", dest="start", type=dt.date.fromisoformat, default=dt.date(2026, 1, 1))
    ap.add_argument("--to", dest="end", type=dt.date.fromisoformat, default=dt.datetime.now(ICT).date())
    a = ap.parse_args()

    stocks = fetch(GROUP)["data"]
    with psycopg.connect(os.environ.get("DATABASE_URL", "postgresql://localhost/datn")) as db:
        db.cursor().executemany(
            """INSERT INTO company (symbol, name, exchange) VALUES (%s, %s, %s)
               ON CONFLICT (symbol) DO UPDATE SET name = excluded.name, exchange = excluded.exchange""",
            [(s["stockSymbol"], s["companyNameVi"], s["exchange"].upper()) for s in stocks])

        for code in INDEXES:
            rows = history(code, a.start, a.end)
            # Chỉ số: các cột *Raw của SSI luôn là 0, giá nằm ở close.
            db.cursor().executemany(
                """INSERT INTO index_daily (code, d, close, volume, value) VALUES (%s, %s, %s, %s, %s)
                   ON CONFLICT (code, d) DO UPDATE
                   SET close = excluded.close, volume = excluded.volume, value = excluded.value""",
                [(code, day(r), Decimal(r["close"]), int(r["totalMatchVol"]), Decimal(r["totalMatchVal"])) for r in rows])
            db.commit()
            print(f"{code}: {len(rows)} phiên")

        for s in stocks:
            sym = s["stockSymbol"]
            # Giá 0 (ngừng giao dịch) thành biến động giả trong chuỗi huấn luyện: bỏ, kiểm tra phiên bên dưới sẽ báo.
            rows = [r for r in history(sym, a.start, a.end) if Decimal(r["closeRaw"]) > 0]
            # Cổ phiếu: *Raw là giá khớp thật; close/closePriceAdjusted đã điều chỉnh cổ tức, chia tách.
            db.cursor().executemany(
                """INSERT INTO price_daily (symbol, d, open, high, low, close, adj_close, volume, value)
                   VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
                   ON CONFLICT (symbol, d) DO UPDATE SET open = excluded.open, high = excluded.high,
                     low = excluded.low, close = excluded.close, adj_close = excluded.adj_close,
                     volume = excluded.volume, value = excluded.value""",
                [(sym, day(r), Decimal(r["openRaw"]), Decimal(r["highRaw"]), Decimal(r["lowRaw"]),
                  Decimal(r["closeRaw"]), Decimal(r["closePriceAdjusted"]), int(r["totalMatchVol"]),
                  Decimal(r["totalMatchVal"])) for r in rows])
            db.commit()
            print(f"{sym}: {len(rows)} phiên")

        # Mỗi mã phải có đủ các phiên của VNINDEX, thiếu phiên thì chuỗi LSTM bị lệch ngày.
        gaps = db.execute(
            """WITH n AS (SELECT count(*) AS want FROM index_daily WHERE code = 'VNINDEX' AND d BETWEEN %(s)s AND %(e)s)
               SELECT c.symbol, count(p.d), n.want
               FROM company c CROSS JOIN n
               LEFT JOIN price_daily p ON p.symbol = c.symbol AND p.d BETWEEN %(s)s AND %(e)s
               WHERE c.symbol = ANY(%(syms)s)
               GROUP BY c.symbol, n.want
               HAVING count(p.d) <> n.want""",
            {"s": a.start, "e": a.end, "syms": [s["stockSymbol"] for s in stocks]}).fetchall()
        for sym, have, want in gaps:
            print(f"CẢNH BÁO {sym}: {have}/{want} phiên (mới niêm yết hoặc ngừng giao dịch?)")

        db.execute("REFRESH MATERIALIZED VIEW feature_daily")
        db.execute("REFRESH MATERIALIZED VIEW sentiment_daily")


if __name__ == "__main__":
    main()
