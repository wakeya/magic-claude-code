import { ref } from 'vue'
import type {
  SessionItem,
  SessionListResponse,
  SessionProject,
} from './useApi.ts'

type SessionBrowserApi = {
  getSessionProjects: () => Promise<SessionProject[]>
  getSessionList: (params: {
    project: string
    page: number
    page_size: number
  }) => Promise<SessionListResponse>
}

export type SessionBrowserSeed = {
  projects: SessionProject[]
  sessions: SessionItem[]
  loading: boolean
  errorMessage?: string
}

// applied=false 表示该结果已被更新的请求取代，调用方不得回写父级缓存（emit refreshed）。
export type SessionRefreshResult = {
  applied: boolean
  projects: SessionProject[]
  sessions: SessionItem[]
}

// SessionBrowser 的列表状态机。每次 reload/selectProject 都会推进 generation，
// 只有最新一次请求（token === generation）的成功或错误才允许写入状态；旧请求的
// 成功或错误一律丢弃，避免快速切换项目/手动刷新时慢响应覆盖当前项目、loading/error。
export function useSessionBrowserData(
  api: SessionBrowserApi,
  loadFailedMessage: () => string,
  seed?: SessionBrowserSeed,
) {
  const projects = ref<SessionProject[]>([...(seed?.projects ?? [])])
  const sessions = ref<SessionItem[]>([...(seed?.sessions ?? [])])
  const loading = ref(seed?.loading ?? false)
  const error = ref(seed?.errorMessage ?? '')
  const selectedProject = ref('')
  let generation = 0

  function snapshot(applied: boolean): SessionRefreshResult {
    return { applied, projects: projects.value, sessions: sessions.value }
  }

  async function reload(): Promise<SessionRefreshResult> {
    const token = ++generation
    error.value = ''
    loading.value = true
    try {
      const projectList = await api.getSessionProjects()
      if (token !== generation) return snapshot(false)
      projects.value = projectList
      const list = await api.getSessionList({
        project: selectedProject.value,
        page: 1,
        page_size: 100,
      })
      if (token !== generation) return snapshot(false)
      sessions.value = list.sessions
      return snapshot(true)
    } catch {
      if (token !== generation) return snapshot(false)
      error.value = loadFailedMessage()
      return snapshot(false)
    } finally {
      if (token === generation) loading.value = false
    }
  }

  async function selectProject(path: string): Promise<SessionRefreshResult> {
    const token = ++generation
    selectedProject.value = path
    error.value = ''
    loading.value = true
    try {
      const list = await api.getSessionList({
        project: path,
        page: 1,
        page_size: 100,
      })
      if (token !== generation) return snapshot(false)
      sessions.value = list.sessions
      return snapshot(true)
    } catch {
      if (token !== generation) return snapshot(false)
      error.value = loadFailedMessage()
      return snapshot(false)
    } finally {
      if (token === generation) loading.value = false
    }
  }

  return {
    projects,
    sessions,
    loading,
    error,
    selectedProject,
    reload,
    selectProject,
  }
}
