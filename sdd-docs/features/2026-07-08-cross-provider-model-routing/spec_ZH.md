# 跨 Provider 模型路由规格

本地页面：管理后台 Provider 编辑弹窗（`ProviderModal.vue`）/ 代理入口：`:443 /v1/messages` 与 `:443 /api/claude_cli/bootstrap` / 参考源站：`claude-code-src/src/src`（Claude Code 2.1.88 源码） / 技术栈：Go 1.26 标准库 + Vue 3 + 内嵌前端 / 最后更新：2026-07-08 / 进度：0 / 7 planned

## 整体分析（源站分析）

### 目标

让 Claude Code 的 `/model` 菜单出现 mcc 配置的自定义模型选项；用户在某个会话切换后，该会话后续请求走切换后的模型（可跨不同 Provider），其他会话保持默认（走 `ActiveProviderID`）。

### Claude Code 侧的三条关键事实（决定方案可行性）

实现者必须先理解这三条，才能理解为什么本方案成立。

**事实 1：`/model` 切换是会话级内存状态，不落盘。**

`claude-code-src/src/src/bootstrap/state.ts:838-849`：

```ts
export function getMainLoopModelOverride(): ModelSetting | undefined {
  return STATE.mainLoopModelOverride
}
export function setMainLoopModelOverride(model: ModelSetting | undefined) {
  STATE.mainLoopModelOverride = model
}
```

`STATE.mainLoopModelOverride` 是进程内存里的一个变量。`/model` 命令选中某项后调用 `setMainLoopModelOverride(value)`，**只影响当前进程**，不写入 `~/.claude.json`。新开的 Claude Code 会话是独立进程，override 为 `undefined`，回退到默认。

模型选择优先级（`claude-code-src/src/src/utils/model/model.ts:50-78`）：

```
1. mainLoopModelOverride（/model 会话内切换）—— 最高优先
2. --model 启动参数
3. ANTHROPIC_MODEL 环境变量
4. settings.model（全局持久化配置）
5. 内置默认
```

→ **结论：用户需求"其他会话默认使用当前模型"在 Claude Code 客户端侧天然满足，mcc 无需做任何会话识别。** 每个 Claude Code 进程独立维护 override，发出去的请求 `model` 字段即代表该会话当前选择。

**事实 2：Claude Code 启动时从 `/api/claude_cli/bootstrap` 拉取额外模型选项。**

`claude-code-src/src/src/services/api/bootstrap.ts:114-141`：

```ts
export async function fetchBootstrapData(): Promise<void> {
  const response = await fetchBootstrapAPI()
  if (!response) return
  const additionalModelOptions = response.additional_model_options ?? []
  // ...
  saveGlobalConfig(current => ({
    ...current,
    additionalModelOptionsCache: additionalModelOptions,
  }))
}
```

响应 schema（`bootstrap.ts:19-38`）：

```ts
z.object({
  client_data: z.record(z.unknown()).nullish(),
  additional_model_options: z.array(
    z.object({
      model: z.string(),
      name: z.string(),
      description: z.string(),
    }).transform(({ model, name, description }) => ({
      value: model, label: name, description,
    })),
  ).nullish(),
})
```

缓存被追加进 `/model` 菜单（`claude-code-src/src/src/utils/model/modelOptions.ts:480-484`）：

```ts
for (const opt of getGlobalConfig().additionalModelOptionsCache ?? []) {
  if (!options.some(existing => existing.value === opt.value)) {
    options.push(opt)
  }
}
```

→ **结论：mcc 只要控制 `/api/claude_cli/bootstrap` 的响应，就能向 `/model` 菜单注入任意模型选项。** 注入项的 `model` 字段会成为菜单项的 value；用户选中后，`setMainLoopModelOverride(value)` 把它存进内存，此后该会话每个 `/v1/messages` 请求的 `model` 字段就是这个 value。

**事实 3（前提）：mcc 已通过 hosts 劫持让 Claude Code 运行在 firstParty 等价路径上。**

`bootstrap.ts:48-51` 有一个门槛：

```ts
if (getAPIProvider() !== 'firstParty') {
  logForDebugging('[Bootstrap] Skipped: 3P provider')
  return null
}
```

3P provider 模式下 Claude Code 不发 bootstrap 请求。但 mcc 的工作方式是通过 hosts 把 `api.anthropic.com` 指向本地代理，Claude Code 仍以 firstParty 自居并发起 bootstrap 请求，被 mcc 拦截。**证据：mcc 已经实现了 `handleBootstrap`（`internal/proxy/hardcoded.go:408`），说明此路径今天已通。** 本功能复用这条已成立的管道。

### Claude Code 对自定义 model 字符串的接受性（已验证）

- `parseUserSpecifiedModel`（`model.ts:445`）：对 `sonnet`/`opus`/`haiku`/`opusplan` 别名做特殊映射，**对其它任意字符串原样返回**。
- `isModelAllowed`（`modelAllowlist.ts:91-106`）：`availableModels` 未设置时放行所有模型；默认无白名单。
- 菜单去重（`modelOptions.ts:481`）：注入项的 value 若与内置项重复则被忽略。

→ **结论：任意非 `claude-*` 前缀的自定义字符串（如 `glm-4.6`、`kimi-k2`）都能被接受并写进请求。约束：`ExposedModel.ID` 不得使用 `claude-*` 前缀（会与内置项撞名被忽略），不得含 `[1m]` 后缀（会被 `parseUserSpecifiedModel` 按 1M 上下文特殊处理），不得使用 `sonnet`/`opus`/`haiku`/`opusplan` 这些 Claude Code 别名。

### mcc 现状（改动基线）

| 文件 | 现状 | 关键位置 |
|------|------|---------|
| `internal/config/provider.go` | `Provider` 有 `ModelMappings map[string]string`（单 provider 内 client→backend 映射）；`MapModel(model)` 方法；`Validate()` | struct 定义 23-103，`MapModel` 239-247 |
| `internal/config/config.go` | `Config.Providers []Provider` + `ActiveProviderID`；`GetActiveProvider()` 固定返回单一 active；`Validate()` 校验 | 21-60，`GetActiveProvider` 211-230 |
| `internal/config/store.go` | `Store.Save` 用 `json.MarshalIndent(cfg)`，`Load` 用 `json.Unmarshal` —— **JSON 透传，新增字段自动支持** | 31-65 |
| `internal/config/sqlite_store.go` | **不是 JSON 透传**：Provider 字段分散在 `providers` 表与 `provider_model_mappings` 表；新增字段必须显式加 SQLite schema/load/save，否则 SQLite 模式保存后会丢失 | `ensureProviderColumns` 145-162，`loadProviders` 290-346，`saveProviders` 386-432，`upsertProvider` 435-509 |
| `internal/proxy/handler.go` | `ServeHTTP`：`activeProvider := cfg.GetActiveProvider()` → `activeProvider.MapModel(...)` → `transformRequest(body, activeProvider)`；`transformRequest` 内部自己做 `MapModel` + `MultimodalSwitch` override | `GetActiveProvider` 用法 73-91，`transformRequest` 625-695，model 映射 640-649 |
| `internal/proxy/hardcoded.go` | `handleBootstrap` 返回固定空 `additional_model_options`，**不读 config** | 408-414 |
| `internal/admin/provider_handler.go` | **手动枚举字段模式**：`providerResponseMap`、`createProvider.req`+构造、`updateProvider.req`+逐字段更新、`handleProviderDuplicate` 构造；create/update/response/import 透传，duplicate 清空 `ExposedModels` | responseMap 19-47，create 85-195，update 240-381，duplicate 781-806 |
| `internal/frontend/src/components/ProviderModal.vue` | provider 编辑表单；`model_mappings` 用动态数组 + add/remove 按钮（134-147 行） | mappings 状态 195，collect 245-252 |
| `internal/frontend/src/composables/useApi.ts` | Provider TypeScript 类型与 create/update payload 类型手动声明，新增 `exposed_models` 必须同步类型 | `Provider` interface 29-55，`createProvider`/`updateProvider` payload 446 起 |

