<template>
  <div :class="['app-panel p-6 rounded-lg mb-3 transition-all duration-200 cursor-default', provider.active ? 'border-secondary bg-secondary-light' : 'hover:border-primary', !provider.enabled ? 'opacity-50' : '']">
    <div class="flex items-center gap-3 mb-3.5">
      <input type="checkbox" :checked="selected" class="w-4 h-4 cursor-pointer flex-shrink-0 accent-primary" @change="$emit('toggle-select', provider.id)" />
      <span :class="['w-2.5 h-2.5 rounded-full flex-shrink-0', provider.enabled ? 'bg-secondary' : 'bg-gray-300']" />
      <span class="text-base font-bold flex-1 min-w-0 truncate">{{ provider.name }}</span>

      <!-- 自动切换优先级 badge（列表 index + 1）；disabled provider 仍显示，因为编号表示列表位置。 -->
      <span
        v-if="orderIndex != null"
        class="inline-flex h-6 w-6 items-center justify-center rounded-full bg-primary text-white text-xs font-bold flex-shrink-0"
        :aria-label="t('providers.priority_label', { n: orderIndex + 1 })"
        :title="t('providers.priority_label', { n: orderIndex + 1 })"
      >{{ orderIndex + 1 }}</span>

      <!-- Quota display (title row, near Active badge) -->
      <template v-if="quotaDisplay">
        <span v-if="quotaDisplay.fiveHour != null" class="text-xs whitespace-nowrap" :class="utilizationColor(quotaDisplay.fiveHour)">
          {{ t('quota.five_hour') }}: {{ Math.round(quotaDisplay.fiveHour) }}%
          <span v-if="quotaDisplay.fiveHourReset" class="text-text-secondary ml-0.5">◷{{ formatCountdown(quotaDisplay.fiveHourReset) }}</span>
        </span>
        <span v-if="quotaDisplay.sevenDay != null" class="text-xs whitespace-nowrap" :class="utilizationColor(quotaDisplay.sevenDay)">
          {{ t('quota.seven_day') }}: {{ Math.round(quotaDisplay.sevenDay) }}%
          <span v-if="quotaDisplay.sevenDayReset" class="text-text-secondary ml-0.5">◷{{ formatCountdown(quotaDisplay.sevenDayReset) }}</span>
        </span>
        <span v-if="quotaDisplay.balance != null" class="text-xs font-medium whitespace-nowrap">
          {{ t('quota.balance') }}: {{ quotaDisplay.balanceUnit === 'USD' ? '$' : '' }}{{ quotaDisplay.balance.toFixed(2) }}
        </span>
        <!-- Refresh button for this card -->
        <button class="p-1 rounded hover:bg-muted transition-colors" :disabled="refreshing" @click.stop="$emit('refresh-quota')" :aria-label="t('quota.refresh_now')" :title="t('quota.refresh_now')">
          <svg :class="['w-3.5 h-3.5 text-text-secondary', refreshing && 'animate-spin']" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
        </button>
        <!-- Stale warning -->
        <span v-if="quotaStale" class="text-warning-dark" :title="t('quota.data_stale')">⚠</span>
      </template>
      <span v-else-if="quotaConfigured && !quotaSnapshot" class="text-xs text-text-secondary whitespace-nowrap">
        {{ t('quota.never_queried') }}
        <button class="p-1 rounded hover:bg-muted transition-colors inline-flex" :disabled="refreshing" @click.stop="$emit('refresh-quota')" :aria-label="t('quota.refresh_now')">
          <svg :class="['w-3.5 h-3.5', refreshing && 'animate-spin']" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
        </button>
      </span>

      <span v-if="provider.active" class="bg-secondary text-white px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider">{{ t('providers.active') }}</span>
    </div>

    <div class="app-muted text-[13px] mb-4">
      <div>API: <code class="app-control px-1.5 py-0.5 rounded text-xs">{{ provider.api_url }}</code></div>
      <div>Token: <code class="app-control px-1.5 py-0.5 rounded text-xs">{{ provider.api_token_mask || t('providers.not_set') }}</code></div>
      <div v-if="Object.keys(provider.model_mappings).length" class="flex flex-wrap gap-1.5 mt-2">
        <span v-for="(to, from) in provider.model_mappings" :key="from" class="bg-primary-light text-primary px-2.5 py-0.5 rounded-full text-xs font-semibold">{{ from }} &rarr; {{ to }}</span>
      </div>
      <div v-if="provider.multimodal_switch && provider.multimodal_model" class="flex flex-wrap gap-1.5 mt-2">
        <span class="bg-secondary-light text-secondary px-2.5 py-0.5 rounded-full text-xs font-semibold">{{ t('modal.multimodal_switch') }} &rarr; {{ provider.multimodal_model }}</span>
      </div>
    </div>

    <div class="flex gap-2 flex-wrap">
      <!-- 拖拽手柄：纯视觉 affordance（实际 draggable 由父级列表容器承担）。键盘/移动端用下方上移/下移按钮。 -->
      <span class="inline-flex items-center px-1.5 text-text-secondary cursor-grab" :aria-label="t('providers.drag_handle')" :title="t('providers.drag_handle')">
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5h.01M9 12h.01M9 19h.01M15 5h.01M15 12h.01M15 19h.01"/></svg>
      </span>
      <button class="flex items-center gap-1 px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:scale-100" :disabled="!canMoveUp" @click="$emit('move-up')" :aria-label="t('providers.move_up')" :title="t('providers.move_up')">
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7"/></svg>
      </button>
      <button class="flex items-center gap-1 px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:scale-100" :disabled="!canMoveDown" @click="$emit('move-down')" :aria-label="t('providers.move_down')" :title="t('providers.move_down')">
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
      </button>
      <button class="flex items-center gap-1 px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted" @click="$emit('edit')">
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"/></svg>
        {{ t('providers.edit') }}
      </button>
      <button class="flex items-center gap-1 px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted" @click="$emit('duplicate', provider.id)">
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"/></svg>
        {{ t('providers.duplicate') }}
      </button>
      <button class="flex items-center gap-1 px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted" @click="$emit('usage')">
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"/></svg>
        {{ t('providers.usage') }}
      </button>
      <button class="flex items-center gap-1 px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted" @click="$emit('test')">
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg>
        {{ t('providers.test') }}
      </button>
      <button v-if="provider.enabled && !provider.active" class="px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-primary text-white" @click="$emit('activate')">{{ t('providers.set_active') }}</button>
      <button :class="['px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 text-white', provider.enabled ? 'bg-accent' : 'bg-secondary']" @click="$emit('toggle')">{{ provider.enabled ? t('providers.disable') : t('providers.enable') }}</button>
      <button class="px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-danger text-white" @click="$emit('delete')">{{ t('providers.delete') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { Provider, QuotaSnapshot } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'

const props = defineProps<{
  provider: Provider
  selected?: boolean
  quotaSnapshot?: QuotaSnapshot | null
  refreshing?: boolean
  // orderIndex 仅用于展示优先级 badge（= 列表 index），不写入 provider 业务字段。
  orderIndex?: number
  canMoveUp?: boolean
  canMoveDown?: boolean
}>()
defineEmits<{
  edit: []
  delete: [id: string]
  activate: [id: string]
  toggle: [id: string]
  test: [id: string]
  duplicate: [id: string]
  usage: []
  'toggle-select': [id: string]
  'refresh-quota': []
  'move-up': []
  'move-down': []
}>()

const { t } = useI18n()

const quotaConfigured = computed(() => props.provider.quota_query?.enabled)

const quotaStale = computed(() => props.quotaSnapshot?.is_stale)

const quotaDisplay = computed(() => {
  const snap = props.quotaSnapshot
  const result = snap?.last_success || snap?.result
  if (!result?.success || (!result.tiers?.length && !result.balances?.length)) return null

  const display: { fiveHour?: number; fiveHourReset?: string; sevenDay?: number; sevenDayReset?: string; balance?: number; balanceUnit?: string } = {}

  for (const tier of result.tiers || []) {
    if (tier.name === 'five_hour') {
      display.fiveHour = tier.utilization
      display.fiveHourReset = tier.resets_at
    } else if (tier.name === 'seven_day') {
      display.sevenDay = tier.utilization
      display.sevenDayReset = tier.resets_at
    }
  }

  if (result.balances?.length) {
    const bal = result.balances[0]
    if (bal.remaining != null) {
      display.balance = bal.remaining
      display.balanceUnit = bal.unit
    }
  }

  return display
})

function utilizationColor(pct: number): string {
  if (pct >= 90) return 'text-danger font-bold'
  if (pct >= 70) return 'text-warning-dark font-bold'
  return 'text-secondary font-bold'
}

function formatCountdown(isoStr: string): string {
  const reset = new Date(isoStr).getTime()
  const now = Date.now()
  const diff = reset - now
  if (diff <= 0) return t('quota.reset_pending')
  const hours = Math.floor(diff / 3600000)
  const mins = Math.floor((diff % 3600000) / 60000)
  const days = Math.floor(hours / 24)
  const remHours = hours % 24
  if (days > 0) return `${days}d${remHours}h`
  if (hours > 0) return `${hours}h${mins}m`
  return `${mins}m`
}
</script>
