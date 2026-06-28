# 供应商额度查询规格

本地页面：`/` 供应商管理、`/providers/:providerId/usage` 用量查询配置
代理入口：不修改模型代理链路；新增认证后的 `/api/providers/*/usage*` 管理 API
参考实现：`/home/www/workspace/open-software/cc-switch` 的 `UsageScriptModal`、`SubscriptionQuotaFooter`、`services/coding_plan.rs`、`services/balance.rs`
技术栈：Go 1.26 + `net/http` + SQLite + 受限 JavaScript 运行时（推荐 `github.com/dop251/goja`）+ Vue 3 + TypeScript + Tailwind + `lucide-vue-next`
最后更新：2026-06-27
状态：draft
进度：0 / 10 已规划

## 整体分析（源站分析）

### 1. 目标与术语

本功能在“供应商管理”中增加供应商账户额度查询。它与现有“使用统计”功能不同：

| 功能 | 数据来源 | 含义 |
| --- | --- | --- |
| 现有 `internal/usage` | 代理响应和 Claude Session 日志 | 已发生的请求数、Token 消耗、覆盖率和请求质量 |
| 本功能“供应商额度查询” | 供应商余额/套餐接口 | 当前账户还剩多少余额，或 5 小时/7 天等套餐窗口已使用多少 |

为了避免代码和 API 混淆，后端包统一命名为 `providerquota`，页面中文仍显示“用量”。不得把新功能并入 `internal/usage`，也不得改变现有 usage 表或统计口径。

本期支持五类查询：

1. `custom`：自定义 HTTP 请求和 JavaScript 响应提取器。
2. `general`：可编辑的通用余额模板。
3. `newapi`：NewAPI/One API 风格面板账户额度。
4. `token_plan`：Kimi、智谱、MiniMax、ZenMux、火山方舟套餐窗口。
5. `official_balance`：DeepSeek、StepFun、SiliconFlow、OpenRouter、Novita AI 官方余额。

本期明确不支持 Claude/Codex/Gemini CLI OAuth 官方订阅查询。该能力读取服务端本机 OAuth，不能与某张供应商卡片的 API Token 一一对应，后续应作为独立功能评估。

Xiaomi MiMo Token Plan 也暂不纳入本期原生适配器。控制台虽然存在可读取套餐额度的私有接口，但已验证其依赖小米账号浏览器会话 Cookie，而不是供应商卡片中的 `tp-...` 推理 API Key；官方文档未公开服务端额度查询协议。详细结论见“Xiaomi MiMo 调查与延期决策”。

### 2. 当前项目状态

当前供应商配置由 `internal/config.Provider` 表达，通过 `ConfigStore` 保存。生产环境使用 SQLite：

- `providers` 表保存供应商主字段和明文 API Token。
- `provider_model_mappings` 保存模型映射。
- `providerResponseMap` 向前端返回 `api_token_mask`，不返回明文 Token。
- `/api/providers/{id}/reveal-token` 是已有的显式明文读取入口。
- 供应商列表由 `DashboardView.vue` 渲染，卡片由 `ProviderCard.vue` 渲染。
- Vue Router 当前只有 `/login` 和 `/`，可直接增加独立用量配置路由。
- 前端已有 `lucide-vue-next`，无需引入另一套图标库。
- 现有 `internal/usage` 使用相同 SQLite DB，但专门负责代理请求统计。

### 3. cc-switch 行为结论

cc-switch 将不同供应商统一为两类输出：

1. 时间窗口额度：`five_hour`、`seven_day/weekly_limit`、`monthly`，核心字段是已用百分比和重置时间。
2. 余额：总额、已用、剩余、单位、套餐名。

截图中的 `5小时: 2%` 表示已使用 2%，不是剩余 2%。颜色阈值为：

- `< 70%`：绿色。
- `70%–89%`：橙色。
- `>= 90%`：红色。

紧随百分比的 `2h30m` 是距离重置的倒计时；“15 分钟前”是最近查询时间；刷新图标触发手动查询。

不同供应商返回能力不一致，因此具体数量字段必须可选：

- MiniMax 当前主要返回剩余百分比，需转换为 `100 - remaining_percent`。
- 智谱返回使用百分比。
- Kimi 返回 limit/remaining，可同时保留具体数量并计算百分比。
- ZenMux 可返回 USD 已用/上限。
- 部分供应商只返回一个 5 小时窗口，不得伪造周窗口。

### 4. 技术方案比较

#### 方案 A：所有供应商均使用 JavaScript 脚本

优点：扩展最快，新增供应商无需编译 Go。
缺点：已知供应商逻辑散落在字符串脚本中，凭据处理、签名、错误分类和回归测试较弱；火山 AK/SK 签名不适合脚本模板。
结论：不采用。

#### 方案 B：所有供应商均使用 Go 原生适配器

优点：类型安全、易测试、可严格控制网络和凭据。
缺点：无法满足用户自定义接口；任何第三方中转协议变化都需要发布新版本。
结论：不采用。

#### 方案 C：Go 原生适配器 + 受限脚本提取器（选定）

- `token_plan` 和 `official_balance` 使用 Go 原生适配器。
- `general`、`newapi`、`custom` 使用统一的受限脚本执行器。
- 所有结果归一化为同一个 `ProviderQuotaResult`。
- 查询管理器统一负责缓存、持久化、自动调度、手动刷新和并发去重。

该方案兼顾已知协议的可靠性与未知协议的扩展性，且能把敏感网络能力留在 Go 层。

### 5. 总体架构

```text
ProviderCard / ProviderUsageView
              │
              ▼
Authenticated Admin API
              │
              ▼
providerquota.Manager
  ├── config resolver（供应商配置 + 用量专用覆盖凭据）
  ├── scheduler / singleflight / concurrency limit
  ├── script executor
  │     ├── custom
  │     ├── general
  │     └── newapi
  ├── native adapters
  │     ├── token plan
  │     └── official balance
  └── snapshot store（SQLite 最新快照）
```

组件边界：

