# Kimi 配额查询与使用统计解析修复规格

本地页面：Dashboard 使用统计标签页（`DashboardView.vue` "使用统计"）、供应商配额弹窗（`ProviderUsageModal.vue`，供应商卡片"用量"按钮）  
代理入口：`POST /v1/messages`（用量记录链路）、`POST /api/providers/{id}/usage/query`（配额立即刷新）、`GET /api/usage/*`（使用统计 API）  
参考源站：kimi-code 参考实现 [`packages/oauth/src/managed-usage.ts`](https://github.com/MoonshotAI/kimi-code/blob/main/packages/oauth/src/managed-usage.ts)、历史规格 `2026-06-27-provider-quota-query`、`2026-06-11-usage-date-range-presets`、`2026-05-15-usage-statistics`  
技术栈：Go 1.26 `encoding/json`（后端）、Vue 3 + TypeScript（前端）、SQLite WAL（`data/proxy.db`）  
最后更新：2026-07-17  
进度：3 / 3 已完成（2026-07-17 验证通过）

## 整体分析（源站分析）

本分支修复三个相互独立的缺陷，源自同一条用户反馈："kimi coding plan 供应商查不了用量，使用统计里 provider 选 kimi-k3 看不到 token 统计"。每个症状各有根因，三个根因都在动任何代码之前基于真实环境（运行中的 `mcc` 容器、生产 `data/proxy.db`、真实供应商 token）逐一证实。

### 症状 1：kimi coding plan 配额查询始终报 `invalid_json`

**线上 API 实测（根因证据）。** 用 DB 中保存的 kimi-k3 token 直连真实端点，HTTP 200 且数据健康——端点、host 识别（`api.kimi.com` → `kimi`，`DetectTokenPlanProvider`，见 `internal/providerquota/token_plan.go:60-75`）、token 都不是问题：

```
GET https://api.kimi.com/coding/v1/usages
Authorization: Bearer <providers.api_token, id=provider-b3010ddf-73f3>

HTTP 200
{
  "user":  {"userId":"...","country":"CN"},
  "usage": {"limit":"100","used":"22","remaining":"78","resetTime":"2026-07-24T01:20:25.362103Z"},
  "limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
             "detail":{"limit":"100","used":"66","remaining":"34","resetTime":"2026-07-17T11:20:25.362103Z"}}],
  "parallel":   {"limit":"10"},
  "totalQuota": {"limit":"100","remaining":"99"},
  "subType": "TYPE_PURCHASE", "authentication": {...}
}
```

**根因链。** 旧 `queryKimi`（`internal/providerquota/token_plan.go`）把响应声明为：

```go
Usage struct {
    Limit     json.Number `json:"limit"`
    Remaining json.Number `json:"remaining"`
    ResetTime json.Number `json:"resetTime"`   // <-- 问题在这
}
```

1. `json.Number` **可以**接受数字字符串（`"100"`），所以 `limit`/`remaining` 从来不是障碍。
2. `resetTime` 线上是 **RFC3339 字符串**（`"2026-07-24T01:20:25.362103Z"`）。`encoding/json` 对 `json.Number` 拒绝非数字字符串（报 `json: invalid number literal, trying to unmarshal ... into Number`），整个 body 的 `json.Unmarshal` 失败，于是每一次真实查询都以 `invalid_json` 告终。
3. 线上 `limits[]` 没有 `name` 字段（窗口用 `limits[].window.{duration,timeUnit}` 描述），即使解析成功，tier 标签也是空字符串。
4. 旧测试（`TestParseKimiResponse`、`TestKimiIntegration`）的假数据用的是 JSON 数字、unix 秒时间戳和 `name` 字段——与真实 API 形态不符，这就是测试全绿而线上必挂的原因。

**参考实现对照（kimi-code）。** MoonshotAI/kimi-code 的 `managed-usage.ts` 请求同一端点、同一请求头，解析刻意宽松：`parseNumber` 同时接受数字与数字字符串；`resetTime` 按 ISO 字符串处理（还兼容 `reset_at/resetAt/reset_time` 多种拼写）；`limits[]` 标签由 `window.duration` + `window.timeUnit` 推导（300 MINUTE → "5h limit"）；`usage` 对象是周限额。mcc 的修复在 Go 侧对齐了同样的宽容度。

**运营侧说明。** 两个 kimi 供应商在 `providers` 表里持久化的 `quota_query_config` 均为 `{"enabled":false}`，`provider_quota_snapshots` 也没有它们的快照行，所以调度器从不查询、供应商卡片无任何用量展示。`POST /api/providers/{id}/usage/query`（"立即刷新"）在**已保存**配置 `enabled:true` 之前一律拒绝并报 `quota query not configured`（`internal/admin/provider_quota_handler.go:283`）——"测试"按钮则通过把表单作为草稿（强制 `enabled=true`）提交来绕过。部署本次解析修复后，用户需要在界面上重新勾选启用并保存配额配置。

### 症状 2：使用统计选 provider = kimi-k3 时全部为零

**后端数据一直是完整的。** 对运行中的管理端直接复现：

```
GET /api/usage/summary?provider_id=provider-b3010ddf-73f3
→ {"provider_requests_total":102,"token_consumption_total":5308687,"usage_coverage_percent":60.8,...}

GET /api/usage/summary?provider_id=provider-b3010ddf-73f3&tz=Asia/Shanghai&from=2026-07-10T00:00:00&to=2026-07-16T23:59:59
→ {"provider_requests_total":0,"token_consumption_total":0,...}   <-- 前端默认参数原样复放
```

**根因链。**

1. 使用统计标签页默认预设为 `last_7_days`（`DashboardView.vue:1029`）。
2. 旧 `usageDateRangeForPreset` 把 `last_7_days` 映射为 `inclusiveDateTimeRange(7, 1)`：from = 7 天前 00:00，**to = 昨天 23:59:59**（`dateTimeInputEndOfDaysAgo(1)`）。默认视图悄悄把"今天"排除在外；`last_30_days` 有同样的 off-by-one（`(30, 1)`）。
3. kimi-k3（`provider-b3010ddf-73f3`）创建于 2026-07-17T01:23 UTC，全部 102 条请求都发生在同一天。按用户时区（Asia/Shanghai）这些全部属于"今天"。
4. 默认范围 ∩ kimi-k3 流量 = ∅ → 所有汇总卡为 0，而其他有历史流量的供应商显示正常，让 kimi-k3 看起来"单独坏掉"。

### 症状 3：非流式 200 响应记录为 `usage_parse_status = parse_error`

审计 kimi-k3 覆盖率时发现（2 行），随后证实这是规模大得多的历史丢失。

**全库分布**（`usage_requests` ⨝ `usage_tokens`，`status_code = 200`；2026-07-17 调查时点的快照——数据库在线写入，计数会随流量增长）：

| stream | 解析状态 | 行数 | 供应商 |
| --- | --- | --- | --- |
| 0 | `parse_error` | **499** | GLM 4.7 ky (438)、Zhipu GLM 5.1 chanhu (25)、Zhipu GLM 5.1 ch (18)、Zhipu GLM 5.2 xs (16)、kimi-k3 (2) |
| 0 | `ok` | 73 | kimi code k2.6 xs (70)、MiniMax (2)、mimo (1) |
| 0 | `missing` | 2 | kimi code k2.6 xs |
| 1 | `ok` | 48,366 | （流式路径对所有供应商健康） |

**根因链。**

1. 现场复现：通过代理发一个极小的非流式请求（GLM 4.7 ky），客户端收到的是 **332 字节完全合法的 JSON**，但落库行仍是 `parse_error`。把这同一份字节离线喂给 `ExtractUsageFromJSON`，精确复现 `parse_error`。
2. 非 SSE 分支（`internal/proxy/handler.go:386`）用 `usage.ExtractUsageFromJSON(observer.Body())` 解析，其中 `Usage map[string]int64`。
3. 智谱（bigmodel）非流式响应在 `usage` 里混有**非数字字段**（直连上游证实）：`"server_tool_use":{"web_search_requests":0}`（对象）和 `"service_tier":"standard"`（字符串）。官方 Anthropic API 同样返回这两个字段。`json.Unmarshal` 到 `map[string]int64` 遇到其中任何一个都会失败 → 函数返回 `parse_error`（且不产生错误消息，所以这类行的 `usage_parse_error` 全为空字符串）。
4. kimi/MiniMax/mimo 的非流式响应 usage 只有数字字段 → 同一段代码对它们解析正常（73 行 `ok`）。kimi-k3 的 2 条失败是 Python-urllib 的大响应（18 KB / 30 KB），其 usage 也带了非数字字段。
5. 流式路径免疫的原因：`SSEObserver` 用显式字段的 `usageJSON` 结构体（`*int64`）解析，`encoding/json` 对未知字段直接忽略。

### 风险总结

1. `token_plan.go` 的改动局限于 Kimi 适配器；`kimiUsageDetail`/`kimiWindowLabel`/`parseKimiResetTime` 是新符号，`parseKimiResetTime` 签名变化（`json.Number` → `json.RawMessage`）在包外无调用方。
2. `usageDateRangeForPreset` 同时驱动 `activeUsageDateRangePreset` 高亮匹配；修改预设范围后该逻辑依然成立，因为两处使用同一函数。已保存自定义 `from/to` 的用户不受影响。
3. 统一提取器有意改变了一个历史行为：`usage` 值**类型**错误（如 `"usage":"n/a"`）现在返回 `missing` 而非 `parse_error`；语法非法的 body 仍返回 `parse_error`。SSE 合并语义（后到事件只覆盖"存在且为数字"的字段）保持不变。
4. 499 条历史 `parse_error` 行无法修复——代理不保留响应体——修复只对未来请求生效。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | ✅ 已完成 | Kimi 配额响应宽松解析 | `internal/providerquota/token_plan.go`、`internal/providerquota/token_plan_test.go` | ✅ 包测试全绿；端到端真实查询返回 5h tier（39%，重置 2026-07-17T16:20:25Z）+ 周限额 tier（36%，重置 2026-07-24T01:20:25Z），标签 "5h limit" |
| 2 | ✅ 已完成 | 使用统计日期预设包含今天 | `internal/frontend/src/views/DashboardView.vue`、`internal/frontend/src/views/DashboardUsageRequests.test.ts` | ✅ 前端 195/195 测试通过；`vite build` 成功；API 复现显示新默认范围覆盖 kimi-k3 的 102 条请求 |
| 3 | ✅ 已完成 | 流式/非流式统一宽松 usage 提取 | `internal/usage/parse.go`、`internal/usage/sse.go`、`internal/usage/parse_test.go`、`internal/usage/sse_test.go` | ✅ `go test ./...` 全绿；此前失败的真实 bigmodel 响应体现在解析为 `provider/ok`（input=6, output=1）；含垃圾字段的 SSE 流 `diag.ParseErrors == 0` |

## 需求

### 交付物

1. `internal/providerquota/token_plan.go`：`queryKimi` 按线上真实形态解析——`limit`/`used`/`remaining` 用 `json.Number`（兼容数字与数字字符串），`resetTime` 用 `json.RawMessage` 并由 `parseKimiResetTime` 解析（RFC3339/RFC3339Nano 字符串、unix 秒/毫秒、带引号或裸值均可），`limits[]` 标签回退链 `name → title → scope → window 推导`（`kimiWindowLabel`，300 MINUTE → "5h limit"），显式 `used` 优先于 `limit - remaining`，周限额 tier 补齐 `Remaining`，忽略未知顶层字段（`user`、`parallel`、`totalQuota`、`authentication`、`subType`、`boosterWallet`）。
2. `internal/frontend/src/views/DashboardView.vue`：`usageDateRangeForPreset` 把 `last_7_days` 映射为 `inclusiveDateTimeRange(6, 0)`、`last_30_days` 映射为 `inclusiveDateTimeRange(29, 0)`（"近 N 天" = 今天 + 之前 N−1 天）；`today` 保持 `(0, 0)`。
3. `internal/usage/parse.go`：共享宽松提取器——`usageCounterKeys`、`extractUsageValues(json.RawMessage)`、`parseUsageFields(json.RawMessage) map[string]int64`、`usageFieldInt64(json.RawMessage) (int64, bool)`——接受 JSON 数字、浮点（`174.0`）、数字字符串（`"174"`），忽略其它一切值；`ExtractUsageFromJSON` 基于它重写。
4. `internal/usage/sse.go`：事件 payload 的 `usage`/`message.usage` 改为 `json.RawMessage`；`merge` 复用同一提取器并保持"仅覆盖存在字段"语义；删除 `usageJSON` 结构体。
5. 覆盖两条路径全部容忍维度的测试（详见各任务）。

### 约束

1. 端点、host 识别（`api.kimi.com` → `kimi`）、认证头（`Authorization: Bearer`）、`TokenPlanAdapter`/`Adapter` 接口均不变。
2. SSE 合并保持逐字段覆盖语义：后到事件仅当某字段在其中"存在且为数字"时才覆盖该计数器；任何存在且为数字的字段（包括真实的 0）都置 `HasAny`，与此前完全一致。
3. `ExtractUsageFromJSON` 保持契约：`(UsageValues, source, status)`；顶层 JSON 非法 → `parse_error`；`usage` 缺失/为空/全零/不可用 → `missing`；否则 `provider`/`ok`。
4. 前端仅改 `usageDateRangeForPreset`；`inclusiveDateTimeRange`、`activeUsageDateRangePreset`、i18n key、后端过滤器（`parseFilterTime` 已支持 datetime-local 格式）均不动。
5. 本仓库 `internal/frontend/dist` 随源码提交，前端改动须附带重新构建的产物（`npm --prefix internal/frontend run build`）。

### 边界情况

1. `resetTime` 为 unix **毫秒**（> 1e12）→ `time.UnixMilli`；带引号数字字符串（`"1753228825"`）→ 按 epoch 解析；`null`/缺失/乱码 → 零值 `time.Time`，tier 省略 `ResetsAt` 即可。
2. `used` 显式为 `0` → 尊重显式零（不被 `limit - remaining` 替代）；`remaining > limit` 时推导值钳到 ≥ 0；超限窗口 `used > limit`（被拒请求仍计数）→ utilization 经 `kimiUtilization` 钳到 100——`NormalizeTier` 拒绝 [0,100] 之外的值，不钳位会让单个超限 tier 把整个查询拖成 `invalid_response`——`Used`/`Remaining` 展示值保留 API 原样。
3. `window.timeUnit` 为枚举形式 `TIME_UNIT_MINUTE` → 经大小写不敏感的 `strings.Contains` 匹配；未知单位或非正 duration → 空标签（tier 仍有效）。
4. bigmodel `usage` 含 `server_tool_use`（对象）、`service_tier`（字符串）或未来任何非数字字段 → 忽略，已知计数器照常提取（SSE 与非 SSE 一致）；超出 int64 范围的计数器值（如 `1e300`）→ 按垃圾字段忽略，避免统计中落入平台相关的溢出值。
5. `usage` 存在但类型不是对象（`"n/a"`、数字、数组）→ `missing`，不是 `parse_error`。
6. 流量全在"今天"的供应商（新建供应商）→ 任务 2 后在默认 `last_7_days` 预设下可见；`today` 预设语义不变。
7. 浏览器任意时区：`from`/`to` 按浏览器本地时间生成，后端按携带的 `tz` 解析；包含今天后，当天新供应商的可见性不再依赖用户时区。

### 非目标

1. 不回溯修复 499 条历史 `parse_error` 行（响应体未持久化，数据不可恢复）。
2. 不代为启用两个 kimi 供应商的 `quota_query_config`（`enabled:false` 是用户侧状态，界面保存流仍是唯一权威）。
3. 不处理 usage 键名别名（`inputTokens`、`prompt_tokens` 等）：OpenAI 格式供应商在观测前已被 `convertOpenAINonStreamingResponse` 统一转成 Anthropic 形态，且无证据表明有 Anthropic 端点供应商乱写键名——明确不做投机性别名。
4. 不采集 `server_tool_use` / `service_tier` 的值——`usage_tokens` 表没有对应列。
5. 不改配额调度器、快照存储或 `Stop()` 语义；不新增配置项。
6. git 提交不在本规格范围内（按用户要求，改动以未提交状态保留在分支 worktree 中）。

## 任务详情

### 任务 1：Kimi 配额响应宽松解析

#### 需求

**Objective（目标）** — 让 `queryKimi` 能解析真实的 `GET https://api.kimi.com/coding/v1/usages` 响应（RFC3339 `resetTime`、数字字符串计数器、`window` 描述的 limits、无 `name`），消除永久性的 `invalid_json` 失败，容忍度对齐 kimi-code 的 `managed-usage.ts`。

**Outcomes（成果）** — `token_plan.go` 新增 `kimiUsageDetail`（第 133 行）、`usedOrDerived`（141）、`kimiWindowLabel`（262）和基于 `json.RawMessage` 的 `parseKimiResetTime`（286）；`queryKimi`（152）能从线上 payload 产出正确的 5 小时与周限额 tier；测试改用线上形态假数据，并保留旧形态的向后兼容用例。

**Evidence（证据）** — `go test ./internal/providerquota/` 通过；端到端运行 `TokenPlanAdapter.Query("kimi", ..., "https://api.kimi.com/coding/", <真实 token>)` 成功，返回 tier `five_hour`（利用率 39，重置 2026-07-17T16:20:25Z，标签 "5h limit"）与 `seven_day`（利用率 36，重置 2026-07-24T01:20:25Z，剩余 64）。

**Constraints（约束）** — 端点/请求头/接口不变；忽略未知响应字段；标签回退顺序固定为 `name → title → scope → window`；新增 `strconv` 导入。

**Edge Cases（边界）** — `resetTime` 各变体（RFC3339Nano、unix 秒、unix 毫秒、数字字符串、null、乱码）；`used` 显式零与推导；`remaining > limit` 钳位；`TIME_UNIT_*` 枚举单位；`detail` 字段缺失且 `limit <= 0` 时跳过。

**Verification（验证）** — 包测试全绿；真实查询成功；旧形态假数据仍可解析（向后兼容）。

#### 计划

1. 在 `internal/providerquota/token_plan.go` 中替换原匿名响应结构体与辅助函数（旧 131–233 行）：
   - `kimiUsageDetail{ Limit, Used, Remaining json.Number; ResetTime json.RawMessage }`，由 `limits[].detail` 与 `usage` 共用。
   - `usedOrDerived(limit, remaining float64) float64`——显式 `used`（含真实的 `0`）优先，否则 `max(limit-remaining, 0)`。
   - `limits[]` 项新增 `Title`、`Scope` 与 `Window{ Duration json.Number; TimeUnit string }`；标签取 `Name/Title/Scope/kimiWindowLabel(Window)` 中第一个非空值。
   - `kimiWindowLabel(duration, timeUnit)`——分钟数能被 60 整除 → `"<h>h limit"`，其余分钟 → `"<m>m limit"`，小时 → `"<h>h limit"`，秒 → `"<s>s limit"`，否则 `""`；单位按大小写不敏感子串（`MINUTE`/`HOUR`/`SECOND`）匹配，兼容 `TIME_UNIT_MINUTE`。
   - `parseKimiResetTime(raw json.RawMessage) time.Time`——trim；`null`/空 → 零值；带引号值先 `strconv.Unquote` 再试 `time.Parse(time.RFC3339Nano, …)`；最后 `strconv.ParseFloat` → unix 秒，> 1e12 时 `time.UnixMilli`。
   - `kimiUtilization(used, limit)`——百分比钳到 [0, 100]，使超限 tier 通过 `NormalizeTier` 校验；`Used`/`Remaining` 展示值保留 API 原样。
   - 周限额 tier 设置 `Remaining`（此前只有 `Used`/`Total`）。
2. 重写 `TestParseKimiResponse` 与 `TestKimiIntegration` 的假数据为线上形态（字符串计数器、RFC3339Nano `resetTime`、无 `name` 的 `window`）；新增 `TestParseKimiResetTime`（8 例）、`TestKimiUsedOrDerived`（3 例）、`TestKimiWindowLabel`（6 例）、`TestKimiUtilization`（4 例，含超限钳位）、`TestKimiIntegrationOverQuota`（used=120/limit=100 → 查询成功、utilization 100、used 展示 120）与 `TestKimiIntegrationLegacyShape`（数字字段、unix `resetTime`、`name` 标签）。
3. 运行 `go test ./internal/providerquota/` 与 `go test ./...`。
4. 端到端探针（临时 `main.go` 置于 `.tmp-kimi-check/`，用后删除）：用真实 kimi-k3 token 执行 `providerquota.NewTokenPlanAdapter(10s).Query(ctx, "kimi", nil, "https://api.kimi.com/coding/", token)`，确认 `success:true` 且两个 tier 正确。

#### 验证

- [x] `go test ./internal/providerquota/` — ok（2.749s）；`go test ./...` — 15 个包全部 ok，`go vet` 干净。
- [x] 端到端真实探针（2026-07-17）：`success:true`，tier `five_hour` 标签 "5h limit"，used 39 / total 100 / remaining 61，重置 2026-07-17T16:20:25Z；tier `seven_day` used 36 / total 100 / remaining 64，重置 2026-07-24T01:20:25Z。
- [x] `TestKimiIntegrationLegacyShape` 证明旧假数据形态（JSON 数字、unix `resetTime`、`name`）仍可解析——对旧版 API 无回归。

### 任务 2：使用统计日期预设包含今天

#### 需求

**Objective（目标）** — 修改使用统计标签页的预设范围，使"近 N 天"包含今天，让流量全在当天的供应商（如新建的 kimi-k3）在默认视图下可见。

**Outcomes（成果）** — `usageDateRangeForPreset`（`DashboardView.vue:1905`）对 `last_7_days` 返回 `inclusiveDateTimeRange(6, 0)`、对 `last_30_days` 返回 `inclusiveDateTimeRange(29, 0)`；源码断言测试同步更新到新契约。

**Evidence（证据）** — 变更前按前端原默认参数复放（`from=2026-07-10T00:00:00&to=2026-07-16T23:59:59&tz=Asia/Shanghai`）请求 `/api/usage/summary?provider_id=provider-b3010ddf-73f3` 返回全零；新范围（`to` = 今天 23:59:59）覆盖了无过滤 API 早已报告的 102 请求 / 5,308,687 tokens。

**Constraints（约束）** — 仅改预设边界；`inclusiveDateTimeRange`、`dateTimeInputStartOfDaysAgo`、`dateTimeInputEndOfDaysAgo`、`activeUsageDateRangePreset`、i18n key 不动；后端 `parseFilterTime` 无需修改（已支持带秒的 datetime-local）。

**Edge Cases（边界）** — `today` 预设不变；用户自定义 `from`/`to` 不受影响；预设高亮匹配因与 `usageDateRangeForPreset` 共用同一输出而保持正确。

**Verification（验证）** — 前端 195/195 单测通过；生产构建成功；dist 产物随改动一并重新生成。

#### 计划

1. 在 `internal/frontend/src/views/DashboardView.vue` 中修改 `usageDateRangeForPreset`：`last_7_days` → `inclusiveDateTimeRange(6, 0)`，`last_30_days` → `inclusiveDateTimeRange(29, 0)`，并加注释说明范围包含今天、避免当天流量的供应商被默认滤掉。
2. 在 `internal/frontend/src/views/DashboardUsageRequests.test.ts` 中将测试改名为 `usage date range presets default to the last 7 days including today`，两条正则断言更新为 `inclusiveDateTimeRange(6, 0)` / `inclusiveDateTimeRange(29, 0)`。
3. 运行 `npm --prefix internal/frontend test` 与 `npm --prefix internal/frontend run build`（重新生成 `dist/assets/index-*.js` 等，本仓库提交 dist）。

#### 验证

- [x] `npm --prefix internal/frontend test` — 195/195 通过（8.26s）。
- [x] `npm --prefix internal/frontend run build` — 10.13s 构建成功；`dist` 已重新生成（旧 hash 包删除、新包加入）。
- [x] API 层前后对照（2026-07-17）：旧默认范围 → `provider_requests_total: 0`；无过滤/新范围 → `provider_requests_total: 102`、`token_consumption_total: 5,308,687`、`usage_coverage_percent: 60.8`。

### 任务 3：流式/非流式统一宽松 usage 提取

#### 需求

**Objective（目标）** — 用一个共享的宽容提取器替换脆弱的非流式 `map[string]int64` 解析与流式 `usageJSON` 类型化解析：任何在 usage 中新增非数字字段的供应商（bigmodel 现状，官方 Anthropic API 也包含）或字段值类型漂移（数字字符串、浮点）时，两条路径都能继续记录 token。

**Outcomes（成果）** — `internal/usage/parse.go` 新增 `usageCounterKeys`（第 61 行）、`extractUsageValues`（72）、`parseUsageFields`（90）、`usageFieldInt64`（110）；`ExtractUsageFromJSON`（42）基于它们重写；`internal/usage/sse.go` 的 payload 对 `usage`/`message.usage` 改用 `json.RawMessage`（164），`merge`（215）复用 `parseUsageFields`；删除 `usageJSON`。

**Evidence（证据）** — 生产环境中记录为 `parse_error` 的那份 332 字节 bigmodel 响应体，修复后解析为 `provider/ok`（input=6, output=1）；含 `server_tool_use`/`service_tier` 的 SSE 流 `diag.ParseErrors == 0`；`go test ./...` 全绿。

**Constraints（约束）** — `ExtractUsageFromJSON` 的 `(values, source, status)` 契约保持（顶层 JSON 非法 → `parse_error`；usage 不可用/缺失/全零 → `missing`）；SSE 合并仅覆盖"存在且为数字"的字段；不做键名别名；不新增采集字段。

**Edge Cases（边界）** — `usage:"n/a"`（类型错误）→ `missing`；`usage:null` / `{}` / 全零 → `missing`；数字字符串与浮点计数器经 `int64(f)` 截断；SSE 中显式 `0` 仍置 `HasAny`（字段存在），保持 `hasUsage` 的既有统计口径。

**Verification（验证）** — 两条路径新增单测；用抓取的生产响应体做离线回归；全量测试通过。

#### 计划

1. 在 `internal/usage/parse.go` 中：
   - 新增 `usageCounterKeys = []string{"input_tokens","output_tokens","cache_creation_input_tokens","cache_read_input_tokens"}`。
   - 新增 `usageFieldInt64(raw json.RawMessage) (int64, bool)`——先 `json.Unmarshal` 到 `json.Number`（兼容 JSON 数字与合法数字字符串），再 `Float64()` → `int64(f)`；缺失/垃圾值以及超出 int64 范围的值（如 `1e300`，直接转换会落入平台相关的溢出值）返回 `ok=false`。
   - 新增 `parseUsageFields(raw) map[string]int64`——把 `raw` 解到 `map[string]json.RawMessage`（失败 → 空 map），只保留存在且为数字的计数器键。
   - 新增 `extractUsageValues(raw) UsageValues`——由 `parseUsageFields` 组装四个计数器并按非零值计算 `HasAny`（语义不变）。
   - 重写 `ExtractUsageFromJSON`：顶层 `{"usage": json.RawMessage}`；解包失败 → `parse_error`；缺失/`!HasAny` → `missing`；否则 `provider`/`ok`。
2. 在 `internal/usage/sse.go` 中：
   - `observeBlock` 的 payload 改为 `Usage json.RawMessage` 与 `Message.Usage json.RawMessage`。
   - 重写 `merge(raw json.RawMessage)`：遍历 `parseUsageFields(raw)`，仅覆盖返回的字段，并按返回字段置 `HasAny`。
   - 删除 `usageJSON` 结构体。
3. 测试：
   - `parse_test.go`：`TestExtractUsageToleratesNonNumericFields`（bigmodel 形态）、`TestExtractUsageToleratesNumericStringsAndFloats`、`TestExtractUsageNullAndZeroAndJunkUsage`（5 个子例）、`TestExtractUsageInvalidJSONReturnsParseError`、`TestExtractUsageRejectsOutOfRangeNumbers`（唯一字段越界 → missing；混合 → 正常字段保留）。
   - `sse_test.go`：`TestSSEObserverToleratesNonNumericUsageFields`（断言 `diag.ParseErrors == 0`）、`TestSSEObserverToleratesNumericStringsAndFloats`。
4. 离线回归：临时探针（`go run`）对抓取的生产响应体 `/tmp/mccdbg/body2.bin` 执行 `ExtractUsageFromJSON`，对比修复前（→ `parse_error`）与修复后（→ `provider/ok`）；探针用后删除。
5. 运行 `go test ./...` 与 `go vet ./...`。

#### 验证

- [x] `go test ./...` — 15 个包全部 ok；`go vet` 干净；`usageJSON` 无残留引用。
- [x] 抓取响应体回归（2026-07-17）：修复前 `source=none status=parse_error`；修复后 `source=provider status=ok values={InputTokens:6 OutputTokens:1 ... HasAny:true}`。
- [x] `TestSSEObserverToleratesNonNumericUsageFields` 确认垃圾字段不再增加 `diag.ParseErrors`，usage 仍正确合并（input=10, output=7）。
- [x] 现场复现证据链保留在本规格"整体分析"中：499 条历史 `parse_error`（bigmodel 497 + kimi 2）、73 条 `ok` 全部来自纯数字 usage 的供应商——分布与根因完全吻合。
