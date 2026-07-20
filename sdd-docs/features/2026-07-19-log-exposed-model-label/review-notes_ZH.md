# 日志显示暴露模型 Label 审查记录

日期：2026-07-20  
审查人：Codex 和 Claude Code

## 范围

审查分支 `feat/log-exposed-model-label` 相对 merge-base `0711f61f174ee666ce58886b6a3fd6d46d7d0d6e` 的改动，覆盖功能规格和以下 source-like diff：

- `internal/admin/failover_handler_test.go`
- `internal/config/config.go`
- `internal/config/failover_test.go`
- `internal/proxy/handler.go`
- `internal/proxy/helpers.go`
- `internal/proxy/helpers_test.go`

Codex Security 扫描报告：`/tmp/codex-security-scans/magic-claude-code/feat_log_exposed_model_label_20260720T000000Z/report.md`。

## 主要发现与处理

1. 未发现阻断性的功能逻辑缺陷。
   - 处理结论：`ModelRoute.ExposedLabel` 是纯新增、仅展示字段；`ResolveRoute` 仍保持原 provider/backend/default-route 语义，请求转换仍使用 backend model，usage 仍记录 original/mapped model。

2. 未发现可报告的安全漏洞。
   - 处理结论：Label 不进入路由、请求体、上游 URL、Header、认证、failover 选择或 usage 持久化。ExposedModel 路由仍为 `DefaultRouted=false`，现有 failover guard 继续阻止固定 `/model` 路由自动重放和修改 active provider。

3. 低危加固建议：`ExposedModel.Label` 由运维配置，当前可包含内部控制字符。
   - 处理结论：该问题需要已认证的 provider 配置权限，影响范围是本地日志可读性，不构成本分支的未授权安全漏洞。后续可在日志层做单行 sanitize，或在 `Provider.Validate` 中拒绝控制字符/超长 Label。

4. 测试覆盖说明：Label 替换已有纯函数单测，但还没有 `ServeHTTP` 日志点的集成断言。
   - 处理结论：当前测试覆盖了 `formatModelLog` 行为和 route 字段契约。建议后续补充捕获日志的 handler 测试，断言 `>>>` 与 `<<<` 都显示 `Label -> BackendModel` 且不含 `em-`，并补一个直接的 `Context1M + ExposedLabel` 断言。

## 最终审查结论

该分支未遗留阻断性的功能逻辑问题或可报告安全问题。实现符合规格中“仅改变日志展示、不改变路由/请求体/failover/usage”的约束；剩余项属于加固与测试深度建议，不作为合并阻断项。

## 残余说明

- Codex 已执行验证：`go test ./internal/config ./internal/proxy` 通过；`git diff --check 0711f61f174ee666ce58886b6a3fd6d46d7d0d6e...HEAD` 通过；`rtk go test ./...` 通过，输出为 `1684 passed in 17 packages`；`rtk go vet ./...` 无问题。
