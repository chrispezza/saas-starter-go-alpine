# Go Performance Starter

A Go + HTMX SaaS starter built for **agent-assisted development** — a layered AI constitution and a halt-on-violation CI gate hold coding agents to the same rules as humans — with **multi-tenancy proven end-to-end**: Postgres Row Level Security scoped by real Supabase identities, exercised by the demo app itself rather than promised in a diagram.

**Stack:** Go (Chi) | templ | HTMX + Alpine.js | Tailwind CSS | Supabase (Auth + PostgreSQL) | Cloudflare

**Why this starter:** the bet is server-rendered Go with a minimal-JS frontend, where performance budgets and AI-agent guardrails are enforced in CI rather than aspired to in a README. It's a deliberately narrow, opinionated foundation — fewer choices, proven defaults — not a framework buffet.

## What You Get

- **Authentication** -- Supabase email/password auth with server-side JWT validation, plus anonymous guest sign-in (server-side GoTrue) so demo visitors get a real identity with zero signup friction ([ADR-024](docs/adr/ADR-024-Demo-Application-Direction.md))
- **A demo that proves the stack** -- a [`/patterns`](docs/adr/ADR-024-Demo-Application-Direction.md) showcase of every HTMX/Alpine pattern the starter supports (live demo + source per pattern), and an architecture quiz whose wrong answers become saveable, per-user flashcards — real rows behind RLS, not an in-memory stub
- **Row Level Security** -- PostgreSQL RLS policies enforced at the database layer and integration-tested; request JWT claims ride into every query via a scoped transaction ([ADR-004](docs/adr/ADR-004-Authorization-Strategy-RLS.md))
- **Type-safe templates** -- templ compiles HTML to Go; typed props, no `map[string]interface{}` (see [ADR-017](docs/adr/ADR-017-Templ-Adoption.md))
- **Type-safe SQL** -- sqlc code generation with repository pattern
- **Performance budgets** -- CI-enforced binary size, gzipped asset budgets, and memory limits
- **Role-based design system** -- semantic tokens (`bg-surface`, `text-muted-foreground`, ...) with dark mode flipping tokens instead of components, CI-enforced against raw grays and `dark:` drift; restyle the whole app from one `@theme` block ([docs/design-system.md](docs/design-system.md), [ADR-029](docs/adr/ADR-029-Role-Based-Design-Tokens.md))
- **Agentic discipline** -- a layered AI constitution and halt-on-violation quality gate (see below)
- **Observability** -- Prometheus metrics, structured logging (log/slog), health checks
- **Developer experience** -- Hot reload (air), Taskfile automation, golangci-lint, CI/CD

## Supabase Is a Committed Bet

This starter does **not** treat auth and data as pluggable adapters. Supabase (GoTrue + Postgres + RLS) is load-bearing by design:

- The auth middleware validates Supabase JWTs and carries the claims into every database transaction (`SET LOCAL ROLE` + `request.jwt.claims`), so `auth.uid()` resolves inside RLS policies — the repository layer physically cannot skip tenant scoping.
- Guest mode issues **real anonymous Supabase identities** server-side; the same `users_self_access` policy covers guests and registered users with no parallel code path.
- The TTL reaper uses the Supabase admin API to expire inactive guests.

If you want vendor-neutral auth, this is the wrong starter — swapping Supabase means rewriting the auth middleware, the RLS scope helper, and the policies. What you get for the lock-in is multi-tenancy that is proven by integration tests and exercised by the live demo, not asserted.

## What's Load-Bearing vs. Removable

Forking this for your own product? The governance apparatus is modular ([ADR-019](docs/adr/ADR-019-Template-Scope-Boundary.md)):

| Piece | Verdict | Notes |
|---|---|---|
| `task ci` quality gate | **Load-bearing** | CI invokes it; the budgets, lint, race tests, and drift checks all hang off it. Removing gates means editing `Taskfile.yml`, not the workflow. |
| sqlc/templ codegen + repository pattern | **Load-bearing** | The RLS scoping lives in the repository layer; handlers depend on generated types. |
| Performance budgets | **Tunable** | Numbers live in `internal/performance/` and `scripts/`; raise or lower them in one place ([ADR-000](docs/adr/ADR-000-Performance-Budgets-and-Quality-Attributes.md) classifies which are enforced vs. aspirational). |
| Layered AI constitution (`CLAUDE.md`, `.claude/`) | **Removable** | Only matters if you develop with agents. Delete it and nothing in the app breaks. |
| `AGENTS.md` cross-tool spine | **Removable with the constitution** | Generated via `task agents:build`; drop the drift check from `Taskfile.yml` if you remove it. |
| Three-pass Architect→Coder→Reviewer workflow | **Removable** | A process convention ([ADR-020](docs/adr/ADR-020-Agent-Roles.md)), not code. |
| Demo surfaces (`/patterns`, `/learn/*`) | **Replaceable** | They exist to prove the stack; swap them for your domain. The quiz/flashcard handlers are the reference implementation for RLS-scoped CRUD. |

