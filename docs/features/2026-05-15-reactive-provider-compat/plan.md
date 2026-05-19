# 反应式供应商兼容性错误恢复 — 实现计划

**目标：** 添加反应式错误恢复：检测供应商 400 错误 → 针对性清理请求 → 重试一次。

**架构：** `internal/proxy/rectifier.go` 中的纯函数负责模式匹配和清理。Handler 仅在上游返回 400 时调用。

**技术栈：** Go 1.26，标准 `encoding/json`，无新依赖。

---

## 文件规划

新建：
1. `internal/proxy/rectifier.go` — 错误模式匹配和请求清理函数。
2. `internal/proxy/rectifier_test.go` — 模式匹配、清理和边界情况测试。

修改：
1. `internal/proxy/handler.go` — 在上游响应后添加 400 重试逻辑。

## 任务 1：错误模式检测

**文件：**
1. 新建：`internal/proxy/rectifier.go`
2. 新建：`internal/proxy/rectifier_test.go`

- [ ] **步骤 1：编写失败的模式检测测试**

测试：
1. `TestMatchErrorPattern_ToolValidation` — 匹配 `"function name or parameters is empty"`
2. `TestMatchErrorPattern_ToolValidationInvalidParams` — 匹配含 tool 上下文的 `"invalid params"`
3. `TestMatchErrorPattern_ThinkingSignature` — 匹配 `"Invalid 'signature' in 'thinking' block"`
4. `TestMatchErrorPattern_ExpectedThinking` — 匹配 `"Expected thinking or redacted_thinking, but found tool_use"`
5. `TestMatchErrorPattern_GenericInvalidRequest` — 单独的 `"invalid_request_error"` 不触发自动恢复
6. `TestMatchErrorPattern_KimiToolValidation` — 匹配含 tool/schema 上下文的 Kimi `Invalid request Error`
7. `TestMatchErrorPattern_NoMatch` — 不匹配 `"rate limit exceeded"` 或 `"timeout"`
8. `TestMatchErrorPattern_NilBody` — 处理 nil/空错误体
9. `TestMatchErrorPattern_NestedJSON` — 匹配 `{"error":{"message":"..."}}` 中的嵌套错误

运行：

```bash
go test ./internal/proxy -run TestMatchErrorPattern -v
```

预期：因函数不存在而失败。

- [ ] **步骤 2：实现模式匹配**

```go
type ErrorPattern int

const (
    PatternNone          ErrorPattern = iota
    PatternToolValidation
    PatternThinkingSignature
)

func matchErrorPattern(errorBody []byte) ErrorPattern
```

实现：
1. 将错误体解析为 JSON。
2. 尝试 `error.message`、`error.error.message`、原始字符串 — 按此顺序。
3. 转小写后匹配模式字符串；普通 `invalid_request_error`、`invalid params`、`非法请求` 必须同时带明确 tool/schema 或 thinking/signature 上下文才可触发恢复。
4. 返回第一个匹配的模式。

- [ ] **步骤 3：验证**

```bash
go test ./internal/proxy -run TestMatchErrorPattern -v
```

## 任务 2：Tool 定义清理

**文件：**
1. 修改：`internal/proxy/rectifier.go`
2. 修改：`internal/proxy/rectifier_test.go`

- [ ] **步骤 1：编写失败的清理测试**

测试：
1. `TestCleanTools_移除CacheControl` — 移除 tools 上的 `cache_control` 字段
2. `TestCleanTools_填充空InputSchema` — 空 `input_schema: {}` 变为 `{"type":"object","properties":{}}`
3. `TestCleanTools_填充缺失InputSchema` — 缺失 `input_schema` 时添加
4. `TestCleanTools_移除Schema元数据` — 移除 `$schema`、`$id`、`$comment`、`additionalProperties`
5. `TestCleanTools_保留核心字段` — `name`、`description`、`properties`、`required` 不变
6. `TestCleanTools_保留有效Schema` — 含 properties 的有效 `input_schema` 保持原样
7. `TestCleanTools_无Tools字段` — 不含 `tools` 的请求体返回不变
8. `TestCleanTools_无效JSON` — 返回原始请求体

运行：

```bash
go test ./internal/proxy -run TestCleanTools -v
```

- [ ] **步骤 2：实现 tool 清理**

```go
func cleanTools(body []byte) ([]byte, bool)
```

规则：
1. 解析为 `map[string]any`。
2. 获取 `tools` 数组。
3. 对每个 tool：
   - 移除 `cache_control`。
   - 获取 `input_schema`（map）。若 nil 或空，设为 `{"type":"object","properties":{}}`。
   - 从 `input_schema` 移除 `$schema`、`$id`、`$comment`、`additionalProperties`。
   - 保留 `type`、`properties`、`required`、`default`、`enum`、`items` 等。
4. 返回清理后的请求体和是否修改。

- [ ] **步骤 3：验证**

```bash
go test ./internal/proxy -run TestCleanTools -v
```

## 任务 3：Thinking/签名清理

**文件：**
1. 修改：`internal/proxy/rectifier.go`
2. 修改：`internal/proxy/rectifier_test.go`

