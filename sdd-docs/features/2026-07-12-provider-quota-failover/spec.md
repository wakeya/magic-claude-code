# Provider Quota Failover Spec

Local page: Provider Management and Dashboard main navigation (`DashboardView.vue`)<br>
Proxy entry: `internal/proxy/handler.go` (`POST /v1/messages`, `POST /anthropic/v1/messages`)<br>
Reference sources: `~/.claude/projects/` (84 JSONL files; 53 API failures), current quota/retry code<br>
Stack: Go 1.26, SQLite, Vue 3<br>
Last updated: 2026-07-12<br>
Progress: 0 / 5 planned

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
| 1 | Planned | Config routing and atomic updates | setting, route marker, atomic mutation | `go test ./internal/config/...` |
| 2 | Planned | Failover domain | classifier, SQLite state/events, recovery | `go test ./internal/failover/...` |
| 3 | Planned | Proxy integration | replay, final response, active update | `go test ./internal/proxy/...` |
| 4 | Planned | Quota/admin integration | reconciliation, Token recovery, APIs | `go test ./internal/admin/... ./internal/providerquota/...` |
| 5 | Planned | Frontend | switch and global event panel | frontend tests/build |

## Requirements

1. Add `Config.AutoFailoverEnabled bool \`json:"auto_failover_enabled"\``, default false. JSON persists it directly; SQLite settings key is `auto_failover_enabled` with `0`/`1`.
2. Add `ModelRoute{Provider *Provider; BackendModel string; DefaultRouted bool}` and `ResolveRoute`; preserve `ResolveModel` as a wrapper. Exposed hit is never default-routed.
3. Add `internal/failover` state/events. Events are redacted, newest-first, capped at 1,000 rows and 30 days; no dangling IDs after provider deletion.
4. Classifier reads at most 64 KiB, restores a non-eligible response body byte-for-byte, recognizes the source-analysis table, and never treats a bare status as enough. `1308` is `five_hour_quota_exhausted`; `1310` is `weekly_quota_exhausted`. Parse RFC3339 and `2006-01-02 15:04:05`; a valid future reset overrides cooldown.
5. Candidates are enabled, non-quarantined providers in existing config order: first `candidate.MapModel(originalModel) == failedMappedModel`, then remaining candidates. Each retry rebuilds transformed body, URL, token, headers and format from immutable client input.
6. The first `<400` retry atomically changes `ActiveProviderID`, emits `switched`, and is the sole client response. Existing same-provider 429 retry runs first. Never replay exposed-model requests, unsafe methods, or a response that has started.
7. A fresh quota snapshot can quarantine at 100% and recover quota state when capacity returns. It MUST NOT recover a credential-invalid state. Credential state clears only after a stored non-empty API Token changes or `POST /api/providers/{id}/test` succeeds; editing name/models/URL or a failed test does not clear it.
8. Add authenticated `GET/PUT /api/providers/failover` and `GET /api/failover/events?limit=1..100`; UI adds an accessible title switch and a DashboardView top-level Failover Events tab adjacent to Session Records without modifying `SessionBrowser`, `SessionDetail`, transcript rendering, or export.

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

- [ ] Write failing `TestResolveRoute*` cases for exposed hit, fallback, disabled skip, and nil provider.
- [ ] Run `go test ./internal/config -run TestResolveRoute -count=1`; expected: FAIL (`ResolveRoute` missing).
- [ ] Add `ModelRoute`, `ResolveRoute`, and compatibility wrapper `ResolveModel` returning its provider/model.
- [ ] Run the same command; expected: PASS.
- [ ] Write failing persistence/concurrency tests for `AutoFailoverEnabled` and atomic active-ID plus toggle changes.
- [ ] Run `go test ./internal/config -run 'TestAutoFailover|TestAtomicConfig' -count=1`; expected: FAIL.
- [ ] Add the config field and store update function: lock, load newest config, apply `func(*Config) error`, validate, save, return committed copy; persist SQLite `0`/`1`; move every config writer to it.
- [ ] Run `go test -v -race ./internal/config/...`; expected: PASS.
- [ ] Commit: `git add internal/config && git commit -m "feat(config): add atomic failover settings"`.

#### Verification

```bash
go test -v -race ./internal/config/...
```

Acceptance assertions:

- [ ] `TestResolveRouteExposedModelIsNotDefaultRouted` returns the exposed provider/backend model with `DefaultRouted=false`.
- [ ] `TestResolveRouteActiveFallbackIsDefaultRouted` returns the active provider `MapModel` value with `DefaultRouted=true`.
- [ ] `TestResolveRouteSkipsDisabledExposedModel` and `TestResolveRouteWithoutActiveProvider` preserve fallback/nil behavior.
- [ ] `TestAutoFailoverEnabledJSONRoundTrip` and `TestAutoFailoverEnabledSQLiteRoundTrip` reload true; an old database without the setting reloads false.
- [ ] `TestAtomicConfigUpdatePreservesConcurrentActiveProviderAndFailoverSetting` repeatedly retains both changes under `-race`.

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

