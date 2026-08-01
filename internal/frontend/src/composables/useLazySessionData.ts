import { ref } from 'vue'
import type {
  SessionItem,
  SessionListResponse,
  SessionProject,
} from './useApi.ts'

type SessionDataApi = {
  getSessionProjects: () => Promise<SessionProject[]>
  getSessionList: (params: {
    project: string
    page: number
    page_size: number
  }) => Promise<SessionListResponse>
}

type RefreshedSessionData = {
  projects: SessionProject[]
  sessions: SessionItem[]
}

export function useLazySessionData(
  api: SessionDataApi,
  loadFailedMessage: () => string,
) {
  const projects = ref<SessionProject[]>([])
  const sessions = ref<SessionItem[]>([])
  const loading = ref(false)
  const error = ref('')
  let loaded = false
  let loadRequest: Promise<void> | undefined
  let generation = 0

  async function loadOnce(): Promise<void> {
    if (loaded) return
    if (loadRequest) return loadRequest

    const loadGeneration = ++generation
    loading.value = true
    error.value = ''
    const nextProjects = api.getSessionProjects()
    const nextPage = api.getSessionList({
      project: '',
      page: 1,
      page_size: 100,
    })
    const request = Promise.all([nextProjects, nextPage])
      .then(([projectList, page]) => {
        if (loadGeneration !== generation) return
        projects.value = projectList
        sessions.value = page.sessions
        loaded = true
      })
      .catch(() => {
        if (loadGeneration !== generation) return
        error.value = loadFailedMessage()
      })
      .finally(() => {
        if (loadGeneration === generation) loading.value = false
        if (loadRequest === request) loadRequest = undefined
      })

    loadRequest = request
    return request
  }

  function applyRefreshed(data: RefreshedSessionData) {
    generation += 1
    loadRequest = undefined
    loaded = true
    projects.value = data.projects
    sessions.value = data.sessions
    loading.value = false
    error.value = ''
  }

  function invalidate() {
    generation += 1
    loadRequest = undefined
    loaded = false
    projects.value = []
    sessions.value = []
    loading.value = false
    error.value = ''
  }

  return {
    projects,
    sessions,
    loading,
    error,
    loadOnce,
    applyRefreshed,
    invalidate,
  }
}
