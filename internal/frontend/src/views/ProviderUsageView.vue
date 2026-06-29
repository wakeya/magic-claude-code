<template>
  <div class="min-h-screen app-bg">
    <header class="border-b border-default app-bg sticky top-0 z-10">
      <div class="max-w-6xl mx-auto px-4 py-3 flex items-center gap-3">
        <button class="text-text-secondary hover:text-primary transition-colors" @click="goBack" :aria-label="t('quota.back')">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7"/></svg>
        </button>
        <div class="text-sm text-text-secondary">
          <span class="hover:text-primary cursor-pointer" @click="goBack">{{ t('providers.title') }}</span>
          <span class="mx-2">/</span>
          <span class="font-semibold text-text">{{ providerName }}</span>
        </div>
      </div>
    </header>

    <div v-if="loading" class="max-w-6xl mx-auto px-4 py-12 text-center text-text-secondary">
      {{ t('quota.refreshing') }}
    </div>

    <div v-else-if="notFound" class="max-w-6xl mx-auto px-4 py-12 text-center">
      <div class="text-lg font-semibold mb-2">{{ t('quota.not_found') }}</div>
      <div class="text-text-secondary mb-4">{{ t('quota.not_found_hint') }}</div>
      <button class="px-4 py-2 bg-primary text-white rounded-md text-sm" @click="goBack">{{ t('quota.back_to_providers') }}</button>
    </div>

    <div v-else class="max-w-6xl mx-auto px-4 py-6 grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- Left: Configuration -->
      <div class="app-panel rounded-lg p-6">
        <h2 class="text-lg font-bold mb-4">{{ t('quota.title') }}</h2>

        <div class="space-y-4">
          <!-- Enable toggle -->
          <label class="flex items-center gap-2 cursor-pointer">
            <input type="checkbox" v-model="form.enabled" class="w-4 h-4 accent-primary" />
            <span class="text-sm font-medium">{{ t('quota.enabled') }}</span>
          </label>

          <!-- Template type -->
          <div>
            <label class="block text-sm font-medium mb-1">{{ t('quota.template_type') }}</label>
            <select v-model="form.template_type" class="w-full app-control rounded-md px-3 py-2 text-sm">
              <option value="general">{{ t('quota.template.general') }}</option>
              <option value="custom">{{ t('quota.template.custom') }}</option>
              <option value="newapi">{{ t('quota.template.newapi') }}</option>
              <option value="token_plan">{{ t('quota.template.token_plan') }}</option>
              <option value="official_balance">{{ t('quota.template.official_balance') }}</option>
            </select>
          </div>

          <!-- Xiaomi MiMo warning (only under token_plan) -->
          <div v-if="showMiMoWarning" class="bg-warning-light border border-warning text-warning-dark rounded-md p-3 text-sm">
            {{ t('quota.xiaomi_mimo_unsupported') }}
          </div>

          <!-- Official balance: detected provider + fixed endpoint -->
          <div v-if="showOfficialBalanceInfo" class="bg-primary-light border border-primary text-primary-dark rounded-md p-3 text-sm">
            <div class="font-medium">{{ t('quota.detected_balance_provider') }}: {{ detectedBalance }}</div>
            <div class="text-xs text-text-secondary mt-1">{{ t('quota.official_balance_endpoint_hint') }}</div>
          </div>

          <!-- Token Plan: detected provider + manual selector -->
          <div v-if="form.template_type === 'token_plan' && !isMiMo">
            <label class="block text-sm font-medium mb-1">{{ t('quota.coding_plan_provider') }}</label>
            <select v-model="form.coding_plan_provider" class="w-full app-control rounded-md px-3 py-2 text-sm">
              <option value="">{{ t('quota.auto_detect') || '自动检测' }}</option>
              <option value="kimi">Kimi</option>
              <option value="zhipu_cn">智谱 (CN)</option>
              <option value="zhipu_en">Zhipu (EN)</option>
              <option value="minimax_cn">MiniMax (CN)</option>
              <option value="minimax_en">MiniMax (EN)</option>
              <option value="zenmux">ZenMux</option>
              <option value="volcengine">火山方舟</option>
            </select>
            <div v-if="detectedTokenPlan && !form.coding_plan_provider" class="text-xs text-text-secondary mt-1">
              {{ detectedTokenPlan }}
            </div>
          </div>

          <!-- Generic Base URL (for general, custom, newapi) -->
          <div v-if="showBaseURL">
            <label class="block text-sm font-medium mb-1">{{ t('quota.base_url') }}</label>
            <input v-model="form.base_url" type="text" class="w-full app-control rounded-md px-3 py-2 text-sm" :placeholder="t('quota.base_url_hint')" />
          </div>

          <!-- Script API Key (for general, custom) -->
          <div v-if="showAPIKey">
            <label class="block text-sm font-medium mb-1">{{ t('quota.script_api_key') }}</label>
            <div class="flex gap-2">
              <input v-model="form.script_api_key" type="password" class="flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.script_api_key_configured ? t('quota.script_api_key_configured') : ''" />
              <button v-if="savedConfig?.script_api_key_configured" class="text-xs text-danger hover:underline whitespace-nowrap" @click="form.clear_script_api_key = true">{{ t('quota.clear_script_key') }}</button>
            </div>
          </div>

          <!-- ZenMux atomic override pair; leave both empty for card fallback. -->
          <template v-if="showZenMuxFields">
            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.zenmux_base_url') }}</label>
              <input v-model="form.zenmux_base_url" type="text" class="w-full app-control rounded-md px-3 py-2 text-sm" :placeholder="t('quota.zenmux_base_url_hint')" />
            </div>
            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.zenmux_api_key') }}</label>
              <div class="flex gap-2">
                <input v-model="form.zenmux_api_key" type="password" class="flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.zenmux_api_key_configured ? t('quota.zenmux_api_key_configured') : ''" />
                <button v-if="savedConfig?.zenmux_api_key_configured" class="text-xs text-danger hover:underline whitespace-nowrap" @click="clearZenMuxOverride">{{ t('quota.clear_zenmux_override') }}</button>
              </div>
            </div>
          </template>

          <!-- Access Token (for newapi) -->
          <div v-if="showAccessToken">
            <label class="block text-sm font-medium mb-1">{{ t('quota.access_token') }}</label>
            <div class="flex gap-2">
              <input v-model="form.access_token" type="password" class="flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.access_token_configured ? t('quota.access_token_configured') : ''" />
              <button v-if="savedConfig?.access_token_configured" class="text-xs text-danger hover:underline whitespace-nowrap" @click="form.clear_access_token = true">{{ t('quota.clear_token') }}</button>
            </div>
          </div>

          <!-- User ID (for newapi) -->
          <div v-if="form.template_type === 'newapi'">
            <label class="block text-sm font-medium mb-1">{{ t('quota.user_id') }}</label>
            <input v-model="form.user_id" type="text" class="w-full app-control rounded-md px-3 py-2 text-sm" />
          </div>

          <!-- Volcengine AK/SK (shown when detected or saved as volcengine) -->
          <template v-if="form.template_type === 'token_plan' && isVolcengine">
            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.access_key_id') }}</label>
              <input v-model="form.access_key_id" type="text" class="w-full app-control rounded-md px-3 py-2 text-sm" />
            </div>
            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.secret_access_key') }}</label>
              <div class="flex gap-2">
                <input v-model="form.secret_access_key" type="password" class="flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.secret_access_key_configured ? t('quota.secret_access_key_configured') : ''" />
                <button v-if="savedConfig?.secret_access_key_configured" class="text-xs text-danger hover:underline whitespace-nowrap" @click="form.clear_secret_access_key = true">{{ t('quota.clear_sk') }}</button>
              </div>
            </div>
          </template>

          <!-- Timeout -->
          <div>
            <label class="block text-sm font-medium mb-1">{{ t('quota.timeout') }}</label>
            <input v-model.number="form.timeout_seconds" type="number" min="2" max="30" class="w-32 app-control rounded-md px-3 py-2 text-sm" />
          </div>

          <!-- Interval -->
          <div>
            <label class="block text-sm font-medium mb-1">{{ t('quota.interval') }}</label>
            <input v-model.number="form.auto_query_interval_minutes" type="number" min="0" max="1440" class="w-32 app-control rounded-md px-3 py-2 text-sm" />
            <div class="text-xs text-text-secondary mt-1">{{ t('quota.interval_hint') }}</div>
          </div>

          <!-- Script editor (for general, custom) -->
          <div v-if="showScript">
            <label class="block text-sm font-medium mb-1">{{ t('quota.script') }}</label>
            <textarea v-model="form.script" rows="12" class="w-full app-control rounded-md px-3 py-2 text-sm font-mono" spellcheck="false"></textarea>
            <div v-if="form.template_type === 'custom'" class="text-xs text-text-secondary mt-1">
              {{ t('quota.return_fields_help') }}
            </div>
          </div>

          <!-- Action buttons -->
          <div class="flex gap-2 pt-2">
            <button class="px-4 py-2 bg-primary text-white rounded-md text-sm font-medium hover:opacity-90 transition-opacity" @click="saveConfig" :disabled="saving">
              {{ t('quota.save') }}
            </button>
            <button class="px-4 py-2 border border-default rounded-md text-sm font-medium hover:bg-muted transition-colors" @click="testQuery" :disabled="testing">
              {{ testing ? t('quota.refreshing') : t('quota.test') }}
            </button>
          </div>

          <!-- Save/test status -->
          <div v-if="saveMsg" :class="['text-sm mt-2', saveOk ? 'text-secondary' : 'text-danger']">{{ saveMsg }}</div>
        </div>
      </div>

      <!-- Right: Latest result -->
      <div class="app-panel rounded-lg p-6">
        <div class="flex items-center justify-between mb-4">
          <h2 class="text-lg font-bold">{{ t('quota.last_result') }}</h2>
          <button class="flex items-center gap-1 px-3 py-1.5 border border-default rounded-md text-sm hover:bg-muted transition-colors" @click="refreshNow" :disabled="refreshing">
            <svg :class="['w-4 h-4', refreshing && 'animate-spin']" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
            {{ refreshing ? t('quota.refreshing') : t('quota.refresh_now') }}
          </button>
        </div>

        <!-- Test result (if shown) -->
        <div v-if="testResult" class="mb-4 border border-warning rounded-md p-3">
          <div class="text-xs font-semibold text-warning-dark mb-2">{{ t('quota.test_result') }}</div>
          <QuotaResultDisplay :result="testResult" />
        </div>

        <!-- Snapshot result -->
        <div v-if="snapshot">
          <div v-if="snapshot.is_stale" class="flex items-center gap-1 text-xs text-warning-dark mb-2">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4.5c-.77-.833-2.694-.833-3.464 0L3.34 16.5c-.77.833.192 2.5 1.732 2.5z"/></svg>
            {{ t('quota.data_stale') }}
          </div>
          <QuotaResultDisplay :result="snapshot.result" />
          <div v-if="snapshot.queried_at" class="text-xs text-text-secondary mt-3">
            {{ t('quota.queried_at') }}: {{ formatRelativeTime(snapshot.queried_at) }}
            <span v-if="snapshot.result?.duration_ms"> · {{ t('quota.duration') }}: {{ snapshot.result.duration_ms }}ms</span>
          </div>
        </div>

        <div v-else class="text-center text-text-secondary py-8">
          {{ t('quota.never_queried') }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useApi } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import type { PublicQuotaConfig, QuotaSnapshot, ProviderQuotaResult } from '@/composables/useApi'
