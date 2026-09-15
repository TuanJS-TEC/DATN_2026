// Package httpx is the plumbing every data source shares: the upstream client,
// the in-memory cache, and the JSON response shape the pages expect.
package httpx

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Second}

// Fetch GETs an upstream URL. SSI, entrade and CafeF all reject a default
// Go User-Agent, hence the browser one.
func Fetch(url string) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	r, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("upstream %d", r.StatusCode)
	}
	return io.ReadAll(r.Body)
}

// Scraping is slow and the sources change by the minute, not the second.
type entry struct {
	at   time.Time
	body []byte
}

var (
	mu    sync.Mutex
	cache = map[string]entry{}
)

// maxAge outlives every TTL in use (longest: symbols, 12h), so an entry is
// only dropped once nothing would serve it, not even as a stale fallback.
const maxAge = 24 * time.Hour

// Cached returns gen's result for key, regenerating after ttl. A failed
// regeneration serves the stale copy: stale beats a blank page.
func Cached(key string, ttl time.Duration, gen func() ([]byte, error)) ([]byte, error) {
	mu.Lock()
	e, hit := cache[key]
	mu.Unlock()
	if hit && time.Since(e.at) < ttl {
		return e.body, nil
	}
	b, err := gen()
	if err != nil {
		if hit {
			return e.body, nil
		}
		return nil, err
	}
	mu.Lock()
	now := time.Now()
	// ponytail: O(n) sweep per write, fine while keys are ~a day of articles;
	// move it to a time.Ticker if the key count reaches tens of thousands.
	for k, e := range cache {
		if now.Sub(e.at) > maxAge {
			delete(cache, k)
		}
	}
	cache[key] = entry{now, b}
	mu.Unlock()
	return b, nil
}

func Send(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(body)
}

func Fail(w http.ResponseWriter, status int, msg string) {
	b, _ := json.Marshal(map[string]string{"error": msg})
	Send(w, status, b)
}

// Param is the upper-cased query value, or def when absent.
func Param(r *http.Request, key, def string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return strings.ToUpper(v)
	}
	return def
}
