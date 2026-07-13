# Provider Quota Failover Spec

Local page: Provider Management and Dashboard main navigation (`DashboardView.vue`)<br>
Proxy entry: `internal/proxy/handler.go` (`POST /v1/messages`, `POST /anthropic/v1/messages`)<br>
Reference sources: `~/.claude/projects/` (84 JSONL files; 53 API failures), current quota/retry code<br>
Stack: Go 1.26, SQLite, Vue 3<br>
Last updated: 2026-07-13<br>
Progress: 6 / 6 implemented (see "Implementation verification evidence" at end)

## Overall Analysis (Source Analysis)

`ResolveModel` first routes an enabled `ExposedModel.ID` (the session `/model` override), then falls back to `ActiveProviderID`. Only that fallback may fail over; exposed-model routing remains pinned and never changes the default.

The complete local Claude history contains 18 HTTP 400, 16 HTTP 429, 2 HTTP 401, 2 HTTP 403, 4 HTTP 404, 1 HTTP 502, and 10 status-less failures. HTTP status alone is insufficient:

| Signal | Classification | Required action |
|---|---|---|
| 429 `[1308]`, `[1310]`, `quota exhausted` | quota exhausted | fail over; quarantine until parsed reset, otherwise 15m |
| 400 `no healthy deployments for this model` | provider-model unavailable | fail over; 1m quarantine |
| 401 invalid API key | credential invalid | fail over; recover only after Token change or successful provider test |
| 403 Cloudflare, 502, 529, ECONNRESET | provider unavailable | fail over; 5m for Cloudflare, 1m otherwise |
| 400 `1210`, tool validation/tool_reference, generic request error; 404 model_not_found; context limit | request/model error | never fail over |

Events cannot be associated reliably with a Claude `sessionId`. They are a global mcc SQLite list; DashboardView main navigation adds a top-level "Failover Events" tab adjacent to existing "Session Records", at the same content-switching level as Usage Statistics' overview/request-log/provider/model tabs. It is not a Session Browser or session-detail child tab. mcc MUST NOT write, append, or alter Claude JSONL files or exported transcript messages.

## Development Checklist

| # | Status | Task | Deliverable | Verification |
|---|---|---|---|---|
| 1 | Done | Config routing and atomic updates | setting, route marker, atomic mutation | `go test ./internal/config/...` |
| 2 | Done | Failover domain | classifier, SQLite state/events, recovery | `go test ./internal/failover/...` |
| 3 | Done | Proxy integration | replay, final response, active update | `go test ./internal/proxy/...` |
| 4 | Done | Quota/admin integration | reconciliation, Token recovery, APIs | `go test ./internal/admin/... ./internal/providerquota/...` |
| 5 | Done | Frontend | switch and global event panel | frontend tests/build |
| 6 | Done | Provider ordering and priority visibility | drag reorder, order badge, tooltip, ordering API | Go/admin/frontend tests/build |

## Requirements

1. Add `Config.AutoFailoverEnabled bool \`json:"auto_failover_enabled"\``, default false. JSON persists it directly; SQLite settings key is `auto_failover_enabled` with `0`/`1`.
2. Add `ModelRoute{Provider *Provider; BackendModel string; DefaultRouted bool}` and `ResolveRoute`; preserve `ResolveModel` as a wrapper. Exposed hit is never default-routed.
3. Add `internal/failover` state/events. Events are redacted, newest-first, capped at 1,000 rows and 30 days; no dangling IDs after provider deletion.
4. Classifier reads at most 64 KiB, restores a non-eligible response body byte-for-byte, recognizes the source-analysis table, and never treats a bare status as enough. `1308` is `five_hour_quota_exhausted`; `1310` is `weekly_quota_exhausted`. Parse RFC3339 and `2006-01-02 15:04:05`; a valid future reset overrides cooldown.
5. Candidates are enabled, non-quarantined providers in existing config order: first `candidate.MapModel(originalModel) == failedMappedModel`, then remaining candidates. Each retry rebuilds transformed body, URL, token, headers and format from immutable client input.
6. The first `<400` retry atomically changes `ActiveProviderID`, emits `switched`, and is the sole client response. Existing same-provider 429 retry runs first. Never replay exposed-model requests, unsafe methods, or a response that has started.
7. A fresh quota snapshot can quarantine at 100% and recover quota state when capacity returns. It MUST NOT recover a credential-invalid state. Credential state clears only after a stored non-empty API Token changes or `POST /api/providers/{id}/test` succeeds; editing name/models/URL or a failed test does not clear it.
8. Add authenticated `GET/PUT /api/providers/failover` and `GET /api/failover/events?limit=1..100`; UI adds an accessible title switch and a DashboardView top-level Failover Events tab adjacent to Session Records without modifying `SessionBrowser`, `SessionDetail`, transcript rendering, or export.
9. Provider Management supports dragging provider cards to reorder providers. That order is the user-controlled automatic failover priority: within the same-mapped-model candidate group and the fallback candidate group, candidates preserve the reordered provider list order. Each provider card shows a blue circular order badge (`index + 1`) at the top-right, immediately left of the quota display block; the badge renumbers immediately after reorder. The auto-failover switch includes an accessible question-mark tooltip explaining that provider cards can be dragged to adjust automatic failover priority. The badge is not a provider business field; order comes from the provider list returned by the backend.

