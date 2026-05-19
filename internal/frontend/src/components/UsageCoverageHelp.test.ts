import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'UsageCoverageHelp.vue'), 'utf8')

test('usage coverage help uses a custom hover tooltip', () => {
  assert.match(source, /group-hover:opacity-100/)
  assert.match(source, /USAGE_COVERAGE_TOOLTIP/)
  assert.doesNotMatch(source, /:title=/)
})
