package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// evictInterval is how often idle buckets are swept.
	evictInterval = 3 * time.Minute
	// staleAfter is how long a client may be idle before its bucket is dropped.
	staleAfter = 10 * time.Minute
)

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// limiterStore holds one token bucket per client IP. It is the state behind
// RateLimiter, split out so the eviction rule and loop are testable on their
// own (ADR-032 keeps this package in the mutation-testing set).
type limiterStore struct {
	mu      sync.Mutex
	entries map[string]*limiterEntry
	rps     rate.Limit
	burst   int
}

func newLimiterStore(rps float64, burst int) *limiterStore {
	return &limiterStore{
		entries: make(map[string]*limiterEntry),
		rps:     rate.Limit(rps),
		burst:   burst,
	}
}

// get returns the bucket for ip, creating it on first sight and refreshing
// lastSeen on every call so active clients are never evicted.
func (s *limiterStore) get(ip string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.entries[ip]
	if !exists {
		entry = &limiterEntry{
			limiter:  rate.NewLimiter(s.rps, s.burst),
			lastSeen: time.Now(),
		}
		s.entries[ip] = entry
	} else {
		entry.lastSeen = time.Now()
	}
	return entry.limiter
}

// evict drops every bucket idle for longer than staleAfter as of now.
func (s *limiterStore) evict(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ip, entry := range s.entries {
		if now.Sub(entry.lastSeen) > staleAfter {
			delete(s.entries, ip)
		}
	}
}

// run sweeps idle buckets every interval until ctx is done. Tying the loop
// to a context (#117) lets an embedder — or a test — construct servers
// repeatedly without leaking a goroutine per limiter.
func (s *limiterStore) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.evict(now)
		}
	}
}

// RateLimiter returns middleware that limits requests per IP address using a
// token bucket algorithm. The background eviction of idle buckets stops when
// ctx is cancelled; server.New owns that context for the app's limiters. Per
// ADR-014 Security Patterns, tiered rates should be applied via route groups:
//
//	auth routes:   RateLimiter(ctx, 5, 5)    // 5 req/sec, burst 5
//	API routes:    RateLimiter(ctx, 100, 20) // 100 req/sec, burst 20
//	public routes: RateLimiter(ctx, 50, 10)  // 50 req/sec, burst 10
func RateLimiter(ctx context.Context, rps float64, burst int) func(http.Handler) http.Handler {
	store := newLimiterStore(rps, burst)
	go store.run(ctx, evictInterval)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr // RealIP middleware normalizes this upstream

			if !store.get(ip).Allow() {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