- `providerquota.Manager`：唯一查询入口，调用方不能直接调用 adapter。
- `providerquota.Store`：只管理最新查询快照，不管理供应商配置。
- `providerquota.ScriptExecutor`：只解析脚本、构造受控请求和提取响应，不拥有调度逻辑。
- `providerquota.Adapter`：每个原生适配器只负责一种上游协议到统一结果的转换。
- Admin handler：只做认证后的 HTTP 参数解析、状态码映射和脱敏响应。
- Vue 页面：不接触明文供应商 Token，不自行请求第三方 API。

### 6. Xiaomi MiMo 调查与延期决策

#### 6.1 已确认的控制台接口

通过已登录的 Xiaomi MiMo 控制台页面及其前端网络请求，确认以下同源接口：

```text
GET  https://platform.xiaomimimo.com/api/v1/tokenPlan/detail
GET  https://platform.xiaomimimo.com/api/v1/tokenPlan/usage
POST https://platform.xiaomimimo.com/api/v1/usage/token-plan/list?<会话参数>
```

`tokenPlan/usage` 返回套餐总量和当前消耗：

```json
{
  "data": {
    "usage": {
      "percent": 0.74,
      "items": [
        {
          "name": "plan_total_token",
          "used": 740,
          "limit": 1000,
          "percent": 0.74
        }
      ]
    }
  }
}
```

`tokenPlan/detail` 返回 `planCode`、`planName`、`currentPeriodEnd`、`expired` 等套餐状态。明细接口按 `year`/`month` 返回 `date`、`model`、`totalToken`、`inputHitToken`、`inputMissToken`、`outputToken`、`requestCount` 和 `inputAudioDuration`。

#### 6.2 不能直接接入的原因

1. 去除浏览器 Cookie 后，`tokenPlan/detail` 和 `tokenPlan/usage` 均返回 HTTP 401。
2. 控制台前端使用 `credentials: "same-origin"`，并依赖 `api-platform_ph`、`api-platform_slh`、`api-platform_serviceToken`、`userId` 等登录 Cookie。
3. 控制台请求没有使用 Token Plan 的 `tp-...` 推理 API Key 作为额度查询凭据。
4. 前端资源将相关 Cookie 描述为登录 Cookie，生命周期约 24 小时；它们不适合作为服务器长期凭据。
5. 官方 Token Plan/FAQ 文档只说明在控制台查看用量，没有公开额度接口、稳定性承诺或 API-Key 鉴权协议。
6. 私有接口和会话参数可能随前端发布变化，无法达到本功能对自动调度和持久化查询的可靠性要求。

#### 6.3 本期决策

- 不为 Xiaomi MiMo 增加 native adapter。
- 不要求用户粘贴、导入或持久化小米账号 Cookie。
- 不通过浏览器自动化、DOM 抓取或调用 `/apiKey/raw` 实现后台额度查询。
- 不把推理 `tp-...` Key 发送到控制台私有接口进行试探性认证。
- 当 Base URL 命中 `token-plan-{cn,sgp,ams}.xiaomimimo.com` 时，配置页可显示“当前没有稳定的 API-Key 额度查询接口”，但不得将其误判为通用 Token Plan adapter。

#### 6.4 未来重新评估条件

满足以下任一条件时可重新评估：

1. Xiaomi 官方文档发布额度查询 endpoint 和请求/响应契约。
2. 官方确认 `tp-...` API Key 或独立长期凭据可用于服务端只读额度查询。
3. 官方 SDK/CLI 提供稳定的额度查询能力，并明确允许在 Coding 工具后端使用。

未来若具备稳定鉴权，建议映射：

```text
window       = plan_period
used         = usage.items[name=plan_total_token].used
total        = usage.items[name=plan_total_token].limit
utilization  = usage.items[name=plan_total_token].percent * 100
resetsAt     = detail.currentPeriodEnd（按 UTC 解析）
unit         = Credits
```

MiMo 当前是月度/年度套餐总 Credits，不得伪造成 5 小时或 7 天窗口。

官方参考文档：

- `https://mimo.mi.com/docs/price/tokenplan/subscription`
- `https://mimo.mi.com/docs/en-US/quick-start/faq/api-integration`

## 开发检查清单

| 序号 | 状态 | 任务 | 主要产出 | 核心验证 |
| --- | --- | --- | --- | --- |
| 1 | 已规划 | 类型、配置与 SQLite schema | `internal/providerquota/types.go`、Provider 配置字段、快照表 | schema、JSON/SQLite round-trip |
| 2 | 已规划 | 受限脚本执行器 | `script.go`、网络安全和归一化 | 超时、同源、响应上限、提取测试 |
| 3 | 已规划 | 官方余额适配器 | `balance.go` | 5 类供应商 fixture 测试 |
| 4 | 已规划 | Token Plan 适配器 | `token_plan.go`、火山签名 | 5 小时/周/月解析和签名测试 |
| 5 | 已规划 | Manager、缓存和自动调度 | `manager.go`、`store.go` | singleflight、并发、due 计算、持久化 |
| 6 | 已规划 | 管理 API | `internal/admin/provider_quota_handler.go` | 方法、404、校验、脱敏、刷新 |
| 7 | 已规划 | 前端 API、路由和配置页 | `ProviderUsageView.vue`、`useApi.ts` | 路由、加载、保存、测试查询 |
| 8 | 已规划 | 供应商卡片展示和按钮图标 | `ProviderCard.vue`、Dashboard 接线 | 布局、百分比、倒计时、刷新状态 |
| 9 | 已规划 | 导入/导出/复制/删除与 i18n | provider handler、双语文案 | 生命周期与秘密字段回归 |
| 10 | 已规划 | 全量验证和文档状态更新 | 测试证据、构建产物 | Go、frontend test/build、race/diff |

## 需求

### 1. 功能范围

#### 1.1 必须交付

1. 每个供应商可独立启用和配置额度查询。
2. 提供独立路由 `/providers/:providerId/usage`，不是弹窗。
3. 供应商卡片标题行展示最近查询时间、手动刷新、5 小时和 7 天已用百分比，并靠近“使用中”标记。
4. 卡片操作顺序固定为：编辑、复制、用量、测试、设为当前（条件显示）、启用/禁用、删除。
5. 编辑、复制、用量、测试四个文字按钮前必须使用语义图标。
6. 配置页保留右侧“最近查询结果”和“立即刷新”，左侧按模板切换配置字段。
7. 自动查询间隔可配置；`0` 表示关闭，合法范围为 `0` 或 `1–1440` 分钟，默认 5 分钟。
8. 查询失败不得清除上一份成功数据；卡片必须同时显示“数据已过期/查询失败”状态和上次成功快照。
9. 所有第三方请求由后端发起，浏览器不得直接携带供应商凭据访问第三方。
10. 新功能不得改变模型请求转发、现有 request usage 统计或供应商激活逻辑。

