# Custom 脚本 Form Body 与附加密钥规格（千问 Token Plan 用量查询）

本地页面：管理后台供应商卡片「用量」弹窗（`ProviderUsageModal.vue`）/ 代理入口：不修改模型代理链路；新增字段复用已有 `/api/providers/{id}/usage*` 管理 API / 参考源站：千问 AI 平台控制台 `platform.qianwenai.com` 私有网关 `cs-data.qianwenai.com`（无官方公开 API 文档，本规格基于 2026-07-27 实抓请求）/ 技术栈：Go 1.26 标准库 + `github.com/dop251/goja` + Vue 3 + TypeScript + Tailwind / 最后更新：2026-07-27 / 状态：validating / 进度：6 / 6 implemented

## 整体分析（源站分析）

### 1. 目标与背景

用户在千问 AI 平台（阿里百炼 / qianwenai）购买了 Token Plan 个人版套餐，希望像 Kimi、智谱、MiniMax 一样，在 mcc 供应商卡片的「用量」区域看到 5 小时 / 7 天已用百分比与重置倒计时。

千问 Token Plan 用量查询**没有官方公开 API**。控制台页面 `platform.qianwenai.com/home/billing/subscription/token-plan-individual` 通过私有网关 `cs-data.qianwenai.com` 拉取用量数据。本规格基于 2026-07-27 对该网关的实抓请求与响应，确认其鉴权模型后，决定**不为千问新增 native adapter**，而是**增强现有 `custom` 脚本机制**，使其能表达千问这类「POST form-urlencoded body + 双凭据（Cookie + 会话 token）」的私有接口，并以千问作为首个配置示例。

本功能与现有「使用统计」（`internal/usage`）和「供应商额度查询」（`internal/providerquota`）的关系：本功能只是 `providerquota` 包内部的能力增强，不新增包、不改统一结果模型、不改 `internal/usage`。

### 2. 千问接口源站分析（2026-07-27 实抓结论）

#### 2.1 请求形态

```text
POST https://cs-data.qianwenai.com/data/api.json?product=sfm_bailian&action=BroadScopeAspnGateway&api=zeldaHttp.apikeyMgr.%2Ftokenplan%2Fpersonal%2Fapi%2Fv2%2Fusage
Content-Type: application/x-www-form-urlencoded
Cookie: login_qianwenai_ticket=...; login_aliyunid_pk=1758748083928576; cna=...; isg=...; tfstk=...; xlly_s=1; account_info_switch=close
Origin: https://platform.qianwenai.com
Referer: https://platform.qianwenai.com/home/billing/subscription/token-plan-individual?...

# body（form-urlencoded，下面是解码后的字段）：
product=sfm_bailian
action=BroadScopeAspnGateway
sec_token=WmFrs6jduM1ff0WGARDqN
region=cn-beijing
params={"Api":"zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage","Data":{"cornerstoneParam":{"domain":"platform.qianwenai.com","consoleSite":"QIANWENAI","console":"ONE_CONSOLE","xsp_lang":"zh-CN","protocol":"V2","productCode":"p_efm"}},"V":"1.0"}
```

#### 2.2 鉴权模型（关键）

| 凭据 | 位置 | 来源 | 生命周期 |
| --- | --- | --- | --- |
| `Cookie` 头（核心是 `login_qianwenai_ticket`） | HTTP 请求头 | 浏览器登录态 | 与阿里云控制台登录会话绑定，数小时到数天 |
| `sec_token` | form body 字段 | 控制台全局变量 `window.ALIYUN_CONSOLE_CONFIG.SEC_TOKEN` | 与同一登录会话绑定，与 Cookie 同时失效 |
| `login_aliyunid_pk`（账号 ID） | Cookie 内 | 登录后写入 | 随 Cookie |

**结论：千问用量接口的鉴权 = Cookie + sec_token 两个独立秘密值，绑定同一登录会话、同时过期。** 这与 mcc spec `2026-06-27-provider-quota-query` §6「Xiaomi MiMo 调查与延期决策」属于同类情形（依赖浏览器会话 Cookie，非稳定 API Key）。因此本规格遵循同一先例：**不为千问新增 native adapter**，而是让用户通过 `custom` 模板自行配置，并接受「每数天手动更新 Cookie+sec_token」的代价。用户已明确接受此代价（2026-07-27 对话确认）。

#### 2.3 响应形态与字段映射

```json
{
  "code": "200",
  "data": {
    "DataV2": {
      "ret": ["SUCCESS::接口调用成功"],
      "data": {
        "msg": "Success.",
        "code": "SUCCESS",
        "data": {
          "per5HourPercentage": 0.0,
          "per1WeekResetTime": 1785462900000,
          "per1WeekPercentage": 1.0
        },
        "requestId": "...",
        "success": true
      }
    },
    "success": true,
    "httpStatus": 200,
    "errorCode": "",
    "api": "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage",
    "errorMsg": ""
  },
  "httpStatusCode": "200",
  "requestId": "...",
  "successResponse": true
}
```

页面「每 5 小时额度 剩余量 100.0%」对应 `per5HourPercentage: 0.0`，「每 7 天额度 剩余量 0.0%」对应 `per1WeekPercentage: 1.0`（2026-07-27 端到端实测确认）。**`perXxxPercentage` 字段是 0–1 的「已用比例」**（0.0 = 已用 0% = 剩余 100%；1.0 = 已用 100% = 剩余 0%），**不是 0–100 的已用百分比**——单看 `per5HourPercentage:0.0` 无法区分两种语义，是 `per1WeekPercentage:1.0` 配合页面「剩余 0%」一锤定音。mcc 的 `utilization` 固定是 0–100 已用百分比，因此 extractor 需 `percentage * 100`。映射到 mcc 统一结果：

| 千问字段 | 含义 | mcc 归一化字段 |
| --- | --- | --- |
| `data.DataV2.data.data.per5HourPercentage` | 5 小时已用比例（0–1） | `tiers[].window="five_hour"`, `utilization = 值 × 100` |
| `data.DataV2.data.data.per1WeekPercentage` | 7 天已用比例（0–1） | `tiers[].window="seven_day"`, `utilization = 值 × 100` |
| `data.DataV2.data.data.per1WeekResetTime` | 毫秒时间戳 | `tiers[].resets_at` |

`parseResetTime`（`internal/providerquota/normalize.go:202-229`）已支持毫秒时间戳（`t > 1e12` 走 `time.UnixMilli`），无需改动。

### 3. 当前 custom 脚本机制的三处限制

实抓请求与现有 `custom` 脚本执行器对照后，确认三处必须改动的限制（均位于 `internal/providerquota/script.go` 与配套文件）：

#### 限制 A：body 只能 JSON 序列化，不能 form-urlencoded

`script.go:241-252` 的 `doHTTPRequest`：

```go
if req.Body != nil {
    bodyBytes, err := json.Marshal(req.Body)   // 永远产出 JSON
    ...
    bodyReader = bytes.NewReader(bodyBytes)
}
```

千问接口要求 `application/x-www-form-urlencoded`。JSON body 被网关按 form 解析必然失败（2026-07-27 已验证 GET 被网关拒、form 原始格式 200、JSON 因浏览器 CORS preflight 无法直测但从协议看必拒）。

#### 限制 B：占位符替换不覆盖 body

`script.go:69-73`：

```go
reqConfig.URL = substitutePlaceholders(reqConfig.URL, placeholderValues)
for k, v := range reqConfig.Headers {
    reqConfig.Headers[k] = substitutePlaceholders(v, placeholderValues)
}
// body 完全没有替换
```

