package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/clownware/go-performance-starter/internal/view/partials"
)

// newPatternsRouter mounts the showcase routes exactly as production will, so
// the tests encode the route shape itself, not just handler behaviour.
func newPatternsRouter(t *testing.T) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	PatternsRoutes(t.Context(), r) // the demo limiter's eviction loop ends with the test (#117)
	return r
}

// patternSlugs is the catalogue retained from ux-overhaul-spec.md — every
// pattern the showcase must demonstrate, each anchored as a section on the
// page (id={slug}) so patterns are individually linkable.
var patternSlugs = []string{
	"partial-swap",
	"live-search",
	"click-to-edit",
	"inline-validation",
	"optimistic-ui",
	"infinite-scroll",
	"typeahead",
	"toasts",
	"dark-mode",
	"skeleton-loading",
	"tabs",
	"bulk-operations",
	// wave 2: the rest of the bells and whistles
	"polling",
	"oob-swap",
	"confirm-delete",
	"view-transitions",
	"loading-states",
	"modal",
	"global-store",
	// ADR-034: the production limiter refusing, on purpose
	"rate-limit",
}

func TestPatternsPage(t *testing.T) {
	// The showcase is public and DB-free (ADR-024): no auth context, no
	// repository — the page must still render every pattern section.
	req := httptest.NewRequest(http.MethodGet, "/patterns", nil)
	w := httptest.NewRecorder()

	newPatternsRouter(t).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /patterns status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<!doctype") && !strings.Contains(body, "<!DOCTYPE") {
		t.Error("GET /patterns should render a full page through the base layout")
	}
	for _, slug := range patternSlugs {
		if !strings.Contains(body, `id="`+slug+`"`) {
			t.Errorf("GET /patterns missing section anchor id=%q", slug)
		}
	}
	// Confirm-delete must intercept htmx:confirm with the styled dialog —
	// the native window.confirm popup renders at the top of the browser.
	if !strings.Contains(body, "@htmx:confirm.prevent") {
		t.Error("confirm-delete demo should intercept htmx:confirm with the styled Alpine dialog")
	}
	// Inline validation must wait for blur before flagging the field.
	if !strings.Contains(body, `@blur="touched = true"`) {
		t.Error("inline-validation demo should only show the error after the field is touched (blur)")
	}
}

func TestPatternsAPI(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		target        string
		wantStatus    int
		wantContains  []string
		wantEmptyBody bool // empty fragment expected (clears the swap target)
		wantHXTrigger bool // response must carry an HX-Trigger header
	}{
		{
			name:       "partial swap returns fragment",
			method:     http.MethodGet,
			target:     "/patterns/api/swap",
			wantStatus: http.StatusOK,
		},
		{
			name:       "live search returns results",
			method:     http.MethodGet,
			target:     "/patterns/api/search?q=a",
			wantStatus: http.StatusOK,
		},
		{
			name:         "live search reports a miss with the query echoed",
			method:       http.MethodGet,
			target:       "/patterns/api/search?q=zzzz",
			wantStatus:   http.StatusOK,
			wantContains: []string{"No matches"},
		},
		{
			name:          "live search with an empty query clears the results",
			method:        http.MethodGet,
			target:        "/patterns/api/search?q=",
			wantStatus:    http.StatusOK,
			wantEmptyBody: true,
		},
		{
			name:          "typeahead with a blank query clears the results",
			method:        http.MethodGet,
			target:        "/patterns/api/typeahead?q=%20",
			wantStatus:    http.StatusOK,
			wantEmptyBody: true,
		},
		{
			name:         "click-to-edit fetch returns a form",
			method:       http.MethodGet,
			target:       "/patterns/api/edit/1",
			wantStatus:   http.StatusOK,
			wantContains: []string{"<form"},
		},
		{
			name:       "click-to-edit save returns display view",
			method:     http.MethodPut,
			target:     "/patterns/api/edit/1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "optimistic favorite toggle",
			method:     http.MethodPost,
			target:     "/patterns/api/favorite/1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "infinite scroll page",
			method:     http.MethodGet,
			target:     "/patterns/api/scroll?page=2",
			wantStatus: http.StatusOK,
			// "revealed" never fires inside the demo's overflow container;
			// the sentinel must observe intersection instead.
			wantContains: []string{`hx-trigger="intersect once"`},
		},
		{
			name:       "typeahead results",
			method:     http.MethodGet,
			target:     "/patterns/api/typeahead?q=a",
			wantStatus: http.StatusOK,
		},
		{
			name:          "toast is triggered via response header",
			method:        http.MethodPost,
			target:        "/patterns/api/toast",
			wantStatus:    http.StatusOK,
			wantHXTrigger: true,
		},
		{
			name:       "toast rejects GET",
			method:     http.MethodGet,
			target:     "/patterns/api/toast",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "skeleton demo content",
			method:     http.MethodGet,
			target:     "/patterns/api/skeleton",
			wantStatus: http.StatusOK,
		},
		{
			name:       "server-loaded tab content",
			method:     http.MethodGet,
			target:     "/patterns/api/tab/templ",
			wantStatus: http.StatusOK,
		},
		{
			name:       "bulk operation result",
			method:     http.MethodPost,
			target:     "/patterns/api/bulk",
			wantStatus: http.StatusOK,
		},
		{
			name:       "polling tick",
			method:     http.MethodGet,
			target:     "/patterns/api/time",
			wantStatus: http.StatusOK,
		},
		{
			name:         "counter response carries an out-of-band swap",
			method:       http.MethodPost,
			target:       "/patterns/api/counter",
			wantStatus:   http.StatusOK,
			wantContains: []string{`hx-swap-oob`},
		},
		{
			name:       "confirmed delete returns the emptied state",
			method:     http.MethodPost,
			target:     "/patterns/api/confirm",
			wantStatus: http.StatusOK,
		},
		{
			name:       "view-transition card",
			method:     http.MethodGet,
			target:     "/patterns/api/transition?step=1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "loading-states content (delay capped server-side)",
			method:     http.MethodGet,
			target:     "/patterns/api/slow?delay=0",
			wantStatus: http.StatusOK,
		},
	}

	router := newPatternsRouter(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, nil)
			req.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("%s %s status = %d, want %d", tt.method, tt.target, w.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK {
				body := w.Body.String()
				if tt.wantEmptyBody {
					if len(strings.TrimSpace(body)) != 0 {
						t.Errorf("%s %s should render an empty fragment to clear its target, got %q", tt.method, tt.target, body)
					}
				} else if len(strings.TrimSpace(body)) == 0 {
					t.Errorf("%s %s rendered an empty fragment", tt.method, tt.target)
				}
				if strings.Contains(body, "<!doctype") || strings.Contains(body, "<!DOCTYPE") {
					t.Errorf("%s %s is a demo endpoint and must return a fragment, not a full page", tt.method, tt.target)
				}
				for _, want := range tt.wantContains {
					if !strings.Contains(body, want) {
						t.Errorf("%s %s body missing %q", tt.method, tt.target, want)
					}
				}
			}
			if tt.wantHXTrigger && w.Header().Get("HX-Trigger") == "" {
				t.Errorf("%s %s must set an HX-Trigger header to fire the toast", tt.method, tt.target)
			}
		})
	}
}