#### 1.2 非目标

1. Claude/Codex/Gemini 官方 OAuth 订阅额度。
2. 历史额度曲线、账单明细、消费预测或额度告警。
3. 修改现有“使用统计”页面和 `internal/usage` 数据口径。
4. 自动切换供应商、熔断或按额度路由。
5. 从浏览器执行任意 JavaScript，或允许脚本直接访问文件、环境变量、进程和网络。
6. 加密现有供应商 Token 或引入独立密钥管理系统；新增秘密沿用现有本地配置安全模型。
7. 使用 Xiaomi MiMo 控制台会话 Cookie、浏览器自动化或页面抓取查询 Token Plan 额度。

### 2. 查询类型

#### 2.1 `general`

默认脚本：

```javascript
({
  request: {
    url: "{{baseUrl}}/user/balance",
    method: "GET",
    headers: {
      "Authorization": "Bearer {{apiKey}}",
      "Accept": "application/json"
    }
  },
  extractor: function (response) {
    return {
      remaining: response.balance,
      unit: "USD"
    };
  }
})
```

该模板是可编辑起点，不应被描述为行业标准协议。用量专用 Base URL/API Key 留空时回退供应商 `APIURL`/`APIToken`。

#### 2.2 `newapi`

请求规则：

```text
GET {baseUrl}/api/user/self
Authorization: Bearer {accessToken}
New-Api-User: {userId}
Content-Type: application/json
```

默认提取：

```text
planName = data.group 或“默认套餐”
remaining = data.quota / 500000
used = data.used_quota / 500000
total = (data.quota + data.used_quota) / 500000
unit = USD
```

`baseUrl`、`accessToken`、`userId` 为必填；不得把 Access Token 返回给前端。业务响应 `success=false` 应转为结构化查询失败。

#### 2.3 `custom`

脚本格式与 `general` 相同，必须返回对象字面量，包含 `request` 和 `extractor`。`extractor` 可返回一个对象或对象数组。

允许返回字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `planName` | string? | 套餐或窗口显示名 |
| `window` | string? | `five_hour`、`seven_day`、`monthly` 或自定义键 |
| `utilization` | number? | 已用百分比，语义固定为 0–100 |
| `resetsAt` | string/number? | RFC3339、秒级或毫秒级时间戳 |
| `used` | number? | 已用具体量 |
| `total` | number? | 总额度 |
| `remaining` | number? | 剩余额度 |
| `unit` | string? | USD、CNY、tokens、requests 等 |
| `isValid` | boolean? | 套餐/账户是否有效 |
| `invalidMessage` | string? | 失效原因 |
| `extra` | string? | 简短补充文本，最大 256 字符 |

如果只给出 `used` 和 `total`，后端计算 `utilization=used/total*100`；如果只给出 `remaining` 和 `total`，后端计算 `(total-remaining)/total*100`。除非脚本明确返回 `window`，普通余额不得被误识别为时间窗口。

通用/自定义模式使用独立的 `BaseURL` 与 `ScriptAPIKey`；`ScriptAPIKey` 留空时回退供应商 APIToken。请求 URL 必须与“有效脚本 Base URL”同源。脚本凭据不得被 ZenMux adapter 使用。

#### 2.4 `token_plan`

| 供应商 | 检测规则 | 请求与鉴权 | 归一化 |
| --- | --- | --- | --- |
| Kimi | `api.kimi.com/coding` | `GET https://api.kimi.com/coding/v1/usages`，Bearer Provider Token | `limits[].detail` -> 5h；`usage` -> 7d；保留 limit/remaining/resetTime |
| 智谱 CN | `bigmodel.cn` | `GET https://open.bigmodel.cn/api/monitor/usage/quota/limit`，`Authorization: {token}` | `TOKENS_LIMIT`，`unit=3` -> 5h，`unit=6` -> 7d；percentage 已是已用百分比 |
| 智谱 EN | `api.z.ai` | 同上但主机为 `https://api.z.ai` | 与 CN 相同 |
| MiniMax CN | `api.minimaxi.com` | `GET https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains`，Bearer | `model_name=general`；`100-current_interval_remaining_percent`；周窗口仅 `current_weekly_status=1` 时展示 |
| MiniMax EN | `api.minimax.io` | 同上但 `.io` 主机 | 与 CN 相同 |
| ZenMux | `zenmux` | 覆盖 URL/Key 均配置时使用覆盖对；两者均空时成对回退卡片 APIURL/APIToken；半配置拒绝 | `quota_5_hour`、`quota_7_day`；`usage_percentage*100`；保留 USD 已用/上限 |
| 火山方舟 | `volces.com/api/coding` | 控制面 `open.volcengineapi.com`，使用独立 AK/SK 签名 | 优先 `GetAFPUsage`，再 `GetCodingPlanUsage`；展示 5h/7d/monthly |

Xiaomi MiMo 的 `token-plan-*.xiaomimimo.com` 不在此表支持范围内；实现者必须遵守“Xiaomi MiMo 调查与延期决策”，不能因为域名包含 `token-plan` 就走通用 adapter。

火山方舟的 AccessKey ID/SecretAccessKey 与推理 Token 不同，必须作为该供应商用量配置中的独立秘密字段。签名逻辑应移植为独立纯函数，并使用固定时间 fixture 测试 canonical request、credential scope 和签名稳定性。

#### 2.5 `official_balance`

根据供应商 API URL 主机自动检测，不允许用户任意选择与主机不匹配的官方 adapter：

