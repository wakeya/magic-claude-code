import type {
  ProviderImportSummary,
  ProvidersResponse,
  QuotaSnapshot,
} from '@/composables/useApi'

interface ProviderImportFlowDependencies {
  importProviders: () => Promise<ProviderImportSummary>
  onImported: (summary: ProviderImportSummary) => void
  reloadProviders: () => Promise<ProvidersResponse>
  reloadSnapshots: () => Promise<{ snapshots: Record<string, QuotaSnapshot> }>
}

type ProviderImportCompletedKind = 'done' | 'with_errors' | 'partial'

export type ProviderImportFlowResult =
  | {
      kind: ProviderImportCompletedKind
      summary: ProviderImportSummary
      providers: ProvidersResponse
      snapshots: { snapshots: Record<string, QuotaSnapshot> }
    }
  | {
      kind: 'refresh_failed'
      summary: ProviderImportSummary
      error: string
    }

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export async function runProviderImportFlow(
  dependencies: ProviderImportFlowDependencies,
): Promise<ProviderImportFlowResult> {
  const summary = await dependencies.importProviders()
  dependencies.onImported(summary)

  const [providersResult, snapshotsResult] = await Promise.allSettled([
    dependencies.reloadProviders(),
    dependencies.reloadSnapshots(),
  ])
  if (providersResult.status === 'rejected' || snapshotsResult.status === 'rejected') {
    const errors = [providersResult, snapshotsResult]
      .filter((result): result is PromiseRejectedResult => result.status === 'rejected')
      .map((result) => errorMessage(result.reason))
    return { kind: 'refresh_failed', summary, error: errors.join('; ') }
  }

  const kind: ProviderImportCompletedKind = !summary.success
    ? 'partial'
    : summary.errors.length > 0
      ? 'with_errors'
      : 'done'
  return {
    kind,
    summary,
    providers: providersResult.value,
    snapshots: snapshotsResult.value,
  }
}
