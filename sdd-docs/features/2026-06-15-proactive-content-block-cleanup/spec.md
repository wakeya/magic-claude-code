# Proactive Content Block Cleanup Spec

Local page: `internal/proxy/handler.go` transformRequest / `internal/proxy/rectifier.go` matchErrorPattern  
Proxy entry: `internal/proxy/handler.go` ServeHTTP  
Reference sources: `~/workspace/open-software/cc-switch/src-tauri/src/proxy/` (body_filter.rs, copilot_optimizer.rs, forwarder.rs), `~/workspace/open-software/kimi-code/packages/kosong/src/providers/`  
Stack: Go 1.26 standard library  
Last updated: 2026-06-15  
Progress: 0 / 5 planned  

## Overall Analysis (Source Analysis)

### Problem

When proxying Claude Code requests to Kimi (kimi-k2.6, kimi-k2.7) via the Anthropic-compatible endpoint, the upstream returns HTTP 400 with:

```json
{"error":{"type":"invalid_request_error","message":"failed to convert tool result content: unsupported content type in ContentBlockParamUnion: tool_reference"}}
```

The existing reactive rectifier (`internal/proxy/rectifier.go`) fails to catch this because `matchErrorPattern` only matches generic phrases like `"invalid request"` / `"invalid_request_error"`. While the error JSON contains `"type":"invalid_request_error"`, the `extractErrorMessage` function extracts the inner `message` field (`"failed to convert tool result content..."`), which does NOT contain any of the matched phrases. The `hasGenericInvalidRequestPhrase` check therefore returns false, the pattern resolves to `PatternNone`, and no cleanup is attempted.

### Root Cause

Claude Code 2.1.x injects a `tool_reference` content block inside `tool_result.content` arrays. This is a client-side metadata block (not part of the Anthropic public API spec) that marks the tool that produced a result. Kimi's Anthropic-compatible endpoint strictly validates content types and rejects `tool_reference`.

### Existing Defense Layers

1. **transformRequest** (`handler.go:508`): Handles model mapping, thinking stripping, and OpenAI format conversion. Does NOT clean non-standard content blocks.

2. **Rectifier** (`rectifier.go`): Reactive 400-recovery via `tryRectify`. Contains `cleanUnknownContentTypes` which uses a whitelist to strip unknown content blocks recursively. But it only triggers when `matchErrorPattern` returns a non-None pattern.

3. **Pattern matching gap**: `hasGenericInvalidRequestPhrase` checks for `"invalid request"`, `"invalid_request_error"`, `"invalid params"`, `"illegal request"`. The extracted message `"failed to convert tool result content: unsupported content type..."` matches none of these.

### Reference Implementations

**cc-switch** (`src-tauri/src/proxy/`):
- `body_filter.rs`: Proactively strips all `_`-prefixed private parameters from the request body before forwarding.
- `copilot_optimizer.rs`: Proactively strips thinking blocks for OpenAI endpoints; converts orphan tool_results to text.
- `thinking_rectifier.rs`: Reactive recovery with broad error pattern matching including `"illegal request"`, `"signature"` patterns, and `"must start with a thinking block"`.
- `forwarder.rs`: Pipeline applies proactive cleaning BEFORE the first attempt, then reactive recovery on 400.

**kimi-code** (`packages/kosong/src/providers/kimi.ts`):
- Does NOT use the Anthropic format for Kimi at all — uses OpenAI Chat Completions format exclusively.
- Think parts are extracted into `reasoning_content` (Moonshot proprietary extension).
- Only supports: `text`, `image_url`, `audio_url`, `video_url`, `think` content types. Unknown types are silently dropped.

### Strategy

**Two-layer fix**:

1. **Proactive cleanup** (Layer 1 — new): For Anthropic-format upstream providers (third-party only), strip non-standard content blocks from `messages[].content` and nested `tool_result.content` BEFORE the first request. This eliminates the 400 round-trip entirely.

2. **Reactive pattern expansion** (Layer 2 — fix): Expand `matchErrorPattern` to recognize `"unsupported content type"` as a generic bad-request trigger, so the existing rectifier catches any remaining edge cases.

### Provider Gating

The proactive cleanup must NOT alter requests sent to providers that are already compatible. Two approaches considered:

- **Option A: Gate by upstream URL** — Only strip when the host is not `api.anthropic.com`. Rejected: this is too broad — GLM-5.2, MiniMax-M3, MiMo-V2.5-Pro already tolerate non-standard content blocks, and stripping may discard future vendor extensions.

- **Option B: New provider capability flag** — Add a `StripUnknownContentBlocks` boolean to the Provider config, defaulting to false. Only enabled for providers with known strict content-type validation (e.g. Kimi).

**Decision**: Option B. Kimi's `tool_reference` 400 is a specific provider's strict-validation issue, not a universal third-party problem. The flag keeps other compatible providers' passthrough behavior unchanged.

### Scope

- **In scope**: Proactive content block cleanup in the request pipeline; reactive error pattern fix.
- **Out of scope**: OpenAI-format thinking history normalization (separate feature); private parameter stripping; cache_control stripping (already handled).

## Development Checklist