### 核心设计

**数据模型**：在 `Provider` 上新增 `ExposedModels []ExposedModel`。每个 `ExposedModel` 声明一个对外暴露给 `/model` 菜单的模型：`ID`（全局唯一路由键，成为请求 model 字段）、`Label`、`Description`、`BackendModel`（该 provider 真实模型名）。

**路由层**：`Config` 新增 `ResolveModel(model) (*Provider, string)` 方法，统一完成"选 provider + 算后端模型名"。查找顺序：
1. 扫描所有 **enabled** provider 的 `ExposedModels`，找到 `ID == model` → 返回该 provider + `BackendModel`（空则回退 `ID`）。
2. 未命中 → 返回 `GetActiveProvider()` + `active.MapModel(model)`（向后兼容现有 `ModelMappings`）。
3. 无 active → 返回 `(nil, model)`。

**模型决策优先级**（请求处理路径上，最终写入后端请求体的 model）：
1. `MultimodalSwitch` 触发（provider 开启且请求含图片/PDF/音视频）→ `MultimodalModel`（后端能力约束，最高优先）
2. 路由命中 → `ExposedModel.BackendModel`
3. fallback → `active.MapModel(model)`

**bootstrap 注入**：`handleBootstrap` 改为读 config，收集所有 enabled provider 的 `ExposedModels`，输出为 `additional_model_options: [{model: ID, name: Label, description: 自动拼接}, ...]`。description 自动拼接 provider 名以体现归属：`Description` 非空时为 `"{Description} · {Provider.Name}"`，为空时只用 `Provider.Name`（零配置即可在 `/model` 菜单看到每个模型归属哪个 provider）。

**与现有 `ModelMappings` 的关系（独立、协同、不冲突）**：
- `ModelMappings`：处理 Claude Code **内置会发的**模型名（`claude-opus-4-8` 等），fallback 路径用。key 在 provider 内唯一，**value 允许重复**（多个 `claude-*` 指向同一后端模型合法）。
- `ExposedModels`：处理用户**显式切换的**模型，路由命中路径用。`ID` 跨所有 provider **全局唯一**。
- 唯一性约束只落在 `ExposedModel.ID`，不涉及 `ModelMappings` 的任何字段。

### 风险总结

1. **`ExposedModel.ID` 全局唯一性**：跨 provider 重复会导致路由歧义（`ResolveModel` 命中第一个）。必须在 `Config.Validate()` 校验，保存时返回 400。
2. **disabled provider 的暴露模型**：`ResolveModel` 与 `handleBootstrap` 收集时都只看 `Enabled == true` 的 provider，避免路由到禁用的 provider。
3. **`transformRequest` 重构风险**：现有 `transformRequest` 内部调 `MapModel`；改造后 model 解析上移到 `ResolveModel`，`transformRequest` 改为接收已解析的 `backendModel`。必须保证 multimodal override 语义不变、OpenAI 格式转换路径不受影响。
4. **bootstrap 读 config 失败**：回退到空 `additional_model_options`（与今天行为一致），不阻断 Claude Code 启动。
5. **菜单项与内置项撞名**：`ExposedModel.ID` 用 `claude-*` 会被菜单去重忽略。命名约束写进校验与文档。
6. **SQLite 持久化遗漏**：`Store` 是 JSON 透传，但 `SQLiteStore` 不是；如果不显式持久化 `ExposedModels`，管理端保存后在 SQLite 模式下会丢配置。
7. **复制 Provider 与全局唯一性冲突**：duplicate 若直接复制 `ExposedModels`，新旧 provider 立刻产生重复 ID。复制 provider 时默认清空 `ExposedModels`，由用户手动重新命名后保存。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | Planned | config 层：`ExposedModel` 类型 + `Provider.ExposedModels` + 校验 + `ResolveModel` | `internal/config/provider.go`、`internal/config/config.go` | `go test ./internal/config/...` |
| 2 | Planned | SQLite 持久化：`SQLiteStore` 保存/加载 `ExposedModels` | `internal/config/sqlite_store.go` | SQLite round-trip 单测 |
| 3 | Planned | 代理路由接入：`handler.go` 用 `ResolveModel`，`transformRequest` 改签名 | `internal/proxy/handler.go` | `go test ./internal/proxy/...` |
| 4 | Planned | bootstrap 注入：`handleBootstrap` 读 config 输出 `additional_model_options` | `internal/proxy/hardcoded.go` | bootstrap 单测 |
| 5 | Planned | admin API 透传 `ExposedModels`（create/update/response/import；duplicate 清空） | `internal/admin/provider_handler.go` | `go test ./internal/admin/...` |
| 6 | Planned | 前端 Provider 编辑表单加"对外模型"编辑区 + i18n + API 类型 | `ProviderModal.vue`、`useI18n.ts`、`useApi.ts` | `npm run build` |
| 7 | Planned | 端到端验证 + 回归 | 验证记录 | 手动全链路 + `make test` |

## 需求

### 交付物

1. `ExposedModel` 类型与 `Provider.ExposedModels` 字段，JSON tag 为 `exposed_models,omitempty`。
2. `Config.Validate()` 跨 provider 校验所有 `ExposedModel.ID` 全局唯一（大小写敏感、去空白），重复返回描述性错误。
3. `Provider.Validate()` 校验单个 `ExposedModel`：`ID`/`Label` 非空（trim 后非空）；`ID` 不得以 `claude-` 开头、不得含 `[1m]`，不得等于 `sonnet`/`opus`/`haiku`/`opusplan`；建议仅允许 `[A-Za-z0-9._:-]+`，避免空白和控制字符；`BackendModel` 为空时在 `ResolveModel` 内回退到 `ID`（不强制要求填写）。
4. `Config.ResolveModel(model string) (*Provider, string)` 方法，语义见"核心设计"。
5. `handler.go` 的 `ServeHTTP` 用 `ResolveModel` 替代 `GetActiveProvider` + `MapModel` 两步；`transformRequest` 签名改为 `(body, provider, backendModel)`，移除其内部 `MapModel` 调用，保留 `MultimodalSwitch` override 与格式转换。
6. `handleBootstrap` 读 config，收集 enabled provider 的 `ExposedModels` 生成 `additional_model_options`；description 自动拼接 provider 名（`"{Description} · {Provider.Name}"`，`Description` 空则只用 `Provider.Name`）；读失败回退空数组。
7. `SQLiteStore` 显式持久化 `ExposedModels`；JSON `Store` 无需改动。
8. admin API 的 create/update/response/import 透传 `ExposedModels`；duplicate **不复制** `ExposedModels`（避免全局唯一 ID 冲突）；导出因用 `config.Provider` 直接序列化，自动包含该字段。
9. 前端 `ProviderModal.vue` 新增"对外模型"编辑区（ID/Label/Description/BackendModel 动态表格），同步更新 `useApi.ts` 类型和 `useI18n.ts` 文案。
10. 单元测试覆盖：`ResolveModel` 全分支、跨 provider ID 唯一性校验、SQLite round-trip、`handleBootstrap` 收集逻辑与 schema 字段名、handler 路由集成、admin create/update/import/duplicate 行为。

### 约束

- 向后兼容：未配置 `ExposedModels` 的 provider 行为与今天完全一致（`ResolveModel` 走 fallback）。
- `Store`（JSON 文件）是 JSON 透传，无需额外持久化逻辑；`SQLiteStore` 不是 JSON 透传，必须显式新增持久化字段/编解码逻辑。
- `ExposedModel.ID` 全局唯一性是路由正确性的硬约束。
- 不修改 Claude Code 源码；本功能完全在 mcc 侧实现。