## Task Details

### Task 1: Config routing and atomic mutation

#### Requirements

**Objective** - Identify default routing and prevent automatic/manual configuration writes from losing each other.

**Outcomes** - `AutoFailoverEnabled`, `ModelRoute`/`ResolveRoute`, and atomic config update are available in JSON and SQLite stores.

**Evidence** - Tests prove exposed route false, active fallback true, old SQLite false, and concurrent switch/active updates preserve both fields.

**Constraints** - Preserve current `ResolveModel`, `GetActiveProvider`, JSON compatibility, and provider order.

**Edge Cases** - Disabled exposed model, no active provider, old DB, concurrent activate and failover.

**Verification** - `go test -v -race ./internal/config/...` passes.

#### Plan

Files: modify `internal/config/config.go`, `store.go`, `sqlite_store.go`; test `config_test.go`, `store_test.go`, `sqlite_store_test.go`.

- [x] Write failing `TestResolveRoute*` cases for exposed hit, fallback, disabled skip, and nil provider.
- [x] Run `go test ./internal/config -run TestResolveRoute -count=1`; expected: FAIL (`ResolveRoute` missing).
- [x] Add `ModelRoute`, `ResolveRoute`, and compatibility wrapper `ResolveModel` returning its provider/model.
- [x] Run the same command; expected: PASS.
- [x] Write failing persistence/concurrency tests for `AutoFailoverEnabled` and atomic active-ID plus toggle changes.
- [x] Run `go test ./internal/config -run 'TestAutoFailover|TestAtomicConfig' -count=1`; expected: FAIL.
- [x] Add the config field and store update function: lock, load newest config, apply `func(*Config) error`, validate, save, return committed copy; persist SQLite `0`/`1`; move every config writer to it.
- [x] Run `go test -v -race ./internal/config/...`; expected: PASS.
- [x] Commit: `git add internal/config && git commit -m "feat(config): add atomic failover settings"`.

#### Verification

```bash
go test -v -race ./internal/config/...
```

Acceptance assertions:

- [x] `TestResolveRouteExposedModelIsNotDefaultRouted` returns the exposed provider/backend model with `DefaultRouted=false`.
- [x] `TestResolveRouteActiveFallbackIsDefaultRouted` returns the active provider `MapModel` value with `DefaultRouted=true`.
- [x] `TestResolveRouteSkipsDisabledExposedModel` and `TestResolveRouteWithoutActiveProvider` preserve fallback/nil behavior.
- [x] `TestAutoFailoverEnabledJSONRoundTrip` and `TestAutoFailoverEnabledSQLiteRoundTrip` reload true; an old database without the setting reloads false.
- [x] `TestAtomicConfigUpdatePreservesConcurrentActiveProviderAndFailoverSetting` repeatedly retains both changes under `-race`.

Expected: exit 0, no data race, and existing `ResolveModel` tests remain green.

### Task 2: Failover classification, state, and recovery

#### Requirements

**Objective** - Persist redacted failure state and classify only known quota, credential, deployment, and availability failures.

**Outcomes** - New `internal/failover/{types,classifier,store,manager}.go`, tables, retention, recovery, and candidate ordering.

**Evidence** - Table-driven tests cover all signals in Overall Analysis, body restoration, retention, token/test recovery, snapshot recovery, and concurrency.

**Constraints** - Read 64 KiB maximum; non-eligible body is restored exactly; static provider order only.

**Edge Cases** - Malformed/oversize body, bare 429, invalid reset, unchanged Token, failed test, missing quota query.

**Verification** - `go test -v -race ./internal/failover/...` passes.

#### Plan

Files: create `internal/failover/types.go`, `classifier.go`, `store.go`, `manager.go` and tests; modify `internal/config/sqlite_store.go` migration.

