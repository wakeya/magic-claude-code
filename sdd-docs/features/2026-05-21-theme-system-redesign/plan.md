# Theme System Redesign Plan

**Goal:** Build a reusable full-stack Light/Dark theme foundation for the admin frontend, starting with a session browser pilot and then promoting the system to the whole dashboard.

**Architecture:** Add frontend theme state, semantic theme tokens, and session-browser theme-aware components first. Keep APIs unchanged in Phase 1. Phase 2 moves the switch to the app header, persists the preference through backend config storage, applies a global `data-theme` attribute, and rolls semantic tokens through shared dashboard surfaces.

**Tech Stack:** Vue 3, TypeScript, Tailwind CSS v4, Go admin API, existing SQLite-backed config storage, `localStorage` fallback.

---

## Phase 1: Session Browser Pilot

### Files

Modify:

1. `internal/frontend/src/styles/main.css`
2. `internal/frontend/src/components/SessionBrowser.vue`
3. `internal/frontend/src/components/SessionDetail.vue`
4. `internal/frontend/src/components/SessionOutline.vue`
5. `internal/frontend/src/components/SessionBrowserLayout.test.ts`

Create if useful:

1. `internal/frontend/src/composables/useTheme.ts`
2. `internal/frontend/src/utils/theme.test.ts`

### Implementation Tasks

1. Add a small theme composable with `light` and `dark` modes.
2. Persist selected mode in `localStorage`.
3. Apply a theme class or data attribute to the session browser root.
4. Define semantic session-browser theme tokens in CSS.
5. Restyle the session browser left panel, session cards, detail header, outline, empty state, and cleanup modal for both themes.
6. Keep user-message blocks green in both themes, with separate light/dark token values.
7. Keep cleanup command blocks code-editor styled in both themes.
8. Add a visible theme switch in the session browser page header.
9. Verify switching theme does not reset current project/session state.
10. Run frontend tests and build.

### Verification Commands

```bash
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

### Manual Verification

1. Open the admin UI.
2. Navigate to `会话记录`.
3. Switch between Light and Dark.
4. Verify selected project and selected session remain unchanged.
5. Verify session list scrolling, detail scrolling, outline scrolling, export button, and cleanup hint still work.
6. Refresh and confirm the selected theme persists.
7. Inspect desktop and narrow viewport layouts.

## Phase 2: Full-Stack Global Theme System

### Backend Files

Modify:

1. `internal/config/config.go`
2. `internal/config/sqlite_store.go`
3. `internal/config/store.go`
4. `internal/admin/server.go`
5. `internal/admin/handler.go` or a new focused `internal/admin/preferences_handler.go`

Add tests:

1. `internal/admin/preferences_handler_test.go`
2. `internal/config/sqlite_store_test.go`
3. `internal/config/store_test.go`

### Frontend Files

Modify:

1. `internal/frontend/src/composables/useTheme.ts`
2. `internal/frontend/src/composables/useTheme.test.ts`
3. `internal/frontend/src/composables/useApi.ts`
4. `internal/frontend/src/components/AppHeader.vue`
5. `internal/frontend/src/components/SessionBrowser.vue`
6. `internal/frontend/src/views/DashboardView.vue`
7. `internal/frontend/src/views/LoginView.vue`
8. `internal/frontend/src/styles/main.css`
9. Existing frontend layout/source tests as needed

### Backend Tasks

1. Add `AdminThemeMode string` to `config.Config` with `json:"admin_theme_mode"`.
2. Normalize accepted theme values to `light` or `dark`; default to `light`.
3. Persist `admin_theme_mode` in SQLite `settings`.
4. Preserve JSON config compatibility for legacy/fallback stores.
5. Add authenticated API routes:
   - `GET /api/preferences`
   - `PUT /api/preferences`
6. Return `400` for invalid theme values and `401` through existing auth middleware for unauthenticated requests.
7. Keep all unrelated config/provider/session APIs unchanged.

### Frontend Tasks

1. Promote `useTheme` from session-browser-local state to app-wide theme state.
2. Apply the active theme globally using `data-theme="light|dark"` on `document.documentElement` or the top app root.
3. Initialize theme synchronously from `localStorage` to avoid first-paint flicker.
4. After authenticated dashboard load, fetch `/api/preferences`, apply the backend theme, and mirror it into `localStorage`.
5. On theme switch, apply the selected theme immediately, write it to `localStorage`, and persist it with `PUT /api/preferences`.
6. If backend persistence fails, keep the local selection and retry/resync on the next app load.
7. Move the visible switch to `AppHeader.vue`, right side, near the language switcher and logout button.
8. Remove the temporary theme switch from `SessionBrowser.vue`.
9. Convert session-browser theme styles to inherit from the global theme state.
10. Apply shared semantic tokens to login, header, tabs, dashboard panels, providers, certificates, usage statistics, forms, modals, tables, badges, and code blocks.
11. Keep page state stable during theme switching.

### API Contract

```http
GET /api/preferences
200 OK
{
  "theme_mode": "light"
}
```

```http
PUT /api/preferences
Content-Type: application/json

{
  "theme_mode": "dark"
}

200 OK
{
  "success": true,
  "theme_mode": "dark"
}
```

Invalid input:

```http
PUT /api/preferences
{"theme_mode":"system"}

400 Bad Request
{"error":"invalid theme_mode"}
```

### Verification Commands

```bash
rtk go test ./...
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
rtk docker compose up -d --build
```

### Manual Verification

1. Log in and confirm the header switch is visible near language/logout controls.
2. Switch Light to Dark from the header.
3. Navigate across all dashboard tabs and confirm the selected theme stays active.
4. Refresh and confirm the backend-persisted theme is restored.
5. Clear `localStorage`, log in again, and confirm the backend preference is restored.
6. Temporarily simulate preference API failure and confirm `localStorage` fallback still applies the last local theme.
7. Confirm switching theme does not reset active tab, open modals, selected provider, selected session, or scrollable panels.
8. Check desktop and narrow viewport layouts.
