# Provider Quota Query Spec

Local pages: `/` provider management, `/providers/:providerId/usage` quota configuration
Proxy entry: no model-proxy changes; authenticated `/api/providers/*/usage*` admin APIs only
Reference implementation: `UsageScriptModal`, `SubscriptionQuotaFooter`, `services/coding_plan.rs`, and `services/balance.rs` under `/home/www/workspace/open-software/cc-switch`
Stack: Go 1.26 + `net/http` + SQLite + restricted JavaScript runtime (recommended: `github.com/dop251/goja`) + Vue 3 + TypeScript + Tailwind + `lucide-vue-next`
Last updated: 2026-06-27
Status: draft
Progress: 0 / 10 planned

## Overall Analysis (Source Analysis)

### 1. Goal and Terminology

This feature adds provider-account quota queries to Provider Management. It is distinct from the existing Usage Statistics feature:

| Feature | Source | Meaning |
| --- | --- | --- |
| Existing `internal/usage` | Proxy responses and Claude session logs | Requests already made, token consumption, coverage, and request quality |
| Provider quota query | Provider balance/subscription APIs | Remaining account balance or utilization of 5-hour/7-day plan windows |

The backend package must be named `providerquota` to prevent confusion with `internal/usage`. The UI label remains “Usage”. The implementation must not modify the existing usage schema or aggregation semantics.

Phase one supports:

1. `custom`: configurable HTTP request plus JavaScript response extractor.
2. `general`: editable generic balance template.
3. `newapi`: NewAPI/One API-style account quota.
4. `token_plan`: Kimi, Zhipu, MiniMax, ZenMux, and Volcengine plan windows.
5. `official_balance`: DeepSeek, StepFun, SiliconFlow, OpenRouter, and Novita AI balance APIs.

Claude/Codex/Gemini CLI OAuth subscription quota is explicitly out of scope. It reads OAuth state from the server host and does not map to an individual provider card/API token.

Xiaomi MiMo Token Plan is also deferred from phase-one native adapters. Its console exposes private quota endpoints, but verification shows that they require Xiaomi-account browser session cookies rather than the provider card's `tp-...` inference API key, and the official documentation does not publish a server-side quota protocol. See “Xiaomi MiMo Investigation and Deferral”.

### 2. Current Project State

- Provider configuration is represented by `internal/config.Provider` and persisted through `ConfigStore`.
- Production uses SQLite tables `providers` and `provider_model_mappings`.
- `providerResponseMap` returns `api_token_mask`, never the raw provider token.
- `/api/providers/{id}/reveal-token` is the existing explicit raw-token endpoint.
- `DashboardView.vue` renders providers through `ProviderCard.vue`.
- Vue Router currently has `/login` and `/`; it can host a dedicated configuration route.
- `lucide-vue-next` already exists.
- `internal/usage` shares the SQLite database but remains dedicated to request statistics.

### 3. cc-switch Findings

cc-switch normalizes providers into two result shapes:

1. Time-window quotas: `five_hour`, `seven_day/weekly_limit`, and `monthly`, with used percentage and reset time.
2. Balances: total, used, remaining, unit, and plan name.

`5-hour: 2%` means 2% used, not 2% remaining. Color thresholds are green below 70%, orange from 70% through 89%, and red at 90% or above. The adjacent duration is time until reset; the relative timestamp is the last query time.

Absolute quantities are optional because providers differ:

- MiniMax currently exposes remaining percentage; convert it with `100 - remaining_percent`.
- Zhipu exposes used percentage.
- Kimi exposes limit/remaining; preserve them and derive percentage.
- ZenMux can expose used/max USD.
- A provider may return only a five-hour window; never synthesize a weekly window.

### 4. Alternatives

#### A. All JavaScript

Highly extensible, but known-provider behavior becomes string code with weaker credential, signing, error, and regression guarantees. Volcengine signing is a poor fit. Rejected.

#### B. All native Go adapters

Type-safe and testable, but cannot serve custom gateways without a new release. Rejected.

#### C. Native adapters plus restricted scripts (selected)

- `token_plan` and `official_balance` use native Go adapters.
- `general`, `newapi`, and `custom` use one restricted script executor.
- Every path returns `ProviderQuotaResult`.
- A manager owns caching, persistence, scheduling, manual refresh, and request deduplication.

