# OpenAI-Compatible API Format Support Spec

Local page: Provider management / Claude Code proxy entry  
Proxy entry: `/v1/messages`  
Reference sources: Agnes Claude CLI integration guide, CC-Switch v3.16.x implementation  
Stack: Go 1.26 + SQLite + Vue 3 + embedded frontend  
Last updated: 2026-06-12  
Progress: 9 / 9 implemented and automatically verified

## Overall Analysis (Source Analysis)

### Agnes Integration Document Analysis

The Agnes document is centered on letting Claude CLI connect to an OpenAI-Compatible API Gateway through CC-Switch. It explicitly requires selecting `OpenAI Chat Completions` as the API format for a Claude Provider in CC-Switch, and configuring the request URL as an OpenAI-style base URL such as `https://apihub.agnes-ai.com/v1`.

The recommended Claude CLI integration flow is:

1. Select `Claude CLI` in the CC-Switch toolbar.
2. Add a Provider, choose `Claude Provider`, then choose `Custom Provider`.
3. Set `Request URL` to the OpenAI-Compatible base URL.
4. Set `API Format` to `OpenAI Chat Completions`.
5. Configure model mappings, for example mapping Claude Code Sonnet / Opus / Haiku to Agnes models.
6. Enable Local Route and the Claude route switch.

The Agnes document also recommends adding the following custom parameters:

```json
{
  "allowed_openai_params": [
    "thinking",
    "context_management"
  ],
  "litellm_settings": {
    "drop_params": true
  }
}
```

This configuration is not a native Claude protocol feature. It is an OpenAI-Compatible gateway compatibility policy:

1. Allow selected OpenAI extension parameters to pass through.
2. Drop unknown model-incompatible parameters automatically.
3. Avoid timeout retries caused by non-chat endpoints or unknown fields when Claude CLI calls an OpenAI-Compatible API through the proxy.
4. Improve Claude Code startup speed by avoiding timeout retry delays on telemetry, statistics, and other non-model chat endpoints.

### CC-Switch Source Analysis

CC-Switch uses `apiFormat` on Provider records to declare the upstream API protocol format explicitly:

```ts
apiFormat?: "anthropic" | "openai_chat" | "openai_responses" | "gemini_native"
```

This design decouples the local Claude Code entry protocol from the actual upstream provider protocol. Claude Code still sends Anthropic `/v1/messages` requests to the local proxy. The proxy decides whether to transform the request, rewrite the upstream endpoint, and transform the response or SSE stream according to the Provider `apiFormat`.

The key CC-Switch flow is:

1. Claude Code sends an Anthropic Messages request.
2. The local proxy reads the active Provider `apiFormat`.
3. When `apiFormat=anthropic`, the request and response are mostly passed through as Anthropic.
4. When `apiFormat=openai_chat`, the Anthropic Messages request is transformed to OpenAI Chat Completions and the upstream endpoint is rewritten to `/v1/chat/completions`.
5. When `apiFormat=openai_responses`, the Anthropic Messages request is transformed to OpenAI Responses and the upstream endpoint is rewritten to `/v1/responses`.
6. The upstream OpenAI non-streaming response or SSE stream is transformed back to Anthropic Messages format expected by Claude Code.

Source implementation references:

| Capability | CC-Switch reference | Notes |
| --- | --- | --- |
| Provider API format declaration | `cc-switch/src/types.ts` | `apiFormat?: "anthropic" | "openai_chat" | "openai_responses" | "gemini_native"` |
| Claude Provider format detection | `cc-switch/src-tauri/src/proxy/providers/claude.rs` | Reads the format and determines whether a transform is required |
| Endpoint rewrite | `cc-switch/src-tauri/src/proxy/forwarder.rs` | `openai_chat` -> `/v1/chat/completions`, `openai_responses` -> `/v1/responses` |
| Anthropic -> OpenAI Chat | `cc-switch/src-tauri/src/proxy/providers/transform.rs` | Transforms system/messages/tools/tool_choice/thinking/usage-related fields |
| OpenAI Chat -> Anthropic | `cc-switch/src-tauri/src/proxy/providers/transform.rs` | Transforms content/reasoning/tool_calls/finish_reason/usage |
| OpenAI SSE -> Anthropic SSE | `cc-switch/src-tauri/src/proxy/providers/streaming.rs` | Emits `message_start`, `content_block_delta`, `message_delta`, `message_stop`, and related events |