// TestPatternsCatalogCategorized pins the discovery model: every pattern
// belongs to one of the five teaching categories, and every category has at
// least one pattern — an uncategorized entry would silently fall out of the
// grouped page.
func TestPatternsCatalogCategorized(t *testing.T) {
	valid := make(map[string]bool)
	for _, c := range patternCategories {
		valid[c.Slug] = true
	}
	if len(valid) != 5 {
		t.Fatalf("expected 5 pattern categories, got %d", len(valid))
	}

	seen := make(map[string]int)
	for _, s := range patternsCatalog {
		if !valid[s.Category] {
			t.Errorf("pattern %q has unknown category %q", s.Slug, s.Category)
		}
		seen[s.Category]++
	}
	for slug := range valid {
		if seen[slug] == 0 {
			t.Errorf("category %q has no patterns", slug)
		}
	}
}

// TestPatternsPageDiscovery pins the navigation/discovery layer: a sidebar
// TOC for the scroll-spy to drive, and one grouped section per category.
func TestPatternsPageDiscovery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/patterns", nil)
	w := httptest.NewRecorder()

	newPatternsRouter(t).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /patterns status = %d, want 200", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, `data-testid="patterns-nav"`) {
		t.Error("patterns page missing the sidebar TOC nav")
	}
	if got := strings.Count(body, `data-testid="pattern-category"`); got != 5 {
		t.Errorf("patterns page renders %d category sections, want 5", got)
	}
	// Every pattern must keep its anchor so deep links and the TOC work.
	for _, slug := range patternSlugs {
		if !strings.Contains(body, `id="`+slug+`"`) {
			t.Errorf("pattern %q lost its section anchor in the grouped layout", slug)
		}
	}
}

// TestPatternRateLimit pins the rate-limit demo (ADR-034): the production
// RateLimiter guards the endpoint with the tier the page advertises, the
// refusal is an HTML fragment HTMX can swap, and it carries Retry-After.
func TestPatternRateLimit(t *testing.T) {
	router := newPatternsRouter(t)
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/patterns/api/limited", nil)
		req.RemoteAddr = "192.0.2.44:1"
		req.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for i := 1; i <= partials.PatternLimitBurst; i++ {
		w := send()
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (burst is %d)", i, w.Code, partials.PatternLimitBurst)
		}
		if !strings.Contains(w.Body.String(), `data-testid="limit-allowed"`) {
			t.Errorf("request %d: body missing the allowed fragment", i)
		}
	}
	w := send()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d: status = %d, want 429", partials.PatternLimitBurst+1, w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != strconv.Itoa(partials.PatternLimitPerSeconds) {
		t.Errorf("Retry-After = %q, want %q", got, strconv.Itoa(partials.PatternLimitPerSeconds))
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html so HTMX swaps the refusal", ct)
	}
	body := w.Body.String()
	for _, want := range []string{`data-testid="limit-refused"`, "429", "Retry-After"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal body missing %q", want)
		}
	}

	// A different client is a different bucket.
	req := httptest.NewRequest(http.MethodGet, "/patterns/api/limited", nil)
	req.RemoteAddr = "192.0.2.45:1"
	other := httptest.NewRecorder()
	router.ServeHTTP(other, req)
	if other.Code != http.StatusOK {
		t.Errorf("another client: status = %d, want 200 (per-IP buckets)", other.Code)
	}
}