### 边界条件

- 请求 `model` 命中某 disabled provider 的 `ExposedModel.ID`：跳过该 provider，继续找其它 enabled provider 的同 ID；都没有则 fallback。
- `ExposedModel.BackendModel` 为空：`ResolveModel` 用 `ID` 作为后端模型名。
- 同一 provider 内两个 `ExposedModel` 指向相同 `BackendModel`：不报错（无意义但合法）。
- `handleBootstrap` 收集到多个 provider 暴露同 `ID`：去重保留第一个（配置校验已禁止，此处为防御性）。
- `additional_model_options` 为空数组时 Claude Code `/model` 菜单无变化。
- 复制 provider：新 provider 默认 `ExposedModels` 为空，避免与原 provider 产生全局 ID 冲突；用户需要手动新增/改名后保存。

## 任务详情

### 任务 1：config 层——类型、校验、路由解析

#### 需求

**Objective（目标）** — 在 config 层定义 `ExposedModel`、给 `Provider` 加字段、加跨 provider 唯一性校验、实现 `ResolveModel` 路由方法。

**Outcomes（成果）** — `internal/config/provider.go` 和 `internal/config/config.go` 变更；`go test ./internal/config/...` 通过新增测试。

**Evidence（证据）** — `ResolveModel` 单测覆盖命中/fallback/disabled 跳过/BackendModel 空回退/无 active 返 nil；`Config.Validate` 测试覆盖 ID 重复报错。

**Constraints（约束）** — 不破坏现有 `MapModel`、`GetActiveProvider`、`Validate` 语义；新字段 JSON tag 带 `omitempty`。

**Edge Cases（边界）** — `ID` 前后空白；disabled provider 的暴露模型；`BackendModel` 空；无 active provider。

**Verification（验证）** — `go test -v -race ./internal/config/...` 全绿。

#### 计划

**文件 1：`internal/config/provider.go`**

在 `Provider` struct 内（现有 `ModelMappings` 字段之后，`SupportsThinking` 之前）新增字段：

```go
// ExposedModels 对外暴露给 Claude Code /model 菜单的模型列表。
// 用户在 /model 选中某项后，该会话后续请求的 model 字段等于 ExposedModel.ID，
// mcc 据此路由到此 provider 并把 model 替换为 BackendModel。
// ID 跨所有 provider 全局唯一（由 Config.Validate 校验）。
ExposedModels []ExposedModel `json:"exposed_models,omitempty"`
```

在文件末尾（`MapModel` 方法之后）新增类型定义：

```go
// ExposedModel 声明一个对外暴露给 Claude Code /model 菜单的模型。
type ExposedModel struct {
    // ID 是全局唯一的逻辑模型名，同时是 /model 菜单选项的 value。
    // 用户选中后，Claude Code 把它作为请求的 model 字段。
    // 不得以 "claude-" 开头（会与内置菜单项撞名被忽略），不得含 "[1m]"。
    ID string `json:"id"`

    // Label 是 /model 菜单里显示的名称。
    Label string `json:"label"`

    // Description 是 /model 菜单里的描述文案。
    Description string `json:"description"`

    // BackendModel 是该 provider 后端真实模型名。
    // 空字符串表示与 ID 相同。
    BackendModel string `json:"backend_model"`
}
```

在 `Provider.Validate()` 内（现有校验末尾、`return nil` 之前）新增单项校验：

```go
// 校验对外暴露模型
seenExposedIDs := make(map[string]bool)
for i, em := range p.ExposedModels {
    id := strings.TrimSpace(em.ID)
    label := strings.TrimSpace(em.Label)
    if id == "" {
        return fmt.Errorf("exposed_models[%d]: id is required", i)
    }
    if label == "" {
        return fmt.Errorf("exposed_models[%d]: label is required", i)
    }
    if strings.HasPrefix(id, "claude-") {
        return fmt.Errorf("exposed_models[%d]: id must not start with \"claude-\" (conflicts with built-in menu items)", i)
    }
    if strings.Contains(id, "[1m]") {
        return fmt.Errorf("exposed_models[%d]: id must not contain \"[1m]\" (reserved by Claude Code 1M-context handling)", i)
    }
    switch id {
    case "sonnet", "opus", "haiku", "opusplan":
        return fmt.Errorf("exposed_models[%d]: id %q is reserved by Claude Code model aliases", i, id)
    }
    if strings.IndexFunc(id, func(r rune) bool {
        return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == ':' || r == '-')
    }) >= 0 {
        return fmt.Errorf("exposed_models[%d]: id may only contain letters, digits, '.', '_', ':' and '-'", i)
    }
    if seenExposedIDs[id] {
        return fmt.Errorf("exposed_models[%d]: duplicate id %q within provider", i, id)
    }
    seenExposedIDs[id] = true
}
```

注意：
- `provider.go` 当前未 import `strings`，需在 import 块加入 `"strings"`。
- 保存前应规范化 `ExposedModel` 字段：`ID`、`Label`、`Description`、`BackendModel` 都 trim 后落盘；否则 bootstrap 可能输出带空白的 `model` 值，导致 `/model` 选中后的请求值和 `ResolveModel` 比较口径不一致。在 `Provider.Validate()` 的 ExposedModel 校验段**开头**用索引遍历 trim 后回写结构体（校验与后续逻辑都基于 trim 后的值）：

```go
for i := range p.ExposedModels {
    p.ExposedModels[i].ID = strings.TrimSpace(p.ExposedModels[i].ID)
    p.ExposedModels[i].Label = strings.TrimSpace(p.ExposedModels[i].Label)
    p.ExposedModels[i].Description = strings.TrimSpace(p.ExposedModels[i].Description)
    p.ExposedModels[i].BackendModel = strings.TrimSpace(p.ExposedModels[i].BackendModel)
}
```

  注意此 trim 回写必须在 `for i, em := range p.ExposedModels` 校验循环**之前**执行，否则校验读到的是未 trim 的副本。

**文件 2：`internal/config/config.go`**

在 `Validate()` 内（现有 provider 逐个校验之后、`return nil` 之前）新增跨 provider 唯一性校验：

```go
// 校验 ExposedModel.ID 跨 provider 全局唯一
exposedIDs := make(map[string]string) // id -> 首次出现的 provider name
for i := range c.Providers {
    for _, em := range c.Providers[i].ExposedModels {
        id := strings.TrimSpace(em.ID)
        if id == "" {
            continue // 单项空 ID 由 Provider.Validate 捕获
        }
        if firstProvider, exists := exposedIDs[id]; exists {
            return fmt.Errorf("exposed model id %q is duplicated between provider %q and %q",
                id, firstProvider, c.Providers[i].Name)
        }
        exposedIDs[id] = c.Providers[i].Name
    }
}
```

在文件末尾（`GetProviderByID` 之后）新增路由方法：

```go
// ResolveModel 根据请求的 model 字段解析出 provider 和应写入后端请求体的模型名。
//
// 查找顺序：
//  1. 扫描所有 enabled provider 的 ExposedModels，命中 ID 匹配项 → 返回该 provider
//     与其 BackendModel（BackendModel 为空则用 ID）。
//  2. 未命中 → 返回 active provider 与 active.MapModel(model)（向后兼容 ModelMappings）。
//  3. 无 active provider → 返回 (nil, model)。
//
// 调用方需处理 provider == nil 的情况（对应"无可用 provider"错误路径）。
func (c *Config) ResolveModel(model string) (*Provider, string) {
    // 1. 暴露模型命中
    for i := range c.Providers {
        p := &c.Providers[i]
        if !p.Enabled {
            continue
        }
        for _, em := range p.ExposedModels {
            if strings.TrimSpace(em.ID) == model {
                backend := em.BackendModel
                if strings.TrimSpace(backend) == "" {
                    backend = em.ID
                }
                return p, backend
            }
        }
    }
    // 2. fallback：active provider + MapModel
    if active := c.GetActiveProvider(); active != nil {
        return active, active.MapModel(model)
    }
    // 3. 无 active
    return nil, model
}
```

