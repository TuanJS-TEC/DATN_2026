// Tin Kinh Tế: the pages in web/ plus the JSON API they read.
//
// Run from the repo root: go run ./cmd/server
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tuanhm/internal/docs"
	"tuanhm/internal/httpx"
	"tuanhm/internal/market"
	"tuanhm/internal/news"
)

const web = "web"

// revalidate makes the browser check with us before reusing a cached page or
// script. Without it net/http sends no Cache-Control at all, browsers fall back
// to heuristic caching, and a deploy can leave a stale board.js calling API
// routes that no longer exist. "no-cache" still allows a 304 off the ETag —
// it forces the check, not a re-download.
func revalidate(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache")
}

func main() {
	// Wrong working dir would otherwise serve a site of 404s without a word.
	if _, err := os.Stat(web + "/index.html"); err != nil {
		log.Fatal("web/index.html not found: run from the repo root")
	}

	mux := http.NewServeMux()
	// No /api/board: board.js reads CafeF directly, see internal/market.
	mux.HandleFunc("/api/groups", market.Groups)
	mux.HandleFunc("/api/index", market.Index)
	mux.HandleFunc("/api/news", news.List)
	mux.HandleFunc("/api/article", news.Article)
	mux.HandleFunc("/api/docs", docs.List)
	mux.HandleFunc("/api/docs/terms", docs.Terms)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, 404, "no such route")
	})
	// ponytail: both FileServer and ServeFile 301 /index.html -> ./; ServeContent
	// doesn't, so the playwright tests (which goto /index.html) get a plain 200.
	mux.HandleFunc("/index.html", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(web + "/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		revalidate(w)
		http.ServeContent(w, r, "index.html", st.ModTime(), f)
	})
	// Only web/ is public; serving "." used to expose the Go source and binary.
	fs := http.FileServer(http.Dir(web))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		revalidate(w)
		fs.ServeHTTP(w, r)
	}))

	addr := "0.0.0.0:8765" // ponytail: ADDR override only so this can run beside server.py
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	} else if p := os.Getenv("PORT"); p != "" { // Render/Heroku set PORT, not ADDR
		addr = "0.0.0.0:" + p
	}
	// Header timeout stops slow clients pinning connections; no WriteTimeout,
	// since ?sym= news fans out to 7 categories and can legitimately take ~20s.
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	// Ctrl+C / SIGTERM (docker stop, systemd): stop accepting, let in-flight requests finish.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	drained := make(chan struct{})
	go func() {
		<-ctx.Done()
		log.Println("shutting down")
		// 25s covers the slowest request (?sym= fan-out, ~20s); after that, drop it.
		c, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := srv.Shutdown(c); err != nil {
			log.Println("shutdown:", err)
		}
		close(drained)
	}()

	log.Println("listening on " + addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-drained // ListenAndServe returns as soon as Shutdown starts, not when it ends
}
