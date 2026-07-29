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

const qwenScript = `({
  request: {
    url: "{{baseUrl}}/data/api.json?product=sfm_bailian&action=BroadScopeAspnGateway&api=zeldaHttp.apikeyMgr.%2Ftokenplan%2Fpersonal%2Fapi%2Fv2%2Fusage",
    method: "POST",
    bodyType: "form",
    headers: {
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded",
      "Accept": "application/json, text/plain, */*",
      "Referer": "https://platform.qianwenai.com/home/billing/subscription/token-plan-individual"
    },
    body: {
      product: "sfm_bailian",
      action: "BroadScopeAspnGateway",
      sec_token: "{{apiKey2}}",
      region: "cn-beijing",
      params: {
        Api: "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage",
        Data: {
          cornerstoneParam: {
            domain: "platform.qianwenai.com",
            consoleSite: "QIANWENAI",
            console: "ONE_CONSOLE",
            xsp_lang: "zh-CN",
            protocol: "V2",
            productCode: "p_efm"
          }
        },
        V: "1.0"
      }
    }
  },
  extractor: function(response) {
    if (response.code !== "200" || response.successResponse !== true) {
      return { __error_code: "upstream_business_error", __error_message: (response.data && response.data.errorMsg) || "qianwen usage query failed" };
    }
    var inner = response.data && response.data.DataV2 && response.data.DataV2.data && response.data.DataV2.data.data;
    if (!inner) { return { __error_code: "invalid_response", __error_message: "missing data.DataV2.data.data" }; }
    var tiers = [];
    if (typeof inner.per5HourPercentage === "number") { tiers.push({ window: "five_hour", utilization: inner.per5HourPercentage * 100 }); }
    if (typeof inner.per1WeekPercentage === "number") { tiers.push({ window: "seven_day", utilization: inner.per1WeekPercentage * 100, resetsAt: inner.per1WeekResetTime }); }
    if (tiers.length === 0) { return { __error_code: "empty_result", __error_message: "no percentage fields" }; }
    return tiers;
  }
})`

const balanceScript = `({
  request: {
    url: "{{baseUrl}}/user/balance",
    method: "GET",
    headers: {
      "Authorization": "Bearer {{apiKey}}",
      "Accept": "application/json"
    }
  },
  extractor: function(response) {
    var infos = response.balance_infos || [];
    var balances = [];
    for (var i = 0; i < infos.length; i++) {
      balances.push({
        planName: infos[i].currency,
        remaining: infos[i].total_balance,
        unit: infos[i].currency
      });
    }
    return balances;
  }
})`

const templateScript = `({
  request: {
    // {{baseUrl}} is replaced from the Base URL field; the URL must share host with it (same-origin).
    url: "{{baseUrl}}/your/endpoint",
    method: "POST",
    // bodyType:"form" encodes body as application/x-www-form-urlencoded;
    // nested object values are JSON-marshaled automatically (e.g. params).
    bodyType: "form",
    headers: {
      // {{apiKey}} / {{apiKey2}} are replaced from Script API Key / Additional secret; never appear in JS runtime.
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded"
    },
    body: {
      token: "{{apiKey2}}",
      param1: "value1",
      nested: { key: "value" }
    }
  },
  extractor: function(response) {
    // window: five_hour | seven_day | monthly; utilization is always 0-100 used percent.
    return [
      { window: "five_hour", utilization: response.used_pct }
    ];
  }
})`

test('ScriptGeneratorModal exists and declares the required props and events', () => {
  assert.equal(modalExists, true, 'ScriptGeneratorModal.vue must exist')
  assert.match(modalSource, /providerId:\s*string/)
  assert.match(modalSource, /llmProviders:\s*LLMProviderOption\[\]/)
  assert.match(modalSource, /id:\s*string[\s\S]*name:\s*string[\s\S]*exposed_models\?:\s*\{\s*backend_model:\s*string\s*\}\[\][\s\S]*model_mappings\?:\s*Record<string,\s*string>/)
  assert.match(modalSource, /generated:\s*\[script:\s*string,\s*warnings\?:\s*ScriptAuditWarning\[\]\]/)
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
  assert.match(modalSource, /emit\('generated',\s*response\.script,\s*warnings\.value\)/)
  assert.match(modalSource, /emit\('close'\)/)
  assert.doesNotMatch(modalSource, /updateProviderUsage|saveConfig/)
})

