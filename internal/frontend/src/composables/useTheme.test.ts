import test from 'node:test'
import assert from 'node:assert/strict'

import { normalizeThemeMode, persistThemeMode, readStoredTheme, themeStorageKey } from './useTheme.ts'

class MemoryStorage {
  private values = new Map<string, string>()

  getItem(key: string): string | null {
    return this.values.get(key) ?? null
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value)
  }
}

test('normalizeThemeMode falls back to light for invalid values', () => {
  assert.equal(normalizeThemeMode(null), 'light')
  assert.equal(normalizeThemeMode(''), 'light')
  assert.equal(normalizeThemeMode('system'), 'light')
  assert.equal(normalizeThemeMode('dark'), 'dark')
})

test('readStoredTheme reads a persisted dark preference', () => {
  const storage = new MemoryStorage()
  storage.setItem(themeStorageKey, 'dark')

  assert.equal(readStoredTheme(storage), 'dark')
})

test('persistThemeMode stores the selected mode', () => {
  const storage = new MemoryStorage()

  persistThemeMode('dark', storage)

  assert.equal(storage.getItem(themeStorageKey), 'dark')
})
