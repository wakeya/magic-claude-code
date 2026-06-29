# Provider Quota Modal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the full-page Provider quota editor with a responsive modal that saves configuration, immediately queries quota, updates the Provider card, and closes only after query success.

**Architecture:** Extract the route-independent modal UI from `ProviderUsageView.vue`, isolate the save→query→snapshot sequence in a unit-testable utility, and let `DashboardView.vue` own modal visibility and Provider-card refresh. Preserve the legacy URL through a router redirect and a one-time Dashboard query parameter.

**Tech Stack:** Vue 3 Composition API, TypeScript, Vue Router, Tailwind CSS, Node.js built-in test runner, Vite, Go embedded frontend assets

---

### Task 1: Add a testable save-query workflow

**Files:**
- Create: `internal/frontend/src/utils/quotaSaveFlow.ts`
- Create: `internal/frontend/src/utils/quotaSaveFlow.test.ts`

- [ ] **Step 1: Write failing workflow tests**

Create `internal/frontend/src/utils/quotaSaveFlow.test.ts`:

```ts
import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import type {
  ProviderQuotaResult,
  ProviderUsageResponse,
  ProviderUsageUpdateRequest,
  PublicQuotaConfig,
  QuotaSnapshot,
} from '../composables/useApi.ts'

const flowModuleURL = new URL('./quotaSaveFlow.ts', import.meta.url)

async function loadFlow() {
  assert.equal(existsSync(fileURLToPath(flowModuleURL)), true, 'quotaSaveFlow.ts must exist')
  return import(flowModuleURL.href)
}

const config = { enabled: true } as PublicQuotaConfig
const result = {
  provider_id: 'provider-1',
  template_type: 'token_plan',
  success: true,
  queried_at: '2026-06-29T00:00:00Z',
  duration_ms: 10,
} satisfies ProviderQuotaResult
const snapshot = {
  provider_id: 'provider-1',
  result,
  queried_at: result.queried_at,
  updated_at: result.queried_at,
  has_last_success: true,
  is_stale: false,
} satisfies QuotaSnapshot

test('save flow updates config, queries, then reloads the snapshot', async () => {
  const { runQuotaSaveFlow } = await loadFlow()
  const calls: string[] = []
  const outcome = await runQuotaSaveFlow({ enabled: true }, {
    update: async (_payload: ProviderUsageUpdateRequest) => {
      calls.push('update')
      return { success: true, config }
    },
    query: async () => {
      calls.push('query')
      return { success: true, result }
    },
    reload: async (): Promise<ProviderUsageResponse> => {
      calls.push('reload')
      return { config, snapshot }
    },
  })

  assert.deepEqual(calls, ['update', 'query', 'reload'])
  assert.deepEqual(outcome, { ok: true, configSaved: true, config, snapshot })
})

test('save failure does not run the production query', async () => {
  const { runQuotaSaveFlow } = await loadFlow()
  let queried = false
  const outcome = await runQuotaSaveFlow({}, {
    update: async () => { throw new Error('save rejected') },
    query: async () => {
      queried = true
      return { success: true, result }
    },
    reload: async () => ({ config, snapshot }),
  })

  assert.equal(queried, false)
  assert.deepEqual(outcome, { ok: false, configSaved: false, error: 'save rejected' })
})

test('query failure reports that configuration remains saved', async () => {
  const { runQuotaSaveFlow } = await loadFlow()
  const failedResult = { ...result, success: false, error_message: 'upstream denied' }
  const outcome = await runQuotaSaveFlow({}, {
    update: async () => ({ success: true, config }),
    query: async () => ({ success: false, result: failedResult }),
    reload: async () => ({ config, snapshot }),
  })

  assert.deepEqual(outcome, {
    ok: false,
    configSaved: true,
    config,
    error: 'upstream denied',
  })
})

test('disabling quota skips querying and clears the card snapshot', async () => {
  const { runQuotaSaveFlow } = await loadFlow()
  const calls: string[] = []
  const outcome = await runQuotaSaveFlow({ enabled: false }, {
    update: async () => {
      calls.push('update')
      return { success: true, config: { ...config, enabled: false } }
    },
    query: async () => {
      calls.push('query')
      return { success: true, result }
    },
    reload: async () => {
      calls.push('reload')
      return { config, snapshot }
    },
  })

  assert.deepEqual(calls, ['update'])
  assert.deepEqual(outcome, {
    ok: true,
    configSaved: true,
    config: { ...config, enabled: false },
    snapshot: null,
  })
})
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
node --test --experimental-strip-types internal/frontend/src/utils/quotaSaveFlow.test.ts
```

