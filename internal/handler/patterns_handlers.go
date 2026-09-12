package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/clownware/go-performance-starter/internal/view"
	"github.com/clownware/go-performance-starter/internal/view/pages"
	"github.com/clownware/go-performance-starter/internal/view/partials"
)

// /patterns is ADR-024 surface 2: the public HTMX/Alpine pattern showcase.
// Self-contained stub data, no database, no auth — the page must render with
// Supabase credentials unset. Each section is a demo panel plus tabbed
// (templ | handler) source, one per pattern in the catalogue retained from
// docs/updates/ux-overhaul-spec.md.

// PatternsRoutes registers the /patterns showcase page and its stub demo API.
func PatternsRoutes(r chi.Router) {
	r.Get("/patterns", PatternsPage)
	r.Route("/patterns/api", func(api chi.Router) {
		api.Get("/swap", PatternSwap)
		api.Get("/search", PatternSearch)
		api.Get("/edit/{id}", PatternEditForm)
		api.Put("/edit/{id}", PatternEditSave)
		api.Post("/favorite/{id}", PatternFavorite)
		api.Get("/scroll", PatternScroll)
		api.Get("/typeahead", PatternTypeahead)
		api.Post("/toast", PatternToast)
		api.Get("/skeleton", PatternSkeleton)
		api.Get("/tab/{name}", PatternTab)
		api.Post("/bulk", PatternBulk)
		api.Get("/time", PatternTime)             // polling tick
		api.Post("/counter", PatternCounter)      // out-of-band swap
		api.Post("/confirm", PatternConfirm)      // hx-confirm + hx-disabled-elt
		api.Get("/transition", PatternTransition) // View Transitions swap
		api.Get("/slow", PatternSlow)             // hx-indicator + hx-disabled-elt
	})
}

// patternStubData is the shared dataset the search, typeahead, and scroll
// demos filter and page over — the pieces of this starter's own stack.
var patternStubData = []string{
	"Go 1.26", "Chi router", "templ", "HTMX", "Alpine.js", "Tailwind CSS",
	"PostgreSQL", "sqlc", "pgx", "Supabase Auth", "Row Level Security",
	"golang-migrate", "Taskfile", "golangci-lint", "Prometheus", "Fly.io",
	"Docker", "GitHub Actions", "air (live reload)", "gofmt",
}

const patternScrollPerPage = 5

// patternCategories are the teaching groups the showcase is organized by,
// in display order. Every catalogue entry names one (enforced by test).
var patternCategories = []view.PatternCategory{
	{Slug: "fetch-swap", Title: "Fetch & swap", Blurb: "The core loop: an attribute fires a request, the server renders HTML, HTMX places it."},
	{Slug: "search-lists", Title: "Search & lists", Blurb: "Server-filtered results with debounce, indicators, and pagination that loads itself."},
	{Slug: "forms-actions", Title: "Forms & actions", Blurb: "Validation, editing, confirmation, and honest in-flight feedback — all server-round-trip."},
	{Slug: "server-driven", Title: "Server-driven UX", Blurb: "Responses that steer the page: toasts from headers, multi-region swaps, animated transitions."},
	{Slug: "alpine-islands", Title: "Alpine islands", Blurb: "The client-only slice: state, teleported modals, and stores shared across components."},
}

// patternGroups orders the catalogue by category for the grouped page.
func patternGroups() []view.PatternGroup {
	groups := make([]view.PatternGroup, 0, len(patternCategories))
	for _, cat := range patternCategories {
		g := view.PatternGroup{Category: cat}
		for _, s := range patternsCatalog {
			if s.Category == cat.Slug {
				g.Sections = append(g.Sections, s)
			}
		}
		groups = append(groups, g)
	}
	return groups
}

