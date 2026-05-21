<template>
  <span class="inline-flex items-center">
    <CircleHelp class="h-3.5 w-3.5 cursor-help text-text-secondary" :aria-label="t('usage.usage_coverage')" />
    <span
      role="tooltip"
      class="pointer-events-none absolute bottom-full left-1/2 z-50 mb-2 w-56 -translate-x-1/2 rounded-md bg-gray-700 px-3 py-2 text-left text-[12px] font-medium normal-case leading-snug tracking-normal text-white opacity-0 shadow-lg transition-opacity duration-150 group-hover:opacity-100 group-focus-within:opacity-100"
    >
      {{ t('usage.usage_coverage_tip') }}
    </span>
  </span>
</template>

<script setup lang="ts">
import { CircleHelp } from 'lucide-vue-next'
import { useI18n } from '@/composables/useI18n'

const { t } = useI18n()
</script>