## Prerequisites

- Go 1.26+
- Docker & Docker Compose (for local development database)
- [Task](https://taskfile.dev) (task runner)
- Node.js 20+ (for Tailwind CSS build)

## Quick Start

```bash
git clone https://github.com/clownware/go-performance-starter.git
cd go-performance-starter

cp .env.example .env
# Edit .env with your Supabase credentials and DATABASE_URL

task db:up              # Start local Postgres
task db:migrate:up      # Run migrations
task db:generate        # Generate sqlc types
go mod tidy             # Install Go dependencies
npm install             # Install Tailwind tooling
task dev                # Start dev server with hot reload
```

The application runs at [http://localhost:4000](http://localhost:4000) by default (`HTTP_PORT`).

**Password reset (one-time Supabase config):** the reset flow verifies the
email link's `token_hash` server-side, so the *Reset Password* email template
must link to this app instead of GoTrue's redirect flow. In the Supabase
dashboard → Authentication → Email Templates → Reset Password, set the link to:

```
{{ .SiteURL }}/auth/reset?token_hash={{ .TokenHash }}&type=recovery
```

(Site URL must point at your deployment — it already does if signup
confirmation emails work. No redirect-URL allowlist entry is needed; the link
goes straight to this app, which exchanges the hash via `POST /auth/v1/verify`.)

Making this template your own (module rename, deploy identity, branding)? Follow the [personalization guide](docs/personalization-guide.md) — ~30 minutes of required changes.

## Available Tasks

Run `task --list` to see all available tasks. Key ones:

| Task | Description |
|------|-------------|
| `task dev` | Start dev server with hot reload |
| `task ci` | Halt-on-violation quality gate (fmt, lint, race tests, agent-spine + versions drift, binary size, vuln scan) |
| `task build` | Compile optimized binary to `./dist/app` |
| `task test` | Run test suite (`task test:coverage` for coverage) |
| `task test:performance` | Check performance budgets |
| `task test:binary-size` | Validate binary size < 20MB |
| `task lint` | Run golangci-lint |
| `task css:build` | Build Tailwind CSS |
| `task docker:build` | Build production Docker image |
| `task scan:vuln` | Run govulncheck |

## Project Structure

```
cmd/api/              Entry point
internal/
  auth/               Supabase auth client
  cache/              In-memory TTL cache
  config/             Environment-based configuration
  database/           sqlc-generated types and queries (generated)
  handler/            HTTP handlers
  jobs/               Background jobs (guest TTL reaper)
  middleware/         Auth, metrics, logging, request ID
  performance/        Performance budget definitions
  repository/         Data access interfaces + implementations
  server/             Router setup and middleware stack
  validate/           Input validation helpers
  view/               templ UI: layouts/, pages/, partials/, components/ (+ render, props)
  webutil/            HTMX + context helpers
web/
  static/             CSS, JS, images
migrations/           golang-migrate SQL files
sql/                  sqlc query and schema definitions
docs/                 ADRs, implementation guides, product docs
.claude/              Layered AI constitution (engineering, workflow, stack, roles, skills, agents)
```

## Agentic Discipline

This starter is built to be developed with AI coding agents — and it holds the agent to the same rules you follow. The discipline is operationalized, not aspirational:

- **Layered AI constitution.** [`CLAUDE.md`](CLAUDE.md) holds ~10 halt-on-violation rules; [`.claude/engineering.md`](.claude/engineering.md), [`.claude/workflow.md`](.claude/workflow.md), and [`.claude/stack.md`](.claude/stack.md) carry engineering defaults, process, and ephemeral stack facts ([ADR-018](docs/adr/ADR-018-Layered-AI-Constitution.md)).
- **Cross-tool spine.** [`AGENTS.md`](AGENTS.md) is **generated** from those layers via `task agents:build` and read natively by Cursor, Copilot, Codex, Windsurf, and others. CI fails if it drifts from its sources ([ADR-022](docs/adr/ADR-022-Cross-Tool-Agents-Spine.md)).
- **Role-separated workflow.** Non-trivial features run a three-pass Architect → Coder → Reviewer flow, each pass producing an ADR, a failing test, or a review ([ADR-020](docs/adr/ADR-020-Agent-Roles.md)).
- **Halt-on-violation gate.** `task ci` is the single definition of "done." An agent must clear it before claiming a change complete — no lowering thresholds, no `--no-verify` ([ADR-021](docs/adr/ADR-021-Halt-On-Violation-Quality-Gate.md)).
- **ADR enforcement.** Every ADR carries an `## Enforcement` section mapping its rules to checks — or naming honestly what no machine can check ([ADR-033](docs/adr/ADR-033-ADR-Enforcement-Architecture.md)).

### ADR Enforcement

`task check:adr` runs a deterministic suite (`scripts/adrcheck`, part of `task ci`) that verifies the testable consequences declared in each ADR's Enforcement section — including a minimal high-signal secret scanner (`adr015-no-hardcoded-secrets`) for the ADR-014/015 "no hardcoded secrets" rule. Checks start life as **warn** — they report but never fail the build — and are promoted to **block** in [`checks/enforcement.config.json`](checks/enforcement.config.json) only after 7+ clean days or one real catch, with the promotion logged in the owning ADR's graduation log and the CHANGELOG. Demotion back to warn is always allowed, same trail. Every failure message names the ADR, the testable consequence, and the remedy; `--json` gives machine-readable output.

Two hooks are the only blocking layer: a **Stop-gate** (agents can't finish a turn with failing tests or a BLOCKER; kill-switch `STOP_GATE_OFF=1`) and a **PreToolUse guard** (agents can't hand-edit existing ADRs, `AGENTS.md`, or sqlc/templ-generated code; kill-switch `ADR_GUARD_OFF=1`).

**If you're using this template:** you inherit the suite, the config, and both hooks. To prune enforcement entirely, delete `scripts/adrcheck`, `scripts/adrguard`, `checks/`, `.claude/hooks/`, the `check:adr` task, and the `hooks` block in [`.claude/settings.json`](.claude/settings.json) — the rest of the quality gate stands on its own. If you keep it, the checks are plain Go in one file; retarget them at your own ADRs as they diverge.

## versions.json Is a Public Contract

[`versions.json`](versions.json) at the repo root is a machine-readable manifest of what this template ships — its own release version (`template`) plus one key per meaningful stack pin (Go, templ, HTMX, Alpine, Tailwind, sqlc, …). External consumers fetch it raw from the default branch at build time, so treat it as a consumption contract: **adding keys is fine; renaming or removing keys is a breaking change.**

It cannot drift: `task versions:check` (part of `task ci`) fails when any key disagrees with its in-repo source of truth (`go.mod`, `package-lock.json`, vendored JS bundles, sqlc headers, workflow pins, Taskfile tool pins), and the release workflow stamps the `template` field from the git tag ([ADR-030](docs/adr/ADR-030-Versions-Manifest-Contract.md)). After bumping a pin, `task versions:sync` rewrites the manifest from those same sources so the bump stays a one-file edit — the check remains the gate.

## Architecture Decisions

This project uses Architecture Decision Records (ADRs) to document key technical choices. See [docs/adr/](docs/adr/) for the full set, including:

- [ADR-001](docs/adr/ADR-001-Foundation.md) -- Foundation (Go, Chi; logging now log/slog per [ADR-026](docs/adr/ADR-026-Logging-Standardization.md))
- [ADR-000](docs/adr/ADR-000-Performance-Budgets-and-Quality-Attributes.md) -- Performance budgets
- [ADR-007](docs/adr/ADR-007-Frontend-Stack-Selection.md) -- Frontend stack (HTMX + Alpine + Tailwind)
- [ADR-014](docs/adr/ADR-014-Security-Patterns-and-Threat-Model.md) -- Security patterns

## Performance Targets

| Metric | Budget |
|--------|--------|
| P95 response time | < 100ms |
| Binary size | < 20MB |
| Docker image | < 30MB |
| Memory (steady state) | < 128MB |
| Startup time | < 500ms |

These are enforced in CI via `task test:performance` and `task test:binary-size`.

## License

MIT -- see [LICENSE](LICENSE).
