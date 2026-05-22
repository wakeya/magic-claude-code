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