test('ScriptGeneratorModal stores and renders script audit warnings', () => {
  assert.match(modalSource, /const warnings = ref<ScriptAuditWarning\[\]>\(\[\]\)/)
  assert.match(modalSource, /warnings\.value\s*=\s*response\.warnings \|\| \[\]/)
  assert.match(modalSource, /v-if="warnings\.length > 0"/)
  assert.match(modalSource, /t\('quota\.ai_generate_warnings'\)/)
  assert.match(modalSource, /v-for="\(\s*w,\s*i\s*\) in warnings"/)
  assert.match(modalSource, /\{\{ translatedWarning\(w\) \}\}/)
  assert.match(modalSource, /function translatedWarning\(warning:\s*ScriptAuditWarning\):\s*string/)
  assert.match(modalSource, /t\(`warning\.\$\{warning\.code\}`\)/)
  assert.match(modalSource, /return warning\.message/)
  assert.match(modalSource, /rgba\(234,\s*179,\s*8,\s*0\.15\)/)
})

test('ScriptGeneratorModal shows multi-round iteration count only after fixes', () => {
  assert.match(modalSource, /const iterations = ref\(0\)/)
  assert.match(modalSource, /iterations\.value\s*=\s*response\.iterations \|\| 0/)
  assert.match(modalSource, /v-if="iterations > 1"/)
  assert.match(modalSource, /t\('quota\.ai_generate_iterations',\s*\{\s*n:\s*iterations\s*\}\)/)
  assert.match(modalSource, /iterations\.value\s*=\s*0/)
})

