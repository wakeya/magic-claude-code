# Usage Date Range Presets — Implementation Plan

**Goal:** Add a compact date range preset bar to the Usage page and reuse the existing `from`, `to`, and `tz` query path to refresh all Usage views.

**Architecture:** Frontend-only change. `DashboardView.vue` maps presets to the existing `usageFilters.from` and `usageFilters.to` fields. Backend APIs and storage remain unchanged.

---

## File Plan

Modify:

1. `internal/frontend/src/views/DashboardView.vue`
2. `internal/frontend/src/composables/useI18n.ts`
3. `internal/frontend/src/views/DashboardUsageRequests.test.ts`

If build artifacts are updated:

1. `internal/frontend/dist/*`

## Task 1: Date preset state and calculation

- [ ] Define preset keys: `today`, `last_7_days`, `last_30_days`.
- [ ] Add local-date formatting that avoids UTC `toISOString()` date shifts.
- [ ] Implement `presetDateRange(key)`:
  - `today`: today to today.
  - `last_7_days`: 7 complete calendar days ending yesterday.
  - `last_30_days`: 30 complete calendar days ending yesterday.
- [ ] Change the default `usageFilters.from/to` to `last_7_days`.
- [ ] Add a computed active preset derived from current `usageFilters.from/to`.

## Task 2: Preset UI

- [ ] Insert a compact toolbar between the Usage sub-tab switcher and the filter grid.
- [ ] Show `时间范围`, `今日`, `近7天`, and `近30天`.
- [ ] Reuse the existing button visual language for active state.
- [ ] Leave all preset buttons unhighlighted when no preset matches.
- [ ] Keep the toolbar responsive and prevent text overflow on mobile.

## Task 3: Click synchronization and refresh

- [ ] Update `usageFilters.from` and `usageFilters.to` when a preset is clicked.
- [ ] Preserve all other filters.
- [ ] Reuse the existing `watch(usageFilters)` behavior to reset request pagination and refresh data.
- [ ] Confirm manual date edits are not overwritten by preset logic.

## Task 4: i18n and tests

- [ ] Add Chinese and English i18n keys:
  - `usage.date_range`
  - `usage.range_today`
  - `usage.range_last_7_days`
  - `usage.range_last_30_days`
- [ ] Extend frontend source tests to check the preset bar, i18n keys, default date semantics, and click handler presence.
- [ ] If date calculation can be extracted cleanly, add deterministic unit tests.

## Task 5: Verification

- [ ] `npm --prefix internal/frontend test`
- [ ] `npm --prefix internal/frontend run build`
- [ ] Browser-check the Usage page:
  - `近7天` is selected by default.
  - Clicking each preset synchronizes start/end dates.
  - Manual custom dates leave all preset buttons unhighlighted.
  - Date changes refresh Usage data.

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| `toISOString()` shifts local dates across time zones | Format `YYYY-MM-DD` with local `getFullYear()`, `getMonth()`, and `getDate()`. |
| Presets overwrite manual date input | Only write dates on preset clicks; derive active state with a computed property. |
| Filter changes cause duplicate requests | Reuse the existing 250ms debounce watcher. |
| Toolbar is too wide on mobile | Use flex wrap and compact button sizing. |
