import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const i18nSource = readFileSync(join(here, '..', 'composables', 'useI18n.ts'), 'utf8')
const apiSource = readFileSync(join(here, '..', 'composables', 'useApi.ts'), 'utf8')

test('usage request log keeps the compact statistical columns', () => {
  for (const key of [
    'usage.time_range',
    'usage.provider',
    'usage.model',
    'usage.source_entrypoint',
    'usage.usage_source',
    'usage.usage_status',
    'usage.duration_ms',
    'usage.upstream_response_header_ms',
    'usage.time_to_first_byte_ms',
    'usage.status',
    'usage.tokens',
  ]) {
    assert.match(dashboardSource, new RegExp(`t\\('${key}'\\)`), `missing compact column ${key}`)
  }

  for (const rawHeader of [
    'id',
    'request_id',
    'ended_at',
    'error_type',
    'error_message',
    'method',
    'request_path',
    'backend_url',
    'provider_id',
    'provider_api_url',
    'source_app',
    'user_agent',
    'stream',
    'request_bytes',
    'response_bytes',
    'usage_parse_error',
  ]) {
    assert.doesNotMatch(dashboardSource, new RegExp(`<th[^>]*>${rawHeader}</th>`), `unexpected raw expanded header ${rawHeader}`)
  }
})

test('usage request log tokens column is total plus i18n token fields', () => {
  assert.match(dashboardSource, /tokenTotal\(row\)/)
  assert.match(dashboardSource, /t\('usage.input_tokens'\)/)
  assert.match(dashboardSource, /t\('usage.output_tokens'\)/)
  assert.match(dashboardSource, /t\('usage.cache_creation_input_tokens'\)/)
  assert.match(dashboardSource, /t\('usage.cache_read_input_tokens'\)/)
  assert.doesNotMatch(dashboardSource, /row\.input_tokens\s*}}\/{{\s*row\.output_tokens/)
})

test('usage request token field labels support Chinese and English', () => {
  for (const key of [
    'usage.tokens',
    'usage.input_tokens',
    'usage.output_tokens',
    'usage.cache_creation_input_tokens',
    'usage.cache_read_input_tokens',
    'usage.duration_ms',
    'usage.upstream_response_header_ms',
    'usage.time_to_first_byte_ms',
  ]) {
    assert.match(i18nSource, new RegExp(`'${key}':`), `missing i18n key ${key}`)
  }
})

test('usage request log uses the standard page content width', () => {
  assert.doesNotMatch(dashboardSource, /max-w-\[1920px\]/)
  assert.match(dashboardSource, /max-w-\[1440px\]/)
})

test('usage request log has bottom-right pagination with page size options', () => {
  assert.match(dashboardSource, /const usageRequestPage = ref\(1\)/)
  assert.match(dashboardSource, /const usageRequestPageSize = ref\(10\)/)
  assert.match(dashboardSource, /const usageRequestPageSizes = \[10, 20, 50, 100\]/)
  assert.match(dashboardSource, /justify-end/)
  assert.match(dashboardSource, /v-model\.number="usageRequestPageSize"/)
  assert.match(dashboardSource, /usageRequestPageSizes/)
})

test('usage request log fetches the selected page and page size', () => {
  assert.match(dashboardSource, /page: usageRequestPage\.value/)
  assert.match(dashboardSource, /page_size: usageRequestPageSize\.value/)
  assert.match(dashboardSource, /goToUsageRequestPage/)
})

test('usage request pagination labels support Chinese and English', () => {
  for (const key of [
    'usage.page_size',
    'usage.per_page',
    'usage.first_page',
    'usage.prev_page',
    'usage.next_page',
    'usage.last_page',
    'usage.page_summary',
  ]) {
    assert.match(i18nSource, new RegExp(`'${key}':`), `missing i18n key ${key}`)
    assert.match(dashboardSource, new RegExp(`t\\('${key}'`), `missing dashboard usage of ${key}`)
  }
})

