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
