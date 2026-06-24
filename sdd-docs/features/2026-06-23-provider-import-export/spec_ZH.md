# 供应商导入导出规格

本地页面：管理后台 → 供应商管理页
代理入口：`internal/admin/provider_handler.go`、`internal/admin/server.go`、`internal/frontend/src/views/DashboardView.vue`、`internal/frontend/src/components/ProviderCard.vue`、`internal/frontend/src/composables/useApi.ts`、`internal/frontend/src/composables/useI18n.ts`
参考源站：`sdd-docs/features/2026-06-13-auto-update/spec.md`（spec 模板与 status/config 暴露模式）、`internal/admin/provider_handler.go`（现有供应商 CRUD 模式）、`internal/config/provider.go`（Provider 结构体）
技术栈：Go 1.26 标准库（`net/http`、`encoding/json`）+ Vue 3 + 内嵌前端
最后更新：2026-06-23
进度：5 / 5 已完成

## 整体分析（源站分析）

### 当前项目状态

管理后台的供应商页支持创建、编辑、测试、激活、禁用、复制、删除供应商配置。每个供应商存储约 20 个字段：API 地址、**API Token（明文）**、API 格式、模型映射、限流队列配置、重试配置、多模态切换、内容块清洗等。

拥有多台主机的用户目前必须在每台主机上手重建所有供应商。本功能新增基于 JSON 的导入导出，使供应商集合可在几秒内跨主机迁移或同步。

### 为什么导出必须包含真实 Token

现有 `GET /api/providers` 接口对 token 做掩码处理（`api_token_mask`，如 `sk-...XXXX`）。这对展示是正确的，但不含真实 token 的导出对跨主机迁移毫无意义——每个导入的供应商都要手动重新输入 token。因此导出接口返回**真实 `api_token`**，UI 必须警告用户下载的文件包含密钥。

### 冲突处理策略

导入时，文件中的供应商可能与已存在的供应商冲突。冲突按 `id` 检测。三种策略：

| 策略 | ID 冲突时的行为 |
| --- | --- |
| `skip`（默认） | 保持现有供应商不变，不导入冲突项。 |
| `overwrite` | 用导入数据替换现有供应商的字段（同 ID）。 |
| `duplicate` | 忽略导入的 ID，生成新 ID，作为新条目追加。 |

名称冲突（同名不同 ID）**不**视为冲突——允许重名，与现有创建流程一致。

### 应用前预览

前端在客户端计算预览：将导入文件的供应商 ID 与当前供应商列表（已通过 `GET /api/providers` 加载）对比。预览显示**新增**（ID 不存在）和**冲突**（ID 已存在）各多少个。用户选择冲突策略后确认。这避免了后端两段式 API。

### 路由关注点

`/api/providers/` 注册为子树处理器（`handleProviderRoutes`），将路径后缀当作供应商 ID。新增的 `/api/providers/export` 和 `/api/providers/import` 必须注册为**精确模式**（类似现有 `/api/providers/test`），以便 Go `ServeMux` 的最长前缀匹配优先于子树处理器路由。

## 开发检查清单

| 顺序 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成  | 导出 API 端点 | `internal/admin/provider_handler.go`、`server.go` | Handler 测试：选中 ID → 含真实 token 的 JSON |
| 2 | 已完成  | 导入 API 端点 | `internal/admin/provider_handler.go`、`server.go` | Handler 测试：skip / overwrite / duplicate 三种策略 |
| 3 | 已完成  | 前端选择（每行复选框） | `ProviderCard.vue`、`DashboardView.vue` | 组件测试：复选框切换选中状态 |
| 4 | 已完成  | 前端工具栏 + 导出/导入流程 | `DashboardView.vue`、`useApi.ts`、`useI18n.ts` | 前端构建 + 导出下载与导入预览组件测试 |
| 5 | 已完成  | i18n 与边界情况打磨 | `useI18n.ts` | zh/en 文案完整；空选择与解析错误处理 |

## 需求

### 交付物

