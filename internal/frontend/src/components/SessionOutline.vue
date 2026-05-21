<template>
  <div class="space-y-2">
    <button
      v-for="item in userItems"
      :key="item.index"
      class="session-outline-item"
      @click="$emit('jump', item.index)"
    >
      <div class="line-clamp-2 font-medium">{{ item.preview }}</div>
      <div class="mt-1 text-xs session-muted">{{ formatMessageTime(item.ts) }}</div>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { SessionMessage } from '@/composables/useApi'

const props = defineProps<{
  messages: SessionMessage[]
}>()

defineEmits<{
  jump: [index: number]
}>()

const userItems = computed(() =>
  props.messages
    .map((message, index) => ({ message, index }))
    .filter(({ message }) => message.role === 'user')
    .map(({ message, index }) => ({
      index,
      ts: message.ts,
      preview: previewText(message.content),
    }))
)

function previewText(value: string): string {
  const compact = value.replace(/\s+/g, ' ').trim()
  if (compact.length <= 50) return compact
  return `${compact.slice(0, 50)}...`
}

function formatMessageTime(ts?: number): string {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString()
}
</script>
