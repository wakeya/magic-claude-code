# Multimodal Model Switch Validation

**Feature:** Multimodal model switch
**Status:** validated
**Last updated:** 2026-06-09

## Acceptance Criteria

1. **Model switching:** If a request contains images, PDFs, audio, or video, and the Provider has `multimodal_switch` enabled with a configured `multimodal_model`, the final `model` uses the multimodal model ID.
2. **Text requests stay mapped:** Pure text requests continue to use existing `model_mappings`.
3. **Disabled switch stays mapped:** Requests containing non-text content do not override model mappings when the switch is disabled.
4. **Nested tool results:** Images embedded in `tool_result.content` are detected.
5. **Persistence:** SQLite save/load preserves multimodal Provider fields.
6. **Old database compatibility:** Existing `providers` tables without the new columns are upgraded automatically.
7. **Admin API:** Provider create, list, update, and duplicate operations preserve multimodal fields.
8. **API validation:** Enabling the switch without a multimodal model ID returns HTTP 400.
9. **UI configuration:** The Provider modal displays `多模态切换`, the question-mark tooltip, and the `多模态模型 ID` input.
10. **Build output:** Frontend dist assets are rebuilt after the UI change.

## Automated Verification

```bash
go test ./internal/proxy ./internal/config ./internal/admin
go test ./...
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

## Evidence

2026-06-09 / 2026-06-10 audit:

1. `go test ./internal/proxy ./internal/config ./internal/admin` passed, covering proxy model selection, SQLite persistence, and Admin API behavior.
2. Proxy tests cover screenshot image tool results, document blocks, PDF media type, audio media type, video media type, pure text fallback, and disabled-switch fallback.
3. Admin API tests cover create/list/detail round-trip, omitted-field update preservation, required model validation, and duplicate preservation.
4. `go test ./...` passed: `311 passed in 8 packages`.
5. `npm --prefix internal/frontend test` passed: `37 passed`.
6. `npm --prefix internal/frontend run build` passed with a successful Vite build.
7. Browser check at `http://127.0.0.1:5178`:
   - Provider modal showed `多模态切换`.
   - Question-mark tooltip was visible.
   - Enabling the switch showed the `多模态模型 ID` input.
8. User verified the new version against the real Mimo multimodal model `mimo-v2.5`; the proxy can access the configured Mimo multimodal model successfully.

## Manual Verification Scenarios

### Scenario 1: Mimo Image Request

Status: verified by user against the real Mimo multimodal model `mimo-v2.5`.

1. Configure model mapping: `claude-opus-4-6 -> mimo-v2.5-pro`.
2. Enable `多模态切换`.
3. Set `多模态模型 ID` to `mimo-v2.5`.
4. Trigger a Claude Code screenshot tool result.
5. Expected: proxy logs show the final `model` as the multimodal model, and the upstream no longer returns `No endpoints found that support image input`.

### Scenario 2: GLM Image-Compatible Model

1. Configure model mapping: `claude-opus-4-6 -> glm-5.1`.
2. Keep `多模态切换` disabled.
3. Send a request containing an image.
4. Expected: proxy continues using `glm-5.1` without model override.

### Scenario 3: Pure Text Request

1. Enable `多模态切换` and configure a model ID.
2. Send a normal pure text request.
3. Expected: proxy uses the mapped text model, not the multimodal model.
