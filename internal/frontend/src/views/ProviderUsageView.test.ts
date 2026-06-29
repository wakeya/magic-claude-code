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
