# Usage Date Range Presets Requirements

**Version:** 1.0
**Date:** 2026-06-11
**Status:** planned
**Lifecycle:** planned

---

## 1. Objective

Add a compact date range preset bar to the Usage page between the Usage sub-tab switcher and the existing filter controls. The bar must provide three presets:

1. `今日`
2. `近7天`
3. `近30天`

When a user selects a preset, the page must synchronize the existing start and end date fields, then refresh Usage data for all Usage sub-pages:

1. Overview
2. Requests
3. Provider
4. Models
5. Usage Coverage

## 2. Background

The Usage page already supports date filtering through the existing `from`, `to`, and `tz` query parameters. Users can manually edit the start and end date fields, but common recent time windows require repetitive date entry.

This feature lowers the cost of common date filtering while preserving the existing date inputs and API query path. It should not introduce a new backend range concept.

## 3. Requirements

### 3.1 Placement and Layout

The preset bar must appear between the Usage sub-tab switcher and the filter grid.

Layout requirements:

1. Use a compact single-row toolbar.
2. Do not use a tall card or long explanatory copy.
3. Display a short label and three preset buttons, for example:

```text
时间范围  今日  近7天  近30天
```

### 3.2 Date Range Semantics

Date ranges are calculated as calendar days in the browser's current time zone. The frontend must continue sending the existing `tz` value to the backend.

`今日`:

1. Includes today.
2. Sets the start date to today.
3. Sets the end date to today.
4. The existing backend parser converts the `to` date into the exclusive upper bound at the next local midnight.

`近7天`:

1. Does not include today.
2. Uses the 7 fully completed calendar days before today.
3. Sets the end date to yesterday.
4. Sets the start date to 6 days before yesterday.

`近30天`:

1. Does not include today.
2. Uses the 30 fully completed calendar days before today.
3. Sets the end date to yesterday.
4. Sets the start date to 29 days before yesterday.

Example when the browser date is `2026-06-10`:

| Preset | Start date | End date | Notes |
|--------|------------|----------|-------|
| 今日 | 2026-06-10 | 2026-06-10 | Today only |
| 近7天 | 2026-06-03 | 2026-06-09 | Excludes today; 7 complete calendar days |
| 近30天 | 2026-05-11 | 2026-06-09 | Excludes today; 30 complete calendar days |

### 3.3 Default State

The Usage page must default to `近7天`.

The initial start and end dates must follow the `近7天` semantics: the 7 fully completed calendar days before today, excluding today.

### 3.4 Preset Click Behavior

When a user clicks a preset:

1. Update `usageFilters.from`.
2. Update `usageFilters.to`.
3. Preserve all other filters, including Provider, model, status, Usage source, statistics scope, source entrypoint, and search query.
4. Reuse the existing filter watcher to refresh Usage data.
5. Reset the request log pagination to the first page through the existing filter-change behavior.

### 3.5 Manual Date Editing and Active State

When the user manually edits the start date or end date:

1. If the current dates exactly match a preset, highlight that preset.
2. If the current dates do not match any preset, no preset button is highlighted.
3. Custom date ranges must remain allowed.
4. Custom date ranges must not be forced back into a preset.

### 3.6 Query Path

This feature must reuse the existing API parameters:

```text
from
to
tz
```

Do not add a backend `range` parameter. The frontend is responsible for mapping presets to the existing start and end date fields.

## 4. Non-Goals

1. Do not add a backend API parameter.
2. Do not change backend `from` and `to` exclusive-upper-bound parsing.
3. Do not add more presets such as week, month, quarter, or custom saved ranges.
4. Do not change Provider, model, status, Usage source, statistics scope, source entrypoint, or search filtering semantics.
5. Do not change Usage aggregation, deduplication, or statistics scope behavior.
6. Do not add chart types or report export behavior.

## 5. Success Criteria

1. The Usage page shows a compact preset bar between the Usage sub-tabs and the filter grid.
2. The default preset is `近7天`, and the default date range excludes today.
3. Clicking `今日` synchronizes both date fields to today and refreshes all Usage views.
4. Clicking `近7天` synchronizes the date fields to the 7 complete calendar days before today and refreshes all Usage views.
5. Clicking `近30天` synchronizes the date fields to the 30 complete calendar days before today and refreshes all Usage views.
6. Manually selected dates that exactly match a preset highlight that preset.
7. Manually selected dates that do not match a preset leave all preset buttons unselected.
8. Existing filter behavior and request log pagination behavior remain unchanged.
9. Frontend tests and frontend build pass.
