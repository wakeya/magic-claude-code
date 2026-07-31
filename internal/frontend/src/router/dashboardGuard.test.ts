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
  const request = async (input: string | URL | Request) => {
    calls.push(String(input))
    return new Response(JSON.stringify(status), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  const result = await guard.guardDashboardRoute(
    { path: '/', fullPath: '/?tab=status' },
    request,
    'America/Los_Angeles',
  )

  assert.equal(result, true)
  assert.deepEqual(calls, ['/api/status?tz=America%2FLos_Angeles'])
  assert.deepEqual(store.consumeDashboardInitialStatus(), status)
  assert.equal(store.consumeDashboardInitialStatus(), undefined)
})

test('dashboard guard preserves 401 redirect behavior without staging status', async () => {
  const { guard, store } = await loadModules()
  store.stageDashboardInitialStatus({ running: true, version: 'stale' })

  const result = await guard.guardDashboardRoute(
    { path: '/', fullPath: '/?tab=usage' },
    async () => new Response(null, { status: 401 }),
    'UTC',
  )

  assert.deepEqual(result, {
    name: 'login',
    query: { redirect: '/?tab=usage' },
  })
  assert.equal(store.consumeDashboardInitialStatus(), undefined)
})

test('dashboard guard leaves non-401 and malformed success responses to dashboard fallback', async () => {
  const { guard, store } = await loadModules()

  const serverError = await guard.guardDashboardRoute(
    { path: '/', fullPath: '/' },
    async () => new Response('failed', { status: 500 }),
    'UTC',
  )
  assert.equal(serverError, true)
  assert.equal(store.consumeDashboardInitialStatus(), undefined)

  const malformedSuccess = await guard.guardDashboardRoute(
    { path: '/', fullPath: '/' },
    async () => new Response('{', { status: 200 }),
    'UTC',
  )
  assert.equal(malformedSuccess, true)
  assert.equal(store.consumeDashboardInitialStatus(), undefined)
})

test('dashboard guard preserves network failure redirect behavior', async () => {
  const { guard, store } = await loadModules()

  const result = await guard.guardDashboardRoute(
    { path: '/', fullPath: 'https://example.com' },
    async () => {
      throw new Error('network down')
    },
    'UTC',
  )

  assert.deepEqual(result, {
    name: 'login',
    query: { redirect: '/' },
  })
  assert.equal(store.consumeDashboardInitialStatus(), undefined)
})

test('login navigation invalidates staged dashboard status without requesting again', async () => {
  const { guard, store } = await loadModules()
  store.stageDashboardInitialStatus({ running: true, version: 'stale' })
  let requested = false

  const result = await guard.guardDashboardRoute(
    { path: '/login', fullPath: '/login' },
    async () => {
      requested = true
      return new Response(null, { status: 200 })
    },
    'UTC',
  )

  assert.equal(result, true)
  assert.equal(requested, false)
  assert.equal(store.consumeDashboardInitialStatus(), undefined)
})
