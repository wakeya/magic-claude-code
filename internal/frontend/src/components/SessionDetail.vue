<template>
  <div class="space-y-3">
    <div
      v-for="(message, index) in detail.messages"
      :id="`session-message-${index}`"
      :key="`${message.role}-${index}`"
      :class="[
        'session-message',
        message.role === 'user' ? 'session-message-user' : '',
        message.role === 'assistant' ? 'session-message-assistant' : '',
        message.role === 'system' || message.role === 'tool' ? 'session-message-technical' : '',
      ]"
    >
      <details v-if="message.role === 'system' || message.role === 'tool'" class="group">
        <summary class="flex cursor-pointer list-none items-center justify-between gap-3 text-xs font-bold uppercase tracking-widest session-muted">
          <span>{{ message.role }}</span>
          <span class="font-mono normal-case tracking-normal">{{ formatMessageTime(message.ts) }}</span>
        </summary>
        <pre class="session-technical-pre">{{ message.content }}</pre>
      </details>
      <div v-else>
        <div
          :class="[
            'mb-2 flex items-center justify-between gap-3 text-xs font-bold uppercase tracking-widest',
            message.role === 'user' ? 'session-user-label' : 'session-muted',
          ]"
        >
          <span>{{ message.role }}</span>
          <span class="font-mono normal-case tracking-normal">{{ formatMessageTime(message.ts) }}</span>
        </div>
        <pre
          :class="[
            'whitespace-pre-wrap break-words font-sans text-[14px] leading-relaxed',
            message.role === 'user' ? 'session-user-text' : 'session-body-text',
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