这是 spec `2026-06-27` §4.1「秘密只由 Go 在最终 HTTP request 中替换，不进入脚本运行时」的安全设计——但只对 URL 和 headers 生效。千问的 `sec_token` 必须出现在 body 里，因此必须把占位符替换扩展到 body（仍在 Go 层，安全模型不变）。

#### 限制 C：custom/general 模板只有一个秘密槽

`manager.go:237-247`（custom/general 分支）：

```go
placeholders := map[string]string{
    "baseUrl": plan.scriptURL,
    "apiKey":  plan.token,   // 只有 ScriptAPIKey 或回退 card APIToken
}
```

`resolve.go:184-193`（custom/general 分支）只解析 `ScriptAPIKey` 一个秘密字段。千问需要 Cookie 与 sec_token 两个独立秘密，必须新增第二个对称的秘密槽 `ScriptAPIKey2` + 占位符 `{{apiKey2}}`。

### 4. 核心设计

#### 4.1 三处增强（最小改动，通用，不限于千问）

1. **`ScriptRequest.BodyType`**（`script.go`）：脚本可通过 `request.bodyType: "form"` 声明 form body；默认 `""`/`"json"` 走现有 JSON 序列化（向后完全兼容）。`bodyType:"form"` 时，`body` 必须是对象，Go 层用 `url.Values` 编码：字符串/数字/布尔值直接转字符串，对象/数组值先 `json.Marshal` 再作为字段值（这样脚本可直接写 `params: {...}`，等价于 `params: JSON.stringify({...})`）。
2. **body 占位符替换**（`script.go`）：新增 `substitutePlaceholdersInBody(body any, values)`，在编码前递归遍历 body，对所有字符串字段值做与 URL/headers 相同的占位符替换。对 JSON body 与 form body 都生效。安全模型不变（替换仍在 Go 层，脚本运行时拿不到真值）。
3. **`ScriptAPIKey2` + `{{apiKey2}}`**（`types.go`/`resolve.go`/`manager.go`/admin handler/前端）：对称于现有 `ScriptAPIKey`，custom/general 模板下可用，作为第二个独立秘密槽。

#### 4.2 千问配置形态（本功能的首个应用案例，写入任务 6 作为端到端验证目标）

| 配置项 | 值 |
| --- | --- |
| 模板类型 | `custom` |
| Base URL | `https://cs-data.qianwenai.com` |
| Script API Key（`apiKey`） | 完整 Cookie 字符串 |
| 附加密钥（`apiKey2`） | `sec_token` 值（如 `WmFrs6jduM1ff0WGARDqN`） |
| 脚本 | 见任务 6「千问 custom 脚本」 |
| Timeout | 10s（默认） |
| Auto Query Interval | 用户自选（默认 5min） |

同源校验：请求 URL `https://cs-data.qianwenai.com/data/api.json?...` 与 Base URL `https://cs-data.qianwenai.com` 同 scheme+host+port，满足 `validateScriptRequest`（`script.go:330-358`）。`Origin`/`Referer` 头由脚本显式设置（不在禁止头列表 `script.go:369-375`），网关不强制校验同源（实抓请求 `Origin`/`Referer` 为 `platform.qianwenai.com`，但本功能从 mcc 后端发请求，建议设置 `Referer` 以最大化兼容性，见任务 6 脚本）。

#### 4.3 为什么不为千问写 native adapter

遵循 `2026-06-27-provider-quota-query` §6 的 MiMo 先例：

1. 鉴权依赖浏览器登录会话 Cookie + 会话级 `sec_token`，不是稳定的 API Key。
2. `cs-data.qianwenai.com` 是控制台私有网关，无官方公开协议承诺，前端发布即可能变化。
3. native adapter（`token_plan.go`）面向「稳定 API Key + 官方 endpoint」的供应商（Kimi/智谱/MiniMax/ZenMux/火山），千问不符合该前提。

因此千问只作为 `custom` 模板的配置示例，规格里给出推荐脚本与获取凭据的步骤，但不进入 `token_plan.go` 的检测表（`token_plan.go` 的 `DetectTokenPlanProvider` 不识别 `qianwenai.com`/`bailian` 主机）。

### 5. 风险总结

1. **凭据过期**：Cookie 与 sec_token 同时过期后，查询返回 `upstream_business_error`（千问 `code!="200"`）或 `invalid_credentials`（HTTP 401/403）。前端按已有错误码翻译显示「查询失败」，`last_success_json` 保留上次成功快照（已有机制，无需改动）。用户需重新获取 Cookie + sec_token 并更新两个秘密槽。规格不在 mcc 内做自动续期（需要阿里云账号密码/OAuth，超范围）。
2. **body 占位符替换的向后兼容**：现有 `general` 默认脚本与所有存量 custom 脚本的 body 不含 `{{...}}` 占位符（因为之前 body 不替换），引入 body 替换对它们无影响（字符串不含占位符时 `strings.ReplaceAll` 是 no-op）。任务 3 的回归测试必须覆盖「JSON body 不含占位符时字节级不变」。
3. **form body 编码确定性**：`url.Values.Encode()` 按键名字典序输出，与千问网关是否容忍字段顺序无关（form-urlencoded 规范不关心顺序）。任务 3 测试固定断言编码后的字符串。
4. **第二秘密槽的语义泛化**：`ScriptAPIKey2` 没有领域命名（不叫 `sec_token`/`cookie`），因为它服务于任意 custom 脚本的第二个秘密需求。前端标签用通用文案「附加密钥（apiKey2）」，tooltip 说明用途。
5. **`NormalizeForTemplate` 字段清理**：`resolve.go:36-72` 按模板清空无关字段。`ScriptAPIKey2` 与 `ScriptAPIKey`/`ZenMuxAPIKey` 同属「独立安全域」（`resolve.go:33-35` 注释），**所有模板保留、不清空**；安全性由 `resolveQueryPlan` 保证——只有 custom/general 分支读取它，其他分支不读，残留不会被误用（与 `ScriptAPIKey` 在 newapi/token_plan 残留但不被读取一致）。
6. **前端弹窗字段密度**：custom 模板已有 Base URL + Script API Key + 脚本编辑器，再加「附加密钥」会增加纵向高度。任务 5 在 script_api_key 正下方紧邻放置，复用同一栅格，不破坏移动端布局。
7. **公开 API 不泄露秘密**：`PublicQuotaConfig`（`types.go:366-382`）必须只暴露 `script_api_key_2_configured: bool`，绝不返回原文。任务 1 + 任务 4 共同保证。

## 开发检查清单

| 序号 | 状态 | 任务 | 主要产出 | 核心验证 |
| --- | --- | --- | --- | --- |
| 1 | Planned | config 层：`ScriptAPIKey2` 字段 + 公开 DTO + 校验 | `internal/providerquota/types.go` | JSON round-trip、`ToPublicConfig` 不泄露原文 |
| 2 | Planned | resolve + manager：`token2` 解析 + `{{apiKey2}}` 占位符 + 字段清理 | `internal/providerquota/resolve.go`、`internal/providerquota/manager.go` | `resolveQueryPlan` custom/general 带 token2；placeholders 含 apiKey2 |
| 3 | Planned | script.go：`bodyType:"form"` 编码 + body 占位符替换 | `internal/providerquota/script.go` | form 编码断言、body 替换、JSON 向后兼容 |
| 4 | Planned | admin handler：`script_api_key_2` 秘密更新语义 | `internal/admin/provider_quota_handler.go` | 保留/替换/清除三态、公开响应 configured 标志 |
| 5 | Planned | 前端：custom/general「附加密钥」输入框 + i18n + API 类型 | `ProviderUsageModal.vue`、`quotaForm.ts`、`useApi.ts`、`useI18n.ts` | 双语、保存/测试 payload、npm test + build |
| 6 | Planned | 千问配置示例 + 端到端验证 + 文档 | 本规格任务 6 脚本、验证证据 | 真实 Cookie+sec_token 跑通 5h/7d tier |