**测试文件：`internal/config/config_test.go`（追加）**

```go
func TestResolveModel_HitExposedModel(t *testing.T) {
    cfg := &Config{
        Providers: []Provider{
            {ID: "a", Name: "A", Enabled: true, ExposedModels: []ExposedModel{
                {ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"},
            }},
        },
    }
    p, backend := cfg.ResolveModel("glm-4.6")
    if p == nil || p.ID != "a" || backend != "glm-4.6" {
        t.Fatalf("expected provider a + glm-4.6, got %v %q", p, backend)
    }
}

func TestResolveModel_BackendModelEmptyFallsBackToID(t *testing.T) {
    cfg := &Config{Providers: []Provider{
        {ID: "a", Name: "A", Enabled: true, ExposedModels: []ExposedModel{
            {ID: "kimi-k2", Label: "Kimi K2"}, // BackendModel 空
        }},
    }}
    p, backend := cfg.ResolveModel("kimi-k2")
    if backend != "kimi-k2" {
        t.Fatalf("expected backend=kimi-k2, got %q", backend)
    }
    if p == nil || p.ID != "a" {
        t.Fatalf("expected provider a, got %v", p)
    }
}

func TestResolveModel_FallbackToActive(t *testing.T) {
    cfg := &Config{
        ActiveProviderID: "a",
        Providers: []Provider{
            {ID: "a", Name: "A", Enabled: true, ModelMappings: map[string]string{
                "claude-opus-4-8": "glm-5.2",
            }},
        },
    }
    p, backend := cfg.ResolveModel("claude-opus-4-8")
    if p == nil || p.ID != "a" {
        t.Fatalf("expected active provider a, got %v", p)
    }
    if backend != "glm-5.2" {
        t.Fatalf("expected mapped glm-5.2, got %q", backend)
    }
}

func TestResolveModel_SkipsDisabledProvider(t *testing.T) {
    cfg := &Config{Providers: []Provider{
        {ID: "disabled", Name: "Disabled", Enabled: false, ExposedModels: []ExposedModel{
            {ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"},
        }},
        {ID: "active", Name: "Active", Enabled: true},
    }}
    cfg.ActiveProviderID = "active"
    // 命中项在 disabled provider，应跳过并 fallback
    p, backend := cfg.ResolveModel("glm-4.6")
    if p == nil || p.ID != "active" {
        t.Fatalf("expected fallback to active, got %v", p)
    }
    // fallback 走 MapModel，无映射则原样
    if backend != "glm-4.6" {
        t.Fatalf("expected original model glm-4.6, got %q", backend)
    }
}

func TestResolveModel_NoActiveReturnsNil(t *testing.T) {
    cfg := &Config{} // 无 provider
    p, backend := cfg.ResolveModel("anything")
    if p != nil {
        t.Fatalf("expected nil provider, got %v", p)
    }
    if backend != "anything" {
        t.Fatalf("expected original model, got %q", backend)
    }
}

func TestValidate_DuplicateExposedModelIDAcrossProviders(t *testing.T) {
    cfg := &Config{Providers: []Provider{
        {ID: "a", Name: "A", Enabled: true, APIURL: "https://a", ExposedModels: []ExposedModel{
            {ID: "glm-4.6", Label: "GLM-4.6"},
        }},
        {ID: "b", Name: "B", Enabled: true, APIURL: "https://b", ExposedModels: []ExposedModel{
            {ID: "glm-4.6", Label: "GLM-4.6"}, // 跨 provider 重复
        }},
    }}
    err := cfg.Validate()
    if err == nil {
        t.Fatal("expected duplicate ID error, got nil")
    }
}
```

**测试文件：`internal/config/provider_test.go`（追加）**

```go
func TestProviderValidate_ExposedModelRejectsClaudePrefix(t *testing.T) {
    p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
        {ID: "claude-opus-4-8", Label: "X"},
    }}
    if err := p.Validate(); err == nil {
        t.Fatal("expected error for claude- prefix ID")
    }
}

func TestProviderValidate_ExposedModelRejects1mSuffix(t *testing.T) {
    p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
        {ID: "glm-4.6[1m]", Label: "X"},
    }}
    if err := p.Validate(); err == nil {
        t.Fatal("expected error for [1m] suffix ID")
    }
}

func TestProviderValidate_ExposedModelDuplicateWithinProvider(t *testing.T) {
    p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
        {ID: "glm-4.6", Label: "X"},
        {ID: "glm-4.6", Label: "Y"},
    }}
    if err := p.Validate(); err == nil {
        t.Fatal("expected duplicate ID error within provider")
    }
}
```

#### 验证

```bash
go test -v -race ./internal/config/...
```

预期：全部通过，包括新增的 9 个测试。

---

### 任务 2：SQLite 持久化——sqlite_store.go

#### 需求

**Objective（目标）** — 让 SQLite 配置存储完整保存/加载 `Provider.ExposedModels`，避免管理端保存后自定义模型配置丢失。

**Outcomes（成果）** — `internal/config/sqlite_store.go` 变更；SQLite round-trip 测试证明 `ExposedModels` 在 Save→Load 后完整保留。

**Evidence（证据）** — 测试覆盖新库建表、旧库自动加列、保存后重新加载、legacy JSON migration 后字段保留。

**Constraints（约束）** — JSON `Store` 不需要改；SQLite 现有 `providers` 表结构保持向后兼容；新增字段默认空数组；损坏 JSON 返回明确错误。

**Edge Cases（边界）** — `nil`/空切片都保存为 `[]`；旧 SQLite 数据库无该列时自动 `ALTER TABLE`；加载空字符串或 `[]` 时得到 nil/空切片均可，但 API 输出必须稳定为 `[]` 或省略，由现有 JSON 行为决定。

**Verification（验证）** — `go test -v -race ./internal/config/...`，重点包含 SQLiteStore round-trip。

#### 计划

**文件：`internal/config/sqlite_store.go`**

新增 `providers.exposed_models` JSON 文本列：

```go
// ensureProviderColumns 的 columns map 中新增：
"exposed_models": `ALTER TABLE providers ADD COLUMN exposed_models TEXT NOT NULL DEFAULT '[]'`,
```

改 `loadProviders()`：

- SQL SELECT 列表加入 `exposed_models`。
- Scan 变量加入 `exposedModels string`。
- Scan 后调用 `decodeExposedModels(exposedModels)`，赋给 `p.ExposedModels`。

改 `upsertProvider()`：

- 在编码 `openAIExtraParams` 和 `quotaQueryConfig` 附近新增：

```go
exposedModels, err := encodeExposedModels(provider.ExposedModels)
if err != nil {
    return err
}
```

- INSERT 列表、VALUES 占位符、ON CONFLICT 更新列表、Exec 参数全部加入 `exposed_models`。

新增编解码 helper：

```go
func encodeExposedModels(models []ExposedModel) (string, error) {
    if len(models) == 0 {
        return "[]", nil
    }
    data, err := json.Marshal(models)
    if err != nil {
        return "", fmt.Errorf("encode exposed_models: %w", err)
    }
    return string(data), nil
}

func decodeExposedModels(value string) ([]ExposedModel, error) {
    if strings.TrimSpace(value) == "" {
        return nil, nil
    }
    var models []ExposedModel
    if err := json.Unmarshal([]byte(value), &models); err != nil {
        return nil, err
    }
    return models, nil
}
```

注意：`sqlite_store.go` 已有 `encoding/json`、`fmt`，但当前没有 `strings`，新增 `decodeExposedModels` 后需要把 `"strings"` 加入 import；不要引入新表，`exposed_models` 是 provider 的嵌套配置，JSON 列足够。

