# Custom 脚本 AI 生成规格

本地页面：管理后台供应商卡片「用量」弹窗（`ProviderUsageModal.vue` + 新组件 `ScriptGeneratorModal.vue`）/ 代理入口：不修改模型代理链路；新增认证后的 `POST /api/providers/{id}/usage/generate-script` / 参考源站：复用 mcc 已配置的 provider 卡片（`APIFormat + APIURL + APIToken`）作为 LLM 调用凭据 / 技术栈：Go 1.26 标准库 + Vue 3 + TypeScript + Tailwind / 最后更新：2026-07-27 / 状态：draft / 进度：0 / 5 planned

## 整体分析

### 1. 目标

为 `custom` 脚本编辑器增加「AI 生成」入口：用户描述需求 + 粘贴响应样例，mcc 后端调用一个已配置的 provider 卡片（LLM）生成符合 MCC `custom` 脚本契约的 JavaScript，填入编辑器供用户测试/保存。降低 custom 模板的使用门槛。

### 2. 核心设计决策（brainstorming 已确认）

1. **LLM 来源**：复用 mcc 已配置的 provider 卡片（用户选一个 LLM provider）。后端读该卡片的 `APIFormat + APIURL + APIToken` 调用，不新增全局 AI 配置、token 不经前端。
2. **三协议支持**：按 `provider.APIFormat` 自动选择：
   - `anthropic` → `POST {base}/v1/messages`
   - `openai_chat` → `POST {base}/v1/chat/completions`
   - `openai_responses` → `POST {base}/v1/responses`
3. **model 来源**：前端 datalist（该 provider 的 `ExposedModels[].ID` + `ModelMappings` values）+ 手输兜底。
4. **响应样例必须**：用户必须粘贴一次真实上游响应 JSON，AI 据此生成准确的 `extractor`（深层嵌套字段路径靠样例定位，不靠猜）。
5. **提示词固化**：MCC custom 脚本契约（格式、占位符、归一化字段、安全约束）由后端写死在系统提示，用户不可改；用户消息只含需求描述 + 响应样例 + 可选请求信息。
6. **安全**：AI 生成的脚本仍走 `ScriptExecutor` 沙箱（goja 无 fetch/require/file，同源校验，GET/POST only，秘密由 Go 层替换）——AI 无法越权，最坏情况是脚本运行报错。

### 3. APIURL 拼接规则（关键细节）

provider 的 `APIURL` 可能是 base（`https://api.anthropic.com`）或已含版本前缀（`https://api.openai.com/v1`、`https://gateway.example/v1`）。后端统一处理：

```go
// trim 尾部 /，然后按是否已含 /v1 决定拼接：
base := strings.TrimRight(provider.APIURL, "/")
var endpoint string
switch provider.APIFormat {
case APIFormatAnthropic:
    if strings.HasSuffix(base, "/v1") { endpoint = base + "/messages" } else { endpoint = base + "/v1/messages" }
case APIFormatOpenAIChat:
    if strings.HasSuffix(base, "/v1") { endpoint = base + "/chat/completions" } else { endpoint = base + "/v1/chat/completions" }
case APIFormatOpenAIResponses:
    if strings.HasSuffix(base, "/v1") { endpoint = base + "/responses" } else { endpoint = base + "/v1/responses" }
}
```

### 4. 三协议请求/响应契约

#### anthropic
```text
POST {endpoint}
x-api-key: {APIToken}
anthropic-version: 2023-06-01
content-type: application/json

body: {
  "model": "{model}",
  "max_tokens": 4096,
  "system": "{systemPrompt}",
  "messages": [{"role":"user","content":"{userMessage}"}]
}

响应解析：respJSON.content[0].text（content 是数组，首元素 type=="text"）
```

#### openai_chat
```text
POST {endpoint}
Authorization: Bearer {APIToken}
content-type: application/json

body: {
  "model": "{model}",
  "messages": [
    {"role":"system","content":"{systemPrompt}"},
    {"role":"user","content":"{userMessage}"}
  ]
}

响应解析：respJSON.choices[0].message.content
```

#### openai_responses
```text
POST {endpoint}
Authorization: Bearer {APIToken}
content-type: application/json

body: {
  "model": "{model}",
  "instructions": "{systemPrompt}",
  "input": "{userMessage}"
}

响应解析：优先 respJSON.output_text；否则遍历 respJSON.output[].content[] 找 type=="output_text" 或 message；兼容中转返回 openai_chat 形态（choices[0].message.content）作为兜底。
```

所有协议：HTTP 非 2xx → 读 body 前 512 字节摘要用于错误信息（脱敏 APIToken）；超时用配置值（默认 30s）。

