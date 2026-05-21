import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'AppHeader.vue'), 'utf8')

test('header owns the global theme switch', () => {
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
