# 供应商额度自动故障切换审查归档

日期：2026-07-13
审查人：Codex

## 范围

审查 `provider-quota-failover` 分支从 SDD 规格提交 `f063686` 到当前 HEAD 的实现。重点覆盖故障分类、代理重放与默认供应商切换、管理端认证 API、额度快照协调、前端与会话记录页面隔离，以及事件脱敏。

## 主要发现与处置

1. 功能缺陷：无 HTTP 状态的上游传输失败不会进入 failover。
   - 证据：`failover.ClassifyError` 已实现 ECONNRESET、timeout、DNS 等分类，spec 也把 ECONNRESET/无状态失败列为可切换信号。但 `internal/proxy/handler.go` 在 `client.Do` 返回 error 时直接写 `502 Backend unavailable`，没有进入候选重放。临时审查测试使用已关闭的上游服务和健康候选，结果仍返回原始 502，没有切换。
   - 处置：本次审查未修复。代理 error 分支应先分类传输错误，并在写 502 前复用候选重放路径。

2. 功能缺陷：字符串业务码 `"1308"` 不会被识别为额度耗尽。
   - 证据：`parseErrorBody` 会把字符串 `error.code` 放入 `codeStr`，但 `ClassifyResponse` 只判断数值 `pe.code == 1308/1310`。临时审查测试使用 `{"error":{"code":"1308","message":"已达到 5 小时的使用上限..."}}`，分类结果不是 eligible。用户需求明确要求同时看返回数据里的业务码，不能只看 HTTP 状态。
   - 处置：本次审查未修复。分类器应把字符串 `"1308"`、`"1310"` 与数值码等价处理，并保留 `BusinessCode`。

3. 功能缺陷：当失败供应商来自 `GetActiveProvider` fallback 时，切换成功但不会持久化新的默认供应商。
   - 证据：当 `ActiveProviderID` 为空或指向 disabled provider 时，`GetActiveProvider` 会 fallback 到第一个 enabled provider。该 provider 失败、候选成功后，`CommitSwitch(fromID, toID, ...)` 仍要求 `cfg.ActiveProviderID == fromID`；但存储中的 active ID 是空或 disabled ID，compare-and-set 不会更新。临时审查测试中客户端收到候选的 200 响应，但 `ActiveProviderID` 仍为空，后续默认请求仍可能反复打到同一个失败 fallback provider。
   - 处置：本次审查未修复。切换提交逻辑需要处理 effective active provider，或在 failover 前规范化 `ActiveProviderID`。

4. 安全审查：已检查路径中未发现直接安全缺陷。
   - 证据：`/api/providers/failover` 与 `/api/failover/events` 均经 `authMiddlewareFunc`；事件表不存 API Token、请求体、响应体或原始 query；事件 API 只返回供应商名/ID、模型、状态码/业务码、原因、结果和时间。前端 `FailoverEventsView` 是 Dashboard 独立 tab，没有传入 `SessionBrowser`、`SessionDetail`、导出或 JSONL 解析。
   - 处置：当前可接受。供应商名和模型名仍应按用户可控文本处理，继续使用 Vue 插值渲染，不使用 raw HTML。

## 最终审查结论

当前实现不应按“全部完成且无缺陷”合并。自带测试通过，但针对性审查测试暴露了 3 个与 spec 不一致的功能缺口：传输/无状态失败不切换、字符串业务码额度错误漏判、fallback active provider 切换后不持久化默认供应商。新管理 API 与事件存储/展示路径暂未发现直接安全问题。

## 验证

- `go test ./... -count=1` 通过。
- `npm --prefix internal/frontend test` 通过，174 个测试。
- `npm --prefix internal/frontend run build` 通过。
- 临时审查测试已本地添加并删除；这些测试按上述问题失败，当前不留在工作区。

## 残余说明

- 现有测试未覆盖 `client.Do` 传输错误时的代理 failover。
- 现有测试未覆盖业务码为字符串的 provider 响应。
- 现有测试未覆盖 `ActiveProviderID == ""` 或 active provider disabled 时的 fallback failover 行为。
