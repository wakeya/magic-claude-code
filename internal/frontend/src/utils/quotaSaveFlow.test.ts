import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const moduleURL = new URL('./quotaSaveFlow.ts', import.meta.url)
const modulePath = fileURLToPath(moduleURL)

async function loadQuotaSaveFlow() {
  assert.equal(
    existsSync(modulePath),
    true,
    'quotaSaveFlow.ts must exist before it can be imported',
  )
  return import(moduleURL.href)
}

const config = {
  enabled: true,
  template_type: 'general',
  timeout_seconds: 10,
  auto_query_interval_minutes: 5,
  script_api_key_configured: false,
  zenmux_api_key_configured: false,
  access_token_configured: false,
  secret_access_key_configured: false,
}

const queryResult = {
  provider_id: 'provider-1',
  template_type: 'general',
  success: true,
  queried_at: '2026-06-29T12:00:00Z',
  duration_ms: 25,
}

const snapshot = {
  provider_id: 'provider-1',
  result: queryResult,
  queried_at: '2026-06-29T12:00:00Z',
  updated_at: '2026-06-29T12:00:01Z',
  has_last_success: true,
  is_stale: false,
}

test('enabled save updates, queries, and reloads the persisted snapshot in order', async () => {
  const { runQuotaSaveFlow } = await loadQuotaSaveFlow()
  const calls: string[] = []
  const payload = { enabled: true, template_type: 'general' }

  const outcome = await runQuotaSaveFlow(payload, {
    async update(received: typeof payload) {
      calls.push('update')
      assert.deepEqual(received, payload)
      return { success: true, config }
    },
    async query() {
      calls.push('query')
      return { success: true, result: queryResult }
    },
    async reload() {
      calls.push('reload')
      return { config, snapshot }
    },
  })

  assert.deepEqual(calls, ['update', 'query', 'reload'])
  assert.deepEqual(outcome, {
    ok: true,
    configSaved: true,
    config,
    snapshot,
  })
})

test('update failure stops before query and reports the config was not saved', async () => {
  const { runQuotaSaveFlow } = await loadQuotaSaveFlow()
  let queryCalled = false

  const outcome = await runQuotaSaveFlow({ enabled: true }, {
    async update() {
      throw new Error('update failed')
    },
    async query() {
      queryCalled = true
      return { success: true, result: queryResult }
    },
    async reload() {
      return { config, snapshot }
    },
  })

  assert.equal(queryCalled, false)
  assert.deepEqual(outcome, {
    ok: false,
    configSaved: false,
    error: 'update failed',
  })
})

test('resolved update failure skips query and reload', async () => {
  const { runQuotaSaveFlow } = await loadQuotaSaveFlow()
  const calls: string[] = []

  const outcome = await runQuotaSaveFlow({ enabled: true }, {
    async update() {
      calls.push('update')
      return { success: false, config }
    },
    async query() {
      calls.push('query')
      return { success: true, result: queryResult }
    },
    async reload() {
      calls.push('reload')
      return { config, snapshot }
    },
  })

  assert.deepEqual(calls, ['update'])
  assert.deepEqual(outcome, {
    ok: false,
    configSaved: false,
    error: 'Quota configuration save failed',
  })
})

test('query response or result failure keeps the saved config and does not reload', async () => {
  const { runQuotaSaveFlow } = await loadQuotaSaveFlow()
  const cases = [
    {
      name: 'response failure',
      response: { success: false, result: queryResult },
      error: 'Quota query failed',
    },
    {
      name: 'result failure',
      response: {
        success: true,
        result: { ...queryResult, success: false, error_message: 'quota unavailable' },
      },
      error: 'quota unavailable',
    },
  ]

  for (const current of cases) {
    let reloadCalled = false
    const outcome = await runQuotaSaveFlow({ enabled: true }, {
      async update() {
        return { success: true, config }
      },
      async query() {
        return current.response
      },
      async reload() {
        reloadCalled = true
        return { config, snapshot }
      },
    })

    assert.equal(reloadCalled, false, current.name)
    assert.deepEqual(outcome, {
      ok: false,
      configSaved: true,
      config,
      error: current.error,
    }, current.name)
  }
})

test('reload rejects a stale snapshot from an earlier query', async () => {
  const { runQuotaSaveFlow } = await loadQuotaSaveFlow()

  const outcome = await runQuotaSaveFlow({ enabled: true }, {
    async update() {
      return { success: true, config }
    },
    async query() {
      return { success: true, result: queryResult }
    },
    async reload() {
      return {
        config,
        snapshot: { ...snapshot, queried_at: '2026-06-29T11:00:00Z' },
      }
    },
  })

  assert.deepEqual(outcome, {
    ok: false,
    configSaved: true,
    config,
    error: 'Quota snapshot missing after query',
  })
})

test('reload rejects a snapshot for a different provider', async () => {
  const { runQuotaSaveFlow } = await loadQuotaSaveFlow()

  const outcome = await runQuotaSaveFlow({ enabled: true }, {
    async update() {
      return { success: true, config }
    },
    async query() {
      return { success: true, result: queryResult }
    },
    async reload() {
      return {
        config,
        snapshot: { ...snapshot, provider_id: 'provider-2' },
      }
    },
  })

  assert.deepEqual(outcome, {
    ok: false,
    configSaved: true,
    config,
    error: 'Quota snapshot missing after query',
  })
})

test('persisted disabled config skips query when payload omits enabled', async () => {
  const { runQuotaSaveFlow } = await loadQuotaSaveFlow()
  const calls: string[] = []

  const outcome = await runQuotaSaveFlow({}, {
    async update() {
      calls.push('update')
      return { success: true, config: { ...config, enabled: false } }
    },
    async query() {
      calls.push('query')
      return { success: true, result: queryResult }
    },
    async reload() {
      calls.push('reload')
      return { config, snapshot }
    },
  })

  assert.deepEqual(calls, ['update'])
  assert.deepEqual(outcome, {
    ok: true,
    configSaved: true,
    config: { ...config, enabled: false },
    snapshot: null,
  })
})