### Current Project State

The current project already has the Claude Code local proxy, provider configuration, model mappings, multimodal switching, thinking removal, and usage statistics. However, Provider records do not yet express the upstream API protocol format.

Current state:

| Module | Current capability | Gap |
| --- | --- | --- |
| `internal/config/provider.go` | Stores Provider base fields, model mappings, thinking, and multimodal settings | Missing `APIFormat` and OpenAI-Compatible custom parameters |
| `internal/admin/provider_handler.go` | Supports Provider CRUD | Request/response structs do not include the new fields |
| `internal/frontend/src/components/ProviderModal.vue` | Provider edit form | Missing API format selection and OpenAI-Compatible parameter input |
| `internal/frontend/src/composables/useApi.ts` | Frontend Provider type | Missing new field types |
| `internal/proxy/handler.go` | Claude request forwarding, model mapping, thinking removal, SSE heartbeat, usage observation | Missing Anthropic/OpenAI request transform, response transform, and endpoint rewrite |

The proxy currently computes the upstream URL with `strings.TrimSuffix(backendURL, "/") + r.URL.Path`. When Claude Code requests `/v1/messages`, an OpenAI-Compatible upstream base URL would incorrectly receive `/v1/messages`. Therefore `apiFormat=openai_chat` and `apiFormat=openai_responses` must take over endpoint selection.

### Protocol Flow Comparison

| Provider format | Claude Code local entry | Upstream endpoint | Request body | Response body | SSE |
| --- | --- | --- | --- | --- | --- |
| `anthropic` | `/v1/messages` | `/v1/messages` or provider-native path | Anthropic Messages | Anthropic Messages | Anthropic SSE |
| `openai_chat` | `/v1/messages` | `/v1/chat/completions` | OpenAI Chat Completions | Converted back to Anthropic Messages | Converted back to Anthropic SSE |
| `openai_responses` | `/v1/messages` | `/v1/responses` | OpenAI Responses | Converted back to Anthropic Messages | Converted back to Anthropic SSE |

The external contract for this feature is that Claude Code does not need to know the upstream protocol changed. Whether the upstream provider is native Anthropic, OpenAI Chat Completions, or OpenAI Responses, the local protocol visible to Claude Code remains Anthropic Messages.

### Risk Summary

1. `apiFormat` cannot be only a frontend switch. It must flow through persistence, Admin API, proxy forwarding, request transforms, response transforms, and tests.
2. Streaming conversion is the key compatibility risk for Claude Code. Converting only non-streaming responses is not enough for real usage.
3. Tool calls and thinking/reasoning fields are high-risk fields and need unit test coverage.
4. Usage statistics rely on Anthropic usage observation. OpenAI responses must preserve or normalize usage after conversion back to Anthropic.
5. `gemini_native` is only reserved as a type concept and is out of scope for this phase.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Done | Add `api_format` and OpenAI-Compatible parameter fields to Provider | Config model, defaults, validation | Provider unit tests |
| 2 | Done | Add new fields to Admin API | Consistent create/update/list/get echo | API handler tests |
| 3 | Done | Add API format controls to provider edit UI | Select control, JSON parameter editor, validation errors | Frontend build and manual form check |
| 4 | Done | Implement Anthropic -> OpenAI Chat request transform | Independent transform module | Request transform unit tests |
| 5 | Done | Implement OpenAI Chat -> Anthropic response and SSE transform | Non-streaming and streaming transforms | Response/SSE unit tests |
| 6 | Done | Implement Anthropic -> OpenAI Responses request transform | Independent transform module | Request transform unit tests |
| 7 | Done | Implement OpenAI Responses -> Anthropic response and SSE transform | Non-streaming and streaming transforms | Response/SSE unit tests |
| 8 | Done | Integrate API format dispatch and endpoint rewrite in proxy entry | `handler.go` dispatch by `api_format` | Proxy integration tests |
| 9 | Done | Document and manually verify Agnes/OpenAI-Compatible behavior | Verification record and usage notes | Mock upstream full-chain verification |

## Requirements

### Deliverables

