# SSE-Labeled HTTP Error Handling Review Notes

Date: 2026-07-01
Reviewers: Codex and Claude Code

## Scope

Reviewed `dd2f8bf...bc28637`, including status-first dispatch, bounded protocol-structure summaries, URL query summarization/redaction, response and heartbeat helpers, usage sanitization, rectifier behavior, production recorder wiring, and both specifications. Validation included focused security reproductions, static checks, and `make test` with the race detector.

## Key Findings And Resolutions

1. The status-first dispatch is logically correct.
   - Resolution: Final statuses `>= 400` preserve upstream status, headers, and body through the existing error observer; successful SSE responses retain heartbeat handling. No response-dispatch, rectifier, or usage-accounting regression was found.
2. Low-severity security defect: SSE-labeled HTTP errors can write top-level `system`, `metadata`, and unknown request fields to process logs (CWE-532).
   - Resolution: Resolved by `dcdc3c4`. `summarizeRequestParams` now admits only typed safe fields (`model`, `stream`, numeric generation controls) and collection counts (`messages`, `tools`, `input`). End-to-end assertions prove system prompts, metadata, credentials, unknown extensions, message content, tool content, and input content do not enter logs.
3. Regression coverage proves the reported 400/`stream=false` case but does not directly table-drive every specified 4xx/5xx and request-stream combination.
   - Resolution: Resolved by `dcdc3c4` and `b37030b`. The Handler test now covers 400/non-stream, 429/stream, and 500/stream responses; a direct summarizer test locks down the allowlist and rejects unsafe value types.
4. Collection counts alone were insufficient for Web-tool compatibility diagnosis, but expanding the log surface could not reintroduce CWE-532.
   - Resolution: `bc28637` adds fixed-allowlist role/content-type histograms, known Web-tool names, a stable tool-name digest, schema-keyword counts, and shapes for sensitive top-level fields. Focused tests prove that message text, tool input/results, arbitrary tool names/descriptions, schema content, metadata keys/values, and unknown extensions stay out of logs while large-collection output remains bounded.
5. Low-severity edge defect: the shared URL redaction helper returned its input after a parse failure, allowing a malformed URL query to reach logs (CWE-532).
   - Resolution: Resolved within `bc28637`. Proxy logs now emit the fixed `<invalid-url>` placeholder on parse failure, while query diagnostics retain only normalized beta state and the non-beta parameter count. A failing test first reproduced exposure of `token=secret-query-value`, then passed after the fix.

## Final Review Conclusion

The SSE error routing, request-summary allowlist, bounded protocol diagnostics, and URL query redaction are verified. No reproducible logic or security defect remains in the reviewed feature scope.

## Verification Evidence

- Before the allowlist fix, the focused Handler test failed and printed `secret-system-prompt` in the detailed error log.
- After the fix, `go test ./internal/proxy -run 'TestProxyRecordsSSELabeledHTTPError|TestSummarizeRequestParamsAllowsOnlySafeDiagnostics' -count=1` passed.
- `go test ./internal/proxy -count=1` passed.
- `go vet ./internal/proxy` passed.
- `make test` passed with the race detector and coverage enabled.
- `git diff --check` passed.

## Residual Notes

- The first full `make test` run hit the unrelated network-dependent `TestDownloadAndApplyRedactsInvalidDownloadURL`: a TLS timeout returned a raw URL error containing its query. The focused rerun and a second complete `make test` passed. This is pre-existing updater behavior outside this diff, but the test should be made deterministic and transport errors should be redacted consistently.
- This new conclusion is a focused security review of Task 4 log data flows. The current session does not permit subagent execution, so it does not claim an exhaustive multi-agent Codex Security diff scan.
