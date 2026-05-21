import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'UsageCoverageHelp.vue'), 'utf8')

test('usage coverage help uses a custom hover tooltip', () => {
  assert.match(source, /group-hover:opacity-100/)
  assert.match(source, /t\('usage\.usage_coverage_tip'\)/)
  assert.doesNotMatch(source, /:title=/)
})

test('usage coverage help renders readable tooltip colors', () => {
  assert.match(source, /bg-gray-700/)
  assert.match(source, /text-white/)
})

test('usage coverage help is triggered by the parent hover group', () => {
  assert.doesNotMatch(source, /class="group/)
  assert.match(source, /absolute bottom-full/)
})
