import test from 'node:test'
import assert from 'node:assert/strict'
import { createRouter, createMemoryHistory } from 'vue-router'
import { guardDashboardRoute } from './dashboardGuard.ts'
import {
  clearDashboardInitialStatus,
} from '../stores/dashboardInitialStatus.ts'
import {
  clearPendingUsageProvider,
  consumePendingUsageProvider,
} from '../stores/pendingUsageProvider.ts'
import { resolveLegacyUsageRedirect } from './legacyUsageRedirect.ts'

type StatusRequest = (input: string) => Promise<Response>

// Builds the production router shape (login, legacy usage redirect, dashboard,
// catch-all) wired to the REAL status guard, using dummy components so the
// legacy /providers/:id/usage flow can be exercised end-to-end in node:test.
function buildRouter(request: StatusRequest) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', name: 'login', component: { template: '<div/>' } },
      {
        path: '/providers/:providerId/usage',
        name: 'provider-usage',
        redirect: resolveLegacyUsageRedirect,
      },
      { path: '/', name: 'dashboard', component: { template: '<div/>' } },
      { path: '/:pathMatch(.*)*', redirect: '/' },
    ],
  })
  router.beforeEach((to) =>
    guardDashboardRoute(
      to as unknown as Parameters<typeof guardDashboardRoute>[0],
      request,
      'UTC',
    ),
  )
  return router
}

function jsonStatus(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

test('first open of the legacy usage URL canonicalizes to the dashboard and requests /api/status exactly once', async () => {
  clearDashboardInitialStatus()
  clearPendingUsageProvider()
  const calls: string[] = []
  const request: StatusRequest = async (input) => {
    calls.push(input)
    return jsonStatus({ running: true, version: 'v-test' })
  }

  const router = buildRouter(request)
  await router.push('/providers/abc/usage')
  const current = router.currentRoute.value

  // Final query/tab semantics: clean providers tab, no leftover usage_provider.
  assert.equal(current.name, 'dashboard')
  assert.equal(current.path, '/')
  assert.equal(current.query.tab, 'providers')
  assert.equal(current.query.usage_provider, undefined)

  // Modal semantics: the provider id is delivered to DashboardView once.
  assert.equal(consumePendingUsageProvider(), 'abc')
  assert.equal(consumePendingUsageProvider(), undefined)

  // The single status request happened on the canonical route only.
  assert.deepEqual(calls, ['/api/status?tz=UTC'])
})

test('legacy usage URL preserves the authenticated login redirect without a second status request', async () => {
  clearDashboardInitialStatus()
  clearPendingUsageProvider()
  const calls: string[] = []
  const request: StatusRequest = async (input) => {
    calls.push(input)
    return new Response(null, { status: 401 })
  }

  const router = buildRouter(request)
  await router.push('/providers/abc/usage')
  const current = router.currentRoute.value

  // Unauthenticated open redirects to login with a safe, canonical destination.
  assert.equal(current.name, 'login')
  const redirect = current.query.redirect
  assert.equal(Array.isArray(redirect) ? redirect[0] : redirect, '/?tab=providers')

  // Only one status probe ran (on the canonical route), and the intended
  // provider is staged so the modal can open after a successful login.
  assert.deepEqual(calls, ['/api/status?tz=UTC'])
  assert.equal(consumePendingUsageProvider(), 'abc')
})

test('legacy usage URL keeps the hash and extra query for history link compatibility', async () => {
  clearDashboardInitialStatus()
  clearPendingUsageProvider()
  const request: StatusRequest = async () =>
    jsonStatus({ running: true, version: 'v-test' })

  const router = buildRouter(request)
  await router.push('/providers/abc/usage?from=bookmarks#quota')
  const current = router.currentRoute.value

  assert.equal(current.path, '/')
  assert.equal(current.query.tab, 'providers')
  assert.equal(current.query.from, 'bookmarks')
  assert.equal(current.hash, '#quota')
  assert.equal(consumePendingUsageProvider(), 'abc')
})

test('direct dashboard entry still performs a single status request', async () => {
  clearDashboardInitialStatus()
  clearPendingUsageProvider()
  const calls: string[] = []
  const request: StatusRequest = async (input) => {
    calls.push(input)
    return jsonStatus({ running: true, version: 'v-test' })
  }

  const router = buildRouter(request)
  await router.push('/')
  assert.equal(router.currentRoute.value.name, 'dashboard')
  assert.deepEqual(calls, ['/api/status?tz=UTC'])
  // No legacy provider is staged for a plain dashboard entry.
  assert.equal(consumePendingUsageProvider(), undefined)
})
