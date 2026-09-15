"""Bộ tách bài CafeF: giữ nội dung, bỏ widget giá, ảnh, tin liên quan. Chạy: .venv/bin/python etl/test_news.py"""
import datetime as dt

from news import parse

PAGE = """
<h1 class="title" data-role="title" itemprop="headline">
    FPT &amp; MWG   lãi kỷ lục
</h1>
<span data-role="author" itemprop="author"><b>Linh Linh</b></span>
<span class="pdate" data-role="publishdate" itemprop="datePublished" datetime="2026-07-05T00:03:00+07:00">05-07-2026</span>
<p class="sapo" data-role="sapo" itemprop="description">
    Doanh thu <b>tăng</b> 20%.
</p>
<div class="detail-content afcbc-body" data-role="content">
  <div class="chisochungkhoan" style="display: none">
    <h2 class="title_box"><a title="TIN DOANH NGHIỆP" href="javascript:;" target="_blank">
        FPT&amp;MWG&amp;ABC: <span></span></a></h2>
    <div class="box1"><div class="gia">Giá hiện tại</div></div>
  </div>
  <p>Đoạn một.</p>
  <figure class="VCSortableInPreviewMode"><div><img src="x.jpg"></div><figcaption>Ảnh minh họa</figcaption></figure>
  <script>var ad = "quảng cáo";</script>
  <p>Đoạn   hai<br>dòng mới.</p>
  <div class="VCSortableInPreviewMode link-content-footer"><a href="/khac.chn">Tin khác</a></div>
</div>
<div class="tindnd clearfix">Tin liên quan</div>
<div data-role="content"><p>Khối thứ hai, không lấy.</p></div>
"""

a = parse(PAGE)
assert a["title"] == "FPT & MWG lãi kỷ lục", a["title"]
assert a["author"] == "Linh Linh", a["author"]
assert a["published_at"] == dt.datetime(2026, 7, 5, 0, 3, tzinfo=dt.timezone(dt.timedelta(hours=7))), a["published_at"]
assert a["tags"] == ["FPT", "MWG", "ABC"], a["tags"]
assert a["body"] == "Doanh thu tăng 20%.\n\nĐoạn một.\nĐoạn hai\ndòng mới.", repr(a["body"])
assert parse("<html>trang lỗi</html>") is None

# eMagazine: không có publishdate, ngày nằm trong JSON-LD và không kèm múi giờ.
mag = parse('<script>{"datePublished": "2026-01-05T07:00:00"}</script><div data-role="content"><p>Bài dài.</p></div>')
assert mag["published_at"] == dt.datetime(2026, 1, 5, 7, 0, tzinfo=dt.timezone(dt.timedelta(hours=7))), mag["published_at"]
print("test_news.py: OK")
