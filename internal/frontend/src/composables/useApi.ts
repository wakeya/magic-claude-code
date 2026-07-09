import type { ThemeMode } from './useTheme'

export type { ThemeMode } from './useTheme'

export interface StatusInfo {
  running: boolean
  version?: string
  backend_url: string
  proxy_listen_addr?: string
  proxy_port?: number
  admin_listen_addr?: string
  admin_port?: number
  gateway_listen_addr?: string
  gateway_listen_port?: number
  configured_mode?: string
  effective_mode?: string
  mode_rationale?: string
  uptime: string
  requests_total: number
  last_request: string | null
  service_requests_total?: number
  provider_requests_total?: number
  today_provider_requests?: number
  today_token_consumption?: number
  usage_coverage?: number
  last_provider_request?: string | null
}

export interface ExposedModel {
  id: string
  label: string
  description: string
  backend_model: string
}

export interface Provider {
  id: string
  name: string
  api_url: string
  api_token_mask: string
  api_format: 'anthropic' | 'openai_chat' | 'openai_responses'
  openai_extra_params?: Record<string, unknown>
  claude_code_compat_hint: boolean
  model_mappings: Record<string, string>
  exposed_models?: ExposedModel[]
  supports_thinking: boolean
  multimodal_switch: boolean
  multimodal_model: string
  strip_unknown_content_blocks: boolean
  rate_limit_queue_enabled: boolean
  max_concurrent_requests: number
  max_queue_size: number
  queue_timeout_ms: number
  retry_429_enabled: boolean
  retry_429_max_attempts: number
  retry_429_initial_delay_ms: number
  retry_429_max_delay_ms: number
  enabled: boolean
  active: boolean
  quota_query?: PublicQuotaConfig
  created_at: string
  updated_at: string
}

export interface ProvidersResponse {
  providers: Provider[]
  active_provider_id: string
}

export interface ProviderImportSummary {
  success: boolean
  imported: number
  skipped: number
  overwritten: number
  duplicated: number
  errors: string[]
}

function isProviderImportSummary(value: unknown): value is ProviderImportSummary {
  if (!value || typeof value !== 'object') return false
  const summary = value as Record<string, unknown>
  const validCount = (count: unknown) => typeof count === 'number' && Number.isInteger(count) && count >= 0
  return typeof summary.success === 'boolean'
    && validCount(summary.imported)
    && validCount(summary.skipped)
    && validCount(summary.overwritten)
    && validCount(summary.duplicated)
    && Array.isArray(summary.errors)
    && summary.errors.every((error) => typeof error === 'string')
}

export interface CertificateInfo {
  ca_cert_path: string
  server_cert_path: string
  ca_expires_at: string
  server_expires_at: string
}

export interface TestResult {
  success: boolean
  status_code?: number
  error?: string
}

export interface UsageSummary {
  service_requests_total: number
  provider_requests_total: number
  today_provider_requests: number
  token_consumption_total: number
  today_token_consumption: number
  failed_requests: number
  usage_coverage: number
  last_provider_request: string | null
}

export interface UsageTrendPoint {
  bucket: string
  input_tokens: number
  output_tokens: number
  cache_creation_input_tokens: number
  cache_read_input_tokens: number
  token_consumption_total: number
  provider_requests_total: number
  failed_requests: number
  usage_coverage: number
}

export interface UsageRequestRow {
  id: string
  request_id: string
  started_at: string
  ended_at: string | null
  duration_ms: number | null
  upstream_response_header_ms: number | null
  time_to_first_byte_ms: number | null
  status_code: number | null
  error_type: string
  error_message: string
  method: string
  request_path: string
  backend_url: string
  provider_id: string
  provider_name: string
  provider_api_url: string
  source_app: string
  source_entrypoint: string
  user_agent: string
  original_model: string
  mapped_model: string
  stream: boolean
  request_bytes: number
  response_bytes: number
  input_tokens: number
  output_tokens: number
  cache_creation_input_tokens: number
  cache_read_input_tokens: number
  usage_source: 'provider' | 'session_log' | 'none'
  usage_parse_status: string
  usage_parse_error: string
  dedupe_status?: 'duplicate' | ''
  dedupe_request_id?: string
}

export interface UsageRequestPage {
  rows: UsageRequestRow[]
  total: number
  page: number
  page_size: number
}

