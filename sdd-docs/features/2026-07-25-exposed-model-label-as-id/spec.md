# Exposed Model Label as Routing ID Spec

**Local page:** Provider Management → Edit → "Switchable via /model" section (`internal/frontend/src/components/ProviderModal.vue` exposed-models block, ~L150-172)
**Proxy entry:** `internal/config/provider.go` (`ExposedModel`, `Provider.Validate`, `generateExposedModelID`, ~L298-342, L204-243); `internal/config/config.go` (cross-provider global uniqueness + `ResolveRoute`, ~L146-160, L287-310); `internal/config/sqlite_store.go` (`migrateExposedModelIDs`, ~L93-119); `internal/proxy/hardcoded.go` (`collectAdditionalModelOptions`, `collectModels`, ~L557-601, L651-689)
**Reference sources:** `sdd-docs/features/2026-07-08-cross-provider-model-routing/spec.md` (cross-provider routing and the random-ID design); `sdd-docs/features/2026-07-19-log-exposed-model-label/spec.md` (log shows Label); `claude-code-src/src/src/utils/model/model.ts` (`parseUserSpecifiedModel` passes custom strings through verbatim, preserving case), `validateModel.ts` (validation via a real API call), `modelAllowlist.ts` (`availableModels` allowlist unset by default → all allowed)
**Stack:** Go 1.26 standard library + Vue 3 frontend
**Last updated:** 2026-07-25
**Progress:** 5 / 5

## Overall Analysis (Source Analysis)

### Symptom

Each "Switchable via /model" entry in the provider editor carries two identifiers:

- `ExposedModel.ID`: the routing key, which becomes the `/model` menu item value and the request `model` field. Currently a system-generated random `em-<hex>` (`generateExposedModelID`, `provider.go:330`); **the frontend hides the ID input, so the user can never know it**.
- `ExposedModel.Label`: the display name shown in the `/model` menu, the dialog, and (since 2026-07-19) the logs.

The mismatch makes `claude --model` unusable:

- `claude --model <model-id>`: the ID is a random `em-<hex>` the user cannot see or remember.
- `claude --model <display-name>`: `ResolveRoute` (`config.go:287`) matches only `em.ID == model`; Label never participates in routing → miss → falls back to the active provider's `MapModel` → the backend receives an unknown Label → 404 "model not found".

### Root Cause (evolution of the random ID)

`git log -S generateExposedModelID` reconstructs the direction changes:

1. The original spec (`406f77b`, cross-provider routing) let users **hand-type a semantic ID** (e.g. `glm-5.2-ky`); the ID was the menu value.
2. Later it switched to **hiding the ID input on the frontend**, auto-generating a stable random `em-<hex>`, plus a one-time migration `migrateExposedModelIDs` (`sqlite_store.go:97`) that force-rewrote existing hand-typed IDs to random em-. Its comment records the known side effect: "after migration the user's stale mainLoopModelOverride in ~/.claude.json is invalidated; they must re-select via /model" — **changing the ID invalidates the client's session-level selection, but that is an acceptable side effect**.
3. Even later (`2026-07-19`) the log layer shows Label instead of the em- ID, fixing log readability but not `--model` usability.

The random ID satisfied "the user need not know the ID, and the ID stays stable across provider reordering", at the cost of splitting "what you see (Label)" from "what you use (ID)".

### Client-side verification (three facts that make the approach viable)

**Fact 1: `--model` does no model-existence check.** `getUserSpecifiedModelSetting` (`model.ts`) only passes through `isModelAllowed` (`modelAllowlist.ts:100`), whose `availableModels` allowlist comes from `settings` and **allows everything when unset (the default)**. So `--model <any string>` flows verbatim into the request `model` field.

**Fact 2: custom model strings pass through verbatim, case preserved.** `parseUserSpecifiedModel` (`model.ts:445`) returns `modelInputTrimmed` for non-alias strings (trims surrounding whitespace only); the comment states "Preserve original case for custom model names". Unicode and interior spaces are preserved; only the `sonnet/opus/haiku/opusplan/best` aliases are special-cased.