// patternsCatalog drives the showcase sections. Source snippets are abridged
// from the real handlers and templates below (kept in sync by hand — see
// ux-overhaul-spec.md for the generated-source future iteration).
var patternsCatalog = []view.PatternSection{
	{
		Slug:         "partial-swap",
		Category:     "fetch-swap",
		Title:        "Partial swap",
		Summary:      "Click a button, replace a fragment. The simplest HTMX pattern — a GET returns rendered HTML and hx-target places it.",
		HTMXFeatures: []string{"hx-get", "hx-target", "hx-swap"},
		TemplSource: `<button hx-get="/patterns/api/swap"
  hx-target="#swap-demo" hx-swap="innerHTML">
  Swap this fragment
</button>
<div id="swap-demo">...</div>`,
		HandlerSource: `func PatternSwap(w http.ResponseWriter, r *http.Request) {
  view.Render(w, r, http.StatusOK,
    partials.PatternSwapResult())
}`,
	},
	{
		Slug:         "live-search",
		Category:     "search-lists",
		Title:        "Live search",
		Summary:      "Type in a search box; results filter server-side with a debounce, so the server stays the single source of truth.",
		HTMXFeatures: []string{"hx-get", `hx-trigger="keyup changed delay:300ms"`, "hx-target"},
		TemplSource: `<input type="search" name="q"
  hx-get="/patterns/api/search"
  hx-trigger="keyup changed delay:300ms"
  hx-target="#search-results"/>`,
		HandlerSource: `q := strings.TrimSpace(r.URL.Query().Get("q"))
if q == "" { // cleared input clears the results
  w.WriteHeader(http.StatusOK)
  return
}
results := filterStubData(q)
view.Render(w, r, http.StatusOK,
  partials.PatternSearchResults(props))`,
	},
	{
		Slug:         "click-to-edit",
		Category:     "forms-actions",
		Title:        "Click to edit",
		Summary:      "Click text to turn it into an edit form; saving swaps the display view back in. Two endpoints, zero client state.",
		HTMXFeatures: []string{"hx-get", "hx-put", "hx-swap"},
		TemplSource: `<div hx-get="/patterns/api/edit/1"
  hx-swap="outerHTML">{ value }</div>
<form hx-put="/patterns/api/edit/1"
  hx-swap="outerHTML">...</form>`,
		HandlerSource: `// GET returns the form; PUT saves and
// returns the display view.
value := r.FormValue("value")
view.Render(w, r, http.StatusOK,
  partials.PatternEditDisplay(props))`,
	},
	{
		Slug:           "inline-validation",
		Category:       "forms-actions",
		Title:          "Inline validation",
		Summary:        "Alpine flags the field once you leave it (not on the first keystroke), then clears live as you fix it; the server re-checks on submit and returns field errors into the same markup.",
		HTMXFeatures:   []string{"hx-post", "hx-target"},
		AlpineFeatures: []string{"x-data", "x-model", "x-show", "@blur"},
		TemplSource: `<form x-data="{ email: '', touched: false }">
  <input type="email" x-model="email"
    @blur="touched = true"/>
  <p x-show="touched && email
      && !email.includes('@')">
    That doesn't look like an email.
  </p>
</form>`,
		HandlerSource: `// Server-side mirror of the client rules —
// see ProfileUpdate for the full pattern:
// errors map rendered at 422 into the form.`,
	},
	{
		Slug:           "optimistic-ui",
		Category:       "forms-actions",
		Title:          "Optimistic UI",
		Summary:        "A favorite toggle that swaps instantly — current state rides along in hx-vals, so the endpoint stays stateless.",
		HTMXFeatures:   []string{"hx-post", `hx-swap="outerHTML"`, "hx-vals"},
		AlpineFeatures: []string{"x-transition"},
		TemplSource: `<button hx-post="/patterns/api/favorite/1"
  hx-vals={ favoritedState }
  hx-swap="outerHTML" x-transition>
  &#9734; Favorite
</button>`,
		HandlerSource: `favorited := r.FormValue("favorited") == "true"
props := partials.PatternFavoriteProps{
  ID: id, Favorited: !favorited}
view.Render(w, r, http.StatusOK,
  partials.PatternFavoriteButton(props))`,
	},
	{
		Slug:         "infinite-scroll",
		Category:     "search-lists",
		Title:        "Infinite scroll",
		Summary:      "Scroll to the bottom and the next page loads automatically; the sentinel row replaces itself with each new page. Inside a scrollable container the trigger is intersect, not revealed — revealed only watches the window scroll.",
		HTMXFeatures: []string{"hx-get", `hx-trigger="intersect once"`, `hx-swap="outerHTML"`},
		TemplSource: `<li hx-get="/patterns/api/scroll?page=2"
  hx-trigger="intersect once" hx-swap="outerHTML">
  Loading more...
</li>`,
		HandlerSource: `page, _ := strconv.Atoi(r.URL.Query().Get("page"))
items, next := stubPage(page)
view.Render(w, r, http.StatusOK,
  partials.PatternScrollPage(props))`,
	},
	{
		Slug:         "typeahead",
		Category:     "search-lists",
		Title:        "Active search (typeahead)",
		Summary:      "Like live search, plus a loading indicator that HTMX toggles for you while the request is in flight.",
		HTMXFeatures: []string{"hx-get", `hx-trigger="keyup changed delay:200ms"`, "hx-indicator"},
		TemplSource: `<input hx-get="/patterns/api/typeahead"
  hx-trigger="keyup changed delay:200ms"
  hx-indicator="#typeahead-indicator"/>
<span id="typeahead-indicator"
  class="htmx-indicator">Searching...</span>`,
		HandlerSource: `// Identical server shape to live search —
// the indicator is pure client wiring.`,
	},
	{
		Slug:           "toasts",
		Category:       "server-driven",
		Title:          "Toast notifications",
		Summary:        "The response body swaps a tiny status line; the toast itself fires from the HX-Trigger header into an Alpine listener.",
		HTMXFeatures:   []string{"HX-Trigger", "HX-Toast-Type"},
		AlpineFeatures: []string{"x-data", "x-for", "x-transition"},
		TemplSource: `<button hx-post="/patterns/api/toast"
  hx-vals={ toastType }>Success toast</button>
<!-- base layout listens for HX-Trigger
     and dispatches a toast event -->`,
		HandlerSource: `view.SetHXTrigger(w, "Toast from the server!")
w.Header().Set("HX-Toast-Type", toastType)
view.Render(w, r, http.StatusOK,
  partials.PatternToastResult())`,
	},
	{
		Slug:           "dark-mode",
		Category:       "alpine-islands",
		Title:          "Dark mode",
		Summary:        "Alpine state seeded from the system preference, persisted to localStorage, applied as a class on the root element.",
		AlpineFeatures: []string{"x-data", "$watch", "localStorage"},
		TemplSource: `<body x-data="{ dark: localStorage.getItem('dark')
    === 'true' || prefersDark }"
  x-init="$watch('dark', v => ...)">`,
		HandlerSource: `// No handler — this pattern is entirely
// client-side; the server ships one class
// toggle and no JavaScript framework.`,
	},
	{
		Slug:           "skeleton-loading",
		Category:       "fetch-swap",
		Title:          "Skeleton loading",
		Summary:        "A CSS skeleton renders instantly; HTMX replaces it with real content on a delayed load trigger.",
		HTMXFeatures:   []string{"hx-get", `hx-trigger="load delay:1.5s"`},
		AlpineFeatures: []string{},
		TemplSource: `<div hx-get="/patterns/api/skeleton"
  hx-trigger="load delay:1.5s">
  <div class="animate-pulse">...</div>
</div>`,
		HandlerSource: `// The server returns immediately — the
// delay is client-side, so no goroutine
// sleeps on a demo's behalf.
view.Render(w, r, http.StatusOK,
  partials.PatternSkeletonContent())`,
	},
	{
		Slug:           "tabs",
		Category:       "fetch-swap",
		Title:          "Tabs",
		Summary:        "Tab buttons fetch server-rendered panels; Alpine handles the client-only variant (see the source panel you're using).",
		HTMXFeatures:   []string{"hx-get", "hx-target"},
		AlpineFeatures: []string{"x-data", "x-show"},
		TemplSource: `<button hx-get="/patterns/api/tab/templ"
  hx-target="#tab-demo-panel">templ</button>
<div id="tab-demo-panel"></div>`,
		HandlerSource: `name := chi.URLParam(r, "name")
content, ok := patternTabs[name]
if !ok { http.NotFound(w, r); return }
view.Render(w, r, http.StatusOK,
  partials.PatternTabPanel(props))`,
	},
	{
		Slug:           "bulk-operations",
		Category:       "forms-actions",
		Title:          "Bulk operations",
		Summary:        "Alpine tracks the checkbox selection client-side; one HTMX POST submits the whole batch as form values.",
		HTMXFeatures:   []string{"hx-post", "hx-target"},
		AlpineFeatures: []string{"x-data", "x-model", "x-text"},
		TemplSource: `<form x-data="{ selected: [] }"
  hx-post="/patterns/api/bulk"
  hx-target="#bulk-demo-result">
  <input type="checkbox" name="selected"
    x-model="selected"/>
  <button :disabled="selected.length === 0">
    Archive (<span x-text="selected.length"/>)
  </button>
</form>`,
		HandlerSource: `r.ParseForm()
count := len(r.Form["selected"])
view.Render(w, r, http.StatusOK,
  partials.PatternBulkResult(count))`,
	},
	{
		Slug:         "polling",
		Category:     "fetch-swap",
		Title:        "Polling",
		Summary:      "A live server value without WebSockets: the element re-fetches itself on a fixed interval — the whole real-time stack is one attribute.",
		HTMXFeatures: []string{`hx-trigger="load, every 5s"`, "hx-get"},
		TemplSource: `<div hx-get="/patterns/api/time"
  hx-trigger="load, every 5s"
  hx-swap="innerHTML">...</div>`,
		HandlerSource: `now := time.Now().Format("15:04:05")
view.Render(w, r, http.StatusOK,
  partials.PatternTimeTick(now, load))`,
	},
	{
		Slug:         "oob-swap",
		Category:     "server-driven",
		Title:        "Out-of-band swap",
		Summary:      "One response updates two regions: the button replaces itself, and a badge elsewhere updates via hx-swap-oob riding the same payload.",
		HTMXFeatures: []string{"hx-swap-oob", "hx-vals", `hx-swap="outerHTML"`},
		TemplSource: `<!-- response contains BOTH: -->
<button id="oob-counter-btn" ...>Add item</button>
<span id="oob-total" hx-swap-oob="true">
  3 added
</span>`,
		HandlerSource: `count, _ := strconv.Atoi(r.FormValue("count"))
view.Render(w, r, http.StatusOK,
  partials.PatternCounterResponse(
    partials.PatternCounterProps{Count: count + 1}))`,
	},
	{
		Slug:           "confirm-delete",
		Category:       "forms-actions",
		Title:          "Confirm + disabled button",
		Summary:        "The htmx:confirm event pauses the request while a styled Alpine dialog asks — no native window.confirm popup — and hx-disabled-elt holds the button while it runs. hx-confirm stays on as the no-Alpine fallback.",
		HTMXFeatures:   []string{"htmx:confirm", "hx-confirm", `hx-disabled-elt="this"`},
		AlpineFeatures: []string{"x-data", "x-teleport", "x-show"},
		TemplSource: `<div x-data="{ open: false, issue: null }"
  @htmx:confirm.prevent="issue =
    $event.detail.issueRequest; open = true">
  <button hx-post="/patterns/api/confirm"
    hx-confirm="Delete this demo item?"
    hx-disabled-elt="this">Delete item</button>
  <!-- styled dialog; Delete resumes with
       issue(true), Cancel just closes -->
</div>`,
		HandlerSource: `// The interesting part happens client-side
// before this runs: htmx:confirm pauses the
// request until issueRequest(true) resumes it.
view.Render(w, r, http.StatusOK,
  partials.PatternConfirmResult())`,
	},
	{
		Slug:         "view-transitions",
		Category:     "server-driven",
		Title:        "View Transitions",
		Summary:      "transition:true on the swap animates it through the browser's View Transitions API — cross-fade with zero animation code, instant fallback elsewhere.",
		HTMXFeatures: []string{`hx-swap="innerHTML transition:true"`},
		TemplSource: `<button hx-get="/patterns/api/transition?step=1"
  hx-target="#vt-card"
  hx-swap="innerHTML transition:true">
  Next card
</button>`,
		HandlerSource: `step, _ := strconv.Atoi(r.URL.Query().Get("step"))
view.Render(w, r, http.StatusOK,
  partials.PatternTransitionCard(step))`,
	},
	{
		Slug:         "loading-states",
		Category:     "forms-actions",
		Title:        "Loading states",
		Summary:      "The right way to show in-flight work: a per-element spinner (hx-indicator) and a disabled trigger (hx-disabled-elt) — never a full-screen overlay.",
		HTMXFeatures: []string{"hx-indicator", `hx-disabled-elt="this"`},
		TemplSource: `<button hx-get="/patterns/api/slow"
  hx-indicator="#slow-spin"
  hx-disabled-elt="this"
  hx-target="#slow-out">Load slowly</button>
<span id="slow-spin" class="htmx-indicator">
  Working...
</span>`,
		HandlerSource: `// Deliberate delay so there is something
// to indicate (capped at 1.5s).
time.Sleep(delay)
view.Render(w, r, http.StatusOK,
  partials.PatternSlowContent())`,
	},
	{
		Slug:           "modal",
		Category:       "alpine-islands",
		Title:          "Modal (teleport)",
		Summary:        "An accessible Alpine modal: x-teleport lifts it to <body> so no ancestor clips it, Escape and backdrop close it, focus lands inside.",
		AlpineFeatures: []string{"x-teleport", "x-show", "@keydown.escape.window", "x-transition"},
		TemplSource: `<button @click="open = true">Open modal</button>
<template x-teleport="body">
  <div x-show="open" @keydown.escape.window="open = false">
    <div @click="open = false" class="backdrop"></div>
    <div role="dialog" aria-modal="true">...</div>
  </div>
</template>`,
		HandlerSource: `// No handler — entirely client-side.
// Server-rendered pages still work without
// it: the modal content is enhancement.`,
	},
	{
		Slug:           "global-store",
		Category:       "alpine-islands",
		Title:          "Global store",
		Summary:        "Alpine.store shares state across unrelated components: two separate x-data islands read and write one store registered in app.js.",
		AlpineFeatures: []string{"Alpine.store", "$store", "x-text"},
		TemplSource: `<!-- two unrelated components: -->
<button @click="$store.demo.inc()">+1</button>
...
<span x-text="$store.demo.count"></span>`,
		HandlerSource: `// app.js, at alpine:init:
Alpine.store("demo", {
  count: 0,
  inc() { this.count++ },
})`,
	},
}

