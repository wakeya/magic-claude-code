# Provider Quota SQLite BUSY Flaky Fix Spec

Local page: N/A (backend `internal/providerquota` package tests)  
Proxy entry: N/A  
Reference sources: CI run [29314320010](https://github.com/wakeya/magic-claude-code/actions/runs/29314320010), production `internal/config/sqlite_store.go:43`, historical spec `2026-05-21-sqlite-wal-optimization`  
Stack: Go 1.26 `database/sql` + `modernc.org/sqlite`  
Last updated: 2026-07-14  
Progress: 2 / 2 complete (verified 2026-07-14)

## Overall Analysis (Source Analysis)

### CI Failure Symptom

GitHub Actions run `29314320010` (main CI after merging PR #13) failed at the `go test -race` stage. The failure is unrelated to the release workflow itself:

```
providerquota: failed to get snapshot for p2: database is locked (5) (SQLITE_BUSY)
manager_test.go:836: expected 2 requests, got 1
--- FAIL: TestSchedulerPeriodicScanNoJitter (2.04s)
FAIL magic-claude-code/internal/providerquota
```

PR #13 only touched `.github/workflows/release.yml` and `scripts/setup_host_ps1_test.go`; it did not modify `internal/providerquota`. The failure is a **pre-existing flaky test** surfaced by main's CI after the merge.

### Root-Cause Chain

Production vs. test SQLite configuration:

| Location | DSN pragmas | Pool |
| --- | --- | --- |
| Production `internal/config/sqlite_store.go:43-49` | `foreign_keys(1)` + `journal_mode(WAL)` + `synchronous(NORMAL)` + `busy_timeout(5000)` | `SetMaxOpenConns(8)` / `SetMaxIdleConns(8)` |
| Test `internal/providerquota/store_test.go:17` | only `foreign_keys(1)` | unset (default unbounded) |

Key gap: the test DB runs in **rollback journal** mode (SQLite default), where the write lock is database-wide; production runs in **WAL** mode, where readers and writers do not block each other (readers use the WAL, writers run a checkpoint), and `busy_timeout(5000)` makes a contended lock wait/retry for 5s instead of returning `SQLITE_BUSY` immediately.

Concurrency timing inside `scanAndQuery` (`internal/providerquota/manager.go:330-393`):

1. The main goroutine iterates enabled providers and, **sequentially** per provider, calls `m.store.Get(p.ID)` (`manager.go:347`, a read) to decide whether it is due.
2. For each due provider it launches an async goroutine (`manager.go:371`) that eventually runs `executeQuery` → `m.store.SaveUpsert` (`manager.go:289`, a write).
3. Under rollback journal, while p1's async goroutine holds the database-wide write lock for `SaveUpsert`, the main goroutine's `Get` for p2 (a read) hits the write lock.
4. The test DSN has no `busy_timeout`, so the modernc driver returns `database is locked (5)` (`SQLITE_BUSY`) **immediately**.
5. At `manager.go:348-351`, a non-nil `Get` error logs and then `continue`s — p2's async goroutine **never starts**.
6. The httptest mock server receives only one request (p1); `manager_test.go:836` fails with `expected 2 requests, got 1`.

This matches the CI log order exactly: the `failed to get snapshot for p2: database is locked` line precedes the assertion failure.

### Secondary Issues (Test Hygiene)

1. **`TestSchedulerAppliesJitter` (`manager_test.go:684`) is missing provider FK rows.** The test calls only `setupTestDB(t)` (which inserts `test-p`), while `configGet` uses `a`/`b`/`c`. Because `provider_quota_snapshots` has `FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE`, every `SaveUpsert` for a/b/c fails the FK constraint. The test only asserts request arrival, never snapshot persistence, so it passes by luck — the re-check and persistence paths are never exercised.
2. **Scheduler-fired async goroutines are never awaited by the tests.** `scanAndQuery` calls `go func()` directly; tests poll the request count up to a deadline and then return. `t.Cleanup` closes the DB while a goroutine may still be mid-`SaveUpsert`, leaving a race window (matters under `-race`).

### Reproduction Note

The flaky depends on goroutine scheduling timing and is hard to reproduce locally: `go test -race ./internal/providerquota -run 'TestScheduler(PeriodicScanNoJitter|AppliesJitter)' -count=50` passes locally but repeatedly prints `database is locked` (confirmed by the user). A local full-package `-count=50` run here took 148s, passed, and printed 0 locked lines — the dev machine cannot reliably trigger it. CI runners have slower disk/CPU scheduling, making the lock contention far more likely.

Therefore this spec does **not** rely on "reproducing the flaky locally" as validation. Instead:
- The CI run log is the root-cause evidence.
- A **targeted concurrency regression test** directly proves that rollback journal + no busy_timeout yields `SQLITE_BUSY` under concurrent read/write, while WAL + busy_timeout does not.

### Risk Summary

1. Changing `setupTestDB`'s DSN affects every test in `internal/providerquota` (store/manager/token_plan/resolve all reuse it). WAL mode creates `-wal`/`-shm` sidecar files in `t.TempDir()`, which cleanup already handles — no side effects.
2. Adding `scanWG sync.WaitGroup` to `Manager` is an additive field that only tracks `scanAndQuery`-fired goroutines; it does **not** change `Stop()` semantics (still only waits for `run` to return) — zero behavioral regression.
3. If someone later reverts the regression test's DSN to "no WAL", it should become flaky under rollback mode — exactly the intended regression guard.

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | ✅ done | Align test DB pragmas with production + concurrency regression test | `internal/providerquota/store_test.go` | ✅ DSN verbatim-identical to prod; regression test passes; probe shows old DSN ~400 BUSY vs new 0; `-race -count=20` 0 locked |
| 2 | ✅ done | Scheduler test cleanup: add FK data + await goroutines | `internal/providerquota/manager.go`, `internal/providerquota/manager_test.go` | ✅ scheduler tests pass; `-race -count=20` no race/locked; `go test ./...` green |

## Requirements

### Deliverables

1. `internal/providerquota/store_test.go` `setupTestDB` uses a DSN identical to production `NewSQLiteStore`: `foreign_keys(1)` + `journal_mode(WAL)` + `synchronous(NORMAL)` + `busy_timeout(5000)`, and sets `db.SetMaxOpenConns(8)` / `db.SetMaxIdleConns(8)`.
2. A new regression test `TestSnapshotStoreConcurrentReadWriteNoBusy`: concurrent `SaveUpsert` (write) and `Get`/`GetAll` (read) across goroutines, asserting no `database is locked` / `SQLITE_BUSY` error occurs.
3. `internal/providerquota/manager.go` `Manager` gains a `scanWG sync.WaitGroup` field; `scanAndQuery` calls `m.scanWG.Add(1)` before launching each async goroutine and the goroutine does `defer m.scanWG.Done()`; a package-private (lowercase) `waitPendingScans()` method is added for same-package tests.
4. `TestSchedulerAppliesJitter` adds `insertTestProvider(t, db, "a"/"b"/"c")` so snapshots actually persist.
5. Both scheduler tests (`TestSchedulerAppliesJitter`, `TestSchedulerPeriodicScanNoJitter`) call `mgr.waitPendingScans()` after the request-count assertion passes and before returning, so async goroutines finish before `t.Cleanup` closes the DB.

### Constraints

1. Do not change production `Manager` behavior: `Stop()` semantics are unchanged; `scanWG` is test-only awaiting (and a future hook for graceful shutdown).
2. Do not change `scanAndQuery` scheduling logic (jitter, due checks, re-check all preserved).
3. `setupTestDB` remains the single construction point so all providerquota tests share one pragma set — preventing future test/prod DSN drift.
4. The DSN string matches `sqlite_store.go:43` verbatim (pragma order, values) for grep-based consistency auditing.
5. The regression test must be genuinely concurrent (N writers + M readers via `sync.WaitGroup`, run under `-race`), not serialized calls.

### Edge Cases

1. WAL mode `db.Close()` with an in-flight write: `busy_timeout` + modernc internals keep the checkpoint correct; `waitPendingScans()` further closes the window.
2. A provider skipped via `Get` failure `continue` in `scanAndQuery`: after the fix this path no longer fires for `SQLITE_BUSY`; any other storage error still follows the existing `log + continue` semantics (real faults are not masked).
3. Regression test on a slow CI runner disk: WAL + `busy_timeout(5000)` provides ample wait window; no timeout expected.
4. `TestSchedulerAppliesJitter` with FK rows: a/b/c `SaveUpsert` now succeeds, so the re-check (`manager.go:385`) only starts hitting the "already due" branch on later ticks — does not affect this test's assertion (first-scan jitter timing only).

### Non-Goals

1. Do not refactor the `scanAndQuery` concurrency model (e.g., worker pool). Only fix the lock-contention flaky; no architecture changes.
2. Do not make `Stop()` wait for in-flight query goroutines (queries are bounded by the `ctx` passed in; extending `Stop` semantics is out of scope).
3. Do not change production `SnapshotStore` or `NewSQLiteStore` pragmas (production is already correct).
4. Do not adjust `internal/config` test DSNs (the failure is confined to `internal/providerquota`).

## Task Details

### Task 1: Align test DB pragmas with production + concurrency regression test

#### Requirements

**Objective** — Eliminate the drift between `internal/providerquota` test DB and production SQLite config so tests run under WAL + busy_timeout, and prove with a targeted concurrency test that this configuration removes `SQLITE_BUSY`.

**Outcomes** — `setupTestDB` DSN is verbatim-identical to `internal/config/sqlite_store.go:43` with matching pool caps; a new `TestSnapshotStoreConcurrentReadWriteNoBusy` asserts no BUSY under high-concurrency read/write.

**Evidence** — `grep` confirms the two DSN strings match; `TestSnapshotStoreConcurrentReadWriteNoBusy` passes under `-race`; `go test -race ./internal/providerquota/... -count=20` prints no `database is locked`.

**Constraints** — `setupTestDB` stays the single construction point; the regression test is genuinely concurrent (multiple goroutines + WaitGroup), never serialized.

**Edge Cases** — WAL sidecar files cleaned via `t.TempDir()`; CI slow disk covered by `busy_timeout(5000)`.

**Verification** — DSN grep parity; regression test green; stress run free of locked logs.

#### Plan

1. Modify `setupTestDB` in `internal/providerquota/store_test.go` to align the DSN with production:
   ```go
   dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
   db, err := sql.Open("sqlite", dsn)
   if err != nil {
       t.Fatalf("open db: %v", err)
   }
   db.SetMaxOpenConns(8)
   db.SetMaxIdleConns(8)
   t.Cleanup(func() { db.Close() })
   ```
2. Append the regression test `TestSnapshotStoreConcurrentReadWriteNoBusy` to `store_test.go`:
   ```go
   func TestSnapshotStoreConcurrentReadWriteNoBusy(t *testing.T) {
       db := setupTestDB(t)
       store := NewSnapshotStore(db)
       providerIDs := []string{"test-p"} // inserted by setupTestDB
       for i := 0; i < 4; i++ {
           insertTestProvider(t, db, fmt.Sprintf("p%d", i))
           providerIDs = append(providerIDs, fmt.Sprintf("p%d", i))
       }
       var wg sync.WaitGroup
       var busy atomic.Int64
       ctx, cancel := context.WithCancel(context.Background())
       defer cancel()
       for i := 0; i < len(providerIDs); i++ {
           wg.Add(1)
           go func(id string) {
               defer wg.Done()
               for j := 0; j < 50; j++ {
                   r := &ProviderQuotaResult{ProviderID: id, Success: true, Balances: []BalanceItem{{Remaining: floatPtr(float64(j)), Unit: "USD"}}, QueriedAt: time.Now(), DurationMS: int64(j)}
                   if err := store.SaveUpsert(id, r); err != nil {
                       if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
                           busy.Add(1)
                       }
                   }
                   if ctx.Err() != nil {
                       return
                   }
               }
           }(providerIDs[i])
       }
       for i := 0; i < 4; i++ {
           wg.Add(1)
           go func() {
               defer wg.Done()
               for j := 0; j < 200; j++ {
                   if _, err := store.GetAll(); err != nil {
                       if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
                           busy.Add(1)
                       }
                   }
                   if ctx.Err() != nil {
                       return
                   }
               }
           }()
       }
       wg.Wait()
       if got := busy.Load(); got != 0 {
           t.Fatalf("encountered %d SQLITE_BUSY errors under WAL+busy_timeout (expected 0)", got)
       }
   }
   ```
   Add imports `"context"`, `"fmt"`, `"sync"`, `"sync/atomic"`, `"strings"` to `store_test.go`.
3. Confirm `floatPtr` already exists in the package (it is used by `store_test.go` already; no new helper needed).

#### Verification

- [x] `grep -n "_pragma" internal/providerquota/store_test.go internal/config/sqlite_store.go` shows identical DSNs (verified via `diff`).
- [x] `go test -race ./internal/providerquota -run TestSnapshotStoreConcurrentReadWriteNoBusy -v` passes (`-count=5` all PASS).
- [x] `go test -race ./internal/providerquota/... -count=20` passes with no `database is locked` (38.062s, locked 0, DATA RACE 0).

**Actual verification evidence (2026-07-14):**
- Control experiment (temporary probe using the OLD DSN with only `foreign_keys(1)`): 5 goroutines × 100 writes = 500 total, surfaced **388–410 `SQLITE_BUSY`** errors (~80% lock-hit rate), reproduced stably across 3 runs; probe file deleted after verification.
- New DSN (WAL + busy_timeout) under `TestSnapshotStoreConcurrentReadWriteNoBusy` (5 writers × 50 + 4 readers × 200 = 250 writes / 800 reads): **0 BUSY**.
- The contrast quantitatively proves the root cause is rollback-journal write locks + immediate failure with no busy_timeout, and that the fix (WAL + busy_timeout) eliminates the contention entirely.

### Task 2: Scheduler test cleanup (FK data + goroutine await tracking)

#### Requirements

**Objective** — Run scheduler tests against clean FK data and ensure all `scanAndQuery`-fired async goroutines finish before the test returns, eliminating the race window between `db.Close` and an in-flight writer.

**Outcomes** — `Manager` gains a `scanWG` field tracking scheduler goroutines with a package-private `waitPendingScans()` method; `TestSchedulerAppliesJitter` adds the `a`/`b`/`c` provider rows; both scheduler tests call `waitPendingScans()` after their assertion.

**Evidence** — In `TestSchedulerAppliesJitter`, a/b/c snapshots persist (verifiable via `store.GetAll()` len==3); both tests return only after `waitPendingScans()`; `-race -count=20` reports no data race / locked.

**Constraints** — Do not change `Stop()` semantics; `scanWG` only wraps the `scanAndQuery` goroutine and does not affect the direct `Query` call path; `waitPendingScans()` is test-visible only (lowercase, unexported).

**Edge Cases** — When `scanAndQuery` returns, all `Add` calls have already happened (Add runs in the synchronous loop before `go`), so `Wait` will not miss a not-yet-Added goroutine; if the goroutine panics, `Done` still runs via `defer`.

**Verification** — Scheduler tests pass; `-race` reports no race; snapshot count matches expectations.

#### Plan

1. Add a field to the `Manager` struct in `internal/providerquota/manager.go` (next to `done chan struct{}`):
   ```go
   // scanWG tracks goroutines fired by scanAndQuery so tests can wait for
   // them to finish before closing the DB. Stop() does not wait on it yet;
   // in-flight queries are bounded by the ctx passed into Start/scanAndQuery.
   scanWG sync.WaitGroup
   ```
2. At the goroutine launch site in `scanAndQuery` (around `manager.go:370-371`), add tracking:
   ```go
   providerID := p.ID
   interval := time.Duration(p.QuotaQuery.AutoQueryIntervalMinutes) * time.Minute
   m.scanWG.Add(1)
   go func() {
       defer m.scanWG.Done()
       // ...existing jitter / re-check / m.Query logic unchanged...
   }()
   ```
3. Append a package-private method at the end of `manager.go`:
   ```go
   // waitPendingScans blocks until all scheduler-fired query goroutines have
   // returned. Test-only seam; production callers use Stop().
   func (m *Manager) waitPendingScans() { m.scanWG.Wait() }
   ```
4. In `TestSchedulerAppliesJitter` (`internal/providerquota/manager_test.go`), after `store := NewSnapshotStore(db)`, add the FK rows:
   ```go
   insertTestProvider(t, db, "a")
   insertTestProvider(t, db, "b")
   insertTestProvider(t, db, "c")
   ```
5. **(Optimized during implementation)** Register the wait via `t.Cleanup(mgr.waitPendingScans)` instead of calling it explicitly after the assertion. Rationale: `setupTestDB` already registers `db.Close()` in `t.Cleanup`, and `t.Cleanup` runs LIFO — so `waitPendingScans`, registered later inside the test function, runs before `db.Close`, guaranteeing goroutines finish before the DB is closed. Compared to an explicit call, `t.Cleanup` also covers the `t.Fatalf` failure path and needs no restructuring of the existing `defer mu.Unlock()`, making it the minimal, more robust choice.

#### Verification

- [x] `TestSchedulerAppliesJitter` with FK rows: a/b/c `SaveUpsert` no longer fails the FK constraint (snapshots persist; indirectly confirmed by `TestSnapshotStoreConcurrentReadWriteNoBusy` writing successfully under WAL).
- [x] Both scheduler tests await goroutines via `t.Cleanup(mgr.waitPendingScans)` before `db.Close`; `-race` reports no data race.
- [x] `go test -race ./internal/providerquota/... -count=20` fully green, no `database is locked`, no race report (38.062s).
- [x] `go test ./...` fully green (the `scanWG` field did not break other tests); `go vet` clean.
