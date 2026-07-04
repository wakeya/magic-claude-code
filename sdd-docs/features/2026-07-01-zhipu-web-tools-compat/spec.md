# Zhipu Web Tool Compatibility Recovery Spec

Proxy entry: `POST /v1/messages`, `POST /anthropic/v1/messages`
Observed client: Claude Code 2.1.196
Observed provider: Zhipu Anthropic-compatible endpoint (`/api/anthropic/v1/messages`)
Stack: Go 1.26, `net/http`, existing reactive rectifier
Last updated: 2026-07-04
Progress: `fix/sse-error-handling` merged into main; all tasks complete — Task 0 safe SSE anomaly diagnostics (commit `b3931c8`), Task 1 red tests, Task 2 1210 classification (commit `439b1ed`), Task 3 regression verified (`make test -race` green); follow-up fix `cb1dd2f` reordered the 1210 classification ahead of the generic invalid-request fallback after review (see Task 2 follow-up); live provider verification skipped by design.

## Overall Analysis (Source Analysis)

### Observed Failure Sequence

The failure is correlated with dynamically loading Claude Code's built-in `WebFetch` or `WebSearch` tool, but it occurs before either tool executes:

1. Claude Code sends a normal streaming request and receives a successful model response containing a `ToolSearch` call.
2. Local `ToolSearch` returns a `tool_reference` for `WebFetch` or `WebSearch`.
3. The next upstream request contains the selected tool definition.
4. The provider answers the `stream: true` request with HTTP 200, an SSE media type, and a short 589-603 byte response whose event structure is not currently recorded.
5. Claude Code falls back by sending the same request again without the `stream` member. The observed request is exactly 14 bytes shorter, matching removal of `"stream":true,`.
6. The provider rejects the non-streaming request with HTTP 400 and structured error code `1210`:

```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "code": "1210",
    "message": "[1210][API 调用参数有误，请检查文档。][request-id]"
  }
}
```

A local fake Anthropic endpoint reproduced Claude Code's fallback deterministically: returning an unusable HTTP 200 response to a `stream: true` request caused Claude Code to issue a second request with the `stream` member absent. This confirms that the two upstream requests can be one client operation, not two user turns. It does not prove that the real 589-603 byte SSE-labeled provider response was JSON or identify the event that triggered the fallback.

### Session and Request-Pair Evidence

Claude Code session `202606302035` ended with this structural timeline:

```text
04:50:02 ToolSearch -> matches [WebSearch, WebFetch]
04:50:04 API 400 / 1210
04:58:13 API 400 / 1210
08:50:58 API 400 / 1210
```

The last failure has a corresponding SQLite pair:

| Attempt | Stream field | Request bytes | HTTP status | Response bytes | Recorded outcome |
| --- | --- | ---: | ---: | ---: | --- |
| first | `true` | 859619 | 200 | 589 | `client_aborted` |
| fallback | absent | 859605 | 400 | 213 | `http_error`, code 1210 |

The 14-byte request delta matches removal of `"stream":true,`. The first 400 immediately follows dynamic loading of both Web tools; the next two turns fail without another ToolSearch, showing that the loaded definitions remain in subsequent requests. An earlier independent session selected only `WebFetch` and then failed, so the evidence is not limited to provider-native WebSearch behavior.

Both actual upstream URLs contained `?beta=true`. Ordinary `>>>`/`<<<` lines hide query strings through `config.RedactURL`; the detailed error line confirmed beta was present. Therefore cc-switch #3090's missing-beta failure mode does not explain these MCC failures.

Current confidence:

| Statement | Status |
| --- | --- |
| Web tool loading precedes and persists across the failed turns | Confirmed |
| Claude Code retries without the 14-byte stream member | Confirmed |
| `beta=true` is present on the failing MCC request | Confirmed |
| The first HTTP-200 SSE response is structurally valid and complete | Unknown |
| `$schema` or `additionalProperties` is the exact rejected field | Unknown |

### Captured Tool Schema Shape

A privacy-preserving local capture recorded only tool names, object keys, and schema structure. No real conversation, credential, system prompt, metadata, or tool description was persisted.

`WebFetch` includes:

