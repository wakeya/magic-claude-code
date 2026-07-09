# Cross-Provider Model Routing Spec

Local page: Admin dashboard provider edit modal (`ProviderModal.vue`) / Proxy entry: `:443 /v1/messages` and `:443 /api/claude_cli/bootstrap` / Reference sources: `claude-code-src/src/src` (Claude Code 2.1.88 source) / Stack: Go 1.26 stdlib + Vue 3 + embedded frontend / Last updated: 2026-07-08 / Progress: 0 / 7 planned

## Overall Analysis (Source Analysis)

### Goal

Make Claude Code's `/model` menu show custom model options configured in mcc; after a user switches in one session, that session's subsequent requests use the switched model (possibly on a different provider), while other sessions keep the default (the `ActiveProviderID`).

### Three Key Facts on the Claude Code Side (why this design works)

Implementers must understand these first.

**Fact 1: `/model` switching is a per-session in-memory state, never persisted.**

`claude-code-src/src/src/bootstrap/state.ts:838-849`:

```ts
export function getMainLoopModelOverride(): ModelSetting | undefined {
  return STATE.mainLoopModelOverride
}
export function setMainLoopModelOverride(model: ModelSetting | undefined) {
  STATE.mainLoopModelOverride = model
}
```

`STATE.mainLoopModelOverride` is a process-memory variable. Selecting an item in `/model` calls `setMainLoopModelOverride(value)` — it affects only the current process and is **not** written to `~/.claude.json`. A new Claude Code session is a separate process whose override is `undefined`, falling back to the default.

Model selection priority (`claude-code-src/src/src/utils/model/model.ts:50-78`):

```
1. mainLoopModelOverride (in-session /model switch) — highest
2. --model startup flag
3. ANTHROPIC_MODEL env var
4. settings.model (persisted global config)
5. built-in default
```

→ **Conclusion: the requirement "other sessions keep the current model" is satisfied natively on the Claude Code client side. mcc needs no session identification.** Each Claude Code process maintains its own override; the `model` field of every request represents that session's current choice.

**Fact 2: Claude Code fetches extra model options from `/api/claude_cli/bootstrap` at startup.**

`claude-code-src/src/src/services/api/bootstrap.ts:114-141`:

```ts
export async function fetchBootstrapData(): Promise<void> {
  const response = await fetchBootstrapAPI()
  if (!response) return
  const additionalModelOptions = response.additional_model_options ?? []
  // ...
  saveGlobalConfig(current => ({
    ...current,
    additionalModelOptionsCache: additionalModelOptions,
  }))
}
```

Response schema (`bootstrap.ts:19-38`):

```ts
z.object({
  client_data: z.record(z.unknown()).nullish(),
  additional_model_options: z.array(
    z.object({
      model: z.string(),
      name: z.string(),
      description: z.string(),
    }).transform(({ model, name, description }) => ({
      value: model, label: name, description,
    })),
  ).nullish(),
})
```

The cache is appended to the `/model` menu (`claude-code-src/src/src/utils/model/modelOptions.ts:480-484`):

```ts
for (const opt of getGlobalConfig().additionalModelOptionsCache ?? []) {
  if (!options.some(existing => existing.value === opt.value)) {
    options.push(opt)
  }
}
```

→ **Conclusion: by controlling the `/api/claude_cli/bootstrap` response, mcc can inject arbitrary options into the `/model` menu.** The `model` field of an injected item becomes that menu item's value; after selection, `setMainLoopModelOverride(value)` stores it in memory, and every subsequent `/v1/messages` request in that session carries this value as its `model` field.

**Fact 3 (prerequisite): mcc already makes Claude Code run on a firstParty-equivalent path via hosts hijacking.**

`bootstrap.ts:48-51` gates the bootstrap request:

```ts
if (getAPIProvider() !== 'firstParty') {
  logForDebugging('[Bootstrap] Skipped: 3P provider')
  return null
}
```

In 3P-provider mode Claude Code does not issue the bootstrap request. But mcc redirects `api.anthropic.com` to the local proxy via hosts; Claude Code still considers itself firstParty and issues the request, which mcc intercepts. **Evidence: mcc already implements `handleBootstrap` (`internal/proxy/hardcoded.go:408`), proving this path works today.** This feature reuses that established pipeline.

### Claude Code Acceptance of Custom Model Strings (verified)

