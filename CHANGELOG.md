# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- RLS isolation check (ADR-034) on the flashcards page: one click runs
  your own `ListFlashcardsByUser` twice through the same repository — as
  you, then as a freshly minted stranger identity — and shows both row
  counts next to the policy text. Zero rows for the stranger is Postgres
  refusing the identical query, not a `WHERE` clause. Works without
  JavaScript via `?check=1`; the HTMX endpoint is
  `GET /learn/flashcards/isolation`. No other visitor's data is read
- Observed-vs-budget grid (ADR-034): the landing page's budgets section now
  shows, per budget, what this process observed since boot — p50/p95/p99
  over the last 4096 requests, memory and its high-water mark, startup
  (process start → listening socket, measured in `main`), the executable
  size, and the shipped assets gzipped — with pass/fail and the ADR-000
  class (Enforced/Monitored/Aspirational). Unmeasured values say so
  instead of showing a passing zero. Every response also carries
  `Server-Timing: app;dur=<ms>`. The asset file lists now live in
  `internal/performance` so the CI gate and the runtime measure the same
  files
- Share and crawl surface: every page carries a real meta description,
  Open Graph and Twitter card tags, and — when `PUBLIC_BASE_URL` is set —
  a canonical link plus absolute `og:url`/`og:image` (a 1200×630 share
  image ships at `/static/img/og.png`). `/robots.txt` disallows `/learn/*`
  on purpose (its identity chain mints a real anonymous Supabase user on
  first touch) and the per-user surfaces; `/sitemap.xml` lists the public
  identity-free pages and exists only when the origin is configured, so
  URLs are never guessed from the Host header. The README now links the
  live demo

