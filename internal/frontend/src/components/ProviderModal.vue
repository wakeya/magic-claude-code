<template>
  <div class="fixed inset-0 bg-black/50 z-50 flex justify-center items-center" @click.self="$emit('close')">
    <div class="bg-white p-8 rounded-lg w-[90%] max-w-[500px] max-h-[90vh] overflow-y-auto">
      <div class="flex justify-between items-center mb-6">
        <h2 class="text-lg font-bold m-0">{{ provider ? t('modal.edit_title') : t('modal.add_title') }}</h2>
        <button class="bg-transparent border-none text-2xl cursor-pointer text-text-secondary hover:text-fg" @click="$emit('close')">&times;</button>
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.name') }}</label>
        <input v-model="form.name" type="text" placeholder="e.g. DashScope (AliCloud)" class="w-full px-4 py-3 bg-muted border-2 border-transparent rounded-lg text-sm transition-all duration-200 outline-none focus:bg-white focus:border-primary placeholder:text-gray-400" />
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.api_url') }}</label>
        <input v-model="form.api_url" type="text" placeholder="https://dashscope.aliyuncs.com/compatible-mode/v1" class="w-full px-4 py-3 bg-muted border-2 border-transparent rounded-lg text-sm transition-all duration-200 outline-none focus:bg-white focus:border-primary placeholder:text-gray-400" />
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.api_token') }}</label>
        <input v-model="form.api_token" type="password" :placeholder="t('modal.api_token_placeholder')" class="w-full px-4 py-3 bg-muted border-2 border-transparent rounded-lg text-sm transition-all duration-200 outline-none focus:bg-white focus:border-primary placeholder:text-gray-400" />
      </div>

      <div class="mb-5">
        <label class="block text-[13px] font-semibold mb-2">{{ t('modal.model_mappings') }}</label>
        <div class="space-y-2.5">
          <div v-for="(_, i) in mappings" :key="i" class="flex gap-2.5 items-center">
            <input v-model="mappings[i].from" type="text" placeholder="claude-sonnet-4" class="flex-1 px-3 py-2 bg-muted border-2 border-transparent rounded-md text-sm outline-none focus:bg-white focus:border-primary placeholder:text-gray-400" />
            <span class="text-primary font-bold">&rarr;</span>
            <input v-model="mappings[i].to" type="text" placeholder="qwen-max" class="flex-1 px-3 py-2 bg-muted border-2 border-transparent rounded-md text-sm outline-none focus:bg-white focus:border-primary placeholder:text-gray-400" />
            <button
              v-if="mappings.length > 1"
              class="px-2 py-1 bg-danger text-white border-none rounded-md text-xs font-semibold cursor-pointer hover:scale-105 transition-all"
              @click="mappings.splice(i, 1)"
            >X</button>
          </div>
        </div>
        <button class="mt-2.5 px-3 py-1.5 bg-muted text-text-secondary border-none rounded-md text-xs font-semibold cursor-pointer hover:bg-gray-200 transition-all" @click="mappings.push({ from: '', to: '' })">{{ t('modal.add_mapping') }}</button>
      </div>

      <div class="flex gap-2.5">
        <button class="px-5 py-2.5 bg-muted text-fg border-none rounded-lg text-sm font-semibold cursor-pointer hover:bg-gray-200 transition-all" @click="$emit('close')">{{ t('modal.cancel') }}</button>
        <button class="px-5 py-2.5 bg-transparent text-text-secondary border-none rounded-lg text-sm font-semibold cursor-pointer hover:bg-muted transition-all" @click="testConnection">{{ t('modal.test') }}</button>
        <button class="px-5 py-2.5 bg-primary text-white border-none rounded-lg text-sm font-semibold cursor-pointer hover:bg-primary-hover hover:scale-[1.02] transition-all" @click="save">{{ t('modal.save') }}</button>
      </div>

      <p v-if="message.text" :class="['mt-3 px-3 py-2 rounded-lg text-sm font-medium', message.ok ? 'bg-green-100 text-green-800' : 'bg-red-100 text-red-800']">
        {{ message.text }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useApi, type Provider } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'

const props = defineProps<{ provider: Provider | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const api = useApi()
const { t } = useI18n()

const form = reactive({
  name: '',
  api_url: '',
  api_token: '',
})

const mappings = ref<{ from: string; to: string }[]>([{ from: '', to: '' }])
const message = ref<{ text: string; ok: boolean }>({ text: '', ok: false })

onMounted(() => {
  if (props.provider) {
    form.name = props.provider.name
    form.api_url = props.provider.api_url
    form.api_token = props.provider.api_token_mask || ''
    const entries = Object.entries(props.provider.model_mappings || {})
    mappings.value = entries.length > 0 ? entries.map(([from, to]) => ({ from, to })) : [{ from: '', to: '' }]
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

async function save() {
  if (!form.name || !form.api_url) {
    message.value = { text: t('modal.required'), ok: false }
    return
  }

  const token = form.api_token.includes('****') ? '' : form.api_token
  const data = {
    name: form.name,
    api_url: form.api_url,
    api_token: token,
    model_mappings: collectMappings(),
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
</script>