Expected: FAIL with `quotaSaveFlow.ts must exist`, proving the missing workflow is under test without a module-loader error.

- [ ] **Step 3: Implement the workflow utility**

Create `internal/frontend/src/utils/quotaSaveFlow.ts`:

```ts
import type {
  ProviderQuotaResult,
  ProviderUsageResponse,
  ProviderUsageUpdateRequest,
  PublicQuotaConfig,
  QuotaSnapshot,
} from '../composables/useApi.ts'

type UpdateResponse = { success: boolean; config: PublicQuotaConfig }
type QueryResponse = { success: boolean; result: ProviderQuotaResult }

export interface QuotaSaveFlowDependencies {
  update: (payload: ProviderUsageUpdateRequest) => Promise<UpdateResponse>
  query: () => Promise<QueryResponse>
  reload: () => Promise<ProviderUsageResponse>
}

export type QuotaSaveFlowOutcome =
  | { ok: true; configSaved: true; config: PublicQuotaConfig; snapshot: QuotaSnapshot | null }
  | { ok: false; configSaved: false; error: string }
  | { ok: false; configSaved: true; config: PublicQuotaConfig; error: string }

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export async function runQuotaSaveFlow(
  payload: ProviderUsageUpdateRequest,
  dependencies: QuotaSaveFlowDependencies,
): Promise<QuotaSaveFlowOutcome> {
  let config: PublicQuotaConfig
  try {
    const updated = await dependencies.update(payload)
    config = updated.config
  } catch (error) {
    return { ok: false, configSaved: false, error: errorMessage(error) }
  }

  if (payload.enabled === false) {
    return { ok: true, configSaved: true, config, snapshot: null }
  }

  try {
    const queried = await dependencies.query()
    if (!queried.success || !queried.result.success) {
      return {
        ok: false,
        configSaved: true,
        config,
        error: queried.result.error_message || 'Quota query failed',
      }
    }

    const reloaded = await dependencies.reload()
    if (!reloaded.snapshot) {
      return { ok: false, configSaved: true, config, error: 'Quota snapshot missing after query' }
    }
    return { ok: true, configSaved: true, config, snapshot: reloaded.snapshot }
  } catch (error) {
    return { ok: false, configSaved: true, config, error: errorMessage(error) }
  }
}
```

- [ ] **Step 4: Run the workflow tests and verify GREEN**

Run:

```bash
node --test --experimental-strip-types internal/frontend/src/utils/quotaSaveFlow.test.ts
```

Expected: 4 tests pass, 0 fail.

- [ ] **Step 5: Commit the workflow utility**

```bash
git add internal/frontend/src/utils/quotaSaveFlow.ts internal/frontend/src/utils/quotaSaveFlow.test.ts
git commit -m "test(frontend): cover quota save and query flow"
```

### Task 2: Extract the responsive Provider quota modal

**Files:**
- Create: `internal/frontend/src/components/ProviderUsageModal.vue`
- Create: `internal/frontend/src/components/ProviderUsageModal.test.ts`
- Modify: `internal/frontend/src/composables/useI18n.ts:394-407,799-812`
- Delete: `internal/frontend/src/views/ProviderUsageView.test.ts`

- [ ] **Step 1: Write the failing modal contract test**

Create `internal/frontend/src/components/ProviderUsageModal.test.ts`:

```ts
import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const modalPath = join(here, 'ProviderUsageModal.vue')
const modalSource = existsSync(modalPath) ? readFileSync(modalPath, 'utf8') : ''
const i18nSource = readFileSync(join(here, '..', 'composables', 'useI18n.ts'), 'utf8')

test('quota modal is route independent and exposes its parent contract', () => {
  assert.notEqual(modalSource, '', 'ProviderUsageModal.vue must exist')
  assert.match(modalSource, /providerId: string/)
  assert.match(modalSource, /providerName: string/)
  assert.match(modalSource, /saved: \[snapshot: QuotaSnapshot \| null\]/)
  assert.doesNotMatch(modalSource, /useRoute|useRouter/)
})

test('quota modal uses the shared responsive dialog presentation', () => {
  assert.match(modalSource, /fixed inset-0 bg-black\/50 z-50/)
  assert.match(modalSource, /role="dialog"/)
  assert.match(modalSource, /aria-modal="true"/)
  assert.match(modalSource, /max-w-\[1180px\]/)
  assert.match(modalSource, /grid-cols-1 lg:grid-cols-2/)
  assert.match(modalSource, /document\.body\.style\.overflow = 'hidden'/)
  assert.match(modalSource, /event\.key === 'Escape'/)
})

test('quota modal wires save through the save-query workflow', () => {
  assert.match(modalSource, /runQuotaSaveFlow/)
  assert.match(modalSource, /window\.setTimeout\(\(\) => emit\('saved', outcome\.snapshot\), 800\)/)
  assert.match(modalSource, /quota\.saved_query_failed/)
})

test('test and refresh actions do not close the modal', () => {
  const testFlow = modalSource.match(/async function testQuery[\s\S]*?\n\}/)?.[0] || ''
  const refreshFlow = modalSource.match(/async function refreshNow[\s\S]*?\n\}/)?.[0] || ''
  assert.doesNotMatch(testFlow, /emit\(['"](?:close|saved)/)
  assert.doesNotMatch(refreshFlow, /emit\(['"](?:close|saved)/)
})

test('quota modal has bilingual post-save query failure text', () => {
  assert.equal((i18nSource.match(/'quota\.saved_query_failed'/g) || []).length, 2)
})
```

- [ ] **Step 2: Run the modal test and verify RED**

Run:

```bash
node --test --experimental-strip-types internal/frontend/src/components/ProviderUsageModal.test.ts
```

Expected: FAIL with `ProviderUsageModal.vue must exist`.

- [ ] **Step 3: Create the modal shell and migrate the existing fields**

Create `ProviderUsageModal.vue` by moving the form, field-visibility computed values, loading logic, test query, refresh query, and result rendering from `ProviderUsageView.vue`. Replace the full-page wrapper and route header with this exact modal structure:

