# SSE-Labeled HTTP Error Handling Spec

Local page: N/A
Proxy entry: `POST /v1/messages`, `POST /anthropic/v1/messages`
Reference sources: Runtime Docker logs, `data/proxy.db`, `internal/proxy/handler.go`, `internal/proxy/heartbeat.go`
Stack: Go 1.26 standard library (`net/http`, `io`, `log`) + SQLite usage recorder
Last updated: 2026-07-01
Progress: validated, 4 / 4 complete

## Overall Analysis (Source Analysis)

### Current Project State

The proxy routes upstream responses by response `Content-Type`. A response containing `text/event-stream` enters the SSE heartbeat path even when its HTTP status is an error. The SSE path observes stream usage but does not capture an error body, assign HTTP error metadata, or emit the detailed `[Proxy] Error` diagnostic line.

The normal non-SSE path already has the required error behavior:

1. It forwards the full upstream body to the client.
2. It captures a bounded copy through `responseObserver`.
3. It records `error_type=http_error` and a sanitized `error_message`.
4. It records `usage_parse_status=skipped_non_2xx`.
5. It logs compatibility headers, summarized request parameters, and the sanitized upstream response.

The reactive 400 rectifier runs before response routing. It may replace the original response with a retry response, or restore the original error body when no cleanup applies. Therefore, routing must be based on the final response returned by the rectifier.

### Runtime Evidence

The Docker instance produced two representative failures on 2026-06-30 at 21:50 and 21:58 local time:

```text
>>> POST ... stream=false ...
<<< 400 ...
[Stream] SSE stream detected ..., enabling heartbeat injection
```

Both responses forwarded 213 bytes, but the corresponding SQLite rows had empty `error_type` and `error_message`, with `usage_parse_status=missing`. Older 400 responses of the same body size routed through the normal path and were recorded as `http_error` with `skipped_non_2xx`.

This proves that the upstream body was present. The body and request summary were not logged because an HTTP error carrying an SSE media type was routed through the success-oriented streaming branch.

After the safe request-field allowlist was deployed, another Zhipu 1210 failure at 2026-07-01 03:40:57 showed the remaining operability gap:

```text
params: {"max_tokens":64000,"messages":"[82 items]","model":"glm-5.2","tools":"[11 items]"}
resp: {"type":"error","error":{"type":"invalid_request_error","code":"1210","message":"[1210][API 调用参数有误，请检查文档。][...]"}}
```

The summary proves collection sizes but cannot distinguish whether `stream` is absent or false, which tools are present, or whether tool schemas contain compatibility-relevant JSON Schema keywords. Subsequent diagnosis established that the failure followed `ToolSearch` loading `WebFetch`/`WebSearch`, and that Claude Code removed the 14-byte `"stream":true,` member during JSON fallback. Full prompt or message content was not needed; protocol structure was.

The same runtime evidence excludes the cc-switch #3090 “missing `beta=true`” failure mode for this MCC request. Both the 03:40:55 streaming URL and the 03:40:57 final 400 URL contained `?beta=true`. The ordinary `>>>`/`<<<` line omits it only because `providerLogFields` intentionally calls `config.RedactURL`, which removes every query parameter from display. The detailed error line currently prints the raw `backendURL`; that makes beta visible but can also expose unrelated query secrets. Task 4 therefore adds an allowlisted beta-state/count summary while keeping the URL itself query-redacted everywhere.

Claude Code session `202606302035` adds session-level correlation evidence. Its final three failed turns were:

```text
04:50:02 ToolSearch -> matches [WebSearch, WebFetch]
04:50:04 API 400 / 1210
04:58:13 API 400 / 1210
08:50:58 API 400 / 1210
```

For the last failure, SQLite recorded a paired request sequence: `stream=true`, 859619 request bytes, HTTP 200, 589 response bytes, `client_aborted`; then stream absent, 859605 request bytes, HTTP 400, 213 response bytes. The 14-byte request delta matches removal of `"stream":true,`. This confirms persistent correlation with the dynamically loaded Web tools and the stream-to-non-stream retry. It does not prove which tool schema field is rejected, nor what structural event in the preceding 589-byte HTTP-200 SSE response causes Claude Code to abort and retry.

### Root Cause

`isSSEStream` only checks whether the response `Content-Type` contains `text/event-stream`. `Handler.ServeHTTP` then gives this media-type result priority over `resp.StatusCode`. The routing decision incorrectly treats SSE as a complete response outcome instead of a transport representation.

HTTP error status must take precedence over media type. Heartbeat injection and SSE usage parsing are only valid for successful, non-error responses. A final response with status `>= 400` must use the error-observation path regardless of `Content-Type` or the request's `stream` value.

### Target Response Matrix

