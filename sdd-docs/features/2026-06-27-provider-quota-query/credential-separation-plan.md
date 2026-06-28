# Provider Quota Credential Separation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split General/Custom and ZenMux quota credentials, preserve legacy configurations, apply cc-switch-compatible atomic ZenMux fallback, and enforce identical save/test/query validation.

**Architecture:** `ProviderQuotaConfig` owns separate script and ZenMux override fields plus a read-only legacy field. A single migration helper classifies legacy JSON using persisted structure and card context; a single ZenMux credential resolver is reused by validation and query planning. Admin DTO patching, frontend payloads, imports/copies, and provider-card snapshot lifecycle use the separated contract.

**Tech Stack:** Go 1.26, `net/http`, SQLite JSON configuration, Vue 3, TypeScript, Node test runner, Vite.

---

### Task 1: Split the backend data model and migrate legacy JSON

**Files:**
- Modify: `internal/providerquota/types.go:32-155`
- Modify: `internal/providerquota/types.go:312-390`
- Test: `internal/providerquota/types_test.go`
- Test: `internal/config/sqlite_store_test.go`

- [ ] **Step 1: Write failing model and migration tests**

Add tests proving separate JSON fields, public configured flags, secret redaction, and these migrations:

```go
func TestMigrateLegacyQuotaCredentials(t *testing.T) {
    general := &ProviderQuotaConfig{TemplateType: TemplateGeneral, LegacyAPIKey: "script-old"}
    MigrateLegacyCredentials(general, "https://gateway.example")
    if general.ScriptAPIKey != "script-old" || general.LegacyAPIKey != "" { t.Fatalf("general migration: %+v", general) }

    zen := &ProviderQuotaConfig{
        TemplateType: TemplateTokenPlan,
        CodingPlanProvider: "",
        BaseURL: "https://quota.zenmux.example/usage",
        LegacyAPIKey: "zen-old",
    }
    MigrateLegacyCredentials(zen, "https://gateway.example")
    if zen.ZenMuxBaseURL != "https://quota.zenmux.example/usage" || zen.ZenMuxAPIKey != "zen-old" || zen.BaseURL != "" {
        t.Fatalf("zenmux migration: %+v", zen)
    }
}
```

