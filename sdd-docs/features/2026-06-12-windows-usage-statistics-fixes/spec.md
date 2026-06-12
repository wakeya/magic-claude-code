# Windows Usage Statistics Reliability Spec

Local routes: `/` status tab, `/` usage tab | Runtime target: Windows amd64 binary
Stack: Go 1.26 + SQLite + Vue 3 + embedded frontend | Last updated: 2026-06-12
Progress: 5 / 5 completed

---

## Problem Analysis

### Symptoms

On Windows, the proxy successfully handled Claude Code model requests and wrote usage rows into `data/proxy.db`, but the admin dashboard showed empty statistics:

| Surface | Observed result |
|---------|-----------------|
| Status page | Provider Requests, Today Provider Requests, Today Token Consumption, Usage Coverage, and Last Provider Request showed zero or empty values |
| Usage page | Today and request log filters showed no rows |
| Console logs | `POST /v1/messages` requests reached the proxy and returned HTTP 200 |
| SQLite database | `usage_requests` and `usage_tokens` contained provider and session log rows |

### Root Causes

1. **Windows timezone lookup failure**
   - The frontend sends the browser timezone, such as `Asia/Shanghai`, as `tz`.
   - The backend calls `time.LoadLocation(tz)` when building usage summaries and filters.
   - Linux has system zoneinfo data, but the Windows standalone binary may not have IANA timezone data available.
   - When timezone loading failed, usage APIs returned errors or status summary fell back to zero values.

2. **SSE stream completion depended on upstream EOF**
   - Some upstream SSE responses emit `message_stop` but keep the HTTP connection open.
   - The proxy previously waited for `io.EOF` before calling `finishUsageRecord`.
   - On Windows with the affected upstream behavior, request rows were delayed or not visible until the connection ended.

3. **Terminal SSE events may carry final usage**
   - Some compatible providers may include final usage in the `message_stop` payload.
   - The parser must merge usage from terminal events before marking the stream complete.

### Evidence

Representative Windows log:

```text
[id] >>> POST /v1/messages model=claude-opus-4-6 -> mimo-v2.5-pro stream=true ...
[id] <<< 200 model=claude-opus-4-6 -> mimo-v2.5-pro upstream=...
[Stream] SSE stream detected ..., enabling heartbeat injection
```

Representative SQLite evidence:

```text
usage_requests: 33 rows
usage_tokens: 33 rows
provider rows on 2026-06-12T03:*Z exist with usage_source=provider and usage_parse_status=ok
```

---

## Requirements

### R1. Windows binary must resolve browser IANA timezones

**Objective** - A standalone Windows build must support `time.LoadLocation("Asia/Shanghai")` and other browser-provided IANA timezone IDs.

**Outcome** - Status and Usage APIs can compute local-day windows and date buckets on Windows without depending on OS zoneinfo files.

**Implementation** - Import Go's embedded timezone database:

```go
_ "time/tzdata"
```

**Acceptance Criteria**

- `GET /api/status?tz=Asia/Shanghai` returns non-zero usage summaries when matching records exist.
- `GET /api/usage/summary?tz=Asia/Shanghai&from=...&to=...` does not fail because of missing timezone data.

### R2. SSE usage recording must not depend solely on EOF

**Objective** - The proxy must record streaming usage when a terminal SSE event is received, even if the upstream connection remains open.

**Terminal events**

| Event | Completion signal |
|-------|-------------------|
| `event: message_stop` | Complete |
| `data: {"type":"message_stop"}` | Complete |
| `data: [DONE]` | Complete |

**Outcome** - `finishUsageRecord` is reached after terminal SSE completion.

**Acceptance Criteria**

- A test backend that writes `message_stop`, flushes, and never closes still results in one recorded usage row.
- The downstream response includes the terminal SSE data before the proxy stops copying.

### R3. Terminal SSE usage must be merged before completion

**Objective** - If a compatible provider includes usage in `message_stop`, the usage parser must keep it.

**Outcome** - Terminal event payloads are parsed before `complete=true`.

**Acceptance Criteria**

- `event: message_stop` with `{"usage":{"output_tokens":9}}` results in `UsageSourceProvider`, `ParseStatusOK`, and `OutputTokens=9`.

### R4. Existing date preset behavior remains unchanged

**Objective** - Do not change Usage page date preset semantics as part of this fix.

**Outcome** - The prior behavior remains:

| Preset | Range |
|--------|-------|
| Today | Today 00:00:00 to today 23:59:59 local browser time |
| Last 7 days | Last seven complete days, excluding today |
| Last 30 days | Last thirty complete days, excluding today |

### R5. Binary package remains self-contained

**Objective** - The Windows binary package must include backend fixes and the current embedded frontend assets.

**Acceptance Criteria**

- `bin/mcc-windows-amd64/mcc.exe` is rebuilt after backend and frontend asset changes.
- The binary contains embedded frontend entry `index-Dxc_BCfC.js`.
- The binary contains Go `time/tzdata` symbols.

---

## Implementation Summary

| Area | File | Change |
|------|------|--------|
| Timezone data | `cmd/server/main.go` | Add blank import for `time/tzdata` |
| SSE parser | `internal/usage/sse.go` | Track completion and merge terminal usage before completion |
| SSE copy loop | `internal/proxy/heartbeat.go` | Stop copying when observer reports terminal completion |
| Stream observer | `internal/proxy/handler.go` | Expose `IsComplete()` from the SSE usage observer |
| Regression tests | `internal/usage/sse_test.go` | Cover `[DONE]`, `message_stop`, and terminal usage merging |
| Regression tests | `internal/proxy/heartbeat_test.go` | Cover copy loop return when observer completes |
| Regression tests | `internal/proxy/server_test.go` | Cover upstream that sends `message_stop` but never closes |

---

## Validation

### Automated

| Command | Expected result |
|---------|-----------------|
| `go test ./...` | 328 tests pass |
| `npm --prefix internal/frontend test` | 45 tests pass |
| `git diff --check` | No whitespace or patch format issues |

### Manual Windows Validation

1. Start the rebuilt Windows binary:

```powershell
.\mcc.exe -password admin
```

2. Send a Claude Code request through the proxy.

3. Verify console logs include:

```text
>>> POST /v1/messages
<<< 200
[Stream] SSE stream detected
```

4. Open the admin dashboard and verify:

| Page | Expected |
|------|----------|
| Status | Provider request counts and last provider request are populated |
| Usage / Today | Provider rows and token totals are visible |
| Usage / Request log | `/v1/messages` rows are visible |

---

## Risks and Boundaries

| Risk | Mitigation |
|------|------------|
| Binary size increases because of embedded tzdata | Acceptable tradeoff for self-contained Windows support |
| A non-terminal event contains `"type":"message_stop"` in a nested unrelated object | Parser only inspects the top-level `type` field |
| Upstream emits malformed JSON before terminal event | Existing parser records parse error behavior |
| Upstream omits both usage and terminal event | Existing EOF behavior remains as fallback |

---

## Completion Status

| # | Task | Status |
|---|------|--------|
| 1 | Diagnose Windows dashboard empty state with real `proxy.db` | Completed |
| 2 | Embed tzdata for Windows IANA timezone support | Completed |
| 3 | Stop SSE copy loop on terminal events | Completed |
| 4 | Merge terminal event usage before completion | Completed |
| 5 | Rebuild Windows binary and verify tests | Completed |
