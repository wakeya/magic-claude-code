# Dashboard Tab Layout Shift Fix Spec

Local page: Admin dashboard (`DashboardView.vue`) tab switching  
Proxy entry: N/A (admin server :8442)  
Reference sources: internal frontend — `DashboardView.vue`, `SessionBrowser.vue`, `SessionDetail.vue`, `AppHeader.vue`, `styles/main.css`  
Stack: Vue 3 (Composition API) + Tailwind CSS v4  
Last updated: 2026-06-24  
Progress: 0 / 4 planned

## Overall Analysis (Source Analysis)

### Symptom (Two Distinct Visual Defects)

When switching between the six admin-dashboard tabs (status / providers / connection / certs / usage / sessions), users observed two separate visual defects:

1. **Smooth left-shift by one scrollbar width** — acceptable, but present. Occurs when switching between `certs`/`sessions` and other tabs. This is a single reflow caused by the browser vertical scrollbar appearing/disappearing (~15px on Windows/Linux).
2. **Visible "repaint-like" judder** — the real complaint. Occurs **every time** when switching from the four stable tabs (status / providers / connection / usage) to `sessions`. Feels like the sessions page is redrawn.

### Root Cause 1 — Scrollbar Reflow (Defect 1)

- `body` (`main.css:20`) and `html` set only font/background/color. A project-wide search finds **no** `overflow-y: scroll` and **no** `scrollbar-gutter`. → The browser uses the default "on-demand scrollbar" behavior.
- `AppHeader.vue:2` is an in-flow `<header class="flex flex-wrap items-center justify-between">` (not `fixed`/`sticky`); its width tracks `viewport − padding` directly, so a ~15px viewport-width change redistributes the left/center/right groups → visible shift. The `w-fit` tab block sits inside an `mx-auto` container (`DashboardView.vue:5-6`), whose auto margins also shift with viewport width.
- Tabs split into two groups by whether `html` shows a vertical scrollbar:
  - **Group A (no scrollbar, content ≤ viewport):** status, providers, connection, usage, certs.
  - **Group B (scrollbar present):** sessions — `SessionDetail.vue:2` has **no** `max-h`/`overflow` cap and renders the entire conversation, so selecting any session inflates `html` height and forces the scrollbar on.
- Cross-group switches (A↔B) flip the scrollbar state → reflow → shift. Same-group switches do not flip → no shift. This explains the observed grouping exactly.

### Root Cause 2 — Sessions Async Re-layout (Defect 2)

- All six tabs use `v-if` (`DashboardView.vue:22/123/170/378/405/757`), so switching away **destroys** the component and switching back **recreates** it.
- The four stable tabs preload their data in `DashboardView.onMounted` via `Promise.all([loadStatus(), loadProviders(), loadCerts(), loadConnectionMode()])` + `loadUsageData()` (`DashboardView.vue:1707-1708`). When activated, content is already in place → single-frame render → no second layout.
- `sessions` is the **only** tab whose data is fetched by the child component itself: `SessionBrowser.onMounted → reload()` (`SessionBrowser.vue:227-229`) issues `getSessionProjects()` + `loadSessions()` (`getSessionList`) (`SessionBrowser.vue:235-236`, two network round-trips). Because of `v-if`, **every** activation is a fresh mount → fresh request → the list goes from empty to full after data arrives → a second layout pass → the "repaint" judder. "Every time, even the second time" is the signature of `v-if` destroy/recreate + per-mount refetch.

### Why `sessions` Is Structurally Different

| Aspect | Four stable tabs | `sessions` |
| --- | --- | --- |
| Data source | `DashboardView.onMounted` preload | Child `SessionBrowser.onMounted` fetch |
| On activation | Data ready, single-frame render | Empty → fetch → fill, second layout |
| `html` scrollbar | Determined by content height; stable group does not flip | Forced on once a session is selected (`SessionDetail` unbounded) |

### Chosen Remedy (Three Layers)

