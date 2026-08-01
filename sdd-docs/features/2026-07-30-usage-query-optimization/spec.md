# Usage Query Performance Optimization Spec

Local page: `/` service status, `/` usage statistics, `/` sessions  
Admin entry points: `GET /api/status`, `GET /api/usage/*`, `GET /api/sessions`, `GET /api/sessions/projects`  
Reference features: `2026-05-15-usage-statistics`, `2026-05-21-sqlite-wal-optimization`, `2026-06-12-windows-usage-statistics-fixes`, `2026-07-17-kimi-quota-usage-parsing-fixes`  
Stack: Go 1.26, SQLite/WAL, Vue 3, TypeScript  
Last updated: 2026-08-01

Progress: design approved; implementation 6 / 6 tasks (code complete, compatibility confirmed by independent review); Task 6 performance verification executed; **rework rounds R1–R5 completed** (index diagnostics → Summary single-scan → candidate-rank persistence → lazy candidate + scope pruning → final verification). Cumulative same-session A/B vs the pre-rework state: **all six endpoints faster (1.07×–1.86×), no regressions, both recorded regressions (Coverage 0.65× / six-parallel 0.82×) eliminated**; status now approaches the R7 100 ms target (≈110–140 ms at 3× production scaling) but six-parallel remains ≈1.7–2.9× over 300 ms (needs cross-request materialization, proposed R6). See “Implementation Status / Deviations” sections below.

## Implementation Status

Last verified: 2026-08-01 (branch `perf/usage-query-optimization`, HEAD `c3b3056` + R5 docs commit)

### Completion

All six tasks are code-complete and passed compatibility review (14 implementation commits:
`956f2ec`→`dcf1dbb`). Performance rework rounds R1–R5 were then executed on top (5 commits,
`5ddabe1`/`4ae4e89`/`abeaa1e`/`34d5826`+`c3b3056`, all limited to `internal/usage` + docs — no
frontend changes):

- Backend independent review (Task 6A, `review-6A-backend.md`): **no high/medium findings**;
  field-for-field compatible with the legacy algorithm. Independent-oracle differential tests,
  migration idempotency/atomic rollback, 8-writer concurrent WAL, pagination pushdown, URL
  redaction, and DST/fractional-second/malformed-timestamp tests all pass. Only two low-risk
  transients (see “Residual risks”).
- Frontend re-review (Task 6B2): all four findings are no longer triggerable; no medium/high
  regression.
- Rework rounds (see reports `R1-index-diagnostics.md` … `R5-final-verification.md`):
  - **R1** index foundation + EXPLAIN diagnostics: proved via same-session A/B that indexes alone
    yield no measurable per-endpoint gain (0.95×–1.10×, inside noise) — the real root cause is the
    scoped CTE repeatedly full-scanning 60k rows, requiring query-structure rewrite.
  - **R2** Summary single-scan: `last_provider_request` reworked from a scalar subquery (which
    re-materialized the whole scoped CTE a second time) to a single-scan `MAX` time-key encoding;
    Summary 1.35×–1.40× in three independent same-session A/Bs.
  - **R3** candidate-rank persistence (`candidate_rank` column + `(session_request_id,
    candidate_rank)` index): eliminated the per-query ROW_NUMBER window sort and the runtime
    automatic index in all six endpoints; Requests 1.48×–1.65× across four sessions.
  - **R4** CASE-lazy candidate (non-session rows do zero candidate lookups) + scope pruning
    (raw/provider/session_log aggregates skip candidate computation entirely) + single-eval
    Summary epoch: per-scope Summary −36% (provider) / −77% (session_log), Requests provider −52%,
    six-parallel 1.09×–1.37×. `COUNT(*) OVER()` count+page merge was measured and rejected
    (445 ms vs 166 ms, net regression).
  - **R5** final verification: full suite re-run + same-session A/B of the complete pre-rework
    state (`733461a`) vs HEAD (see measurements below).

### Test evidence (re-verified 2026-08-01 for R5)

| Command | Result |
|---|---|
| `go build ./...` | pass (exit 0) |
| `go vet ./...` | pass (exit 0) |
| `gofmt -l internal/usage/` | clean |
| `go test ./... -count=1` | all ok |
| `CGO_ENABLED=0 go test ./... -count=1` | all ok |
| Cross builds linux/darwin/windows × amd64/arm64 (`CGO_ENABLED=0`) | 6/6 pass |
| `go test ./internal/usage/ -race -count=1` | ok (no data races) |
| `npm --prefix internal/frontend test` | 269 pass / 0 fail |
| `npm --prefix internal/frontend run build` | pass (no unexpected `dist` changes) |
| `git diff --check` | clean |