**测试：`internal/config/sqlite_store_test.go`（追加）**

测试文件需要用到 `path/filepath` 与 `reflect`。

```go
func TestSQLiteStoreRoundTripsExposedModels(t *testing.T) {
    path := filepath.Join(t.TempDir(), "config.db")
    store, err := NewSQLiteStore(path, "")
    if err != nil {
        t.Fatalf("NewSQLiteStore: %v", err)
    }
    defer store.Close()

    cfg := DefaultConfig()
    provider := NewProvider("A", "https://a.example/anthropic", "token")
    provider.ExposedModels = []ExposedModel{
        {ID: "glm-4.6", Label: "GLM-4.6", Description: "GLM route", BackendModel: "glm-4.6"},
        {ID: "kimi-k2", Label: "Kimi K2", BackendModel: "moonshot-v1-128k"},
    }
    cfg.Providers = []Provider{*provider}
    cfg.ActiveProviderID = provider.ID

    if err := store.Save(cfg); err != nil {
        t.Fatalf("Save: %v", err)
    }
    loaded, err := store.Load()
    if err != nil {
        t.Fatalf("Load: %v", err)
    }
    got := loaded.GetProviderByID(provider.ID)
    if got == nil {
        t.Fatal("provider missing after load")
    }
    if !reflect.DeepEqual(got.ExposedModels, provider.ExposedModels) {
        t.Fatalf("ExposedModels = %#v, want %#v", got.ExposedModels, provider.ExposedModels)
    }
}
```

再补一个旧库迁移测试：先创建不含 `exposed_models` 列的 SQLiteStore/或手动 drop 无法 drop 时用旧 schema SQL 建库，再 `NewSQLiteStore` 触发 `ensureProviderColumns`，断言 `PRAGMA table_info(providers)` 包含 `exposed_models` 且默认值可 Load。

#### 验证

```bash
go test -v -race ./internal/config/...
```

预期：SQLiteStore 新旧库都能保存/读取 `ExposedModels`。

---

### 任务 3：代理路由接入——handler.go

#### 需求

**Objective（目标）** — 让 `ServeHTTP` 用 `ResolveModel` 选 provider 和后端模型；`transformRequest` 改为接收已解析的 `backendModel`，不再自己 `MapModel`。

**Outcomes（成果）** — `internal/proxy/handler.go` 变更；现有 proxy 测试不回归。

**Evidence（证据）** — 现有 `server_test.go` 全绿；新增"带 ExposedModel.ID 的请求路由到对应 provider"集成测试通过。

**Constraints（约束）** — `MultimodalSwitch` override 语义不变；OpenAI 格式转换路径不变；usage 记录的 `mappedModel` 语义不变。

**Edge Cases（边界）** — `ResolveModel` 返回 nil provider（走现有"No active provider"错误路径）；命中 provider 的 URL/Token 被用于转发。

**Verification（验证）** — `go test -race ./internal/proxy/...`。

#### 计划

**文件：`internal/proxy/handler.go`**

改动点集中在 `ServeHTTP` 中"确定 active provider"到"transformRequest"这一段。

**改动 1（ServeHTTP，约 73-123 行）**：把"加载 active provider → 读 body → metadata → MapModel → transformRequest"调整为"读 body → metadata → ResolveModel → transformRequest"。

原代码（before，约 73-123 行）：

```go
activeProvider := cfg.GetActiveProvider()
var backendURL string
var apiToken string
if activeProvider != nil {
    backendURL = activeProvider.APIURL
    apiToken = activeProvider.APIToken
} else if cfg.BackendURL != "" {
    backendURL = cfg.BackendURL
    apiToken = ""
} else {
    log.Printf("No active provider configured")
    http.Error(w, "No active provider", http.StatusServiceUnavailable)
    return
}

body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize+1))
// ... err 检查、size 检查 ...

modifiedBody := body
metadata := usage.ParseRequestMetadata(body, r.Header)
mappedModel := metadata.OriginalModel
if activeProvider != nil {
    mappedModel = activeProvider.MapModel(metadata.OriginalModel)
    modifiedBody, err = h.transformRequest(body, activeProvider)
    if err != nil {
        log.Printf("Error transforming request: %v", err)
        http.Error(w, "Error transforming request", http.StatusBadRequest)
        return
    } else {
        mappedModel = usage.ParseRequestMetadata(modifiedBody, r.Header).OriginalModel
    }
}
```

新代码（after）：

```go
// 先读 body 再路由：model 字段在 body 内
body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize+1))
if err != nil {
    log.Printf("Error reading request body: %v", err)
    http.Error(w, "Error reading request body", http.StatusBadRequest)
    return
}
r.Body.Close()
if len(body) > maxRequestBodySize {
    log.Printf("Request body too large: %d bytes", len(body))
    http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
    return
}

metadata := usage.ParseRequestMetadata(body, r.Header)

// 按 model 路由：命中暴露模型 → 对应 provider；否则 fallback active
selectedProvider, backendModel := cfg.ResolveModel(metadata.OriginalModel)

var backendURL string
var apiToken string
if selectedProvider != nil {
    backendURL = selectedProvider.APIURL
    apiToken = selectedProvider.APIToken
} else if cfg.BackendURL != "" {
    backendURL = cfg.BackendURL
    apiToken = ""
} else {
    log.Printf("No active provider configured")
    http.Error(w, "No active provider", http.StatusServiceUnavailable)
    return
}

modifiedBody, err := h.transformRequest(body, selectedProvider, backendModel)
if err != nil {
    log.Printf("Error transforming request: %v", err)
    http.Error(w, "Error transforming request", http.StatusBadRequest)
    return
}
mappedModel := usage.ParseRequestMetadata(modifiedBody, r.Header).OriginalModel
```

注意：
- `mappedModel` 从 `modifiedBody` 重新解析（保留原语义：反映 transform 后实际发往后端的 model）。
- 原代码 `else if cfg.BackendURL != ""` 分支保留，用于向后兼容无 provider 只有 `BackendURL` 的旧配置。
- 原代码中 `activeProvider` 变量被后续多处引用（`providerAPIFormat(activeProvider)`、`h.newUsageRequest(r, activeProvider, ...)`、`providerLogFields(activeProvider, ...)`、限流/重试分支）。**这些引用全部改为 `selectedProvider`**。实现时全文搜索 `activeProvider` 确认无遗漏（`ServeHTTP` 内）。

**改动 2（`transformRequest`，约 625-695 行）**：签名加 `backendModel string` 参数；移除内部 `provider.MapModel(model)` 调用，改为直接用传入的 `backendModel` 作为基础，再叠加 `MultimodalSwitch` override。

原签名与 model 映射段（before）：

```go
func (h *Handler) transformRequest(body []byte, provider *config.Provider) ([]byte, error) {
    var req map[string]any
    if err := json.Unmarshal(body, &req); err != nil {
        return body, nil
    }
    changed := false
    if providerAPIFormat(provider) == config.APIFormatAnthropic && provider.StripUnknownContentBlocks {
        if proactiveCleanUnknownContentTypes(req); cleanChanged { ... }
    }
    // 模型映射
    if model, ok := req["model"].(string); ok {
        mapped := provider.MapModel(model)
        if provider.MultimodalSwitch && provider.MultimodalModel != "" && requestContainsNonTextContent(req) {
            mapped = provider.MultimodalModel
        }
        if mapped != model {
            req["model"] = mapped
            changed = true
        }
    }
    // ... thinking 剥离、OpenAI 格式转换 ...
}
```

新签名与 model 映射段（after）：