```json
{
  "name": "WebFetch",
  "input_schema": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "properties": {
      "url": {"type": "string", "format": "uri"},
      "prompt": {"type": "string"}
    },
    "required": ["url", "prompt"],
    "additionalProperties": false
  }
}
```

`WebSearch` includes the same root `$schema` and `additionalProperties` members, with `query`, `allowed_domains`, and `blocked_domains` properties. The exact individual field rejected by the provider has not been proven. The common root metadata is, however, already covered by the repository's established tool compatibility cleanup.

### Existing Recovery Gap

`internal/proxy/rectifier.go` already provides the required safe transformation:

- `cleanTools` removes root `$schema`, `$id`, `$comment`, and `additionalProperties` from each tool's `input_schema`.
- It also removes tool-level `cache_control` and repairs missing or empty schemas.
- `RectifyRequest` reports `applied=false` when no tool definition can be changed, preventing a retry.

The gap is error classification. `matchErrorPattern` does not recognize Zhipu's structured code `1210` or the phrase `API 调用参数有误`, so the existing cleanup never runs.

### Selected Design

Use two phases. First, add privacy-safe structural diagnostics for anomalous SSE responses so the 589-byte HTTP-200 precursor can be classified without recording generated text or thinking. Only after that evidence is available should the existing reactive recovery be expanded.

The proposed recovery phase remains request-dependent:

Use reactive, request-dependent recovery:

1. Recognize a structured `error.code == "1210"`, with the exact `[1210]` / `API 调用参数有误` message form as a compatibility fallback.
2. Classify that response as a tool-cleanup candidate only after higher-specificity tool, thinking/signature, and content-block patterns have had priority.
3. Pass the original request through the existing `PatternToolValidation` cleanup.
4. Retry only if `cleanTools` actually changes the request.
5. Preserve the existing one-retry limit and final response handling.

This design is provider-URL agnostic, does not mutate successful requests, and does not retry opaque 1210 responses when the request has no cleanable tool schema.

### Rejected Alternatives

1. **Proactively clean every request.** This removes the first 400 but changes requests sent to providers that already support the full Anthropic schema.
2. **Match `bigmodel.cn` and rewrite provider requests.** This couples protocol behavior to hostnames and misses compatible gateways or alternate Zhipu domains.
3. **Treat every Chinese parameter error as a generic retry.** This can retry unrelated invalid model, token, or message parameters without a request-dependent guard.

### Dependency on the Pending SSE Branch

Implementation must not be added to the current `fix/sse-error-handling` worktree while its review and merge disposition remain unresolved. That branch changes status-first response dispatch, error observation, and related proxy tests on the same 400 path.

Before implementation:

1. Resolve whether `fix/sse-error-handling` is merged, revised, or abandoned.
2. Start from the resulting mainline state.
3. Re-read `handler.go` and the 400 integration tests before applying this plan.
4. Execute Task 0 first and review the anomalous SSE structure before enabling Tasks 1-3.
5. Keep the recovery phase limited to rectifier classification and tests unless Task 0 proves a separate HTTP-200 SSE defect requires a revised spec.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Complete | Diagnose ToolSearch/WebFetch/WebSearch sequence | Sanitized request-shape and fallback evidence in this spec | Local fake endpoint reproduced stream fallback |
| 2 | Complete | Approve minimal reactive design | Error-code classification plus existing tool cleanup | User approved on 2026-07-01 |
| 3 | Complete | Resolve pending SSE branch disposition | Stable mainline base | `fix/sse-error-handling` merged into main |
| 4 | Complete | Capture anomalous SSE structure safely | `internal/usage/sse.go`, proxy anomaly log and tests | Synthetic incomplete/error stream produces structure-only evidence; no content leakage (commit `b3931c8`) |
| 5 | Complete | Review Task 0 evidence gate | Recorded decision in this spec | GO — see Evidence Gate Decision under Task 0 |
| 6 | Complete | Add failing unit and proxy regression tests | `rectifier_test.go`, `server_test.go` | RED confirmed (3 pass / 6 fail for the right reason); turned GREEN in Task 2 (commit `439b1ed`) |
| 7 | Complete | Recognize opaque Zhipu parameter errors safely | `rectifier.go` | `isOpaqueToolCompatibilityError` added after higher-priority classifications; recovery only when `cleanTools` changes the body (commit `439b1ed`) |
| 8 | Complete | Run full regression and record evidence | Updated progress and verification sections | `make test -race` passes (exit 0); no regressions in Kimi/thinking/content-type/SSE/usage suites |
| 9 | Complete | Fix 1210 classification ordering vs generic invalid-request phrases | `rectifier.go`, `rectifier_test.go` | Split `isUnsupportedContentTypePhrase` ahead of 1210; 1210 now precedes the generic invalid-request fallback (commit `cb1dd2f`) |

