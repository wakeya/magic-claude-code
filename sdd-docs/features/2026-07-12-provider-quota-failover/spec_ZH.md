# 供应商额度自动故障切换规格

本地页面：供应商管理与 Dashboard 主导航（`DashboardView.vue`）<br>
代理入口：`internal/proxy/handler.go`（`POST /v1/messages`、`POST /anthropic/v1/messages`）<br>
参考源站：`~/.claude/projects/`（84 个 JSONL、53 条 API 失败记录）、现有额度查询/重试代码<br>
技术栈：Go 1.26、SQLite、Vue 3<br>
最后更新：2026-07-12<br>
进度：5 / 5 implemented（见文末「实现验证证据」）

## 整体分析（源站分析）

`ResolveModel` 先路由已启用 `ExposedModel.ID`（会话内 `/model` 选择），再回退到 `ActiveProviderID`。只有后者可自动切换；前者固定路由，绝不改默认供应商。

完整 Claude 历史为 18 条 HTTP 400、16 条 429、2 条 401、2 条 403、4 条 404、1 条 502、10 条无状态失败。HTTP 状态本身不充分：

| 信号 | 分类 | 必须动作 |
|---|---|---|
| 429 `[1308]`、`[1310]`、`quota exhausted` | 额度耗尽 | 切换；摘除至重置时间，否则 15 分钟 |
| 400 `no healthy deployments for this model` | 当前供应商模型部署不可用 | 切换；摘除 1 分钟 |
| 401 invalid API key | 凭据失效 | 切换；仅 Token 变更或供应商测试成功恢复 |
| 403 Cloudflare、502、529、ECONNRESET | 供应商不可用 | Cloudflare 摘除 5 分钟，其余 1 分钟 |
| 400 `1210`、工具校验/tool_reference、普通 request error；404 model_not_found；上下文满 | 请求/模型错误 | 不切换 |

代理不能可靠取得 Claude `sessionId`。事件是 mcc 自己的全局 SQLite 列表；MUST NOT 写入、追加或改动 Claude JSONL 及导出消息。它在 `DashboardView` 主导航中作为“切换事件”一级 tab，紧邻既有“会话记录”tab，和“使用统计”的“概览 / 请求日志 / Provider / 模型”处于同一内容切换层级；不是会话详情或 `SessionBrowser` 的子 tab。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
|---|---|---|---|---|
| 1 | Done | 配置路由与原子更新 | setting、route marker、atomic mutation | `go test ./internal/config/...` |
| 2 | Done | Failover 领域 | classifier、SQLite state/events、recovery | `go test ./internal/failover/...` |
| 3 | Done | 代理集成 | replay、最终响应、active update | `go test ./internal/proxy/...` |
| 4 | Done | 额度/管理端接入 | reconciliation、Token recovery、API | `go test ./internal/admin/... ./internal/providerquota/...` |
| 5 | Done | 前端 | switch、global event panel | 前端测试/build |

## 需求

1. 新增 `Config.AutoFailoverEnabled bool \`json:"auto_failover_enabled"\``，默认 false；JSON 直接保存；SQLite settings 的键为 `auto_failover_enabled`，值为 `0`/`1`。
2. 新增 `ModelRoute{Provider *Provider; BackendModel string; DefaultRouted bool}` 和 `ResolveRoute`；保留 `ResolveModel` 包装。Exposed 命中绝非 default-routed。
3. 新增 `internal/failover` state/events。events 脱敏、最新优先，最多 1,000 条、最长 30 天；删除 provider 后不返回悬空 ID。
4. 分类器最多读取 64 KiB，非合格 response body 逐字节恢复；识别分析表中的情况，绝不靠裸状态码。`1308` 为 `five_hour_quota_exhausted`，`1310` 为 `weekly_quota_exhausted`；解析 RFC3339 和 `2006-01-02 15:04:05`，可信未来重置时间优先。
5. 候选是配置顺序中的 enabled、未摘除 provider：先 `candidate.MapModel(originalModel) == failedMappedModel`，后其余候选。每次从不可变客户端输入重建转换 body、URL、Token、header、format。
6. 第一个 `<400` 重试原子更新 `ActiveProviderID`、写 `switched`，且是唯一客户端响应。现有同 provider 429 retry 先运行。ExposedModel、不安全 method、已开始响应均不回放。
7. 新鲜 100% 额度快照可摘除，容量恢复可清额度 state；它 MUST NOT 清 401。只有已保存的非空 API Token 实际改变或 `POST /api/providers/{id}/test` 成功才清凭据 state；编辑名称/模型/URL 或测试失败均不清。
8. 新增认证后的 `GET/PUT /api/providers/failover` 与 `GET /api/failover/events?limit=1..100`；前端增加可访问标题 switch；`DashboardView` 主导航在“会话记录”旁增加“切换事件”一级 tab，后者展示全局事件页面，不修改 `SessionBrowser`、`SessionDetail`、会话内容渲染或 export。