**Fact 3: interactive validation makes a real API call.** When a custom name is typed into the `/model` menu, `validateModel` (`validateModel.ts:19`) issues a real `sideQuery` (max_tokens=1); success means valid. So as long as mcc routes the string, interactive validation also passes.

→ **Conclusion: the only blocker is that mcc's `ResolveRoute` matches `em.ID` alone.** Making Label the routing key unblocks both the `--model` main-loop path and the `/model` interactive-validation path, with zero client changes.

### Design decision: Label = ID (what-you-see-is-what-you-get)

**Core: keep the `ExposedModel.ID` field (the sqlite/admin/frontend pass-through chain is untouched), but on save set `em.ID = TrimSpace(em.Label)`, unifying the display name and the routing key.** The user fills in a single "display name" that is simultaneously the `/model` menu display text, the menu value, the request `model` field, and the routing match key.

| Option | Description | Adopted |
| --- | --- | --- |
| A. Label = ID | Unify ID and display name; routing logic unchanged; menu value == display text | ✅ |
| B. Route falls back to Label match | Keep random ID; add a secondary Label match in `ResolveRoute` | — (split remains) |
| C. Auto semantic slug | Derive a slug from Label as the ID | — (Chinese hard to slugify; stability coupled to Label) |
| D. New alias field | Add a field across the whole chain (SQLite/admin/frontend/i18n) | — (heaviest change) |

Why A:

- **Zero routing change** — `ResolveRoute` still matches `em.ID`; only the ID's value becomes the Label.
- **Zero bootstrap/`/v1/models` change** — `collectAdditionalModelOptions` already emits `model=ID, name=Label`; once ID=Label the two are naturally identical (`value == display text`). The admin pass-through chain likewise needs no change (`Provider.Validate` normalizes ID=Label).
- **Eliminates the split entirely** — what `/model` shows is usable with `--model`.

