import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const i18nSource = readFileSync(join(here, '..', 'composables', 'useI18n.ts'), 'utf8')

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

  assert.match(dashboardSource, /stats_scope: 'effective'/)
  assert.match(dashboardSource, /stats_scope: usageFilters\.stats_scope/)
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
