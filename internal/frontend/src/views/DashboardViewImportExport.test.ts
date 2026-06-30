import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { useApi, type Provider, type ProviderImportSummary } from '../composables/useApi.ts'

const here = dirname(fileURLToPath(import.meta.url))
const useApiSource = readFileSync(join(here, '..', 'composables', 'useApi.ts'), 'utf8')
const dashSource = readFileSync(join(here, 'DashboardView.vue'), 'utf8')

test('useApi exposes exportProviders method', () => {
  assert.match(useApiSource, /exportProviders/)
  assert.match(useApiSource, /\/api\/providers\/export/)
})

test('useApi exposes importProviders method', () => {
  assert.match(useApiSource, /importProviders/)
  assert.match(useApiSource, /\/api\/providers\/import/)
})

test('exportProviders sends selected IDs via POST', () => {
  // The method must POST a JSON body with an ids array
  const methodSection = useApiSource.match(/exportProviders[\s\S]*?\n  \}/)?.[0] || ''
  assert.match(methodSection, /ids/)
  assert.match(methodSection, /POST|method/)
})

test('importProviders sends providers and strategy via POST', () => {
  const methodSection = useApiSource.match(/importProviders[\s\S]*?\n  \}/)?.[0] || ''
  assert.match(methodSection, /strategy/)
  assert.match(methodSection, /providers/)
})

test('DashboardView has export and import buttons in providers toolbar', () => {
  // Buttons must appear in the providers tab toolbar area
  assert.match(dashSource, /providers\.export|exportButton|handleExport/i)
  assert.match(dashSource, /providers\.import|importButton|handleImport/i)
})

test('export button is disabled when no providers selected', () => {
  // The export button must have a :disabled binding tied to selection size
  assert.match(dashSource, /disabled.*selectedProviderIds|selectedProviderIds.*disabled|selectedProviderIds\.size/)
})

test('DashboardView has import preview modal logic', () => {
  // Preview classification: new vs conflict
  assert.match(dashSource, /conflict|preview/i)
})

test('DashboardView triggers file download on export', () => {
  // Blob + download link pattern
  assert.match(dashSource, /Blob|createObjectURL|download/)
})

test('import file parse validates version field', () => {
  // The import handler must reject files missing version or with version != 1
  // before showing the preview (client-side guard mirrors backend).
  assert.match(dashSource, /parsed\.version/)
})

test('export shows confirmation warning before downloading', () => {
  // A confirm() call must precede the export API call
  const exportSection = dashSource.match(/handleExport[\s\S]*?\n\}/)?.[0] || ''
  assert.match(exportSection, /confirm/)
})

test('useApi exportProviders checks res.ok', () => {
  const methodSection = useApiSource.match(/exportProviders[\s\S]*?\n  \}/)?.[0] || ''
  assert.match(methodSection, /res\.ok|!res\.ok|res\.status/)
})

test('useApi importProviders checks res.ok', () => {
  const methodSection = useApiSource.match(/importProviders[\s\S]*?\n  \}/)?.[0] || ''
  assert.match(methodSection, /res\.ok|!res\.ok|res\.status/)
})

test('importProviders preserves a structured summary returned with HTTP 500', async () => {
  const summary: ProviderImportSummary = {
    success: false,
    imported: 1,
    skipped: 2,
    overwritten: 3,
    duplicated: 4,
    errors: ['config saved but failed to clear quota snapshot'],
  }
  const originalFetch = globalThis.fetch
  globalThis.fetch = async () => new Response(JSON.stringify(summary), {
    status: 500,
    headers: { 'Content-Type': 'application/json' },
  })

  try {
    const result = await useApi().importProviders([] as Provider[], 'overwrite')
    assert.deepEqual(result, summary)
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('importProviders reports backend error and status for an unstructured HTTP error', async () => {
  const originalFetch = globalThis.fetch
  globalThis.fetch = async () => new Response(JSON.stringify({ error: 'database unavailable' }), {
    status: 503,
    headers: { 'Content-Type': 'application/json' },
  })

  try {
    await assert.rejects(
      useApi().importProviders([] as Provider[], 'overwrite'),
      /database unavailable.*503|503.*database unavailable/,
    )
  } finally {
    globalThis.fetch = originalFetch
  }
})

test('Dashboard refreshes persisted providers before reporting either import outcome', () => {
  const section = dashSource.match(/async function confirmImport\(\)[\s\S]*?\n\}/)?.[0] || ''
  assert.match(section, /const result = await api\.importProviders[\s\S]*?importPreview\.value = null[\s\S]*?await loadProviders\(\)[\s\S]*?if \(result\.success\)/)
  assert.match(section, /providers\.import_partial/)
  assert.match(section, /result\.errors\.join/)
  assert.doesNotMatch(section, /catch[\s\S]*?providers\.import_invalid/)
})