import QuotaResultDisplay from '@/components/QuotaResultDisplay.vue'
import {
  effectiveTokenPlanProvider as resolveEffectiveProvider,
  showBaseURLField,
  showAPIKeyField,
  showZenMuxFields as shouldShowZenMuxFields,
  showVolcengineFields,
  shouldShowMiMoWarning,
  shouldShowOfficialBalanceInfo,
  buildSavePayload,
  buildTestPayload,
} from '@/utils/quotaForm'

const route = useRoute()
const router = useRouter()
const api = useApi()
const { t } = useI18n()

const providerId = computed(() => route.params.providerId as string)
const providerName = ref('')
const loading = ref(true)
const notFound = ref(false)
const saving = ref(false)
const testing = ref(false)
const refreshing = ref(false)
const saveMsg = ref('')
const saveOk = ref(false)
const savedConfig = ref<PublicQuotaConfig | null>(null)
const snapshot = ref<QuotaSnapshot | null>(null)
const testResult = ref<ProviderQuotaResult | null>(null)
const detectedTokenPlan = ref('')
const detectedBalance = ref('')
const isMiMoDetected = ref(false)

const form = reactive({
  enabled: false,
  template_type: 'general',
  coding_plan_provider: '',
  timeout_seconds: 10,
  auto_query_interval_minutes: 5,
  script: '',
  base_url: '',
  script_api_key: '',
  zenmux_base_url: '',
  zenmux_api_key: '',
  access_token: '',
  user_id: '',
  access_key_id: '',
  secret_access_key: '',
  clear_script_api_key: false,
  clear_zenmux_api_key: false,
  clear_access_token: false,
  clear_secret_access_key: false,
})

