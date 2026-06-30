import test from 'node:test'
import assert from 'node:assert/strict'
import type { ProvidersResponse, QuotaSnapshot } from '../composables/useApi.ts'

const flowModule = await import('./providerImportFlow.ts').catch(() => null)

function runner() {
  if (!flowModule) throw new Error('providerImportFlow module is missing')
  return flowModule.runProviderImportFlow
}

function summary(overrides: Partial<{
  success: boolean
  imported: number
  skipped: number
  overwritten: number
  duplicated: number
  errors: string[]
}> = {}) {
  return {
    success: true,
    imported: 1,
    skipped: 2,
    overwritten: 3,
    duplicated: 4,
    errors: [],
    ...overrides,
  }
}

test('import flow closes preview after summary then refreshes providers and snapshots', async () => {
  const calls: string[] = []
  const imported = summary()
  const providers = { providers: [{ id: 'provider-a' }], active_provider_id: 'provider-a' } as unknown as ProvidersResponse
  const snapshots = { snapshots: { 'provider-a': { provider_id: 'provider-a' } } } as unknown as { snapshots: Record<string, QuotaSnapshot> }

  const result = await runner()({
    importProviders: async () => {
      calls.push('import')
      return imported
    },
    onImported: () => calls.push('close-preview'),
    reloadProviders: async () => {
      calls.push('providers')
      return providers
    },
    reloadSnapshots: async () => {
      calls.push('snapshots')
      return snapshots
    },
  })

  assert.deepEqual(calls.slice(0, 2), ['import', 'close-preview'])
  assert.deepEqual(new Set(calls.slice(2)), new Set(['providers', 'snapshots']))
  assert.deepEqual(result, { kind: 'done', summary: imported, providers, snapshots })
})

test('import flow reports validation errors independently from success', async () => {
  const imported = summary({ success: true, errors: ['Bad Provider: api_url is required'] })
  const result = await runner()({
    importProviders: async () => imported,
    onImported: () => {},
    reloadProviders: async () => ({ providers: [], active_provider_id: '' }),
    reloadSnapshots: async () => ({ snapshots: {} }),
  })

  assert.equal(result.kind, 'with_errors')
  assert.deepEqual(result.summary, imported)
})

test('import flow reports snapshot cleanup partial failure', async () => {
  const imported = summary({ success: false, errors: ['provider-a: config saved but failed to clear quota snapshot'] })
  const result = await runner()({
    importProviders: async () => imported,
    onImported: () => {},
    reloadProviders: async () => ({ providers: [], active_provider_id: '' }),
    reloadSnapshots: async () => ({ snapshots: {} }),
  })

  assert.equal(result.kind, 'partial')
  assert.deepEqual(result.summary, imported)
})

for (const failedReload of ['providers', 'snapshots'] as const) {
  test(`import flow reports ${failedReload} refresh failure without a completed outcome`, async () => {
    const calls: string[] = []
    const imported = summary({ errors: ['Bad Provider: invalid'] })
    const result = await runner()({
      importProviders: async () => imported,
      onImported: () => calls.push('close-preview'),
      reloadProviders: async () => {
        calls.push('providers')
        if (failedReload === 'providers') throw new Error('providers unavailable')
        return { providers: [], active_provider_id: '' }
      },
      reloadSnapshots: async () => {
        calls.push('snapshots')
        if (failedReload === 'snapshots') throw new Error('snapshots unavailable')
        return { snapshots: {} }
      },
    })

    if (result.kind !== 'refresh_failed') assert.fail(`kind = ${result.kind}, want refresh_failed`)
    assert.deepEqual(result.summary, imported)
    assert.match(result.error, new RegExp(`${failedReload} unavailable`))
    assert.deepEqual(new Set(calls), new Set(['close-preview', 'providers', 'snapshots']))
    assert.equal('providers' in result, false)
    assert.equal('snapshots' in result, false)
  })
}

test('import flow leaves import transport errors to the caller and does not refresh', async () => {
  const calls: string[] = []
  await assert.rejects(
    runner()({
      importProviders: async () => { throw new Error('network offline') },
      onImported: () => calls.push('close-preview'),
      reloadProviders: async () => { calls.push('providers'); return { providers: [], active_provider_id: '' } },
      reloadSnapshots: async () => { calls.push('snapshots'); return { snapshots: {} } },
    }),
    /network offline/,
  )
  assert.deepEqual(calls, [])
})
