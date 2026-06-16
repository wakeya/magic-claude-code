# Provider Rate-Limit Queue Spec

Local page: Admin Provider modal / usage and request logs  
Proxy entry: `internal/proxy/handler.go` ServeHTTP / upstream request path around `tryRectify`  
Reference sources: User-provided provider rate-limit guidance (error codes 1302, 1305)  
Stack: Go 1.26 stdlib (`sync`, `time`, `container/list` or channels) + Vue 3 + SQLite config store  
Last updated: 2026-06-16  
Progress: 0 / 6 planned

## Overall Analysis (Source Analysis)

### Problem

During peak periods, upstream domestic model providers often return HTTP 429. The provider guidance separates these failures into two categories:

1. **Error code 1302: user rate limit**  
   Usually caused by an account reaching the concurrent request limit for a model, or by requests arriving too densely. Recommended mitigation: reduce concurrency and use a request queue or concurrency pool.

2. **Error code 1305: platform overload**  
   Caused by high global model traffic, saturated compute capacity, maintenance, expansion, or recovery. Recommended mitigation: retry later, increase retry intervals, and avoid immediate high-frequency retries.

The current proxy forwards each incoming request to the upstream immediately. Claude Code streaming requests can live for a long time, so a few concurrent streams can exhaust an account-level concurrency limit. Without a local concurrency limit, users see fast 429 failures, and automatic retries can amplify pressure on the provider.

### Design Goal

This feature trades uncontrolled 429 failures for controlled local waiting:

- For 1302 account concurrency limits, use provider-scoped concurrency pools and queues before sending requests upstream.
- For 1305 platform overload, use bounded exponential backoff retries rather than immediate retries.
- Keep existing provider behavior unchanged by default; every rate-limit feature must be explicitly enabled.
- Let users tune queue size, concurrency, and wait limits per Provider.

### Why Requests May Become Slower

When a queue is enabled, requests above the configured concurrency limit do not go upstream immediately. They wait locally for an execution slot, so their start time can be later. This is intentional: when the provider will reject excess concurrency anyway, "wait a little and succeed" is usually better than "send immediately and fail with 429".

The queue must not wait forever. Each queued request needs a maximum wait time, and the queue needs a maximum length. When either limit is exceeded, the proxy must return a clear local error.

### Current Project State

Provider config already includes:

- `APIFormat`: distinguishes Anthropic, OpenAI Chat Completions, and OpenAI Responses.
- `ModelMappings`: maps Claude Code model IDs to upstream model IDs.
- Capability flags such as `SupportsThinking` and `MultimodalSwitch`.

The rate-limit queue should follow the Provider capability-flag pattern instead of guessing by model ID or URL. Model mapping only controls the outbound `model` field and is not a sufficient basis for rate-limit policy.

### Strategy

Use two layers:

1. **Pre-request concurrency pool + queue**
   - Applies per Provider in the first phase; Provider+Model can be a future extension.
   - When enabled and `max_concurrent_requests > 0`, the request must acquire an execution slot before it is sent upstream.
   - If no slot is available, the request enters a FIFO queue.
   - If the queue is full or the request waits too long, return a clear local error without sending anything upstream.
   - Streaming requests hold the execution slot until the response body has fully been forwarded.

2. **429 backoff retry**
   - Only active when explicitly enabled for the Provider.
   - Parse upstream 429 bodies and recognize `1302` / `1305`.
   - Respect `Retry-After` when present.
   - Otherwise use exponential backoff with small jitter.
   - Retry count is bounded to avoid worsening platform overload.

### Scope

- **In scope**: Provider-level queue config, pre-request concurrency control, queue timeout, bounded 429 backoff retry, admin UI config, request-log observability, unit tests.
- **Out of scope**: distributed cross-process throttling, global multi-account scheduling, Batch API, async job system, dynamic auto-tuning of concurrency based on 429 frequency.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | Provider rate-limit config model | Provider fields, SQLite schema, admin API | Config round-trip tests |
| 2 | Planned | Provider concurrency pool and queue | `internal/proxy/ratelimit` or equivalent component | Concurrency, queue, timeout tests |
| 3 | Planned | Proxy request-path wiring | Acquire/release around upstream request path | Streaming release tests |
| 4 | Planned | 429 backoff retry | 1302/1305 detection, Retry-After, exponential backoff | Mock upstream 429 retry tests |
| 5 | Planned | Admin UI config | ProviderModal, useApi, i18n | Frontend build and interaction check |
| 6 | Planned | Observability and safety verification | Request logs, queue rejection errors, test record | `go test ./...`, frontend build |

## Requirements

### Deliverables