| 供应商 | 端点 | 主要映射 |
| --- | --- | --- |
| DeepSeek | `GET https://api.deepseek.com/user/balance` | `balance_infos[].total_balance`，按 currency 形成余额项；`is_available` -> isValid |
| StepFun | `GET https://api.stepfun.com/v1/accounts` | `balance`，单位 CNY |
| SiliconFlow CN | `GET https://api.siliconflow.cn/v1/user/info` | `data.totalBalance`，单位 CNY |
| SiliconFlow EN | `GET https://api.siliconflow.com/v1/user/info` | `data.totalBalance`，单位 USD |
| OpenRouter | `GET https://openrouter.ai/api/v1/credits` | total=`total_credits`，used=`total_usage`，remaining=差值，USD |
| Novita AI | `GET https://api.novita.ai/v3/user/balance` | `availableBalance/10000`，USD |

所有接口使用供应商 APIToken 作为 Bearer Token。401/403 归类为 `invalid_credentials`；其他非 2xx 为 `upstream_http_error`。

### 3. 数据模型

#### 3.1 Provider 配置

在 `internal/config.Provider` 增加：

```go
type ProviderQuotaConfig struct {
    Enabled                  bool   `json:"enabled"`
    TemplateType             string `json:"template_type"`
    TimeoutSeconds           int    `json:"timeout_seconds"`
    AutoQueryIntervalMinutes int    `json:"auto_query_interval_minutes"`
    Script                   string `json:"script,omitempty"`

    BaseURL              string `json:"base_url,omitempty"`
    ScriptAPIKey         string `json:"script_api_key,omitempty"`
    ZenMuxBaseURL        string `json:"zenmux_base_url,omitempty"`
    ZenMuxAPIKey         string `json:"zenmux_api_key,omitempty"`
    LegacyAPIKey         string `json:"api_key,omitempty"` // 仅旧 JSON 解码迁移
    AccessToken          string `json:"access_token,omitempty"`
    UserID               string `json:"user_id,omitempty"`
    CodingPlanProvider   string `json:"coding_plan_provider,omitempty"`
    AccessKeyID          string `json:"access_key_id,omitempty"`
    SecretAccessKey      string `json:"secret_access_key,omitempty"`
}

type Provider struct {
    // existing fields...
    QuotaQuery *ProviderQuotaConfig `json:"quota_query,omitempty"`
}
```

验证规则：

- `TemplateType` 必须是五个已知值之一。
- `TimeoutSeconds` 默认 10；合法范围 2–30。
- `AutoQueryIntervalMinutes` 默认 5；合法值为 0 或 1–1440。
- script 最大 64 KiB。
- Base URL 与 ZenMux Base URL 必须为绝对 HTTP/HTTPS URL，不得含 userinfo。
- NewAPI 必须具有 base URL、access token、user ID。
- Token Plan 必须能从 URL 自动检测，或显式 provider 与 URL 匹配。
- 火山必须具有 AK/SK。
- ZenMux 覆盖必须同时具有 `zenmux_base_url` 与 `zenmux_api_key`；两者都空时，卡片 APIURL/APIToken 必须完整存在。禁止混用覆盖 URL 与卡片 Token，或卡片 URL 与覆盖 Key。

旧配置迁移规则：General/Custom 的旧 `api_key` 迁移为 `script_api_key`；Token Plan 中旧 `base_url + api_key` 结构迁移为 ZenMux 覆盖对，即使卡片 URL 后来变化仍按持久化结构识别；其他用途的旧 `api_key` 丢弃。新字段优先，迁移后不再写出旧字段。

SQLite `providers` 增加 `quota_query_config TEXT NOT NULL DEFAULT '{}'`。JSON Store、SQLite Store、MockStore 必须保持相同语义。

#### 3.2 统一结果

```go
type QuotaTier struct {
    Name        string     `json:"name"`
    Label       string     `json:"label,omitempty"`
    Utilization float64    `json:"utilization"`
    ResetsAt   *time.Time `json:"resets_at,omitempty"`
    Used        *float64   `json:"used,omitempty"`
    Total       *float64   `json:"total,omitempty"`
    Remaining   *float64   `json:"remaining,omitempty"`
    Unit        string     `json:"unit,omitempty"`
}

type BalanceItem struct {
    PlanName       string   `json:"plan_name,omitempty"`
    Remaining      *float64 `json:"remaining,omitempty"`
    Used           *float64 `json:"used,omitempty"`
    Total          *float64 `json:"total,omitempty"`
    Unit           string   `json:"unit,omitempty"`
    IsValid        *bool    `json:"is_valid,omitempty"`
    InvalidMessage string   `json:"invalid_message,omitempty"`
    Extra          string   `json:"extra,omitempty"`
}

type ProviderQuotaResult struct {
    ProviderID       string        `json:"provider_id"`
    TemplateType     string        `json:"template_type"`
    Success          bool          `json:"success"`
    CredentialStatus string        `json:"credential_status"`
    Tiers            []QuotaTier   `json:"tiers"`
    Balances         []BalanceItem `json:"balances"`
    ErrorCode        string        `json:"error_code,omitempty"`
    ErrorMessage     string        `json:"error_message,omitempty"`
    QueriedAt        time.Time     `json:"queried_at"`
    DurationMS       int64         `json:"duration_ms"`
}
```

约束：

- `utilization` 永远表示“已用百分比”。
- adapter 输入中的 NaN/Inf 必须拒绝。
- 对 UI 输出将百分比钳制到 0–100；解析异常值时记录 `invalid_response`，不得生成负宽度。
- `five_hour`、`seven_day`、`monthly` 是规范窗口名；`weekly_limit` 归一为 `seven_day`。
- 重置时间统一转 UTC RFC3339；前端按浏览器本地时间显示 title，倒计时按绝对时间计算。
- 空 tiers + 空 balances 的成功响应视为 `empty_result` 失败，避免卡片静默显示成功。

#### 3.3 最新快照

新增表：

```sql
CREATE TABLE IF NOT EXISTS provider_quota_snapshots (
    provider_id TEXT PRIMARY KEY,
    result_json TEXT NOT NULL,
    last_success_json TEXT,
    queried_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
);
```

`result_json` 保存最近一次尝试；`last_success_json` 保存最近一次成功结果。失败时更新 `result_json`，保留 `last_success_json`。卡片可同时显示上次成功额度与最新失败标记。只保存最新状态，不保存历史序列。

