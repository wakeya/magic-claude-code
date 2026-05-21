<template>
  <div class="grid grid-cols-1 gap-4 xl:grid-cols-[360px_minmax(0,1fr)]">
    <aside class="rounded-lg border-2 border-border bg-white">
      <div class="border-b border-border p-4">
        <div class="mb-2 flex items-center justify-between gap-2">
          <label for="session-project-select" class="flex items-center gap-2 text-sm font-bold">
            <Folder class="h-4 w-4" />
            {{ t('sessions.projects') }}
          </label>
          <button class="rounded-md p-2 text-text-secondary hover:bg-muted hover:text-fg" :title="t('sessions.refresh')" @click="reload">
            <RefreshCw class="h-4 w-4" />
          </button>
        </div>
        <select
          id="session-project-select"
          v-model="selectedProject"
          class="w-full rounded-lg border-2 border-border bg-white px-3 py-2 text-sm font-semibold text-fg outline-none transition focus:border-primary"
          @change="selectProject(selectedProject)"
        >
          <option value="">{{ t('sessions.all_projects') }} ({{ totalSessions }})</option>
          <option v-for="project in projects" :key="project.path" :value="project.path">
            {{ project.name }} ({{ project.session_count }})
          </option>
        </select>
      </div>

      <div class="max-h-[calc(100vh-260px)] overflow-y-auto p-3">
        <div class="mb-2 flex items-center gap-2 text-xs font-bold uppercase tracking-widest text-text-secondary">
          <MessageSquare class="h-3.5 w-3.5" />
          {{ t('sessions.sessions') }}
        </div>
        <div v-if="loading" class="py-8 text-center text-sm text-text-secondary">{{ t('sessions.loading') }}</div>
        <div v-else-if="sessions.length === 0" class="py-8 text-center text-sm text-text-secondary">
          {{ selectedProject ? t('sessions.empty_project') : t('sessions.empty') }}
        </div>
        <button
          v-for="session in sessions"
          :key="session.source_path"
          :class="[
            'mb-2 w-full rounded-lg border-2 p-3 text-left transition',
            selectedSession?.source_path === session.source_path ? 'border-primary bg-primary-light' : 'border-border bg-white hover:border-primary/50',
          ]"
          @click="selectSession(session)"
        >
          <div class="line-clamp-2 text-sm font-bold text-fg">{{ session.title }}</div>
          <div v-if="!selectedProject" class="mt-1 truncate text-xs text-text-secondary">{{ projectLabel(session.project_path) }}</div>
          <div class="mt-2 flex items-center justify-between gap-2 text-xs text-text-secondary">
            <span>{{ relativeTime(session.last_active_at) }}</span>
            <span>{{ t('sessions.messages', { count: session.message_count }) }}</span>
          </div>
        </button>
      </div>
    </aside>

    <section class="min-w-0">
      <div v-if="!detail" class="rounded-lg border-2 border-border bg-white p-10 text-center text-text-secondary">
        {{ error || t('sessions.select') }}
      </div>
      <div v-else class="grid grid-cols-1 gap-4 2xl:grid-cols-[minmax(0,1fr)_260px]">
        <div class="min-w-0">
          <div class="mb-4 rounded-lg border-2 border-border bg-white p-5">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <h2 class="break-words text-xl font-bold">{{ detail.session.title }}</h2>
                <div class="mt-1 break-all text-sm text-text-secondary">{{ detail.session.project_path }}</div>
                <div class="mt-2 text-xs text-text-secondary">
                  {{ formatDateTime(detail.session.created_at) }} - {{ formatDateTime(detail.session.last_active_at) }}
                </div>
              </div>
              <div class="flex shrink-0 gap-2">
                <button class="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-white hover:bg-primary-hover" @click="exportSelected">
                  <Download class="h-4 w-4" />
                  {{ t('sessions.export') }}
                </button>
                <button class="inline-flex items-center gap-2 rounded-lg border-2 border-border px-4 py-2 text-sm font-semibold text-fg hover:border-primary" @click="openCleanupHint">
                  <Terminal class="h-4 w-4" />
                  {{ t('sessions.cleanup') }}
                </button>
                <button class="inline-flex items-center gap-2 rounded-lg border-2 border-border px-3 py-2 text-sm font-semibold text-fg hover:border-primary 2xl:hidden" @click="showOutline = true">
                  <List class="h-4 w-4" />
                  {{ t('sessions.outline') }}
                </button>
              </div>
            </div>
          </div>
          <SessionDetail ref="detailRef" :detail="detail" />
        </div>

        <aside class="hidden 2xl:block">
          <div class="sticky top-4 max-h-[calc(100vh-2rem)] overflow-y-auto overscroll-contain rounded-lg border-2 border-border bg-muted p-3">
            <div class="mb-3 text-xs font-bold uppercase tracking-widest text-text-secondary">{{ t('sessions.outline') }}</div>
            <SessionOutline :messages="detail.messages" @jump="jumpToMessage" />
          </div>
        </aside>
      </div>
    </section>

    <div v-if="showOutline && detail" class="fixed inset-0 z-40 bg-black/40 p-4 2xl:hidden" @click.self="showOutline = false">
      <div class="ml-auto max-h-full w-full max-w-sm overflow-y-auto rounded-lg bg-muted p-4 shadow-xl">
        <div class="mb-3 flex items-center justify-between">
          <div class="text-sm font-bold">{{ t('sessions.outline') }}</div>
          <button class="rounded-md p-2 hover:bg-white" @click="showOutline = false"><X class="h-4 w-4" /></button>
        </div>
        <SessionOutline :messages="detail.messages" @jump="jumpToMessage" />
      </div>
    </div>

    <div v-if="cleanupHint" class="fixed inset-0 z-50 bg-black/40 p-4" @click.self="cleanupHint = null">
      <div class="mx-auto mt-20 max-w-2xl rounded-lg bg-white p-5 shadow-xl">
        <div class="mb-4 flex items-center justify-between gap-3">
          <div class="text-lg font-bold">{{ t('sessions.cleanup') }}</div>
          <button class="rounded-md p-2 hover:bg-muted" @click="cleanupHint = null"><X class="h-4 w-4" /></button>
        </div>
        <p class="mb-4 text-sm text-text-secondary">{{ cleanupHint.note || t('sessions.cleanup_note') }}</p>
        <CommandBlock :label="t('sessions.preview_command')" :command="cleanupHint.preview_command" />
        <CommandBlock :label="t('sessions.interactive_command')" :command="cleanupHint.interactive_command" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, ref } from 'vue'
