# Theme System Redesign Decisions

## D-001: Pilot the Theme System on the Session Browser First

**Date:** 2026-05-21
**Status:** accepted

**Context:** The target end state is a full application Light/Dark theme system. Reworking every dashboard screen at once would touch many components and make visual review, regression testing, and rollback harder.

**Decision:** Start with the session browser page as the pilot. The session browser is visually dense, contains navigation, cards, detail panels, code blocks, modals, and message content, so it is a good proving ground for the theme system.

**Consequences:** The first implementation validates the visual direction without forcing a full-dashboard rewrite. Theme primitives must still be designed for later reuse.

**Revisit when:** The session browser pilot is accepted and the same system is ready to move into `AppHeader.vue` and shared components.

## D-002: Explicit Theme Switch

**Date:** 2026-05-21
**Status:** accepted

**Context:** A system-only theme can be surprising for users who want a stable admin UI. The user specifically requested an explicit switch.

**Decision:** Provide a visible Light/Dark switch in the session browser pilot. Persist the choice locally in Phase 1. Design the control so it can later move to the app header as the full-dashboard theme switch.

**Consequences:** Theme changes are deliberate and testable. Phase 1 does not need backend persistence.

**Revisit when:** Multiple admin users or shared preferences require server-side persistence.

## D-003: Reference Styles Are Directional, Not Assets

**Date:** 2026-05-21
**Status:** accepted

**Context:** The reference pages provide useful visual direction, but the admin dashboard has different density, workflows, and brand needs.

**Decision:** Use the references as style direction only. Do not copy external runtime assets, layouts verbatim, or marketing-page composition. Adapt the visual language to the existing admin workflows.

**Consequences:** The result should feel inspired by the references while remaining a practical dashboard.

## D-004: Move the Final Theme Switch to the App Header

**Date:** 2026-05-21
**Status:** accepted

**Context:** The session browser pilot placed the switch inside the session page to validate the visual direction quickly. The final product should expose theme selection as a global application preference, not as a page-specific control.

**Decision:** In the full rollout, move the Light/Dark switch to `AppHeader.vue`, in the right-side action area near the language switcher and logout button. Remove the temporary session-browser-local switch once the header switch controls the whole app.

**Consequences:** Users can change theme from any dashboard tab. The theme control becomes part of the global shell, and session browser styles must inherit the global theme instead of owning a separate local theme source.

## D-005: Persist Theme Preference in the Backend with localStorage Fallback

**Date:** 2026-05-21
**Status:** accepted

**Context:** `localStorage` was enough for the pilot, but the target full-stack system should survive browser/device changes when the same admin data store is used.

**Decision:** Store the admin theme preference in backend config storage and expose it through authenticated preference endpoints. Keep `localStorage` as the fast startup and failure fallback.

**Consequences:** The UI can apply a theme immediately on load and later reconcile with the backend. The backend becomes the source of truth after login, while local persistence keeps the UI resilient if the preference API is temporarily unavailable.

## D-006: Use Explicit `light` and `dark` Modes Only

**Date:** 2026-05-21
**Status:** accepted

**Context:** A system-following mode would add startup, synchronization, and testing complexity. The user requested an explicit switch.

**Decision:** Phase 2 supports only `light` and `dark`. Invalid or missing values fall back to `light`.

**Consequences:** The theme state stays simple and predictable. A future `system` mode can be reconsidered only if users ask for it after the full rollout.