## 需求

### 1. 功能范围

#### 1.1 必须交付

1. `custom`/`general` 模板支持 `request.bodyType: "form"`，Go 层用 `url.Values` 编码对象型 body；`bodyType` 缺省或为 `"json"` 时维持现有 JSON 序列化行为。
2. 占位符替换（`{{baseUrl}}` `{{apiKey}}` `{{apiKey2}}` `{{accessToken}}` `{{userId}}`）扩展到 `request.body` 的所有字符串值，对 JSON 与 form body 都生效；替换发生在编码前、在 Go 层。
3. `ProviderQuotaConfig` 新增 `ScriptAPIKey2 string`，对称于 `ScriptAPIKey`：custom/general 模板下作为第二个独立秘密，通过 `{{apiKey2}}` 引用；与 `ScriptAPIKey` 同属独立安全域，`NormalizeForTemplate` 不清空（其他模板分支不读取它）。
4. `PublicQuotaConfig` 新增 `ScriptAPIKey2Configured bool`，`ToPublicConfig` 只输出该布尔值；admin 公开响应（GET 配置、批量快照）不返回 `script_api_key_2` 原文。
5. admin `PUT /api/providers/{id}/usage` 的秘密更新语义支持 `script_api_key_2`（非空替换、`clear_script_api_key_2=true` 清除、缺省保留），与现有 `script_api_key` 三态对称；`POST /usage/test` 草稿同样支持。
6. 前端 `ProviderUsageModal.vue` 在 custom/general 模板下、现有「Script API Key」正下方新增「附加密钥（apiKey2）」输入框（password 类型 + 已配置时显示「清除」按钮），与 script_api_key 视觉一致。
7. 本规格任务 6 给出千问 Token Plan 用量的完整 custom 脚本与配置参数，并记录一次真实端到端查询的证据（5h/7d tier 数值与页面一致）。

#### 1.2 非目标

1. 不为千问新增 native adapter 或进入 `DetectTokenPlanProvider` 检测表。
2. 不自动续期 Cookie / sec_token。
3. 不改 `ProviderQuotaResult`/`QuotaTier`/`BalanceItem` 统一结果模型。
4. 不改 `internal/usage` 或模型代理链路。
5. 不在脚本执行器暴露 `fetch`/文件/环境变量/进程能力（spec `2026-06-27` §4.1 不变）。
6. 不加密 `ScriptAPIKey2`（沿用现有本地配置安全模型）。
7. 不支持 `bodyType:"form"` 时 body 为非对象（字符串/数组/数字）——form body 语义上必须是 `key=value` 映射。

### 2. 数据模型变更

#### 2.1 `ProviderQuotaConfig`（`internal/providerquota/types.go:34-53`）

在 `ScriptAPIKey` 之后新增字段：

```go
type ProviderQuotaConfig struct {
    // ...现有字段...
    BaseURL            string `json:"base_url,omitempty"`
    ScriptAPIKey       string `json:"script_api_key,omitempty"`
    ScriptAPIKey2      string `json:"script_api_key_2,omitempty"` // 新增：custom/general 第二秘密槽
    ZenMuxBaseURL      string `json:"zenmux_base_url,omitempty"`
    // ...
}
```

`HasSecrets`（`types.go:159-165`）追加 `|| c.ScriptAPIKey2 != ""`。

#### 2.2 `PublicQuotaConfig`（`types.go:366-382`）

在 `ScriptAPIKeyConfigured` 之后新增：

```go
type PublicQuotaConfig struct {
    // ...
    ScriptAPIKeyConfigured    bool   `json:"script_api_key_configured"`
    ScriptAPIKey2Configured   bool   `json:"script_api_key_2_configured"` // 新增
    // ...
}
```

`ToPublicConfig`（`types.go:386-406`）追加 `ScriptAPIKey2Configured: c.ScriptAPIKey2 != ""`。

#### 2.3 `queryPlan`（`resolve.go:21-31`）

新增 `token2 string` 字段（custom/general 第二秘密）。

#### 2.4 `ScriptRequest`（`script.go:27-32`）

新增 `BodyType string` 字段：

```go
type ScriptRequest struct {
    URL      string            `json:"url"`
    Method   string            `json:"method"`
    Headers  map[string]string `json:"headers,omitempty"`
    Body     any               `json:"body,omitempty"`
    BodyType string            `json:"bodyType,omitempty"` // 新增："form" | "" | "json"
}
```

### 3. form body 编码规则（任务 3 实现契约）

当 `ScriptRequest.BodyType == "form"` 时：

1. `Body` 必须是 `map[string]any`（对象）。其他类型 → `script_error`（"form body must be an object"）。
2. 对 body 递归做占位符替换（先于编码，见第 4 节）。
3. 构造 `url.Values`，遍历 body 对象的每个键值：
   - 值为 `string`：直接加入。
   - 值为 `float64`/`int`/`int64`/`bool`：用 `fmt.Sprintf("%v", v)` 转字符串加入。
   - 值为 `nil`：跳过该键（不加入 form）。
   - 值为 `map`/`[]any`（对象/数组）：先 `json.Marshal` 成 JSON 字符串再加入（支持千问 `params` 嵌套对象）。
   - 其他类型：`json.Marshal` 成字符串加入。
4. 若脚本未显式设置 `Content-Type` 头，Go 层自动设为 `application/x-www-form-urlencoded`；若脚本已设，以脚本值为准（允许用户覆盖）。
5. 最终 body 字节 = `url.Values.Encode()` 的结果（按键名字典序、URL-encoded）。

当 `BodyType` 缺省、空或 `"json"` 时：维持 `script.go:245` 现有 `json.Marshal(req.Body)` 行为，但在 marshal 前对 body 递归做占位符替换（见第 4 节）。

### 4. 占位符替换扩展（任务 3 实现契约）

新增函数 `substitutePlaceholdersInBody(body any, values map[string]string) any`，语义：

- `string`：返回 `substitutePlaceholders(s, values)`。
- `map[string]any`：对每个 value 递归替换，返回新 map（不改原 map）。
- `[]any`：对每个元素递归替换，返回新 slice。
- 其他类型（数字/布尔/nil）：原样返回。

调用时机（`script.go` `ExecuteScript`，在 `validateScriptRequest` 之前、URL/headers 替换之后）：

```go
reqConfig.URL = substitutePlaceholders(reqConfig.URL, placeholderValues)
for k, v := range reqConfig.Headers {
    reqConfig.Headers[k] = substitutePlaceholders(v, placeholderValues)
}
reqConfig.Body = substitutePlaceholdersInBody(reqConfig.Body, placeholderValues) // 新增
```

`placeholderValues` 由 `manager.go` 构造，custom/general 分支新增 `"apiKey2": plan.token2`。

### 5. 千问配置示例（任务 6 交付，端到端验证目标）

#### 5.1 凭据获取步骤（写入规格，供用户操作）

1. 登录 `platform.qianwenai.com`，打开「Token Plan 管理」页面。
2. **Cookie**：浏览器 DevTools → Network → 任意 `cs-data.qianwenai.com/data/api.json` 请求 → Request Headers → 复制完整 `Cookie:` 值（含 `login_qianwenai_ticket=...; login_aliyunid_pk=...; cna=...; isg=...; tfstk=...` 等）。
3. **sec_token**：DevTools → Console → 执行 `ALIYUN_CONSOLE_CONFIG.SEC_TOKEN` → 复制返回的字符串（如 `WmFrs6jduM1ff0WGARDqN`）。