### 5. 系统提示词（固化）

```
You generate a JavaScript quota-query script for MCC (Magic Claude Code), a Claude Code proxy.

OUTPUT FORMAT — return ONLY a single JavaScript expression: an object literal `({ request, extractor })`. No markdown fences, no explanation, no leading/trailing text.

REQUEST CONTRACT:
- request.url: string, may use placeholders {{baseUrl}} (replaced by the script's Base URL at runtime).
- request.method: "GET" or "POST" only.
- request.headers: object of string values; may use placeholders {{apiKey}}, {{apiKey2}}, {{accessToken}}, {{userId}} (replaced by Go, never appear in JS runtime). Do NOT set Host/Content-Length/Transfer-Encoding/Connection/Proxy-Authorization.
- request.body: for POST, a JSON object (default), OR set request.bodyType: "form" and make body a flat object whose values may be strings/numbers/booleans/nested objects (nested values are JSON-marshaled then form-encoded).
- The request URL must share scheme+host+port with {{baseUrl}} (same-origin enforced).

EXTRACTOR CONTRACT — extractor(response) returns one object or an array of objects. Each item:
- Time-window tier (preferred when the response has a time-bounded quota): { window: "five_hour"|"seven_day"|"monthly"|"weekly", utilization: <0-100 USED percent>, resetsAt: <RFC3339|string>|<unix seconds>|<unix ms number>, used?: number, total?: number, remaining?: number, unit?: string }
- Balance (no time window): { planName?: string, remaining?: number, used?: number, total?: number, unit?: string, isValid?: boolean, invalidMessage?: string }
- If the upstream returned a business error, return { __error_code: "upstream_business_error"|"invalid_credentials"|..., __error_message: "..." }.
- utilization is ALWAYS "used percent" in 0-100. If the source field is a 0-1 ratio, multiply by 100. If the source is "remaining percent", compute 100 - remaining.

SECURITY — the script runs in a sandboxed goja runtime WITHOUT fetch/require/file/env/process. Do not call any global API; only manipulate the response argument and return literals.

PLACEHOLDERS — {{apiKey}} / {{apiKey2}} are the two configured secrets, {{baseUrl}} is the Base URL, {{accessToken}}/{{userId}} for newapi. Use them in headers/body; never hardcode secrets.

Given the user's description and a real response sample, produce the script. The response sample is authoritative for field paths.
```

用户消息模板（后端拼接）：
```
Need: {prompt}

Request info (best-effart, may be partial):
{request_info or "(not provided — infer from need)"}

Real response sample (JSON):
{response_sample}
```

### 6. LLM 响应解析（剥离 markdown fence）

