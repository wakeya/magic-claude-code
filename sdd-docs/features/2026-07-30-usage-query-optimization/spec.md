# Usage Query Performance Optimization Spec

Local page: `/` service status, `/` usage statistics, `/` sessions  
Admin entry points: `GET /api/status`, `GET /api/usage/*`, `GET /api/sessions`, `GET /api/sessions/projects`  
Reference features: `2026-05-15-usage-statistics`, `2026-05-21-sqlite-wal-optimization`, `2026-06-12-windows-usage-statistics-fixes`, `2026-07-17-kimi-quota-usage-parsing-fixes`  
Stack: Go 1.26, SQLite/WAL, Vue 3, TypeScript  
Last updated: 2026-07-30  
Progress: design approved; written spec awaiting review; implementation 0 / 6 tasks

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

## Development Checklist

| # | Status | Item | Evidence |
|---|---|---|---|
| 1 | Pending | Candidate schema, atomic backfill, and idempotent migration | Migration and compatibility tests |
| 2 | Pending | Atomic incremental candidate maintenance for either insertion order | Dedupe write-path tests |
| 3 | Pending | Filtered/scoped SQL dataset and real request pagination | Differential and pagination tests |
| 4 | Pending | Dedicated SQL aggregation for all statistics endpoints | Differential tests and query plans |
| 5 | Pending | One initial status request and lazy session loading | Frontend source/behavior tests |
| 6 | Pending | Full regression, cross-build, production-data, and synthetic benchmarks | Recorded verification evidence |

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

- [ ] Add non-flaky benchmarks to `internal/usage/performance_test.go`, with
  `MCC_USAGE_BENCH_ROWS` selecting dataset size.
- [ ] Run focused benchmarks at 60 thousand rows and the opt-in one-million-row profile.
- [ ] Run `go test ./...`, `go vet ./...`, `npm --prefix internal/frontend test`,
  `npm --prefix internal/frontend run build`, and `git diff --check`.
- [ ] Run `CGO_ENABLED=0 go test ./...` and compile `./cmd/server` for linux, darwin, and
  windows on amd64 and arm64.
- [ ] Probe a read-only copy/snapshot of the production database and record status,
  request-page, six-operation parallel, and migration timings.
- [ ] Update both specs with actual evidence and commit validation documentation.

#### Verification

- [ ] Go and frontend regression
- [ ] Vet/build/diff checks
- [ ] Six cross-platform builds
- [ ] Synthetic 60-thousand/one-million evidence
- [ ] Production-data evidence
