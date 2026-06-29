# 模型用量凭据加固设计

日期：2026-06-28

## 背景

供应商用量查询完成 Script API Key 与 ZenMux API Key 分离后，审核确认仍有三个缺陷：

1. ZenMux 原生适配器使用 Go 默认重定向行为，携带 `Authorization` 的 HTTPS 请求可被重定向到 HTTP。
2. `config.Store.Load` 只执行默认值归一化，没有迁移旧版 `api_key`；SQLite 加载路径则会迁移。
3. 前端可能同时提交非空替换密钥和对应的 `clear_*` 标记，而后端当前让清除操作优先，导致替换密钥被删除。

## 目标与非目标

目标：

- 所有经 `TokenPlanAdapter` 发出的认证请求不得跨源重定向，也不得从 HTTPS 降级到 HTTP。
- JSON Store 与 SQLite Store 对旧版额度查询凭据采用相同的迁移语义。
- 密钥替换与清除在前端和后端均为互斥操作，不再存在含糊请求。
- 使用 TDD 为三个缺陷增加回归测试。

非目标：

- 不重构 Script Executor 的现有手动重定向实现。
- 不更改供应商检测、额度解析、快照缓存或凭据回退规则。
- 不增加新的数据库字段或 API 响应字段。
- 不修改已有审核记录文件。

## 方案

### 1. 认证请求重定向策略

在 `TokenPlanAdapter` 的请求执行边界统一应用安全重定向策略，而不是只修补 ZenMux 调用点：

- 复制现有 `http.Client`，不修改注入或共享的客户端实例。
- 为副本安装 `CheckRedirect`。
- 每次跳转都要求目标 URL 与首个请求 URL 同源；同源定义为 scheme、hostname 和有效端口全部相同。
- 因此 HTTPS→HTTP、HTTP→HTTPS、主机变化和端口变化都被拒绝。
- 保留现有 timeout、transport、cookie jar 等客户端设置。
- 若调用方原本配置了 `CheckRedirect`，先执行安全边界，再执行原回调；任何一方拒绝即停止。
- 重定向拒绝必须发生在下一跳发送前，保证 `Authorization` 不到达被拒绝目标。

该策略覆盖 ZenMux 卡片 Token、ZenMux 专用 Key，以及其他 Token Plan 原生适配器的认证请求，避免相同缺陷在固定端点适配器中继续存在。

### 2. 统一旧凭据迁移

把额度查询旧凭据迁移纳入 `Config.NormalizeDefaults` 的供应商循环：

- 先执行供应商原有默认值归一化。
- 再以供应商自身 `APIURL` 调用 `providerquota.MigrateLegacyCredentials`。
- `Store.Load` 已调用 `NormalizeDefaults`，因此 JSON 加载自动获得迁移。
- SQLite 当前显式迁移保留；迁移函数必须继续保持幂等，重复调用不改变结果。

这样迁移语义位于公共配置归一化入口，避免不同 `ConfigStore` 实现漂移。

### 3. 密钥替换与清除互斥

前端与后端同时约束：

- 前端 `buildSavePayload`：对应输入存在非空替换值时，只发送替换字段，不发送 `clear_*`；只有替换值为空且用户明确清除时才发送 `clear_*`。
- 前端组件：用户在点击清除后重新输入非空值时，payload 构建规则自动以替换为准，无需依赖 watcher。
- 后端：在 PUT `/usage` 与 POST `/usage/test` 进入配置合并前检查请求；同一凭据同时包含非空替换值和 `clear_* = true` 时返回 HTTP 400。
- 后端继续保留 `applySecretPatch` 的 clear-first 内部语义，因为冲突请求已在边界被拒绝；这避免悄然改变既有内部函数契约。
- ZenMux 清除覆盖 `zenmux_api_key`，同时由前端清空 `zenmux_base_url`，保持覆盖 URL/Key 的原子性。

需要校验的四组凭据为：Script API Key、ZenMux API Key、NewAPI Access Token、火山 Secret Access Key。

## 数据流与错误处理

保存/测试请求先完成 JSON 解码，再进行凭据补丁冲突校验。冲突返回稳定的 HTTP 400 错误，不加载、修改或保存配置，也不发起网络请求。

认证额度请求在发送第一跳前复制客户端并安装重定向策略。被拒绝的跳转按现有网络错误路径返回，不包含原始凭据。

旧 JSON 配置加载后立即在内存中迁移；后续保存只写入用途明确的新字段，不再写回 `api_key`。

## TDD 验收

### RED

- TokenPlanAdapter：HTTPS→HTTP 重定向目标不得收到 Authorization；跨源 HTTPS 目标也不得收到 Authorization；同源 HTTPS 重定向仍可按策略处理。
- JSON Store：加载 General/Custom 旧 `api_key` 后得到 `ScriptAPIKey`；加载旧 ZenMux `base_url + api_key` 后得到原子 ZenMux 覆盖；`LegacyAPIKey` 被清空。
- 前端：替换值和 clear 标记同时存在时，payload 只包含替换值。
- Handler：四类凭据的冲突请求均返回 400，配置和快照保持不变，不发起查询。

### GREEN

- 只实现使上述测试通过的最小生产改动。
- 运行受影响包测试、race 测试、全量 Go 测试、vet、build、前端测试和前端 build。

## 兼容性与风险

- 原生 Token Plan 适配器如果依赖跨源或协议变化的重定向，将改为明确失败。这是安全边界收紧；固定官方端点不应依赖此行为。
- JSON/SQLite 重复迁移依赖 `MigrateLegacyCredentials` 幂等，现有实现满足该条件，并增加回归测试固定该契约。
- 后端开始拒绝此前含糊的替换+清除请求；正确客户端不受影响，旧前端由本次同步修复。

## 完成标准

- 三个已复现缺陷都有先红后绿的自动化测试。
- 不再能通过重定向把 Authorization 发送到降级或跨源目标。
- JSON Store 和 SQLite Store 的旧凭据加载结果一致。
- 前端不会生成含糊密钥补丁，后端也不会接受该类补丁。
- 工作树只包含本次相关改动，不修改 `review-notes.md` 或 `review-notes_ZH.md`。