## Requirements

### Functional Requirements

1. An anomalous SSE response must expose a bounded structural diagnostic containing byte count, completion state, parse-error count, allowlisted event/content-block counters, allowlisted stop reason, and numeric error code when present.
2. SSE diagnostics must be emitted only for an interrupted, incomplete, parse-failed, or explicit error-event stream; normal completed SSE responses must not add anomaly logs.
3. A 400 response with structured `error.code` equal to string `1210` must become a tool-compatibility cleanup candidate only after the Task 0 evidence gate confirms the recovery target remains valid.
4. The known message form containing `[1210]` and `API 调用参数有误` must be recognized when the structured code is absent or cannot be extracted.
5. Existing higher-specificity classifications for explicit tool errors, thinking/signature errors, and unsupported content blocks must retain their current priority.
6. The candidate request must pass through the existing `cleanTools` behavior; no new proactive request transform is introduced.
7. A retry occurs only when cleanup reports an actual change.
8. A request without tools, or with tools containing no cleanable compatibility fields, must not be retried solely because the provider returned 1210.
9. The retry must preserve model mapping, messages, core tool names/descriptions/properties/required fields, headers, authentication behavior, and endpoint.
10. Each client request remains limited to one rectifier retry.
11. If the retry fails, the retry response remains the final response, consistent with current rectifier semantics.

### Security and Privacy Requirements

1. Do not log or persist raw request bodies, system prompts, messages, metadata, tool descriptions, tool schemas, authorization values, or credentials.
2. Do not include provider request IDs in new log messages beyond the already-sanitized upstream response handling.
3. Do not broaden request-parameter logging as part of this feature.
4. Error detection must use bounded error-body data already read by `tryRectify`; do not add a second unbounded read.
5. Live provider verification is optional and requires explicit approval because it consumes quota and transmits a request externally. Automated acceptance must use `httptest` fixtures.
6. SSE diagnostics must never retain or log text/thinking deltas, tool input/result content, error messages, arbitrary event/type strings, response headers outside the existing allowlist, or raw response payloads.

### Constraints

1. Do not add a provider hostname check or configuration switch.
2. Do not change Claude Code's stream fallback behavior.
3. Task 0 may extend SSE observation metadata, but must not change heartbeat timing, response-body forwarding, usage values/status, 429 retry, provider selection, or model mapping.
4. Do not remove nested property constraints such as `format: "uri"` or `minLength` unless a separate failing fixture proves they are incompatible.
5. Do not alter successful first-attempt requests.
6. Do not implement this feature until the pending SSE branch disposition is resolved.

### Edge Cases

1. `error.code` is absent but the exact bracketed 1210 message is present.
2. An unrelated error message happens to contain the digits `1210`; it must not match without the structured code or exact marker.
3. A 1210 response accompanies a request with no `tools` array; cleanup makes no change and no retry occurs.
4. A 1210 response accompanies tools whose schemas already contain only supported core fields; cleanup makes no change and no retry occurs.
5. The request contains both cleanable tool metadata and thinking history; the 1210 fallback performs only tool cleanup, not speculative thinking cleanup.
6. The retry returns successful SSE, successful JSON, another 400, or a transport error; existing handler semantics remain authoritative.
7. The first 400 body exceeds the rectifier observation limit; full client forwarding behavior remains governed by the resolved SSE/error branch.
8. The HTTP-200 SSE stream closes without `message_stop`, contains malformed JSON, emits an explicit error event, or is interrupted by the client.

