# 反应式供应商兼容性错误恢复 — 验证清单

**功能：** 反应式供应商兼容性错误恢复
**状态：** draft
**最后更新：** 2026-05-18

## 验收标准

1. **模式检测：** `matchErrorPattern` 能正确识别 tool 校验、thinking/签名等 JSON 错误体；普通 `invalid_request_error` 不自动恢复。
2. **Tool 清理：** `cleanTools` 移除 `cache_control`、填充空 `input_schema`、移除 JSON Schema 元数据、保留核心字段。
3. **Thinking 清理：** `cleanThinking` 移除 thinking/redacted_thinking 块、移除 signature 字段、在不匹配时移除顶层 `thinking`。
4. **Rectify 分发：** `RectifyRequest` 对每种错误模式执行正确的清理策略。
5. **无误报：** 不含 `tools`、`messages` 或不匹配模式的请求体返回不变。
6. **Handler 重试：** 上游对 `/v1/messages` 返回 400 时，handler 尝试用清理后的请求体重试一次。
7. **非 messages 路径不重试：** 健康检查和其他端点不触发重试。
8. **非 400 状态不重试：** 401、403、429、500 响应直接转发。
9. **供应商隔离：** GLM、Anthropic 官方和其他正常供应商收到未修改的请求（无 `[Rectifier]` 日志）。
10. **错误体完整性：** 非可恢复 400 响应完整透传，不因 128KB 模式检测缓冲被截断。
11. **现有测试通过：** 所有既有的代理和配置测试继续通过。

## 自动化验证

```bash
# 模式匹配
go test ./internal/proxy -run TestMatchErrorPattern -v

# Tool 清理
go test ./internal/proxy -run TestCleanTools -v

# Thinking 清理
go test ./internal/proxy -run TestCleanThinking -v

# 组合 rectify
go test ./internal/proxy -run TestRectifyRequest -v

# 全部代理测试
go test ./internal/proxy -v

# 全量后端测试
go test ./...
```

## 手动验证

### 场景 1：MiniMax Tool 校验恢复

1. 配置供应商基础地址为 `https://api.minimaxi.com/anthropic`。
2. 使用该供应商启动 Claude Code 会话。
3. 触发含 tools 的请求（任何常规 Claude Code 操作）。
4. **预期：** 直接成功，或 `[Rectifier]` 日志显示 tool 清理 + 重试成功。

### 场景 2：GLM 直接透传

1. 配置供应商指向 GLM Anthropic 端点。
2. 发送相同的 Claude Code 请求。
3. **预期：** 无 `[Rectifier]` 日志。请求未修改直接透传。

### 场景 3：Kimi Code 长对话历史

1. 配置供应商指向 `https://api.kimi.com/coding/`。
2. 积累含 tool 使用的长对话。
3. **预期：** 持续成功，或 `[Rectifier]` 日志显示清理尝试。

### 场景 4：非 messages 路径

1. 向 `/`（健康检查）发送请求。
2. **预期：** 无论状态码如何，不触发重试逻辑。

## 验证证据日志

- 2026-05-19：新增 MiniMax/Kimi tool 400 回归测试。MiniMax 样例错误：`invalid params, function name or parameters is empty (2013)`；Kimi 样例错误：`tools.0.input_schema.additionalProperties is not supported`。
- 2026-05-19：新增 handler 集成测试，验证 `/v1/messages` 首次 400 后只重试一次，且重试请求体已移除 `cache_control` 和 `additionalProperties`。
- 2026-05-19：新增大响应体回归测试，验证非可恢复 400 响应不会因 128KB 模式检测缓冲而截断。
- 2026-05-19：`go test ./internal/proxy -count=1` 通过。
