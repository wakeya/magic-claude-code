# 主动式 Content Block 清洗规格

本地页面：`internal/proxy/handler.go` transformRequest / `internal/proxy/rectifier.go` matchErrorPattern  
代理入口：`internal/proxy/handler.go` ServeHTTP  
参考源站：`~/workspace/open-software/cc-switch/src-tauri/src/proxy/`（body_filter.rs、copilot_optimizer.rs、forwarder.rs），`~/workspace/open-software/kimi-code/packages/kosong/src/providers/`  
技术栈：Go 1.26 标准库  
最后更新：2026-06-15  
进度：0 / 5 已规划  

## 整体分析（源站分析）

### 问题

将 Claude Code 请求代理到 Kimi（kimi-k2.6、kimi-k2.7）的 Anthropic 兼容端点时，上游返回 HTTP 400：

```json
{"error":{"type":"invalid_request_error","message":"failed to convert tool result content: unsupported content type in ContentBlockParamUnion: tool_reference"}}
```

现有反应式 rectifier（`internal/proxy/rectifier.go`）未能捕获此错误，因为 `matchErrorPattern` 只匹配 `"invalid request"` / `"invalid_request_error"` 等泛化短语。虽然错误 JSON 包含 `"type":"invalid_request_error"`，但 `extractErrorMessage` 提取的是内部 `message` 字段（`"failed to convert tool result content..."`），该字段不包含任何已匹配的短语。因此 `hasGenericInvalidRequestPhrase` 返回 false，模式解析为 `PatternNone`，不执行任何清理。

### 根因

Claude Code 2.1.x 在 `tool_result.content` 数组中注入 `tool_reference` 内容块。这是客户端侧元数据块（不在 Anthropic 公开 API 规范中），标记产生结果的工具。Kimi 的 Anthropic 兼容端点严格校验内容类型，拒绝 `tool_reference`。

### 现有防御层

1. **transformRequest**（`handler.go:508`）：处理模型映射、thinking 剥离和 OpenAI 格式转换。不清洗非标准内容块。

2. **Rectifier**（`rectifier.go`）：反应式 400 恢复，通过 `tryRectify` 实现。其中 `cleanUnknownContentTypes` 使用白名单递归剥离未知内容块。但仅在 `matchErrorPattern` 返回非 None 模式时触发。

3. **模式匹配缺口**：`hasGenericInvalidRequestPhrase` 检查 `"invalid request"`、`"invalid_request_error"`、`"invalid params"`、`"illegal request"`。提取的消息 `"failed to convert tool result content: unsupported content type..."` 不匹配任何一个。

### 参考实现

**cc-switch**（`src-tauri/src/proxy/`）：
- `body_filter.rs`：转发前主动剥离请求体中所有 `_` 前缀的私有参数。
- `copilot_optimizer.rs`：对 OpenAI 端点主动剥离 thinking 块；将孤立 tool_result 转为 text。
- `thinking_rectifier.rs`：反应式恢复，错误模式匹配范围广，包括 `"illegal request"`、`"signature"` 模式等。
- `forwarder.rs`：管道在首次请求前应用主动清洗，然后在 400 时做反应式恢复。

**kimi-code**（`packages/kosong/src/providers/kimi.ts`）：
- 对 Kimi 完全不使用 Anthropic 格式 — 仅使用 OpenAI Chat Completions 格式。
- think 部分被提取到 `reasoning_content` 字段（Moonshot 专有扩展）。
- 仅支持 `text`、`image_url`、`audio_url`、`video_url`、`think` 内容类型，未知类型被静默丢弃。

### 策略

**双层修复**：

1. **主动清洗**（第 1 层 — 新增）：对开启了 `strip_unknown_content_blocks` 的供应商（且 API 格式为 `anthropic`），在首次请求前从 `messages[].content` 和嵌套 `tool_result.content` 中剥离非标准内容块。完全消除 400 往返。

2. **反应式模式扩展**（第 2 层 — 修复）：扩展 `matchErrorPattern`，使 `"unsupported content type"` 被识别为通用 bad-request 触发器，现有 rectifier 能捕获剩余边缘情况。

### 供应商门控

主动清洗不能影响已经兼容良好的供应商。考虑了两种方案：

- **方案 A：按上游 URL 门控** — 仅在上游主机不是 `api.anthropic.com` 时剥离。已否决：范围过宽 — GLM-5.2、MiniMax-M3、MiMo-V2.5-Pro 已兼容非标准内容块，剥离可能损失未来的厂商扩展能力。

- **方案 B：新增供应商能力开关** — 在 Provider 配置中添加 `StripUnknownContentBlocks` 布尔值，默认 false。仅对已知有严格内容类型校验的供应商（如 Kimi）开启。

**决策**：方案 B。Kimi 的 `tool_reference` 400 是特定供应商的严格校验问题，不是所有第三方供应商的通病。开关保持其他兼容供应商的原样透传行为不变。

