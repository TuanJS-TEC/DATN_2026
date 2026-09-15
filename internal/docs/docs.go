// Package docs proxies Vietstock's company-document library: BCTC, giải trình
// KQKD, báo cáo quản trị, báo cáo thường niên, nghị quyết and tài liệu ĐHĐCĐ.
//
// The pages at finance.vietstock.vn/tai-lieu/*.htm are empty shells filled by
// POSTs to /data/getrpt*. Those sit behind ASP.NET anti-forgery: they need the
// session cookie AND the form token from a page load, or they 302 to /Error.
package docs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tuanhm/internal/httpx"
)

const vs = "https://finance.vietstock.vn"

// Whitelist: our key -> Vietstock DocumentTypeID (from /data/getrptdoctype).
var types = map[string]string{
	"BCTC":        "1",
	"GIAI-TRINH":  "8",
	"QUAN-TRI":    "9",
	"THUONG-NIEN": "2",
	"NGHI-QUYET":  "4",
	"DHCD":        "5",
}

// Vietstock exchangeID; "" = every exchange, OTC included.
var exchanges = map[string]string{"": "0", "HOSE": "1", "HNX": "2", "UPCOM": "5", "OTC": "3"}

const perPage = 20

// ---- session ----

var (
	mu      sync.Mutex
	client  *http.Client
	token   string
	reToken = regexp.MustCompile(`name="?__RequestVerificationToken"?[^>]*value="?([^" >]+)`)
)

