# 请求日志增强实施计划

**Goal:** 改进代理日志输出，为每个请求生成短 ID 并打印配对的入口/出口日志。入口日志包含路径、模型映射、消息/工具数量、请求体大小；出口日志包含状态码和上游耗时。移除 `transformRequest` 中的重复模型映射日志。

**Spec:** [requirements.md](requirements.md)

**Impact:** 修改 `internal/proxy/handler.go` 和 `internal/proxy/hardcoded.go`。

---

## Phase 1: 新增辅助函数

- [x] **新增 `requestBodySummary(body []byte) (msgs int, tools int, stream bool)`**
  - 使用 `json.RawMessage` 延迟解析，仅提取 `messages`、`tools`、`stream` 三个字段
  - JSON 解析失败时返回 `(0, 0, false)`
  - 位置：`summarizeRequestParams()` 函数之后

---

## Phase 2: 添加请求 ID

- [x] **在入口日志前生成 `reqID`**
  - 使用 `randomHex(8)` 生成 8 字符十六进制 ID
  - 入口和出口日志统一使用 `[reqID]` 前缀

---

## Phase 3: 添加入口日志

- [x] **在 `ServeHTTP()` 中添加 `>>>` 入口日志**
  - 位置：`transformRequest()` 调用之后、创建后端请求之前
  - 格式：`[reqID] >>> METHOD /path model=%s stream=%v msgs=%d tools=%d size=%d`
  - 模型映射时显示 `original -> mapped` 格式

---

## Phase 4: 添加出口日志

- [x] **正常响应路径：在 `ServeHTTP()` 中添加 `<<<` 出口日志**
  - 位置：收到上游响应后、写入客户端响应体之前
  - 格式：`[reqID] <<< STATUS model=%s upstream=%dms`
  - 使用已有的 `headerMS` 变量（从发送请求到收到响应头的耗时）

- [x] **网络错误路径：在 `client.Do()` 错误分支添加 `<<<` 出口日志**
  - 格式：`[reqID] <<< 502 upstream=%dms error=%v`
  - 确保每个 `>>>` 入口日志都有对应的 `<<<` 出口日志

---

## Phase 5: 硬编码端点日志统一化

- [x] **在 `handleHardcodedEndpoint()` 入口统一打印日志**
  - 位置：`drainRequestBody` 之后、switch 之前
  - 格式：`[Hardcoded] Handling METHOD /path`
  - `count_tokens` 保留特殊格式：`[Hardcoded] Handling METHOD /v1/messages/count_tokens: size=N estimated_tokens=N`

- [x] **移除各 handler 函数中的分散日志**
  - 移除 `handleFeedback`、`handleMetric`、`handleMetricsEnabled` 等 15+ 处 `log.Printf("[Hardcoded]...")`

---

## Phase 5: 移除重复日志

- [x] **移除 `transformRequest()` 中的 `Model mapping` 日志**
  - 删除 `log.Printf("Model mapping: %s -> %s (provider: %s)", ...)`
  - 入口日志已包含映射信息，无需重复

---

## Phase 6: 验证

- [x] `go build ./...` 编译通过
- [x] `go test ./internal/proxy/... -v` 184 个测试全部通过
