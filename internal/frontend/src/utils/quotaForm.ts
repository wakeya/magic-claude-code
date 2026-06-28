// Pure helpers extracted from ProviderUsageView.vue so the quota configuration
// behavior (field visibility, payload construction, provider precedence) is
// unit-testable without mounting the Vue component.

export type TemplateType =
  | 'general'
  | 'custom'
  | 'newapi'
  | 'token_plan'
  | 'official_balance'

export interface QuotaFormState {
  enabled: boolean
  template_type: TemplateType
  coding_plan_provider: string
  timeout_seconds: number
  auto_query_interval_minutes: number
  script: string
  base_url: string
  api_key: string
  access_token: string
  user_id: string
  access_key_id: string
  secret_access_key: string
  clear_api_key: boolean
  clear_access_token: boolean
  clear_secret_access_key: boolean
}

// Explicit provider selection takes precedence over auto-detection.
export function effectiveTokenPlanProvider(
  savedProvider: string,
  detectedProvider: string
): string {
  return savedProvider || detectedProvider || ''
}

export function isZenMux(provider: string): boolean {
  return provider === 'zenmux'
}

export function isVolcengine(provider: string): boolean {
  return provider === 'volcengine'
}

// ZenMux shows its own Base URL + API Key under token_plan.
export function showZenMuxFields(templateType: TemplateType, provider: string): boolean {
  return templateType === 'token_plan' && isZenMux(provider)
}

// MiMo deferral warning is only meaningful for token_plan (MiMo is a token-plan
// host). Suppress it for other templates so a detected MiMo card does not show
// a misleading warning under, e.g., official_balance.
export function shouldShowMiMoWarning(templateType: TemplateType, isMiMo: boolean): boolean {
  return templateType === 'token_plan' && isMiMo
}

// Official-balance detection info is shown only under official_balance and only
// when a provider was actually detected from the card URL.
export function shouldShowOfficialBalanceInfo(
  templateType: TemplateType,
  detectedBalance: string
): boolean {
  return templateType === 'official_balance' && !!detectedBalance
}

// Volcengine shows AK/SK when detected or explicitly selected.
export function showVolcengineFields(templateType: TemplateType, provider: string): boolean {
  return templateType === 'token_plan' && isVolcengine(provider)
}

// Base URL is shown for general/custom/newapi, plus ZenMux under token_plan.
export function showBaseURLField(templateType: TemplateType, provider: string): boolean {
  if (['general', 'custom', 'newapi'].includes(templateType)) return true
  return showZenMuxFields(templateType, provider)
}

// API Key is shown for general/custom, plus ZenMux under token_plan.
export function showAPIKeyField(templateType: TemplateType, provider: string): boolean {
  if (['general', 'custom'].includes(templateType)) return true
  return showZenMuxFields(templateType, provider)
}

export interface SavedConfig {
  api_key_configured?: boolean
  access_token_configured?: boolean
  secret_access_key_configured?: boolean
}

// buildSavePayload constructs the PUT /usage body from the form state.
// Key rules:
//  - coding_plan_provider is sent only for token_plan, using the effective
//    (explicit > detected) provider.
//  - base_url / api_key are sent only when the template/provider uses them.
//    Switching away from ZenMux therefore drops the stale ZenMux URL so the
//    backend does not persist or query it.
//  - When switching away from a template/provider that had a configured secret,
//    an explicit clear_* flag is emitted as defense-in-depth (the backend
//    NormalizeForTemplate is the primary safety boundary).
export function buildSavePayload(
  form: QuotaFormState,
  detectedTokenPlan: string,
  saved: SavedConfig | null
): Record<string, unknown> {
  const provider = effectiveTokenPlanProvider(form.coding_plan_provider, detectedTokenPlan)
  const zenmux = showZenMuxFields(form.template_type, provider)
  const baseURL = ['general', 'custom', 'newapi'].includes(form.template_type) || zenmux
  const usesAPIKey = ['general', 'custom'].includes(form.template_type) || zenmux
  const usesAccessToken = form.template_type === 'newapi'
  const usesVolcSK = showVolcengineFields(form.template_type, provider)

  const data: Record<string, unknown> = {
    enabled: form.enabled,
    template_type: form.template_type,
    timeout_seconds: form.timeout_seconds,
    auto_query_interval_minutes: form.auto_query_interval_minutes,
  }
  if (form.template_type === 'token_plan' && provider) {
    data.coding_plan_provider = provider
  }
  if (['general', 'custom'].includes(form.template_type)) {
    data.script = form.script
  }
  if (baseURL) {
    data.base_url = form.base_url
  } else {
    // The current template/provider does not use a quota Base URL. Send an
    // explicit empty value so a stale URL from a previous config (e.g. a
    // leftover ZenMux URL after switching to Kimi) is cleared rather than
    // silently retained by the backend's partial update.
    data.base_url = ''
  }
  if (usesAPIKey && form.api_key) data.api_key = form.api_key
  if (usesAccessToken && form.access_token) data.access_token = form.access_token
  if (usesAccessToken && form.user_id) data.user_id = form.user_id
  if (usesVolcSK && form.access_key_id) data.access_key_id = form.access_key_id
  if (usesVolcSK && form.secret_access_key) data.secret_access_key = form.secret_access_key

  // Defense-in-depth: emit explicit clear flags when a previously configured
  // secret is no longer applicable to the new template/provider.
  if (saved?.api_key_configured && !usesAPIKey) data.clear_api_key = true
  if (saved?.access_token_configured && !usesAccessToken) data.clear_access_token = true
  if (saved?.secret_access_key_configured && !usesVolcSK) data.clear_secret_access_key = true

  // User-initiated explicit clears always propagate.
  if (form.clear_api_key) data.clear_api_key = true
  if (form.clear_access_token) data.clear_access_token = true
  if (form.clear_secret_access_key) data.clear_secret_access_key = true
  return data
}

// buildTestPayload constructs the POST /usage/test body. Unlike save, it does
// not clear secrets, but — like buildSavePayload — it only carries fields
// applicable to the current template/provider so stale credentials from a
// different configuration are not transmitted to the backend at all.
export function buildTestPayload(
  form: QuotaFormState,
  detectedTokenPlan: string
): Record<string, unknown> {
  const provider = effectiveTokenPlanProvider(form.coding_plan_provider, detectedTokenPlan)
  const usesBaseURL =
    ['general', 'custom', 'newapi'].includes(form.template_type) ||
    showZenMuxFields(form.template_type, provider)
  const usesAPIKey = ['general', 'custom'].includes(form.template_type) || showZenMuxFields(form.template_type, provider)
  const usesAccessToken = form.template_type === 'newapi'
  const usesVolcSK = showVolcengineFields(form.template_type, provider)

  const data: Record<string, unknown> = {
    enabled: true,
    template_type: form.template_type,
    timeout_seconds: form.timeout_seconds,
  }
  if (['general', 'custom'].includes(form.template_type)) {
    data.script = form.script
  }
  if (usesBaseURL) data.base_url = form.base_url
  if (form.template_type === 'token_plan' && provider) {
    data.coding_plan_provider = provider
  }
  if (usesAPIKey && form.api_key) data.api_key = form.api_key
  if (usesAccessToken && form.access_token) data.access_token = form.access_token
  if (usesAccessToken && form.user_id) data.user_id = form.user_id
  if (usesVolcSK && form.access_key_id) data.access_key_id = form.access_key_id
  if (usesVolcSK && form.secret_access_key) data.secret_access_key = form.secret_access_key
  return data
}
