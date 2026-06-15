<template>
  <header class="app-header border-b px-4 py-3 min-h-16 flex flex-wrap items-center justify-between gap-3 sm:px-8">
    <div class="flex items-center gap-3.5">
      <div class="app-logo-mark w-8 h-8 rounded-md flex items-center justify-center">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 2L2 7l10 5 10-5-10-5z" /><path d="M2 17l10 5 10-5" /><path d="M2 12l10 5 10-5" />
        </svg>
      </div>
      <h1 class="text-[17px] font-bold tracking-tight">Magic Claude Code</h1>
      <span v-if="!updateAvailable"
        class="text-[11px] app-muted font-mono"
      >{{ currentVersion }}</span>
      <button v-else
        type="button"
        class="update-pulse text-[12px] font-mono font-semibold px-2 py-0.5 rounded-md cursor-pointer transition-all duration-200 flex items-center gap-1"
        style="background: var(--app-accent-soft); color: var(--app-accent);"
        @click="showUpdateDialog = true"
        :title="t('update.title')"
      >
        {{ currentVersion }}
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><polyline points="7 10 12 15 17 10" /><line x1="12" y1="15" x2="12" y2="3" />
        </svg>
        {{ updateInfo?.latest_version }}
      </button>
    </div>
    <div class="flex flex-wrap items-center justify-end gap-2 sm:gap-3">
      <a href="https://github.com/wakeya/magic-claude-code" target="_blank" rel="noopener noreferrer"
         class="app-muted hover:text-fg transition-colors duration-200" title="GitHub">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>
      </a>
      <div class="app-theme-toggle" :aria-label="t('header.theme')">
        <button
          type="button"
          :aria-pressed="themeMode === 'light'"
          :class="['app-theme-toggle-button', themeMode === 'light' ? 'app-theme-toggle-active' : '']"
          @click="persistTheme(api.updatePreferences, 'light')"
        >
          <Sun class="h-3.5 w-3.5" />
          {{ t('header.theme_light') }}
        </button>
        <button
          type="button"
          :aria-pressed="themeMode === 'dark'"
          :class="['app-theme-toggle-button', themeMode === 'dark' ? 'app-theme-toggle-active' : '']"
          @click="persistTheme(api.updatePreferences, 'dark')"
        >
          <Moon class="h-3.5 w-3.5" />
          {{ t('header.theme_dark') }}
        </button>
      </div>
      <span v-if="syncError" class="text-[11px] text-[var(--app-danger)] truncate max-w-32" :title="syncError">{{ syncError }}</span>
      <!-- Language Switcher -->
      <div ref="langMenuRef" class="relative">
        <button
          type="button"
          class="app-control flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-semibold cursor-pointer transition-all duration-200"
          @click="langOpen = !langOpen"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" /><line x1="2" y1="12" x2="22" y2="12" /><path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z" />
          </svg>
          {{ locale === 'zh' ? '中文' : 'EN' }}
        </button>
        <div v-if="langOpen" class="app-panel absolute right-0 top-full mt-1 rounded-lg overflow-hidden z-50 min-w-[100px]">
          <button
            v-for="opt in langOptions"
            :key="opt.value"
            type="button"
            :class="['w-full px-4 py-2.5 text-left text-sm font-medium border-none cursor-pointer transition-all duration-150', locale === opt.value ? 'bg-primary-light text-primary font-semibold' : 'bg-transparent app-muted hover:bg-muted']"
            @click="setLocale(opt.value); langOpen = false"
          >
            {{ opt.label }}
          </button>
        </div>
      </div>
      <button
        type="button"
        class="app-control px-5 py-2 rounded-lg text-[13px] font-semibold cursor-pointer transition-all duration-200"
        @click="$emit('logout')"
      >
        {{ t('header.logout') }}
      </button>
    </div>
    <!-- Update Dialog -->
    <Teleport to="body">
      <div v-if="showUpdateDialog" class="fixed inset-0 z-[100] flex items-center justify-center" style="background: rgba(0,0,0,0.5);">
        <div class="app-panel rounded-xl p-6 max-w-md w-full mx-4 shadow-2xl">
          <h3 class="text-lg font-bold mb-4">{{ t('update.title') }}</h3>
          <div class="space-y-2 mb-4 text-sm">
            <div class="flex justify-between">
              <span class="app-muted">{{ t('update.current') }}:</span>
              <span class="font-mono">{{ updateInfo?.current_version }}</span>
            </div>
            <div class="flex justify-between">
              <span class="app-muted">{{ t('update.latest') }}:</span>
              <span class="font-mono font-bold" style="color: var(--app-accent);">{{ updateInfo?.latest_version }}</span>
            </div>
          </div>
          <p v-if="updateMessage" class="text-sm mb-3" style="color: var(--app-success);">{{ updateMessage }}</p>
          <p v-if="updateError" class="text-sm mb-3" style="color: var(--app-danger);">{{ updateError }}</p>
          <p class="text-xs app-muted mb-4">{{ t('update.confirm') }}</p>
          <div class="flex gap-3 justify-end">
            <button type="button" class="app-control px-4 py-2 rounded-lg text-sm font-semibold cursor-pointer" :disabled="updating"
              @click="showUpdateDialog = false">
              {{ t('update.cancel') }}
            </button>
            <button type="button" class="px-4 py-2 rounded-lg text-sm font-semibold text-white cursor-pointer transition-all duration-200"
              style="background: var(--app-accent);"
              :disabled="updating"
              @click="doApplyUpdate">
              {{ updating ? t('update.applying') : t('update.apply') }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </header>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Moon, Sun } from 'lucide-vue-next'
