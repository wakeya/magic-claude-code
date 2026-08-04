# DeepSeek Cache Usage Parsing Fix Spec

Local page: `internal/proxy/handler.go` (upstream response conversion entry), `internal/proxy/transform/openai_chat.go`, `internal/proxy/transform/openai_responses.go`
Proxy entry: `internal/proxy/handler.go` `ServeHTTP`, upstream SSE/non-stream response conversion paths
Reference sources: [DeepSeek KV Cache](https://api-docs.deepseek.com/guides/kv_cache), [DeepSeek Chat Completion API](https://api-docs.deepseek.com/zh-cn/api/create-chat-completion/), pi `packages/ai/src/api/openai-completions.ts`
Stack: Go 1.26 standard library and the existing `internal/proxy/transform` test suite
Last updated: 2026-08-04
Progress: 2 / 2 planned tasks (implementation complete, tests passing, specs updated)
Status: implemented

## Overall Analysis (Source Analysis)

### Problem

DeepSeek's OpenAI-compatible API reports cached and uncached input tokens as `prompt_cache_hit_tokens` and `prompt_cache_miss_tokens`. MCC converts OpenAI Chat Completions or Responses responses into Anthropic responses, after which Claude Code and MCC usage statistics depend on the converted `cache_read_input_tokens` and `input_tokens` fields.

The current Chat Completions converter `openAIUsageToAnthropic` only reads `prompt_tokens_details.cached_tokens` and does not read DeepSeek's `prompt_cache_hit_tokens`. The current Responses converter `responsesUsageToAnthropic` only copies `cached_tokens` and likewise has no DeepSeek fallback. Consequently, an upstream response can have a real cache hit while MCC returns `cache_read_input_tokens = 0` and counts the entire prompt as uncached input.

pi already supports DeepSeek's `prompt_cache_hit_tokens`, which explains why pi can show a high cache ratio while the same upstream response appears to have a low ratio after MCC conversion. This is a usage mapping defect; it is not proof that the upstream KV cache failed.

### Existing Code Evidence

- `openAIUsageToAnthropic` derives `cache_read_input_tokens` from `prompt_tokens_details.cached_tokens`, then subtracts cache read and cache creation tokens from `prompt_tokens`; it has no `prompt_cache_hit_tokens` fallback.
- `responsesUsageToAnthropic` copies only `input_tokens`, `output_tokens`, and `cached_tokens`; it has no DeepSeek hit-token fallback and does not subtract cached input from uncached input.
- Existing Chat non-stream and SSE tests cover only `prompt_tokens_details.cached_tokens`; there are no tests for DeepSeek hit/miss fields.
- This fix does not alter request protocol conversion and does not claim that different endpoints, model routes, or request prefixes share caches. It only exposes cache usage already returned by the upstream provider.

### Target Behavior

Use the following cache-read precedence without double counting:

1. For Chat Completions, use `prompt_tokens_details.cached_tokens` first; if it is missing or not parseable, use `prompt_cache_hit_tokens`.
2. For Responses, preserve the existing `cached_tokens` representation; if missing, use `prompt_cache_hit_tokens`, while also accepting the same `prompt_tokens_details.cached_tokens` nested representation as Chat.
3. An explicitly present numeric value of `0` is valid and must not be replaced by a fallback field.
4. Compute uncached input as total input minus cache read minus cache creation. If total input is absent but hit and miss fields exist, use `prompt_cache_miss_tokens` as uncached input.
5. Continue emitting Anthropic usage: `cache_read_input_tokens` is the hit count, `input_tokens` is uncached input, and `output_tokens` keeps the existing mapping.

### Scope

- **In scope**: OpenAI Chat Completions and OpenAI Responses non-stream responses and SSE usage events; DeepSeek hit/miss fields; cache-field precedence; input-token arithmetic; synthetic unit tests; regression verification.
- **Out of scope**: Anthropic-to-OpenAI request conversion, preserving `cache_control`, adding `prompt_cache_key`, session affinity, cache endpoint routing, model mapping, cache TTL, database schema, or frontend cache-ratio algorithms.

## Related Issues / Follow-up Features

### Real cache degradation caused by protocol conversion (independent follow-up, NOT in this branch)

- MCC converts Anthropic Messages requests into OpenAI Chat Completions / Responses requests and does not preserve Anthropic `cache_control` markers, while DeepSeek's KV cache is keyed on the exact prompt prefix. After this parsing fix, pi and MCC agree on the usage the upstream reports; if a low hit ratio still persists, it is real upstream cache degradation caused by the request-protocol conversion (missing `cache_control`, altered prompt structure, no session affinity), not a usage-parsing defect.
- Fixing that degradation — preserving `cache_control`, adding a prompt cache key, session affinity, or cache-aware endpoint routing — is an independent follow-up feature with its own design and is explicitly NOT implemented in this branch (`fix/deepseek-cache-usage-parsing`). This branch only fixes how the cache usage already returned by the upstream is parsed and converted.

## Development Checklist

- [x] Add `prompt_cache_hit_tokens` fallback tests for Chat non-stream and SSE responses.
- [x] Add `prompt_cache_hit_tokens` fallback tests for Responses non-stream and SSE responses.
- [x] Cover precedence when `cached_tokens` and `prompt_cache_hit_tokens` coexist, including an explicit zero.
- [x] Cover `prompt_cache_miss_tokens` as the uncached-input fallback and preserve no-cache behavior.
- [x] Run `go test ./internal/proxy/transform -count=1`.
- [x] Run `go test ./...`.
- [x] Update progress, checklist, and verification evidence in both specs after implementation.

## Requirements

### Requirement 1: Recognize DeepSeek cache hits in Chat Completions

`openAIUsageToAnthropic` MUST read `prompt_cache_hit_tokens` when a valid `prompt_tokens_details.cached_tokens` value is unavailable, and map the result to `cache_read_input_tokens`.

### Requirement 2: Recognize DeepSeek cache hits in Responses

`responsesUsageToAnthropic` MUST read `prompt_cache_hit_tokens` when the existing cache fields are unavailable, and subtract the cache-read count from uncached input.

### Requirement 3: Compute uncached input correctly

When the upstream returns total input, cache hit, and cache creation tokens:

```text
input_tokens = max(total_input_tokens - cache_read_input_tokens - cache_creation_input_tokens, 0)
```

When total input is absent but `prompt_cache_hit_tokens` and `prompt_cache_miss_tokens` are present, `input_tokens` MUST use `prompt_cache_miss_tokens`. Hit tokens MUST NOT be counted again in `input_tokens`.

### Requirement 4: Preserve existing usage formats

Existing `prompt_tokens_details.cached_tokens`, Responses `cached_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`, and responses with no cache fields MUST continue to produce correct results. The compatibility logic MUST NOT change normal `output_tokens` mapping or error handling.

### Requirement 5: Make stream and non-stream results equivalent

The same usage data, when processed through non-stream conversion and the final SSE usage event conversion, MUST produce identical Anthropic cache fields and input-token counts.

### Requirement 6: Do not invent cache hits

`cache_read_input_tokens` MUST be set only when the upstream supplies a valid cache field. Missing, unparseable, or negative cache values MUST be treated as absent; cache hits MUST NOT be inferred from prompt length.

## Task Details

### Task 1: Normalize cache usage and fix Chat Completions

#### Requirements

**Objective** — Add reusable cache-usage parsing that preserves existing standard-field semantics and makes Chat Completions non-stream/SSE conversion recognize DeepSeek `prompt_cache_hit_tokens` and `prompt_cache_miss_tokens`.

**Outcomes** — `internal/proxy/transform/openai_chat.go` emits correct `cache_read_input_tokens` and `input_tokens`; `internal/proxy/transform/openai_chat_test.go` covers standard fields, DeepSeek fields, precedence, explicit zero, missing total input, and streaming.

**Evidence** — A synthetic response with `prompt_tokens=1000`, `prompt_cache_hit_tokens=600`, and `prompt_cache_miss_tokens=400` converts to `cache_read_input_tokens=600` and `input_tokens=400`; when standard `cached_tokens=300` and the hit field coexist, 300 wins; an explicit `cached_tokens=0` does not fall back to 600.

**Constraints** — Keep Anthropic usage field names unchanged; do not put hit tokens into `input_tokens` twice; do not infer hits from input length; treat unparseable fields as absent; preserve existing cache-write and output-token mapping.

**Edge Cases** — Missing `prompt_tokens_details`; zero `cached_tokens`; only one of hit/miss; total input smaller than hit plus write; string or non-numeric cache fields; usage arriving in a distinct final SSE event.

**Verification** — Run Chat conversion tests and confirm old standard-format tests remain unchanged while the DeepSeek fixture reports 400 uncached and 600 cached input tokens.

#### Plan

1. In `internal/proxy/transform/openai_chat_test.go`, add failing tests before implementation:
   - `TestOpenAIChatToAnthropicUsesPromptCacheHitTokens`: a non-stream usage object with only `prompt_cache_hit_tokens=600` and `prompt_cache_miss_tokens=400` must produce Anthropic `input_tokens=400` and `cache_read_input_tokens=600`.
   - `TestOpenAIChatToAnthropicPrefersCachedTokens`: with both `prompt_tokens_details.cached_tokens=300` and `prompt_cache_hit_tokens=600`, assert that 300 is used.
   - `TestOpenAIChatToAnthropicPreservesExplicitZeroCache`: with `cached_tokens=0` and hit=600, assert that cache read remains 0.
   - `TestOpenAIChatSSEToAnthropicUsesPromptCacheHitTokens`: an SSE usage event with the same hit/miss fields must produce the same final `message_delta.usage` as the non-stream path.
2. Run `go test ./internal/proxy/transform -run 'TestOpenAIChat.*PromptCache|TestOpenAIChatSSEToAnthropicUsesPromptCacheHitTokens' -count=1` and confirm the new tests fail before implementation.
3. Add shared cache-usage helpers in `internal/proxy/transform` with these exact behaviors:
   - select an existing valid standard cache field before the DeepSeek hit field; preserve an explicitly present zero;
   - subtract cache read and cache creation from `prompt_tokens` or the equivalent total input; if total input is absent, use `prompt_cache_miss_tokens`;
   - treat negative and unparseable values as absent and clamp final input tokens at zero.
4. Make `openAIUsageToAnthropic` use the helper while preserving current `prompt_tokens_details.cached_tokens`, `cache_read_input_tokens`, and `cache_creation_input_tokens` compatibility.
5. Re-run the targeted tests and confirm they pass.
6. Commit the task implementation and tests with `fix: parse DeepSeek cache usage`. Tasks 1 and 2 were completed together in that single commit (see Implementation Record below).

#### Verification

- [x] New tests fail first, proving the existing implementation omits the DeepSeek field.
- [x] Chat non-stream and SSE tests pass.
- [x] Existing `openai_chat_test.go` usage tests pass.
- [x] `go test ./internal/proxy/transform -count=1` passes.

### Task 2: Fix Responses usage, run regressions, and update the specs

#### Requirements

**Objective** — Make OpenAI Responses non-stream and SSE usage use the same cache-field precedence, confirm no-cache and legacy behavior, and complete full validation.

**Outcomes** — `internal/proxy/transform/openai_responses.go` recognizes existing `cached_tokens` and DeepSeek hit fields; Responses stream and non-stream usage matches Chat semantics; progress and evidence are updated in both specs.

**Evidence** — A Responses fixture with `input_tokens=1000`, hit=600, and miss=400 converts to `input_tokens=400` and `cache_read_input_tokens=600`; the existing no-cache fixture keeps its input/output values; stream and non-stream results match.

**Constraints** — Do not modify Responses request-body conversion; preserve no-cache response behavior; add no database fields; use synthetic fixtures only, with no real token or network request.

**Edge Cases** — Responses `cached_tokens`; Responses `prompt_tokens_details.cached_tokens`; missing hit/miss fields; total input and miss both present (total-input arithmetic takes precedence); final input clamped at zero.

**Verification** — Run focused Responses tests, the whole transform package, and `go test ./...`; confirm the worktree diff contains only transform implementation, relevant tests, and the two feature specs.

#### Plan

1. In `internal/proxy/transform/openai_responses_test.go`, add failing tests:
   - `TestOpenAIResponsesToAnthropicUsesPromptCacheHitTokens`: a non-stream usage object with total input, hit, and miss fields must produce correct input and cache-read values.
   - `TestOpenAIResponsesSSEToAnthropicUsesPromptCacheHitTokens`: a `response.completed` usage object with hit/miss fields must produce correct `message_delta.usage`.
   - `TestOpenAIResponsesToAnthropicKeepsUncachedUsageWithoutCacheFields`: no cache fields must preserve the existing input/output behavior.
2. Run `go test ./internal/proxy/transform -run 'TestOpenAIResponses.*PromptCache|TestOpenAIResponses.*UncachedUsage' -count=1` and confirm the new tests fail before implementation.
3. Update `responsesUsageToAnthropic` to use Task 1's cache helper: preserve existing `cached_tokens`, fall back to `prompt_tokens_details.cached_tokens` and then `prompt_cache_hit_tokens`, subtract hit/write tokens from uncached input, and use `prompt_cache_miss_tokens` when total input is absent.
4. Run:
   ```bash
   go test ./internal/proxy/transform -count=1
   go test ./...
   git diff --check
   git status --short
   ```
   Expected results: transform tests pass, all Go tests pass, `git diff --check` has no output, and no unrelated files are present.
5. Update `spec.md` and `spec_ZH.md` with actual commands, results, completed tasks, and commit information; use `test: cover DeepSeek cache usage mapping` or the actual matching commit message. The actual single commit is `fix: parse DeepSeek cache usage` (see Implementation Record below).

#### Verification

- [x] Responses non-stream and SSE tests fail first and then pass.
- [x] `go test ./internal/proxy/transform -count=1` passes.
- [x] `go test ./...` passes.
- [x] `git diff --check` passes, with no request-conversion, database, or frontend changes.
- [x] After implementation, update progress, checklist, and verification evidence in `spec.md` and `spec_ZH.md`.

## Implementation Record (2026-08-04)

Branch: `fix/deepseek-cache-usage-parsing`; single commit `fix: parse DeepSeek cache usage`.

Files changed:
- `internal/proxy/transform/usage.go` (new): shared `normalizeOpenAIUsage` helper implementing cache-field precedence (standard field first, DeepSeek `prompt_cache_hit_tokens` fallback, explicit zero preserved), input arithmetic (`total - cache_read - cache_creation`, `prompt_cache_miss_tokens` fallback, zero clamp), and validity handling (negative / unparseable values treated as absent).
- `internal/proxy/transform/usage_test.go` (new): helper-level tests for miss fallback without total input, zero clamping, unparseable and negative cache values, and cache-creation subtraction.
- `internal/proxy/transform/openai_chat.go`: `openAIUsageToAnthropic` delegates to the helper with paths `prompt_tokens_details.cached_tokens` → `cache_read_input_tokens` → `prompt_cache_hit_tokens`.
- `internal/proxy/transform/openai_chat_test.go`: DeepSeek hit/miss non-stream and SSE tests, standard-field precedence, explicit zero.
- `internal/proxy/transform/openai_responses.go`: `responsesUsageToAnthropic` delegates to the helper with paths `cached_tokens` / nested cached-token fields → `prompt_cache_hit_tokens`.
- `internal/proxy/transform/openai_responses_test.go`: DeepSeek hit/miss non-stream and SSE tests, no-cache preservation, precedence, explicit zero.
- `sdd-docs/features/2026-08-03-deepseek-cache-usage-parsing/spec.md` and `spec_ZH.md`: this record.

Commands and results:
- `go test ./internal/proxy/transform -count=1` → `ok`
- `go test ./...` → `ok`
- `git diff --check` → clean
- `git status --short` → only the files listed above

Task 1 and Task 2 were implemented together in the single commit above.
