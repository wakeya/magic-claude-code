<template>
  <div class="fixed inset-0 bg-black/50 z-[60] flex justify-center items-center px-4" @click.self="emit('close')">
    <div class="app-panel rounded-lg w-[92vw] max-w-[1100px] max-h-[90vh] overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="script-generator-title">
      <header class="sticky top-0 z-10 app-panel border-b border-default px-6 py-4 flex items-start justify-between gap-4">
        <div>
          <h2 id="script-generator-title" class="text-lg font-bold m-0">{{ t('quota.ai_generate_title') }}</h2>
        </div>
        <button
          type="button"
          class="bg-transparent border-none text-2xl cursor-pointer app-muted hover:text-fg disabled:opacity-50"
          :aria-label="t('modal.cancel')"
          :disabled="loading"
          @click="emit('close')"
        >&times;</button>
      </header>

      <main class="p-6">
        <div class="flex flex-col md:flex-row gap-6">
          <div class="flex-1 min-w-0 space-y-4">
            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.ai_generate_provider') }}</label>
              <select
                v-model="selectedProviderId"
                class="w-full app-control rounded-md px-3 py-2 text-sm"
                :disabled="loading || props.llmProviders.length === 0"
              >
                <option v-for="provider in props.llmProviders" :key="provider.id" :value="provider.id">
                  {{ provider.name }}
                </option>
              </select>
              <div class="text-xs text-text-secondary mt-1">{{ t('quota.ai_generate_provider_hint') }}</div>
            </div>

            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.ai_generate_model') }}</label>
              <input
                v-model="model"
                :list="modelOptionsId"
                type="text"
                class="w-full app-control rounded-md px-3 py-2 text-sm"
              />
              <datalist :id="modelOptionsId">
                <option v-for="option in modelOptions" :key="option" :value="option" />
              </datalist>
            </div>

            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.ai_generate_prompt') }}</label>
              <textarea
                v-model="prompt"
                rows="4"
                class="w-full app-control rounded-md px-3 py-2 text-sm"
                :placeholder="t('quota.ai_generate_prompt_hint')"
              ></textarea>
            </div>

            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.ai_generate_sample') }}</label>
              <textarea
                v-model="responseSample"
                rows="8"
                class="w-full app-control rounded-md px-3 py-2 text-sm font-mono"
                spellcheck="false"
                :placeholder="t('quota.ai_generate_sample_hint')"
              ></textarea>
            </div>

            <div>
              <label class="block text-sm font-medium mb-1">{{ t('quota.ai_generate_request_info') }}</label>
              <textarea
                v-model="requestInfo"
                rows="3"
                class="w-full app-control rounded-md px-3 py-2 text-sm"
                :placeholder="t('quota.ai_generate_request_info_hint')"
              ></textarea>
            </div>

            <div v-if="error" class="text-sm text-danger" role="alert">{{ error }}</div>
            <div
              v-if="warnings.length > 0"
              class="text-sm rounded-md p-3"
              style="background: rgba(234, 179, 8, 0.15); color: rgb(161, 98, 7);"
              role="status"
            >
              <div class="font-medium mb-1">{{ t('quota.ai_generate_warnings') }}</div>
              <ul class="list-disc ml-5 space-y-1">
                <li v-for="(w, i) in warnings" :key="i" class="text-xs">{{ w }}</li>
              </ul>
            </div>
          </div>

          <aside class="w-full md:w-[320px] shrink-0">
            <h3 class="text-sm font-semibold mb-3">{{ t('quota.ai_generate_examples_title') }}</h3>
            <div v-for="ex in scriptExamples" :key="ex.id" class="border border-default rounded-md p-3 mb-3">
              <div class="flex items-start justify-between gap-2">
                <button
                  type="button"
                  class="min-w-0 text-left text-sm font-medium hover:underline"
                  :aria-expanded="expanded[ex.id] === true"
                  @click="toggleExample(ex.id)"
                >
                  <span aria-hidden="true">{{ expanded[ex.id] ? '▼' : '▶' }}</span>
                  {{ t(ex.titleKey) }}
                </button>
                <button
                  type="button"
                  class="shrink-0 text-xs text-primary hover:underline disabled:opacity-60"
                  :disabled="copyingId === ex.id"
                  @click="copyExample(ex.id)"
                >
                  {{ copiedId === ex.id ? t('quota.ai_generate_copied') : copyFailedId === ex.id ? t('quota.ai_generate_copy_failed') : t('quota.ai_generate_copy') }}
                </button>
              </div>
              <div class="text-xs text-text-secondary mt-1">{{ t(ex.descKey) }}</div>
              <pre v-if="expanded[ex.id]" class="mt-2 text-xs font-mono whitespace-pre overflow-x-auto bg-black/5 dark:bg-white/5 rounded p-2 max-h-[300px] overflow-y-auto">{{ ex.script }}</pre>
            </div>
          </aside>
        </div>
      </main>

      <footer class="sticky bottom-0 z-10 app-panel border-t border-default px-6 py-4 flex justify-end gap-2">
        <button type="button" class="app-control px-4 py-2 rounded-md text-sm font-medium disabled:opacity-50" :disabled="loading" @click="emit('close')">
          {{ t('modal.cancel') }}
        </button>
        <button
          type="button"
          class="px-4 py-2 bg-primary text-white rounded-md text-sm font-medium hover:opacity-90 disabled:opacity-50"
          :disabled="loading || !canSubmit"
          @click="generate"
        >
          {{ loading ? t('quota.ai_generating') : t('quota.ai_generate_submit') }}
        </button>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useApi } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'

