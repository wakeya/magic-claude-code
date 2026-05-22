# 反应式供应商兼容性错误恢复

**版本：** 1.0
**日期：** 2026-05-18
**状态：** draft
**生命周期：** draft
**替代：** v0.2 "Kimi Code 供应商兼容性"（主动式供应商预处理方案）

---

## 1. 目标

添加通用的反应式错误恢复机制：当供应商返回 400 错误且错误信息匹配已知模式时，自动清理请求体中的不兼容字段，然后重试一次。不影响正常工作的供应商。

## 2. 问题

Claude Code 发送的请求包含 Anthropic 协议专有字段，第三方供应商可能不完全支持：

1. **Tool 定义校验失败** — MiniMax 拒绝包含空或不完整 `input_schema` 的 tool：
   ```json
   {"error":{"type":"invalid_request_error","message":"invalid params, function name or parameters is empty (2013)"}}
   ```

2. **Thinking/签名不兼容** — 部分供应商拒绝包含 `thinking`、`redacted_thinking` 或 `signature` 内容块的请求。

3. **历史内容块不兼容** — Kimi Code 拒绝包含 `tool_reference`、`server_tool_use` 等 Claude Code 专有块类型的长对话历史。

当前代理直接将错误响应转发给 Claude Code 客户端，客户端无法恢复。用户只能 `/clear` 或新建会话。

## 3. 方案：反应式恢复

**不**为特定供应商做主动预处理（旧方案）。

**而是**在供应商确认拒绝请求后，检测可恢复的错误，清理请求体，重试一次。

理由：
- 正常工作的供应商（GLM、Anthropic 官方）收到未修改的原始请求 — 零风险。
- 清理仅在确认出错后才执行。
- 无需供应商特定的 URL 检测或配置。

## 4. 错误模式目录

### 模式 1：Tool 定义校验

**匹配规则（不区分大小写）：**
- `"function name or parameters is empty"`
- `"invalid params"` 配合 `tool`、`function`、`parameters`、`input_schema`、`schema`、`additionalProperties` 等 tool 上下文
- `"invalid_request_error"` 或 `"Invalid request Error"` 配合明确的 tool/schema 上下文

**清理策略：**
对 `tools` 数组中的每个 tool：
1. 移除 `cache_control` 字段（Anthropic 缓存提示，非 OpenAPI tool 规范的一部分）。
2. 确保 `input_schema` 存在且非空。缺失或为 `{}` 时，填充为 `{"type":"object","properties":{}}`。
3. 从 `input_schema` 中移除 JSON Schema 元数据：`$schema`、`$id`、`$comment`、`additionalProperties`。
4. 保留 `type`、`properties`、`required` — 这些是模型 tool 调用的核心字段。

### 模式 2：Thinking/签名不兼容

**匹配规则（不区分大小写）：**
- `"signature"` 且（`"thinking"` 或 `"invalid"` 或 `"field required"` 或 `"extra inputs"`）
- `"must start with a thinking block"`
- `"expected"` 且（`"thinking"` 或 `"redacted_thinking"`）且 `"tool_use"`
- `"thinking"` 且 `"cannot be modified"`

**清理策略：**
对每条 message 的 content 数组：
1. 移除 `type: "thinking"` 或 `type: "redacted_thinking"` 的块。
2. 移除剩余块上的 `signature` 字段。
3. 若顶层 `thinking.type` 为 `"enabled"` 且最后一条 assistant 消息不以 thinking 块开头，移除顶层 `thinking` 字段。

### 非恢复模式：通用非法请求

普通 `"invalid request"`、`"invalid_request_error"`、`"非法请求"` 或 `"illegal request"` 本身**不**触发自动恢复。只有错误文本明确指向 tool/schema 或 thinking/signature 时，才按模式 1 或模式 2 处理。

## 5. 重试逻辑

```
请求 → transformRequest（模型映射 + thinking 剥离）→ 转发到上游
                                                          ↓
                                                   上游返回 400？
                                                          ↓ 是
                                                   读取并缓冲错误体
                                                          ↓
                                                   匹配错误模式？
                                                          ↓ 是
                                                   对请求体执行模式对应的清理
                                                          ↓
                                                   重试一次（同一供应商、同一端点）
                                                          ↓
                                                   将重试响应返回给客户端
```

规则：
1. 每个客户端请求**最多重试一次**。
2. 仅在 HTTP 400 时重试（不是 401、403、429、500 等）。
3. 仅在 `/v1/messages` 和 `/anthropic/v1/messages` 端点重试。
4. 重试请求使用相同的 HTTP 方法、头部和认证。
5. 若重试仍返回 400，将重试的错误返回给客户端（不是原始错误）。
6. 以 `[Rectifier]` 前缀记录清理动作，便于调试。

## 6. 对 handler.go 的实现影响

当前流程：
```go
resp, err := client.Do(backendReq)
// ... 立即将 resp headers 写入 w
w.WriteHeader(resp.StatusCode)
// ... 将 resp body 流式写入 w
```

所需变更：当 `resp.StatusCode == 400` 时，**先缓冲上游响应体**再决定是否写入 `w`，以便判断是否可以重试。

此变更仅影响 400 错误路径。200 响应和 SSE 流继续直接透传，不缓冲。

## 7. 范围

### 包含

- 已知 400 错误模式的检测。
- Tool 定义清理。
- Thinking/签名块清理。
- 清理后单次重试。
- 清理动作的日志记录。

### 不包含

1. 不为特定供应商做主动请求修改。
2. 不做供应商特定的 URL 检测。
3. 不处理流式响应的重试（SSE 400 极少见，响应体通常是 JSON）。
4. 不做错误模式的配置 UI（硬编码模式，通过代码扩展）。
5. 不修改 Claude Code 客户端行为。
6. 不做多供应商故障转移（仅单供应商重试）。

## 8. 文件规划

新建：
1. `internal/proxy/rectifier.go` — 错误模式匹配和请求清理函数。
2. `internal/proxy/rectifier_test.go` — 模式匹配和清理的单元测试。

修改：
1. `internal/proxy/handler.go` — 在 400 错误路径中添加重试逻辑。

## 9. 风险

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 清理移除了模型需要的字段 | Tool 调用准确度下降 | 仅移除非必要的元数据（cache_control、$schema、空 schema） |
| 重试在 400 时增加延迟 | 用户等待更久才看到错误 | 最多 1 次重试；记录耗时 |
| 模式匹配过于宽泛 | 正常 400 错误被不必要的清理 | 模式必须匹配特定的已知错误字符串 |
| Tool 标准化改变了 tool 语义 | 模型生成不同的 tool 调用 | 仅标准化 schema 元数据，不动 name/description/properties |
| 缓冲 400 响应消耗内存 | 大错误体导致内存峰值 | 模式检测仅缓冲前 128KB；非可恢复错误必须拼回剩余响应体完整透传 |

## 10. 成功标准

1. MiniMax 的 `function name or parameters is empty` 错误能自动恢复。
2. Kimi Code 含明确 tool/schema 上下文的 `Invalid request Error` 能自动恢复。
3. GLM、Anthropic 官方和其他正常供应商收到未修改的原始请求。
4. 每个客户端请求最多重试 1 次。
5. 重试在失败路径上增加的延迟不超过 2 秒。
6. 普通或非可恢复 400 响应完整透传，不因模式检测缓冲被截断。
7. 所有现有代理测试继续通过。
