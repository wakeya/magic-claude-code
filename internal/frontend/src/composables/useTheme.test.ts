import test from 'node:test'
import assert from 'node:assert/strict'

import {
  normalizeThemeMode,
  persistThemeMode,
  readStoredTheme,
  resolveBackendTheme,
  themeStorageKey,
  useTheme,
} from './useTheme.ts'

class MemoryStorage {
  private values = new Map<string, string>()

  getItem(key: string): string | null {
    return this.values.get(key) ?? null
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value)
  }
}

class ThrowingStorage {
  getItem(): string | null {
    throw new Error('storage locked')
  }

  setItem(): void {
    throw new Error('storage locked')
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

test('readStoredTheme falls back to light when storage throws', () => {
  assert.equal(readStoredTheme(new ThrowingStorage()), 'light')
})

test('persistThemeMode ignores storage write failures', () => {
  assert.doesNotThrow(() => persistThemeMode('dark', new ThrowingStorage()))
})

test('resolveBackendTheme returns backend value and persists it', () => {
  const storage = new MemoryStorage()

  const got = resolveBackendTheme('dark', storage)

  assert.equal(got, 'dark')
  assert.equal(storage.getItem(themeStorageKey), 'dark')
})

test('resolveBackendTheme falls back to stored theme for invalid backend value', () => {
  const storage = new MemoryStorage()
  storage.setItem(themeStorageKey, 'dark')

  const got = resolveBackendTheme('system', storage)

  assert.equal(got, 'dark')
})

test('resolveBackendTheme defaults to light when backend and storage are invalid', () => {
  const storage = new MemoryStorage()
  storage.setItem(themeStorageKey, 'system')

  const got = resolveBackendTheme('system', storage)

  assert.equal(got, 'light')
})

test('persistTheme keeps local selection when backend save fails', async () => {
  const { themeMode, persistTheme, setTheme, syncError } = useTheme()
  setTheme('light')

  await persistTheme(async () => {
    throw new Error('offline')
  }, 'dark')

  assert.equal(themeMode.value, 'dark')
  assert.equal(syncError.value, 'offline')
})

test('syncTheme does not overwrite a newer local selection', async () => {
  const { themeMode, syncTheme, persistTheme, setTheme } = useTheme()
  setTheme('light')
  let resolvePreference: (value: { theme_mode: 'light' }) => void = () => {}
  const pendingPreference = new Promise<{ theme_mode: 'light' }>((resolve) => {
    resolvePreference = resolve
  })

  const syncPromise = syncTheme(() => pendingPreference)
  await persistTheme(async () => undefined, 'dark')
  resolvePreference({ theme_mode: 'light' })
  await syncPromise

  assert.equal(themeMode.value, 'dark')
})

test('stale syncTheme failure does not replace a newer successful local state', async () => {
  const { syncTheme, persistTheme, setTheme, syncError } = useTheme()
  setTheme('light')
  syncError.value = null
  let rejectPreference: (reason: Error) => void = () => {}
  const pendingPreference = new Promise<{ theme_mode: 'light' }>((_, reject) => {
    rejectPreference = reject
  })

  const syncPromise = syncTheme(() => pendingPreference)
  await persistTheme(async () => undefined, 'dark')
  rejectPreference(new Error('stale offline'))
  await syncPromise

  assert.equal(syncError.value, null)
})