1. Provider configuration supports:
   - `api_format`: `"anthropic" | "openai_chat" | "openai_responses"`.
   - OpenAI-Compatible custom parameters saved and passed through as any JSON object. Agnes `allowed_openai_params` and `litellm_settings.drop_params` are recommended templates, not special structured fields.
2. Admin API supports the new fields for create, update, get, and list.
3. The provider edit UI supports API format selection and OpenAI-Compatible JSON parameter editing.
4. The proxy chooses the upstream endpoint according to Provider `api_format`.
5. The proxy supports bidirectional conversion between Anthropic Messages and OpenAI Chat Completions.
6. The proxy supports bidirectional conversion between Anthropic Messages and OpenAI Responses.
7. Streaming responses are converted to Anthropic SSE events that Claude Code can consume.
8. Usage statistics continue to work for OpenAI-Compatible responses.
9. Unit tests, integration tests, and manual verification records are included.

### Directory Structure

Suggested files and locations. The final implementation may adapt to existing code organization, but transform logic must remain testable and reusable.

```text
internal/
  config/
    provider.go
  admin/
    provider_handler.go
  proxy/
    handler.go
    transform/
      anthropic_openai_chat.go
      anthropic_openai_chat_test.go
      anthropic_openai_responses.go
      anthropic_openai_responses_test.go
      sse.go
      sse_test.go
  frontend/
    src/
      components/
        ProviderModal.vue
      composables/
        useApi.ts
```

### Data Model

Provider should add:

```go
type APIFormat string

const (
    APIFormatAnthropic       APIFormat = "anthropic"
    APIFormatOpenAIChat      APIFormat = "openai_chat"
    APIFormatOpenAIResponses APIFormat = "openai_responses"
)

type Provider struct {
    // existing fields...
    APIFormat            APIFormat      `json:"api_format"`
    OpenAIExtraParams    map[string]any `json:"openai_extra_params,omitempty"`
    ClaudeCodeCompatHint *bool          `json:"claude_code_compat_hint,omitempty"`
}
```

Default rules:

1. Existing configurations without `api_format` default to `anthropic`.
2. New Providers default to `anthropic` when no format is selected.
3. OpenAI-Compatible custom parameters are shown and used only for `openai_chat` and `openai_responses`.
4. Unknown `api_format` values are rejected, including `gemini_native` in this phase.

### Protocol Path Rules

| `api_format` | Upstream URL calculation |
| --- | --- |
| `anthropic` | `trim(api_url) + original request path`, preserving current behavior |
| `openai_chat` | Prefer `trim(api_url) + /chat/completions`; if the user already entered a full `/chat/completions` endpoint, use that URL directly |
| `openai_responses` | Prefer `trim(api_url) + /responses`; if the user already entered a full `/responses` endpoint, use that URL directly |

The frontend should prompt users to enter a base URL, for example `https://example.com/v1`. The implementation must still support full endpoints such as `https://example.com/v1/chat/completions` or `https://example.com/v1/responses`.

### Constraints

1. The local proxy protocol used by Claude Code must remain Anthropic Messages.
2. `anthropic` must preserve current behavior to avoid breaking existing providers.
3. OpenAI-Compatible transform logic must be independent from the HTTP handler so it can be unit tested.
4. `gemini_native` is out of scope for this phase and must not appear as a selectable UI option.
5. Existing model mapping, multimodal switching, thinking removal, and usage statistics must not be removed while adding OpenAI compatibility.
6. Custom parameters must be validated as JSON objects before being saved.
7. Auth headers must be unified to `Authorization: Bearer <token>` regardless of the original header type (`X-Api-Key` or `Authorization`).
8. Unknown-field dropping policies apply only to OpenAI-Compatible request construction and must not affect native Anthropic providers.
9. Anthropic-specific request headers and query parameters must be stripped before forwarding to OpenAI-Compatible upstreams. OpenAI-specific response headers must be stripped before returning to Claude Code. The main request path and the retry path (`tryRectify`) must share the same header filtering logic.
10. SSE streaming transforms must use an explicit `bufio.Scanner` buffer of at least 128KB to avoid `bufio.ErrTooLong` on large SSE data chunks.
11. SSRF protection (`isInternalIP`) must perform actual DNS resolution and use `net.IP` standard library methods (`IsPrivate`, `IsLoopback`, `IsLinkLocalUnicast`, `IsLinkLocalMulticast`, `IsUnspecified`) to cover both IPv4 and IPv6. Unresolvable hostnames must be blocked.
12. All Admin API endpoints that return Provider data (including duplicate) must mask the API token via `maskToken()`. No endpoint may return the plaintext token except `/reveal-token`.