#### 5.2 推荐 custom 脚本

```javascript
({
  request: {
    url: "{{baseUrl}}/data/api.json?product=sfm_bailian&action=BroadScopeAspnGateway&api=zeldaHttp.apikeyMgr.%2Ftokenplan%2Fpersonal%2Fapi%2Fv2%2Fusage",
    method: "POST",
    bodyType: "form",
    headers: {
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded",
      "Accept": "application/json, text/plain, */*",
      "Referer": "https://platform.qianwenai.com/home/billing/subscription/token-plan-individual"
    },
    body: {
      product: "sfm_bailian",
      action: "BroadScopeAspnGateway",
      sec_token: "{{apiKey2}}",
      region: "cn-beijing",
      params: {
        Api: "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage",
        Data: {
          cornerstoneParam: {
            domain: "platform.qianwenai.com",
            consoleSite: "QIANWENAI",
            console: "ONE_CONSOLE",
            xsp_lang: "zh-CN",
            protocol: "V2",
            productCode: "p_efm"
          }
        },
        V: "1.0"
      }
    }
  },
  extractor: function (response) {
    if (response.code !== "200" || response.successResponse !== true) {
      return {
        __error_code: "upstream_business_error",
        __error_message: (response.data && response.data.errorMsg) || "qianwen usage query failed"
      };
    }
    var inner = response.data && response.data.DataV2 && response.data.DataV2.data && response.data.DataV2.data.data;
    if (!inner) {
      return {
        __error_code: "invalid_response",
        __error_message: "qianwen usage: missing data.DataV2.data.data"
      };
    }
    var tiers = [];
    if (typeof inner.per5HourPercentage === "number") {
      tiers.push({ window: "five_hour", utilization: inner.per5HourPercentage * 100 });
    }
    if (typeof inner.per1WeekPercentage === "number") {
      tiers.push({
        window: "seven_day",
        utilization: inner.per1WeekPercentage * 100,
        resetsAt: inner.per1WeekResetTime
      });
    }
    if (tiers.length === 0) {
      return {
        __error_code: "empty_result",
        __error_message: "qianwen usage: no percentage fields in response"
      };
    }
    return tiers;
  }
})
```

#### 5.3 字段映射与预期结果

- `per5HourPercentage`（5h 已用比例 0–1）→ `tier{window:"five_hour", utilization:<值 × 100>}`。
- `per1WeekPercentage`（7d 已用比例 0–1）→ `tier{window:"seven_day", utilization:<值 × 100>, resetsAt:<毫秒时间戳>}`。
- 卡片显示形如「5小时: 0%」「7天: 1% ◷ 6d23h」（倒计时由前端 `QuotaResultDisplay.vue` 已有逻辑计算）。

### 6. 生命周期与边界

1. **新字段持久化**：`ProviderQuotaConfig` 是 JSON 透传（`EncodeQuotaConfig`/`DecodeQuotaConfig`，`types.go:419-467`），新增 `ScriptAPIKey2` 自动随 JSON column 持久化，无需改 SQLite schema（与 `ExposedModels` 不同，`providerquota` 配置整体存为一个 JSON 文本列）。
2. **旧配置兼容**：缺失 `script_api_key_2` 字段时反序列化为空字符串，行为等同「未配置第二秘密」。
3. **模板切换**：`NormalizeForTemplate`（`resolve.go:36-72`）**不清空 `ScriptAPIKey2`**（与 `ScriptAPIKey`/`ZenMuxAPIKey` 同属独立安全域）；切换到 newapi/token_plan 后残留值不会被误用，因为 `resolveQueryPlan` 对应分支只读各自凭据。
4. **导入/导出/复制**：`ScriptAPIKey2` 随 `ProviderQuotaConfig` 一起导出（沿用现有导出秘密警告）；复制 provider 复制配置但不复制 snapshot（已有规则，新字段自动跟随）。
5. **凭据过期降级**：查询失败时 `result.success=false` + 对应错误码；`last_success_json` 保留上次成功（已有机制）；卡片显示警告图标 + 旧值。
6. **同源校验**：千问请求 URL 必须与 Base URL `https://cs-data.qianwenai.com` 同源；用户若误填 Base URL 会被 `validateScriptRequest` 拒绝（`invalid_config`）。
7. **重定向**：千问网关正常返回 200，不涉及重定向；现有重定向同源校验（`script.go:272-310`）不变。

## 任务详情

### 任务 1：config 层——`ScriptAPIKey2` 字段与公开 DTO

#### 需求

**Objective（目标）** — 在 `ProviderQuotaConfig` 增加 `ScriptAPIKey2` 秘密字段，在 `PublicQuotaConfig` 增加只读 `ScriptAPIKey2Configured` 标志，保证 JSON round-trip 与「公开响应不泄露原文」。

**Outcomes（成果）** — `internal/providerquota/types.go` 变更；`go test ./internal/providerquota/ -run 'Config|Public|Secrets'` 通过新增测试。

**Evidence（证据）** — 新增测试：`ScriptAPIKey2` JSON marshal 含字段、unmarshal 还原、`ToPublicConfig` 只输出 `ScriptAPIKey2Configured` 布尔、`HasSecrets` 在仅 `ScriptAPIKey2` 非空时返回 true。

**Constraints（约束）** — 不改 `ProviderQuotaConfig.Validate`（`ScriptAPIKey2` 可选，无强制校验）；JSON tag 用 `script_api_key_2,omitempty`；字段位置紧跟 `ScriptAPIKey` 之后（保持秘密字段聚集）。

**Edge Cases（边界）** — 空字符串；旧配置缺失该字段；同时配置 `ScriptAPIKey` 与 `ScriptAPIKey2`；`ToPublicConfig(nil)` 返回零值。

**Verification（验证）** — `go test -v -race ./internal/providerquota/ -run 'Config|Public|Secrets'` 全绿。

#### 计划

**文件：`internal/providerquota/types.go`**

1. 在 `ScriptAPIKey` 字段之后（约第 44 行后）新增：
   ```go
   // ScriptAPIKey2 is an optional second secret for custom/general templates
   // (e.g. a session token that must appear in the request body). It maps to
   // the {{apiKey2}} placeholder and is never returned in PublicQuotaConfig.
   ScriptAPIKey2 string `json:"script_api_key_2,omitempty"`
   ```
2. 在 `HasSecrets`（第 159-165 行）的返回表达式追加 `|| c.ScriptAPIKey2 != ""`。
3. 在 `PublicQuotaConfig`（第 366-382 行）的 `ScriptAPIKeyConfigured` 之后新增 `ScriptAPIKey2Configured bool \`json:"script_api_key_2_configured"\``。
4. 在 `ToPublicConfig`（第 386-406 行）的返回结构体里，`ScriptAPIKeyConfigured` 之后新增 `ScriptAPIKey2Configured: c.ScriptAPIKey2 != ""`。

**测试文件：`internal/providerquota/types_test.go`**

5. 新增 `TestScriptAPIKey2RoundTrip`：
   - 构造 `ProviderQuotaConfig{ScriptAPIKey2: "sec-token-abc"}`，`EncodeQuotaConfig` → `DecodeQuotaConfig`，断言 `ScriptAPIKey2 == "sec-token-abc"`。
   - `json.Marshal` 输出含 `"script_api_key_2":"sec-token-abc"`。