任何会改变查询结果语义的配置更新（模板、脚本、URL、凭据、供应商 adapter）必须删除该 provider 的当前快照；仅修改自动查询间隔不删除快照。这样 `last_success_json` 永远属于当前查询配置，不需要额外的配置指纹字段。

### 4. 受限脚本执行与网络安全

#### 4.1 执行模型

推荐使用纯 Go `goja`，每个阶段创建独立 Runtime：

1. Runtime A 在 200 ms CPU/墙钟上限内解析配置对象，序列化 `request`。
2. Go 校验请求并替换占位符，然后使用受限 HTTP client 发请求。
3. Runtime B 重新加载同一脚本，在 500 ms 上限内调用 `extractor(responseJSON)`。
4. Go 将返回值解析、验证、截断并归一化。

Runtime 不注入 `fetch`、`require`、文件 API、环境变量、进程 API 或 Go 原生对象。超时调用 `Runtime.Interrupt`，每次执行后释放 Runtime。实现不得依赖系统 Node.js。

秘密不能替换进 JavaScript 源码，也不能作为 extractor 参数。脚本先产出含 `{{apiKey}}`、`{{baseUrl}}`、`{{accessToken}}`、`{{userId}}` 的普通字符串，Go 只在最终 HTTP request 中替换，防止脚本直接返回秘密。

#### 4.2 HTTP 限制

1. 仅允许 GET/POST；body 仅允许 JSON 可序列化值，最大 256 KiB。
2. 只允许 HTTP/HTTPS。默认要求 HTTPS；如果供应商自身 APIURL 明确为 HTTP，允许同源 HTTP。
3. 请求 URL 必须与有效用量 Base URL 同 scheme + host + effective port。
4. 重定向最多 3 次，每次重新执行同源校验；跨源重定向拒绝。
5. 禁止脚本设置 `Host`、`Content-Length`、`Transfer-Encoding`、`Connection`、`Proxy-Authorization`。
6. 响应体最大 2 MiB；超出返回 `response_too_large`。
7. 总 HTTP 超时使用配置的 2–30 秒，必须绑定 request context。
8. 日志和 API 错误移除 userinfo、query、fragment、Authorization、Cookie、Token 和响应原文中的疑似秘密。
9. 上游错误 body 只保留脱敏后的 512 字节摘要，不写入前端或日志完整正文。
10. 测试查询和正式查询使用同一执行路径和安全策略。

### 5. 查询管理器、缓存和自动调度

`Manager.Query(ctx, providerID, options)` 是唯一正式查询入口。

#### 5.1 并发规则

- 同一 provider 同时只允许一个上游查询；手动刷新与 scheduler 命中时共享同一个 in-flight 结果。
- 全局最多 4 个上游额度查询并发。
- 一个慢供应商不得阻塞其他供应商或管理 API。
- 调用 context 取消后应停止 HTTP 请求；不能泄漏 goroutine。

#### 5.2 自动调度

- Manager 使用一个 30 秒 ticker 扫描，不为每个 provider 创建永久 goroutine。
- 只有 provider enabled、quota query enabled、interval > 0 时参与自动查询。
- due 条件：不存在快照，或 `now - lastAttempt >= interval`。
- 启动时对 due provider 加确定性 0–30 秒抖动，避免同时请求所有供应商。
- 保存配置后通知 Manager 重新评估该 provider；从 disabled -> enabled 时可立即排入查询。
- interval=0 仍允许手动查询和测试查询。
- Manager 使用 server 根 context；`cmd/server/main.go` 关闭时显式 cancel 并等待退出。

#### 5.3 快照读取

- 供应商列表只读 SQLite 快照，绝不因为页面加载直接 N 次请求第三方。
- Dashboard 每 30 秒批量获取快照，或在已有状态刷新循环中合并调用。
- 手动刷新成功后前端立即替换该 provider 快照。
- 配置变化时旧快照标记 stale；模板类型或凭据改变后不得把旧结果当作当前结果。
- 实现方式固定为：实质配置变化时删除快照并显示“尚未查询”；仅 interval 变化保留快照。

### 6. 管理 API

所有端点都挂在现有 session auth middleware 后。

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET | `/api/providers/usage` | 批量返回所有 provider 的公开快照，不触发上游查询 |
| GET | `/api/providers/{id}/usage` | 返回公开配置 + 最近结果 + 最近成功结果 |
| PUT | `/api/providers/{id}/usage` | 校验并保存配置，秘密按保留/替换/清除语义处理 |
| POST | `/api/providers/{id}/usage/test` | 使用未保存草稿执行测试，不持久化配置或快照 |
| POST | `/api/providers/{id}/usage/query` | 手动正式查询并持久化快照 |

公开配置不得返回秘密原文：

```json
{
  "enabled": true,
  "template_type": "newapi",
  "base_url": "https://example.com",
  "script_api_key_configured": false,
  "zenmux_base_url": "https://quota.zenmux.example/usage",
  "zenmux_api_key_configured": true,
  "access_token_configured": true,
  "access_key_id": "AKLT…1234",
  "secret_access_key_configured": true
}
```

PUT/test 草稿中的秘密更新语义：

- 字段缺失或空字符串：保留对应用途已存秘密；若无脚本密钥则回退供应商 APIToken。
- `clear_script_api_key=true` 与 `clear_zenmux_api_key=true` 分别清除对应秘密，互不影响。
- ZenMux URL/Key 覆盖是原子对；只有两者都空时才回退卡片凭据。
- 新的非空值：替换。
- API 响应始终只返回 `*_configured`。
- 旧客户端的 `api_key`/`clear_api_key` 仅在请求边界按当前有效模板/provider 路由，不进入新持久化模型。

HTTP 语义：

- 400：配置、脚本或 URL 校验失败。
- 404：provider 不存在。
- 405：方法错误。
- 200 + `success=false`：上游已执行但认证、HTTP、解析或空结果失败。
- 500：本地存储/内部错误，错误文案必须脱敏。

Provider subtree router 必须在通用 `/{id}` 分发前识别 `/usage`、`/usage/test`、`/usage/query`，测试防止 ID 解析错误。

### 7. 前端与交互

#### 7.1 独立页面

Vue Router 增加：

```ts
{
  path: '/providers/:providerId/usage',
  name: 'provider-usage',
  component: ProviderUsageView,
}
```

