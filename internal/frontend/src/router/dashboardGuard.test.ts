import test from 'node:test'
import assert from 'node:assert/strict'

async function loadModules() {
  try {
    const [guard, store] = await Promise.all([
      import('./dashboardGuard.ts'),
      import('../stores/dashboardInitialStatus.ts'),
    ])
    return { guard, store }
  } catch {
    assert.fail('dashboard guard or initial status store is missing')
  }
}

test('dashboard guard stages one successful timezone-aware status response', async () => {
  const { guard, store } = await loadModules()
  store.clearDashboardInitialStatus()
  const calls: string[] = []
  const status = { running: true, version: 'v-test' }
  const route = {
    path: '/',
    fullPath: '/?tab=status',
    meta: {} as Record<string, unknown>,
  }
  const request = async (input: string | URL | Request) => {
    calls.push(String(input))
    return new Response(JSON.stringify(status), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  const result = await guard.guardDashboardRoute(
    route,
    request,
    'America/Los_Angeles',
  )

  assert.equal(result, true)
  assert.deepEqual(calls, ['/api/status?tz=America%2FLos_Angeles'])
  const navigation = route.meta.dashboardInitialStatusNavigation
  assert.deepEqual(store.consumeDashboardInitialStatus(navigation), status)
  assert.equal(store.consumeDashboardInitialStatus(navigation), undefined)
})

test('cancelled older navigation cannot overwrite status staged by the current navigation', async () => {
  const { guard, store } = await loadModules()
  store.clearDashboardInitialStatus()

  const olderRoute = {
    path: '/',
    fullPath: '/?tab=status',
    meta: {} as Record<string, unknown>,
  }
  const currentRoute = {
    path: '/',
    fullPath: '/?tab=providers',
    meta: {} as Record<string, unknown>,
  }
  let resolveOlder!: (response: Response) => void
  let resolveCurrent!: (response: Response) => void
  const olderResponse = new Promise<Response>((resolve) => {
    resolveOlder = resolve
  })
  const currentResponse = new Promise<Response>((resolve) => {
    resolveCurrent = resolve
  })

  const olderNavigation = guard.guardDashboardRoute(
    olderRoute,
    async () => olderResponse,
    'UTC',
  )
  const currentNavigation = guard.guardDashboardRoute(
    currentRoute,
    async () => currentResponse,
    'UTC',
  )

  resolveCurrent(new Response(JSON.stringify({ running: true, version: 'current-navigation' })))
  assert.equal(await currentNavigation, true)
  resolveOlder(new Response(JSON.stringify({ running: true, version: 'cancelled-older-navigation' })))
  assert.equal(await olderNavigation, true)

  const consumeForNavigation = store.consumeDashboardInitialStatus
  assert.equal(
    consumeForNavigation(olderRoute.meta.dashboardInitialStatusNavigation),
    undefined,
  )
  assert.deepEqual(
    consumeForNavigation(currentRoute.meta.dashboardInitialStatusNavigation),
    { running: true, version: 'current-navigation' },
  )
})

test('dashboard guard preserves 401 redirect behavior without staging status', async () => {
  const { guard, store } = await loadModules()
  const staleNavigation = store.beginDashboardInitialStatusNavigation()
  store.stageDashboardInitialStatus(staleNavigation, { running: true, version: 'stale' })
  const route = {
    path: '/',
    fullPath: '/?tab=usage',
    meta: {} as Record<string, unknown>,
  }

  const result = await guard.guardDashboardRoute(
    route,
    async () => new Response(null, { status: 401 }),
    'UTC',
  )

  assert.deepEqual(result, {
    name: 'login',
    query: { redirect: '/?tab=usage' },
  })
  assert.equal(
    store.consumeDashboardInitialStatus(route.meta.dashboardInitialStatusNavigation),
    undefined,
  )
})

test('dashboard guard leaves non-401 and malformed success responses to dashboard fallback', async () => {
  const { guard, store } = await loadModules()
  const serverErrorRoute = {
    path: '/',
    fullPath: '/',
    meta: {} as Record<string, unknown>,
  }

  const serverError = await guard.guardDashboardRoute(
    serverErrorRoute,
    async () => new Response('failed', { status: 500 }),
    'UTC',
  )
  assert.equal(serverError, true)
  assert.equal(
    store.consumeDashboardInitialStatus(serverErrorRoute.meta.dashboardInitialStatusNavigation),
    undefined,
  )

  const malformedSuccessRoute = {
    path: '/',
    fullPath: '/',
    meta: {} as Record<string, unknown>,
  }
  const malformedSuccess = await guard.guardDashboardRoute(
    malformedSuccessRoute,
    async () => new Response('{', { status: 200 }),
    'UTC',
  )
  assert.equal(malformedSuccess, true)
  assert.equal(
    store.consumeDashboardInitialStatus(malformedSuccessRoute.meta.dashboardInitialStatusNavigation),
    undefined,
  )
})

test('dashboard guard preserves network failure redirect behavior', async () => {
  const { guard, store } = await loadModules()
  const route = {
    path: '/',
    fullPath: 'https://example.com',
    meta: {} as Record<string, unknown>,
  }

  const result = await guard.guardDashboardRoute(
    route,
    async () => {
      throw new Error('network down')
    },
    'UTC',
  )

  assert.deepEqual(result, {
    name: 'login',
    query: { redirect: '/' },
  })
  assert.equal(
    store.consumeDashboardInitialStatus(route.meta.dashboardInitialStatusNavigation),
    undefined,
  )
})

test('login navigation invalidates staged dashboard status without requesting again', async () => {
  const { guard, store } = await loadModules()
  const staleNavigation = store.beginDashboardInitialStatusNavigation()
  store.stageDashboardInitialStatus(staleNavigation, { running: true, version: 'stale' })
  const route = {
    path: '/login',
    fullPath: '/login',
    meta: {} as Record<string, unknown>,
  }
  let requested = false

  const result = await guard.guardDashboardRoute(
    route,
    async () => {
      requested = true
      return new Response(null, { status: 200 })
    },
    'UTC',
  )

  assert.equal(result, true)
  assert.equal(requested, false)
  assert.equal(
    store.consumeDashboardInitialStatus(route.meta.dashboardInitialStatusNavigation),
    undefined,
  )
})
