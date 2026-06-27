<template>
  <div v-if="!result" class="text-text-secondary text-sm">{{ t('quota.never_queried') }}</div>
  <div v-else-if="!result.success" class="text-sm">
    <div class="text-danger font-medium">{{ t('quota.query_failed') }}</div>
    <div v-if="result.error_code" class="text-xs text-text-secondary mt-1">{{ result.error_code }}</div>
    <div v-if="result.error_message" class="text-xs text-text-secondary mt-0.5">{{ result.error_message }}</div>
  </div>
  <div v-else class="space-y-3">
    <!-- Tiers -->
    <div v-for="tier in result.tiers" :key="tier.name" class="flex items-center gap-2 text-sm">
      <span class="font-medium text-text-secondary min-w-[50px]">{{ tierLabel(tier.name) }}:</span>
      <span :class="utilizationColor(tier.utilization)" class="font-bold">{{ Math.round(tier.utilization) }}%</span>
      <span v-if="tier.resets_at" class="text-xs text-text-secondary" :title="new Date(tier.resets_at).toLocaleString()">
        ◷{{ formatCountdown(tier.resets_at) }}
      </span>
      <span v-if="tier.used != null && tier.total != null" class="text-xs text-text-secondary">
        ({{ formatNum(tier.used) }}/{{ formatNum(tier.total) }}{{ tier.unit ? ' ' + tier.unit : '' }})
      </span>
    </div>

    <!-- Balances -->
    <div v-for="(bal, i) in result.balances" :key="i" class="flex items-center gap-2 text-sm">
      <span class="font-medium text-text-secondary">{{ bal.plan_name || t('quota.balance') }}:</span>
      <span class="font-bold">{{ formatBalance(bal) }}</span>
      <span v-if="bal.is_valid === false" class="text-xs text-danger" :title="bal.invalid_message">⚠</span>
      <span v-if="bal.extra" class="text-xs text-text-secondary">{{ bal.extra }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from '@/composables/useI18n'
import type { ProviderQuotaResult, QuotaTier, BalanceItem } from '@/composables/useApi'

defineProps<{ result: ProviderQuotaResult | null | undefined }>()

const { t } = useI18n()

function tierLabel(name: string): string {
  switch (name) {
    case 'five_hour': return t('quota.five_hour')
    case 'seven_day': return t('quota.seven_day')
    case 'monthly': return t('quota.monthly')
    default: return name
  }
}

function utilizationColor(pct: number): string {
  if (pct >= 90) return 'text-danger'
  if (pct >= 70) return 'text-warning-dark'
  return 'text-secondary'
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

function formatNum(v: number): string {
  if (v >= 1000000) return (v / 1000000).toFixed(1) + 'M'
  if (v >= 1000) return (v / 1000).toFixed(1) + 'K'
  return v.toFixed(v % 1 === 0 ? 0 : 2)
}

function formatBalance(bal: BalanceItem): string {
  if (bal.remaining != null) {
    const prefix = bal.unit === 'USD' || bal.unit === 'CNY' ? '$' : ''
    const val = bal.remaining.toFixed(2)
    return `${prefix}${val} ${bal.unit || ''}`.trim()
  }
  if (bal.total != null) {
    return `${bal.total.toFixed(2)} ${bal.unit || ''}`.trim()
  }
  return '-'
}
</script>
