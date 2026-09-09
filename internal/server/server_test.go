package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/clownware/go-performance-starter/internal/config"
	mw "github.com/clownware/go-performance-starter/internal/middleware"
)

// testConfig returns the baseline auth-disabled config for the given env.
// Tests that need other knobs (trusted proxies, metrics token, Supabase)
// mutate the returned value before calling newServer.
func testConfig(env string) *config.Config {
	return &config.Config{
		Env:                 env,
		HTTPPort:            "4000",
		DatabaseURL:         "postgres://localhost:5432/test",
		DBMaxConns:          25,
		MaxRequestBodyBytes: 1 << 20,
	}
}

func newServer(t *testing.T, cfg *config.Config, db *pgxpool.Pool) *Server {
	t.Helper()
	srv, err := New(cfg, db)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func newTestServer(t *testing.T, env string) *Server {
	t.Helper()
	return newServer(t, testConfig(env), nil)
}

// TestServer_CSRFWiring proves the CSRF middleware is wired into the router,
// not just unit-correct: unsafe requests without a token are rejected and
// pages issue the cookie.
func TestServer_CSRFWiring(t *testing.T) {
	srv := newTestServer(t, "development")

	// GET issues the CSRF cookie.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	var token string
	for _, c := range rec.Result().Cookies() {
		if c.Name == mw.CSRFCookieName {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("GET / did not set the CSRF cookie")
	}

	// POST without a token is rejected.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/patterns/api/toast", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST without CSRF token: status = %d, want 403", rec.Code)
	}

	// POST with cookie + matching header passes CSRF.
	req := httptest.NewRequest(http.MethodPost, "/patterns/api/toast", nil)
	req.AddCookie(&http.Cookie{Name: mw.CSRFCookieName, Value: token})
	req.Header.Set(mw.CSRFHeaderName, token)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Errorf("POST with valid CSRF token: status = %d, want non-403", rec.Code)
	}
}

// TestServer_StubDemosRetired proves the pre-ADR-024 stub surfaces are gone
// and the replacement demo is routed: the in-memory Items demo and the
// hardcoded JSON API served fake data ("Stub User", org1) that under-sold the
// stack, and ADR-024 Slice C retires them.
func TestServer_StubDemosRetired(t *testing.T) {
	srv := newTestServer(t, "development")

	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{"items page retired", http.MethodGet, "/items", http.StatusNotFound},
		{"items list retired", http.MethodGet, "/items/list", http.StatusNotFound},
		{"stub user API retired", http.MethodGet, "/api/users/123", http.StatusNotFound},
		{"stub organizations API retired", http.MethodGet, "/api/organizations", http.StatusNotFound},
		{"API placeholder retired", http.MethodGet, "/api", http.StatusNotFound},
		{"patterns showcase replaces them", http.MethodGet, "/patterns", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))
			if rec.Code != tt.wantStatus {
				t.Errorf("%s %s status = %d, want %d", tt.method, tt.target, rec.Code, tt.wantStatus)
			}
		})
	}
}

// TestServer_DashboardWithoutAuth proves the dashboard stays routed when the
// /learn identity chain is not mounted (no auth client, no DB): the nav link
// lands on the teaser shell, never a 404, while the widget fragments — which
// need repositories — are absent (#69).
func TestServer_DashboardWithoutAuth(t *testing.T) {
	srv := newTestServer(t, "development")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `data-testid="dashboard-teaser"`) {
		t.Error("GET /dashboard without auth should render the teaser shell")
	}

	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/widgets/quiz", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /dashboard/widgets/quiz without auth status = %d, want 404 (no repositories to serve it)", rec.Code)
	}
}