const showBaseURL = computed(() =>
  showBaseURLField(form.template_type, effectiveTokenPlanProvider.value)
)
const showAPIKey = computed(() =>
  showAPIKeyField(form.template_type, effectiveTokenPlanProvider.value)
)
const showAccessToken = computed(() => form.template_type === 'newapi')
const showScript = computed(() => ['general', 'custom'].includes(form.template_type))

// Effective token-plan provider: saved explicit value wins, else auto-detected.
const effectiveTokenPlanProvider = computed(() =>
  resolveEffectiveProvider(form.coding_plan_provider, detectedTokenPlan.value)
)
const isVolcengine = computed(() => showVolcengineFields(form.template_type, effectiveTokenPlanProvider.value))
const isZenMux = computed(() => shouldShowZenMuxFields(form.template_type, effectiveTokenPlanProvider.value))
const isMiMo = computed(() => isMiMoDetected.value)
const showMiMoWarning = computed(() => shouldShowMiMoWarning(form.template_type, isMiMoDetected.value))
const showOfficialBalanceInfo = computed(() =>
  shouldShowOfficialBalanceInfo(form.template_type, detectedBalance.value)
)
// Alias kept for template clarity; isVolcengine/isZenMux already gate the fields.
const showZenMuxFields = isZenMux

