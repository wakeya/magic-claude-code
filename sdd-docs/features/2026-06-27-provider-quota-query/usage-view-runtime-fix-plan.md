# Provider Usage View Runtime Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the ZenMux field-visibility binding collision that makes the Provider usage page render blank with `TypeError: Q is not a function`.

**Architecture:** Keep the template-facing computed ref unchanged, but give the imported predicate a distinct local alias and call that alias from the computed getter. Protect the source wiring with a focused Node test that follows the repository's existing Vue source-test pattern.

**Tech Stack:** Vue 3 SFC, TypeScript, Vite, Node.js built-in test runner, Go embedded frontend assets

---

### Task 1: Add the failing source-wiring regression test

**Files:**
- Create: `internal/frontend/src/views/ProviderUsageView.test.ts`
- Test: `internal/frontend/src/views/ProviderUsageView.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const viewSource = readFileSync(join(here, 'ProviderUsageView.vue'), 'utf8')

test('ProviderUsageView keeps the ZenMux predicate distinct from its computed binding', () => {
  assert.match(viewSource, /showZenMuxFields as shouldShowZenMuxFields/)
  assert.match(viewSource, /const isZenMux = computed\(\(\) => shouldShowZenMuxFields\(/)
  assert.match(viewSource, /const showZenMuxFields = isZenMux/)
})
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
node --test --experimental-strip-types internal/frontend/src/views/ProviderUsageView.test.ts
```

Expected: FAIL because `ProviderUsageView.vue` imports `showZenMuxFields` without the `shouldShowZenMuxFields` alias.

### Task 2: Remove the runtime binding collision

**Files:**
- Modify: `internal/frontend/src/views/ProviderUsageView.vue:218-295`
- Test: `internal/frontend/src/views/ProviderUsageView.test.ts`

- [ ] **Step 1: Alias the imported predicate**

Change the quota form import to:

```ts
showZenMuxFields as shouldShowZenMuxFields,
```

- [ ] **Step 2: Call the aliased predicate from the computed getter**

Change the getter to:

```ts
const isZenMux = computed(() => shouldShowZenMuxFields(form.template_type, effectiveTokenPlanProvider.value))
```

Keep the template-facing alias unchanged:

```ts
const showZenMuxFields = isZenMux
```

- [ ] **Step 3: Run the focused test and verify GREEN**

Run:

```bash
node --test --experimental-strip-types internal/frontend/src/views/ProviderUsageView.test.ts
```

Expected: PASS with one passing test and no failures.

### Task 3: Verify and commit the fix

**Files:**
- Modify through build: `internal/frontend/dist/**`
- Verify: `internal/frontend/src/views/ProviderUsageView.vue`
- Verify: `internal/frontend/src/views/ProviderUsageView.test.ts`

- [ ] **Step 1: Run the complete frontend test suite**

Run:

```bash
npm --prefix internal/frontend test
```

Expected: all frontend tests pass with zero failures.

- [ ] **Step 2: Rebuild the frontend assets**

Run:

```bash
npm --prefix internal/frontend run build
```

Expected: Vite exits successfully and refreshes `internal/frontend/dist`.

- [ ] **Step 3: Verify the generated chunk does not self-call its computed ref**

Run:

```bash
chunk=$(find internal/frontend/dist/assets -maxdepth 1 -name 'ProviderUsageView-*.js' -print -quit)
node -e 'const fs=require("node:fs"); const source=fs.readFileSync(process.argv[1], "utf8"); const collision=/([A-Za-z_$][\w$]*)=[A-Za-z_$][\w$]*\(\(\)=>([A-Za-z_$][\w$]*)\([^;]+,\2=\1;/.test(source); if (collision) process.exit(1)' "$chunk"
```

Expected: exit code 0.

- [ ] **Step 4: Run the Go test suite**

Run:

```bash
go test ./...
```

Expected: all Go packages pass.

- [ ] **Step 5: Check the final diff and commit**

Run:

```bash
git diff --check
git status --short
git diff --stat
git add internal/frontend/src/views/ProviderUsageView.vue internal/frontend/src/views/ProviderUsageView.test.ts internal/frontend/dist
git commit -m "fix(frontend): prevent provider usage view binding collision"
```

Expected: the commit contains only the focused source change, regression test, and refreshed frontend assets.
