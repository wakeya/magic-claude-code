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