```vue
<template>
  <div class="fixed inset-0 bg-black/50 z-50 flex justify-center items-center px-4" @click.self="requestClose">
    <div
      ref="dialogEl"
      class="app-panel rounded-lg w-[90vw] max-w-[1180px] max-h-[90vh] overflow-y-auto"
      role="dialog"
      aria-modal="true"
      aria-labelledby="provider-usage-title"
      tabindex="-1"
    >
      <header class="sticky top-0 z-10 app-panel flex items-center justify-between px-6 py-4 border-b border-default">
        <div>
          <h2 id="provider-usage-title" class="text-lg font-bold">{{ t('quota.title') }} · {{ providerName }}</h2>
          <p class="text-xs text-text-secondary mt-1">{{ t('quota.modal_subtitle') }}</p>
        </div>
        <button type="button" class="bg-transparent border-none text-2xl cursor-pointer app-muted hover:text-fg" :aria-label="t('modal.cancel')" @click="requestClose">&times;</button>
      </header>

      <div v-if="loading" class="px-6 py-12 text-center text-text-secondary">{{ t('quota.refreshing') }}</div>
      <div v-else-if="notFound" class="px-6 py-12 text-center">
        <div class="text-lg font-semibold mb-2">{{ t('quota.not_found') }}</div>
        <div class="text-text-secondary mb-4">{{ t('quota.not_found_hint') }}</div>
        <button type="button" class="app-control px-4 py-2 rounded-lg" @click="requestClose">{{ t('modal.cancel') }}</button>
      </div>
      <div v-else-if="loadError" class="px-6 py-12 text-center text-danger">{{ loadError }}</div>

      <div v-else class="grid grid-cols-1 lg:grid-cols-2 gap-6 p-6">
        <section class="min-w-0" data-section="quota-configuration"></section>
        <section class="min-w-0 lg:border-l lg:border-default lg:pl-6" data-section="quota-results"></section>
      </div>

      <footer v-if="!loading && !notFound && !loadError" class="sticky bottom-0 app-panel flex flex-wrap justify-end gap-2 px-6 py-4 border-t border-default">
        <span v-if="saveMsg" :class="['mr-auto self-center text-sm', saveOk ? 'text-secondary' : 'text-danger']">{{ saveMsg }}</span>
        <button type="button" class="app-control px-5 py-2.5 rounded-lg text-sm font-semibold" @click="requestClose">{{ t('modal.cancel') }}</button>
        <button type="button" class="app-control px-5 py-2.5 rounded-lg text-sm font-semibold" :disabled="testing" @click="testQuery">{{ testing ? t('quota.refreshing') : t('quota.test') }}</button>
        <button type="button" class="bg-primary text-white px-5 py-2.5 rounded-lg text-sm font-semibold disabled:opacity-60" :disabled="saving" @click="saveConfig">{{ saving ? t('quota.refreshing') : t('quota.save') }}</button>
      </footer>
    </div>
  </div>
</template>
```

Populate `data-section="quota-configuration"` with the complete configuration markup currently inside `ProviderUsageView.vue` lines 31–171, excluding its old action-button block because actions now live in the footer. Populate `data-section="quota-results"` with the complete latest-result markup currently on lines 176–207. Preserve all existing conditional fields and `QuotaResultDisplay` calls without changing their data contracts.

The script contract and lifecycle must be:

```ts
const props = defineProps<{ providerId: string; providerName: string }>()
const emit = defineEmits<{ close: []; saved: [snapshot: QuotaSnapshot | null] }>()
const dialogEl = ref<HTMLElement | null>(null)
const loadError = ref('')
let previousBodyOverflow = ''

function requestClose() {
  if (!saving.value) emit('close')
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') requestClose()
}

onMounted(async () => {
  previousBodyOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  document.addEventListener('keydown', onKeydown)
  await nextTick()
  dialogEl.value?.focus()
  await loadConfig()
})

onBeforeUnmount(() => {
  document.body.style.overflow = previousBodyOverflow
  document.removeEventListener('keydown', onKeydown)
})
```

Change `loadConfig` to call only `api.getProviderUsage(props.providerId)`. Remove the redundant Providers-list request, `goBack`, route state, and router state. Set `loadError.value = t('quota.load_failed')` for non-404 errors.

- [ ] **Step 4: Implement save→query→close and visible errors**

Replace `saveConfig` with:

```ts
function clearSubmittedSecrets() {
  form.script_api_key = ''
  form.zenmux_api_key = ''
  form.access_token = ''
  form.secret_access_key = ''
  form.clear_script_api_key = false
  form.clear_zenmux_api_key = false
  form.clear_access_token = false
  form.clear_secret_access_key = false
}

async function saveConfig() {
  saving.value = true
  saveMsg.value = ''
  const payload = buildSavePayload(form, detectedTokenPlan.value, savedConfig.value)
  const outcome = await runQuotaSaveFlow(payload, {
    update: data => api.updateProviderUsage(props.providerId, data),
    query: () => api.queryProviderUsage(props.providerId),
    reload: () => api.getProviderUsage(props.providerId),
  })

  if (outcome.configSaved) {
    savedConfig.value = outcome.config
    clearSubmittedSecrets()
  }
  if (!outcome.ok) {
    saveOk.value = false
    saveMsg.value = outcome.configSaved
      ? t('quota.saved_query_failed', { error: outcome.error })
      : outcome.error
    saving.value = false
    return
  }

  saveOk.value = true
  saveMsg.value = t('quota.query_success')
  window.setTimeout(() => emit('saved', outcome.snapshot), 800)
}
```