// patternTabs is the content served by the server-loaded tabs demo.
var patternTabs = map[string]string{
	"templ":  "templ compiles these panels to type-checked Go — a bad prop is a build error, not a runtime surprise.",
	"htmx":   "HTMX fetched this panel with one attribute. The server rendered it; no client router, no JSON glue.",
	"alpine": "Alpine covers what stays client-only — the source tabs next to each demo are an x-show, no round trip.",
}

// PatternsPage renders the full showcase.
func PatternsPage(w http.ResponseWriter, r *http.Request) {
	props := pages.PatternsPageProps{
		BaseProps: patternsBaseProps(),
		Groups:    patternGroups(),
	}
	if err := view.Render(w, r, http.StatusOK, pages.PatternsPage(props)); err != nil {
		slog.Error("Failed to render patterns page", "error", err)
	}
}

// PatternSwap serves the partial-swap demo fragment.
func PatternSwap(w http.ResponseWriter, r *http.Request) {
	renderPattern(w, r, "swap", partials.PatternSwapResult())
}

// PatternSearch serves the live-search demo results. An empty query returns
// an empty fragment so clearing the input clears the results, rather than
// leaving stale matches or a `No matches for ""` message behind.
func PatternSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	props := partials.PatternSearchProps{Query: q, Results: filterPatternStubData(q)}
	renderPattern(w, r, "search", partials.PatternSearchResults(props))
}

