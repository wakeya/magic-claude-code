<template>
  <div class="app-panel rounded-lg p-5">
    <!-- 事件是 MCC 全局观测数据：不关联 Claude Code 会话、不写入 JSONL / 导出内容。 -->
    <div class="flex flex-wrap items-center justify-between gap-3 mb-4">
      <div>
        <div class="flex items-center gap-2 text-[15px] font-bold">
          <span class="inline-block h-2.5 w-2.5 rounded-full bg-primary"></span>
          {{ t('failover.title') }}
          <span class="rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-semibold text-primary">
            {{ t('failover.global_tag') }}
          </span>
        </div>
        <p class="mt-1 text-xs text-text-secondary">{{ t('failover.disclaimer') }}</p>
      </div>
      <button
        class="flex items-center gap-1.5 rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-white transition-all duration-200 hover:bg-primary-hover disabled:cursor-not-allowed disabled:opacity-50"
        :disabled="loading"
        @click="loadEvents"
      >
        <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" />
        {{ loading ? t('failover.refreshing') : t('failover.refresh') }}
      </button>
    </div>

    <div v-if="errorMessage" class="mb-4 rounded-lg border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-700">
      {{ errorMessage }}
    </div>

    <div v-if="!loading && events.length === 0 && !errorMessage" class="rounded-lg border border-dashed py-10 text-center text-sm text-text-secondary">
      {{ t('failover.empty') }}
    </div>

    <div v-if="events.length > 0" class="overflow-x-auto">
      <table class="w-full border-collapse text-sm">
        <thead>
          <tr class="border-b text-left text-xs uppercase tracking-wide text-text-secondary">
            <th class="py-2 pr-4 font-semibold">{{ t('failover.col_time') }}</th>
            <th class="py-2 pr-4 font-semibold">{{ t('failover.col_route') }}</th>
            <th class="py-2 pr-4 font-semibold">{{ t('failover.col_model') }}</th>
            <th class="py-2 pr-4 font-semibold">{{ t('failover.col_signal') }}</th>
            <th class="py-2 pr-4 font-semibold">{{ t('failover.col_reason') }}</th>
            <th class="py-2 pr-4 font-semibold">{{ t('failover.col_outcome') }}</th>
            <th class="py-2 pr-4 font-semibold">{{ t('failover.col_disabled_until') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="event in events" :key="event.id" class="border-b last:border-0 align-top">
            <td class="py-2 pr-4 whitespace-nowrap tabular-nums text-text-secondary">{{ formatTime(event.occurred_at) }}</td>
            <td class="py-2 pr-4">
              <span class="font-medium">{{ routeLabel(event) }}</span>
            </td>
            <td class="py-2 pr-4 text-text-secondary">{{ modelLabel(event) }}</td>
            <td class="py-2 pr-4 text-text-secondary">{{ signalLabel(event) }}</td>
            <td class="py-2 pr-4 text-text-secondary">{{ reasonLabel(event) }}</td>
            <td class="py-2 pr-4">
              <span :class="outcomeClass(event.outcome)" class="rounded-full px-2 py-0.5 text-[11px] font-semibold whitespace-nowrap">
                {{ outcomeLabel(event.outcome) }}
              </span>
            </td>
            <td class="py-2 pr-4 whitespace-nowrap text-text-secondary">{{ disabledUntilLabel(event) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { useApi, type FailoverEvent } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'

const api = useApi()
const { t, locale } = useI18n()

const events = ref<FailoverEvent[]>([])
const loading = ref(false)
const errorMessage = ref('')

async function loadEvents() {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await api.getFailoverEvents(100)
    events.value = res.events || []
  } catch (err) {
    errorMessage.value = t('failover.load_failed')
    events.value = []
    // 不把原始错误对象塞进 DOM，避免意外泄露上游细节。
    void err
  } finally {
    loading.value = false
  }
}

onMounted(loadEvents)

function formatTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(locale.value === 'en' ? 'en-US' : 'zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

// routeLabel：源 → 目标；恢复事件只显示源。名字缺失时回落到 ID（ID 也可能为空）。
function routeLabel(event: FailoverEvent): string {
  const from = event.from_provider_name || event.from_provider_id || '—'
  if (event.outcome === 'recovered' || !event.to_provider_name && !event.to_provider_id) {
    return from
  }
  const to = event.to_provider_name || event.to_provider_id || '—'
  return `${from} → ${to}`
}

function modelLabel(event: FailoverEvent): string {
  if (!event.original_model && !event.mapped_model) return '—'
  if (event.original_model && event.mapped_model && event.original_model !== event.mapped_model) {
    return `${event.original_model} → ${event.mapped_model}`
  }
  return event.mapped_model || event.original_model || '—'
}

function signalLabel(event: FailoverEvent): string {
  if (event.upstream_code && event.reason) return `${event.upstream_code} / ${event.reason}`
  if (event.upstream_code) return String(event.upstream_code)
  return event.reason || '—'
}

function reasonLabel(event: FailoverEvent): string {
  return event.reason || '—'
}

function outcomeLabel(outcome: FailoverEvent['outcome']): string {
  switch (outcome) {
    case 'switched': return t('failover.outcome_switched')
    case 'exhausted': return t('failover.outcome_exhausted')
    case 'retry_failed': return t('failover.outcome_retry_failed')
    case 'recovered': return t('failover.outcome_recovered')
    default: return outcome
  }
}

function outcomeClass(outcome: FailoverEvent['outcome']): string {
  switch (outcome) {
    case 'switched': return 'bg-primary/10 text-primary'
    case 'recovered': return 'bg-green-100 text-green-700'
    case 'exhausted': return 'bg-amber-100 text-amber-700'
    case 'retry_failed': return 'bg-red-100 text-red-700'
    default: return 'bg-gray-100 text-gray-700'
  }
}

function disabledUntilLabel(event: FailoverEvent): string {
  if (!event.disabled_until) {
    // 凭据失效等无时间恢复：仅当 outcome 为凭据相关失败时提示等待人工恢复。
    return event.outcome === 'retry_failed' ? t('failover.credential_pending') : t('failover.no_disabled_until')
  }
  return formatTime(event.disabled_until)
}
</script>
