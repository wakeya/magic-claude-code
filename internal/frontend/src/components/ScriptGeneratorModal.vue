<template>
  <div class="fixed inset-0 bg-black/50 z-[60] flex justify-center items-center px-4" @click.self="emit('close')">
    <div class="app-panel rounded-lg w-[92vw] max-w-[760px] max-h-[90vh] overflow-y-auto" role="dialog" aria-modal="true" aria-labelledby="script-generator-title">
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

      <main class="p-6 space-y-4">
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

const props = defineProps<{
  providerId: string
  llmProviders: LLMProviderOption[]
}>()

const emit = defineEmits<{
  generated: [script: string]
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

async function generate() {
  if (!canSubmit.value || loading.value) return
  loading.value = true
  error.value = ''
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
    emit('generated', response.script)
    emit('close')
  } catch (cause: unknown) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    loading.value = false
  }
}
</script>
