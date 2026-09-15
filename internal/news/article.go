package news

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"tuanhm/internal/httpx"
)

var (
	rePath  = regexp.MustCompile(`^/[a-z0-9-]+\.chn$`)
	reOn    = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*"[^"]*"`)
	reTitle = regexp.MustCompile(`(?s)data-role="title"[^>]*>\s*(.*?)\s*</h1>`)
	reAuth  = regexp.MustCompile(`(?s)data-role="author"[^>]*>\s*(?:<b>)?(.*?)(?:</b>)?\s*</span>`)
	reDate  = regexp.MustCompile(`data-role="publishdate"[^>]*datetime="([^"]+)"`)
	reSapo2 = regexp.MustCompile(`(?s)data-role="sapo"[^>]*>\s*(.*?)\s*</p>`)
)

// block returns the <div>…</div> whose opening tag contains marker, counting nesting.
func block(s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(s[:i], "<div")
	if start < 0 {
		return ""
	}
	depth, j := 0, start
	for {
		o := strings.Index(s[j:], "<div")
		c := strings.Index(s[j:], "</div")
		if c < 0 {
			return "" // unbalanced: safer to drop than to guess
		}
		if o >= 0 && o < c {
			depth++
			j += o + len("<div")
			continue
		}
		depth--
		j += c + len("</div>")
		if depth == 0 {
			return s[start:j]
		}
	}
}

// dropTag removes every <name …>…</name> pair.
func dropTag(s, name string) string {
	low := strings.ToLower(s)
	for {
		i := strings.Index(low, "<"+name)
		if i < 0 {
			return s
		}
		j := strings.Index(low[i:], "</"+name)
		if j < 0 {
			return s[:i]
		}
		k := strings.Index(low[i+j:], ">")
		if k < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+k+1:]
		low = strings.ToLower(s)
	}
}

// clean keeps the article body and strips everything executable: the HTML is
// third-party and ends up in the DOM via innerHTML, so this is a trust boundary.
func clean(page string) string {
	c := block(page, `data-role="content"`)
	if c == "" {
		return ""
	}
	// Hidden ticker widget and ad slots are dead weight without CafeF's own JS.
	for _, box := range []string{`class="chisochungkhoan"`, `id="admzone`, `class="inner-article-`, `class="tindnd`} {
		for b := block(c, box); b != ""; b = block(c, box) {
			c = strings.Replace(c, b, "", 1)
		}
	}
	return sanitize(c, cafef)
}

// sanitize strips everything executable and points relative links at origin.
func sanitize(c, origin string) string {
	for _, t := range []string{"script", "style", "iframe", "noscript", "ins", "select", "svg", "form", "object"} {
		c = dropTag(c, t)
	}
	c = reOn.ReplaceAllString(c, "")
	c = strings.ReplaceAll(c, `href="javascript:`, `href="#`)
	c = strings.ReplaceAll(c, `href="/`, `target="_blank" href="`+origin+`/`)
	return c
}

var (
	reVneTitle = regexp.MustCompile(`(?s)<h1 class="title-detail[^"]*">\s*(.*?)\s*</h1>`)
	reVneSapo  = regexp.MustCompile(`(?s)<p class="description">(.*?)</p>`)
	reVneAuth  = regexp.MustCompile(`<p class="Normal" style="text-align:right;"><strong>([^<]+)</strong></p>`)
	reVneDate  = regexp.MustCompile(`content="([^"]+)" itemprop="datePublished"`)
	reDataURI  = regexp.MustCompile(`\ssrc="data:[^"]*"`)
)

// cleanVne returns the VnExpress <article class="fck_detail"> body minus the
// title and summary (shown separately), with lazy images made eager.
func cleanVne(page string) string {
	i := strings.Index(page, `<article class="fck_detail`)
	if i < 0 {
		return ""
	}
	j := strings.Index(page[i:], "</article>")
	if j < 0 {
		return ""
	}
	c := page[i:][:j]
	c = c[strings.Index(c, ">")+1:]
	c = reVneTitle.ReplaceAllString(c, "")
	c = reVneSapo.ReplaceAllString(c, "")
	c = reVneAuth.ReplaceAllString(c, "")
	// Placeholder gif in src, real image in data-src/data-srcset for their lazy loader.
	c = reDataURI.ReplaceAllString(c, "")
	c = strings.ReplaceAll(c, " data-src", " src")
	return sanitize(c, vne)
}

// Article serves one sanitized article body (?p= a CafeF path or VnExpress URL).
func Article(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	var src string
	var parse func(string) map[string]string
	switch {
	case rePath.MatchString(p):
		src = cafef + p
		parse = func(s string) map[string]string {
			return map[string]string{"title": grab(reTitle, s), "author": grab(reAuth, s),
				"date": grab(reDate, s), "sapo": grab(reSapo2, s), "content": clean(s)}
		}
	case reVnePath.MatchString(p):
		src = p
		parse = func(s string) map[string]string {
			return map[string]string{"title": grab(reVneTitle, s), "author": grab(reVneAuth, s),
				"date":    grab(reVneDate, s),
				"sapo":    strings.TrimSpace(reTag.ReplaceAllString(grab(reVneSapo, s), "")),
				"content": cleanVne(s)}
		}
	default:
		httpx.Fail(w, 400, "bad article path")
		return
	}
	b, err := httpx.Cached("art:"+p, time.Hour, func() ([]byte, error) {
		page, err := httpx.Fetch(src)
		if err != nil {
			return nil, err
		}
		a := parse(string(page))
		if a["content"] == "" {
			return nil, fmt.Errorf("article body not found (layout changed?)")
		}
		a["source"] = src
		return json.Marshal(a)
	})
	if err != nil {
		httpx.Fail(w, 502, err.Error())
		return
	}
	httpx.Send(w, 200, b)
}
