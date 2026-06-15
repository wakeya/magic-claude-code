# Version Display Fix Spec

Local page: Admin dashboard header  
Proxy entry: N/A (admin server :8442)  
Reference sources: `/api/status` (version field), `/api/update/check` (current_version field)  
Stack: Vue 3 + TypeScript  
Last updated: 2026-06-15  
Progress: 1 / 1 done

## Overall Analysis (Source Analysis)

### Bug Description

The header version label (`currentVersion` in `AppHeader.vue`) was bound to `updateInfo.value?.current_version`, which only gets populated when `/api/update/check` is called successfully. However, the update check is throttled to once per 24 hours per browser via localStorage. On subsequent page loads within the throttle window, `shouldCheckForUpdate()` returns `false`, `checkUpdate()` short-circuits, `updateInfo` stays `null`, and `currentVersion` falls back to `'dev'`.

### Root Cause

The design coupled two unrelated concerns into a single data source:

1. **Version display** — a static piece of information that should always be available.
2. **Update availability check** — a remote operation that is legitimately throttled.

By deriving the version label from `updateInfo.current_version`, the 24-hour throttle on update checks inadvertently caused the version label to disappear on every page reload after the first one.

### Data Flow Before Fix

```
Page load
  → checkUpdate()
    → shouldCheckForUpdate() == false (throttled)
    → return early
  → updateInfo stays null
  → currentVersion = updateInfo?.current_version || 'dev'
  → header shows 'dev'
```

### Data Flow After Fix

```
Page load
  → fetchStatusVersion()         (always called, not throttled)
    → GET /api/status
    → statusVersion = status.version
  → checkUpdate()                (throttled, only for update availability)
    → shouldCheckForUpdate() == false
    → return early (no impact on version display)
  → currentVersion = statusVersion.value
  → header shows correct version
```

### Backend Verification

Both endpoints derive the version from `internal/version.Version` in successful responses. `GET /api/status` always returns the `version` field. `GET /api/update/check` returns `current_version` only on success; on failure it may return only an `error` field without `current_version`.

The fix decouples version display from update checking, using the always-available `/api/status` endpoint.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Done | Decouple version display from update check | `useApi.ts`, `AppHeader.vue`, `AppHeader.test.ts` | Tests pass; build passes |

## Requirements

### Deliverables

1. `StatusInfo` interface in `useApi.ts` includes a `version?: string` field, matching the backend `/api/status` response.
2. `AppHeader.vue` fetches the version from `/api/status` on every page load via a dedicated `fetchStatusVersion()` function, independent of the update check throttle.
3. `currentVersion` computed property reads from `statusVersion` ref, not from `updateInfo.current_version`.
4. `onMounted` calls both `fetchStatusVersion()` (always) and `checkUpdate()` (throttled), in parallel.
5. A regression test in `AppHeader.test.ts` locks the version source to `/api/status` and prevents re-coupling to `updateInfo`.

### Constraints

1. The fix must not change the 24-hour update check throttle behavior — throttling applies only to update availability checks, not to version display.
2. `fetchStatusVersion()` must be best-effort and silent on failure — if `/api/status` fails, the version falls back to `'dev'`, same as before.
3. The update dialog still uses `updateInfo.current_version` and `updateInfo.latest_version` — this is correct because the dialog only opens after a successful update check.

### Edge Cases

1. `/api/status` is unreachable — version falls back to `'dev'`; no error shown to user.
2. Update check is throttled — version still displays correctly from `/api/status`.
3. Update check succeeds — `updateInfo` is populated for the dialog; version display is unaffected (already correct from `/api/status`).
4. Both `/api/status` and `/api/update/check` fail — version shows `'dev'`; no update notification shown.

### Related Specs

- [Auto-Update Spec](../2026-06-13-auto-update/spec.md) — Task 5 originally specified `currentVersion` derived from `updateInfo.current_version`. This fix corrects that design.

## Task Details

### Task 1: Decouple Version Display from Update Check

#### Requirements

**Objective** - Fix the regression where the header version label shows `dev` on page reloads within the 24-hour update check throttle window.

**Outcomes** - `StatusInfo` interface includes `version?: string`; `AppHeader.vue` fetches version from `/api/status` independently; `currentVersion` reads from `statusVersion`, not `updateInfo`; regression test added.

**Evidence** - `npm test` passes 50/50 including new regression test; `npm run build` succeeds.

**Constraints** - Do not change update check throttle logic; do not change the update dialog data source; `fetchStatusVersion` must be silent on failure.

**Edge Cases** - `/api/status` fails (falls back to `'dev'`); update check throttled (version still correct); both fail (version `'dev'`, no update badge).

**Verification** - Frontend tests pass; build passes.

#### Plan

1. Add `version?: string` to `StatusInfo` in `useApi.ts`.
2. Add `statusVersion` ref (default `'dev'`) and `fetchStatusVersion()` in `AppHeader.vue`.
3. Change `currentVersion` computed to read `statusVersion.value`.
4. Call `fetchStatusVersion()` in `onMounted` alongside `checkUpdate()`.
5. Add regression test in `AppHeader.test.ts`.

#### Verification

- [x] `StatusInfo` interface includes `version?: string`.
- [x] `currentVersion` reads from `statusVersion.value`, not `updateInfo.current_version`.
- [x] `fetchStatusVersion()` calls `api.getStatus()` and sets `statusVersion`.
- [x] `onMounted` calls both `fetchStatusVersion()` and `checkUpdate()`.
- [x] Regression test asserts version source is independent of update check.
- [x] `npm test` passes (50/50).
- [x] `npm run build` succeeds.