Extend SQLite round-trip coverage to assert `script_api_key`, `zenmux_base_url`, and `zenmux_api_key` persist while raw values never appear in `ToPublicConfig` JSON.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./internal/providerquota ./internal/config -run 'Test(MigrateLegacyQuotaCredentials|EncodeDecodeQuotaConfigRoundTrip|SQLiteStorePersistsQuotaQueryConfig)' -count=1 -v
```

Expected: compile failures for the new fields and migration helper.

- [ ] **Step 3: Implement the split fields and migration helper**

Use this model:

```go
BaseURL       string `json:"base_url,omitempty"`
ScriptAPIKey string `json:"script_api_key,omitempty"`
ZenMuxBaseURL string `json:"zenmux_base_url,omitempty"`
ZenMuxAPIKey string `json:"zenmux_api_key,omitempty"`
LegacyAPIKey string `json:"api_key,omitempty"` // decode compatibility only
```

Implement `MigrateLegacyCredentials(c, cardAPIURL)` with structural legacy ZenMux detection: Token Plan + legacy key + legacy BaseURL is a legacy ZenMux override even when the explicit provider is empty and the card URL has since changed. Move values only into empty destination fields, then clear `LegacyAPIKey`; clear `BaseURL` after moving a legacy ZenMux URL so new encodes do not reproduce the overloaded representation.

Update `HasSecrets` and `PublicQuotaConfig` to expose only:

```go
ScriptAPIKeyConfigured bool `json:"script_api_key_configured"`
ZenMuxAPIKeyConfigured bool `json:"zenmux_api_key_configured"`
ZenMuxBaseURL string `json:"zenmux_base_url,omitempty"`
```

- [ ] **Step 4: Run model tests and verify GREEN**

Run the Step 2 command. Expected: PASS.

- [ ] **Step 5: Commit the model slice**

```bash
git add internal/providerquota/types.go internal/providerquota/types_test.go internal/config/sqlite_store_test.go
git commit -m "refactor(providerquota): split script and zenmux credentials"
```

### Task 2: Centralize effective-provider validation and atomic ZenMux fallback

**Files:**
- Modify: `internal/providerquota/resolve.go`
- Modify: `internal/providerquota/manager.go:125-250`
- Modify: `internal/providerquota/token_plan.go:64-90,448-468`
- Test: `internal/providerquota/resolve_test.go`
- Test: `internal/providerquota/manager_test.go`

- [ ] **Step 1: Write failing resolver tests**

Cover all atomic combinations:

```go
func TestResolveZenMuxCredentialsAtomic(t *testing.T) {
    tests := []struct{
        name, overrideURL, overrideKey, cardURL, cardKey string
        wantURL, wantKey string
        wantErr bool
    }{
        {"override pair", "https://quota.zenmux.example/usage", "zen-key", "https://api.zenmux.example/v1", "card", "https://quota.zenmux.example/usage", "zen-key", false},
        {"card fallback pair", "", "", "https://api.zenmux.example/usage", "card", "https://api.zenmux.example/usage", "card", false},
        {"url only rejected", "https://quota.zenmux.example/usage", "", "https://api.zenmux.example/v1", "card", "", "", true},
        {"key only rejected", "", "zen-key", "https://api.zenmux.example/v1", "card", "", "", true},
    }
    // call resolveZenMuxCredentials and assert the exact pair
}
```

Add `ValidateForCard` tests for explicit/auto ZenMux, auto Volcengine, provider mismatch, MiMo deferral, and disabled configs. Add Manager integration tests proving General sends only `ScriptAPIKey` and ZenMux sends only `ZenMuxAPIKey` or the complete card fallback pair.

- [ ] **Step 2: Run resolver tests and verify RED**

```bash
go test ./internal/providerquota -run 'Test(ResolveZenMuxCredentialsAtomic|ValidateForCard|ManagerSeparatedCredentials)' -count=1 -v
```

Expected: compile failures for missing helpers/fields.

- [ ] **Step 3: Implement shared resolution and validation**

Implement:

```go
func resolveZenMuxCredentials(cfg *ProviderQuotaConfig, cardURL, cardToken string) (string, string, error)
func (c *ProviderQuotaConfig) ValidateForCard(cardURL, cardToken string) error
```

`resolveZenMuxCredentials` must reject a half-configured override and must never combine an override URL with a card token or a card URL with an override key. `ValidateForCard` calls `Validate`, resolves the effective provider with `ResolveTokenPlanProvider`, then checks ZenMux and Volcengine credentials.

Update `resolveQueryPlan`:

```go
case TemplateGeneral, TemplateCustom:
    token := cardAPIToken
    if cfg.ScriptAPIKey != "" { token = cfg.ScriptAPIKey }
case TemplateTokenPlan:
    // ZenMux uses resolveZenMuxCredentials; other providers keep their existing binding.
```

Pass the resolved ZenMux URL into `TokenPlanAdapter.Query`; change `queryZenMux` to accept that URL directly instead of rereading the overloaded config field.

- [ ] **Step 4: Run providerquota tests and verify GREEN**

```bash
go test ./internal/providerquota/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit resolver changes**

```bash
git add internal/providerquota
git commit -m "fix(providerquota): bind separated credentials to query plans"
```

### Task 3: Apply separated secret patches in PUT and test drafts

**Files:**
- Modify: `internal/admin/provider_quota_handler.go:122-365`
- Test: `internal/admin/provider_quota_handler_test.go`

- [ ] **Step 1: Write failing handler tests**

Add handler tests for:

```go
// General -> ZenMux with zenmux_api_key preserves the replacement.
// ZenMux -> General with script_api_key preserves the replacement.
// clear_script_api_key does not clear zenmux_api_key and vice versa.
// legacy api_key is routed by the new template for backward-compatible clients.
// explicit/auto ZenMux half-pairs and auto Volcengine missing AK/SK return 400 and leave storage unchanged.
```

