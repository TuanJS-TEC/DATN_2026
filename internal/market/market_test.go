package market

import "testing"

func TestToday(t *testing.T) {
	const d = 86400
	// bars at VN-day 100 and 101 (UTC+7 shifted); only the newest day survives
	ts := []int64{100*d - 7*3600, 101*d - 7*3600, 101*d - 7*3600 + 60}
	got := today(ts, []float64{1, 2, 3})
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("got %v, want [2 3]", got)
	}
	if got := today(nil, nil); len(got) != 0 {
		t.Fatalf("empty: got %v", got)
	}
	// short c (truncated upstream response) must not panic
	if got := today(ts, []float64{1}); len(got) != 0 {
		t.Fatalf("short c: got %v", got)
	}
}

func TestNum(t *testing.T) {
	for in, want := range map[string]float64{
		"1,810.11": 1810.11, "592,811,094": 592811094, "-0.06": -0.06, "": 0,
	} {
		if got := num(in); got != want {
			t.Errorf("num(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTally(t *testing.T) {
	// ceiling/floor are counted on top of advances/declines, and an untraded
	// symbol (Match 0) is flat however its change field reads.
	rs := []row{
		{Sym: "ACB", Ceiling: 11, Floor: 9, Match: 11, Change: 1}, // up, at ceiling
		{Sym: "BID", Ceiling: 11, Floor: 9, Match: 9, Change: -1}, // down, at floor
		{Sym: "FPT", Ceiling: 11, Floor: 9, Match: 10, Change: 0}, // flat
		{Sym: "GAS", Ceiling: 11, Floor: 9, Match: 0, Change: -1}, // untraded
		{Sym: "ZZZ", Ceiling: 11, Floor: 9, Match: 10, Change: 1}, // not in VN30
	}
	want := counts{Advances: 1, Declines: 1, Nochanges: 2, Ceiling: 1, Floor: 1}
	if got := tally(rs, "VN30"); got != want {
		t.Errorf("basket: got %+v, want %+v", got, want)
	}
	// No basket: every row counts, so ZZZ joins the advances.
	if got := tally(rs, ""); got.Advances != 2 {
		t.Errorf("whole exchange: advances = %d, want 2", got.Advances)
	}
}
