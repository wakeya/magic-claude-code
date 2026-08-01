import test from 'node:test'
import assert from 'node:assert/strict'
import type { SessionItem, SessionListResponse, SessionProject } from './useApi.ts'
import { useSessionBrowserData } from './useSessionBrowserData.ts'

type Deferred<T> = {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (reason?: unknown) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((nextResolve, nextReject) => {
    resolve = nextResolve
    reject = nextReject
  })
  return { promise, resolve, reject }
}

const project = (name: string): SessionProject => ({
  name,
  path: `/projects/${name}`,
  session_count: 1,
  last_active_at: '2026-07-30T00:00:00Z',
})

const session = (id: string): SessionItem => ({
  id,
  title: id,
  project_path: '/projects/test',
  source_path: `/sessions/${id}.jsonl`,
  created_at: '2026-07-30T00:00:00Z',
  last_active_at: '2026-07-30T00:00:00Z',
  message_count: 1,
})

const page = (sessions: SessionItem[]): SessionListResponse => ({
  sessions,
  total: sessions.length,
  page: 1,
  page_size: 100,
})

test('seeding browser state reflects preloaded dashboard props', () => {
  const state = useSessionBrowserData(
    {
      async getSessionProjects() {
        return []
      },
      async getSessionList() {
        return page([])
      },
    },
    () => 'load failed',
    {
      projects: [project('seed')],
      sessions: [session('seed')],
      loading: true,
      errorMessage: 'boom',
    },
  )

  assert.deepEqual(state.projects.value, [project('seed')])
  assert.deepEqual(state.sessions.value, [session('seed')])
  assert.equal(state.loading.value, true)
  assert.equal(state.error.value, 'boom')
  assert.equal(state.selectedProject.value, '')
})

test('a slow project switch must not overwrite a faster later selection', async () => {
  const slow = deferred<SessionListResponse>()
  const fast = deferred<SessionListResponse>()
  const requests: Array<Deferred<SessionListResponse>> = []
  const state = useSessionBrowserData(
    {
      async getSessionProjects() {
        return []
      },
      getSessionList(params) {
        const request = params.project === '/projects/a' ? slow : fast
        requests.push(request)
        return request.promise
      },
    },
    () => 'load failed',
  )

  // A is slow, the user quickly switches to B which resolves first.
  const aLoad = state.selectProject('/projects/a')
  assert.equal(state.selectedProject.value, '/projects/a')
  assert.equal(state.loading.value, true)

  const bLoad = state.selectProject('/projects/b')
  assert.equal(state.selectedProject.value, '/projects/b')
  assert.equal(state.loading.value, true)

  fast.resolve(page([session('b')]))
  const bResult = await bLoad
  assert.equal(bResult.applied, true)
  assert.deepEqual(state.sessions.value, [session('b')])
  assert.equal(state.loading.value, false)
  assert.equal(state.error.value, '')

  // The stale A response arrives later and must be discarded entirely.
  slow.resolve(page([session('a')]))
  const aResult = await aLoad
  assert.equal(aResult.applied, false)
  assert.deepEqual(state.sessions.value, [session('b')])
  assert.equal(state.selectedProject.value, '/projects/b')
  assert.equal(state.loading.value, false)
  assert.equal(state.error.value, '')
  // The stale result must not hand the caller data to write back to the parent cache.
  assert.deepEqual(aResult.sessions, [session('b')])
})

test('a stale project-switch error must not clobber a current success', async () => {
  const slowFail = deferred<SessionListResponse>()
  const fast = deferred<SessionListResponse>()
  const state = useSessionBrowserData(
    {
      async getSessionProjects() {
        return []
      },
      getSessionList(params) {
        return params.project === '/projects/a' ? slowFail.promise : fast.promise
      },
    },
    () => 'load failed',
  )

  const aLoad = state.selectProject('/projects/a')
  const bLoad = state.selectProject('/projects/b')

  fast.resolve(page([session('b')]))
  await bLoad
  assert.equal(state.error.value, '')

  slowFail.reject(new Error('network'))
  const aResult = await aLoad
  assert.equal(aResult.applied, false)
  assert.equal(state.error.value, '')
  assert.deepEqual(state.sessions.value, [session('b')])
  assert.equal(state.loading.value, false)
})

test('a stale project-switch success must not clobber a current error', async () => {
  const slow = deferred<SessionListResponse>()
  const fastFail = deferred<SessionListResponse>()
  const state = useSessionBrowserData(
    {
      async getSessionProjects() {
        return []
      },
      getSessionList(params) {
        return params.project === '/projects/a' ? slow.promise : fastFail.promise
      },
    },
    () => 'load failed',
  )

  const aLoad = state.selectProject('/projects/a')
  const bLoad = state.selectProject('/projects/b')

  fastFail.reject(new Error('network'))
  const bResult = await bLoad
  assert.equal(bResult.applied, false)
  assert.equal(state.error.value, 'load failed')
  assert.equal(state.loading.value, false)

  slow.resolve(page([session('a')]))
  const aResult = await aLoad
  assert.equal(aResult.applied, false)
  // The current error and empty list must survive the stale success.
  assert.equal(state.error.value, 'load failed')
  assert.deepEqual(state.sessions.value, [])
  assert.equal(state.selectedProject.value, '/projects/b')
  assert.equal(state.loading.value, false)
})

