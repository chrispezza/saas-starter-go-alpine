package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	tests := []struct {
		name         string
		rps          float64
		burst        int
		requests     int
		remoteAddrs  []string
		wantStatuses []int
	}{
		{
			name:  "same IP exceeding burst is limited",
			rps:   0.001, // effectively no refill during the test
			burst: 2,
			remoteAddrs: []string{
				"10.0.0.1:1234", "10.0.0.1:1234", "10.0.0.1:1234",
			},
			wantStatuses: []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests},
		},
		{
			name:  "distinct IPs get independent buckets",
			rps:   0.001,
			burst: 1,
			remoteAddrs: []string{
				"10.0.0.1:1234", "10.0.0.2:1234", "10.0.0.1:1234",
			},
			wantStatuses: []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := RateLimiter(t.Context(), tt.rps, tt.burst)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			for i, addr := range tt.remoteAddrs {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = addr
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				if rec.Code != tt.wantStatuses[i] {
					t.Errorf("request %d from %s: status = %d, want %d", i+1, addr, rec.Code, tt.wantStatuses[i])
				}
			}
		})
	}
}

// TestLimiterStore_Evict pins the staleness rule: an entry is dropped only
// once it has been idle for longer than staleAfter (#117; mutation set per
// ADR-032, so the boundary is exercised explicitly).
func TestLimiterStore_Evict(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		lastSeen map[string]time.Time
		wantKept []string
		wantGone []string
	}{
		{
			name:     "fresh entries are kept",
			lastSeen: map[string]time.Time{"10.0.0.1": now, "10.0.0.2": now.Add(-staleAfter / 2)},
			wantKept: []string{"10.0.0.1", "10.0.0.2"},
		},
		{
			name:     "entries idle longer than staleAfter are evicted",
			lastSeen: map[string]time.Time{"10.0.0.1": now.Add(-staleAfter - time.Second), "10.0.0.2": now},
			wantKept: []string{"10.0.0.2"},
			wantGone: []string{"10.0.0.1"},
		},
		{
			name:     "an entry idle exactly staleAfter is kept",
			lastSeen: map[string]time.Time{"10.0.0.1": now.Add(-staleAfter)},
			wantKept: []string{"10.0.0.1"},
		},
		{
			name:     "empty store is a no-op",
			lastSeen: map[string]time.Time{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newLimiterStore(1, 1)
			for ip, seen := range tt.lastSeen {
				s.get(ip)
				s.entries[ip].lastSeen = seen
			}

			s.evict(now)

			for _, ip := range tt.wantKept {
				if _, ok := s.entries[ip]; !ok {
					t.Errorf("evict() dropped %s, want kept", ip)
				}
			}
			for _, ip := range tt.wantGone {
				if _, ok := s.entries[ip]; ok {
					t.Errorf("evict() kept %s, want dropped", ip)
				}
			}
		})
	}
}

// TestLimiterStore_RunEvictsOnTick proves the loop actually calls evict on
// each tick, not just that it sleeps.
func TestLimiterStore_RunEvictsOnTick(t *testing.T) {
	s := newLimiterStore(1, 1)
	s.get("10.0.0.1")
	s.entries["10.0.0.1"].lastSeen = time.Now().Add(-staleAfter - time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.run(ctx, time.Millisecond)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, present := s.entries["10.0.0.1"]
		s.mu.Unlock()
		if !present {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run() never evicted the stale entry")
}

// TestLimiterStore_RunStopsOnContextCancel is the #117 fix: the eviction
// loop must return once its context is done instead of living until process
// exit.
func TestLimiterStore_RunStopsOnContextCancel(t *testing.T) {
	s := newLimiterStore(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.run(ctx, time.Hour)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return after context cancellation")
	}
}

// TestLimiterStore_GetRefreshesLastSeen guards the eviction input: a hit
// must move lastSeen forward so active clients are never evicted.
func TestLimiterStore_GetRefreshesLastSeen(t *testing.T) {
	s := newLimiterStore(1, 1)
	first := s.get("10.0.0.1")
	s.entries["10.0.0.1"].lastSeen = time.Now().Add(-staleAfter - time.Minute)

	second := s.get("10.0.0.1")

	if first != second {
		t.Fatal("get() returned a new limiter for a known IP, want the same bucket")
	}
	if age := time.Since(s.entries["10.0.0.1"].lastSeen); age > time.Second {
		t.Errorf("get() left lastSeen %v old, want refreshed", age)
	}
}

// TestRateLimiter_RetryAfter pins ADR-034 TC-5: every 429 tells the client
// when to come back. With one token per second and a burst of one, the
// second immediate request must wait ~1s — reported as a whole second,
// never zero.
func TestRateLimiter_RetryAfter(t *testing.T) {
	tests := []struct {
		name           string
		rps            float64
		burst          int
		wantRetryAfter string
	}{
		{name: "one token per second", rps: 1, burst: 1, wantRetryAfter: "1"},
		{name: "one token per two seconds", rps: 0.5, burst: 1, wantRetryAfter: "2"},
		{name: "glacial refill still reports whole seconds", rps: 0.001, burst: 1, wantRetryAfter: "1000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := RateLimiter(t.Context(), tt.rps, tt.burst)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			for i := 0; i < tt.burst; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = "10.0.0.9:1"
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("warm-up request %d: status = %d, want 200", i+1, rec.Code)
				}
				if rec.Header().Get("Retry-After") != "" {
					t.Errorf("allowed request must not carry Retry-After, got %q", rec.Header().Get("Retry-After"))
				}
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "10.0.0.9:1"
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("limited request: status = %d, want 429", rec.Code)
			}
			if got := rec.Header().Get("Retry-After"); got != tt.wantRetryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.wantRetryAfter)
			}
		})
	}
}

// TestRateLimiterWith_Refusal pins the seam the /patterns demo uses: the
// caller supplies the refusal response, the limiter supplies the verdict
// and Retry-After before handing over — so the demo's HTML 429 is the
// production limiter refusing, not a handler pretending.
func TestRateLimiterWith_Refusal(t *testing.T) {
	refused := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("<li>refused, retry after " + w.Header().Get("Retry-After") + "s</li>"))
	})
	h := RateLimiterWith(t.Context(), 0.5, 1, refused)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<li>allowed</li>"))
	}))

	send := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.7:1"
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := send(); rec.Code != http.StatusOK || rec.Body.String() != "<li>allowed</li>" {
		t.Fatalf("first request: status %d body %q, want 200 allowed", rec.Code, rec.Body.String())
	}
	rec := send()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429", rec.Code)
	}
	if got := rec.Body.String(); got != "<li>refused, retry after 2s</li>" {
		t.Errorf("refusal body = %q, want the custom fragment with Retry-After visible to it", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the custom handler's", ct)
	}
}
