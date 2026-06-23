# Provider Import/Export Spec

Local page: Admin dashboard → Providers tab
Proxy entry: `internal/admin/provider_handler.go`, `internal/admin/server.go`, `internal/frontend/src/views/DashboardView.vue`, `internal/frontend/src/components/ProviderCard.vue`, `internal/frontend/src/composables/useApi.ts`, `internal/frontend/src/composables/useI18n.ts`
Reference sources: `sdd-docs/features/2026-06-13-auto-update/spec.md` (spec template and status/config exposure pattern), `internal/admin/provider_handler.go` (existing provider CRUD pattern), `internal/config/provider.go` (Provider struct)
Stack: Go 1.26 stdlib (`net/http`, `encoding/json`) + Vue 3 + embedded frontend
Last updated: 2026-06-23
Progress: 5 / 5 completed

## Overall Analysis (Source Analysis)

### Current Project State

The admin dashboard's Providers tab lets users create, edit, test, activate, disable, duplicate, and delete provider configurations. Each provider stores ~20 fields: API URL, **API Token (plaintext)**, API format, model mappings, rate-limit queue settings, retry settings, multimodal switch, content-block stripping, etc.

Users with multiple machines must currently recreate every provider by hand on each host. This feature adds JSON-based import/export so a provider set can be migrated or synced across machines in seconds.

### Why Export Must Include Real Tokens

The existing `GET /api/providers` endpoint masks tokens (`api_token_mask`, e.g. `sk-...XXXX`). That is correct for display, but an export without real tokens would be useless for cross-machine migration — every imported provider would need its token re-entered manually. Therefore the export endpoint returns the **real `api_token`**, and the UI must warn the user that the downloaded file contains secrets.

### Conflict Resolution Strategy

When importing, a provider from the file may collide with an existing provider. Collisions are detected by `id`. Three strategies:

| Strategy | Behavior on ID conflict |
| --- | --- |
| `skip` (default) | Keep the existing provider unchanged; do not import the conflicting one. |
| `overwrite` | Replace the existing provider's fields with the imported data (same ID). |
| `duplicate` | Ignore the imported ID, generate a new ID, and append the provider as a new entry. |

Name collisions (same name, different ID) are **not** treated as conflicts — duplicate names are allowed, matching the current create flow.

### Preview Before Apply

The frontend computes the preview client-side by comparing the import file's provider IDs against the current provider list (already loaded via `GET /api/providers`). The preview shows how many providers are **new** (ID not present) vs **conflicting** (ID already present). The user selects a conflict strategy, then confirms. This avoids a two-phase backend API.

### Routing Concern

`/api/providers/` is registered as a subtree handler (`handleProviderRoutes`) that treats the path suffix as a provider ID. The new `/api/providers/export` and `/api/providers/import` must be registered as **exact patterns** (like the existing `/api/providers/test`) so Go's `ServeMux` longest-prefix matching routes them before the subtree handler.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Done     | Export API endpoint | `internal/admin/provider_handler.go`, `server.go` | Handler test: selected IDs → JSON with real tokens |
| 2 | Done     | Import API endpoint | `internal/admin/provider_handler.go`, `server.go` | Handler tests: skip / overwrite / duplicate strategies |
| 3 | Done     | Frontend selection (checkbox per provider) | `ProviderCard.vue`, `DashboardView.vue` | Component test: checkbox toggles selection |
| 4 | Done     | Frontend toolbar + export/import flows | `DashboardView.vue`, `useApi.ts`, `useI18n.ts` | Frontend build + component tests for export download and import preview |
| 5 | Done     | i18n and edge-case polish | `useI18n.ts` | zh/en strings complete; empty-selection and parse-error handling |

## Requirements

### Deliverables

