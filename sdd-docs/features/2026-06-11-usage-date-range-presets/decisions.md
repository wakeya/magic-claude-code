# Usage Date Range Presets — Decisions

## D-001: Map presets to existing from/to filters in the frontend

**Date:** 2026-06-11
**Status:** accepted

**Context:** The Usage backend already supports `from`, `to`, and `tz`, and all Usage views share the same filter path. Adding a backend `range` parameter would expand API behavior and test scope.

**Decision:** Date presets are implemented as frontend mappings to the existing `usageFilters.from` and `usageFilters.to` fields. Queries continue to use the existing API parameters.

**Impact:** The change remains small, and every Usage view naturally reuses the existing refresh path. Preset semantics live in the frontend.

**Re-evaluation trigger:** Revisit this if multiple clients need to share the same preset semantics, or if the backend needs dedicated authorization, caching, or audit behavior for named ranges.

## D-002: Last 7 days and last 30 days exclude today

**Date:** 2026-06-11
**Status:** accepted

**Context:** Today's data is not complete until the day ends. Including today in `近7天` or `近30天` would mix a partial day into otherwise complete windows.

**Decision:** `近7天` and `近30天` exclude today and cover only fully completed calendar days. `今日` is the only preset that includes today.

**Impact:** Recent windows are more stable and easier to compare. Users explicitly select `今日` when they want current-day data.

**Re-evaluation trigger:** Revisit this if users request rolling windows that include today, such as "last 7 days including today".

## D-003: Do not highlight a preset for non-matching custom ranges

**Date:** 2026-06-11
**Status:** accepted

**Context:** Users still need arbitrary custom date ranges. Preset active state should mean "the current dates exactly equal this preset", not "the current dates roughly resemble this preset".

**Decision:** Manual dates highlight a preset only on an exact match. If the current dates do not match any preset, all preset buttons are unselected.

**Impact:** The UI state remains precise and avoids implying that a custom date range belongs to a preset.

**Re-evaluation trigger:** Revisit this if the UI later adds an explicit "custom" state or needs to show approximate relationships to presets.
