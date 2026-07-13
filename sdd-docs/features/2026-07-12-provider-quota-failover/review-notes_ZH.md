# 供应商额度自动故障切换审查归档

日期：2026-07-13（Codex 审查）/ 2026-07-13（Claude Code 修复）
审查人：Codex（审查）、Claude Code（修复）

## 范围

审查 `provider-quota-failover` 分支从 SDD 规格提交 `f063686` 到当前 HEAD 的实现。重点覆盖故障分类、代理重放与默认供应商切换、管理端认证 API、额度快照协调、前端与会话记录页面隔离，以及事件脱敏。

## 主要发现与处置

1. 功能缺陷：无 HTTP 状态的上游传输失败不会进入 failover。
   - 证据：`failover.ClassifyError` 已实现 ECONNRESET、timeout、DNS 等分类，spec 也把 ECONNRESET/无状态失败列为可切换信号。但代理在 `client.Do` 返回 error 时直接写 `502 Backend unavailable`，没有进入候选重放。
   - 处置：**已修复**。代理错误分支在写 502 前先用 `shouldFailoverOnError` 准入并 `ClassifyError` 分类，命中则复用新增的 `attemptFailoverOnError`（与响应路径共用 `runCandidateReplay`）做候选重放；成功则跳过 502 继续到流式输出。回归测试：`TestFailoverSwitchesOnTransportError`（关闭上游触发连接拒绝 → 切换到候选、持久化默认供应商）。

2. 功能缺陷：字符串业务码 `"1308"` 不会被识别为额度耗尽。
   - 证据：`parseErrorBody` 把字符串 `error.code` 放入 `codeStr`，但 `ClassifyResponse` 只判断数值 `pe.code == 1308/1310`。
   - 处置：**已修复**。新增 `codeIs(pe, want)` 同时接受数值与字符串形式（`1308` 与 `"1308"` 等价），`BusinessCode` 仍保留为字符串。回归测试：`TestClassify1308WithStringCodeIsEquivalent`、`TestClassify1310WithStringCodeIsEquivalent`。

3. 功能缺陷：当失败供应商来自 `GetActiveProvider` fallback 时，切换成功但不会持久化新的默认供应商。
   - 证据：`ActiveProviderID` 为空或指向 disabled provider 时，`GetActiveProvider` 回退到第一个 enabled provider；该 provider 失败、候选成功后，原 `CommitSwitch` 要求 `cfg.ActiveProviderID == fromID`，compare-and-set 不成立。
   - 处置：**已修复**。`CommitSwitch` 改为与「有效默认供应商」比较（`c.GetActiveProvider().ID == fromID`），命中即写入 `ActiveProviderID = toID`，保证切换持久化；并发下仍只有单一赢家（第二个并发请求的有效默认已是新供应商）。回归测试：`TestCommitSwitchPersistsWhenActiveIsEmptyAndFallbackFailed`、`TestCommitSwitchPersistsWhenActivePointsToDisabled`。

4. 安全审查：已检查路径中未发现直接安全缺陷。
   - 证据：`/api/providers/failover` 与 `/api/failover/events` 均经 `authMiddlewareFunc`；事件表不存 API Token、请求体、响应体或原始 query；事件 API 只返回供应商名/ID、模型、状态码/业务码、原因、结果和时间。前端 `FailoverEventsView` 是 Dashboard 独立 tab，没有传入 `SessionBrowser`、`SessionDetail`、导出或 JSONL 解析。
   - 处置：当前可接受。供应商名和模型名仍按用户可控文本处理，继续使用 Vue 插值渲染，不使用 raw HTML。

## 最终审查结论

Codex 审查发现的 3 个功能缺陷均已修复并通过 TDD 回归测试（先写失败用例复现，再修，再绿）。2026-07-13 的 Codex 二次复审已重新阅读补丁，并重跑针对性回归、Go 全量测试、前端测试、前端构建和 Go 全量 race 测试。未发现遗留逻辑或安全缺陷。

## 验证

- `go test ./... -race -count=1` 通过（1503）。
- `npm --prefix internal/frontend test` 通过（174）。
- `npm --prefix internal/frontend run build` 通过。
- Codex 二次验证还执行了 `go test ./... -count=1`、针对性 failover 回归、针对性 race 回归和 `go test -race ./... -count=1`；均通过。
- 针对性复现测试（传输错误切换、字符串业务码、空/disabled active 持久化）已固化为常规测试，留在工作区。

## 残余说明

- 命中故障切换时，原失败上游连接在 handler 返回时由既有 `defer resp.Body.Close()` 关闭（属连接复用稍延后，非泄露）。
- 凭据恢复的「测试成功」= 测试请求完成且上游非 401（与既有测试端点「连通即成功」语义一致）。
- 同一供应商的并发失败可能向同一候选多发一次重放（功能正确；compare-and-set 仍保证单一 `switched` 事件）。
- 未推送：提交在 `provider-quota-failover` 分支本地，待用户确认。

