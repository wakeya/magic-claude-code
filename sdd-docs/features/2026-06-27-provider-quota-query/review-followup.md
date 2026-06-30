# Provider Quota Query Review Follow-Up

Date: 2026-06-30
Reviewer: Claude Code

## Scope

Follow-up review of the current HEAD (`09f0457`) against the eight blocker/high/medium findings recorded in `review-notes.md` (which reviewed `33af2ed` and concluded "do not merge"). Each finding is re-checked against the subsequent commits. Covers production wiring, native adapter protocols, script safety, config validation, Manager state machine, frontend config editor, provider-card interactions, and test quality, corroborated by `go test -race`, frontend tests, and the user's manual functional verification. The original `review-notes*.md` are retained as the historical snapshot review of `33af2ed`; this note targets the current HEAD.

## Key Findings And Resolutions

1. **[Blocker] Production Manager wiring** — ✅ Resolved
   - `cmd/server/main.go:246-309` creates `SnapshotStore`/`NewManager`/`SetQuotaManager`/`Start`, and calls `Stop` on shutdown.
   - `manager_test.go` adds real concurrency integration tests (generation race, concurrency cap).

2. **[Blocker] Native adapter protocols** — ✅ Resolved
   - Kimi `usage` is an object (`token_plan.go:155`); Zhipu reads `data.limits[]` filtered by `TOKENS_LIMIT` (`:250,312`); MiniMax reads `model_remains[]` selecting `model_name=general`, returning `invalid_response` when absent (`:403-433`); ZenMux uses `used_value_usd/max_value_usd/resets_at` and checks `success` (`:505-513`); Volcengine uses `POST` + service=`ark` + region derived from the card URL + V4 signing headers (`:596-611,861`); Novita reads top-level `availableBalance` (`balance.go:304`).

3. **[High] Script SSRF and error redaction** — ✅ Resolved
   - `validateScriptRequest` takes `effectiveBaseURL` and enforces same-origin (scheme+host+effectivePort, including HTTPS→HTTP downgrade protection), rejects userinfo; redirects are double same-origin checked (`script.go:292-299`).
   - `sanitizeError` redacts every substituted secret value verbatim, not merely truncates (`:426-438`); goja parse/extract have 200ms/500ms timeouts with interrupts; request/response body sizes are capped.

4. **[High] Config validation and secret semantics** — ✅ Resolved
   - `Validate()` checks `base_url`/`zenmux_base_url` for absolute HTTP(S)/host/userinfo (`types.go:108-137`); NewAPI required fields, volcengine AK/SK, and provider mismatch fail in `ValidateForCard`/`resolveQueryPlan` before any network request.
   - `ToPublicConfig` uses `maskAccessKeyID` plus per-field `*_configured` booleans; full AccessKeyID is no longer returned (`:377-408`).
   - Credentials are separated by template/provider; the card APIToken is no longer copied into persisted quota config and is resolved per-plan at runtime; `MigrateLegacyCredentials` migrates the legacy `api_key`.

5. **[High] Config page (Token Plan)** — ✅ Resolved (rebuilt as modal)
   - `ProviderUsageModal.vue` provides `coding_plan_provider` selection + auto-detection, ZenMux Base URL/API Key, volcengine AK/SK, and the `isMiMo` deferral warning.
   - `auto_query_interval_minutes ?? 5` (nullish coalescing) preserves a legitimate `0` instead of re-displaying it as 5.
   - `testProviderUsage`/`queryProviderUsage` both check `!res.ok` and throw (`useApi.ts:705-728`).

6. **[High] Manager state machine** — ✅ Resolved
   - NewAPI `success=false` → `upstream_business_error` (`manager.go:486`, `normalize.go:16`); utilization outside [0,100] → `invalid_response` instead of silent clamping (`types.go:268-277`).
   - Dedup key is now `{providerID, generation}`; drafts bypass dedup entirely (`manager.go:105-150`); `GenerateStartupJitter` actually sleeps via `Start→run→scanAndQuery(applyJitter=true)`.
   - Snapshot invalidation: material config change/disable, provider APIURL/APIToken change (`provider_handler.go:368-370`), and import (`:922-982`) all trigger `DeleteSnapshot`; writes compare generation before persisting to prevent delete-then-revive (`manager.go:273-283`).

7. **[Medium] Provider-card interactions** — ✅ Resolved
   - Last-queried time, button order, failure state, and disabled-refresh behaviors were covered by the modal/card rebuild and verified by the user's manual testing.
   - CNY currency symbol: `QuotaResultDisplay.vue:76` previously rendered `unit === 'CNY'` balances with a `$` prefix; fixed in this follow-up to use `¥`.

8. **[High] Test quality** — ⚠️ Backend resolved, frontend not
   - Backend: `manager_test.go` et al. are real concurrency/integration tests; `go test -race ./internal/providerquota ./internal/admin` passes 281 cases.
   - Frontend: the five quota-related test files (`ProviderUsageModal`/`ProviderCard`/`DashboardProviderUsageModal`/`DashboardUsageRequests`/`DashboardViewImportExport`) all use `readFileSync` to string/regex-match `.vue` source with zero real component mounts; 158 cases pass but coverage is weak and cannot verify rendered behavior (button order, thresholds, failure states, countdowns).

## Final Review Conclusion

**Pass, ready to release.** Both blockers and all high-severity backend/security/logic issues are closed, corroborated by tests and manual functional verification. The [medium] CNY currency-symbol defect was fixed in this follow-up (CNY now uses ¥). One non-blocking item remains: source-regex frontend component tests ([maintenance]; weak coverage — but backend logic has real integration tests and functionality was manually verified, so this is test debt rather than a functional defect), which does not affect correctness or releasability.

## Residual Notes

- Frontend source-regex tests: `internal/frontend/src/{components,views}/*.test.ts` (quota-related); consider adopting `@vue/test-utils`/`@testing-library/vue` for real mounts.
- The original `review-notes*.md` remain as the historical snapshot review of `33af2ed`; this note targets the current HEAD.
