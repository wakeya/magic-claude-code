<template>
  <div class="fixed inset-0 bg-black/50 z-50 flex justify-center items-center px-4" @click.self="requestClose">
    <div
      ref="dialogEl"
      class="app-panel rounded-lg w-[90vw] max-w-[1180px] max-h-[90vh] overflow-y-auto"
      role="dialog"
      aria-modal="true"
      aria-labelledby="provider-usage-title"
      tabindex="-1"
    >
      <header class="sticky top-0 z-10 app-panel border-b border-default px-6 py-4 flex items-start justify-between gap-4">
        <div>
          <h2 id="provider-usage-title" class="text-lg font-bold m-0">{{ t('quota.title') }} · {{ providerName }}</h2>
          <p class="text-sm text-text-secondary mt-1">{{ t('quota.modal_subtitle') }}</p>
        </div>
        <button
          type="button"
          class="bg-transparent border-none text-2xl cursor-pointer app-muted hover:text-fg disabled:opacity-50"
          :aria-label="t('modal.cancel')"
          :disabled="saving"
          @click="requestClose"
        >&times;</button>
      </header>

      <div v-if="loading" class="px-6 py-12 text-center text-text-secondary">
        {{ t('quota.refreshing') }}
      </div>

      <div v-else-if="notFound" class="px-6 py-12 text-center">
        <div class="text-lg font-semibold mb-2">{{ t('quota.not_found') }}</div>
        <div class="text-text-secondary">{{ t('quota.not_found_hint') }}</div>
      </div>

      <div v-else-if="loadError" class="px-6 py-12 text-center text-danger" role="alert">
        {{ loadError }}
      </div>

      <main v-else class="grid grid-cols-1 lg:grid-cols-2 gap-6 p-6">
        <section class="min-w-0">
          <h3 class="text-lg font-bold mb-4">{{ t('quota.title') }}</h3>

          <div class="space-y-4">
            <label class="flex items-center gap-2 cursor-pointer">
              <input v-model="form.enabled" type="checkbox" class="w-4 h-4 accent-primary" />
              <span class="text-sm font-medium">{{ t('quota.enabled') }}</span>
            </label>

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

            <div v-if="showMiMoWarning" class="bg-warning-light border border-warning text-warning-dark rounded-md p-3 text-sm">
              {{ t('quota.xiaomi_mimo_unsupported') }}
            </div>

            <div v-if="showOfficialBalanceInfo" class="bg-primary-light border border-primary text-primary-dark rounded-md p-3 text-sm">
              <div class="font-medium">{{ t('quota.detected_balance_provider') }}: {{ detectedBalance }}</div>
              <div class="text-xs text-text-secondary mt-1">{{ t('quota.official_balance_endpoint_hint') }}</div>
            </div>

            <div v-if="form.template_type === 'token_plan' && !isMiMo">
              <label class="block text-sm font-medium mb-1">{{ t('quota.coding_plan_provider') }}</label>
              <select v-model="form.coding_plan_provider" class="w-full app-control rounded-md px-3 py-2 text-sm">
                <option value="">{{ t('quota.auto_detect') }}</option>
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

            <div v-if="showBaseURL">
              <label class="block text-sm font-medium mb-1">{{ t('quota.base_url') }}</label>
              <input v-model="form.base_url" type="text" class="w-full app-control rounded-md px-3 py-2 text-sm" :placeholder="t('quota.base_url_hint')" />
            </div>

            <div v-if="showAPIKey">
              <label class="block text-sm font-medium mb-1">{{ t('quota.script_api_key') }}</label>
              <div class="flex flex-wrap sm:flex-nowrap gap-2 min-w-0">
                <input v-model="form.script_api_key" type="password" class="min-w-0 flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.script_api_key_configured ? t('quota.script_api_key_configured') : ''" />
                <button v-if="savedConfig?.script_api_key_configured" type="button" class="text-xs text-danger hover:underline whitespace-nowrap" @click="form.clear_script_api_key = true">{{ t('quota.clear_script_key') }}</button>
              </div>
            </div>

            <template v-if="showZenMuxFields">
              <div>
                <label class="block text-sm font-medium mb-1">{{ t('quota.zenmux_base_url') }}</label>
                <input v-model="form.zenmux_base_url" type="text" class="w-full app-control rounded-md px-3 py-2 text-sm" :placeholder="t('quota.zenmux_base_url_hint')" />
              </div>
              <div>
                <label class="block text-sm font-medium mb-1">{{ t('quota.zenmux_api_key') }}</label>
                <div class="flex flex-wrap sm:flex-nowrap gap-2 min-w-0">
                  <input v-model="form.zenmux_api_key" type="password" class="min-w-0 flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.zenmux_api_key_configured ? t('quota.zenmux_api_key_configured') : ''" />
                  <button v-if="savedConfig?.zenmux_api_key_configured" type="button" class="text-xs text-danger hover:underline whitespace-nowrap" @click="clearZenMuxOverride">{{ t('quota.clear_zenmux_override') }}</button>
                </div>
              </div>
            </template>

            <div v-if="showAccessToken">
              <label class="block text-sm font-medium mb-1">{{ t('quota.access_token') }}</label>
              <div class="flex flex-wrap sm:flex-nowrap gap-2 min-w-0">
                <input v-model="form.access_token" type="password" class="min-w-0 flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.access_token_configured ? t('quota.access_token_configured') : ''" />
                <button v-if="savedConfig?.access_token_configured" type="button" class="text-xs text-danger hover:underline whitespace-nowrap" @click="form.clear_access_token = true">{{ t('quota.clear_token') }}</button>
              </div>
            </div>

            <div v-if="form.template_type === 'newapi'">
              <label class="block text-sm font-medium mb-1">{{ t('quota.user_id') }}</label>
              <input v-model="form.user_id" type="text" class="w-full app-control rounded-md px-3 py-2 text-sm" />
            </div>

            <template v-if="form.template_type === 'token_plan' && isVolcengine">
              <div>
                <label class="block text-sm font-medium mb-1">{{ t('quota.access_key_id') }}</label>
                <input v-model="form.access_key_id" type="text" class="w-full app-control rounded-md px-3 py-2 text-sm" />
              </div>
              <div>
                <label class="block text-sm font-medium mb-1">{{ t('quota.secret_access_key') }}</label>
                <div class="flex flex-wrap sm:flex-nowrap gap-2 min-w-0">
                  <input v-model="form.secret_access_key" type="password" class="min-w-0 flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.secret_access_key_configured ? t('quota.secret_access_key_configured') : ''" />
                  <button v-if="savedConfig?.secret_access_key_configured" type="button" class="text-xs text-danger hover:underline whitespace-nowrap" @click="form.clear_secret_access_key = true">{{ t('quota.clear_sk') }}</button>
                </div>
              </div>
            </template>

            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.timeout') }}</label>
              <input v-model.number="form.timeout_seconds" type="number" min="2" max="30" class="w-32 app-control rounded-md px-3 py-2 text-sm" />
            </div>

            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.interval') }}</label>
              <input v-model.number="form.auto_query_interval_minutes" type="number" min="0" max="1440" class="w-32 app-control rounded-md px-3 py-2 text-sm" />
              <div class="text-xs text-text-secondary mt-1">{{ t('quota.interval_hint') }}</div>
            </div>

            <div v-if="showScript">
              <label class="block text-sm font-medium mb-1">{{ t('quota.script') }}</label>
              <textarea v-model="form.script" rows="12" class="w-full app-control rounded-md px-3 py-2 text-sm font-mono" spellcheck="false"></textarea>
              <div v-if="form.template_type === 'custom'" class="text-xs text-text-secondary mt-1">
                {{ t('quota.return_fields_help') }}
              </div>
            </div>
          </div>
        </section>

        <section class="min-w-0">
          <div class="flex items-center justify-between gap-3 mb-4">
            <h3 class="text-lg font-bold">{{ t('quota.last_result') }}</h3>
            <button
              type="button"
              class="flex items-center gap-1 px-3 py-1.5 border border-default rounded-md text-sm hover:bg-muted transition-colors disabled:opacity-50"
              :disabled="refreshing || saving"
              @click="refreshNow"
            >
              <svg :class="['w-4 h-4', refreshing && 'animate-spin']" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
              {{ refreshing ? t('quota.refreshing') : t('quota.refresh_now') }}
            </button>
          </div>

          <div v-if="refreshError" class="mb-4 text-sm text-danger" role="alert">{{ refreshError }}</div>

          <div v-if="testResult" class="mb-4 border border-warning rounded-md p-3">
            <div class="text-xs font-semibold text-warning-dark mb-2">{{ t('quota.test_result') }}</div>
            <QuotaResultDisplay :result="testResult" />
          </div>

          <div v-if="snapshot">
            <div v-if="snapshot.is_stale" class="flex items-center gap-1 text-xs text-warning-dark mb-2">
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4.5c-.77-.833-2.694-.833-3.464 0L3.34 16.5c-.77.833.192 2.5 1.732 2.5z"/></svg>
              {{ t('quota.data_stale') }}
            </div>
            <QuotaResultDisplay v-if="snapshot.result" :result="snapshot.result" />
            <div v-if="snapshot.queried_at" class="text-xs text-text-secondary mt-3">
              {{ t('quota.queried_at') }}: {{ formatRelativeTime(snapshot.queried_at) }}
              <span v-if="snapshot.result?.duration_ms"> · {{ t('quota.duration') }}: {{ snapshot.result.duration_ms }}ms</span>
            </div>
          </div>

          <div v-else class="text-center text-text-secondary py-8">
            {{ t('quota.never_queried') }}
          </div>
        </section>
      </main>

      <footer v-if="!loading && !notFound && !loadError" class="sticky bottom-0 z-10 app-panel border-t border-default px-6 py-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div v-if="saveMsg" :class="['text-sm', saveOk ? 'text-secondary' : 'text-danger']" role="status">{{ saveMsg }}</div>
        <div v-else></div>
        <div class="flex flex-wrap justify-end gap-2">
          <button type="button" class="app-control px-4 py-2 rounded-md text-sm font-medium disabled:opacity-50" :disabled="saving" @click="requestClose">{{ t('modal.cancel') }}</button>
          <button type="button" class="app-control px-4 py-2 rounded-md text-sm font-medium disabled:opacity-50" :disabled="testing || saving" @click="testQuery">{{ testing ? t('quota.refreshing') : t('quota.test') }}</button>
          <button type="button" class="px-4 py-2 bg-primary text-white rounded-md text-sm font-medium hover:opacity-90 disabled:opacity-50" :disabled="saving || testing" @click="saveConfig">{{ t('quota.save') }}</button>
        </div>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useApi } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import type {
  ProviderQuotaResult,
  ProviderUsageUpdateRequest,
  PublicQuotaConfig,
  QuotaSnapshot,
} from '@/composables/useApi'
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
  type QuotaFormState,
} from '@/utils/quotaForm'
import { runQuotaSaveFlow } from '@/utils/quotaSaveFlow'