- [x] Write failing `TestClassify` table rows for 1308, 1310, quota text, healthy deployment, 401, 403, 502/529/ECONNRESET, and all non-switch examples.
- [x] Run `go test ./internal/failover -run TestClassify -count=1`; expected: FAIL (package missing).
- [x] Implement `Classification{Eligible, Reason, UpstreamCode, DisabledUntil}` and `captureAndRestore`: read `io.LimitReader(resp.Body, 64*1024+1)`, parse only `error.code`, `error.message`, `code`, `message`, string `error`; restore exact bytes when not eligible.
- [x] Use 15m quota fallback, 1m deployment/502/529/reset, 5m Cloudflare, and credential state with no time-based recovery.
- [x] Write failing store tests for `provider_failover_state`, `provider_failover_events`, 30-day/1,000 retention, no secret fields, expiry, Token-change/test recovery, unchanged-token non-recovery, and snapshot recovery.
- [x] Implement CRUD/list/pruning, `ClearCredentialFailure(providerID, tokenChanged, testSucceeded)`, and manager selection under one mutex (same model pass then fallback pass).
- [x] Run `go test -v -race ./internal/failover/...`; expected: PASS.
- [x] Commit: `git add internal/failover internal/config/sqlite_store.go && git commit -m "feat(failover): classify and quarantine providers"`.

#### Verification

```bash
go test -v -race ./internal/failover/...
```

Acceptance assertions:

- [x] `TestClassify1308WithReset` and `TestClassify1310WithReset` assert code, reason, and future reset; invalid/past/>7-day reset falls back to 15m.
- [x] `TestClassifyHealthyDeployment400`, `TestClassifyInvalidAPIKey401`, `TestClassifyCloudflare403`, and `TestClassifyAvailabilityFailures` produce only the specified cooldown/credential state.
- [x] `TestBare429DoesNotFailover`, `TestClassify1210DoesNotFailover`, `TestModelNotFoundDoesNotFailover`, `TestContextLimitDoesNotFailover`, and tool compatibility cases assert `Eligible=false`.
- [x] `TestClassifierRestoresNonEligibleBody` compares exact bytes; `TestOversizedBodyDoesNotFailover` rejects bodies over 64 KiB.
- [x] `TestFailoverEventRetention` proves 30-day/1,000-row pruning; `TestFailoverEventRedactsSecrets` excludes token, request body, and query.
- [x] `TestCredentialFailureRequiresTokenChangeOrSuccessfulTest` rejects name/mapping-only edit and failed test; accepts Token change and successful test.
- [x] `TestConcurrentFailoverSelectionHasSingleWinner` asserts one active update and one `switched` event under `-race`.

Expected: exit 0; state/event transactions are consistent and race-free.

### Task 3: Proxy replay and default activation

#### Requirements

**Objective** - Retry eligible default-routed requests without double writes or cross-provider conversion/auth leakage.

**Outcomes** - Proxy selects candidates and records only the final response/provider.

**Evidence** - Httptest proves disabled passthrough, same-model-first/fallback switch, default persistence, exposed-model isolation, no-switch errors, availability switch, and exhausted candidates.

**Constraints** - Existing `DoWithRetry429` runs before classification; no client response starts before selection.

**Edge Cases** - OpenAI format, queue/retry settings, concurrent switches, client disconnect.

**Verification** - `go test -v -race ./internal/proxy/...` passes.

#### Plan

Files: modify `internal/proxy/handler.go`, `internal/proxy/ratelimit/retry429.go`, `cmd/server/main.go`; test `internal/proxy/server_test.go` and focused failover tests.

- [x] Write failing `TestFailover*` httptest cases from Evidence.
- [x] Run `go test ./internal/proxy -run TestFailover -count=1`; expected: FAIL.
- [x] Add `Handler.SetFailoverManager(*failover.Manager)` and a helper accepting `(originalBody, provider, backendModel)` that creates fresh transformed body, URL, token, headers, and API format.
- [x] Run same-provider retry first; classify its final response only; apply candidate queue/retry and close discarded responses.
- [x] Persist active provider only after `<400`, emit `switched`/`retry_failed`/`exhausted`, and record usage once for the final response.
- [x] Run `go test -v -race ./internal/proxy/...`; expected: PASS.
- [x] Commit: `git add internal/proxy cmd/server/main.go && git commit -m "feat(proxy): fail over default providers"`.

#### Verification

```bash
go test -v -race ./internal/proxy/...
```

Acceptance assertions:

- [x] `TestFailoverDisabledPasses1308Through` returns original 429, leaves active ID unchanged, and emits no switch event.
- [x] `TestFailoverSwitchesSameMappedModelFirst` retries the first same-mapped-model provider, returns 2xx, and persists its active ID/event.
- [x] `TestFailoverFallsBackInProviderOrder` skips failed, disabled, and quarantined providers and follows config order.
- [x] `TestFailoverNeverChangesExposedModelRoute` proves an `ExposedModel.ID` request never retries or changes active provider.
- [x] `TestFailoverDoesNotSwitchRequestOrModelErrors` covers bare 429, 1210, 404, and tool errors; `TestFailoverSwitchesAvailabilityFailure` covers 502.
- [x] `TestFailoverRebuildsOpenAIAndAnthropicRequests` asserts candidate URL, auth, model mapping, and conversion are candidate-owned.
- [x] `TestFailoverRecordsOnlyFinalUsage` has one final-provider usage row; `TestFailoverAllCandidatesExhausted` does not double-write headers/body.

Expected: exit 0, full proxy regression green, and `-race` reports no race.

### Task 4: Quota and admin recovery APIs

#### Requirements

**Objective** - Reconcile quota evidence, recover credential state only from proof, and expose authenticated controls.

**Outcomes** - Quota notifier/ticker, token-update/provider-test hooks, and failover API handlers.

**Evidence** - Admin tests prove auth, method/body/limit validation, redaction, token changed recovery, unchanged-token non-recovery, test success recovery, and event ordering.

**Constraints** - Quota credentials may differ from inference Token; quota success never clears 401 state.

**Edge Cases** - Failed test, deleted/disabled provider, stale snapshot, invalid limit.

**Verification** - `go test -v -race ./internal/admin/... ./internal/providerquota/...` passes.

#### Plan

Files: modify `internal/providerquota/manager.go`, `internal/admin/server.go`, `provider_handler.go`; create `internal/admin/failover_handler.go`; add admin/quota tests.

- [x] Write failing API/auth tests for `GET/PUT /api/providers/failover` and event list, including malformed body and limits.
- [x] Run `go test ./internal/admin -run TestFailover -count=1`; expected: FAIL.
- [x] Register routes and return only `{"enabled":bool}` / `{"events":[...]}` via Task 1 atomic update.
- [x] Write failing token-update and successful/failed provider-test recovery tests.
- [x] Compare old/new stored Token before update; clear credential state only when non-empty Token changed. Clear it after successful provider test only; failed test leaves state. Reconcile fresh snapshots after persistence and every 30s, clearing quota state only.
- [x] Run `go test -v -race ./internal/admin/... ./internal/providerquota/...`; expected: PASS.
- [x] Commit: `git add internal/admin internal/providerquota && git commit -m "feat(admin): expose failover controls and recovery"`.

#### Verification

```bash
go test -v -race ./internal/admin/... ./internal/providerquota/...
```

Acceptance assertions:

- [x] `TestFailoverSettingsRequireAuth` returns 401; `TestFailoverSettingsMethods` returns 405 for bad methods and 400 for bad JSON/unknown fields.
- [x] `TestFailoverSettingsRoundTrip` persists true across reload; `TestFailoverEventsLimitAndOrder` proves default 50, clamp 1..100, and `occurred_at DESC,id DESC` order.
- [x] `TestFailoverEventsDoNotExposeSecrets` excludes token, response body, and query URL.
- [x] `TestProviderTokenChangeClearsCredentialFailure` accepts only a non-empty changed Token; `TestProviderEditWithoutTokenChangeKeepsCredentialFailure` rejects name/URL/model-only edits.
- [x] `TestSuccessfulProviderTestClearsCredentialFailure` emits `recovered`; failed test remains quarantined.
- [x] `TestQuotaSnapshotRecoveryDoesNotClearCredentialFailure` and `TestQuotaSnapshotExhaustionQuarantinesUntilReset` distinguish quota and credential state.
- [x] `TestProviderDeleteLeavesNoDanglingFailoverEventIDs` proves returned events have no deleted provider IDs.

Expected: exit 0, all endpoints remain authenticated, and quota credentials never leak.

### Task 5: Provider switch and global event UI

#### Requirements

**Objective** - Control automatic failover and inspect global events without changing Claude JSONL or export content.

**Outcomes** - Accessible Provider Management switch and localized DashboardView main-navigation adjacent Session Records/Failover Events tabs.

**Evidence** - Frontend tests assert API methods, switch revert, main-navigation ordering and independent event page, global disclosure/fields, and no SessionDetail/export mutation.

**Constraints** - Render escaped text; refresh provider cards every 15s only while Providers tab is active; no websocket. The existing Session Records page content area is out of scope and MUST remain visually and behaviorally unchanged.

**Edge Cases** - Save/API failure, no event, no selected session, no-target event, tab refresh, time-zone display.

**Verification** - frontend tests and build pass.

#### Plan