Keep `saving` true through the success delay so close actions cannot interrupt the completed flow. `testQuery` and `refreshNow` retain their existing non-closing behavior, but each catch block must set a visible error/result instead of silently swallowing it.

- [ ] **Step 5: Add bilingual modal messages**

Add these keys to both locales in `useI18n.ts`:

```ts
// zh
'quota.modal_subtitle': '配置自动查询并查看最新结果',
'quota.load_failed': '加载用量配置失败',
'quota.saved_query_failed': '配置已保存，但查询失败：{error}',

// en
'quota.modal_subtitle': 'Configure automatic queries and view the latest result',
'quota.load_failed': 'Failed to load quota configuration',
'quota.saved_query_failed': 'Configuration saved, but the query failed: {error}',
```

- [ ] **Step 6: Replace the obsolete view test and verify GREEN**

Delete `internal/frontend/src/views/ProviderUsageView.test.ts`; its ZenMux alias regression assertion must remain in `ProviderUsageModal.test.ts`:

```ts
test('quota modal keeps the ZenMux predicate distinct from its computed binding', () => {
  assert.match(modalSource, /showZenMuxFields as shouldShowZenMuxFields/)
  assert.match(modalSource, /const isZenMux = computed\(\(\) => shouldShowZenMuxFields\(/)
})
```

Run:

```bash
node --test --experimental-strip-types internal/frontend/src/components/ProviderUsageModal.test.ts
node --test --experimental-strip-types internal/frontend/src/utils/quotaSaveFlow.test.ts
```

Expected: all modal and workflow tests pass.

- [ ] **Step 7: Commit the modal component**

```bash
git add internal/frontend/src/components/ProviderUsageModal.vue internal/frontend/src/components/ProviderUsageModal.test.ts internal/frontend/src/composables/useI18n.ts internal/frontend/src/views/ProviderUsageView.test.ts
git commit -m "feat(frontend): add provider quota modal"
```

### Task 3: Wire the modal into Dashboard and preserve the legacy route

**Files:**
- Modify: `internal/frontend/src/views/DashboardView.vue:123-170,803-854,970-1055,1209-1225,1789-1805`
- Modify: `internal/frontend/src/main.ts:8-14`
- Create: `internal/frontend/src/views/DashboardProviderUsageModal.test.ts`

- [ ] **Step 1: Write the failing Dashboard and route wiring tests**

Create `internal/frontend/src/views/DashboardProviderUsageModal.test.ts`:

```ts
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const mainSource = readFileSync(join(here, '..', 'main.ts'), 'utf8')

test('Provider Usage opens a lazy modal without navigating', () => {
  assert.match(dashboardSource, /defineAsyncComponent\(\(\) => import\('@\/components\/ProviderUsageModal\.vue'\)\)/)
  assert.match(dashboardSource, /@usage="openProviderUsage\(p\.id\)"/)
  assert.doesNotMatch(dashboardSource, /function goToUsage[\s\S]*?router\.push/)
})

test('Dashboard renders the selected Provider in the quota modal', () => {
  assert.match(dashboardSource, /<ProviderUsageModal/)
  assert.match(dashboardSource, /:provider-id="usageProviderId"/)
  assert.match(dashboardSource, /:provider-name="usageProviderName"/)
  assert.match(dashboardSource, /@saved="handleProviderUsageSaved"/)
})

test('successful quota save updates the Provider card before closing', () => {
  const savedFlow = dashboardSource.match(/async function handleProviderUsageSaved[\s\S]*?\n\}/)?.[0] || ''
  assert.match(savedFlow, /quotaSnapshots\.value/)
  assert.match(savedFlow, /snapshot\.provider_id/)
  assert.match(savedFlow, /closeProviderUsage\(false\)/)
})

test('disabling quota removes the Provider card snapshot before closing', () => {
  const savedFlow = dashboardSource.match(/async function handleProviderUsageSaved[\s\S]*?\n\}/)?.[0] || ''
  assert.match(savedFlow, /delete nextSnapshots\[usageProviderId\.value\]/)
})

test('closing the quota modal refreshes snapshots and restores focus', () => {
  const closeFlow = dashboardSource.match(/async function closeProviderUsage[\s\S]*?\n\}/)?.[0] || ''
  assert.match(closeFlow, /loadQuotaSnapshots/)
  assert.match(closeFlow, /usageTriggerEl[\s\S]*?focus/)
})

test('legacy Provider Usage route redirects through a one-time parameter', () => {
  assert.match(mainSource, /usage_provider/)
  assert.match(mainSource, /providerId/)
  assert.match(dashboardSource, /searchParams\.get\('usage_provider'\)/)
  assert.match(dashboardSource, /searchParams\.delete\('usage_provider'\)/)
  assert.match(dashboardSource, /router\.replace/)
})
```

- [ ] **Step 2: Run the Dashboard test and verify RED**

Run:

```bash
node --test --experimental-strip-types internal/frontend/src/views/DashboardProviderUsageModal.test.ts
```

Expected: FAIL because Dashboard still calls `goToUsage` and does not render the modal.

- [ ] **Step 3: Add Dashboard modal state and handlers**

Extend the Vue import and add the lazy component:

```ts
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
const ProviderUsageModal = defineAsyncComponent(() => import('@/components/ProviderUsageModal.vue'))
```

Add state and computed values near the existing Provider modal state:

```ts
const usageProviderId = ref('')
const usageTriggerEl = ref<HTMLElement | null>(null)
const usageProviderName = computed(() =>
  providers.value.find(provider => provider.id === usageProviderId.value)?.name || usageProviderId.value
)
```

Replace `goToUsage` with:

```ts
function openProviderUsage(providerId: string) {
  usageTriggerEl.value = document.activeElement instanceof HTMLElement ? document.activeElement : null
  usageProviderId.value = providerId
}

async function closeProviderUsage(reloadSnapshots = true) {
  usageProviderId.value = ''
  if (reloadSnapshots) await loadQuotaSnapshots()
  await nextTick()
  usageTriggerEl.value?.focus()
  usageTriggerEl.value = null
}

async function handleProviderUsageSaved(snapshot: QuotaSnapshot | null) {
  const nextSnapshots = { ...quotaSnapshots.value }
  if (snapshot) {
    nextSnapshots[snapshot.provider_id] = snapshot
  } else {
    delete nextSnapshots[usageProviderId.value]
  }
  quotaSnapshots.value = nextSnapshots
  await closeProviderUsage(false)
}
```

Change the card event to:

```vue
@usage="openProviderUsage(p.id)"
```

Render the modal next to `ProviderModal`:

```vue
<ProviderUsageModal
  v-if="usageProviderId"
  :provider-id="usageProviderId"
  :provider-name="usageProviderName"
  @close="closeProviderUsage()"
  @saved="handleProviderUsageSaved"
/>
```

- [ ] **Step 4: Add legacy route redirection and one-time query consumption**

Change the Provider Usage route in `main.ts` to:

```ts
{
  path: '/providers/:providerId/usage',
  name: 'provider-usage',
  redirect: to => ({
    path: '/',
    query: { tab: 'providers', usage_provider: String(to.params.providerId) },
  }),
},
```

At the start of Dashboard's `onMounted`, after reading `tab`, consume the parameter:

```ts
const searchParams = new URLSearchParams(window.location.search)
const legacyUsageProviderId = searchParams.get('usage_provider')
if (legacyUsageProviderId) {
  activeTab.value = 'providers'
  usageProviderId.value = legacyUsageProviderId
  searchParams.delete('usage_provider')
  const query = searchParams.toString()
  await router.replace(query ? `/?${query}` : '/')
}
```

- [ ] **Step 5: Update the existing Provider-card navigation contract**

Replace the existing `ProviderCard.test.ts` navigation test with:

```ts
test('DashboardView handles usage modal opening', () => {
  assert.match(dashSource, /openProviderUsage|ProviderUsageModal/)
})
```

- [ ] **Step 6: Run Dashboard, modal, and existing Provider-card tests**

Run:

```bash
node --test --experimental-strip-types internal/frontend/src/views/DashboardProviderUsageModal.test.ts
node --test --experimental-strip-types internal/frontend/src/components/ProviderUsageModal.test.ts
node --test --experimental-strip-types internal/frontend/src/components/ProviderCard.test.ts
```

Expected: all tests pass.

- [ ] **Step 7: Commit Dashboard and route integration**

```bash
git add internal/frontend/src/views/DashboardView.vue internal/frontend/src/views/DashboardProviderUsageModal.test.ts internal/frontend/src/components/ProviderCard.test.ts internal/frontend/src/main.ts
git commit -m "feat(frontend): open provider quota editor as modal"
```

### Task 4: Remove the full-page view and verify the complete application

**Files:**
- Delete: `internal/frontend/src/views/ProviderUsageView.vue`
- Modify through build: `internal/frontend/dist/**`

- [ ] **Step 1: Delete the obsolete full-page view**

Delete `internal/frontend/src/views/ProviderUsageView.vue` only after `main.ts` no longer imports it and all migrated modal tests pass.

- [ ] **Step 2: Prove no source code references the deleted view**

Run:

```bash
rg -n "ProviderUsageView|goToUsage\(" internal/frontend/src
```

Expected: no matches. The command exits 1 because no references remain.

- [ ] **Step 3: Run the full frontend suite**

Run:

```bash
npm --prefix internal/frontend test
```

Expected: all frontend tests pass with zero failures.

- [ ] **Step 4: Build production frontend assets**

Run:

```bash
npm --prefix internal/frontend run build
```

Expected: Vite succeeds, emits a lazy `ProviderUsageModal-*.js` chunk, removes the old `ProviderUsageView-*.js` asset, and refreshes `internal/frontend/dist/index.html` plus dependent hashes.

- [ ] **Step 5: Run the Go suite against rebuilt embedded assets**

Run:

```bash
go test ./...
```

Expected: every Go package passes.

- [ ] **Step 6: Check the final diff and commit rebuilt assets**

Run:

```bash
git diff --check
git status --short
git diff --stat
git add internal/frontend/src/views/ProviderUsageView.vue internal/frontend/dist
git commit -m "build(frontend): refresh provider quota modal assets"
```

Expected: only the obsolete source view deletion and deterministic frontend build artifacts are committed.

### Task 5: Final behavioral verification

**Files:**
- Verify only: all files changed by Tasks 1–4

- [ ] **Step 1: Re-run all required checks from a clean committed tree**

Run:

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
go test ./...
git diff --check
git status --short
```

Expected: frontend tests pass, Vite succeeds, Go tests pass, and Git reports a clean worktree.

- [ ] **Step 2: Manually verify the Docker flow**

Run:

```bash
docker compose up -d --build
```

Expected behavior in the browser:

1. Open the Providers tab and click Usage; the URL does not change.
2. The translucent modal opens over the Provider list with two desktop columns and a stacked mobile layout.
3. Test Query and Refresh Now keep the modal open.
4. Save persists configuration, runs a production query, updates the Provider card, and closes after success.
5. A failed post-save query keeps the modal open with “configuration saved, query failed”.
6. Disabling quota saves without querying, removes the card snapshot, and closes.
7. Directly opening `/providers/<id>/usage` lands on the Providers tab, opens the modal, and cleans `usage_provider` from the URL.