- [x] Fix `matchErrorPattern` to recognize `"unsupported content type"` pattern
- [x] Add `StripUnknownContentBlocks` provider capability flag (default false)
- [x] Add proactive content block cleanup in `transformRequest` gated by provider flag
- [x] Write unit tests for all 5 scenarios
- [x] Update existing tests for behavioral changes

## Requirements

### Requirement 1: Reactive Error Pattern Expansion

The rectifier must recognize `"unsupported content type"` in upstream 400 error messages and trigger the existing `cleanUnknownContentTypes` cleanup.

### Requirement 2: Proactive Content Block Cleanup (Opt-in)

When a provider has `strip_unknown_content_blocks: true`, the proxy must strip non-standard content blocks from `messages[].content` (including nested `tool_result.content`) BEFORE the first request attempt. Providers without this flag (default false) continue to pass through content unchanged.

### Requirement 3: Default Passthrough for Anthropic-compatible Providers

Requests forwarded to any Anthropic-compatible provider WITHOUT `strip_unknown_content_blocks` must not be modified proactively. Compatibility issues are handled reactively by the rectifier after a 400 response.

### Requirement 4: OpenAI Format Delegation

Providers using `openai_chat` or `openai_responses` API format do not invoke proactive cleanup. Their requests are handled entirely by the OpenAI transform layer.

## Task Details

### Task 1: Fix Reactive Error Pattern Matching

#### Requirements

**Objective** — Expand `hasGenericInvalidRequestPhrase` or add a new pattern check so that `"unsupported content type"` triggers `PatternGenericBadRequest`.

**Outcomes** — The rectifier's reactive cleanup path works for Kimi's specific error message.

**Evidence** — Unit test: feed the actual Kimi error JSON, assert `matchErrorPattern` returns `PatternGenericBadRequest`.

**Constraints** — Must not over-match: only match messages that clearly indicate a content type or conversion error.

**Edge Cases** — Message variations: `"unsupported content type in ContentBlockParamUnion"`, `"unsupported content type"`, `"unknown content type"`.

**Verification** — `go test ./internal/proxy/... -run TestMatchErrorPattern -v`

#### Plan

1. Add `"unsupported content type"` and `"unknown content type"` to the generic bad-request phrase list in `rectifier.go`.
2. Add a unit test with the actual Kimi error message.

#### Verification

Run `go test ./internal/proxy/... -run TestMatchErrorPattern -v` and confirm the new case passes.

### Task 2: Add Proactive Content Block Cleanup

#### Requirements

**Objective** — In `transformRequest`, when `provider.StripUnknownContentBlocks == true` AND `apiFormat == anthropic`, strip non-standard content blocks from messages before the first request.

**Outcomes** — Zero-latency handling of `tool_reference` and other non-standard content blocks for providers that need it (e.g. Kimi).

**Evidence** — Unit test: request with `tool_reference` in `tool_result.content` is cleaned to only standard types after `transformRequest`.

**Constraints** — Only apply when `providerAPIFormat(provider) == config.APIFormatAnthropic && provider.StripUnknownContentBlocks`. OpenAI Chat/Responses formats delegate to the transform layer. Must use the same whitelist as `knownContentTypes`.

**Edge Cases** — Empty content arrays after cleanup; deeply nested `tool_result.content.tool_result.content`; non-array content (string).

**Verification** — `go test ./internal/proxy/... -run TestProactiveClean -v`

#### Plan

1. Add `proactiveCleanUnknownContentTypes(req map[string]any)` that reuses the existing `filterContentBlocks` logic.
2. Call it in `transformRequest` when `providerAPIFormat == anthropic && provider.StripUnknownContentBlocks`.
3. Add `StripUnknownContentBlocks` to Provider config, admin API, SQLite store, and frontend.

#### Verification

Run unit tests; confirm strip=false preserves all content blocks (passthrough); strip=true cleans unknown types.

### Task 3: Test Coverage

#### Requirements

**Objective** — Comprehensive test coverage for both the reactive pattern fix and the proactive cleanup.

**Outcomes** — All new code paths are covered by tests.

**Evidence** — `go test -cover ./internal/proxy/...` shows no regression in coverage.

**Verification** — `go test ./internal/proxy/... -v`

## 2026-07-21 Correction

The kimi upstream behavior this spec relied on has changed. A controlled live probe on 2026-07-21 (`sdd-docs/features/2026-07-21-preserve-tool-reference/`) shows all three kimi Anthropic-compatible endpoints (moonshot k2.6, coding k2.7, coding k3) now **accept** `tool_reference` as a content type when it references a tool present in the request's `tools` array; the `"unsupported content type"` 400 no longer reproduces. The 400s still observed come from `tool_reference` pointing at an undefined tool, not from the type itself.

Consequence: proactive stripping of `tool_reference` is now pure side-effect — it removes the Claude Code deferred-tool load marker the model relies on. `filterContentBlocks` was parameterized (`preserveToolReference`): proactive cleanup keeps `tool_reference`; reactive cleanup (`tryRectify`) still strips it to recover the undefined-tool-reference 400. Reactive error matching was extended so current kimi 400 strings (`"Invalid request Error"`, `"Tool reference ... not found"`) still trigger cleanup. The `StripUnknownContentBlocks` flag is now a no-op in practice and will be deprecated in a follow-up. See `2026-07-21-preserve-tool-reference/` for the full analysis and the live-probe matrix.