// TestServer_MetricsGating proves /metrics visibility follows environment and
// METRICS_TOKEN: open in development, hidden (404) in production without a
// token, and bearer-gated (401 on mismatch) in either env once a token is set.
func TestServer_MetricsGating(t *testing.T) {
	tests := []struct {
		name          string
		env           string
		token         string
		authorization string
		wantStatus    int
	}{
		{"open in development", "development", "", "", http.StatusOK},
		{"hidden in production without token", "production", "", "", http.StatusNotFound},
		{"token accepted in development", "development", "metrics-test-token", "Bearer metrics-test-token", http.StatusOK},
		{"token accepted in production", "production", "metrics-test-token", "Bearer metrics-test-token", http.StatusOK},
		{"missing bearer rejected when token set", "development", "metrics-test-token", "", http.StatusUnauthorized},
		{"wrong bearer rejected in production", "production", "metrics-test-token", "Bearer nope", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(tt.env)
			cfg.MetricsToken = tt.token
			srv := newServer(t, cfg, nil)
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("GET /metrics status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

// TestServer_HSTSGating proves the HSTS header follows environment (ADR-025 §2).
func TestServer_HSTSGating(t *testing.T) {
	tests := []struct {
		env      string
		wantHSTS bool
	}{
		{"development", false},
		{"production", true},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			srv := newTestServer(t, tt.env)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			got := rec.Header().Get("Strict-Transport-Security") != ""
			if got != tt.wantHSTS {
				t.Errorf("ENV=%s: HSTS present = %v, want %v", tt.env, got, tt.wantHSTS)
			}
		})
	}
}

// TestServer_NavAndBrand pins the Phase-A UX fixes: the header carries the
// real project name and links to every demo surface (the /learn hrefs are
// static layout markup, so they render even when auth is disabled), and no
// page ships inline <script> blocks — script-src forbids them (ADR-028), so
// behavior must live in external files.
func TestServer_NavAndBrand(t *testing.T) {
	srv := newTestServer(t, "development")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		"Go Performance Starter",
		`href="/patterns"`,
		`href="/learn/quiz"`,
		`href="/learn/flashcards"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / body missing %q", want)
		}
	}
	if strings.Contains(body, "Micro SaaS Starter") {
		t.Error("GET / still carries the stale 'Micro SaaS Starter' brand")
	}

	// Every <script> must have a src attribute; inline bodies are CSP-blocked.
	for _, m := range regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(m, "src=") {
			t.Errorf("inline script tag %q would be blocked by script-src 'self' (ADR-028)", m)
		}
	}
}

// TestServer_HomeDirectoryAndBrand pins the brand/home rework: the home page
// is a landing with a directory of the demo surfaces (not the old
// form-validation stub), the brand mark (the project's own bolt-in-brackets
// SVG — NOT the Pezza emblem) appears only in brand placements, and the
// lockup link is accessibly labelled.
func TestServer_HomeDirectoryAndBrand(t *testing.T) {
	srv := newTestServer(t, "development")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	if !strings.Contains(body, `data-testid="surface-directory"`) {
		t.Error("home page missing the surface directory")
	}
	if got := strings.Count(body, `data-testid="surface-card"`); got != 4 {
		t.Errorf("surface directory has %d cards, want 4 (patterns, quiz, flashcards, dashboard)", got)
	}
	if strings.Contains(body, `name="test_field"`) {
		t.Error("home page still renders the form-validation stub")
	}
	if !strings.Contains(body, `aria-label="Go Performance Starter`) {
		t.Error("brand lockup link missing its accessible label")
	}
	// The mark appears in exactly two places on the landing page: the header
	// lockup and the hero. A third occurrence means a functional control
	// (e.g. the theme toggle) is wearing the brand again.
	if got := strings.Count(body, "data-brand-mark"); got != 2 {
		t.Errorf("brand mark rendered %d times, want exactly 2 (header lockup + hero — controls must not wear the brand)", got)
	}
	// The Pezza emblem is a different brand and must never reappear here.
	if strings.Contains(body, "emblem-") {
		t.Error("page references the retired Pezza emblem assets")
	}
}

// TestServer_AssetCacheBusting proves rendered pages reference static assets
// through version-stamped URLs: /static ships a 1-year Cache-Control, so an
// unstamped reference strands every returning browser on stale CSS/JS after
// a deploy (ADR-016 assumed versioned assets; this pins the implementation).
func TestServer_AssetCacheBusting(t *testing.T) {
	srv := newTestServer(t, "development")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, asset := range []string{"app.css", "htmx.min.js", "alpine.min.js", "app.js"} {
		if !strings.Contains(body, asset+"?v=") {
			t.Errorf("asset %s referenced without a cache-busting ?v= stamp", asset)
		}
	}
	// ADR-029: one stylesheet — the role tokens live in app.css.
	if strings.Contains(body, "semantic-colors.css") {
		t.Error("page still references the retired semantic-colors.css")
	}
	// No global loading overlay: it flashed the full screen on every HTMX
	// fragment swap. In-flight feedback is per-element (.htmx-request,
	// hx-indicator, hx-disabled-elt).
	if strings.Contains(body, "global-loading") {
		t.Error("page still renders the retired full-screen loading overlay")
	}
}

// TestServer_HomeExplainer pins ADR-024 surface 1 (#67): the landing page
// renders the five-node architecture walkthrough with anchors, source
// peeks and ADR links, plus the live budget grid whose values come from
// internal/performance — while the original hero and surface directory
// stay in place (the directory is the skeleton the explainer grows around).
func TestServer_HomeExplainer(t *testing.T) {
	srv := newTestServer(t, "development")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	if !strings.Contains(body, `data-testid="explainer"`) {
		t.Fatal("home page missing the architecture explainer section")
	}
	if got := strings.Count(body, `data-testid="explainer-node"`); got != 5 {
		t.Errorf("explainer renders %d nodes, want 5 (routing, handlers, database, frontend, performance)", got)
	}
	for _, anchor := range []string{`id="routing"`, `id="handlers"`, `id="database"`, `id="frontend"`, `id="performance"`} {
		if !strings.Contains(body, anchor) {
			t.Errorf("explainer missing node anchor %s", anchor)
		}
	}
	for _, adr := range []string{"ADR-014-", "ADR-017-", "ADR-003-", "ADR-007-", "ADR-000-"} {
		if !strings.Contains(body, "docs/adr/"+adr) {
			t.Errorf("explainer missing a link to %s", adr)
		}
	}
	if got := strings.Count(body, "<details"); got != 5 {
		t.Errorf("explainer renders %d source peeks, want 5 (one <details> per node, no JS required)", got)
	}
	if !strings.Contains(body, `href="/learn/quiz"`) {
		t.Error("explainer must hand off to the quiz (read → quiz → flashcards)")
	}

	if !strings.Contains(body, `data-testid="perf-stats"`) {
		t.Fatal("home page missing the live performance budgets section")
	}
	if got := strings.Count(body, `data-testid="perf-stat"`); got != 10 {
		t.Errorf("budget grid renders %d stats, want 10", got)
	}
	// Values come from the constants, not the template: spot-check two.
	for _, want := range []string{"&lt; 100ms", "&lt; 20 MB", "&lt; 50 KB"} {
		if !strings.Contains(body, want) {
			t.Errorf("budget grid missing %q (rendered from internal/performance)", want)
		}
	}
}

// TestServer_CloseIsIdempotentAndKeepsServing pins the #117 lifecycle
// contract: Close releases router-owned goroutines, may be called more than
// once, and never breaks request handling — the rate limiter keeps limiting
// without its eviction sweep.
func TestServer_CloseIsIdempotentAndKeepsServing(t *testing.T) {
	srv := newTestServer(t, "development")

	srv.Close()
	srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz after Close() status = %d, want %d", rec.Code, http.StatusOK)
	}
}
