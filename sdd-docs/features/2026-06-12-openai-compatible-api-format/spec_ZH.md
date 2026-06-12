# OpenAI-Compatible API Format 兼容规格

本地页面：供应商管理 / Claude Code 代理入口  
代理入口：`/v1/messages`  
参考源站：Agnes Claude CLI 接入文档、CC-Switch v3.16.x 实现  
技术栈：Go 1.26 + SQLite + Vue 3 + 内嵌前端  
最后更新：2026-06-12  
进度：9 / 9 已实现并通过自动化验证

## 整体分析（源站分析）

### Agnes 接入文档分析

Agnes 文档的核心目标是让 Claude CLI 通过 CC-Switch 接入 OpenAI-Compatible API Gateway。文档明确要求在 CC-Switch 的 Claude Provider 中选择 `OpenAI Chat Completions`，并把请求地址配置为 OpenAI 风格的 base URL，例如 `https://apihub.agnes-ai.com/v1`。

文档推荐的 Claude CLI 接入流程如下：

1. 在 CC-Switch 顶部工具栏选择 `Claude CLI`。
2. 新增 Provider，类型选择 `Claude Provider`，再选择 `Custom Provider`。
3. `Request URL` 填 OpenAI-Compatible base URL。
4. `API Format` 选择 `OpenAI Chat Completions`。
5. 配置模型映射，例如把 Claude Code 的 Sonnet / Opus / Haiku 映射到 Agnes 模型。
6. 启用 Local Route 和 Claude route switch。

Agnes 文档还建议在自定义参数中增加：

```json
{
  "allowed_openai_params": [
    "thinking",
    "context_management"
  ],
  "litellm_settings": {
    "drop_params": true
  }
}
```

该配置的意义不是 Claude 原生协议能力，而是 OpenAI-Compatible 网关兼容策略：

1. 允许指定的 OpenAI 扩展参数穿透。
2. 自动丢弃模型不兼容的未知参数。
3. 避免 Claude CLI 通过代理调用 OpenAI-Compatible API 时因为非模型对话端点或未知字段产生超时重试。
4. 提升 Claude Code 启动速度，尤其是规避数据上报、统计类、非模型对话端点的超时重试成本。

### CC-Switch 源码分析

CC-Switch 的 Provider 使用 `apiFormat` 明确声明上游 API 协议格式：

```ts
apiFormat?: "anthropic" | "openai_chat" | "openai_responses" | "gemini_native"
```

这个设计把“Claude Code 对外入口保持 Anthropic 协议”和“上游供应商实际协议格式”解耦。Claude Code 仍然请求本地 Anthropic `/v1/messages`，代理内部根据 Provider 的 `apiFormat` 决定是否转换请求、重写上游 endpoint、转换响应和 SSE 事件。

CC-Switch 的关键链路可以概括为：

1. Claude Code 发起 Anthropic Messages 请求。
2. 本地代理读取当前 Provider 的 `apiFormat`。
3. 当 `apiFormat=anthropic` 时，基本保持 Anthropic 请求和响应透传。
4. 当 `apiFormat=openai_chat` 时，把 Anthropic Messages 请求转换为 OpenAI Chat Completions 请求，上游 endpoint 重写为 `/v1/chat/completions`。
5. 当 `apiFormat=openai_responses` 时，把 Anthropic Messages 请求转换为 OpenAI Responses 请求，上游 endpoint 重写为 `/v1/responses`。
6. 上游返回后，把 OpenAI 非流式响应或 SSE 流转换回 Claude Code 期望的 Anthropic Messages 响应格式。

源码侧可参考的实现点：