## 任务详情

### 任务 1：配置路由与原子更新

#### 需求

**Objective（目标）** — 标识默认路由，并防止自动/手工配置写入相互覆盖。

**Outcomes（成果）** — JSON/SQLite Store 有 `AutoFailoverEnabled`、`ModelRoute`/`ResolveRoute` 与原子 config 更新。

**Evidence（证据）** — 测试证明 exposed route 为 false、active fallback 为 true、旧 SQLite 为 false、并发开关/active 更新均保留。

**Constraints（约束）** — 保持 `ResolveModel`、`GetActiveProvider`、JSON 兼容性、provider 顺序。

**Edge Cases（边界）** — disabled exposed、无 active、旧 DB、并发 activate/failover。

**Verification（验证）** — `go test -v -race ./internal/config/...` 通过。

#### 计划

文件：修改 `internal/config/config.go`、`store.go`、`sqlite_store.go`；测试 `config_test.go`、`store_test.go`、`sqlite_store_test.go`。

- [ ] 写 `TestResolveRoute*` 失败测试：exposed hit、fallback、disabled skip、nil provider。
- [ ] 执行 `go test ./internal/config -run TestResolveRoute -count=1`；预期：`ResolveRoute` 缺失而失败。
- [ ] 加 `ModelRoute`、`ResolveRoute`；`ResolveModel` 包装返回其中 provider/model。
- [ ] 再执行该命令；预期：通过。
- [ ] 写 `AutoFailoverEnabled` 持久化和 active-ID/开关并发更新的失败测试。
- [ ] 执行 `go test ./internal/config -run 'TestAutoFailover|TestAtomicConfig' -count=1`；预期：失败。
- [ ] 加 config 字段与 Store 原子更新：锁定、读最新、执行 `func(*Config) error`、校验、保存、返回已提交副本；SQLite 保存 `0`/`1`；所有配置写入走此路径。
- [ ] 执行 `go test -v -race ./internal/config/...`；预期：通过。
- [ ] 提交：`git add internal/config && git commit -m "feat(config): add atomic failover settings"`。

#### 验证

```bash
go test -v -race ./internal/config/...
```

逐项验收：

- [ ] `TestResolveRouteExposedModelIsNotDefaultRouted`：返回 exposed provider/`BackendModel`，且 `DefaultRouted=false`。
- [ ] `TestResolveRouteActiveFallbackIsDefaultRouted`：返回 active provider 的 `MapModel` 值，且 `DefaultRouted=true`。
- [ ] `TestResolveRouteSkipsDisabledExposedModel` 与 `TestResolveRouteWithoutActiveProvider` 保持既有 fallback/nil 语义。
- [ ] `TestAutoFailoverEnabledJSONRoundTrip`、`TestAutoFailoverEnabledSQLiteRoundTrip` 保存后重载仍为 true；旧数据库缺少 setting 时为 false。
- [ ] `TestAtomicConfigUpdatePreservesConcurrentActiveProviderAndFailoverSetting` 在 `-race` 下多次运行，最终同时保留新 active ID 和新开关值。

预期：命令返回 0；没有 data race；`ResolveModel` 的既有测试仍全绿。

### 任务 2：Failover 分类、state 与恢复

#### 需求