### Non-Goals

1. Proving which one of `$schema` or `additionalProperties` is rejected by every Zhipu deployment.
2. Preventing Claude Code from performing its stream-to-non-stream fallback.
3. Supporting provider-native web search or web fetch server tools.
4. General schema downgrading for all JSON Schema 2020-12 keywords.
5. Adding provider-specific UI or configuration.
6. Implementing or merging the pending SSE error-handling branch.
7. Logging successful SSE response content or adding a general-purpose response capture facility.

### Acceptance Criteria

1. A synthetic incomplete/error SSE fixture emits bounded event structure and no content markers; a normal completed SSE fixture emits no anomaly log.
2. Task 0 evidence and the recovery go/no-go decision are recorded before rectifier implementation.
3. The exact captured Zhipu error fixture is classified as a tool cleanup candidate after the evidence gate.
4. WebFetch and WebSearch request fixtures are retried once after root `$schema` and `additionalProperties` are removed.
5. Core schema fields, including WebFetch `format: "uri"` and WebSearch `minLength`, remain intact.
6. A 1210 fixture with no cleanable tool fields is forwarded without retry.
7. Existing Kimi, thinking/signature, unknown-content, large-400-body, SSE-status, heartbeat, and usage tests continue to pass.
8. `make test` passes on the resolved mainline base.

## Task Details

### Task 0: Observe Anomalous SSE Structure Without Content

#### Requirements

**Objective** - Identify why the short HTTP-200 SSE response is aborted before making the 1210 recovery assumption executable.

**Outcomes** - The existing SSE observer exposes fixed, bounded structural diagnostics; the proxy emits one correlated anomaly line only for interrupted, incomplete, parse-failed, or explicit-error streams.

**Evidence** - Synthetic incomplete/error streams report event counts, completion, parse failures, stop reason, numeric error code, bytes, and the safe request structure while secret text/thinking/error messages remain absent. Completed streams remain quiet.

**Constraints** - Task 4 of `fix/sse-error-handling` must be resolved first so anomaly logs can reuse its safe request summary. Do not buffer or persist raw SSE payloads and do not change stream forwarding or usage results.

**Edge Cases** - Events split across chunks; CRLF delimiters; `[DONE]`; EOF without `message_stop`; malformed data JSON; explicit `event: error`; arbitrary event/type/error strings; numeric versus nonnumeric provider error codes; client disconnect after partial output.

**Verification** - Focused observer and handler tests pass; the anomaly line is bounded and free of synthetic secret markers; the existing SSE suite and `make test` remain green.

#### Plan

- [x] Confirm the SSE branch has been resolved and its safe protocol-summary Task 4 is present on the implementation base.
- [x] Add RED tests to `internal/usage/sse_test.go` for a stream containing `message_start`, `content_block_start`, `content_block_delta`, `message_delta`, and an explicit `error` event without `message_stop`. Include unique strings in text, thinking, tool input, and `error.message`. Assert the desired diagnostic contract:

```go
type SSEDiagnostics struct {
    Complete          bool           `json:"complete"`
    ParseErrors       int            `json:"parse_errors"`
    Events            map[string]int `json:"events"`
    ContentBlockTypes map[string]int `json:"content_block_types"`
    StopReasons       map[string]int `json:"stop_reasons"`
    ErrorEvents       int            `json:"error_events"`
    ErrorTypes        map[string]int `json:"error_types"`
    NumericErrorCodes map[string]int `json:"numeric_error_codes"`
}
```

  Expected fixed categories:

```text
events: message_start, content_block_start, content_block_delta,
        content_block_stop, message_delta, message_stop, error, ping, other
content_block_types: text, thinking, redacted_thinking, tool_use,
                     server_tool_use, web_search_tool_result, other
stop_reasons: end_turn, max_tokens, tool_use, stop_sequence,
              pause_turn, refusal, model_context_window_exceeded, other
error_types: invalid_request_error, api_error, authentication_error,
             permission_error, rate_limit_error, overloaded_error, other
```

  Only 1-8 digit numeric error codes may appear as keys; nonnumeric or oversized codes increment an `other` counter rather than being retained. Assert the serialized diagnostics contain no supplied content strings, arbitrary event/type values, or error message.