| 能力 | CC-Switch 参考位置 | 说明 |
| --- | --- | --- |
| Provider API 格式声明 | `cc-switch/src/types.ts` | `apiFormat?: "anthropic" | "openai_chat" | "openai_responses" | "gemini_native"` |
| Claude Provider 格式判断 | `cc-switch/src-tauri/src/proxy/providers/claude.rs` | 读取格式并判断是否需要转换 |
| endpoint 重写 | `cc-switch/src-tauri/src/proxy/forwarder.rs` | `openai_chat` -> `/v1/chat/completions`，`openai_responses` -> `/v1/responses` |
| Anthropic -> OpenAI Chat | `cc-switch/src-tauri/src/proxy/providers/transform.rs` | 转换 system/messages/tools/tool_choice/thinking/usage 相关字段 |
| OpenAI Chat -> Anthropic | `cc-switch/src-tauri/src/proxy/providers/transform.rs` | 转换 content/reasoning/tool_calls/finish_reason/usage |
| OpenAI SSE -> Anthropic SSE | `cc-switch/src-tauri/src/proxy/providers/streaming.rs` | 输出 `message_start`、`content_block_delta`、`message_delta`、`message_stop` 等事件 |

### 当前项目现状

当前项目已有 Claude Code 本地代理、供应商配置、模型映射、多模态切换、thinking 删除、用量统计等能力，但 Provider 尚未表达上游 API 协议格式。

当前关键现状：

| 模块 | 当前能力 | 缺口 |
| --- | --- | --- |
| `internal/config/provider.go` | 保存 Provider 基础字段、模型映射、thinking、多模态配置 | 缺少 `APIFormat` 和 OpenAI-Compatible 自定义参数 |
| `internal/admin/provider_handler.go` | 支持 Provider CRUD | 请求/响应结构未包含新字段 |
| `internal/frontend/src/components/ProviderModal.vue` | 供应商编辑表单 | 缺少 API 格式选择、OpenAI-Compatible 参数输入 |
| `internal/frontend/src/composables/useApi.ts` | 前端 Provider 类型 | 缺少新字段类型 |
| `internal/proxy/handler.go` | Claude 请求转发、模型映射、thinking 删除、SSE heartbeat、用量观察 | 缺少 Anthropic/OpenAI 请求转换、响应转换、endpoint 重写 |

目前代理转发 URL 使用 `strings.TrimSuffix(backendURL, "/") + r.URL.Path`。当 Claude Code 请求 `/v1/messages`，如果上游是 OpenAI-Compatible base URL，就会错误转发到 OpenAI 上游的 `/v1/messages`。因此 `apiFormat=openai_chat` 和 `apiFormat=openai_responses` 必须接管 endpoint 选择。

### 协议链路对比

| Provider 格式 | Claude Code 本地入口 | 上游 endpoint | 请求体 | 响应体 | SSE |
| --- | --- | --- | --- | --- | --- |
| `anthropic` | `/v1/messages` | `/v1/messages` 或供应商原始路径 | Anthropic Messages | Anthropic Messages | Anthropic SSE |
| `openai_chat` | `/v1/messages` | `/v1/chat/completions` | OpenAI Chat Completions | 转回 Anthropic Messages | 转回 Anthropic SSE |
| `openai_responses` | `/v1/messages` | `/v1/responses` | OpenAI Responses | 转回 Anthropic Messages | 转回 Anthropic SSE |

本功能的外部契约是：Claude Code 不需要知道上游格式变化。无论上游是 Anthropic 原生、OpenAI Chat Completions，还是 OpenAI Responses，Claude Code 看到的本地协议都应保持 Anthropic Messages。

### 风险结论

1. `apiFormat` 不能只作为前端开关存在，必须贯穿持久化、Admin API、代理转发、请求转换、响应转换和测试。
2. 流式转换是兼容 Claude Code 的关键风险点。仅转换非流式响应不足以支持真实使用。
3. 工具调用和 thinking/reasoning 字段是高风险字段，必须有单元测试覆盖。
4. 用量统计依赖 Anthropic usage 观察逻辑，OpenAI 响应转回 Anthropic 时必须保留或归一化 usage。
5. `gemini_native` 只做类型预留，不进入本阶段实现，避免扩大范围。

## 开发检查清单