6. 新增 `TestPublicQuotaConfigRedactsScriptAPIKey2`：
   - `ToPublicConfig(&ProviderQuotaConfig{ScriptAPIKey2: "sec-token-abc"})` → 断言 `ScriptAPIKey2Configured == true`，且 marshal 后的 JSON 不含字符串 `sec-token-abc`。
7. 扩展 `TestHasSecrets`（若已存在）或新增 `TestHasSecretsScriptAPIKey2`：仅 `ScriptAPIKey2` 非空时 `HasSecrets() == true`。

#### 验证

- [ ] `go test -v -race ./internal/providerquota/ -run 'Config|Public|Secrets|HasSecrets'` 全绿。
- [ ] 手动确认 `ToPublicConfig` 输出 JSON 不含 `script_api_key_2` 原文（只有 `script_api_key_2_configured`）。

---

### 任务 2：resolve + manager——`token2` 解析与 `{{apiKey2}}` 占位符

#### 需求

**Objective（目标）** — 让 custom/general 模板的查询计划携带第二秘密 `token2`，并在 `manager.go` 构造 placeholders 时注入 `apiKey2`。`ScriptAPIKey2` 与 `ScriptAPIKey` 同属独立安全域，`NormalizeForTemplate` 不清空。

**Outcomes（成果）** — `internal/providerquota/resolve.go`、`internal/providerquota/manager.go` 变更；定向测试通过。

**Evidence（证据）** — `resolveQueryPlan` 对 custom/general 返回 `queryPlan.token2 == cfg.ScriptAPIKey2`，其他模板 `token2==""`（分支不读）；`NormalizeForTemplate` 保留 `ScriptAPIKey2`（与 `ScriptAPIKey` 一致，独立安全域）；manager 的 placeholders 含 `apiKey2`。

**Constraints（约束）** — `token2` 不参与 `ValidateForCard` 的「missing_credentials」校验（第二秘密可选）；custom/general 的第一秘密回退逻辑（`ScriptAPIKey` 否则 card APIToken）不变。

**Edge Cases（边界）** — `ScriptAPIKey2` 为空时 `token2=""`、占位符替换为空字符串（与 `apiKey` 同语义）；模板从 custom 切到 newapi 后 `ScriptAPIKey2` 残留但 `token2` 不被读取（newapi 分支只用 AccessToken）。

**Verification（验证）** — `go test -v -race ./internal/providerquota/ -run 'Resolve|Normalize|QueryPlan'` 全绿。

#### 计划

**文件：`internal/providerquota/resolve.go`**

1. 在 `queryPlan` struct（第 21-31 行）新增字段 `token2 string`，注释说明「custom/general 第二秘密（ScriptAPIKey2），其他模板为零值」。
2. 在 `resolveQueryPlan` 的 `case TemplateGeneral, TemplateCustom:`（第 184-193 行），返回的 `queryPlan` 追加 `token2: cfg.ScriptAPIKey2`：
   ```go
   return &queryPlan{
       template: cfg.TemplateType,
       scriptURL: baseURL,
       token:     token,
       token2:    cfg.ScriptAPIKey2, // 新增
   }, nil
   ```
3. `NormalizeForTemplate`（第 36-72 行）**不需要为 `ScriptAPIKey2` 增加清理逻辑**——`ScriptAPIKey` 与 `ZenMuxAPIKey` 在现有设计里是「独立安全域」（`resolve.go:33-35` 注释），所有模板保留、模板切换不清空；`ScriptAPIKey2` 遵循同一原则。安全性由 `resolveQueryPlan` 保证：只有 custom/general 分支读取 `ScriptAPIKey2`（作为 `token2`），其他模板分支不读，残留值不会被误用。

**文件：`internal/providerquota/manager.go`**

4. 在 `executeQuery` 的 `case plan.template == TemplateCustom || plan.template == TemplateGeneral:`（第 237-253 行），placeholders map 追加 `"apiKey2": plan.token2`：
   ```go
   placeholders := map[string]string{
       "baseUrl": plan.scriptURL,
       "apiKey":  plan.token,
       "apiKey2": plan.token2, // 新增
   }
   ```

**测试文件：`internal/providerquota/resolve_test.go`**

5. 新增 `TestResolveQueryPlanCustomToken2`：`resolveQueryPlan(&ProviderQuotaConfig{Enabled:true, TemplateType:"custom", BaseURL:"https://h.example", ScriptAPIKey2:"sec-xyz"}, "", "")` → 断言 `plan.token2 == "sec-xyz"`、`plan.token == ""`（无 ScriptAPIKey 且无 card token）。
6. 新增 `TestResolveQueryPlanCustomToken2Empty`：`ScriptAPIKey2` 缺省 → `plan.token2 == ""`。
7. 扩展 `TestNormalizeForTemplate`（若已存在）或新增 `TestNormalizeForTemplateClearsScriptAPIKey2`：
   - newapi 配置带 `ScriptAPIKey2:"x"` → normalize 后 `ScriptAPIKey2 == ""`。
   - custom 配置带 `ScriptAPIKey2:"x"` → normalize 后保留。

**测试文件：`internal/providerquota/manager_test.go`**

8. 新增或扩展 manager → script 集成测试（使用 `adapterHTTPClient` 注入 `httptest.Server`），断言 custom 脚本里 `{{apiKey2}}` 被替换为 `plan.token2` 的值，且出现在上游收到的 form body 的 `sec_token` 字段里。此测试依赖任务 3 的 form body 实现，可先写测试再在任务 3 后运行；或在任务 3 的测试里一并覆盖（见任务 3 步骤 8）。

#### 验证

- [ ] `go test -v -race ./internal/providerquota/ -run 'Resolve|Normalize'` 全绿。
- [ ] `go test -v -race ./internal/providerquota/ -run 'QueryPlan'` 全绿。
- [ ] manager → script 集成测试（任务 3 完成后）证明 `apiKey2` 到达上游 body。

---

### 任务 3：script.go——`bodyType:"form"` 编码与 body 占位符替换

#### 需求

**Objective（目标）** — 让 `custom`/`general` 脚本能产出 `application/x-www-form-urlencoded` body（通过 `request.bodyType:"form"` 声明），并把占位符替换从 URL/headers 扩展到 body 的所有字符串值（对 JSON 与 form body 都生效），同时严格保持现有 JSON body 行为向后兼容。

**Outcomes（成果）** — `internal/providerquota/script.go` 变更；定向测试覆盖 form 编码、body 替换、JSON 向后兼容、千问 fixture。

**Evidence（证据）** — `form body` 用 `url.Values` 编码、对象值 JSON marshal；`{{apiKey2}}` 在 body 内被替换；现有 JSON body 不含占位符时字节级不变；千问 fixture（fixture 响应 + form body + 双占位符）端到端返回两个 tier。

**Constraints（约束）** — 占位符替换仍在 Go 层（`ExecuteScript` 内），脚本运行时（goja）拿不到真值，spec `2026-06-27` §4.1 安全模型不变；`bodyType:"form"` 时 body 必须是对象，否则 `script_error`；不引入新的 HTTP 方法或禁止头变化；`substitutePlaceholdersInBody` 不修改入参原 map（返回新值）。

**Edge Cases（边界）** — body 为 nil（无 body）；body 是对象/数组/字符串/数字；form body 含对象值（千问 `params`）；占位符在 body 嵌套对象里；JSON body 含 `{{apiKey}}`（此前不替换，现在替换——需确认对存量脚本无影响）；form body 脚本未设 Content-Type（Go 自动补）。

**Verification（验证）** — `go test -v -race ./internal/providerquota/ -run 'Script|Form|Body|Placeholder'` 全绿。

#### 计划

**文件：`internal/providerquota/script.go`**