### 5. Architecture

```text
ProviderCard / ProviderUsageView
              │
              ▼
Authenticated Admin API
              │
              ▼
providerquota.Manager
  ├── credential/config resolver
  ├── scheduler, singleflight, concurrency limiter
  ├── script executor (custom/general/newapi)
  ├── native adapters (token plan/official balance)
  └── SQLite latest-snapshot store
```

Boundaries:

- `Manager` is the only production query entry point.
- `Store` owns latest snapshots only, not provider configuration.
- `ScriptExecutor` parses scripts, builds controlled requests, and extracts responses; it does not schedule.
- Each native adapter converts one upstream protocol to the common model.
- Admin handlers parse authenticated HTTP requests and return redacted DTOs.
- The browser never receives provider credentials or calls third-party quota APIs directly.

### 6. Xiaomi MiMo Investigation and Deferral

#### Confirmed Console Endpoints

Inspection of an authenticated Xiaomi MiMo console page and its first-party requests confirmed:

```text
GET  https://platform.xiaomimimo.com/api/v1/tokenPlan/detail
GET  https://platform.xiaomimimo.com/api/v1/tokenPlan/usage
POST https://platform.xiaomimimo.com/api/v1/usage/token-plan/list?<session parameter>
```

`tokenPlan/usage` returns plan consumption and limit through `usage.items[name=plan_total_token]`; `tokenPlan/detail` returns `planCode`, `planName`, `currentPeriodEnd`, and `expired`. The detail-list endpoint accepts year/month and returns date/model/token/request fields.

#### Why It Is Not Integrated

1. Both summary endpoints return HTTP 401 when browser cookies are omitted.
2. The console uses `credentials: "same-origin"` and login cookies including `api-platform_ph`, `api-platform_slh`, `api-platform_serviceToken`, and `userId`.
3. The console request does not use the `tp-...` Token Plan inference key as quota-query authentication.
4. First-party frontend metadata describes these as login cookies with an approximately 24-hour lifetime.
5. Official Token Plan/FAQ documentation explains console viewing but does not publish a quota endpoint, compatibility promise, or API-key authentication contract.
6. Private endpoint/session-parameter changes cannot meet the reliability requirements of persistent automatic scheduling.

#### Phase-One Decision

- Do not add a Xiaomi MiMo native adapter.
- Do not ask users to paste, import, or persist Xiaomi-account cookies.
- Do not implement browser automation, DOM scraping, or `/apiKey/raw` access.
- Do not probe the private console endpoint with the `tp-...` inference key.
- A `token-plan-{cn,sgp,ams}.xiaomimimo.com` Base URL may show an explicit “No stable API-key quota endpoint is currently available” message, but must not be treated as a generic Token Plan adapter.

#### Re-evaluation Triggers

Re-evaluate when Xiaomi publishes a quota endpoint/contract, confirms a durable read-only server credential, or ships an officially supported SDK/CLI quota operation for coding-tool backends.

Future normalized mapping would be:

```text
window       = plan_period
used         = usage.items[name=plan_total_token].used
total        = usage.items[name=plan_total_token].limit
utilization  = usage.items[name=plan_total_token].percent * 100
resetsAt     = detail.currentPeriodEnd parsed as UTC
unit         = Credits
```

MiMo currently exposes monthly/yearly plan Credits and must not be represented as a fabricated five-hour or seven-day window.

Official references:

- `https://mimo.mi.com/docs/price/tokenplan/subscription`
- `https://mimo.mi.com/docs/en-US/quick-start/faq/api-integration`

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | Types, config, SQLite schema | `internal/providerquota/types.go`, provider config, snapshots | schema and store round trips |
| 2 | Planned | Restricted script executor | `script.go`, network policy, normalization | timeout/origin/size/extractor tests |
| 3 | Planned | Official balance adapters | `balance.go` | provider fixture tests |
| 4 | Planned | Token Plan adapters | `token_plan.go`, Volcengine signing | window parsing/signing tests |
| 5 | Planned | Manager, cache, scheduler | `manager.go`, `store.go` | singleflight/concurrency/due tests |
| 6 | Planned | Admin APIs | `provider_quota_handler.go` | method, validation, redaction tests |
| 7 | Planned | Frontend API, route, editor | `ProviderUsageView.vue`, `useApi.ts` | route/load/save/test-query tests |
| 8 | Planned | Provider card UI and icons | `ProviderCard.vue`, Dashboard wiring | layout/percentage/countdown/refresh tests |
| 9 | Planned | Lifecycle/import/export/i18n | handlers and bilingual strings | lifecycle and secret regressions |
| 10 | Planned | Full verification | evidence and status update | Go/frontend/race/vet/build/diff |