| 顺序 | 状态 | 任务 | 输出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成 | Provider 增加 `api_format` 和 OpenAI-Compatible 参数字段 | 配置模型、默认值、校验逻辑 | Provider 单元测试 |
| 2 | 已完成 | Admin API 支持新字段 | create/update/list/get 回写一致 | API handler 测试 |
| 3 | 已完成 | 前端供应商编辑 UI 支持 API 格式 | 下拉选择、参数 JSON 编辑、错误提示 | 前端构建和手动表单验证 |
| 4 | 已完成 | 实现 Anthropic -> OpenAI Chat 请求转换 | 独立转换模块 | 请求转换单元测试 |
| 5 | 已完成 | 实现 OpenAI Chat -> Anthropic 响应和 SSE 转换 | 非流式与流式转换 | 响应/SSE 单元测试 |
| 6 | 已完成 | 实现 Anthropic -> OpenAI Responses 请求转换 | 独立转换模块 | 请求转换单元测试 |
| 7 | 已完成 | 实现 OpenAI Responses -> Anthropic 响应和 SSE 转换 | 非流式与流式转换 | 响应/SSE 单元测试 |
| 8 | 已完成 | 代理入口集成 API 格式分发和 endpoint 重写 | `handler.go` 按 `api_format` 分发 | 代理集成测试 |
| 9 | 已完成 | Agnes/OpenAI-Compatible 手动验证与文档化 | 验证记录和使用说明 | Mock upstream 全链路验证 |

## 需求

### 交付物

1. Provider 配置支持：
   - `api_format`: `"anthropic" | "openai_chat" | "openai_responses"`。
   - OpenAI-Compatible 自定义参数配置，按任意 JSON 对象保存和透传，Agnes 文档中的 `allowed_openai_params` 和 `litellm_settings.drop_params` 只是推荐模板。
2. Admin API 支持新字段创建、更新、查询和列表回显。
3. 前端供应商编辑支持选择 API 格式，并在 OpenAI-Compatible 格式下编辑自定义参数。
4. 代理支持按 Provider `api_format` 选择上游 endpoint。
5. 代理支持 Anthropic Messages 与 OpenAI Chat Completions 的双向转换。
6. 代理支持 Anthropic Messages 与 OpenAI Responses 的双向转换。
7. 流式响应转换为 Claude Code 可识别的 Anthropic SSE 事件。
8. 用量统计在 OpenAI-Compatible 响应下继续可用。
9. 单元测试、集成测试和手动验证记录。

### 目录结构

建议新增或调整以下文件。最终实现可根据现有代码组织微调，但需要保持转换逻辑可测试、可复用。

```text
internal/
  config/
    provider.go
  admin/
    provider_handler.go
  proxy/
    handler.go
    transform/
      anthropic_openai_chat.go
      anthropic_openai_chat_test.go
      anthropic_openai_responses.go
      anthropic_openai_responses_test.go
      sse.go
      sse_test.go
  frontend/
    src/
      components/
        ProviderModal.vue
      composables/
        useApi.ts
```

### 数据结构

Provider 建议增加：

```go
type APIFormat string

const (
    APIFormatAnthropic       APIFormat = "anthropic"
    APIFormatOpenAIChat      APIFormat = "openai_chat"
    APIFormatOpenAIResponses APIFormat = "openai_responses"
)

type Provider struct {
    // existing fields...
    APIFormat            APIFormat       `json:"api_format"`
    OpenAIExtraParams    map[string]any  `json:"openai_extra_params,omitempty"`
    ClaudeCodeCompatHint *bool           `json:"claude_code_compat_hint,omitempty"`
}
```

默认值规则：

1. 老配置没有 `api_format` 时默认 `anthropic`。
2. 新建 Provider 未选择时默认 `anthropic`。
3. `openai_chat` 和 `openai_responses` 才展示和使用 OpenAI-Compatible 自定义参数。
4. 不接受未知 `api_format`，包括本阶段暂不实现的 `gemini_native`。

### 协议路径规则

| `api_format` | 上游 URL 计算 |
| --- | --- |
| `anthropic` | `trim(api_url) + 原始请求路径`，保持现有行为 |
| `openai_chat` | 推荐 `trim(api_url) + /chat/completions`；若用户已填写完整 `/chat/completions` endpoint，则直接使用该 URL |
| `openai_responses` | 推荐 `trim(api_url) + /responses`；若用户已填写完整 `/responses` endpoint，则直接使用该 URL |

注意：前端提示用户填写 base URL，例如 `https://example.com/v1`。实现上需要兼容用户直接填写完整 endpoint，例如 `https://example.com/v1/chat/completions` 或 `https://example.com/v1/responses`。