// login loads a document page for a fresh cookie + form token. Caller holds mu.
func login() error {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{
		Timeout: 15 * time.Second,
		Jar:     jar,
		// A rejected session answers 302 -> /Error/Index: stop there so post can see it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, _ := http.NewRequest("GET", vs+"/tai-lieu/bao-cao-tai-chinh.htm", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	page, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	m := reToken.FindSubmatch(page)
	if m == nil {
		return errors.New("vietstock: no anti-forgery token (layout changed?)")
	}
	client, token = c, string(m[1])
	return nil
}

// post calls a /data/ endpoint, logging in again once if the session was rejected.
func post(path string, form url.Values) ([]byte, error) {
	for retry := false; ; retry = true {
		mu.Lock()
		if client == nil || retry {
			if err := login(); err != nil {
				mu.Unlock()
				return nil, err
			}
		}
		c, tok := client, token
		mu.Unlock()

		f := url.Values{"__RequestVerificationToken": {tok}}
		for k, v := range form {
			f[k] = v
		}
		req, _ := http.NewRequest("POST", vs+"/data/"+path, strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		res, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == http.StatusFound && !retry {
			continue
		}
		if err != nil {
			return nil, err
		}
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("upstream %d", res.StatusCode)
		}
		return body, nil
	}
}

// ---- terms ----

type term struct {
	Year int    `json:"year"`
	ID   int    `json:"id"`
	Name string `json:"name"` // "Quý 2", "6T", "Năm"
}

func terms(typ string) ([]term, error) {
	b, err := httpx.Cached("docterms:"+typ, 6*time.Hour, func() ([]byte, error) {
		raw, err := post("getrptterm", url.Values{"documentTypeID": {types[typ]}, "top": {"8"}})
		if err != nil {
			return nil, err
		}
		var in []struct {
			YearPeriod   int
			ReportTermID int
			Description  string
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("terms: %w", err)
		}
		out := []term{}
		for _, t := range in {
			out = append(out, term{t.YearPeriod, t.ReportTermID, t.Description})
		}
		return json.Marshal(out)
	})
	if err != nil {
		return nil, err
	}
	var out []term
	return out, json.Unmarshal(b, &out)
}

// Terms serves the reporting periods available for one document type (?type=).
func Terms(w http.ResponseWriter, r *http.Request) {
	typ := httpx.Param(r, "type", "BCTC")
	if _, ok := types[typ]; !ok {
		httpx.Fail(w, 400, "unknown type: "+typ)
		return
	}
	ts, err := terms(typ)
	if err != nil {
		httpx.Fail(w, 502, err.Error())
		return
	}
	out, _ := json.Marshal(ts)
	httpx.Send(w, 200, out)
}

// ---- documents ----

type doc struct {
	Sym      string `json:"sym"` // "" for OTC firms Vietstock lists by name
	Exchange string `json:"exchange"`
	Company  string `json:"company"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Ext      string `json:"ext"`
	Time     string `json:"time"`
}

type row struct {
	StockCode   string
	CatID       string
	CompanyName string
	Url         string
	FullName    string
	FileExt     string
	LastUpdate  string
	TotalRow    int
}

var (
	reSym  = regexp.MustCompile(`^[A-Z0-9]{3}$`)
	reDate = regexp.MustCompile(`^/Date\((\d+)\)/$`)
	vnTZ   = time.FixedZone("ICT", 7*3600)
)

// parseRows turns getrptfile's rows into docs plus the total row count.
func parseRows(raw []byte) ([]doc, int, error) {
	var rows []row
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, 0, fmt.Errorf("documents: %w", err)
	}
	out, total := []doc{}, 0
	for _, r := range rows {
		total = r.TotalRow
		// The link lands in an href: only real http(s) file URLs get through.
		u := strings.ReplaceAll(strings.TrimSpace(r.Url), " ", "%20")
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			continue
		}
		d := doc{
			Exchange: strings.ToUpper(r.CatID),
			Company:  strings.TrimSpace(r.CompanyName),
			Title:    strings.TrimSpace(r.FullName),
			URL:      u,
			Ext:      strings.ToLower(strings.TrimPrefix(strings.TrimSpace(r.FileExt), ".")),
		}
		if reSym.MatchString(r.StockCode) {
			d.Sym = r.StockCode
		}
		if m := reDate.FindStringSubmatch(r.LastUpdate); m != nil {
			ms, _ := strconv.ParseInt(m[1], 10, 64)
			d.Time = time.UnixMilli(ms).In(vnTZ).Format("2006-01-02T15:04:05")
		}
		out = append(out, d)
	}
	return out, total, nil
}

func files(typ, ex, sym string, t term, page int) ([]doc, int, error) {
	key := fmt.Sprintf("docs:%s:%s:%s:%d:%d:%d", typ, ex, sym, t.Year, t.ID, page)
	b, err := httpx.Cached(key, 10*time.Minute, func() ([]byte, error) {
		raw, err := post("getrptfile", url.Values{
			"stockCode":      {sym},
			"documentTypeID": {types[typ]},
			"reportTermID":   {strconv.Itoa(t.ID)},
			"yearPeriod":     {strconv.Itoa(t.Year)},
			"exchangeID":     {exchanges[ex]},
			"orderBy":        {"2"}, // publish time
			"orderDir":       {"2"}, // newest first
			"page":           {strconv.Itoa(page)},
			"pageSize":       {strconv.Itoa(perPage)},
		})
		if err != nil {
			return nil, err
		}
		docs, total, err := parseRows(raw)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Total int   `json:"total"`
			Items []doc `json:"items"`
		}{total, docs})
	})
	if err != nil {
		return nil, 0, err
	}
	var v struct {
		Total int   `json:"total"`
		Items []doc `json:"items"`
	}
	return v.Items, v.Total, json.Unmarshal(b, &v)
}

// List serves one page of documents.
//
//	?type=BCTC&ex=HOSE&sym=HPG&year=2026&term=3&page=1
//
// Without year+term it picks the newest period worth showing: Vietstock opens
// on periods that have barely started (Quý 4/2026 with 4 files).
func List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	typ := httpx.Param(r, "type", "BCTC")
	ex := httpx.Param(r, "ex", "")
	sym := httpx.Param(r, "sym", "")
	page, _ := strconv.Atoi(q.Get("page"))
	if _, ok := types[typ]; !ok {
		httpx.Fail(w, 400, "unknown type: "+typ)
		return
	}
	if _, ok := exchanges[ex]; !ok {
		httpx.Fail(w, 400, "unknown exchange: "+ex)
		return
	}
	if sym != "" && !reSym.MatchString(sym) {
		httpx.Fail(w, 400, "bad ticker: "+sym)
		return
	}
	page = max(1, min(page, 1000))

	ts, err := terms(typ)
	if err != nil {
		httpx.Fail(w, 502, err.Error())
		return
	}
	if len(ts) == 0 {
		httpx.Fail(w, 502, "no reporting periods")
		return
	}

	var picked term
	year, _ := strconv.Atoi(q.Get("year"))
	id, _ := strconv.Atoi(q.Get("term"))
	for _, t := range ts {
		if t.Year == year && t.ID == id {
			picked = t
		}
	}

	var docs []doc
	var total int
	if picked.ID != 0 {
		docs, total, err = files(typ, ex, sym, picked, page)
	} else {
		// A ticker has one or two files per period, so any non-empty one will do;
		// the whole market needs a period that has had time to fill up.
		enough := perPage
		if sym != "" {
			enough = 1
		}
		for _, t := range ts {
			picked = t
			docs, total, err = files(typ, ex, sym, t, 1)
			if err != nil || total >= enough {
				break
			}
		}
		page = 1
	}
	if err != nil {
		httpx.Fail(w, 502, err.Error())
		return
	}
	out, _ := json.Marshal(map[string]any{
		"term": picked, "page": page, "per": perPage, "total": total, "items": docs, "terms": ts,
	})
	httpx.Send(w, 200, out)
}
