# Multimodal Model Switch Requirements

**Version:** 1.0
**Date:** 2026-06-09
**Status:** implemented
**Lifecycle:** validated

---

## 1. Objective

Add an optional Provider-level multimodal model switch. When a Claude Code request contains non-text content such as images, PDFs, audio, or video, the proxy can replace the request model with a single configured multimodal model ID for that Provider.

This prevents upstream text-only models from rejecting requests that include screenshots or other multimodal tool results.

## 2. Background

The Xiaomi Mimo Provider returned this error when a request contained a screenshot image:

```json
{"error":{"code":"404","message":"No endpoints found that support image input","param":"","type":""}}
```

Investigation confirmed that the image came from a `mcp__chrome-devtools-mcp__take_screenshot` tool result. The request body included an `image/png` base64 content block inside `tool_result.content`. The existing model mapping sent `claude-opus-4-6` to `mimo-v2.5-pro`, which does not support image input.

Some Providers or models, such as GLM-compatible endpoints, may accept image-bearing requests even when the configured model appears to be text-oriented. Therefore this must be an explicit Provider strategy, not a global automatic behavior.

## 3. Requirements

### 3.1 Provider Configuration

Each Provider must support two new fields:

1. `multimodal_switch`: whether the proxy should switch models for non-text requests.
2. `multimodal_model`: the target model ID used when non-text content is detected.

Defaults:

1. `multimodal_switch = false`
2. `multimodal_model = ""`

### 3.2 Model Selection Rules

Before forwarding a request, the proxy must select the final `model` as follows:

1. Read the original request `model`.
2. Apply existing `model_mappings` to get the normal text-request target model.
3. Override that mapped model with `multimodal_model` only when all conditions are true:
   - the request body contains non-text content;
   - the active Provider has `multimodal_switch = true`;
   - the active Provider has a non-empty `multimodal_model`.
4. Otherwise, keep the existing mapped model behavior.

`supports_thinking` remains independent. After model selection, the existing thinking-field compatibility behavior still applies.

### 3.3 Non-Text Detection Scope

Detection must cover common Claude Messages request structures, including nested tool results:

1. `messages`
2. `system`
3. nested `tool_result.content`
4. arrays and objects at arbitrary depth under those fields

The request contains non-text content if any scanned content block matches:

1. `type` is `image`, `input_image`, or `document`.
2. `source.media_type` starts with `image/`, `video/`, or `audio/`.
3. `source.media_type` equals `application/pdf`.

### 3.4 Admin API

Provider create, read, update, list, and duplicate operations must preserve multimodal configuration.

API constraints:

1. Creating a Provider with `multimodal_switch=true` requires a non-empty `multimodal_model`.
2. Updating a Provider without multimodal fields must preserve the existing multimodal configuration.
3. Updating a Provider into a state where `multimodal_switch=true` and `multimodal_model` is empty must return HTTP 400.

### 3.5 Frontend UI

The Provider modal must add:

1. Switch label: `多模态切换`
2. Input label: `多模态模型 ID`
3. Tooltip copy: `请求含图片、PDF 等非文本内容时，自动改用该模型。若当前模型已兼容多模态，可关闭。`

UI behavior:

1. The switch is off by default.
2. Enabling the switch shows the `多模态模型 ID` input.
3. Saving with the switch enabled requires a non-empty model ID.
4. Provider cards show a compact multimodal switch summary for troubleshooting.

## 4. Non-Goals

1. Do not add per-original-model multimodal mappings.
2. Do not strip images, PDFs, audio, or video from requests.
3. Do not infer multimodal behavior from Provider URLs.
4. Do not modify Anthropic Beta headers or other protocol headers.
5. Do not add multi-Provider failover.
6. Do not change behavior for Providers that do not enable this switch.

## 5. Success Criteria

1. Providers whose text model rejects image input can configure `multimodal_switch=true` and a `multimodal_model` to route multimodal requests successfully.
2. Providers that already accept image-bearing requests can keep the switch disabled and retain existing model mappings.
3. Screenshot images embedded in tool results are detected.
4. Pure text requests do not trigger multimodal model switching.
5. Existing SQLite databases upgrade without startup failures.
6. Backend tests, frontend tests, and frontend build pass.
