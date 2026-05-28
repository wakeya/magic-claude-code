# Requirements

## 1. Overview

Add a right-side outline navigation panel and a back-to-top button to exported HTML session files, providing a browsing experience consistent with the online session detail page.

### Goals

- Exported HTML files are fully self-contained static files (no external dependencies, works offline)
- Outline displays summaries of every user message; clicking navigates to the message; scrolling auto-highlights the current item
- Responsive: fixed right panel on large screens, modal overlay on small screens

### Non-Goals

- No changes to the existing session record page (SessionBrowser.vue)
- No external dependencies introduced (CDN, frameworks, etc.)

---

## 2. Scope

### In Scope

- Modify `internal/session/export.go` to add outline panel HTML/CSS/JS in the export template
- Build outline data in Go and inject into the template
- Add `id="msg-{index}"` to each `<section>` for anchor navigation
- Responsive: large screen (≥1024px) fixed right panel, small screen (<1024px) modal overlay
- Theme follows the `theme` parameter passed at export time

### Out of Scope

- Changes to the session record page
- Export to PDF or other formats
- Server-side real-time preview

---

## 3. Acceptance Criteria

1. Exported HTML files have no external dependencies and can be opened directly in a browser
2. Outline panel displays previews (max 50 characters + `...`) for all user messages
3. Clicking an outline entry smoothly scrolls to the corresponding message
4. Scrolling the page auto-highlights the currently visible message's outline entry
5. Back-to-top button is always functional
6. At ≥1024px width: right panel is shown; at <1024px: modal overlay is shown
7. Theme (light/dark) follows the `theme` parameter passed at export time
8. File size growth stays within reasonable bounds (estimated <20KB increase)

---

## 4. Constraints

- Must reuse the existing CSS variable system (`--surface`, `--text`, `--accent`, `--border`, etc.)
- Must reuse the existing `data-theme` attribute mechanism
- JS embedded in Go templates must be vanilla JS with no framework dependencies
- No Tailwind; must use hand-written inline CSS