1. `POST /api/providers/export` — accepts `{"ids": ["id1", "id2", ...]}`, returns a JSON file with `version`, `exported_at`, and a `providers` array containing full `config.Provider` objects **including real `api_token`**. An empty `ids` array exports nothing (the frontend sends the explicitly selected IDs; "select all" is a frontend concern).
2. `POST /api/providers/import` — accepts `{"providers": [...], "strategy": "skip"|"overwrite"|"duplicate"}`, applies the import per strategy, and returns a summary `{"imported": N, "skipped": N, "overwritten": N, "duplicated": N}`.
3. The export endpoint validates that every requested ID exists; unknown IDs are silently skipped (defensive — the frontend only sends known IDs, but the backend must not 500 on a stale selection).
4. The import endpoint validates each imported provider via `Provider.Validate()`; an invalid provider is skipped with the error counted in the summary (the import does not abort entirely on one bad entry).
5. `ProviderCard.vue` gains a checkbox in the **top-left** corner (left of the provider name). Clicking it toggles the card's selected state without triggering edit/delete/etc.
6. The Providers tab toolbar gains **Export** and **Import** buttons to the **right** of the existing "Add Provider" button. Export is disabled when no providers are selected. Import opens a file picker.
7. The export flow: collect selected IDs → `POST /api/providers/export` → trigger a browser file download (`providers-export-YYYYMMDD-HHMMSS.json`).
8. The import flow: user picks a JSON file → frontend reads it client-side → shows a **preview modal** listing new vs conflicting counts and a conflict-strategy selector (default `skip`) → on confirm, `POST /api/providers/import` with the file's providers + strategy → refresh the list.
9. zh/en i18n covers all new labels, button text, modal copy, preview descriptions, conflict-strategy options, and the token-security warning.
10. The export download is offered as a JSON file; the import only accepts `.json` files and shows a clear error for malformed JSON or wrong schema (`version`/`providers` missing).

### Data Model

**Export file format:**

```json
{
  "version": 1,
  "exported_at": "2026-06-23T12:00:00Z",
  "providers": [
    {
      "id": "abc123",
      "name": "GLM",
      "api_url": "https://open.bigmodel.cn/api/anthropic",
      "api_token": "sk-real-token-here",
      "api_format": "anthropic",
      "openai_extra_params": {},
      "claude_code_compat_hint": null,
      "model_mappings": {"claude-sonnet-4": "glm-5"},
      "supports_thinking": false,
      "multimodal_switch": false,
      "multimodal_model": "",
      "strip_unknown_content_blocks": false,
      "rate_limit_queue_enabled": false,
      "max_concurrent_requests": 0,
      "max_queue_size": 0,
      "queue_timeout_ms": 0,
      "retry_429_enabled": false,
      "retry_429_max_attempts": 0,
      "retry_429_initial_delay_ms": 0,
      "retry_429_max_delay_ms": 0,
      "enabled": true,
      "created_at": "2026-06-20T10:00:00Z",
      "updated_at": "2026-06-23T09:00:00Z"
    }
  ]
}
```

The `version` field is `1`. Future format changes bump this number; the importer rejects unknown versions with a clear error.

**Import request format:**

```json
{
  "providers": [ /* same shape as export file's providers */ ],
  "strategy": "skip"
}
```

**Import response format:**

```json
{
  "success": true,
  "imported": 3,
  "skipped": 1,
  "overwritten": 0,
  "duplicated": 0,
  "errors": []
}
```

### Constraints