Add `/usage/test` tests with a real Manager/rewritten HTTP transport that assert request count and exact Authorization for both General and ZenMux.

- [ ] **Step 2: Run handler tests and verify RED**

```bash
go test ./internal/admin -run 'TestProviderUsage(Separated|Legacy|EffectiveValidation|TestSeparated)' -count=1 -v
```

Expected: failures because the DTO and validation still use `api_key`.

- [ ] **Step 3: Implement the new DTO and patch order**

Add request fields:

```go
ScriptAPIKey *string `json:"script_api_key"`
ZenMuxBaseURL *string `json:"zenmux_base_url"`
ZenMuxAPIKey *string `json:"zenmux_api_key"`
ClearScriptAPIKey bool `json:"clear_script_api_key"`
ClearZenMuxAPIKey bool `json:"clear_zenmux_api_key"`
LegacyAPIKey *string `json:"api_key"`
```

In `applyQuotaUpdate`, migrate the existing config before patching. Apply the two secret patches independently. Route a non-empty legacy request key according to the new template/effective provider only when the corresponding new field is absent. Normalize without comparing previous domains because independent fields may coexist.

Change PUT and `/usage/test` to call:

```go
if err := newCfg.ValidateForCard(provider.APIURL, provider.APIToken); err != nil { /* 400 */ }
```

Do not assign `provider.QuotaQuery` before validation succeeds.

- [ ] **Step 4: Run admin tests and verify GREEN**