function goBack() {
  const tab = new URLSearchParams(window.location.search).get('tab')
  router.push(tab ? `/?tab=${tab}` : '/?tab=providers')
}

function clearZenMuxOverride() {
  form.zenmux_base_url = ''
  form.zenmux_api_key = ''
  form.clear_zenmux_api_key = true
}

function formatRelativeTime(isoStr: string): string {
  const d = new Date(isoStr)
  const now = Date.now()
  const diffMs = now - d.getTime()
  if (diffMs < 60000) return t('quota.just_now')
  const mins = Math.floor(diffMs / 60000)
  if (mins < 60) return t('quota.minutes_ago', { n: mins })
  const hours = Math.floor(mins / 60)
  if (hours < 24) return t('quota.hours_ago', { n: hours })
  const days = Math.floor(hours / 24)
  return t('quota.days_ago', { n: days })
}

async function loadConfig() {
  loading.value = true
  try {
    // Get provider name from providers list.
    const providersRes = await api.getProviders()
    const provider = providersRes.providers.find(p => p.id === providerId.value)
    if (!provider) {
      notFound.value = true
      return
    }
    providerName.value = provider.name

    // Get quota config and snapshot.
    const data = await api.getProviderUsage(providerId.value)
    savedConfig.value = data.config
    snapshot.value = data.snapshot || null
    detectedTokenPlan.value = data.detected_token_plan || ''
    detectedBalance.value = data.detected_balance || ''
    isMiMoDetected.value = !!data.is_mimo

    // Populate form from saved config.
    form.enabled = data.config.enabled
    form.template_type = data.config.template_type || 'general'
    form.coding_plan_provider = data.config.coding_plan_provider || ''
    form.timeout_seconds = data.config.timeout_seconds || 10
    form.auto_query_interval_minutes = data.config.auto_query_interval_minutes ?? 5
    form.script = data.config.script || ''
    form.base_url = data.config.base_url || ''
    form.zenmux_base_url = data.config.zenmux_base_url || ''
    form.user_id = data.config.user_id || ''
    form.access_key_id = data.config.access_key_id || ''
    form.clear_script_api_key = false
    form.clear_zenmux_api_key = false
    form.clear_access_token = false
    form.clear_secret_access_key = false
  } catch (e: any) {
    if (e.message?.includes('not found')) {
      notFound.value = true
    }
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  saving.value = true
  saveMsg.value = ''
  try {
    const data = buildSavePayload(form, detectedTokenPlan.value, savedConfig.value)

    const res = await api.updateProviderUsage(providerId.value, data)
    savedConfig.value = res.config
    saveOk.value = true
    saveMsg.value = t('quota.query_success')
    form.script_api_key = ''
    form.zenmux_api_key = ''
    form.access_token = ''
    form.secret_access_key = ''
    form.clear_script_api_key = false
    form.clear_zenmux_api_key = false
    form.clear_access_token = false
    form.clear_secret_access_key = false
  } catch (e: any) {
    saveOk.value = false
    saveMsg.value = e.message || 'Save failed'
  } finally {
    saving.value = false
  }
}

async function testQuery() {
  testing.value = true
  testResult.value = null
  try {
    const data = buildTestPayload(form, detectedTokenPlan.value)

    const res = await api.testProviderUsage(providerId.value, data)
    testResult.value = res.result
  } catch (e: any) {
    testResult.value = {
      provider_id: providerId.value,
      template_type: form.template_type,
      success: false,
      error_code: 'network_error',
      error_message: e.message,
      queried_at: new Date().toISOString(),
      duration_ms: 0,
    }
  } finally {
    testing.value = false
  }
}

async function refreshNow() {
  refreshing.value = true
  try {
    const res = await api.queryProviderUsage(providerId.value)
    // Reload the snapshot.
    const data = await api.getProviderUsage(providerId.value)
    snapshot.value = data.snapshot || null
    testResult.value = null
  } catch (e: any) {
    // Silently fail; the snapshot will show the error.
  } finally {
    refreshing.value = false
  }
}

onMounted(loadConfig)
</script>
