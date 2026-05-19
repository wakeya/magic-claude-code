export interface StatusInfo {
  running: boolean
  backend_url: string
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

export interface Provider {
  id: string
  name: string
  api_url: string
  api_token_mask: string
  model_mappings: Record<string, string>
  supports_thinking: boolean
  enabled: boolean
  active: boolean
  created_at: string
  updated_at: string
}

export interface ProvidersResponse {
  providers: Provider[]
  active_provider_id: string
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

export type UsageParams = Record<string, string | number | boolean | null | undefined>

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

  async function getStatus(): Promise<StatusInfo> {
    const res = await fetch('/api/status')
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
    model_mappings: Record<string, string>
    supports_thinking?: boolean
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
      model_mappings?: Record<string, string>
      supports_thinking?: boolean
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

  return {
    login,
    logout,
    getStatus,
    getProviders,
    createProvider,
    updateProvider,
    deleteProvider,
    activateProvider,
    toggleProvider,
    duplicateProvider,
    revealProviderToken,
    testProvider,
    testProviderConnection,
    getCertificates,
    getUsageSummary,
    getUsageTrends,
    getUsageRequests,
    getUsageProviders,
    getUsageModels,
    getUsageCoverage,
  }
}
