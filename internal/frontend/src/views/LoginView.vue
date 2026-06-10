<template>
  <div class="app-shell min-h-screen flex items-center justify-center relative overflow-hidden px-4">
    <a href="https://github.com/wakeya/magic-claude-code" target="_blank" rel="noopener noreferrer"
       class="absolute top-5 right-5 z-20 app-muted hover:text-fg transition-colors duration-200" :title="'GitHub'">
      <svg width="22" height="22" viewBox="0 0 24 24" fill="currentColor"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>
    </a>
    <div class="app-panel w-full max-w-[400px] relative z-10 p-8 rounded-xl shadow-xl">
      <div class="app-logo-mark w-14 h-14 rounded-lg flex items-center justify-center mx-auto mb-7">
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 2L2 7l10 5 10-5-10-5z" /><path d="M2 17l10 5 10-5" /><path d="M2 12l10 5 10-5" />
        </svg>
      </div>

      <h1 class="text-center text-[26px] font-extrabold tracking-tight mb-1.5">Magic Claude Code</h1>
      <p class="app-muted text-center text-sm mb-9">{{ t('login.subtitle') }}</p>

      <form @submit.prevent="handleLogin">
        <div class="mb-5">
          <label class="block text-[13px] font-semibold mb-2">{{ t('login.password') }}</label>
          <input
            v-model="password"
            type="password"
            class="app-control w-full px-4 py-3.5 rounded-lg text-[15px] transition-all duration-200 outline-none focus:border-primary"
            :placeholder="t('login.password_placeholder')"
            autofocus
          />
        </div>
        <button
          type="submit"
          :disabled="loading"
          class="w-full py-3.5 bg-primary text-white border-none rounded-lg text-[15px] font-semibold cursor-pointer transition-all duration-200 hover:bg-primary-hover hover:scale-[1.02] active:scale-[0.98] disabled:opacity-70 disabled:cursor-not-allowed disabled:transform-none"
        >
          {{ loading ? t('login.submitting') : t('login.submit') }}
        </button>
      </form>

      <p v-if="error" class="text-danger text-center text-sm font-medium mt-4">{{ error }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useApi } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import { useTheme } from '@/composables/useTheme'

const router = useRouter()
const api = useApi()
const { t } = useI18n()
useTheme()

const password = ref('')
const loading = ref(false)
const error = ref('')

async function handleLogin() {
  if (!password.value) return
  loading.value = true
  error.value = ''
  try {
    const ok = await api.login(password.value)
    if (ok) {
      router.push('/')
    } else {
      error.value = t('login.error.invalid')
    }
  } catch {
    error.value = t('login.error.network')
  } finally {
    loading.value = false
  }
}
</script>
