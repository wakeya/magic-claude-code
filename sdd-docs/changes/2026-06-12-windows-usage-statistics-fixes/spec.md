# Change Spec: Windows Usage Statistics Reliability Fixes

Date: 2026-06-12
Status: validated
Related feature spec: `sdd-docs/features/2026-06-12-windows-usage-statistics-fixes/spec.md`

---

## Summary

This change fixes Windows-specific usage dashboard empty states by making the binary self-contained for timezone lookups and by recording streaming usage when terminal SSE events are observed.

## User Impact

Before this change, Windows users could see successful proxy logs and valid rows in `data/proxy.db`, while the Status and Usage pages still showed zero values. After this change, the dashboard can query the existing rows using the browser timezone and streaming requests are finalized without waiting for upstream EOF.

## Fixed

| Area | Fix |
|------|-----|
| Windows timezone support | Embed Go `time/tzdata` so IANA browser timezones such as `Asia/Shanghai` load in the Windows binary |
| Streaming usage persistence | Treat `message_stop` and `[DONE]` as SSE terminal events, so usage recording does not wait forever for upstream EOF |
| Terminal usage parsing | Merge usage fields from `message_stop` payloads before marking the stream complete |
| Windows binary package | Rebuild `bin/mcc-windows-amd64/mcc.exe` with backend fixes and current embedded frontend assets |

## Validation Evidence

| Check | Result |
|-------|--------|
| `go test ./...` | 328 passed |
| `npm --prefix internal/frontend test` | 45 passed |
| `git diff --check` | passed |
| Binary inspection | Windows exe contains `time/tzdata`, `Asia/Shanghai`, `IsComplete`, and `index-Dxc_BCfC.js` |

## Rollout Notes

1. Replace the Windows `mcc.exe` with the rebuilt binary.
2. Restart the service.
3. Hard refresh the browser if the admin page was already open.
4. Existing rows in `data/proxy.db` should become visible without clearing the database.

## Compatibility

- Existing Linux behavior is preserved.
- Existing date preset semantics are preserved.
- Existing EOF-based SSE fallback remains.
- The Windows binary size increases due to embedded timezone data.
