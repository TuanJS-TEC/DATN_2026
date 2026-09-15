// Package news serves CafeF (scraped) + VnExpress (RSS): category listings,
// ticker tags and sanitized article bodies, as JSON.
//
// CafeF has no public API and sends X-Frame-Options: SAMEORIGIN, so the page
// can neither fetch nor iframe it — everything goes through here.
package news

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"tuanhm/internal/httpx"
)

const (
	cafef = "https://cafef.vn"
	vne   = "https://vnexpress.net"
)

// VnExpress only publishes top-level RSS feeds (kinh-doanh/chung-khoan.rss 302s),
// so only the categories with a feed that actually fits get one.
var feeds = map[string]string{
	"DOANH-NGHIEP": "kinh-doanh",
	"BAT-DONG-SAN": "bat-dong-san",
}

var vnTZ = time.FixedZone("ICT", 7*3600)

// Whitelist: user input never reaches the upstream path directly.
var cats = map[string]string{
	"CHUNG-KHOAN":  "thi-truong-chung-khoan",
	"BAT-DONG-SAN": "bat-dong-san",
	"DOANH-NGHIEP": "doanh-nghiep",
	"NGAN-HANG":    "tai-chinh-ngan-hang",
	"VI-MO":        "vi-mo-dau-tu",
	"HANG-HOA":     "hang-hoa-nguyen-lieu",
	"QUOC-TE":      "tai-chinh-quoc-te",
}

var (
	reLink = regexp.MustCompile(`href="(/[^"]+\.chn)"[^>]*>\s*([^<]+?)\s*</a>`)
	reImg  = regexp.MustCompile(`src="(https://cafefcdn\.com/[^"]+)"`)
	reTime = regexp.MustCompile(`title="(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})"`)
	// Featured cards carry dd/MM/yyyy instead, sometimes with a junk leading digit
	// that CafeF's own JS strips: data-time="214/09/2026 - 21:56".
	reDMY   = regexp.MustCompile(`data-time="\d?(\d{2})/(\d{2})/(\d{4}) - (\d{2}):(\d{2})`)
	reDigit = regexp.MustCompile(`^\d{15,}$`)
	reSapo  = regexp.MustCompile(`class="sapo[^"]*"[^>]*>\s*([^<]+?)\s*<`)
)

type article struct {
	URL   string   `json:"url"`
	Title string   `json:"title"`
	Img   string   `json:"img"`
	Time  string   `json:"time"`
	Sapo  string   `json:"sapo"`
	Cat   string   `json:"cat"`
	Syms  []string `json:"syms"` // tickers mentioned in title/sapo
}

func grab(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

// when returns the publish time as ISO, from whichever of the three shapes
// CafeF used on this card: an ISO title, a dd/MM/yyyy attribute, or — when the
// card leaves the time to JS — the yyMMddHHmmss baked into the article id.
func when(chunk, url string) string {
	if m := reTime.FindStringSubmatch(chunk); m != nil {
		return m[1]
	}
	if m := reDMY.FindStringSubmatch(chunk); m != nil {
		return m[3] + "-" + m[2] + "-" + m[1] + "T" + m[4] + ":" + m[5] + ":00"
	}
	id := strings.TrimSuffix(url[strings.LastIndex(url, "-")+1:], ".chn")
	if reDigit.MatchString(id) {
		return "20" + id[3:5] + "-" + id[5:7] + "-" + id[7:9] + "T" + id[9:11] + ":" + id[11:13] + ":" + id[13:15]
	}
	return ""
}

// parseList reads the <div role="article"> cards of a category page.
func parseList(page string) []article {
	out := []article{}
	seen := map[string]bool{}
	for _, chunk := range strings.Split(page, `role="article"`)[1:] {
		m := reLink.FindStringSubmatch(chunk)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, article{
			URL:   m[1],
			Title: html.UnescapeString(m[2]),
			Img:   grab(reImg, chunk),
			Time:  when(chunk, m[1]),
			Sapo:  grab(reSapo, chunk),
		})
	}
	return out
}

var (
	reVnePath = regexp.MustCompile(`^https://vnexpress\.net/[a-z0-9-]+-\d+\.html$`)
	reTag     = regexp.MustCompile(`<[^>]*>`)
)