### Edge Cases

1. Existing Provider configuration has no `api_format`.
2. OpenAI-Compatible upstream returns a non-SSE error body.
3. OpenAI Chat streaming response sends role before content delta.
4. OpenAI Chat returns chunked `tool_calls`.
5. OpenAI Responses returns mixed output item types.
6. Upstream usage fields are missing, use different names, or omit cache token fields.
7. Claude request contains `thinking`, but the target OpenAI-Compatible model does not support it.
8. Custom parameter JSON is empty, invalid, or not a top-level object.
9. User accidentally configures an OpenAI-Compatible Provider as `anthropic`.
10. Anthropic-specific headers (`Anthropic-Version`, `Anthropic-Beta`) and query parameters (`beta=true`) leak to OpenAI-Compatible upstream, triggering 403 from upstream WAF/CDN.
11. OpenAI-Compatible upstream returns OpenAI-specific response headers (`Openai-*`, `X-Ratelimit-*`) that confuse Claude Code.
12. SSE data chunks exceed `bufio.Scanner` default 64KB line limit, causing `bufio.ErrTooLong` and stream interruption.
13. Provider duplicate endpoint returns plaintext `APIToken` in response, leaking the secret to the frontend.
14. SSRF protection uses hostname string matching only, allowing DNS rebinding attacks and missing IPv6 private ranges.

### Non-Goals

1. Do not implement `gemini_native` in this phase.
2. Do not implement OpenAI model list fetching in this phase.
3. Do not change the Claude Code client installation flow.
4. Do not implement a cross-provider parameter template marketplace.
5. Do not change the existing GitHub/GitLab Release build automation.

### Review Decisions

1. OpenAI-Compatible custom parameters only need to support arbitrary JSON objects. The frontend does not need dedicated structured controls for `allowed_openai_params` and `litellm_settings.drop_params`.
2. `api_url` must support full endpoints such as `https://example.com/v1/chat/completions`, while helper text still recommends a base URL such as `https://example.com/v1`.
3. `openai_responses` must fully support tools in the first version, not only text, thinking/reasoning, and usage.

## Task Details

### Task 1: Configuration Model and Compatibility Defaults

#### Requirements

**Objective** - Add an explicit upstream API format declaration to Provider so the system can distinguish native Anthropic, OpenAI Chat Completions, and OpenAI Responses.

**Outcomes** - Provider supports `api_format` with `anthropic` as the default; Provider supports OpenAI-Compatible custom parameters; existing configurations continue to load correctly.

**Evidence** - Unit tests cover empty-field defaults, valid enum values, invalid enum values, and old configuration compatibility; the code uses constants or typed values instead of scattered strings.

**Constraints** - Do not expose `gemini_native` as a usable path; do not break the existing Provider JSON shape; existing Providers must not require a migration script to load.

**Edge Cases** - Missing config field, empty string format, unknown format, empty custom parameter object, invalid custom parameter type.

**Verification** - Run Provider configuration tests and confirm missing `api_format` defaults to `anthropic`, while unknown values are rejected or produce a clear error.

#### Plan

1. Define `APIFormat` and the three legal values in `internal/config/provider.go`.
2. Add `APIFormat` and `OpenAIExtraParams` fields to Provider.
3. Set defaults when Providers are created, updated, or loaded.
4. Add a shared format validation function.
5. Add serialization and deserialization tests.

#### Verification

- [x] Existing Provider JSON without `api_format` loads as `anthropic`.
- [x] `openai_chat` and `openai_responses` can be saved and loaded.
- [x] Unknown values cannot silently enter the proxy execution path.
- [x] Empty `OpenAIExtraParams` does not affect native Anthropic providers.

### Task 2: Admin API and Persistence Fields

#### Requirements

**Objective** - Let Provider management APIs fully support API format and OpenAI-Compatible parameters.

**Outcomes** - create, update, list, and get request/response paths include `api_format`; OpenAI-Compatible parameters can be saved, updated, cleared, and echoed back.