## Requirements

### 1. Scope

#### Required

1. Each provider has independent quota configuration and enablement.
2. Configuration uses `/providers/:providerId/usage`, not a modal.
3. The card title row places last query time, refresh, 5-hour and 7-day used percentages near the Active badge.
4. Action order is Edit, Duplicate, Usage, Test, Set Current (conditional), Enable/Disable, Delete.
5. Edit, Duplicate, Usage, and Test use contextual icons before their labels.
6. The editor retains the approved two-column layout: configuration on the left, latest result and manual refresh on the right.
7. Automatic interval accepts `0` (disabled) or `1–1440` minutes and defaults to 5.
8. A failed attempt preserves the previous successful data while exposing stale/failure state.
9. All third-party requests originate on the backend.
10. Model forwarding, provider activation, and existing request usage statistics must not change.

#### Non-goals

- Official CLI OAuth subscription quota.
- Quota history, billing detail, forecasts, alerts, or quota-based routing.
- Changes to `internal/usage` or its pages.
- Arbitrary browser JavaScript or script access to filesystem, environment, process, or network.
- A new at-rest secret-management system; new secrets follow the existing local provider-token security model.
- Xiaomi MiMo quota access through console session cookies, browser automation, or page scraping.

### 2. Query Types

#### `general`

Provide an editable default script that requests `{{baseUrl}}/user/balance` with `Bearer {{apiKey}}` and extracts `response.balance` as USD remaining balance. Dedicated query Base URL/API key are optional and fall back to provider `APIURL`/`APIToken`. The UI must describe this as a starting template, not an industry-standard API.

#### `newapi`

```text
GET {baseUrl}/api/user/self
Authorization: Bearer {accessToken}
New-Api-User: {userId}
Content-Type: application/json
```

Normalize `data.group` as plan name, divide `quota` and `used_quota` by 500000, and expose remaining/used/total in USD. Base URL, access token, and user ID are required. A business response with `success=false` becomes a structured failure.

#### `custom`

The script evaluates to an object containing `request` and `extractor`. The extractor returns one object or an array with optional:

```text
planName, window, utilization, resetsAt,
used, total, remaining, unit,
isValid, invalidMessage, extra
```

`utilization` always means used percentage. Derive it from used/total or remaining/total when possible. `extra` is capped at 256 characters. A normal balance is not a time window unless `window` is explicitly returned. Dedicated query Base URL/API key may override provider credentials. Request URLs must share the effective query Base URL origin.

#### `token_plan`

| Provider | Detection | Request/auth | Normalization |
| --- | --- | --- | --- |
| Kimi | `api.kimi.com/coding` | `GET https://api.kimi.com/coding/v1/usages`, Bearer | `limits[].detail` -> 5h; `usage` -> 7d; preserve limit/remaining/reset |
| Zhipu CN | `bigmodel.cn` | `GET https://open.bigmodel.cn/api/monitor/usage/quota/limit`, raw Authorization | `TOKENS_LIMIT`, unit 3 -> 5h, unit 6 -> 7d |
| Zhipu EN | `api.z.ai` | Same path on `https://api.z.ai` | same parsing |
| MiniMax CN | `api.minimaxi.com` | `GET .../v1/api/openplatform/coding_plan/remains`, Bearer | `model_name=general`; invert remaining percentage; weekly only when status=1 |
| MiniMax EN | `api.minimax.io` | same on `.io` | same parsing |
| ZenMux | `zenmux` | GET configured quota URL, Bearer | `quota_5_hour`, `quota_7_day`; usage fraction x100; preserve USD values |
| Volcengine | `volces.com/api/coding` | `open.volcengineapi.com`, dedicated AK/SK signing | try `GetAFPUsage`, then `GetCodingPlanUsage`; 5h/7d/monthly |

`token-plan-*.xiaomimimo.com` is not supported by this table. Implementers must follow the Xiaomi MiMo deferral decision rather than treating any `token-plan` hostname as a generic native adapter.