const props = defineProps<{
  providerId: string
  providerName: string
}>()
const emit = defineEmits<{
  close: []
  saved: [snapshot: QuotaSnapshot | null]
}>()

const api = useApi()
const { t } = useI18n()
const dialogEl = ref<HTMLElement | null>(null)
const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')
const saving = ref(false)
const testing = ref(false)
const refreshing = ref(false)
const refreshError = ref('')
const saveMsg = ref('')
const saveOk = ref(false)
const savedConfig = ref<PublicQuotaConfig | null>(null)
const snapshot = ref<QuotaSnapshot | null>(null)
const testResult = ref<ProviderQuotaResult | null>(null)
const detectedTokenPlan = ref('')
const detectedBalance = ref('')
const isMiMoDetected = ref(false)
let previousBodyOverflow = ''
let disposed = false
let savedTimer: ReturnType<typeof setTimeout> | null = null
let settleSavedDelay: (() => void) | null = null

const form = reactive<QuotaFormState>({
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

const effectiveTokenPlanProvider = computed(() =>
  resolveEffectiveProvider(form.coding_plan_provider, detectedTokenPlan.value)
)
const showBaseURL = computed(() => showBaseURLField(form.template_type, effectiveTokenPlanProvider.value))
const showAPIKey = computed(() => showAPIKeyField(form.template_type, effectiveTokenPlanProvider.value))
const showAccessToken = computed(() => form.template_type === 'newapi')
const showScript = computed(() => ['general', 'custom'].includes(form.template_type))
const isVolcengine = computed(() => showVolcengineFields(form.template_type, effectiveTokenPlanProvider.value))
const isZenMux = computed(() => shouldShowZenMuxFields(form.template_type, effectiveTokenPlanProvider.value))
const isMiMo = computed(() => isMiMoDetected.value)
const showMiMoWarning = computed(() => shouldShowMiMoWarning(form.template_type, isMiMoDetected.value))
const showOfficialBalanceInfo = computed(() => shouldShowOfficialBalanceInfo(form.template_type, detectedBalance.value))
const showZenMuxFields = isZenMux

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function requestClose() {
  if (!saving.value) emit('close')
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    requestClose()
    return
  }
  if (event.key !== 'Tab' || !dialogEl.value) return

  const focusable = Array.from(dialogEl.value.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
  )).filter(element => element.getAttribute('aria-hidden') !== 'true')

  if (focusable.length === 0) {
    event.preventDefault()
    dialogEl.value.focus()
    return
  }

  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement
  if (event.shiftKey && (active === first || !focusable.includes(active as HTMLElement))) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && (active === last || !focusable.includes(active as HTMLElement))) {
    event.preventDefault()
    first.focus()
  }
}