**Evidence** - Admin API handler tests cover create, update, get, invalid JSON, and invalid enum values; frontend types match backend JSON fields.

**Constraints** - Existing field semantics must not change; update behavior for omitted new fields must be explicit to avoid accidental clearing.

**Edge Cases** - PATCH/PUT semantic differences, empty string format, parameter JSON as array, parameter JSON as string, old frontend payload without new fields.

**Verification** - Use handler tests or local API requests to verify the full Provider CRUD flow.

#### Plan

1. Update Admin Provider request/response structs.
2. Validate `api_format` in create and update paths.
3. Validate OpenAI-Compatible parameters as top-level JSON objects.
4. Keep list/get response fields consistent.
5. Add API handler tests.

#### Verification

- [x] A new Provider can be saved with `api_format=openai_chat`.
- [x] A Provider can be updated from `openai_chat` back to `anthropic`.
- [x] Invalid `api_format` returns a clear error.
- [x] Non-object JSON parameters return a clear error.
- [x] Provider duplicate endpoint returns `api_token_mask` instead of plaintext token.

### Task 3: Frontend Provider Edit UI

#### Requirements

**Objective** - Add API format selection to the provider edit form so users can configure OpenAI-Compatible upstream providers.

**Outcomes** - Provider form includes API format selection; it supports `Anthropic`, `OpenAI Chat Completions`, and `OpenAI Responses`; OpenAI-Compatible parameters can be edited and validated.

**Evidence** - Frontend build passes; manual checks show create/edit Provider displays, saves, and reloads the fields consistently; invalid JSON shows a clear error.

**Constraints** - Do not show `gemini_native`; do not expose OpenAI parameter editing in a confusing way for `anthropic` mode; keep the existing form layout and interaction style.

**Edge Cases** - Whether existing parameters are preserved when switching API format; empty JSON editor; form state after save failure; old Provider opened without `api_format`.

**Verification** - Run the frontend build and manually create/edit Providers in all three formats.

#### Plan

1. Update the Provider type in `useApi.ts`.
2. Add an API format select control to `ProviderModal.vue`.
3. Show the custom parameter JSON area only for OpenAI-Compatible formats.
4. Provide a default parameter template compatible with the Agnes recommendation.
5. Validate the JSON top-level object before saving.

#### Verification

- [x] New Provider defaults to `Anthropic`.
- [x] Selecting `OpenAI Chat Completions` shows the parameter editor.
- [x] Invalid JSON blocks submission.
- [x] Existing Provider edit correctly displays `api_format` and parameters.

### Task 4: OpenAI Chat Completions Request Transform

#### Requirements

**Objective** - Convert Claude Code Anthropic Messages requests to OpenAI Chat Completions requests.

**Outcomes** - Support conversion for system, messages, text, multimodal image blocks, tools, tool_choice, stream, max_tokens, temperature, top_p, stop_sequences, and thinking/reasoning-related fields.

**Evidence** - Transform unit tests use representative Anthropic request fixtures and assert the expected OpenAI Chat request structure.

**Constraints** - Transform logic must be independently testable; the HTTP handler must not manually assemble JSON inline; existing model mapping still applies.

**Edge Cases** - system as string or block array; content as string or block array; interleaved tool_use and tool_result; unsupported image source types; thinking field present but unsupported upstream.

**Verification** - Unit tests cover plain text, tool calls, multimodal content, thinking, and custom parameter merging.

#### Plan

1. Add an independent transform package or file.
2. Define minimal Anthropic and OpenAI Chat request structs, or use `map[string]any` with strict validation.
3. Implement system/messages/content block conversion.
4. Implement tools and tool_choice conversion.
5. Merge `OpenAIExtraParams` and apply `allowed_openai_params` / `drop_params` compatibility behavior.
6. Add request transform tests.

#### Verification

- [x] Text requests convert to a `messages` array.
- [x] `tools` convert to OpenAI `tools: [{type:function,...}]`.
- [x] `tool_result` converts to a role `tool` message.
- [x] `stream=true` is preserved and `stream_options.include_usage=true` is added when needed.
- [x] Agnes recommended parameters can be merged into the request body.

### Task 5: OpenAI Chat Completions Response and SSE Transform

#### Requirements

**Objective** - Convert OpenAI Chat Completions non-streaming responses and SSE streams to Anthropic Messages responses expected by Claude Code.