- [ ] **步骤 1：编写失败的清理测试**

测试：
1. `TestCleanThinking_移除Thinking块` — 移除 `type: "thinking"` 块
2. `TestCleanThinking_移除RedactedThinking块` — 移除 `type: "redacted_thinking"` 块
3. `TestCleanThinking_移除Signature字段` — 移除剩余块上的 `signature` 键
4. `TestCleanThinking_移除顶层Thinking` — 最后 assistant 消息不以 thinking 开头时移除 `thinking`
5. `TestCleanThinking_保留非Thinking消息` — user 消息不变
6. `TestCleanThinking_保留Adaptive类型` — `thinking.type: "adaptive"` 不移除
7. `TestCleanThinking_无Messages字段` — 不含 `messages` 的请求体返回不变

运行：

```bash
go test ./internal/proxy -run TestCleanThinking -v
```

- [ ] **步骤 2：实现 thinking 清理**

```go
func cleanThinking(body []byte) ([]byte, bool)
```

规则：
1. 解析为 `map[string]any`。
2. 对 `messages` 中的每条消息：
   - 过滤掉 `type: "thinking"` 或 `type: "redacted_thinking"` 的 content 块。
   - 移除剩余块上的 `signature` 键。
3. 若 `thinking.type == "enabled"` 且最后 assistant 消息不以 thinking 块开头，移除顶层 `thinking`。
4. 返回清理后的请求体和是否修改。

- [ ] **步骤 3：验证**

```bash
go test ./internal/proxy -run TestCleanThinking -v
```

## 任务 4：组合 Rectify 函数

**文件：**
1. 修改：`internal/proxy/rectifier.go`
2. 修改：`internal/proxy/rectifier_test.go`

- [ ] **步骤 1：编写失败的集成测试**

测试：
1. `TestRectifyRequest_Tool校验` — PatternToolValidation 时执行 tool 清理
2. `TestRectifyRequest_Thinking签名` — PatternThinkingSignature 时执行 thinking 清理
3. `TestRectifyRequest_无匹配` — PatternNone 时返回原始请求体
4. `TestRectifyRequest_与现有Transform共存` — 清理后模型映射保持不变

运行：

```bash
go test ./internal/proxy -run TestRectifyRequest -v
```

- [ ] **步骤 2：实现 rectify**

```go
func RectifyRequest(body []byte, pattern ErrorPattern) ([]byte, bool)
```

分发逻辑：
- `PatternToolValidation` → 仅 `cleanTools`
- `PatternThinkingSignature` → 仅 `cleanThinking`

返回清理后的请求体和是否执行了清理。

- [ ] **步骤 3：验证**

```bash
go test ./internal/proxy -run TestRectifyRequest -v
```

## 任务 5：接入 Handler 重试逻辑

**文件：**
1. 修改：`internal/proxy/handler.go`

- [ ] **步骤 1：重构 handler 缓冲 400 响应**

当前代码立即将上游 headers 和状态码写入 `w`：

```go
// 当前：直接转发
for key, values := range resp.Header { ... }
w.WriteHeader(resp.StatusCode)
```

重构为：
1. 若 `resp.StatusCode == 400` 且路径为 `/v1/messages` 或 `/anthropic/v1/messages`：
   - 缓冲错误体（上限 128KB）。
   - 调用 `matchErrorPattern(bufferedBody)`。
   - 若模式匹配，调用 `RectifyRequest(modifiedBody, pattern)`。
   - 若清理修改了请求体，重试请求一次。
   - 返回重试响应。
   - 若不匹配或未重试，必须把已缓冲部分和剩余响应体拼回去完整透传。
2. 否则，继续现有流程。

- [ ] **步骤 2：添加重试逻辑**

```go
func (h *Handler) tryRectify(r *http.Request, originalBody []byte, resp *http.Response, backendURL string, apiToken string, client *http.Client) (*http.Response, io.ReadCloser)
```

返回：
- 重试后的最终响应（如重试成功）。
- 或恢复后的原始响应体（未重试或重试创建/发送失败时，用于完整透传）。

- [ ] **步骤 3：验证 handler 仍正常工作**

```bash
go test ./internal/proxy -v
```

## 任务 6：全量验证

**文件：**
1. 更新：`docs/features/2026-05-15-reactive-provider-compat/validation.md`
2. 更新：`docs/features/2026-05-15-reactive-provider-compat/status.md`

- [ ] **步骤 1：运行代理测试**

```bash
go test ./internal/proxy -v
```

预期：全部通过。

- [ ] **步骤 2：运行全量测试**

```bash
go test ./...
```

预期：全部通过。

- [ ] **步骤 3：手动供应商验证**

1. 配置 MiniMax 供应商。发送含 tools 的 Claude Code 请求。确认成功响应或观察到 `[Rectifier]` 日志显示清理 + 重试。
2. 配置 GLM 供应商。发送相同请求。确认无 `[Rectifier]` 日志（直接透传）。
3. 配置 Kimi Code 供应商，积累长对话。确认恢复或改进的错误信息。

- [ ] **步骤 4：记录验证证据**

在 `validation.md` 中记录命令输出和手动观察结果。
