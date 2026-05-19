import test from 'node:test'
import assert from 'node:assert/strict'

import { USAGE_COVERAGE_TOOLTIP, formatPercent } from './formatters.ts'

test('formatPercent keeps four decimal places', () => {
  assert.equal(formatPercent(0.9969798658), '99.6980%')
  assert.equal(formatPercent(1), '100.0000%')
  assert.equal(formatPercent(0), '0.0000%')
  assert.equal(formatPercent(null), '0.0000%')
})

test('usage coverage tooltip explains the calculation', () => {
  assert.match(USAGE_COVERAGE_TOOLTIP, /成功解析到 usage/)
  assert.match(USAGE_COVERAGE_TOOLTIP, /请求总数/)
})
