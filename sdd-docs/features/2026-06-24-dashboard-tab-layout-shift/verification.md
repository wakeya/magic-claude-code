# Dashboard Tab Layout Shift Verification

Date: 2026-06-24

## Automated Verification

- `npm --prefix internal/frontend test` — pass, 88/88 tests.
- `npm --prefix internal/frontend run build` — pass.
- `go test ./...` — pass, 756 tests across 14 packages.

## Browser Verification

Environment: Vite dev server on `127.0.0.1:5174`, API responses mocked in DevTools.

- Stable tab to stable tab: no visible header/tab movement.
- Status, certificates, and sessions measured the same layout bounds:
  - `body.clientWidth`: 1852 on all three tabs.
  - Header bounds: `left=0`, `right=1852` on all three tabs.
  - Tab block bounds: `left=150`, `right=818` on all three tabs.
  - `body.scrollWidth === body.clientWidth`, so no horizontal scrollbar.
- Sessions preload completed before activation: switching to sessions rendered 12 session cards with no skeleton/empty intermediate state.
- User refresh in sessions: skeleton appeared with 7 placeholder rows, then returned to 12 session cards.
- Project switch in sessions: skeleton appeared during reload, then returned to 6 filtered session cards.
- Slow first-load probe: when sessions was opened before preload completed, samples at 100ms and 1000ms showed 7 skeleton rows and 0 session cards; after data returned, skeleton disappeared and 8 session cards rendered.

## Notes

- In Chrome, `document.documentElement.clientWidth` still reports the full viewport width on short pages even while the body layout gutter is reserved. The user-visible layout is stable; header and tab block DOM bounds do not shift.
- macOS overlay scrollbar behavior was not directly available in this Linux browser environment; the CSS uses `scrollbar-gutter: stable` with `overflow-y: auto`, not `overflow-y: scroll`.