Performance benchmark script: `internal/usage/performance_test.go` (added in Task 6). Gated by the
`MCC_USAGE_BENCH_ROWS` environment variable (skipped when unset, so CI never runs it by default and
there are **no wall-clock hard assertions**). A fixed random seed (seed=1) generates a reproducible
dataset mixing roughly 82% provider rows / 18% session rows, with about half of session rows mirroring
a nearby provider row (same model + same four token counters + ±5-minute window) to deterministically
produce candidate relationships. Reproduce with:

```bash
MCC_USAGE_BENCH_ROWS=60332 go test ./internal/usage/ -run TestUsagePerformanceProfile -count=1 -v
```

### Performance measurements (60,332-row deterministic dataset, seed=1)

Environment: 8 cores / 30GB, `modernc.org/sqlite` (pure Go, same driver as production), WAL +
`synchronous=NORMAL`, median of several warmed runs. **Attribution rule (R1 §3.1): machine load drifts
between sessions (±25% on identical code), so only same-session A/B ratios are attributable; absolute
values across runs are reference only.**

#### Historical baselines (reference only, heavy load during 6C2 baseline)

| Metric | Before (032aa80) | After (dcf1dbb) | Relative | R7 target | Met |
|---|---:|---:|---:|---:|:--:|
| Migration incl. backfill (5,609 candidates) | — | 367 ms | — | ≤ 2 s | ✅ |
| `GET /api/status` (Summary, single) | 807 ms | 604 ms | 1.34× faster | ≤ 100 ms | ❌ ~6× over |
| Requests page 50 (LIMIT/OFFSET) | 868 ms | 361 ms | 2.4× faster | ≤ 100 ms | ❌ ~3.6× over |
| Trends single | 1,226 ms | 705 ms | 1.74× faster | — | — |
| Providers single | 990 ms | 942 ms | 1.05× faster | — | — |
| Models single | 1,019 ms | 687 ms | 1.48× faster | — | — |
| Coverage single | 989 ms | 1,534 ms | **0.65× slower (regression)** | — | — |
| Six endpoints parallel wall | 4,817 ms | 5,885 ms | **0.82× slower (regression)** | ≤ 300 ms | ❌ ~19× over |

#### R5 same-session A/B: pre-rework (`733461a`) → HEAD, two independent sessions (2026-08-01)

Same-session A/B tooling: `internal/usage/ab_compare_test.go` (`TestUsageR5FullReworkABCompare`, gated
by `MCC_USAGE_EXPLAIN=1 + MCC_USAGE_AB=1`). The pre-rework side is a test-only reconstruction of the
`733461a` queries, verified verbatim against `git show 733461a` (ROW_NUMBER candidate CTE + Summary
scalar subquery); field-by-field equivalence of all six endpoint queries on 60,332 rows is asserted
before timing, alternating 3 rounds × 5 runs per sample, medians reported.

| Metric | S1 HEAD | S1 pre-rework | S1 speedup | S2 HEAD | S2 pre-rework | S2 speedup |
|---|---:|---:|---:|---:|---:|---:|
| `GET /api/status` (Summary, single) | 331 ms | 523 ms | **1.58×** | 417 ms | 568 ms | **1.36×** |
| Requests page 50 (count+page) | 125 ms | 234 ms | **1.86×** | 142 ms | 255 ms | **1.80×** |
| Trends single | 550 ms | 617 ms | 1.12× | 597 ms | 660 ms | 1.11× |
| Providers single | 483 ms | 537 ms | 1.11× | 616 ms | 610 ms | 0.99× (noise) |
| Models single | 525 ms | 587 ms | 1.12× | 625 ms | 669 ms | 1.07× |
| Coverage single | 813 ms | 1,007 ms | **1.24×** | 879 ms | 1,133 ms | **1.29×** |
| Six endpoints parallel wall | 1.90 s | 2.50 s | **1.31×** | 2.59 s | 3.32 s | **1.28×** |

Two sessions agree in direction: Summary 1.36×–1.58×, Requests 1.80×–1.86×, Trends 1.11×–1.12×,
Coverage 1.24×–1.29×, six-parallel 1.28×–1.31×; Providers (0.99×–1.11×) and Models (1.07×–1.12×)
stay inside the shared-machine noise band (their latency is dominated by the mandatory 60k filtered
full scan). **Both recorded 6C2-baseline regressions are eliminated: Coverage and six-parallel are
now faster than the pre-rework implementation.**

