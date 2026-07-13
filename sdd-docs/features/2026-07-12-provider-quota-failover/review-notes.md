# Provider Quota Failover Review Notes

Date: 2026-07-13
Reviewers: Codex

## Scope

Reviewed the implemented provider quota failover feature on branch `provider-quota-failover`, from the SDD spec commit `f063686` through current HEAD. The review covered failover classification, proxy replay and default-provider switching, authenticated admin APIs, quota snapshot reconciliation, frontend isolation from Session Browser, and event redaction.

## Key Findings And Resolutions

1. Functional defect: status-less upstream failures do not enter failover.
   - Evidence: `failover.ClassifyError` classifies ECONNRESET/timeouts/DNS failures, and the spec lists ECONNRESET/status-less failures as failover-eligible. However, `internal/proxy/handler.go` returns `502 Backend unavailable` immediately when `client.Do` returns an error, before any failover attempt. A temporary review test using a closed upstream server plus a healthy candidate failed with original `502` instead of switching.
   - Resolution: Not fixed in this review. The proxy error branch should classify transport errors and reuse the candidate replay path before writing the 502 response.

2. Functional defect: string business codes such as `"1308"` are not classified as quota exhaustion.
   - Evidence: `parseErrorBody` extracts string `error.code` into `codeStr`, but `ClassifyResponse` only checks numeric `pe.code == 1308/1310`. A temporary review test with `{"error":{"code":"1308","message":"已达到 5 小时的使用上限..."}}` failed to classify as eligible. This matters because the user requirement explicitly said to consider the provider response data code, not only HTTP status.
   - Resolution: Not fixed in this review. Classification should treat string codes `"1308"` and `"1310"` equivalently to numeric codes, and should preserve `BusinessCode`.

3. Functional defect: failover does not persist the new default provider when the active provider was selected via `GetActiveProvider` fallback.
   - Evidence: If `ActiveProviderID` is empty or points to a disabled provider, `GetActiveProvider` falls back to the first enabled provider. When that fallback provider fails and a candidate succeeds, `CommitSwitch(fromID, toID, ...)` compares `cfg.ActiveProviderID == fromID`; because the stored active ID is empty/disabled, the compare-and-set does not update `ActiveProviderID`. A temporary review test returned the candidate response but left `ActiveProviderID == ""`, so later default requests can keep hitting the same failed fallback provider.
   - Resolution: Not fixed in this review. The switch commit path should handle the effective active provider case, or normalize `ActiveProviderID` before failover.

4. Security review: no direct security defect found in the inspected paths.
   - Evidence: `/api/providers/failover` and `/api/failover/events` are registered behind `authMiddlewareFunc`; event tables do not store API tokens, request bodies, response bodies, or raw query strings; event responses contain provider names/IDs, model names, status/code/reason/outcome, and timestamps only. Frontend `FailoverEventsView` is an independent Dashboard tab and is not passed into `SessionBrowser`, `SessionDetail`, export, or JSONL parsing.
   - Resolution: Acceptable for now. Continue treating provider names and model names as user-controlled display strings rendered by Vue interpolation, not raw HTML.

## Final Review Conclusion

The implementation is not ready to merge as complete. The self-reported tests pass, but targeted review tests exposed three functional gaps against the spec: no failover on transport/status-less failures, missed string business-code quota errors, and failure to persist the new default provider when the failed provider was selected through fallback active-provider resolution. I did not find a direct security issue in the new admin APIs or event storage/display paths.

## Verification

- `go test ./... -count=1` passed.
- `npm --prefix internal/frontend test` passed, 174 tests.
- `npm --prefix internal/frontend run build` passed.
- Temporary review tests were added and removed locally; they failed as described above and are not part of the working tree.

## Residual Notes

- Existing tests do not cover proxy failover on `client.Do` transport errors.
- Existing tests do not cover provider business codes serialized as strings.
- Existing tests do not cover `ActiveProviderID == ""` or disabled-active fallback behavior during failover.
