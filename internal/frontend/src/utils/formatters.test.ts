import test from 'node:test'
import assert from 'node:assert/strict'

import { formatPercent } from './formatters.ts'

test('formatPercent keeps four decimal places', () => {
  assert.equal(formatPercent(0.9969798658), '99.6980%')
  assert.equal(formatPercent(1), '100.0000%')
  assert.equal(formatPercent(0), '0.0000%')
  assert.equal(formatPercent(null), '0.0000%')
})