// PatternTypeahead serves the typeahead demo results (same server shape as
// live search, including the clear-on-empty contract; the loading indicator
// is client wiring).
func PatternTypeahead(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	props := partials.PatternSearchProps{Query: q, Results: filterPatternStubData(q)}
	renderPattern(w, r, "typeahead", partials.PatternSearchResults(props))
}

// PatternEditForm serves the click-to-edit form.
func PatternEditForm(w http.ResponseWriter, r *http.Request) {
	props := partials.PatternEditProps{ID: chi.URLParam(r, "id"), Value: "Click me, then save your edit"}
	renderPattern(w, r, "edit form", partials.PatternEditForm(props))
}

// PatternEditSave accepts the edit and returns the display view.
func PatternEditSave(w http.ResponseWriter, r *http.Request) {
	value := strings.TrimSpace(r.FormValue("value"))
	if value == "" {
		value = "(nothing entered)"
	}
	props := partials.PatternEditProps{ID: chi.URLParam(r, "id"), Value: value}
	renderPattern(w, r, "edit save", partials.PatternEditDisplay(props))
}

// PatternFavorite flips the favorite state carried in hx-vals — stateless on
// purpose, so the demo needs no store to retire later (ADR-024 Slice C).
func PatternFavorite(w http.ResponseWriter, r *http.Request) {
	favorited := r.FormValue("favorited") == "true"
	props := partials.PatternFavoriteProps{ID: chi.URLParam(r, "id"), Favorited: !favorited}
	renderPattern(w, r, "favorite", partials.PatternFavoriteButton(props))
}

