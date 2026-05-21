<template>
  <div class="app-shell min-h-screen flex items-center justify-center relative overflow-hidden px-4">
    <div class="app-panel w-full max-w-[400px] relative z-10 p-8 rounded-xl shadow-xl">
      <div class="app-logo-mark w-14 h-14 rounded-lg flex items-center justify-center mx-auto mb-7">
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 2L2 7l10 5 10-5-10-5z" /><path d="M2 17l10 5 10-5" /><path d="M2 12l10 5 10-5" />
        </svg>
      </div>

      <h1 class="text-center text-[26px] font-extrabold tracking-tight mb-1.5">Claude Code Proxy</h1>
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
