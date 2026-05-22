# Theme System Redesign Validation

## Automated Validation

Run after Phase 1 implementation:

```bash
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

2026-05-21 results:

```text
rtk npm --prefix internal/frontend test
Result: 6 passed, 0 failed

rtk npm --prefix internal/frontend run build
Result: Vite production build succeeded
```

Container validation:

```text
rtk docker compose up -d --build
Result: frontend production build completed and claude_code_proxy_dns restarted successfully
```

## Phase 2 Validation

2026-05-21 Phase 2 results:

```text
rtk go test ./...
Result: 288 passed in 8 packages

rtk npm --prefix internal/frontend test
Result: 31 passed, 0 failed

rtk npm --prefix internal/frontend run build
Result: Vite production build succeeded

rtk docker compose up -d --build
Result: container rebuilt and started
```

Backend checks:

- [x] `GET /api/preferences` returns the persisted `theme_mode`.
- [x] `PUT /api/preferences` accepts `light` and `dark`.
- [x] `PUT /api/preferences` rejects invalid values with `400`.
- [x] Preference routes require the existing admin session cookie.
- [x] `admin_theme_mode` persists through SQLite-backed config storage.

Frontend checks:

- [x] Header switch appears near language/logout controls.
- [x] Session-browser-local switch is removed.
- [x] Global `data-theme` updates on switch.
- [x] Backend preference restores after clearing `localStorage`.
- [x] `localStorage` fallback applies when preference API fails.
- [x] Theme switching does not reset active tab, provider/session selection, or scrollable panels.
- [x] Login, status, providers, certificates, usage, sessions, modals, forms, tables, badges, and code blocks are readable in both themes.

## Manual Validation Checklist

- [x] Session browser opens by persisted preference after refresh.
- [x] Theme switch toggles between Light and Dark.
- [x] Theme selection persists after refresh.
- [x] Switching theme does not reset selected project.
- [x] Switching theme does not reset selected session.
- [x] Left session list remains independently scrollable.
- [x] Desktop outline remains independently scrollable.
- [x] Cleanup command dialog remains readable in Dark mode.
- [x] User message blocks remain visually prominent and readable in the opened Dark-mode detail view.
- [x] Export action remains available from the session detail toolbar.
- [x] Narrow viewport remains usable.

## Visual Review Notes

Record observations after reviewing the implemented pilot:

```text
2026-05-21 browser smoke review:
- Logged in to https://localhost:8442 and opened the session browser.
- Confirmed Light/Dark switch is visible.
- Switched to Dark mode and confirmed localStorage stores claude-proxy-theme=dark.
- Reloaded and reopened the session browser; Dark preference persisted.
- Opened a session detail page; selected project/session stayed stable when switching themes.
- Opened the cleanup command dialog in Dark mode; syntax-colored command blocks were readable.

Pending product-owner visual review:
- Full Light-mode product taste pass.
```

```text
2026-05-21 Phase 2 browser validation:
- Logged in to https://localhost:8442 after rebuilding the container.
- Confirmed the header-level Light/Dark switch is visible beside language/logout.
- Switched to Dark mode and confirmed documentElement data-theme=dark.
- Confirmed localStorage stores claude-proxy-theme=dark.
- Cleared localStorage, reloaded, and confirmed the backend preference restored Dark mode and repopulated localStorage.
- Navigated to the session browser and opened a session detail page.
- Confirmed no session-browser-local theme switch is present.
- Confirmed the user message block background is green in Dark mode and readable.
- Opened the cleanup hint modal and confirmed the command block uses the dark code editor style with syntax-colored tokens.
- Confirmed the desktop outline panel uses overflow-y:auto with a bounded max height.
- Confirmed the usage overview chart reads app theme tokens for tooltip, legend, axis, grid, and series colors.
- Checked a narrow viewport and adjusted the shell/header to avoid layout breakage.
```
