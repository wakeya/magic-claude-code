<template>
  <div class="space-y-3">
    <div
      v-for="(message, index) in detail.messages"
      :id="`session-message-${index}`"
      :key="`${message.role}-${index}`"
      :class="[
        'rounded-lg border-2 p-4',
        message.role === 'user' ? 'border-green-300 bg-green-100 text-green-950' : '',
        message.role === 'assistant' ? 'border-border bg-white' : '',
        message.role === 'system' || message.role === 'tool' ? 'border-accent/40 bg-accent-light/40' : '',
      ]"
    >
      <details v-if="message.role === 'system' || message.role === 'tool'" class="group">
        <summary class="flex cursor-pointer list-none items-center justify-between gap-3 text-xs font-bold uppercase tracking-widest text-text-secondary">
          <span>{{ message.role }}</span>
          <span class="font-mono normal-case tracking-normal">{{ formatMessageTime(message.ts) }}</span>
        </summary>
        <pre class="mt-3 whitespace-pre-wrap break-words rounded-md bg-fg p-4 font-mono text-[13px] leading-relaxed text-gray-100">{{ message.content }}</pre>
      </details>
      <div v-else>
        <div
          :class="[
            'mb-2 flex items-center justify-between gap-3 text-xs font-bold uppercase tracking-widest',
            message.role === 'user' ? 'text-green-800' : 'text-text-secondary',
          ]"
        >
          <span>{{ message.role }}</span>
          <span class="font-mono normal-case tracking-normal">{{ formatMessageTime(message.ts) }}</span>
        </div>
        <pre
          :class="[
            'whitespace-pre-wrap break-words font-sans text-[14px] leading-relaxed',
            message.role === 'user' ? 'text-green-950' : 'text-fg',
          ]"
        >{{ message.content }}</pre>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { SessionDetailResponse } from '@/composables/useApi'

const props = defineProps<{
  detail: SessionDetailResponse
}>()

function scrollToMessage(index: number) {
  document.getElementById(`session-message-${index}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function formatMessageTime(ts?: number): string {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString()
}

defineExpose({ scrollToMessage })
</script>