LLM 偶尔忽略"无 markdown"指令，返回 ```` ```js\n({ ... })\n``` ````。后端 `extractScript(text)` 容忍：
1. trim 首尾空白。
2. 若以 ```` ``` ```` 开头：去掉首行（```` ```js ```` 或 ```` ```javascript ```` 或 ```` ``` ````）和结尾 ```` ``` ````。
3. 再次 trim。
4. 必须以 `(` 或 `({` 开头，以 `})` 或 `)` 结尾；否则返回 error（脚本不像对象字面量）。

可选：用 `ScriptExecutor.parseRequest` 预验证脚本能解析（不执行 HTTP，只解析 request 结构）。预验证失败 → 返回 `script_error` + LLM 原文摘要，前端提示重试。

### 7. 错误分类（稳定错误码）

```text
invalid_config         # provider 不是 LLM / model 空 / APIFormat 未知
missing_credentials    # provider 无 APIToken
request_timeout        # LLM 调用超时
network_error          # LLM 调用网络失败
upstream_http_error    # LLM 返回 4xx/5xx（401/403 → invalid_credentials）
invalid_response       # LLM 响应无法解析出脚本
script_error           # 解析出的脚本 parseRequest 失败
internal_error         # 其他
```

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | Planned | LLM 客户端（三协议） | `internal/providerquota/llm_client.go` | httptest mock 三协议 |
| 2 | Planned | 脚本生成器（提示词 + 解析 + 预验证） | `internal/providerquota/script_generator.go` | 提示词注入、fence 剥离、parseRequest 预验证 |
| 3 | Planned | admin generate-script 端点 | `internal/admin/provider_quota_handler.go` | 端点测试、错误码、脱敏 |
| 4 | Planned | 前端 AI 生成对话框 | `ScriptGeneratorModal.vue`、`ProviderUsageModal.vue`、`useApi.ts`、`useI18n.ts` | 组件测试、npm test + build |
| 5 | Planned | 端到端验证 + spec 回写 | 验证证据 | 真实 LLM 生成千问脚本可用 |

## 需求

### 1. 功能范围

#### 1.1 必须交付

1. 后端 LLM 客户端支持 `anthropic` / `openai_chat` / `openai_responses` 三协议，输入 `(ctx, provider, model, systemPrompt, userMessage, timeout)`，输出 `(text string, err error)`。
2. 后端脚本生成器固化系统提示，注入用户 `prompt + response_sample + request_info`，调 LLM 客户端，解析响应（剥离 markdown fence），可选 `parseRequest` 预验证，返回 `(script string, errResult *ProviderQuotaResult)`。
3. admin `POST /api/providers/{id}/usage/generate-script`，body `{ model, prompt, response_sample, request_info? }`，响应 `{ script }` 或 `{ error_code, error_message }`；provider 不存在 404；未登录 401。
4. 前端 `ScriptGeneratorModal.vue`：model datalist（该 provider ExposedModels + ModelMappings values）+ 手输；需求 textarea；响应样例 textarea；可选请求信息 textarea；「生成」按钮调用后端，成功后把脚本回填到父表单 `form.script`（不自动保存）。
5. `ProviderUsageModal.vue` custom/general 模板脚本编辑器上方加「AI 生成」按钮，打开 `ScriptGeneratorModal`，传入当前 provider id + 已保存的 ExposedModels/ModelMappings。
6. 双语文案（useI18n.ts）覆盖所有新增 label/placeholder/error。

#### 1.2 非目标

1. 不流式输出（首版一次性返回；流式留待后续）。
2. 不支持多轮对话（一次生成；用户手动改脚本或重新生成）。
3. 不自动保存生成脚本（只填入编辑器，用户点「测试查询」/「保存」）。
4. 不为非 custom/general 模板提供 AI 生成（只服务 custom/general）。
5. 不暴露/允许修改系统提示。
6. 不在 LLM 调用中携带上游供应商的会话/Cookie（这是给 Claude Code 的代理卡片，AI 调用只用 Bearer/x-api-key）。

### 2. 数据流

```text
ScriptGeneratorModal
  │  { model, prompt, response_sample, request_info? }
  ▼
POST /api/providers/{id}/usage/generate-script  (auth)
  │  读 provider (APIFormat, APIURL, APIToken)
  ▼
GenerateScript(ctx, provider, model, prompt, responseSample, requestInfo)
  │  buildSystemPrompt() + buildUserMessage()
  ▼
LLMClient.Call(ctx, provider, model, system, user, timeout)
  │  按 APIFormat 调上游 LLM
  ▼
extractScript(llmText)  →  剥离 fence
  │
  ▼ (可选) ScriptExecutor.parseRequest 预验证
返回 { script } 或错误码
  ▼
ScriptGeneratorModal 把 script emit 给父组件
  ▼
ProviderUsageModal: form.script = script（用户可编辑/测试/保存）
```

### 3. 端点契约

`POST /api/providers/{id}/usage/generate-script`

请求体：
```json
{
  "model": "claude-sonnet-5",
  "prompt": "查询千问 token plan 5 小时/7 天已用百分比",
  "response_sample": "{\"code\":\"200\",\"data\":{...}}",
  "request_info": "POST form-urlencoded, needs Cookie + sec_token" 
}
```

响应 200：
```json
{ "script": "({request:{...}, extractor: function(response){...}})" }
```

响应 200 + 生成失败（已调 LLM 但解析/预验证失败）：
```json
{ "script": "", "error_code": "invalid_response", "error_message": "..." }
```

响应 4xx/5xx：provider 不存在 404；未登录 401；provider 非 LLM（无 APIToken 或 APIFormat 未知）400 + `invalid_config`。

### 4. 生命周期与边界

1. provider 必须是 LLM（`APIFormat` 是三种之一 + `APIToken` 非空）。非 LLM 卡片（如纯转发）由 handler 校验返回 `invalid_config`。
2. model 必须非空（前端 datalist 不强制选，但后端校验非空）。
3. `response_sample` 必须非空（前端校验 + 后端校验）。
4. 超时默认 30s（handler 可用配置覆盖，首版硬编码 30s）。
5. 生成失败不持久化、不留快照（与正式 usage query 不同；这只是编辑辅助）。
6. LLM 响应体最大 256 KiB（超出 `invalid_response`）。

## 任务详情

### 任务 1：LLM 客户端（三协议）

#### 需求

**Objective（目标）** — 实现纯 Go LLM 客户端，按 `provider.APIFormat` 调用 anthropic / openai_chat / openai_responses 三协议，返回 LLM 文本。

**Outcomes（成果）** — `internal/providerquota/llm_client.go` + `llm_client_test.go`。

**Evidence（证据）** — httptest mock 三协议，断言请求 URL/headers/body 正确、响应文本解析正确、401→`invalid_credentials`、超时→`request_timeout`、网络错→`network_error`。

**Constraints（约束）** — 不依赖第三方 SDK；标准库 `net/http` + `encoding/json`；不记录 APIToken；错误信息脱敏 APIToken。

**Edge Cases（边界）** — APIURL 含/不含 `/v1`；HTTP 401/403/5xx；超时；响应体非法 JSON；anthropic `content` 数组首元素非 text；openai_responses 各种返回形态。

**Verification（验证）** — `go test -v -race ./internal/providerquota/ -run LLMClient`。

#### 计划

**文件：`internal/providerquota/llm_client.go`**

```go
package providerquota

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"

    "magic-claude-code/internal/config"
)

