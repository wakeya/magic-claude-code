import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const dashboardSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')
const apiSource = readFileSync(join(here, '..', 'composables', 'useApi.ts'), 'utf8')

const requestLogFields = [
  'id',
  'request_id',
  'started_at',
  'ended_at',
  'duration_ms',
  'upstream_response_header_ms',
  'time_to_first_byte_ms',
  'status_code',
  'error_type',
  'error_message',
  'method',
  'request_path',
  'backend_url',
  'provider_id',
  'provider_name',
  'provider_api_url',
  'source_app',
  'source_entrypoint',
  'user_agent',
  'original_model',
  'mapped_model',
  'stream',
  'request_bytes',
  'response_bytes',
  'tokens',
  'input_tokens',
  'output_tokens',
  'cache_creation_input_tokens',
  'cache_read_input_tokens',
  'usage_source',
  'usage_parse_status',
  'usage_parse_error',
]

test('usage request log table expands all row fields', () => {
  for (const field of requestLogFields) {
    assert.match(dashboardSource, new RegExp(`>${field}<|\\b${field}\\b`), `missing request log field ${field}`)
  }
})

test('usage request log tokens column is total plus named token fields', () => {
  assert.match(dashboardSource, /tokenTotal\(row\)/)
  assert.match(dashboardSource, />input_tokens</)
  assert.match(dashboardSource, />output_tokens</)
  assert.match(dashboardSource, />cache_creation_input_tokens</)
  assert.match(dashboardSource, />cache_read_input_tokens</)
  assert.doesNotMatch(dashboardSource, /row\.input_tokens\s*}}\/{{\s*row\.output_tokens/)
})

test('usage request row type includes all fields returned by API', () => {
  for (const field of requestLogFields.filter((field) => field !== 'tokens')) {
    assert.match(apiSource, new RegExp(`${field}:`), `missing UsageRequestRow property ${field}`)
  }
})
