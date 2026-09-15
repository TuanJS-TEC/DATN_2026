package docs

import "testing"

func TestParseRows(t *testing.T) {
	raw := []byte(`[
	 {"StockCode":"HPG","CatID":"HoSE","CompanyName":" Hòa Phát ","Url":"https://static2.vietstock.vn/data/HOSE/2026/BCTC/VN/QUY 2/HPG.pdf",
	  "FullName":"Báo cáo tài chính Hợp nhất quý 2 năm 2026 ","FileExt":".pdf ","LastUpdate":"\/Date(1785460097350)\/","TotalRow":3},
	 {"StockCode":"Eximland","CatID":"OTC","CompanyName":"Eximland","Url":"https://static2.vietstock.vn/a.zip",
	  "FullName":"BCTC","FileExt":".ZIP","LastUpdate":"garbage","TotalRow":3},
	 {"StockCode":"BAD","CatID":"HNX","Url":"javascript:alert(1)","FullName":"x","TotalRow":3}
	]`)
	docs, total, err := parseRows(raw)
	if err != nil || total != 3 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	if len(docs) != 2 {
		t.Fatalf("javascript: URL must be dropped, got %d docs: %+v", len(docs), docs)
	}
	d := docs[0]
	if d.Sym != "HPG" || d.Exchange != "HOSE" || d.Company != "Hòa Phát" || d.Ext != "pdf" ||
		d.URL != "https://static2.vietstock.vn/data/HOSE/2026/BCTC/VN/QUY%202/HPG.pdf" ||
		d.Title != "Báo cáo tài chính Hợp nhất quý 2 năm 2026" || d.Time != "2026-07-31T08:08:17" {
		t.Fatalf("bad doc: %+v", d)
	}
	if docs[1].Sym != "" || docs[1].Time != "" || docs[1].Ext != "zip" {
		t.Fatalf("name-only OTC code must not become a ticker: %+v", docs[1])
	}
	if _, _, err := parseRows([]byte(`<html>Object moved</html>`)); err == nil {
		t.Fatal("HTML error page must be an error, not an empty list")
	}
}

func TestToken(t *testing.T) {
	for _, page := range []string{
		`<input name=__RequestVerificationToken type=hidden value=abc-123_X>`,
		`<input name="__RequestVerificationToken" type="hidden" value="abc-123_X" />`,
	} {
		if m := reToken.FindStringSubmatch(page); m == nil || m[1] != "abc-123_X" {
			t.Fatalf("token not found in %s: %v", page, m)
		}
	}
}