// PatternScroll serves one page of the infinite-scroll demo.
func PatternScroll(w http.ResponseWriter, r *http.Request) {
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	start := (page - 1) * patternScrollPerPage
	if start >= len(patternStubData) {
		start = len(patternStubData)
	}
	end := start + patternScrollPerPage
	if end > len(patternStubData) {
		end = len(patternStubData)
	}
	next := page + 1
	if end == len(patternStubData) {
		next = 0 // last page: no sentinel
	}
	props := partials.PatternScrollProps{Items: patternStubData[start:end], NextPage: next}
	renderPattern(w, r, "scroll", partials.PatternScrollPage(props))
}

// PatternToast fires a toast through the HX-Trigger header contract the base
// layout listens for; the body is just a small status line.
func PatternToast(w http.ResponseWriter, r *http.Request) {
	toastType := r.FormValue("type")
	switch toastType {
	case "error", "warning":
	default:
		toastType = "success"
	}
	// HX-Trigger rides an HTTP header (latin-1): keep the message ASCII or
	// browsers render mojibake.
	view.SetHXTrigger(w, "Toast from the server - no client toast library involved.")
	w.Header().Set("HX-Toast-Type", toastType)
	renderPattern(w, r, "toast", partials.PatternToastResult())
}

// PatternSkeleton serves the content behind the skeleton-loading demo. The
// perceived delay is the client-side load trigger — the server returns
// immediately rather than sleeping on a demo's behalf.
func PatternSkeleton(w http.ResponseWriter, r *http.Request) {
	renderPattern(w, r, "skeleton", partials.PatternSkeletonContent())
}