页面结构必须保持已确认设计：

- 顶部：面包屑、供应商名、返回供应商按钮。
- 主体两栏：左侧查询配置，右侧最近查询结果。
- 右侧“立即刷新”始终可见；查询中显示旋转图标并禁用重复点击。
- 返回跳转 `/?tab=providers`；Dashboard 必须从 query 初始化 tab，刷新后仍停留供应商页。
- provider 不存在显示明确空状态和返回按钮，不静默重定向。

左侧模板状态：

- `general`：脚本 API Key/Base URL 可选覆盖、timeout、interval、脚本编辑器。
- `custom`：有效变量、脚本 API Key/Base URL 可选覆盖、timeout、interval、脚本编辑器和返回字段帮助。
- `newapi`：Base URL、Access Token、User ID、timeout、interval；脚本可展示但首版可只读，避免用户误以为 native。
- `token_plan`：供应商选择/自动检测；ZenMux 显示独立的用量 URL/Key，并提示两者都空时成对回退；火山显示 AK/SK；其他复用供应商字段。
- `official_balance`：显示自动检测出的供应商和固定端点，不展示脚本编辑器。

测试查询：

- 使用当前未保存草稿。
- 成功时更新页面右侧“测试结果”标记，但不能更新卡片正式快照。
- 保存后仍需用户点击“立即刷新”或等待 scheduler 才生成正式快照；若产品希望保存即查询，应在实现前明确修改本条，首版不隐式发请求。

#### 7.2 供应商卡片

桌面标题行顺序：

```text
[选择框] [启用点] 供应商名称 ... [最近查询] [刷新] [5小时] [7天] [使用中]
```

规则：

- 用量信息与供应商名称、“使用中”同一行，靠右且紧邻“使用中”。
- 小屏允许用量组整体换到标题下方，不能横向溢出或截断操作按钮。
- 仅配置并启用后显示额度区域。
- 无快照时显示“尚未查询”+ 刷新按钮。
- 时间窗口显示 `5小时: 2% ◷2h30m`、`7天: 7% ◷1d6h`。
- 只有余额时显示最主要余额，例如 `余额: $12.34`；多余额详情放 title 或配置页，不塞满卡片。
- 上次尝试失败但存在 last success 时继续显示旧值，并增加警告图标/title。
- 刷新按钮必须 stop propagation，不触发卡片其他操作。
- 查询中只旋转当前卡片刷新按钮。

操作按钮顺序和图标：

| 操作 | 图标建议 |
| --- | --- |
| 编辑 | `Pencil` |
| 复制 | `Copy` |
| 用量 | `Gauge` 或 `ChartNoAxesColumnIncreasing` |
| 测试 | `CircleCheck` 或 `PlugZap` |

这四个按钮必须为“图标 + 文本”；其余按钮保持现有视觉语义即可。“用量”必须位于“复制”和“测试”之间。

#### 7.3 格式化

- 百分比四舍五入为整数；title 保留最多两位小数。
- 倒计时每 30 秒重算：`XdYh`、`XhYm`、`Xm`。
- `resetsAt <= now` 显示“待刷新”，不显示负数。
- 查询时间使用“刚刚 / N 分钟前 / N 小时前 / N 天前”。
- 金额最多两位小数；未知单位使用原 unit 文本。
- ARIA label 和 title 必须覆盖图标按钮、查询错误、重置绝对时间。

### 8. 生命周期行为

1. 新建 provider：quota query 默认为 disabled，不自动外联。
2. 编辑 provider：不覆盖 quota query config。
3. 复制 provider：复制 quota config 和秘密，但不复制 snapshot；新卡片显示“尚未查询”。
4. 删除 provider：通过外键级联删除 snapshot。
5. 禁用 provider：scheduler 停止自动查询；保留配置和 snapshot；手动查询按钮禁用并说明原因。
6. 导出 provider：quota config 随 provider 一起导出，包括用量专用秘密；沿用现有导出秘密警告。
7. 导入 provider：验证 quota config；旧导出文件缺失该字段时兼容为 disabled。
8. overwrite import：覆盖 quota config 并使旧 snapshot stale/删除。
9. duplicate import：复制配置到新 ID，不复制 snapshot。
10. 更新 API URL 或 API Token：删除依赖卡片回退的旧快照；后续查询自动采用新值，脚本/ZenMux 独立覆盖字段保持不变。

### 9. 错误分类

稳定错误码：

```text
not_configured
invalid_config
missing_credentials
invalid_credentials
unsupported_provider
request_timeout
network_error
upstream_http_error
upstream_business_error
response_too_large
invalid_json
script_timeout
script_error
invalid_response
empty_result
internal_error
```

前端按错误码翻译，不能依赖匹配英文错误字符串。开发日志可记录 provider ID、adapter、HTTP 状态和脱敏 URL；不得记录任何 Token、AK/SK、Authorization header 或完整响应 body。

### 10. 文件影响清单

建议文件结构：

```text
internal/providerquota/
  types.go
  normalize.go
  script.go
  balance.go
  token_plan.go
  volcengine_sign.go
  store.go
  manager.go
  *_test.go
internal/config/
  provider.go                 # quota config + validation
  sqlite_store.go             # JSON column round-trip
internal/admin/
  provider_quota_handler.go
  provider_quota_handler_test.go
  provider_handler.go         # routes/lifecycle/import/export/duplicate
  server.go                   # manager injection and routes
cmd/server/main.go            # construct/start/stop providerquota manager
internal/frontend/src/
  main.ts                     # provider usage route
  composables/useApi.ts       # types and API methods
  composables/useI18n.ts      # zh/en strings
  components/ProviderCard.vue
  components/ProviderCard.test.ts
  components/ProviderQuotaResult.vue
  views/ProviderUsageView.vue
  views/ProviderUsageView.test.ts
  views/DashboardView.vue
```

允许实现者根据现有代码边界合并小型文件，但不得把所有后端逻辑塞入 handler，或把整个前端页面继续塞入已经很大的 `DashboardView.vue`。

### 11. 验收标准

#### 后端

