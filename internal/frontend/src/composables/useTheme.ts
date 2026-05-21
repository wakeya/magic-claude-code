import { computed, ref } from 'vue'

export type ThemeMode = 'light' | 'dark'

export const themeStorageKey = 'claude-proxy-theme'

export interface ThemeStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

function defaultStorage(): ThemeStorage | undefined {
  if (typeof localStorage === 'undefined') return undefined
  return localStorage
}

export function normalizeThemeMode(value: string | null | undefined): ThemeMode {
  return value === 'dark' ? 'dark' : 'light'
}

export function readStoredTheme(storage: ThemeStorage | undefined = defaultStorage()): ThemeMode {
  if (!storage) return 'light'
  try {
    return normalizeThemeMode(storage.getItem(themeStorageKey))
  } catch {
    return 'light'
  }
}

export function persistThemeMode(mode: ThemeMode, storage: ThemeStorage | undefined = defaultStorage()): void {
  if (!storage) return
  try {
    storage.setItem(themeStorageKey, mode)
  } catch {
    // Storage can fail in private browsing or locked-down browser contexts.
  }
}

const themeMode = ref<ThemeMode>(readStoredTheme())

export function useTheme() {
  const isDark = computed(() => themeMode.value === 'dark')

  function setTheme(mode: ThemeMode) {
    themeMode.value = mode
    persistThemeMode(mode)
  }

  function toggleTheme() {
    setTheme(isDark.value ? 'light' : 'dark')
  }

  return {
    themeMode,
    isDark,
    setTheme,
    toggleTheme,
  }
}