**Objective（目标）** — 持久化脱敏失败 state，仅分类已知额度/凭据/部署/可用性失败。

**Outcomes（成果）** — 新建 `internal/failover/{types,classifier,store,manager}.go`、表、保留、恢复、候选顺序。

**Evidence（证据）** — 表驱动测试覆盖整体分析全部信号、body 恢复、保留、Token/测试恢复、快照恢复、并发。

**Constraints（约束）** — 最多读 64 KiB；非合格 body 精确恢复；仅使用静态 provider 顺序。

**Edge Cases（边界）** — 畸形/超大 body、裸 429、无效 reset、Token 未变、测试失败、无额度查询。

**Verification（验证）** — `go test -v -race ./internal/failover/...` 通过。

#### 计划

文件：新建 `internal/failover/types.go`、`classifier.go`、`store.go`、`manager.go` 与测试；修改 `internal/config/sqlite_store.go` migration。

- [ ] 写 `TestClassify` 失败表：1308、1310、额度文字、healthy deployment、401、403、502/529/ECONNRESET 与所有不切换样例。
- [ ] 执行 `go test ./internal/failover -run TestClassify -count=1`；预期：package 缺失失败。
- [ ] 实现 `Classification{Eligible, Reason, UpstreamCode, DisabledUntil}` 与 `captureAndRestore`：用 `io.LimitReader(resp.Body, 64*1024+1)`，只解析 `error.code`、`error.message`、`code`、`message`、字符串 `error`；不合格时精确还原 bytes。
- [ ] 使用额度无 reset 15m、deployment/502/529/reset 1m、Cloudflare 5m；credential state 无时间恢复。
- [ ] 写 state/event 表、30 天/1,000、无 secret、过期、Token 改变/测试成功恢复、未改变不恢复、快照恢复的失败测试。
- [ ] 实现 CRUD/list/prune、`ClearCredentialFailure(providerID, tokenChanged, testSucceeded)`，仅 `tokenChanged || testSucceeded` 才清 401；manager 在一个 mutex 内按同模型后 fallback 选择。
- [ ] 执行 `go test -v -race ./internal/failover/...`；预期：通过。
- [ ] 提交：`git add internal/failover internal/config/sqlite_store.go && git commit -m "feat(failover): classify and quarantine providers"`。

#### 验证

```bash
go test -v -race ./internal/failover/...
```

逐项验收：

- [ ] `TestClassify1308WithReset` 与 `TestClassify1310WithReset` 识别 code、原因和未来重置时间；无效/过去/超过 7 天 reset 回退到 15 分钟。
- [ ] `TestClassifyHealthyDeployment400`、`TestClassifyInvalidAPIKey401`、`TestClassifyCloudflare403`、`TestClassifyAvailabilityFailures` 只对规定信号产生正确冷却或凭据 state。
- [ ] `TestBare429DoesNotFailover`、`TestClassify1210DoesNotFailover`、`TestModelNotFoundDoesNotFailover`、`TestContextLimitDoesNotFailover` 与工具兼容错误均断言 `Eligible=false`。
- [ ] `TestClassifierRestoresNonEligibleBody` 比较原始 response bytes；`TestOversizedBodyDoesNotFailover` 断言超过 64 KiB 不触发切换。
- [ ] `TestFailoverEventRetention` 同时验证删除 30 天前行和仅保留 1,000 条；`TestFailoverEventRedactsSecrets` 断言 token、请求 body、query 不进入 event。
- [ ] `TestCredentialFailureRequiresTokenChangeOrSuccessfulTest` 验证仅改名称/映射和失败测试不恢复；Token 改变、成功测试恢复。
- [ ] `TestConcurrentFailoverSelectionHasSingleWinner` 在 `-race` 下断言一个 active 更新和一条 `switched`。

预期：命令返回 0；所有 state/event 事务一致，且无 race。

### 任务 3：代理回放与默认激活

#### 需求

**Objective（目标）** — 回放合格默认请求，且不重复写响应、不泄漏跨 provider 转换/认证。

**Outcomes（成果）** — Proxy 选择候选，只记录/返回最终 response/provider。