| Final response status | Response media type | Required path | Heartbeat | Error persistence and detailed log |
| --- | --- | --- | --- | --- |
| `< 400` | `text/event-stream` | SSE streaming | Enabled | No |
| `< 400` | Other | Non-streaming response | Disabled | No |
| `>= 400` | `text/event-stream` | HTTP error observation | Disabled | Yes |
| `>= 400` | Other | HTTP error observation | Disabled | Yes |

### Design Decision

Use status-first routing in `Handler.ServeHTTP`: enter the SSE branch only when the final response status is below 400 and `isSSEStream(resp)` is true. All final 4xx and 5xx responses reuse the existing non-SSE observer and error-recording path.

This approach is preferred over duplicating error capture inside the SSE branch because it keeps one canonical HTTP error path. A broader response-pipeline refactor is not required for this defect.

For error-log request diagnostics, retain secure-by-default content redaction but replace collection-only summaries with a bounded protocol-structure summary. It records field presence, safe scalar controls, collection counts, message role/content-block histograms, a one-way tool-name-set fingerprint, explicit recognition of `ToolSearch`/`WebFetch`/`WebSearch`, and aggregate schema-keyword counts. It never records prompts, message text, metadata values or keys, arbitrary tool names, tool descriptions, schema property names/descriptions, credentials, or unknown extension values. The fingerprint supports equality comparison; it is not treated as encryption or a confidentiality boundary.

The existing compatibility-header allowlist remains unchanged in this task: Anthropic version/beta and content type are retained; credentials stay masked. URL query diagnostics report only whether `beta` is absent, true, false, or another value plus the count of other parameters; no other query names or values are logged. A future authenticated, TTL-bound full-capture facility is a separate feature and cannot replace safe console diagnostics.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Completed | Make HTTP error status take precedence over SSE media type | `internal/proxy/handler.go` | Regression tests prove error body forwarding, diagnostic logging, and usage persistence |
| 2 | Completed | Verify streaming and rectifier regressions | `internal/proxy/server_test.go` and existing proxy tests | Targeted proxy tests and full Go test suite pass |
| 3 | Completed | Restrict request summaries to safe diagnostic fields | `internal/proxy/handler.go`, `internal/proxy/server_test.go` | Security reproduction fails before the fix; allowlist, negative leak checks, status matrix, and race suite pass afterward |
| 4 | Completed | Add bounded protocol-structure diagnostics | `internal/proxy/request_diagnostics.go`, focused tests, Handler log assertions | RED proves current summary lacks stream presence/tool/schema structure; GREEN retains zero secret markers and bounded output |

## Requirements

### Deliverables

1. The final upstream response enters the SSE heartbeat path only when `resp.StatusCode < 400` and `isSSEStream(resp)` is true.
2. Every final 4xx or 5xx response on a usage-recorded Messages path uses `responseObserver` even when the upstream declares `Content-Type: text/event-stream`.
3. The proxy preserves the upstream status, forwardable headers, and complete response body delivered to the client.
4. Usage persistence for the final HTTP error records:
   - `status_code` equal to the final upstream status;
   - `error_type` equal to `http_error`;
   - `error_message` equal to the sanitized, bounded captured error body;
   - `response_bytes` equal to the full forwarded body size;
   - token `usage_source` equal to `none`;
   - token `usage_parse_status` equal to `skipped_non_2xx`.
5. The proxy emits the existing detailed error log format for the final HTTP error:

   ```text
   [Proxy] Error <status> <upstream> | headers: <summary> | params: <summary> | resp: <sanitized error>
   ```

6. A final error response must not start the SSE heartbeat goroutine or emit `[Stream] SSE stream detected`.
7. A successful SSE response continues to stream, flush, inject idle heartbeats, and record SSE usage exactly as before.
8. Reactive 400 rectification behavior remains unchanged:
   - a successful retry follows the response path appropriate to the retry response;
   - an unsuccessful or inapplicable rectification forwards and records the restored final error once.
9. Detailed error logs contain a bounded, type-checked protocol-structure summary:
   - `model`, numeric generation controls, request byte length, and explicit `stream.present` plus a boolean value only when correctly typed;
   - counts and allowlisted role/content-block histograms for `messages`;
   - tool count, stable SHA-256 digest of the sorted tool-name set, exact names only for `ToolSearch`, `WebFetch`, and `WebSearch`, and aggregate counts for compatibility-relevant schema keywords;
   - type/size shapes for `system`, `metadata`, `thinking`, and `input`, without values or object keys;
   - a count of unrecognized top-level fields, without their names or values.
10. The request summary must never contain prompt/message/input text, metadata keys or values, arbitrary tool names, tool descriptions, schema property names/descriptions, credentials, authorization values, or unknown extension names/values.
11. Request diagnostic output must remain bounded independently of message/tool collection size; aggregate maps use fixed allowlists and exact known web-tool names are de-duplicated.
12. Every logged upstream URL must remain query-redacted. Separate query diagnostics may report only the normalized beta state (`absent`, `true`, `false`, or `other`) and the count of non-beta parameters.