Files: modify `internal/frontend/src/composables/useApi.ts`, `useI18n.ts`, `views/DashboardView.vue`; add `views/FailoverEventsView.vue`; add/update frontend tests. Do not edit `components/SessionBrowser.vue`, `components/SessionDetail.vue`, session export code, or JSONL rendering unless an existing type import must be adjusted by the compiler.

- [x] Write failing tests for `getFailoverSettings`, `setFailoverSettings`, `getFailoverEvents`, title switch, `tab.failover` immediately after `tab.sessions` in main navigation, independent event page, global transcript disclaimer, and source/target/model/reason/outcome fields.
- [x] Run `npm --prefix internal/frontend test -- --run`; expected: FAIL.
- [x] Add typed `FailoverEvent`/settings APIs, save-disabled optimistic switch with failure rollback, and 15s active-tab provider refresh.
- [x] Extend DashboardView `MainTab` with `'failover'`, put `{ key: 'failover', labelKey: 'tab.failover' }` immediately after `sessions`, keep the existing `activeTab === 'sessions'` branch and all `SessionBrowser` props/children exactly as they are today, and create `FailoverEventsView.vue` rendered only for `activeTab === 'failover'`; fetch on entering/refreshing it; never pass events to SessionBrowser/SessionDetail/export.
- [x] Add semantically matching Chinese/English i18n keys.
- [x] Run `npm --prefix internal/frontend test && npm --prefix internal/frontend run build`; expected: PASS.
- [x] Commit: `git add internal/frontend && git commit -m "feat(ui): show provider failover events"`.

#### Verification

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

Acceptance assertions:

- [x] `useApi` tests assert GET settings, PUT JSON settings, and safely encoded event limit.
- [x] `DashboardFailoverSwitch` asserts a labelled adjacent switch, disabled save state, PUT failure rollback, and active-Providers-tab-only 15-second refresh.
- [x] `DashboardFailoverTab` asserts `tab.failover` immediately follows `tab.sessions`; `FailoverEventsView` asserts global disclaimer plus source→target, model, reason, status/code, outcome, and disabled-until; returning to Session Records does not mutate SessionBrowser state, DOM structure, selected session, filters, export behavior, or transcript rendering.
- [x] Tests prove events are not passed as `SessionDetail.messages` and export remains `/api/sessions/{id}/export` only.
- [x] Every new i18n key exists in both locales; API failure preserves existing error state and JSONL content.

Expected: both commands exit 0 and generate `internal/frontend/dist` without TypeScript/Vite errors.

### Task 6: Provider ordering, priority badge, and failover-priority help

#### Requirements

**Objective** - Let users directly control automatic failover priority and clearly see the current priority order on the Provider Management page.

**Outcomes** - Provider cards support drag reorder; order persists; failover candidates use the reordered order; provider cards show a circular priority badge; the auto-failover switch has an accessible tooltip explaining reorder priority.

**Evidence** - Backend tests prove ordering API auth, validation, persistence, and SQLite restart stability; failover tests prove same-model and fallback groups use the new order; frontend tests prove dragging calls the ordering API, failures roll back, badges renumber, and the tooltip is accessible/localized.

**Constraints** - Do not add a provider business-priority field for frontend consumption; the displayed number is always `index + 1` from the current provider list. `Set current`, auto-failover, enable/disable, quota refresh, Token edits, and model-mapping edits must not change order. Disabled providers remain visible and numbered but are still filtered from failover candidates. Do not affect `/model` session routing.

**Edge Cases** - Missing IDs, duplicate IDs, unknown IDs, omitted existing providers, empty list, concurrent provider deletion/creation, API failure, dropping at the same position, disabled provider first, current provider later in list, mobile/keyboard users who cannot drag.

**Verification** - `go test -v -race ./internal/config/... ./internal/admin/... ./internal/failover/... ./internal/proxy/...`, `npm --prefix internal/frontend test`, and `npm --prefix internal/frontend run build` all pass.

#### Design

Ordering semantics:

- Provider list order is the automatic failover priority order.
- Failover candidate selection keeps the existing two-pass logic: first collect candidates where `candidate.MapModel(originalModel) == failedMappedModel`, then collect other fallback candidates. Each pass must preserve provider list order.
- Drag reorder changes only list order, never `ActiveProviderID`. Auto-failover changes only `ActiveProviderID`, never list order.
- `ExposedModel` / `/model` pinned routing does not use this order for automatic failover.

Backend persistence:

- JSON store uses the `Config.Providers` slice order directly.
- SQLite store MUST add `providers.sort_order INTEGER NOT NULL DEFAULT 0`; otherwise the current `ORDER BY created_at ASC, id ASC` will overwrite drag order after restart.
- SQLite migration adds `sort_order` in `ensureProviderColumns`; saving providers writes `sort_order=index`; loading providers orders by `sort_order ASC, created_at ASC, id ASC`.
- Old databases where all rows have `sort_order=0` keep their initial `created_at ASC, id ASC` order until the first save/reorder writes stable sort values.

