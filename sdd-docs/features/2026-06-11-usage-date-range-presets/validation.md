# Usage Date Range Presets — Validation Checklist

**Feature:** Usage date range presets
**Status:** planned
**Last updated:** 2026-06-11

## Acceptance Criteria

1. **Placement:** The preset bar appears between the Usage sub-tabs and the filter grid.
2. **Default range:** The Usage page defaults to `近7天`.
3. **Last 7 days semantics:** `近7天` excludes today, ends yesterday, and starts 6 days before yesterday.
4. **Last 30 days semantics:** `近30天` excludes today, ends yesterday, and starts 29 days before yesterday.
5. **Today semantics:** `今日` sets both start and end date to today.
6. **Synchronization and refresh:** Clicking any preset synchronizes start/end dates and refreshes Overview, Requests, Provider, Models, and Usage Coverage data.
7. **Filter preservation:** Clicking a preset does not reset Provider, model, status, Usage source, statistics scope, source entrypoint, or search query.
8. **Pagination behavior:** Request log pagination returns to page one after date changes.
9. **Manual exact match:** Manually selected dates that exactly match a preset highlight that preset.
10. **Manual custom range:** Manually selected dates that do not match any preset leave all preset buttons unhighlighted.
11. **Responsive layout:** The preset bar does not overflow or overlap controls on small screens.

## Automated Verification

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

## Manual Verification Suggestions

### Scenario 1: Initial page load

1. Open the admin page and navigate to Usage.
2. Expected: The preset bar appears between the sub-tabs and filters.
3. Expected: `近7天` is highlighted.
4. Expected: Start and end dates match 7 complete calendar days excluding today.

### Scenario 2: Switch presets

1. Click `今日`.
2. Expected: Start and end dates both equal today.
3. Click `近30天`.
4. Expected: End date is yesterday and start date is 29 days before yesterday.
5. Expected: Usage data reloads.

### Scenario 3: Manual dates

1. Manually enter a date range that does not match any preset.
2. Expected: All preset buttons are unhighlighted.
3. Manually enter a date range that exactly equals `近7天`.
4. Expected: `近7天` is highlighted.

## Verification Evidence

To be recorded after implementation.
