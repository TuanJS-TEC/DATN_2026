// Package market serves the index cards and the ticker list.
//
// The price board itself is no longer here: web/board.js reads CafeF straight
// from the browser. What needs a server is the intraday index line, because
// entrade sends no Access-Control-Allow-Origin at all.
//
// SSI used to serve all of this. Its API sits behind Cloudflare, which
// challenges datacenter IPs — from a Vietnamese home connection every call
// returns 200, but from a host outside Vietnam it returns a 403 HTML challenge
// page that no Go client can solve. CafeF runs on plain IIS and answers
// Access-Control-Allow-Origin: *, so it works from any host and from the
// browser directly.
package market

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tuanhm/internal/httpx"
)

const (
	cafef   = "https://banggia.cafef.vn/stockhandler.ashx?center="
	entrade = "https://services.entrade.com.vn/chart-api/v2/ohlcs/index"
)

// CafeF publishes one feed per exchange; every basket is a slice of one of them.
var centers = map[string]string{"HOSE": "1", "HNX": "2", "UPCOM": "9"}

// Baskets CafeF has no feed of its own for, so they're filtered by membership.
//
// ponytail: two known ceilings, both cosmetic. The committee rebalances these
// twice a year — refresh by hand from
// https://iboard-query.ssi.com.vn/stock/group/VN30 on a machine with a
// Vietnamese IP. And a member CafeF files under another exchange (today: MCH
// under UPCOM, TAL likewise) or hasn't listed yet (TCX) simply won't appear,
// because each basket is filtered out of one feed and CafeF's handler ignores
// every parameter but center, so there's no way to top up single symbols
// without pulling a second 200KB feed on every poll.
var baskets = map[string][]string{
	"VN30": {"ACB", "BID", "BSR", "CTG", "FPT", "GAS", "GVR", "HDB", "HPG", "LPB",
		"MBB", "MCH", "MSN", "MWG", "SAB", "SHB", "SSB", "SSI", "STB", "TCB",
		"TCX", "VCB", "VHM", "VIB", "VIC", "VJC", "VNM", "VPB", "VPL", "VRE"},
	"VN100": {"ACB", "ANV", "BAF", "BCM", "BID", "BMP", "BSI", "BSR", "BVH", "BWE",
		"CII", "CMG", "CTD", "CTG", "CTR", "CTS", "DBC", "DCM", "DGW", "DIG",
		"DPM", "DSE", "DXG", "EIB", "EVF", "FPT", "FRT", "FTS", "GAS", "GEE",
		"GEX", "GMD", "GVR", "HAG", "HCM", "HDB", "HDG", "HHV", "HPG", "HSG",
		"HT1", "KBC", "KDC", "KDH", "KOS", "LPB", "MBB", "MCH", "MSB", "MSN",
		"MWG", "NAB", "NKG", "NLG", "NT2", "NVL", "OCB", "PAN", "PC1", "PDR",
		"PHR", "PLX", "PNJ", "POW", "PVD", "PVT", "REE", "SAB", "SBT", "SHB",
		"SIP", "SJS", "SSB", "SSI", "STB", "TAL", "TCB", "TCH", "TCX", "TPB",
		"VCB", "VCG", "VCI", "VCK", "VGC", "VHC", "VHM", "VIB", "VIC", "VIX",
		"VJC", "VND", "VNM", "VPB", "VPI", "VPL", "VPX", "VRE", "VSC", "VTP"},
	"HNX30": {"BVS", "CAP", "CEO", "DP3", "DTD", "DVM", "DXP", "HUT", "IDC", "IDV",
		"L14", "L18", "LAS", "LHC", "MBS", "NDN", "NTP", "PLC", "PSD", "PVB",
		"PVC", "PVS", "SHS", "SLS", "TMB", "TNG", "TVD", "VC3", "VCS", "VFS"},
}