**Evidence（证据）** — Httptest 证明开关关闭透传、同模型/fallback、默认持久化、`/model` 隔离、不切换错误、availability 切换、全耗尽。

**Constraints（约束）** — 现有 `DoWithRetry429` 先于分类；选择前不开始客户端响应。

**Edge Cases（边界）** — OpenAI format、queue/retry、并发、客户端断开。

**Verification（验证）** — `go test -v -race ./internal/proxy/...` 通过。

#### 计划

文件：修改 `internal/proxy/handler.go`、`internal/proxy/ratelimit/retry429.go`、`cmd/server/main.go`；测试 `internal/proxy/server_test.go` 与 failover 聚焦测试。

- [ ] 写 `TestFailover*` httptest 失败用例，覆盖 Evidence。
- [ ] 执行 `go test ./internal/proxy -run TestFailover -count=1`；预期：失败。
- [ ] 加 `Handler.SetFailoverManager(*failover.Manager)` 与输入 `(originalBody, provider, backendModel)` 的 helper，重新生成转换 body、URL、Token、header、format。
- [ ] 先运行同 provider retry，仅分类最终 response；候选应用自身 queue/retry，丢弃 response 先关闭。
- [ ] 仅 `<400` 保存 active，并写 `switched`/`retry_failed`/`exhausted`；usage 只记最终 response。
- [ ] 执行 `go test -v -race ./internal/proxy/...`；预期：通过。
- [ ] 提交：`git add internal/proxy cmd/server/main.go && git commit -m "feat(proxy): fail over default providers"`。

#### 验证

```bash
go test -v -race ./internal/proxy/...
```

逐项验收：

- [ ] `TestFailoverDisabledPasses1308Through`：客户端仍收到原 429，`active_provider_id` 不变，且无切换事件。
- [ ] `TestFailoverSwitchesSameMappedModelFirst`：1308 后首个同 mapped model 的 provider 收到重新构造的请求，响应为 2xx，active ID 和 `switched` event 指向它。
- [ ] `TestFailoverFallsBackInProviderOrder`：无同模型候选时按配置顺序；已摘除、禁用、失败来源 provider 均不访问。
- [ ] `TestFailoverNeverChangesExposedModelRoute`：`ExposedModel.ID` 请求收到 1308 时不重试、不改 active。
- [ ] `TestFailoverDoesNotSwitchRequestOrModelErrors` 覆盖裸 429、1210、404、工具兼容错误；`TestFailoverSwitchesAvailabilityFailure` 覆盖 502。
- [ ] `TestFailoverRebuildsOpenAIAndAnthropicRequests` 断言候选 URL、认证头、映射模型、格式转换来自候选而非失败 provider。
- [ ] `TestFailoverRecordsOnlyFinalUsage` 断言 usage 只有最终成功 provider 一行；`TestFailoverAllCandidatesExhausted` 不双写 header/body。

预期：命令返回 0；全量 proxy 回归通过，`go test -race` 不报 race。

### 任务 4：额度与管理端恢复 API

#### 需求

**Objective（目标）** — 协调额度证据，仅在有证明时恢复凭据 state，提供认证控制 API。

**Outcomes（成果）** — quota notifier/ticker、Token update/provider-test hook、failover API handler。

**Evidence（证据）** — admin 测试证明认证、method/body/limit、脱敏、Token 改变恢复、未变不恢复、测试成功恢复、event 顺序。

**Constraints（约束）** — 额度凭据可能不同于推理 Token；quota 成功不得清 401。

**Edge Cases（边界）** — 测试失败、删除/禁用 provider、过期 snapshot、非法 limit。

**Verification（验证）** — `go test -v -race ./internal/admin/... ./internal/providerquota/...` 通过。

#### 计划

文件：修改 `internal/providerquota/manager.go`、`internal/admin/server.go`、`provider_handler.go`；新建 `internal/admin/failover_handler.go`；添加 admin/quota 测试。