1. `POST /api/providers/export` — 接收 `{"ids": ["id1", "id2", ...]}`，返回 JSON 文件，含 `version`、`exported_at` 和 `providers` 数组，其中是完整的 `config.Provider` 对象**包含真实 `api_token`**。空 `ids` 数组导出空结果（前端发送显式选中的 ID；"全选"是前端逻辑）。
2. `POST /api/providers/import` — 接收 `{"providers": [...], "strategy": "skip"|"overwrite"|"duplicate"}`，按策略应用导入，返回摘要 `{"imported": N, "skipped": N, "overwritten": N, "duplicated": N}`。
3. 导出接口验证每个请求的 ID 是否存在；未知 ID 静默跳过（防御性——前端只发送已知 ID，但后端不能因陈旧选择返回 500）。
4. 导入接口对每个导入的供应商调用 `Provider.Validate()`；无效供应商被跳过，错误计入摘要（不因一条坏数据中断整个导入）。
5. `ProviderCard.vue` 在**左上角**（供应商名称左侧）新增复选框。点击切换卡片选中状态，不触发编辑/删除等操作。
6. 供应商页工具栏在现有"添加供应商"按钮**右侧**新增**导出**和**导入**按钮。未选中任何供应商时导出禁用。导入打开文件选择器。
7. 导出流程：收集选中 ID → `POST /api/providers/export` → 触发浏览器文件下载（`providers-export-YYYYMMDD-HHMMSS.json`）。
8. 导入流程：用户选择 JSON 文件 → 前端客户端读取 → 显示**预览弹窗**（新增/冲突计数 + 冲突策略选择器，默认 `skip`）→ 确认后 `POST /api/providers/import`（带文件 providers + 策略）→ 刷新列表。
9. zh/en i18n 覆盖所有新 label、按钮文案、弹窗文案、预览说明、冲突策略选项和 token 安全警告。
10. 导出下载为 JSON 文件；导入仅接受 `.json` 文件，对格式错误的 JSON 或 schema 不对（缺 `version`/`providers`）显示明确错误。

### 数据模型

**导出文件格式：**

```json
{
  "version": 1,
  "exported_at": "2026-06-23T12:00:00Z",
  "providers": [
    {
      "id": "abc123",
      "name": "GLM",
      "api_url": "https://open.bigmodel.cn/api/anthropic",
      "api_token": "sk-real-token-here",
      "api_format": "anthropic",
      "openai_extra_params": {},
      "claude_code_compat_hint": null,
      "model_mappings": {"claude-sonnet-4": "glm-5"},
      "supports_thinking": false,
      "multimodal_switch": false,
      "multimodal_model": "",
      "strip_unknown_content_blocks": false,
      "rate_limit_queue_enabled": false,
      "max_concurrent_requests": 0,
      "max_queue_size": 0,
      "queue_timeout_ms": 0,
      "retry_429_enabled": false,
      "retry_429_max_attempts": 0,
      "retry_429_initial_delay_ms": 0,
      "retry_429_max_delay_ms": 0,
      "enabled": true,
      "created_at": "2026-06-20T10:00:00Z",
      "updated_at": "2026-06-23T09:00:00Z"
    }
  ]
}
```

`version` 字段为 `1`。未来格式变更递增此数字；导入器拒绝未知版本并返回明确错误。

**导入请求格式：**

```json
{
  "providers": [ /* 同导出文件的 providers */ ],
  "strategy": "skip"
}
```

**导入响应格式：**

```json
{
  "success": true,
  "imported": 3,
  "skipped": 1,
  "overwritten": 0,
  "duplicated": 0,
  "errors": []
}
```

### 约束

