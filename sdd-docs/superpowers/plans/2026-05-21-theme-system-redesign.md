# Theme System Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Phase 1 session browser pilot for the full admin Light/Dark theme system.

**Architecture:** Add a small frontend theme composable with localStorage persistence, add semantic session-browser CSS tokens, and restyle the session browser components against those tokens. Keep backend APIs and exported HTML unchanged in this phase.

**Tech Stack:** Vue 3, TypeScript, Tailwind CSS v4, node:test, Vite.

---

## File Map

Create:

1. `internal/frontend/src/composables/useTheme.ts`: theme state, persistence helpers, and toggle API.
2. `internal/frontend/src/composables/useTheme.test.ts`: tests for storage normalization and persistence behavior.

Modify:

1. `internal/frontend/src/styles/main.css`: session-browser semantic theme tokens and scoped component classes.
2. `internal/frontend/src/components/SessionBrowser.vue`: theme root, explicit switch, theme-aware panels/cards/modals.
3. `internal/frontend/src/components/SessionDetail.vue`: theme-aware message surfaces.
4. `internal/frontend/src/components/SessionOutline.vue`: theme-aware outline items.
5. `internal/frontend/src/components/SessionBrowserLayout.test.ts`: source-level assertions for theme switch and scoped theme root.
6. `internal/frontend/src/composables/useI18n.ts`: Light/Dark labels.

## Task 1: Theme Composable

- [ ] **Step 1: Write failing tests**

Add tests covering invalid values falling back to `light`, persisted `dark`, and write behavior.

- [ ] **Step 2: Run RED**

Run:

```bash
rtk npm --prefix internal/frontend test -- --test-name-pattern theme
```

Expected: fail because `useTheme.ts` does not exist.

- [ ] **Step 3: Implement `useTheme.ts`**

Add `ThemeMode`, `normalizeThemeMode`, `readStoredTheme`, `persistThemeMode`, and `useTheme`.

- [ ] **Step 4: Run GREEN**

Run:

```bash
rtk npm --prefix internal/frontend test -- --test-name-pattern theme
```

Expected: pass.

## Task 2: Session Browser Theme Markup

- [ ] **Step 1: Extend source-level layout test**

Assert `SessionBrowser.vue` uses the theme composable, has a `data-theme` binding, and renders Light/Dark buttons.

- [ ] **Step 2: Run RED**

Run:

```bash
rtk npm --prefix internal/frontend test -- --test-name-pattern "session browser"
```

Expected: fail because theme markup is not present.

- [ ] **Step 3: Update `SessionBrowser.vue`**

Wrap the page in a `session-theme` root, add a header with explicit theme switch, preserve all existing state refs, and convert the main session browser surfaces to scoped theme classes.

- [ ] **Step 4: Run GREEN**

Run:

```bash
rtk npm --prefix internal/frontend test -- --test-name-pattern "session browser"
```

Expected: pass.

## Task 3: Theme Styles and Child Components

- [ ] **Step 1: Add scoped CSS tokens**

Add `.session-theme[data-theme="light"]` and `.session-theme[data-theme="dark"]` variables to `main.css`, plus scoped utility classes for session panels, cards, buttons, code blocks, and message surfaces.

- [ ] **Step 2: Update `SessionDetail.vue` and `SessionOutline.vue`**

Use the scoped session theme classes while preserving user-message green highlighting and outline click behavior.

- [ ] **Step 3: Run frontend test and build**

Run:

```bash
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

Expected: both pass.

## Task 4: Manual Verification

- [ ] **Step 1: Start/rebuild the app if needed**

Run Docker build only if manual browser verification is needed against the container:

```bash
rtk docker compose up -d --build
```

- [ ] **Step 2: Verify behavior**

Open the admin UI, navigate to `会话记录`, switch Light/Dark, refresh, and confirm the selected theme persists. Confirm switching theme does not reset selected project/session and that cleanup commands remain readable.

