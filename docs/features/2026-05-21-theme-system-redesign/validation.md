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

## Phase 2 Planned Validation

Run after the full-stack rollout:

```bash
rtk go test ./...
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
rtk docker compose up -d --build
```

Required backend checks:

- [ ] `GET /api/preferences` returns the persisted `theme_mode`.
- [ ] `PUT /api/preferences` accepts `light` and `dark`.
- [ ] `PUT /api/preferences` rejects invalid values with `400`.
- [ ] Preference routes require the existing admin session cookie.
- [ ] `admin_theme_mode` persists through SQLite-backed config storage.

Required frontend checks:

- [ ] Header switch appears near language/logout controls.
- [ ] Session-browser-local switch is removed.
- [ ] Global `data-theme` updates on switch.
- [ ] Backend preference restores after clearing `localStorage`.
- [ ] `localStorage` fallback applies when preference API fails.
- [ ] Theme switching does not reset active tab, open modal, provider/session selection, or scrollable panels.
- [ ] Login, status, providers, certificates, usage, sessions, modals, forms, tables, badges, and code blocks are readable in both themes.

## Manual Validation Checklist

- [x] Session browser opens by persisted preference after refresh.
- [x] Theme switch toggles between Light and Dark.
- [x] Theme selection persists after refresh.
- [x] Switching theme does not reset selected project.
- [x] Switching theme does not reset selected session.
- [ ] Left session list remains independently scrollable.
- [x] Desktop outline remains independently scrollable.
- [x] Cleanup command dialog remains readable in Dark mode.
- [x] User message blocks remain visually prominent and readable in the opened Dark-mode detail view.
- [ ] Export action still downloads HTML.
- [ ] Narrow viewport remains usable.

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
- Full Light-mode visual pass.
- Export download behavior.
- Narrow viewport pass.
```
