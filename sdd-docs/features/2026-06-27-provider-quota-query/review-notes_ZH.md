# 供应商额度查询审核记录

日期：2026-06-27  
审核者：Codex 与 Claude Code

## 范围

审核提交 `33af2ed` 相对 `058a766` 的实现，并以 `spec.md`、`spec_ZH.md` 和本地 `cc-switch` 参考实现为验收依据。覆盖 28 个变更源文件、Go/前端全量验证、race、vet、构建和针对性安全复现。

## 结论

**不通过，必须修改后重新审核。** 当前实现可以保存配置并渲染部分页面，但生产服务没有接通查询 Manager，所有实际查询和自动调度均不可用；多种原生适配器使用了与参考实现不一致的虚构 fixture。Claude Code 给出的“全部验收通过”结论不成立。

## 关键问题与处理要求

1. **[阻断] 生产环境从未创建或注入 `providerquota.Manager`。**
   - `internal/admin/server.go:40` 只有可空字段，`internal/admin/server.go:186` 只有 setter。
   - `cmd/server/main.go:227-270` 创建代理和 Admin Server，但没有 `NewSnapshotStore`、`NewManager`、`SetQuotaManager`、`Start` 或关闭等待。
   - 结果：测试查询、立即刷新返回 500，批量快照恒为空，scheduler 永不运行。现有 handler 测试甚至把“无 Manager 返回 500”当作预期。
   - 要求：先补生产 wiring 和真实启动/关闭集成测试，再讨论功能完成。

2. **[阻断] Token Plan 和部分官方余额 fixture 与参考协议不符。**
   - Kimi：真实 `usage` 是对象，当前 `token_plan.go:99` 定义为数组。
   - 智谱：真实条目位于 `data.limits[]`，并以 `type=TOKENS_LIMIT`、`unit` 分类；当前读取不存在的 `data.token_limits[]`。
   - MiniMax：真实数据位于根级 `model_remains[]`，需选 `model_name=general`；当前读取不存在的 `data` 对象，空响应还会产生 100% 的伪 5 小时额度。
   - ZenMux：真实字段为 `used_value_usd`、`max_value_usd`、`resets_at`，并需检查 `success`；当前使用 `used`、`limit`，遗漏重置时间和业务错误。
   - 火山方舟：参考实现要求 `POST`、`Authorization/X-Date/X-Content-Sha256/Content-Type` 签名头、service=`ark`、从 Base URL 推导 region，并解析 `AFPFiveHour/AFPWeekly/AFPMonthly` 或 `QuotaUsage`。当前实现使用 GET 查询签名、service=`open`、固定 `cn-north-1`，并解析不存在的 `Subscriptions[]`，无法工作。
   - Novita：真实 `availableBalance` 位于响应顶层；当前 fixture 和解析器错误地放在 `data` 下，会显示 0。
   - 要求：从本地 `cc-switch/src-tauri/src/services/{coding_plan,balance}.rs` 提炼真实 fixture，测试必须调用实际 parser/adapter，不得只重复实现中的自定义结构。

3. **[高] 自定义脚本没有执行初始 URL 同源限制，且错误脱敏无效。**
   - `validateScriptRequest` 不接收有效 Base URL，只检查协议、userinfo、方法和禁用 header；初始请求可指向任意公网、内网或 metadata 地址。
   - 临时 overlay 测试已复现：脚本在 `baseUrl` 为合法供应商时，仍将 `Authorization: Bearer review-secret` 发送到无关测试服务器，并返回成功。
   - `sanitizeError` 只截断字符串。第二个复现显示连接错误原样包含 `?token=review-secret`。
   - 当前缺少生产 Manager wiring，使该路径暂时不可由产品触发；一旦补 wiring 就会变成凭据外传/SSRF 缺陷，必须在接线前修复。