- [ ] Write failing `TestClassify` table rows for 1308, 1310, quota text, healthy deployment, 401, 403, 502/529/ECONNRESET, and all non-switch examples.
- [ ] Run `go test ./internal/failover -run TestClassify -count=1`; expected: FAIL (package missing).
- [ ] Implement `Classification{Eligible, Reason, UpstreamCode, DisabledUntil}` and `captureAndRestore`: read `io.LimitReader(resp.Body, 64*1024+1)`, parse only `error.code`, `error.message`, `code`, `message`, string `error`; restore exact bytes when not eligible.
- [ ] Use 15m quota fallback, 1m deployment/502/529/reset, 5m Cloudflare, and credential state with no time-based recovery.
- [ ] Write failing store tests for `provider_failover_state`, `provider_failover_events`, 30-day/1,000 retention, no secret fields, expiry, Token-change/test recovery, unchanged-token non-recovery, and snapshot recovery.
- [ ] Implement CRUD/list/pruning, `ClearCredentialFailure(providerID, tokenChanged, testSucceeded)`, and manager selection under one mutex (same model pass then fallback pass).
- [ ] Run `go test -v -race ./internal/failover/...`; expected: PASS.
- [ ] Commit: `git add internal/failover internal/config/sqlite_store.go && git commit -m "feat(failover): classify and quarantine providers"`.

#### Verification

```bash
go test -v -race ./internal/failover/...
```

Acceptance assertions:

- [ ] `TestClassify1308WithReset` and `TestClassify1310WithReset` assert code, reason, and future reset; invalid/past/>7-day reset falls back to 15m.
- [ ] `TestClassifyHealthyDeployment400`, `TestClassifyInvalidAPIKey401`, `TestClassifyCloudflare403`, and `TestClassifyAvailabilityFailures` produce only the specified cooldown/credential state.
- [ ] `TestBare429DoesNotFailover`, `TestClassify1210DoesNotFailover`, `TestModelNotFoundDoesNotFailover`, `TestContextLimitDoesNotFailover`, and tool compatibility cases assert `Eligible=false`.
- [ ] `TestClassifierRestoresNonEligibleBody` compares exact bytes; `TestOversizedBodyDoesNotFailover` rejects bodies over 64 KiB.
- [ ] `TestFailoverEventRetention` proves 30-day/1,000-row pruning; `TestFailoverEventRedactsSecrets` excludes token, request body, and query.
- [ ] `TestCredentialFailureRequiresTokenChangeOrSuccessfulTest` rejects name/mapping-only edit and failed test; accepts Token change and successful test.
- [ ] `TestConcurrentFailoverSelectionHasSingleWinner` asserts one active update and one `switched` event under `-race`.

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

- [ ] Write failing `TestFailover*` httptest cases from Evidence.
- [ ] Run `go test ./internal/proxy -run TestFailover -count=1`; expected: FAIL.
- [ ] Add `Handler.SetFailoverManager(*failover.Manager)` and a helper accepting `(originalBody, provider, backendModel)` that creates fresh transformed body, URL, token, headers, and API format.
- [ ] Run same-provider retry first; classify its final response only; apply candidate queue/retry and close discarded responses.
- [ ] Persist active provider only after `<400`, emit `switched`/`retry_failed`/`exhausted`, and record usage once for the final response.
- [ ] Run `go test -v -race ./internal/proxy/...`; expected: PASS.
- [ ] Commit: `git add internal/proxy cmd/server/main.go && git commit -m "feat(proxy): fail over default providers"`.

#### Verification

```bash
go test -v -race ./internal/proxy/...
```

Acceptance assertions:

- [ ] `TestFailoverDisabledPasses1308Through` returns original 429, leaves active ID unchanged, and emits no switch event.
- [ ] `TestFailoverSwitchesSameMappedModelFirst` retries the first same-mapped-model provider, returns 2xx, and persists its active ID/event.
- [ ] `TestFailoverFallsBackInProviderOrder` skips failed, disabled, and quarantined providers and follows config order.
- [ ] `TestFailoverNeverChangesExposedModelRoute` proves an `ExposedModel.ID` request never retries or changes active provider.
- [ ] `TestFailoverDoesNotSwitchRequestOrModelErrors` covers bare 429, 1210, 404, and tool errors; `TestFailoverSwitchesAvailabilityFailure` covers 502.
- [ ] `TestFailoverRebuildsOpenAIAndAnthropicRequests` asserts candidate URL, auth, model mapping, and conversion are candidate-owned.
- [ ] `TestFailoverRecordsOnlyFinalUsage` has one final-provider usage row; `TestFailoverAllCandidatesExhausted` does not double-write headers/body.

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

