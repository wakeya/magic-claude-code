<template>
  <div class="min-h-screen bg-muted">
    <AppHeader @logout="handleLogout" />

    <div class="max-w-[900px] mx-auto px-6 py-8">
      <div class="flex gap-1 mb-8 bg-white p-1 rounded-lg border-2 border-border w-fit">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          :class="[
            'px-6 py-2.5 rounded-md text-sm font-semibold cursor-pointer transition-all duration-150 border-none',
            activeTab === tab.key
              ? 'bg-primary text-white'
              : 'bg-transparent text-text-secondary hover:text-fg',
          ]"
          @click="activeTab = tab.key"
        >
          {{ t('tab.' + tab.key) }}
        </button>
      </div>

      <!-- Status -->
      <div v-if="activeTab === 'status'">
        <div class="grid grid-cols-3 gap-4 mb-6">
          <div class="bg-primary text-white p-7 rounded-lg text-center transition-all duration-200 hover:scale-[1.02] cursor-default">
            <div class="text-[28px] font-extrabold tracking-tight">{{ status?.running ? t('status.running') : t('status.stopped') }}</div>
            <div class="text-[13px] mt-1 font-medium opacity-85">{{ t('status.service_status') }}</div>
          </div>
          <div class="bg-secondary text-white p-7 rounded-lg text-center transition-all duration-200 hover:scale-[1.02] cursor-default">
            <div class="text-[28px] font-extrabold tracking-tight">{{ status?.uptime || '-' }}</div>
            <div class="text-[13px] mt-1 font-medium opacity-85">{{ t('status.uptime') }}</div>
          </div>
          <div class="bg-accent text-white p-7 rounded-lg text-center transition-all duration-200 hover:scale-[1.02] cursor-default">
            <div class="text-[28px] font-extrabold tracking-tight">{{ status?.requests_total ?? 0 }}</div>
            <div class="text-[13px] mt-1 font-medium opacity-85">{{ t('status.total_requests') }}</div>
          </div>
        </div>

        <div v-if="activeProvider" class="bg-white p-6 rounded-lg border-2 border-border mb-8">
          <h3 class="text-xs font-bold text-text-secondary uppercase tracking-widest mb-3.5">{{ t('status.active_provider') }}</h3>
          <div class="text-xl font-bold mb-1">{{ activeProvider.name }}</div>
          <div class="text-[13px] text-text-secondary font-mono">{{ activeProvider.api_url }}</div>
          <div v-if="Object.keys(activeProvider.model_mappings).length" class="flex flex-wrap gap-2 mt-3.5">
            <span v-for="(to, from) in activeProvider.model_mappings" :key="from" class="bg-primary-light text-primary px-3.5 py-1 rounded-full text-xs font-semibold">
              {{ from }} &rarr; {{ to }}
            </span>
          </div>
        </div>
        <div v-else class="bg-white p-6 rounded-lg border-2 border-border mb-8 text-center">
          <p class="text-danger font-medium">{{ t('status.no_provider') }}</p>
        </div>
      </div>

      <!-- Providers -->
      <div v-if="activeTab === 'providers'">
        <div class="flex items-center justify-between mb-5">
          <div class="flex items-center gap-2 text-[15px] font-bold">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <rect x="2" y="2" width="20" height="8" rx="2" /><rect x="2" y="14" width="20" height="8" rx="2" /><circle cx="6" cy="6" r="1" /><circle cx="6" cy="18" r="1" />
            </svg>
            {{ t('providers.title') }}
          </div>
          <button class="flex items-center gap-2 px-5 py-2.5 bg-primary text-white border-none rounded-lg text-sm font-semibold cursor-pointer transition-all duration-200 hover:bg-primary-hover hover:scale-[1.02]" @click="openAddModal">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
              <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {{ t('providers.add') }}
          </button>
        </div>

        <div v-if="providers.length === 0" class="text-center py-12 text-text-secondary">{{ t('providers.empty') }}</div>

        <ProviderCard v-for="p in providers" :key="p.id" :provider="p" @edit="openEditModal(p)" @delete="handleDelete(p.id)" @activate="handleActivate(p.id)" @toggle="handleToggle(p.id)" @test="handleTest(p.id)" />
      </div>

      <!-- Certificates -->
      <div v-if="activeTab === 'certs'">
        <div class="flex items-center gap-2 text-[15px] font-bold mb-5">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="11" width="18" height="11" rx="2" /><path d="M7 11V7a5 5 0 0110 0v4" />
          </svg>
          {{ t('certs.title') }}
        </div>

        <div v-if="certs" class="space-y-3">
          <div class="p-5 bg-white rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-1.5">{{ t('certs.ca_path') }}</label>
            <div class="text-sm font-medium">{{ certs.ca_cert_path }}</div>
          </div>
          <div class="p-5 bg-white rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-1.5">{{ t('certs.ca_expires') }}</label>
            <div class="text-sm font-medium">{{ formatDate(certs.ca_expires_at) }}</div>
          </div>
          <div class="p-5 bg-white rounded-lg border-2 border-border">
            <label class="block text-xs font-bold text-text-secondary uppercase tracking-widest mb-1.5">{{ t('certs.node_config') }}</label>
            <div class="bg-fg text-gray-200 px-4 py-3.5 rounded-lg font-mono text-[13px] flex justify-between items-center mt-2">
              <span>NODE_EXTRA_CA_CERTS={{ certs.ca_cert_path }}</span>
              <button class="px-3.5 py-1 bg-primary text-white border-none rounded text-xs font-semibold cursor-pointer transition-all duration-200 hover:scale-105" @click="copyPath">{{ t('certs.copy') }}</button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <ProviderModal v-if="showModal" :provider="editingProvider" @close="closeModal" @saved="handleSaved" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useApi, type Provider, type StatusInfo, type CertificateInfo } from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import AppHeader from '@/components/AppHeader.vue'