import { useApi, type UpdateCheckResult } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import { useTheme } from '@/composables/useTheme'

defineEmits<{ logout: [] }>()

const api = useApi()
const { locale, t, setLocale } = useI18n()
const { themeMode, persistTheme, syncError } = useTheme()
const langOpen = ref(false)
const langMenuRef = ref<HTMLElement | null>(null)
const langOptions = [
  { value: 'zh' as const, label: '中文' },
  { value: 'en' as const, label: 'English' },
]

const updateAvailable = ref(false)
const updateInfo = ref<UpdateCheckResult | null>(null)
const showUpdateDialog = ref(false)
const updating = ref(false)
const updateMessage = ref('')
const updateError = ref('')
const updateCheckStorageKey = 'magic-claude-code:last-update-check-at'
const updateCheckIntervalMs = 24 * 60 * 60 * 1000
const statusVersion = ref('dev')

const currentVersion = computed(() => statusVersion.value)

function shouldCheckForUpdate(now = Date.now()) {
  try {
    const lastChecked = Number(window.localStorage.getItem(updateCheckStorageKey) || '0')
    return !Number.isFinite(lastChecked) || lastChecked <= 0 || now - lastChecked >= updateCheckIntervalMs
  } catch {
    return true
  }
}

function markUpdateChecked(now = Date.now()) {
  try {
    window.localStorage.setItem(updateCheckStorageKey, String(now))
  } catch {
    // Ignore storage failures; update checks remain best-effort.
  }
}

async function fetchStatusVersion() {
  try {
    const status = await api.getStatus()
    if (status.version) statusVersion.value = status.version
  } catch {
    // silently ignore — version display is best-effort
  }
}

async function checkUpdate() {
  if (!shouldCheckForUpdate()) return
  markUpdateChecked()
  try {
    const result = await api.checkForUpdate()
    updateInfo.value = result
    updateAvailable.value = result.update_available
  } catch {
    // silently ignore — update check is best-effort
  }
}

async function doApplyUpdate() {
  updating.value = true
  updateMessage.value = ''
  updateError.value = ''
  try {
    const result = await api.applyUpdate()
    if (result.success) {
      updateAvailable.value = false
      updateMessage.value = result.message || t('update.success')
      if (result.restarting) {
        setTimeout(() => window.location.reload(), 3000)
      }
    } else {
      updateError.value = result.error || t('update.error')
    }
  } catch (e) {
    updateError.value = String(e)
  } finally {
    updating.value = false
  }
}

onMounted(() => {
  fetchStatusVersion()
  checkUpdate()
})

function closeLanguageMenuOnOutsideClick(e: MouseEvent) {
  const target = e.target as Node | null
  if (target && !langMenuRef.value?.contains(target)) langOpen.value = false
}

if (typeof window !== 'undefined') {
  window.addEventListener('click', closeLanguageMenuOnOutsideClick)
  onBeforeUnmount(() => {
    window.removeEventListener('click', closeLanguageMenuOnOutsideClick)
  })
}
</script>
