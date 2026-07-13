import test from 'node:test'
import assert from 'node:assert/strict'
import { useApi } from './useApi.ts'

// 用最小 fetch 替身捕获请求方法/URL/body，避免真实网络。
function mockFetch() {
  const calls: Array<{ input: string; init?: RequestInit }> = []
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ input: String(input), init })
    return new Response(JSON.stringify({ enabled: true, events: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof fetch
  return calls
}

test('getFailoverSettings issues GET /api/providers/failover', async () => {
  const calls = mockFetch()
  const api = useApi()
  const res = await api.getFailoverSettings()
  assert.equal(res.enabled, true)
  assert.equal(calls[0].input, '/api/providers/failover')
  assert.equal(calls[0].init?.method ?? 'GET', 'GET')
})

test('setFailoverSettings issues PUT with JSON body', async () => {
  const calls = mockFetch()
  const api = useApi()
  await api.setFailoverSettings(true)
  assert.equal(calls[0].input, '/api/providers/failover')
  assert.equal(calls[0].init?.method, 'PUT')
  assert.deepEqual(JSON.parse(String(calls[0].init?.body)), { enabled: true })
  const headers = calls[0].init?.headers as Record<string, string>
  assert.equal(headers['Content-Type'], 'application/json')
})

test('getFailoverEvents safely encodes and clamps limit', async () => {
  const calls = mockFetch()
  const api = useApi()

  // limit > 100 钳制到 100。
  await api.getFailoverEvents(500)
  assert.equal(calls[0].input, '/api/failover/events?limit=100')

  // limit <= 0 不带 query（默认 50 由后端处理）。
  await api.getFailoverEvents(0)
  assert.equal(calls[1].input, '/api/failover/events')

  // 负数同样不带 query。
  await api.getFailoverEvents(-3)
  assert.equal(calls[2].input, '/api/failover/events')

  // 正常值原样编码。
  await api.getFailoverEvents(25)
  assert.equal(calls[3].input, '/api/failover/events?limit=25')
})