### Files in Scope

```text
internal/proxy/
  handler.go          (modify: status-aware response routing)
  request_diagnostics.go (create in Task 4: bounded safe protocol summary)
  request_diagnostics_test.go (create in Task 4: focused redaction and structure tests)
  server_test.go      (modify: regression coverage for SSE-labeled errors)
```

`heartbeat.go` remains the media-type detector and heartbeat implementation. Its behavior does not need to change because the handler will call it only for non-error responses.

### Constraints

1. Do not change upstream request transformation, model mapping, provider selection, rate limiting, or 429 retry behavior.
2. Do not change the response body delivered to the client, including SSE-labeled JSON error bodies.
3. Do not parse an HTTP error body as token usage.
4. Preserve existing error sanitization and size limits: the client receives the complete body while persisted/logged error text remains bounded and secret-sanitized.
5. Preserve existing compatibility header summarization. Request diagnostics may record only the structural aggregates defined above; do not log raw system prompts, metadata keys/values, message/input content, arbitrary tool names, tool/schema content, credentials, authorization values, unknown extensions, or raw URL query names/values other than normalized beta state.
6. Do not add a configuration switch. Correct routing is protocol behavior, not a user preference.
7. Keep the change localized; do not refactor unrelated streaming or usage-recording code.

### Edge Cases

1. The request has `stream=false`, but a 400 response declares `text/event-stream`.
2. The request has `stream=true`, but a 400, 429, or 5xx response declares `text/event-stream`.
3. The response body is valid JSON even though the media type is SSE.
4. The response body is SSE-formatted error data. It is still forwarded unchanged and treated as an HTTP error because the status is authoritative.
5. The rectifier consumes a prefix of a 400 body and restores it because no supported pattern matches. The observer must still see and forward the complete restored body.
6. The rectifier retries a 400 and receives a successful SSE response. The retry must still use normal SSE handling.
7. The error body exceeds the observer's capture limit. The client receives the full body; only the stored/logged diagnostic copy is bounded.
8. The client disconnects while an error body is copied. Existing copy-error behavior remains in effect; no heartbeat is started.

### Non-Goals

1. Identifying which upstream request field caused provider error code 1210.
2. Changing provider-specific request cleanup or adding new rectifier patterns.
3. Changing the upstream's response headers or correcting its declared media type.
4. Persisting full request or response payloads beyond current bounded diagnostics.
5. Redesigning the usage database schema or admin usage UI.
6. Changing behavior for successful SSE event payloads that encode application-level errors under HTTP status below 400.
7. Adding the admin-controlled full request/response capture, persistence, export, retention, or deletion feature.
8. Diagnosing the event structure of status-below-400 SSE responses. The 200/589-byte `client_aborted` precursor is assigned to Task 0 of `2026-07-01-zhipu-web-tools-compat` so this branch remains limited to final HTTP errors and safe request diagnostics.

## Task Details

### Embedded Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to execute this plan task-by-task. Track progress by updating the checkboxes in this spec.

**Goal:** Preserve every final upstream HTTP error and emit enough bounded protocol structure to diagnose compatibility failures without logging request content.

**Architecture:** Keep the status-aware response pipeline. Every final 4xx or 5xx response uses the existing non-streaming observer; its request summary is produced by a separate bounded structural summarizer that aggregates only allowlisted protocol facts and never copies content-bearing values.

**Tech Stack:** Go 1.26, `net/http`, `httptest`, standard `log`, existing `fakeUsageRecorder` test helper.

### Task 1: Route Final HTTP Errors Through the Error Observer

#### Requirements

**Objective** - Ensure final upstream HTTP errors are observed, logged, and persisted even when their media type is `text/event-stream`.

**Outcomes** - `Handler.ServeHTTP` gives status `>= 400` precedence over SSE media type; the existing non-SSE error path forwards the response and records complete error metadata.

**Evidence** - A test backend returns status 400, `Content-Type: text/event-stream`, and a known error body. The client receives the exact body, the fake usage recorder contains `http_error` and `skipped_non_2xx`, and captured logs contain the request parameter summary and sanitized response.

**Constraints** - Use the existing observer, sanitizer, log formatter, and recorder. Do not duplicate HTTP error handling inside the SSE branch.

**Edge Cases** - Request stream setting does not match the response media type; rectifier restores or replaces the response; error body is larger than the diagnostic capture limit.

**Verification** - Focused handler tests prove response fidelity, absence of SSE heartbeat routing, detailed log output, and persisted error metadata.

#### Plan

**Files:**

- Modify: `internal/proxy/handler.go:262`
- Test: `internal/proxy/server_test.go:492`

