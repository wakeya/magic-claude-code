import test from 'node:test'
import assert from 'node:assert/strict'
import { useApi } from './useApi.ts'

test('reorderProviders issues PUT /api/providers/order with full provider_ids body', async () => {
  const calls: Array<{ input: string; init?: RequestInit }> = []
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ input: String(input), init })
    return new Response(JSON.stringify({ success: true, providers: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof fetch

  const api = useApi()
  await api.reorderProviders(['id-a', 'id-b', 'id-c'])

  assert.equal(calls[0].input, '/api/providers/order')
  assert.equal(calls[0].init?.method, 'PUT')
  assert.deepEqual(JSON.parse(String(calls[0].init?.body)), { provider_ids: ['id-a', 'id-b', 'id-c'] })
  const headers = calls[0].init?.headers as Record<string, string>
  assert.equal(headers['Content-Type'], 'application/json')
})

test('reorderProviders throws on non-2xx', async () => {
  globalThis.fetch = (async () => new Response('{"error":"conflict"}', { status: 409 })) as typeof fetch
  const api = useApi()
  await assert.rejects(() => api.reorderProviders(['a', 'b']), /reorder|order/i)
})
