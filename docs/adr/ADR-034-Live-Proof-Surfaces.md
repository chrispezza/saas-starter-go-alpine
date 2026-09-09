# ADR-034: Live Proof Surfaces

**Date**: 2026-09-09

## Status

Accepted

## Context

The demo's brand line is "proved, not promised" (ADR-024), and the surfaces it shipped — the pattern showcase, the architecture tour, the RLS-scoped quiz and flashcards — do prove the *mechanics*. But the two headline claims stopped one step short of proof:

- The landing page's "live performance budgets" grid rendered the ADR-000 **constants** only. A reader saw `< 100ms` and could not tell a ceiling from an observation; nothing on the page distinguished Enforced from Monitored from Aspirational, a classification that existed only inside ADR-000.
- The flashcards page said "a real row scoped to you by Row Level Security" as prose. A visitor could not witness the boundary — the one differentiator every other starter lacks was a sentence.

Meanwhile the process already had everything needed to show both: a request-duration histogram in the metrics middleware, `runtime.MemStats`, a measurable startup path, the shipped assets on disk, and repositories whose scoping comes purely from context claims (ADR-004). And the security tiers ADR-014 mandates — per-IP rate-limit tiers in particular — were invisible by construction; a 429 was plain text with no `Retry-After`.

## Decision

The demo gains three **proof surfaces**, each showing a claim rather than stating it, each built from the production code path it proves (no parallel demo-only mechanism):

1. **Observed-vs-budget grid** (landing page). `internal/performance` gains an `Observer`: a ring of the last 4096 request durations (nearest-rank p50/p95/p99), memory from the runtime with a since-boot high-water mark, the startup duration recorded by `main` once the listener is bound (process start → listening socket, exactly as ADR-000 defines it), the running executable's size, and the gzipped totals of the shipped assets measured from the same file lists the CI gate uses (`ShippedJS`/`ShippedCSS`, stated once). The metrics middleware feeds it. The grid renders **budget, observed, pass/fail, and the ADR-000 class** per row. A zero observation renders as *not measured* — a proof surface must never show a passing zero. The Total Page budget stays visibly Aspirational until a measurement exists.
2. **`Server-Timing` on every response.** The metrics middleware stamps `Server-Timing: app;dur=<ms>` (handler time to first byte) when headers are committed, so any browser's devtools show the server-side cost of the page being read.
3. **RLS isolation check** (flashcards page) and **rate-limit demo** (`/patterns`), specified here and delivered in the two changes stacked on this one:
   - The isolation check runs the visitor's own `ListByUser` query twice through the *same* repository interface: once with the request's claims, once with a freshly minted stranger identity (`authenticated` role, random `sub`) — same SQL, same `user_id` parameter, different `auth.uid()` — and renders both row counts next to the policy text. Zero rows for the stranger is Postgres refusing, not a `WHERE` clause. No other visitor's data is touched or counted.
   - The rate-limit demo mounts the production `RateLimiter` on a `/patterns` fragment endpoint with a deliberately tight tier and lets the visitor hit 429. The limiter now sets `Retry-After` on every 429 (a correctness fix the demo made visible), and the limited response for the demo is an HTML fragment HTMX swaps in — the existing `htmx:beforeSwap` allow-list grows by "429 with an HTML body".

### Observed values are readings, not SLOs

The observed column is per-process, since boot, on whatever instance served the request (on the demo, a single scale-to-zero machine). It is a live reading that makes the budgets concrete, not a production baseline; ADR-000's graduation rule (Monitored → Enforced needs a baseline) is unchanged. The page says so in its legend.

## Consequences

- The landing page now depends on `internal/performance` for observations as well as constants; the observer is package-level (`performance.Default`) like the Prometheus collectors it sits beside, and tests construct their own.
- `main` binds the listener explicitly (`net.Listen` + `Serve`) so startup can be measured; behaviour is otherwise unchanged.
- The asset file lists move from `scripts/check-asset-budgets` into `internal/performance`; the CI gate and the runtime read one list.
- Every response grows one small header. `Server-Timing` exposes handler duration only — no route, identity or infrastructure detail.
- A dev build shows an unstripped, larger executable against the 20 MB budget and may read "over budget" locally; the note on the row says CI gates the stripped linux build. Honest beats flattering.

## Alternatives Considered

- **Read percentiles from the Prometheus histogram.** Rejected — histogram buckets give interpolated estimates, and the page would then depend on the metrics registry's label cardinality. A bounded ring gives exact nearest-rank values over recent traffic for a few kilobytes.
- **A separate "status" page.** Rejected — the proof belongs next to the claim; the landing page already narrates the budgets in tour step 5.
- **Prove isolation by reading another guest's rows under `service_role`.** Rejected — it would put a second identity's data (even a count) on a visitor's page, and it needs a system-role path the request handlers otherwise never touch. The stranger-identity check proves the same thing using only the visitor's own rows.
- **Fake the 429 in a demo handler.** Rejected — the point is that the production limiter refuses; a handler pretending to would be exactly the promise-not-proof this ADR exists to end.

## References

- [ADR-000](ADR-000-Performance-Budgets-and-Quality-Attributes.md) budgets and their classification; [ADR-004](ADR-004-Authorization-Strategy-RLS.md) RLS; [ADR-014](ADR-014-Security-Patterns-and-Threat-Model.md) rate-limit tiers; [ADR-024](ADR-024-Demo-Application-Direction.md) demo direction; [ADR-031](ADR-031-Public-Demo-Operations.md) public demo operations.
- `internal/performance/observed.go`, `internal/middleware/metrics.go`, `internal/handler/home_explainer.go`.

## Enforcement
<!-- added 2026-09-09, see ADR-033 (Enforcement Architecture) -->
- **Testable consequences:**
  - TC-1: Every budget row renders budget, observed value, status and ADR-000 class; an unmeasured value renders as unmeasured, never as a passing zero.
  - TC-2: The CI asset gate and the runtime observer measure the same shipped-asset file lists.
  - TC-3: Every response through the metrics middleware carries `Server-Timing: app;dur=<ms>`.
  - TC-4: The isolation check goes through `repository.FlashcardRepository` only, and the stranger identity yields zero rows against real Postgres RLS.
  - TC-5: Every 429 from `RateLimiter` carries `Retry-After`.
- **Checks:**
  - TC-1 → `internal/handler` `TestPerfBudgetStats_Observed`, `internal/server` `TestServer_HomeExplainer` (status: **block**, `task test`)
  - TC-2 → `internal/performance` `TestShippedAssetPaths` + `scripts/check-asset-budgets` importing the lists (status: **block**, compile + `task test`)
  - TC-3 → `internal/middleware` `TestMetrics_ServerTiming` (status: **block**, `task test`)
  - TC-4 → `internal/handler` isolation-check tests with the fake repository; `internal/repository/postgres` `TestScopedRepoRLSIsolation` stranger case (status: **block**, `task test`; the Postgres leg runs in CI with `DATABASE_URL`)
  - TC-5 → `internal/middleware` `TestRateLimiter_RetryAfter` (status: **block**, `task test`)
- **Not machine-checkable:** that the observed column is *read* as a live reading rather than an SLO — the legend's wording is review territory.
- **Graduation log:** _(empty)_