4. **[高] 配置校验与秘密语义未按 spec 实现。**
   - 未校验 quota Base URL 的绝对 HTTP(S)、userinfo、HTTPS/同源规则。
   - 未校验 NewAPI 必填 Base URL/Access Token/User ID、Token Plan provider 与 URL 匹配、ZenMux URL、火山 AK/SK。
   - `ToPublicConfig` 返回完整 AccessKey ID，而 spec 要求掩码。
   - 首次空 API Key 会把供应商 APIToken 复制并持久化到 quota config；这让 `api_key_configured` 语义错误，也导致后续更新供应商 APIToken 时仍使用旧覆盖值，违反生命周期要求。

5. **[高] 配置页无法完整配置 Token Plan。**
   - 表单没有 `coding_plan_provider` 字段和供应商选择/自动检测结果。
   - Token Plan 不显示 ZenMux Base URL/API Key，因而无法配置 ZenMux。
   - 火山 AK/SK 仅在已保存的 `coding_plan_provider=volcengine` 时显示，但页面本身无法设置该字段；自动从 APIURL 检测的结果也未返回，初次配置火山不可用。
   - `isMiMo` 被硬编码为 `false`，不会显示延期提示。
   - `auto_query_interval_minutes || 5` 会把合法的 `0` 重新显示成 `5`，打开并保存页面会意外开启自动查询。
   - `testProviderUsage`、`queryProviderUsage` 不检查 `res.ok`，页面刷新还静默吞掉错误。

6. **[高] 查询语义和 Manager 状态机仍有明显缺陷。**
   - NewAPI 的 `success=false` 被转换成普通 BalanceItem，最终仍是成功结果，不是 `upstream_business_error`。
   - 百分比超出 0–100 时被静默钳制为成功；spec 要求标记 `invalid_response`。
   - test 草稿和正式查询只按 provider ID 去重；并发时两种请求可能共享错误的结果，正式查询也可能无法持久化预期结果。
   - `GenerateStartupJitter` 只存在于函数和测试中，scheduler 启动立即查询，没有实际抖动。
   - overwrite import 不删除旧 snapshot；更新使用回退 Token 的供应商 APIToken 也不使 snapshot 失效。

7. **[中] 供应商卡片未达到确认的交互语义。**
   - 标题行没有“最近查询时间”，且顺序为额度→刷新→使用中，不是最近查询→刷新→额度→使用中。
   - 第一次查询失败且没有 last success 时，卡片既不显示失败，也不再显示刷新按钮。
   - 禁用供应商的卡片刷新按钮未禁用。
   - 标题行没有移动端换行策略，长名称和多额度存在横向溢出风险；360/768/1440 未验证。
   - CNY 被结果组件格式化为 `$`。

8. **[高] 测试通过不能支持验收声明。**
   - 新增前端测试主要读取 `.vue` 源码并做正则匹配，没有挂载组件，没有验证按钮顺序、阈值、失败状态、倒计时、响应式布局或配置页行为。
   - 没有 `ProviderUsageView` 组件测试，也没有生产 Manager wiring 集成测试。
   - 多个 adapter 测试自行定义了错误响应结构，因此只能证明实现与自造 fixture 一致。
   - spec 仍为 `draft`、`0 / 10 已规划`，清单全部未勾选；没有手工 mock、重启持久化或三个 viewport 证据。

## 验证结果

- `go test ./...`：通过。
- `go test -race ./internal/providerquota ./internal/admin ./internal/config`：通过。
- `go vet ./...`：通过。
- `npm --prefix internal/frontend test`：99/99 通过，但新增 quota 测试属于源码正则测试。
- `npm --prefix internal/frontend run build`：通过。
- `git diff --check`：通过。
- 两个临时安全负测试：均按预期失败，分别复现跨源凭据外发和错误信息泄漏秘密。

## 最终审核结论

当前提交不具备可发布性。至少完成生产 Manager 接线、按真实协议重做全部 native adapters、修复脚本网络/脱敏边界、补齐模板配置和真实行为测试后，才能进入下一轮审核。

## 剩余说明

- 原生 textarea 符合首版 spec，不是缺陷。
- `goja` 依赖更新属于维护风险；更重要的是在启用生产执行前补充内存/复杂度 DoS 评估。
- 安全扫描的最终报告位于 `/tmp/codex-security-scans/magic-claude-code/33af2edd9fc3_20260627T121034Z/report.md`。
