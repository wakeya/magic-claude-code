<template>
  <div class="fixed inset-0 bg-black/50 z-50 flex justify-center items-center" @click.self="$emit('close')">
    <div class="app-panel p-8 rounded-lg w-[90%] max-w-[1080px] max-h-[90vh] overflow-y-auto">
      <div class="flex justify-between items-center mb-6">
        <h2 class="text-lg font-bold m-0">{{ provider ? t('modal.edit_title') : t('modal.add_title') }}</h2>
        <button class="bg-transparent border-none text-2xl cursor-pointer app-muted hover:text-fg" @click="$emit('close')">&times;</button>
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.name') }}</label>
        <input v-model="form.name" type="text" placeholder="e.g. DashScope (AliCloud)" class="app-control w-full px-4 py-3 rounded-lg text-sm transition-all duration-200 outline-none focus:border-primary" />
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.api_url') }}</label>
        <input v-model="form.api_url" type="text" :placeholder="apiURLPlaceholder" class="app-control w-full px-4 py-3 rounded-lg text-sm transition-all duration-200 outline-none focus:border-primary" />
        <p class="app-muted text-xs mt-1.5">{{ t('modal.api_url_hint') }}</p>
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.api_format') }}</label>
        <select v-model="form.api_format" class="app-control w-full px-4 py-3 rounded-lg text-sm transition-all duration-200 outline-none focus:border-primary">
          <option value="anthropic">{{ t('modal.api_format_anthropic') }}</option>
          <option value="openai_chat">{{ t('modal.api_format_openai_chat') }}</option>
          <option value="openai_responses">{{ t('modal.api_format_openai_responses') }}</option>
        </select>
      </div>

      <div v-if="isOpenAICompatible" class="mb-5">
        <label class="flex items-center gap-2 cursor-pointer mb-4">
          <input v-model="form.claude_code_compat_hint" type="checkbox" class="w-4 h-4 accent-primary cursor-pointer" />
          <span class="app-muted text-sm">{{ t('modal.claude_code_compat_hint') }}</span>
        </label>
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.openai_extra_params') }}</label>
        <textarea
          v-model="openAIExtraParamsText"
          rows="7"
          spellcheck="false"
          class="app-control w-full px-4 py-3 rounded-lg text-sm font-mono transition-all duration-200 outline-none focus:border-primary"
        ></textarea>
        <p class="app-muted text-xs mt-1.5">{{ t('modal.openai_extra_params_hint') }}</p>
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.api_token') }}</label>
        <div class="relative">
          <input v-model="form.api_token" :type="showToken ? 'text' : 'password'" :placeholder="t('modal.api_token_placeholder')" class="app-control w-full px-4 py-3 pr-10 rounded-lg text-sm transition-all duration-200 outline-none focus:border-primary" />
          <button type="button" class="absolute right-3 top-1/2 -translate-y-1/2 bg-transparent border-none cursor-pointer text-text-secondary hover:text-fg p-0" @click="toggleToken">
            <svg v-if="!showToken" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
            <svg v-else width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
          </button>
        </div>
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.supports_thinking') }}</label>
        <label class="flex items-center gap-2 cursor-pointer">
          <input v-model="form.supports_thinking" type="checkbox" class="w-4 h-4 accent-primary cursor-pointer" />
          <span class="app-muted text-sm">{{ t('modal.supports_thinking_hint') }}</span>
        </label>
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.strip_unknown_content_blocks') }}</label>
        <label class="flex items-center gap-2 cursor-pointer">
          <input v-model="form.strip_unknown_content_blocks" type="checkbox" class="w-4 h-4 accent-primary cursor-pointer" />
          <span class="app-muted text-sm">{{ t('modal.strip_unknown_content_blocks_hint') }}</span>
        </label>
      </div>

      <div class="mb-5">
        <div class="flex items-center gap-2 mb-2">
          <label class="block text-[13px] font-semibold">{{ t('modal.multimodal_switch') }}</label>
          <span class="inline-flex h-5 w-5 items-center justify-center rounded-full app-control text-xs font-bold cursor-help" :title="t('modal.multimodal_hint')">?</span>
        </div>
        <label class="flex items-center gap-2 cursor-pointer">
          <input v-model="form.multimodal_switch" type="checkbox" class="w-4 h-4 accent-primary cursor-pointer" />
          <span class="app-muted text-sm">{{ t('modal.multimodal_hint') }}</span>
        </label>
      </div>

      <div v-if="form.multimodal_switch" class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.multimodal_model') }}</label>
        <input v-model="form.multimodal_model" type="text" placeholder="mimo-vl-pro" class="app-control w-full px-4 py-3 rounded-lg text-sm transition-all duration-200 outline-none focus:border-primary" />
      </div>

      <div class="mb-5 pt-4" style="border-top: 1px solid var(--app-border)">
        <h3 class="text-sm font-bold mb-3">{{ t('modal.rate_limit_section') }}</h3>

        <label class="flex items-center gap-2 cursor-pointer mb-2">
          <input v-model="form.rate_limit_queue_enabled" type="checkbox" class="w-4 h-4 accent-primary cursor-pointer" />
          <span class="text-sm font-semibold">{{ t('modal.rate_limit_queue_enabled') }}</span>
        </label>
        <p class="app-muted text-xs mb-3">{{ t('modal.rate_limit_queue_hint') }}</p>

        <div v-if="form.rate_limit_queue_enabled" class="grid grid-cols-3 gap-3 mb-4">
          <div>
            <label class="block text-xs font-semibold mb-1">{{ t('modal.max_concurrent_requests') }}</label>
            <input v-model.number="form.max_concurrent_requests" type="number" min="1" class="app-control w-full px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
          </div>
          <div>
            <label class="block text-xs font-semibold mb-1">{{ t('modal.max_queue_size') }}</label>
            <input v-model.number="form.max_queue_size" type="number" min="0" class="app-control w-full px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
          </div>
          <div>
            <label class="block text-xs font-semibold mb-1">{{ t('modal.queue_timeout_ms') }}</label>
            <input v-model.number="form.queue_timeout_ms" type="number" min="1000" class="app-control w-full px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
          </div>
        </div>

        <label class="flex items-center gap-2 cursor-pointer mb-2">
          <input v-model="form.retry_429_enabled" type="checkbox" class="w-4 h-4 accent-primary cursor-pointer" />
          <span class="text-sm font-semibold">{{ t('modal.retry_429_enabled') }}</span>
        </label>
        <p class="app-muted text-xs mb-3">{{ t('modal.retry_429_hint') }}</p>

        <div v-if="form.retry_429_enabled" class="grid grid-cols-3 gap-3">
          <div>
            <label class="block text-xs font-semibold mb-1">{{ t('modal.retry_429_max_attempts') }}</label>
            <input v-model.number="form.retry_429_max_attempts" type="number" min="1" class="app-control w-full px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
          </div>
          <div>
            <label class="block text-xs font-semibold mb-1">{{ t('modal.retry_429_initial_delay_ms') }}</label>
            <input v-model.number="form.retry_429_initial_delay_ms" type="number" min="100" class="app-control w-full px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
          </div>
          <div>
            <label class="block text-xs font-semibold mb-1">{{ t('modal.retry_429_max_delay_ms') }}</label>
            <input v-model.number="form.retry_429_max_delay_ms" type="number" min="1000" class="app-control w-full px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
          </div>
        </div>
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.model_mappings') }}</label>
        <div class="space-y-2.5">
          <div v-for="(_, i) in mappings" :key="i" class="flex gap-2.5 items-center">
            <input v-model="mappings[i].from" type="text" placeholder="claude-sonnet-4" class="app-control flex-1 px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
            <span class="text-primary font-bold">&rarr;</span>
            <input v-model="mappings[i].to" type="text" placeholder="qwen-max" class="app-control flex-1 px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
            <button
              v-if="mappings.length > 1"
              class="px-2 py-1 bg-danger text-white border-none rounded-md text-xs font-semibold cursor-pointer hover:scale-105 transition-all"
              @click="mappings.splice(i, 1)"
            >X</button>
          </div>
        </div>
        <button class="app-control mt-2.5 px-3 py-1.5 rounded-md text-xs font-semibold cursor-pointer transition-all" @click="mappings.push({ from: '', to: '' })">{{ t('modal.add_mapping') }}</button>
      </div>

      <div class="mb-5 pt-4" style="border-top: 1px solid var(--app-border)">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.exposed_models') }}</label>
        <div class="space-y-2.5">
          <div v-for="(em, i) in exposedModels" :key="i" class="grid grid-cols-1 md:grid-cols-[1fr_1fr_1.2fr_1fr_auto_auto] gap-2 items-center">
            <input v-model="em.id" type="text" :placeholder="t('modal.exposed_model_id')" class="app-control px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
            <input v-model="em.label" type="text" :placeholder="t('modal.exposed_model_label')" class="app-control px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
            <input v-model="em.description" type="text" :placeholder="t('modal.exposed_model_desc')" class="app-control px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
            <input v-model="em.backend_model" type="text" :placeholder="t('modal.exposed_model_backend')" class="app-control px-3 py-2 rounded-md text-sm outline-none focus:border-primary" />
            <label class="flex items-center gap-1 text-xs whitespace-nowrap cursor-pointer" :title="t('modal.exposed_model_1m_hint')">
              <input v-model="em.context_1m" type="checkbox" class="w-4 h-4 accent-primary cursor-pointer" />
              1M
            </label>
            <button
              class="px-2 py-1 bg-danger text-white border-none rounded-md text-xs font-semibold cursor-pointer hover:scale-105 transition-all whitespace-nowrap"
              @click="exposedModels.splice(i, 1)"
            >X</button>
          </div>
        </div>
        <button class="app-control mt-2.5 px-3 py-1.5 rounded-md text-xs font-semibold cursor-pointer transition-all" @click="exposedModels.push({ id: '', label: '', description: '', backend_model: '', context_1m: false })">{{ t('modal.add_exposed_model') }}</button>
      </div>

      <div class="flex gap-2.5">
        <button class="app-control px-5 py-2.5 rounded-lg text-sm font-semibold cursor-pointer transition-all" @click="$emit('close')">{{ t('modal.cancel') }}</button>
        <button class="app-control px-5 py-2.5 rounded-lg text-sm font-semibold cursor-pointer transition-all" @click="testConnection">{{ t('modal.test') }}</button>
        <button class="px-5 py-2.5 bg-primary text-white border-none rounded-lg text-sm font-semibold cursor-pointer hover:bg-primary-hover hover:scale-[1.02] transition-all" @click="save">{{ t('modal.save') }}</button>
      </div>

      <p v-if="message.text" :class="['mt-3 px-3 py-2 rounded-lg text-sm font-medium', message.ok ? 'bg-green-100 text-green-800' : 'bg-red-100 text-red-800']">
        {{ message.text }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed, watch } from 'vue'
