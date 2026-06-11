<template>
  <span class="inline-flex items-center">
    <CircleHelp class="h-3.5 w-3.5 cursor-help text-text-secondary" :aria-label="t('usage.stats_scope')" />
    <span
      role="tooltip"
      class="pointer-events-none absolute bottom-full left-1/2 z-50 mb-2 w-96 -translate-x-1/2 whitespace-normal rounded-md bg-gray-700 px-4 py-3 text-left text-[12px] font-medium normal-case leading-snug tracking-normal text-white opacity-0 shadow-lg transition-opacity duration-150 group-hover:opacity-100 group-focus-within:opacity-100"
    >
      <span class="flex flex-col gap-2">
        <span v-for="item in statsScopeTips" :key="item.labelKey" class="block">
          <span class="font-bold">{{ t(item.labelKey) }}：</span>
          <span>{{ t(item.descriptionKey) }}</span>
        </span>
      </span>
    </span>
  </span>
</template>

<script setup lang="ts">
import { CircleHelp } from 'lucide-vue-next'
import { useI18n } from '@/composables/useI18n'

const { t } = useI18n()

const statsScopeTips = [
  { labelKey: 'usage.stats_scope_effective', descriptionKey: 'usage.stats_scope_tip_effective' },
  { labelKey: 'usage.stats_scope_provider', descriptionKey: 'usage.stats_scope_tip_provider' },
  { labelKey: 'usage.stats_scope_session_log', descriptionKey: 'usage.stats_scope_tip_session_log' },
  { labelKey: 'usage.stats_scope_raw', descriptionKey: 'usage.stats_scope_tip_raw' },
]
</script>
