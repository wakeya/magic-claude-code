# Decisions

## D-001: Each User Message as an Outline Entry

**Date:** 2026-05-28
**Status:** accepted

**Context:** User requested following the existing session record page's implementation. SessionOutline.vue generates one entry per user message.

**Decision:** The outline has one entry per user message, with the entry showing a preview (first 50 characters, whitespace collapsed, truncated with `...`) and a timestamp.

**Impact:** Simple, deterministic, consistent with the existing page. The downside is that very long messages still produce lengthy entries.

**Re-evaluation trigger:** User feedback that entry granularity is too coarse or too verbose.

---

## D-002: IntersectionObserver for Scroll Auto-Highlight

**Date:** 2026-05-28
**Status:** accepted

**Context:** Need to implement scroll-time auto-highlighting of the current outline item in both large-screen and small-screen exported HTML.

**Decision:** Use the native `IntersectionObserver` API to watch all `#msg-*` elements, dynamically updating the outline's active state.

**Impact:** Good performance (no scroll event dependency), no external dependencies. Downside is lack of IE support, but exported HTML targets modern browsers.

**Re-evaluation trigger:** Target browser requires IE support.
