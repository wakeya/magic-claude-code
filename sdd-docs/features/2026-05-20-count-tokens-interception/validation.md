# count_tokens 端点拦截验证清单

**版本**: 1.0
**日期**: 2026-05-20

---

## 1. 代码完整性

- [x] `isHardcodedEndpoint()` 精确匹配列表包含 `"/v1/messages/count_tokens"`
- [x] `handleHardcodedEndpoint()` 在 `drainRequestBody` 之前拦截 count_tokens（因为需要读取请求体）
- [x] `handleCountTokens()` 方法存在，读取请求体并返回估算 token 数
- [x] `handleCountTokens()` 不转发请求到上游

## 2. 响应格式正确性

- [x] 返回 HTTP 200 OK
- [x] Content-Type 为 `application/json`
- [x] 响应体为合法 JSON：`{"input_tokens": <integer>}`
- [x] `input_tokens` 值始终 >= 1

## 3. Token 估算合理性

| 请求体大小 | 预期 input_tokens |
|-----------|-------------------|
| 0 bytes | 1（最小值） |
| 400 bytes | 100 |
| 4 bytes | 1 |
| 1200 bytes | 300 |

- [x] 估算值 = `max(1, bodySize / 4)`
- [x] 空请求体返回 1 而非 0

## 4. 日志格式

- [x] 日志包含 `[Hardcoded]` 前缀、HTTP 方法、请求路径
- [x] 日志格式：`[Hardcoded] Handling POST /v1/messages/count_tokens: size=N estimated_tokens=N`

## 5. 编译与测试

- [x] `go build ./...` 编译通过
- [x] `go test ./internal/proxy/... -v` 全部通过（244 tests）

## 6. 运行验证

部署后观察 Docker 日志：

- [ ] count_tokens 请求不再转发到上游（无 `[Proxy]` 的 count_tokens 日志）
- [ ] 出现 `[Hardcoded] Handling POST /v1/messages/count_tokens` 日志
- [ ] Claude Code 上下文管理正常工作（无上下文溢出错误）
