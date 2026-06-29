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

test('wires compound save through runQuotaSaveFlow and delays successful saved event by 800ms', () => {
  assert.match(modalSource, /runQuotaSaveFlow\(payload,\s*\{/)
  assert.match(modalSource, /update:\s*\(data\)\s*=>\s*api\.updateProviderUsage\(props\.providerId,\s*data\)/)
  assert.match(modalSource, /query:\s*\(\)\s*=>\s*api\.queryProviderUsage\(props\.providerId\)/)
  assert.match(modalSource, /reload:\s*\(\)\s*=>\s*api\.getProviderUsage\(props\.providerId\)/)
  assert.match(modalSource, /setTimeout\(\(\)\s*=>\s*\{[\s\S]*?emit\('saved',\s*outcome\.snapshot\)[\s\S]*?\},\s*800\)/)
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