---

## 任务 6 审查（GPT-5.5，2026-07-13）

范围：供应商排序与优先级可视化（拖拽排序、序号 badge、tooltip、`PUT /api/providers/order`、SQLite `sort_order`）。

主要发现与处置：

1. 功能缺陷：排序会在 `ActiveProviderID` 为空/缺失/disabled 时改变「有效默认供应商」。
   - 证据：`GetActiveProvider()` 在 `ActiveProviderID` 不指向 enabled provider 时回退到首位 enabled；原 order handler 只是不改 `ActiveProviderID` 字段，但重排会静默改变有效默认（[A,B,C] active="" → 有效 A；拖成 [B,A,C] → 有效 B）。
   - 处置：**已修复**。order handler 在重排前捕获 `effectiveBefore := cfg.GetActiveProvider()`；重排后仅当存储的 `ActiveProviderID` 未指向 enabled provider 时，把旧有效默认固化进去（`cfg.ActiveProviderID = effectiveBefore.ID`）。已明确设置的 active provider 不受影响。回归测试：`TestProviderOrderPreservesEffectiveDefaultWhenActiveIDEmpty`、`TestProviderOrderPreservesEffectiveDefaultWhenActiveIDMissingOrDisabled`。

2. 建议改进：拖拽绑定在整张卡片，手柄只是视觉元素，可能从按钮/文本/Token 区域误发起拖拽。
   - 处置：**已修复**。拖拽手柄加 `data-provider-drag-handle`；`onProviderDragStart($event, index)` 校验事件来源 `closest('[data-provider-drag-handle]')`，非手柄发起则 `preventDefault()`。回归测试：`DashboardProviderReorder` 的「drag starts only from the drag handle」。

其它结论：排序 API 认证、400/409 校验、脱敏返回、SQLite `sort_order` 持久化方向正确；failover 候选顺序测试覆盖同映射模型段与 fallback 段内部按 provider 顺序；tooltip、序号 badge、上移/下移可达性已实现；无 token 泄露或未认证写入。

验证：`go test -race ./...` 1525 passed；`npm test` 193 passed；`npm run build` 通过；`git diff --check` clean。

---

## 供应商事件展示审查（Codex，2026-07-13）

范围：提交 `8954320`（`fix(failover): show provider names and ids in events`），覆盖 `/api/failover/events` 的供应商名称回填，以及前端“切换事件”表格拆分为“供应商”和“供应商 ID”两列。

主要发现与处置：

1. 逻辑审查：当前供应商的展示行为正确。
   - 证据：`handleFailoverEvents` 仍把当前已知 provider ID 集合传给 `failoverManager.Events`，因此已删除 provider 的 ID 仍会先由 store 抹空；新增的 `providerNames` 只在事件返回后仍带有当前已知 provider ID 时回填名称。
   - 处置：无需改代码。该实现保留了既有的悬空 ID 保护，同时让 exhausted/recovered 等只存 ID 的事件可以展示供应商名称，而不是只显示 `provider-*` ID。

2. 安全审查：没有引入 Token、请求体、query 或 raw HTML 暴露。
   - 证据：API 响应仍只包含既有事件字段（供应商 ID/名称、模型、状态码/业务码、原因、结果、时间）；端点仍位于已认证的管理路由后面。前端 `FailoverEventsView` 使用 Vue 插值渲染供应商名称和 ID，没有使用 `v-html`，因此用户可控的供应商名称会被 HTML 转义。未修改 SessionBrowser、SessionDetail、导出或 JSONL 逻辑。
   - 处置：未发现安全缺陷。

3. 残余语义说明：未存历史供应商名称的事件，会按当前配置名称回填。
   - 证据：名称回填发生在 API 读取阶段，来源是 `cfg.Providers`。如果旧事件没有存储名称，且之后 provider 被删除，则 ID 会按设计被抹空，缺失的名称也无法恢复。
   - 处置：对本次修复可接受，因为它保留了删除 provider 后不暴露悬空 ID 的隐私保护，并解决了当前 provider 展示成 ID 的问题。如果未来要求“删除后仍展示历史名称”，应在所有事件写入路径中持久化 provider 名称。

验证：

- `go test ./internal/admin -run 'TestFailoverEvents|TestProviderDeleteLeavesNoDanglingFailoverEventIDs' -count=1` 通过。
- `npm --prefix internal/frontend test -- src/views/FailoverEventsView.test.ts` 通过（195/195）。
- `git diff --check` 通过。

结论：提交 `8954320` 未发现遗留功能逻辑缺陷或安全缺陷。唯一残余说明是：旧事件如果没有存储供应商名称，且供应商后来被删除，则无法再恢复历史名称。