**Outcomes** - Non-streaming responses produce Anthropic `message`; streaming responses produce Anthropic SSE events; content, reasoning, tool_calls, finish_reason, and usage are supported.

**Evidence** - Response transform tests cover non-streaming and SSE; Claude Code can consume the converted streaming events.

**Constraints** - Do not pass OpenAI SSE through to Claude Code unchanged; error responses should preserve status code and readable error body where possible; usage must stay compatible with the current usage observer.

**Edge Cases** - Empty SSE chunks, `[DONE]`, role-only delta, chunked tool_calls arguments, reasoning_content chunks, usage emitted only at the end.

**Verification** - Use OpenAI Chat SSE fixtures and assert output contains `message_start`, `content_block_start`, `content_block_delta`, `message_delta`, and `message_stop`.

#### Plan

1. Implement non-streaming OpenAI Chat response to Anthropic message conversion.
2. Implement an OpenAI Chat SSE parser.
3. Map delta events to Anthropic SSE content block events.
4. Accumulate tool_calls argument chunks and emit Anthropic `tool_use`.
5. Map usage to Anthropic usage fields.
6. Add response and SSE tests.

#### Verification

- [x] Non-streaming text response converts to Anthropic `content: [{type:"text"}]`.
- [x] OpenAI `tool_calls` convert to Anthropic `tool_use`.
- [x] SSE output order matches Anthropic Messages streaming expectations.
- [x] usage can be read by the existing observer or by the converted Anthropic usage.

### Task 6: OpenAI Responses Request Transform

#### Requirements

**Objective** - Support converting Anthropic Messages requests to OpenAI Responses API requests for providers that use the newer Responses format.

**Outcomes** - When Provider uses `openai_responses`, upstream requests go to `/v1/responses`, and the request body expresses input, tools, streaming, and reasoning parameters using Responses API structure.

**Evidence** - Request transform unit tests cover text, multi-turn messages, tools, stream, and reasoning parameters.

**Constraints** - Do not affect `openai_chat` conversion; the first version must fully support tools; unsupported Anthropic blocks must produce clear errors or explicit degradation.

**Edge Cases** - Responses input items do not map one-to-one with Anthropic content blocks; tool result representation differs; reasoning parameter names differ; multimodal image formats differ.

**Verification** - Fixture input converts to the expected Responses API structure and can be accepted by a mock upstream.

#### Plan

1. Add a Responses request transform function.
2. Convert Anthropic system/messages to Responses `input`.
3. Convert tools, tool_choice, stream, max_output_tokens or equivalent fields.
4. Handle thinking/reasoning parameter mapping.
5. Add Responses request transform tests.

#### Verification

- [x] `api_format=openai_responses` uses `/v1/responses`.
- [x] Text messages convert to Responses `input`.
- [x] tools convert to Responses tool definitions.
- [x] stream parameter is passed correctly.

### Task 7: OpenAI Responses Response and SSE Transform

#### Requirements

**Objective** - Convert OpenAI Responses API responses and event streams to Anthropic Messages responses that Claude Code can consume.

**Outcomes** - Responses non-streaming output converts to Anthropic content; Responses streaming events convert to Anthropic SSE; usage and stop reason are preserved.

**Evidence** - Unit tests cover Responses completed, output_text, reasoning, tool_call, usage, and other typical structures.

**Constraints** - Do not pass raw Responses events through to Claude Code; unknown events may be ignored or logged, but must not interrupt normal text output.

**Edge Cases** - Mixed Responses output item types; chunked tool call arguments; event order differs from Anthropic SSE order; missing usage.

**Verification** - Use Responses fixtures to assert the converted Anthropic response and SSE event sequence.

#### Plan

1. Implement Responses non-streaming response parsing.
2. Implement a Responses streaming event parser.
3. Map output text/reasoning/tool_call to Anthropic content blocks.
4. Map completion status to Anthropic stop_reason.
5. Map usage to Anthropic usage.
6. Add tests.

#### Verification

- [x] Responses text output converts to Anthropic text block.
- [x] Responses tool call converts to Anthropic tool_use block.
- [x] Converted Responses stream has stable SSE event order.
- [x] Unknown events do not break already generated content.

### Task 8: Proxy Entry Integration, Endpoint Rewrite, and Usage Statistics

