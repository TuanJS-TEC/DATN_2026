"""Nạp tin CafeF về các mã trong bảng company vào document, document_symbol. Chạy etl/prices.py trước.

    .venv/bin/python etl/news.py                          # 01/01/2026 -> hôm nay
    .venv/bin/python etl/news.py --from 2026-09-01

Nguồn là danh sách tin theo mã của CafeF: có tin cũ, còn trang chuyên mục (internal/news) chỉ có tin mới.
Bỏ văn bản công bố thông tin (/du-lieu/..., nội dung là PDF). Chạy lại chỉ tải bài chưa có.
"""
import argparse
import datetime as dt
import hashlib
import html
import http.client
import os
import re
import sys
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from html.parser import HTMLParser

import psycopg

CAFEF = "https://cafef.vn"
LISTING = CAFEF + "/du-lieu/Ajax/Events_RelatedNews_New.aspx?symbol={}&floorID=0&configID=0&PageIndex={}&PageSize=30&Type=2"
ICT = dt.timezone(dt.timedelta(hours=7))

reItem = re.compile(r'timeTitle">([^<]+)</span>.*?href="([^"?]+)[^"]*" title=', re.S)
rePath = re.compile(r"^/[a-z0-9-]+\.chn$")
reTitle = re.compile(r'(?s)data-role="title"[^>]*>\s*(.*?)\s*</h1>')
reSapo = re.compile(r'(?s)data-role="sapo"[^>]*>\s*(.*?)\s*</p>')
reAuthor = re.compile(r'(?s)data-role="author"[^>]*>\s*(?:<b>)?(.*?)(?:</b>)?\s*</span>')
reDate = re.compile(r'data-role="publishdate"[^>]*datetime="([^"]+)"')
reLdDate = re.compile(r'"datePublished"\s*:\s*"([^"]+)"')  # JSON-LD: bài eMagazine không có publishdate
# Widget giá ẩn đầu bài liệt kê các mã CafeF gắn cho bài: "VHM&amp;VIC&amp;FPT:".
reTags = re.compile(r'class="title_box"><a[^>]*>\s*([A-Z0-9]{3}(?:&amp;[A-Z0-9]{3})*):')
reTag = re.compile(r"<[^>]*>")


def get(url):
    # CafeF chặn User-Agent mặc định, như internal/httpx.
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    with urllib.request.urlopen(req, timeout=20) as r:
        return r.read().decode("utf-8", "replace")


def listing(sym, start, end):
    """URL bài báo về sym có ngày trong [start, end]. Danh sách xếp mới trước, lật trang tới trước start."""
    urls, page, prev = [], 1, None
    while True:
        items = reItem.findall(get(LISTING.format(sym, page)))
        if not items or items == prev:  # hết trang (hoặc CafeF trả lại trang cuối)
            break
        for t, path in items:
            if start <= dt.datetime.strptime(t, "%d/%m/%Y %H:%M").date() <= end and rePath.match(path):
                urls.append(CAFEF + path)
        if dt.datetime.strptime(items[-1][0], "%d/%m/%Y %H:%M").date() < start:
            break
        prev, page = items, page + 1
    return list(dict.fromkeys(urls))


class Body(HTMLParser):
    """Chữ trong khối data-role="content" đầu tiên, bỏ widget giá, quảng cáo, tin liên quan, ảnh
    (cùng danh sách với clean() trong internal/news/article.go)."""
    SKIP_BOX = re.compile(r"chisochungkhoan|admzone|inner-article-|tindnd|link-content-footer")
    SKIP_TAG = {"script", "style", "noscript", "iframe", "figure"}
    BREAK = {"p", "div", "br", "li", "tr", "h2", "h3", "h4"}

    def __init__(self):
        super().__init__()
        self.depth = 0         # số div đang mở trong khối content; 0 = ngoài khối
        self.skip_at = None    # depth của div bị bỏ đang mở
        self.skip_tags = 0
        self.done = False
        self.parts = []

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if self.done:
            return
        if not self.depth:
            if tag == "div" and a.get("data-role") == "content":
                self.depth = 1
            return
        if tag == "div":
            self.depth += 1
            if self.skip_at is None and self.SKIP_BOX.search(f'{a.get("class", "")} {a.get("id", "")}'):
                self.skip_at = self.depth
        elif tag in self.SKIP_TAG:
            self.skip_tags += 1
        if tag in self.BREAK:
            self.parts.append("\n")

    def handle_endtag(self, tag):
        if self.done or not self.depth:
            return
        if tag == "div":
            if self.skip_at == self.depth:
                self.skip_at = None
            self.depth -= 1
            self.done = self.depth == 0
        elif tag in self.SKIP_TAG:
            self.skip_tags = max(0, self.skip_tags - 1)
        if tag in self.BREAK:
            self.parts.append("\n")

    def handle_data(self, data):
        if self.depth and self.skip_at is None and not self.skip_tags:
            self.parts.append(data)

    def text(self):
        lines = (" ".join(line.split()) for line in "".join(self.parts).splitlines())
        return "\n".join(line for line in lines if line)