1. 在 `ScriptRequest`（第 27-32 行）新增字段 `BodyType string \`json:"bodyType,omitempty"\``。
2. 新增函数 `substitutePlaceholdersInBody`（放在 `substitutePlaceholders` 之后，约第 390 行后）：
   ```go
   // substitutePlaceholdersInBody recursively replaces placeholders in all
   // string values within body. It returns a new value; the input is not
   // mutated. Non-string scalars (numbers, bools, nil) are returned as-is.
   func substitutePlaceholdersInBody(body any, values map[string]string) any {
       switch v := body.(type) {
       case string:
           return substitutePlaceholders(v, values)
       case map[string]any:
           out := make(map[string]any, len(v))
           for k, val := range v {
               out[k] = substitutePlaceholdersInBody(val, values)
           }
           return out
       case []any:
           out := make([]any, len(v))
           for i, val := range v {
               out[i] = substitutePlaceholdersInBody(val, values)
           }
           return out
       default:
           return body
       }
   }
   ```
3. 在 `ExecuteScript`（第 69-73 行）的 headers 替换循环之后新增 body 替换：
   ```go
   reqConfig.Body = substitutePlaceholdersInBody(reqConfig.Body, placeholderValues)
   ```
4. 重构 `doHTTPRequest`（第 241-252 行）的 body 构造逻辑，按 `BodyType` 分支：
   ```go
   var bodyReader io.Reader
   if req.Body != nil {
       bodyBytes, err := encodeRequestBody(req)
       if err != nil {
           return nil, 0, err
       }
       if len(bodyBytes) > maxRequestBodySize {
           return nil, 0, fmt.Errorf("request body exceeds %d bytes", maxRequestBodySize)
       }
       bodyReader = bytes.NewReader(bodyBytes)
   }
   ```
   并新增 `encodeRequestBody`：
   ```go
   // encodeRequestBody serializes the script body. BodyType "form" produces
   // application/x-www-form-urlencoded; "" or "json" produces JSON (existing
   // behavior). Form body must be an object; object/array field values are
   // JSON-marshaled to support nested structures like qianwen's params field.
   func encodeRequestBody(req *ScriptRequest) ([]byte, error) {
       if strings.EqualFold(req.BodyType, "form") {
           obj, ok := req.Body.(map[string]any)
           if !ok {
               return nil, fmt.Errorf("form body must be an object, got %T", req.Body)
           }
           v := make(url.Values, len(obj))
           for key, val := range obj {
               s, err := formFieldValue(val)
               if err != nil {
                   return nil, fmt.Errorf("form field %q: %w", key, err)
               }
               if s == nil {
                   continue // nil values skipped
               }
               v.Set(key, *s)
           }
           return []byte(v.Encode()), nil
       }
       // Default: JSON (existing behavior).
       return json.Marshal(req.Body)
   }

   // formFieldValue converts a script body field value to its form-urlencoded
   // string representation. Returns nil for nil values (field skipped).
   func formFieldValue(val any) (*string, error) {
       switch v := val.(type) {
       case nil:
           return nil, nil
       case string:
           s := v
           return &s, nil
       case bool, float64, int, int64:
           s := fmt.Sprintf("%v", v)
           return &s, nil
       default:
           // Objects, arrays, and any other type: JSON-marshal.
           b, err := json.Marshal(v)
           if err != nil {
               return nil, err
           }
           s := string(b)
           return &s, nil
       }
   }
   ```
5. 在 `doHTTPRequest` 创建 `httpReq` 之后（第 254-258 行附近），对 form body 自动补 Content-Type：若 `BodyType=="form"` 且脚本未设 `Content-Type`（大小写不敏感遍历 `req.Headers`），则 `httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")`。

**测试文件：`internal/providerquota/script_test.go`**

6. 新增 `TestEncodeRequestBodyForm`：
   - `ScriptRequest{BodyType:"form", Body: map[string]any{"a":"1","b":"2"}}` → 输出 `"a=1&b=2"`（`url.Values.Encode` 字典序）。
   - 对象值：`Body: map[string]any{"params": map[string]any{"x":1}}` → 输出 `"params=%7B%22x%22%3A1%7D"`（JSON-encoded + URL-encoded）。
   - 非对象 body：`Body: "string"` → 返回 error `"form body must be an object"`。
   - nil 值：`Body: map[string]any{"skip": nil, "keep": "y"}` → 输出 `"keep=y"`。
7. 新增 `TestSubstitutePlaceholdersInBody`：
   - 字符串：`"{{apiKey2}}"` + `{"apiKey2":"sec"}` → `"sec"`。
   - 嵌套对象：`{a:{b:"{{apiKey2}}"}}` → `{a:{b:"sec"}}`。
   - 数组：`["{{apiKey}}", 1]` → `["k", 1]`。
   - 数字/布尔不变。
   - 入参原 map 不被修改（深拷贝断言）。
8. 新增 `TestExecuteScriptFormBodyQianwenFixture`（端到端，依赖任务 1-2）：
   - 启动 `httptest.Server`，记录收到的请求方法和 body。
   - 脚本用第 5.2 节的千问脚本（`bodyType:"form"`，`sec_token:"{{apiKey2}}"`，`Cookie:"{{apiKey}}"`，`params` 为对象）。
   - `placeholderValues = {"baseUrl": server.URL, "apiKey": "cookie-val", "apiKey2": "sec-tok"}`。
   - server 返回第 2.3 节的千问 fixture。
   - 断言：server 收到 POST、Content-Type 含 `application/x-www-form-urlencoded`、body 解码后 `sec_token=="sec-tok"`、`product=="sfm_bailian"`、`params` 解析为 JSON 后 `Api` 字段正确；`Cookie` 头含 `cookie-val`。
   - 断言结果：`Success==true`、`Tiers` 长度 2、`five_hour` utilization 0、`seven_day` utilization 1 且 `ResetsAt` 对应 `1785462900000` 毫秒。
9. 新增 `TestExecuteScriptJSONBodyBackwardCompat`：
   - JSON body（无 BodyType）不含占位符时，上游收到的 body 与 `json.Marshal` 字节级一致（用 `bytes.Equal` 断言原始对象 marshal 结果）。
   - JSON body 含 `{{apiKey}}` 时（此前不替换），现在替换为真值——断言新行为（这是有意的增强，测试锁定）。
10. 扩展 `TestValidateScriptRequest`（若已存在）覆盖 form body 的同源校验仍生效。

#### 验证

- [ ] `go test -v -race ./internal/providerquota/ -run 'Script|Form|Body|Placeholder|EncodeRequest'` 全绿。
- [ ] 千问 fixture 测试证明 `sec_token`/`Cookie`/`params` 正确到达上游，结果含两个 tier。
- [ ] JSON body 向后兼容测试字节级通过。

---

### 任务 4：admin handler——`script_api_key_2` 秘密更新语义

#### 需求

**Objective（目标）** — 让 admin `PUT /api/providers/{id}/usage` 与 `POST /usage/test` 支持 `script_api_key_2` 的「保留 / 替换 / 清除」三态语义，对称于现有 `script_api_key`，并在公开响应中只返回 `script_api_key_2_configured`。

**Outcomes（成果）** — `internal/admin/provider_quota_handler.go` 变更；handler 测试覆盖三态。

**Evidence（证据）** — 测试：PUT 带 `script_api_key_2:"x"` 替换、缺省保留、`clear_script_api_key_2:true` 清除；GET 配置响应含 `script_api_key_2_configured` 且不含原文；test 草稿携带 `script_api_key_2`。