export interface UsageAggregateRow {
  name: string
  provider_id?: string
  provider_name?: string
  mapped_model?: string
  total_requests: number
  failed_requests: number
  token_consumption_total: number
  usage_coverage: number
  average_duration_ms: number
}

export interface UsageCoverageRow {
  provider_name: string
  provider_api_url: string
  mapped_model: string
  source_entrypoint: string
  total_requests: number
  success_requests: number
  error_requests: number
  with_usage_requests: number
  without_usage_requests: number
  usage_coverage: number
  top_usage_parse_status: string
  last_seen_at: string
}

export interface UsageClearResult {
  success: boolean
  cleared_requests: number
  cleared_tokens: number
  reset_session_sync: boolean
}

export type UsageParams = Record<string, string | number | boolean | null | undefined>

export interface PreferencesResponse {
  theme_mode: ThemeMode
  success?: boolean
}

export interface ConfigResponse {
  backend_url: string
  connection_mode: string
  gateway_listen_addr?: string
  gateway_listen_port?: number
  gateway_restarted?: boolean
  gateway_restart_failed?: string
  success?: boolean
}

export interface SessionProject {
  path: string
  name: string
  session_count: number
  last_active_at: string
}

export interface SessionItem {
  id: string
  title: string
  project_path: string
  source_path: string
  created_at: string
  last_active_at: string
  message_count: number
}

export interface SessionMessage {
  role: 'system' | 'user' | 'assistant' | 'tool'
  content: string
  ts?: number
}

export interface SessionDetailResponse {
  session: SessionItem
  messages: SessionMessage[]
  message_count: number
}

export interface SessionListResponse {
  sessions: SessionItem[]
  total: number
  page: number
  page_size: number
}

export interface SessionCleanupHint {
  project_path: string
  preview_command: string
  interactive_command: string
  windows_preview_command: string
  windows_interactive_command: string
}

export interface UpdateCheckResult {
  current_version: string
  latest_version: string
  update_available: boolean
  source?: string
  release_url?: string
  error?: string
}

export interface UpdateApplyResult {
  success: boolean
  new_version?: string
  message?: string
  restarting?: boolean
  error?: string
}

// Provider Quota types

export interface PublicQuotaConfig {
  enabled: boolean
  template_type: string
  timeout_seconds: number
  auto_query_interval_minutes: number
  script?: string
  base_url?: string
  script_api_key_configured: boolean
  zenmux_base_url?: string
  zenmux_api_key_configured: boolean
  access_token_configured: boolean
  user_id?: string
  coding_plan_provider?: string
  access_key_id?: string
  secret_access_key_configured: boolean
}

export interface QuotaTier {
  name: string
  label?: string
  utilization: number
  resets_at?: string
  used?: number
  total?: number
  remaining?: number
  unit?: string
}

export interface BalanceItem {
  plan_name?: string
  remaining?: number
  used?: number
  total?: number
  unit?: string
  is_valid?: boolean
  invalid_message?: string
  extra?: string
}

export interface ProviderQuotaResult {
  provider_id: string
  template_type: string
  success: boolean
  credential_status?: string
  tiers?: QuotaTier[]
  balances?: BalanceItem[]
  error_code?: string
  error_message?: string
  queried_at: string
  duration_ms: number
}

export interface QuotaSnapshot {
  provider_id: string
  result?: ProviderQuotaResult
  last_success?: ProviderQuotaResult
  queried_at: string
  updated_at: string
  has_last_success: boolean
  is_stale: boolean
}

export interface ProviderUsageResponse {
  config: PublicQuotaConfig
  snapshot?: QuotaSnapshot
  detected_token_plan?: string
  detected_balance?: string
  is_mimo?: boolean
}

export interface ProviderUsageUpdateRequest {
  enabled?: boolean
  template_type?: string
  timeout_seconds?: number
  auto_query_interval_minutes?: number
  script?: string
  base_url?: string
  script_api_key?: string
  zenmux_base_url?: string
  zenmux_api_key?: string
  access_token?: string
  user_id?: string
  coding_plan_provider?: string
  access_key_id?: string
  secret_access_key?: string
  clear_script_api_key?: boolean
  clear_zenmux_api_key?: boolean
  clear_access_token?: boolean
  clear_secret_access_key?: boolean
}

