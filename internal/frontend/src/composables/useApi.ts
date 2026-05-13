export interface StatusInfo {
  running: boolean
  backend_url: string
  uptime: string
  requests_total: number
  last_request: string | null
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

export function useApi() {
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
  }
}