import { Copy, Download, Folder, List, MessageSquare, RefreshCw, Terminal, X } from 'lucide-vue-next'
import {
  useApi,
  type SessionCleanupHint,
  type SessionDetailResponse,
  type SessionItem,
  type SessionProject,
} from '@/composables/useApi'
import { useI18n } from '@/composables/useI18n'
import SessionDetail from '@/components/SessionDetail.vue'
import SessionOutline from '@/components/SessionOutline.vue'
import { tokenizeCommand, type CommandTokenKind } from '@/utils/sessionCommands'

const api = useApi()
const { t, locale } = useI18n()

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
        h('div', { class: 'mb-1 text-xs font-bold uppercase tracking-widest text-text-secondary' }, props.label),
        h('div', { class: 'flex items-start gap-2 rounded-lg border border-slate-700 bg-slate-950 p-3 shadow-inner' }, [
          h(
            'code',
            { class: 'min-w-0 flex-1 whitespace-pre-wrap break-all font-mono text-sm leading-relaxed text-slate-100' },
            tokenizeCommand(props.command).map((token) => h('span', { class: commandTokenClass(token.kind) }, token.text))
          ),
          h(
            'button',
            { class: 'rounded-md bg-white/10 p-2 text-slate-100 hover:bg-white/20', onClick: copyCommand, title: copied.value ? t('sessions.copied') : t('sessions.copy') },
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
  detail.value = await api.getSessionDetail(session.id, session.source_path)
}

async function exportSelected() {
  if (!selectedSession.value) return
  try {
    const blob = await api.exportSessionHTML(selectedSession.value.id, selectedSession.value.source_path)
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

function commandTokenClass(kind: CommandTokenKind): string {
  const classes: Record<CommandTokenKind, string> = {
    command: 'text-emerald-300',
    keyword: 'text-sky-300',
    flag: 'text-amber-300',
    path: 'text-fuchsia-200',
    space: 'text-slate-300',
    text: 'text-slate-100',
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
