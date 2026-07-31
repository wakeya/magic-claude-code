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
  const navigation = store.beginDashboardInitialStatusNavigation()

  store.stageDashboardInitialStatus(navigation, status)

  assert.equal(store.consumeDashboardInitialStatus(navigation), status)
  assert.equal(store.consumeDashboardInitialStatus(navigation), undefined)
})

test('dashboard initial status can be explicitly invalidated across login sessions', async () => {
  const store = await loadStore()
  const navigation = store.beginDashboardInitialStatusNavigation()
  store.stageDashboardInitialStatus(navigation, { running: true, version: 'stale' })

  store.clearDashboardInitialStatus()

  assert.equal(store.consumeDashboardInitialStatus(navigation), undefined)
})