### 约束条件

1. Claude Code 对本地代理的入口协议必须保持 Anthropic Messages，不改变用户使用方式。
2. `anthropic` 默认路径必须保持现有行为，避免破坏已有供应商。
3. OpenAI-Compatible 转换逻辑必须独立于 HTTP handler，便于单元测试。
4. `gemini_native` 不在本阶段实现，不能出现在可选 UI 中。
5. 不能为了兼容 OpenAI 而删除现有模型映射、多模态切换、thinking 删除和用量统计能力。
6. 自定义参数必须经过 JSON 校验，不能把非法 JSON 保存为 Provider 配置。
7. 认证头必须统一为 `Authorization: Bearer <token>`，无论原始请求使用的是 `X-Api-Key` 还是 `Authorization`。
8. 对未知字段的丢弃策略只应用到 OpenAI-Compatible 请求构造，不影响原生 Anthropic 供应商。
9. Anthropic 专属请求头和查询参数在转发到 OpenAI-Compatible 上游前必须剥离。OpenAI 专属响应头在返回 Claude Code 前必须剥离。主请求路径和重试路径（`tryRectify`）必须共用同一份 header 过滤逻辑。
10. SSE 流式转换必须使用显式的 `bufio.Scanner` 缓冲区（至少 128KB），避免大块 SSE 数据触发 `bufio.ErrTooLong`。
11. SSRF 防护（`isInternalIP`）必须执行实际 DNS 解析，并使用 `net.IP` 标准库方法（`IsPrivate`、`IsLoopback`、`IsLinkLocalUnicast`、`IsLinkLocalMulticast`、`IsUnspecified`）覆盖 IPv4 和 IPv6。无法解析的域名必须拒绝。
12. 所有返回 Provider 数据的 Admin API 端点（包括复制端点）必须通过 `maskToken()` 脱敏。除 `/reveal-token` 外，任何端点不得返回明文 Token。

### 边界情况

1. Provider 旧配置没有 `api_format`。
2. OpenAI-Compatible 上游返回非 SSE 错误体。
3. OpenAI Chat 流式响应中先返回 role，再返回 content delta。
4. OpenAI Chat 返回 `tool_calls` 分片。
5. OpenAI Responses 返回多类型 output item。
6. 上游 usage 字段缺失、字段名差异或 cache token 字段不存在。
7. Claude 请求包含 `thinking`，但目标 OpenAI-Compatible 模型不支持。
8. 自定义参数 JSON 为空、非法、或包含非对象顶层结构。
9. 用户将 OpenAI-Compatible Provider 误配置为 `anthropic`。
10. Anthropic 专属请求头（`Anthropic-Version`、`Anthropic-Beta`）和查询参数（`beta=true`）泄漏到 OpenAI-Compatible 上游，触发上游 WAF/CDN 返回 403。
11. OpenAI-Compatible 上游返回的 OpenAI 专属响应头（`Openai-*`、`X-Ratelimit-*`）干扰 Claude Code 客户端。
12. SSE 数据块超过 `bufio.Scanner` 默认 64KB 行限制，导致 `bufio.ErrTooLong` 和流中断。
13. Provider 复制端点在响应中返回明文 `APIToken`，向前端泄露密钥。
14. SSRF 防护仅做主机名字符串匹配，允许 DNS rebinding 攻击，且遗漏 IPv6 私有地址段。

### 非目标

1. 本阶段不实现 `gemini_native`。
2. 本阶段不实现 OpenAI 模型列表自动拉取。
3. 本阶段不改造 Claude Code 客户端安装流程。
4. 本阶段不实现跨供应商参数模板市场。
5. 本阶段不改变现有 GitHub/GitLab Release 自动构建逻辑。

### 审核结论

1. OpenAI-Compatible 自定义参数只需要支持任意 JSON 对象；前端不需要为 `allowed_openai_params` 和 `litellm_settings.drop_params` 单独做结构化表单。
2. `api_url` 需要兼容用户直接填写完整 endpoint，例如 `https://example.com/v1/chat/completions`；但提示信息仍推荐填写 base URL，例如 `https://example.com/v1`。
3. `openai_responses` 首版需要完整支持 tools，不只支持文本、thinking/reasoning 和 usage。