1. 导出接口返回真实 `api_token`——这是跨主机迁移的有意设计。前端必须在导出前后显示安全警告。接口与其他供应商路由一样位于 `authMiddlewareFunc` 之后。
2. 冲突检测**仅**按供应商 `id`。允许名称冲突（两个供应商可同名）。
3. `duplicate` 策略通过 `generateProviderID()` 生成新 ID，并将 `created_at`/`updated_at` 重置为 `time.Now()`。
4. `overwrite` 策略保留现有供应商的 `created_at`，但将 `updated_at` 更新为 `time.Now()`。
5. 导入接口不能"部分应用后失败"——整个导入在一次 `Load → 合并 → Save` 周期内完成。若 `Save` 失败，不更改任何供应商（丢弃重新加载的 config）。
6. 导出请求中的未知供应商 ID 静默跳过（不算错误）。
7. 导入请求中的无效供应商（`Provider.Validate()` 失败）被跳过并计入 `errors` 数组；导入继续处理其余条目。
8. 导入器拒绝 `version` 非 `1` 的值，返回明确错误。
9. 无新增外部依赖；JSON 处理继续用 `encoding/json`。
10. `claude_code_compat_hint` 字段是 `*bool`——`null` 表示"用格式默认值"。导出/导入必须保留指针（不能拍平为 bool），否则重新导入会丢失"未设置"语义。
11. 活动供应商（`active_provider_id`）**不**属于导入/导出范围——它是机器特定的。导入的供应商若恰好与活动 ID 相同，不改变哪个供应商是活动的（除非 `overwrite` 替换了该供应商的字段；活动 ID 本身保持不变）。

### 边界情况

1. 用户导出零个供应商——按钮禁用；若请求以空 `ids` 数组到达 API，响应含空 `providers` 数组（非错误）。
2. 用户导入非合法 JSON 的文件——前端在调用 API 前显示解析错误。
3. 用户导入合法 JSON 但 `version: 2`（未来）——API 返回明确的"不支持的导出版本"错误。
4. 导入文件中两个供应商 ID 相同——skip/overwrite 下第一个生效，duplicate 下各自获得新 ID；导入器在应用前对文件内部去重。
5. 导入文件引用当前活动供应商的 ID，用 `overwrite` 策略——活动供应商字段被替换；`active_provider_id` 不变。
6. 导出或导入期间网络/API 错误——前端显示错误消息，列表保持不变。
7. 导出文件很大（很多供应商）——不分页；单个 JSON 响应。
8. 导入文件含 `api_url` 校验失败的供应商——该供应商被跳过并计入 `errors`；其余正常导入。
9. 导入文件省略可选字段（如 `model_mappings`）——`json.Unmarshal` 保留零值；`Provider.normalizeDefaults()`（在 `Validate` 内调用）按需将 map 填为非 nil 空值。

### 非目标

1. 不导出/导入 `active_provider_id`——它是机器特定状态。
2. 不支持加密/密码保护的导出文件——明文 JSON 是有意设计（token 安全警告是缓解措施）。
3. 不做云同步或跨主机推送——这是基于文件的手动导入导出。
4. 不导出/导入全局配置字段（backend URL、主题、连接模式、监听地址）——仅供应商。
5. 不支持部分字段导入（如"仅导入模型映射"）——整供应商导入。
6. 不支持导入撤销——用户可在导入前先导出作为备份。

## 任务详情

### 任务 1：导出 API 端点

#### 需求

**Objective（目标）** — 新增后端端点，将选中供应商导出为含真实 token 的 JSON 文件，用于跨主机迁移。

**Outcomes（成果）** — `POST /api/providers/export` 接收 `{"ids": [...]}`，返回 `{"version": 1, "exported_at": "...", "providers": [...]}`，含完整 `config.Provider` 对象（含 `api_token`）。未知 ID 静默跳过。

**Evidence（证据）** — Handler 测试：创建 N 个供应商，按 ID 导出子集，断言响应恰好含这些供应商且 token 非掩码。

**Constraints（约束）** — 接口位于 `authMiddlewareFunc` 之后；注册为精确模式 `/api/providers/export`（在 `/api/providers/` 子树处理器之前，与 `/api/providers/test` 放置一致）；有意返回真实 token。

**Edge Cases（边界）** — 空 `ids` 数组 → 空 `providers` 数组；全部 ID 未知 → 空 `providers` 数组；请求体非法 → 400。

**Verification（验证）** — `go test ./internal/admin/`。

#### 计划

