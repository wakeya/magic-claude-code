import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'DashboardView.vue'), 'utf8')

test('listen status block exists in the status overview tab', () => {
  // The read-only listen status block must render all three services' addresses
  assert.match(source, /proxy_listen_addr/)
  assert.match(source, /admin_listen_addr/)
  assert.match(source, /gateway_listen_addr/)
})

test('listen status block uses i18n keys', () => {
  // i18n keys for the listen status labels and the modify hint
  assert.match(source, /listen\.status\.title/)
  assert.match(source, /listen\.status\.modify_hint/)
  assert.match(source, /listen\.status\.proxy_label/)
  assert.match(source, /listen\.status\.admin_label/)
  assert.match(source, /listen\.status\.gateway_label/)
})

test('listen status block is read-only (no input fields)', () => {
  // The listen status block must NOT contain input/v-model for listen fields
  // to prevent users from assuming they can edit them here.
  const listenStatusSection = source.match(/listen\.status\.title[\s\S]{0,2000}/)?.[0] || ''
  // No <input> or v-model in the listen status section
  assert.doesNotMatch(listenStatusSection, /<input/)
  assert.doesNotMatch(listenStatusSection, /v-model/)
})

test('StatusInfo interface includes listen address fields', () => {
  const useApi = readFileSync(join(here, '..', 'composables', 'useApi.ts'), 'utf8')
  assert.match(useApi, /proxy_listen_addr/)
  assert.match(useApi, /proxy_port/)
  assert.match(useApi, /admin_listen_addr/)
  assert.match(useApi, /admin_port/)
  assert.match(useApi, /gateway_listen_addr/)
  assert.match(useApi, /gateway_listen_port/)
})
