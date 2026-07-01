# SSE-Labeled HTTP Error Handling Spec

Local page: N/A
Proxy entry: `POST /v1/messages`, `POST /anthropic/v1/messages`
Reference sources: Runtime Docker logs, `data/proxy.db`, `internal/proxy/handler.go`, `internal/proxy/heartbeat.go`
Stack: Go 1.26 standard library (`net/http`, `io`, `log`) + SQLite usage recorder
Last updated: 2026-06-30
Progress: draft, 0 / 2 planned

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

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | Make HTTP error status take precedence over SSE media type | `internal/proxy/handler.go` | Regression tests prove error body forwarding, diagnostic logging, and usage persistence |
| 2 | Planned | Verify streaming and rectifier regressions | `internal/proxy/server_test.go` and existing proxy tests | Targeted proxy tests and full Go test suite pass |

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

### Files in Scope

```text
internal/proxy/
  handler.go          (modify: status-aware response routing)
  server_test.go      (modify: regression coverage for SSE-labeled errors)
```

`heartbeat.go` remains the media-type detector and heartbeat implementation. Its behavior does not need to change because the handler will call it only for non-error responses.

### Constraints

1. Do not change upstream request transformation, model mapping, provider selection, rate limiting, or 429 retry behavior.
2. Do not change the response body delivered to the client, including SSE-labeled JSON error bodies.
3. Do not parse an HTTP error body as token usage.
4. Preserve existing error sanitization and size limits: the client receives the complete body while persisted/logged error text remains bounded and secret-sanitized.
5. Preserve existing compatibility header and request-parameter summarization; do not add raw message content, tool schemas, credentials, or authorization values to logs.
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

## Task Details

### Task 1: Route Final HTTP Errors Through the Error Observer

#### Requirements

**Objective** - Ensure final upstream HTTP errors are observed, logged, and persisted even when their media type is `text/event-stream`.

**Outcomes** - `Handler.ServeHTTP` gives status `>= 400` precedence over SSE media type; the existing non-SSE error path forwards the response and records complete error metadata.

**Evidence** - A test backend returns status 400, `Content-Type: text/event-stream`, and a known 213-byte error body. The client receives the exact body, the fake usage recorder contains `http_error` and `skipped_non_2xx`, and captured logs contain the request parameter summary and sanitized response.

**Constraints** - Use the existing observer, sanitizer, log formatter, and recorder. Do not duplicate HTTP error handling inside the SSE branch.

**Edge Cases** - Request stream setting does not match the response media type; rectifier restores or replaces the response; error body is larger than the diagnostic capture limit.

**Verification** - Focused handler tests prove response fidelity, absence of SSE heartbeat routing, detailed log output, and persisted error metadata.

#### Plan

1. Change the SSE branch predicate so it requires both a non-error final status and an SSE media type.
2. Leave response header forwarding and status writing unchanged.
3. Reuse the existing non-SSE `responseObserver` path for every final status `>= 400`.
4. Confirm the existing error log and usage-recording assignments execute for an SSE-labeled 400.

#### Verification

- [ ] A 400 response with `Content-Type: text/event-stream` is forwarded byte-for-byte.
- [ ] The response does not produce the SSE detection log or heartbeat behavior.
- [ ] The detailed error log contains `headers`, `params`, and sanitized `resp` fields.
- [ ] The usage request records `http_error`, the sanitized error message, final status, and full response byte count.
- [ ] The usage token record uses `none` and `skipped_non_2xx`.

### Task 2: Add Regression Coverage and Run Verification

#### Requirements

**Objective** - Prevent future response-routing changes from reintroducing missing error diagnostics or breaking successful SSE streaming.

**Outcomes** - Automated tests cover the reported failure and retain coverage for normal SSE responses and reactive 400 handling.

**Evidence** - Targeted proxy tests and `go test ./...` pass from a clean worktree; `git diff --check` reports no whitespace errors.

**Constraints** - Tests use `httptest.Server`, the existing fake usage recorder, and bounded log capture. No real provider credentials or network calls are required.

**Edge Cases** - HTTP 400 with an SSE media type is the required regression case. Additional table cases may cover 429 and 5xx if they do not obscure the original failure.

**Verification** - Run targeted and full Go test commands and record their actual results after implementation.

#### Plan

1. Add a regression test that returns an SSE-labeled 400 body and invokes the proxy with a Messages request.
2. Assert exact status and body forwarding.
3. Assert usage request and token error fields.
4. Capture logs and assert the detailed error line is present while the SSE detection line is absent.
5. Run existing successful SSE and rectifier tests.
6. Run the full Go test suite and whitespace validation.

#### Verification

- [ ] `go test ./internal/proxy -run 'Test.*SSE.*Error|TestProxyRecordsStreamingUsage|TestProxyRetries.*400' -count=1`
- [ ] `go test ./...`
- [ ] `git diff --check`