Backend API:

- Add authenticated endpoint: `PUT /api/providers/order`.
- Request body: `{"provider_ids":["id-a","id-b","id-c"]}`.
- Success response: `{"success":true,"providers":[...]}` where `providers` uses the existing redacted `providerResponseMap` format and the new order.
- Use `configStore.Update` atomically: load latest config under lock, validate the ID set, reorder `cfg.Providers`, save.
- Validation rules:
  - invalid JSON, missing `provider_ids`, or non-array `provider_ids`: return 400;
  - empty `provider_ids` while current providers are non-empty: return 409;
  - duplicate IDs: return 400;
  - unknown IDs: return 400;
  - omitted existing providers or length mismatch with the current provider count: return 409;
  - current providers empty and request array empty: return 200 with empty `providers`;
  - concurrent create/delete causing a set mismatch returns 409 Conflict.
- The endpoint must not return raw API Tokens, change `ActiveProviderID`, or mutate provider content fields.

Frontend interaction:

- Implement provider-list drag reorder in `DashboardView.vue`. Prefer native HTML5 drag/drop or a small local implementation; do not add a large drag dependency unless the project already uses it.
- On drag start store the dragged provider ID; while dragging over cards, show a clear insertion or hover state; on drop, optimistically reorder local `providers`.
- Call `api.reorderProviders(providerIds)` to persist. On success replace local providers with the returned ordered providers and preserve `selectedProviderIds`. On failure roll back to the previous order and show a short error.
- Dropping at the same position must not call the API.
- Reordering must not trigger `activate`, `edit`, `delete`, `toggle`, `usage`, or `refresh-quota` card actions.
- Provider cards need an explicit drag affordance. Either make the whole card draggable or add a drag handle in the card header. If adding a handle, use the existing icon system such as lucide `GripVertical`, with `title` / `aria-label`.
- Mobile and keyboard users need an accessible alternative: each card provides move-up/move-down buttons or menu items that call the same reorder function. Buttons are disabled for the first/last item. If this is not implemented, it is a blocker, not a nice-to-have.

Priority badge:

- `ProviderCard` renders a circular order badge at the top-right, immediately left of the quota display block.
- Badge text is the current list position `index + 1`, for example `1`, `2`, `3`.
- Badge uses a blue background and white text; suggested class shape: `inline-flex h-6 w-6 items-center justify-center rounded-full bg-primary text-white text-xs font-bold`, adjusted to existing theme tokens.
- Badge renumbers immediately when local list order changes. It must not be saved as an independent provider field. The backend does not need to return `priority`.
- Badge has an `aria-label`, for example `Failover priority 1`.
- Disabled providers still show the badge and number because the number means list position, not current availability.

Auto-failover tooltip:

- Add a question-mark icon next to the "Auto failover" switch text.
- Tooltip appears on hover and keyboard focus; mouse-only tooltip is not acceptable.
- Chinese tooltip:

  `开启后，默认供应商遇到额度耗尽、凭据失效或供应商不可用时，会按供应商列表顺序自动切换。可拖拽供应商卡片调整自动切换优先级。不会影响会话内 /model 选择。`

- English tooltip:

  `When enabled, MCC automatically switches the global default provider by provider list order when quota, credential, or availability failures occur. Drag provider cards to adjust failover priority. This does not affect in-session /model choices.`

- Tooltip text must use i18n keys. Suggested keys:
  - `failover.switch_help`
  - `providers.reorder_failed`
  - `providers.drag_handle`
  - `providers.move_up`
  - `providers.move_down`
  - `providers.priority_label`

#### Plan

Files:

- Backend: `internal/config/sqlite_store.go`, `internal/admin/server.go`, `internal/admin/provider_handler.go` or new `internal/admin/provider_order_handler.go`, plus related admin/config/proxy/failover tests.
- Frontend: `internal/frontend/src/composables/useApi.ts`, `useI18n.ts`, `views/DashboardView.vue`, `components/ProviderCard.vue`, related frontend tests; rebuild `internal/frontend/dist`.

Steps:

- [x] Write failing backend tests: `TestProviderOrderRequiresAuth`, `TestProviderOrderRejectsInvalidSets`, `TestProviderOrderPersistsInSQLiteOrder`, `TestProviderOrderDoesNotChangeActiveProvider`. (Actual SQLite coverage is `TestSQLiteProviderOrderRoundTrip`, `TestSQLiteProviderOrderSurvivesReopen`, and `TestSQLiteProviderOrderOldDBFallsBackToCreatedAt`; additional effective-default regressions are `TestProviderOrderPreservesEffectiveDefaultWhenActiveIDEmpty` and `TestProviderOrderPreservesEffectiveDefaultWhenActiveIDMissingOrDisabled`.)
- [x] Run `go test ./internal/admin ./internal/config -run 'TestProviderOrder|TestSQLiteProviderOrder' -count=1`; expected: FAIL. (Completed by the implementer in TDD flow and archived in review notes.)
- [x] Add SQLite `providers.sort_order` migration; save provider slice index to `sort_order`; load by `sort_order, created_at, id`; JSON store needs no extra field.
- [x] Implement `PUT /api/providers/order` behind `authMiddlewareFunc` and `configStore.Update`; validate the complete ID set and return redacted ordered providers.
- [x] Run the backend tests above; expected: PASS.
- [x] Write failover-order tests: construct/order providers `[A, C, B, D]`, A fails, B/C same mapped model, D fallback; assert candidate order `[C, B, D]`; reorder to `[A, B, C, D]`, assert `[B, C, D]`.
- [x] Run `go test ./internal/failover ./internal/proxy -run 'TestSelectCandidatesUsesProviderOrder|TestFailoverUsesReorderedProviderPriority' -count=1`; expected: FAIL first, PASS after implementation. (Implemented as failover manager candidate-order tests; proxy path remains covered by existing proxy failover tests plus the ordered candidate unit tests. No same-named proxy test was added.)
- [x] Add frontend `reorderProviders(providerIds: string[])` API; test it sends PUT `/api/providers/order` with complete `provider_ids` and throws on non-2xx.
- [x] Modify `ProviderCard.vue` to accept an `orderIndex` or `priority` prop for display only, derived by the parent as `index + 1`; render the order badge left of quota display with an aria-label.
- [x] Modify `DashboardView.vue` provider list: implement drag/drop plus move-up/move-down fallback; optimistic reorder, success replacement, failure rollback; same-position drop does not request; preserve `selectedProviderIds`.
- [x] Add the question-mark tooltip beside the auto-failover switch, using i18n and supporting hover/focus.
- [x] Write frontend tests: `DashboardProviderReorder.test.ts`, `ProviderCardPriorityBadge.test.ts`, `DashboardFailoverTooltip.test.ts`, covering drag/move buttons, rollback, badge numbering, tooltip text, and zh/en i18n keys. (Tooltip assertions live in `DashboardProviderReorder.test.ts` / `DashboardViewFailover.test.ts`; no separate `DashboardFailoverTooltip.test.ts` file was added.)
- [x] Run `npm --prefix internal/frontend test`; expected: PASS.
- [x] Run `npm --prefix internal/frontend run build`; expected: PASS and `dist` updated.
- [x] Run `go test -v -race ./internal/config/... ./internal/admin/... ./internal/failover/... ./internal/proxy/...`; expected: PASS.
- [x] Commit: `git add internal/config internal/admin internal/failover internal/proxy internal/frontend && git commit -m "feat(providers): reorder failover priority"`. (Implementation commit: `74c3b20`; follow-up fixes: `f30879a`, `5bbf17f`.)

#### Verification

