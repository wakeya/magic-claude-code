import test from 'node:test'
import assert from 'node:assert/strict'
import {
  effectiveTokenPlanProvider,
  showZenMuxFields,
  showVolcengineFields,
  showBaseURLField,
  showAPIKeyField,
  buildSavePayload,
  buildTestPayload,
  shouldShowMiMoWarning,
  shouldShowOfficialBalanceInfo,
  type QuotaFormState,
} from './quotaForm.ts'

const baseForm: QuotaFormState = {
  enabled: true,
  template_type: 'general',
  coding_plan_provider: '',
  timeout_seconds: 10,
  auto_query_interval_minutes: 5,
  script: '',
  base_url: '',
  script_api_key: '',
  zenmux_base_url: '',
  zenmux_api_key: '',
  access_token: '',
  user_id: '',
  access_key_id: '',
  secret_access_key: '',
  clear_script_api_key: false,
  clear_zenmux_api_key: false,
  clear_access_token: false,
  clear_secret_access_key: false,
}

test('effectiveTokenPlanProvider keeps explicit selection precedence', () => {
  assert.equal(effectiveTokenPlanProvider('kimi', 'zenmux'), 'kimi')
  assert.equal(effectiveTokenPlanProvider('', 'zhipu_cn'), 'zhipu_cn')
})

test('field visibility separates generic script fields from ZenMux fields', () => {
  assert.equal(showBaseURLField('general', ''), true)
  assert.equal(showBaseURLField('newapi', ''), true)
  assert.equal(showBaseURLField('token_plan', 'zenmux'), false)
  assert.equal(showAPIKeyField('general', ''), true)
  assert.equal(showAPIKeyField('token_plan', 'zenmux'), false)
  assert.equal(showZenMuxFields('token_plan', 'zenmux'), true)
  assert.equal(showZenMuxFields('general', 'zenmux'), false)
  assert.equal(showVolcengineFields('token_plan', 'volcengine'), true)
})

test('buildSavePayload sends only script credential fields for General', () => {
  const payload = buildSavePayload({
    ...baseForm,
    template_type: 'general',
    base_url: 'https://gateway.example/v1',
    script_api_key: 'script-new',
    zenmux_base_url: 'https://quota.zenmux.example/usage',
    zenmux_api_key: 'zenmux-stored',
  }, '', null)

  assert.equal(payload['base_url'], 'https://gateway.example/v1')
  assert.equal(payload['script_api_key'], 'script-new')
  assert.equal('zenmux_base_url' in payload, false)
  assert.equal('zenmux_api_key' in payload, false)
  assert.equal('api_key' in payload, false)
})

test('buildSavePayload sends only ZenMux override fields for ZenMux', () => {
  const payload = buildSavePayload({
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'zenmux',
    base_url: 'https://generic-must-not-leak.example/v1',
    script_api_key: 'script-must-not-leak',
    zenmux_base_url: 'https://quota.zenmux.example/usage',
    zenmux_api_key: 'zenmux-new',
  }, '', null)

  assert.equal(payload['coding_plan_provider'], 'zenmux')
  assert.equal(payload['zenmux_base_url'], 'https://quota.zenmux.example/usage')
  assert.equal(payload['zenmux_api_key'], 'zenmux-new')
  assert.equal(payload['base_url'], '')
  assert.equal('script_api_key' in payload, false)
  assert.equal('api_key' in payload, false)
})

test('buildSavePayload allows empty ZenMux override for atomic card fallback', () => {
  const payload = buildSavePayload({
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'zenmux',
  }, '', null)
  assert.equal(payload['zenmux_base_url'], '')
  assert.equal('zenmux_api_key' in payload, false)
})

test('buildSavePayload keeps separated configured keys when switching templates', () => {
  const payload = buildSavePayload({
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'kimi',
  }, '', {
    script_api_key_configured: true,
    zenmux_api_key_configured: true,
    access_token_configured: false,
    secret_access_key_configured: false,
  })
  assert.equal('clear_script_api_key' in payload, false)
  assert.equal('clear_zenmux_api_key' in payload, false)
  assert.equal('script_api_key' in payload, false)
  assert.equal('zenmux_api_key' in payload, false)
})

test('buildSavePayload propagates independent clear flags', () => {
  const scriptClear = buildSavePayload({...baseForm, clear_script_api_key: true}, '', null)
  assert.equal(scriptClear['clear_script_api_key'], true)
  assert.equal('clear_zenmux_api_key' in scriptClear, false)

  const zenmuxClear = buildSavePayload({...baseForm, clear_zenmux_api_key: true}, '', null)
  assert.equal(zenmuxClear['clear_zenmux_api_key'], true)
  assert.equal('clear_script_api_key' in zenmuxClear, false)
})

test('buildSavePayload preserves NewAPI and Volcengine cleanup behavior', () => {
  const payload = buildSavePayload(baseForm, '', {
    script_api_key_configured: false,
    zenmux_api_key_configured: false,
    access_token_configured: true,
    secret_access_key_configured: true,
  })
  assert.equal(payload['clear_access_token'], true)
  assert.equal(payload['clear_secret_access_key'], true)
})

test('buildTestPayload sends only active script key', () => {
  const payload = buildTestPayload({
    ...baseForm,
    template_type: 'custom',
    script_api_key: 'script-new',
    zenmux_api_key: 'zenmux-must-not-leak',
  }, '')
  assert.equal(payload['script_api_key'], 'script-new')
  assert.equal('zenmux_api_key' in payload, false)
  assert.equal('api_key' in payload, false)
})

test('buildTestPayload sends only active ZenMux key and URL', () => {
  const payload = buildTestPayload({
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'zenmux',
    script_api_key: 'script-must-not-leak',
    zenmux_base_url: 'https://quota.zenmux.example/usage',
    zenmux_api_key: 'zenmux-new',
  }, '')
  assert.equal(payload['zenmux_base_url'], 'https://quota.zenmux.example/usage')
  assert.equal(payload['zenmux_api_key'], 'zenmux-new')
  assert.equal('script_api_key' in payload, false)
  assert.equal('api_key' in payload, false)
})

test('buildTestPayload omits all unrelated secrets for Kimi', () => {
  const payload = buildTestPayload({
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'kimi',
    script_api_key: 'script-stale',
    zenmux_api_key: 'zenmux-stale',
    access_token: 'access-stale',
    secret_access_key: 'sk-stale',
  }, '')
  for (const field of ['script_api_key', 'zenmux_api_key', 'access_token', 'secret_access_key', 'api_key']) {
    assert.equal(field in payload, false, `${field} must be omitted`)
  }
})

test('MiMo and official balance notices remain scoped to their templates', () => {
  assert.equal(shouldShowMiMoWarning('token_plan', true), true)
  assert.equal(shouldShowMiMoWarning('general', true), false)
  assert.equal(shouldShowOfficialBalanceInfo('official_balance', 'deepseek'), true)
  assert.equal(shouldShowOfficialBalanceInfo('general', 'deepseek'), false)
})