Volcengine AK/SK are separate from inference credentials and live in the provider quota configuration. Signing must be isolated in pure functions with fixed-time canonical-request/signature tests.

#### `official_balance`

Adapter selection must match the provider API host:

| Provider | Endpoint | Mapping |
| --- | --- | --- |
| DeepSeek | `https://api.deepseek.com/user/balance` | one item per `balance_infos` currency; `is_available` |
| StepFun | `https://api.stepfun.com/v1/accounts` | `balance`, CNY |
| SiliconFlow CN | `https://api.siliconflow.cn/v1/user/info` | `data.totalBalance`, CNY |
| SiliconFlow EN | `https://api.siliconflow.com/v1/user/info` | `data.totalBalance`, USD |
| OpenRouter | `https://openrouter.ai/api/v1/credits` | total credits, usage, computed remaining, USD |
| Novita AI | `https://api.novita.ai/v3/user/balance` | `availableBalance / 10000`, USD |

Use provider APIToken as Bearer. Map 401/403 to `invalid_credentials` and other non-2xx responses to `upstream_http_error`.

### 3. Data Model

Add `*ProviderQuotaConfig` to `config.Provider` with:

```text
enabled, template_type, timeout_seconds,
auto_query_interval_minutes, script,
base_url, api_key, access_token, user_id,
coding_plan_provider, access_key_id, secret_access_key
```

Validation:

- Template is one of the five supported values.
- Timeout defaults to 10 and is 2–30 seconds.
- Interval defaults to 5 and is 0 or 1–1440 minutes.
- Script is at most 64 KiB.
- Base URL is absolute HTTP/HTTPS without userinfo.
- NewAPI requires Base URL/access token/user ID.
- Token Plan must auto-detect or explicitly match its URL.
- Volcengine requires AK/SK; ZenMux requires an explicit quota URL.

Add `quota_query_config TEXT NOT NULL DEFAULT '{}'` to `providers`; JSON, SQLite, and mock stores must have the same semantics.

The common result contains:

```text
ProviderQuotaResult:
  provider_id, template_type, success, credential_status,
  tiers[], balances[], error_code, error_message,
  queried_at, duration_ms

QuotaTier:
  name, label, utilization, resets_at,
  used?, total?, remaining?, unit?

BalanceItem:
  plan_name, remaining?, used?, total?, unit?,
  is_valid?, invalid_message?, extra?
```

Rules:

- Utilization is used percentage.
- Reject NaN/Inf; clamp public UI percentage to 0–100 and classify malformed upstream values as `invalid_response`.
- Canonical windows are `five_hour`, `seven_day`, `monthly`; normalize `weekly_limit` to `seven_day`.
- Normalize reset times to UTC RFC3339.
- Empty tiers and balances are `empty_result`, not a successful blank result.

Latest snapshots use:

```sql
CREATE TABLE IF NOT EXISTS provider_quota_snapshots (
    provider_id TEXT PRIMARY KEY,
    result_json TEXT NOT NULL,
    last_success_json TEXT,
    queried_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);
```

Store the latest attempt and latest success separately. A failure updates the attempt but retains the previous success. No historical series is stored.

Any material query-semantic change (template, script, URL, credential, or native adapter) deletes that provider's snapshots. An interval-only change keeps snapshots. Consequently, `last_success_json` always belongs to the current configuration and no config-fingerprint column is required.

### 4. Restricted Script and Network Policy

Use a fresh pure-Go runtime for each phase:

1. Runtime A parses/serializes request configuration within 200 ms.
2. Go validates the request, substitutes placeholders, and performs controlled HTTP.
3. Runtime B reloads the script and invokes `extractor(responseJSON)` within 500 ms.
4. Go validates and normalizes returned values.

Do not inject `fetch`, `require`, filesystem, environment, process APIs, or native Go objects. Interrupt timeouts and release runtimes. Do not depend on Node.js.

Never substitute secrets into source code or pass them to the extractor. Scripts emit literal placeholders; Go substitutes them only into the final HTTP request.

HTTP restrictions:

1. GET/POST only; JSON body up to 256 KiB.
2. HTTP/HTTPS only. Require HTTPS unless the provider APIURL itself explicitly uses same-origin HTTP.
3. Same scheme, host, and effective port as the effective query Base URL.
4. At most three redirects, with same-origin validation on each.
5. Reject Host, Content-Length, Transfer-Encoding, Connection, and Proxy-Authorization headers.
6. Response body limit: 2 MiB.
7. Total timeout: configured 2–30 seconds, tied to context.
8. Redact userinfo, query, fragment, auth/cookies/tokens, and probable secrets from logs/errors.
9. Keep at most 512 redacted bytes of upstream error body.
10. Test and production queries use the same execution path and policy.

### 5. Manager, Cache, and Scheduler

`Manager.Query(ctx, providerID, options)` is the only production query entry point.

- Deduplicate concurrent queries per provider; manual and scheduled callers share the same in-flight result.
- Limit global upstream quota-query concurrency to four.
- Context cancellation must cancel HTTP and not leak goroutines.
- Use one 30-second scan ticker, not one permanent goroutine per provider.
- Auto-query only enabled providers with enabled quota config and interval > 0.
- A provider is due when no snapshot exists or the last attempt is at least one interval old.
- Apply deterministic 0–30 second startup jitter.
- Saving configuration notifies Manager to re-evaluate that provider.
- Interval 0 still allows manual and test queries.
- Start/stop Manager from the server root context.

Provider-list loads read SQLite snapshots and never cause N third-party requests. Dashboard polls the cheap batch snapshot endpoint every 30 seconds. Manual refresh replaces that provider’s snapshot immediately. Material configuration changes implement staleness by deleting snapshots and returning to “Never queried”; interval-only changes retain snapshots.

### 6. Admin API

All endpoints use existing session authentication:

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/api/providers/usage` | Public snapshots for all providers; no upstream query |
| GET | `/api/providers/{id}/usage` | Public config, latest attempt, latest success |
| PUT | `/api/providers/{id}/usage` | Validate/save config with secret patch semantics |
| POST | `/api/providers/{id}/usage/test` | Run unsaved draft; persist neither config nor snapshot |
| POST | `/api/providers/{id}/usage/query` | Manual production query and snapshot persistence |

Public config returns `*_configured` booleans and masked AccessKey ID only, never raw API key/access token/secret key.

Secret patch semantics:

- Missing/empty field keeps stored secret; if absent, supported templates fall back to provider APIToken.
- Explicit `clear_*` clears a dedicated secret.
- Non-empty value replaces it.
- Responses only expose configured flags.

Status semantics: 400 invalid local config/script/URL, 404 missing provider, 405 wrong method, 200 with `success=false` for completed upstream/auth/parse failures, and 500 for redacted local storage/internal failures. Provider subtree routing must recognize usage suffixes before generic provider-ID dispatch.

### 7. Frontend

Add route:

```ts
{
  path: '/providers/:providerId/usage',
  name: 'provider-usage',
  component: ProviderUsageView,
}
```

Preserve the approved layout:

- Breadcrumb/provider name/back button at top.
- Left configuration panel and right latest-result panel.
- “Refresh now” remains visible on the right; spin/disable while querying.
- Back navigates to `/?tab=providers`; Dashboard initializes its tab from the query.
- Missing provider shows a clear empty state.

Template fields:

- General: optional API key/Base URL override, timeout, interval, script editor.
- Custom: effective variables, optional overrides, timeout, interval, editor, return-field help.
- NewAPI: Base URL, Access Token, User ID, timeout, interval; script may be read-only.
- Token Plan: detected/selected provider; ZenMux URL/key; Volcengine AK/SK; others reuse provider credentials.
- Official balance: detected provider/fixed endpoint, no script editor.

Test runs the unsaved draft and updates a clearly marked test result only. It does not update the provider card. Saving does not implicitly query; manual refresh or the scheduler creates a production snapshot.

Provider card desktop title order:

```text
[select] [enabled] Provider Name ... [last query] [refresh] [5-hour] [7-day] [Active]
```

- Quota stays on the name/Active row on desktop; the whole group may wrap below on small screens.
- Show only when configured/enabled.
- No snapshot: “Never queried” plus refresh.
- Window example: `5-hour: 2% ◷2h30m`.
- Balance-only result shows the primary balance compactly.
- Failed latest attempt plus last success shows old values with a warning.
- Refresh stops event propagation and spins only for its card.

Action order is fixed. Use `Pencil`, `Copy`, `Gauge`/`ChartNoAxesColumnIncreasing`, and `CircleCheck`/`PlugZap` for Edit, Duplicate, Usage, and Test.

Formatting:

- Round displayed percentage to integer; title may show two decimals.
- Recompute countdown every 30 seconds as `XdYh`, `XhYm`, or `Xm`.
- Expired reset displays “Refresh pending”, never a negative duration.
- Relative query time uses just now/minutes/hours/days.
- Currency uses at most two decimals.
- Add ARIA labels and titles for icon controls, failures, and absolute reset times.

### 8. Provider Lifecycle

1. New provider: quota disabled; no automatic network call.
2. Provider edits preserve quota config.
3. Duplicate copies quota config/secrets, not snapshots.
4. Delete cascades snapshot deletion.
5. Disabled provider stops scheduling, retains config/snapshot, and cannot manually query.
6. Export includes quota config and its dedicated secrets under the existing secret warning; never exports snapshots.
7. Import validates quota config; old exports default to disabled.
8. Overwrite import replaces config and deletes/invalidates old snapshots.
9. Duplicate import gets new ID and no snapshot.
10. Provider-token updates affect fallback credentials; dedicated overrides remain unchanged.

### 9. Stable Error Codes

```text
not_configured, invalid_config, missing_credentials,
invalid_credentials, unsupported_provider,
request_timeout, network_error, upstream_http_error,
upstream_business_error, response_too_large,
invalid_json, script_timeout, script_error,
invalid_response, empty_result, internal_error
```

Frontend translates codes and never parses English messages. Logs may contain provider ID, adapter, status, and redacted URL only—never tokens, AK/SK, Authorization, or full response bodies.

### 10. Expected Files

```text
internal/providerquota/
  types.go normalize.go script.go balance.go token_plan.go
  volcengine_sign.go store.go manager.go *_test.go