function waitForSavedDelay(): Promise<boolean> {
  return new Promise((resolve) => {
    let settled = false
    const settle = (shouldEmit: boolean) => {
      if (settled) return
      settled = true
      savedTimer = null
      settleSavedDelay = null
      resolve(shouldEmit)
    }
    settleSavedDelay = () => settle(false)
    savedTimer = setTimeout(() => settle(!disposed), 800)
  })
}

function clearZenMuxOverride() {
  form.zenmux_base_url = ''
  form.zenmux_api_key = ''
  form.clear_zenmux_api_key = true
}

function clearSubmittedSecrets() {
  form.script_api_key = ''
  form.zenmux_api_key = ''
  form.access_token = ''
  form.secret_access_key = ''
  form.clear_script_api_key = false
  form.clear_zenmux_api_key = false
  form.clear_access_token = false
  form.clear_secret_access_key = false
}

function populateForm(config: PublicQuotaConfig) {
  form.enabled = config.enabled
  form.template_type = (config.template_type || 'general') as QuotaFormState['template_type']
  form.coding_plan_provider = config.coding_plan_provider || ''
  form.timeout_seconds = config.timeout_seconds || 10
  form.auto_query_interval_minutes = config.auto_query_interval_minutes ?? 5
  form.script = config.script || ''
  form.base_url = config.base_url || ''
  form.zenmux_base_url = config.zenmux_base_url || ''
  form.user_id = config.user_id || ''
  form.access_key_id = config.access_key_id || ''
  clearSubmittedSecrets()
}