- `parseUserSpecifiedModel` (`model.ts:445`): maps `sonnet`/`opus`/`haiku`/`opusplan` aliases; **returns arbitrary other strings as-is**.
- `isModelAllowed` (`modelAllowlist.ts:91-106`): when `availableModels` is unset, all models are allowed; no allowlist by default.
- Menu dedup (`modelOptions.ts:481`): an injected value colliding with a built-in item is ignored.

→ **Conclusion: any non-`claude-*` custom string (e.g. `glm-4.6`, `kimi-k2`) is accepted and written into the request. Constraint: `ExposedModel.ID` must not use the `claude-*` prefix (collides with built-in items and is ignored), must not contain `[1m]` (special-cased as 1M context by `parseUserSpecifiedModel`), and must not equal Claude Code aliases `sonnet`/`opus`/`haiku`/`opusplan`.**

### mcc Baseline (change targets)

| File | Current state | Key locations |
|------|---------------|---------------|
| `internal/config/provider.go` | `Provider` has `ModelMappings map[string]string` (single-provider client→backend map); `MapModel(model)`; `Validate()` | struct 23-103, `MapModel` 239-247 |
| `internal/config/config.go` | `Config.Providers []Provider` + `ActiveProviderID`; `GetActiveProvider()` returns single active; `Validate()` | 21-60, `GetActiveProvider` 211-230 |
| `internal/config/store.go` | JSON `Store.Save` uses `json.MarshalIndent(cfg)`, `Load` uses `json.Unmarshal` — new fields supported automatically | 31-65 |
| `internal/config/sqlite_store.go` | **Not JSON pass-through**: provider fields are explicitly stored in SQLite columns/tables; `ExposedModels` must be added to schema/load/save or SQLite mode will drop the setting | `ensureProviderColumns` 145-162, `loadProviders` 290-346, `saveProviders` 386-432, `upsertProvider` 435-509 |
| `internal/proxy/handler.go` | `ServeHTTP`: `activeProvider := cfg.GetActiveProvider()` → `activeProvider.MapModel(...)` → `transformRequest(body, activeProvider)`; `transformRequest` internally does `MapModel` + `MultimodalSwitch` override | `GetActiveProvider` usage 73-91, `transformRequest` 625-695, model map 640-649 |
| `internal/proxy/hardcoded.go` | `handleBootstrap` returns a fixed empty `additional_model_options`, **does not read config** | 408-414 |
| `internal/admin/provider_handler.go` | **Manual field enumeration**: `providerResponseMap`, `createProvider.req`+ctor, `updateProvider.req`+per-field update, `handleProviderDuplicate` ctor; create/update/response/import pass through, duplicate clears `ExposedModels` | responseMap 19-47, create 85-195, update 240-381, duplicate 781-806 |
| `internal/frontend/src/components/ProviderModal.vue` | Provider edit form; `model_mappings` uses a dynamic array + add/remove (lines 134-147) | mappings state 195, collect 245-252 |
| `internal/frontend/src/composables/useApi.ts` | Provider and create/update payload TypeScript types are manually declared; add `exposed_models` there too | `Provider` interface 29-55, create/update payloads 446+ |

### Core Design