- [ ] 写开关/event API、认证、坏 method/body/limit 的失败测试。
- [ ] 执行 `go test ./internal/admin -run TestFailover -count=1`；预期：失败。
- [ ] 注册路由，只返回 `{"enabled":bool}` / `{"events":[...]}`，通过任务 1 原子更新。
- [ ] 写 Token 更新、成功/失败 provider test 恢复失败测试。
- [ ] 更新前比较旧/新 Token；只有非空 Token 改变才清 credential state；只有成功 test 才清；snapshot 持久化后和 30s ticker 做 reconciliation，只清 quota state。
- [ ] 执行 `go test -v -race ./internal/admin/... ./internal/providerquota/...`；预期：通过。
- [ ] 提交：`git add internal/admin internal/providerquota && git commit -m "feat(admin): expose failover controls and recovery"`。

#### 验证

```bash
go test -v -race ./internal/admin/... ./internal/providerquota/...
```

逐项验收：

- [ ] `TestFailoverSettingsRequireAuth` 返回 401；`TestFailoverSettingsMethods` 对错误 method 返回 405；错误 JSON/未知字段返回 400。
- [ ] `TestFailoverSettingsRoundTrip` PUT true 后 GET 为 true，重启/重载仍为 true；`TestFailoverEventsLimitAndOrder` 验证默认 50、1..100 钳制、按 `occurred_at DESC,id DESC`。
- [ ] `TestFailoverEventsDoNotExposeSecrets` 响应不含 API token、响应 body、带 query URL。
- [ ] `TestProviderTokenChangeClearsCredentialFailure` 仅非空且实际变更的 Token 清 401 state；`TestProviderEditWithoutTokenChangeKeepsCredentialFailure` 断言名称/URL/模型编辑不清。
- [ ] `TestSuccessfulProviderTestClearsCredentialFailure` 清 state 并产生 `recovered`；失败 provider test 仍摘除。
- [ ] `TestQuotaSnapshotRecoveryDoesNotClearCredentialFailure` 验证可用额度只恢复 quota state；`TestQuotaSnapshotExhaustionQuarantinesUntilReset` 验证 100% tier 的 reset。
- [ ] `TestProviderDeleteLeavesNoDanglingFailoverEventIDs` 验证 API 返回的 event 不含已删除 provider ID。

预期：命令返回 0；所有新端点保持认证，且 quota 凭据不泄露。

### 任务 5：供应商开关与全局事件 UI

#### 需求

**Objective（目标）** — 控制自动切换、查看全局事件，绝不修改 Claude JSONL 或导出内容。

**Outcomes（成果）** — 可访问供应商标题开关、`DashboardView` 主导航的“会话记录/切换事件”相邻一级 tab 及中英文本。

**Evidence（证据）** — 前端测试断言 API、switch rollback、主导航会话记录/切换事件的顺序和独立页面、全局说明/event 字段、无 SessionDetail/export mutation。

**Constraints（约束）** — 仅渲染转义文本；仅 Providers tab 激活时 15s 刷卡；不用 websocket。现有“会话记录”页面内容区不在本任务范围内，MUST 保持视觉和行为不变。

**Edge Cases（边界）** — 保存/API 失败、无 event、未选 session、无 target event、切换 tab 后刷新、时区展示。

**Verification（验证）** — 前端测试与 build 通过。

#### 计划

文件：修改 `internal/frontend/src/composables/useApi.ts`、`useI18n.ts`、`views/DashboardView.vue`；新增 `views/FailoverEventsView.vue`；增加/修改前端测试。除非编译器要求调整已有 type import，不得编辑 `components/SessionBrowser.vue`、`components/SessionDetail.vue`、会话 export 代码或 JSONL 渲染代码。

