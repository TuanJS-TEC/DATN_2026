// Package market proxies price data: SSI iBoard (board, index snapshot, ticker
// list) and entrade (intraday index line).
//
// The browser can't call either API directly: SSI only allows its own origin,
// and both reject a default Go/Python User-Agent.
package market

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"tuanhm/internal/httpx"
)

const (
	ssi     = "https://iboard-query.ssi.com.vn/"
	entrade = "https://services.entrade.com.vn/chart-api/v2/ohlcs/index"
)

// Whitelist: user input never reaches the upstream path directly.
var boards = map[string]string{
	"VN30":  "stock/group/VN30",
	"VN100": "stock/group/VN100",
	"HOSE":  "stock/exchange/hose",
	"HNX":   "stock/exchange/hnx",
	"HNX30": "stock/group/HNX30",
	"UPCOM": "stock/exchange/upcom",
}

// Snapshot lives on SSI, the intraday line on entrade — neither has both.
var indexes = map[string][2]string{
	"VNINDEX": {"vnindex", "VNINDEX"},
	"VN30":    {"vn30", "VN30"},
	"HNX":     {"hnxindex", "HNX"},
	"HNX30":   {"hnx30", "HNX30"},
	"UPCOM":   {"hnxupcomindex", "UPCOM"},
}

// Board serves one price board (?group=).
func Board(w http.ResponseWriter, r *http.Request) {
	group := httpx.Param(r, "group", "VN30")
	path, ok := boards[group]
	if !ok {
		httpx.Fail(w, 400, "unknown group: "+group)
		return
	}
	// board.js polls every 2-10s per tab; without this every tab was its own
	// upstream hit, which is what trips SSI's rate rules. Also gives the stale
	// fallback the news pages already have.
	b, err := httpx.Cached("board:"+group, 2*time.Second, func() ([]byte, error) {
		return httpx.Fetch(ssi + path)
	})
	if err != nil {
		httpx.Fail(w, 502, err.Error())
		return
	}
	httpx.Send(w, 200, b)
}

// Index serves one index's snapshot plus today's closes (?id=).
func Index(w http.ResponseWriter, r *http.Request) {
	key := httpx.Param(r, "id", "VNINDEX")
	ids, ok := indexes[key]
	if !ok {
		httpx.Fail(w, 400, "unknown index: "+key)
		return
	}

	// Five cards poll this every 5s per tab — see the note in Board.
	out, err := httpx.Cached("index:"+key, 5*time.Second, func() ([]byte, error) {
		raw, err := httpx.Fetch(ssi + "exchange-index/" + ids[0])
		if err != nil {
			return nil, err
		}
		var snapWrap struct {
			Data json.RawMessage `json:"data"`
		}
		json.Unmarshal(raw, &snapWrap)
		if len(snapWrap.Data) == 0 {
			snapWrap.Data = json.RawMessage("{}")
		}

		// 7 days back so a holiday or weekend still shows the last real session.
		to := time.Now().Unix()
		raw, err = httpx.Fetch(fmt.Sprintf("%s?from=%d&to=%d&symbol=%s&resolution=1", entrade, to-7*86400, to, ids[1]))
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
			Snap json.RawMessage `json:"snap"`
			C    []float64       `json:"c"`
		}{snapWrap.Data, today(ch.T, ch.C)})
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
		for _, ex := range []string{"hose", "hnx", "upcom"} {
			raw, err := httpx.Fetch(ssi + "stock/exchange/" + ex)
			if err != nil {
				return nil, err
			}
			var j struct {
				Data []struct {
					Sym string `json:"stockSymbol"`
				} `json:"data"`
			}
			if err := json.Unmarshal(raw, &j); err != nil {
				return nil, err
			}
			for _, d := range j.Data {
				all = append(all, d.Sym)
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