```go
// transformRequest 转换请求体。
// backendModel 是由 Config.ResolveModel 解析出的、应写入后端请求体的模型名
// （暴露模型命中 → BackendModel；fallback → active.MapModel 结果）。
// MultimodalSwitch 触发时覆盖为 MultimodalModel。
func (h *Handler) transformRequest(body []byte, provider *config.Provider, backendModel string) ([]byte, error) {
    var req map[string]any
    if err := json.Unmarshal(body, &req); err != nil {
        return body, nil
    }
    changed := false
    if providerAPIFormat(provider) == config.APIFormatAnthropic && provider.StripUnknownContentBlocks {
        if proactiveCleanUnknownContentTypes(req) {
            changed = true
        }
    }
    // 模型替换：以 ResolveModel 的结果为基础，叠加多模态 override
    if model, ok := req["model"].(string); ok {
        finalModel := backendModel
        if provider.MultimodalSwitch && provider.MultimodalModel != "" && requestContainsNonTextContent(req) {
            finalModel = provider.MultimodalModel
        }
        if finalModel != model {
            req["model"] = finalModel
            changed = true
        }
    }
    // ... thinking 剥离、OpenAI 格式转换逻辑不变 ...
}
```

`provider` 可能为 nil（`cfg.BackendURL` 兼容分支）——但此分支下 `transformRequest` 原本也不会被以 nil 调用的方式触发问题，因为 `providerAPIFormat(nil)` 返回 `APIFormatAnthropic`、`provider.MapModel` 原本会 nil panic 但实际上原代码在 `activeProvider != nil` 时才调 `transformRequest`。**新代码在 `selectedProvider == nil && cfg.BackendURL != ""` 分支也会调 `transformRequest(body, nil, backendModel)`**。需保证 `transformRequest` 对 nil provider 安全：

- `providerAPIFormat(nil)` 已安全（返回默认 Anthropic）。
- `provider.StripUnknownContentBlocks`、`provider.MultimodalSwitch`、`provider.SupportsThinking` 对 nil 会 panic。

**处理方式**：在 `transformRequest` 开头加 nil 守卫，或在 `ServeHTTP` 的 `BackendURL` 兼容分支跳过 transform。推荐前者（防御性）：

```go
func (h *Handler) transformRequest(body []byte, provider *config.Provider, backendModel string) ([]byte, error) {
    if provider == nil {
        return body, nil // 无 provider（BackendURL 兼容模式），不转换
    }
    // ... 其余逻辑 ...
}
```

**现有测试同步要求**：`server_test.go` 中有多处直接调用 `handler.transformRequest(body, provider)`。改签名后必须全部补第三参：
- 普通模型映射测试：传 `provider.MapModel(originalModel)`，例如 original=`claude-sonnet-4-5` 时传 `provider.MapModel("claude-sonnet-4-5")`。
- 不关心模型映射、只测 thinking/清洗/格式转换的测试：传请求体里的原始 model，或先解析后传同值。
- 新增暴露模型路径测试：传 `ResolveModel` 得到的 `backendModel`，验证 `ExposedModel.BackendModel` 能成为最终请求 model。
- 多模态测试：第三参传基础 backend model，仍断言 `MultimodalSwitch` 覆盖为 `MultimodalModel`。

**测试：`internal/proxy/server_test.go`（追加集成测试）**

```go
func TestServeHTTP_RoutesByExposedModelID(t *testing.T) {
    // 两个 provider，各暴露一个模型；active 是 A
    store := config.NewMockStore(&config.Config{
        ActiveProviderID: "a",
        Providers: []config.Provider{
            {ID: "a", Name: "A", Enabled: true, APIURL: upstreamA.URL, APIToken: "ta",
                ExposedModels: []config.ExposedModel{{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"}}},
            {ID: "b", Name: "B", Enabled: true, APIURL: upstreamB.URL, APIToken: "tb",
                ExposedModels: []config.ExposedModel{{ID: "kimi-k2", Label: "Kimi K2", BackendModel: "kimi-k2"}}},
        },
    })
    h := NewHandler(store, transport)
    // 发 model=kimi-k2 的请求，应路由到 upstreamB，body 的 model 被替换为 kimi-k2
    req := newMessagesRequest("kimi-k2")
    rec := serve(h, req)
    // 断言 upstreamB 收到请求、upstreamA 未收到；upstreamB 收到的 body model == "kimi-k2"
}
```

（具体 upstream mock 构造参照 `server_test.go` 现有 `httptest.Server` 用法。）

#### 验证

```bash
go build ./...
go test -race ./internal/proxy/...
```

预期：现有测试全绿（语义等价改造）+ 新增路由测试通过。

---

### 任务 4：bootstrap 注入——hardcoded.go

#### 需求

**Objective（目标）** — `handleBootstrap` 读 config，把 enabled provider 的 `ExposedModels` 输出为 `additional_model_options`。

**Outcomes（成果）** — `internal/proxy/hardcoded.go` 变更；bootstrap 单测验证字段名与收集逻辑。

**Evidence（证据）** — 测试构造带 `ExposedModels` 的 config，断言响应 `additional_model_options` 含正确条目、字段名为 `model`/`name`/`description`、跨 provider 去重、disabled provider 不出现。

**Constraints（约束）** — 读 config 失败回退空数组；字段顺序稳定（按 provider 顺序 + `ExposedModels` 顺序）；不泄露 token 等敏感信息。

**Edge Cases（边界）** — config 为 nil；Load 出错；两个 provider 暴露同 ID（防御性去重）。

**Verification（验证）** — `go test -run TestHandleBootstrap ./internal/proxy/...`。

#### 计划

**文件：`internal/proxy/hardcoded.go`**

`handleBootstrap` 是 `Handler` 的方法，`Handler` 已有 `configStore` 字段（见 `handler.go:27`）。改造为读 config：

```go
// handleBootstrap 处理启动引导配置。
// 源码期望 client_data + additional_model_options + cwk_cfg_key。
// additional_model_options 收集所有 enabled provider 的 ExposedModels，
// 让 Claude Code /model 菜单出现 mcc 配置的自定义模型。
func (h *Handler) handleBootstrap(w http.ResponseWriter) {
    writeJSONResponse(w, http.StatusOK, map[string]any{
        "client_data":              map[string]any{},
        "additional_model_options": h.collectAdditionalModelOptions(),
        "cwk_cfg_key":              nil,
    })
}

// collectAdditionalModelOptions 收集 enabled provider 的 ExposedModels，
// 生成 Claude Code bootstrap 响应所需的 additional_model_options 数组。
// 读 config 失败或无数据时返回空数组（保持与历史行为一致）。
func (h *Handler) collectAdditionalModelOptions() []map[string]string {
    if h.configStore == nil {
        return []map[string]string{}
    }
    cfg, err := h.configStore.Load()
    if err != nil || cfg == nil {
        return []map[string]string{}
    }
    opts := make([]map[string]string, 0)
    seen := make(map[string]bool)
    for i := range cfg.Providers {
        p := &cfg.Providers[i]
        if !p.Enabled {
            continue
        }
        for _, em := range p.ExposedModels {
            id := strings.TrimSpace(em.ID)
            if id == "" || seen[id] {
                continue // 单项空 ID 由校验保证；此处防御性去重
            }
            seen[id] = true
            // 自动把 provider 名附到 description，让 /model 菜单体现模型归属（零配置）
            desc := strings.TrimSpace(em.Description)
            if providerName := strings.TrimSpace(p.Name); providerName != "" {
                if desc == "" {
                    desc = providerName
                } else {
                    desc = desc + " · " + providerName
                }
            }
            opts = append(opts, map[string]string{
                "model":       id,
                "name":        strings.TrimSpace(em.Label),
                "description": desc,
            })
        }
    }
    return opts
}
```

注意：`hardcoded.go` 当前未 import `strings`，需加入 import。

**测试：`internal/proxy/hardcoded_test.go`**

现有 `TestHandleBootstrap`（212 行）断言三个字段存在；需扩展。更新/追加：