- [ ] 写 `getFailoverSettings`、`setFailoverSettings`、`getFailoverEvents`、标题 switch、主导航中紧邻 `tab.sessions` 的 `tab.failover`、独立切换事件页面、全局 transcript disclaimer、source/target/model/reason/outcome 的失败测试。
- [ ] 执行 `npm --prefix internal/frontend test -- --run`；预期：失败。
- [ ] 加 typed `FailoverEvent`/settings API、保存禁用/失败回滚 switch、active Providers tab 15s refresh。
- [ ] 在 `DashboardView.vue` 将 `MainTab` 扩为含 `'failover'`，在 `tabs` 中把 `{ key: 'failover', labelKey: 'tab.failover' }` 放在 `sessions` 之后；保持现有 `activeTab === 'sessions'` 分支以及所有 `SessionBrowser` props/children 和当前代码完全一致；新建 `FailoverEventsView.vue`，只在 `activeTab === 'failover'` 渲染并在进入/刷新时 fetch；不得传给 SessionBrowser/SessionDetail/export。
- [ ] 加语义一致中英文 i18n key。
- [ ] 执行 `npm --prefix internal/frontend test && npm --prefix internal/frontend run build`；预期：通过。
- [ ] 提交：`git add internal/frontend && git commit -m "feat(ui): show provider failover events"`。

#### 验证

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

逐项验收：

- [ ] `useApi` 测试断言 `getFailoverSettings` 使用 GET、`setFailoverSettings` 使用 PUT JSON、`getFailoverEvents` 安全编码 limit。
- [ ] `DashboardFailoverSwitch` 断言开关紧邻供应商管理标题，有 accessible label，保存中 disabled，PUT 失败时回滚，Providers tab 激活时才启动 15 秒刷新。
- [ ] `DashboardFailoverTab` 断言主导航 `tab.sessions` 后紧邻 `tab.failover`；`FailoverEventsView` 断言全局“不关联 Claude Code 对话记录”说明、source→target、model、reason、status/code、outcome、disabled-until；切换回会话记录时 `SessionBrowser` 状态、DOM 结构、已选会话、筛选条件、导出行为和 transcript 渲染均不被事件页修改。
- [ ] 断言 event 不作为 `SessionDetail.messages` 输入，导出调用仍只使用 `/api/sessions/{id}/export`。
- [ ] 中文/英文所有新增 i18n key 存在；API 失败显示现有错误状态且不破坏 JSONL 会话内容。

预期：两条命令均返回 0；产物生成到 `internal/frontend/dist`，无 TypeScript/Vite 错误。

## 实现验证证据（2026-07-12）

任务 1–5 全部实现并提交（分支 `provider-quota-failover`，本地 commit 未推送）。

验证命令与结果：

- `go test -race ./...`：1498 passed（含 `-race`，17 包）。
- `npm --prefix internal/frontend test`：174 passed。
- `npm --prefix internal/frontend run build`：成功，产物含 `FailoverEventsView` chunk。
- `git diff --check`：clean；`git status`：clean。

边界符合性自审（对应用户重点）：

- 自动切换只改 `ActiveProviderID`（默认路由）；`ExposedModel`（/model 会话路由）`DefaultRouted=false`，永不切换（`TestFailoverNeverChangesExposedModelRoute`）。
- 事件存 MCC 自己的 SQLite（`provider_failover_state` / `provider_failover_events`），不写、不改任何 `~/.claude/projects/**/*.jsonl`（grep 确认 failover 代码无 JSONL/文件写）。
- 事件字段完整：时间、原/目标供应商、原/映射模型、HTTP 状态码、业务码、原因、摘除至何时、结果（`FailoverEventsView`）。
- Dashboard 主导航 `tab.failover` 紧邻 `tab.sessions`；`SessionBrowser`/`SessionDetail`/会话导出未修改。
- 供应商管理标题右侧有可访问的自动切换开关，PUT 失败回滚；Providers tab 激活时 15s 刷新。
- 分类按 spec 表逐信号处理；裸 429 保持同供应商 retry 不切换；401 仅在非空 Token 实际变更或测试成功（非 401）才恢复；额度快照恢复绝不清凭据状态。
- 新 API 全部经 `authMiddlewareFunc`；响应只含 `{enabled}` / `{events:[…]}`，无 token/body/query。

已知限制 / 后续：

- 代理在故障切换命中后，原失败响应的上游连接在函数返回时才关闭（被既有 `defer resp.Body.Close()` 兜底，无泄露；仅是连接复用稍延后）。
- 供应商测试「成功」的凭据恢复判定为：测试请求完成且上游非 401（与既有测试端点语义一致）。
