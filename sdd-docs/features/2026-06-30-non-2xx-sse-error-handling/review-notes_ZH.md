# SSE 标记的 HTTP 错误处理审查记录

日期：2026-07-01
审查者：Codex 和 Claude Code

## 审查范围

审查了 `dd2f8bf...b37030b`，包括两个变更 Go 文件全文、中英文规格、响应/心跳辅助逻辑、用量清洗、400 修复器、生产环境记录器接线以及请求摘要安全修复。验证包含定向安全复现和带 race detector 的 `make test`。

## 主要发现与处理结论

1. 状态码优先的分派逻辑正确。
   - 处理结论：最终状态 `>= 400` 时通过现有错误观察器保留上游状态、响应头和完整响应体；成功 SSE 响应继续使用心跳处理。未发现响应分派、修复器或用量统计回归。
2. 低危安全缺陷：SSE 标记的 HTTP 错误会把顶层 `system`、`metadata` 和未知请求字段写入进程日志（CWE-532）。
   - 处理结论：已由 `dcdc3c4` 解决。`summarizeRequestParams` 现在只接受经过类型检查的安全字段（`model`、`stream`、数值生成参数）和集合数量（`messages`、`tools`、`input`）。端到端断言证明系统提示词、metadata、凭据、未知扩展、消息内容、工具内容和 input 内容不会进入日志。
3. 回归测试证明了 400/`stream=false` 场景，但没有直接参数化覆盖规格中的全部 4xx/5xx 与请求 stream 组合。
   - 处理结论：已由 `dcdc3c4` 和 `b37030b` 解决。Handler 测试现覆盖 400/非流式、429/流式和 500/流式响应；直接摘要器测试固化白名单并拒绝不安全的值类型。

## 最终审查结论

SSE 错误分派修复和请求摘要白名单均已验证。审查范围内没有遗留的逻辑或安全缺陷。

## 验证证据

- 白名单修复前，定向 Handler 测试失败，并在详细错误日志中打印了 `secret-system-prompt`。
- 修复后，`go test ./internal/proxy -run 'TestProxyRecordsSSELabeledHTTPError|TestSummarizeRequestParamsAllowsOnlySafeDiagnostics' -count=1` 通过。
- `go test ./internal/proxy -count=1` 通过。
- `make test` 在启用 race detector 和覆盖率的情况下通过。

## 遗留说明

- 第一次完整 `make test` 在无关的网络依赖测试 `TestDownloadAndApplyRedactsInvalidDownloadURL` 上失败：TLS 握手超时时返回的原始 URL 错误包含 query。定向重跑和第二次完整 `make test` 均通过。该问题是本 diff 之外的既有 updater 行为，但测试应改为确定性实现，并统一清洗传输层错误。