**Data model**: add `ExposedModels []ExposedModel` on `Provider`. Each `ExposedModel` declares a model exposed to the `/model` menu: `ID` (globally-unique routing key, becomes the request `model` field), `Label`, `Description`, `BackendModel` (the provider's real model name).

**Routing layer**: a new `Config.ResolveModel(model) (*Provider, string)` method unifies "pick provider" and "compute backend model". Lookup order:
1. Scan all **enabled** providers' `ExposedModels`; if `ID == model` (stripping a trailing `[1m]` for Context1M tolerance) → return that provider + `BackendModel` (Validate guarantees non-empty).
2. No hit → return `GetActiveProvider()` + `active.MapModel(model)` (backward compatible with existing `ModelMappings`).
3. No active → return `(nil, model)`.

**Model decision priority** (the final model written into the upstream request body):
1. `MultimodalSwitch` fires (provider enabled and request contains image/PDF/audio/video) → `MultimodalModel` (backend capability constraint, highest priority)
2. Routing hit → `ExposedModel.BackendModel`
3. fallback → `active.MapModel(model)`

**Bootstrap injection**: `handleBootstrap` now reads config, collects all enabled providers' `ExposedModels`, and emits `additional_model_options: [{model: ID, name: Label, description: auto-composed}, ...]`. The `description` auto-appends the provider name for attribution: `"{Description} · {Provider.Name}"` when Description is non-empty, else just `Provider.Name` — so each menu option shows its provider with zero extra config.

**Relationship with existing `ModelMappings` (independent, cooperative, non-conflicting)**:
- `ModelMappings`: handles Claude Code's **built-in** model names (`claude-opus-4-8` etc.), used on the fallback path. Key is unique within a provider; **value may repeat** (multiple `claude-*` → same backend model is legal).
- `ExposedModels`: handles **explicitly switched** models, used on the routing-hit path. `ID` is **globally unique** across all providers.
- The uniqueness constraint applies only to `ExposedModel.ID`, never to any `ModelMappings` field.

### Risk Summary

1. **`ExposedModel.ID` global uniqueness**: cross-provider duplicates cause routing ambiguity (`ResolveModel` hits the first). Must be validated in `Config.Validate()`, returning 400 on save.
2. **Exposed models of disabled providers**: both `ResolveModel` and `handleBootstrap` collection consider only `Enabled == true` providers, avoiding routing to a disabled provider.
3. **`transformRequest` refactor risk**: the current `transformRequest` internally calls `MapModel`; after refactor, model resolution moves up to `ResolveModel`, and `transformRequest` receives the resolved `backendModel`. Multimodal override semantics and OpenAI-format conversion paths must remain unchanged.
4. **Bootstrap config read failure**: fall back to an empty `additional_model_options` (same as today), must not block Claude Code startup.
5. **Menu item name collision with built-ins**: an `ExposedModel.ID` using `claude-*` is deduped away. The naming constraint is enforced in validation and docs.
6. **SQLite persistence**: JSON `Store` is pass-through, but `SQLiteStore` is not. Missing SQLite support would silently drop `ExposedModels`.
7. **Duplicate provider conflict**: duplicating `ExposedModels` would immediately violate global ID uniqueness. Duplicate provider should clear `ExposedModels`.

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | config layer: `ExposedModel` type + `Provider.ExposedModels` + validation + `ResolveModel` | `internal/config/provider.go`, `internal/config/config.go` | `go test ./internal/config/...` |
| 2 | Planned | SQLite persistence for `ExposedModels` | `internal/config/sqlite_store.go` | SQLite round-trip unit test |
| 3 | Planned | proxy routing: `handler.go` uses `ResolveModel`; `transformRequest` signature change | `internal/proxy/handler.go` | `go test ./internal/proxy/...` |
| 4 | Planned | bootstrap injection: `handleBootstrap` reads config, emits `additional_model_options` | `internal/proxy/hardcoded.go` | bootstrap unit test |
| 5 | Planned | admin API passes through `ExposedModels`; duplicate clears them | `internal/admin/provider_handler.go` | `go test ./internal/admin/...` |
| 6 | Planned | frontend provider form "Exposed Models" editor + i18n + API types | `ProviderModal.vue`, `useI18n.ts`, `useApi.ts` | `npm run build` |
| 7 | Planned | end-to-end verification + regression | verification record | manual full chain + `make test` |

## Requirements

### Deliverables

1. `ExposedModel` type and `Provider.ExposedModels` field, JSON tag `exposed_models,omitempty`.
2. `Config.Validate()` cross-provider uniqueness check on all `ExposedModel.ID` (case-sensitive, trimmed); descriptive error on duplicate.
3. `Provider.Validate()` validates each `ExposedModel`: empty `ID` is auto-generated as `em-<hex>` stable random ID (frontend no longer asks users to type it); `Label` non-empty (after trim); `ID` (auto-generated or legacy) must not start with `claude-`, must not contain `[1m]`, must not equal aliases `sonnet`/`opus`/`haiku`/`opusplan`, and only allows `[A-Za-z0-9._:-]+`; `BackendModel` is **required** (validated non-empty, no longer "falls back to ID"). New `Context1M bool` field.
4. `Config.ResolveModel(model string) (*Provider, string)` per "Core Design". Exposed-model matching strips a trailing `[1m]` from the request model (Context1M tolerance).
5. `handler.go` `ServeHTTP` uses `ResolveModel` in place of `GetActiveProvider` + `MapModel`; `transformRequest` signature becomes `(body, provider, backendModel)`, drops its internal `MapModel`, keeps `MultimodalSwitch` override and format conversion.
6. `handleBootstrap` reads config, collects enabled providers' `ExposedModels` into `additional_model_options`; when `Context1M=true` the menu value is suffixed with `[1m]` so Claude Code treats it as 1M context; description auto-appends provider name; falls back to empty on read failure.
7. `SQLiteStore` explicitly persists `ExposedModels`; JSON `Store` needs no extra work. One-time startup migration regenerates non-`em-` legacy IDs to `em-<hex>`.
8. admin API create/update/response/import pass through `ExposedModels`; duplicate does **not** copy them; export serializes `config.Provider` directly.
9. frontend `ProviderModal.vue` adds an "Exposed Models" editor (Label/Description/BackendModel + 1M checkbox; **ID input hidden** — new rows have empty ID → backend auto-generates `em-<hex>`, editing keeps existing IDs); BackendModel input offers a datalist for quick fill from that provider's model-mapping values (not enforced); `useApi.ts`/`useI18n.ts` updated.
10. unit tests cover: `ResolveModel` all branches (incl. `[1m]` tolerance), cross-provider ID uniqueness, SQLite round-trip, `handleBootstrap` collection and schema field names, handler routing integration, admin create/update/import/duplicate behavior, context-1m beta stripping, legacy ID migration.
11. `Context1M` + beta stripping: `copyUpstreamHeaders` strips `context-1m-*` entries from `Anthropic-Beta` in anthropic format (mcc targets third-party providers that don't recognize this beta; other betas like interleaved-thinking are kept). Design trade-off: a provider-level toggle would be needed to support official Anthropic or backends that truly require the beta.

### Constraints

- Backward compatible: providers without `ExposedModels` behave exactly as today (`ResolveModel` falls back).
- JSON `Store` is pass-through; `SQLiteStore` is not and must explicitly persist `ExposedModels`.
- `ExposedModel.ID` global uniqueness is a hard correctness constraint.
- Claude Code source is not modified; the feature is implemented entirely in mcc.

### Edge Cases

- A request `model` hits an `ExposedModel.ID` of a **disabled** provider: skip that provider, keep searching other enabled providers for the same ID; none → fallback.
- `ExposedModel.BackendModel` is required (Validate rejects empty); an empty value fails the save with a descriptive error.
- Two `ExposedModel`s in the same provider pointing to the same `BackendModel`: no error (meaningless but legal).
- `handleBootstrap` collecting the same `ID` from multiple providers: dedup, keep first (config validation forbids this; defensive).
- Empty `additional_model_options` leaves the Claude Code `/model` menu unchanged.
- Duplicating a provider clears `ExposedModels`; the user must create new globally unique IDs manually.

## Task Details

### Task 1: config layer — type, validation, routing

#### Requirements

**Objective** — Define `ExposedModel`, add the field to `Provider`, add cross-provider uniqueness validation, implement `ResolveModel`.

**Outcomes** — Changes to `internal/config/provider.go` and `internal/config/config.go`; new tests pass.

**Evidence** — `ResolveModel` unit tests cover hit / fallback / disabled-skip / `[1m]` tolerance / no-active-nil; `Config.Validate` covers duplicate ID error (asserts message contains `duplicated`).

**Constraints** — Preserve existing `MapModel`, `GetActiveProvider`, `Validate` semantics; new field uses `omitempty`.

**Edge Cases** — Whitespace around `ID`; disabled provider's exposed model; empty `BackendModel`; no active provider.

**Verification** — `go test -v -race ./internal/config/...` green.

#### Plan

See `spec_ZH.md` Task 1 for exact code (struct fields, `Validate` additions, `ResolveModel` implementation, and the full test list). The two files are semantically identical; apply the same code blocks.

Key additions:
- `provider.go`: `ExposedModels []ExposedModel` field on `Provider`; `ExposedModel` type; per-item validation inside `Provider.Validate()` (reject empty ID/Label, `claude-` prefix, `[1m]`, Claude Code aliases `sonnet`/`opus`/`haiku`/`opusplan`, characters outside `[A-Za-z0-9._:-]`, intra-provider duplicate); trim all fields (by index) before validating/persisting; import `"strings"`.
- `config.go`: cross-provider `ID` uniqueness inside `Validate()`; `ResolveModel(model string) (*Provider, string)` method after `GetProviderByID`.

#### Verification

```bash
go test -v -race ./internal/config/...
```

Expected: all pass, including 9 new tests.

---

### Task 2: SQLite persistence — sqlite_store.go

#### Requirements

**Objective** — Persist `Provider.ExposedModels` in SQLite mode.

**Outcomes** — `internal/config/sqlite_store.go` changes; SQLite Save→Load preserves `ExposedModels`.

**Evidence** — Unit tests cover new database creation, old database column migration, SQLite round-trip, and legacy JSON migration preservation.

**Constraints** — JSON `Store` needs no change; SQLite stores `exposed_models` as a JSON text column on `providers` with default `[]`; malformed JSON returns a clear load error.

**Edge Cases** — nil/empty slice persists as `[]`; old SQLite databases without the column get it through `ensureProviderColumns`; load of empty value yields nil/empty slice.

**Verification** — `go test -v -race ./internal/config/...`.

#### Plan

See `spec_ZH.md` Task 2 for exact code. Summary:
- Add `exposed_models TEXT NOT NULL DEFAULT '[]'` in `ensureProviderColumns`.
- Add `exposed_models` to `loadProviders` SELECT/Scan and decode JSON into `p.ExposedModels`.
- Add encode/decode helpers for `[]ExposedModel`.
- Add `exposed_models` to `upsertProvider` INSERT/UPDATE/args.
- Add SQLite round-trip and old-schema migration tests.

---

### Task 3: proxy routing — handler.go

#### Requirements

**Objective** — `ServeHTTP` uses `ResolveModel` to pick provider + backend model; `transformRequest` receives the resolved `backendModel` and no longer calls `MapModel` itself.

**Outcomes** — `internal/proxy/handler.go` changes; no regression in existing proxy tests.

**Evidence** — Existing `server_test.go` green; new integration test "request with an ExposedModel.ID routes to the right provider" passes.

**Constraints** — `MultimodalSwitch` override semantics unchanged; OpenAI-format conversion path unchanged; usage-record `mappedModel` semantics unchanged.

**Edge Cases** — `ResolveModel` returns nil provider (existing "No active provider" path); the hit provider's URL/Token are used for forwarding.

**Verification** — `go test -race ./internal/proxy/...`.

#### Plan

See `spec_ZH.md` Task 3 for before/after code. Summary of changes:
- `ServeHTTP`: read body first, then `metadata := ParseRequestMetadata(body)`, then `selectedProvider, backendModel := cfg.ResolveModel(metadata.OriginalModel)`; derive `backendURL`/`apiToken` from `selectedProvider`; call `transformRequest(body, selectedProvider, backendModel)`. Replace all subsequent `activeProvider` references in `ServeHTTP` with `selectedProvider`.
- `transformRequest`: signature `(body, provider, backendModel)`; nil-provider guard (`if provider == nil { return body, nil }`); replace the `provider.MapModel(model)` line with `finalModel := backendModel`, then apply `MultimodalSwitch` override on top. Update all existing direct `transformRequest` tests to pass the new third argument.

#### Verification

```bash
go build ./...
go test -race ./internal/proxy/...
```

Expected: existing tests green (semantically equivalent) + new routing test passes.

---

### Task 4: bootstrap injection — hardcoded.go

#### Requirements

**Objective** — `handleBootstrap` reads config and emits enabled providers' `ExposedModels` as `additional_model_options`.

**Outcomes** — `internal/proxy/hardcoded.go` changes; bootstrap unit test verifies field names and collection.

**Evidence** — Test asserts `additional_model_options` contents, field names `model`/`name`/`description`, cross-provider dedup, disabled-provider exclusion.

**Constraints** — Read failure falls back to empty array; stable order (provider order + `ExposedModels` order); no sensitive data leakage.

**Edge Cases** — nil config; Load error; two providers exposing the same ID (defensive dedup).

**Verification** — `go test -run TestHandleBootstrap ./internal/proxy/...`.

#### Plan

See `spec_ZH.md` Task 4 for the `handleBootstrap` + `collectAdditionalModelOptions` implementation and tests. Add `"strings"` to imports. The emitted `description` auto-appends the provider name for attribution: `"{Description} · {Provider.Name}"` when Description is non-empty, else just `Provider.Name` — so each `/model` menu option shows its provider with zero extra config. Emit trimmed `model`/`name` values. Tests must cover both the append case and the empty-Description case.

#### Verification

```bash
go test -v -run TestHandleBootstrap -race ./internal/proxy/...
```

Expected: both tests pass.

---

### Task 5: admin API passthrough

#### Requirements

**Objective** — Pass `ExposedModels` through manual-enumeration sites in `provider_handler.go`; clear them when duplicating a provider.

**Outcomes** — `internal/admin/provider_handler.go` changes; admin tests cover create/update with `ExposedModels`.

**Evidence** — POST create with `exposed_models` → response includes it; PUT update → GET reflects it; duplicate-ID across providers → 400; duplicate provider response has empty `exposed_models`.

**Constraints** — update uses `*[]config.ExposedModel` for optional update (nil = no change, non-nil including empty slice = replace); validation reuses `cfg.Validate()` for cross-provider uniqueness; duplicate clears `ExposedModels`.

**Edge Cases** — `null` vs `[]` on update (null = unchanged, [] = cleared).

**Verification** — `go test -race ./internal/admin/...`.

#### Plan

See `spec_ZH.md` Task 5 for the edits (responseMap, create req+ctor, update req+per-field, duplicate clear behavior) and the `cfg.Validate()` upgrade at save time (applies to create, update, import, and duplicate).

#### Verification

```bash
go test -race ./internal/admin/...
```

Expected: green, including new tests.

---

### Task 6: frontend provider form

#### Requirements

**Objective** — `ProviderModal.vue` adds an "Exposed Models" editor reusing `model_mappings`'s dynamic-array interaction.

**Outcomes** — `ProviderModal.vue`, `useI18n.ts`, and `useApi.ts` changes; `npm run build` passes.

**Evidence** — Build succeeds; form can add/remove `ExposedModel` rows and submit on save.

**Constraints** — Three columns + 1M checkbox: Label / Description / BackendModel + 1M; **ID input hidden** (new rows have empty ID → backend auto-generates `em-<hex>`, editing keeps existing IDs); `BackendModel` is required and offers a datalist quick-fill from that provider's model-mapping values (not enforced); empty rows not submitted; partially filled rows are rejected before submit; mobile layout must not overlap; bilingual i18n; API TypeScript types are updated.

**Edge Cases** — Partial fill on save (backend rejects; frontend should pre-validate); backfill when editing an existing provider.

**Verification** — `npm --prefix internal/frontend run build`; manual check.

#### Plan

See `spec_ZH.md` Task 6 for template, state, collection code, `useApi.ts` types, and i18n entries.

#### Verification

```bash
npm --prefix internal/frontend run build
```

Expected: build success, `internal/frontend/dist` updated.

---

### Task 7: end-to-end verification + regression

#### Requirements

**Objective** — Full-chain verification: configure ExposedModel → restart/refresh Claude Code → `/model` shows the option → switched session routes correctly → other sessions unaffected.

**Outcomes** — Verification record filled into this file's Verification section; `make test` green.

**Evidence** — Manual verification screenshots/logs; `go test ./...` output.

**Constraints** — No regression for existing "single active provider + ModelMappings" users.

**Edge Cases** — Switching back to "Default" clears override → active provider; hit provider unreachable → existing 502 path.

**Verification** — see below.

#### Plan

1. Full tests: `make test`.
2. Frontend build: `npm --prefix internal/frontend run build`.
3. Manual full chain (needs a real mcc instance + Claude Code):
   - Add ExposedModel `{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"}` to provider A; save.
   - Add `{ID: "kimi-k2", Label: "Kimi K2", BackendModel: "kimi-k2"}` to provider B; save.
   - Start/restart mcc, open a new Claude Code session, `/model` should list GLM-4.6 and Kimi K2.
   - Session 1 switches to GLM-4.6 → requests carry `model: glm-4.6`, mcc logs show routing to provider A, backend model `glm-4.6`.
   - Open session 2 (no switch) → requests go to the active provider (default unchanged).
   - Session 1 switches back to Default → active provider.
4. Regression: remove all ExposedModels config → behavior identical to pre-release (no custom items in `/model`).

#### Verification

```bash
make test
npm --prefix internal/frontend run build
```

Backfill actual output summary and manual conclusions after implementation.

---

## Status

`draft` → `approved` → `planned` after user confirmation.
