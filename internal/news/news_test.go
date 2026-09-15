package news

import (
	"strings"
	"testing"
)

const page = `
<div role="article" class="tlitem box-category-item" data-id="1">
  <h3><a href="/bai-mot-188.chn">Tiêu đề &amp; một</a></h3>
  <div class="tlitem-flex">
    <a class="avatar" href="/bai-mot-188.chn" title="x">
      <img src="https://cafefcdn.com/zoom/250_157/a.png" alt="x"></a>
    <div class="knswli-right">
      <span class="time time-ago" title="2026-09-15T15:09:00">2026-09-15T15:09:00</span>
      <p class="sapo box-category-sapo">Tóm t&#7855;t.</p>
    </div>
  </div>
</div>
<div role="article" class="firstitem" data-id="2">
  <h3><a href="/bai-hai-189.chn">Bài hai</a></h3>
  <p class="time" data-time="214/09/2026 - 21:56">x</p>
</div>
<div role="article" data-id="3">
  <h3><a href="/bai-ba-188260915150505817.chn">Bài ba</a></h3>
</div>`

func TestParseList(t *testing.T) {
	got := parseList(page)
	if len(got) != 3 {
		t.Fatalf("want 3 articles, got %d: %+v", len(got), got)
	}
	a := got[0]
	if a.URL != "/bai-mot-188.chn" || a.Title != "Tiêu đề & một" ||
		a.Img != "https://cafefcdn.com/zoom/250_157/a.png" ||
		a.Time != "2026-09-15T15:09:00" || a.Sapo != "Tóm tắt." {
		t.Fatalf("bad parse: %+v", a)
	}
	if got[1].URL != "/bai-hai-189.chn" || got[1].Img != "" || got[1].Time != "2026-09-14T21:56:00" {
		t.Fatalf("featured card: %+v", got[1])
	}
	// No time in the markup at all: fall back to the id embedded in the URL.
	if got[2].Time != "2026-09-15T15:05:05" {
		t.Fatalf("id-derived time: %+v", got[2])
	}
	if len(parseList("<html>no articles</html>")) != 0 {
		t.Fatal("empty page must yield no articles")
	}
}

func TestClean(t *testing.T) {
	body := `<div class="x"><div class="detail-content" data-role="content">` +
		`<div class="chisochungkhoan" style="display:none"><div>GEX</div></div>` +
		`<p onclick="steal()">Nội dung <a href="/co-phieu.html">link</a></p>` +
		`<script>evil()</script><iframe src="//ads"></iframe>` +
		`<img src="https://cafefcdn.com/b.jpg"></div></div>`
	c := clean(body)
	for _, bad := range []string{"<script", "<iframe", "onclick", "chisochungkhoan", "GEX"} {
		if strings.Contains(c, bad) {
			t.Fatalf("%q survived clean: %s", bad, c)
		}
	}
	for _, want := range []string{"Nội dung", `href="https://cafef.vn/co-phieu.html"`, "cafefcdn.com/b.jpg"} {
		if !strings.Contains(c, want) {
			t.Fatalf("%q missing from clean: %s", want, c)
		}
	}
	if clean("<div>no content marker</div>") != "" {
		t.Fatal("missing content div must return empty")
	}
}

func TestTag(t *testing.T) {
	set := map[string]bool{"GEX": true, "HPG": true, "C69": true, "SSI": true, "HCM": true}
	got := tag("GELEX (GEX) lên tiếng, HPG và C69 tăng; SSI Research: GEX, ssi, HPGX, USD, nhà ở TP.HCM", set)
	if strings.Join(got, ",") != "GEX,HPG,C69,SSI" {
		t.Fatalf("got %v", got)
	}
	if got := tag("Không nhắc mã nào", set); got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil, got %#v", got)
	}
}

func TestParseRSS(t *testing.T) {
	feed := `<?xml version="1.0" encoding="UTF-8"?><rss><channel>
<item><title>EVN hết lỗ &amp;quot;lũy kế&amp;quot;</title>
<description><![CDATA[<a href="x"><img src="y"></a></br>EVN xử lý &amp; lãi.]]></description>
<pubDate>Tue, 15 Sep 2026 15:56:28 +0700</pubDate>
<link>https://vnexpress.net/evn-het-lo-5120560.html</link>
<enclosure type="image/jpeg" url="https://i1.vnecdn.net/a.png?w=1200&amp;h=0"/></item>
<item><title>Video</title><link>https://video.vnexpress.net/v-1.html</link></item>
</channel></rss>`
	got := parseRSS([]byte(feed))
	if len(got) != 1 {
		t.Fatalf("want 1 article, got %+v", got)
	}
	a := got[0]
	if a.URL != "https://vnexpress.net/evn-het-lo-5120560.html" || a.Title != `EVN hết lỗ "lũy kế"` ||
		a.Img != "https://i1.vnecdn.net/a.png?w=1200&h=0" || a.Time != "2026-09-15T15:56:28" || a.Sapo != "EVN xử lý & lãi." {
		t.Fatalf("bad parse: %+v", a)
	}
}

func TestCleanVne(t *testing.T) {
	page := `<article class="fck_detail "><h1 class="title-detail mt20">T</h1><p class="description">S</p>` +
		`<p class="Normal">Thân <a href="/x.html">bài</a></p><script>evil()</script>` +
		`<img alt="a" class="lazy" src="data:image/gif;base64,R0l" data-src="https://i1.vnecdn.net/b.jpg">` +
		`<p class="Normal" style="text-align:right;"><strong>Phương Dung</strong></p></article><div>rác</div>`
	c := cleanVne(page)
	for _, bad := range []string{"<script", "data:image", "title-detail", "description", "Phương Dung", "rác", "article"} {
		if strings.Contains(c, bad) {
			t.Fatalf("%q survived: %s", bad, c)
		}
	}
	for _, want := range []string{"Thân", `src="https://i1.vnecdn.net/b.jpg"`, `href="https://vnexpress.net/x.html"`} {
		if !strings.Contains(c, want) {
			t.Fatalf("%q missing: %s", want, c)
		}
	}
}
