# 请求日志增强验证清单

**版本**: 1.1
**日期**: 2026-05-20

---

## 验证方法

逐项检查，通过后标记 `[x]`。

---

## 1. 代码完整性

- [x] `requestBodySummary()` 函数存在于 `handler.go`
- [x] 函数签名正确：`(body []byte) (msgs int, tools int, stream bool)`
- [x] 使用 `json.RawMessage` 避免完整反序列化
- [x] JSON 解析失败时返回 `(0, 0, false)`

## 2. 请求 ID

- [x] 入口日志前使用 `randomHex(8)` 生成 `reqID`
- [x] 入口日志格式：`[reqID] >>> ...`
- [x] 出口日志格式：`[reqID] <<< ...`

## 3. 入口日志

- [x] `ServeHTTP()` 中在 `transformRequest` 之后有 `>>>` 日志
- [x] 日志格式包含：reqID、METHOD、路径、model、stream、msgs、tools、size
- [x] 模型映射时显示 `original -> mapped` 格式
- [x] 无映射时只显示原始模型名

## 4. 出口日志

- [x] 收到上游响应后有 `<<<` 日志
- [x] 日志格式包含：reqID、状态码、model、upstream 耗时
- [x] 使用 `headerMS` 变量表示上游响应头耗时
- [x] 网络错误路径（`client.Do()` 失败）也有 `<<<` 日志，格式为 `[reqID] <<< 502 upstream=Nms error=...`

## 5. 硬编码端点日志

- [x] `handleHardcodedEndpoint()` 入口统一打印 `[Hardcoded] Handling METHOD /path`
- [x] 各 handler 函数（`handleFeedback`、`handleMetric` 等）不再有单独的 `log.Printf`
- [x] `handleCountTokens` 保留特殊格式：`[Hardcoded] Handling METHOD /v1/messages/count_tokens: size=N estimated_tokens=N`

## 6. 重复日志移除

- [x] `transformRequest()` 中不再有 `Model mapping` 日志
- [x] 模型映射逻辑（`req["model"] = mapped`）未被删除

## 7. 编译与测试

- [x] `go build ./...` 编译通过
- [x] `go test ./internal/proxy/... -v` 全部通过（184 tests）

## 8. 日志格式验证

启动代理后观察 Docker 日志，确认：

- [ ] 非 `/v1/messages` 路径的请求也有日志（如 GET /v1/me）
- [ ] `/v1/messages` 请求日志包含正确的 msgs/tools/stream/size 值
- [ ] 入口日志（`>>>`）和出口日志（`<<<`）的 `[reqID]` 一致
- [ ] 出口日志显示正确的状态码和 upstream 耗时
- [ ] 网络错误（上游不可达）时也有 `<<< 502` 出口日志
- [ ] 模型映射时显示箭头格式（`claude-opus-4-6 -> glm-5.1`）
- [ ] 无映射时只显示模型名（不包含箭头）
- [ ] 硬编码端点日志格式统一为 `[Hardcoded] Handling METHOD /path`
- [ ] `count_tokens` 日志额外显示 `size=N estimated_tokens=N`