## 任务详情

### 任务 1：配置模型与兼容默认值

#### 需求

**Objective（目标）** — 为 Provider 增加明确的上游 API 格式声明，让系统能够区分 Anthropic 原生、OpenAI Chat Completions 和 OpenAI Responses。

**Outcomes（成果）** — Provider 支持 `api_format` 字段，默认值为 `anthropic`；支持 OpenAI-Compatible 自定义参数字段；旧配置读取后仍能正常工作。

**Evidence（证据）** — 单元测试覆盖空字段默认值、合法枚举、非法枚举、旧配置兼容；代码中有明确常量或类型定义，避免散落字符串。

**Constraints（约束）** — 不引入 `gemini_native` 可用路径；不得破坏现有 Provider JSON 结构；旧 Provider 不需要迁移脚本也能读取。

**Edge Cases（边界）** — 配置文件缺字段、字段为空字符串、字段为未知值、自定义参数为空对象、自定义参数为非法类型。

**Verification（验证）** — 运行 Provider 配置相关测试，确认旧配置默认 `anthropic`，未知 `api_format` 会被拒绝或回退到明确错误。

#### 计划

1. 在 `internal/config/provider.go` 中定义 `APIFormat` 类型和三种合法值。
2. 为 Provider 增加 `APIFormat` 和 `OpenAIExtraParams` 字段。
3. 在 Provider 创建、更新或加载流程中设置默认值。
4. 增加字段校验函数，统一判断合法格式。
5. 补充配置序列化和反序列化测试。

#### 验证

- [x] 旧 Provider JSON 缺少 `api_format` 时读取为 `anthropic`。
- [x] `openai_chat` 和 `openai_responses` 能成功保存和读取。
- [x] 未知值不能静默进入代理执行路径。
- [x] `OpenAIExtraParams` 空值不会影响 Anthropic 原生供应商。

### 任务 2：Admin API 与持久化字段

#### 需求

**Objective（目标）** — 让 Provider 管理接口完整支持 API 格式和 OpenAI-Compatible 参数。

**Outcomes（成果）** — create、update、list、get 的请求和响应都包含 `api_format`；OpenAI-Compatible 参数能保存、更新、清空和回显。

**Evidence（证据）** — Admin API handler 测试覆盖创建、更新、查询、非法 JSON、非法枚举；前端类型与后端 JSON 字段一致。

**Constraints（约束）** — 不能改变现有字段含义；更新 Provider 时未提交的新字段要有明确策略，避免误清空。

**Edge Cases（边界）** — PATCH/PUT 语义差异、空字符串格式、参数 JSON 为数组、参数 JSON 为字符串、旧前端提交不包含新字段。

**Verification（验证）** — 使用 handler 测试或本地 API 请求验证 Provider CRUD 全链路。

#### 计划

1. 更新 Admin Provider request/response 结构。
2. 在创建和更新入口调用 `api_format` 校验。
3. 对 OpenAI-Compatible 参数做顶层对象校验。
4. 保持 list/get 返回字段一致。
5. 补充 API handler 测试。

#### 验证

- [x] 新建 Provider 可保存 `api_format=openai_chat`。
- [x] 更新 Provider 可从 `openai_chat` 切回 `anthropic`。
- [x] 非法 `api_format` 返回明确错误。
- [x] 非对象 JSON 参数返回明确错误。
- [x] Provider 复制端点返回 `api_token_mask` 而非明文 Token。

### 任务 3：前端供应商编辑 UI

#### 需求

**Objective（目标）** — 在供应商编辑中增加 API 格式选择，让用户能把 Provider 配置为 OpenAI-Compatible 上游。

**Outcomes（成果）** — Provider 表单包含 API 格式选择；支持 `Anthropic`、`OpenAI Chat Completions`、`OpenAI Responses`；OpenAI-Compatible 参数可编辑并校验。

**Evidence（证据）** — 前端构建通过；手动验证新增和编辑 Provider 时字段展示、保存、回显一致；非法 JSON 有可理解的错误提示。

**Constraints（约束）** — 不展示 `gemini_native`；不把 OpenAI 参数编辑暴露给 `anthropic` 模式造成误解；保持现有表单布局和交互风格。

