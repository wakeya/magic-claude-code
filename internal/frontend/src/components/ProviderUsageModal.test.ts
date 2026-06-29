import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const modalPath = join(here, 'ProviderUsageModal.vue')
const modalExists = existsSync(modalPath)
const modalSource = modalExists ? readFileSync(modalPath, 'utf8') : ''
const i18nSource = readFileSync(join(here, '../composables/useI18n.ts'), 'utf8')

function functionSource(name: string, nextName: string): string {
  const start = modalSource.indexOf(`async function ${name}(`)
  const end = modalSource.indexOf(`async function ${nextName}(`, start + 1)
  return start >= 0 && end > start ? modalSource.slice(start, end) : ''
}

function sourceBetween(startMarker: string, endMarker: string): string {
  const start = modalSource.indexOf(startMarker)
  const end = modalSource.indexOf(endMarker, start + startMarker.length)
  return start >= 0 && end > start ? modalSource.slice(start, end) : ''
}

test('ProviderUsageModal exists before its source contract is inspected', () => {
  assert.equal(modalExists, true, 'ProviderUsageModal.vue must exist')
})

test('declares route-independent provider props and close/saved events', () => {
  assert.match(modalSource, /defineProps<\{\s*providerId:\s*string\s*providerName:\s*string\s*\}>\(\)/s)
  assert.match(modalSource, /defineEmits<\{\s*close:\s*\[\]\s*saved:\s*\[snapshot:\s*QuotaSnapshot\s*\|\s*null\]\s*\}>\(\)/s)
  assert.doesNotMatch(modalSource, /useRoute|useRouter|vue-router/)
  assert.match(modalSource, /api\.getProviderUsage\(props\.providerId\)/)
  assert.doesNotMatch(modalSource, /api\.getProviders\(/)
})

test('renders the responsive, accessible shared modal shell and complete result layout', () => {
  assert.match(modalSource, /fixed inset-0 bg-black\/50 z-50 flex[^"\n]*items-center[^"\n]*px-4/)
  assert.match(modalSource, /@click\.self="requestClose"/)
  assert.match(modalSource, /ref="dialogEl"/)
  assert.match(modalSource, /app-panel rounded-lg w-\[90vw\] max-w-\[1180px\] max-h-\[90vh\] overflow-y-auto/)
  assert.match(modalSource, /role="dialog"/)
  assert.match(modalSource, /aria-modal="true"/)
  assert.match(modalSource, /aria-labelledby="provider-usage-title"/)
  assert.match(modalSource, /id="provider-usage-title"/)
  assert.match(modalSource, /tabindex="-1"/)
  assert.match(modalSource, /grid grid-cols-1 lg:grid-cols-2/)
  assert.match(modalSource, /<QuotaResultDisplay\s+:result="testResult"/)
  assert.match(modalSource, /<QuotaResultDisplay[^>]*:result="snapshot\.result"/)
  assert.match(modalSource, /snapshot\.is_stale/)
  assert.match(modalSource, /snapshot\.queried_at/)
})

test('locks body scrolling, focuses the dialog, and closes on Escape', () => {
  assert.match(modalSource, /document\.body\.style\.overflow\s*=\s*'hidden'/)
  assert.match(modalSource, /document\.addEventListener\('keydown',\s*handleKeydown\)/)
  assert.match(modalSource, /event\.key\s*===\s*'Escape'/)
  assert.match(modalSource, /await nextTick\(\)/)
  assert.match(modalSource, /dialogEl\.value\?\.focus\(\)/)
  assert.match(modalSource, /document\.body\.style\.overflow\s*=\s*previousBodyOverflow/)
  assert.match(modalSource, /document\.removeEventListener\('keydown',\s*handleKeydown\)/)
})

test('traps forward and reverse Tab focus inside the dialog', () => {
  assert.match(modalSource, /event\.key\s*!==\s*'Tab'/)
  assert.match(modalSource, /dialogEl\.value\.querySelectorAll<HTMLElement>\(/)
  assert.match(modalSource, /button:not\(\[disabled\]\)/)
  assert.match(modalSource, /if \(focusable\.length === 0\)[\s\S]*?event\.preventDefault\(\)[\s\S]*?dialogEl\.value\.focus\(\)/)
  assert.match(modalSource, /!focusable\.includes\(active as HTMLElement\)/)
  assert.match(modalSource, /event\.shiftKey[\s\S]*?last\.focus\(\)/)
  assert.match(modalSource, /!event\.shiftKey[\s\S]*?first\.focus\(\)/)
})

test('settles the save delay and prevents post-unmount async mutations or events', () => {
  assert.match(modalSource, /let disposed = false/)
  assert.match(modalSource, /let settleSavedDelay:\s*\(\(\)\s*=>\s*void\)\s*\|\s*null\s*=\s*null/)
  assert.match(modalSource, /function waitForSavedDelay\(\): Promise<boolean>[\s\S]*?setTimeout\([\s\S]*?800\)/)

  const loadSource = sourceBetween('async function loadConfig()', 'async function testQuery()')
  const testSource = sourceBetween('async function testQuery()', 'async function refreshNow()')
  const refreshSource = sourceBetween('async function refreshNow()', 'async function saveConfig()')
  const saveSource = sourceBetween('async function saveConfig()', 'onMounted(')
  const mountedSource = sourceBetween('onMounted(', 'onUnmounted(')
  const unmountedSource = modalSource.slice(modalSource.indexOf('onUnmounted('))

  assert.match(loadSource, /await api\.getProviderUsage\([\s\S]*?if \(disposed\) return/)
  assert.match(testSource, /await api\.testProviderUsage\([\s\S]*?if \(disposed\) return/)
  assert.match(refreshSource, /await api\.queryProviderUsage\([\s\S]*?if \(disposed\) return[\s\S]*?await api\.getProviderUsage\([\s\S]*?if \(disposed\) return/)
  assert.match(saveSource, /await runQuotaSaveFlow\([\s\S]*?if \(disposed\) return/)
  assert.match(saveSource, /const shouldEmit = await waitForSavedDelay\(\)[\s\S]*?if \(!shouldEmit \|\| disposed\) return[\s\S]*?emit\('saved',\s*outcome\.snapshot\)/)
  assert.match(mountedSource, /await nextTick\(\)[\s\S]*?if \(disposed\) return/)
  assert.match(unmountedSource, /disposed = true[\s\S]*?clearTimeout\(savedTimer\)[\s\S]*?settleSavedDelay\?\.\(\)[\s\S]*?document\.body\.style\.overflow/)
})

test('keeps both columns and credential rows shrinkable on narrow screens', () => {
  assert.equal((modalSource.match(/<section class="min-w-0">/g) || []).length, 2)
  assert.ok(
    (modalSource.match(/class="flex flex-wrap sm:flex-nowrap gap-2 min-w-0"/g) || []).length >= 4,
    'all credential rows should wrap safely',
  )
  assert.ok(
    (modalSource.match(/class="min-w-0 flex-1 app-control/g) || []).length >= 4,
    'credential inputs should be allowed to shrink',
  )
})

test('wires compound save through runQuotaSaveFlow and delays successful saved event by 800ms', () => {
  assert.match(modalSource, /runQuotaSaveFlow\(payload,\s*\{/)
  assert.match(modalSource, /update:\s*\(data\)\s*=>\s*api\.updateProviderUsage\(props\.providerId,\s*data\)/)
  assert.match(modalSource, /query:\s*\(\)\s*=>\s*api\.queryProviderUsage\(props\.providerId\)/)
  assert.match(modalSource, /reload:\s*\(\)\s*=>\s*api\.getProviderUsage\(props\.providerId\)/)
  assert.match(modalSource, /const shouldEmit = await waitForSavedDelay\(\)/)
  assert.match(modalSource, /if \(!shouldEmit \|\| disposed\) return[\s\S]*?emit\('saved',\s*outcome\.snapshot\)/)
  assert.match(modalSource, /if \(outcome\.configSaved\)[\s\S]*?savedConfig\.value\s*=\s*outcome\.config[\s\S]*?clearSubmittedSecrets\(\)/)
  assert.match(modalSource, /function clearSubmittedSecrets\(\)[\s\S]*?form\.script_api_key\s*=\s*''[\s\S]*?form\.clear_secret_access_key\s*=\s*false/)
  assert.match(modalSource, /t\('quota\.saved_query_failed',\s*\{\s*error:\s*outcome\.error\s*\}\)/)
})

test('test and refresh operations stay open and expose failures', () => {
  const testQuerySource = functionSource('testQuery', 'refreshNow')
  const refreshSource = functionSource('refreshNow', 'saveConfig')
  assert.doesNotMatch(testQuerySource, /emit\(['"](?:close|saved)/)
  assert.doesNotMatch(refreshSource, /emit\(['"](?:close|saved)/)
  assert.match(testQuerySource, /error_code:\s*'network_error'/)
  assert.match(refreshSource, /refreshError\.value\s*=\s*error/)
  assert.match(modalSource, /v-if="refreshError"/)
})

test('keeps the ZenMux utility alias distinct from its computed template binding', () => {
  assert.match(modalSource, /showZenMuxFields as shouldShowZenMuxFields/)
  assert.match(modalSource, /const isZenMux = computed\(\(\) => shouldShowZenMuxFields\(/)
  assert.match(modalSource, /const showZenMuxFields = isZenMux/)
})

test('defines required bilingual modal messages', () => {
  assert.match(i18nSource, /'quota\.modal_subtitle': '配置自动查询并查看最新结果'/)
  assert.match(i18nSource, /'quota\.load_failed': '加载用量配置失败'/)
  assert.match(i18nSource, /'quota\.saved_query_failed': '配置已保存，但查询失败：\{error\}'/)
  assert.match(i18nSource, /'quota\.modal_subtitle': 'Configure automatic queries and view the latest result'/)
  assert.match(i18nSource, /'quota\.load_failed': 'Failed to load quota configuration'/)
  assert.match(i18nSource, /'quota\.saved_query_failed': 'Configuration saved, but the query failed: \{error\}'/)
})

test('uses a localized save-success message when disabled config skips querying', () => {
  assert.match(modalSource, /outcome\.snapshot === null\s*\? t\('quota\.save_success'\)\s*:\s*t\('quota\.query_success'\)/)
  assert.match(i18nSource, /'quota\.save_success': '配置保存成功'/)
  assert.match(i18nSource, /'quota\.save_success': 'Configuration saved successfully'/)
})
