# Plan

## Step 1: Modify Go Data Structures

**File:** `internal/session/export.go`

In `ExportHTML()`:

1. Add `OutlineItem` struct: `Index int`, `Preview string`, `Timestamp int64`
2. Add `outlineItems` helper function: iterate over `Messages`, filter role `user`, generate `[]OutlineItem`
3. Build `Preview` by collapsing whitespace and truncating at 50 characters
4. Inject `"OutlineItems": outlineItems(detail.Messages)` into the template data `map[string]any`
5. Add `previewText` function to template funcs
6. Add `formatTime` function to template funcs (format `YYYY-MM-DD HH:mm:ss`)

**Commit:** `feat: add OutlineItem struct and template data for export outline`

---

## Step 2: Modify HTML Template — Structure and Styles

**File:** `internal/session/export.go` (`exportTemplate` constant)

1. Wrap `<header>` in `<div class="layout">` containing `<main>` and `<aside>`
2. Move existing `<main>` content into `<main>` tag
3. Add `<aside class="outline-panel">` after `<main>` (visible on large screens)
4. Add `<button class="outline-toggle">` after `<header>` (floating button on small screens)
5. Add `<dialog class="outline-modal">` after `<aside>` (modal on small screens)
6. Add `<script>` tag before `</body>`
7. Add CSS for outline-related styles (outline-panel, outline-title, outline-item, outline-active, back-to-top, outline-toggle, outline-modal, etc.)

**Commit:** `feat: add outline panel HTML and CSS to export template`

---

## Step 3: Add Anchor IDs to Message Sections

**File:** `internal/session/export.go` (`exportTemplate` constant)

In the `{{range .Messages}}` loop, add `id="msg-{{.Index}}"` to `<section class="message {{.Role}}">`.

Note: Go templates change the scope of `.` during `range`; need to use `$index` to track the original index.

**Commit:** `feat: add anchor IDs to exported message sections`

---

## Step 4: Render Outline Data in Template

**File:** `internal/session/export.go` (`exportTemplate` constant)

In `<aside class="outline-panel">`, use Go template `{{range .OutlineItems}}` to render outline entries, each entry:
- `onclick` calls `jumpToMsg('msg-{Index}')`
- Display `{{previewText .Preview}}` (first 50 chars)
- Display formatted timestamp

Same rendering in `<dialog class="outline-modal">`.

**Commit:** `feat: render outline items in export template`

---

## Step 5: Add Interactive JS

**File:** `internal/session/export.go` (`exportTemplate` constant, `<script>` tag)

Implement three features:

1. **Click navigation** — `jumpToMsg(id)`: calls `document.getElementById(id).scrollIntoView({ behavior: 'smooth' })`, closes small-screen modal after jump
2. **Scroll auto-highlight** — `IntersectionObserver` watches all `section[id^="msg-"]`, adds `.outline-active` class to the corresponding outline item when element enters viewport
3. **Back to top** — `backToTop()`: calls `window.scrollTo({ top: 0, behavior: 'smooth' })`
4. **Small-screen modal** — floating button toggles `<dialog>` with `showModal()` / `close()`

**Commit:** `feat: add interactive JS for outline navigation and back-to-top`

---

## Step 6: Test and Validate

**Actions:**

1. Run `go test ./internal/session/...` to ensure existing tests pass
2. Manually export a few session HTML files of varying sizes
3. Open in browser and verify:
   - Outline panel displays correctly
   - Click-to-navigate and smooth scroll work
   - Scroll auto-highlight works
   - Back-to-top works
   - Responsive (small-screen modal) works
   - Light/dark theme works
4. Check file size growth is within expected bounds

**Commit:** `test: verify exported HTML outline functionality`

---

## Modified Files

| File | Action |
|------|--------|
| `internal/session/export.go` | Modify |