```bash
go test ./internal/admin/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit handler changes**

```bash
git add internal/admin/provider_quota_handler.go internal/admin/provider_quota_handler_test.go
git commit -m "fix(admin): patch separated quota credentials safely"
```

### Task 4: Migrate store/import/copy paths and invalidate fallback snapshots

**Files:**
- Modify: `internal/config/provider.go:121-195`
- Modify: `internal/config/sqlite_store.go:291-330`
- Modify: `internal/admin/provider_handler.go:238-365,748-805,981-995`
- Test: `internal/config/sqlite_store_test.go`
- Test: `internal/admin/provider_handler_test.go`

- [ ] **Step 1: Write failing lifecycle tests**

Add tests proving:

```text
SQLite load migrates legacy General and legacy auto-ZenMux JSON.
Provider duplicate copies both separated credentials without aliasing.
Provider APIURL or APIToken update deletes a quota snapshot when quota config exists.
Provider name-only update does not delete the snapshot.
```

- [ ] **Step 2: Run lifecycle tests and verify RED**

```bash
go test ./internal/config ./internal/admin -run 'Test(SQLiteStoreMigratesLegacyQuota|DuplicateProviderSeparatedQuota|ProviderUpdateInvalidatesQuotaSnapshot)' -count=1 -v
```

Expected: migration/snapshot assertions fail.

- [ ] **Step 3: Wire migration and invalidation**

After SQLite row scan and quota decode, call `MigrateLegacyCredentials(qq, p.APIURL)`. Call the same helper before provider validation on JSON/import paths and before copying quota configuration.

In `updateProvider`, capture old APIURL/APIToken, save the provider, then delete the provider snapshot when either credential changed and `QuotaQuery != nil`. Do not delete for name-only changes. Preserve the existing review-note files and unrelated provider behavior.

- [ ] **Step 4: Run lifecycle tests and verify GREEN**

Run the Step 2 command. Expected: PASS.

- [ ] **Step 5: Commit lifecycle changes**

```bash
git add internal/config internal/admin/provider_handler.go internal/admin/provider_handler_test.go
git commit -m "fix(providerquota): migrate legacy credentials and invalidate fallback snapshots"
```

### Task 5: Update frontend contracts and form behavior

**Files:**
- Modify: `internal/frontend/src/composables/useApi.ts`
- Modify: `internal/frontend/src/utils/quotaForm.ts`
- Modify: `internal/frontend/src/utils/quotaForm.test.ts`
- Modify: `internal/frontend/src/views/ProviderUsageView.vue`
- Modify: `internal/frontend/src/composables/useI18n.ts`

- [ ] **Step 1: Write failing frontend behavior tests**

Update `quotaForm.test.ts` to assert:

```ts
const form = {
  ...baseForm,
  template_type: 'token_plan',
  coding_plan_provider: 'zenmux',
  script_api_key: 'script-new',
  zenmux_base_url: 'https://quota.zenmux.example/usage',
  zenmux_api_key: 'zen-new',
}
const payload = buildTestPayload(form, '')
assert.equal(payload.zenmux_api_key, 'zen-new')
assert.equal('script_api_key' in payload, false)
```

Also assert General sends only `script_api_key`, clear flags are independent, template switches do not auto-clear the other configured field, and no payload emits legacy `api_key`.

- [ ] **Step 2: Run frontend tests and verify RED**

```bash
npm --prefix internal/frontend test
```

Expected: TypeScript compile/assertion failures for the new form contract.

- [ ] **Step 3: Implement frontend split fields**

Replace `api_key` in form state with `script_api_key` and `zenmux_api_key`; add `zenmux_base_url` and independent clear flags. Bind General/Custom inputs to Script key and ZenMux inputs to ZenMux URL/key. Load only public configured flags and URL; clear typed secrets after a successful save.

`buildSavePayload` may preserve inactive configured keys by omitting them. `buildTestPayload` must include only the current template's newly entered secret. Update bilingual labels to distinguish “脚本 API Key” and “ZenMux 用量 API Key”.

- [ ] **Step 4: Run frontend tests and build**

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

Expected: 0 failed tests and successful Vite build.

- [ ] **Step 5: Commit frontend changes including generated dist**

```bash
git add internal/frontend/src internal/frontend/dist
git commit -m "feat(frontend): separate script and zenmux quota credentials"
```

### Task 6: Full regression verification and documentation closure

**Files:**
- Modify: `sdd-docs/features/2026-06-27-provider-quota-query/spec_ZH.md`
- Modify: `sdd-docs/features/2026-06-27-provider-quota-query/spec.md`
- Preserve uncommitted: `sdd-docs/features/2026-06-27-provider-quota-query/review-notes.md`
- Preserve uncommitted: `sdd-docs/features/2026-06-27-provider-quota-query/review-notes_ZH.md`

- [ ] **Step 1: Run focused security and migration regressions**

```bash
go test ./internal/providerquota ./internal/admin ./internal/config -run 'Test(ResolveZenMuxCredentialsAtomic|ValidateForCard|ManagerSeparatedCredentials|ProviderUsageSeparated|ProviderUsageLegacy|ProviderUsageEffectiveValidation|SQLiteStoreMigratesLegacyQuota|ProviderUpdateInvalidatesQuotaSnapshot)' -count=1 -v
```

Expected: PASS with no secret values in failure output.

- [ ] **Step 2: Run the complete quality gate**

```bash
go test ./...
go test -race ./internal/providerquota ./internal/admin ./internal/config
go vet ./...
go build ./...
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 3: Verify repository hygiene**

```bash
git status --short
git diff --cached --name-only
git diff -- sdd-docs/features/2026-06-27-provider-quota-query/review-notes.md sdd-docs/features/2026-06-27-provider-quota-query/review-notes_ZH.md
```

Expected: no temporary tests or binaries; pre-existing review-note modifications remain unstaged and preserved.

- [ ] **Step 4: Update the specs with the final field contract and evidence**

Replace the legacy `APIKey` data-model description with `ScriptAPIKey`, `ZenMuxBaseURL`, and `ZenMuxAPIKey`; document atomic fallback and migration compatibility in both languages. Record actual verification commands and results without marking unrelated viewport/card acceptance complete.

- [ ] **Step 5: Commit documentation and verification metadata**

```bash
git add sdd-docs/features/2026-06-27-provider-quota-query/spec.md sdd-docs/features/2026-06-27-provider-quota-query/spec_ZH.md
git commit -m "docs(providerquota): document separated credential contract"
```