// The five cards the board page shows, each stitched from two upstreams.
var indexes = map[string]struct {
	cafef   string // name in CafeF's ?index=true feed
	entrade string // symbol in entrade's chart API
	center  string // exchange feed the breadth counts are tallied from
	basket  string // non-empty: count only this basket's symbols
}{
	"VNINDEX": {"VNINDEX", "VNINDEX", "1", ""},
	"VN30":    {"VN30", "VN30", "1", "VN30"},
	"HNX":     {"HNXINDEX", "HNX", "2", ""},
	"HNX30":   {"HNX30", "HNX30", "2", "HNX30"},
	"UPCOM":   {"HNXUPCOMINDEX", "UPCOM", "9", ""},
}

// Groups tells board.js which boards exist, which CafeF feed each one reads and
// which symbols a basket holds. Deliberately no upstream call: the browser
// fetches the prices itself, so this must not be the thing that can fail.
func Groups(w http.ResponseWriter, r *http.Request) {
	type group struct {
		Center string   `json:"center"`
		Syms   []string `json:"syms,omitempty"`
	}
	out := map[string]group{}
	for name, c := range centers {
		out[name] = group{Center: c}
	}
	for name, syms := range baskets {
		c := "1" // every basket but HNX30 is cut out of HOSE
		if strings.HasPrefix(name, "HNX") {
			c = "2"
		}
		out[name] = group{Center: c, Syms: syms}
	}
	b, _ := json.Marshal(out)
	httpx.Send(w, 200, b)
}

// row is the slice of CafeF's board record the breadth counts need. Its keys
// are single letters: a symbol, b reference, c ceiling, d floor, k change,
// l matched price.
type row struct {
	Sym     string  `json:"a"`
	Ceiling float64 `json:"c"`
	Floor   float64 `json:"d"`
	Change  float64 `json:"k"`
	Match   float64 `json:"l"`
}

// feed returns one CafeF exchange feed. Cached because all five cards poll at
// once and three of them read the same two feeds.
func feed(center string) ([]row, error) {
	b, err := httpx.Cached("cafef:"+center, 3*time.Second, func() ([]byte, error) {
		return httpx.Fetch(cafef + center)
	})
	if err != nil {
		return nil, err
	}
	var rs []row
	return rs, json.Unmarshal(b, &rs)
}

// counts is the advancing/declining tally under each index card. Ceiling and
// floor are counted on top of Advances/Declines, not instead of them — same as
// the totals row in board.js.
type counts struct {
	Advances  int `json:"advances"`
	Nochanges int `json:"nochanges"`
	Declines  int `json:"declines"`
	Ceiling   int `json:"ceiling"`
	Floor     int `json:"floor"`
}

// breadth tallies one exchange, or one basket within it. CafeF's index feed
// carries no such counts, so they come from the board feed instead. A failed
// fetch yields zeroes rather than failing the card: the index value itself is
// what the card is for.
func breadth(center, basket string) counts {
	rs, err := feed(center)
	if err != nil {
		return counts{}
	}
	return tally(rs, basket)
}

// tally is breadth's arithmetic, split out so it can be tested without a fetch.
func tally(rs []row, basket string) counts {
	var in map[string]bool
	if syms, ok := baskets[basket]; ok {
		in = make(map[string]bool, len(syms))
		for _, s := range syms {
			in[s] = true
		}
	}
	var c counts
	for _, r := range rs {
		if in != nil && !in[r.Sym] {
			continue
		}
		if r.Match == 0 { // untraded so far today
			c.Nochanges++
			continue
		}
		if r.Match >= r.Ceiling {
			c.Ceiling++
		}
		if r.Match <= r.Floor {
			c.Floor++
		}
		switch {
		case r.Change > 0:
			c.Advances++
		case r.Change < 0:
			c.Declines++
		default:
			c.Nochanges++
		}
	}
	return c
}

// num parses CafeF's display numbers: "1,810.11", "592,811,094".
func num(s string) float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return f
}

