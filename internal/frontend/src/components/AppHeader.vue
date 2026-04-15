<template>
  <header class="bg-white border-b-2 border-border px-8 h-16 flex items-center justify-between">
    <div class="flex items-center gap-3.5">
      <div class="w-8 h-8 bg-primary rounded-md flex items-center justify-center">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M12 2L2 7l10 5 10-5-10-5z" /><path d="M2 17l10 5 10-5" /><path d="M2 12l10 5 10-5" />
        </svg>
      </div>
      <h1 class="text-[17px] font-bold tracking-tight">Claude Code Proxy</h1>
    </div>
    <div class="flex items-center gap-3">
      <!-- Language Switcher -->
      <div class="relative">
        <button
          class="flex items-center gap-1.5 px-3 py-1.5 bg-transparent border-2 border-border rounded-lg text-[13px] font-semibold cursor-pointer text-text-secondary transition-all duration-200 hover:border-primary hover:text-primary"
          @click="langOpen = !langOpen"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="10" /><line x1="2" y1="12" x2="22" y2="12" /><path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z" />
          </svg>
          {{ locale === 'zh' ? '中文' : 'EN' }}
        </button>
        <div v-if="langOpen" class="absolute right-0 top-full mt-1 bg-white border-2 border-border rounded-lg overflow-hidden z-50 min-w-[100px]">
          <button
            v-for="opt in langOptions"
            :key="opt.value"
            :class="['w-full px-4 py-2.5 text-left text-sm font-medium border-none cursor-pointer transition-all duration-150', locale === opt.value ? 'bg-primary-light text-primary font-semibold' : 'bg-transparent text-fg hover:bg-muted']"
            @click="setLocale(opt.value); langOpen = false"
          >
            {{ opt.label }}
          </button>
        </div>
      </div>
      <button
        class="px-5 py-2 bg-transparent border-2 border-border text-text-secondary rounded-lg text-[13px] font-semibold cursor-pointer transition-all duration-200 hover:border-danger hover:text-danger hover:bg-danger-light"
        @click="$emit('logout')"
      >
        {{ t('header.logout') }}
      </button>
    </div>
  </header>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from '@/composables/useI18n'

defineEmits<{ logout: [] }>()

const { locale, t, setLocale } = useI18n()
const langOpen = ref(false)
const langOptions = [
  { value: 'zh' as const, label: '中文' },
  { value: 'en' as const, label: 'English' },
]

// 点击外部关闭下拉
if (typeof window !== 'undefined') {
  window.addEventListener('click', (e: MouseEvent) => {
    const target = e.target as HTMLElement
    if (!target.closest('.relative')) langOpen.value = false
  })
}
</script>
