import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const modalPath = join(here, 'ScriptGeneratorModal.vue')
const providerUsagePath = join(here, 'ProviderUsageModal.vue')
const apiPath = join(here, '../composables/useApi.ts')
const i18nPath = join(here, '../composables/useI18n.ts')

const modalExists = existsSync(modalPath)
const modalSource = modalExists ? readFileSync(modalPath, 'utf8') : ''
const providerUsageSource = readFileSync(providerUsagePath, 'utf8')
const apiSource = readFileSync(apiPath, 'utf8')
const i18nSource = readFileSync(i18nPath, 'utf8')

test('ScriptGeneratorModal exists and declares the required props and events', () => {
  assert.equal(modalExists, true, 'ScriptGeneratorModal.vue must exist')
  assert.match(modalSource, /providerId:\s*string/)
  assert.match(modalSource, /llmProviders:\s*LLMProviderOption\[\]/)
  assert.match(modalSource, /id:\s*string[\s\S]*name:\s*string[\s\S]*exposed_models\?:\s*\{\s*backend_model:\s*string\s*\}\[\][\s\S]*model_mappings\?:\s*Record<string,\s*string>/)
  assert.match(modalSource, /generated:\s*\[script:\s*string\]/)
  assert.match(modalSource, /close:\s*\[\]/)
})

test('ScriptGeneratorModal renders provider select, model datalist and all input fields', () => {
  assert.match(modalSource, /<select[^>]+v-model="selectedProviderId"/s)
  assert.match(modalSource, /v-for="provider in props\.llmProviders"/)
  assert.match(modalSource, /:value="provider\.id"/)
  assert.match(modalSource, /provider\.name/)
  assert.match(modalSource, /t\('quota\.ai_generate_provider'\)/)
  assert.match(modalSource, /t\('quota\.ai_generate_provider_hint'\)/)
  assert.match(modalSource, /<input[^>]+v-model="model"[^>]+:list="modelOptionsId"/s)
  assert.match(modalSource, /<datalist\s+:id="modelOptionsId"/)
  assert.match(modalSource, /v-for="option in modelOptions"/)
  assert.match(modalSource, /v-model="prompt"/)
  assert.match(modalSource, /v-model="responseSample"/)
  assert.match(modalSource, /v-model="requestInfo"/)
  assert.match(modalSource, /t\('quota\.ai_generate_prompt_hint'\)/)
  assert.match(modalSource, /t\('quota\.ai_generate_sample_hint'\)/)
})

test('ScriptGeneratorModal derives model options from the selected LLM provider and clears model on provider change', () => {
  assert.match(modalSource, /const selectedProviderId = ref\(/)
  assert.match(modalSource, /props\.llmProviders\.find\(provider => provider\.id === selectedProviderId\.value\)/)
  assert.match(modalSource, /selectedProvider\.value\?\.exposed_models \|\| \[\]/)
  assert.match(modalSource, /\.map\(item => item\.backend_model\)/)
  assert.match(modalSource, /Object\.values\(selectedProvider\.value\?\.model_mappings \|\| \{\}\)/)
  assert.match(modalSource, /watch\(selectedProviderId,[\s\S]*model\.value\s*=\s*''/)
})

test('ScriptGeneratorModal calls generateUsageScript and emits generated script without saving', () => {
  assert.match(modalSource, /api\.generateUsageScript\(props\.providerId,\s*\{/)
  assert.match(modalSource, /llm_provider_id:\s*selectedProviderId\.value/)
  assert.match(modalSource, /model:\s*model\.value/)
  assert.match(modalSource, /response_sample:\s*responseSample\.value/)
  assert.match(modalSource, /request_info:\s*requestInfo\.value/)
  assert.match(modalSource, /emit\('generated',\s*response\.script\)/)
  assert.match(modalSource, /emit\('close'\)/)
  assert.doesNotMatch(modalSource, /updateProviderUsage|saveConfig/)
})

test('ScriptGeneratorModal disables generation while loading and displays translated errors', () => {
  assert.match(modalSource, /:disabled="loading \|\| !canSubmit"/)
  assert.match(modalSource, /loading \? t\('quota\.ai_generating'\) : t\('quota\.ai_generate_submit'\)/)
  assert.match(modalSource, /response\.error_code/)
  assert.match(modalSource, /t\(`error\.\$\{code\}`\)/)
  assert.match(modalSource, /role="alert"/)
})

test('useApi exposes generateUsageScript with the specified request and response types', () => {
  assert.match(apiSource, /export interface GenerateScriptRequest[\s\S]*llm_provider_id\?:\s*string[\s\S]*model:\s*string[\s\S]*response_sample:\s*string[\s\S]*request_info\?:\s*string/)
  assert.match(apiSource, /export interface GenerateScriptResponse[\s\S]*script:\s*string[\s\S]*error_code\?:\s*string/)
  assert.match(apiSource, /async function generateUsageScript\(providerId:\s*string,\s*req:\s*GenerateScriptRequest\):\s*Promise<GenerateScriptResponse>/)
  assert.match(apiSource, /fetch\(`\/api\/providers\/\$\{providerId\}\/usage\/generate-script`/)
  assert.match(apiSource, /generateUsageScript,/)
})

test('ProviderUsageModal shows AI generation only for script templates and fills form.script', () => {
  assert.match(providerUsageSource, /import ScriptGeneratorModal from '@\/components\/ScriptGeneratorModal\.vue'/)
  assert.match(providerUsageSource, /v-if="showScript"[\s\S]*t\('quota\.ai_generate'\)/)
  assert.match(providerUsageSource, /showGenerator\.value\s*=\s*true/)
  assert.match(providerUsageSource, /<ScriptGeneratorModal\s+v-if="showGenerator"/)
  assert.match(providerUsageSource, /:providerId="providerId"/)
  assert.match(providerUsageSource, /:llmProviders="llmProviders"/)
  assert.match(providerUsageSource, /const llmProviders = computed\(\(\) =>/)
  assert.match(providerUsageSource, /\['anthropic',\s*'openai_chat',\s*'openai_responses'\]\.includes\(provider\.api_format\)/)
  assert.match(providerUsageSource, /provider\.enabled/)
  assert.match(providerUsageSource, /provider\.api_token_configured \?\? !!provider\.api_token_mask/)
  assert.match(providerUsageSource, /function onGeneratedScript\(script:\s*string\)[\s\S]*form\.script\s*=\s*script[\s\S]*showGenerator\.value\s*=\s*false/)
  assert.match(providerUsageSource, /api\.getProviders\(\)/)
})

test('defines required bilingual AI generation messages and error codes', () => {
  for (const key of [
    'quota.ai_generate',
    'quota.ai_generate_title',
    'quota.ai_generate_provider',
    'quota.ai_generate_provider_hint',
    'quota.ai_generate_model',
    'quota.ai_generate_prompt',
    'quota.ai_generate_prompt_hint',
    'quota.ai_generate_sample',
    'quota.ai_generate_sample_hint',
    'quota.ai_generate_request_info',
    'quota.ai_generate_submit',
    'quota.ai_generating',
    'error.invalid_config',
    'error.missing_credentials',
    'error.request_timeout',
    'error.network_error',
    'error.upstream_http_error',
    'error.invalid_credentials',
    'error.invalid_response',
    'error.script_error',
    'error.internal_error',
  ]) {
    assert.match(i18nSource, new RegExp(`'${key}':`, 'g'), `missing ${key}`)
  }
})