1. 在 `provider_handler.go` 新增 `handleExportProviders`：解码 `{"ids": [...]}`，`Load` config，按 ID 集合过滤 `cfg.Providers`，构建导出结构体，编码 JSON。
2. 在 `server.go` 注册 `mux.HandleFunc("/api/providers/export", s.authMiddlewareFunc(s.handleExportProviders))`（放在 `/api/providers/` 行之前以便阅读，与 `/api/providers/test` 放置一致）。
3. 定义 `exportFile` 结构体（`Version int`、`ExportedAt time.Time`、`Providers []config.Provider`）。
4. 编写 handler 测试覆盖：子集导出、全部未知 ID、空 IDs。

#### 验证

- [x] `POST /api/providers/export` 传有效 ID 返回含真实 token 的供应商。
- [x] 未知 ID 静默跳过。
- [x] 响应结构匹配导出文件格式（`version`、`exported_at`、`providers`）。

### 任务 2：导入 API 端点

#### 需求

**Objective（目标）** — 新增后端端点，从 JSON 负载导入供应商，支持用户选择的冲突策略。

**Outcomes（成果）** — `POST /api/providers/import` 接收 `{"providers": [...], "strategy": "skip"|"overwrite"|"duplicate"}`，在一次 `Load → 合并 → Save` 周期内应用导入，返回 `{"success": true, "imported": N, "skipped": N, "overwritten": N, "duplicated": N, "errors": [...]}`。

**Evidence（证据）** — 每种策略的 Handler 测试：(a) `skip`——冲突 ID 不变；(b) `overwrite`——冲突 ID 字段被替换，`created_at` 保留，`updated_at` 刷新；(c) `duplicate`——生成新 ID 追加。

**Constraints（约束）** — 单次 Load/Save 周期（Save 失败不部分应用）；无效供应商跳过并计入 `errors`；拒绝 `version != 1`；保留 `claude_code_compat_hint` 指针。

**Edge Cases（边界）** — 导入文件内 ID 重复；无效供应商（URL 不合法）；`version` 不匹配；未知策略值（默认 `skip`）。

**Verification（验证）** — `go test ./internal/admin/`。

#### 计划

1. 在 `provider_handler.go` 新增 `handleImportProviders`：解码请求，校验 `version == 1`，`Load` config，遍历供应商应用策略，`Save`，返回摘要。
2. 注册 `mux.HandleFunc("/api/providers/import", s.authMiddlewareFunc(s.handleImportProviders))`。
3. 应用前按 ID 对导入文件内供应商去重（skip/overwrite 下首次出现生效）。
4. `duplicate` 策略调用 `generateProviderID()` 并重置时间戳。
5. `overwrite` 策略保留 `created_at`，设置 `updated_at = time.Now()`。
6. 每条调用 `provider.Validate()`；出错则追加到 `errors` 并跳过。
7. 编写 handler 测试覆盖三种策略 + 版本不匹配 + 无效供应商。

#### 验证

- [x] `skip` 策略保留冲突供应商不变。
- [x] `overwrite` 策略替换字段，保留 `created_at`。
- [x] `duplicate` 策略以新 ID 追加。
- [x] 无效供应商被跳过并报告到 `errors`。
- [x] `version != 1` 返回明确错误。

### 任务 3：前端供应商选择

#### 需求

**Objective（目标）** — 让用户通过每张卡片的复选框选择一个或多个供应商。

**Outcomes（成果）** — `ProviderCard.vue` 在左上角（名称左侧）新增复选框。`DashboardView.vue` 用 `Set<string>` 跟踪选中 ID。点击复选框切换选中，不触发卡片其他操作。

**Evidence（证据）** — 组件测试：点击复选框从选择集合增删供应商 ID；点击编辑/删除不切换选中。

**Constraints（约束）** — 复选框在卡片头部行，名称之前，与状态圆点垂直对齐；卡片现有操作按钮不受影响。

**Edge Cases（边界）** — 全选；取消全选；选中状态下供应商被删除（从选择集合移除该 ID）。

#### 计划

