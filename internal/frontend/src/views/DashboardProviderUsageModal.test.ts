import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const mainSource = readFileSync(join(here, '..', 'main.ts'), 'utf8')
const loginSource = readFileSync(join(here, 'LoginView.vue'), 'utf8')

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
    /<ProviderUsageModal[\s\S]*?v-if="usageProviderId"[\s\S]*?:key="usageProviderId"[\s\S]*?:provider-id="usageProviderId"[\s\S]*?:provider-name="usageProviderName"[\s\S]*?@close="closeProviderUsage"[\s\S]*?@saved="handleProviderUsageSaved"/,
  )
})

test('saved usage snapshots update the card before the modal closes', () => {
  const handler = dashboardSource.match(
    /async function handleProviderUsageSaved\(snapshot: QuotaSnapshot \| null\)[\s\S]*?\n}/,
  )?.[0] || ''
  assert.match(handler, /const nextSnapshots = \{ \.\.\.quotaSnapshots\.value \}/)
  assert.match(handler, /quotaSnapshotLoadVersion \+= 1[\s\S]*?quotaSnapshots\.value = nextSnapshots/)
  assert.match(handler, /nextSnapshots\[snapshot\.provider_id\] = snapshot[\s\S]*?quotaSnapshots\.value = nextSnapshots[\s\S]*?await closeProviderUsage\(false\)/)
})

test('a null saved snapshot removes the current provider quota snapshot', () => {
  const handler = dashboardSource.match(
    /async function handleProviderUsageSaved\(snapshot: QuotaSnapshot \| null\)[\s\S]*?\n}/,
  )?.[0] || ''
  assert.match(handler, /delete nextSnapshots\[usageProviderId\.value\]/)
})

test('normal modal close restores its captured trigger before starting snapshot reload', () => {
  const handler = dashboardSource.match(
    /async function closeProviderUsage\(reloadSnapshots = true\)[\s\S]*?\n}/,
  )?.[0] || ''
  assert.match(handler, /const trigger = usageTriggerEl\.value/)
  assert.match(handler, /usageTriggerEl\.value = null[\s\S]*?usageProviderId\.value = ''/)
  assert.match(handler, /await nextTick\(\)[\s\S]*?if \(trigger\?\.isConnected\) trigger\.focus\(\)[\s\S]*?if \(reloadSnapshots\) void loadQuotaSnapshots\(\)/)
})

test('snapshot loads are versioned and direct snapshot refreshes invalidate older loads', () => {
  const loader = dashboardSource.match(/async function loadQuotaSnapshots\(\)[\s\S]*?\n}/)?.[0] || ''
  const refresh = dashboardSource.match(/async function refreshProviderQuota\(providerId: string\)[\s\S]*?\n}/)?.[0] || ''
  assert.match(dashboardSource, /let quotaSnapshotLoadVersion = 0/)
  assert.match(loader, /const loadVersion = \+\+quotaSnapshotLoadVersion[\s\S]*?await api\.getAllProviderUsageSnapshots\(\)[\s\S]*?if \(loadVersion !== quotaSnapshotLoadVersion\) return[\s\S]*?quotaSnapshots\.value = data\.snapshots \|\| \{}/)
  assert.match(refresh, /quotaSnapshotLoadVersion \+= 1[\s\S]*?quotaSnapshots\.value = \{ \.\.\.quotaSnapshots\.value, \[providerId\]: data\.snapshot \}/)
})

test('legacy usage route preserves navigation state and is consumed reactively', () => {
  assert.doesNotMatch(mainSource, /ProviderUsageView/)
  assert.match(
    mainSource,
    /path: '\/providers\/:providerId\/usage'[\s\S]*?redirect: \(to\) => \(\{[\s\S]*?path: '\/'[\s\S]*?query: \{ \.\.\.to\.query, tab: 'providers', usage_provider: String\(to\.params\.providerId\) \}[\s\S]*?hash: to\.hash/,
  )
  assert.match(dashboardSource, /import \{ useRoute, useRouter \} from 'vue-router'/)
  assert.match(dashboardSource, /watch\([\s\S]*?\(\) => route\.query\.usage_provider[\s\S]*?\{ immediate: true \}/)
  assert.match(dashboardSource, /Array\.isArray\(value\) \? value\[0\] : value/)
  assert.match(dashboardSource, /activeTab\.value = 'providers'[\s\S]*?usageTriggerEl\.value = null[\s\S]*?usageProviderId\.value = providerId/)
  assert.match(dashboardSource, /const \{ usage_provider: _usageProvider, \.\.\.query \} = route\.query/)
  assert.match(dashboardSource, /router\.replace\(\{ path: route\.path, query, hash: route\.hash \}\)/)
  const mounted = dashboardSource.match(/onMounted\(async \(\) => \{[\s\S]*?\n}\)/)?.[0] || ''
  assert.doesNotMatch(mounted, /usage_provider/)
})

test('authentication carries and safely restores the intended legacy destination', () => {
  assert.match(mainSource, /to\.fullPath\.startsWith\('\/'\) && !to\.fullPath\.startsWith\('\/\/'\)/)
  assert.match(mainSource, /\{ name: 'login', query: \{ redirect \} \}/)
  assert.match(loginSource, /import \{ useRoute, useRouter \} from 'vue-router'/)
  assert.match(loginSource, /Array\.isArray\(route\.query\.redirect\) \? route\.query\.redirect\[0\] : route\.query\.redirect/)
  assert.match(loginSource, /redirect\.startsWith\('\/'\) && !redirect\.startsWith\('\/\/'\)/)
  assert.match(loginSource, /router\.push\(destination\)/)
})