**Charset constraint (Label inherits the ID's client-side constraints once it is the routing key):** allow Unicode (so Chinese display names like "智谱GLM" work), **forbid spaces and control characters** (spaces need shell quoting in `--model` and are error-prone), forbid the `claude-` prefix, `[1m]`, and the `sonnet|opus|haiku|opusplan` Claude Code reserved aliases (reuse existing checks, `provider.go:224-238`).

**Uniqueness scope: globally unique (across all providers).** Routing matches globally across providers (`ResolveRoute` scans every enabled provider); duplicate Labels would cause routing ambiguity (first hit wins). Reuse the existing `em.ID` global-uniqueness check in `config.go:146-160` (once ID=Label, that is Label global uniqueness).

### Impact analysis

| File | Change | Notes |
|------|--------|-------|
| `internal/config/provider.go` | **change** | `Validate` sets `em.ID = em.Label`; charset check switches from an ASCII allowlist to a "forbid space/control" denylist; remove the `em.ID == "" → generateExposedModelID()` branch; delete `generateExposedModelID` |
| `internal/config/config.go` | **minor** | Global-uniqueness error wording changes from "exposed model id" to mention the "display name" (more accurate once ID=Label); logic unchanged |
| `internal/config/sqlite_store.go` | **change** | `migrateExposedModelIDs` reversed: rewrite `em.ID` to `TrimSpace(em.Label)` (reversing the historical "hand-typed→random" direction); idempotent |
| `internal/proxy/hardcoded.go` | **no change** | Once ID=Label, `model == name` and `id == display_name` hold naturally |
| `internal/admin/provider_handler.go` | **no change** | Passes `ExposedModels` through; `store.Update → Validate` normalizes ID=Label automatically |
| `internal/frontend/.../ProviderModal.vue` | **change** | Add a hint to the display-name input: "display name is the model ID, usable directly with `claude --model <name>`" |
| `internal/frontend/.../useI18n.ts` | **change** | Add/update hint strings (bilingual); update the stale "ID is auto-generated" wording |

### Backward compatibility

- **Existing random em- IDs**: the startup migration `em.ID = TrimSpace(em.Label)` rewrites them to Label. Side effect: the client's stale `mainLoopModelOverride` (holding the old em- ID) is invalidated and must be re-selected via `/model` — consistent with the historical migration's side effect, acceptable (`/model` is session-level in-memory state; see the cross-provider spec).
- **Existing Labels containing spaces**: the migration does not strip interior spaces (`TrimSpace` only trims the ends); ID=Label keeps interior spaces and **routing still hits** (`--model "Kimi K2"` goes through `parseUserSpecifiedModel` which trims ends only, preserving interior spaces). But the new charset check will reject a space-containing Label on the next edit-save (`store.Update → Validate`); the user must rename it to be space-free — this is "surfaced", not silently broken, per coding guideline #6.
- **Existing duplicate Labels across providers**: after migration the IDs duplicate; routing hits the first (no crash); the global-uniqueness check returns 400 on the next edit-save, with the provider names in the error for locating the conflict.
- **JSON Store**: `cmd/server/main.go:121` uses `NewSQLiteStore` only; the JSON `Store` is a legacy migration source and **needs no migration**; its users get normalized on the next edit-save via `Validate`.

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | ✅ | config layer: `Validate` sets ID=Label + charset denylist + remove random generation; global-uniqueness wording | `internal/config/provider.go`, `internal/config/config.go` | `go test ./internal/config/...` |
| 2 | ✅ | SQLite reverse migration: `migrateExposedModelIDs` rewrites em- ID → Label | `internal/config/sqlite_store.go` | SQLite round-trip migration test |
| 3 | ✅ | Frontend hint + i18n (bilingual) | `ProviderModal.vue`, `useI18n.ts`, `dist/` | `npm --prefix internal/frontend test && run build` |
| 4 | ✅ | Update affected tests + full regression | `provider_test.go`, `sqlite_store_test.go`, etc. | `make test` |
| 5 | ✅ | End-to-end verification + commit | verification record | manual `/model` + `--model` full chain; `git commit` (no push) |

## Requirements

### Deliverables

1. `Provider.Validate` (`provider.go`) sets `em.ID = em.Label` for each `ExposedModel` after TrimSpace; removes the "empty ID → `generateExposedModelID()`" branch; deletes the `generateExposedModelID` function.
2. `Provider.Validate`'s ID charset check changes from the ASCII allowlist `[a-zA-Z0-9._:-]` to a denylist: **forbid spaces (`unicode.IsSpace`) and control characters (`unicode.IsControl`)**, allowing all other Unicode. Keep the `claude-` prefix, `[1m]`, and `sonnet|opus|haiku|opusplan` alias prohibitions.
3. `Config.Validate` (`config.go`) global-uniqueness logic is unchanged (still keyed on `em.ID`, which equals Label); the error wording changes from "exposed model id %q is duplicated" to mention "display name (model id)" for clarity.
4. `SQLiteStore.migrateExposedModelIDs` (`sqlite_store.go`) reverses direction: for each `ExposedModel`, when `TrimSpace(em.Label)` is non-empty and `em.ID != TrimSpace(em.Label)`, set `em.ID = TrimSpace(em.Label)` and mark changed; `s.save(cfg)` if any change. Idempotent (no re-trigger on second startup). Remove the `generateExposedModelID` call.
5. Frontend `ProviderModal.vue` adds a hint in the exposed-models section: the display name is the model ID, usable directly with `claude --model <name>`; `useI18n.ts` bilingual, and the stale "ID is auto-generated" wording becomes "the display name becomes the model ID".
6. `internal/proxy/hardcoded.go` and `internal/admin/provider_handler.go` are **not changed** (naturally satisfied once ID=Label).

### Constraints

- The `ExposedModel.ID` field is retained (no data-model/schema change); only its assignment semantics become "= Label".
- Route matching (`ResolveRoute`), request body, `usage`, and failover logic are all untouched.
- Global uniqueness reuses the existing check path (`Config.Validate`); no parallel validation.
- After frontend source changes, `npm run build` must rebuild `dist/` (embedded in the Go binary; see CLAUDE.md commit constraint #4).
- Bilingual docs/strings stay in sync (see global memory Bilingual Output Requirement).

### Edge cases

- Empty Label → `label is required` (existing check).
- Label with spaces/control chars → rejected by the new charset check.
- Label starting with `claude-` / containing `[1m]` / a reserved alias → rejected (existing).
- Label duplicated across providers → `Config.Validate` returns 400 (existing global uniqueness).
- Label in Chinese/Unicode (no spaces) → passes; usable as a routing key.
- Context1M model: the bootstrap value still carries `[1m]` (ID+`[1m]`); `ResolveRoute` strips `[1m]` then matches the bare Label.
- Migration: an existing entry with an empty Label is skipped (ID not set to ""); migration is idempotent.

## Task Details

### Task 1: config layer — Label=ID + charset denylist + remove random generation

#### Requirements

**Objective** — Make `ExposedModel.ID` equal its `Label` on save (display name is the routing key), switch the charset check to "allow Unicode, forbid spaces/control chars", and remove the random `em-<hex>` generation path, so the `/model` menu display text and the value usable with `claude --model` are unified.

**Outcomes** — `provider.go`: `Validate`'s second loop sets `em.ID = em.Label` at its head (Label already TrimSpace'd in the first loop); delete the `if em.ID == "" { em.ID = generateExposedModelID() }` branch; replace the charset check with the `unicode.IsSpace(r) || unicode.IsControl(r)` denylist (import `unicode`); delete `generateExposedModelID` (its `crypto/rand`/`encoding/hex` deps are still used by `generateProviderID`/`randomHex`, so keep the imports). `config.go`: the global-uniqueness error wording changes to mention the "display name".

**Evidence** — `go test ./internal/config/` passes: after `Validate`, `em.ID == em.Label`; a space-containing Label errors; a Chinese Label (e.g. `智谱GLM`) passes with `em.ID == "智谱GLM"`; `claude-` prefix / `[1m]` / aliases still rejected; duplicate Labels across providers return 400.

**Constraints** — The `ExposedModel.ID` field is retained; `ResolveRoute` is untouched (still matches `em.ID`); keep the `claude-`/`[1m]`/alias prohibitions and the Label/BackendModel non-empty checks; keep the per-provider duplicate check `seenExposedIDs` (once ID=Label it is per-provider Label dedup).

**Edge Cases** — Empty Label → `label is required`; all-whitespace Label (empty after TrimSpace) → `label is required`; Label with interior space → charset rejects; Label with tab/newline (control/space) → rejects; Chinese/Japanese/emoji (no spaces) → passes.

**Verification** — `go test ./internal/config/ -run TestProvider` and `go vet ./internal/config/` green.

#### Plan

1. **Change the failing test first.** `internal/config/provider_test.go`:
   - Replace the L224-234 "empty ID auto-generates em- prefix" case with "ID equals Label": build `Provider{... ExposedModels: []ExposedModel{{Label: "GLM-4.6", BackendModel: "glm-4.6"}}}` (empty ID), and after `Validate()` assert `em.ID == "GLM-4.6"`.
   - Add a case: `Label: "智谱GLM"` → `Validate()` passes and `em.ID == "智谱GLM"`.
   - Add a case: `Label: "Kimi K2"` (with space) → `Validate()` returns an error containing "space".
   - Add a case: `Label: "Kimi\tK2"` (with tab) → returns an error.
   - Keep and confirm the `claude-` prefix, `[1m]`, and `sonnet` alias rejection cases still pass.
2. **Confirm failure.** `go test ./internal/config/ -run TestProvider` → fails (ID still empty or em-, space not rejected).
3. **Minimal implementation.** `internal/config/provider.go`:
   - Add `"unicode"` to the import block.
   - At the head of the second loop (~L211-243):
     ```go
     em := &p.ExposedModels[i]
     // What-you-see-is-what-you-get: the display name is the unique routing key; normalize ID to Label
     em.ID = em.Label
     if em.Label == "" {
         return fmt.Errorf("exposed_models[%d]: label is required", i)
     }
     ```
     (delete the original `if em.ID == "" { em.ID = generateExposedModelID() }`)
   - Replace the charset check (~L234-238):
     ```go
     if strings.IndexFunc(em.ID, func(r rune) bool {
         return unicode.IsSpace(r) || unicode.IsControl(r)
     }) >= 0 {
         return fmt.Errorf("exposed_models[%d]: display name (model id) must not contain spaces or control characters", i)
     }
     ```
   - Delete `generateExposedModelID` (~L327-332) and its comment.
4. **Minor config.go change.** `internal/config/config.go:155` error becomes:
   ```go
   return fmt.Errorf("exposed model display name (id) %q is duplicated between provider %q and %q", id, firstProvider, c.Providers[i].Name)
   ```
5. **Confirm pass.** `go test ./internal/config/ -run TestProvider` all pass; `go test ./internal/config/` full package regression.
6. **Regression.** `go vet ./internal/config/`.
7. **Commit.** `git add internal/config/provider.go internal/config/config.go internal/config/provider_test.go && git commit -m "feat(config): exposed model label as routable id (what-you-see-is-what-you-get)"`.

#### Verification

- [x] `go test ./internal/config/ -run TestProviderValidate_ExposedModel` — all pass (ID=Label, Unicode passes, spaces/control/`claude-`/`[1m]`/aliases rejected, per-provider duplicate rejected).
- [x] `go test ./internal/config/` and `go vet ./internal/config/` — clean.

### Task 2: SQLite reverse migration — em- ID → Label

#### Requirements

**Objective** — Reverse `migrateExposedModelIDs` from the historical "hand-typed ID → random em-" to "random em- ID → Label", so existing configs unify ID and display name on startup and `claude --model <display-name>` works immediately for existing data.

**Outcomes** — `migrateExposedModelIDs` in `sqlite_store.go:97-119` becomes: iterate every provider's `ExposedModels`; when `label := strings.TrimSpace(em.Label)` is non-empty and `em.ID != label`, set `em.ID = label`, `changed = true`; `s.save(cfg)` if changed. Remove the `generateExposedModelID` call. Update the function comment to describe the new direction and the side effect (stale mainLoopModelOverride invalidated; re-select via /model).

**Evidence** — `go test ./internal/config/ -run TestMigrate` passes: seed `ExposedModels: [{ID:"em-abcd1234", Label:"GLM-4.6", BackendModel:"x"}, {ID:"em-ffff0000", Label:"Kimi", BackendModel:"y"}]`; after opening a fresh store (triggering migration) the two IDs become `"GLM-4.6"` and `"Kimi"`; a second Load makes no further change (idempotent); an entry with an empty Label does not get its ID blanked.

**Constraints** — Migration uses `s.save(cfg)` (lowercase, **does not trigger Validate**), so existing space-containing Labels are not rejected during migration; migration is idempotent; SQLite store only (`cmd/server/main.go:121` is the sole store in use); the JSON Store needs no migration.

**Edge Cases** — Empty Label → skipped (ID not set to ""); ID already == Label → unchanged (idempotent); Label with interior spaces → ID keeps interior spaces (migration does not scrub; surfaced on next edit validation); duplicate Labels across providers → migration writes them anyway (no validation), routing hits the first, edit-save returns 400.

**Verification** — `go test ./internal/config/ -run TestMigrate` green; `go test ./internal/config/` full package regression.

#### Plan

1. **Change the failing test first.** Rewrite the migration test in `internal/config/sqlite_store_test.go` (~L955-1006) to the reverse direction:
   - Seed the old format: `{ID: "em-abcd1234", Label: "GLM-4.6", BackendModel: "x"}` and `{ID: "em-ffff0000", Label: "Kimi", BackendModel: "y"}`, written directly to the DB (bypassing Validate).
   - Open a fresh `SQLiteStore` to trigger `init → migrateExposedModelIDs`.
   - Assert `got[0].ID == "GLM-4.6"` and `got[1].ID == "Kimi"`.
   - Assert idempotency: a second `Load` leaves the IDs unchanged.
   - Add an assertion that an empty-Label entry is not blanked.
2. **Confirm failure.** `go test ./internal/config/ -run TestMigrate` → fails (IDs still em-).
3. **Minimal implementation.** In `internal/config/sqlite_store.go`, replace `migrateExposedModelIDs` (L97-119) with:
   ```go
   // migrateExposedModelIDs one-time migration (what-you-see-is-what-you-get direction): rewrite
   // existing random em-<hex> IDs to TrimSpace(Label), making the display name the routing key.
   // After migration ID==Label, so it is idempotent and never re-triggers. Uses s.save (no Validate)
   // so existing space-containing Labels are not rejected during migration; their charset constraint
   // is surfaced on the next edit-save. Side effect: the client's stale mainLoopModelOverride is
   // invalidated and must be re-selected via /model.
   func (s *SQLiteStore) migrateExposedModelIDs() error {
       cfg, err := s.Load()
       if err != nil {
           return err
       }
       if cfg == nil {
           return nil
       }
       changed := false
       for i := range cfg.Providers {
           for j := range cfg.Providers[i].ExposedModels {
               em := &cfg.Providers[i].ExposedModels[j]
               if label := strings.TrimSpace(em.Label); label != "" && em.ID != label {
                   em.ID = label
                   changed = true
               }
           }
       }
       if changed {
           return s.save(cfg)
       }
       return nil
   }
   ```
4. **Confirm pass.** `go test ./internal/config/ -run TestMigrate` all pass.
5. **Regression.** `go test ./internal/config/`, `go vet ./internal/config/`.
6. **Commit.** `git add internal/config/sqlite_store.go internal/config/sqlite_store_test.go && git commit -m "feat(config): reverse migration rewrites exposed model id to label"`.

#### Verification

- [x] `go test ./internal/config/ -run TestSQLiteStoreMigratesLegacyExposedModelIDs` — all pass (em- → Label, idempotent, empty Label not blanked).
- [x] `go test ./internal/config/` and `go vet` — clean.

### Task 3: Frontend hint + i18n

#### Requirements

**Objective** — Make it clear in the exposed-model editor that "the display name is the model ID, usable directly with `claude --model`", and remove the stale "ID is auto-generated" wording.

**Outcomes** — The hint in the `ProviderModal.vue` exposed-models section (~L150-172) is updated to state that the display name is the ID; the corresponding keys in `useI18n.ts` are updated bilingually (`exposed_model_backend_hint` etc., ~Chinese L168-175, English L638-645), changing "ID 由系统自动生成 / ID is auto-generated" to "显示名将作为模型 ID，可直接用于 claude --model <显示名> / Display name becomes the model ID, usable directly with claude --model <name>". `npm run build` rebuilds `dist/`.

**Evidence** — `npm --prefix internal/frontend test` passes; `npm --prefix internal/frontend run build` succeeds and `dist/` is updated; opening the edit modal shows the new hint.

**Constraints** — No change to the form data structure or submit logic (still submits `id: em.id`; the backend `Validate` normalizes it to Label); strings stay bilingual.

**Edge Cases** — Reused i18n keys get their values updated; new keys are added in both languages; the build artifact `dist/` is committed.

**Verification** — Frontend tests and build green; `dist/` changed.

#### Plan

1. Locate the existing hint in the `ProviderModal.vue` exposed-models section (`t('modal.exposed_model_backend_hint')`, ~L167) and the corresponding key values in `useI18n.ts` (Chinese ~L173, English ~L643).
2. Update `useI18n.ts`: change "可从上方"模型映射"的映射值快速填充；ID 由系统自动生成" to "可从上方"模型映射"快速填充后端模型名；显示名将作为模型 ID，可直接用于 claude --model <显示名>"; English in sync: "Autofill backend model from the mappings above; the display name becomes the model ID, usable directly with claude --model <name>".
3. If a standalone hint beside the display-name input is desired, add an `exposed_model_label_hint` key (bilingual) and render it below the display-name input in `ProviderModal.vue`.
4. `npm --prefix internal/frontend test`.
5. `npm --prefix internal/frontend run build` (rebuild `dist/`).
6. **Commit.** `git add internal/frontend/src internal/frontend/dist && git commit -m "feat(frontend): hint that exposed model display name is the routable id"`.

#### Verification

- [x] `npm test` (internal/frontend) — 195 pass, 0 fail.
- [x] `npm run build` — succeeds, `dist/` rebuilt (useI18n/index asset hashes updated).

### Task 4: Update affected tests + full regression

#### Requirements

**Objective** — Fix every test that depended on the old "random em- ID" contract so `make test` (with race + coverage) is fully green.

**Outcomes** — `provider_test.go` and `sqlite_store_test.go` are already updated in Tasks 1/2; this task sweeps the remaining tests that ID=Label may affect (`config_test.go`, `failover_test.go`, `admin/provider_handler_test.go`, `proxy/hardcoded_test.go`, etc.: any case that calls `Validate` while explicitly setting `ID != Label` will have ID normalized to Label — adjust the assertion or align Label with ID).

**Evidence** — `make test` fully green (0 failures); `go vet ./...` clean.

**Constraints** — Do not change production logic to appease tests; test assertions reflect the new contract (ID=Label).

**Edge Cases** — `ResolveRoute` unit tests mostly construct `ExposedModel{ID:..., Label:...}` directly without `Validate` and are usually unaffected, but confirm each.

**Verification** — `make test` green.

#### Plan

1. `grep -rn "ExposedModel{" internal/ | grep _test` to sweep all test construction sites and find cases that go through `Validate` with ID≠Label.
2. Fix each: align `Label` with the expected routing key, or adjust the assertion.
3. `make test` (= `go test -v -race -coverprofile=coverage.out ./...`).
4. `go vet ./...`.
5. **Commit (if tests changed).** `git add -A internal && git commit -m "test: align exposed model tests with label-as-id contract"`.

#### Verification

- [x] `go test -race ./...` — all 15 packages ok, 0 failures (incl. affected-test updates: 3 admin cases, 4 proxy bootstrap/models cases switched to the ID=Label contract + what-you-see-is-what-you-get assertions).
- [x] `go vet ./...` — clean.

### Task 5: End-to-end verification + commit wrap-up

#### Requirements

**Objective** — Verify "what-you-see-is-what-you-get" on a real chain: the display name shown in the `/model` menu is directly usable with `claude --model`, and requests route correctly to the target provider's BackendModel.

**Outcomes** — Build the binary, configure an exposed model (display name e.g. `GLM-4.6`, BackendModel e.g. `glm-4.6`), and verify: (a) the startup migration rewrites existing em- IDs to Label; (b) `/api/claude_cli/bootstrap`'s `additional_model_options` has `model == name == GLM-4.6`; (c) `GET /v1/models` returns `id == display_name == GLM-4.6`; (d) a `/v1/messages` request with `model=GLM-4.6` hits that provider and forwards the BackendModel; (e) a `claude --model GLM-4.6` session works.

**Evidence** — Actual output of each step recorded here.

**Constraints** — Commit only, do not push (see global memory Local Commit Before Push); before committing, `git status --short` / `git diff --stat` confirm only this feature's files are included.

**Edge Cases** — Also verify a model with a Chinese display name (e.g. `智谱GLM`).

**Verification** — Full chain passes; working tree contains only this feature's changes.

#### Plan

1. `make build` (or `go build ./cmd/server`) to build.
2. Configure the exposed model, start, and `curl` to verify bootstrap and `/v1/models` emit `model == name`, `id == display_name`.
3. Craft a `POST /v1/messages` body `{"model":"GLM-4.6",...}` and confirm the log hits the target provider with `-> BackendModel`.
4. Repeat once with a Chinese display name.
5. `git status --short && git diff --stat` to check the change scope; confirm the frontend `dist/` is included.
6. Consolidate the per-task commits; **do not push**, wait for user confirmation.

#### Verification

- [x] bootstrap emits `model==name` (asserted by `TestHandleBootstrap_EmitsExposedModels`); `/v1/models` emits `id==display_name` (asserted by `TestHardcodedModelsUsesConfiguredProviders`).
- [x] Display-name routing: `TestResolveRouteByDisplayNameAfterValidate` proves that after `Validate` normalization, `ResolveRoute("智谱GLM")` hits the target provider + BackendModel (i.e. the `claude --model <display-name>` chain).
- [x] Context1M model value carries `[1m]` and still routes by the bare Label (`TestHandleBootstrap_Context1MAppendsBracket1m`).
- [x] `git status` shows only this feature's changes; committed, not pushed.

> Verification-level note: the above is integration-test-level verification (httptest + real Handler/Store + real `Validate` path), covering the full bootstrap, `/v1/models`, and `ResolveRoute` chain. A literal `claude --model` CLI on-device check can be done manually before merge.
