# 供应商额度查询凭据拆分设计

日期：2026-06-28

## 目标

解决 `ProviderQuotaConfig.APIKey` 同时代表通用脚本凭据和 ZenMux 用量凭据造成的歧义，并对齐 cc-switch 的 ZenMux 行为：手工填写的完整用量 URL/Key 组合优先，否则整组回退供应商卡片 APIURL/Token。

设计必须满足：

- General/Custom 凭据永远不会被 ZenMux 查询使用。
- ZenMux 专用凭据永远不会被 General/Custom 脚本使用。
- 模板切换时允许在同一请求中提交并保留新凭据。
- 未使用的另一类凭据可以保留，切回模板时无需重新输入；查询分派必须严格隔离。
- 旧版 `api_key`/ZenMux `base_url` 配置可自动迁移，不泄漏或误分类。
- enabled 配置在保存前按 effective provider 完成必需凭据校验。

## 方案选择

### 采用：独立字段与原子回退

新增独立配置字段：

```go
ScriptAPIKey  string `json:"script_api_key,omitempty"`
ZenMuxBaseURL string `json:"zenmux_base_url,omitempty"`
ZenMuxAPIKey  string `json:"zenmux_api_key,omitempty"`
```

保留现有 `BaseURL`，仅用于 General、Custom 和 NewAPI。旧 `APIKey` 字段只承担兼容读取，不再作为新配置的运行时凭据来源。

不采用以下替代方案：

- 单字段加 `api_key_purpose`：仍需处理 partial patch、历史 purpose 和字段重解释，维护成本高。
- 比较新旧 key 字符串：无法区分用户重新提交相同值，也无法抵抗卡片 URL 变化导致的历史语义丢失。

## 凭据解析规则

### General/Custom

```text
ScriptAPIKey 非空 → 使用 ScriptAPIKey
ScriptAPIKey 为空 → 回退供应商卡片 APIToken
```

脚本 Base URL 使用现有 `BaseURL`；为空时回退卡片 APIURL。

### ZenMux Token Plan

ZenMux 覆盖凭据必须作为原子组合处理：

```text
ZenMuxBaseURL 与 ZenMuxAPIKey 都非空 → 使用专用覆盖组合
两者都为空 → 整组回退卡片 APIURL + APIToken
只有一个非空 → invalid_config，禁止 URL/Key 混搭
```

该规则与 cc-switch 的“手填组合优先，否则回退 provider credentials”一致。

provider 解析继续遵循现有安全规则：

- MiMo 始终返回 unsupported。
- 可检测卡片 URL 与显式 provider 不一致时，在网络请求前拒绝。
- 无法检测的自定义网关允许显式选择 provider。

### 其他模板

- NewAPI：只使用 `AccessToken`。
- Kimi/智谱/MiniMax：只使用卡片 APIToken。
- 火山：只使用 AccessKey ID/SecretAccessKey。
- 官方余额：只使用卡片 APIToken。

## 保存、测试和清除语义

请求 DTO 新增：

```text
script_api_key
clear_script_api_key
zenmux_base_url
zenmux_api_key
clear_zenmux_api_key
```

处理顺序：

1. 克隆旧配置。
2. 应用普通字段和各自独立的 secret patch。
3. 迁移仍存在的 legacy 字段。
4. 按模板清理真正不适用的非凭据字段；`ScriptAPIKey` 与 `ZenMuxAPIKey` 相互独立并允许同时保存。
5. 使用卡片 APIURL 解析 effective provider。
6. 按 effective provider 验证完整配置。
7. PUT 持久化；`/usage/test` 只使用草稿，不持久化。

显式 clear 只清除对应字段，不影响另一类 key。

## 有效配置校验

新增带卡片上下文的校验入口，例如：

```go
ValidateForCard(cardAPIURL, cardAPIToken string) error
```

它先执行现有结构校验，再解析 effective provider：