test('usage coverage tooltip uses bilingual i18n text', () => {
  assert.match(i18nSource, /'usage\.usage_coverage_tip': 'Usage 覆盖率 =/)
  assert.match(i18nSource, /'usage\.usage_coverage_tip': 'Usage Coverage =/)
})

test('usage coverage tooltip is triggered from status and usage parent blocks', () => {
  assert.match(dashboardSource, /class="app-panel p-5 rounded-lg group relative"[\s\S]*t\('status\.usage_coverage'\)[\s\S]*<UsageCoverageHelp \/>/)
  assert.match(dashboardSource, /class="app-panel p-5 rounded-lg group relative"[\s\S]*t\('usage\.usage_coverage'\)[\s\S]*<UsageCoverageHelp \/>/)
  assert.match(dashboardSource, /<th class="py-3 pr-4 group relative">[\s\S]*<UsageCoverageHelp \/>/)
})

test('usage coverage table can scroll horizontally when wider than the panel', () => {
  assert.match(dashboardSource, /v-if="activeUsageTab === 'coverage'" class="app-panel p-5 rounded-lg overflow-x-auto"/)
  assert.doesNotMatch(dashboardSource, /v-if="activeUsageTab === 'coverage'"[^>]*overflow-x:clip/)
  assert.match(dashboardSource, /v-if="activeUsageTab === 'coverage'"[\s\S]*<table class="min-w-\[1400px\] w-full text-sm">/)
  assert.match(dashboardSource, /v-if="activeUsageTab === 'coverage'"[\s\S]*<UsageCoverageHelp placement="bottom" \/>/)
})

test('usage page exposes stats scope controls and sends stats_scope', () => {
  for (const key of [
    'usage.stats_scope',
    'usage.stats_scope_effective',
    'usage.stats_scope_provider',
    'usage.stats_scope_session_log',
    'usage.stats_scope_raw',
  ]) {
    assert.match(i18nSource, new RegExp(`'${key}':`), `missing i18n key ${key}`)
    assert.match(dashboardSource, new RegExp(`t\\('${key}'`), `missing dashboard usage of ${key}`)
  }

  for (const key of [
    'usage.stats_scope_tip_effective',
    'usage.stats_scope_tip_provider',
    'usage.stats_scope_tip_session_log',
    'usage.stats_scope_tip_raw',
  ]) {
    assert.match(i18nSource, new RegExp(`'${key}':`), `missing i18n key ${key}`)
  }
  assert.match(dashboardSource, /stats_scope: 'effective'/)
  assert.match(dashboardSource, /stats_scope: usageFilters\.stats_scope/)
})

test('usage stats scope filter has a hover help tooltip', () => {
  assert.match(dashboardSource, /import UsageStatsScopeHelp from '@\/components\/UsageStatsScopeHelp\.vue'/)
  assert.match(dashboardSource, /t\('usage\.stats_scope'\)[\s\S]*<UsageStatsScopeHelp \/>/)
  const statsScopeHelpSource = readFileSync(join(here, '..', 'components', 'UsageStatsScopeHelp.vue'), 'utf8')
  assert.match(statsScopeHelpSource, /w-96/)
  assert.match(statsScopeHelpSource, /v-for="item in statsScopeTips"/)
  assert.match(statsScopeHelpSource, /t\(item\.labelKey\)/)
  assert.match(statsScopeHelpSource, /t\(item\.descriptionKey\)/)
})

test('usage page exposes bilingual date range presets between tabs and filters', () => {
  for (const key of [
    'usage.date_range',
    'usage.range_today',
    'usage.range_last_7_days',
    'usage.range_last_30_days',
  ]) {
    assert.match(i18nSource, new RegExp(`'${key}':`), `missing i18n key ${key}`)
    assert.match(dashboardSource, new RegExp(`labelKey: '${key}'|t\\('${key}'`), `missing dashboard usage of ${key}`)
  }

  assert.match(dashboardSource, /const usageDateRangePresets/)
  assert.match(dashboardSource, /key: 'today'/)
  assert.match(dashboardSource, /key: 'last_7_days'/)
  assert.match(dashboardSource, /key: 'last_30_days'/)
  assert.match(dashboardSource, /@click="applyUsageDateRangePreset\(preset\.key\)"/)
})

test('usage page exposes clear data action with session sync reset option', () => {
  for (const key of [
    'usage.clear_data',
    'usage.clear_data_title',
    'usage.clear_data_confirm',
    'usage.clear_data_reset_session_sync',
    'usage.clear_data_reset_session_sync_hint',
    'usage.clear_data_success',
    'usage.clear_data_failed',
  ]) {
    assert.match(i18nSource, new RegExp(`'${key}':`), `missing i18n key ${key}`)
    assert.match(dashboardSource, new RegExp(`t\\('${key}'`), `missing dashboard usage of ${key}`)
  }

  assert.match(apiSource, /interface UsageClearResult/)
  assert.match(apiSource, /async function clearUsageData/)
  assert.match(apiSource, /fetch\('\/api\/usage\/clear'/)
  assert.match(apiSource, /reset_session_sync/)
  assert.match(apiSource, /clearUsageData/)
  assert.match(dashboardSource, /@click="openUsageClearModal"/)
  assert.match(dashboardSource, /v-if="showUsageClearModal"/)
  assert.match(dashboardSource, /v-model="resetUsageSessionSync"/)
  assert.match(dashboardSource, /api\.clearUsageData\(resetUsageSessionSync\.value\)/)
  assert.match(dashboardSource, /await loadUsageData\(\)/)
})

test('usage date range presets default to the last 7 complete days excluding today', () => {
  assert.match(dashboardSource, /const defaultUsageDateRange = usageDateRangeForPreset\('last_7_days'\)/)
  assert.match(dashboardSource, /from: defaultUsageDateRange\.from/)
  assert.match(dashboardSource, /to: defaultUsageDateRange\.to/)
  assert.match(dashboardSource, /function usageDateRangeForPreset/)
  assert.match(dashboardSource, /type="datetime-local"/)
  assert.match(dashboardSource, /step="1"/)
  assert.match(dashboardSource, /function formatDateTimeInput/)
  assert.match(dashboardSource, /new Date\(date\.getFullYear\(\), date\.getMonth\(\), date\.getDate\(\), 0, 0, 0, 0\)/)
  assert.match(dashboardSource, /new Date\(date\.getFullYear\(\), date\.getMonth\(\), date\.getDate\(\), 23, 59, 59, 0\)/)
  assert.match(dashboardSource, /case 'last_7_days':/)
  assert.match(dashboardSource, /return inclusiveDateTimeRange\(7, 1\)/)
  assert.match(dashboardSource, /case 'last_30_days':/)
  assert.match(dashboardSource, /return inclusiveDateTimeRange\(30, 1\)/)
  assert.match(dashboardSource, /case 'today':/)
  assert.match(dashboardSource, /return inclusiveDateTimeRange\(0, 0\)/)
  assert.match(dashboardSource, /const activeUsageDateRangePreset = computed/)
})

test('usage request log displays session log source and duplicate marker', () => {
  assert.match(dashboardSource, /<option value="session_log">/)
  assert.match(dashboardSource, /usage\.usage_source_session_log/)
  assert.match(dashboardSource, /usage\.dedupe_duplicate/)
  assert.match(dashboardSource, /row\.dedupe_status === 'duplicate'/)
})

test('usage overview lazy loads echarts instead of bundling it in the main chunk', () => {
  assert.doesNotMatch(dashboardSource, /import \* as echarts from 'echarts'/)
  assert.match(dashboardSource, /import type \{ EChartsType \} from 'echarts\/core'/)
  assert.match(dashboardSource, /import\('echarts\/core'\)/)
  assert.match(dashboardSource, /import\('echarts\/charts'\)/)
  assert.match(dashboardSource, /import\('echarts\/components'\)/)
  assert.match(dashboardSource, /import\('echarts\/renderers'\)/)
})

test('usage overview chart uses app theme tokens', () => {
  assert.match(dashboardSource, /function usageChartTheme\(\)/)
  assert.match(dashboardSource, /appCssVar\('--app-text'/)
  assert.match(dashboardSource, /appCssVar\('--app-surface-raised'/)
  assert.match(dashboardSource, /legend: \{ top: 0, textStyle: \{ color: theme\.muted \} \}/)
  assert.match(dashboardSource, /tooltip: \{[\s\S]*backgroundColor: theme\.surface/)
  assert.match(dashboardSource, /axisLabel: \{ color: theme\.muted/)
  assert.match(dashboardSource, /watch\(themeMode/)
})
