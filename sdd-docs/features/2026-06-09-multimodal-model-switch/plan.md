# Multimodal Model Switch Implementation Plan

**Goal:** Add optional multimodal model switch configuration to Providers, then select the final upstream model based on request content during proxy request transformation.

**Architecture:** Reuse the existing Provider model, SQLite store, Admin API, and `transformRequest` path. No new external dependency is required.

---

## File Plan

Modify:

1. `internal/config/provider.go`
2. `internal/config/sqlite_store.go`
3. `internal/admin/provider_handler.go`
4. `internal/proxy/handler.go`
5. `internal/frontend/src/composables/useApi.ts`
6. `internal/frontend/src/composables/useI18n.ts`
7. `internal/frontend/src/components/ProviderModal.vue`
8. `internal/frontend/src/components/ProviderCard.vue`
9. `internal/frontend/dist/*`

Add:

1. `internal/admin/provider_handler_test.go`

Extend tests:

1. `internal/proxy/server_test.go`
2. `internal/config/sqlite_store_test.go`

## Task 1: Proxy Model Selection Tests

- [x] Add failing test: when `tool_result.content` contains `type: image`, use `multimodal_model`.
- [x] Add media detection tests for document blocks, PDF media type, audio media type, and video media type.
- [x] Add failing test: pure text requests still use `model_mappings` even when the switch is enabled.
- [x] Add failing test: image requests still use `model_mappings` when the switch is disabled.
- [x] Verify RED: tests fail because Provider does not yet expose the new fields.

## Task 2: Configuration Model and Request Transformation

- [x] Add `MultimodalSwitch` and `MultimodalModel` to Provider.
- [x] Reuse the parsed JSON in `transformRequest` to recursively detect non-text content under `messages/system`.
- [x] When non-text content is detected and Provider configuration is valid, replace request `model` with `MultimodalModel`.
- [x] Update request logging and usage records so `mapped_model` reflects the final forwarded model.

## Task 3: SQLite Storage and Migration

- [x] Add `multimodal_switch` and `multimodal_model` to the `providers` table.
- [x] Add compatibility migration for existing SQLite databases.
- [x] Persist and load the new Provider fields.
- [x] Add a migration test for the old Provider table schema.

## Task 4: Admin API

- [x] Return multimodal fields from Provider list and detail responses.
- [x] Accept and persist multimodal fields when creating Providers.
- [x] Support partial updates without clearing existing multimodal configuration when fields are omitted.
- [x] Preserve multimodal configuration when duplicating Providers.
- [x] Validate that enabling the switch requires a non-empty multimodal model ID.
- [x] Cover list/detail/update/duplicate behavior with Admin API tests.

## Task 5: Frontend Configuration UI

- [x] Add `multimodal_switch` and `multimodal_model` to API types.
- [x] Add a `多模态切换` switch to the Provider modal.
- [x] Add a question-mark tooltip next to the switch.
- [x] Show the `多模态模型 ID` input when the switch is enabled.
- [x] Submit multimodal fields when saving Providers.
- [x] Show a compact multimodal switch summary on Provider cards.
- [x] Add Chinese and English i18n strings.

## Task 6: Verification

- [x] `go test ./internal/proxy ./internal/config ./internal/admin`
- [x] `go test ./...`
- [x] `npm --prefix internal/frontend test`
- [x] `npm --prefix internal/frontend run build`
- [x] Browser render check for the Provider modal.

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Recursive request scanning affects latency | Reuse the existing parsed request map in `transformRequest`; scan once per request. |
| Plain text requests are misclassified as multimodal | Match only explicit content block types or explicit `media_type` values. |
| Partial API updates erase existing settings | Use pointer fields to distinguish omitted fields from explicit false/empty values. |
| Old databases lack the new columns | Add startup-time column checks and `ALTER TABLE` migrations. |
| Switch enabled without a model ID | Validate in both frontend and Admin API. |
