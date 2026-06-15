import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'AppHeader.vue'), 'utf8')

test('header owns the global theme switch', () => {
  assert.match(source, /Magic Claude Code/)
  assert.match(source, /https:\/\/github\.com\/wakeya\/magic-claude-code/)
  assert.match(source, /useTheme/)
  assert.match(source, /persistTheme/)
  assert.match(source, /themeMode/)
  assert.match(source, /header\.theme_light/)
  assert.match(source, /header\.theme_dark/)
  assert.match(source, /:aria-pressed="themeMode === 'light'"/)
  assert.match(source, /:aria-pressed="themeMode === 'dark'"/)
  assert.match(source, /type="button"/)
  assert.match(source, /langMenuRef/)
  assert.match(source, /onBeforeUnmount/)
  assert.match(source, /removeEventListener\('click', closeLanguageMenuOnOutsideClick\)/)
  assert.doesNotMatch(source, /closest\('\\.relative'\)/)
})

test('header exposes theme sync error', () => {
  assert.match(source, /syncError/)
  assert.match(source, /v-if="syncError"/)
})

test('update apply success reloads only when backend is restarting', () => {
  assert.match(source, /updateMessage/)
  assert.match(source, /result\.message \|\| t\('update\.success'\)/)
  assert.match(source, /if \(result\.restarting\)/)
  assert.match(source, /window\.location\.reload/)
  assert.doesNotMatch(source, /alert\(t\('update\.success'\)\)/)
})

test('update check is throttled to once every 24 hours per browser', () => {
  assert.match(source, /updateCheckStorageKey/)
  assert.match(source, /updateCheckIntervalMs = 24 \* 60 \* 60 \* 1000/)
  assert.match(source, /function shouldCheckForUpdate/)
  assert.match(source, /function markUpdateChecked/)
  assert.match(source, /if \(!shouldCheckForUpdate\(\)\) return/)
  assert.match(source, /markUpdateChecked\(\)\s+try \{\s+const result = await api\.checkForUpdate\(\)/)
})

test('header version comes from status endpoint, not from update check', () => {
  assert.match(source, /const statusVersion = ref\('dev'\)/)
  assert.match(source, /const currentVersion = computed\(\(\) => statusVersion\.value\)/)
  assert.match(source, /async function fetchStatusVersion\(\)/)
  assert.match(source, /const status = await api\.getStatus\(\)/)
  assert.match(source, /if \(status\.version\) statusVersion\.value = status\.version/)
  assert.doesNotMatch(
    source,
    /const currentVersion = computed\(\(\) => updateInfo\.value\?\.current_version \|\| 'dev'\)/
  )
})