export function useApi() {
  function buildQuery(params?: UsageParams): string {
    const search = new URLSearchParams()
    for (const [key, value] of Object.entries(params || {})) {
      if (value !== undefined && value !== null && value !== '' && value !== 'all') {
        search.set(key, String(value))
      }
    }
    const query = search.toString()
    return query ? `?${query}` : ''
  }

  async function login(password: string): Promise<boolean> {
    const res = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password }),
    })
    return res.ok
  }

  async function logout(): Promise<void> {
    await fetch('/api/logout', { method: 'POST' })
  }

  async function getPreferences(): Promise<PreferencesResponse> {
    const res = await fetch('/api/preferences')
    if (!res.ok) throw new Error('Failed to fetch preferences')
    return res.json()
  }

  async function updatePreferences(themeMode: ThemeMode): Promise<PreferencesResponse> {
    const res = await fetch('/api/preferences', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ theme_mode: themeMode }),
    })
    if (!res.ok) throw new Error('Failed to update preferences')
    return res.json()
  }

  async function getConfig(): Promise<ConfigResponse> {
    const res = await fetch('/api/config')
    if (!res.ok) throw new Error('Failed to fetch config')
    return res.json()
  }

  async function updateConfig(data: {
    backend_url?: string
    connection_mode?: 'transparent' | 'tunnel' | 'gateway'
    gateway_listen_addr?: string
    gateway_listen_port?: number
  }): Promise<ConfigResponse> {
    const res = await fetch('/api/config', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: 'request failed' }))
      throw new Error(err.error || `HTTP ${res.status}`)
    }
    return res.json()
  }

  async function getStatus(tz?: string): Promise<StatusInfo> {
    const query = tz ? `?tz=${encodeURIComponent(tz)}` : ''
    const res = await fetch(`/api/status${query}`)
    if (!res.ok) throw new Error('Failed to fetch status')
    return res.json()
  }

  async function getProviders(): Promise<ProvidersResponse> {
    const res = await fetch('/api/providers')
    if (!res.ok) throw new Error('Failed to fetch providers')
    return res.json()
  }

  async function createProvider(data: {
    name: string
    api_url: string
    api_token: string
    api_format?: 'anthropic' | 'openai_chat' | 'openai_responses'
    openai_extra_params?: Record<string, unknown>
    claude_code_compat_hint?: boolean
    model_mappings: Record<string, string>
    exposed_models?: ExposedModel[]
    supports_thinking?: boolean
    multimodal_switch?: boolean
    multimodal_model?: string
    strip_unknown_content_blocks?: boolean
    rate_limit_queue_enabled?: boolean
    max_concurrent_requests?: number
    max_queue_size?: number
    queue_timeout_ms?: number
    retry_429_enabled?: boolean
    retry_429_max_attempts?: number
    retry_429_initial_delay_ms?: number
    retry_429_max_delay_ms?: number
  }): Promise<{ success: boolean; provider: Provider }> {
    const res = await fetch('/api/providers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: 'request failed' }))
      throw new Error(err.error || `HTTP ${res.status}`)
    }
    return res.json()
  }

  async function updateProvider(
    id: string,
    data: {
      name?: string
      api_url?: string
      api_token?: string
      api_format?: 'anthropic' | 'openai_chat' | 'openai_responses'
      openai_extra_params?: Record<string, unknown>
      claude_code_compat_hint?: boolean
      model_mappings?: Record<string, string>
      exposed_models?: ExposedModel[]
      supports_thinking?: boolean
      multimodal_switch?: boolean
      multimodal_model?: string
      strip_unknown_content_blocks?: boolean
      rate_limit_queue_enabled?: boolean
      max_concurrent_requests?: number
      max_queue_size?: number
      queue_timeout_ms?: number
      retry_429_enabled?: boolean
      retry_429_max_attempts?: number
      retry_429_initial_delay_ms?: number
      retry_429_max_delay_ms?: number
      enabled?: boolean
    }
  ): Promise<{ success: boolean; provider: Provider }> {
    const res = await fetch(`/api/providers/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: 'request failed' }))
      throw new Error(err.error || `HTTP ${res.status}`)
    }
    return res.json()
  }

  async function deleteProvider(id: string): Promise<boolean> {
    const res = await fetch(`/api/providers/${id}`, { method: 'DELETE' })
    return res.ok
  }

  async function activateProvider(id: string): Promise<boolean> {
    const res = await fetch(`/api/providers/${id}/activate`, { method: 'POST' })
    return res.ok
  }

  async function toggleProvider(id: string): Promise<{ success: boolean; enabled: boolean }> {
    const res = await fetch(`/api/providers/${id}/toggle`, { method: 'POST' })
    return res.json()
  }

  async function duplicateProvider(id: string): Promise<{ success: boolean; provider: Provider }> {
    const res = await fetch(`/api/providers/${id}/duplicate`, { method: 'POST' })
    return res.json()
  }

  async function revealProviderToken(id: string): Promise<{ api_token: string }> {
    const res = await fetch(`/api/providers/${id}/reveal-token`, { method: 'POST' })
    return res.json()
  }

  async function exportProviders(ids: string[]): Promise<{ version: number; exported_at: string; providers: Provider[] }> {
    const res = await fetch('/api/providers/export', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ids }),
    })
    if (!res.ok) throw new Error(`export failed: ${res.status}`)
    return res.json()
  }

  async function importProviders(
    providers: Provider[],
    strategy: 'skip' | 'overwrite' | 'duplicate'
  ): Promise<ProviderImportSummary> {
    const res = await fetch('/api/providers/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ version: 1, providers, strategy }),
    })
    const body: unknown = await res.json().catch(() => null)
    if (isProviderImportSummary(body) && res.ok === body.success) return body

    const backendError = body && typeof body === 'object' && 'error' in body && typeof body.error === 'string'
      ? body.error
      : 'import failed'
    throw new Error(`${backendError} (HTTP ${res.status})`)
  }

  async function testProvider(id: string): Promise<TestResult> {
    const res = await fetch(`/api/providers/${id}/test`, { method: 'POST' })
    return res.json()
  }

  async function testProviderConnection(
    api_url: string,
    api_token: string
  ): Promise<TestResult> {
    const res = await fetch('/api/providers/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ api_url, api_token }),
    })
    return res.json()
  }

  async function getCertificates(): Promise<CertificateInfo> {
    const res = await fetch('/api/certificates')
    if (!res.ok) throw new Error('Failed to fetch certificates')
    return res.json()
  }

  async function getUsageSummary(params?: UsageParams): Promise<UsageSummary> {
    const res = await fetch(`/api/usage/summary${buildQuery(params)}`)
    if (!res.ok) throw new Error('Failed to fetch usage summary')
    return res.json()
  }

  async function getUsageTrends(params?: UsageParams): Promise<UsageTrendPoint[]> {
    const res = await fetch(`/api/usage/trends${buildQuery(params)}`)
    if (!res.ok) throw new Error('Failed to fetch usage trends')
    return res.json()
  }

  async function getUsageRequests(params?: UsageParams): Promise<UsageRequestPage> {
    const res = await fetch(`/api/usage/requests${buildQuery(params)}`)
    if (!res.ok) throw new Error('Failed to fetch usage requests')
    return res.json()
  }

  async function getUsageProviders(params?: UsageParams): Promise<UsageAggregateRow[]> {
    const res = await fetch(`/api/usage/providers${buildQuery(params)}`)
    if (!res.ok) throw new Error('Failed to fetch usage providers')
    return res.json()
  }

  async function getUsageModels(params?: UsageParams): Promise<UsageAggregateRow[]> {
    const res = await fetch(`/api/usage/models${buildQuery(params)}`)
    if (!res.ok) throw new Error('Failed to fetch usage models')
    return res.json()
  }

  async function getUsageCoverage(params?: UsageParams): Promise<UsageCoverageRow[]> {
    const res = await fetch(`/api/usage/coverage${buildQuery(params)}`)
    if (!res.ok) throw new Error('Failed to fetch usage coverage')
    return res.json()
  }

  async function clearUsageData(resetSessionSync: boolean): Promise<UsageClearResult> {
    const res = await fetch('/api/usage/clear', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reset_session_sync: resetSessionSync }),
    })
    if (!res.ok) throw new Error('Failed to clear usage data')
    return res.json()
  }

  async function getSessionProjects(): Promise<SessionProject[]> {
    const res = await fetch('/api/sessions/projects')
    if (!res.ok) throw new Error('Failed to fetch session projects')
    return res.json()
  }

  async function getSessionList(params?: { project?: string; page?: number; page_size?: number }): Promise<SessionListResponse> {
    const res = await fetch(`/api/sessions${buildQuery(params)}`)
    if (!res.ok) throw new Error('Failed to fetch sessions')
    return res.json()
  }

  async function getSessionDetail(id: string, source: string): Promise<SessionDetailResponse> {
    const res = await fetch(`/api/sessions/${encodeURIComponent(id)}${buildQuery({ source })}`)
    if (!res.ok) throw new Error('Failed to fetch session detail')
    return res.json()
  }

  async function exportSessionHTML(id: string, source: string, theme: string, locale: string): Promise<Blob> {
    const res = await fetch(`/api/sessions/${encodeURIComponent(id)}/export${buildQuery({ source, theme, locale })}`)
    if (!res.ok) throw new Error('Failed to export session')
    return res.blob()
  }

  async function getSessionCleanupHint(id: string, source: string): Promise<SessionCleanupHint> {
    const res = await fetch(`/api/sessions/${encodeURIComponent(id)}/cleanup-hint${buildQuery({ source })}`)
    if (!res.ok) throw new Error('Failed to fetch cleanup hint')
    return res.json()
  }

  async function checkForUpdate(): Promise<UpdateCheckResult> {
    const res = await fetch('/api/update/check')
    if (!res.ok) throw new Error('Failed to check for update')
    return res.json()
  }

  async function applyUpdate(): Promise<UpdateApplyResult> {
    const res = await fetch('/api/update/apply', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    })
    if (!res.ok) throw new Error('Failed to apply update')
    return res.json()
  }

  async function getProviderUsage(id: string): Promise<ProviderUsageResponse> {
    const res = await fetch(`/api/providers/${id}/usage`)
    if (!res.ok) {
      if (res.status === 404) throw new Error('Provider not found')
      throw new Error('Failed to fetch usage config')
    }
    return res.json()
  }

  async function updateProviderUsage(id: string, data: ProviderUsageUpdateRequest): Promise<{ success: boolean; config: PublicQuotaConfig }> {
    const res = await fetch(`/api/providers/${id}/usage`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: 'request failed' }))
      throw new Error(err.error || `HTTP ${res.status}`)
    }
    return res.json()
  }

  async function testProviderUsage(id: string, data: ProviderUsageUpdateRequest): Promise<{ success: boolean; result: ProviderQuotaResult }> {
    const res = await fetch(`/api/providers/${id}/usage/test`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: 'request failed' }))
      throw new Error(err.error || `HTTP ${res.status}`)
    }
    return res.json()
  }

  async function queryProviderUsage(id: string): Promise<{ success: boolean; result: ProviderQuotaResult }> {
    const res = await fetch(`/api/providers/${id}/usage/query`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    })
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: 'request failed' }))
      throw new Error(err.error || `HTTP ${res.status}`)
    }
    return res.json()
  }

  async function getAllProviderUsageSnapshots(): Promise<{ snapshots: Record<string, QuotaSnapshot> }> {
    const res = await fetch('/api/providers/usage')
    if (!res.ok) throw new Error('Failed to fetch snapshots')
    return res.json()
  }

  return {
    login,
    logout,
    getPreferences,
    updatePreferences,
    getConfig,
    updateConfig,
    getStatus,
    getProviders,
    createProvider,
    updateProvider,
    deleteProvider,
    activateProvider,
    toggleProvider,
    duplicateProvider,
    revealProviderToken,
    exportProviders,
    importProviders,
    testProvider,
    testProviderConnection,
    getCertificates,
    getUsageSummary,
    getUsageTrends,
    getUsageRequests,
    getUsageProviders,
    getUsageModels,
    getUsageCoverage,
    clearUsageData,
    getSessionProjects,
    getSessionList,
    getSessionDetail,
    exportSessionHTML,
    getSessionCleanupHint,
    checkForUpdate,
    applyUpdate,
    getProviderUsage,
    updateProviderUsage,
    testProviderUsage,
    queryProviderUsage,
    getAllProviderUsageSnapshots,
  }
}