function formatRelativeTime(isoStr: string): string {
  const diffMs = Date.now() - new Date(isoStr).getTime()
  if (diffMs < 60000) return t('quota.just_now')
  const mins = Math.floor(diffMs / 60000)
  if (mins < 60) return t('quota.minutes_ago', { n: mins })
  const hours = Math.floor(mins / 60)
  if (hours < 24) return t('quota.hours_ago', { n: hours })
  return t('quota.days_ago', { n: Math.floor(hours / 24) })
}

async function loadConfig() {
  loading.value = true
  notFound.value = false
  loadError.value = ''
  try {
    const data = await api.getProviderUsage(props.providerId)
    if (disposed) return
    savedConfig.value = data.config
    snapshot.value = data.snapshot || null
    detectedTokenPlan.value = data.detected_token_plan || ''
    detectedBalance.value = data.detected_balance || ''
    isMiMoDetected.value = !!data.is_mimo
    populateForm(data.config)
  } catch (error: unknown) {
    if (disposed) return
    const detail = errorMessage(error)
    if (detail.toLowerCase().includes('not found') || detail.includes('404')) {
      notFound.value = true
    } else {
      loadError.value = `${t('quota.load_failed')}: ${detail}`
    }
  } finally {
    if (!disposed) loading.value = false
  }
}