- [ ] Write failing API/auth tests for `GET/PUT /api/providers/failover` and event list, including malformed body and limits.
- [ ] Run `go test ./internal/admin -run TestFailover -count=1`; expected: FAIL.
- [ ] Register routes and return only `{"enabled":bool}` / `{"events":[...]}` via Task 1 atomic update.
- [ ] Write failing token-update and successful/failed provider-test recovery tests.
- [ ] Compare old/new stored Token before update; clear credential state only when non-empty Token changed. Clear it after successful provider test only; failed test leaves state. Reconcile fresh snapshots after persistence and every 30s, clearing quota state only.
- [ ] Run `go test -v -race ./internal/admin/... ./internal/providerquota/...`; expected: PASS.
- [ ] Commit: `git add internal/admin internal/providerquota && git commit -m "feat(admin): expose failover controls and recovery"`.

#### Verification

```bash
go test -v -race ./internal/admin/... ./internal/providerquota/...
```

Acceptance assertions:

- [ ] `TestFailoverSettingsRequireAuth` returns 401; `TestFailoverSettingsMethods` returns 405 for bad methods and 400 for bad JSON/unknown fields.
- [ ] `TestFailoverSettingsRoundTrip` persists true across reload; `TestFailoverEventsLimitAndOrder` proves default 50, clamp 1..100, and `occurred_at DESC,id DESC` order.
- [ ] `TestFailoverEventsDoNotExposeSecrets` excludes token, response body, and query URL.
- [ ] `TestProviderTokenChangeClearsCredentialFailure` accepts only a non-empty changed Token; `TestProviderEditWithoutTokenChangeKeepsCredentialFailure` rejects name/URL/model-only edits.
- [ ] `TestSuccessfulProviderTestClearsCredentialFailure` emits `recovered`; failed test remains quarantined.
- [ ] `TestQuotaSnapshotRecoveryDoesNotClearCredentialFailure` and `TestQuotaSnapshotExhaustionQuarantinesUntilReset` distinguish quota and credential state.
- [ ] `TestProviderDeleteLeavesNoDanglingFailoverEventIDs` proves returned events have no deleted provider IDs.

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

- [ ] Write failing tests for `getFailoverSettings`, `setFailoverSettings`, `getFailoverEvents`, title switch, `tab.failover` immediately after `tab.sessions` in main navigation, independent event page, global transcript disclaimer, and source/target/model/reason/outcome fields.
- [ ] Run `npm --prefix internal/frontend test -- --run`; expected: FAIL.
- [ ] Add typed `FailoverEvent`/settings APIs, save-disabled optimistic switch with failure rollback, and 15s active-tab provider refresh.
- [ ] Extend DashboardView `MainTab` with `'failover'`, put `{ key: 'failover', labelKey: 'tab.failover' }` immediately after `sessions`, keep the existing `activeTab === 'sessions'` branch and all `SessionBrowser` props/children exactly as they are today, and create `FailoverEventsView.vue` rendered only for `activeTab === 'failover'`; fetch on entering/refreshing it; never pass events to SessionBrowser/SessionDetail/export.
- [ ] Add semantically matching Chinese/English i18n keys.
- [ ] Run `npm --prefix internal/frontend test && npm --prefix internal/frontend run build`; expected: PASS.
- [ ] Commit: `git add internal/frontend && git commit -m "feat(ui): show provider failover events"`.

#### Verification

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

Acceptance assertions:

- [ ] `useApi` tests assert GET settings, PUT JSON settings, and safely encoded event limit.
- [ ] `DashboardFailoverSwitch` asserts a labelled adjacent switch, disabled save state, PUT failure rollback, and active-Providers-tab-only 15-second refresh.
- [ ] `DashboardFailoverTab` asserts `tab.failover` immediately follows `tab.sessions`; `FailoverEventsView` asserts global disclaimer plus source→target, model, reason, status/code, outcome, and disabled-until; returning to Session Records does not mutate SessionBrowser state, DOM structure, selected session, filters, export behavior, or transcript rendering.
- [ ] Tests prove events are not passed as `SessionDetail.messages` and export remains `/api/sessions/{id}/export` only.
- [ ] Every new i18n key exists in both locales; API failure preserves existing error state and JSONL content.

Expected: both commands exit 0 and generate `internal/frontend/dist` without TypeScript/Vite errors.
