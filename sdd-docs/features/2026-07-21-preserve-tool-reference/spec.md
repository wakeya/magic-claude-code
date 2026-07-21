# Preserve tool_reference in Proactive Cleanup Spec

Local page: `internal/proxy/rectifier.go` (knownContentTypes, filterContentBlocks, cleanUnknownContentTypes), `internal/proxy/handler.go` (proactiveCleanUnknownContentTypes in transformRequest)
Proxy entry: `internal/proxy/handler.go` ServeHTTP / tryRectify
Reference sources: `~/workspace/open-software/cc-switch/src-tauri/src/proxy/body_filter.rs`, `sdd-docs/research/2026-05-15-rectifier-pattern3-generic-bad-request.md`, `sdd-docs/features/2026-06-15-proactive-content-block-cleanup/spec.md`, live curl probe against `api.moonshot.cn/anthropic` and `api.kimi.com/coding` (2026-07-21)
Stack: Go 1.26 standard library
Last updated: 2026-07-21
Progress: 3 / 3 planned (Tasks 1–3 shipped)

## Overall Analysis (Source Analysis)

### Problem

When a provider has `strip_unknown_content_blocks: true`, `transformRequest` calls `proactiveCleanUnknownContentTypes`, which recursively removes every content block whose `type` is not in the `knownContentTypes` whitelist. The whitelist is `text, image, tool_use, tool_result, thinking, redacted_thinking, document, file` — it does **not** contain `tool_reference`.

Claude Code 2.1.x emits `tool_reference` blocks inside `tool_result.content` arrays as the client-side marker for deferred-tool load results (ToolSearch). When MCC strips them, the upstream model still receives the surrounding `tool_result` but loses the marker that tells it "this result loaded deferred tool X". On kimi-k3 (observed in session `f6549d4f-dddf-45a2-ad45-976a72476207.jsonl`, turns L1719–L1734), the model then degrades: it announces "I will call list_pages" in text, but the actual `tool_use` block it emits collapses to a fixed placeholder `Bash{command:"true",description:"noop"}` with empty `thinking`, and loops ("我又发了一次空调用", "我一直在发空调用，无法停止"). The same kimi-k3 session successfully calls playwright MCP tools 5 times — only chrome-devtools-mcp (loaded via ToolSearch, guarded by `tool_reference`) is unreachable.

Disabling `strip_unknown_content_blocks` on the kimi-k3 provider restores tool calling, confirming the strip as the regression source.

### Root Cause Evolution

The cleanup was introduced against a different upstream behavior than today's:

1. **2026-05-15 research** (`sdd-docs/research/2026-05-15-...`): kimi-k2.6 returned HTTP 400 with `"unsupported content type: tool_reference"` / `"failed to convert tool result content: unsupported content type in ContentBlockParamUnion: tool_reference"` whenever the request body contained a `tool_reference` block. curl reproduction at the time: with `tool_reference` → 400, without → 200. The reactive rectifier (`bbfc0fd`) was added to strip the block after a 400, then `d9104a1` promoted it to a proactive opt-in cleanup gated by `StripUnknownContentBlocks`.

2. **2026-06-15 spec** (`sdd-docs/features/2026-06-15-proactive-content-block-cleanup/`): codified the whitelist without `tool_reference`, citing cc-switch's layered cleanup as the architectural reference. cc-switch's `body_filter.rs` was mis-read at the time as evidence supporting content-block cleaning — in fact cc-switch only strips `_`-prefixed private parameters and **never** touches `tool_reference` or any content block (0 occurrences of `tool_reference` in `src-tauri/src/proxy/`).

3. **Today (2026-07-21)**: kimi's Anthropic-compatible endpoints have been upgraded. A controlled live probe (see Empirical Evidence) shows all three endpoints now **accept** `tool_reference` as a content type when it references a tool that exists in the request's `tools` array. The original 400 ("unsupported content type") no longer reproduces; the cleanup's precondition is gone and the cleanup is now pure side-effect.

### Empirical Evidence (live probe, 2026-07-21)

Request shape: minimal Anthropic `/v1/messages`, non-streaming, with one `tool_use`→`tool_result` round-trip. The `tool_result.content` contains `[tool_reference, text]` in the "ref" variants. `tools` defines `WebSearch`.

| endpoint / model | `tool_reference` → **defined** tool | `tool_reference` → **undefined** tool | no `tool_reference` |
|---|---|---|---|
| `api.moonshot.cn/anthropic` kimi-k2.6 | **200** | 400 `Tool reference 'ToolSearch' not found in available tools` | 200 |
| `api.kimi.com/coding` kimi-for-coding (k2.7) | **200** | 400 `Invalid request Error` | 200 |
| `api.kimi.com/coding` k3 | **200** | 400 `Invalid request Error` | 200 |

