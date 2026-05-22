<template>
  <div class="session-theme rounded-[1.35rem] p-4 sm:p-5">
    <div class="grid grid-cols-1 gap-4 xl:grid-cols-[360px_minmax(0,1fr)]">
      <aside class="session-panel session-sidebar">
        <div class="session-panel-header p-4">
        <div class="mb-2 flex items-center justify-between gap-2">
          <label for="session-project-select" class="flex items-center gap-2 text-sm font-bold session-heading">
            <Folder class="h-4 w-4" />
            {{ t('sessions.projects') }}
          </label>
          <button class="session-icon-button" :title="t('sessions.refresh')" @click="reload">
            <RefreshCw class="h-4 w-4" />
          </button>
        </div>
        <select
          id="session-project-select"
          v-model="selectedProject"
          class="session-select"
          @change="selectProject(selectedProject)"
        >
          <option value="">{{ t('sessions.all_projects') }} ({{ totalSessions }})</option>
          <option v-for="project in projects" :key="project.path" :value="project.path">
            {{ project.name }} ({{ project.session_count }})
          </option>
        </select>
      </div>

      <div class="max-h-[calc(100vh-260px)] overflow-y-auto p-3">
        <div class="mb-2 flex items-center gap-2 text-xs font-bold uppercase tracking-widest session-muted">
          <MessageSquare class="h-3.5 w-3.5" />
          {{ t('sessions.sessions') }}
        </div>
        <div v-if="loading" class="session-empty-compact">{{ t('sessions.loading') }}</div>
        <div v-else-if="sessions.length === 0" class="session-empty-compact">
          {{ selectedProject ? t('sessions.empty_project') : t('sessions.empty') }}
        </div>
        <button
          v-for="session in sessions"
          :key="session.source_path"
          :class="[
            'session-card',
            selectedSession?.source_path === session.source_path ? 'session-card-active' : '',
          ]"
          @click="selectSession(session)"
        >
          <div class="line-clamp-2 text-sm font-bold session-heading">{{ session.title }}</div>
          <div v-if="!selectedProject" class="mt-1 truncate text-xs session-muted">{{ projectLabel(session.project_path) }}</div>
          <div class="mt-2 flex items-center justify-between gap-2 text-xs session-muted">
            <span>{{ relativeTime(session.last_active_at) }}</span>
            <span>{{ t('sessions.messages', { count: session.message_count }) }}</span>
          </div>
        </button>
      </div>
    </aside>

    <section class="min-w-0">
      <div v-if="!detail" class="session-empty-state">
        {{ error || t('sessions.select') }}
      </div>
      <div v-else class="grid grid-cols-1 gap-4 2xl:grid-cols-[minmax(0,1fr)_260px]">
        <div class="min-w-0">
          <div class="session-panel mb-4 p-5">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <h2 class="break-words text-xl font-bold session-heading">{{ detail.session.title }}</h2>
                <div class="mt-1 break-all text-sm session-muted">{{ detail.session.project_path }}</div>
                <div class="mt-1 flex items-center gap-2">
                  <FileText class="h-3.5 w-3.5 shrink-0 session-muted" />
                  <span class="truncate font-mono text-xs session-muted" :title="detail.session.source_path">{{ sourceFileName }}</span>
                  <button class="session-icon-button shrink-0" :title="copiedSource ? t('sessions.copied') : t('sessions.copy')" @click="copySourcePath">
                    <Copy v-if="!copiedSource" class="h-3.5 w-3.5" />
                    <Check v-else class="h-3.5 w-3.5 text-green-500" />
                  </button>
                </div>
                <div class="mt-2 text-xs session-muted">
                  {{ formatDateTime(detail.session.created_at) }} - {{ formatDateTime(detail.session.last_active_at) }}
                </div>
              </div>
              <div class="flex shrink-0 gap-2">
                <button class="session-primary-button" @click="exportSelected">
                  <Download class="h-4 w-4" />
                  {{ t('sessions.export') }}
                </button>
                <button class="session-secondary-button" @click="openCleanupHint">
                  <Terminal class="h-4 w-4" />
                  {{ t('sessions.cleanup') }}
                </button>
                <button class="session-secondary-button 2xl:hidden" @click="showOutline = true">
                  <List class="h-4 w-4" />
                  {{ t('sessions.outline') }}
                </button>
              </div>
            </div>
          </div>
          <SessionDetail ref="detailRef" :detail="detail" />
        </div>

        <aside class="hidden 2xl:block">
          <div class="session-outline-panel sticky top-4 max-h-[calc(100vh-2rem)] overflow-y-auto overscroll-contain p-3">
            <div class="mb-3 text-xs font-bold uppercase tracking-widest session-muted">{{ t('sessions.outline') }}</div>
            <SessionOutline :messages="detail.messages" @jump="jumpToMessage" />
            <div class="sticky bottom-0 flex justify-end pt-2">
              <button class="session-icon-button" :title="t('sessions.back_to_top')" @click="scrollToTop">
                <ArrowUp class="h-4 w-4" />
              </button>
            </div>
          </div>
        </aside>
      </div>
    </section>

    <div v-if="showOutline && detail" class="session-modal-backdrop fixed inset-0 z-40 p-4 2xl:hidden" @click.self="showOutline = false">
      <div class="session-modal-panel ml-auto max-h-full w-full max-w-sm overflow-y-auto p-4">
        <div class="mb-3 flex items-center justify-between">
          <div class="text-sm font-bold session-heading">{{ t('sessions.outline') }}</div>
          <button class="session-icon-button" @click="showOutline = false"><X class="h-4 w-4" /></button>
        </div>
        <SessionOutline :messages="detail.messages" @jump="jumpToMessage" />
        <div class="flex justify-end pt-3">
          <button class="session-icon-button" @click="scrollToTop(); showOutline = false">
            <ArrowUp class="h-4 w-4" />
            {{ t('sessions.back_to_top') }}
          </button>
        </div>
      </div>
    </div>

    <div v-if="cleanupHint" class="session-modal-backdrop fixed inset-0 z-50 p-4" @click.self="cleanupHint = null">
      <div class="session-modal-panel mx-auto mt-20 max-w-2xl p-5">
        <div class="mb-4 flex items-center justify-between gap-3">
          <div class="text-lg font-bold session-heading">{{ t('sessions.cleanup') }}</div>
          <button class="session-icon-button" @click="cleanupHint = null"><X class="h-4 w-4" /></button>
        </div>
        <p class="mb-4 text-sm session-muted">{{ cleanupHint.note || t('sessions.cleanup_note') }}</p>
        <CommandBlock :label="t('sessions.preview_command')" :command="cleanupHint.preview_command" />
        <CommandBlock :label="t('sessions.interactive_command')" :command="cleanupHint.interactive_command" />
      </div>
    </div>
  </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref } from 'vue'