1. 给 `ProviderCard.vue` 新增 `selected` prop 和 `toggle-select` emit。
2. 在头部行起始处（状态圆点之前）放置 `<input type="checkbox">`。
3. 在 `DashboardView.vue` 新增 `selectedProviderIds = ref(new Set<string>())`。
4. 在每个 `<ProviderCard>` 上绑定 `:selected` 和 `@toggle-select`。
5. 供应商删除时从集合移除其 ID。

#### 验证

- [x] 复选框出现在每张卡片左上角。
- [x] 切换复选框更新选择集合。
- [x] 卡片操作按钮（编辑、删除等）仍独立工作。

### 任务 4：前端导出/导入流程

#### 需求

**Objective（目标）** — 在工具栏新增导出和导入按钮，并打通完整的导出/导入用户流程（含预览弹窗）。

**Outcomes（成果）** — 工具栏在"添加供应商"右侧有导出（未选中时禁用）和导入按钮。导出收集选中 ID、调用 API、触发 JSON 下载。导入打开文件选择器、客户端读取文件、显示预览弹窗（新增/冲突计数 + 策略选择器）、确认后调用导入 API 并刷新列表。

**Evidence（证据）** — 前端构建通过；组件测试断言：空选择时导出禁用；导入预览显示正确的新增/冲突计数；确认发送正确策略。

**Constraints（约束）** — 导出文件名：`providers-export-YYYYMMDD-HHMMSS.json`；导入仅接受 `.json`；预览在客户端计算（对比导入文件 ID 与当前供应商 ID）。

**Edge Cases（边界）** — 导出空选择（按钮禁用）；导入文件非法（API 调用前解析错误）；API 错误（显示消息，列表不变）；导入同一台机器的导出文件（全部冲突）。

#### 计划

1. 在 `useApi.ts` 新增 `exportProviders(ids: string[])` 和 `importProviders(providers, strategy)`。
2. 在 `DashboardView.vue` 的"添加供应商"按钮后新增导出和导入按钮。
3. 导出处理：调用 `exportProviders([...selectedProviderIds])`，构建 `Blob`，触发下载。
4. 导入处理：隐藏的 `<input type="file" accept=".json">`；change 时通过 `FileReader` 读取，`JSON.parse`，计算预览（新增 vs 冲突），打开弹窗。
5. 预览弹窗：显示计数、策略单选（默认 `skip`）、确认按钮 → 调用 `importProviders` → 刷新列表。
6. 导出禁用状态与导入预览的组件测试。

#### 验证

- [x] 未选中供应商时导出按钮禁用。
- [x] 导出下载含选中供应商的 `.json` 文件。
- [x] 导入预览正确分类新增与冲突供应商。
- [x] 确认应用所选策略并刷新列表。

### 任务 5：i18n 与边界情况打磨

#### 需求

**Objective（目标）** — 完成所有新 UI 字符串的 zh/en i18n，加固边界情况（解析错误、API 错误、安全警告）。

**Outcomes（成果）** — `useI18n.ts` 新增 zh/en key：工具栏按钮（导出/导入）、选择状态、预览弹窗（标题、新增计数、冲突计数、策略 label、确认/取消）、安全警告、错误消息。所有用户可见字符串走 i18n——无硬编码字面量。

**Evidence（证据）** — 手动：切换 zh/en 验证每个新字符串已翻译；触发每个错误路径（非法文件、API 失败）验证消息已本地化。

**Constraints（约束）** — 遵循现有 i18n key 命名约定；安全警告须醒目展示（导出流程）并提及文件含 API token。

**Edge Cases（边界）** — 语言切换不破坏弹窗；超长供应商列表不溢出弹窗；文件过大。

#### 计划

1. 在 `providers.export.*`、`providers.import.*`、`providers.preview.*` 下新增 zh/en key。
2. 将每个新 UI 字符串接入 `t(...)`。
3. 在导出流程（下载前或成功 toast）加入 token 安全警告。
4. 本地化解析错误和 API 错误消息。

#### 验证

- [x] 每个新 UI 字符串有 zh 和 en 翻译。
- [x] 安全警告在导出流程中可见。
- [x] 错误路径显示本地化消息。
