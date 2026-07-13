# Provider Quota Failover Review Notes

Date: 2026-07-13 (Codex review) / 2026-07-13 (Claude Code fixes)
Reviewers: Codex (review), Claude Code (fixes)

## Scope

Reviewed the implemented provider quota failover feature on branch `provider-quota-failover`, from the SDD spec commit `f063686` through current HEAD. The review covered failover classification, proxy replay and default-provider switching, authenticated admin APIs, quota snapshot reconciliation, frontend isolation from Session Browser, and event redaction.

## Key Findings And Resolutions

1. Functional defect: status-less upstream failures did not enter failover.
   - Evidence: `failover.ClassifyError` classifies ECONNRESET/timeouts/DNS failures, and the spec lists ECONNRESET/status-less failures as failover-eligible. However the proxy returned `502 Backend unavailable` immediately when `client.Do` returned an error, before any failover attempt.
   - Resolution: **Fixed.** The proxy error branch now gates with `shouldFailoverOnError` and classifies via `ClassifyError` before writing 502; on an eligible transport error it reuses the new `attemptFailoverOnError` (sharing `runCandidateReplay` with the response path), and on success skips the 502 and continues to streaming. Regression test: `TestFailoverSwitchesOnTransportError` (closed upstream → connection refused → switches to candidate and persists the default provider).

2. Functional defect: string business codes such as `"1308"` were not classified as quota exhaustion.
   - Evidence: `parseErrorBody` extracts string `error.code` into `codeStr`, but `ClassifyResponse` only checked numeric `pe.code == 1308/1310`.
   - Resolution: **Fixed.** Added `codeIs(pe, want)` that accepts both numeric and string forms (`1308` and `"1308"` are equivalent); `BusinessCode` is still preserved as a string. Regression tests: `TestClassify1308WithStringCodeIsEquivalent`, `TestClassify1310WithStringCodeIsEquivalent`.

3. Functional defect: failover did not persist the new default provider when the active provider was selected via `GetActiveProvider` fallback.
   - Evidence: If `ActiveProviderID` is empty or points to a disabled provider, `GetActiveProvider` falls back to the first enabled provider; when that fallback fails and a candidate succeeds, the original `CommitSwitch` required `cfg.ActiveProviderID == fromID`, so the compare-and-set did not fire.
   - Resolution: **Fixed.** `CommitSwitch` now compares against the *effective* active provider (`c.GetActiveProvider().ID == fromID`) and writes `ActiveProviderID = toID` on a match, so the switch persists; concurrency still yields a single winner (a second concurrent request sees the new provider as the effective active). Regression tests: `TestCommitSwitchPersistsWhenActiveIsEmptyAndFallbackFailed`, `TestCommitSwitchPersistsWhenActivePointsToDisabled`.

4. Security review: no direct security defect found in the inspected paths.
   - Evidence: `/api/providers/failover` and `/api/failover/events` are registered behind `authMiddlewareFunc`; event tables do not store API tokens, request bodies, response bodies, or raw query strings; event responses contain provider names/IDs, model names, status/code/reason/outcome, and timestamps only. Frontend `FailoverEventsView` is an independent Dashboard tab and is not passed into `SessionBrowser`, `SessionDetail`, export, or JSONL parsing.
   - Resolution: Acceptable for now. Continue treating provider names and model names as user-controlled display strings rendered by Vue interpolation, not raw HTML.

## Final Review Conclusion

All three functional defects raised by the Codex review have been fixed and verified with TDD (a failing reproduction test written first, then the fix, then green). A second Codex review on 2026-07-13 re-read the patch and reran targeted regressions, full Go tests, frontend tests, frontend build, and full Go race tests. No logic or security defects remain.

## Verification

- `go test ./... -race -count=1` passed (1503).
- `npm --prefix internal/frontend test` passed (174).
- `npm --prefix internal/frontend run build` passed.
- Second Codex verification also ran `go test ./... -count=1`, targeted failover regressions, targeted race regressions, and `go test -race ./... -count=1`; all passed.
- The targeted reproduction tests (transport-error failover, string business code, empty/disabled active persistence) are kept as permanent tests in the working tree.

## Residual Notes

- On a failover hit, the original failed upstream connection is closed at handler return via the existing `defer resp.Body.Close()` (delayed reuse, not a leak).
- "Successful provider test" for credential recovery = the test request completed and the upstream returned non-401 (matches the existing test endpoint's "connectivity succeeded" semantics).
- Concurrent failures of the same provider may replay once to the same candidate (functionally correct; the compare-and-set still guarantees a single `switched` event).
- Not pushed: commits are local on `provider-quota-failover`, awaiting user confirmation.