```go
func TestHandleBootstrap_EmitsExposedModels(t *testing.T) {
    store := config.NewMockStore(&config.Config{
        Providers: []config.Provider{
            {ID: "a", Name: "智谱", Enabled: true, ExposedModels: []config.ExposedModel{
                {ID: "glm-4.6", Label: "GLM-4.6", Description: "日常编码", BackendModel: "glm-4.6"},
            }},
            {ID: "b", Name: "B", Enabled: false, ExposedModels: []config.ExposedModel{
                {ID: "disabled-model", Label: "Disabled"},
            }},
        },
    })
    handler := &Handler{configStore: store}
    rec := httptest.NewRecorder()
    handler.handleBootstrap(rec)

    var resp struct {
        ClientData           any                     `json:"client_data"`
        AdditionalModelOptions []map[string]string   `json:"additional_model_options"`
        CwkCfgKey            any                     `json:"cwk_cfg_key"`
    }
    if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
        t.Fatalf("invalid json: %v", err)
    }
    if len(resp.AdditionalModelOptions) != 1 {
        t.Fatalf("expected 1 option (disabled provider excluded), got %d: %v",
            len(resp.AdditionalModelOptions), resp.AdditionalModelOptions)
    }
    opt := resp.AdditionalModelOptions[0]
    if opt["model"] != "glm-4.6" || opt["name"] != "GLM-4.6" || opt["description"] != "日常编码 · 智谱" {
        t.Fatalf("unexpected option fields (description should auto-append provider name): %v", opt)
    }
}

func TestHandleBootstrap_DescriptionEmptyUsesProviderName(t *testing.T) {
    store := config.NewMockStore(&config.Config{
        Providers: []config.Provider{
            {ID: "a", Name: "月之暗面", Enabled: true, ExposedModels: []config.ExposedModel{
                {ID: "kimi-k2", Label: "Kimi K2", Description: ""}, // description 空
            }},
        },
    })
    handler := &Handler{configStore: store}
    rec := httptest.NewRecorder()
    handler.handleBootstrap(rec)

    var resp struct {
        AdditionalModelOptions []map[string]string `json:"additional_model_options"`
    }
    if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
        t.Fatalf("invalid json: %v", err)
    }
    if len(resp.AdditionalModelOptions) != 1 {
        t.Fatalf("expected 1 option, got %d", len(resp.AdditionalModelOptions))
    }
    // description 为空时只用 provider 名
    if resp.AdditionalModelOptions[0]["description"] != "月之暗面" {
        t.Fatalf("expected description=月之暗面, got %q", resp.AdditionalModelOptions[0]["description"])
    }
}

func TestHandleBootstrap_NoConfigReturnsEmpty(t *testing.T) {
    handler := &Handler{configStore: nil} // 无 store
    rec := httptest.NewRecorder()
    handler.handleBootstrap(rec)
    var resp map[string]any
    json.Unmarshal(rec.Body.Bytes(), &resp)
    arr, ok := resp["additional_model_options"].([]any)
    if !ok || len(arr) != 0 {
        t.Fatalf("expected empty array, got %v", resp["additional_model_options"])
    }
}
```

#### 验证

```bash
go test -v -run TestHandleBootstrap -race ./internal/proxy/...
```

预期：两个测试通过。

---

### 任务 5：admin API 透传 ExposedModels

#### 需求

**Objective（目标）** — 在 `provider_handler.go` 的手动枚举位置透传 `ExposedModels`，使前端可读写；复制 provider 时清空该字段，避免全局唯一 ID 冲突。

**Outcomes（成果）** — `internal/admin/provider_handler.go` 变更；admin 测试覆盖 create/update 带 `ExposedModels`。

**Evidence（证据）** — 测试：POST 创建带 `exposed_models` 的 provider → 响应含该字段；PUT 更新该字段 → 再次 GET 反映更新；duplicate 后新 provider 的 `exposed_models` 为空；导入重复 ID 时全局校验拒绝。

**Constraints（约束）** — update 用指针类型（`*[]config.ExposedModel`）做可选更新，与现有可选字段模式一致；校验复用 `Provider.Validate()`（任务 1 已含 ExposedModel 校验）+ `Config.Validate()` 跨 provider 唯一性；duplicate 不复制 `ExposedModels`。

**Edge Cases（边界）** — update 传 `null` 与传空数组 `[]` 的区别：传 `null`（字段省略）不变，传 `[]` 清空。`*[]T` 为 nil 时表示"不更新"，非 nil 时（含空切片）表示"替换为该值"。

**Verification（验证）** — `go test -race ./internal/admin/...`。

#### 计划

**文件：`internal/admin/provider_handler.go`**，关键改动：

**改动 1：`providerResponseMap`（19-47 行）**，在 `"model_mappings"` 之后加：

```go
"exposed_models": p.ExposedModels,
```

**改动 2：`createProvider` 的 req struct（85-105 行）**，加字段：

```go
ExposedModels []config.ExposedModel `json:"exposed_models"`
```

并在 provider 构造（147-171 行）加：

```go
ExposedModels: req.ExposedModels,
```

**改动 3：`updateProvider` 的 req struct（240-261 行）**，加可选字段（指针）：

```go
ExposedModels *[]config.ExposedModel `json:"exposed_models"`
```

并在逐字段更新段（283-352 行，`ModelMappings` 之后）加：

```go
if req.ExposedModels != nil {
    provider.ExposedModels = *req.ExposedModels
}
```

**改动 4：`handleProviderDuplicate` 的 newProvider 构造（781-806 行）**：

```go
// 不复制 ExposedModels：ID 需要全局唯一，直接复制会让新旧 provider 冲突。
ExposedModels: nil,
```

也可以省略该字段，让零值 nil 生效；但测试必须明确 duplicate 后不带任何暴露模型。

**改动 5（校验升级）**：`createProvider` 和 `updateProvider` 现有调用是 `provider.Validate()`（单 provider 校验）。**跨 provider ID 唯一性需要 `cfg.Validate()`**。在 create/update/import/duplicate 四条会写入 config 的路径，在 `Save(cfg)` 前都跑 `cfg.Validate()`（或直接把 create/update 的 `provider.Validate()` 替换为 `cfg.Validate()`——后者内部会调每个 provider 的 `Validate`）。

推荐：在每个 `s.configStore.Save(cfg)` 之前加：

```go
if err := cfg.Validate(); err != nil {
    jsonErr, _ := json.Marshal(map[string]string{"error": err.Error()})
    http.Error(w, string(jsonErr), http.StatusBadRequest)
    return
}
```

保留现有 `provider.Validate()` 或删除均可（`cfg.Validate()` 已覆盖）。为减少重复，可删除 `provider.Validate()` 单独调用，改用 `cfg.Validate()`。**导入路径同样适用**：`handleImportProviders` 当前用 `cp.Validate()`（937 行），需在合并完成后、Save 前加 `cfg.Validate()` 全局校验。duplicate 虽然清空了 `ExposedModels`，仍应 Save 前跑 `cfg.Validate()`，防止复制时带入其它非法配置。

**测试：`internal/admin/config_handler_test.go` 或 `provider_handler_test.go`（追加）**

```go
func TestCreateProvider_WithExposedModels(t *testing.T) {
    store := config.NewMockStore(config.DefaultConfig())
    s := newTestServer(store) // 参照现有测试构造
    body := `{"name":"A","api_url":"https://a","exposed_models":[
        {"id":"glm-4.6","label":"GLM-4.6","description":"d","backend_model":"glm-4.6"}]}`
    req := httptest.NewRequest("POST", "/api/providers", strings.NewReader(body))
    rec := httptest.NewRecorder()
    s.handleProviders(rec, req)
    if rec.Code != 200 {
        t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
    }
    // 验证响应与 store 都含 exposed_models
}

func TestUpdateProvider_RejectsDuplicateExposedID(t *testing.T) {
    // provider A 已有 glm-4.6；给 provider B 也加 glm-4.6 → 400
}

func TestDuplicateProviderClearsExposedModels(t *testing.T) {
    // 原 provider 有 ExposedModels；duplicate 后新 provider.ExposedModels 必须为空，
    // 否则 cfg.Validate 会因全局重复 ID 失败，或者留下不可保存配置。
}
```