1. **Layer 1 — `html { scrollbar-gutter: stable }`**: eliminates Defect 1 by reserving a stable gutter so `html` width is constant regardless of scrollbar presence/absence.
2. **Layer 2 — preload sessions list data into `DashboardView` (B2)**: aligns `sessions` with the other tabs — `projects` + initial `sessions` list are fetched during `DashboardView.onMounted` and passed to `SessionBrowser` via props, so activation renders the list in a single frame (no empty→full second layout). `SessionDetail` remains loaded on demand (clicking a session), which is the intended interaction.
3. **Layer 3 — skeleton placeholder for the sessions list**: a lightweight skeleton (Tailwind `animate-pulse`) replaces the current plain-text loading state (`SessionBrowser.vue:33`). It covers the residual first-load window (preload not yet finished) and user-triggered reloads/project switches, preventing empty→full jumps. The project currently has no skeleton precedent, so this is new but minimal.

### Risk Summary

1. `scrollbar-gutter: stable` leaves a ~15px same-color gutter on short pages (LoginView, stable tabs) — it shows `--app-bg`, matching `.app-shell`'s right edge, so visually near-invisible. No `100vw`/`w-screen` usage exists in the project, so no horizontal-overflow trap.
2. On macOS (overlay scrollbars), `scrollbar-gutter` is a no-op and changes nothing.
3. Promoting sessions data to `DashboardView` changes `SessionBrowser`'s data ownership (self-managed `projects`/`sessions` → props). Per the project's shared-state field rule, all readers of these fields (`reload`, `loadSessions`, `selectProject`, `totalSessions`, template list render) must be updated consistently.
4. Preload failure must not break other tabs; `sessions` must degrade gracefully (skeleton → error state).
5. `SessionDetail` unbounded rendering is **not** in scope (no virtual scroll / pagination) — that is a separate performance topic. The scrollbar it forces is handled by Layer 1.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | Layer 1 — stable scrollbar gutter | `internal/frontend/src/styles/main.css` | Build; confirm no horizontal scrollbar; macOS unchanged |
| 2 | Planned | Layer 2 — preload sessions list in `DashboardView` | `DashboardView.vue`, `SessionBrowser.vue` | Unit tests; manual switch stable-tab → sessions shows no repaint |
| 3 | Planned | Layer 3 — sessions list skeleton | `SessionBrowser.vue` (or new `SessionListSkeleton.vue`) | Build; first-load shows skeleton, no empty→full jump |
| 4 | Planned | Test, build, and manual verification | Test/build green; verification record | `npm test` + `npm run build`; cross-tab switch matrix |

## Requirements

### Deliverables

1. `html { scrollbar-gutter: stable }` is added to `internal/frontend/src/styles/main.css` so the document width stays constant whether or not a vertical scrollbar is present.
2. `DashboardView.onMounted` preloads sessions list data (`getSessionProjects()` + `getSessionList({ page: 1, page_size: 100 })`) alongside the existing `Promise.all`, and exposes `sessionProjects` / `sessionList` / `sessionsLoading` reactive state.
3. `SessionBrowser` receives `projects` / `sessions` / `loading` via props (initial data from the parent preload) and **no longer** auto-calls `reload()` in its own `onMounted`. It retains `reload()` (refresh button), `loadSessions()`, `selectProject()`, `selectSession()` (on-demand detail), export, and cleanup logic for user-triggered interactions.
4. A skeleton placeholder (Tailwind `animate-pulse`) replaces the plain-text loading state in the sessions list area, with a reserved height close to the final list height to avoid skeleton→list jumps.
5. Frontend unit tests (`npm --prefix internal/frontend test`) and build (`npm --prefix internal/frontend run build`) pass; existing tests (`SessionBrowserLayout.test.ts`, `DashboardUsageRequests.test.ts`, etc.) are not broken.
6. Manual verification confirms: stable-tab ↔ stable-tab no shift (regression); stable-tab → sessions no repaint judder; certs ↔ sessions only the (now gutter-stabilized) transition; first-load shows skeleton; macOS unchanged.