**Edge Cases（边界）** — 用户切换 API 格式后已有参数是否保留；JSON 编辑器为空；保存失败后表单状态保留；旧 Provider 打开时没有 `api_format`。

**Verification（验证）** — 运行前端构建，并手动创建/编辑三类格式 Provider。

#### 计划

1. 更新 `useApi.ts` Provider 类型定义。
2. 在 `ProviderModal.vue` 增加 API 格式选择控件。
3. 当格式为 OpenAI-Compatible 时显示自定义参数 JSON 输入区域。
4. 提供默认参数模板，可覆盖 Agnes 文档推荐值。
5. 保存前校验 JSON 顶层对象。

#### 验证

- [x] 新增 Provider 默认 `Anthropic`。
- [x] 选择 `OpenAI Chat Completions` 后显示参数编辑区。
- [x] 非法 JSON 阻止提交。
- [x] 编辑已有 Provider 时 `api_format` 和参数正确回显。

### 任务 4：OpenAI Chat Completions 请求转换

#### 需求

**Objective（目标）** — 把 Claude Code 发来的 Anthropic Messages 请求转换为 OpenAI Chat Completions 请求。

**Outcomes（成果）** — 支持 system、messages、文本、多模态图片、tools、tool_choice、stream、max_tokens、temperature、top_p、stop_sequences、thinking/reasoning 相关字段转换。

**Evidence（证据）** — 转换模块单元测试使用代表性 Anthropic 请求 fixture，断言 OpenAI Chat 请求结构符合预期。

**Constraints（约束）** — 转换逻辑必须独立可测；不能直接在 HTTP handler 中拼 JSON；模型映射仍由现有逻辑生效。

**Edge Cases（边界）** — system 为字符串或 block 数组；content 为字符串或 block 数组；tool_use 和 tool_result 交错；图片 source 类型不支持；thinking 字段存在但上游不支持。

**Verification（验证）** — 使用单元测试覆盖普通文本、工具调用、多模态、thinking、自定义参数合并。

#### 计划

1. 新增独立 transform 包或文件。
2. 定义 Anthropic 请求和 OpenAI Chat 请求的最小结构体或使用 `map[string]any` 加强校验。
3. 实现 system/messages/content block 转换。
4. 实现 tools 和 tool_choice 转换。
5. 合并 `OpenAIExtraParams`，并按 `allowed_openai_params` / `drop_params` 规则处理兼容参数。
6. 增加请求转换测试。

#### 验证

- [x] 文本请求转换为 `messages` 数组。
- [x] `tools` 转换为 OpenAI `tools: [{type:function,...}]`。
- [x] `tool_result` 转换为 role `tool` 消息。
- [x] `stream=true` 被保留，并按需要补充 `stream_options.include_usage=true`。
- [x] Agnes 推荐参数能合并到请求体。

### 任务 5：OpenAI Chat Completions 响应与 SSE 转换

#### 需求

**Objective（目标）** — 把 OpenAI Chat Completions 的非流式响应和 SSE 流转换为 Claude Code 期望的 Anthropic Messages 响应。

**Outcomes（成果）** — 非流式响应输出 Anthropic `message`；流式响应输出 Anthropic SSE 事件序列；支持 content、reasoning、tool_calls、finish_reason、usage。

**Evidence（证据）** — 响应转换测试覆盖非流式和 SSE；Claude Code 可消费转换后的流式事件。

**Constraints（约束）** — 不能把 OpenAI SSE 原样透传给 Claude Code；错误响应应尽量保持原状态码和可读错误体；usage 需要兼容现有统计观察器。

**Edge Cases（边界）** — SSE 分片为空、`[DONE]`、role-only delta、tool_calls arguments 分片、reasoning_content 分片、usage 最后才出现。

**Verification（验证）** — 使用 fixture 模拟 OpenAI Chat SSE，断言输出包含 `message_start`、`content_block_start`、`content_block_delta`、`message_delta`、`message_stop`。

#### 计划