- [x] Add tests proving a complete `message_stop`/`[DONE]` stream sets `Complete=true`, chunk boundaries do not change counters, malformed data increments `ParseErrors`, and every counter map remains bounded to its fixed categories.
- [x] Run and verify RED:

```bash
go test ./internal/usage -run '^TestSSEObserverDiagnostics' -count=1
```

Expected: FAIL because `SSEDiagnostics` and `Diagnostics()` do not exist.

- [x] Extend `internal/usage/sse.go` so `SSEObserver.observeBlock` updates diagnostics while it performs its existing usage parsing. Add:

```go
func (o *SSEObserver) Diagnostics() SSEDiagnostics
```

  Parse only the existing event name plus these structural JSON fields: top-level `type`; `content_block.type`; `delta.stop_reason`; `error.type`; `error.code`. Map every non-allowlisted string to `other`. Never retain `text`, `thinking`, `input`, result content, `error.message`, raw data lines, or headers. Return copies of maps so callers cannot mutate observer state.

- [x] Run the focused observer tests and verify GREEN.
- [x] Add RED proxy tests in `internal/proxy/server_test.go`:
  1. an HTTP-200 SSE backend emits a short explicit error event containing code `1210` and secret message, then closes without `message_stop`; assert exactly one `[Stream] anomaly` log contains the request ID, response byte count, `complete=false`, `error_events=1`, numeric code `1210`, and the safe request diagnostic, but not the secret message or payload;
  2. a normal completed SSE response emits no anomaly line;
  3. a client-aborted stream emits the same structural line while retaining existing `client_aborted` usage behavior.
- [x] Extend `streamUsageObserver` in `internal/proxy/handler.go` with:

```go
func (o *streamUsageObserver) Diagnostics() usage.SSEDiagnostics
```

  Store the result of `copyWithHeartbeatAndObserver` in `streamErr`. After copying, emit one bounded anomaly line when `streamErr != nil`, `!streamObserver.IsComplete()`, `ParseErrors > 0`, or `ErrorEvents > 0`. Marshal only `SSEDiagnostics`, response bytes, and the safe request summary from the resolved SSE branch. Preserve current error assignment: only an actual copy/client error sets `usage.ErrorClientAborted`; clean EOF without a terminal event is diagnostic but not relabeled as client abort.
- [x] Run focused proxy and existing SSE tests:

```bash
go test ./internal/proxy -run 'TestProxyLogsAnomalousSSEStructure|TestProxyDoesNotLogCompletedSSEAsAnomaly|TestProxyRecordsStreamingUsage|TestProxyRecordsStreamingUsageWhenUpstreamDoesNotCloseAfterMessageStop' -count=1
```

Expected: PASS with byte-for-byte response forwarding and unchanged usage results.

- [x] Run `go test ./internal/usage -count=1`, `go test ./internal/proxy -count=1`, and `make test`.
- [x] Commit Task 0 independently:

```bash
git add internal/usage/sse.go internal/usage/sse_test.go \
  internal/proxy/handler.go internal/proxy/server_test.go
git commit -m "feat(proxy): log safe SSE anomaly structure"
```

- [x] With explicit approval for quota-consuming live verification, reproduce one failed Web tool turn and record only the anomaly JSON, paired request sizes/statuses, and commit hash in this spec. If no live run is approved, mark the evidence gate pending and do not start Task 1.
- [x] Record a go/no-go decision: continue Tasks 1-3 only if the captured structure supports tool/request incompatibility recoverable by rectification; otherwise revise this spec around the observed HTTP-200 SSE defect before coding recovery.

#### Verification

- [x] Diagnostics are fixed-size structural metadata, not response capture.
- [x] Anomalous streams are logged once and normal complete streams remain quiet.
- [x] Text, thinking, tool data, arbitrary strings, and error messages never appear.
- [x] Response forwarding, heartbeat completion, usage values/status, and client-abort classification remain unchanged.
- [x] The Task 0 evidence gate is recorded before Task 1 begins.

#### Evidence Gate Decision (2026-07-01, commit `b3931c8`)

