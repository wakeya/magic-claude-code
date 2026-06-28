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
  script_api_key: string
  zenmux_base_url: string
  zenmux_api_key: string
  access_token: string
  user_id: string
  access_key_id: string
  secret_access_key: string
  clear_script_api_key: boolean
  clear_zenmux_api_key: boolean
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

// Generic Base URL belongs only to script templates and NewAPI. ZenMux has a
// separately named override URL so the two destinations cannot be confused.
export function showBaseURLField(templateType: TemplateType, _provider: string): boolean {
  return ['general', 'custom', 'newapi'].includes(templateType)
}

// Script API Key belongs only to General/Custom.
export function showAPIKeyField(templateType: TemplateType, _provider: string): boolean {
  return ['general', 'custom'].includes(templateType)
}

export interface SavedConfig {
  script_api_key_configured?: boolean
  zenmux_api_key_configured?: boolean
  access_token_configured?: boolean
  secret_access_key_configured?: boolean
}

// buildSavePayload constructs the PUT /usage body from the form state.
// Key rules:
//  - coding_plan_provider is sent only for token_plan, using the effective
//    (explicit > detected) provider.
//  - script and ZenMux credentials use distinct field names and are emitted
//    only for their active purpose.
//  - Inactive Script/ZenMux credentials are omitted and retained independently;
//    only an explicit clear flag removes either one.
export function buildSavePayload(
  form: QuotaFormState,
  detectedTokenPlan: string,
  saved: SavedConfig | null
): Record<string, unknown> {
  const provider = effectiveTokenPlanProvider(form.coding_plan_provider, detectedTokenPlan)
  const zenmux = showZenMuxFields(form.template_type, provider)
  const usesBaseURL = ['general', 'custom', 'newapi'].includes(form.template_type)
  const usesScriptAPIKey = ['general', 'custom'].includes(form.template_type)
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
  if (usesBaseURL) {
    data.base_url = form.base_url
  } else {
    // Clear the generic URL when leaving its templates. The independent
    // ZenMux URL is intentionally unaffected.
    data.base_url = ''
  }
  if (usesScriptAPIKey && form.script_api_key) data.script_api_key = form.script_api_key
  if (zenmux) {
    // Empty URL + omitted key selects the complete card credential fallback.
    data.zenmux_base_url = form.zenmux_base_url
    if (form.zenmux_api_key) data.zenmux_api_key = form.zenmux_api_key
  }
  if (usesAccessToken && form.access_token) data.access_token = form.access_token
  if (usesAccessToken && form.user_id) data.user_id = form.user_id
  if (usesVolcSK && form.access_key_id) data.access_key_id = form.access_key_id
  if (usesVolcSK && form.secret_access_key) data.secret_access_key = form.secret_access_key

  // NewAPI and Volcengine retain their existing cleanup behavior. Script and
  // ZenMux keys are independent and are never auto-cleared on template switch.
  if (saved?.access_token_configured && !usesAccessToken) data.clear_access_token = true
  if (saved?.secret_access_key_configured && !usesVolcSK) data.clear_secret_access_key = true

  // User-initiated explicit clears always propagate.
  if (form.clear_script_api_key) data.clear_script_api_key = true
  if (form.clear_zenmux_api_key) data.clear_zenmux_api_key = true
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
  const usesBaseURL = ['general', 'custom', 'newapi'].includes(form.template_type)
  const usesScriptAPIKey = ['general', 'custom'].includes(form.template_type)
  const zenmux = showZenMuxFields(form.template_type, provider)
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
  if (usesScriptAPIKey && form.script_api_key) data.script_api_key = form.script_api_key
  if (zenmux) {
    data.zenmux_base_url = form.zenmux_base_url
    if (form.zenmux_api_key) data.zenmux_api_key = form.zenmux_api_key
  }
  if (usesAccessToken && form.access_token) data.access_token = form.access_token
  if (usesAccessToken && form.user_id) data.user_id = form.user_id
  if (usesVolcSK && form.access_key_id) data.access_key_id = form.access_key_id
  if (usesVolcSK && form.secret_access_key) data.secret_access_key = form.secret_access_key
  return data
}