1. Provider gains rate-limit config fields:
   - `rate_limit_queue_enabled`: enable local queueing.
   - `max_concurrent_requests`: maximum concurrent upstream requests for this Provider; `0` means unlimited.
   - `max_queue_size`: maximum waiting requests; `0` means no waiting queue.
   - `queue_timeout_ms`: maximum local queue wait for one request.
   - `retry_429_enabled`: enable 429 backoff retry.
   - `retry_429_max_attempts`: maximum retries, excluding the first attempt.
   - `retry_429_initial_delay_ms`: initial delay when no `Retry-After` is present.
   - `retry_429_max_delay_ms`: maximum delay when no `Retry-After` is present.

2. Admin Provider create, update, duplicate, list, and detail endpoints fully persist and return these fields.

3. SQLite config storage fully persists these fields and migrates old databases with safe defaults.

4. Before sending a request upstream:
   - If the Provider queue is disabled or `max_concurrent_requests <= 0`, keep current behavior.
   - If enabled, the request must acquire an execution slot first.
   - If the queue is full, return HTTP 429 or 503 with a local queue-full message.
   - If queue wait times out, return HTTP 504 or 429 with a local queue-timeout message.

5. Streaming requests must release the execution slot only after the response stream fully completes.

6. Non-streaming requests must release the execution slot after the upstream response body has been read and forwarded.

7. 429 backoff retry applies only to upstream 429 responses, not local queue-full or queue-timeout errors.

8. 429 retry must preserve the existing HTTP 400 rectifier flow and must not swallow compatibility recovery.

9. Request logs and usage records should distinguish:
   - Normal upstream requests.
   - Local queue wait.
   - Local queue-full rejection.
   - Queue timeout.
   - Upstream 429 retry success or failure.

10. Defaults must be backward-compatible: all existing Providers remain unqueued after upgrade and current request behavior does not change.

### Data Model

```go
type Provider struct {
    RateLimitQueueEnabled bool `json:"rate_limit_queue_enabled"`
    MaxConcurrentRequests int  `json:"max_concurrent_requests"`
    MaxQueueSize          int  `json:"max_queue_size"`
    QueueTimeoutMS        int  `json:"queue_timeout_ms"`
    Retry429Enabled       bool `json:"retry_429_enabled"`
    Retry429MaxAttempts   int  `json:"retry_429_max_attempts"`
    Retry429InitialDelayMS int `json:"retry_429_initial_delay_ms"`
    Retry429MaxDelayMS    int  `json:"retry_429_max_delay_ms"`
}
```

Suggested defaults:

| Field | Default | Meaning |
| --- | --- | --- |
| `rate_limit_queue_enabled` | `false` | Do not change behavior after upgrade |
| `max_concurrent_requests` | `0` | Unlimited concurrency |
| `max_queue_size` | `0` | No waiting queue |
| `queue_timeout_ms` | `60000` | When explicitly enabled, default max wait is 60 seconds |
| `retry_429_enabled` | `false` | Do not hide upstream errors by default |
| `retry_429_max_attempts` | `2` | At most two extra attempts |
| `retry_429_initial_delay_ms` | `1000` | Initial delay of 1 second |
| `retry_429_max_delay_ms` | `10000` | Single delay capped at 10 seconds |

### Queue Semantics

1. First phase queue granularity is Provider-scoped. Provider+Model granularity is a future extension.
2. Queueing is FIFO to avoid later requests jumping ahead.
3. If the request context is cancelled, the queued item must be removed and must not leak.
4. Queue length counts waiting requests only, not active requests.
5. Active requests must never exceed `max_concurrent_requests`.
6. When admin config changes, new requests use the new config; already-active or queued requests are not forcibly migrated.

### 429 Retry Semantics

1. Only HTTP 429 from upstream is handled.
2. If `Retry-After` exists:
   - Numeric format is seconds.
   - HTTP-date format waits until that target time.
   - If the delay exceeds `retry_429_max_delay_ms`, cap it.
3. Without `Retry-After`, use exponential backoff: `initial * 2^(attempt-1)` plus 0-250ms jitter.
4. `1302` is retryable, but the concurrency pool should be the primary mitigation.
5. `1305` is retryable, but retry must be conservative and never immediate.
6. The previous response body must be fully closed before each retry to avoid connection leaks.
7. Retry uses the same Provider and does not trigger provider failover.

### Admin UI

The Provider modal gains a "Rate Limit and Retry" section. The section is collapsed by default so ordinary Provider configuration is not cluttered by advanced options. When expanded, it first shows two master switches:

- Enable request queue: checkbox.
- Enable 429 backoff retry: checkbox.

Queue parameters are shown only after "Enable request queue" is checked:

- Max concurrent requests: numeric input.
- Max queued requests: numeric input.
- Queue timeout: numeric input, with clear unit text.

Retry parameters are shown only after "Enable 429 backoff retry" is checked:

- Max retry attempts: numeric input.
- Initial retry delay: numeric input.
- Max retry delay: numeric input.

Validation:

- Numeric values must not be negative.
- If queueing is enabled, `max_concurrent_requests` must be greater than 0.
- If queueing is enabled and waiting is allowed, `max_queue_size` must be greater than 0.
- If 429 retry is enabled, `retry_429_max_attempts` must be greater than 0.

### Safety and Stability Constraints

1. Queue growth must be bounded.
2. Queue wait time must be bounded.
3. Request cancellation must release slots or remove queue entries.
4. Logs must not include API tokens, Authorization values, full request bodies, or user-sensitive data.
5. Retry must not bypass request size limits, header filtering, model mapping, format conversion, or the HTTP 400 rectifier.
6. The implementation must not hold a global lock during upstream network I/O. Locks should only protect queue state.
7. Streaming responses must release slots correctly, including abnormal disconnects.

### Edge Cases

1. Queue disabled: behavior is identical to the current version.
2. `max_concurrent_requests=1`: strict serial execution for one Provider.
3. `max_queue_size=0`: reject immediately when all execution slots are occupied.
4. Client disconnects while queued: queued item is cancelled.
5. Client disconnects during streaming response: execution slot is released.
6. Upstream keeps returning 1305: retry until limit, then return the final error.
7. Upstream returns 429 with non-JSON body: use generic 429 backoff policy.
8. Upstream returns 1302 but queue is disabled: only retry policy applies; queue is not automatically enabled.
9. Provider is disabled or switched: in-flight requests continue with their selected Provider; new requests use the new config.
10. Admin saves invalid config: API returns 400 and does not persist it.

### Non-Goals

1. No distributed queue across processes or machines.
2. No automatic detection of account concurrency limits.
3. No automatic rate-limit rules based on model ID.
4. No Batch API or async job system.
5. No request priority or manual queue cancellation UI.
6. No automatic fallback to another model; fallback can be a separate future feature.

## Task Details

### Task 1: Provider Rate-Limit Config Model

#### Requirements

**Objective** - Add local queue and 429 retry configuration to Provider and persist it through every config path.

**Outcomes** - Provider struct, SQLite schema, JSON store, admin API create/update/list/detail/duplicate, and frontend types all support the new fields.

**Evidence** - Unit tests cover create, update, duplicate, SQLite save/load, and old-database migration defaults.

**Constraints** - Queueing and retry are disabled by default so existing Providers behave unchanged.

**Edge Cases** - Old SQLite database missing columns; JSON config missing fields; invalid negative values.

**Verification** - `go test ./internal/config ./internal/admin -run RateLimit -v`

#### Plan

1. Add rate-limit fields and validation in `internal/config/provider.go`.
2. Add SQLite `providers` columns and `ensureProviderColumns` migrations.
3. Update load/save SQL.
4. Update admin API request/response structures.
5. Update frontend `Provider` types and save payload.
6. Add config round-trip tests.

#### Verification

- [ ] Old database auto-adds columns.
- [ ] Admin create returns the fields.
- [ ] Admin update persists the fields.
- [ ] Duplicate Provider keeps the rate-limit config.

### Task 2: Provider Concurrency Pool and Queue

#### Requirements

**Objective** - Implement Provider-scoped execution slots and a FIFO wait queue to control concurrent upstream requests.

**Outcomes** - The proxy can limit Provider concurrency locally, queue overflow requests, time them out, or reject them according to config.

**Evidence** - Concurrency tests prove active requests never exceed the limit; queue-full and timeout cases return expected errors.

**Constraints** - No lock may be held during upstream network requests; client cancellation must clean up waiting entries.

**Edge Cases** - Serial concurrency, queue size 0, cancellation while queued, cancellation while active.

**Verification** - `go test ./internal/proxy/... -run RateLimitQueue -v`

#### Plan

1. Add an internal queue component keyed by Provider ID.
2. Provide `Acquire(ctx, provider)` and `Release()` APIs.
3. Return distinguishable errors for queue full, queue timeout, and context cancellation.
4. Write concurrency, FIFO, cancellation, and timeout tests.

#### Verification

- [ ] Concurrency limit is stable.
- [ ] FIFO order is stable.
- [ ] Timeout does not leak slots or queue entries.
- [ ] Cancellation does not leak slots or queue entries.

### Task 3: Proxy Request-Path Wiring

#### Requirements