import { useApi, type Provider } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import { isOpenAICompatibleFormat, shouldDefaultClaudeCodeCompatHint, type ProviderAPIFormat } from '@/utils/providerForm'

const props = defineProps<{ provider: Provider | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const api = useApi()
const { t } = useI18n()

const form = reactive({
  name: '',
  api_url: '',
  api_token: '',
  api_format: 'anthropic' as ProviderAPIFormat,
  claude_code_compat_hint: true,
  supports_thinking: false,
  multimodal_switch: false,
  multimodal_model: '',
  strip_unknown_content_blocks: false,
  rate_limit_queue_enabled: false,
  max_concurrent_requests: 5,
  max_queue_size: 20,
  queue_timeout_ms: 60000,
  retry_429_enabled: false,
  retry_429_max_attempts: 2,
  retry_429_initial_delay_ms: 1000,
  retry_429_max_delay_ms: 10000,
})

const mappings = ref<{ from: string; to: string }[]>([{ from: '', to: '' }])
const exposedModels = ref<{ id: string; label: string; description: string; backend_model: string; context_1m: boolean }[]>([])
const openAIExtraParamsText = ref(formatOpenAIExtraParams(defaultOpenAIExtraParams()))
const message = ref<{ text: string; ok: boolean }>({ text: '', ok: false })
const showToken = ref(false)
const tokenRevealed = ref(false)
const isOpenAICompatible = computed(() => isOpenAICompatibleFormat(form.api_format))
const apiURLPlaceholder = computed(() => isOpenAICompatible.value ? 'https://example.com/v1' : 'https://api.anthropic.com')

watch(() => form.api_format, (newFormat, oldFormat) => {
  if (shouldDefaultClaudeCodeCompatHint(oldFormat, newFormat)) {
    form.claude_code_compat_hint = true
  }
})

watch(() => form.rate_limit_queue_enabled, (enabled) => {
  if (enabled) {
    if (form.max_concurrent_requests <= 0) form.max_concurrent_requests = 5
    if (form.max_queue_size <= 0) form.max_queue_size = 20
    if (form.queue_timeout_ms <= 0) form.queue_timeout_ms = 60000
  }
})

watch(() => form.retry_429_enabled, (enabled) => {
  if (enabled) {
    if (form.retry_429_max_attempts <= 0) form.retry_429_max_attempts = 2
    if (form.retry_429_initial_delay_ms <= 0) form.retry_429_initial_delay_ms = 1000
    if (form.retry_429_max_delay_ms <= 0) form.retry_429_max_delay_ms = 10000
  }
})

onMounted(() => {
  if (props.provider) {
    form.name = props.provider.name
    form.api_url = props.provider.api_url
    form.api_token = props.provider.api_token_mask || ''
    form.api_format = props.provider.api_format || 'anthropic'
    form.claude_code_compat_hint = props.provider.claude_code_compat_hint ?? true
    form.supports_thinking = props.provider.supports_thinking || false
    form.multimodal_switch = props.provider.multimodal_switch || false
    form.multimodal_model = props.provider.multimodal_model || ''
    form.strip_unknown_content_blocks = props.provider.strip_unknown_content_blocks || false
    form.rate_limit_queue_enabled = props.provider.rate_limit_queue_enabled || false
    form.max_concurrent_requests = props.provider.max_concurrent_requests || 5
    form.max_queue_size = props.provider.max_queue_size || 20
    form.queue_timeout_ms = props.provider.queue_timeout_ms || 60000
    form.retry_429_enabled = props.provider.retry_429_enabled || false
    form.retry_429_max_attempts = props.provider.retry_429_max_attempts || 2
    form.retry_429_initial_delay_ms = props.provider.retry_429_initial_delay_ms || 1000
    form.retry_429_max_delay_ms = props.provider.retry_429_max_delay_ms || 10000
    openAIExtraParamsText.value = formatOpenAIExtraParams(props.provider.openai_extra_params || defaultOpenAIExtraParams())
    const entries = Object.entries(props.provider.model_mappings || {})
    mappings.value = entries.length > 0 ? entries.map(([from, to]) => ({ from, to })) : [{ from: '', to: '' }]
    if (props.provider.exposed_models?.length) {
      exposedModels.value = props.provider.exposed_models.map(em => ({
        id: em.id, label: em.label, description: em.description, backend_model: em.backend_model, context_1m: em.context_1m ?? false,
      }))
    }
  }
})

function collectMappings(): Record<string, string> {
  const result: Record<string, string> = {}
  for (const m of mappings.value) {
    if (m.from.trim() && m.to.trim()) {
      result[m.from.trim()] = m.to.trim()
    }
  }
  return result
}

function isEmptyExposedModel(em: { id: string; label: string; description: string; backend_model: string; context_1m: boolean }) {
  return !em.id && !em.label && !em.description && !em.backend_model
}

function collectExposedModels(): { ok: true; value: { id: string; label: string; description: string; backend_model: string; context_1m: boolean }[] } | { ok: false; error: string } {
  const rows = exposedModels.value.map(em => ({
    id: em.id.trim(),
    label: em.label.trim(),
    description: em.description.trim(),
    backend_model: em.backend_model.trim(),
    context_1m: em.context_1m ?? false,
  }))
  const partial = rows.find(em => !isEmptyExposedModel(em) && (!em.id || !em.label))
  if (partial) {
    return { ok: false as const, error: t('modal.exposed_model_required') }
  }
  return { ok: true as const, value: rows.filter(em => !isEmptyExposedModel(em)) }
}

async function save() {
  if (!form.name || !form.api_url) {
    message.value = { text: t('modal.required'), ok: false }
    return
  }
  if (form.multimodal_switch && !form.multimodal_model.trim()) {
    message.value = { text: t('modal.required'), ok: false }
    return
  }
  const collected = collectExposedModels()
  if (!collected.ok) {
    message.value = { text: collected.error, ok: false }
    return
  }
  const openAIExtraParams = parseOpenAIExtraParams()
  if (!openAIExtraParams.ok) {
    message.value = { text: openAIExtraParams.error, ok: false }
    return
  }

  const token = form.api_token.includes('****') ? '' : form.api_token
  const data = {
    name: form.name,
    api_url: form.api_url,
    api_token: token,
    api_format: form.api_format,
    ...(isOpenAICompatible.value ? { claude_code_compat_hint: form.claude_code_compat_hint } : {}),
    openai_extra_params: isOpenAICompatible.value ? openAIExtraParams.value : {},
    model_mappings: collectMappings(),
    exposed_models: collected.value,
    supports_thinking: form.supports_thinking,
    multimodal_switch: form.multimodal_switch,
    multimodal_model: form.multimodal_switch ? form.multimodal_model.trim() : '',
    strip_unknown_content_blocks: form.strip_unknown_content_blocks,
    rate_limit_queue_enabled: form.rate_limit_queue_enabled,
    max_concurrent_requests: form.rate_limit_queue_enabled ? Number(form.max_concurrent_requests) : 0,
    max_queue_size: form.rate_limit_queue_enabled ? Number(form.max_queue_size) : 0,
    queue_timeout_ms: form.rate_limit_queue_enabled ? Number(form.queue_timeout_ms) : 0,
    retry_429_enabled: form.retry_429_enabled,
    retry_429_max_attempts: form.retry_429_enabled ? Number(form.retry_429_max_attempts) : 0,
    retry_429_initial_delay_ms: form.retry_429_enabled ? Number(form.retry_429_initial_delay_ms) : 0,
    retry_429_max_delay_ms: form.retry_429_enabled ? Number(form.retry_429_max_delay_ms) : 0,
  }

  try {
    if (props.provider) {
      await api.updateProvider(props.provider.id, data)
    } else {
      await api.createProvider(data)
    }
    message.value = { text: props.provider ? t('modal.provider_updated') : t('modal.provider_created'), ok: true }
    setTimeout(() => emit('saved'), 800)
  } catch (e: any) {
    message.value = { text: e.message || t('modal.save_failed'), ok: false }
  }
}

function defaultOpenAIExtraParams(): Record<string, unknown> {
  return {
    allowed_openai_params: ['thinking', 'context_management'],
    litellm_settings: { drop_params: true },
  }
}

function formatOpenAIExtraParams(params: Record<string, unknown>): string {
  return JSON.stringify(params, null, 2)
}

function parseOpenAIExtraParams(): { ok: true; value: Record<string, unknown> } | { ok: false; error: string } {
  if (!isOpenAICompatible.value) {
    return { ok: true, value: {} }
  }
  const raw = openAIExtraParamsText.value.trim()
  if (!raw) {
    return { ok: true, value: {} }
  }
  try {
    const parsed = JSON.parse(raw)
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      return { ok: false, error: t('modal.openai_extra_params_invalid') }
    }
    return { ok: true, value: parsed as Record<string, unknown> }
  } catch {
    return { ok: false, error: t('modal.openai_extra_params_invalid') }
  }
}

async function testConnection() {
  if (!form.api_url) {
    message.value = { text: t('modal.enter_api_url'), ok: false }
    return
  }
  const token = form.api_token.includes('****') ? '' : form.api_token
  const res = await api.testProviderConnection(form.api_url, token)
  if (res.success) {
    message.value = { text: t('modal.test_ok', { code: res.status_code ?? 0 }), ok: true }
  } else {
    message.value = { text: t('modal.test_fail', { error: res.error }), ok: false }
  }
}

async function toggleToken() {
  showToken.value = !showToken.value
  if (showToken.value && props.provider && !tokenRevealed.value) {
    try {
      const res = await api.revealProviderToken(props.provider.id)
      form.api_token = res.api_token
      tokenRevealed.value = true
    } catch { /* fallback to masked value */ }
  }
}
</script>
