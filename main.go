package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultUsername = "bocan"

var ghClient *GitHubClient

func main() {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("GITHUB_TOKEN environment variable is required")
	}

	username := os.Getenv("GITHUB_USERNAME")
	if username == "" {
		username = defaultUsername
	}

	cacheMinutes := 60 * time.Minute

	ghClient = NewGitHubClient(token, username, cacheMinutes)

	rl := newRateLimiter(30, time.Minute) // 30 req/min per IP

	mux := http.NewServeMux()
	mux.Handle("/stats", rl.wrap(http.HandlerFunc(handleStats)))
	mux.Handle("/langs", rl.wrap(http.HandlerFunc(handleLangs)))
	mux.Handle("/cache/clear", http.HandlerFunc(handleCacheClear))
	mux.Handle("/health", http.HandlerFunc(handleHealth))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("github-status serving %s on :%s", username, port)
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// ---- handlers ----

func handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := ghClient.FetchStats()
	if err != nil {
		log.Printf("stats error: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	writeSVG(w, RenderStats(stats))
}

func handleLangs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	langs, err := ghClient.FetchLangs()
	if err != nil {
		log.Printf("langs error: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	writeSVG(w, RenderLangs(langs))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func handleCacheClear(w http.ResponseWriter, r *http.Request) {
	ghClient.mu.Lock()
	ghClient.cache = make(map[string]cacheEntry)
	ghClient.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("cache cleared\n"))
}

func writeSVG(w http.ResponseWriter, svg string) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Allow embedding from GitHub profile pages
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(svg))
}

// ---- rate limiter ----

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string][]time.Time
	max     int
	window  time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		entries: make(map[string][]time.Time),
		max:     max,
		window:  window,
	}
	// Periodically purge stale entries to prevent unbounded growth
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rl.purge()
		}
	}()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	var recent []time.Time
	for _, t := range rl.entries[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= rl.max {
		rl.entries[ip] = recent
		return false
	}

	rl.entries[ip] = append(recent, now)
	return true
}

func (rl *rateLimiter) purge() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-rl.window)
	for ip, times := range rl.entries {
		var recent []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(rl.entries, ip)
		} else {
			rl.entries[ip] = recent
		}
	}
}

func (rl *rateLimiter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := realIP(r)
		if !rl.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// realIP extracts the client IP, trusting X-Real-IP set by a reverse proxy.
// Only the immediate connection IP is used if no trusted header is present.
func realIP(r *http.Request) string {
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		// Validate it looks like an IP before trusting it
		if net.ParseIP(strings.TrimSpace(xri)) != nil {
			return strings.TrimSpace(xri)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