**Live verification:** Not performed by design. Live provider verification requires explicit approval (it consumes quota and transmits requests externally); none was granted, so synthetic `httptest` fixtures are used per the task boundary.

**Decision: GO — proceed to Tasks 1-3.**

The recovery target is the structured `error.code == "1210"` 400 response (the fallback request), not the HTTP-200 SSE precursor. That 400 body and the paired request sizes/statuses are already captured in the Source Analysis above, and the request carrying WebFetch/WebSearch definitions includes root `$schema` + `additionalProperties` (Captured Tool Schema Shape), which the existing `cleanTools` removes while preserving `format`, `minLength`, `properties`, and `required`. Task 0 added the safe diagnostics infrastructure and verified with synthetic fixtures that:

- an explicit `event: error` SSE stream with code `1210` produces exactly one bounded `[Stream] anomaly` line containing `complete=false`, `error_events=1`, numeric code `1210`, and the safe request summary, with no text/thinking/error-message/raw-payload leakage;
- a normal completed stream stays quiet;
- a simulated client disconnect still emits the anomaly while retaining `ErrorClientAborted`;
- `make test -race` passes; response forwarding, heartbeat, usage values/status, and client-abort classification are unchanged.

**Residual limitation:** The real 589-603 byte HTTP-200 SSE precursor structure remains uncaptured. The new diagnostics will record it safely if a live run is later approved. This does not block the 1210 recovery, which targets the 400 response that is already recorded.

### Task 1: Add Red Tests for Zhipu 1210 Tool Recovery

#### Requirements

**Objective** - Reproduce the missing error classification and end-to-end retry behavior before changing production code.

**Outcomes** - Unit fixtures cover structured and message-only 1210 errors; proxy fixtures cover WebFetch, WebSearch, and the no-cleanup guard.

**Evidence** - Targeted tests fail because `matchErrorPattern` returns `PatternNone` and the proxy makes only one upstream request for the recoverable fixtures.

**Constraints** - Use synthetic prompts, schemas, credentials, and request IDs only. Do not call a live provider.

**Edge Cases** - Exact marker fallback, unrelated digits, no tools, already-clean schemas.

**Verification** - Run the named tests and record their expected failures before editing `rectifier.go`.

#### Plan

- [x] Confirm the pending SSE branch has been resolved and run `git status --short --branch` from the selected implementation base.
- [x] Modify `internal/proxy/rectifier_test.go` with table-driven cases equivalent to:

```go
func TestMatchErrorPattern_Zhipu1210(t *testing.T) {
    tests := []struct {
        name string
        body string
        want ErrorPattern
    }{
        {
            name: "structured code",
            body: `{"type":"error","error":{"type":"invalid_request_error","code":"1210","message":"[1210][API 调用参数有误，请检查文档。][synthetic-id]"}}`,
            want: PatternToolValidation,
        },
        {
            name: "exact message fallback",
            body: `{"error":{"message":"[1210][API 调用参数有误，请检查文档。][synthetic-id]"}}`,
            want: PatternToolValidation,
        },
        {
            name: "unrelated digits",
            body: `{"error":{"message":"request 1210 could not be found"}}`,
            want: PatternNone,
        },
        {
            name: "content block remains higher priority",
            body: `{"error":{"code":"1210","message":"unsupported content type: tool_reference"}}`,
            want: PatternGenericBadRequest,
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := matchErrorPattern([]byte(tt.body)); got != tt.want {
                t.Fatalf("matchErrorPattern() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

- [x] Modify `internal/proxy/server_test.go` with table-driven WebFetch and WebSearch fixtures. On the first backend request, return the exact synthetic 1210 JSON. On the retry, assert that root `$schema`, `additionalProperties`, and tool-level `cache_control` are absent while `name`, `properties`, `required`, WebFetch `format`, and WebSearch `minLength` remain present; then return HTTP 200.
- [x] Add a separate proxy fixture whose tool schema has no cleanable fields. Return 1210 and assert the backend receives exactly one request and the client receives the original 400 body unchanged.
- [x] Run:

```bash
go test ./internal/proxy -run 'TestMatchErrorPattern_Zhipu1210|TestProxy(RetriesZhipu1210WebTools|DoesNotRetryZhipu1210WhenToolCleanupMakesNoChanges)' -count=1
```

Expected result: FAIL before production changes. The structured/message 1210 cases return `PatternNone`; recoverable proxy cases observe one request instead of two.

#### Verification

- [x] Red tests failed for the intended missing behavior, not for fixture syntax or setup errors.
- [x] Test data contains no real credential, prompt, conversation, or provider request ID.

### Task 2: Add Minimal 1210 Classification

#### Requirements

**Objective** - Route the narrow Zhipu parameter-error form into existing tool cleanup without changing normal request processing.

**Outcomes** - `matchErrorPattern` recognizes structured code 1210 and the exact message fallback; `RectifyRequest` and `tryRectify` remain unchanged.

**Evidence** - Task 1 tests pass after a localized `rectifier.go` change.

**Constraints** - Explicit tool, thinking/signature, and unsupported/unknown content-type classifications retain priority. Do not match arbitrary occurrences of `1210`.

**Edge Cases** - Invalid JSON, numeric or unrelated text, exact Chinese phrase without code.

**Verification** - Run focused rectifier and proxy tests.

#### Plan

- [x] Modify `internal/proxy/rectifier.go` to add a narrow helper with this interface:

```go
func isOpaqueToolCompatibilityError(errorBody []byte, lowerMessage string) bool
```

The helper must unmarshal only the structured string code needed from `error.code` and return true when either:

```go
code == "1210"
```

or both exact message markers are present:

```go
strings.Contains(lowerMessage, "[1210]") &&
    strings.Contains(lowerMessage, "api 调用参数有误")
