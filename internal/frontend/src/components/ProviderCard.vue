<template>
  <div :class="['app-panel p-6 rounded-lg mb-3 transition-all duration-200 cursor-default', provider.active ? 'border-secondary bg-secondary-light' : 'hover:border-primary', !provider.enabled ? 'opacity-50' : '']">
    <div class="flex items-center gap-3 mb-3.5">
      <span :class="['w-2.5 h-2.5 rounded-full flex-shrink-0', provider.enabled ? 'bg-secondary' : 'bg-gray-300']" />
      <span class="text-base font-bold flex-1">{{ provider.name }}</span>
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
      <button class="px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted" @click="$emit('edit')">{{ t('providers.edit') }}</button>
      <button class="px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted" @click="$emit('duplicate', provider.id)">{{ t('providers.duplicate') }}</button>
      <button class="px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-transparent text-text-secondary hover:bg-muted" @click="$emit('test')">{{ t('providers.test') }}</button>
      <button v-if="provider.enabled && !provider.active" class="px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-primary text-white" @click="$emit('activate')">{{ t('providers.set_active') }}</button>
      <button :class="['px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 text-white', provider.enabled ? 'bg-accent' : 'bg-secondary']" @click="$emit('toggle')">{{ provider.enabled ? t('providers.disable') : t('providers.enable') }}</button>
      <button class="px-3.5 py-1.5 border-none rounded-md text-xs font-semibold cursor-pointer transition-all duration-150 hover:scale-105 bg-danger text-white" @click="$emit('delete')">{{ t('providers.delete') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { Provider } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'

defineProps<{ provider: Provider }>()
defineEmits<{ edit: []; delete: [id: string]; activate: [id: string]; toggle: [id: string]; test: [id: string]; duplicate: [id: string] }>()

const { t } = useI18n()
</script>