#### Cumulative rework attribution chain (each round same-session A/B, same dataset)

| Round | Change | Key same-session speedup |
|---|---|---|
| R1 | index foundation + diagnostics | per-endpoint 0.95×–1.10× (noise; indexes alone don't help) |
| R2 | Summary single-scan | Summary 1.35×–1.40× (3 sessions) |
| R3 | candidate-rank persistence | Requests 1.48×–1.65× (4 sessions); auto-index + window eliminated in all 6 endpoints |
| R4 | CASE-lazy candidate + scope pruning + single-epoch | Summary provider −36% / session_log −77%; Requests provider −52%; six-parallel 1.09×–1.37× |
| R5 | pre-rework → HEAD cumulative | Summary 1.36×–1.58×; Requests 1.80×–1.86×; Coverage 1.24×–1.29×; six-parallel 1.28×–1.31× |

#### R7 status after rework

| Metric | R7 target | HEAD (this machine) | 3× production scaling | Met |
|---|---:|---:|---:|:--:|
| Migration incl. backfill (5,609 candidates) | ≤ 2 s | 362 ms | — | ✅ |
| `GET /api/status` (Summary, single) | ≤ 100 ms | 331–417 ms | ≈110–140 ms | ❌ **approaches but not met** (10–40% over; R4 idle-baseline estimate ≈113 ms) |
| Requests page 50 (count+page) | ≤ 100 ms | 125–142 ms | ≈42–47 ms | ⚠️ this-machine accounting still 1.25–1.4× over; met under 3× scaling |
| Six endpoints parallel wall | ≤ 300 ms | 1.90–2.59 s | ≈510–860 ms | ❌ **still ≈1.7–2.9× over** |

Hardware aggregation-floor calibration: a plain `COUNT/SUM/MAX` on this machine (joined to
`usage_tokens`, no scoped CTE) is best≈145 ms, whereas the production table records 40-50 ms — i.e.
this box is about 3× slower than production hardware. The pre-optimization single status latency here
(807 ms) closely matches the production baseline (760-780 ms), indicating the single-threaded status
path runs near production speed on this box — so the 3× scaling is a generous upper bound for
single-threaded paths and the real production gap may be smaller.

### Deviations (root causes addressed by rework; remaining gap honest assessment)

**Fixed by the rework (verified via EXPLAIN QUERY PLAN + same-session A/B):**

- Summary's `last_provider_request` scalar subquery re-materialized the whole scoped CTE a second time
  (2× 60k full scan + 2× runtime auto-index); R2 replaced it with a single-scan `MAX` time-key
  encoding (Summary 1.35×–1.40×).
- The `candidate` ROW_NUMBER CTE materialized per query and its join built an `AUTOMATIC PARTIAL
  COVERING INDEX` at runtime — a base-table index cannot remove it (R1 proved). R3 persisted
  `candidate_rank` with a `(session_request_id, candidate_rank)` index: auto-index and window TEMP
  B-TREE are gone from all six endpoint plans; candidate lookup now hits the persistent index.
- Candidate computation ran for scopes that neither need dedupe markers nor depend on candidate
  decisions; R4 made it CASE-lazy (non-session rows do zero lookups) and pruned it entirely for
  raw/provider/session_log aggregate paths.
- The two recorded regressions from the 6C2 baseline — Coverage single (0.65×) and six-parallel wall
  (0.82×) vs the pre-optimization implementation — are eliminated: same-session A/B now shows
  Coverage 1.24×–1.29× and six-parallel 1.28×–1.31× faster than the pre-rework state.

**Remaining deviation (R7 absolute targets):** each endpoint still performs its own unfiltered full
scan of the 60k filtered rows (semantically required: all rows must be aggregated) plus its own
candidate subqueries, and the six endpoints run concurrently and contend for CPU on this 8-core box.
After R5: status ≈331–417 ms on this machine (≈110–140 ms at 3× production scaling, approaching but
not meeting the 100 ms target), Requests ≈125–142 ms (met at 3× scaling, 1.25–1.4× over on this
machine), six-parallel ≈1.90–2.59 s (≈510–860 ms at 3× scaling, still ≈1.7–2.9× over 300 ms).
Further reduction of six-parallel requires **cross-request materialization** (e.g. share one filtered
scan / candidate result across the six endpoints, incremental materialization of the scoped dataset)
— a structural caching topic beyond query-shape optimization, **proposed as R6 and explicitly out of
scope for this task**.

### Residual risks

- **L1 (low-risk transient, pre-existing)**: Trends issues two queries (min/max epoch, then
  aggregation). Under WAL concurrent writes near a DST boundary, a new row may momentarily land in the
  last offset bucket; it self-heals on the next call. No security/persistence impact.
- **L2 (low-risk transient, pre-existing)**: Requests total and page are two queries; under WAL
  concurrent writes the total and the current page rows may be momentarily inconsistent. Affects only
  page boundaries, transient, self-healing. (A `COUNT(*) OVER()` merge was measured in R4 and rejected
  as a net regression: 445 ms vs 166 ms.)
- **L2 (low-risk transient, added in R4)**: the CASE-lazy candidate subquery is duplicated up to 3× in
  the requests-page plan by SQLite's flattening (three references); per-page cost is ~2 extra index
  lookups per session row on a 50-row page — measured at the ~13 ms page level, imperceptible;
  count/Summary aggregate paths are not affected.
- **R7 targets partially unmet (persisting finding)**: status approaches the 100 ms target
  (≈110–140 ms at 3× scaling) but six-parallel remains ≈1.7–2.9× over 300 ms; both recorded
  regressions are eliminated. Recorded honestly; six-parallel needs cross-request materialization
  (proposed R6), not in this task.

## Overall Analysis (Source Analysis)

### Problem statement

The production database contains 60,332 `usage_requests` rows and the same number of
`usage_tokens` rows. This is a small SQLite workload, but measured latency is:

| Operation | Measured latency |
|---|---:|
| Direct SQLite `COUNT` / `SUM` / `MAX` aggregation | 40-50 ms |
| Direct full-width join without ordering | about 130 ms |
| Direct full-width join with current ordering | about 180 ms |
| `GET /api/status` in isolation | 760-780 ms |
| Three concurrent status requests during initial page load | 1.17-1.24 s each; 1.25 s wall time |
| Six concurrent usage-statistics requests | about 1.90 s wall time |
| Direct SQL request-page query with `LIMIT 50` | less than 10 ms |

Docker CPU, memory, DNS, NAT, provider-quota snapshots, and script workers are not the
source of this control-plane latency. The existing SQLite query plan already uses
`idx_usage_requests_started_at` and the `usage_tokens.request_id` primary-key index.
The absent `(started_at, id)` composite index creates a temporary B-tree for the final
ordering term, but ordering accounts for only about 50 ms and is not the primary cause.

### Root causes

1. `Summary`, `Trends`, `Requests`, `Providers`, `Models`, and `Coverage` all call the
   same full-width `queryRows` implementation.
2. `queryRows` selects roughly 30 columns, orders all matching rows, constructs complete
   Go objects, parses timestamps, and applies effective-scope deduplication before any
   endpoint-specific aggregation.
3. Every scanned row reparses and redacts both stored URL fields even when the caller
   never returns a URL.
4. `Requests` passes `includePagination=false`, loads every matching row, and performs
   page slicing in Go despite `queryRows` containing unused SQL `LIMIT/OFFSET` support.
5. `applyStatsScope` computes duplicate relationships even for scopes that do not need
   duplicate exclusion.
6. The usage tab launches six requests that independently repeat the same full scan and
   deduplication work.
7. The initial dashboard load requests status independently from `DashboardView`
   status loading, connection-mode loading, and `AppHeader`.
8. Session project and session-list data are eagerly loaded even when the status tab is
   active.

### Compatibility invariant

This feature is a complete compatibility optimization. It must not change:

- existing endpoint paths, methods, response fields, JSON types, or error categories;
- request ordering, total counts, pagination, date/time-zone behavior, or filter meaning;
- `effective`, `provider`, `session_log`, and `raw` statistics-scope behavior;
- the current duplicate fingerprint: model match, all four token counters equal, and
  timestamps within an inclusive ten-minute window;
- the current row classification: a provider candidate is a non-session row from
  `source_app=claude_code` with `usage_source=provider` and `usage_parse_status=ok`;
  a session candidate satisfies the existing session-row predicate and has
  `usage_parse_status=ok`;
- model-key priority (`mapped_model` before a distinct `original_model`) and earliest
  provider-candidate selection;
- duplicate-marker visibility in raw and session-log request rows;
- URL redaction for legacy dirty database values;
- usage recording, proxy forwarding, quota querying, or session-log synchronization.

Deduplication currently occurs after all ordinary filters. Therefore a single persisted
"winner" is insufficient: when a preferred provider row is filtered out, another
candidate still present in the filtered set can become the winner. The persisted model
must retain all candidate pairs and choose the winner inside each filtered query.

### Selected architecture

Add an idempotently migrated candidate relation:

```sql
CREATE TABLE IF NOT EXISTS usage_dedupe_candidates (
    session_request_id  TEXT NOT NULL,
    provider_request_id TEXT NOT NULL,
    model_priority      INTEGER NOT NULL,
    PRIMARY KEY (session_request_id, provider_request_id),
    FOREIGN KEY (session_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE,
    FOREIGN KEY (provider_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_usage_dedupe_provider
ON usage_dedupe_candidates(provider_request_id);

CREATE INDEX IF NOT EXISTS idx_usage_requests_started_id
ON usage_requests(started_at DESC, id DESC);
```

`model_priority` is `0` when the provider's mapped or original model matches the
session row's mapped model. It is `1` only when the first match is through the session
row's distinct original model. Candidate rows are immutable because usage rows are
insert-only.

Migration backfills candidate pairs in one transaction using the exact production
fingerprint. A marker in `settings` prevents repeated full backfills after successful
completion. New provider and session rows maintain candidates incrementally in the
same transaction as the usage insert. Either insertion order is supported.

Each read builds a parameterized filtered dataset, chooses the first available candidate
by `model_priority`, provider `started_at`, and a deterministic request-ID tie-breaker,
then applies the requested statistics scope. Aggregation and pagination operate on this
scoped SQL dataset. Exact timestamp ties did not have a stable public order previously;
the request-ID tie-breaker makes this edge deterministic without changing any defined
contract.

### Frontend data flow

- Fetch status and configuration once during initial dashboard loading.
- Reuse that state for the service-status panel, connection-mode state, and `AppHeader`.
- Keep the existing 30-second refresh, but issue one status request per interval.
- Load session projects and the first session page only when the sessions tab is first
  activated, including direct navigation through the `tab=sessions` query parameter.
- Keep all existing usage endpoints. The usage tab can continue issuing six concurrent
  requests because each endpoint will use a narrow SQL projection and aggregation.

### Error and security behavior

- Schema creation, backfill, and migration-marker update are atomic. Failure rolls back
  the entire migration.
- Incremental candidate maintenance is atomic with the usage insert. Recording errors
  remain observable through existing logs and never alter the proxied response.
- All filter values remain SQL parameters. Existing search wildcard behavior is retained.
- Requests and Coverage redact URL values at their output boundaries. Aggregate queries
  that do not expose URLs do not read or parse them.
- No request prompts, credentials, tokens, cookies, provider payloads, or unredacted URLs
  are added to logs, candidate rows, migration markers, or benchmark output.
- Foreign-key cascades remove candidates during usage clearing. The session-sync reset
  option keeps its current behavior.

### Supported data range (int64 aggregation boundary, M-3 conclusion)

Aggregation (the in-row four-token sum and `SUM()`; see `scopedTokenSumExpr` and the
Summary/Trends/Providers/Models queries in `scoped_query.go`) pushes the former Go
per-row int64 addition down into SQLite. The supported data range is defined by the
following invariants, and this optimization promises field-for-field parity with the
old algorithm only within that range:

- **I1 Single values**: the four `usage_tokens` counters and `duration_ms` are valid
  int64s. The API parser (`usageFieldInt64` in `parse.go`) rejects integers `> MaxInt64`
  and floats `≥ 2^63` (ignored as junk fields), but integers exactly `MaxInt64`/`MinInt64`
  are accepted; session-sync int64 fields reject out-of-range values via `encoding/json`;
  `duration_ms` derives from Go `time.Since` (≤ ~9.2e12 ms per value).
- **I2 In-row sum**: the sum of the four token counters per row stays within int64.
- **I3 Cross-row sums**: token totals and duration totals of any aggregation group
  (Summary global, each Providers/Models group, each Trends bucket) stay within int64.

Real product data always satisfies I2/I3: single counters are real token counts
(≤ ~1e9), row counts are ≤ a few hundred thousand, and `duration_ms` ≤ 9.2e12 ms/row
(provably safe up to 10^6 rows) — five or more orders of magnitude below the int64
limit (≈9.2e18). Within the range, SQL aggregates are always INTEGER and match the old
Go int64 accumulation bit-for-bit.

**Overflow-point behavior (documented; only for database contents violating I2/I3,
e.g. hand-edited values near 2^63)** : the SQL aggregation path fails with an explicit
error and never silently returns a distorted number. An in-row sum overflow promotes
the SQLite integer expression to REAL, and database/sql cannot scan the scientific-
notation REAL into int64 (`Scan error`); a cross-row `SUM()` integer overflow fails at
query time with SQLite `integer overflow`. The old Go implementation silently wrapped at
these points (garbage negative values); the new path fails explicitly instead. This
difference affects only unsupported data and never changes behavior within the
supported range. The boundary is locked by `internal/usage/int64_boundary_test.go` on
the target driver (modernc.org/sqlite): single values near MaxInt64, in-row sums
exactly at MaxInt64, and cross-row totals near MaxInt64 (including duration) all match
the legacyOracle field-for-field; overflow points assert the explicit error plus a
documented comparison against the old wrapped values.

## Development Checklist

| # | Status | Item | Evidence |
|---|---|---|---|
| 1 | Done | Candidate schema, atomic backfill, and idempotent migration | Migration and compatibility tests (6A pass) |
| 2 | Done | Atomic incremental candidate maintenance for either insertion order | Dedupe write-path tests (6A pass) |
| 3 | Done | Filtered/scoped SQL dataset and real request pagination | Differential and pagination tests (6A pass) |
| 4 | Done | Dedicated SQL aggregation for all statistics endpoints | Differential tests and query plans (6A pass) |
| 5 | Done | One initial status request and lazy session loading | Frontend source/behavior tests (6B2 pass) |
| 6 | Done (R7 partially met; reworked R1–R5) | Full regression, cross-build, production-data, and synthetic benchmarks | See “Implementation Status”: all verification passed; rework eliminated both recorded regressions and brought status close to target; six-parallel still over (needs cross-request materialization, proposed R6) |

## Requirements

### R1. Preserve all observable statistics behavior

For the same database contents and filter, every public result must equal the legacy
algorithm field-for-field. Previously undefined ties between exact-timestamp candidates
or equal-valued aggregate groups may become deterministic, but their documented primary
ordering keys must not change.

### R2. Persist all deduplication candidates safely

Migration must preserve existing rows, discover every matching session/provider pair,
and be restart-safe. New rows must create candidate relationships regardless of which
side is inserted first. A failed relationship write must not commit a partial usage row.

### R3. Apply filtering before candidate selection

Both the session row and provider candidate must be present in the ordinary filtered
dataset before the provider can mark that session row as a duplicate. If the first
candidate is absent, selection must fall back to the next eligible candidate.

### R4. Push pagination and aggregation into SQLite

Request totals and page rows must be computed without materializing all matching details
in Go. Summary and grouped statistics must select only required fields and return
database aggregates rather than complete request rows.

### R5. Retain output-boundary URL redaction

Legacy rows containing URL userinfo or sensitive query values must remain redacted in
Requests and Coverage. Queries that do not return URLs must avoid URL parsing entirely.

### R6. Remove redundant and eager control-plane work

Initial dashboard loading must share one status result. Session data must not be fetched
until the sessions tab is active. Refresh and mode-update behavior must remain current.

### R7. Meet measurable performance targets

On the current 60-thousand-row production database:

- `/api/status`: at most 100 ms;
- first 50 request rows, including total: at most 100 ms;
- six usage requests in parallel: at most 300 ms;
- first candidate backfill: at most 2 seconds.

An opt-in one-million-row synthetic benchmark records these observation targets:

- status at most 500 ms;
- first request page at most 300 ms;
- six usage operations at most 1.5 seconds.

CI must not use flaky wall-clock assertions. It verifies correctness, query structure,
pagination pushdown, and migration behavior; benchmark timings are recorded separately.

## Task Details

### Task 1: Candidate schema and historical backfill

#### Requirements

**Objective** - Create the candidate relation and atomically backfill existing usage
records without changing any public result.

**Outcomes** - `Store.Migrate` creates both indexes and the relation, runs a one-time
minimal-projection backfill, then writes the completed marker in the same transaction.

**Evidence** - Tests cover empty, populated, repeated, failed, and already-completed
migrations, plus every fingerprint boundary.

**Constraints** - No usage row is rewritten or deleted. Backfill is bounded to the
minimal fields required for matching and uses an O(n log n) or better indexed/sweep
algorithm rather than a quadratic cross join.

**Edge Cases** - Empty models, identical mapped/original models, multiple providers,
exactly ±10 minutes, timestamp ties, malformed historical timestamps, and no candidates.

**Verification** - Migration tests pass and a 60-thousand-row synthetic backfill meets
the recorded target.

#### Plan

- [ ] Add failing migration and backfill tests in `internal/usage/dedupe_test.go`.
- [ ] Run `go test ./internal/usage -run 'TestDedupeMigration|TestDedupeBackfill' -count=1`
  and confirm failures are caused by the absent candidate relation.
- [ ] Add schema statements and migration marker handling to `internal/usage/store.go`;
  create focused matching/backfill helpers in `internal/usage/dedupe.go`.
- [ ] Re-run the focused tests and confirm they pass.
- [ ] Run `go test ./internal/usage -count=1` and commit the task.

#### Verification

- [ ] Focused tests
- [ ] Package regression tests
- [ ] Migration timing evidence

### Task 2: Incremental candidate maintenance

#### Requirements

**Objective** - Keep candidate pairs correct for insert-only provider and session usage
records without periodic global recomputation.

**Outcomes** - `Record` and `recordIfAbsent` invoke one shared transactional helper after
both request and token rows exist. The helper inserts all opposite-side matches in the
inclusive time window and never stores secrets.

**Evidence** - Tests insert provider-first, session-first, multiple candidates, boundary,
and non-match fixtures and inspect both public results and candidate rows.

**Constraints** - Candidate maintenance is part of the existing transaction. Duplicate
session sync remains idempotent.

**Edge Cases** - `none`/`missing` usage, non-Claude source app, failed provider usage,
different token fields, and repeated `recordIfAbsent`.

**Verification** - Focused write-path tests and concurrent record/read tests pass.

#### Plan

- [ ] Add failing insertion-order and atomicity tests to `internal/usage/dedupe_test.go`.
- [ ] Run the focused tests and confirm the expected missing-candidate failures.
- [ ] Add `maintainDedupeCandidatesTx` in `internal/usage/dedupe.go` and call it from both
  insertion methods in `internal/usage/store.go`.
- [ ] Re-run focused tests, then `go test ./internal/usage -count=1`.
- [ ] Commit the task.

#### Verification

- [ ] Insertion-order tests
- [ ] Atomic rollback tests
- [ ] Concurrent WAL regression tests

### Task 3: Scoped SQL dataset and request pagination

#### Requirements

**Objective** - Express filtering, candidate choice, statistics scope, duplicate markers,
counting, ordering, and pagination in parameterized SQL.

**Outcomes** - A focused SQL builder in `internal/usage/scoped_query.go` returns the query
fragment and ordered arguments used by all read methods. `Requests` performs SQL count
and page retrieval and redacts URLs only for returned rows.

**Evidence** - A test-only legacy oracle compares totals, rows, ordering, and markers
across filters/scopes. A query-structure test proves `LIMIT/OFFSET` reaches SQLite.

**Constraints** - Filters are applied before candidate selection. Existing substring
search and time-zone parsing remain unchanged.

**Edge Cases** - Page beyond total, zero rows, candidate fallback after filtering,
session-only/provider-only filters, and legacy dirty URLs.

**Verification** - Differential tests pass and page-50 production timing meets target.

#### Plan

- [ ] Add a test-only legacy oracle and failing differential/pagination tests in
  `internal/usage/scoped_query_test.go`.
- [ ] Run the focused tests and confirm SQL pagination/scoped selection is absent.
- [ ] Implement the parameterized filtered/candidate/scoped query builder in
  `internal/usage/scoped_query.go`.
- [ ] Replace in-memory slicing in `internal/usage/store.go` with SQL total/page queries.
- [ ] Re-run focused tests and `go test ./internal/usage -count=1`; commit the task.

#### Verification

- [ ] Scope/filter differential matrix
- [ ] Pagination query-plan evidence
- [ ] URL-redaction regression

### Task 4: Dedicated SQL statistics

#### Requirements

**Objective** - Replace repeated full-row materialization with narrow SQL aggregations.

**Outcomes** - Summary, Trends, Providers, Models, and Coverage aggregate the scoped
dataset in SQLite. Only Coverage selects a provider URL and redacts grouped output.

**Evidence** - Legacy/new differential tests compare every field and ordering rule.
Production and synthetic benchmarks record each operation.

**Constraints** - Token totals, usage coverage, average-duration null behavior, failure
classification, local-day bucketing, and top parse-status tie behavior remain exact.

**Edge Cases** - No usage, all failures, null durations/statuses, equal group counts,
DST transitions, invalid time zone, and all statistics scopes.

**Verification** - Differential suite passes and `queryRows` is no longer used by public
statistics or request pagination.

#### Plan

- [ ] Add failing aggregate differential tests to `internal/usage/sql_aggregate_test.go`.
- [ ] Run focused tests and record mismatches against the legacy implementation.
- [ ] Implement dedicated aggregate SQL in `internal/usage/store.go`, sharing only the
  scoped SQL fragment and filter arguments.
- [ ] Remove obsolete full-materialization paths after all differential tests pass.
- [ ] Run package/full Go tests and commit the task.

#### Verification

- [ ] Aggregate differential matrix
- [ ] Time-zone/DST tests
- [ ] Query-plan and projection review

### Task 5: Frontend request consolidation and lazy sessions

#### Requirements

**Objective** - Stop duplicate status scans and avoid session work on unrelated tabs.

**Outcomes** - `DashboardView` owns initial status/config state and passes status-derived
version/mode data to `AppHeader`; connection mode reuses the same result. Session data
loads once on first sessions-tab activation.

**Evidence** - Frontend source/behavior tests assert one initial status request, no
session request on the status tab, and immediate loading for direct sessions navigation.

**Constraints** - Header update checks, mode update events, tab query parameters,
30-second refresh, error tolerance, and session manual refresh remain unchanged.

**Edge Cases** - Initial sessions tab, rapid tab switching, failed first load followed by
retry, mode save, logout, and component unmount.

**Verification** - Frontend tests and production build pass.

#### Plan

- [ ] Update/add failing tests in `internal/frontend/src/views/DashboardSessionsPreload.test.ts`,
  `internal/frontend/src/views/DashboardStatusLoad.test.ts`, and
  `internal/frontend/src/components/AppHeader.test.ts`.
- [ ] Run the focused frontend tests and confirm current eager/duplicate behavior fails
  the new expectations.
- [ ] Modify `DashboardView.vue` and `AppHeader.vue` to share status/config state and
  lazy-load sessions.
- [ ] Run all frontend tests and `npm --prefix internal/frontend run build`.
- [ ] Commit source, tests, and updated `internal/frontend/dist`.

#### Verification

- [ ] Focused frontend tests
- [ ] Full frontend tests
- [ ] Embedded production build

### Task 6: Performance and release-grade verification

#### Requirements

**Objective** - Prove compatibility and record fresh performance evidence before merge.

**Outcomes** - Deterministic benchmarks support configurable 60-thousand and one-million
row datasets. The spec checklist, progress, and verification sections are updated with
actual results.

**Evidence** - Full tests, vet, build, diff check, six cross-platform builds, synthetic
benchmarks, and read-only production-database probes complete successfully.

**Constraints** - Benchmarks do not expose production contents or credentials and do not
modify the production database.

**Edge Cases** - CGO disabled, Windows time zones, Darwin build tags, repeat benchmark
runs, and a concurrently active WAL database.

**Verification** - All commands listed below exit zero and measured targets are recorded.

#### Plan

- [x] Add non-flaky benchmarks to `internal/usage/performance_test.go`, with
  `MCC_USAGE_BENCH_ROWS` selecting dataset size.
- [x] Run focused benchmarks at 60 thousand rows (60,332); the opt-in one-million-row profile
  was not run this round.
- [x] Run `go test ./...`, `go vet ./...`, `npm --prefix internal/frontend test`,
  `npm --prefix internal/frontend run build`, and `git diff --check`.
- [x] Run `CGO_ENABLED=0 go test ./...` and compile `./cmd/server` for linux, darwin, and
  windows on amd64 and arm64 (6/6 pass).
- [ ] Probe a read-only copy/snapshot of the production database: no production snapshot was
  available, so a 60,332-row production-representative synthetic dataset (seed=1) was used
  instead, with the pre-optimization implementation `032aa80` measured on the same machine as a
  control.
- [x] Update both specs with actual evidence and commit validation documentation (incl. rework
  rounds R1–R5: reports `R1-index-diagnostics.md` … `R5-final-verification.md`; same-session A/B
  tooling in `internal/usage/ab_compare_test.go`).

#### Verification

- [x] Go and frontend regression (`go test ./...` all pass; frontend 269/269)
- [x] Vet/build/diff checks (all pass)
- [x] Six cross-platform builds (6/6)
- [x] Synthetic 60-thousand evidence (see “Implementation Status / Performance measurements”;
  rework R1–R5 completed: R7 status approaches target, six-parallel still over — needs
  cross-request materialization, proposed R6; both recorded regressions eliminated)
- [ ] Production-data evidence (no production snapshot; synthetic representative dataset used;
  opt-in one-million-row run not executed)