1. 实现非流式 OpenAI Chat 响应到 Anthropic message 的转换。
2. 实现 OpenAI Chat SSE parser。
3. 将 delta 事件映射为 Anthropic SSE content block 事件。
4. 汇总 tool_calls arguments 分片并输出 Anthropic `tool_use`。
5. 映射 usage 到 Anthropic usage 字段。
6. 补充响应和 SSE 测试。

#### 验证

- [x] 非流式文本响应转换为 Anthropic `content: [{type:"text"}]`。
- [x] OpenAI `tool_calls` 转换为 Anthropic `tool_use`。
- [x] SSE 输出事件顺序符合 Anthropic Messages 流式协议。
- [x] usage 可被现有观察器读取或经转换后统计。

### 任务 6：OpenAI Responses 请求转换

#### 需求

**Objective（目标）** — 支持把 Anthropic Messages 请求转换为 OpenAI Responses API 请求，为使用新 OpenAI Responses 格式的供应商预留兼容路径。

**Outcomes（成果）** — Provider 选择 `openai_responses` 时，上游请求发送到 `/v1/responses`，请求体使用 Responses API 结构表达输入、工具、流式和推理参数。

**Evidence（证据）** — 请求转换单元测试覆盖文本、多轮消息、tools、stream、reasoning 参数。

**Constraints（约束）** — 不影响 `openai_chat` 转换；首版需要完整支持 tools；不支持的 Anthropic block 需要显式错误或降级。

**Edge Cases（边界）** — Responses input item 与 Anthropic content block 不完全同构；工具结果表达差异；reasoning 参数名称差异；多模态图片格式差异。

**Verification（验证）** — fixture 输入转换后符合 Responses API 预期结构，并能被 mock 上游接收。

#### 计划

1. 新增 Responses 请求转换函数。
2. 将 Anthropic system/messages 转为 Responses `input`。
3. 转换 tools、tool_choice、stream、max_output_tokens 或等价字段。
4. 处理 thinking/reasoning 参数映射。
5. 增加 Responses 请求转换测试。

#### 验证

- [x] `api_format=openai_responses` 使用 `/v1/responses` endpoint。
- [x] 文本消息转换为 Responses `input`。
- [x] tools 能转换为 Responses 工具定义。
- [x] stream 参数能正确传递。

### 任务 7：OpenAI Responses 响应与 SSE 转换

#### 需求

**Objective（目标）** — 把 OpenAI Responses API 的响应和事件流转换为 Anthropic Messages 响应，保证 Claude Code 可消费。

**Outcomes（成果）** — 支持 Responses 非流式 output 转 Anthropic content；支持 Responses 流式事件转 Anthropic SSE；保留 usage 和 stop reason。

**Evidence（证据）** — 单元测试覆盖 Responses completed、output_text、reasoning、tool_call、usage 等典型结构。

**Constraints（约束）** — 不把 Responses 原始事件直接透传给 Claude Code；遇到未知事件应可忽略或记录，不应中断普通文本输出。

**Edge Cases（边界）** — Responses output item 多类型混合；工具调用参数分片；事件顺序和 Anthropic SSE 事件顺序不同；usage 缺失。

**Verification（验证）** — 使用 Responses fixture 断言转换后的 Anthropic 响应和 SSE 事件序列。

#### 计划

1. 实现 Responses 非流式响应解析。
2. 实现 Responses 流式事件 parser。
3. 将 output text/reasoning/tool_call 映射到 Anthropic content block。
4. 映射 completion 状态到 Anthropic stop_reason。
5. 映射 usage 到 Anthropic usage。
6. 补充测试。

#### 验证

- [x] Responses 文本输出转换为 Anthropic text block。
- [x] Responses 工具调用转换为 Anthropic tool_use block。
- [x] Responses stream 转换后的 SSE 事件顺序稳定。
- [x] 未知事件不会破坏已生成内容。

### 任务 8：代理入口集成、路径重写与用量统计

#### 需求

**Objective（目标）** — 在现有代理入口中按 Provider `api_format` 分发请求转换、上游 endpoint、响应转换和用量统计。

**Outcomes（成果）** — `anthropic` 保持现有行为；`openai_chat` 和 `openai_responses` 自动重写 endpoint 并进行双向协议转换；用量统计继续工作。

**Evidence（证据）** — 集成测试使用 mock upstream 验证实际请求路径、请求体、响应体和 SSE；现有 Anthropic 代理测试不回退。