interface LLMProviderOption {
  id: string
  name: string
  exposed_models?: { backend_model: string }[]
  model_mappings?: Record<string, string>
}

const scriptExamples = [
  {
    id: 'qwen',
    titleKey: 'quota.ai_generate_ex_qwen_title',
    descKey: 'quota.ai_generate_ex_qwen_desc',
    script: `({
  request: {
    url: "{{baseUrl}}/data/api.json?product=sfm_bailian&action=BroadScopeAspnGateway&api=zeldaHttp.apikeyMgr.%2Ftokenplan%2Fpersonal%2Fapi%2Fv2%2Fusage",
    method: "POST",
    bodyType: "form",
    headers: {
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded",
      "Accept": "application/json, text/plain, */*",
      "Referer": "https://platform.qianwenai.com/home/billing/subscription/token-plan-individual"
    },
    body: {
      product: "sfm_bailian",
      action: "BroadScopeAspnGateway",
      sec_token: "{{apiKey2}}",
      region: "cn-beijing",
      params: {
        Api: "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage",
        Data: {
          cornerstoneParam: {
            domain: "platform.qianwenai.com",
            consoleSite: "QIANWENAI",
            console: "ONE_CONSOLE",
            xsp_lang: "zh-CN",
            protocol: "V2",
            productCode: "p_efm"
          }
        },
        V: "1.0"
      }
    }
  },
  extractor: function(response) {
    if (response.code !== "200" || response.successResponse !== true) {
      return { __error_code: "upstream_business_error", __error_message: (response.data && response.data.errorMsg) || "qianwen usage query failed" };
    }
    var inner = response.data && response.data.DataV2 && response.data.DataV2.data && response.data.DataV2.data.data;
    if (!inner) { return { __error_code: "invalid_response", __error_message: "missing data.DataV2.data.data" }; }
    var tiers = [];
    if (typeof inner.per5HourPercentage === "number") { tiers.push({ window: "five_hour", utilization: inner.per5HourPercentage * 100 }); }
    if (typeof inner.per1WeekPercentage === "number") { tiers.push({ window: "seven_day", utilization: inner.per1WeekPercentage * 100, resetsAt: inner.per1WeekResetTime }); }
    if (tiers.length === 0) { return { __error_code: "empty_result", __error_message: "no percentage fields" }; }
    return tiers;
  }
})`,
  },
  {
    id: 'balance',
    titleKey: 'quota.ai_generate_ex_balance_title',
    descKey: 'quota.ai_generate_ex_balance_desc',
    script: `({
  request: {
    url: "{{baseUrl}}/user/balance",
    method: "GET",
    headers: {
      "Authorization": "Bearer {{apiKey}}",
      "Accept": "application/json"
    }
  },
  extractor: function(response) {
    var infos = response.balance_infos || [];
    var balances = [];
    for (var i = 0; i < infos.length; i++) {
      balances.push({
        planName: infos[i].currency,
        remaining: infos[i].total_balance,
        unit: infos[i].currency
      });
    }
    return balances;
  }
})`,
  },
  {
    id: 'template',
    titleKey: 'quota.ai_generate_ex_template_title',
    descKey: 'quota.ai_generate_ex_template_desc',
    script: `({
  request: {
    // {{baseUrl}} is replaced from the Base URL field; the URL must share host with it (same-origin).
    url: "{{baseUrl}}/your/endpoint",
    method: "POST",
    // bodyType:"form" encodes body as application/x-www-form-urlencoded;
    // nested object values are JSON-marshaled automatically (e.g. params).
    bodyType: "form",
    headers: {
      // {{apiKey}} / {{apiKey2}} are replaced from Script API Key / Additional secret; never appear in JS runtime.
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded"
    },
    body: {
      token: "{{apiKey2}}",
      param1: "value1",
      nested: { key: "value" }
    }
  },
  extractor: function(response) {
    // window: five_hour | seven_day | monthly; utilization is always 0-100 used percent.
    return [
      { window: "five_hour", utilization: response.used_pct }
    ];
  }
})`,
  },
]