### 范围

- **纳入范围**：请求管道中的主动内容块清洗；反应式错误模式修复。
- **排除范围**：OpenAI 格式的 thinking 历史标准化（独立功能）；私有参数剥离；cache_control 剥离（已处理）。

## 开发检查清单

- [x] 修复 `matchErrorPattern` 识别 `"unsupported content type"` 模式
- [x] 新增 `StripUnknownContentBlocks` 供应商能力开关（默认 false）
- [x] 在 `transformRequest` 中按供应商开关门控主动清洗
- [x] 编写覆盖 5 个场景的单元测试
- [x] 更新现有测试以覆盖行为变化

## 需求

### 需求 1：反应式错误模式扩展

rectifier 必须识别上游 400 错误消息中的 `"unsupported content type"`，并触发现有的 `cleanUnknownContentTypes` 清理。

### 需求 2：主动内容块清洗（按需开启）

当供应商设置了 `strip_unknown_content_blocks: true` 时，代理必须在首次请求前从 `messages[].content`（包括嵌套 `tool_result.content`）中剥离非标准内容块。未设置此开关的供应商（默认 false）继续原样透传。

### 需求 3：Anthropic-compatible 供应商默认透传

转发到任何未开启 `strip_unknown_content_blocks` 的 Anthropic-compatible 供应商的请求，不得被主动修改。兼容性问题由 rectifier 在 400 响应后被动处理。

### 需求 4：OpenAI 格式委托转换层

使用 `openai_chat` 或 `openai_responses` API 格式的供应商不调用主动清洗。其请求完全由 OpenAI 转换层处理。

## 任务详情

### 任务 1：修复反应式错误模式匹配

#### 需求

**Objective（目标）** — 扩展 `hasGenericInvalidRequestPhrase` 或添加新的模式检查，使 `"unsupported content type"` 触发 `PatternGenericBadRequest`。

**Outcomes（成果）** — rectifier 的反应式清理路径对 Kimi 的特定错误消息生效。

**Evidence（证据）** — 单元测试：输入实际的 Kimi 错误 JSON，断言 `matchErrorPattern` 返回 `PatternGenericBadRequest`。

**Constraints（约束）** — 不得过度匹配：仅匹配明确指示内容类型或转换错误的消息。

**Edge Cases（边界）** — 消息变体：`"unsupported content type in ContentBlockParamUnion"`、`"unsupported content type"`、`"unknown content type"`。

**Verification（验证）** — `go test ./internal/proxy/... -run TestMatchErrorPattern -v`

#### 计划

1. 在 `rectifier.go` 的 `hasGenericInvalidRequestPhrase` 中添加 `"unsupported content type"` 和 `"unknown content type"`。
2. 添加使用实际 Kimi 错误消息的单元测试。

#### 验证

运行 `go test ./internal/proxy/... -run TestMatchErrorPattern -v`，确认新用例通过。

### 任务 2：添加主动内容块清洗

#### 需求

**Objective（目标）** — 在 `transformRequest` 中，当 `provider.StripUnknownContentBlocks == true` 且 `apiFormat == anthropic` 时，在首次请求前从 messages 中剥离非标准内容块。

**Outcomes（成果）** — 需要此功能的供应商（如 Kimi）的 `tool_reference` 和其他非标准内容块零延迟处理。

**Evidence（证据）** — 单元测试：包含 `tool_reference` 的 `tool_result.content` 在 `transformRequest` 后被清洗为仅标准类型。

**Constraints（约束）** — 仅在 `providerAPIFormat(provider) == config.APIFormatAnthropic && provider.StripUnknownContentBlocks` 时应用。OpenAI Chat/Responses 格式委托转换层处理。必须使用与 `knownContentTypes` 相同的白名单。

**Edge Cases（边界）** — 清洗后内容数组为空；深层嵌套 `tool_result.content.tool_result.content`；非数组内容（字符串）。

**Verification（验证）** — `go test ./internal/proxy/... -run TestProactiveClean -v`

#### 计划

1. 添加 `proactiveCleanUnknownContentTypes(req map[string]any)` 复用现有 `filterContentBlocks` 逻辑。
2. 在 `transformRequest` 中当 `providerAPIFormat == anthropic && provider.StripUnknownContentBlocks` 时调用。
3. 添加 `StripUnknownContentBlocks` 到 Provider 配置、管理后台 API、SQLite 存储、前端。

#### 验证

运行单元测试；确认 strip=false 时保持原样透传；strip=true 时清洗未知类型。

### 任务 3：测试覆盖

#### 需求

**Objective（目标）** — 为反应式模式修复和主动清洗提供全面测试覆盖。

**Outcomes（成果）** — 所有新代码路径都有测试覆盖。

**Evidence（证据）** — `go test -cover ./internal/proxy/...` 显示覆盖率无回归。

**Verification（验证）** — `go test ./internal/proxy/... -v`