async function testQuery() {
  testing.value = true
  testResult.value = null
  try {
    const payload = buildTestPayload(form, detectedTokenPlan.value) as ProviderUsageUpdateRequest
    const response = await api.testProviderUsage(props.providerId, payload)
    if (disposed) return
    testResult.value = response.result
  } catch (error: unknown) {
    if (disposed) return
    testResult.value = {
      provider_id: props.providerId,
      template_type: form.template_type,
      success: false,
      error_code: 'network_error',
      error_message: errorMessage(error),
      queried_at: new Date().toISOString(),
      duration_ms: 0,
    }
  } finally {
    if (!disposed) testing.value = false
  }
}

async function refreshNow() {
  refreshing.value = true
  refreshError.value = ''
  try {
    const response = await api.queryProviderUsage(props.providerId)
    if (disposed) return
    if (!response.success || !response.result.success) {
      throw new Error(response.result.error_message || t('quota.query_failed'))
    }
    const data = await api.getProviderUsage(props.providerId)
    if (disposed) return
    snapshot.value = data.snapshot || null
    testResult.value = null
  } catch (cause: unknown) {
    if (disposed) return
    const error = errorMessage(cause)
    refreshError.value = error
  } finally {
    if (!disposed) refreshing.value = false
  }
}

async function saveConfig() {
  saving.value = true
  saveMsg.value = ''
  refreshError.value = ''
  try {
    const payload = buildSavePayload(form, detectedTokenPlan.value, savedConfig.value) as ProviderUsageUpdateRequest
    const outcome = await runQuotaSaveFlow(payload, {
      update: (data) => api.updateProviderUsage(props.providerId, data),
      query: () => api.queryProviderUsage(props.providerId),
      reload: () => api.getProviderUsage(props.providerId),
    })
    if (disposed) return

    if (outcome.configSaved) {
      savedConfig.value = outcome.config
      clearSubmittedSecrets()
    }

    if (!outcome.ok) {
      saveOk.value = false
      saveMsg.value = outcome.configSaved
        ? t('quota.saved_query_failed', { error: outcome.error })
        : outcome.error
      return
    }

    snapshot.value = outcome.snapshot
    testResult.value = null
    saveOk.value = true
    saveMsg.value = outcome.snapshot === null
      ? t('quota.save_success')
      : t('quota.query_success')
    const shouldEmit = await waitForSavedDelay()
    if (!shouldEmit || disposed) return
    emit('saved', outcome.snapshot)
  } finally {
    if (!disposed) saving.value = false
  }
}

onMounted(async () => {
  previousBodyOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  document.addEventListener('keydown', handleKeydown)
  await nextTick()
  if (disposed) return
  dialogEl.value?.focus()
  await loadConfig()
})

onUnmounted(() => {
  disposed = true
  if (savedTimer !== null) clearTimeout(savedTimer)
  settleSavedDelay?.()
  document.body.style.overflow = previousBodyOverflow
  document.removeEventListener('keydown', handleKeydown)
})
</script>