// PatternTab serves a server-loaded tab panel.
func PatternTab(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	content, ok := patternTabs[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	props := partials.PatternTabProps{Name: name, Content: content}
	renderPattern(w, r, "tab", partials.PatternTabPanel(props))
}

// PatternBulk reports how many selected items the batch contained.
func PatternBulk(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	renderPattern(w, r, "bulk", partials.PatternBulkResult(len(r.Form["selected"])))
}

// PatternTime serves one polling tick (client re-requests every 5s).
func PatternTime(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Format("15:04:05")
	load := fmt.Sprintf("goroutines: %d", runtime.NumGoroutine())
	renderPattern(w, r, "time", partials.PatternTimeTick(now, load))
}

// PatternCounter answers with the incremented button AND an out-of-band
// badge update — one response, two swapped regions. State rides in hx-vals
// so the endpoint stays stateless like the favorite toggle.
func PatternCounter(w http.ResponseWriter, r *http.Request) {
	count, _ := strconv.Atoi(r.FormValue("count"))
	if count < 0 || count > 1_000_000 {
		count = 0
	}
	renderPattern(w, r, "counter", partials.PatternCounterResponse(partials.PatternCounterProps{Count: count + 1}))
}

// PatternConfirm serves the post-confirmation state; the interesting part
// (hx-confirm, hx-disabled-elt) happens client-side before this runs.
func PatternConfirm(w http.ResponseWriter, r *http.Request) {
	renderPattern(w, r, "confirm", partials.PatternConfirmResult())
}

// PatternTransition serves the next card for the View Transitions demo.
func PatternTransition(w http.ResponseWriter, r *http.Request) {
	step, _ := strconv.Atoi(r.URL.Query().Get("step"))
	if step < 0 {
		step = 0
	}
	renderPattern(w, r, "transition", partials.PatternTransitionCard(step))
}

// PatternSlow delays server-side so hx-indicator/hx-disabled-elt have
// something to indicate. The delay is capped and overridable (tests pass 0).
func PatternSlow(w http.ResponseWriter, r *http.Request) {
	delay := 800
	if d, err := strconv.Atoi(r.URL.Query().Get("delay")); err == nil && d >= 0 {
		delay = d
	}
	if delay > 1500 {
		delay = 1500
	}
	select {
	case <-time.After(time.Duration(delay) * time.Millisecond):
	case <-r.Context().Done():
		return
	}
	renderPattern(w, r, "slow", partials.PatternSlowContent())
}

// renderPattern renders a demo fragment, logging render failures like every
// other handler in this package.
func renderPattern(w http.ResponseWriter, r *http.Request, name string, c templ.Component) {
	if err := view.Render(w, r, http.StatusOK, c); err != nil {
		slog.Error("Failed to render pattern fragment", "pattern", name, "error", err)
	}
}

// filterPatternStubData does the case-insensitive contains filtering shared
// by the search demos.
func filterPatternStubData(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var results []string
	for _, item := range patternStubData {
		if strings.Contains(strings.ToLower(item), q) {
			results = append(results, item)
		}
	}
	return results
}

// patternsBaseProps carries the showcase's own share description.
func patternsBaseProps() view.BaseProps {
	base := view.NewBaseProps("Pattern Showcase")
	base.Description = "Every HTMX and Alpine.js pattern this Go starter supports, live, with the templ and handler source beside each demo."
	return base
}
