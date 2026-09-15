package httpx

import (
	"errors"
	"testing"
	"time"
)

func TestCached(t *testing.T) {
	calls := 0
	gen := func() ([]byte, error) { calls++; return []byte("v"), nil }
	down := func() ([]byte, error) { return nil, errors.New("upstream down") }

	Cached("k", time.Hour, gen)
	if b, _ := Cached("k", time.Hour, gen); string(b) != "v" || calls != 1 {
		t.Fatalf("fresh hit must not regenerate: calls=%d", calls)
	}
	if b, err := Cached("k", 0, down); err != nil || string(b) != "v" {
		t.Fatalf("stale copy must survive a failed refresh: %q %v", b, err)
	}
	if _, err := Cached("missing", 0, down); err == nil {
		t.Fatal("no copy + failed gen must error")
	}

	mu.Lock()
	cache["old"] = entry{time.Now().Add(-maxAge - time.Minute), []byte("x")}
	mu.Unlock()
	Cached("new", time.Hour, gen)
	mu.Lock()
	_, old := cache["old"]
	_, kept := cache["k"]
	mu.Unlock()
	if old || !kept {
		t.Fatalf("write must evict only entries past maxAge: old=%v k=%v", old, kept)
	}
}
