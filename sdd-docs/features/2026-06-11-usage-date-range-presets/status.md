# Usage Date Range Presets — Status

**Feature:** Usage date range presets
**Current status:** planned
**Created:** 2026-06-11
**Last updated:** 2026-06-11
**Owner:** Local project maintainer

## Lifecycle

```text
draft -> approved -> planned -> implementing -> implemented -> validating -> validated -> shipped
```

Current position:

```text
planned
```

## Summary

The Usage page will add a compact preset bar with `今日`, `近7天`, and `近30天`. Preset selection updates the existing start and end date filters and reuses the existing Usage query flow.

`今日` includes today. `近7天` and `近30天` exclude today and cover fully completed calendar days before today.

## Confirmed

1. The preset bar appears between Usage sub-tabs and the filter grid.
2. The UI uses a compact single-row toolbar.
3. `今日` includes today.
4. `近7天` and `近30天` exclude today.
5. The default preset is `近7天`.
6. Manual dates highlight a preset only on exact match.
7. Queries reuse existing `from`, `to`, and `tz`; no backend range parameter is added.

## Completed Documentation

1. Requirements are recorded in `requirements.md` and `requirements_ZH.md`.
2. Decisions are recorded in `decisions.md` and `decisions_ZH.md`.
3. Implementation plans are recorded in `plan.md` and `plan_ZH.md`.
4. Validation checklists are recorded in `validation.md` and `validation_ZH.md`.

## Pending

1. Implement frontend UI, date synchronization logic, and tests.
2. Record verification evidence after implementation.
3. Move status to `validated` after verification passes.

## Blockers

None.