#### 验证

```bash
go test -race ./internal/admin/...
```

预期：全绿，含新增测试。

---

### 任务 6：前端 Provider 编辑表单

#### 需求

**Objective（目标）** — `ProviderModal.vue` 新增"对外模型"编辑区，复用 `model_mappings` 的动态数组交互模式。

**Outcomes（成果）** — `ProviderModal.vue`、`internal/frontend/src/composables/useI18n.ts`、`internal/frontend/src/composables/useApi.ts` 变更；`npm run build` 通过。

**Evidence（证据）** — 前端构建无错；表单能增删 `ExposedModel` 行并随保存提交。

**Constraints（约束）** — 四列：ID / Label / Description / BackendModel；空行不提交；i18n 文案中英双语；移动端不能挤压重叠；TypeScript API 类型必须同步。

**Edge Cases（边界）** — 用户填了部分字段就保存（后端校验拒绝，前端应预校验非空提示）；编辑现有 provider 时回填。

**Verification（验证）** — `npm --prefix internal/frontend run build`；手动检查表单。

#### 计划

**文件 1：`internal/frontend/src/components/ProviderModal.vue`**

参照现有 `model_mappings` 实现（134-147 行模板、195 行状态、245-252 行收集）。在 `model_mappings` 编辑区之后新增"对外模型"编辑区：

模板（参照 134-147 行的 `v-for` + add/remove 按钮结构）。不要只用 `pop()` 删除最后一行，必须支持删除当前行；移动端用响应式布局避免四列挤压：

```vue
<label class="block text-[13px] font-semibold mb-2">{{ t('modal.exposed_models') }}</label>
<div class="space-y-2">
  <div v-for="(em, i) in exposedModels" :key="i" class="grid grid-cols-1 md:grid-cols-[1fr_1fr_1.2fr_1fr_auto] gap-2">
    <input v-model="em.id" :placeholder="t('modal.exposed_model_id')" class="..." />
    <input v-model="em.label" :placeholder="t('modal.exposed_model_label')" class="..." />
    <input v-model="em.description" :placeholder="t('modal.exposed_model_desc')" class="..." />
    <input v-model="em.backend_model" :placeholder="t('modal.exposed_model_backend')" class="..." />
    <button type="button" @click="exposedModels.splice(i, 1)">X</button>
  </div>
</div>
<button type="button" @click="exposedModels.push({ id: '', label: '', description: '', backend_model: '' })">
  {{ t('modal.add_exposed_model') }}
</button>
```

状态（参照 195 行 `mappings` 的 ref 初始化）：

```ts
const exposedModels = ref<{ id: string; label: string; description: string; backend_model: string }[]>([])

// 初始化（参照 245 行 collectMappings 之前的 entries 回填）
if (props.provider?.exposed_models?.length) {
  exposedModels.value = props.provider.exposed_models.map(em => ({ ...em }))
}
```

收集提交（参照 283 行 `collectMappings()` 的调用处，在提交 payload 里加 `exposed_models`）：

```ts
function collectExposedModels() {
  const rows = exposedModels.value.map(em => ({
    id: em.id.trim(),
    label: em.label.trim(),
    description: em.description.trim(),
    backend_model: em.backend_model.trim(),
  }))
  const partial = rows.find(em => !isEmptyExposedModel(em) && (!em.id || !em.label))
  if (partial) {
    return { ok: false as const, error: t('modal.exposed_model_required') }
  }
  return { ok: true as const, value: rows.filter(em => !isEmptyExposedModel(em)) }
}

function isEmptyExposedModel(em: { id: string; label: string; description: string; backend_model: string }) {
  return !em.id && !em.label && !em.description && !em.backend_model
}
```

在 `save()` 内先调用 `collectExposedModels()`；失败则显示错误并停止提交；成功时用其 `value`：

```ts
const collected = collectExposedModels()
if (!collected.ok) {
  // 显示 collected.error（例如"对外模型需填写 ID 和显示名"），停止提交
  formError.value = collected.error
  return
}
// 在提交对象里加（注意是 collected.value，不是 exposed.value）：
exposed_models: collected.value,
```

**文件 2：`internal/frontend/src/composables/useApi.ts`**

新增类型并同步 Provider/create/update payload：

```ts
export interface ExposedModel {
  id: string
  label: string
  description: string
  backend_model: string
}
```

在 `Provider` interface 中加入：

```ts
exposed_models?: ExposedModel[]
```

在 `createProvider` 和 `updateProvider` 的 `data` 类型中加入：

```ts
exposed_models?: ExposedModel[]
```

否则 `ProviderModal.vue` 的 `props.provider.exposed_models` 和提交 payload 会在 `npm run build` 时出现类型错误。

**文件 3：`internal/frontend/src/composables/useI18n.ts`**

在 i18n 字典的 `modal` 命名空间下加（中英各一份，参照现有 `add_mapping` 等条目位置）：

```
exposed_models: '对外模型 / Exposed Models'
add_exposed_model: '添加模型 / Add Model'
remove_exposed_model: '移除 / Remove'
exposed_model_id: 'ID（菜单值）'
exposed_model_label: '显示名'
exposed_model_desc: '描述'
exposed_model_backend: '后端模型名（空=同ID）'
exposed_model_required: '对外模型需填写 ID 和显示名'
```

（实际按 `useI18n.ts` 现有的中英分离结构填写两条。）

#### 验证

```bash
npm --prefix internal/frontend run build
```

预期：构建成功，`internal/frontend/dist` 更新。

---

### 任务 7：端到端验证 + 回归

#### 需求

**Objective（目标）** — 全链路验证：配置 ExposedModel → 重启/刷新 Claude Code → `/model` 出现选项 → 切换后请求路由正确 → 其他会话不受影响。

**Outcomes（成果）** — 验证记录写入本文件"验证"小节；`make test` 全绿。

**Evidence（证据）** — 手动验证截图/日志；`go test ./...` 输出。

**Constraints（约束）** — 不破坏现有"单 active provider + ModelMappings"用户的体验。

**Edge Cases（边界）** — 切回"Default"菜单项 → override 清空 → 回到 active provider；命中 provider 不可达 → 现有 502 路径。

**Verification（验证）** — 见下。

#### 计划

1. 全量测试：`make test`（等价 `go test -v -race -coverprofile=coverage.out ./...`）。
2. 前端构建：`npm --prefix internal/frontend run build`。
3. 手动全链路（需真实 mcc 实例 + Claude Code）：
   - 在管理后台给 provider A 加一个 ExposedModel `{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"}`，保存。
   - 给 provider B（不同后端）加 `{ID: "kimi-k2", Label: "Kimi K2", BackendModel: "kimi-k2"}`，保存。
   - 启动/重启 mcc，新开 Claude Code 会话，`/model` 菜单应出现 GLM-4.6 与 Kimi K2。
   - 会话 1 切到 GLM-4.6 → 该会话请求 model 字段为 `glm-4.6`，mcc 日志显示路由到 provider A、后端 model=`glm-4.6`。
   - 新开会话 2（不切换）→ 请求走 active provider（默认行为不变）。
   - 会话 1 切回 Default → 回到 active provider。
4. 回归检查：删除所有 ExposedModels 配置 → 行为与发布前完全一致（`/model` 菜单无自定义项）。

#### 验证

```bash
make test
npm --prefix internal/frontend run build
```

实现完成后在此回填实际输出摘要与手动验证结论。

---

## 状态

`draft` → 待用户确认后转 `approved` → `planned`。