### Directory Structure

```text
internal/frontend/src/
  styles/
    main.css                          (modify: add html { scrollbar-gutter: stable })
  views/
    DashboardView.vue                 (modify: preload sessions list, pass props, skeleton wiring)
  components/
    SessionBrowser.vue                (modify: accept props, drop onMounted auto-reload, skeleton)
    SessionListSkeleton.vue           (new: lightweight list skeleton, optional standalone component)
```

### Data Model

No persistent data model changes. Frontend-only reactive state additions in `DashboardView.vue`:

```ts
// DashboardView.vue (script setup)
const sessionProjects = ref<SessionProject[]>([])
const sessionList = ref<SessionItem[]>([])
const sessionsLoading = ref(false)

// onMounted: extend the existing Promise.all
await Promise.all([
  loadStatus(),
  loadProviders(),
  loadCerts(),
  loadConnectionMode(),
  loadSessionsList(),   // new: populates sessionProjects + sessionList
])
```

`SessionBrowser.vue` props:

```ts
const props = defineProps<{
  projects: SessionProject[]
  sessions: SessionItem[]
  loading: boolean
}>()
```

### Constraints

1. Layer 1 uses `scrollbar-gutter: stable` on `html` (not `overflow-y: scroll`) to avoid an empty visible scrollbar track on short pages.
2. Layer 2 preloads only the sessions **list** (`projects` + initial `sessions`). `SessionDetail` (full conversation) remains loaded on demand when a user clicks a session — preloading all details is out of scope and unnecessary.
3. `SessionBrowser` must keep all existing interactions working after the data-ownership change: refresh button (`reload`), project switch (`selectProject` → reload list for that project), session select (`getSessionDetail`), export, cleanup hint, copy path, back-to-top, outline jump.
4. When `SessionBrowser` reloads/switches project on its own (user-triggered), it updates its local reactive copy and may emit a `refreshed` event so the parent can stay in sync; the parent preload is the **initial** source only.
5. Preload failure of sessions must be contained: `sessionsLoading=false`, an error state is shown inside `sessions`, and other tabs are unaffected.
6. The skeleton must reserve a height close to the real list (e.g. several placeholder rows within the existing `max-h-[calc(100vh-260px)]` container) so the skeleton→list transition does not itself cause a layout jump.
7. No global state store (pinia) is introduced; data flows via props/events, consistent with the current composable + component-local-state architecture.
8. `SessionDetail` unbounded rendering and virtual scrolling are explicitly out of scope.

### Edge Cases

1. Sessions preload not finished when the user first opens `sessions` → skeleton shows until data arrives; no empty→full jump.
2. Sessions preload fails (API error) → error state inside `sessions`; other tabs unaffected.
3. No sessions / no projects exist → empty states render as today.
4. Slow network on first load → skeleton persists; once data arrives, single-frame fill.
5. User clicks refresh / switches project inside `sessions` → local reload with skeleton/loading state; parent state updated via event.
6. macOS overlay scrollbars → `scrollbar-gutter` is a no-op; no visual change, no regression.
7. `fixed inset-0` modals (ProviderModal, update dialog, import preview) → per CSS spec, `position: fixed` ICB is unaffected by `scrollbar-gutter`; modals still cover the viewport.
8. LoginView (short page) → right-side gutter shows `--app-bg`, matching the shell background; near-invisible.
9. Theme toggle (light/dark) → gutter color follows `--app-bg` via the theme variables; no mismatch.

### Non-Goals

1. Do not change `SessionDetail` to bounded-height / internal-scroll / virtual scrolling — separate performance topic; the scrollbar it induces is handled by Layer 1.
2. Do not convert tabs from `v-if` to `v-show`/`KeepAlive` — Layer 2 (preload) addresses the root cause for `sessions` without changing the shared tab mechanism.
3. Do not change `AppHeader` to `fixed`/`sticky`.
4. Do not introduce pinia or a global store.
5. Do not preload `SessionDetail` for all sessions.