```bash
go test -v -race ./internal/config/... ./internal/admin/... ./internal/failover/... ./internal/proxy/...
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

Acceptance assertions:

- [x] `TestProviderOrderRequiresAuth`: unauthenticated returns 401; non-PUT returns 405.
- [x] `TestProviderOrderRejectsInvalidSets`: invalid JSON/non-array/duplicate ID/unknown ID return 400; missing ID, wrong length, and concurrent set mismatch return 409; config remains unchanged.
- [x] SQLite ordering tests: `TestSQLiteProviderOrderRoundTrip` and `TestSQLiteProviderOrderSurvivesReopen` prove `Load()` and a closed/reopened SQLite store retain the new order; `TestSQLiteProviderOrderOldDBFallsBackToCreatedAt` proves old-DB compatibility.
- [x] `TestProviderOrderDoesNotChangeActiveProvider`: explicitly set `ActiveProviderID` is unchanged by reorder; `TestProviderOrderPreservesEffectiveDefaultWhenActiveIDEmpty` and `TestProviderOrderPreservesEffectiveDefaultWhenActiveIDMissingOrDisabled` prove empty/missing/disabled active IDs do not silently change the effective default provider.
- [x] `TestSelectCandidatesUsesProviderOrderWithinSameMappedModel`: same-mapped-model group strictly follows reordered order.
- [x] `TestSelectCandidatesUsesProviderOrderWithinFallbackGroup`: fallback group strictly follows reordered order.
- [x] `TestFailoverUsesReorderedProviderPriority`: proxy failover contacts the first available high-priority candidate and updates active provider to it. (No same-named proxy test was added; covered by the `SelectCandidates` order tests, SQLite persistence tests, and existing proxy failover tests together.)
- [x] `useApi.reorderProviders` asserts PUT `/api/providers/order`, full `provider_ids` body, and non-2xx throw.
- [x] `ProviderCardPriorityBadge` asserts the badge is left of quota display, shows `1/2/3`, uses i18n aria-label, and appears on disabled providers.
- [x] `DashboardProviderReorder` asserts drag/handle gating, success adopts server order, failure rolls back and shows `providers.reorder_failed`; `drag handle arms the outer draggable card before dragstart fires` covers the mouse-drag-from-handle regression.
- [x] `DashboardProviderKeyboardReorder` asserts move-up/move-down buttons work, first move-up is disabled, last move-down is disabled. (Covered in `DashboardProviderReorder.test.ts`.)
- [x] `DashboardFailoverTooltip` asserts the question icon is adjacent to the auto-failover switch, and hover/focus text includes "drag provider cards to adjust failover priority" plus "does not affect /model" in both locales. (Covered in `DashboardProviderReorder.test.ts` / `DashboardViewFailover.test.ts`.)
- [x] Regression asserts `SessionBrowser.vue`, `SessionDetail.vue`, session export, and JSONL parsing remain untouched by ordering.

Manual smoke verification (2026-07-13, confirmed by the user):

- [x] In Provider Management, dragging by the provider-card handle reorders providers with the mouse.
- [x] Move up / move down buttons reorder providers; first/last disabled states are correct.
- [x] Priority numbers update with drag and button reorder.
- [x] Clicking edit/delete/test/set-current card actions does not accidentally start drag.
- [x] Auto-failover question-mark tooltip is visible; ordering does not affect Session Records, the Failover Events tab, or JSONL rendering.

Expected: all commands exit 0; order remains stable after restart; drag and keyboard reorder change failover priority; the auto-failover tooltip explains the ordering relationship; no token/body/query leakage.

## Implementation verification evidence (2026-07-12 / 2026-07-13)

Tasks 1-6 are implemented and committed on branch `provider-quota-failover` (local commits, not pushed). Task 6 implementation, review fix, and manual-smoke drag fix are `74c3b20`, `f30879a`, and `5bbf17f`; bilingual review archival is `b74dec3`.

Verification commands and results:

- `go test -race ./...`: 1525 passed (with `-race`).
- `npm --prefix internal/frontend test`: 194 passed.
- `npm --prefix internal/frontend run build`: succeeds; `dist` updated.
- `git diff --check`: clean; `git status`: clean.
- Manual smoke (user-confirmed): provider cards can be reordered by dragging the handle; move up / move down works; priority numbers follow order; card action buttons do not accidentally trigger drag.

Boundary self-audit (mapped to user emphasis):

- Auto-switch changes only `ActiveProviderID` (default route); `ExposedModel` (/model session route) has `DefaultRouted=false` and never fails over (`TestFailoverNeverChangesExposedModelRoute`).
- Events live in MCC-owned SQLite (`provider_failover_state` / `provider_failover_events`); no writes/edits to any `~/.claude/projects/**/*.jsonl` (grep confirms the failover code has no JSONL/file writes).
- Event fields are complete: time, source/target provider, original/mapped model, HTTP status, business code, reason, disabled-until, outcome (`FailoverEventsView`).
- Dashboard main nav has `tab.failover` immediately after `tab.sessions`; `SessionBrowser`/`SessionDetail`/session export are untouched.
- Provider-management title has an accessible auto-failover switch with PUT-failure rollback; provider cards refresh every 15s only while the Providers tab is active.
- Classifier handles each signal per the spec table; bare 429 keeps same-provider retry (no switch); 401 recovers only after a non-empty token actually changes or a non-401 provider test; quota-snapshot recovery never clears credential state.
- New APIs are all behind `authMiddlewareFunc`; responses expose only `{enabled}` / `{events:[...]}` — no token/body/query.
- Task 6 ordering API is behind `authMiddlewareFunc`; `PUT /api/providers/order` returns redacted ordered providers without leaking tokens; SQLite `sort_order` keeps order stable after restart; reorder does not change the effective default provider unless a later auto-failover actually succeeds.

Known limitations / follow-ups:

- On a failover hit, the original failed upstream connection is closed at handler return (covered by the existing `defer resp.Body.Close()`; no leak — only connection reuse is slightly delayed).
- "Successful provider test" for credential recovery is defined as: the test request completed and the upstream returned non-401 (matches the existing test endpoint semantics).