Conclusions:

- `tool_reference` **as a content type is accepted by all three endpoints**, including the k2.6 moonshot endpoint that originally triggered the cleanup.
- The 400s still observed are caused by `tool_reference` pointing at a tool **not present in the request's `tools` array**, not by the type itself. In Claude Code's normal flow, the referenced tool is injected into `tools` on the next turn after ToolSearch loads it, so this 400 is an anomaly path, not the common path.
- Stripping `tool_reference` on the common path therefore removes information the model uses, with no 400 to prevent.

### Reference Implementations

**cc-switch** (`src-tauri/src/proxy/body_filter.rs`): `filter_private_params_with_whitelist` recursively removes only keys starting with `_` (with a whitelist escape hatch and a JSON-schema property-name guard). It does **not** inspect or filter content-block `type` values. The 2026-06-15 spec cited cc-switch as a reference for the layered architecture (proactive + reactive), not for content-block filtering — that filtering is MCC-specific and is the part being corrected here.

### Strategy

Split the cleanup behavior between the two call sites so each matches its own precondition:

- **Proactive cleanup** (`proactiveCleanUnknownContentTypes`, runs before the first upstream request): **preserve** `tool_reference`. The common path no longer 400s on `tool_reference`, and preserving it keeps the model's deferred-tool context intact.
- **Reactive cleanup** (`cleanUnknownContentTypes`, runs inside `tryRectify` after a 400): **keep the ability to remove** `tool_reference`. The anomaly path (`tool_reference` pointing at an undefined tool) still 400s, and stripping the offending block still recovers the request on retry.

This is implemented by giving `filterContentBlocks` a `preserveToolReference bool` parameter. Proactive passes `true`; reactive passes `false` (current behavior unchanged).

A follow-up, separate feature will deprecate the `strip_unknown_content_blocks` provider flag and its frontend checkbox: once proactive cleanup no longer removes `tool_reference` (the only non-standard type ever observed in practice), the flag becomes a no-op and the UI only confuses users (this very incident). That work is out of scope here to keep the fix reviewable and reversible; it is tracked as a follow-up.

### Scope

- **In scope**: parameterize `filterContentBlocks`; verify/extend reactive error-pattern matching for current kimi error strings; tests; review notes; update the 2026-06-15 spec/research with a dated correction.
- **Out of scope**: removing the `StripUnknownContentBlocks` flag, DB column, admin API field, and frontend checkbox (follow-up feature); changing OpenAI-format transforms; changing thinking stripping.

## Development Checklist

- [x] Parameterize `filterContentBlocks(msg, preserveToolReference)`; proactive passes `true`, reactive passes `false`.
- [x] Verify `matchErrorPattern` recognizes current kimi 400 strings (`"Invalid request Error"`, `"Tool reference '<name>' not found in available tools"`); extend phrase list if needed.
- [x] Unit tests: proactive preserves `tool_reference`; reactive strips `tool_reference`; new error-pattern cases use the live error strings.
- [x] Run `go test ./internal/proxy/...` and `go test ./...`.
- [x] Add review notes (`review-notes.md` / `review-notes_ZH.md`) and a dated correction note in the 2026-06-15 spec and 2026-05-15 research.

## Requirements

### Requirement 1: Proactive Cleanup Preserves tool_reference

When `provider.StripUnknownContentBlocks == true` and `apiFormat == anthropic`, `transformRequest` must NOT remove `tool_reference` blocks from `messages[].content` or nested `tool_result.content`. Other non-standard types (any type not in `knownContentTypes` and not `tool_reference`) continue to be stripped.

### Requirement 2: Reactive Cleanup Retains tool_reference Removal

`cleanUnknownContentTypes` (used by `tryRectify` after a 400) must continue to strip `tool_reference`, so the anomaly path (a `tool_reference` pointing at a tool absent from `tools`) can still recover by retrying without the block.

### Requirement 3: Reactive Pattern Matching Covers Current Kimi Errors

`matchErrorPattern` must classify the current kimi 400 messages as `PatternGenericBadRequest` so `tryRectify` fires. At minimum: `"Invalid request Error"` (coding endpoint, k2.7/k3) and `"Tool reference '<name>' not found in available tools"` (moonshot endpoint, k2.6).

### Requirement 4: No Regression for Other Non-Standard Types

Proactive cleanup with `StripUnknownContentBlocks == true` must still remove types other than `tool_reference` that are not in `knownContentTypes` (e.g., a hypothetical future `server_tool_use`), preserving the original defense for genuinely unknown types.

### Requirement 5: Default Passthrough Unchanged

Providers with `StripUnknownContentBlocks == false` (default) are not modified at all by the proactive path; behavior is identical to today.

## Task Details

