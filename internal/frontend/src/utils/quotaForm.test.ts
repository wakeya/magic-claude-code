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
  api_key: '',
  access_token: '',
  user_id: '',
  access_key_id: '',
  secret_access_key: '',
  clear_api_key: false,
  clear_access_token: false,
  clear_secret_access_key: false,
}

test('effectiveTokenPlanProvider: explicit selection beats auto-detection', () => {
  assert.equal(effectiveTokenPlanProvider('kimi', 'zenmux'), 'kimi')
  assert.equal(effectiveTokenPlanProvider('', 'zhipu_cn'), 'zhipu_cn')
  assert.equal(effectiveTokenPlanProvider('', ''), '')
})

test('showZenMuxFields: only under token_plan + zenmux', () => {
  assert.equal(showZenMuxFields('token_plan', 'zenmux'), true)
  assert.equal(showZenMuxFields('token_plan', 'kimi'), false)
  assert.equal(showZenMuxFields('general', 'zenmux'), false)
})

test('showVolcengineFields: only under token_plan + volcengine', () => {
  assert.equal(showVolcengineFields('token_plan', 'volcengine'), true)
  assert.equal(showVolcengineFields('token_plan', 'kimi'), false)
  assert.equal(showVolcengineFields('general', 'volcengine'), false)
})

test('showBaseURLField: general/custom/newapi always; zenmux under token_plan', () => {
  assert.equal(showBaseURLField('general', ''), true)
  assert.equal(showBaseURLField('custom', ''), true)
  assert.equal(showBaseURLField('newapi', ''), true)
  assert.equal(showBaseURLField('token_plan', 'zenmux'), true)
  // Kimi/volcengine under token_plan do NOT show base_url (fixed endpoints).
  assert.equal(showBaseURLField('token_plan', 'kimi'), false)
  assert.equal(showBaseURLField('token_plan', 'volcengine'), false)
  assert.equal(showBaseURLField('official_balance', ''), false)
})

test('showAPIKeyField: general/custom always; zenmux under token_plan', () => {
  assert.equal(showAPIKeyField('general', ''), true)
  assert.equal(showAPIKeyField('custom', ''), true)
  assert.equal(showAPIKeyField('token_plan', 'zenmux'), true)
  assert.equal(showAPIKeyField('token_plan', 'kimi'), false)
})

test('buildSavePayload: switching ZenMux → Kimi drops stale base_url', () => {
  const form: QuotaFormState = {
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'kimi',
    // Stale ZenMux URL left in the form from a previous config.
    base_url: 'https://quota.zenmux.example/v1',
  }
  const payload = buildSavePayload(form, '', null)
  assert.equal(payload['coding_plan_provider'], 'kimi')
  // base_url must be cleared (empty) for Kimi so the stale ZenMux URL is not
  // persisted and cannot override the Kimi query.
  assert.equal('base_url' in payload, true, 'base_url should be present to clear stale value')
  assert.equal(payload['base_url'], '', 'stale ZenMux base_url should be cleared to empty')
})

test('buildSavePayload: ZenMux sends base_url + api_key', () => {
  const form: QuotaFormState = {
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'zenmux',
    base_url: 'https://quota.zenmux.example/v1',
    api_key: 'zen-key',
  }
  const payload = buildSavePayload(form, '', null)
  assert.equal(payload['coding_plan_provider'], 'zenmux')
  assert.equal(payload['base_url'], 'https://quota.zenmux.example/v1')
  assert.equal(payload['api_key'], 'zen-key')
})

test('buildSavePayload: volcengine sends AK/SK + coding_plan_provider', () => {
  const form: QuotaFormState = {
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'volcengine',
    access_key_id: 'AKLT1234',
    secret_access_key: 'secret-sk',
  }
  const payload = buildSavePayload(form, '', null)
  assert.equal(payload['coding_plan_provider'], 'volcengine')
  assert.equal(payload['access_key_id'], 'AKLT1234')
  assert.equal(payload['secret_access_key'], 'secret-sk')
  // base_url cleared for volcengine (region derived from card URL).
  assert.equal(payload['base_url'], '')
})

test('buildSavePayload: auto-detected provider used when no explicit selection', () => {
  const form: QuotaFormState = {
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: '', // not explicitly set
  }
  const payload = buildSavePayload(form, 'minimax_cn', null)
  assert.equal(payload['coding_plan_provider'], 'minimax_cn')
})

test('buildSavePayload: official_balance sends no coding_plan_provider/script/base_url', () => {
  const form: QuotaFormState = {
    ...baseForm,
    template_type: 'official_balance',
  }
  const payload = buildSavePayload(form, '', null)
  assert.equal('coding_plan_provider' in payload, false)
  assert.equal('script' in payload, false)
  assert.equal(payload['base_url'], '')
})

test('buildSavePayload: clear flags propagate', () => {
  const form: QuotaFormState = {
    ...baseForm,
    clear_api_key: true,
    clear_access_token: true,
    clear_secret_access_key: true,
  }
  const payload = buildSavePayload(form, '', null)
  assert.equal(payload['clear_api_key'], true)
  assert.equal(payload['clear_access_token'], true)
  assert.equal(payload['clear_secret_access_key'], true)
})

test('buildTestPayload: carries effective provider for token_plan', () => {
  const form: QuotaFormState = {
    ...baseForm,
    template_type: 'token_plan',
    coding_plan_provider: 'kimi',
    base_url: 'https://quota.zenmux.example/v1', // stale
  }
  const payload = buildTestPayload(form, '')
  assert.equal(payload['coding_plan_provider'], 'kimi')
  // Test payload still carries base_url (backend draft resolves effective URL),
  // but the explicit provider ensures the adapter targets Kimi.
  assert.equal(payload['template_type'], 'token_plan')
})

test('buildTestPayload: newapi carries access_token + user_id', () => {
  const form: QuotaFormState = {
    ...baseForm,
    template_type: 'newapi',
    base_url: 'https://panel.example.com',
    access_token: 'tok',
    user_id: 'u1',
  }
  const payload = buildTestPayload(form, '')
  assert.equal(payload['access_token'], 'tok')
  assert.equal(payload['user_id'], 'u1')
})