def parse(page):
    """Các trường của một trang bài CafeF, hoặc None khi không thấy nội dung/ngày đăng.
    ponytail: bài longform/infographic dựng riêng (~1-2%) không có khối content nên bị bỏ; thêm bộ tách riêng nếu cần."""
    b = Body()
    b.feed(page)
    date = reDate.search(page) or reLdDate.search(page)
    if not b.text() or not date:
        return None

    def clean(m):
        return " ".join(html.unescape(reTag.sub("", m[1])).split()) if m else ""

    published = dt.datetime.fromisoformat(date[1])
    tags = reTags.search(page)
    sapo = clean(reSapo.search(page))
    return {
        "title": clean(reTitle.search(page)),
        "body": f"{sapo}\n\n{b.text()}".strip(),
        "author": clean(reAuthor.search(page)),
        "published_at": published if published.tzinfo else published.replace(tzinfo=ICT),
        "tags": tags[1].split("&amp;") if tags else [],
    }


def page_or_none(url):
    try:
        return get(url)
    except (OSError, http.client.HTTPException) as e:
        print(f"  bỏ qua {url}: {e}", file=sys.stderr)
        return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--from", dest="start", type=dt.date.fromisoformat, default=dt.date(2026, 1, 1))
    ap.add_argument("--to", dest="end", type=dt.date.fromisoformat, default=dt.datetime.now(ICT).date())
    a = ap.parse_args()

    with psycopg.connect(os.environ.get("DATABASE_URL", "postgresql://localhost/datn")) as db, \
            ThreadPoolExecutor(4) as pool:  # ponytail: 4 luồng cho nhẹ tay với CafeF; tăng nếu không bị chặn
        known = {s for (s,) in db.execute("SELECT symbol FROM company")}
        if not known:
            sys.exit("Bảng company trống: chạy etl/prices.py trước.")
        total_new = total_bad = 0
        for sym in sorted(known):
            urls = listing(sym, a.start, a.end)
            have = {u for (u,) in db.execute("SELECT url FROM document WHERE source = 'cafef' AND url = ANY(%s)", [urls])}
            new = [u for u in urls if u not in have]
            bad = 0
            for url, page in zip(new, pool.map(page_or_none, new)):
                art = parse(page) if page else None
                if not art:
                    if page:
                        print(f"  không tách được (bố cục đặc biệt): {url}", file=sys.stderr)
                    bad += 1
                    continue
                doc = db.execute(
                    """INSERT INTO document (source, kind, url, title, body, raw, author_hash, published_at, content_hash)
                       VALUES ('cafef', 'news', %s, %s, %s, %s, %s, %s, %s)
                       ON CONFLICT (source, url) DO NOTHING RETURNING id""",
                    (url, art["title"], art["body"], page,
                     hashlib.sha256(art["author"].encode()).hexdigest() if art["author"] else None,
                     art["published_at"], hashlib.sha256(art["body"].encode()).digest())).fetchone()
                if doc:
                    db.cursor().executemany(
                        "INSERT INTO document_symbol VALUES (%s, %s, 'cafef') ON CONFLICT DO NOTHING",
                        [(doc[0], t) for t in art["tags"] if t in known])
            # Bài nằm trong danh sách tin của sym thì chắc chắn về sym, kể cả bài đã nạp từ mã khác.
            db.execute("""INSERT INTO document_symbol (doc_id, symbol, method)
                          SELECT id, %s, 'cafef' FROM document WHERE source = 'cafef' AND url = ANY(%s)
                          ON CONFLICT DO NOTHING""", (sym, urls))
            db.commit()
            total_new += len(new) - bad
            total_bad += bad
            print(f"{sym}: {len(urls)} bài, {len(new) - bad} mới" + (f", {bad} lỗi" if bad else ""))
        print(f"Tổng: {total_new} bài mới, {total_bad} lỗi (lỗi mạng thì chạy lại; bố cục đặc biệt thì chạy lại cũng không được)")


if __name__ == "__main__":
    main()