- [x] **Step 1: Add the failing regression test**

  Add this test next to `TestProxyRecordsHTTPErrorAndForwardsFullBody` in `internal/proxy/server_test.go`:

  ```go
  func TestProxyRecordsSSELabeledHTTPError(t *testing.T) {
      var logBuf bytes.Buffer
      oldOutput := log.Writer()
      oldFlags := log.Flags()
      oldPrefix := log.Prefix()
      log.SetOutput(&logBuf)
      log.SetFlags(0)
      log.SetPrefix("")
      t.Cleanup(func() {
          log.SetOutput(oldOutput)
          log.SetFlags(oldFlags)
          log.SetPrefix(oldPrefix)
      })

      recorder := &fakeUsageRecorder{}
      errorBody := `{"type":"error","error":{"type":"provider_error","message":"request rejected"}}`
      backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          w.Header().Set("Content-Type", "text/event-stream")
          w.WriteHeader(http.StatusBadRequest)
          _, _ = w.Write([]byte(errorBody))
      }))
      defer backend.Close()

      handler := NewHandler(
          config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))),
          http.DefaultTransport.(*http.Transport),
          recorder,
      )
      req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
          "model":"claude-sonnet",
          "stream":false,
          "max_tokens":64,
          "messages":[{"role":"user","content":"hello"}]
      }`))
      req.Header.Set("Content-Type", "application/json")
      rec := httptest.NewRecorder()

      handler.ServeHTTP(rec, req)

      if rec.Code != http.StatusBadRequest {
          t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
      }
      if rec.Body.String() != errorBody {
          t.Fatalf("body = %q, want %q", rec.Body.String(), errorBody)
      }

      record := recorder.onlyRecord(t)
      if record.req.StatusCode == nil || *record.req.StatusCode != http.StatusBadRequest {
          t.Fatalf("StatusCode = %v", record.req.StatusCode)
      }
      if record.req.ErrorType != usage.ErrorHTTP {
          t.Fatalf("ErrorType = %q", record.req.ErrorType)
      }
      if record.req.ErrorMessage != errorBody {
          t.Fatalf("ErrorMessage = %q, want %q", record.req.ErrorMessage, errorBody)
      }
      if record.req.ResponseBytes != int64(len(errorBody)) {
          t.Fatalf("ResponseBytes = %d, want %d", record.req.ResponseBytes, len(errorBody))
      }
      if record.tok.UsageSource != usage.UsageSourceNone {
          t.Fatalf("UsageSource = %q", record.tok.UsageSource)
      }
      if record.tok.UsageParseStatus != usage.ParseStatusSkippedNon2xx {
          t.Fatalf("UsageParseStatus = %q", record.tok.UsageParseStatus)
      }

      logs := logBuf.String()
      if !strings.Contains(logs, "[Proxy] Error 400") ||
          !strings.Contains(logs, `"max_tokens":64`) ||
          !strings.Contains(logs, "resp: "+errorBody) {
          t.Fatalf("missing detailed HTTP error log:\n%s", logs)
      }
      if strings.Contains(logs, "[Stream] SSE stream detected") {
          t.Fatalf("HTTP error incorrectly entered SSE path:\n%s", logs)
      }
  }
  ```

- [x] **Step 2: Run the regression test and confirm the current failure**

  Run:

  ```bash
  go test ./internal/proxy -run '^TestProxyRecordsSSELabeledHTTPError$' -count=1
  ```

  Expected before the fix: `FAIL`; the recorded `ErrorType` is empty, `UsageParseStatus` is `missing`, and logs contain `[Stream] SSE stream detected` instead of the detailed HTTP error line.

- [x] **Step 3: Implement the minimal status-first routing change**

  In `internal/proxy/handler.go`, replace the SSE branch predicate with:

  ```go
  if resp.StatusCode < 400 && isSSEStream(resp) {
  ```

  Do not move or duplicate the existing response observer, error assignments, or `[Proxy] Error` logging block.

- [x] **Step 4: Format the touched Go files**

  Run:

  ```bash
  gofmt -w internal/proxy/handler.go internal/proxy/server_test.go
  ```

  Expected: both files are formatted with no unrelated content changes.

- [x] **Step 5: Run the regression test and confirm it passes**

  Run:

  ```bash
  go test ./internal/proxy -run '^TestProxyRecordsSSELabeledHTTPError$' -count=1
  ```

  Expected after the fix: `ok magic-claude-code/internal/proxy`; the exact error body is forwarded and all log and usage assertions pass.

- [x] **Step 6: Run focused SSE and rectifier regressions**

  Run:

  ```bash
  go test ./internal/proxy -run 'TestProxyRecordsStreamingUsage|TestProxyRecordsStreamingUsageWhenUpstreamDoesNotCloseAfterMessageStop|TestProxyRetriesKimiTool400WithCleanedRequestBody|TestProxyForwardsLargeNonRecoverable400Body' -count=1
  ```

  Expected: `ok magic-claude-code/internal/proxy`; successful SSE usage, terminal-event handling, 400 rectification, and full large-body forwarding remain unchanged.

- [x] **Step 7: Commit the implementation and regression test**

  ```bash
  git add internal/proxy/handler.go internal/proxy/server_test.go
  git commit -m "fix(proxy): record SSE-labeled HTTP errors"
  ```

#### Verification

- [x] A 400 response with `Content-Type: text/event-stream` is forwarded byte-for-byte.
- [x] The response does not produce the SSE detection log or heartbeat behavior.
- [x] The detailed error log contains `headers`, `params`, and sanitized `resp` fields.
- [x] The usage request records `http_error`, the sanitized error message, final status, and full response byte count.
- [x] The usage token record uses `none` and `skipped_non_2xx`.

### Task 2: Add Regression Coverage and Run Verification

#### Requirements

**Objective** - Prevent future response-routing changes from reintroducing missing error diagnostics or breaking successful SSE streaming.

**Outcomes** - Automated tests cover the reported failure and retain coverage for normal SSE responses and reactive 400 handling.

**Evidence** - Targeted proxy tests and `go test ./...` pass from a clean worktree; `git diff --check` reports no whitespace errors.

**Constraints** - Tests use `httptest.Server`, the existing fake usage recorder, and bounded log capture. No real provider credentials or network calls are required.

**Edge Cases** - HTTP 400 with an SSE media type is the required regression case. Additional table cases may cover 429 and 5xx if they do not obscure the original failure.

**Verification** - Run targeted and full Go test commands and record their actual results after implementation.

#### Plan

**Files:**

- Verify: `internal/proxy/handler.go`
- Verify: `internal/proxy/server_test.go`
- Update after successful verification: `sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec.md`
- Update after successful verification: `sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec_ZH.md`

- [x] **Step 1: Run the complete proxy package tests without cache**

  Run:

  ```bash
  go test ./internal/proxy -count=1
  ```

  Expected: `ok magic-claude-code/internal/proxy`.

- [x] **Step 2: Run the complete Go test suite**

  Run:

  ```bash
  go test ./...
  ```

  Expected: all Go packages pass with no failures.

- [x] **Step 3: Validate whitespace**

  Run:

  ```bash
  git diff --check
  ```

  Expected: `git diff --check` produces no output.

- [x] **Step 4: Inspect the scoped code diff**

  Run:

  ```bash
  git show --format= HEAD -- internal/proxy/handler.go internal/proxy/server_test.go
  ```

  Expected: the implementation commit contains one handler predicate change and one focused regression test, with no unrelated edits.

- [x] **Step 5: Record verification in this single-file spec pair**

  After all commands pass, update both specs in the same commit:

  - change progress to `validated, 2 / 2 complete` / `已验证，2 / 2 已完成`;
  - change both Development Checklist rows to `Completed` / `已完成`;
  - mark completed plan and verification checkboxes;
  - add the actual command outcomes and implementation commit hash without creating a separate validation or plan file.

- [x] **Step 6: Commit the verification record**

  ```bash
  git add \
    sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec.md \
    sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec_ZH.md
  git commit -m "docs: record SSE error handling verification"
  ```

#### Verification

- [x] `go test ./internal/proxy -run 'Test.*SSE.*Error|TestProxyRecordsStreamingUsage|TestProxyRetries.*400' -count=1`
- [x] `go test ./...`
- [x] `git diff --check`

#### Actual Verification Evidence

Date: 2026-06-30
Implementation commit: `43dd1f0` (`fix(proxy): record SSE-labeled HTTP errors`)

- RED: `go test ./internal/proxy -run '^TestProxyRecordsSSELabeledHTTPError$' -count=1` failed before the implementation with `ErrorType = ""`, confirming the regression test exercised the missing HTTP error path.
- GREEN: the same focused regression command passed after the status-first predicate change.
- Focused SSE and rectifier regressions passed.
- `go test ./internal/proxy -count=1` passed in 4.515 seconds.
- `go test ./...` passed for all Go packages; packages without tests reported `[no test files]`.
- `git diff --check` produced no output, and inspection of `43dd1f0` confirmed one handler predicate change plus one focused regression test.

### Task 3: Restrict Error-Log Request Summaries

#### Requirements

**Objective** - Prevent detailed HTTP error logs from copying prompt-bearing, identifying, credential, or unknown top-level request fields.

**Outcomes** - `summarizeRequestParams` uses an explicit typed allowlist; Handler-level tests prove sensitive values do not reach logs while safe diagnostic fields remain available.

**Evidence** - Before the fix, the focused Handler test reproduced `secret-system-prompt` in the process log. After the fix, the same path omits every sensitive marker for SSE-labeled 400, 429, and 500 responses.

**Constraints** - Keep the status-first SSE predicate, complete response forwarding, error persistence, rectifier behavior, and detailed response logging unchanged. Unknown fields default to omission.

**Edge Cases** - Allowlisted keys with object or string values of the wrong type; OpenAI Responses `input` arrays; stream and non-stream requests; 4xx and 5xx responses.

**Verification** - Focused security tests, the complete proxy package, and `make test` with the race detector pass.

#### Plan

**Files:**

- Modify: `internal/proxy/handler.go:742`
- Test: `internal/proxy/server_test.go`
- Archive: `sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/review-notes.md`
- Archive: `sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/review-notes_ZH.md`

- [x] Extend the Handler test with unique markers in `system`, `metadata`, a credential-shaped field, an unknown extension, and message content.
- [x] Run the focused test before the fix and confirm it fails because `secret-system-prompt` appears in `[Proxy] Error` logs.
- [x] Replace the request-summary denylist with typed allowlisting for `model`, `stream`, numeric generation controls, and collection counts.
- [x] Table-drive the Handler path across 400/non-stream, 429/stream, and 500/stream responses.
- [x] Add direct helper coverage for safe output and wrong-type omission.
- [x] Run focused tests, full proxy tests, and `make test`.
- [x] Commit code as `dcdc3c4` and direct helper coverage as `b37030b`.

#### Verification

- [x] Safe fields and collection counts remain in diagnostic logs.
- [x] System prompts, metadata, credentials, unknown extensions, message content, tool content, and input content do not appear in logs.
- [x] Wrongly typed values under allowlisted names are omitted.
- [x] SSE-labeled 400, 429, and 500 responses retain status, body, HTTP error persistence, and no-heartbeat behavior.
- [x] `go test ./internal/proxy -run 'TestProxyRecordsSSELabeledHTTPError|TestSummarizeRequestParamsAllowsOnlySafeDiagnostics' -count=1`
- [x] `go test ./internal/proxy -count=1`
- [x] `make test`

#### Actual Verification Evidence

- RED reproduced the CWE-532 path with `secret-system-prompt` present in the detailed error log.
- GREEN focused tests passed after `dcdc3c4`; direct allowlist coverage passed in `b37030b`.
- `go test ./internal/proxy -count=1` passed.
- `make test` passed with `-race` and coverage enabled.

### Task 4: Add Bounded Protocol-Structure Diagnostics

#### Requirements

**Objective** - Make compatibility errors diagnosable from the default console log without restoring raw request logging.

**Outcomes** - `summarizeRequestParams` reports whether `stream` is absent, safe message/tool structure, known Web tool presence, schema compatibility features, and fixed-size shapes for sensitive fields. URL logs expose normalized beta state without raw query data. Content-bearing values remain unavailable.

**Evidence** - A fixture shaped like the observed 1210 request produces `stream.present=false`, recognizes `WebFetch`, and reports `$schema`, `additionalProperties=false`, and nested `format` counts while every secret marker is absent.

**Constraints** - The summary is derived from the transformed upstream body already held in memory. No raw payload is persisted, no header policy changes, no configuration is added, and no rectifier behavior changes.

**Edge Cases** - Missing versus wrongly typed `stream`; string versus array message content; unknown roles/block types; duplicate known Web tools; arbitrary or secret-bearing custom tool names; deeply nested schemas; large message/tool arrays; malformed JSON; absent/repeated/non-boolean beta query values; secret-bearing non-beta query parameters.

**Verification** - RED/GREEN focused tests, Handler-level log tests, full proxy tests, and `make test` pass. The final summary remains below 4096 bytes for a synthetic large request.

#### Plan

**Files:**

- Create: `internal/proxy/request_diagnostics.go`
- Create: `internal/proxy/request_diagnostics_test.go`
- Modify: `internal/proxy/handler.go` (remove the old collection-only `summarizeRequestParams`; keep its call site)
- Modify: `internal/proxy/server_test.go` (assert structural output on the real error path)
- Update after verification: `sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec.md`
- Update after verification: `sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec_ZH.md`

- [x] **Step 1: Write the failing focused tests**

  Create `internal/proxy/request_diagnostics_test.go` with a request containing:

  ```json
  {
    "model": "glm-5.2",
    "max_tokens": 64000,
    "messages": [
      {"role":"user","content":"secret-user-text"},
      {"role":"assistant","content":[{"type":"tool_use","name":"secret-call-name","input":{"secret":"value"}}]},
      {"role":"user","content":[{"type":"tool_result","content":"secret-result"},{"type":"tool_reference","tool_name":"WebFetch"}]}
    ],
    "tools": [
      {
        "name":"WebFetch",
        "description":"secret-web-description",
        "input_schema":{
          "$schema":"https://json-schema.org/draft/2020-12/schema",
          "type":"object",
          "properties":{"url":{"type":"string","format":"uri"}},
          "required":["url"],
          "additionalProperties":false
        }
      },
      {
        "name":"secret-custom-tool",
        "description":"secret-custom-description",
        "input_schema":{"type":"object","properties":{"secret-property":{"type":"string","description":"secret-schema-description"}}}
      }
    ],
    "system":"secret-system-prompt",
    "metadata":{"secret-user-id":"secret-metadata-value"},
    "unknown_secret_extension":{"secret":"secret-extension-value"}
  }
  ```

  Parse the returned JSON and assert this exact contract:

  - `body_bytes` equals the input byte length;
  - `model` and `max_tokens` are retained;
  - `stream` equals `{"present":false}`;
  - message count is 3, roles are `user=2` and `assistant=1`, and known block types count `tool_use=1`, `tool_result=1`, `tool_reference=1`;
  - tool count is 2, `known_names` is exactly `["WebFetch"]`, and `names_sha256` is a 64-character lowercase hex digest;
  - schema aggregates report one draft-2020-12 marker, one `additionalProperties=false`, one `format`, two root property entries, and one required entry;
  - `system` is `{"type":"string","chars":20}`, `metadata` is `{"type":"object","keys":1}`, and `unknown_top_level_fields=1`;
  - none of the ten `secret-*` markers appears in the serialized summary.

  Add cases proving `stream=true`, `stream=false`, and a wrong-type stream produce respectively `{present:true,value:true}`, `{present:true,value:false}`, and `{present:true,type:"string"}` without copying the wrong value.

  Add a stable-digest test that reverses tool order and expects the same `names_sha256`. Add a large synthetic fixture with at least 500 messages and 500 tools and assert the summary is shorter than 4096 bytes and contains no generated secret marker.

  Add focused query-summary cases for no query, `beta=true`, `beta=false`, `beta=unexpected`, repeated beta values, and `beta=true&token=secret-query-value&signature=secret-signature`. Assert the output contains only normalized beta state and `other_count=2`, and never contains `token`, `signature`, or either secret value.

- [x] **Step 2: Run the focused tests and verify RED**

  ```bash
  go test ./internal/proxy -run '^TestSummarizeRequestParams(ProtocolStructure|StreamPresence|StableToolDigest|BoundsLargeCollections)$' -count=1
  ```

  Expected before implementation: FAIL because the current summary has no `body_bytes`, explicit stream presence, role/block histograms, tool digest, known Web names, schema aggregates, or sensitive-field shapes.

- [x] **Step 3: Implement the bounded structural summarizer**

  Create `internal/proxy/request_diagnostics.go` with these private responsibilities:

  ```go
  func summarizeRequestParams(body []byte) string
  func summarizeValueShape(value any) map[string]any
  func summarizeMessages(value any) map[string]any
  func summarizeTools(value any) map[string]any
  func collectSchemaDiagnostics(value any, depth int, stats map[string]int)
  func digestToolNames(names []string) string
  func summarizeUpstreamQuery(rawURL string) string
  ```

  Use fixed allowlists:

  ```go
  knownToolNames := {"ToolSearch", "WebFetch", "WebSearch"}
  knownRoles := {"user", "assistant", "system", "tool"}
  knownContentTypes := {
      "text", "image", "document", "tool_use", "tool_result",
      "thinking", "redacted_thinking", "server_tool_use", "tool_reference",
  }
  schemaKeywords := {"$schema", "additionalProperties", "format", "minLength", "maxLength", "oneOf", "anyOf", "allOf", "$ref"}
  ```

  Implementation rules:

  1. Preserve the current malformed-JSON fallback `<N bytes, not JSON>`.
  2. Retain only correctly typed `model` and numeric generation controls.
  3. Always emit `stream.present`; emit `value` only for a bool, otherwise only a JSON type label.
  4. Count only allowlisted roles and content-block types; aggregate every other string as `other` without retaining it.
  5. Hash the sorted complete tool-name list with SHA-256 using length-prefixed entries. Emit exact names only when present in `knownToolNames`, sorted and de-duplicated.
  6. Traverse schema maps/arrays to a maximum depth of 32. Count only fixed keyword categories and root `properties`/`required` sizes; never retain property names, descriptions, enum values, defaults, examples, regex patterns, or arbitrary strings.
  7. Summarize `system`, `metadata`, `thinking`, and `input` only as JSON type plus string character count, array item count, or object key count.
  8. Count all top-level keys outside the safe scalar/collection/shape allowlists as `unknown_top_level_fields`; do not retain their names.
  9. Use only fixed-size maps, counters, one digest, and the three-name known-tool set so output size is independent of input collection cardinality.
  10. Parse the upstream URL and summarize query state as `beta=absent|true|false|other` plus `other_count=N`. Count but never retain non-beta parameter names/values. Treat repeated or mixed beta values as `other`.

  Remove the old `summarizeRequestParams` definition from `handler.go`. Change the detailed error log to use `redactUpstreamURL(backendURL)` plus `summarizeUpstreamQuery(backendURL)` rather than printing raw `backendURL`. Append the same normalized beta/other-count fields to `providerLogFields` so ordinary `>>>`/`<<<` lines distinguish query redaction from query absence.

- [x] **Step 4: Format and verify GREEN**

  ```bash
  gofmt -w internal/proxy/request_diagnostics.go internal/proxy/request_diagnostics_test.go internal/proxy/handler.go
  go test ./internal/proxy -run '^TestSummarizeRequestParams(ProtocolStructure|StreamPresence|StableToolDigest|BoundsLargeCollections)$' -count=1
  ```

  Expected: `ok magic-claude-code/internal/proxy`.

- [x] **Step 5: Update Handler-level security assertions**

  Extend `TestProxyRecordsSSELabeledHTTPError` and `TestSummarizeRequestParamsAllowsOnlySafeDiagnostics` in `internal/proxy/server_test.go` to assert the new structural fields appear on 400, 429, and 500 error logs while all existing secret markers, custom tool names, descriptions, schema property names, metadata keys, and unknown extension names remain absent.

  Make the test request URL include `?beta=true&token=secret-query-value`. Assert ordinary and detailed error logs contain the redacted upstream URL, `beta=true`, and `other_count=1`, while neither the raw query nor the `token` name/value appears.

  Run:

  ```bash
  go test ./internal/proxy -run 'TestProxyRecordsSSELabeledHTTPError|TestSummarizeRequestParamsAllowsOnlySafeDiagnostics' -count=1
  ```

  Expected: PASS with unchanged response status/body, usage persistence, and no-heartbeat behavior.

- [x] **Step 6: Run regression verification**

  ```bash
  go test ./internal/proxy -count=1
  make test
  git diff --check
  ```

  Expected: all commands pass; `make test` includes the race detector and coverage.

- [x] **Step 7: Record evidence and commit**

  Update both specs to 4 / 4 complete, check every Task 4 item, record RED/GREEN command evidence and the implementation commit, and request a fresh security review of the expanded diagnostic surface.

  ```bash
  git add internal/proxy/request_diagnostics.go \
    internal/proxy/request_diagnostics_test.go \
    internal/proxy/handler.go \
    internal/proxy/server_test.go \
    sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec.md \
    sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec_ZH.md
  git commit -m "fix(proxy): add safe protocol diagnostics"
  ```

#### Verification

- [x] Missing, false, true, and wrongly typed `stream` states are distinguishable.
- [x] Tool composition changes are comparable by digest and known Web tools are visible.
- [x] Compatibility-relevant schema features are counted without retaining schema content.
- [x] Message structure is visible without message text, tool input, or tool result content.
- [x] System, metadata, thinking, input, and unknown fields reveal shape/count only.
- [x] Summary size remains bounded for large collections.
- [x] Existing response, usage, heartbeat, rectifier, and security behavior remains unchanged.
- [x] Logs distinguish beta presence from URL redaction without exposing other query parameters.
- [x] Focused tests, full proxy tests, `make test`, and fresh security review pass.

#### Actual Verification Evidence

- RED: `go test ./internal/proxy -run '^TestSummarizeRequestParams(ProtocolStructure|StreamPresence|StableToolDigest|BoundsLargeCollections)$' -count=1` failed, proving the old summary lacked `body_bytes`, stream presence, tool fingerprints, and schema structure.
- Query RED: `go test ./internal/proxy -run '^TestSummarizeUpstreamQuery$' -count=1` failed because the query summarizer did not exist.
- Handler RED: focused tests reproduced raw query exposure and missing structural fields in ordinary, SSE, and detailed error logs.
- GREEN: the focused structural-summary, query-summary, and Handler security assertions all passed.
- The security review additionally found that a malformed URL could be returned verbatim by the shared redaction helper after parse failure. `TestRedactUpstreamURL` reproduced it first; the proxy log fallback now emits the fixed `<invalid-url>` placeholder and the test passes.
- `go test ./internal/proxy -count=1`, `go vet ./internal/proxy`, `make test`, and `git diff --check` all passed. `make test` enabled the race detector and coverage.
- Implementation commit: `bc28637` (`fix(proxy): add safe protocol diagnostics`).
- The fresh focused security conclusion is archived in `review-notes_ZH.md` and `review-notes.md`; no reproducible logic or security defect remains.