### Changed
- `/health` readiness probe pings its dependency through a consumer-side
  `handler.Pinger` seam instead of importing `pgxpool` directly — the one
  standing `adr003-no-sql-in-handlers` warning from the ADR-033 launch is
  resolved by refactor, not allowlist (#92, #99)

### Fixed
- Found-work batch (2026-08-19): the Docker builder derives the templ CLI
  version from go.mod instead of a stale hand pin (the image regenerated
  committed output with v0.3.1001 while the repo was on v0.3.1020 — the
  same drift class `check:generated` now polices); static-asset error
  responses go out `no-store` instead of inheriting the one-year success
  header (a cached 404 could outlive the fix); the dashboard quiz total
  comes from a dedicated `CountAttemptsByUser` query instead of the length
  of a 200-row listing window, so long histories read true; the stray
  hand-written `internal/database/users.sql` inside the sqlc output dir is
  removed; the home dashboard card stops promising widgets that now exist
- The ADR-029 token scan (`internal/view/tokens_test.go`) now rejects raw
  utilities from every Tailwind palette family, not just gray; the four
  pre-ADR leftovers it surfaced (profile-form success box, first-run CTA
  card, two `bg-blue-600` empty-state buttons) now ride `bg-success/10
  text-success` and `.btn-primary` (#85)
- The guest reaper now sweeps the auth side too (#82): anonymous GoTrue
  identities older than the TTL with no `public.users` row — orphans from
  failed provisioning (the #81 window) or sign-ins that never hit a
  UserLoader route — were invisible to the row-driven reap and lingered
  forever. A second pass lists anonymous users via the admin API
  (paginated, page-capped), asks the database which have rows under
  `service_role`, and deletes only the positively row-less ones; failures
  are logged, never guessed through
- WCAG AA contrast across both modes (2026-07-17 audit; every Lighthouse
  a11y deduction was this): `--color-teal` darkened `#468189` → `#427a82`
  (white button text 4.41→4.84:1), small-text `text-primary` usages moved
  to the flipping `text-link` role, alert text rides a new
  `--color-danger-emphasis` token (5.5:1 on the danger tint), and `.input`
  borders ride `--color-border-input` (≥3.6:1, WCAG 1.4.11). Ratios are
  now CI-enforced by `internal/view/tokens_contrast_test.go`.

### Added
- Landing architecture explainer (ADR-024 surface 1, #67): the home page
  now walks one request through the stack in five anchored nodes — Chi
  router + middleware, handler, repository/sqlc/RLS, templ/HTMX/Alpine,
  performance budgets — each with prose, a native `<details>` source peek
  of the real file (no JS), its ADR link, and a hand-off to the quiz topic
  it feeds; plus a live budgets grid rendered from
  `internal/performance/budgets.go` constants on every request. The hero
  and surface directory stay as the navigational skeleton
- Profile page build-out (#97): the account email is shown read-only from
  the authenticated GoTrue user (guests see an upgrade prompt instead), a
  change-password section reuses the reset machinery — an authenticated
  `UpdateUser` on the session token, no current-password step, guests
  refused — behind the strict credential rate tier at
  `POST /profile/password`, and a sign-out form lives on the page itself
  instead of only in the nav menu. Works as plain forms without JS
- `task check:generated` (`scripts/checkgenerated`, in `task ci`): regenerates
  sqlc and templ output into a scratch copy — never the working tree — and
  diffs it against the committed code, flagging modified, missing, and stale
  generated files; warn-only until graduated per ADR-033 §4 (#90). CI now
  installs sqlc and no longer regenerates templ before the gate (that step
  was hiding exactly this drift)
- `adr015-no-hardcoded-secrets` in the ADR check suite (`task check:adr`,
  warn-only): a minimal high-signal secret scanner over shipped source and
  config — AWS/GitHub/Slack/Google/Stripe key shapes, private-key blocks
  and JWT literals everywhere (tests included), plus secret-named
  assignments to opaque literals outside tests with placeholders excused.
  Go-native inside adrcheck per ADR-033 §5; gitleaks stays the escalation
  if the graduation log shows gaps (#91). The suite's walkers now also skip
  `.claude/` (agent worktrees were tripping `adr025-deploy-scope`)
- Password reset flow (#71): request page (`/auth/recover`, anti-enumeration
  generic response), server-side `token_hash` verification via direct GoTrue
  REST (`/auth/reset` — no client JS; requires the Reset Password email
  template to link `{{ .SiteURL }}/auth/reset?token_hash={{ .TokenHash }}&type=recovery`,
  see README), update-password form on the recovery session, and the restored
  "Forgot your password?" link on the login card
- ADR enforcement architecture (ADR-033): `## Enforcement` sections on
  all 34 ADRs (the 33 pre-existing plus ADR-033 itself), a warn-only check suite (`scripts/adrcheck`, ten checks,
  wired into `task ci` as `check:adr`), per-check graduation via
  `checks/enforcement.config.json`, and two blocking hooks — a
  Stop-gate (tests + suite on agent turn completion) and a PreToolUse
  guard denying hand-edits to existing ADRs, `AGENTS.md`, and
  sqlc/templ-generated code (`scripts/adrguard`)
- Dashboard progress widgets (#69): `/dashboard` now rides the `/learn`
  identity chain (GuestSession → OptionalAuth → OptionalUserLoader) and
  hosts two skeleton slots that load over HTMX on page load —
  `/dashboard/widgets/quiz` (correct/attempts/accuracy from
  `CountCorrectByUser` + `ListAttemptsByUser`, its first production caller,
  plus the five most recent attempts) and `/dashboard/widgets/flashcards`
  (cards to review / known / total). Signed-out visitors get the
  browse-first teaser; with auth disabled the shell stays routed so the nav
  link never 404s. The pre-pivot "Create Project" empty state is gone

## [0.8.0] - 2026-07-12

### Changed
- The 19 showcase patterns are grouped into five teaching categories
  (Fetch & swap, Search & lists, Forms & actions, Server-driven UX,
  Alpine islands) with a sticky scroll-spied sidebar TOC on desktop and
  grouped pill navigation on mobile; every pattern keeps its deep-link
  anchor

## [0.7.0] - 2026-07-12

### Added
- **Seven new showcase patterns** (12 -> 19): polling, out-of-band swap,
  confirm + disabled button, View Transitions, loading states done right,
  Alpine modal (x-teleport), and Alpine global store

### Fixed
- **The screen flash on every demo click**: the global full-screen loading
  overlay toggled on every HTMX request — removed in favor of per-element
  feedback (`.htmx-request` styling, `hx-indicator`, `hx-disabled-elt`),
  which the new loading-states pattern now teaches
- Alpine `alpine:init` registrations never ran: app.js loaded after Alpine,
  which fires the event the moment its deferred script executes; app.js now
  loads in `<head>` before Alpine

## [0.6.0] - 2026-07-12

Guest mode goes live and the view layer gets one design language.

### Added
- **Guest mode enabled** (`GUEST_MODE_ENABLED = "true"` in fly.toml):
  anonymous sign-ins are on in Supabase, so visitors to /learn get a real
  anonymous identity server-side — quiz and flashcards with zero signup
  friction, RLS-scoped, TTL-reaped (ADR-024 complete)
- **ADR-029 role-based design tokens**, mirroring the astro starter's
  ADR-047: one token source in `input.css`, dark mode flips role variables,
  components never write `dark:` color variants or raw grays — enforced by
  a templ-source scan in `task ci`

### Changed
- Toasts redesigned to surface + status border (readable in both modes);
  status feedback everywhere is tint + role text per ADR-029 §4
- Auth forms rebuilt on the shared `.input`/`.btn` components with the
  double-spacing collapsed and labels de-bolded

### Removed
- `semantic-colors.css` (was loaded twice) and the stale committed
  `output.css`; the fake `primary-500/600` scale — CSS shrinks to 7.3KB
  gzipped

### Fixed
- An explicit light-mode choice now overrides a dark OS preference (the
  old init ORed the stored choice with `matchMedia`, so light never won)
- Dead `Reset password` link removed from the login card
- `HX-Trigger` toast text kept ASCII — HTTP headers are latin-1, so the
  em-dash rendered as mojibake
- Focus-visible rings on home directory cards and the patterns pill nav

## [0.5.2] - 2026-07-12

### Fixed
- Static assets are cached for a year but shipped unversioned URLs, so
  every deploy stranded returning browsers on stale CSS/JS (the cause of
  three separate misrenders during the 0.5.x design work). Asset references
  now carry a `?v=` stamp — the build version in releases, a process-start
  stamp in dev — implementing the versioned-asset assumption ADR-016 was
  built on

## [0.5.1] - 2026-07-12

### Fixed
- Signing up appeared to do nothing: the auth handlers sent the toast as a
  JSON envelope the layout listener doesn't parse (rendering raw JSON in a
  success-green toast), returned an empty body, and HTMX skips swapping 4xx
  responses anyway. Auth submits now return a plain-message toast with the
  level in `HX-Toast-Type` plus a persistent inline alert swapped into
  `#auth-messages`, and `app.js` enables swaps for 400/401/409/422 — which
  also un-breaks the quiz answer form's 422 re-render

## [0.5.0] - 2026-07-12

Identity for the project and a browse-first front door: an original brand
mark, a real landing page, a proper auth card, and previews that explain the
sign-in instead of demanding it.

### Added
- **Original brand mark** — a lightning bolt between angle brackets (fast
  hypermedia, server-rendered) as a `currentColor` templ component; bolt-on-
  teal favicon (SVG + PNG). Brand rules pinned by test: lockup in the header
  (mark-only on phones), mark + wordmark in the hero, wordmark in the footer,
  and never on functional controls
- **Home landing page**: hero with tagline and CTAs plus a four-card
  directory of the demo surfaces — the navigational spine until the full
  ADR-024 explainer lands
- **Signed-out teasers** for `/learn/quiz` and `/learn/flashcards`: new
  `OptionalAuth`/`OptionalUserLoader` middlewares pass anonymous GETs
  through so the pages can preview what's inside and why it needs an
  identity (RLS-scoped per-user rows) with sign-in CTAs; mutations still
  redirect

### Changed
- Auth page is a single tabbed card (login default, signup one tab away);
  the active tab is server-rendered via `?mode` links so it works without
  JS, with Alpine upgrading the switch
- Dark-mode toggle wears sun/moon icons reflecting the current mode — it
  previously dressed the brand emblem in a pill, reading as a second logo
- Redundant "Home" nav item removed (the brand lockup is the home link)

### Fixed
- The auth forms posted into `#auth-messages`, which didn't exist; the
  region now lives in the card
- The form-validation stub is retired from the home page (the profile form
  is the server-validation reference)

### Removed
- The Pezza emblem assets — that mark belongs to a different brand

## [0.4.1] - 2026-07-12

### Fixed
- Release pipeline deploy step: Fly cannot pull private GHCR images, so
  `flyctl deploy --image ghcr.io/...` failed with "Authentication required"
  (v0.3.0 and v0.4.0 both died here; v0.3.1's jobs skipped). The deploy job
  now mirrors the budget-checked image into `registry.fly.io` and deploys
  that — works with private packages and on forks; GHCR remains the
  published artifact

## [0.4.0] - 2026-07-12

The ADR-024 demo application, live end to end: the visible product finally
exercises the stack the starter exists to prove — plus the design-review
fixes that made the client layer actually work in a browser.

### Added
- **`/patterns` showcase** (ADR-024 surface 2): twelve HTMX/Alpine patterns,
  each a live demo panel with tabbed templ|handler source; self-contained
  stub endpoints under `/patterns/api`, no database or auth required
- **Architecture quiz** at `/learn/quiz` (ADR-024 surface 3): questions
  served through the RLS-scoped `QuizRepository`, running score, progressive
  enhancement (full no-JS flow, HTMX card swaps when available)
- **Flashcards** at `/learn/flashcards`: wrong quiz answers offer a
  save-as-flashcard; review with flip, mark-known, and idempotent delete —
  every row RLS-scoped to its owner
- Migration `000007` seeds ten quiz questions, two per explainer topic,
  idempotent by slug
- Primary navigation to the demo surfaces, and a mobile disclosure menu —
  small screens previously had no navigation at all
- **ADR-028**: CSP compatibility with Alpine — `script-src` gains
  `'unsafe-eval'` (Alpine 3's expression engine requires it; every Alpine
  behavior failed silently before); inline scripts remain forbidden and a
  server test now pins that

### Changed
- README repositioned: leads with agent-assisted development and proven RLS
  multi-tenancy; new "Supabase Is a Committed Bet" and "What's Load-Bearing
  vs. Removable" sections (pointer added to ADR-019)
- Unauthenticated browser navigation to protected pages redirects to the
  login page instead of a plain-text 401 (HTMX keeps 401 + `HX-Redirect`)
- Header brand corrected to "Go Performance Starter"

### Removed
- The pre-ADR-024 stubs: the in-memory Items demo (handlers, map store,
  views) and the hardcoded `/api` JSON handlers ("Stub User", org1/org2) —
  a routing test pins `/items` and `/api/*` as retired

### Fixed
- `[x-cloak]` CSS rule added — the attribute was used but never defined, so
  hidden panels flashed on load
- Base layout's inline scripts moved to `app.js` (they were CSP-blocked)

## [0.3.1] - 2026-07-11

First release deployed against hosted Supabase: security-audit fixes, the
migration bug that blocked production, and the handler/repository test-coverage
gaps closed.

### Security
- Client IP resolution now honors only trusted proxies (ADR-027,
  `TRUSTED_PROXY_CIDRS`; default trusts none) so rate limits can't be bypassed
  with forged `X-Forwarded-For` headers
- Request bodies capped before handlers read them (`MAX_REQUEST_BODY_BYTES`,
  1 MiB default)
- Cookie `Secure` flags made consistent across auth middleware and logout
  (edge-TLS aware per ADR-025 instead of `r.TLS`)
- Form-validation `x-data` JSON-escaped, closing a latent XSS path in the
  shared form component
- 5xx JSON error bodies genericized so internal error strings never reach
  clients
- Go toolchain bumped to 1.26.5 (GO-2026-5856, `crypto/tls`); release workflow
  actions pinned to commit SHAs

### Fixed
- Migration `000002` created its RLS helper function in the Supabase-reserved
  `auth` schema, which hosted Supabase rejects (`permission denied`); moved to
  `public`. CI's vanilla-Postgres auth stub owns that schema, which masked the
  failure until the first production migration run

### Added
- Handler tests for the auth flow (login/signup/logout against an `httptest`
  fake GoTrue), profile, and first-run onboarding; integration tests for the
  organization and organization-member repositories

## [0.3.0] - 2026-07-05

Deployment-readiness release: the 2026-07-05 audit's findings implemented end
to end — decided deployment topology, security hardening, runtime RLS
enforcement, unified logging, guest-mode backend, and a release pipeline.

### Added
- **ADR-025 Deployment Target**: stateless container behind the Cloudflare
  proxy (Fly.io worked example, `fly.toml` at repo root); edge-terminated TLS;
  stateless JWT-cookie sessions; Supabase-delegated backups; forward-only
  pre-deploy migrations
- **ADR-026 Logging Standardization**: stdlib `log/slog` everywhere
- **CSRF protection** (ADR-014 §3): double-submit-cookie middleware, token via
  `hx-headers` on `<body>` plus hidden-field fallback in all POST forms
- **Runtime RLS enforcement** (ADR-004): every repository call carries the
  requester's JWT identity onto the connection (`SET LOCAL ROLE` +
  `request.jwt.claims`); CI integration tests prove cross-user isolation
  through the production path
- **Anonymous guest-mode backend** (ADR-024): server-side GoTrue anonymous
  sign-in, `is_anonymous` claim plumbing, `GuestSession` middleware, TTL
  reaper with GoTrue-side cleanup (`GUEST_MODE_ENABLED`, `GUEST_TTL`,
  `REAPER_INTERVAL`)
- **User provisioning**: `UserLoader` middleware JIT-creates the `users` row
  on first authenticated request (RLS `WITH CHECK` proves ownership); fixes
  the first-run onboarding flow
- Tiered rate limiting: 5/min per IP on login/signup atop the global limiter
- HTTP server timeouts (read/write/idle/read-header); env-tunable pgxpool
  (`DB_MAX_CONNS` etc.); `config.Validate()` fail-fast at boot
- `/metrics` guard: bearer `METRICS_TOKEN`, hidden in production when unset
- HSTS emitted when `ENV=production`; gzip compression middleware
- Release workflow: ghcr.io image publish with a 30MB image-budget gate,
  migrations applied before deploy, secret-gated Fly.io deploy
- DB migration workflow now validates the full chain against a fresh Postgres
  before touching production (and actually triggers — it watched the wrong
  branch)
- Migrations 000005 (rescope `service_role_bypass` for pre-fix deployments)
  and 000006 (`users.is_anonymous`)

### Changed
- ADR-024 accepted (demo direction + anonymous sign-in mechanism); ADR-001/013
  amended per ADR-025/026; ADR-014's OWASP table corrected to audited reality
- Logging: env-driven setup — JSON in production, `LOG_LEVEL` (default info);
  auth logs no longer contain email addresses (ADR-014 §7)
- Go toolchain pinned to a patched release; pgx upgraded (govulncheck clean)

### Removed
- zerolog dependency; dead `profile_handler.go`; stub `user_postgres.go`
  (nil-returning methods and a hand-written SQL string)

## [0.2.0] - 2026-04-05

### Added
- Type-safe templ component library: buttons, cards, forms, inputs, alerts,
  accessibility helpers — all with typed props structs (ADR-017)
- ADR-017 documenting templ adoption rationale and migration strategy
- `UserName` field on `BaseProps` — user menu now shows the authenticated user's
  display name (falls back to email, then "User"); guest pages show "Sign In" link
- `lint:install` Taskfile task — auto-installs golangci-lint for local dev parity
  with CI
- HTMX response helpers (`SetHXTrigger`, `SetHXRedirect`) consolidated into `view`
  package alongside `IsHTMXRequest`

### Changed
- **Complete templ migration** — all pages and partials converted from `html/template`
  to [templ](https://templ.guide) type-safe templates
  - Pages: home, dashboard, terms, privacy, logout, auth (login/signup), profile, items
  - Partials: profile_form, items_list, item (optimistic UI), first_run onboarding,
    empty_state
  - All template data is now typed props structs instead of `map[string]interface{}`
  - HTMX partial responses render fragments directly (no layout wrapper)
- **Tailwind CSS 4 upgrade** — removed `postcss.config.js` and `tailwind.config.js`
  in favor of CSS-based `@import "tailwindcss"` configuration
- Semantic color system updated to Tailwind 4 `@theme` syntax
- CI workflow and Dockerfile updated for Tailwind 4 build process
- `lint` task now auto-installs golangci-lint if missing

### Removed
- `web/templates/` directory — all legacy `.html` template files
- `webutil.RenderTemplate`, `RenderTemplateWithErrors`, `FormErrors`,
  `view.TemplateFuncs()` — no longer needed with templ
- Unused component templates that were defined but never invoked
- `postcss.config.js` and `tailwind.config.js` (replaced by CSS-based config)
- `webutil/htmx_helpers.go` — HTMX helpers consolidated into `view` package
- Unused HTMX helpers: `GetHTMXRequest`, `AddHTMXResponseHeaders`,
  `SetHXTriggerAfterSwap`, `SetHXTriggerAfterSettle`

### Fixed
- Hardcoded "John Doe" in user menu, profile page, and API stub replaced with
  dynamic user data from auth context (closes #3)

## [0.1.0] - 2025-04-04

### Added
- Go (Chi) server with graceful shutdown and signal handling
- Supabase authentication integration (login, signup, logout)
- HTMX + Alpine.js frontend with dark mode support
- Tailwind CSS with semantic color system
- PostgreSQL database layer via pgx and sqlc
- Prometheus metrics and performance budget tracking
- Optimistic UI for item favorite toggle
- HTMX items list with pagination and typeahead
- Profile page with HTMX form submission
- Health check endpoint (`/healthz`)
- Dockerfile with multi-stage build (frontend + Go + minimal Alpine runtime)
- GitHub Actions workflow for Supabase DB migrations
- Database migrations via golang-migrate
- Hot-reload development with Air
- Taskfile for common operations (build, test, lint, migrate, css)
- golangci-lint configuration
- Performance budget tests and binary size checks

### Fixed
- `envconfig` struct tags corrected (`env:` → `envconfig:`) — config loading
  no longer relies on manual `os.Getenv` fallbacks
- Version variable (`main.version`) now declared in `main.go` so `-ldflags -X`
  injection works correctly
- Build commands in Taskfile now include `-ldflags` version injection

[Unreleased]: https://github.com/clownware/go-performance-starter/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/clownware/go-performance-starter/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/clownware/go-performance-starter/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/clownware/go-performance-starter/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/clownware/go-performance-starter/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/clownware/go-performance-starter/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/clownware/go-performance-starter/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/clownware/go-performance-starter/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/clownware/go-performance-starter/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/clownware/go-performance-starter/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/clownware/go-performance-starter/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/clownware/go-performance-starter/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/clownware/go-performance-starter/releases/tag/v0.1.0