// snap is the index card's header. Field names are the ones board.js already
// reads, so the template didn't have to change.
type snap struct {
	Label          string  `json:"label"`
	IndexValue     float64 `json:"indexValue"`
	PrevIndexValue float64 `json:"prevIndexValue"`
	Change         float64 `json:"change"`
	ChangePercent  float64 `json:"changePercent"`
	AllQty         float64 `json:"allQty"`
	AllValue       float64 `json:"allValue"`
	counts
}

// Index serves one index's snapshot plus today's closes (?id=).
func Index(w http.ResponseWriter, r *http.Request) {
	key := httpx.Param(r, "id", "VNINDEX")
	ids, ok := indexes[key]
	if !ok {
		httpx.Fail(w, 400, "unknown index: "+key)
		return
	}

	// Five cards poll this every 5s per tab, so dedupe across tabs.
	out, err := httpx.Cached("index:"+key, 5*time.Second, func() ([]byte, error) {
		raw, err := httpx.Cached("cafef:index", 3*time.Second, func() ([]byte, error) {
			return httpx.Fetch("https://banggia.cafef.vn/stockhandler.ashx?index=true")
		})
		if err != nil {
			return nil, err
		}
		var all []struct {
			Name    string `json:"name"`
			Index   string `json:"index"`
			Change  string `json:"change"`
			Percent string `json:"percent"`
			Volume  string `json:"volume"`
			Value   string `json:"value"`
		}
		if err := json.Unmarshal(raw, &all); err != nil {
			return nil, err
		}
		s := snap{Label: key, counts: breadth(ids.center, ids.basket)}
		for _, x := range all {
			if x.Name != ids.cafef {
				continue
			}
			s.IndexValue = num(x.Index)
			s.Change = num(x.Change)
			s.ChangePercent = num(x.Percent)
			s.PrevIndexValue = s.IndexValue - s.Change
			s.AllQty = num(x.Volume)
			s.AllValue = num(x.Value) * 1e9 // CafeF reports turnover in tỷ đồng
			break
		}
		if s.IndexValue == 0 {
			return nil, fmt.Errorf("index %s missing from CafeF feed", ids.cafef)
		}

		// 7 days back so a holiday or weekend still shows the last real session.
		to := time.Now().Unix()
		raw, err = httpx.Fetch(fmt.Sprintf("%s?from=%d&to=%d&symbol=%s&resolution=1", entrade, to-7*86400, to, ids.entrade))
		if err != nil {
			return nil, err
		}
		var ch struct {
			T []int64   `json:"t"`
			C []float64 `json:"c"`
		}
		if err := json.Unmarshal(raw, &ch); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Snap snap      `json:"snap"`
			C    []float64 `json:"c"`
		}{s, today(ch.T, ch.C)})
	})
	if err != nil {
		httpx.Fail(w, 502, err.Error())
		return
	}
	httpx.Send(w, 200, out)
}

// today keeps only the bars sharing the VN calendar day (UTC+7) of the newest bar.
func today(ts []int64, cs []float64) []float64 {
	out := []float64{}
	if len(ts) == 0 {
		return out
	}
	day := (ts[len(ts)-1] + 7*3600) / 86400
	for i, t := range ts {
		if i < len(cs) && (t+7*3600)/86400 == day {
			out = append(out, cs[i])
		}
	}
	return out
}

// Symbols lists every ticker on HOSE, HNX and UPCOM, cached 12h.
func Symbols() ([]string, error) {
	b, err := httpx.Cached("symbols", 12*time.Hour, func() ([]byte, error) {
		var all []string
		for _, c := range []string{"1", "2", "9"} {
			rs, err := feed(c)
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				all = append(all, r.Sym)
			}
		}
		return json.Marshal(all)
	})
	if err != nil {
		return nil, err
	}
	var all []string
	return all, json.Unmarshal(b, &all)
}
