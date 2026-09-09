# Stack

Technology facts. This file updates when dependencies change or commands move; rules elsewhere are stable.

## Versions

- **Language:** Go 1.26 (`go.mod`)
- **Router:** Chi v5 (stdlib-aligned, lightweight)
- **Templating:** templ (type-safe, compiles to Go) — replaces `html/template` (ADR-017)
- **Frontend:** HTMX + Alpine.js (minimal JS), Tailwind CSS v4 (`@tailwindcss/cli`)
- **Database:** PostgreSQL via pgx/v5 + pgxpool; sqlc for type-safe queries; golang-migrate for migrations
- **Auth:** Supabase (gotrue) — JWT validation server-side; auth is optional/disabled if unconfigured
- **Observability:** Prometheus (`client_golang`) + structured logging via stdlib `log/slog` (ADR-026; JSON in production, `LOG_LEVEL` env)
- **Task runner:** Taskfile (`taskfile.dev`)
- **Node:** 20+ (Tailwind CLI only)

## Key Commands

```bash
task dev               # hot-reload dev server (air); watches .go, .templ
task ci                # quality gate: fmt + lint + test(-race -cover) + agents:check + versions:check + check:adr + check:generated + binary-size + asset-budgets + vuln
task test              # go test ./...
task test:coverage     # coverage report (HTML)
task lint              # golangci-lint
task fmt               # gofmt
task templ:generate    # regenerate *_templ.go from .templ
task db:generate       # regenerate internal/database from sql/ via sqlc
task db:migrate:up     # apply migrations
task css:build         # build Tailwind CSS
task test:performance  # performance budget tests + binary size
task scan:vuln         # govulncheck
task agents:build      # regenerate AGENTS.md from CLAUDE.md + .claude/*.md
task agents:check      # CI gate: fail if AGENTS.md drifts from sources
task versions:check    # CI gate: fail if versions.json drifts from repo pins (ADR-030)
task versions:sync     # rewrite versions.json from the repo pins after a dependency/toolchain bump (ADR-030)
task check:generated   # CI gate (warn-only): sqlc/templ output regenerated into a scratch copy must equal the committed code (ADR-003/017, #90)
task demo:seed         # load demo fixtures (refuses without DEMO_MODE=1, ADR-031)
task demo:reset        # purge guests' demo content + re-seed (refuses without DEMO_MODE=1)
```

## Performance Budgets (ADR-000)

| Metric | Budget |
|---|---|
| P95 response time | < 100ms |
| P99 response time | < 200ms |
| Binary size (linux production, stripped) | < 20MB |
| Docker image | < 30MB |
| Memory (steady state) | < 128MB |
| Startup time | < 500ms |
| JavaScript (gzip) | < 50KB |
| CSS (gzip) | < 30KB |

Budgets are enforced in CI via `task ci`: the `go test` leg runs the `internal/performance` budget tests, and `test:binary-size` / `test:asset-budgets` gate binary and gzipped JS/CSS sizes (`task test:performance` bundles the same checks for local runs). The 20MB binary budget targets the stripped linux build (`-ldflags="-s -w"`), not local debug builds.

## Key ADRs

18+ ADRs in `docs/adr/`. The structurally important ones:

- **ADR-000:** Performance budgets and quality attributes
- **ADR-001:** Foundation (Go, Chi, logging)
- **ADR-003:** sqlc + repository pattern for data access
- **ADR-007 / ADR-012:** Frontend stack (HTMX + Alpine + Tailwind), routing & UI patterns
- **ADR-015:** Configuration via environment (twelve-factor)
- **ADR-017:** templ adoption (supersedes ADR-008)
- **ADR-018:** Layered AI constitution (this file structure)
- **ADR-019–022:** Scope boundary, agent roles, quality gate, cross-tool AGENTS.md spine
- **ADR-023:** Testing philosophy
- **ADR-024:** Demo application direction (explainer + /patterns + quiz/flashcards, anonymous guest auth)
- **ADR-025:** Deployment target (stateless container behind Cloudflare; supersedes ADR-001 §5)
- **ADR-026:** Logging standardized on `log/slog` (supersedes ADR-001 §3)
- **ADR-030:** `versions.json` public manifest — CI-checked against repo pins, `template` stamped by release
- **ADR-031:** Public demo operations (deploy-on-merge, `DEMO_MODE`-gated seed/reset, nightly reset workflow)

## Deployment

- **Default port:** `4000` (`HTTP_PORT`, see `internal/config/config.go`)
- **Build:** `task build` → `./dist/app` (stripped). Multi-stage Dockerfile (Node→Tailwind, Go→templ+build, Alpine runtime).
- **Release:** pushing a `v*` tag runs `.github/workflows/release.yml` — ghcr.io image (30MB budget gate), `versions.json` template stamp (ADR-030), migrations before deploy, secret-gated Fly deploy. `fly.toml` at repo root is the ADR-025 worked example. `db-migrate.yml` validates migrations on a fresh Postgres before applying to production.
- **Continuous demo deploy (ADR-031):** merges to the default branch run `.github/workflows/deploy.yml` (secret-gated on `FLY_API_TOKEN` via a gate job; skips cleanly on clones). `demo-reset.yml` resets demo data nightly, double-gated on the `SUPABASE_DATABASE_URL` secret and the `DEMO_MODE=1` repo variable.
- **Target (ADR-025):** single stateless container on a container host (Fly.io as the worked example) behind the Cloudflare proxy, which terminates TLS and serves as CDN. Cloudflare Workers is not an application runtime. Sessions are stateless (JWT cookies); backups are delegated to Supabase managed Postgres; production migrations are forward-only and run before deploy.

## Cross-tool spine

The cross-tool agent context lives in [`AGENTS.md`](../AGENTS.md) at the repo root. It is generated from `CLAUDE.md` plus `.claude/engineering.md`, `.claude/workflow.md`, and this file via `task agents:build`. CI fails if `AGENTS.md` drifts from its sources (see [ADR-022](../docs/adr/ADR-022-Cross-Tool-Agents-Spine.md)). Do not edit `AGENTS.md` directly; edit the source layer instead.
