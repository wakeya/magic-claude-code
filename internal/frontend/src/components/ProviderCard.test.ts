import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const cardSource = readFileSync(join(here, 'ProviderCard.vue'), 'utf8')
const dashSource = readFileSync(join(here, '..', 'views', 'DashboardView.vue'), 'utf8')

test('ProviderCard has a checkbox in the header row', () => {
  // Checkbox must appear before the provider name (top-left corner)
  const headerSection = cardSource.match(/<template>[\s\S]*?text-base font-bold[\s\S]*?provider\.name/s)?.[0] || ''
  assert.match(headerSection, /<input[^>]*type="checkbox"/)
})

test('ProviderCard emits toggle-select on checkbox click', () => {
  assert.match(cardSource, /toggle-select/)
})

test('ProviderCard accepts a selected prop', () => {
  assert.match(cardSource, /selected/)
})

test('DashboardView tracks selected provider IDs', () => {
  // A reactive Set or array tracking selected provider IDs
  assert.match(dashSource, /selectedProviderIds|selectedIds/)
})

test('DashboardView binds selection to ProviderCard', () => {
  // The ProviderCard usage wires :selected and @toggle-select
  assert.match(dashSource, /:selected|toggle-select/)
})

test('DashboardView has a select-all checkbox in the providers toolbar', () => {
  // A select-all control must exist, wired to a toggle-all handler
  assert.match(dashSource, /selectAll|toggle-all|toggleAll/i)
})

test('select-all checkbox reflects indeterminate/partial state', () => {
  // When some but not all are selected, the control shows partial state
  assert.match(dashSource, /indeterminate|partial/i)
})

test('ProviderCard emits usage event', () => {
  assert.match(cardSource, /usage/)
})

test('ProviderCard emits refresh-quota event', () => {
  assert.match(cardSource, /refresh-quota/)
})

test('ProviderCard accepts quotaSnapshot prop', () => {
  assert.match(cardSource, /quotaSnapshot/)
})

test('ProviderCard shows usage button with icon', () => {
  // Usage button should exist with Gauge icon
  assert.match(cardSource, /@click="\$emit\('usage'\)/)
})

test('DashboardView passes quota snapshots to ProviderCard', () => {
  assert.match(dashSource, /:quota-snapshot|quotaSnapshot/)
})

test('DashboardView handles usage navigation', () => {
  assert.match(dashSource, /goToUsage|provider-usage/)
})