// LLMClient calls an LLM provider using the card's APIFormat.
type LLMClient struct {
    HTTPClient *http.Client
}

// NewLLMClient builds an LLMClient with the given timeout.
func NewLLMClient(timeout time.Duration) *LLMClient {
    return &LLMClient{HTTPClient: &http.Client{Timeout: timeout}}
}

// LLMCallResult is the text returned by the LLM or a structured error.
type LLMCallResult struct {
    Text       string
    ErrorCode  string // "" when success
    ErrorMessage string
}

// Call invokes the LLM and returns its text response.
func (c *LLMClient) Call(ctx context.Context, provider config.Provider, model, systemPrompt, userMessage string) LLMCallResult {
    // 1. validate provider is an LLM
    if provider.APIToken == "" {
        return LLMCallResult{ErrorCode: "missing_credentials", ErrorMessage: "provider has no api_token"}
    }
    if model == "" {
        return LLMCallResult{ErrorCode: "invalid_config", ErrorMessage: "model is required"}
    }
    endpoint, body, err := buildLLMRequest(provider, model, systemPrompt, userMessage)
    if err != nil {
        return LLMCallResult{ErrorCode: "invalid_config", ErrorMessage: err.Error()}
    }
    // 2. do request (body bytes from buildLLMRequest)
    // 3. on status >= 400: map 401/403 → invalid_credentials, else upstream_http_error
    // 4. parse per protocol, extract text
    // 5. return LLMCallResult{Text: ...}
    // ... (实现按上文契约)
}