**Objective** - Acquire an execution slot before upstream dispatch and release it only after the response is fully handled.

**Outcomes** - Non-streaming and streaming requests both occupy and release Provider slots correctly.

**Evidence** - Mock upstream tests show long streaming responses occupy a slot until the stream ends.

**Constraints** - Existing model mapping, format conversion, header filtering, usage recording, and HTTP 400 rectifier behavior must remain intact.

**Edge Cases** - Upstream connection failure, request construction failure, client disconnect during streaming, rectifier retry.

**Verification** - `go test ./internal/proxy/... -run ProxyRateLimit -v`

#### Plan

1. Add or inject a Provider queue manager in `Handler`.
2. Acquire a slot before creating/sending the upstream request.
3. Use `defer release()` for non-streaming paths.
4. Wrap streaming response body so `Close` or EOF releases the slot.
5. Ensure HTTP 400 rectifier retries either reuse the same slot or have clearly bounded slot behavior.

#### Verification

- [ ] Non-streaming success and failure release slots.
- [ ] Streaming EOF releases slots.
- [ ] Client disconnect releases slots.
- [ ] HTTP 400 rectifier still works.

### Task 4: 429 Backoff Retry

#### Requirements

**Objective** - Retry upstream 429 responses with bounded backoff to reduce peak-hour transient failures.

**Outcomes** - The proxy recognizes 1302/1305 and retries after `Retry-After` or exponential backoff.

**Evidence** - Mock upstream returns 429 then 200 and the proxy succeeds; repeated 429 reaches the limit and returns the final error.

**Constraints** - Do not retry local queue errors; do not interfere with HTTP 400 rectifier; never retry forever.

**Edge Cases** - Non-JSON 429, missing error code, Retry-After seconds/date formats, client cancellation.

**Verification** - `go test ./internal/proxy/... -run Retry429 -v`

#### Plan

1. Add a 429 parser to extract code and message.
2. Add Retry-After parsing.
3. Implement exponential backoff with jitter.
4. Apply retry logic in the upstream request path.
5. Close each previous response body before retrying.

#### Verification

- [ ] 1302 retries according to config.
- [ ] 1305 retries with backoff.
- [ ] Retry-After has priority over local delay calculation.
- [ ] Retry stops at the configured maximum attempts.

### Task 5: Admin UI Config

#### Requirements

**Objective** - Let users configure queueing and 429 retry parameters from the Provider modal.

**Outcomes** - Admin UI shows a "Rate Limit and Retry" section; saved config affects the Provider.

**Evidence** - Frontend build passes; manual create/edit returns correct fields from the API.

**Constraints** - The "Rate Limit and Retry" section is collapsed by default; when expanded it shows only the two master switches first; queue numeric fields are shown only after "Enable request queue" is checked; retry numeric fields are shown only after "Enable 429 backoff retry" is checked; every numeric field must show clear units.

**Edge Cases** - Negative numbers, blank values, enabled queue without concurrency, editing old Provider.

**Verification** - Run the existing frontend build command.

#### Plan

1. Update `useApi.ts` Provider type.
2. Update `ProviderModal.vue` form state, initialization, and save payload.
3. Update `useI18n.ts` English and Chinese copy.
4. Add frontend validation.

#### Verification

- [ ] New Provider can save rate-limit fields.
- [ ] Edit Provider can render rate-limit fields.
- [ ] The default UI shows only the collapsed entry point or master switches, not every advanced numeric field.
- [ ] Checking "Enable request queue" reveals queue numeric fields.
- [ ] Checking "Enable 429 backoff retry" reveals retry numeric fields.
- [ ] Invalid config is blocked by frontend or backend.
- [ ] Frontend build passes.

### Task 6: Observability and Safety Verification

#### Requirements

**Objective** - Make rate-limit behavior observable and ensure it does not leak resources or sensitive data.

**Outcomes** - Logs explain queue wait, rejection, timeout, and retry; tests prove no slot leaks.

**Evidence** - Automated tests pass; manual mock scenarios produce readable logs.

**Constraints** - Do not log sensitive request bodies or tokens; avoid noisy repeated logs while a request waits in queue.

**Edge Cases** - High concurrency stress, long streaming request, client disconnect, continuous upstream 429.

**Verification** - `go test ./...`; use race testing for the queue component if practical.

#### Plan

1. Add clear error types for local queue rejection and timeout.
2. Add queued duration and 429 retry count to request logs.
3. Add resource-release tests.
4. Run full Go tests.

#### Verification

- [ ] `go test ./...` passes.
- [ ] Logs contain no sensitive data.
- [ ] Queue full, timeout, and retry exhaustion are easy to diagnose.