```

- [x] Keep this classification order in `matchErrorPattern`: explicit tool error; thinking/signature error; unsupported or unknown content type (`PatternGenericBadRequest`); opaque 1210 compatibility error; remaining generic invalid request. Return `PatternToolValidation` for the 1210 match. This reuses `cleanTools`; `tryRectify` will retry only when `RectifyRequest` reports a change.
- [x] Do not modify `cleanTools`, `RectifyRequest`, or `Handler.tryRectify` unless the resolved mainline has changed their contracts. If a contract changed, update this spec before implementation.
- [x] Run the focused command from Task 1.

Expected result: PASS.

- [x] Run all rectifier tests:

```bash
go test ./internal/proxy -run 'TestMatchErrorPattern|TestCleanTools|TestRectifyRequest|TestProxyRetries' -count=1
```

Expected result: PASS.

- [x] Commit only after review of the focused diff:

```bash
git diff --check
git diff -- internal/proxy/rectifier.go internal/proxy/rectifier_test.go internal/proxy/server_test.go
git add internal/proxy/rectifier.go internal/proxy/rectifier_test.go internal/proxy/server_test.go
git commit -m "fix(proxy): recover Zhipu web tool parameter errors"
```

#### Verification

- [x] Classification is based on structured code or exact markers.
- [x] No provider hostname or proactive request transform was added.
- [x] No-cleanup requests are not retried.

#### Follow-up Fix (commit `cb1dd2f`)

Review found that the initial `439b1ed` implementation checked `isOpaqueToolCompatibilityError` *after* the combined `hasGenericInvalidRequestPhrase`, which matches both content-type phrases and generic invalid-request phrases (`invalid request`, `invalid_request_error`, `invalid params`, `非法请求`, `illegal request`). A 400 body whose structured `error.code == "1210"` also carried a generic invalid-request phrase in its message was therefore misclassified as `PatternGenericBadRequest`, routing it to `cleanUnknownContentTypes` instead of `cleanTools` and likely skipping the retry.

The real Zhipu message (`[1210][API 调用参数有误…]`) contains no generic phrase, so the original tests stayed green; this was a latent robustness gap rather than an observed regression.

The fix splits the content-type check (`isUnsupportedContentTypePhrase`) out as its own higher-priority branch, keeps the 1210 check next, and leaves `hasGenericInvalidRequestPhrase` as the final generic fallback — matching the ordering this task's plan already specified. Two regression cases were added to `TestMatchErrorPattern_Zhipu1210`: `error.code == "1210"` co-occurring with `Invalid request` and with `非法请求`, both asserting `PatternToolValidation`.

### Task 3: Regression Verification and Spec Closure

#### Requirements

**Objective** - Prove the compatibility change does not regress the pending branch's status-first error handling or existing proxy behavior.

**Outcomes** - Full test results and final file/commit evidence are recorded in both specs.

**Evidence** - Fresh focused and full-suite command output.

**Constraints** - Live Zhipu verification remains optional and approval-gated.

**Edge Cases** - A known unrelated flaky network updater test must be rerun and identified separately; it must not be silently treated as success.

**Verification** - Run the full repository test target from a clean implementation worktree.

#### Plan

- [x] Run:

```bash
make test
```

Expected result: PASS, including the race detector configured by the repository.

- [x] Run:

```bash
git status --short
git diff --check HEAD^ HEAD
```

Expected result: only intended feature files are present and no whitespace errors are reported.

- [x] If the user explicitly approves a live verification, send one minimal synthetic request through the configured provider and record only status, request count, tool names, and whether cleanup occurred. Do not record raw bodies or credentials. Otherwise mark live verification as not performed by design.
- [x] Update `Progress`, the development checklist, and this task's verification section in both `spec.md` and `spec_ZH.md` with exact commands, results, commit hash, and any residual limitation.

#### Verification

- [x] Focused tests pass.
- [x] `make test` passes.
- [x] English and Chinese specs remain semantically aligned.
- [x] Live verification status is stated explicitly.

#### Closure Evidence (2026-07-01)

**Commits on `zhipu-web-compat` (not pushed):**
- `b3931c8` — `feat(proxy): log safe SSE anomaly structure` (Task 0)
- `344ab54` — `docs(zhipu-web): record Task 0 evidence gate decision`
- `439b1ed` — `fix(proxy): recover Zhipu web tool parameter errors` (Tasks 1+2)
- `510143a` — `docs(zhipu-web): close out Tasks 1-3 and record regression evidence`
- `cb1dd2f` — `fix(proxy): classify Zhipu 1210 before generic invalid-request phrases` (Task 2 follow-up)

**Commands and results:**
- `go test ./internal/usage -count=1` → 56 passed.
- `go test ./internal/proxy -run 'TestMatchErrorPattern|TestCleanTools|TestRectifyRequest|TestProxyRetries' -count=1` → 43 passed.
- `go test ./internal/proxy -run 'TestMatchErrorPattern_Zhipu1210|TestProxy(RetriesZhipu1210WebTools|DoesNotRetryZhipu1210WhenToolCleanupMakesNoChanges)' -count=1` → 9 passed (RED before `439b1ed`, GREEN after); `cb1dd2f` added 2 more `TestMatchErrorPattern_Zhipu1210` cases (11 total) covering 1210 co-occurring with `Invalid request` and `非法请求`.
- `make test` (= `go test -v -race -coverprofile=coverage.out ./...`) → exit 0, no `FAIL`.
- `git status --short` → clean working tree after commits.
- `git diff --check HEAD^ HEAD` → no whitespace errors.

**Acceptance Criteria status:** all 8 pass via synthetic `httptest` fixtures — (1) anomaly fixture emits bounded structure with no content markers; (2) Task 0 evidence and GO decision recorded before recovery; (3) Zhipu 1210 fixture classified as tool-cleanup candidate; (4) WebFetch/WebSearch retried once after `$schema`/`additionalProperties` removal; (5) `format`/`minLength`/`properties`/`required` preserved; (6) no-cleanup 1210 fixture forwarded without retry; (7) existing Kimi/thinking/unknown-content/large-400/SSE-status/heartbeat/usage tests pass; (8) `make test` passes.

**Live provider verification:** Not performed by design (no explicit approval; consumes quota and transmits requests externally). Synthetic fixtures reproduce the 1210 error form and the cleanup/retry semantics deterministically.

**Residual limitation:** The real 589-603 byte HTTP-200 SSE precursor structure remains uncaptured; the Task 0 diagnostics will record it safely if a live run is later approved. This does not affect the 1210 recovery, which targets the already-recorded 400 response.
