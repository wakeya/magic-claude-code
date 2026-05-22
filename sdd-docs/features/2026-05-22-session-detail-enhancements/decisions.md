# Session Detail Enhancements — Design Decisions

**Date**: 2026-05-22

## D1: JSONL Path — Display Location

**Decision**: Place the JSONL filename between project path and timestamp, not as the first row.

**Rationale**: The session title is the primary identifier. Project path provides context. The JSONL filename is secondary metadata — useful for debugging/copying but shouldn't dominate the visual hierarchy.

## D2: Message Count — Two-Phase Strategy

**Decision**: Use approximate counting in the list scan (head/tail sampling), accurate counting only in the detail API.

**Alternatives considered**:
- Full file read during scan: Rejected. 88 files × 41,685 total lines caused 2s+ load time.
- Background pre-scanning: Rejected. Adds complexity without solving the first-load problem.
- No count in sidebar: Rejected. Users expect to see message density at a glance.

**Rationale**: The list view needs to be fast (sub-second). The detail API already parses the full file (`ParseMessages`), so `len(messages)` is free. Updating the sidebar on selection gives the user the accurate count exactly when they care about it.

## D3: Colored Borders — CSS Variable Choice

**Decision**: Use `--session-accent` (blue) for assistant, hardcode `#f59e0b` (amber) for technical messages.

**Rationale**: The export HTML template already defines these colors. Using the same values ensures visual consistency between the admin panel and exported HTML. The amber color is not theme-dependent — it provides sufficient contrast in both light and dark modes.

## D4: Icon Button Background

**Decision**: Use `var(--session-border)` as the default background instead of `var(--session-surface-muted)`.

**Rationale**: `--session-surface-muted` was too close to the dark theme background, making buttons invisible. `--session-border` resolves to `#dbeafe` (light) and `#263449` (dark), providing visible contrast in both themes without being distracting.

## D5: GitHub Icon — Inline SVG

**Decision**: Inline the GitHub SVG path rather than importing from lucide-vue-next.

**Rationale**: Lucide does not include brand logos (GitHub, Twitter, etc.). The GitHub mark is a simple single-path SVG. Inlining avoids adding another dependency.

## D6: SSE Usage Extraction — Strip Accept-Encoding

**Decision**: Strip `Accept-Encoding` and `TE` headers in the proxy before forwarding to upstream providers, rather than adding gzip decompression to the SSE pipeline.

**Problem**: MiniMax (and potentially other providers) gzip-compress SSE responses when the client sends `Accept-Encoding: gzip`. Go's `http.Transport` does NOT auto-decompress when the `Accept-Encoding` header was set by application code (not by the Transport itself). The SSEObserver received compressed binary data and could not parse usage tokens.

**Alternatives considered**:
- Add gzip reader in SSE pipeline: Rejected. Adds complexity, latency, and edge cases (chunk boundaries across gzip frames).
- Set `DisableCompression: false` on Transport: Rejected. Only affects headers the Transport adds itself — does not override application-set headers.
- Wrap `resp.Body` with `gzip.NewReader`: Rejected. Would require detecting `Content-Encoding` and handling both compressed/uncompressed responses.

**Rationale**: Stripping the headers is a one-line fix that prevents the problem at the source. No upstream SSE response will be compressed, so the SSEObserver always receives plain text. Other providers (Zhipu GLM, Kimi) that ignore `Accept-Encoding` for SSE are unaffected.

## D7: Project Name Inference — Valid CWD Priority + Directory Name Fallback

**Decision**: When `foldSourceProjectSessions` collects `ProjectPath` values from sibling sessions in the same directory, filter out `""` and `"Unknown Project"` before inference. When all paths are invalid, extract the last segment from the encoded directory name as a fallback.

**Problem**: Some JSONL files lack the `cwd` field (e.g., sessions launched from `~`), causing `scanSessionFile` to return `"Unknown Project"`. The old code passed "Unknown Project" alongside valid paths to `inferProjectRoot`. Since `isAncestorOfAll` returns false for "Unknown Project", the entire group inference failed — even when sibling sessions in the same directory had correct `cwd` values.

**Alternatives considered**:
- Full-path decoding from directory name: Rejected. The encoding (`/` → `-`) is lossy — project names containing `-` cannot be reliably recovered. E.g., `-home-www-claude-workspace` could be `/home/www/claude/workspace` or `/home/www/claude-workspace`.
- Filter only, no fallback: Partial fix. Still shows "Unknown Project" when all sessions in a directory lack CWD.
- Infer full path from directory name: Rejected. Unnecessary — `projectName()` only uses the last segment for display, so a full path isn't needed.

**Rationale**: Two-tier strategy — trust signal data first (valid CWD from siblings), only fall back to directory name when all signals are absent. The directory name fallback is lossy for project names containing `-` (e.g., `pm0511-lvshixiehui` → `lvshixiehui`), but is still better than "Unknown Project".

## D8: Nil Slice JSON Serialization — Dual Backend + Frontend Defense

**Decision**: Convert nil messages to empty slice in `handleSessionDetail` and `handleSessionExport` on the backend; add `|| []` null-guard on `props.messages` in the frontend `SessionOutline.vue`.

**Problem**: In Go, `var msgs []Message` is a nil slice, and `json.Marshal` outputs `null` rather than `[]`. The TypeScript type annotation `SessionMessage[]` does not reflect the runtime null possibility. `SessionOutline.vue`'s computed property directly calls `props.messages.map(...)`, throwing `TypeError: Cannot read properties of null (reading 'map')` when messages is null.

**Alternatives considered**:
- Frontend-only fix: Would fix the immediate error, but other potential consumers could still hit the same issue.
- Backend-only fix: Would ensure data integrity, but the frontend would lack a defensive layer.

**Rationale**: Dual-layer fix — backend ensures the data contract (`"messages"` is always an array), frontend defends against unexpected nulls. Go's `json.Marshal([]Message(nil))` → `null` is a classic pitfall worth explicitly guarding against in the handler layer. The root cause is `SessionOutline.vue:28`'s `.map()` call; the backend fix is data integrity insurance.