## Task Details

### Task 1: Stable Scrollbar Gutter

#### Requirements

**Objective** - Eliminate the scrollbar reflow (Defect 1) so the document width is constant regardless of vertical scrollbar presence.

**Outcomes** - `internal/frontend/src/styles/main.css` adds `html { scrollbar-gutter: stable; }`. Switching between `certs`/`sessions` and any tab no longer causes a horizontal shift of the header and tab block.

**Evidence** - Frontend build passes; DevTools shows `document.documentElement.clientWidth` no longer changes when toggling between short-content and long-content tabs.

**Constraints** - Use `scrollbar-gutter: stable` (not `overflow-y: scroll`) to avoid an empty scrollbar track; do not remove the existing `.app-shell { overflow-x: clip }`.

**Edge Cases** - macOS overlay scrollbars (no-op, no change); short pages show a same-color gutter; `fixed inset-0` modals unaffected.

**Verification** - Build; manual cross-tab switch confirms no horizontal shift; macOS unchanged.

#### Plan

1. In `internal/frontend/src/styles/main.css`, add a rule for `html` with `scrollbar-gutter: stable;` (place it near the top base styles, before `.app-shell`).
2. Confirm no `100vw`/`w-screen` usage exists (already verified — none).
3. Build the frontend and visually confirm.

#### Verification

- [ ] `html { scrollbar-gutter: stable }` present in `main.css`.
- [ ] No horizontal scrollbar appears on any tab.
- [ ] `certs` ↔ `sessions` no longer shifts the header/tab block horizontally.
- [ ] macOS shows no change (overlay scrollbars).

### Task 2: Preload Sessions List in DashboardView

#### Requirements

**Objective** - Eliminate the sessions activation repaint (Defect 2) by preloading the sessions list during `DashboardView.onMounted`, so activating `sessions` renders the list in a single frame.

**Outcomes** - `DashboardView.vue` holds `sessionProjects` / `sessionList` / `sessionsLoading` and preloads them in the existing `onMounted` `Promise.all`. `SessionBrowser.vue` accepts `projects` / `sessions` / `loading` props, drops the `onMounted` auto-`reload()`, and uses the prop data as its initial list source. Switching stable-tab → `sessions` no longer repaints.

**Evidence** - Frontend unit tests pass; manual switch from a stable tab to `sessions` shows the list immediately with no empty→full second layout.

**Constraints** - Preload list only (`projects` + initial `sessions`); keep `SessionDetail` on-demand; preserve all `SessionBrowser` interactions (refresh, project switch, session select, export, cleanup, copy, back-to-top, outline); update all readers of the previously self-managed `projects`/`sessions` fields consistently (`totalSessions`, template list, `reload`, `loadSessions`, `selectProject`); no global store.

**Edge Cases** - Preload not finished on first open (skeleton covers it — Task 3); preload failure (contained error state); empty projects/sessions; user-triggered reload/project switch updates local state and syncs parent via event.

**Verification** - Unit tests green; manual stable-tab → sessions shows no repaint; refresh/project-switch still work.

#### Plan

1. In `DashboardView.vue` script setup, add `sessionProjects` / `sessionList` / `sessionsLoading` refs and a `loadSessionsList()` that calls `api.getSessionProjects()` + `api.getSessionList({ project: '', page: 1, page_size: 100 })`, setting loading/error states.
2. Add `loadSessionsList()` to the existing `onMounted` `Promise.all` (line ~1707).
3. Pass `:projects="sessionProjects" :sessions="sessionList" :loading="sessionsLoading"` to `<SessionBrowser />` (line ~758), and listen for `@refreshed` to update parent state.
4. In `SessionBrowser.vue`, convert `projects`/`sessions` to props (keep local refs initialized from props for interaction), remove `onMounted(() => void reload())` (line 227-229), keep `reload()`/`loadSessions()`/`selectProject()` working off local state and emitting `refreshed`.
5. Audit every reader of the old self-managed `projects`/`sessions` (`totalSessions` computed, list `v-for`, `selectProject`, `loadSessions`) to use the new prop-backed local state.