- [ ] 五类模板均能通过 API 保存、读取和测试。
- [ ] 供应商列表和配置 API 不泄漏任何新增秘密。
- [ ] script timeout、HTTP timeout、响应大小和同源限制都有自动化测试。
- [ ] Kimi、智谱、MiniMax、ZenMux、火山 fixture 正确生成 5h/7d/monthly。
- [ ] Xiaomi MiMo Base URL 返回明确的“暂不支持”提示，且不会请求控制台私有接口或要求 Cookie。
- [ ] DeepSeek、StepFun、SiliconFlow、OpenRouter、Novita fixture 正确生成余额。
- [ ] 同一 provider 并发手动/自动查询只产生一次上游请求。
- [ ] 失败保留 last success；删除 provider 级联删除 snapshot。
- [ ] JSON Store 和 SQLite Store 都能 round-trip 新配置。

#### 前端

- [ ] `/providers/:id/usage` 可刷新直接打开，并通过现有登录守卫。
- [ ] 页面保持已确认两栏结构，右侧有“立即刷新”。
- [ ] 卡片用量与名称/使用中同一标题行，桌面布局不漂移。
- [ ] 卡片刷新按钮存在且只刷新当前 provider。
- [ ] 按钮顺序固定，用量位于复制与测试之间；四个文本按钮有图标。
- [ ] 百分比显示已用语义和绿/橙/红阈值。
- [ ] 过期 reset 不出现负倒计时。
- [ ] 中文/英文切换无裸 key。
- [ ] 360px、768px、1440px 宽度不横向溢出。

#### 全量验证

```bash
go test ./...
go test -race ./internal/providerquota ./internal/admin ./internal/config
go vet ./...
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
git diff --check
```

手工验证至少覆盖：Kimi 或 MiniMax Token Plan、一个官方余额、NewAPI mock、自定义 mock、错误凭据、超时、保存后重启快照仍存在。

## 任务详情

### 任务 1：建立类型、配置和 SQLite schema

#### 需求

**Objective（目标）** — 建立独立、可持久化且不与 request usage 混淆的供应商额度数据模型。
**Outcomes（成果）** — Provider quota config、统一结果、SQLite JSON column、snapshot 表。
**Evidence（证据）** — JSON/SQLite/MockStore round-trip 测试和旧 schema 迁移测试。
**Constraints（约束）** — 旧配置无字段时默认 disabled；秘密不进入公开 provider response。
**Edge Cases（边界）** — `{}`、损坏 JSON、未知 template、旧 DB 缺列、删除级联。
**Verification（验证）** — `go test ./internal/config ./internal/providerquota -count=1`。

#### 计划

1. 先写配置默认值、验证和存储 round-trip 失败测试。
2. 增加类型和规范化函数。
3. 在 SQLite schema 创建 JSON column 和 snapshot 表。
4. 更新 load/upsert/migration/legacy JSON 路径。
5. 确认 providerResponseMap 仅返回公开摘要。

#### 验证

- [ ] 旧 DB 自动补列且数据不丢失。
- [ ] Provider save/load 保留所有非秘密和秘密字段。
- [ ] API response 无 `script_api_key`、`zenmux_api_key`、`access_token`、`secret_access_key` 原文，仅有 configured 标志。

### 任务 2：实现受限脚本执行器

#### 需求

**Objective（目标）** — 支持 general/NewAPI/custom，同时把网络和秘密控制留在 Go 层。
**Outcomes（成果）** — 配置解析、占位符替换、受限 HTTP、extractor、结果归一化。
**Evidence（证据）** — httptest + 恶意脚本测试。
**Constraints（约束）** — 不依赖 Node，不暴露 fetch/file/env，不跨源，不记录秘密。
**Edge Cases（边界）** — 无限循环、大响应、跨源 redirect、非法 header、非 JSON、NaN。
**Verification（验证）** — 定向运行 script/normalize 测试并通过 race。

#### 计划

1. 添加 goja 依赖并用测试锁定 Interrupt 行为。
2. 实现两阶段 runtime 和严格 request schema。
3. 实现 HTTP client 限制与脱敏。
4. 实现对象/数组返回归一化。
5. 固化 general/NewAPI 默认模板。

#### 验证

- [ ] 无限循环在规定时间内退出。
- [ ] 脚本无法读取 API Key。
- [ ] 跨源 URL/redirect 被拒绝。
- [ ] 2 MiB 限制和 context timeout 生效。

### 任务 3：实现官方余额适配器

#### 需求

**Objective（目标）** — 用 Go 原生、可测试实现 6 个官方余额入口。
**Outcomes（成果）** — 主机检测、请求、解析、认证错误分类。
**Evidence（证据）** — 每个 provider 成功和错误 fixture。
**Constraints（约束）** — 固定官方 endpoint；不得根据用户输入拼接任意 host。
**Edge Cases（边界）** — 数字/字符串金额、空数组、余额为 0、多币种。
**Verification（验证）** — `go test ./internal/providerquota -run 'Balance' -count=1`。

#### 计划

1. 实现 adapter interface 和 host detection。
2. 为每个 provider 写 parser fixture。
3. 通过可注入 transport/base endpoint 测试请求 header 和 path。
4. 添加错误分类和脱敏。

#### 验证

- [ ] 多币种 DeepSeek 产生多个 BalanceItem。
- [ ] OpenRouter 正确计算 remaining。
- [ ] Novita 单位转换正确。

### 任务 4：实现 Token Plan 适配器

#### 需求

**Objective（目标）** — 原生支持 Kimi、智谱、MiniMax、ZenMux、火山套餐窗口。
**Outcomes（成果）** — 5h/7d/monthly 统一 tiers 和具体数量保留。
**Evidence（证据）** — 从 cc-switch 行为提炼的 JSON fixture 和火山签名向量。
**Constraints（约束）** — 百分比语义固定为已用；没有周窗口时不补假数据。
**Edge Cases（边界）** — 旧智谱单 tier、MiniMax 无周套餐、时间戳格式、火山无订阅。
**Verification（验证）** — 定向 adapter/signing 测试。

#### 计划

1. 实现 provider detection 和 reset time parser。
2. 逐一实现四类普通 Bearer/raw-key adapter。
3. 实现火山 region、canonical query、HMAC 签名和双 API fallback。
4. 统一错误和 credential status。

#### 验证

