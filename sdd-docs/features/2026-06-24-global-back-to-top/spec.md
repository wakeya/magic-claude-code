# Global Back-to-Top Floating Button Spec

Local page: Admin dashboard (`DashboardView.vue`)  
Proxy entry: N/A (admin server :8442)  
Reference sources: internal frontend — `DashboardView.vue`, `SessionBrowser.vue`, `styles/main.css`, `useI18n.ts`  
Stack: Vue 3 (Composition API) + Tailwind CSS v4  
Last updated: 2026-06-24  
Progress: 3 / 3 planned, all implemented and verified

## Overall Analysis (Source Analysis)

### Current State

A floating "back to top" button exists only inside `SessionBrowser.vue` (line 117–120):

```html
<button class="session-icon-button fixed bottom-5 right-5 z-50 shadow-md"
        :title="t('sessions.back_to_top')" @click="scrollToTop">
  <ArrowUp class="h-4 w-4" />
</button>
```

`scrollToTop` (line 334–335) calls `window.scrollTo({ top: 0, behavior: 'smooth' })`.

The button uses `session-icon-button` defined globally in `main.css` (line 285–296), styled as a compact icon button with hover/focus-visible states. It is `fixed bottom-5 right-5 z-50 shadow-md`, positioned viewport-fixed at the bottom-right corner.

Other tabs (status / providers / connection / certs / usage) also overflow the viewport height but have **no** back-to-top affordance. The sessions tab's left list has an internal scroll container (`max-h-[calc(100vh-260px)] overflow-y-auto`, SessionBrowser.vue:28), but the right panel and all other tabs scroll via `window`.

### Goal

Move the back-to-top button out of `SessionBrowser` and into `DashboardView` as a **global** floating button, visible on **every** tab whenever the page has scrolled down. Remove the now-redundant button from `SessionBrowser` (keep the one inside the outline modal which is a separate context).

### Design Summary

1. **Global placement** — Add a `fixed bottom-5 right-5 z-30` button in `DashboardView.vue` template, outside the `<div v-if="activeTab === ...">` blocks, positioned after the tab content container and before the modals/overlays.
2. **Show condition** — Reactive `scrolled = window.scrollY > 100` (100px threshold avoids flicker on minor scrolls). Use `@scroll` on `window` via `onMounted`/`onBeforeUnmount` listener, updating a reactive ref.
3. **Behavior** — `window.scrollTo({ top: 0, behavior: 'smooth' })`, same as current implementation.
4. **Style** — Reuse `session-icon-button` + `fixed bottom-5 right-5 z-50 shadow-md` (already globally defined in `main.css`). Icon: `ArrowUp` from lucide-vue-next.
5. **i18n** — Either reuse `sessions.back_to_top` or create a new generic key `common.back_to_top`. The existing key works for both zh/en; creating a new key is cleaner semantically but requires a `useI18n.ts` update.
6. **SessionBrowser cleanup** — Remove the `fixed bottom-5 right-5 z-50` back-to-top button from `SessionBrowser.vue` (line 117–120). Keep the back-to-top button inside the mobile outline modal (`v-if="showOutline && detail"` block, line 129–131) since it serves a different UX context (closing the modal + scrolling up).

### Risk Summary

1. The global button appears on **all** tabs including certs (short tab). On certs, `window.scrollY` stays 0 → button stays hidden → no visual noise. ✓
2. Sessions' internal scroll container (`max-h-[calc(100vh-260px)] overflow-y-auto`) scrolls independently of `window`. The global button does not respond to internal container scrolls — this is correct and accepted: the internal container has its own scroll context.
3. `session-icon-button` is already globally defined in `main.css` → no new styles needed. ✓
4. LoginView is outside `DashboardView` → unaffected. ✓

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | Global back-to-top button in `DashboardView` | `DashboardView.vue` | Build; button visible on scrolled status/providers tabs |
| 2 | Planned | Remove per-tab button from `SessionBrowser` | `SessionBrowser.vue` | Build; no duplicate button; outline modal button preserved |
| 3 | Planned | Test, build, and manual verification | Test/build green; verification record | `npm test` + `npm run build`; cross-tab scroll matrix |

## Requirements

### Deliverables

1. `DashboardView.vue` adds a global `fixed bottom-5 right-5 z-30` back-to-top button, shown only when `window.scrollY > 100`.
2. The button uses `window.scrollTo({ top: 0, behavior: 'smooth' })` and the existing `session-icon-button` + `ArrowUp` icon.
3. `SessionBrowser.vue` removes its `fixed bottom-5 right-5 z-50` back-to-top button (line 117–120). The outline modal's internal back-to-top button (line 129–131) is preserved.
4. The i18n key decision is documented: either reuse `sessions.back_to_top` or create `common.back_to_top` in `useI18n.ts` (spec recommends creating a new key for semantic cleanliness).
5. Frontend tests and build pass; no new warnings.

### Constraints

1. The button must not appear on short pages/tabs where `window.scrollY` is 0 (e.g., certs tab on tall viewports).
2. The scroll listener must be cleaned up in `onBeforeUnmount` to avoid leaks.
3. The button must not overlap with `SessionBrowser`'s outline modal's mobile back-to-top button (different z-index context: modal is `z-40`, global button is `z-30`; modal opens as overlay, global button is behind it).
4. Threshold of 100px is chosen to avoid flicker on minor scrolls; this is a UX decision that can be tuned later.

