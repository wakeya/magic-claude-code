# count_tokens 端点拦截实施计划

> **Goal:** 拦截 `/v1/messages/count_tokens` 端点，本地估算 token 数后直接返回，避免 68+ 次无意义的上游请求。

**Spec:** [requirements.md](requirements.md)

**Impact:** 仅修改 `internal/proxy/hardcoded.go` 和 `internal/proxy/hardcoded_test.go`。

---

## Phase 1: 注册端点

- [x] **在 `isHardcodedEndpoint()` 精确匹配列表中添加 `"/v1/messages/count_tokens"`**
  - 位置：精确匹配列表末尾

- [x] **在 `handleHardcodedEndpoint()` 中单独拦截（在 `drainRequestBody` 之前）**
  - 因为 count_tokens 需要读取请求体，不能先 drain
  - 使用 `if path == "/v1/messages/count_tokens"` 提前分支
  - 调用 `handleCountTokens(w, r)` 并 `return true`

---

## Phase 2: 实现 Handler

- [x] **新增 `handleCountTokens(w http.ResponseWriter, r *http.Request)` 方法**
  - 读取请求体长度（`io.ReadAll` + `len`）
  - 估算 token 数：`max(1, bodySize / 4)`
  - 返回 `{"input_tokens": estimated}`
  - 日志：`[Hardcoded] Handling POST /v1/messages/count_tokens: size=%d estimated_tokens=%d`

---

## Phase 3: 测试

- [x] **单元测试 `isHardcodedEndpoint` 匹配**
  - `"/v1/messages/count_tokens"` 返回 true

- [x] **单元测试 `handleCountTokens` 响应格式**
  - 验证返回 200 OK
  - 验证 Content-Type 为 application/json
  - 验证响应体包含 `input_tokens` 字段
  - 验证 `input_tokens` 值 > 0

- [x] **单元测试 token 估算逻辑**
  - 空请求体：`input_tokens` = 1
  - 小请求体（400 bytes）：`input_tokens` = 100
  - 中等请求体（1200 bytes）：`input_tokens` = 300

- [x] **集成测试**
  - 确保 `handleCountTokens` 被 `handleHardcodedEndpoint` 正确路由
