import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const mainSource = readFileSync(join(here, '..', 'main.ts'), 'utf8')

test('Dashboard lazily loads ProviderUsageModal', () => {
  assert.match(
    dashboardSource,
    /const ProviderUsageModal = defineAsyncComponent\(\(\) => import\('@\/components\/ProviderUsageModal\.vue'\)\)/,
  )
})

test('ProviderCard opens usage in the modal instead of navigating', () => {
  assert.match(dashboardSource, /@usage="openProviderUsage\(p\.id\)"/)
  assert.doesNotMatch(dashboardSource, /function goToUsage\(|router\.push\(`\/providers\/\$\{providerId\}\/usage`\)/)
})

test('Dashboard renders ProviderUsageModal with provider details and handlers', () => {
  assert.match(
    dashboardSource,
    /<ProviderUsageModal[\s\S]*?v-if="usageProviderId"[\s\S]*?:provider-id="usageProviderId"[\s\S]*?:provider-name="usageProviderName"[\s\S]*?@close="closeProviderUsage"[\s\S]*?@saved="handleProviderUsageSaved"/,
  )
})

test('saved usage snapshots update the card before the modal closes', () => {
  const handler = dashboardSource.match(
    /async function handleProviderUsageSaved\(snapshot: QuotaSnapshot \| null\)[\s\S]*?\n}/,
  )?.[0] || ''
  assert.match(handler, /const nextSnapshots = \{ \.\.\.quotaSnapshots\.value \}/)
  assert.match(handler, /nextSnapshots\[snapshot\.provider_id\] = snapshot[\s\S]*?quotaSnapshots\.value = nextSnapshots[\s\S]*?await closeProviderUsage\(false\)/)
})

test('a null saved snapshot removes the current provider quota snapshot', () => {
  const handler = dashboardSource.match(
    /async function handleProviderUsageSaved\(snapshot: QuotaSnapshot \| null\)[\s\S]*?\n}/,
  )?.[0] || ''
  assert.match(handler, /delete nextSnapshots\[usageProviderId\.value\]/)
})

test('normal modal close reloads snapshots and restores trigger focus after nextTick', () => {
  const handler = dashboardSource.match(
    /async function closeProviderUsage\(reloadSnapshots = true\)[\s\S]*?\n}/,
  )?.[0] || ''
  assert.match(handler, /usageProviderId\.value = ''/)
  assert.match(handler, /if \(reloadSnapshots\) await loadQuotaSnapshots\(\)[\s\S]*?await nextTick\(\)[\s\S]*?usageTriggerEl\.value\?\.focus\(\)/)
  assert.match(handler, /usageTriggerEl\.value = null/)
})

test('legacy usage route redirects once into the dashboard modal and cleans the URL', () => {
  assert.doesNotMatch(mainSource, /ProviderUsageView/)
  assert.match(
    mainSource,
    /path: '\/providers\/:providerId\/usage'[\s\S]*?redirect: \(to\) => \(\{[\s\S]*?path: '\/'[\s\S]*?query: \{ tab: 'providers', usage_provider: String\(to\.params\.providerId\) \}/,
  )
  assert.match(dashboardSource, /const usageProvider = query\.get\('usage_provider'\)/)
  assert.match(dashboardSource, /if \(usageProvider\)[\s\S]*?activeTab\.value = 'providers'[\s\S]*?usageProviderId\.value = usageProvider/)
  assert.match(dashboardSource, /query\.delete\('usage_provider'\)/)
  assert.match(dashboardSource, /await router\.replace\(query\.toString\(\) \? `\/\?\$\{query\.toString\(\)\}` : '\/'\)/)
})