### Edge Cases

1. Short tab (certs) → `scrollY` stays 0 → button never shown.
2. User scrolls to bottom of long tab (sessions) → button appears; click scrolls to top smoothly.
3. User opens outline modal (sessions) → global button is behind the modal overlay (z-30 button vs z-40 modal backdrop); modal's own back-to-top button is the active affordance.
4. Tab switch from scrolled tab to short tab → `scrollY` resets to 0 → button hides (browser auto-scrolls to top on `v-if` content swap if new content is shorter; `scroll` event fires and hides button).
5. macOS overlay scrollbars → `window.scrollTo` works normally.

## Task Details

### Task 1: Global Back-to-Top Button in DashboardView

#### Requirements

**Objective** — Add a viewport-fixed back-to-top button in `DashboardView` that appears on all tabs when the page is scrolled down.

**Outcomes** — `DashboardView.vue` template includes a `fixed bottom-5 right-5 z-50` button using `session-icon-button` + `ArrowUp`, with `v-show="scrolled"` driven by a reactive `scrolled` ref updated via `window.scroll` listener. The button calls `window.scrollTo({ top: 0, behavior: 'smooth' })`.

**Evidence** — Frontend build passes; manual scroll on status/providers tab shows the button; clicking scrolls to top.

**Constraints** — Scroll listener registered in `onMounted`, removed in `onBeforeUnmount`; threshold 100px; i18n key reused or newly created (documented).

**Edge Cases** — Short tabs (button hidden); tab switch resets scroll; outline modal overlay context.

**Verification** — Build; manual cross-tab scroll confirms button behavior.

#### Plan

1. Import `ArrowUp` from `lucide-vue-next` in `DashboardView.vue` (already has other lucide imports).
2. Add `const scrolled = ref(false)` and scroll handler in script setup.
3. Register `window.addEventListener('scroll', ...)` in `onMounted`, remove in `onBeforeUnmount`.
4. Add the button in template after the tab content container (`</div>` closing the `containerClass` div) and before the modals section.
5. i18n: reuse `sessions.back_to_top` or add `common.back_to_top` to `useI18n.ts`.

#### Verification

- [ ] Button appears when scrolling down on status/providers/connection/usage/sessions tabs.
- [ ] Button does not appear on certs tab (when content fits viewport).
- [ ] Clicking button scrolls page to top smoothly.
- [ ] Scroll listener is cleaned up on component unmount.

### Task 2: Remove Per-Tab Button from SessionBrowser

#### Requirements

**Objective** — Remove the now-redundant global back-to-top button from `SessionBrowser.vue` to avoid duplicate affordance.

**Outcomes** — The `fixed bottom-5 right-5 z-50` button (line 117–120) is removed from `SessionBrowser.vue`. The outline modal's internal back-to-top button (line 129–131) is preserved.

**Evidence** — Frontend build passes; no duplicate back-to-top buttons visible on sessions tab; outline modal back-to-top still works.

**Constraints** — Only remove the fixed global button; do not remove the outline modal's button; `scrollToTop` function can remain (still used by outline modal) or be removed if unused after cleanup.

**Edge Cases** — Outline modal opens → its own back-to-top button is the active affordance, unaffected by global button removal.

**Verification** — Build; manual sessions tab shows only the global button (from DashboardView); outline modal button still works.

#### Plan

1. Remove lines 117–120 (`<button class="session-icon-button fixed bottom-5 right-5 z-50 shadow-md" ...>`) from `SessionBrowser.vue`.
2. Check if `scrollToTop` is still used by outline modal (line 129) → if yes, keep the function; if no, remove it.
3. If `ArrowUp` import is no longer used after removal (only used by the removed button and outline modal), verify before removing the import.

#### Verification

- [ ] Sessions tab shows no duplicate back-to-top button.
- [ ] Outline modal's back-to-top button still works.
- [ ] `scrollToTop` function preserved if still referenced.

### Task 3: Test, Build, and Manual Verification

#### Requirements

**Objective** — Verify the global back-to-top works correctly across all tabs and no regressions were introduced.

**Outcomes** — `npm --prefix internal/frontend test` and `npm --prefix internal/frontend run build` pass; manual verification matrix documented.

**Evidence** — Green test/build; verification checklist.

**Constraints** — Existing tests pass; add source-level assertion that `DashboardView` contains the global button and `SessionBrowser` no longer has the fixed button.

**Verification** — Full matrix below.

#### Verification Matrix

- [ ] Status tab: scroll down → button appears → click → scrolls to top.
- [ ] Providers tab: same behavior.
- [ ] Connection tab: same behavior.
- [ ] Usage tab: same behavior.
- [ ] Certs tab: button does not appear (content fits viewport).
- [ ] Sessions tab: button appears when scrolled; no duplicate button; outline modal button still works.
- [ ] `npm --prefix internal/frontend test` passes.
- [ ] `npm --prefix internal/frontend run build` passes.
