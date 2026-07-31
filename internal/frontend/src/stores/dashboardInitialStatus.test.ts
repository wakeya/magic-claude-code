import test from 'node:test'
import assert from 'node:assert/strict'

async function loadStore() {
  try {
    return await import('./dashboardInitialStatus.ts')
  } catch {
    assert.fail('dashboard initial status store is missing')
  }
}

test('dashboard initial status is consumed by one dashboard mount only', async () => {
  const store = await loadStore()
  const status = { running: true, version: 'v-test' }

  store.stageDashboardInitialStatus(status)

  assert.equal(store.consumeDashboardInitialStatus(), status)
  assert.equal(store.consumeDashboardInitialStatus(), undefined)
})

test('dashboard initial status can be explicitly invalidated across login sessions', async () => {
  const store = await loadStore()
  store.stageDashboardInitialStatus({ running: true, version: 'stale' })

  store.clearDashboardInitialStatus()

  assert.equal(store.consumeDashboardInitialStatus(), undefined)
})
