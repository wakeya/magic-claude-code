import test from 'node:test'
import assert from 'node:assert/strict'
import type { SessionItem, SessionProject } from './useApi.ts'
import { useLazySessionData } from './useLazySessionData.ts'

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

test('creating lazy session state does not issue a session request', async () => {
  let projectCalls = 0
  let listCalls = 0

  useLazySessionData({
    async getSessionProjects() {
      projectCalls += 1
      return []
    },
    async getSessionList() {
      listCalls += 1
      return { sessions: [], total: 0, page: 1, page_size: 100 }
    },
  }, () => 'load failed')

  assert.equal(projectCalls, 0)
  assert.equal(listCalls, 0)
})

test('concurrent session activations share one request and successful data is cached', async () => {
  const projectsRequest = deferred<SessionProject[]>()
  const listRequest = deferred<{
    sessions: SessionItem[]
    total: number
    page: number
    page_size: number
  }>()
  let projectCalls = 0
  let listCalls = 0
  const state = useLazySessionData({
    getSessionProjects() {
      projectCalls += 1
      return projectsRequest.promise
    },
    getSessionList() {
      listCalls += 1
      return listRequest.promise
    },
  }, () => 'load failed')

  const firstLoad = state.loadOnce()
  const concurrentLoad = state.loadOnce()
  assert.equal(projectCalls, 1)
  assert.equal(listCalls, 1)
  assert.equal(state.loading.value, true)

  projectsRequest.resolve([project('test')])
  listRequest.resolve({ sessions: [session('one')], total: 1, page: 1, page_size: 100 })
  await Promise.all([firstLoad, concurrentLoad])
  await state.loadOnce()

  assert.equal(projectCalls, 1)
  assert.equal(listCalls, 1)
  assert.deepEqual(state.projects.value, [project('test')])
  assert.deepEqual(state.sessions.value, [session('one')])
  assert.equal(state.loading.value, false)
  assert.equal(state.error.value, '')
})

test('a failed first session load retries on the next activation', async () => {
  let attempts = 0
  const state = useLazySessionData({
    async getSessionProjects() {
      attempts += 1
      if (attempts === 1) throw new Error('temporary failure')
      return [project('retry')]
    },
    async getSessionList() {
      return { sessions: [session('retry')], total: 1, page: 1, page_size: 100 }
    },
  }, () => 'load failed')

  await state.loadOnce()
  assert.equal(state.error.value, 'load failed')

  await state.loadOnce()
  assert.equal(attempts, 2)
  assert.deepEqual(state.projects.value, [project('retry')])
  assert.deepEqual(state.sessions.value, [session('retry')])
  assert.equal(state.error.value, '')
})

test('invalidating session state prevents an old authentication response from applying', async () => {
  const oldProjects = deferred<SessionProject[]>()
  const oldList = deferred<{
    sessions: SessionItem[]
    total: number
    page: number
    page_size: number
  }>()
  let generation = 0
  const state = useLazySessionData({
    getSessionProjects() {
      if (generation === 0) return oldProjects.promise
      return Promise.resolve([project('new-auth')])
    },
    getSessionList() {
      if (generation === 0) return oldList.promise
      return Promise.resolve({ sessions: [session('new-auth')], total: 1, page: 1, page_size: 100 })
    },
  }, () => 'load failed')

  const oldLoad = state.loadOnce()
  state.invalidate()
  generation = 1
  oldProjects.resolve([project('old-auth')])
  oldList.resolve({ sessions: [session('old-auth')], total: 1, page: 1, page_size: 100 })
  await oldLoad

  assert.deepEqual(state.projects.value, [])
  assert.deepEqual(state.sessions.value, [])
  await state.loadOnce()
  assert.deepEqual(state.projects.value, [project('new-auth')])
  assert.deepEqual(state.sessions.value, [session('new-auth')])
})

test('manual refresh data supersedes an in-flight initial session load', async () => {
  const initialProjects = deferred<SessionProject[]>()
  const initialList = deferred<{
    sessions: SessionItem[]
    total: number
    page: number
    page_size: number
  }>()
  const state = useLazySessionData({
    getSessionProjects: () => initialProjects.promise,
    getSessionList: () => initialList.promise,
  }, () => 'load failed')

  const initialLoad = state.loadOnce()
  state.applyRefreshed({
    projects: [project('manual')],
    sessions: [session('manual')],
  })
  initialProjects.resolve([project('initial')])
  initialList.resolve({ sessions: [session('initial')], total: 1, page: 1, page_size: 100 })
  await initialLoad

  assert.deepEqual(state.projects.value, [project('manual')])
  assert.deepEqual(state.sessions.value, [session('manual')])
  assert.equal(state.loading.value, false)
  assert.equal(state.error.value, '')
})
