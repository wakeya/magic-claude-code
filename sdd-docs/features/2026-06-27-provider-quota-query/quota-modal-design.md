# Provider Quota Modal Design

## Goal

Replace the full-page Provider quota editor with a modal that matches the existing Provider edit experience. The Provider list remains visible behind a translucent overlay, while quota configuration and the latest result remain available side by side.

## Scope

This is a frontend presentation and orchestration change. Existing quota APIs, backend scheduling, credential semantics, query adapters, and snapshot storage remain unchanged.

## Component Architecture

### `ProviderUsageModal.vue`

A new, route-independent component owns the quota form and query state. It receives:

- `providerId`: the Provider whose quota configuration is being edited.
- `providerName`: the title displayed in the modal.

It emits:

- `close`: cancel, backdrop, close button, or Escape requested dismissal.
- `saved`: configuration was saved, a production query succeeded, and the refreshed snapshot is ready for the Provider card.

The component reuses `QuotaResultDisplay`, `quotaForm` utilities, `useApi`, and existing i18n keys. It does not use `useRoute` or `useRouter`.

### `DashboardView.vue`

The Dashboard owns `usageProviderId`. The Provider card's Usage event sets this ID and opens an asynchronously loaded `ProviderUsageModal`; it does not navigate and does not change the URL.

On `saved`, the Dashboard updates or reloads `quotaSnapshots`, clears `usageProviderId`, and restores focus to the Usage button that opened the modal. On a normal close, it clears the ID and reloads snapshots so manual refreshes performed inside the modal are reflected on the card.

### Legacy route compatibility

`/providers/:providerId/usage` remains accepted as a compatibility entry point. The router redirects it to `/?tab=providers&usage_provider=<id>`. Dashboard consumes that one-time parameter, opens the modal, and immediately removes `usage_provider` with `router.replace`, leaving the stable Provider-list URL.

The obsolete full-page `ProviderUsageView.vue` is removed after its logic is migrated.

## Layout and Visual Design

The modal follows `ProviderModal` as the visual source of truth rather than introducing a new palette or typography:

- Translucent black backdrop and centered `app-panel` surface.
- Approximately `90vw`, maximum width `1180px`, and maximum height `90vh`.
- Existing border, radius, spacing, control, typography, theme, and button tokens.
- Header title: `Quota Usage · <Provider name>` with a close button.
- Desktop (`lg` and above): two columns. Configuration is on the left; latest result and Refresh Now are on the right.
- Smaller screens: the columns stack vertically, configuration first and result second, without horizontal scrolling.
- Footer actions: Cancel, Test Query, and Save.

No new design-system colors or fonts are introduced. The existing application's light and dark themes remain authoritative.

## Interaction Model

- Opening shows a loading state until the quota configuration and snapshot are available.
- Cancel, backdrop click, close button, or Escape closes without an unsaved-change prompt, matching `ProviderModal`.
- Test Query runs against the draft form, displays a temporary result, and keeps the modal open.
- Refresh Now runs a production query, reloads the saved snapshot, and keeps the modal open.
- Each in-flight action disables only the corresponding action to prevent duplicate submissions.

### Save, query, and close sequence

Save is intentionally a compound operation:

1. Build and submit the saved quota configuration.
2. Clear submitted secret inputs and reset clear-secret flags after the save succeeds.
3. Immediately run a production quota query using the saved configuration.
4. Treat the operation as query success only when the query response and normalized result both report success.
5. Reload the Provider usage response to obtain the persisted snapshot.
6. Emit `saved` with the snapshot, update the Provider card, and close the modal after the same short success delay used by `ProviderModal` (approximately 800 ms).

If configuration saving fails, the modal remains open and shows the save error. If saving succeeds but the production query fails, the saved configuration is retained, the modal remains open, and a clear message states that configuration was saved but the query failed. This prevents closing onto a Provider card with no current result.

## Error and Empty States

- An unknown Provider displays a localized not-found state with a Close action.
- Initial load failures are visible instead of being silently swallowed.
- Save, test, and refresh failures appear next to their relevant action or result area.
- A missing snapshot shows the existing never-queried state.
- A failed save never triggers a production query.
- A failed production query after save never emits `saved` and never auto-closes.

## Accessibility

- The panel uses `role="dialog"`, `aria-modal="true"`, and an `aria-labelledby` title.
- Keyboard focus enters the dialog on open and returns to the originating Usage button on close.
- Escape closes the dialog when no destructive confirmation is active.
- All fields retain visible labels and focus states.
- Buttons use semantic elements and expose disabled states during requests.
- Backdrop scrolling is prevented while the dialog is open.

## Testing

Focused frontend tests cover:

- Provider Usage opens the modal without changing the URL.
- The correct Provider ID and name reach the modal.
- The modal has the shared backdrop/panel structure, desktop two-column layout, mobile stacking, and dialog attributes.
- Save calls update before production query.
- Successful save plus query emits the refreshed snapshot and closes after the success delay.
- Save failure and post-save query failure keep the modal open with distinct errors.
- Test Query and Refresh Now do not close the modal.
- Normal close reloads Dashboard snapshots and restores focus.
- The legacy route opens the modal and removes the one-time parameter.

Verification runs the complete frontend test suite, frontend production build, and Go test suite. No backend changes are expected.

## Non-goals

- Changing quota query intervals or scheduler behavior.
- Changing backend API contracts or snapshot persistence.
- Redesigning `ProviderModal` or other Dashboard dialogs.
- Adding a general-purpose dialog framework or a new form library.
