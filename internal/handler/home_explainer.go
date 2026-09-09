package handler

import (
	"fmt"

	"github.com/clownware/go-performance-starter/internal/performance"
	"github.com/clownware/go-performance-starter/internal/view/pages"
)

// The landing explainer is ADR-024 surface 1 (#67): a server-rendered
// walkthrough of this system's own architecture, narrated as a request's
// journey (design brief §Explainer content map). Each node carries prose, a
// source peek and its ADR link, and is keyed to the quiz topic that draws
// from it, so the loop closes: read → quiz → flashcards. The live budget
// stats are rendered from internal/performance constants at request time —
// server-rendered dynamic content as its own demo (brief §Live performance
// stats: "render from constants, never hardcode").

const adrBase = "https://github.com/clownware/go-performance-starter/blob/master/docs/adr/"

func adrLink(file, label string) pages.ExplainerLink {
	return pages.ExplainerLink{Href: adrBase + file, Label: label}
}

// ExplainerNodes returns the five-step spine in request order. Source peeks
// are abridged excerpts of the real files they cite (same hand-maintained
// convention as the patterns catalogue; #73 tracks generating them).
func ExplainerNodes() []pages.ExplainerNode {
	return []pages.ExplainerNode{
		{
			Step:      1,
			Anchor:    "routing",
			QuizTopic: "routing",
			Title:     "Request in: Chi router + middleware stack",
			Summary:   "One deliberate chain — security headers, request ID, trusted-proxy client IP, rate limits, compression, metrics, logging, recovery, timeout, CSRF — then the session middlewares per route group.",
			Prose: []string{
				"Every request enters through a single chi router whose middleware order is a design decision, not an accident: SecurityHeaders first, RealIP before the rate limiter (so limits key on the real client behind the Cloudflare proxy), CSRF before any handler runs.",
				"Identity is layered per route group rather than globally. Public pages carry no session at all; /learn mounts GuestSession → OptionalAuth → OptionalUserLoader so a visitor gets a real anonymous Supabase identity on first touch; /profile and /auth/* add the strict tiers. Credential endpoints sit under a second, tighter rate limiter on top of the global one.",
			},
			ADR: adrLink("ADR-014-Security-Patterns-and-Threat-Model.md", "ADR-014 Security patterns · ADR-027 trusted proxies"),
			Source: pages.ExplainerSource{
				File: "internal/server/server.go",
				Snippet: `s.router.Use(mw.SecurityHeaders(isProd))
s.router.Use(mw.RequestID)
s.router.Use(mw.RealIP(s.cfg.TrustedProxyCIDRs, s.cfg.ClientIPHeader)) // before the limiter
s.router.Use(mw.RateLimiter(ctx, 50, 10)) // eviction stops with the server (#117)
s.router.Use(mw.Compress(5))
s.router.Use(mw.Metrics)
s.router.Use(mw.RequestLogger)
s.router.Use(mw.Recoverer)
s.router.Use(mw.Timeout(30 * time.Second))
s.router.Use(mw.CSRF(isProd))

r.Group(func(learn chi.Router) {
    learn.Use(mw.GuestSession(s.authClient, isProd)) // anonymous identity on first touch
    learn.Use(mw.OptionalAuth(s.authClient, isProd))
    learn.Use(mw.OptionalUserLoader(userRepo))
    learn.Use(mw.RateLimiter(ctx, 30.0/60.0, 20))    // stricter tier, anonymous-writable
    handler.QuizRoutes(learn, quizRepo)
})`,
			},
		},
		{
			Step:      2,
			Anchor:    "handlers",
			QuizTopic: "handlers",
			Title:     "Handler resolves typed input",
			Summary:   "Handlers parse the form, validate at the boundary, call a repository interface, and hand a typed props struct to a templ view — one render path for pages and fragments.",
			Prose: []string{
				"A handler never sees SQL and never builds a map[string]interface{}. It reads its input, validates once at the system boundary, talks to a repository interface (the concrete Postgres type is injected), and constructs a typed props struct that the template was compiled against — a missing field is a build error, not a runtime surprise.",
				"view.Render is the single render path. The same handler answers a full navigation with the page and an HTMX request with just the partial, decided by view.IsHTMXRequest — which is what makes progressive enhancement cheap: the page works as plain forms, HTMX upgrades it.",
			},
			ADR: adrLink("ADR-017-Templ-Adoption.md", "ADR-017 templ · ADR-012 routing & UI patterns"),
			Source: pages.ExplainerSource{
				File: "internal/handler/profile_handlers.go",
				Snippet: `func ProfileUpdate(w http.ResponseWriter, r *http.Request) {
    name := strings.TrimSpace(r.FormValue("name"))
    if name == "" { /* 422 with a field error, partial or page */ }

    repo := webutil.GetUserRepoFromContext(r.Context()) // repository interface, not *pgxpool.Pool
    if _, err := repo.UpdateName(r.Context(), user.ID, name); err != nil { /* 500 */ }

    if view.IsHTMXRequest(r) {
        view.SetHXTrigger(w, "Profile updated successfully!")
        view.Render(w, r, http.StatusOK, partials.ProfileForm(partials.ProfileFormProps{Name: name, Success: true}))
        return
    }
    http.Redirect(w, r, "/profile", http.StatusSeeOther)
}`,
			},
		},
		{
			Step:      3,
			Anchor:    "database",
			QuizTopic: "database",
			Title:     "Repository → sqlc → Postgres, scoped by RLS",
			Summary:   "Queries are declared in SQL and compiled by sqlc into typed Go; the repository runs each one inside a transaction that carries the request's JWT claims, so Row Level Security scopes every row to auth.uid().",
			Prose: []string{
				"Data access is a compile step, not string concatenation: sql/queries/*.sql is the source, sqlc generates the typed Go in internal/database, and the repository layer is the only thing that calls it. A regeneration-drift check keeps the generated code honest against its sources.",
				"The tenant boundary lives in the database. The repository opens a transaction, sets the request's JWT claims as a local setting, and switches to the authenticated role — so users_self_access (auth_id = auth.uid()) is enforced by Postgres on every read and write. Anonymous guests are scoped by exactly the same policy as registered users: no parallel code path, no way for a handler to forget.",
			},
			ADR: adrLink("ADR-003-SQL-Code-Generation-and-Data-Access.md", "ADR-003 sqlc + repositories · ADR-004 RLS"),
			Source: pages.ExplainerSource{
				File: "sql/queries/flashcards.sql",
				Snippet: `-- name: ListFlashcardsByUser :many
SELECT id, user_id, question_id, front, back, is_known, created_at, updated_at
FROM flashcards
WHERE user_id = $1
ORDER BY created_at DESC;

-- migrations/000003_add_quiz_flashcards.up.sql
ALTER TABLE flashcards ENABLE ROW LEVEL SECURITY;
ALTER TABLE flashcards FORCE ROW LEVEL SECURITY;
CREATE POLICY flashcards_self_access ON flashcards
    USING      (EXISTS (SELECT 1 FROM users u WHERE u.id = flashcards.user_id AND u.auth_id = auth.uid()::text))
    WITH CHECK (EXISTS (SELECT 1 FROM users u WHERE u.id = flashcards.user_id AND u.auth_id = auth.uid()::text));`,
			},
		},
		{
			Step:      4,
			Anchor:    "frontend",
			QuizTopic: "frontend",
			Title:     "templ renders, HTMX swaps, Alpine sprinkles",
			Summary:   "Components are typed Go functions compiled from .templ files; HTMX requests fragments and swaps them in place; Alpine handles the light client-only interactivity. Role tokens, not raw colors, so dark mode flips variables instead of components.",
			Prose: []string{
				"templ compiles every template to Go, so props are structs and the view layer type-checks with the rest of the build. Pages compose a layout; partials render standalone so an HTMX fragment is exactly the markup the page would contain.",
				"HTMX is the interaction model — hx-get/hx-post on plain elements, the server answering with HTML, swaps targeted by id — and every page must still work when it is not there (progressive enhancement). Alpine is reserved for client-only state like tabs and modals. Styling speaks role tokens (bg-surface, text-muted-foreground); a CI scan fails the build on raw palette colors or dark: variants.",
			},
			ADR: adrLink("ADR-007-Frontend-Stack-Selection.md", "ADR-007 frontend stack · ADR-029 role tokens"),
			Source: pages.ExplainerSource{
				File: "internal/view/partials/quiz_question.templ",
				Snippet: `// A standalone fragment for HTMX swaps AND a plain form without JS:
// hx-post swaps the card in place, the form action posts the same payload full-page.
templ QuizQuestionCard(props QuizQuestionProps) {
    <form
        method="post"
        action={ templ.SafeURL(quizAnswerAction(props.Slug)) }
        hx-post={ quizAnswerAction(props.Slug) }
        hx-target="#quiz-card"
        hx-swap="innerHTML"
    >
        @components.CSRFField()
        for i, choice := range props.Choices {
            <label class="flex items-center gap-3 p-3 rounded-md border border-border hover:border-primary">
                <input type="radio" name="choice" value={ fmt.Sprintf("%d", i) } required/>
                <span>{ choice }</span>
            </label>
        }
        <button type="submit" class="btn btn-primary">Check answer</button>
    </form>
}`,
			},
		},
		{
			Step:      5,
			Anchor:    "performance",
			QuizTopic: "performance",
			Title:     "Performance budgets, enforced in CI and observed in production",
			Summary:   "Binary, memory, startup and gzipped JS/CSS budgets are constants in Go, gated by task ci, and observed via Prometheus — the numbers below are rendered from those constants on every request.",
			Prose: []string{
				"ADR-000 states the budgets once, in internal/performance/budgets.go. task ci builds the stripped binary and measures it, gzips the shipped JS and CSS and measures those, and runs the budget tests with the race detector — so a regression is a red build, not a slow page someone notices later.",
				"At runtime the same constants feed this page: the stats grid is not a hardcoded list, it is a handler reading the constants and a templ component rendering them — dynamic server-rendered content with zero client JavaScript. Request latency and memory are exported to Prometheus at /metrics (bearer-gated in production).",
			},
			ADR: adrLink("ADR-000-Performance-Budgets-and-Quality-Attributes.md", "ADR-000 performance budgets · ADR-021 quality gate"),
			Source: pages.ExplainerSource{
				File: "internal/performance/budgets.go",
				Snippet: `const (
    MaxP95ResponseTime = 100 * time.Millisecond
    MaxP99ResponseTime = 200 * time.Millisecond
    MaxBinarySize      = 20 * 1024 * 1024  // stripped linux build
    MaxMemoryUsage     = 128 * 1024 * 1024
    MaxStartupTime     = 500 * time.Millisecond
    MaxJavaScriptSize  = 50 * 1024         // gzipped
    MaxCSSSize         = 30 * 1024         // gzipped
)
// task ci → test:binary-size, test:asset-budgets, go test ./internal/performance/...`,
			},
		},
	}
}

