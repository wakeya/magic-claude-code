import type {
  ProviderQuotaResult,
  ProviderUsageResponse,
  ProviderUsageUpdateRequest,
  PublicQuotaConfig,
  QuotaSnapshot,
} from '../composables/useApi.ts'

export interface QuotaSaveFlowDependencies {
  update(payload: ProviderUsageUpdateRequest): Promise<{
    success: boolean
    config: PublicQuotaConfig
  }>
  query(): Promise<{
    success: boolean
    result: ProviderQuotaResult
  }>
  reload(): Promise<ProviderUsageResponse>
}

export type QuotaSaveFlowOutcome =
  | {
      ok: true
      configSaved: true
      config: PublicQuotaConfig
      snapshot: QuotaSnapshot | null
    }
  | {
      ok: false
      configSaved: false
      error: string
    }
  | {
      ok: false
      configSaved: true
      config: PublicQuotaConfig
      error: string
    }

function errorToString(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export async function runQuotaSaveFlow(
  payload: ProviderUsageUpdateRequest,
  deps: QuotaSaveFlowDependencies,
): Promise<QuotaSaveFlowOutcome> {
  let config: PublicQuotaConfig
  try {
    const response = await deps.update(payload)
    config = response.config
  } catch (error: unknown) {
    return {
      ok: false,
      configSaved: false,
      error: errorToString(error),
    }
  }

  if (payload.enabled === false) {
    return {
      ok: true,
      configSaved: true,
      config,
      snapshot: null,
    }
  }

  try {
    const response = await deps.query()
    if (!response.success || !response.result.success) {
      return {
        ok: false,
        configSaved: true,
        config,
        error: response.result.error_message || 'Quota query failed',
      }
    }

    const reloaded = await deps.reload()
    if (!reloaded.snapshot) {
      return {
        ok: false,
        configSaved: true,
        config,
        error: 'Quota snapshot missing after query',
      }
    }

    return {
      ok: true,
      configSaved: true,
      config,
      snapshot: reloaded.snapshot,
    }
  } catch (error: unknown) {
    return {
      ok: false,
      configSaved: true,
      config,
      error: errorToString(error),
    }
  }
}