// buildLLMRequest returns endpoint URL, request body bytes, and sets headers on the http.Request later.
// It switches on provider.APIFormat.
func buildLLMRequest(provider config.Provider, model, systemPrompt, userMessage string) (endpoint string, bodyBytes []byte, err error) {
    base := strings.TrimRight(provider.APIURL, "/")
    switch provider.APIFormat {
    case config.APIFormatAnthropic:
        // endpoint per §3 rules; body {model,max_tokens:4096,system,messages:[{role:user,content:userMessage}]}
    case config.APIFormatOpenAIChat:
        // endpoint; body {model, messages:[{system},{user}]}
    case config.APIFormatOpenAIResponses:
        // endpoint; body {model, instructions:system, input:userMessage}
    default:
        return "", nil, fmt.Errorf("unsupported api_format %q for LLM call", provider.APIFormat)
    }
}
```

请求头设置（在 `Call` 里构造 `http.Request` 后）：
- anthropic: `x-api-key: {token}`, `anthropic-version: 2023-06-01`, `content-type: application/json`
- openai_chat / openai_responses: `Authorization: Bearer {token}`, `content-type: application/json`

响应解析（在 `Call` 里读 body 后，按 `provider.APIFormat` switch）：
- anthropic: `resp.content[0].text`
- openai_chat: `resp.choices[0].message.content`
- openai_responses: 优先 `resp.output_text`，否则遍历 `resp.output[].content[]` 找 `type=="output_text"`，兜底 `resp.choices[0].message.content`（兼容中转）

错误分类（`Call` 里）：
- `ctx.Err()==DeadlineExceeded` 或 timeout → `request_timeout`
- 其他网络错 → `network_error`
- status 401/403 → `invalid_credentials`
- status >= 400 → `upstream_http_error` + `HTTP {status}`
- 响应体非法 JSON / 解析不出文本 → `invalid_response`

**测试文件：`internal/providerquota/llm_client_test.go`**

为每个协议写一个 `TestLLMClient_<protocol>`：
- 启 httptest server，校验收到的 URL（含/不含 `/v1` 两种 APIURL）、headers（x-api-key / Bearer）、body（model/system/user 在位）。
- 返回该协议的 fixture 响应。
- 断言 `Call` 返回的 `Text` 是期望的 LLM 文本。
- 写 `TestLLMClient_401`（openai_chat 返回 401 → `invalid_credentials`）、`TestLLMClient_Timeout`（server sleep > timeout → `request_timeout`）、`TestLLMClient_NoToken`（`missing_credentials`）、`TestLLMClient_BadFormat`（APIFormat=unknown → `invalid_config`）。

#### 验证

- [ ] `go test -v -race ./internal/providerquota/ -run LLMClient` 全绿。

---

### 任务 2：脚本生成器（提示词 + 解析 + 预验证）

#### 需求

**Objective（目标）** — 固化系统提示，注入用户输入，调 LLMClient，解析响应（剥离 markdown fence），可选 `parseRequest` 预验证。

**Outcomes（成果）** — `internal/providerquota/script_generator.go` + `script_generator_test.go`。

**Evidence（证据）** — 测试断言：系统提示含脚本契约关键词；用户消息含 prompt/response_sample；`extractScript` 剥离 ```` ```js ```` / ```` ``` ```` / 无 fence 三形态；预验证成功/失败两条路径。

**Constraints（约束）** — 系统提示在 Go 常量/函数里固化，不暴露 API；用户消息只注入用户提供的字段；不执行 HTTP（预验证只用 `ScriptExecutor.parseRequest`）。

**Edge Cases（边界）** — LLM 返回带解释文字（首尾非 `(`）；fence 是 ```` ```javascript ```` ；响应空；预验证失败。

**Verification（验证）** — `go test -v -race ./internal/providerquota/ -run ScriptGenerator|ExtractScript`。

#### 计划

**文件：`internal/providerquota/script_generator.go`**

```go
package providerquota

import (
    "context"
    "fmt"
    "strings"
    "time"

    "magic-claude-code/internal/config"
)

// GenerateScriptRequest is the user input for AI script generation.
type GenerateScriptRequest struct {
    Model          string
    Prompt         string
    ResponseSample string
    RequestInfo    string
}

// GenerateScriptResult is the generated script or a structured error.
type GenerateScriptResult struct {
    Script       string
    ErrorCode    string
    ErrorMessage string
}

// GenerateScript builds prompts, calls the LLM, and extracts the script.
func GenerateScript(ctx context.Context, llm *LLMClient, provider config.Provider, req GenerateScriptRequest, timeout time.Duration) GenerateScriptResult {
    if strings.TrimSpace(req.Prompt) == "" || strings.TrimSpace(req.ResponseSample) == "" {
        return GenerateScriptResult{ErrorCode: "invalid_config", ErrorMessage: "prompt and response_sample are required"}
    }
    system := systemPromptForScript()
    user := buildUserMessage(req)
    call := llm.Call(ctx, provider, req.Model, system, user)
    if call.ErrorCode != "" {
        return GenerateScriptResult{ErrorCode: call.ErrorCode, ErrorMessage: call.ErrorMessage}
    }
    script, err := extractScript(call.Text)
    if err != nil {
        return GenerateScriptResult{ErrorCode: "invalid_response", ErrorMessage: err.Error()}
    }
    // optional pre-validation via ScriptExecutor.parseRequest
    exec := &ScriptExecutor{}
    if _, err := exec.parseRequest(script); err != nil {
        return GenerateScriptResult{ErrorCode: "script_error", ErrorMessage: fmt.Sprintf("generated script failed to parse: %v", err)}
    }
    return GenerateScriptResult{Script: script}
}

// systemPromptForScript returns the fixed system prompt (§5 text).
func systemPromptForScript() string { /* 返回 §5 的系统提示全文 */ }

// buildUserMessage assembles the user message from req.
func buildUserMessage(req GenerateScriptRequest) string {
    info := strings.TrimSpace(req.RequestInfo)
    if info == "" {
        info = "(not provided — infer from need)"
    }
    return fmt.Sprintf("Need: %s\n\nRequest info (best-effort, may be partial):\n%s\n\nReal response sample (JSON):\n%s", req.Prompt, info, req.ResponseSample)
}

// extractScript strips markdown fences and validates the result looks like an object literal.
func extractScript(text string) (string, error) {
    s := strings.TrimSpace(text)
    // strip leading ```js / ```javascript / ``` and trailing ```
    if strings.HasPrefix(s, "```") {
        // drop first line
        if idx := strings.IndexByte(s, '\n'); idx >= 0 {
            s = strings.TrimSpace(s[idx+1:])
        }
        if strings.HasSuffix(s, "```") {
            s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
        }
    }
    if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
        return "", fmt.Errorf("LLM output does not look like an object literal (expected leading '(' and trailing ')')")
    }
    return s, nil
}
```

**测试文件：`internal/providerquota/script_generator_test.go`**

1. `TestSystemPromptContainsContract`：`systemPromptForScript()` 含 `"extractor"`、`"utilization"`、`"window"`、`"{{apiKey}}"`、`"bodyType"`、`"sandbox"`。
2. `TestBuildUserMessage`：三字段都注入；`RequestInfo` 空时含 `(not provided`。
3. `TestExtractScript`：
   - 纯 `({...})` → 原样返回。
   - ```` ```js\n({...})\n``` ```` → 剥离。
   - ```` ```javascript\n({...})\n``` ```` → 剥离。
   - ```` ```\n({...})\n``` ```` → 剥离。
   - `"Here is the script:\n({...})"` → error（不以 `(` 开头）。
   - 空串 → error。
4. `TestGenerateScript_EndToEnd`：用注入的 `LLMClient`（通过 `httptest` 或 fake `LLMClient` 字段），mock 返回 ```` ```js\n({request:{...},extractor:function(r){return {remaining:r.balance,unit:\"USD\"};}})\n``` ````，断言 `GenerateScriptResult.Script` 可被 `ScriptExecutor.parseRequest` 解析。
5. `TestGenerateScript_LLMError`：mock LLM 返回 `invalid_credentials` → 透传。
6. `TestGenerateScript_ParseFail`：mock LLM 返回 `"not a script"` → `script_error`。
7. `TestGenerateScript_MissingInput`：空 prompt / 空 sample → `invalid_config`。

（`LLMClient` 若要 fake，把 `Call` 抽成方法即可直接 mock；或测试用 httptest server 走真实 HTTP 路径，任务 1 已覆盖协议，这里也可复用。）

#### 验证

- [ ] `go test -v -race ./internal/providerquota/ -run 'ScriptGenerator|ExtractScript|SystemPrompt|BuildUserMessage'` 全绿。

---

### 任务 3：admin generate-script 端点

#### 需求

**Objective（目标）** — 暴露 `POST /api/providers/{id}/usage/generate-script`，读 provider，调 `GenerateScript`，返回 `{script}` 或错误。

**Outcomes（成果）** — `internal/admin/provider_quota_handler.go`（新 handler + 路由注册）+ 测试。

**Evidence（证据）** — 端点测试覆盖：成功路径、provider 不存在 404、未登录 401、provider 非 LLM（`invalid_config`）、LLM 调用失败（错误码透传）、生成脚本预验证失败（`script_error`）。

**Constraints（约束）** — 复用现有 provider 子树路由分发（`handleProviderRoutes` 识别 `/usage/generate-script`）；handler 不直接 `http.NewRequest`，注入 `LLMClient` 工厂便于测试；超时 30s。

**Edge Cases（边界）** — provider 无 QuotaQuery 配置（仍允许生成，因为 AI 生成是编辑辅助）；model 空；response_sample 空；request_info 缺省。

**Verification（验证）** — `go test -v -race ./internal/admin/ -run GenerateScript`。

#### 计划

**文件：`internal/admin/provider_quota_handler.go`**

1. 新增请求 DTO：
   ```go
   type generateScriptRequest struct {
       Model          string `json:"model"`
       Prompt         string `json:"prompt"`
       ResponseSample string `json:"response_sample"`
       RequestInfo    string `json:"request_info,omitempty"`
   }
   ```
2. 新增 `handleGenerateUsageScript(w, r)`：
   - 从 path 取 `{id}`（复用 `handleProviderRoutes` 的 ID 解析）。
   - 找 provider；不存在 → 404。
   - 校验 provider 是 LLM：`APIFormat` ∈ {anthropic, openai_chat, openai_responses} 且 `APIToken != ""`；否则 400 + `{error: "invalid_config", ...}`。
   - 解析 body `generateScriptRequest`；`model`/`prompt`/`response_sample` 空 → 400 + `invalid_config`。
   - 调 `providerquota.GenerateScript(ctx, NewLLMClient(30s), providerCfg, req, 30s)`。
   - 成功：200 `{script}`。
   - 生成失败：200 `{script:"", error_code, error_message}`（与端点契约一致；HTTP 200 让前端按 error_code 翻译，不触发 HTTP 错误分支）。
3. provider 配置转 `config.Provider`：handler 已有 `configStore` 与 provider 读取路径（参考 `handleProviderUsage`）；把 `config.Provider` 传给 `GenerateScript`（它需要 `APIFormat/APIURL/APIToken`）。

**路由注册**（`server.go` 或 `handleProviderRoutes`）：在 `/api/providers/` 子树里识别 `/usage/generate-script`（在 `/usage/test`、`/usage/query` 之后、通用 `/{id}` 之前）。复用现有分发模式。

**测试文件：`internal/admin/provider_quota_handler_test.go`**

1. `TestGenerateScriptSuccess`：provider 配 `APIFormat:anthropic, APIToken:"k", APIURL:httptest.URL`；mock LLM server 返回 anthropic fixture（content[0].text = `({request:{url:\"{{baseUrl}}/balance\",method:\"GET\"},extractor:function(r){return {remaining:r.balance,unit:\"USD\"};}})`）；POST → 200 + `script` 非空 + 能 `ScriptExecutor.parseRequest`。
2. `TestGenerateScriptProviderNotFound`：404。
3. `TestGenerateScriptUnauthorized`：无 session → 401。
4. `TestGenerateScriptNonLLMProvider`：provider `APIFormat` 非三种 / 无 token → 400 `invalid_config`。
5. `TestGenerateScriptMissingFields`：空 model / prompt / sample → 400 `invalid_config`。
6. `TestGenerateScriptLLMUpstreamError`：mock LLM 返回 401 → 200 `{error_code:"invalid_credentials", ...}`。

#### 验证

- [ ] `go test -v -race ./internal/admin/ -run GenerateScript` 全绿。
- [ ] 端点不返回 APIToken（错误信息脱敏）。

---

### 任务 4：前端 AI 生成对话框

#### 需求

**Objective（目标）** — 新组件 `ScriptGeneratorModal.vue`：model datalist + 需求 + 响应样例 + 可选请求信息 + 「生成」按钮；`ProviderUsageModal.vue` 在 custom/general 脚本编辑器上方加「AI 生成」按钮打开它；生成成功把脚本回填 `form.script`。

**Outcomes（成果）** — `internal/frontend/src/components/ScriptGeneratorModal.vue`、`ProviderUsageModal.vue`、`composables/useApi.ts`、`composables/useI18n.ts` + 测试。

**Evidence（证据）** — 组件测试：custom 模式下「AI 生成」按钮可见、newapi 下不可见；对话框字段齐全；生成成功 emit script；生成失败显示错误码翻译文案。

**Constraints（约束）** — 不自动保存（只回填 form.script）；复用 `useApi.ts` 的 fetch 封装；复用现有 modal 视觉风格（参考 `ProviderUsageModal.vue`）；中英文案。

**Edge Cases（边界）** — provider 无 ExposedModels（datalist 空，手输）；生成中禁用按钮；网络错；错误码翻译兜底。

**Verification（验证）** — `npm --prefix internal/frontend test` 全绿；`npm run build` 成功。

#### 计划

**文件：`internal/frontend/src/composables/useApi.ts`**

新增类型与 API 方法：
```ts
export interface GenerateScriptRequest {
  model: string
  prompt: string
  response_sample: string
  request_info?: string
}
export interface GenerateScriptResponse {
  script: string
  error_code?: string
  error_message?: string
}
// 在现有 quota 相关 API 附近新增：
export async function generateUsageScript(providerId: string, req: GenerateScriptRequest): Promise<GenerateScriptResponse>
```

**文件：`internal/frontend/src/components/ScriptGeneratorModal.vue`**

Props: `providerId: string`、`exposedModels: string[]`（datalist 选项）、`modelMappings: string[]`（datalist 选项）。
State: `model`、`prompt`、`responseSample`、`requestInfo`、`loading`、`error`。
Emits: `generated(script: string)`、`close`。
布局（参考 ProviderUsageModal 的 modal 结构）：
- 标题「AI 生成 custom 脚本」
- model: `<input list="ai-model-options">` + `<datalist id="ai-model-options">`（options = exposedModels + modelMappings 去重）
- prompt: textarea，placeholder「描述你想查询的额度/用量，例如：查询 X 平台 5 小时/7 天已用百分比」
- response_sample: textarea（等宽字体），placeholder「粘贴一次真实的上游响应 JSON」
- request_info: textarea（可选），placeholder「请求方式/鉴权（可选，不知道可留空让 AI 推断）」
- 错误区：`error` 非空时显示翻译后的错误文案
- 「生成」按钮：loading 时禁用 + 旋转图标；点击调 `generateUsageScript`，成功 emit `generated(script)` + 关闭，失败设 error。

**文件：`internal/frontend/src/components/ProviderUsageModal.vue`**

1. 在 custom/general 模板的脚本编辑器 label 行（或紧邻处）加按钮：
   ```vue
   <button v-if="showScript" type="button" class="text-xs text-primary hover:underline" @click="showGenerator = true">{{ t('quota.ai_generate') }}</button>
   ```
2. 加状态 `const showGenerator = ref(false)`。
3. 在 modal 末尾挂 `<ScriptGeneratorModal v-if="showGenerator" :providerId="providerId" :exposedModels="..." :modelMappings="..." @generated="onGenerated" @close="showGenerator = false" />`。
4. `onGenerated(script)`: `form.script = script; showGenerator = false;`。
5. datalist 选项来源：从 `savedConfig` 或父级 props 获取该 provider 的 ExposedModels/ModelMappings（若 ProviderUsageModal 已有 provider 完整配置则直接用；否则传空数组，用户手输 model）。

**文件：`internal/frontend/src/composables/useI18n.ts`**

新增 keys（中英）：
- `quota.ai_generate`: 「AI 生成」/「AI Generate」
- `quota.ai_generate_title`: 「AI 生成 custom 脚本」/「AI Generate Custom Script」
- `quota.ai_generate_model`: 「模型」/「Model」
- `quota.ai_generate_prompt`: 「需求描述」/「Requirement」
- `quota.ai_generate_prompt_hint`: 「描述你想查询的额度/用量」/「Describe the quota/usage you want to query」
- `quota.ai_generate_sample`: 「响应样例（JSON）」/「Response Sample (JSON)」
- `quota.ai_generate_sample_hint`: 「粘贴一次真实的上游响应 JSON，AI 据此生成准确的字段提取」/「Paste a real upstream response JSON; the AI uses it to generate accurate field extraction」
- `quota.ai_generate_request_info`: 「请求信息（可选）」/「Request Info (optional)」
- `quota.ai_generate_submit`: 「生成」/「Generate」
- `quota.ai_generating`: 「生成中…」/「Generating…」
- 错误码翻译：`error.invalid_config`/`error.missing_credentials`/`error.request_timeout`/`error.network_error`/`error.upstream_http_error`/`error.invalid_credentials`/`error.invalid_response`/`error.script_error`/`error.internal_error`（中英）。

**测试文件：`internal/frontend/src/components/ScriptGeneratorModal.test.ts`（或并入 ProviderUsageModal.test.ts）**

1. custom 模式下「AI 生成」按钮可见；newapi 下不可见。
2. 点击打开对话框；填字段；mock `generateUsageScript` 成功 → emit `generated` + 脚本回填 form.script。
3. mock 失败（`error_code: 'invalid_response'`）→ 显示翻译文案。

#### 验证

- [ ] `npm --prefix internal/frontend test` 全绿。
- [ ] `npm --prefix internal/frontend run build` 成功。
- [ ] `grep -r "ai_generate" internal/frontend/src` 中英都命中。
- [ ] 手测：custom 模式 → AI 生成 → 脚本回填 → 测试查询可用。

---

### 任务 5：端到端验证 + spec 回写

#### 需求

**Objective（目标）** — 用真实 provider 卡片 + LLM 生成千问 custom 脚本，确认生成脚本能跑通用量查询；把证据与最终提示词回写本规格。

**Outcomes（成果）** — 本规格「验证」小节填写实测；任务 1-4 全绿。

**Evidence（证据）** — 一次真实生成的脚本 + 测试查询成功（5h/7d tier 数值与千问页面一致）。

**Constraints（约束）** — 真实 LLM 调用消耗用户 provider 额度；生成结果可能因 LLM 不同而异，spec 记录所用 model 与生成脚本摘要。

**Edge Cases（边界）** — LLM 生成脚本首次不可用（用户手动修正或重新生成）。

**Verification（验证）** — custom 模式 + AI 生成 → 脚本能 `parseRequest` + 测试查询 success:true。

#### 计划

1. 完成任务 1-4，`make test` 全绿。
2. 启动 mcc，为千问 provider 打开「用量」→ custom → 「AI 生成」。
3. 填：model（如 claude-sonnet-5）、prompt（「查询千问 token plan 5 小时/7 天已用百分比，perXxxPercentage 是 0-1 已用比例需 ×100」）、response_sample（粘贴 §2.3 fixture）、request_info（「POST form-urlencoded, Cookie={{apiKey}}, sec_token={{apiKey2}} in body」）。
4. 生成 → 回填脚本 → 人工检查 bodyType/占位符 → 保存 → 测试查询。
5. 把生成脚本的关键片段 + 测试结果填入「验证」。

#### 验证

- [ ] 任务 1-4 全绿，`make test` 通过。
- [ ] 真实 LLM 生成的脚本能 `parseRequest`。
- [ ] 测试查询 success:true，5h/7d tier 数值与千问页面一致。
- [ ] 实测记录：
  - 使用的 provider + model
  - 生成的脚本摘要（request.bodyType / 占位符 / extractor 字段映射）
  - 测试查询结果（5h/7d utilization）
