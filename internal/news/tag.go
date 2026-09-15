package news

import (
	"regexp"
	"slices"
	"strings"

	"tuanhm/internal/market"
)

// Every HOSE/HNX/UPCOM ticker is exactly 3 chars, digits allowed (C69, L14).
var reTok = regexp.MustCompile(`\b[A-Z0-9]{3}\b`)

// Real tickers that far more often mean something else in a headline.
// ponytail: static list; VND (VNDirect) is lost to "1.000 tỷ VND" — swap for
// context rules (e.g. "cổ phiếu VND") if that ticker's news matters.
var notSym = map[string]bool{"VND": true, "USD": true, "EUR": true, "GDP": true, "CPI": true,
	"ETF": true, "FED": true, "IMF": true, "VAT": true, "CEO": true, "HNX": true, "HSX": true, "EVN": true,
	"PPP": true, "BOT": true, "FDI": true, "ODA": true, "VIP": true, "ESG": true}

// symbols is the listed-ticker set minus the headline false positives.
func symbols() map[string]bool {
	set := map[string]bool{}
	all, _ := market.Symbols() // untagged news beats no news
	for _, t := range all {
		if !notSym[t] {
			set[t] = true
		}
	}
	return set
}

// tag lists the tickers from set that appear as whole uppercase tokens.
func tag(text string, set map[string]bool) []string {
	out := []string{}
	// The city, not Chứng khoán HSC (ticker HCM).
	text = strings.NewReplacer("TP.HCM", "", "TPHCM", "", "TP HCM", "").Replace(text)
	for _, t := range reTok.FindAllString(text, -1) {
		if set[t] && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}
