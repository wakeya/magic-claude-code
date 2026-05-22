# Theme System Redesign Requirements

**Version:** 0.2
**Date:** 2026-05-21
**Status:** Phase 2 specification in review

---

## 1. Objective

Introduce a full application Light/Dark theme system for the admin frontend. Phase 1 has validated the direction on the `会话记录` page. Phase 2 promotes that pilot into a full-stack theme system: a global header switch, backend-persisted admin preference, local frontend fallback, and shared theme tokens applied across the dashboard.

Reference directions:

1. Light theme: `https://www.uupm.cc/demo/educational-platform`
2. Dark theme: `https://www.uupm.cc/demo/developer-tools`

## 2. Success Criteria

Phase 1 is successful when:

1. The session browser page supports an explicit Light/Dark switch.
2. The selected theme persists across reloads.
3. Light mode feels bright, rounded, calm, and approachable, inspired by the educational platform reference.
4. Dark mode feels like a modern developer tool: deep surfaces, code-editor contrast, restrained cyan/green accents, and clear syntax/code affordances.
5. Existing session browser workflows continue to work: project selection, session list scrolling, detail view, user-message outline scrolling, export, and cleanup command hints.
6. The implementation creates reusable theme primitives that can later be promoted to the full dashboard without rewriting the session browser again.

Phase 1 is not considered complete if it only swaps colors but leaves the session browser with mismatched spacing, borders, focus states, and command/code treatments.

Phase 2 is successful when:

1. The Light/Dark switch is moved to the top app header, positioned on the right side near the language switcher and logout button.
2. The header switch is the single global theme entry point; the temporary session-browser-local switch is removed.
3. The selected theme is persisted by the backend after login and survives browser/device changes that use the same admin data store.
4. `localStorage` remains as a fast startup and offline/failure fallback, so the UI can apply the last known theme before the preference API returns.
5. All dashboard pages use the same semantic theme tokens: login, header, tabs, providers, certificates, usage statistics, sessions, modals, forms, tables, badges, and code blocks.
6. Switching themes does not reset the active tab, loaded data, current modal state, selected provider, selected session, or scrollable panel state.
7. Existing API behavior unrelated to theme preferences remains unchanged.

## 3. Scope

### 3.1 Phase 1: Session Browser Pilot

Apply the theme system only to:

1. `SessionBrowser.vue`
2. `SessionDetail.vue`
3. `SessionOutline.vue`
4. Session cleanup command code blocks
5. Session browser empty/loading/error states
6. Theme switch control

The switch may live in the session browser header for the pilot, but it must be designed so it can move to `AppHeader.vue` during the full rollout.

### 3.2 Phase 2: Full-Stack Full Rollout

After the pilot is accepted, extend the same theme system to:

1. Login page
2. App header and navigation tabs
3. Providers pages and modals
4. Certificates page
5. Usage statistics filters, cards, charts, and tables
6. Shared buttons, inputs, selects, badges, tables, modals, and code blocks
7. Backend preference API and storage
8. Frontend startup synchronization between backend preference and `localStorage`

The final theme switch location is `AppHeader.vue`, in the right-side action area, visually grouped with the language switcher and logout button.

## 4. Theme Model

Theme mode values:

| Mode | Meaning |
|------|---------|
| `light` | Educational-platform inspired light UI |
| `dark` | Developer-tools inspired dark UI |

Phase 1 uses a simple persisted frontend preference in `localStorage`.

Phase 2 must use backend persistence with local fallback:

1. Store the admin theme preference in the existing configuration storage.
2. Use `localStorage` for immediate first paint and as fallback if the preference API fails.
3. Accept only `light` and `dark` values. Unknown, missing, or invalid values fall back to `light`.
4. Do not introduce a `system` mode in Phase 2. The admin UI should remain explicit and predictable.

Backend preference API:

| Method | Path | Request | Response |
|--------|------|---------|----------|
| `GET` | `/api/preferences` | none | `{ "theme_mode": "light" \| "dark" }` |
| `PUT` | `/api/preferences` | `{ "theme_mode": "light" \| "dark" }` | `{ "success": true, "theme_mode": "light" \| "dark" }` |

Both endpoints require the existing admin session cookie. Invalid `theme_mode` returns `400`.

Backend storage:

1. Extend `config.Config` with `AdminThemeMode string`.
2. Persist the value as `admin_theme_mode` in the existing SQLite `settings` table.
3. Preserve legacy JSON compatibility by adding `json:"admin_theme_mode"` to the config field.
4. Default to `light` when no persisted preference exists.

Theme should be expressed with reusable CSS variables or semantic utility classes, not hard-coded one-off colors spread through every component. Phase 2 should apply the active theme globally using a stable attribute such as `data-theme="light|dark"` on the document root or top app root.

Required semantic token groups:

| Token Group | Purpose |
|-------------|---------|
| App background | Page-level backdrop |
| Surface | Main cards, panels, dialogs |
| Surface raised | Selected cards, popovers, focused areas |
| Border | Normal and emphasized borders |
| Text | Primary, secondary, muted text |
| Accent | Primary action and selected state |
| Success/User | User message blocks |
| Warning | Cleanup hints and caution states |
| Code | Command/code editor surfaces and token colors |

## 5. Visual Direction

### 5.1 Light Theme

Light mode should:

1. Use a bright off-white/blue-tinted background instead of flat gray.
2. Use white translucent or soft panels with subtle blue borders.
3. Use rounded but controlled corners; cards should stay professional and not become oversized marketing blocks.
4. Use blue as the main action/selection accent and green for user-message highlights.
5. Keep information density suitable for repeated admin work.
6. Preserve existing readable typography using the project's Outfit font.

### 5.2 Dark Theme

Dark mode should:

1. Use deep navy/slate backgrounds, not pure black.
2. Use layered dark surfaces with visible but restrained borders.
3. Use cyan/sky accents for selected states and primary actions.
4. Keep green user-message blocks readable in dark mode without becoming neon.
5. Render cleanup commands in a dark code-editor style with clear token colors.
6. Maintain accessible contrast for text, focus states, and controls.

## 6. Interaction Requirements

1. Phase 1 theme switch is explicit and visible on the session browser page.
2. Phase 2 theme switch is explicit and visible in the top app header, near the language switcher and logout button.
3. Switching theme must not reset selected project, selected session, scroll position, loaded detail state, active tab, open modal, or provider form state.
4. Theme preference persists across reloads and authenticated sessions.
5. Keyboard focus states must be visible in both themes.
6. Existing copy/export/cleanup interactions must remain unchanged.
7. If the backend preference update fails, the UI keeps the locally selected theme, stores it in `localStorage`, and can retry or resync on the next app load.

## 7. Non-Goals

1. Do not redesign backend APIs in Phase 1.
2. Do not redesign every dashboard page in Phase 1.
3. Do not introduce multi-user roles or per-user accounts solely for theme preferences.
4. Do not introduce a heavy component library.
5. Do not add animation-heavy or marketing-style hero sections to the admin dashboard.
6. Do not use external runtime assets from the reference sites.
7. Do not add a `system` theme mode in Phase 2.

## 8. Compatibility

1. The pilot must keep the existing Vue 3 + Tailwind CSS v4 stack.
2. Theme styles must work with the existing embedded frontend build.
3. The session browser must remain usable on desktop and smaller screens.
4. Dark mode should not break exported HTML; export styling remains controlled by the backend export template unless explicitly changed in a later phase.
5. The preference API must be protected by the same admin authentication middleware as the existing dashboard APIs.