test('manual refresh supersedes an in-flight project switch and vice versa', async () => {
  const switchRequest = deferred<SessionListResponse>()
  const refreshProjects = deferred<SessionProject[]>()
  const refreshList = deferred<SessionListResponse>()
  let listCalls = 0
  const state = useSessionBrowserData(
    {
      getSessionProjects() {
        return refreshProjects.promise
      },
      getSessionList() {
        listCalls += 1
        // First list call belongs to the project switch, second to the refresh.
        return listCalls === 1 ? switchRequest.promise : refreshList.promise
      },
    },
    () => 'load failed',
  )

  const switchLoad = state.selectProject('/projects/a')
  const refreshLoad = state.reload()

  // Refresh resolves first; the slower project switch must be discarded.
  refreshProjects.resolve([project('refreshed')])
  refreshList.resolve(page([session('refreshed')]))
  const refreshResult = await refreshLoad
  assert.equal(refreshResult.applied, true)
  assert.deepEqual(state.projects.value, [project('refreshed')])
  assert.deepEqual(state.sessions.value, [session('refreshed')])
  assert.equal(state.loading.value, false)

  switchRequest.resolve(page([session('stale-switch')]))
  const switchResult = await switchLoad
  assert.equal(switchResult.applied, false)
  assert.deepEqual(state.sessions.value, [session('refreshed')])
  assert.deepEqual(state.projects.value, [project('refreshed')])
  assert.equal(state.loading.value, false)
  assert.equal(state.error.value, '')
})

test('a stale manual refresh must not overwrite a later project switch', async () => {
  const refreshProjects = deferred<SessionProject[]>()
  const refreshList = deferred<SessionListResponse>()
  const switchRequest = deferred<SessionListResponse>()
  let projectCalls = 0
  let listCalls = 0
  const state = useSessionBrowserData(
    {
      getSessionProjects() {
        projectCalls += 1
        return refreshProjects.promise
      },
      getSessionList(params) {
        listCalls += 1
        // The refresh issues a list call for the current (empty) project first,
        // then the project switch issues its own call.
        if (params.project === '') return refreshList.promise
        return switchRequest.promise
      },
    },
    () => 'load failed',
  )

  const refreshLoad = state.reload()
  const switchLoad = state.selectProject('/projects/b')

  switchRequest.resolve(page([session('b')]))
  const switchResult = await switchLoad
  assert.equal(switchResult.applied, true)
  assert.deepEqual(state.sessions.value, [session('b')])

  // The slow refresh finally resolves and must be ignored, including its
  // projects payload, so nothing stale is handed back for parent write-back.
  refreshProjects.resolve([project('stale')])
  refreshList.resolve(page([session('stale')]))
  const refreshResult = await refreshLoad
  assert.equal(refreshResult.applied, false)
  assert.deepEqual(state.sessions.value, [session('b')])
  assert.equal(state.selectedProject.value, '/projects/b')
  assert.equal(state.loading.value, false)
  assert.equal(state.error.value, '')
})

test('a stale reload error must not clobber a current project-switch success', async () => {
  const refreshProjects = deferred<SessionProject[]>()
  const switchRequest = deferred<SessionListResponse>()
  const state = useSessionBrowserData(
    {
      getSessionProjects() {
        return refreshProjects.promise
      },
      getSessionList() {
        return switchRequest.promise
      },
    },
    () => 'load failed',
  )

  const refreshLoad = state.reload()
  const switchLoad = state.selectProject('/projects/b')

  switchRequest.resolve(page([session('b')]))
  await switchLoad
  assert.equal(state.error.value, '')

  refreshProjects.reject(new Error('network'))
  const refreshResult = await refreshLoad
  assert.equal(refreshResult.applied, false)
  assert.equal(state.error.value, '')
  assert.deepEqual(state.sessions.value, [session('b')])
})

test('re-entering the same project and retrying after failure still apply data', async () => {
  let attempts = 0
  const state = useSessionBrowserData(
    {
      async getSessionProjects() {
        return []
      },
      async getSessionList() {
        attempts += 1
        if (attempts === 1) throw new Error('temporary failure')
        return page([session('retry')])
      },
    },
    () => 'load failed',
  )

  const first = await state.selectProject('/projects/a')
  assert.equal(first.applied, false)
  assert.equal(state.error.value, 'load failed')
  assert.equal(state.loading.value, false)

  const second = await state.selectProject('/projects/a')
  assert.equal(second.applied, true)
  assert.equal(attempts, 2)
  assert.deepEqual(state.sessions.value, [session('retry')])
  assert.equal(state.error.value, '')
  assert.equal(state.loading.value, false)
})

test('a successful reload reports applied data for parent cache write-back', async () => {
  const state = useSessionBrowserData(
    {
      async getSessionProjects() {
        return [project('fresh')]
      },
      async getSessionList() {
        return page([session('fresh')])
      },
    },
    () => 'load failed',
  )

  const result = await state.reload()
  assert.equal(result.applied, true)
  assert.deepEqual(result.projects, [project('fresh')])
  assert.deepEqual(result.sessions, [session('fresh')])
  assert.deepEqual(state.projects.value, [project('fresh')])
  assert.deepEqual(state.sessions.value, [session('fresh')])
  assert.equal(state.loading.value, false)
  assert.equal(state.error.value, '')
})

test('a failed reload reports not applied and surfaces the localized error', async () => {
  const state = useSessionBrowserData(
    {
      async getSessionProjects() {
        throw new Error('network')
      },
      async getSessionList() {
        return page([])
      },
    },
    () => 'load failed',
  )

  const result = await state.reload()
  assert.equal(result.applied, false)
  assert.equal(state.error.value, 'load failed')
  assert.equal(state.loading.value, false)
})