const props = defineProps<{
  providerId: string
  llmProviders: LLMProviderOption[]
}>()

const emit = defineEmits<{
  generated: [script: string, warnings?: string[]]
  close: []
}>()

const api = useApi()
const { t } = useI18n()
const selectedProviderId = ref(defaultProviderId())
const model = ref('')
const prompt = ref('')
const responseSample = ref('')
const requestInfo = ref('')
const loading = ref(false)
const error = ref('')
const warnings = ref<string[]>([])
const expanded = ref<Record<string, boolean>>({})
const copiedId = ref('')
const copyFailedId = ref('')
const copyingId = ref('')
const modelOptionsId = `script-generator-models-${Math.random().toString(36).slice(2)}`

function defaultProviderId(): string {
  if (props.llmProviders.some(provider => provider.id === props.providerId)) {
    return props.providerId
  }
  return props.llmProviders[0]?.id || ''
}

const selectedProvider = computed(() =>
  props.llmProviders.find(provider => provider.id === selectedProviderId.value)
)

const modelOptions = computed(() => {
  const seen = new Set<string>()
  const options: string[] = []
  const exposedModels = (selectedProvider.value?.exposed_models || [])
    .map(item => item.backend_model)
  const mappedModels = Object.values(selectedProvider.value?.model_mappings || {})
    .map(model => String(model))
  for (const value of [...exposedModels, ...mappedModels]) {
    const option = value.trim()
    if (option && !seen.has(option)) {
      seen.add(option)
      options.push(option)
    }
  }
  return options
})

const canSubmit = computed(() =>
  selectedProviderId.value.trim() !== '' &&
  model.value.trim() !== '' &&
  prompt.value.trim() !== '' &&
  responseSample.value.trim() !== ''
)

watch(() => props.providerId, () => {
  const nextProviderId = defaultProviderId()
  if (selectedProviderId.value !== nextProviderId) selectedProviderId.value = nextProviderId
})

watch(() => props.llmProviders, () => {
  if (props.llmProviders.some(provider => provider.id === selectedProviderId.value)) return
  selectedProviderId.value = defaultProviderId()
}, { deep: true })

watch(selectedProviderId, () => {
  model.value = ''
})

function translatedError(code: string, message?: string): string {
  const localized = t(`error.${code}`)
  if (localized !== `error.${code}`) return localized
  return message || code
}

function toggleExample(id: string) {
  expanded.value = {
    ...expanded.value,
    [id]: !expanded.value[id],
  }
}

async function copyExample(id: string) {
  const ex = scriptExamples.find(e => e.id === id)
  if (!ex || copyingId.value === id) return
  copyingId.value = id
  copiedId.value = ''
  copyFailedId.value = ''
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(ex.script)
    } else {
      const ta = document.createElement('textarea')
      ta.value = ex.script
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      try {
        if (!document.execCommand('copy')) throw new Error('copy failed')
      } finally {
        document.body.removeChild(ta)
      }
    }
    copiedId.value = id
    setTimeout(() => { if (copiedId.value === id) copiedId.value = '' }, 2000)
  } catch {
    copyFailedId.value = id
    setTimeout(() => { if (copyFailedId.value === id) copyFailedId.value = '' }, 2000)
  } finally {
    copyingId.value = ''
  }
}

async function generate() {
  if (!canSubmit.value || loading.value) return
  loading.value = true
  error.value = ''
  warnings.value = []
  try {
    const response = await api.generateUsageScript(props.providerId, {
      llm_provider_id: selectedProviderId.value,
      model: model.value,
      prompt: prompt.value,
      response_sample: responseSample.value,
      request_info: requestInfo.value,
    })
    if (response.error_code) {
      error.value = translatedError(response.error_code, response.error_message)
      return
    }
    warnings.value = response.warnings || []
    emit('generated', response.script, warnings.value)
    if (warnings.value.length === 0) {
      emit('close')
    }
  } catch (cause: unknown) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    loading.value = false
  }
}
</script>