import ProviderCard from '@/components/ProviderCard.vue'
import ProviderModal from '@/components/ProviderModal.vue'

const router = useRouter()
const api = useApi()
const { t, locale } = useI18n()

const activeTab = ref('status')
const tabs = [
  { key: 'status' },
  { key: 'providers' },
  { key: 'certs' },
]

const status = ref<StatusInfo | null>(null)
const providers = ref<Provider[]>([])
const activeProviderId = ref('')
const certs = ref<CertificateInfo | null>(null)

const activeProvider = computed(() => providers.value.find((p) => p.id === activeProviderId.value))

const showModal = ref(false)
const editingProvider = ref<Provider | null>(null)

function openAddModal() { editingProvider.value = null; showModal.value = true }
function openEditModal(p: Provider) { editingProvider.value = p; showModal.value = true }
function closeModal() { showModal.value = false; editingProvider.value = null }

async function handleSaved() { closeModal(); await loadProviders() }

async function handleDelete(id: string) {
  if (!confirm(t('providers.confirm_delete'))) return
  await api.deleteProvider(id)
  await loadProviders()
}

async function handleActivate(id: string) {
  const res = await api.activateProvider(id)
  if (!res) alert(t('test.activate_failed'))
  await loadProviders()
}

async function handleToggle(id: string) {
  const res = await api.toggleProvider(id)
  if (!res.success) alert(t('test.toggle_failed'))
  await loadProviders()
}

async function handleTest(id: string) {
  const res = await api.testProvider(id)
  if (res.success) {
    const code = res.status_code ?? 0
    if (code >= 200 && code < 300) alert(t('modal.connection_ok', { code }))
    else if (code === 404) alert(t('modal.connection_404'))
    else if (code === 401 || code === 403) alert(t('modal.connection_auth', { code }))
    else alert(t('modal.connection_other', { code }))
  } else {
    alert(t('modal.connection_failed', { error: res.error }))
  }
}

async function loadStatus() { try { status.value = await api.getStatus() } catch { /* */ } }
async function loadProviders() {
  try {
    const data = await api.getProviders()
    providers.value = data.providers
    activeProviderId.value = data.active_provider_id
  } catch { /* */ }
}
async function loadCerts() { try { certs.value = await api.getCertificates() } catch { /* */ } }

async function handleLogout() { await api.logout(); router.push('/login') }

function formatDate(dateStr: string): string {
  try {
    const d = new Date(dateStr)
    if (isNaN(d.getTime())) return dateStr
    return d.toLocaleDateString(locale.value === 'zh' ? 'zh-CN' : 'en-US', { year: 'numeric', month: 'long', day: 'numeric' })
  } catch { return dateStr }
}

function copyPath() {
  if (certs.value) {
    navigator.clipboard.writeText(`NODE_EXTRA_CA_CERTS=${certs.value.ca_cert_path}`)
    alert(t('certs.copied'))
  }
}

onMounted(async () => {
  await Promise.all([loadStatus(), loadProviders(), loadCerts()])
  setInterval(loadStatus, 30000)
})
</script>
