# Provider Usage View Runtime Fix Design

## Problem

`ProviderUsageView.vue` imports the `showZenMuxFields` utility and also declares a local computed value with the same name. Vite currently builds this collision without failing, but the generated module assigns the computed ref to the same binding used by its getter. Rendering the page then calls that ref as a function and throws `TypeError: Q is not a function`, leaving the provider usage page blank.

## Scope

Apply a minimal frontend-only correction. Do not change quota behavior, API contracts, form payloads, or unrelated TypeScript errors.

## Design

- Alias the imported utility to `shouldShowZenMuxFields`.
- Keep the local computed value named `showZenMuxFields` so the template remains unchanged.
- Have the computed getter call `shouldShowZenMuxFields`, eliminating the binding collision and self-reference.
- Add a focused source-wiring regression test that fails while the import and computed binding collide and passes only when the utility is distinctly aliased and invoked.

## Verification

- Run the new focused regression test and confirm it fails before the production change.
- Apply the import alias and getter update, then confirm the focused test passes.
- Run the complete frontend test suite.
- Build the frontend and confirm the generated provider usage chunk no longer contains the self-referential computed call.
- Run the repository Go test suite to ensure the refreshed embedded frontend remains compatible.

## Non-goals

- Enabling `vue-tsc` as a mandatory build step. The repository currently has unrelated existing type errors, so that requires separate cleanup.
- Refactoring provider quota form state or field-visibility utilities.
- Changing the provider usage page layout or behavior.
