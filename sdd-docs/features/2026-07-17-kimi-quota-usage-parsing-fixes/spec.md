# Kimi Quota Query and Usage Statistics Parsing Fixes Spec

Local page: Dashboard usage tab (`DashboardView.vue` "使用统计"), provider quota modal (`ProviderUsageModal.vue`, "用量" button on a provider card)  
Proxy entry: `POST /v1/messages` (usage recording path), `POST /api/providers/{id}/usage/query` (quota refresh), `GET /api/usage/*` (usage statistics API)  
Reference sources: kimi-code reference implementation [`packages/oauth/src/managed-usage.ts`](https://github.com/MoonshotAI/kimi-code/blob/main/packages/oauth/src/managed-usage.ts), historical specs `2026-06-27-provider-quota-query`, `2026-06-11-usage-date-range-presets`, `2026-05-15-usage-statistics`  
Stack: Go 1.26 `encoding/json` (backend), Vue 3 + TypeScript (frontend), SQLite WAL (`data/proxy.db`)  
Last updated: 2026-07-17  
Progress: 3 / 3 complete (verified 2026-07-17)

## Overall Analysis (Source Analysis)

This branch fixes three independent defects found while investigating one user report: "the Kimi coding-plan provider cannot query quota, and the usage-statistics page shows no token data when provider = kimi-k3". Each symptom has its own root cause; all three were confirmed against the live environment (running container `mcc`, production `data/proxy.db`, real provider tokens) before any fix was written.

### Symptom 1: Kimi coding-plan quota query fails with `invalid_json`

**Live API probe (root-cause evidence).** Querying the real endpoint with the stored kimi-k3 token returns HTTP 200 and a healthy payload — the endpoint, host detection (`api.kimi.com` → `kimi`, `DetectTokenPlanProvider` in `internal/providerquota/token_plan.go:60-75`), and token were never the problem:

```
GET https://api.kimi.com/coding/v1/usages
Authorization: Bearer <token from providers.api_token, id=provider-b3010ddf-73f3>

HTTP 200
{
  "user":  {"userId":"...","country":"CN"},
  "usage": {"limit":"100","used":"22","remaining":"78","resetTime":"2026-07-24T01:20:25.362103Z"},
  "limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
             "detail":{"limit":"100","used":"66","remaining":"34","resetTime":"2026-07-17T11:20:25.362103Z"}}],
  "parallel":   {"limit":"10"},
  "totalQuota": {"limit":"100","remaining":"99"},
  "subType": "TYPE_PURCHASE", "authentication": {...}
}
```

**Root-cause chain.** The old `queryKimi` (`internal/providerquota/token_plan.go`) declared the response as:

```go
Usage struct {
    Limit     json.Number `json:"limit"`
    Remaining json.Number `json:"remaining"`
    ResetTime json.Number `json:"resetTime"`   // <-- breaks
}
```

1. `json.Number` **does** accept numeric strings (`"100"`), so `limit`/`remaining` were never a problem.
2. `resetTime` arrives as an **RFC3339 string** (`"2026-07-24T01:20:25.362103Z"`). `encoding/json` rejects a non-numeric string for `json.Number` (`json: invalid number literal, trying to unmarshal ... into Number`), so `json.Unmarshal` of the whole body failed and every real query ended as `invalid_json`.
3. `limits[].name` does not exist in the live payload (the window is described by `limits[].window.{duration,timeUnit}` instead), so even a successful parse would have produced tiers with empty labels.
4. The old tests (`TestParseKimiResponse`, `TestKimiIntegration`) used fixtures with JSON numbers, unix-second `resetTime`, and a `name` field — a shape the real API does not return. That is why the test suite stayed green while production always failed.

**Reference comparison (kimi-code).** `managed-usage.ts` in MoonshotAI/kimi-code queries the identical endpoint with the identical headers and parses deliberately loosely: `parseNumber` accepts number-or-numeric-string; `resetTime` is treated as an ISO string (also accepting `reset_at/resetAt/reset_time` spellings); the `limits[]` label is derived from `window.duration` + `window.timeUnit` (300 MINUTE → "5h limit"); the `usage` object is the weekly limit. mcc's fix mirrors this tolerance in Go.

**Operational note.** Both Kimi providers' persisted `quota_query_config` in `providers` is `{"enabled":false}` and `provider_quota_snapshots` holds no rows for them, so the scheduler never queried and the provider card shows nothing. `POST /api/providers/{id}/usage/query` ("立即刷新") rejects with `quota query not configured` unless the **saved** config has `enabled:true` (`internal/admin/provider_quota_handler.go:283`) — the "测试" button works around this by sending the form as a draft with `enabled` forced true. After deploying the parser fix the user must re-enable and save the quota config in the UI.

### Symptom 2: Usage statistics shows zero data when provider = kimi-k3

**The backend data was always complete.** Direct API reproduction against the running admin server:

```
GET /api/usage/summary?provider_id=provider-b3010ddf-73f3
→ {"provider_requests_total":102,"token_consumption_total":5308687,"usage_coverage_percent":60.8,...}

GET /api/usage/summary?provider_id=provider-b3010ddf-73f3&tz=Asia/Shanghai&from=2026-07-10T00:00:00&to=2026-07-16T23:59:59
→ {"provider_requests_total":0,"token_consumption_total":0,...}   <-- exact frontend default
```

**Root-cause chain.**

1. The usage tab defaults to preset `last_7_days` (`DashboardView.vue:1029`).
2. Old `usageDateRangeForPreset` mapped `last_7_days` → `inclusiveDateTimeRange(7, 1)`: from = 00:00 seven days ago, **to = 23:59:59 yesterday** (`dateTimeInputEndOfDaysAgo(1)`). The default view silently excluded today. `last_30_days` had the same off-by-one (`(30, 1)`).
3. Provider kimi-k3 (`provider-b3010ddf-73f3`) was created 2026-07-17T01:23 UTC and all 102 of its requests happened that same day. In the user's timezone (Asia/Shanghai) that is entirely "today".
4. Default range ∩ kimi-k3 traffic = ∅ → all summary cards show 0, while other providers with older history look normal — making kimi-k3 appear specifically broken.

### Symptom 3: Non-streaming 200 responses record `usage_parse_status = parse_error`

Found while auditing kimi-k3's coverage (2 rows), then shown to be a much larger historical loss.

**Fleet-wide distribution** (`usage_requests` ⨝ `usage_tokens`, `status_code = 200`; point-in-time snapshot taken 2026-07-17 during the investigation — the database is live and these counts grow with traffic):

| stream | parse status | rows | providers |
| --- | --- | --- | --- |
| 0 | `parse_error` | **499** | GLM 4.7 ky (438), Zhipu GLM 5.1 chanhu (25), Zhipu GLM 5.1 ch (18), Zhipu GLM 5.2 xs (16), kimi-k3 (2) |
| 0 | `ok` | 73 | kimi code k2.6 xs (70), MiniMax (2), mimo (1) |
| 0 | `missing` | 2 | kimi code k2.6 xs |
| 1 | `ok` | 48,366 | (streaming path healthy for all providers) |

**Root-cause chain.**

1. Live reproduction: a tiny non-stream request through the proxy (GLM 4.7 ky) returned a **332-byte, fully valid JSON** body to the client — yet the recorded row was `parse_error`. Feeding those exact bytes to `ExtractUsageFromJSON` reproduced `parse_error` offline.
2. The non-SSE branch (`internal/proxy/handler.go:386`) parses usage via `usage.ExtractUsageFromJSON(observer.Body())`, which declared `Usage map[string]int64`.
3. Zhipu (bigmodel) non-streaming responses embed **non-numeric fields inside `usage`** (confirmed by direct upstream call): `"server_tool_use":{"web_search_requests":0}` (object) and `"service_tier":"standard"` (string). The official Anthropic API returns these fields too. `json.Unmarshal` into `map[string]int64` fails on either value → the function returns `parse_error` (and produces no error message, which is why `usage_parse_error` is empty in every such row).
4. kimi/MiniMax/mimo non-stream responses carry only numeric usage fields → the same code parsed them fine (the 73 `ok` rows). kimi-k3's 2 failures were large Python-urllib responses (18 KB / 30 KB) whose usage also carried a non-numeric field.
5. The streaming path was immune because `SSEObserver` parsed usage into a typed `usageJSON` struct (`*int64` fields); `encoding/json` simply ignores unknown fields there.

### Risk Summary

1. `token_plan.go` changes are confined to the Kimi adapter; `kimiUsageDetail`/`kimiWindowLabel`/`parseKimiResetTime` are new symbols, and `parseKimiResetTime`'s signature changed (`json.Number` → `json.RawMessage`) with no callers outside the package.
2. `usageDateRangeForPreset` also drives `activeUsageDateRangePreset` highlight matching; changing the preset ranges keeps that logic working because both use the same function. Users with a previously saved custom `from/to` are unaffected.
3. The unified usage extractor changes one legacy behavior deliberately: a `usage` value of the wrong **type** (e.g. `"usage":"n/a"`) now yields `missing` instead of `parse_error`; a syntactically invalid body still yields `parse_error`. SSE merge semantics (later events overwrite only fields present-and-numeric) are preserved.
4. The 499 historical `parse_error` rows cannot be repaired — the proxy does not retain response bodies — so the fix is forward-looking only.

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | ✅ done | Kimi quota response tolerant parsing | `internal/providerquota/token_plan.go`, `internal/providerquota/token_plan_test.go` | ✅ package tests green; live end-to-end query returns 5h tier (39%, resets 2026-07-17T16:20:25Z) + weekly tier (36%, resets 2026-07-24T01:20:25Z) with label "5h limit" |
| 2 | ✅ done | Usage date-range presets include today | `internal/frontend/src/views/DashboardView.vue`, `internal/frontend/src/views/DashboardUsageRequests.test.ts` | ✅ 195/195 frontend tests pass; `vite build` ok; API repro shows default range now covers the 102 kimi-k3 requests |
| 3 | ✅ done | Unified tolerant usage extraction (stream + non-stream) | `internal/usage/parse.go`, `internal/usage/sse.go`, `internal/usage/parse_test.go`, `internal/usage/sse_test.go` | ✅ `go test ./...` green; real bigmodel body that previously failed now parses `provider/ok` (input=6, output=1); SSE `diag.ParseErrors == 0` on junk-field streams |

## Requirements

### Deliverables

1. `internal/providerquota/token_plan.go`: `queryKimi` parses the live response shape — `limit`/`used`/`remaining` as `json.Number` (number or numeric string), `resetTime` as `json.RawMessage` resolved by `parseKimiResetTime` (RFC3339/RFC3339Nano strings and unix seconds/milliseconds, quoted or bare), `limits[]` label fallback chain `name → title → scope → window-derived` (`kimiWindowLabel`, 300 MINUTE → "5h limit"), explicit `used` preferred over `limit - remaining`, weekly tier populated with `Remaining`, unknown top-level fields (`user`, `parallel`, `totalQuota`, `authentication`, `subType`, `boosterWallet`) ignored.
2. `internal/frontend/src/views/DashboardView.vue`: `usageDateRangeForPreset` maps `last_7_days` → `inclusiveDateTimeRange(6, 0)` and `last_30_days` → `inclusiveDateTimeRange(29, 0)` ("last N days" = today plus the previous N−1 days); `today` stays `(0, 0)`.
3. `internal/usage/parse.go`: a shared tolerant extractor — `usageCounterKeys`, `extractUsageValues(json.RawMessage)`, `parseUsageFields(json.RawMessage) map[string]int64`, `usageFieldInt64(json.RawMessage) (int64, bool)` — accepting JSON numbers, floats (`174.0`), and numeric strings (`"174"`), ignoring any other value; `ExtractUsageFromJSON` reimplemented on top of it.
4. `internal/usage/sse.go`: event payload `usage`/`message.usage` changed to `json.RawMessage`; `merge` reuses the same extractor with present-field-only overwrite semantics; the typed `usageJSON` struct removed.
5. Tests covering every tolerance axis for both paths (details per task).

### Constraints

1. Endpoint, host detection (`api.kimi.com` → `kimi`), auth header (`Authorization: Bearer`), and the `TokenPlanAdapter`/`Adapter` interfaces are unchanged.
2. SSE merge keeps per-field overwrite semantics: a later event only overwrites a counter when that field is present and numeric in the later event; `HasAny` becomes true on any present-and-numeric field (even a real zero) exactly as before.
3. `ExtractUsageFromJSON` keeps its contract: `(UsageValues, source, status)`; invalid top-level JSON → `parse_error`; absent/empty/all-zero/unusable `usage` → `missing`; otherwise `provider`/`ok`.
4. Frontend preset change is limited to `usageDateRangeForPreset`; no changes to `inclusiveDateTimeRange`, `activeUsageDateRangePreset`, i18n keys, or the backend filter (`parseFilterTime` already parses the datetime-local format).
5. `internal/frontend/dist` is committed in this repo, so the frontend change ships with a rebuilt bundle (`npm --prefix internal/frontend run build`).

### Edge Cases

1. `resetTime` as unix **milliseconds** (> 1e12) → `time.UnixMilli`; quoted numeric string (`"1753228825"`) → parsed as epoch; `null`/missing/garbage → zero `time.Time`, tier simply omits `ResetsAt`.
2. `used` present as `0` → respected as an explicit zero (not replaced by `limit - remaining`); derived `used` clamped to ≥ 0 when `remaining > limit`; over-quota `used > limit` (rejected requests still count) → utilization clamped to 100 via `kimiUtilization` — `NormalizeTier` rejects values outside [0,100], so an unclamped tier would fail the whole query as `invalid_response` — while `Used`/`Remaining` display values stay as reported.
3. `window.timeUnit` arrives as the enum form `TIME_UNIT_MINUTE` → matched via case-insensitive `strings.Contains`; unknown units or non-positive duration → empty label (tier still valid).
4. bigmodel `usage` containing `server_tool_use` (object), `service_tier` (string), or any future non-numeric field → ignored, known counters still extracted (both SSE and non-SSE); counter values outside the int64 range (e.g. `1e300`) → treated as junk and ignored, preventing implementation-defined overflow values in statistics.
5. `usage` present but of a non-object type (`"n/a"`, number, array) → `missing`, not `parse_error`.
6. Provider traffic that is entirely "today" (new providers) → visible under the default `last_7_days` preset after Task 2; the `today` preset semantics are unchanged.
7. Browser in any timezone: `from`/`to` are computed in browser-local time and parsed by the backend with the supplied `tz`; including today removes the previous dependence on the user's timezone for same-day providers.

### Non-Goals

1. No backfill of the 499 historical `parse_error` rows (response bodies are not persisted; data is unrecoverable).
2. No automatic re-enabling of the two Kimi providers' `quota_query_config` (`enabled:false` is user-side state; the UI save flow stays authoritative).
3. No handling of usage key-name aliases (`inputTokens`, `prompt_tokens`, …): OpenAI-format providers are converted to Anthropic shape before observation (`convertOpenAINonStreamingResponse`), and no Anthropic-endpoint provider is known to misspell keys — speculative aliases are deliberately omitted.
4. No collection of `server_tool_use` / `service_tier` values — `usage_tokens` has no columns for them.
5. No changes to the quota scheduler, snapshot store, or `Stop()` semantics; no new configuration options.
6. No git commits are part of this spec's scope (changes remain uncommitted in the branch worktree by user request).

## Task Details

### Task 1: Kimi quota response tolerant parsing

#### Requirements

**Objective** — Make `queryKimi` parse the real `GET https://api.kimi.com/coding/v1/usages` response (RFC3339 `resetTime`, numeric-string counters, `window`-described limits, no `name`), eliminating the permanent `invalid_json` failure, with tolerance aligned to kimi-code's `managed-usage.ts`.

**Outcomes** — `token_plan.go` gains `kimiUsageDetail` (line 133), `usedOrDerived` (141), `kimiWindowLabel` (262), and a `json.RawMessage`-based `parseKimiResetTime` (286); `queryKimi` (152) returns correct five-hour and weekly tiers from the live payload; tests use live-shaped fixtures plus a legacy-shape backward-tolerance case.

**Evidence** — `go test ./internal/providerquota/` passes; an end-to-end run of `TokenPlanAdapter.Query("kimi", ..., "https://api.kimi.com/coding/", <real token>)` succeeds and reports tiers `five_hour` (utilization 39, resets 2026-07-17T16:20:25Z, label "5h limit") and `seven_day` (utilization 36, resets 2026-07-24T01:20:25Z, remaining 64).

**Constraints** — Endpoint/headers/interface unchanged; unknown response fields ignored; label fallback order fixed as `name → title → scope → window`; `strconv` added to imports.

**Edge Cases** — `resetTime` variants (RFC3339Nano, unix seconds, unix millis, numeric string, null, garbage); `used` explicit-zero vs derived; `remaining > limit` clamp; `TIME_UNIT_*` enum units; missing `detail` fields skipped when `limit <= 0`.

**Verification** — Package tests green; live query succeeds; legacy-shape fixture still parses (backward tolerance).

#### Plan

1. In `internal/providerquota/token_plan.go`, replace the anonymous response struct and helpers (old lines 131–233) with:
   - `kimiUsageDetail{ Limit, Used, Remaining json.Number; ResetTime json.RawMessage }` shared by `limits[].detail` and `usage`.
   - `usedOrDerived(limit, remaining float64) float64` — explicit `used` wins when present (including a real `0`), else `max(limit-remaining, 0)`.
   - `limits[]` item gains `Title`, `Scope`, and `Window{ Duration json.Number; TimeUnit string }`; label = first non-empty of `Name/Title/Scope/kimiWindowLabel(Window)`.
   - `kimiWindowLabel(duration, timeUnit)` — minute multiples of 60 → `"<h>h limit"`, other minutes → `"<m>m limit"`, hours → `"<h>h limit"`, seconds → `"<s>s limit"`, else `""`; unit matched case-insensitively by substring (`MINUTE`/`HOUR`/`SECOND`) to accept `TIME_UNIT_MINUTE`.
   - `parseKimiResetTime(raw json.RawMessage) time.Time` — trim; `null`/empty → zero; `strconv.Unquote` quoted values then try `time.Parse(time.RFC3339Nano, …)`; finally `strconv.ParseFloat` → unix seconds, or `time.UnixMilli` when > 1e12.
   - `kimiUtilization(used, limit)` — percentage clamped to [0, 100] so over-quota tiers pass `NormalizeTier`; `Used`/`Remaining` display values stay as reported.
   - Weekly tier sets `Remaining` (previously only `Used`/`Total`).
2. Rewrite `TestParseKimiResponse` and `TestKimiIntegration` fixtures to the live shape (string counters, RFC3339Nano `resetTime`, `window` without `name`); add `TestParseKimiResetTime` (8 cases), `TestKimiUsedOrDerived` (3 cases), `TestKimiWindowLabel` (6 cases), `TestKimiUtilization` (4 cases incl. over-quota clamp), `TestKimiIntegrationOverQuota` (used=120/limit=100 → success, utilization 100, used reported as 120), and `TestKimiIntegrationLegacyShape` (numeric fields, unix `resetTime`, `name` label).
3. Run `go test ./internal/providerquota/` and `go test ./...`.
4. End-to-end probe (temporary `main.go` under `.tmp-kimi-check/`, deleted afterwards): `providerquota.NewTokenPlanAdapter(10s).Query(ctx, "kimi", nil, "https://api.kimi.com/coding/", token)` with the real kimi-k3 token; confirm `success:true` and both tiers.

#### Verification

- [x] `go test ./internal/providerquota/` — ok (2.749s); `go test ./...` — all 15 packages ok, `go vet` clean.
- [x] Live end-to-end probe (2026-07-17): `success:true`, tier `five_hour` label "5h limit", used 39 / total 100 / remaining 61, resets 2026-07-17T16:20:25Z; tier `seven_day` used 36 / total 100 / remaining 64, resets 2026-07-24T01:20:25Z.
- [x] `TestKimiIntegrationLegacyShape` proves the pre-change fixture shape (JSON numbers, unix `resetTime`, `name`) still parses — no regression for older API versions.

### Task 2: Usage date-range presets include today

#### Requirements

**Objective** — Change the usage tab's preset ranges so "last N days" includes today, making same-day-only providers (e.g. newly created kimi-k3) visible in the default view.

**Outcomes** — `usageDateRangeForPreset` (`DashboardView.vue:1905`) returns `inclusiveDateTimeRange(6, 0)` for `last_7_days` and `inclusiveDateTimeRange(29, 0)` for `last_30_days`; the source-assertion test is updated to the new contract.

**Evidence** — Replaying the frontend's previous default query (`from=2026-07-10T00:00:00&to=2026-07-16T23:59:59&tz=Asia/Shanghai`) against `/api/usage/summary?provider_id=provider-b3010ddf-73f3` returned all zeros before the change; the new range (`to` = end of today) covers the 102 requests / 5,308,687 tokens that the unfiltered API already reported.

**Constraints** — Only preset boundaries change; `inclusiveDateTimeRange`, `dateTimeInputStartOfDaysAgo`, `dateTimeInputEndOfDaysAgo`, `activeUsageDateRangePreset`, and i18n keys untouched; backend `parseFilterTime` needs no change (datetime-local with seconds already supported).

**Edge Cases** — `today` preset unchanged; user-customized `from`/`to` untouched; preset-highlight matching keeps working because it compares against the same `usageDateRangeForPreset` output.

**Verification** — 195/195 frontend unit tests; production build succeeds; dist bundle regenerated and committed with the change.

#### Plan

1. In `internal/frontend/src/views/DashboardView.vue`, edit `usageDateRangeForPreset`: `last_7_days` → `inclusiveDateTimeRange(6, 0)`, `last_30_days` → `inclusiveDateTimeRange(29, 0)`, with a comment documenting that ranges include today so same-day providers are not filtered out by default.
2. In `internal/frontend/src/views/DashboardUsageRequests.test.ts`, rename the test to `usage date range presets default to the last 7 days including today` and update the two regex assertions to `inclusiveDateTimeRange(6, 0)` / `inclusiveDateTimeRange(29, 0)`.
3. Run `npm --prefix internal/frontend test` and `npm --prefix internal/frontend run build` (regenerates `dist/assets/index-*.js` and friends, which this repo commits).

#### Verification

- [x] `npm --prefix internal/frontend test` — 195/195 passed (8.26s).
- [x] `npm --prefix internal/frontend run build` — built in 10.13s; `dist` regenerated (old hashed bundles deleted, new ones added).
- [x] API-level before/after (2026-07-17): old default range → `provider_requests_total: 0`; unfiltered/new range → `provider_requests_total: 102`, `token_consumption_total: 5,308,687`, `usage_coverage_percent: 60.8`.

### Task 3: Unified tolerant usage extraction (stream + non-stream)

#### Requirements

**Objective** — Replace the brittle `map[string]int64` non-stream parse and the typed `usageJSON` SSE parse with one shared, tolerance-first extractor so any provider that adds non-numeric usage fields (bigmodel today, official Anthropic API included) or shifts value types (numeric strings, floats) keeps recording tokens on both paths.

**Outcomes** — `internal/usage/parse.go` gains `usageCounterKeys` (line 61), `extractUsageValues` (72), `parseUsageFields` (90), `usageFieldInt64` (110); `ExtractUsageFromJSON` (42) is reimplemented on them; `internal/usage/sse.go` payload uses `json.RawMessage` for `usage`/`message.usage` (164) and `merge` (215) reuses `parseUsageFields`; `usageJSON` is deleted.

**Evidence** — The exact 332-byte bigmodel body that recorded `parse_error` in production parses as `provider/ok` (input=6, output=1) after the change; SSE streams containing `server_tool_use`/`service_tier` report `diag.ParseErrors == 0`; `go test ./...` fully green.

**Constraints** — `ExtractUsageFromJSON`'s `(values, source, status)` contract preserved (invalid top-level JSON → `parse_error`; unusable/absent/all-zero usage → `missing`); SSE merge overwrites only present-and-numeric fields; no key aliases; no new collected fields.

**Edge Cases** — `usage:"n/a"` (wrong type) → `missing`; `usage:null` / `{}` / all-zero → `missing`; numeric-string and float counters truncated via `int64(f)`; explicit `0` in SSE still marks `HasAny` (present field), preserving previous accounting in `hasUsage`.

**Verification** — New unit cases for both paths; offline regression on the captured production body; full suite green.

#### Plan

1. In `internal/usage/parse.go`:
   - Add `usageCounterKeys = []string{"input_tokens","output_tokens","cache_creation_input_tokens","cache_read_input_tokens"}`.
   - Add `usageFieldInt64(raw json.RawMessage) (int64, bool)` — `json.Unmarshal` into `json.Number` (accepts JSON numbers and valid numeric strings), then `Float64()` → `int64(f)`; `ok=false` for absent/junk values and for values outside the int64 range (e.g. `1e300`, which would otherwise store an implementation-defined overflow value).
   - Add `parseUsageFields(raw) map[string]int64` — unmarshal `raw` into `map[string]json.RawMessage` (failure → empty map), then keep only present-and-numeric counter keys.
   - Add `extractUsageValues(raw) UsageValues` — build the four counters from `parseUsageFields` and compute `HasAny` on non-zero values (unchanged semantics).
   - Reimplement `ExtractUsageFromJSON`: top-level `{"usage": json.RawMessage}`; unmarshal failure → `parse_error`; absent/`!HasAny` → `missing`; else `provider`/`ok`.
2. In `internal/usage/sse.go`:
   - Change the `observeBlock` payload to `Usage json.RawMessage` and `Message.Usage json.RawMessage`.
   - Rewrite `merge(raw json.RawMessage)` to iterate `parseUsageFields(raw)` and overwrite only returned fields, setting `HasAny` per returned field.
   - Delete the `usageJSON` struct.
3. Tests:
   - `parse_test.go`: `TestExtractUsageToleratesNonNumericFields` (bigmodel shape), `TestExtractUsageToleratesNumericStringsAndFloats`, `TestExtractUsageNullAndZeroAndJunkUsage` (5 sub-cases), `TestExtractUsageInvalidJSONReturnsParseError`, `TestExtractUsageRejectsOutOfRangeNumbers` (huge-only → missing; mixed → valid fields kept).
   - `sse_test.go`: `TestSSEObserverToleratesNonNumericUsageFields` (asserts `diag.ParseErrors == 0`), `TestSSEObserverToleratesNumericStringsAndFloats`.
4. Offline regression: temporary probe (`go run`) executing `ExtractUsageFromJSON` on the captured production body `/tmp/mccdbg/body2.bin` before (→ `parse_error`) and after (→ `provider/ok`); probe deleted afterwards.
5. Run `go test ./...` and `go vet ./...`.

#### Verification

- [x] `go test ./...` — all 15 packages ok; `go vet` clean; no remaining references to `usageJSON`.
- [x] Captured-body regression (2026-07-17): before fix `source=none status=parse_error`; after fix `source=provider status=ok values={InputTokens:6 OutputTokens:1 ... HasAny:true}`.
- [x] `TestSSEObserverToleratesNonNumericUsageFields` confirms junk fields no longer increment `diag.ParseErrors` and usage still merges (input=10, output=7).
- [x] Live-repro evidence chain preserved in this spec's Overall Analysis: 499 historical `parse_error` rows (bigmodel 497 + kimi 2), 73 `ok` rows all from numeric-only providers — distribution fully explained by the root cause.