import { ArrowUp, Check, Copy, Download, FileText, Folder, List, MessageSquare, RefreshCw, Terminal, X } from 'lucide-vue-next'
import {
  useApi,
  type SessionCleanupHint,
  type SessionDetailResponse,
  type SessionItem,
  type SessionProject,
} from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import { useTheme } from '@/composables/useTheme'
import SessionDetail from '@/components/SessionDetail.vue'
import SessionOutline from '@/components/SessionOutline.vue'
import { tokenizeCommand, type CommandTokenKind } from '@/utils/sessionCommands'

const api = useApi()
const { t, locale } = useI18n()
const { themeMode } = useTheme()

const projects = ref<SessionProject[]>([])
const sessions = ref<SessionItem[]>([])
const selectedProject = ref('')
const selectedSession = ref<SessionItem | null>(null)
const detail = ref<SessionDetailResponse | null>(null)
const cleanupHint = ref<SessionCleanupHint | null>(null)
const loading = ref(false)
const error = ref('')
const showOutline = ref(false)
const detailRef = ref<InstanceType<typeof SessionDetail> | null>(null)

const totalSessions = computed(() => projects.value.reduce((sum, project) => sum + project.session_count, 0))
const sourceFileName = computed(() => {
  const path = detail.value?.session.source_path
  if (!path) return ''
  const parts = path.split(/[\\/]/)
  return parts.at(-1) || path
})