#### Requirements

**Objective** - Dispatch request transform, upstream endpoint, response transform, and usage statistics in the existing proxy entry according to Provider `api_format`.

**Outcomes** - `anthropic` keeps current behavior; `openai_chat` and `openai_responses` rewrite endpoints automatically and perform bidirectional protocol conversion; usage statistics continue to work.

**Evidence** - Integration tests with mock upstream verify actual request paths, request bodies, response bodies, and SSE; existing Anthropic proxy tests do not regress.

**Constraints** - Do not change the local Claude Code entry; do not break heartbeat; error responses should remain diagnosable; transform failures return clear errors.

**Edge Cases** - Upstream returns 4xx/5xx; upstream disconnects during SSE; transform sees unknown content block; Provider is disabled or missing a token.

**Verification** - Run proxy handler tests and manual curl/mock-upstream checks for all three formats.

#### Plan

1. Read active Provider `APIFormat` in `handler.go`.
2. Build the upstream URL according to the format.
3. Call the corresponding request transform before sending upstream.
4. After receiving the upstream response, decide whether to pass through or transform by format.
5. Ensure SSE heartbeat works with the converted Anthropic SSE stream.
6. Keep the usage observer able to read final Anthropic usage.
7. Add proxy integration tests.

#### Verification

- [x] `anthropic` requests still forward to the original `/v1/messages`.
- [x] `openai_chat` requests forward to `/v1/chat/completions`.
- [x] `openai_responses` requests forward to `/v1/responses`.
- [x] Model mapping works under all three formats.
- [x] OpenAI-Compatible responses update usage statistics.
- [x] Anthropic-specific headers (`Anthropic-Version`, `Anthropic-Beta`) are not forwarded to OpenAI-Compatible upstreams.
- [x] Anthropic-specific query parameters (`beta=true`) are not appended to OpenAI-Compatible upstream URLs.
- [x] OpenAI-specific response headers are not forwarded to Claude Code.
- [x] The retry path (`tryRectify`) applies the same header filtering as the main request path.
- [x] SSE streaming transforms use a 128KB scanner buffer and do not fail on large data chunks.
- [x] SSRF protection resolves DNS and checks both IPv4 and IPv6 private/reserved ranges.

### Task 9: Agnes/OpenAI-Compatible Manual Verification and Documentation

#### Requirements

**Objective** - Verify that a real OpenAI-Compatible provider configuration can be used by Claude Code, and record usage in project documentation or the spec verification notes.

**Outcomes** - Complete one Claude Code request with an Agnes-style configuration; record Provider configuration, model mappings, custom parameters, request result, and known limits.

**Evidence** - Manual verification log includes local proxy startup command, key Provider settings, Claude Code request result, upstream response path, and usage statistics result.

**Constraints** - Do not leak API keys in documentation; use mock upstream as the minimum verification when the real provider is unavailable; document the `openai_responses` verification coverage.

**Edge Cases** - Network unreachable, invalid API key, wrong model name, provider does not support tools/thinking, upstream returns non-standard OpenAI-Compatible fields.

**Verification** - Complete at least one real or mock full-chain `openai_chat` verification; complete an `openai_responses` mock full-chain verification that covers tools.

#### Plan

1. Prepare an Agnes-style Provider configuration example.
2. Configure model mappings, for example mapping Sonnet/Opus/Haiku to the same OpenAI-Compatible model.
3. Add recommended custom parameters:
   - `allowed_openai_params`: `["thinking", "context_management"]`
   - `litellm_settings.drop_params`: `true`
4. Start the local proxy and send a request through Claude Code or curl.
5. Check response, SSE, usage, and logs.
6. Add verification findings to the documentation.

#### Verification

- [x] Agnes-style `openai_chat` Provider can be saved and enabled.
- [x] Claude Code request can route through the local proxy to the OpenAI-Compatible endpoint.
- [x] Full `/chat/completions` endpoint input still forwards correctly.
- [x] `openai_responses` mock verification covers tool request and response conversion.
- [x] Non-model chat endpoints do not cause long timeout retries because of OpenAI-Compatible configuration.
- [x] Documentation states that this capability can reduce Claude Code startup delay from telemetry endpoint timeout retries.
- [x] Verification records do not contain sensitive tokens.