**Constraints（约束）** — 复用现有两处对称模式：`applyQuotaUpdate`（第 357 行起）第 397-400 行的显式 `applySecretPatch` 调用列表，以及 `validateProviderQuotaSecretPatches`（第 335-352 行）的 `patches` slice（用于拒绝「同时替换+清除」）；不改变其他秘密字段的语义；`script_api_key_2` 只在 custom/general 模板下被接受（其他模板下被 `NormalizeForTemplate` 清空，无需 handler 特判）。

**Edge Cases（边界）** — 同时 `script_api_key_2:"x"` 与 `clear_script_api_key_2:true`（替换优先，与 `script_api_key` 一致）；模板切换后残留（由 `NormalizeForTemplate` 处理）；旧客户端不发送该字段（保留空）。

**Verification（验证）** — `go test -v -race ./internal/admin/ -run 'ProviderQuota'` 全绿。

#### 计划

**文件：`internal/admin/provider_quota_handler.go`**

1. 在 quota 配置更新请求结构体（第 319 行 `ScriptAPIKey *string` 附近）新增：
   ```go
   ScriptAPIKey2      *string `json:"script_api_key_2"`
   ```
   并在第 328-329 行 `ClearScriptAPIKey`/`ClearZenMuxAPIKey` 附近新增：
   ```go
   ClearScriptAPIKey2 bool `json:"clear_script_api_key_2"`
   ```
2. 在 `validateProviderQuotaSecretPatches`（第 335-352 行）的 `patches` slice 末尾追加一行，让「同时替换+清除」校验覆盖新字段：
   ```go
   {name: "script_api_key_2", value: req.ScriptAPIKey2, clear: req.ClearScriptAPIKey2},
   ```
3. 在 `applyQuotaUpdate`（第 357 行起）第 397-400 行的显式 `applySecretPatch` 调用列表末尾追加：
   ```go
   applySecretPatch(&c.ScriptAPIKey2, req.ScriptAPIKey2, req.ClearScriptAPIKey2)
   ```
4. 在公开配置响应构造（搜索 `ScriptAPIKeyConfigured` 的输出处，通常在 `ToPublicConfig` 调用结果或 handler 自建的 DTO 里），确认 `script_api_key_2_configured` 由 `ToPublicConfig` 自动产出（任务 1 已加），handler 无需额外改动——**此步骤为验证项**，若 handler 自建 DTO 覆盖了 `PublicQuotaConfig`，需补字段。

**测试文件：`internal/admin/provider_quota_handler_test.go`**

4. 新增 `TestPUTProviderUsageScriptAPIKey2Replace`：PUT 带 `script_api_key_2:"new-sec"` → 再 GET → `script_api_key_2_configured==true`；直接读存储确认原文为 `"new-sec"`。
5. 新增 `TestPUTProviderUsageScriptAPIKey2Preserve`：已有 `ScriptAPIKey2:"old"`，PUT 不带该字段 → 原文仍为 `"old"`。
6. 新增 `TestPUTProviderUsageScriptAPIKey2Clear`：已有 `ScriptAPIKey2:"old"`，PUT 带 `clear_script_api_key_2:true` → 原文为空、`script_api_key_2_configured==false`。
7. 新增 `TestPOSTProviderUsageTestScriptAPIKey2`：test 草稿带 `script_api_key_2` → 上游（mock）收到对应替换值（与任务 3 步骤 8 呼应，可在 providerquota 包的 manager 集成测试里覆盖，handler 层只验证草稿透传）。

#### 验证

- [ ] `go test -v -race ./internal/admin/ -run 'ProviderQuota'` 全绿。
- [ ] GET 配置响应 JSON 不含 `script_api_key_2` 原文。

---

### 任务 5：前端——custom/general「附加密钥」输入框 + i18n + API 类型

#### 需求

**Objective（目标）** — 在 `ProviderUsageModal.vue` 的 custom/general 模板下、「Script API Key」正下方新增「附加密钥（apiKey2）」输入框，复用 script_api_key 的视觉与三态交互（输入替换、已配置显示「清除」），并在 `quotaForm.ts`/`useApi.ts`/`useI18n.ts` 同步类型与双语文案。

**Outcomes（成果）** — `internal/frontend/src/components/ProviderUsageModal.vue`、`internal/frontend/src/utils/quotaForm.ts`、`internal/frontend/src/composables/useApi.ts`、`internal/frontend/src/composables/useI18n.ts` 变更；前端测试通过、`npm run build` 成功。

**Evidence（证据）** — 组件测试断言 custom 模板下显示附加密钥输入框、newapi 模板下隐藏；quotaForm 测试断言 buildSavePayload/buildTestPayload 在填入值时携带 `script_api_key_2`、勾选清除时携带 `clear_script_api_key_2`；中英文案键存在且无裸 key。

**Constraints（约束）** — 不改变现有 script_api_key 字段的布局与逻辑；附加密钥输入框只在与 script_api_key 相同的显示条件下出现（custom/general，由 `showAPIKey` 控制逻辑的对称变量 `showAPIKey2` 控制）；密码类型 input；移动端不横向溢出。

**Edge Cases（边界）** — 已配置 + 未输入（显示「已配置」占位 + 「清除」按钮）；已配置 + 输入新值（替换）；未配置 + 空输入（不发送字段）；模板从 custom 切到 newapi（输入框隐藏，form 字段重置）。

**Verification（验证）** — `npm --prefix internal/frontend test` 全绿；`npm --prefix internal/frontend run build` 成功；360/768/1440px 不溢出（手测）。

#### 计划

**文件：`internal/frontend/src/utils/quotaForm.ts`**

1. 在 `QuotaFormState`（第 14 行起，第 20 行 `script_api_key` 附近）新增：
   ```ts
   script_api_key_2: string
   ```
   并在 `clear_*` 区域（第 27 行 `clear_script_api_key` 附近）新增：
   ```ts
   clear_script_api_key_2: boolean
   ```
2. 在 `initialQuotaForm`/默认 form（`ProviderUsageModal.vue` 第 271-284 行的 `form` reactive 对象，或 quotaForm.ts 的工厂函数）补 `script_api_key_2: ''` 与 `clear_script_api_key_2: false`。
3. 在 `showAPIKeyField`（第 83 行 `return ['general','custom'].includes(templateType)`）旁新增 `showAPIKey2Field`，语义完全相同（custom/general 显示）；或直接复用 `showAPIKeyField`（两者显示条件一致，可共用一个 computed）。
4. 在 `buildSavePayload`（第 107 行起，第 109/112/136/154 行的 `usesScriptAPIKey`/`replacesScriptAPIKey` 逻辑）对称新增：
   ```ts
   const replacesScriptAPIKey2 = usesScriptAPIKey && !!form.script_api_key_2
   // ...
   if (replacesScriptAPIKey2) data.script_api_key_2 = form.script_api_key_2
   // ...
   if (form.clear_script_api_key_2 && !replacesScriptAPIKey2) data.clear_script_api_key_2 = true
   ```
   `buildTestPayload`（第 170 行起）同理：`if (usesScriptAPIKey && form.script_api_key_2) data.script_api_key_2 = form.script_api_key_2`。
5. 在 form 重置逻辑（`ProviderUsageModal.vue` 第 363-367 行 `form.script_api_key = ''` / `form.clear_script_api_key = false` 附近）补 `form.script_api_key_2 = ''` 与 `form.clear_script_api_key_2 = false`。

**文件：`internal/frontend/src/components/ProviderUsageModal.vue`**

