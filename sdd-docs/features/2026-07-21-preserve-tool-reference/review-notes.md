# Review Notes: Preserve tool_reference in Proactive Cleanup

**Date:** 2026-07-21
**Branch:** `fix/preserve-tool-reference`
**Commits:** `7d8a8ca` (spec), `13bef71` (Task 1), `c099e4d` (Task 2)
**Status:** Validated — `go test ./...` green

## Conclusion

Proactive content-block cleanup (`proactiveCleanUnknownContentTypes`, gated by `StripUnknownContentBlocks`) used to strip `tool_reference` blocks. That was correct against the **2026-05 kimi upstream** (which rejected the type with `"unsupported content type"`) but harmful against the **current upstream**, which accepts `tool_reference` and where Claude Code 2.1.x relies on it as the deferred-tool load marker. With the strip active, kimi-k3 degraded: tool calls collapsed to a fixed `Bash{command:"true",description:"noop"}` placeholder with empty `thinking`.

Fix: parameterize `filterContentBlocks(preserveToolReference)`. Proactive passes `true` (keep `tool_reference`, restore model context); reactive (`tryRectify` → `cleanUnknownContentTypes`) passes `false` (keep the ability to recover a 400 caused by a `tool_reference` pointing at an undefined tool). Reactive error matching extended so current kimi 400 strings still trigger cleanup.

## Root-Cause Evolution

| Phase | Source | Claim | Valid |
|-------|--------|-------|:-----:|
| 2026-05-15 | `research/2026-05-15-...` | kimi-k2.6 rejects `tool_reference` with "unsupported content type" 400 | then |
| 2026-06-15 | `features/2026-06-15-...` | proactive strip gated by `StripUnknownContentBlocks`; whitelist excludes `tool_reference` | then |
| 2026-07-21 | live probe (this review) | all three kimi endpoints **accept** `tool_reference` when it references a defined tool; 400 only when the referenced tool is absent from `tools` | now |

The original diagnosis conflated two variables: it attributed the 400 to the `tool_reference` type itself, when the real cause was the referenced tool being undefined. Harmless in 2026-05 (stripping fixed the 400 regardless); pure side-effect once the upstream started accepting the type.

## Empirical Evidence (2026-07-21 live probe)

Minimal Anthropic `/v1/messages`, non-stream, one `tool_use`→`tool_result` round. `tools` defines `WebSearch`. The `tool_reference` in "ref" variants points at `WebSearch` (defined) or `ToolSearch` (undefined).

| endpoint / model | ref → defined | ref → undefined | no ref |
|---|---|---|---|
| `api.moonshot.cn/anthropic` kimi-k2.6 | 200 | 400 `Tool reference 'ToolSearch' not found in available tools` | 200 |
| `api.kimi.com/coding` kimi-for-coding (k2.7) | 200 | 400 `Invalid request Error` | 200 |
| `api.kimi.com/coding` k3 | 200 | 400 `Invalid request Error` | 200 |

`tool_reference` as a content type is accepted by all three. The 400s come from referencing an undefined tool, not from the type.

## Changes

1. `internal/proxy/rectifier.go`
   - `filterContentBlocks(msg, preserveToolReference bool)`: when `preserveToolReference && btype == "tool_reference"`, the block is kept; all other logic byte-for-byte unchanged. Recursive call forwards the flag.
   - `cleanUnknownContentTypes` calls `filterContentBlocks(msg, false)` (reactive — behavior unchanged).
   - `isUnsupportedContentTypePhrase` extended with `"tool reference"` to match the current moonshot 400 string.
2. `internal/proxy/handler.go`
   - `proactiveCleanUnknownContentTypes` calls `filterContentBlocks(msg, true)` (proactive — now preserves `tool_reference`).
3. Tests (`internal/proxy/rectifier_test.go`, `server_test.go`):
   - `TestProactiveClean_PreservesToolReference` — `tool_reference` + synthetic `server_tool_use` + `text`; `tool_reference` and `text` survive, `server_tool_use` stripped.
   - `TestReactiveClean_StripsToolReference` — reactive path still strips `tool_reference`.
   - `TestMatchErrorPattern_KimiCurrentErrors` — coding "Invalid request Error" + moonshot "Tool reference not found".
   - Existing `TestProactiveClean_AnthropicStripEnabled_*` renamed from `Removes` to `Preserves`.

## Verification

- `go test ./internal/proxy/...` — 571 passed.
- `go test ./...` — 1689 passed across 17 packages.
- Old `"unsupported content type"` rectifier tests still green (backward compat).

## Residual / Follow-up

- `StripUnknownContentBlocks` provider flag and its frontend checkbox become a **no-op** after this fix (the only non-standard type ever observed in practice was `tool_reference`, now preserved). Deprecating the flag (DB column kept for read-compat, admin API + frontend removed) is a **separate follow-up feature**, intentionally out of scope here to keep the fix reviewable and reversible.
- The 2026-06-15 spec and 2026-05-15 research carry dated correction notes pointing here.