#### Verification

- [ ] `DashboardView.onMounted` preloads sessions list.
- [ ] `SessionBrowser` no longer auto-`reload()`s on mount.
- [ ] Switching stable-tab → `sessions` renders the list in a single frame (no repaint).
- [ ] Refresh button, project switch, session select, export, cleanup all still work.
- [ ] `totalSessions` and list render use the prop-backed state.

### Task 3: Sessions List Skeleton

#### Requirements

**Objective** - Prevent empty→full list jumps during the residual first-load window and user-triggered reloads by showing a skeleton placeholder.

**Outcomes** - A Tailwind `animate-pulse` skeleton replaces the plain-text loading state (`SessionBrowser.vue:33`) in the sessions list area. The skeleton reserves a height close to the real list inside the existing `max-h-[calc(100vh-260px)]` container.

**Evidence** - Frontend build passes; on first load (or refresh), the list area shows pulsing placeholder rows instead of empty space, then fills in a single frame.

**Constraints** - Keep the skeleton lightweight (no new dependency); reserve height to avoid skeleton→list jump; show the same skeleton for initial load, refresh, and project switch; preserve existing empty/error states.

**Edge Cases** - Slow network (skeleton persists then fills); empty result (skeleton → empty state, no jump because skeleton height is bounded); very fast network (skeleton may flash briefly — acceptable).

**Verification** - Build; manual first-load and refresh show skeleton with no empty→full jump.

#### Plan

1. Add a skeleton block (e.g. 6–8 `animate-pulse` rounded bars) inside the sessions list container, shown when `loading` is true, replacing the current `<div v-if="loading" class="session-empty-compact">`.
2. Give the skeleton container a min-height approximating the real list (reuse `max-h-[calc(100vh-260px)]` bounds).
3. (Optional) Extract the skeleton into `components/SessionListSkeleton.vue` for reuse/clarity.

#### Verification

- [ ] Loading state shows a pulsing skeleton, not plain text.
- [ ] Skeleton height approximates the real list (no skeleton→list jump).
- [ ] Empty and error states still render correctly after loading completes.

### Task 4: Test, Build, and Manual Verification

#### Requirements

**Objective** - Ensure the change is covered by tests, builds cleanly, and is verified manually across the full tab-switch matrix.

**Outcomes** - `npm --prefix internal/frontend test` and `npm --prefix internal/frontend run build` pass; a manual verification record covering the switch matrix and edge cases.

**Evidence** - Green test/build output; verification checklist completed.

**Constraints** - Do not break existing tests (`SessionBrowserLayout.test.ts`, `DashboardUsageRequests.test.ts`, `DashboardViewImportExport.test.ts`, `DashboardViewListenStatus.test.ts`); add or adjust tests for the new preload flow and props contract; commit only related files (per project commit policy — commit, no push until confirmed).

**Edge Cases** - Preload failure path; empty sessions; macOS overlay scrollbars; theme toggle.

**Verification** - Full test + build + manual matrix below.

#### Plan

1. Run `npm --prefix internal/frontend test`; fix any breakage from the `SessionBrowser` props change.
2. Add/adjust unit tests asserting `DashboardView` preloads sessions and `SessionBrowser` does not auto-fetch on mount (e.g. source-level assertion that `onMounted` no longer calls `reload`, mirroring existing source-assertion tests).
3. Run `npm --prefix internal/frontend run build`.
4. Execute the manual verification matrix and record results.

#### Verification

- [ ] `npm --prefix internal/frontend test` passes.
- [ ] `npm --prefix internal/frontend run build` passes.
- [ ] Manual matrix: stable↔stable no shift; stable→sessions no repaint; certs↔sessions gutter-stabilized; first-load skeleton; refresh/project-switch work.
- [ ] macOS shows no regression.
- [ ] `git status` shows only task-related files staged.