### Task 1: Parameterize filterContentBlocks

#### Requirements

**Objective** — Give `filterContentBlocks` a `preserveToolReference bool` parameter so proactive and reactive callers can diverge: proactive keeps `tool_reference` (restore deferred-tool context for the model), reactive still strips it (recover the anomaly-path 400).

**Outcomes** — `internal/proxy/rectifier.go`: `filterContentBlocks(msg map[string]any, preserveToolReference bool) bool`; `proactiveCleanUnknownContentTypes` calls it with `true`; `cleanUnknownContentTypes` calls it with `false`. `tool_reference` is not added to the `knownContentTypes` map (it remains "non-standard" by the map's definition); instead the parameter short-circuits removal for that one type. All other type-filtering logic is byte-for-byte unchanged.

**Evidence** — New unit tests `TestProactiveClean_PreservesToolReference` and `TestReactiveClean_StripsToolReference` both pass; existing `TestCleanUnknownContentTypes_*` tests pass after updating the call site signature.

**Constraints** — Do not add `tool_reference` to `knownContentTypes` (that would silently disable reactive stripping too). The parameter only exempts `tool_reference` from removal; recursion into `tool_result.content` is preserved. No change to the `btype == ""` passthrough branch.

**Edge Cases** — `tool_reference` at top-level `content`; `tool_reference` nested in `tool_result.content`; `tool_reference` with extra fields (`tool_name`); multiple `tool_reference` blocks in one `tool_result`; `tool_reference` mixed with genuinely-unknown types (only `tool_reference` survives, others stripped — proactive).

**Verification** — `go test ./internal/proxy/... -run 'TestProactiveClean|TestReactiveClean|TestCleanUnknownContentTypes|TestFilterContentBlocks' -v`.

#### Plan

1. In `internal/proxy/rectifier.go`, change the signature:
   ```go
   func filterContentBlocks(msg map[string]any, preserveToolReference bool) bool {
   ```
2. Inside the block loop, before the `!knownContentTypes[btype] && btype != ""` check, add:
   ```go
   if preserveToolReference && btype == "tool_reference" {
       filtered = append(filtered, block)
       continue
   }
   ```
   (`tool_reference` has no nested `content`, so no recursion needed.)
3. Update `cleanUnknownContentTypes` (reactive): its recursive call site `filterContentBlocks(msg)` → `filterContentBlocks(msg, false)`. Behavior identical to today.
4. In `internal/proxy/handler.go`, update `proactiveCleanUnknownContentTypes` (which currently calls `filterContentBlocks(msg)` indirectly via `cleanUnknownContentTypes`'s helper — verify the call chain): ensure the proactive path calls `filterContentBlocks(msg, true)`. If `proactiveCleanUnknownContentTypes` currently delegates to `cleanUnknownContentTypes`'s loop, extract a shared helper that takes the parameter, or inline the loop in the proactive function with `preserveToolReference=true`.
5. Add unit tests (see Task 3).

#### Verification

- [x] Signature change compiles; all callers updated (`rectifier.go` cleanUnknownContentTypes → `false`, `handler.go` proactiveCleanUnknownContentTypes → `true`, recursive call forwards the parameter).
- [x] `TestProactiveClean_PreservesToolReference` green: input `[{tool_reference}, {server_tool_use}, {text}]` keeps `tool_reference` + `text`, strips `server_tool_use`.
- [x] `TestReactiveClean_StripsToolReference` green: input `[{tool_reference}, {text}]` leaves only `[{text}]`.
- [x] Existing `server_test.go` `TestProactiveClean_AnthropicStripEnabled_*` updated from `RemovesToolReference` to `PreservesToolReference`.
- [x] `go test ./internal/proxy/...` — 571 passed; `go test ./...` — 1686 passed across 17 packages.

### Task 2: Verify/Extend Reactive Error-Pattern Matching

#### Requirements

**Objective** — Ensure `tryRectify` fires on the current kimi 400 messages so the reactive `tool_reference` stripping (Requirement 2) can actually trigger. Confirm coverage for `"Invalid request Error"` (coding endpoint) and `"Tool reference '<name>' not found in available tools"` (moonshot endpoint).

**Outcomes** — `internal/proxy/rectifier.go` `hasGenericInvalidRequestPhrase` (or the matching dispatch) recognizes both strings as `PatternGenericBadRequest`. If either is already matched by an existing phrase (e.g., `"invalid request"` case-insensitively covers `"Invalid request Error"`), document it with a test and make no code change. Add the moonshot `Tool reference ... not found` phrase if not covered.

**Evidence** — Unit tests with the exact live error JSON bodies (see Empirical Evidence) assert `matchErrorPattern` returns `PatternGenericBadRequest`.

**Constraints** — Do not over-match: the new phrase must indicate a content/tool-reference problem, not a generic transient error. Keep the phrase narrow (e.g., `"tool reference"` + `"not found"`, or the literal `"tool reference '"`).

**Edge Cases** — Error body wrapping variations (`{"error":{"message":...}}`, `{"error":{"code":"1210","message":...}}`, top-level `{"message":...}`); mixed casing; the old `"unsupported content type"` phrase still matched (backward compatibility with older upstream versions).

**Verification** — `go test ./internal/proxy/... -run TestMatchErrorPattern -v`.

#### Plan

1. Read the current `hasGenericInvalidRequestPhrase` and the full `matchErrorPattern` dispatch.
2. Add two test cases feeding the live JSON:
   - coding: `{"error":{"type":"invalid_request_error","message":"Invalid request Error"}}`
   - moonshot: `{"error":{"type":"invalid_request_error","message":"messages.2.content.0.tool_result.content: Tool reference 'ToolSearch' not found in available tools"}}`
3. If `"Invalid request Error"` is already matched by an existing case-insensitive `"invalid request"` phrase, the first case passes with no code change — record that in the test's comment. If `"Tool reference ... not found"` is not matched, add a phrase (e.g., `"tool reference"`).
4. Re-run the existing `rectifier_test.go:434` and `:472` cases (old "unsupported content type" strings) to confirm no regression.

#### Verification

- [x] coding `"Invalid request Error"` already matched by `hasGenericInvalidRequestPhrase` (phrase `"invalid request"`); test documents this, no code change needed.
- [x] moonshot `"Tool reference '<name>' not found in available tools"` newly matched via `isUnsupportedContentTypePhrase` extension (added `"tool reference"` phrase).
- [x] Old `"unsupported content type"` cases still classify (backward compat); full `TestMatchErrorPattern` suite 34 passed.
- [x] `go test ./...` — 1689 passed across 17 packages.

### Task 3: Tests, Review Notes, and Spec Correction

#### Requirements

**Objective** — Lock the new behavior with tests, archive the review conclusion, and correct the record in the 2026-06-15 spec and 2026-05-15 research so future readers know the upstream behavior changed and the cleanup's precondition no longer holds.

**Outcomes** — New tests in `internal/proxy/rectifier_test.go` (or a focused `_test.go`): `TestProactiveClean_PreservesToolReference`, `TestReactiveClean_StripsToolReference`, and the two `TestMatchErrorPattern` cases from Task 2. Review notes files in this feature dir. A dated addendum section appended to `sdd-docs/features/2026-06-15-proactive-content-block-cleanup/spec.md` (+ ZH) and `sdd-docs/research/2026-05-15-rectifier-pattern3-generic-bad-request.md` pointing to this feature.

**Evidence** — `go test ./...` green; review notes cite the live-probe matrix; the 2026-06-15 spec and 2026-05-15 research carry a "2026-07-21 correction" note.

**Constraints** — Tests must not embed real API tokens; use synthetic fixtures. The correction notes are additive (do not rewrite history); they record that the upstream behavior changed and link here.

**Edge Cases** — Ensure `tool_reference` preservation does not leak into the reactive path test (they must diverge); ensure the proactive test still strips a genuinely-unknown type (e.g., `server_tool_use`) to prove Requirement 4.

**Verification** — `go test ./...` green; `git diff --stat` limited to `internal/proxy/rectifier.go`, `internal/proxy/handler.go` (if touched), the test file, the review-notes files, and the two dated correction notes.

#### Plan

1. Add the four tests described above. Include a proactive test case mixing `tool_reference` with a synthetic `server_tool_use` block — assert `tool_reference` survives and `server_tool_use` is stripped.
2. Write `sdd-docs/features/2026-07-21-preserve-tool-reference/review-notes.md` and `review-notes_ZH.md` with: the live-probe matrix, the root-cause-evolution summary, the chosen strategy (proactive preserve / reactive strip), and explicit mention that the `StripUnknownContentBlocks` flag deprecation is a follow-up.
3. Append a "## 2026-07-21 Correction" section to `sdd-docs/features/2026-06-15-proactive-content-block-cleanup/spec.md` and `spec_ZH.md`, and a matching note at the bottom of `sdd-docs/research/2026-05-15-rectifier-pattern3-generic-bad-request.md`, each linking back to this feature dir and summarizing that kimi upstream now accepts `tool_reference`.
4. `go test ./...`; confirm green; commit on this branch only.

#### Verification

- [x] `go test ./...` — 1689 passed across 17 packages.
- [x] Review notes present: `review-notes.md` + `review-notes_ZH.md`.
- [x] 2026-06-15 spec (EN+ZH) + 2026-05-15 research carry "2026-07-21 Correction" notes linking here.