1. The export endpoint returns real `api_token` values — this is intentional for cross-machine migration. The frontend must show a security warning before/after export. The endpoint sits behind `authMiddlewareFunc` like every other provider route.
2. Conflict detection uses **provider `id`** only. Name collisions are allowed (two providers may share a name).
3. The `duplicate` strategy generates a new ID via `generateProviderID()` and resets `created_at`/`updated_at` to `time.Now()`.
4. The `overwrite` strategy preserves the existing provider's `created_at` but updates `updated_at` to `time.Now()`.
5. The import endpoint must not partially apply then fail — the entire import runs in one `Load → merge → Save` cycle. If `Save` fails, no providers are changed (the config reload is discarded).
6. Unknown provider IDs in the export request are silently skipped (not an error).
7. Invalid providers in the import request (failing `Provider.Validate()`) are skipped and reported in the `errors` array; the import continues for the remaining entries.
8. The importer rejects `version` values other than `1` with a clear error.
9. No new external dependencies; JSON handling stays on `encoding/json`.
10. The `claude_code_compat_hint` field is a `*bool` — `null` means "use format default". Export/import must preserve the pointer (not flatten it to a bool), otherwise re-import would lose the "unset" semantics.
11. The active provider (`active_provider_id`) is **not** part of the import/export — it is machine-specific. An imported provider that happens to share the active ID does not change which provider is active (unless `overwrite` replaces that exact provider's fields; the active ID itself stays unchanged).

### Edge Cases

1. User exports zero providers — the button is disabled; if the request somehow reaches the API with an empty `ids` array, the response contains an empty `providers` array (not an error).
2. User imports a file that is not valid JSON — frontend shows a parse error before calling the API.
3. User imports a valid JSON file with `version: 2` (future) — the API returns a clear "unsupported export version" error.
4. Two providers in the import file share the same ID — the first wins (skip/overwrite) or each gets a distinct new ID (duplicate); the importer deduplicates within the file before applying.
5. The import file references a provider ID that is the currently active provider, with `overwrite` strategy — the active provider's fields are replaced; `active_provider_id` is unchanged.
6. Network/API error during export or import — the frontend shows the error message and leaves the list unchanged.
7. The export file is large (many providers) — no pagination; a single JSON response.
8. The import file contains a provider with an `api_url` that fails validation — that provider is skipped and counted in `errors`; others import normally.
9. The import file omits optional fields (e.g. `model_mappings`) — `json.Unmarshal` leaves them as zero values; `Provider.normalizeDefaults()` (called inside `Validate`) fills maps to non-nil empties as needed.

### Non-Goals

1. No export/import of the `active_provider_id` — it is machine-specific state.
2. No encrypted/password-protected export file — the plaintext JSON is by design (the token-security warning is the mitigation).
3. No cloud sync or cross-machine push — this is a manual file-based export/import.
4. No export/import of global config fields (backend URL, theme, connection mode, listen addresses) — only providers.
5. No partial-field import (e.g. "import only model mappings") — the whole provider is imported.
6. No undo for import — the user can re-export before importing if they want a backup.

## Task Details

### Task 1: Export API Endpoint

#### Requirements

**Objective** — Add a backend endpoint that exports selected providers as a JSON file with real tokens for cross-machine migration.

**Outcomes** — `POST /api/providers/export` accepts `{"ids": [...]}` and returns `{"version": 1, "exported_at": "...", "providers": [...]}` with full `config.Provider` objects (including `api_token`). Unknown IDs are silently skipped.

**Evidence** — Handler test: create N providers, export a subset by ID, assert the response contains exactly those providers with non-masked tokens.

**Constraints** — Endpoint behind `authMiddlewareFunc`; registered as exact pattern `/api/providers/export` before the `/api/providers/` subtree handler; returns real tokens by design.

**Edge Cases** — Empty `ids` array → empty `providers` array; all IDs unknown → empty `providers` array; request body malformed → 400.

**Verification** — `go test ./internal/admin/`.

#### Plan

1. Add `handleExportProviders` to `provider_handler.go`: decode `{"ids": [...]}`, `Load` config, filter `cfg.Providers` by ID set, build the export struct, encode JSON.
2. Register `mux.HandleFunc("/api/providers/export", s.authMiddlewareFunc(s.handleExportProviders))` in `server.go` (before the `/api/providers/` line for readability, matching the `/api/providers/test` placement).
3. Define an `exportFile` struct (`Version int`, `ExportedAt time.Time`, `Providers []config.Provider`).
4. Write handler tests covering: subset export, all-unknown IDs, empty IDs.

#### Verification

- [ ] `POST /api/providers/export` with valid IDs returns those providers with real tokens.
- [ ] Unknown IDs are silently skipped.
- [ ] Response shape matches the export file format (`version`, `exported_at`, `providers`).

### Task 2: Import API Endpoint

#### Requirements

**Objective** — Add a backend endpoint that imports providers from a JSON payload with a user-chosen conflict strategy.

**Outcomes** — `POST /api/providers/import` accepts `{"providers": [...], "strategy": "skip"|"overwrite"|"duplicate"}`, applies the import in a single `Load → merge → Save` cycle, and returns `{"success": true, "imported": N, "skipped": N, "overwritten": N, "duplicated": N, "errors": [...]}`.

**Evidence** — Handler tests for each strategy: (a) `skip` — conflicting ID is not changed; (b) `overwrite` — conflicting ID's fields are replaced, `created_at` preserved, `updated_at` refreshed; (c) `duplicate` — new ID generated, appended.

**Constraints** — Single Load/Save cycle (no partial apply on Save failure); invalid providers skipped and counted in `errors`; `version != 1` rejected; `claude_code_compat_hint` pointer preserved.

**Edge Cases** — Duplicate IDs within the import file; invalid provider (bad URL); `version` mismatch; unknown strategy value (default to `skip`).

**Verification** — `go test ./internal/admin/`.

#### Plan

1. Add `handleImportProviders` to `provider_handler.go`: decode request, validate `version == 1`, `Load` config, iterate providers applying the strategy, `Save`, return summary.
2. Register `mux.HandleFunc("/api/providers/import", s.authMiddlewareFunc(s.handleImportProviders))`.
3. Deduplicate providers within the import file by ID before applying (first occurrence wins for skip/overwrite).
4. For `duplicate`, call `generateProviderID()` and reset timestamps.
5. For `overwrite`, preserve `created_at`, set `updated_at = time.Now()`.
6. Call `provider.Validate()` per entry; on error, append to `errors` and skip.
7. Write handler tests for all three strategies + version-mismatch + invalid-provider.

#### Verification

- [ ] `skip` strategy leaves conflicting providers unchanged.
- [ ] `overwrite` strategy replaces fields, preserves `created_at`.
- [ ] `duplicate` strategy appends with a new ID.
- [ ] Invalid providers are skipped and reported in `errors`.
- [ ] `version != 1` returns a clear error.

### Task 3: Frontend Provider Selection

#### Requirements

**Objective** — Let the user select one or more providers via a checkbox on each card.

**Outcomes** — `ProviderCard.vue` gains a checkbox in the top-left corner (left of the provider name). `DashboardView.vue` tracks a `Set<string>` of selected IDs. Clicking the checkbox toggles selection without triggering other card actions.

**Evidence** — Component test: clicking the checkbox adds/removes the provider ID from the selection; clicking edit/delete does not toggle selection.

**Constraints** — Checkbox is in the card's header row, left of the name, vertically aligned with the status dot; the card's existing action buttons remain unaffected.

**Edge Cases** — Selecting all providers; deselecting all; a provider is deleted while selected (the ID is removed from the selection).

#### Plan

1. Add a `selected` prop and `toggle-select` emit to `ProviderCard.vue`.
2. Place a `<input type="checkbox">` at the start of the header row (before the status dot).
3. In `DashboardView.vue`, add `selectedProviderIds = ref(new Set<string>())`.
4. Bind `:selected` and `@toggle-select` on each `<ProviderCard>`.
5. When a provider is deleted, remove its ID from the set.

#### Verification

- [ ] Checkbox appears top-left of each card.
- [ ] Toggling the checkbox updates the selection set.
- [ ] Card action buttons (edit, delete, etc.) still work independently.

### Task 4: Frontend Export/Import Flows

#### Requirements

**Objective** — Add Export and Import buttons to the toolbar and wire the full export/import user flows including the preview modal.

**Outcomes** — Toolbar has Export (disabled when nothing selected) and Import buttons to the right of "Add Provider". Export collects selected IDs, calls the API, and triggers a JSON download. Import opens a file picker, reads the file client-side, shows a preview modal (new vs conflict counts + strategy selector), and on confirm calls the import API and refreshes the list.

**Evidence** — Frontend build succeeds; component tests assert: export button disabled with empty selection; import preview shows correct new/conflict counts; confirm sends the right strategy.

**Constraints** — Export filename: `providers-export-YYYYMMDD-HHMMSS.json`; import accepts `.json` only; preview is computed client-side (compare import file IDs against current provider IDs).

**Edge Cases** — Empty selection on export (button disabled); malformed import file (parse error before API call); API error (show message, leave list unchanged); importing a file that is actually the export of the same machine (all conflicts).

#### Plan

1. Add `exportProviders(ids: string[])` and `importProviders(providers, strategy)` to `useApi.ts`.
2. In `DashboardView.vue`, add Export and Import buttons after the "Add Provider" button.
3. Export handler: call `exportProviders([...selectedProviderIds])`, build a `Blob`, trigger download.
4. Import handler: hidden `<input type="file" accept=".json">`; on change, read file via `FileReader`, `JSON.parse`, compute preview (new vs conflict), open modal.
5. Preview modal: show counts, strategy radio (default `skip`), confirm button → call `importProviders` → refresh list.
6. Component tests for export-disabled state and import preview.

#### Verification

- [ ] Export button disabled when no providers selected.
- [ ] Export downloads a `.json` file with selected providers.
- [ ] Import preview correctly classifies new vs conflicting providers.
- [ ] Confirm applies the chosen strategy and refreshes the list.

### Task 5: i18n and Edge-Case Polish

#### Requirements

**Objective** — Complete zh/en i18n for all new UI strings and harden edge cases (parse errors, API errors, security warning).

**Outcomes** — `useI18n.ts` gains zh/en keys for: toolbar buttons (export/import), selection state, preview modal (title, new count, conflict count, strategy labels, confirm/cancel), security warning, and error messages. All user-facing strings come from i18n — no hardcoded literals.

**Evidence** — Manual: toggle zh/en and verify every new string is translated; trigger each error path (malformed file, API failure) and verify the message is localized.

**Constraints** — Follow existing i18n key naming conventions; the security warning must appear prominently (export flow) and mention that the file contains API tokens.

**Edge Cases** — Locale switch does not break the modal; long provider lists do not overflow the modal; file larger than expected.

#### Plan

1. Add zh/en keys under `providers.export.*`, `providers.import.*`, `providers.preview.*`.
2. Wire every new UI string through `t(...)`.
3. Add the token-security warning to the export flow (before download or in the success toast).
4. Localize parse-error and API-error messages.

#### Verification

- [ ] Every new UI string has zh and en translations.
- [ ] Security warning is visible in the export flow.
- [ ] Error paths show localized messages.
