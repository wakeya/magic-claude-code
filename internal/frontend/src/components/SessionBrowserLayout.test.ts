import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'SessionBrowser.vue'), 'utf8')

test('desktop session outline panel scrolls independently when many user messages exist', () => {
  assert.match(source, /sticky top-4[^"]*max-h-\[calc\(100vh-2rem\)\][^"]*overflow-y-auto/)
  assert.match(source, /SessionOutline :messages="detail\.messages"/)
})

test('session browser no longer owns global theme switch', () => {
  assert.doesNotMatch(source, /session-theme-toggle/)
  assert.doesNotMatch(source, /setTheme\('light'\)/)
  assert.doesNotMatch(source, /setTheme\('dark'\)/)
})

test('session browser does not render the redundant selected-session hero block', () => {
  assert.doesNotMatch(source, /session-theme-hero/)
  assert.match(source, /v-if="!detail" class="session-empty-state"/)
})
