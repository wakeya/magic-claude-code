<template>
  <span class="group relative inline-flex items-center">
    <CircleHelp class="h-3.5 w-3.5 cursor-help text-text-secondary" aria-label="Usage 覆盖率说明" />
    <span
      role="tooltip"
      class="pointer-events-none absolute left-1/2 top-full z-50 mt-2 w-80 max-w-[min(20rem,calc(100vw-2rem))] -translate-x-1/2 rounded-md border border-border bg-fg px-3 py-2 text-left text-xs font-medium normal-case leading-relaxed tracking-normal text-white opacity-0 shadow-lg transition-opacity duration-150 group-hover:opacity-100 group-focus-within:opacity-100"
    >
      {{ USAGE_COVERAGE_TOOLTIP }}
    </span>
  </span>
</template>

<script setup lang="ts">
import { CircleHelp } from 'lucide-vue-next'
import { USAGE_COVERAGE_TOOLTIP } from '@/utils/formatters'
</script>
