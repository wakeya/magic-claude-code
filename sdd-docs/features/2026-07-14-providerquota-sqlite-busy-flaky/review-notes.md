# Provider Quota SQLite BUSY Flaky Review Notes

Date: 2026-07-14  
Reviewers: Codex and Claude Code

## Scope

Reviewed commit `d54dd74` for the providerquota SQLite BUSY flaky fix: test SQLite DSN/pool parity with production, scheduler goroutine tracking, FK fixture repair, and the new concurrent read/write regression test. Follow-up review covered commit `0f7595e`, which implements the non-BUSY error capture maintenance item.

## Key Findings And Resolutions

1. No blocking logic or security defects were found.
   - Resolution: The test DSN now uses the same SQLite pragmas and pool caps as production, the scheduler tests wait for `scanAndQuery` goroutines before DB cleanup, and the FK fixture rows make jitter-test snapshot persistence possible.

2. Maintenance note: `TestSnapshotStoreConcurrentReadWriteNoBusy` only counts `locked`/`busy` errors and ignores other unexpected store errors.
   - Resolution: Acceptable for this fix because the test is scoped to the BUSY regression and the existing store tests cover normal persistence paths. If this test is edited again, record the first non-BUSY error and fail after `wg.Wait()` so unrelated storage regressions cannot pass silently. **[Implemented 2026-07-14]** — the test now records the first non-BUSY store error via a mutex-guarded `firstErr` and fails after `wg.Wait()` if one is present; BUSY counting is unchanged. Re-verified green under `-race -count=5` (firstErr stays nil on the normal WAL path).

## Final Review Conclusion

The GLM-5.2 fix is acceptable as submitted. The root-cause fix matches production SQLite behavior, the scheduler cleanup is scoped to tests without changing `Stop()` semantics, and no logic or security defects remain for this issue. The one follow-up maintenance item (non-BUSY error capture in the regression test) has since been implemented and verified.

## Residual Notes

- Verified with `go test -race ./internal/providerquota -run 'TestSnapshotStoreConcurrentReadWriteNoBusy|TestScheduler(AppliesJitter|PeriodicScanNoJitter)' -count=5 -v`.
- Verified with `go test -race ./internal/providerquota/... -count=20`.
- Verified with `go vet ./...`.
- Verified with `go test ./...`.
- Follow-up (2026-07-14): added non-BUSY error capture to `TestSnapshotStoreConcurrentReadWriteNoBusy`; re-verified with `go test -race ./internal/providerquota -run TestSnapshotStoreConcurrentReadWriteNoBusy -count=5 -v`.
- Follow-up review (2026-07-14): reviewed commit `0f7595e`; no new blocking findings.