// PerfBudgetStats renders the ADR-000 budgets from the constants in
// internal/performance — never from literals here, so the landing page can
// only ever show what CI actually enforces.
func PerfBudgetStats() []pages.BudgetStat {
	return []pages.BudgetStat{
		{Label: "P50 response", Value: performance.MaxP50ResponseTime.String(), Note: "median latency target"},
		{Label: "P95 response", Value: performance.MaxP95ResponseTime.String(), Note: "enforced by the budget tests"},
		{Label: "P99 response", Value: performance.MaxP99ResponseTime.String(), Note: "tail latency ceiling"},
		{Label: "Binary size", Value: formatBudgetBytes(performance.MaxBinarySize), Note: "stripped linux build, gated in task ci"},
		{Label: "Memory", Value: formatBudgetBytes(performance.MaxMemoryUsage), Note: "steady state"},
		{Label: "Peak memory", Value: formatBudgetBytes(performance.MaxPeakMemory), Note: "under load"},
		{Label: "Startup", Value: performance.MaxStartupTime.String(), Note: "process start to listening"},
		{Label: "JavaScript", Value: formatBudgetBytes(performance.MaxJavaScriptSize), Note: "gzipped — htmx + Alpine + app.js"},
		{Label: "CSS", Value: formatBudgetBytes(performance.MaxCSSSize), Note: "gzipped Tailwind build"},
		{Label: "Total page", Value: formatBudgetBytes(performance.MaxTotalPageSize), Note: "HTML + assets"},
	}
}

// formatBudgetBytes renders byte budgets in the units ADR-000 states them
// in (whole KB/MB when exact, one decimal otherwise).
func formatBudgetBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	value := float64(n) / float64(div)
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d %cB", int64(value), "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", value, "KMGTPE"[exp])
}
