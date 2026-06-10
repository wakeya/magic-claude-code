import { computed, ref } from 'vue'

export type ThemeMode = 'light' | 'dark'

export const themeStorageKey = 'magic-claude-code-theme'

export interface ThemeStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

function browserStorage(): ThemeStorage | undefined {
  try {
    if (typeof globalThis === 'undefined' || !('localStorage' in globalThis)) return undefined
    return globalThis.localStorage
  } catch {
    return undefined
  }
}

export function normalizeThemeMode(value: unknown): ThemeMode {
  return value === 'dark' ? 'dark' : 'light'
}

export function readStoredTheme(storage: ThemeStorage | undefined = browserStorage()): ThemeMode {
  if (!storage) return 'light'
  try {
    return normalizeThemeMode(storage.getItem(themeStorageKey))
  } catch {
    return 'light'
  }
}

export function persistThemeMode(mode: ThemeMode, storage: ThemeStorage | undefined = browserStorage()): void {
  if (!storage) return
  try {
    storage.setItem(themeStorageKey, mode)
  } catch {
    // Ignore storage failures; theme state still updates in memory.
  }
}

export function resolveBackendTheme(
  value: unknown,
  storage: ThemeStorage | undefined = browserStorage()
): ThemeMode {
  if (value === 'light' || value === 'dark') {
    persistThemeMode(value, storage)
    return value
  }
  return readStoredTheme(storage)
}

const themeMode = ref<ThemeMode>(readStoredTheme())
const syncError = ref<string | null>(null)
let themeVersion = 0
let syncRequestVersion = 0

function applyTheme(mode: ThemeMode): void {
  if (typeof document !== 'undefined') {
    document.documentElement.dataset.theme = mode
  }
}

applyTheme(themeMode.value)

export function useTheme() {
  const isDark = computed(() => themeMode.value === 'dark')

  function setTheme(mode: ThemeMode) {
    themeVersion += 1
    themeMode.value = mode
    persistThemeMode(mode)
    applyTheme(mode)
  }

  function toggleTheme() {
    setTheme(themeMode.value === 'dark' ? 'light' : 'dark')
  }

  async function syncTheme(loadPreference: () => Promise<{ theme_mode: ThemeMode }>) {
    const requestVersion = ++syncRequestVersion
    const startThemeVersion = themeVersion
    try {
      const prefs = await loadPreference()
      if (requestVersion !== syncRequestVersion || startThemeVersion !== themeVersion) return
      syncError.value = null
      setTheme(resolveBackendTheme(prefs.theme_mode))
    } catch (err) {
      if (requestVersion !== syncRequestVersion || startThemeVersion !== themeVersion) return
      syncError.value = err instanceof Error ? err.message : 'Failed to sync theme'
      applyTheme(themeMode.value)
    }
  }

  async function persistTheme(
    savePreference: (mode: ThemeMode) => Promise<unknown>,
    mode: ThemeMode
  ) {
    setTheme(mode)
    try {
      await savePreference(mode)
      syncError.value = null
    } catch (err) {
      syncError.value = err instanceof Error ? err.message : 'Failed to save theme'
    }
  }

  return {
    themeMode,
    isDark,
    syncError,
    setTheme,
    toggleTheme,
    syncTheme,
    persistTheme,
  }
}
