# Session Detail Enhancements — Status

**Date**: 2026-05-22
**Status**: Shipped

## Implementation Summary

All 26 requirements (R001–R026) implemented and verified.

## Test Results

- Go test suite: 46 passed in 3 packages (session: 26, admin: 18, others: 2)
- Frontend build: successful (Vite 550–590ms)
- No new test files added for the Accept-Encoding fix (verified via manual testing against MiniMax API)
- Project name inference fix: 3 new test cases (1 unit test for `projectNameFromDir` + 2 integration tests for fold)
- Null messages fix: backend nil → `[]` conversion, frontend `|| []` guard

## Performance Impact

| Metric | Before | After |
|--------|--------|-------|
| Session list scan | ~3520 lines read (head/tail) | ~3520 lines read (unchanged) |
| Session detail load | Full file parse | Full file parse + `message_count` field (negligible) |
| Proxy upstream SSE | Transparent passthrough | `Accept-Encoding` stripped; no compression overhead |
| Project name inference | Single `inferProjectRoot` call | Filter invalid paths then infer + directory name fallback (no extra I/O) |

The initial `countMessages` implementation (which read all 41,685 lines during scan) was identified and reverted within the same session. Net performance impact is zero.

## Manual Verification

- [x] JSONL filename displays in correct position (between project path and timestamp)
- [x] Copy button copies full `source_path`, shows green check for 1.2s
- [x] Assistant messages show blue left border
- [x] Tool/system messages show amber left border
- [x] Sidebar message count updates to accurate value after selecting a session
- [x] Icon buttons visible in both light and dark themes
- [x] GitHub link appears on login page (top-right) and app header (before theme toggle)
- [x] Both GitHub links open in new tab
- [x] MiniMax SSE usage extraction: `usage_source=provider, parse_status=ok` for all requests
- [x] Zhipu GLM SSE usage extraction: still works correctly (no regression)
- [x] Large requests (100+ messages) correctly extract usage from MiniMax
- [x] Sessions without `cwd` show correct project name when sibling sessions in same directory have valid CWD
- [x] All sessions in a directory missing CWD fall back to directory name's last segment (instead of "Unknown Project")
- [x] Clicking a session with 0 messages no longer triggers a console error