**Constraints（约束）** — 不改变 Claude Code 本地入口；不破坏 heartbeat；错误响应应保留可排查信息；转换失败要返回明确错误。

**Edge Cases（边界）** — 上游返回 4xx/5xx；上游中途断开 SSE；转换过程中出现未知 content block；Provider 未启用或缺少 token。

**Verification（验证）** — 运行代理 handler 测试和手动 curl/mock upstream 验证三种格式。

#### 计划

1. 在 `handler.go` 中读取 active Provider 的 `APIFormat`。
2. 根据格式构造上游 URL。
3. 在发出请求前调用对应请求转换。
4. 在收到响应后按格式决定透传或转换。
5. 确保 SSE heartbeat 与转换后的 Anthropic SSE 协同工作。
6. 保持 usage observer 能读取最终 Anthropic usage。
7. 增加代理集成测试。

#### 验证

- [x] `anthropic` 请求仍转发到原始 `/v1/messages`。
- [x] `openai_chat` 请求转发到 `/v1/chat/completions`。
- [x] `openai_responses` 请求转发到 `/v1/responses`。
- [x] 三种格式下模型映射都生效。
- [x] OpenAI-Compatible 响应能更新用量统计。
- [x] Anthropic 专属请求头（`Anthropic-Version`、`Anthropic-Beta`）不会转发到 OpenAI-Compatible 上游。
- [x] Anthropic 专属查询参数（`beta=true`）不会拼接到 OpenAI-Compatible 上游 URL。
- [x] OpenAI 专属响应头不会转发给 Claude Code。
- [x] 重试路径（`tryRectify`）与主请求路径使用相同的 header 过滤逻辑。
- [x] SSE 流式转换使用 128KB scanner 缓冲区，不会因大数据块失败。
- [x] SSRF 防护执行 DNS 解析并检查 IPv4/IPv6 私有/保留地址段。

### 任务 9：Agnes/OpenAI-Compatible 手动验证与文档化

#### 需求

**Objective（目标）** — 验证真实 OpenAI-Compatible 供应商配置可以被 Claude Code 使用，并把使用方式写入项目文档或规格验证记录。

**Outcomes（成果）** — 使用 Agnes 风格配置完成一次 Claude Code 请求；记录 Provider 配置、模型映射、自定义参数、请求结果和已知限制。

**Evidence（证据）** — 手动验证日志包含本地代理启动命令、Provider 关键配置、Claude Code 请求结果、上游响应路径和用量统计结果。

**Constraints（约束）** — 不在文档中泄露 API Key；真实供应商不可用时使用 mock upstream 作为最低验证；说明 `openai_responses` 的验证覆盖程度。

**Edge Cases（边界）** — 网络不可达、API Key 无效、模型名错误、供应商不支持 tools/thinking、上游返回非标准 OpenAI-Compatible 字段。

**Verification（验证）** — 完成至少一次 `openai_chat` 真实或 mock 全链路验证；完成包含 tools 的 `openai_responses` mock 全链路验证。

#### 计划

1. 准备 Agnes 风格 Provider 配置示例。
2. 配置模型映射，例如 Sonnet/Opus/Haiku 指向同一个 OpenAI-Compatible 模型。
3. 填入推荐自定义参数：
   - `allowed_openai_params`: `["thinking", "context_management"]`
   - `litellm_settings.drop_params`: `true`
4. 启动本地代理并通过 Claude Code 或 curl 发起请求。
5. 检查响应、SSE、usage 和日志。
6. 将验证结论补充到文档。

#### 验证

- [x] Agnes 风格 `openai_chat` Provider 可保存并启用。
- [x] Claude Code 请求能经本地代理转到 OpenAI-Compatible endpoint。
- [x] 用户填写完整 `/chat/completions` endpoint 时仍能正确转发。
- [x] `openai_responses` mock 验证覆盖 tools 请求和响应转换。
- [x] 非模型对话端点不会因为 OpenAI-Compatible 配置导致长时间超时重试。
- [x] 文档说明该能力可减少数据上报接口超时重试带来的启动等待。
- [x] 验证记录不包含任何敏感 token。