- ZenMux：专用 URL/Key 必须同时存在或同时为空；回退时卡片 URL/Token 必须完整。
- 火山：AK/SK 必须完整。
- 显式/检测 provider 不一致：返回 `provider_mismatch`。
- enabled 配置校验失败时 PUT 返回 400，原配置保持不变。

`resolveQueryPlan` 与保存校验必须复用同一 ZenMux 原子凭据解析 helper，避免保存和查询规则漂移。

## 旧配置迁移

旧 JSON 可能只有 `api_key` 和 `base_url`。迁移依据旧配置本身与卡片 URL：

- General/Custom：legacy `api_key` → `ScriptAPIKey`。
- Token Plan + effective ZenMux：legacy `api_key` → `ZenMuxAPIKey`；legacy `base_url` → `ZenMuxBaseURL`。
- 其他模板/provider：丢弃无效 legacy `api_key`。

迁移不得依赖后来修改后的卡片 URL来重建历史用途。对于旧 Token Plan、空显式 provider、同时存在 legacy `api_key` 和 `base_url` 的配置，按其持久化结构判定为旧 ZenMux 覆盖组合；这是旧实现中唯一合法使用该组合的 Token Plan 类型。

迁移要求：

- SQLite 加载 provider 后，在已有卡片 APIURL 上下文中执行内存迁移。
- 导入、复制和保存路径执行同一迁移 helper。
- 新写入 JSON 不再产生 legacy `api_key`。
- 在兼容期内仍可读取 legacy 字段；公开 DTO 永不返回秘密原文。

## 供应商卡片 URL/Token 更新

修改卡片 APIURL 或 APIToken 时：

- 不重解释已拆分的 Script/ZenMux 专用字段。
- 使用卡片 fallback 的额度配置必须清除旧 snapshot。
- 自动检测 provider 的结果在下一次保存/测试/查询时基于新卡片 URL重新解析。
- 如果新 URL 与显式 provider 冲突，查询和保存均返回 provider mismatch，不发送网络请求。

## 前端与公开 DTO

公开配置新增：

```text
script_api_key_configured
zenmux_api_key_configured
zenmux_base_url
```

不返回任何秘密原文。

- General/Custom 表单绑定 `script_api_key`。
- ZenMux 表单绑定 `zenmux_base_url`、`zenmux_api_key`。
- 清除按钮分别发送对应 clear flag。
- 模板切换不复制或重命名 key。
- test payload 只携带当前模板允许的新输入字段。

## TDD 验收

必须先观察以下测试 RED，再逐项实现至 GREEN：

1. General → ZenMux 同一 PUT 提交新 ZenMux key：ZenMux key 保存为新值；原 Script key 保留在独立字段但 ZenMux 查询绝不使用它。
2. ZenMux → General 同一 PUT 提交新 Script key：Script key 保存为新值；原 ZenMux key 保留在独立字段但 General 查询绝不使用它。
3. 两类 key 同时存在时，General 查询只发送 Script key，ZenMux 查询只发送 ZenMux key。
4. ZenMux 专用 URL/Key 均空时，整组回退卡片凭据。
5. ZenMux 专用 URL/Key 只有一个时，PUT 和 `/usage/test` 均拒绝且不发网络请求。
6. 显式/自动 ZenMux 与自动火山缺必需凭据时，enabled PUT 返回 400 且不修改存储。
7. legacy General、legacy 显式 ZenMux、legacy 自动 ZenMux 配置迁移正确。
8. 卡片 URL 更新后，旧 ZenMux key 不会变成 Script key；fallback snapshot 被清除。
9. 公开 DTO、供应商列表、日志和错误不包含任何 key 原文。
10. 前端 payload 和字段可见性测试覆盖两套独立 key。

全量门禁：

```bash
go test ./...
go test -race ./internal/providerquota ./internal/admin ./internal/config
go vet ./...
go build ./...
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
git diff --check
```

## 非目标

- 不改变 ZenMux 上游响应格式与额度解析。
- 不新增数据库列；额度配置继续存储为 provider 行中的 JSON。
- 不把秘密原文返回浏览器。
- 不修改其他供应商的协议适配器。
