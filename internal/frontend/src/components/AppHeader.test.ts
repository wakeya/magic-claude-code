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

test('header version and mode come from dashboard state, not duplicate status requests', () => {
  assert.match(source, /defineProps/)
  assert.match(source, /version\?: string/)
  assert.match(source, /configuredMode\?: ConnectionMode/)
  assert.match(source, /effectiveMode\?: ConnectionMode/)
  assert.match(source, /const currentVersion = computed\(\(\) => version \|\| 'dev'\)/)
  assert.doesNotMatch(source, /fetchStatusVersion/)
  assert.doesNotMatch(source, /api\.getStatus\(/)
  assert.doesNotMatch(source, /api\.getConfig\(/)
  assert.doesNotMatch(
    source,
    /const currentVersion = computed\(\(\) => updateInfo\.value\?\.current_version \|\| 'dev'\)/
  )
})

test('header keeps destructured dashboard props reactive across refreshes', () => {
  assert.match(source, /version = 'dev'[\s\S]*configuredMode = 'transparent'[\s\S]*effectiveMode = 'transparent'[\s\S]*} = defineProps/)
  assert.doesNotMatch(source, /withDefaults\(defineProps/)
})

test('header mode entry is compact and emits showConnectionMode instead of inline switching', () => {
  assert.match(source, /mode-entry/)
  assert.match(source, /t\('mode\.entry'\)/)
  assert.match(source, /modeTitle\(configuredMode\)/)
  assert.match(source, /t\('mode\.effective_mode'\)/)
  assert.match(source, /t\('mode\.details'\)/)
  assert.match(source, /showConnectionMode/)
  assert.match(source, /\$emit\('showConnectionMode'\)/)
  assert.doesNotMatch(source, /showModeModal/)
  assert.doesNotMatch(source, /saveMode\(opt\.value\)/)
  assert.doesNotMatch(source, /modeOptions/)
  assert.doesNotMatch(source, /modeSaving/)
  assert.doesNotMatch(source, /modeMessage/)
})

test('header no longer contains mode modal', () => {
  assert.doesNotMatch(source, /showModeModal = true/)
  assert.doesNotMatch(source, /mode\.modal_title/)
  assert.doesNotMatch(source, /mode\.close/)
})

test('header leaves mode-updated refresh ownership to dashboard', () => {
  assert.doesNotMatch(source, /addEventListener\('mcc:mode-updated'/)
  assert.doesNotMatch(source, /removeEventListener\('mcc:mode-updated'/)
})
