# SSE-Labeled HTTP Error Handling Review Notes

Date: 2026-07-01
Reviewers: Codex and Claude Code

## Scope

Reviewed `dd2f8bf...b37030b`, including the complete changed Go files, bilingual specifications, response/heartbeat helpers, usage sanitization, rectifier behavior, production recorder wiring, and the request-summary security fix. Validation included a focused security reproduction and `make test` with the race detector.

## Key Findings And Resolutions

1. The status-first dispatch is logically correct.
   - Resolution: Final statuses `>= 400` preserve upstream status, headers, and body through the existing error observer; successful SSE responses retain heartbeat handling. No response-dispatch, rectifier, or usage-accounting regression was found.
2. Low-severity security defect: SSE-labeled HTTP errors can write top-level `system`, `metadata`, and unknown request fields to process logs (CWE-532).
   - Resolution: Resolved by `dcdc3c4`. `summarizeRequestParams` now admits only typed safe fields (`model`, `stream`, numeric generation controls) and collection counts (`messages`, `tools`, `input`). End-to-end assertions prove system prompts, metadata, credentials, unknown extensions, message content, tool content, and input content do not enter logs.
3. Regression coverage proves the reported 400/`stream=false` case but does not directly table-drive every specified 4xx/5xx and request-stream combination.
   - Resolution: Resolved by `dcdc3c4` and `b37030b`. The Handler test now covers 400/non-stream, 429/stream, and 500/stream responses; a direct summarizer test locks down the allowlist and rejects unsafe value types.

## Final Review Conclusion

The SSE error-routing fix and the request-summary allowlist are both verified. No logic or security defect remains in the reviewed feature scope.

## Verification Evidence

- Before the allowlist fix, the focused Handler test failed and printed `secret-system-prompt` in the detailed error log.
- After the fix, `go test ./internal/proxy -run 'TestProxyRecordsSSELabeledHTTPError|TestSummarizeRequestParamsAllowsOnlySafeDiagnostics' -count=1` passed.
- `go test ./internal/proxy -count=1` passed.
- `make test` passed with the race detector and coverage enabled.

## Residual Notes

- The first full `make test` run hit the unrelated network-dependent `TestDownloadAndApplyRedactsInvalidDownloadURL`: a TLS timeout returned a raw URL error containing its query. The focused rerun and a second complete `make test` passed. This is pre-existing updater behavior outside this diff, but the test should be made deterministic and transport errors should be redacted consistently.