internal/config/provider.go
internal/config/sqlite_store.go
internal/admin/provider_quota_handler.go
internal/admin/provider_quota_handler_test.go
internal/admin/provider_handler.go
internal/admin/server.go
cmd/server/main.go
internal/frontend/src/main.ts
internal/frontend/src/composables/useApi.ts
internal/frontend/src/composables/useI18n.ts
internal/frontend/src/components/ProviderCard.vue
internal/frontend/src/components/ProviderCard.test.ts
internal/frontend/src/components/ProviderQuotaResult.vue
internal/frontend/src/views/ProviderUsageView.vue
internal/frontend/src/views/ProviderUsageView.test.ts
internal/frontend/src/views/DashboardView.vue
```

Small files may be combined when boundaries remain clear. Do not place all backend behavior in handlers or add the entire page to the already-large Dashboard component.

### 11. Acceptance Criteria

Backend:

- [ ] All five templates can be saved, loaded, and tested.
- [ ] No new secret leaks through provider/config APIs.
- [ ] Script timeout, HTTP timeout, response limit, and origin policy have tests.
- [ ] Kimi/Zhipu/MiniMax/ZenMux/Volcengine fixtures produce correct windows.
- [ ] A Xiaomi MiMo Base URL returns an explicit deferred/unsupported message without calling private console endpoints or requesting cookies.
- [ ] Official balance fixtures produce correct balance items.
- [ ] Concurrent manual/scheduled requests for one provider produce one upstream call.
- [ ] Failure retains last success; provider deletion cascades snapshots.
- [ ] JSON and SQLite stores round-trip config.

Frontend:

- [ ] Direct refresh of `/providers/:id/usage` works through the auth guard.
- [ ] Approved two-column layout and right-side manual refresh remain.
- [ ] Card quota is on the desktop title row near Active.
- [ ] Card refresh only refreshes that provider.
- [ ] Usage is between Duplicate and Test; the four text actions have icons.
- [ ] Used-percentage semantics and color thresholds are correct.
- [ ] Expired reset never shows negative time.
- [ ] Chinese/English have no raw keys.
- [ ] No horizontal overflow at 360px, 768px, or 1440px.

Verification commands:

```bash
go test ./...
go test -race ./internal/providerquota ./internal/admin ./internal/config
go vet ./...
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
git diff --check
```

Manual verification covers one Kimi/MiniMax Token Plan, one official balance, NewAPI mock, custom mock, invalid credentials, timeout, and snapshot persistence after restart.

## Task Details

### Task 1: Types, Configuration, and SQLite Schema

#### Requirements

**Objective** — Establish an independent persistent quota model.
**Outcomes** — Provider quota config, normalized results, SQLite config column, snapshot table.
**Evidence** — JSON/SQLite/mock round trips and old-schema migration tests.
**Constraints** — Missing legacy field defaults to disabled; public responses exclude secrets.
**Edge Cases** — Empty/malformed JSON, unknown template, missing legacy column, cascade delete.
**Verification** — `go test ./internal/config ./internal/providerquota -count=1`.

#### Plan

1. Write failing defaults/validation/store tests.
2. Add types and normalization.
3. Add provider column and snapshot table.
4. Update load/upsert/migration/legacy JSON paths.
5. Verify public DTO redaction.

#### Verification

- [ ] Old DB upgrades without data loss.
- [ ] Every field round-trips.
- [ ] Public JSON excludes all raw quota secrets.

### Task 2: Restricted Script Executor

#### Requirements

**Objective** — Support general/NewAPI/custom while retaining network and secret control in Go.
**Outcomes** — Script parsing, placeholder substitution, controlled HTTP, extractor, normalization.
**Evidence** — httptest and malicious-script tests.
**Constraints** — No Node/fetch/file/env, no cross-origin request, no secret logging.
**Edge Cases** — Infinite loop, oversized response, redirect, forbidden header, malformed JSON, NaN.
**Verification** — Script/normalization tests including race.

#### Plan

1. Add goja and lock interrupt behavior with tests.
2. Implement two-phase runtimes and request schema.
3. Implement HTTP restrictions/redaction.
4. Normalize object/array output.
5. Add General/NewAPI defaults.

#### Verification

- [ ] Infinite loop interrupts.
- [ ] Script cannot read API key.
- [ ] Cross-origin request/redirect fails.
- [ ] Size and context timeout work.

### Task 3: Official Balance Adapters

#### Requirements

**Objective** — Implement known balances natively.
**Outcomes** — Host detection, requests, parsing, auth classification.
**Evidence** — Success/error fixture per provider.
**Constraints** — Fixed official endpoints.
**Edge Cases** — Numeric strings, empty arrays, zero balance, multiple currencies.
**Verification** — `go test ./internal/providerquota -run 'Balance' -count=1`.

#### Plan

1. Define adapter interface/detection.
2. Add parser fixtures.
3. Test headers/paths through injectable transport.
4. Add classification/redaction.

#### Verification

- [ ] DeepSeek supports multiple currencies.
- [ ] OpenRouter remaining is correct.
- [ ] Novita conversion is correct.

### Task 4: Token Plan Adapters

#### Requirements

**Objective** — Support Kimi, Zhipu, MiniMax, ZenMux, and Volcengine windows.
**Outcomes** — Canonical 5h/7d/monthly tiers and preserved quantities.
**Evidence** — JSON fixtures and Volcengine signature vectors.
**Constraints** — Used-percentage semantics; never fabricate weekly data.
**Edge Cases** — Old Zhipu one-tier plans, no-week MiniMax, timestamp variants, no Volcengine subscription.
**Verification** — Adapter/signing tests.

#### Plan

1. Implement detection/reset parsing.
2. Implement normal token adapters.
3. Implement Volcengine region/canonical query/HMAC/fallback.
4. Normalize errors and credential status.

#### Verification

- [ ] MiniMax 98% remaining -> 2% used.
- [ ] Zhipu unit 3/6 classification is not reset-order based.
- [ ] Kimi retains quantities.
- [ ] Volcengine signature is deterministic.

### Task 5: Store, Manager, and Scheduler

#### Requirements

**Objective** — Reliable snapshots and shared manual/automatic query execution.
**Outcomes** — Manager, snapshot store, ticker, limits, singleflight.
**Evidence** — Fake adapter/counter/clock tests.
**Constraints** — Page load causes no upstream fan-out; shutdown leaks no goroutine.
**Edge Cases** — Config changes during query, deletion, concurrent refresh, retry after failure.
**Verification** — Manager/store tests and race.

#### Plan

1. Inject clock/store/adapter resolver.
2. Implement query and transactional persistence.
3. Implement per-provider dedupe/global semaphore.
4. Implement scan/due/jitter.
5. Wire lifecycle.

#### Verification

- [ ] Ten same-provider refreshes produce one request.
- [ ] Global concurrency never exceeds four.
- [ ] Failure retains last success.

### Task 6: Authenticated Admin APIs

#### Requirements

**Objective** — Expose config, test, refresh, and batch snapshots without secrets.
**Outcomes** — Five APIs, handlers, wiring, tests.
**Evidence** — httptest for success/failure/method/redaction.
**Constraints** — Existing provider endpoints remain compatible.
**Edge Cases** — Suffix routing, missing provider, malformed JSON, concurrent query.
**Verification** — `go test ./internal/admin -run 'ProviderQuota' -count=1`.

#### Plan

1. Define public and secret-patch DTOs.
2. Add handlers/subtree dispatch.
3. Inject Manager.
4. Test redaction/statuses.

#### Verification

- [ ] Unauthenticated requests return 401.
- [ ] Wrong methods return 405.
- [ ] Test does not persist; Query does.

### Task 7: Frontend Route and Configuration Page

#### Requirements

**Objective** — Deliver the approved two-column editor for all five templates.
**Outcomes** — Route, API types, view, result component, i18n.
**Evidence** — Component tests and build.
**Constraints** — Keep page out of DashboardView; never reveal stored secrets.
**Edge Cases** — Direct route refresh, missing provider, save failure, test/production result distinction.
**Verification** — Frontend tests/build.

#### Plan

1. Add route/API mock tests.
2. Implement fields/configured-secret states.
3. Add script editor (styled textarea is acceptable unless an editor already exists).
4. Implement test/save/refresh state machine.
5. Reuse a result component.

#### Verification

- [ ] Correct fields per template.
- [ ] Empty secret does not clear stored secret.
- [ ] Back navigation keeps Provider tab.

### Task 8: Provider Card Quota and Action Icons

#### Requirements

**Objective** — Compact card quota display and confirmed action ordering.
**Outcomes** — Title-row quota/refresh/icons and Dashboard snapshot wiring.
**Evidence** — Card tests and responsive checks.
**Constraints** — Preserve selection, active state, toggles, and export.
**Edge Cases** — Long name, multiple windows, balance only, failed snapshot, mobile width.
**Verification** — Tests/build and three viewports.

#### Plan

1. Extend card props/emits.
2. Extract/test formatters.
3. Add title row and loading refresh.
4. Reorder actions/add Lucide icons.
5. Load batch snapshots and refresh one card.

#### Verification

- [ ] Usage is between Duplicate and Test.
- [ ] 2/80/95% use green/orange/red.
- [ ] Refresh affects one card.

### Task 9: Lifecycle, Import/Export, and i18n

#### Requirements

**Objective** — Keep new config consistent across provider lifecycle and languages.
**Outcomes** — Copy/delete/import/export behavior and complete zh/en.
**Evidence** — Handler/i18n tests.
**Constraints** — Retain existing export-secret warning; never export snapshots.
**Edge Cases** — overwrite/duplicate/skip, legacy format, unknown template.
**Verification** — Existing import/export suite with quota assertions.

#### Plan

1. Extend export/import validation.
2. Implement duplicate/overwrite snapshot rules.
3. Add all i18n keys and no-raw-key tests.
4. Regress existing provider actions.

#### Verification

- [ ] Legacy files import.
- [ ] New config export/import round-trips.
- [ ] Snapshot is absent from export.

### Task 10: Full Verification and Handoff

#### Requirements

**Objective** — Provide actual evidence for reliability, security, and no proxy regression.
**Outcomes** — Passing suite, verification record, updated status.
**Evidence** — Command output, fixture list, UI manual matrix.
**Constraints** — Compilation alone is not acceptance.
**Edge Cases** — Windows/Docker, restart, offline network.
**Verification** — Run every acceptance command.

#### Plan

1. Run focused tests and fix failures.
2. Run full Go/frontend/race/vet/build.
3. Manually test all templates with local mock servers.
4. Restart and verify snapshots/scheduler.
5. Update progress and evidence without inventing unrun results.

#### Verification

- [ ] Every required command actually passes.
- [ ] Existing proxy/usage/provider tests do not regress.
- [ ] Document status matches evidence.