const copiedSource = ref(false)
async function copySourcePath() {
  const path = detail.value?.session.source_path
  if (!path) return
  await navigator.clipboard?.writeText(path)
  copiedSource.value = true
  window.setTimeout(() => { copiedSource.value = false }, 1200)
}

const CommandBlock = defineComponent({
  props: {
    label: { type: String, required: true },
    command: { type: String, required: true },
  },
  setup(props) {
    const copied = ref(false)
    async function copyCommand() {
      await navigator.clipboard?.writeText(props.command)
      copied.value = true
      window.setTimeout(() => {
        copied.value = false
      }, 1200)
    }
    return () =>
      h('div', { class: 'mb-4' }, [
        h('div', { class: 'session-command-label' }, props.label),
        h('div', { class: 'session-command-block' }, [
          h(
            'code',
            { class: 'session-command-code' },
            tokenizeCommand(props.command).map((token) => h('span', { class: commandTokenClass(token.kind) }, token.text))
          ),
          h(
            'button',
            { class: 'session-command-copy', onClick: copyCommand, title: copied.value ? t('sessions.copied') : t('sessions.copy') },
            [h(Copy, { class: 'h-4 w-4' })]
          ),
        ]),
      ])
  },
})

onMounted(() => {
  void reload()
})

async function reload() {
  error.value = ''
  loading.value = true
  try {
    projects.value = await api.getSessionProjects()
    await loadSessions()
  } catch {
    error.value = t('sessions.load_failed')
  } finally {
    loading.value = false
  }
}

async function selectProject(path: string) {
  selectedProject.value = path
  selectedSession.value = null
  detail.value = null
  await loadSessions()
}

async function loadSessions() {
  const page = await api.getSessionList({ project: selectedProject.value, page: 1, page_size: 100 })
  sessions.value = page.sessions
}

async function selectSession(session: SessionItem) {
  selectedSession.value = session
  copiedSource.value = false
  const res = await api.getSessionDetail(session.id, session.source_path)
  detail.value = res
  if (res.message_count && res.message_count !== session.message_count) {
    session.message_count = res.message_count
  }
}

async function exportSelected() {
  if (!selectedSession.value) return
  try {
    const blob = await api.exportSessionHTML(selectedSession.value.id, selectedSession.value.source_path, themeMode.value)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${downloadName(selectedSession.value.title)}.html`
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    alert(t('sessions.export_failed'))
  }
}

async function openCleanupHint() {
  if (!selectedSession.value) return
  cleanupHint.value = await api.getSessionCleanupHint(selectedSession.value.id, selectedSession.value.source_path)
}

function jumpToMessage(index: number) {
  showOutline.value = false
  detailRef.value?.scrollToMessage(index)
}

function scrollToTop() {
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

function commandTokenClass(kind: CommandTokenKind): string {
  const classes: Record<CommandTokenKind, string> = {
    command: 'session-token-command',
    keyword: 'session-token-keyword',
    flag: 'session-token-flag',
    path: 'session-token-path',
    space: 'session-token-space',
    text: 'session-token-text',
  }
  return classes[kind]
}

function projectLabel(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean)
  return parts.at(-1) || path || '-'
}

function relativeTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  const diff = Date.now() - date.getTime()
  const minutes = Math.max(0, Math.round(diff / 60000))
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(minutes / 60)
  if (hours < 48) return `${hours}h`
  return `${Math.round(hours / 24)}d`
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value || '-'
  return date.toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-US')
}

function downloadName(value: string): string {
  return value.trim().replace(/[^a-zA-Z0-9\u4e00-\u9fa5_-]+/g, '-').replace(/^-+|-+$/g, '') || 'claude-session'
}
</script>