6. 在「Script API Key」输入块（第 91-94 行）正下方，复制一个对称的「附加密钥」输入块：
   ```vue
   <div v-if="showAPIKey" class="mt-3">
     <label class="block text-sm font-medium mb-1">{{ t('quota.script_api_key_2') }}</label>
     <div class="flex gap-2 items-center">
       <input v-model="form.script_api_key_2" type="password" class="min-w-0 flex-1 app-control rounded-md px-3 py-2 text-sm" :placeholder="savedConfig?.script_api_key_2_configured ? t('quota.script_api_key_configured') : ''" />
       <button v-if="savedConfig?.script_api_key_2_configured" type="button" class="text-xs text-danger hover:underline whitespace-nowrap" @click="form.clear_script_api_key_2 = true">{{ t('quota.clear_script_key') }}</button>
     </div>
     <div class="text-xs text-text-secondary mt-1">{{ t('quota.script_api_key_2_hint') }}</div>
   </div>
   ```
   （`showAPIKey` computed 已在第 294 行存在且条件为 custom/general，直接复用。）

**文件：`internal/frontend/src/composables/useApi.ts`**

7. 在 quota 配置 TypeScript 类型（搜索 `script_api_key_configured` 字段处）新增 `script_api_key_2_configured?: boolean`；在保存/测试 payload 类型（搜索 `script_api_key?: string` 处）新增 `script_api_key_2?: string` 与 `clear_script_api_key_2?: boolean`。

**文件：`internal/frontend/src/composables/useI18n.ts`**

8. 在中英文 quota 文案（搜索 `quota.script_api_key` 与 `quota.clear_script_key` 处）新增三条：
   - `quota.script_api_key_2`：中「附加密钥（apiKey2）」/ 英「Additional secret (apiKey2)」。
   - `quota.script_api_key_2_hint`：中「用于 custom 脚本的第二个占位符 {{apiKey2}}，例如千问 Token Plan 的 sec_token」/ 英「Second placeholder {{apiKey2}} for custom scripts, e.g. qianwen Token Plan sec_token」。
   - `quota.script_api_key_configured`（已存在）复用为占位文案；若不存在则补「中：已配置 / 英：Configured」。

**测试文件：`internal/frontend/src/utils/quotaForm.test.ts` 与 `internal/frontend/src/components/ProviderUsageModal.test.ts`**

9. `quotaForm.test.ts` 新增：
   - custom 模板 + `script_api_key_2:"x"` → save payload 含 `script_api_key_2:"x"`。
   - custom 模板 + `clear_script_api_key_2:true` 且无输入 → save payload 含 `clear_script_api_key_2:true`。
   - newapi 模板 → save/test payload 不含 `script_api_key_2`。
10. `ProviderUsageModal.test.ts` 新增：custom 模板渲染时「附加密钥」label 可见；newapi 模式不可见。

#### 验证

- [ ] `npm --prefix internal/frontend test` 全绿。
- [ ] `npm --prefix internal/frontend run build` 成功。
- [ ] `grep -r "quota.script_api_key_2" internal/frontend/src` 中英文都命中。
- [ ] 手测：custom 模式下输入框出现、保存后 GET 显示「已配置」、清除后还原。

---

### 任务 6：千问 Token Plan 配置示例 + 端到端验证 + 文档回写

#### 需求

**Objective（目标）** — 用真实千问凭据端到端验证「form body + 双占位符 + 双秘密槽」链路，确认 mcc 卡片能正确显示 5 小时与 7 天已用百分比及重置倒计时，并把验证证据与最终脚本回写本规格。

**Outcomes（成果）** — 本规格「验证」小节填写实测数值；`internal/frontend/dist` 不需要改动（无前端产物变更以外的 dist 重建需求，除非任务 5 改了前端源码——此时按 CLAUDE.md 提交重建的 dist）。

**Evidence（证据）** — 一次真实查询的 `ProviderQuotaResult`（success:true、两个 tier、数值与千问页面一致）；卡片截图或日志片段。

**Constraints（约束）** — 验证用的 Cookie + sec_token 是用户私有凭据，**不写入规格/代码/提交**，仅在本地 mcc 配置界面填入；端到端验证后不清除配置（用户持续使用）。

**Edge Cases（边界）** — 凭据过期（重新获取）；`per5HourPercentage` 为 0（已用 0%，页面剩余 100%）或 `per1WeekPercentage` 为 1.0（已用 100%，页面剩余 0%）；`per1WeekResetTime` 已过（倒计时显示「待刷新」，已有逻辑）。

**Verification（验证）** — mcc 卡片在 custom 模板 + 千问配置下，手动刷新后显示两个 tier，数值与千问页面「每 5 小时额度 / 每 7 天额度」一致。

#### 计划

1. 完成任务 1-5 的代码与测试，`make test` 全绿。
2. `make build` 生成二进制（或 `go run ./cmd/server`），启动 mcc。
3. 在 mcc 管理后台为千问供应商卡片（或新建一个 BaseURL=`https://cs-data.qianwenai.com` 的专用卡片）打开「用量」弹窗，配置：
   - 模板：custom
   - Base URL：`https://cs-data.qianwenai.com`
   - Script API Key：第 5.1 节获取的完整 Cookie
   - 附加密钥（apiKey2）：第 5.1 节获取的 sec_token
   - 脚本：第 5.2 节的推荐脚本
4. 点击「测试查询」，确认右侧结果显示 `success:true` 与两个 tier。
5. 保存配置，点击卡片「刷新」，确认卡片标题行显示「5小时: X%」「7天: Y% ◷ <倒计时>」。
6. 把实测的 `per5HourPercentage`/`per1WeekPercentage` 数值与查询时间填入下方「验证」小节；若数值与千问页面不一致，回到任务 3 检查 extractor。
7. （可选）在 `sdd-docs/changes/changelog.md` 或下一版 release notes 里提及「custom 脚本支持 form body 与附加密钥」。

#### 验证

- [x] 任务 1-5 全部 `[x]`，`go test ./...` + `go vet ./...` + race（providerquota/admin/config）+ `npm test`（211）+ `npm run build` 全绿。
- [x] 千问页面「每 5 小时额度 剩余量 100.0%」与 mcc 卡片 `five_hour.utilization = 0`（已用 0%）一致。
- [x] 千问页面「每 7 天额度 剩余量 0.0%」与 mcc 卡片 `seven_day.utilization = 100`（已用 100%）一致。
- [x] 千问页面「额度重置时间 2026-07-30 18:55:00」与 mcc 卡片 `seven_day.resetsAt`（`per1WeekResetTime = 1785462900000` ms）一致。
- [x] 实测数值回写本小节（2026-07-27 端到端验证）：
  - 查询时间：2026-07-27（用户本地端到端）
  - `per5HourPercentage: 0.0` → mcc `five_hour.utilization: 0`（× 100；已用 0% = 页面剩余 100%）
  - `per1WeekPercentage: 1.0` → mcc `seven_day.utilization: 100`（× 100；已用 100% = 页面剩余 0%）
  - `per1WeekResetTime: 1785462900000` → mcc `seven_day.resetsAt`，前端显示 2026-07-30 18:55:00（与页面一致）
  - **修正记录**：首次验证发现 `perXxxPercentage` 语义误判——首次抓包仅见 `per5HourPercentage:0.0`，两种语义（0–100 已用百分比 vs 0–1 已用比例）都成立，误判为前者；端到端实测 `per1WeekPercentage:1.0` 配合页面「剩余量 0.0%」确认为 **0–1 已用比例**。extractor 已修正为 `utilization = percentage * 100`，fixture 测试断言同步更新（`seven_day.utilization == 100`）。**用户需用 §5.2 最新脚本替换 mcc 配置中已保存的旧脚本**，否则 7 天会显示 1%（应为 100%）。
