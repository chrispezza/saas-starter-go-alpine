package middleware

import (
	"context"
	"math"
	"net/http"
	"strconv"
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
//
// A limited request is answered 429 with a Retry-After header (whole
// seconds, at least 1) and a plain-text body.
func RateLimiter(ctx context.Context, rps float64, burst int) func(http.Handler) http.Handler {
	return RateLimiterWith(ctx, rps, burst, nil)
}

// RateLimiterWith is RateLimiter with a caller-supplied refusal response.
// The limiter decides and sets Retry-After, then hands the request to
// refused (nil means the plain-text default) — so a surface that wants an
// HTML 429 (the /patterns demo, ADR-034) still gets the production verdict.
func RateLimiterWith(ctx context.Context, rps float64, burst int, refused http.Handler) func(http.Handler) http.Handler {
	store := newLimiterStore(rps, burst)
	go store.run(ctx, evictInterval)
	if refused == nil {
		refused = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		})
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr // RealIP middleware normalizes this upstream

			if wait, ok := take(store.get(ip)); !ok {
				w.Header().Set("Retry-After", retryAfterSeconds(wait))
				refused.ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// take consumes one token if one is available now. Otherwise it reports how
// long the bucket needs before one would be, leaving the bucket untouched
// (the reservation is cancelled) so a refused request costs nothing.
func take(lim *rate.Limiter) (wait time.Duration, ok bool) {
	now := time.Now()
	res := lim.ReserveN(now, 1)
	if !res.OK() {
		return time.Second, false // burst of zero: never allowed
	}
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now)
		return delay, false
	}
	return 0, true
}

// retryAfterSeconds renders a wait as the whole-second Retry-After value
// RFC 9110 specifies, rounding up so a client never retries early, and
// never saying 0.
func retryAfterSeconds(wait time.Duration) string {
	secs := int64(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return strconv.FormatInt(secs, 10)
}