// parseRSS reads a VnExpress feed. Times come out in CafeF's zone-less VN-local
// shape so both sources sort together as plain strings.
func parseRSS(raw []byte) []article {
	var feed struct {
		Items []struct {
			Title string `xml:"title"`
			Link  string `xml:"link"`
			Desc  string `xml:"description"`
			Date  string `xml:"pubDate"`
			Enc   struct {
				URL string `xml:"url,attr"`
			} `xml:"enclosure"`
		} `xml:"channel>item"`
	}
	out := []article{}
	if xml.Unmarshal(raw, &feed) != nil {
		return out
	}
	for _, it := range feed.Items {
		if !reVnePath.MatchString(it.Link) {
			continue // video/podcast pages: Article couldn't open them
		}
		a := article{
			URL:   it.Link,
			Title: html.UnescapeString(strings.TrimSpace(it.Title)),
			Img:   it.Enc.URL,
			// description is "<a><img></a></br>summary"
			Sapo: html.UnescapeString(strings.TrimSpace(reTag.ReplaceAllString(it.Desc, ""))),
		}
		if t, err := time.Parse(time.RFC1123Z, it.Date); err == nil {
			a.Time = t.In(vnTZ).Format("2006-01-02T15:04:05")
		}
		out = append(out, a)
	}
	return out
}

// list returns one category's tagged articles from every source, newest first, cached.
func list(cat string) ([]article, error) {
	// ponytail: 1 min = freshness vs upstream load; pages poll at the same rate (web/news.js watchNew).
	// A slow first load is fine here; lower it if a minute of lag is too much.
	b, err := httpx.Cached("list:"+cat, time.Minute, func() ([]byte, error) {
		// One dead source shouldn't hide the other.
		var arts []article
		var errs []error
		if page, err := httpx.Fetch(cafef + "/" + cats[cat] + ".chn"); err == nil {
			arts = parseList(string(page))
		} else {
			errs = append(errs, err)
		}
		if feed, ok := feeds[cat]; ok {
			if raw, err := httpx.Fetch(vne + "/rss/" + feed + ".rss"); err == nil {
				arts = append(arts, parseRSS(raw)...)
			} else {
				errs = append(errs, err)
			}
		}
		if len(arts) == 0 {
			return nil, fmt.Errorf("no articles found (layout changed?) %w", errors.Join(errs...))
		}
		slices.SortStableFunc(arts, func(a, b article) int { return strings.Compare(b.Time, a.Time) })
		set := symbols()
		for i := range arts {
			arts[i].Cat = cat
			arts[i].Syms = tag(arts[i].Title+" "+arts[i].Sapo, set)
		}
		return json.Marshal(arts)
	})
	if err != nil {
		return nil, err
	}
	var arts []article
	return arts, json.Unmarshal(b, &arts)
}

// List serves one category (?cat=) or every category's articles about one ticker (?sym=).
func List(w http.ResponseWriter, r *http.Request) {
	if sym := httpx.Param(r, "sym", ""); sym != "" {
		symbols() // warm once, or each goroutine below would fetch it again
		var (
			wg    sync.WaitGroup
			lock  sync.Mutex
			found = []article{}
		)
		for cat := range cats {
			wg.Go(func() {
				arts, _ := list(cat) // one dead category shouldn't hide the rest
				lock.Lock()
				defer lock.Unlock()
				for _, a := range arts {
					if slices.Contains(a.Syms, sym) && !slices.ContainsFunc(found, func(f article) bool { return f.URL == a.URL }) {
						found = append(found, a)
					}
				}
			})
		}
		wg.Wait()
		slices.SortFunc(found, func(a, b article) int { return strings.Compare(b.Time, a.Time) })
		out, _ := json.Marshal(found)
		httpx.Send(w, 200, out)
		return
	}

	cat := httpx.Param(r, "cat", "CHUNG-KHOAN")
	if _, ok := cats[cat]; !ok {
		httpx.Fail(w, 400, "unknown category: "+cat)
		return
	}
	arts, err := list(cat)
	if err != nil {
		httpx.Fail(w, 502, err.Error())
		return
	}
	out, _ := json.Marshal(arts)
	httpx.Send(w, 200, out)
}