- [ ] MiniMax 98% remaining 显示 2% used。
- [ ] 智谱 unit 3/6 不依赖 reset 时间排序。
- [ ] Kimi 保留 limit/remaining。
- [ ] 火山签名 deterministic。

### 任务 5：实现 Store、Manager 和 scheduler

#### 需求

**Objective（目标）** — 提供可靠缓存、last-success 保留、手动/自动共用的查询入口。
**Outcomes（成果）** — Manager、snapshot store、ticker、并发限制和 singleflight。
**Evidence（证据）** — fake adapter/counter/clock 测试。
**Constraints（约束）** — 页面加载不触发 N 次上游；关闭服务无 goroutine 泄漏。
**Edge Cases（边界）** — 配置更新中查询、provider 删除、并发刷新、失败后重试。
**Verification（验证）** — manager/store tests + race。

#### 计划

1. 以接口注入 clock、store、adapter resolver。
2. 实现 query path 和持久化事务。
3. 实现 per-provider in-flight 去重与全局 semaphore。
4. 实现 30 秒扫描和 due/jitter。
5. 接入 server lifecycle。

#### 验证

- [ ] 10 个并发刷新只请求一次同一 provider。
- [ ] 不同 provider 最大并发不超过 4。
- [ ] 失败更新 last attempt 但保留 last success。

### 任务 6：实现认证管理 API

#### 需求

**Objective（目标）** — 暴露配置、测试、刷新和批量快照，保持秘密不可见。
**Outcomes（成果）** — 五个 API、handler 测试、server wiring。
**Evidence（证据）** — httptest 覆盖 auth 后的成功/失败和 method guard。
**Constraints（约束）** — 不改变已有 provider endpoint 语义。
**Edge Cases（边界）** — 路由后缀、缺 provider、malformed JSON、并发查询。
**Verification（验证）** — `go test ./internal/admin -run 'ProviderQuota' -count=1`。

#### 计划

1. 定义公开 DTO 和 secret patch DTO。
2. 添加 handler 与 subtree route dispatch。
3. 注入 Manager，不在 handler new HTTP client。
4. 测试脱敏和状态码。

#### 验证

- [ ] 所有 endpoint 未登录返回 401。
- [ ] 错误方法返回 405。
- [ ] test 不写 snapshot，query 写 snapshot。

### 任务 7：实现前端路由和配置页

#### 需求

**Objective（目标）** — 按已确认两栏设计提供完整五模板配置体验。
**Outcomes（成果）** — route、API types、ProviderUsageView、结果组件、i18n。
**Evidence（证据）** — component tests 和 build。
**Constraints（约束）** — 不把页面合并进 DashboardView；不显示已保存秘密。
**Edge Cases（边界）** — 直接刷新路由、404 provider、保存失败、测试与正式结果区分。
**Verification（验证）** — frontend tests/build。

#### 计划

1. 先创建路由/API mock 测试。
2. 实现模板字段和秘密 configured 状态。
3. 实现脚本 editor（可先使用 textarea/code style，除非仓库已有 editor）。
4. 实现测试/保存/立即刷新状态机。
5. 实现右侧结果复用组件。

#### 验证

- [ ] 所有模板切换显示正确字段。
- [ ] 空 secret 不误清除已存 secret。
- [ ] 返回供应商后 tab 保持。

### 任务 8：实现供应商卡片额度和操作图标

#### 需求

**Objective（目标）** — 在卡片紧凑展示额度，并按确认顺序改造操作行。
**Outcomes（成果）** — 标题行 quota、刷新、按钮图标和 Dashboard snapshot 接线。
**Evidence（证据）** — ProviderCard tests + 响应式手测。
**Constraints（约束）** — 不破坏选择框、active、enable/disable 和批量导出。
**Edge Cases（边界）** — 长名称、多窗口、只有余额、失败快照、移动宽度。
**Verification（验证）** — test/build + 三个 viewport。

#### 计划

1. 扩展 ProviderCard props/emits。
2. 抽取格式化函数并单测。
3. 实现标题行和 refresh loading。
4. 重排操作按钮并添加 lucide 图标。
5. Dashboard 批量加载快照和处理单卡刷新。

#### 验证

- [ ] 用量严格位于复制和测试之间。
- [ ] 2/80/95% 分别使用绿/橙/红。
- [ ] 刷新只影响当前卡片。

### 任务 9：补齐供应商生命周期、导入导出和双语

#### 需求

**Objective（目标）** — 让新配置在复制、删除、导入、导出和语言切换中行为一致。
**Outcomes（成果）** — 生命周期逻辑、兼容旧导出、完整 zh/en。
**Evidence（证据）** — handler/frontend i18n tests。
**Constraints（约束）** — 沿用现有导出秘密警告；snapshot 不导出。
**Edge Cases（边界）** — overwrite/duplicate/skip、旧格式、未知模板。
**Verification（验证）** — 现有 provider import/export suite 加新断言。

#### 计划

1. 扩展 export/import schema 和 validation。
2. 明确 duplicate/overwrite snapshot 行为。
3. 添加所有 i18n keys 和无裸 key 测试。
4. 回归供应商现有操作。

#### 验证

- [ ] 旧文件可导入。
- [ ] 新配置可导出再导入。
- [ ] snapshot 不出现在导出 JSON。

### 任务 10：全量验证与交付

#### 需求

**Objective（目标）** — 用自动化和手工证据证明功能可靠、安全且不回归代理。
**Outcomes（成果）** — 全量通过、验证记录、spec 状态更新。
**Evidence（证据）** — 命令输出、fixture 列表、UI 截图/手测表。
**Constraints（约束）** — 不以“能编译”替代行为验证。
**Edge Cases（边界）** — Windows/Docker 路径、服务重启、无网络。
**Verification（验证）** — 执行验收章节全部命令。

#### 计划

1. 跑定向测试并修复。
2. 跑全量 Go/frontend/race/vet/build。
3. 使用本地 mock server 手测五模板。
4. 重启验证 snapshot 和 scheduler。
5. 更新 checklist、进度与实际证据，不伪造未执行结果。

#### 验证

- [ ] 所有要求命令实际通过。
- [ ] 现有 proxy/usage/provider 测试无回归。
- [ ] 文档中的最终状态与证据一致。