test('ScriptGeneratorModal keeps warning results visible without blocking generated script', () => {
  assert.match(modalSource, /emit\('generated',\s*response\.script,\s*warnings\.value\)[\s\S]*if \(warnings\.value\.length === 0\) \{[\s\S]*emit\('close'\)/)
})

test('ScriptGeneratorModal disables generation while loading and displays translated errors', () => {
  assert.match(modalSource, /:disabled="loading \|\| !canSubmit"/)
  assert.match(modalSource, /loading \? t\('quota\.ai_generating'\) : t\('quota\.ai_generate_submit'\)/)
  assert.match(modalSource, /response\.error_code/)
  assert.match(modalSource, /t\(`error\.\$\{code\}`\)/)
  assert.match(modalSource, /role="alert"/)
})

test('ScriptGeneratorModal renders the hardcoded script examples library', () => {
  assert.match(modalSource, /max-w-\[1100px\]/)
  assert.match(modalSource, /const scriptExamples = \[/)
  assert.match(modalSource, /id: 'qwen'/)
  assert.match(modalSource, /id: 'balance'/)
  assert.match(modalSource, /id: 'template'/)
  for (const key of [
    'quota.ai_generate_examples_title',
    'quota.ai_generate_ex_qwen_title',
    'quota.ai_generate_ex_balance_title',
    'quota.ai_generate_ex_template_title',
  ]) {
    assert.match(modalSource, new RegExp(`'${key}'`), `missing example key ${key}`)
  }
  assert.match(modalSource, /v-for="ex in scriptExamples"/)
  assert.match(modalSource, /\{\{ t\(ex\.titleKey\) \}\}/)
  assert.match(modalSource, /<pre[\s\S]*>\{\{ ex\.script \}\}<\/pre>/)
})

test('ScriptGeneratorModal keeps example script bodies exactly as specified', () => {
  assert.ok(modalSource.includes(qwenScript), 'qwen example script differs from spec')
  assert.ok(modalSource.includes(balanceScript), 'balance example script differs from spec')
  assert.ok(modalSource.includes(templateScript), 'template example script differs from spec')
})

test('ScriptGeneratorModal copies an example script and shows copied or failed feedback', () => {
  assert.match(modalSource, /async function copyExample\(id: string\)/)
  assert.match(modalSource, /navigator\.clipboard\?\.writeText/)
  assert.match(modalSource, /await navigator\.clipboard\.writeText\(ex\.script\)/)
  assert.match(modalSource, /document\.execCommand\('copy'\)/)
  assert.match(modalSource, /copiedId\.value = id/)
  assert.match(modalSource, /copyFailedId\.value = id/)
  assert.match(modalSource, /t\('quota\.ai_generate_copied'\)/)
  assert.match(modalSource, /t\('quota\.ai_generate_copy_failed'\)/)
  assert.match(modalSource, /setTimeout\(\(\) => \{ if \(copiedId\.value === id\) copiedId\.value = '' \}, 2000\)/)
})

test('useApi exposes generateUsageScript with the specified request and response types', () => {
  assert.match(apiSource, /export interface GenerateScriptRequest[\s\S]*llm_provider_id\?:\s*string[\s\S]*model:\s*string[\s\S]*response_sample:\s*string[\s\S]*request_info\?:\s*string/)
  assert.match(apiSource, /export interface ScriptAuditWarning[\s\S]*code:\s*string[\s\S]*message:\s*string/)
  assert.match(apiSource, /export interface GenerateScriptResponse[\s\S]*script:\s*string[\s\S]*warnings\?:\s*ScriptAuditWarning\[\][\s\S]*iterations\?:\s*number[\s\S]*error_code\?:\s*string/)
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
  assert.match(providerUsageSource, /function onGeneratedScript\(script:\s*string,\s*warnings:\s*ScriptAuditWarning\[\]\s*=\s*\[\]\)[\s\S]*form\.script\s*=\s*script[\s\S]*if \(warnings\.length === 0\) \{[\s\S]*showGenerator\.value\s*=\s*false/)
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
    'quota.ai_generate_warnings',
    'quota.ai_generate_iterations',
    'quota.ai_generate_examples_title',
    'quota.ai_generate_copy',
    'quota.ai_generate_copied',
    'quota.ai_generate_copy_failed',
    'quota.ai_generate_ex_qwen_title',
    'quota.ai_generate_ex_qwen_desc',
    'quota.ai_generate_ex_balance_title',
    'quota.ai_generate_ex_balance_desc',
    'quota.ai_generate_ex_template_title',
    'quota.ai_generate_ex_template_desc',
    'error.invalid_config',
    'error.missing_credentials',
    'error.request_timeout',
    'error.network_error',
    'error.upstream_http_error',
    'error.invalid_credentials',
    'error.invalid_response',
    'error.script_error',
    'error.internal_error',
    'warning.missing_cookie',
    'warning.missing_authorization',
    'warning.missing_sec_token',
    'warning.response_body_misuse',
    'warning.empty_post_body',
    'warning.no_credential_placeholder',
    'warning.hardcoded_url',
  ]) {
    assert.match(i18nSource, new RegExp(`'${key}':`, 'g'), `missing ${key}`)
  }
})

test('defines warning i18n copy with fix suggestions in both languages', () => {
  for (const [key, zhNeedle, enNeedle] of [
    ['warning.missing_cookie', '请求信息含 Cookie', 'Request info contains Cookie'],
    ['warning.missing_authorization', '请求信息含 Authorization', 'Request info contains Authorization'],
    ['warning.missing_sec_token', '请求信息含 sec_token', 'Request info contains sec_token'],
    ['warning.response_body_misuse', 'extractor 收到的 response 已是解析好的 JSON 对象', 'extractor receives response as an already parsed JSON object'],
    ['warning.empty_post_body', 'POST 请求的 body 为空', 'POST request body is empty'],
    ['warning.no_credential_placeholder', '脚本没用任何凭据占位符', 'The script does not use any credential placeholder'],
    ['warning.hardcoded_url', '脚本 URL 没用 {{baseUrl}} 占位符', 'The script URL does not use the {{baseUrl}} placeholder'],
  ]) {
    const escapedKey = key.replace('.', '\\.')
    assert.match(i18nSource, new RegExp(`'${escapedKey}':\\s*'[^']*${zhNeedle}[^']*→ 修复`, 's'), `missing zh fix copy for ${key}`)
    assert.match(i18nSource, new RegExp(`'${escapedKey}':\\s*'[^']*${enNeedle}[^']*Fix:`, 's'), `missing en fix copy for ${key}`)
  }
})
