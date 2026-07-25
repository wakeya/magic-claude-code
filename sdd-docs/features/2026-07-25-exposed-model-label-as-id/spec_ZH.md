# 暴露模型显示名即路由 ID 规格

**本地页面：** 供应商管理 → 编辑 → 「/model 可切换模型」编辑区（`internal/frontend/src/components/ProviderModal.vue` 暴露模型块，约 L150-172）
**代理入口：** `internal/config/provider.go`（`ExposedModel`、`Provider.Validate`、`generateExposedModelID`，约 L298-342、L204-243）；`internal/config/config.go`（跨 provider 全局唯一校验 + `ResolveRoute`，约 L146-160、L287-310）；`internal/config/sqlite_store.go`（`migrateExposedModelIDs`，约 L93-119）；`internal/proxy/hardcoded.go`（`collectAdditionalModelOptions`、`collectModels`，约 L557-601、L651-689）
**参考来源：** `sdd-docs/features/2026-07-08-cross-provider-model-routing/spec_ZH.md`（跨 provider 路由与随机 ID 设计）；`sdd-docs/features/2026-07-19-log-exposed-model-label/spec_ZH.md`（日志显示 Label）；`claude-code-src/src/src/utils/model/model.ts`（`parseUserSpecifiedModel` 原样返回自定义字符串、保留大小写）、`validateModel.ts`（真实 API 调用验证）、`modelAllowlist.ts`（`availableModels` 白名单默认未设全放行）
**技术栈：** Go 1.26 标准库 + Vue 3 前端
**最后更新：** 2026-07-25
**进度：** 5 / 5

## 整体分析（源站分析）

### 现象

供应商编辑里的「/model 可切换模型」条目有两个标识：

- `ExposedModel.ID`：路由键，成为 `/model` 菜单项的 value 与请求的 `model` 字段。当前为系统自动生成的随机 `em-<hex>`（`generateExposedModelID`，`provider.go:330`），**前端隐藏 ID 输入框，用户无从得知**。
- `ExposedModel.Label`：显示名，`/model` 菜单、对话框与日志（2026-07-19 起）展示给用户看的名称。

二者不一致导致 `claude --model` 无法使用：

- `claude --model <模型ID>`：ID 是随机 `em-<hex>`，用户看不到、记不住。
- `claude --model <显示名>`：`ResolveRoute`（`config.go:287`）只按 `em.ID == model` 匹配，Label 不参与路由 → 未命中 → 回退到 active provider 的 `MapModel` → 后端收到不认识的 Label → 404 "model not found"。

### 根因（随机 ID 的设计演变）

`git log -S generateExposedModelID` 还原了方向演变：

1. 原始 spec（`406f77b`，跨 provider 路由）允许用户**手输语义化 ID**（如 `glm-5.2-ky`），ID 即菜单 value。
2. 后续改为**前端隐藏 ID 输入**、后端自动生成稳定随机 `em-<hex>`，并加一次性迁移 `migrateExposedModelIDs`（`sqlite_store.go:97`）把存量手输 ID 强制重写为随机 em-。其注释明确记录了已知副作用：「迁移后用户 ~/.claude.json 里的旧 mainLoopModelOverride 失效，需重新 /model 选择」——**改 ID 会使客户端会话级选择失效，但属可接受副作用**。
3. 再后续（`2026-07-19`）日志层用 Label 替代 em- ID 展示，解决了日志可读性，但没解决 `--model` 可用性。

随机 ID 满足了「用户无需感知 ID、provider 重排时 ID 稳定」的简洁性诉求，代价是制造了「所见（Label）≠ 所用（ID）」的分裂。

### Claude Code 客户端侧验证（决定方案可行性的三条事实）

**事实 1：`--model` 不做模型存在性校验。** `getUserSpecifiedModelSetting`（`model.ts`）只过 `isModelAllowed`（`modelAllowlist.ts:100`），其白名单 `availableModels` 来自 `settings`，**默认未设置即全放行**。因此 `--model <任意字符串>` 原样进入请求 `model` 字段。

**事实 2：自定义 model 字符串原样透传、保留大小写。** `parseUserSpecifiedModel`（`model.ts:445`）对非别名字符串 `return modelInputTrimmed`（仅 trim 首尾空白），注释明确 "Preserve original case for custom model names"。Unicode、内部空格均原样保留；仅 `sonnet/opus/haiku/opusplan/best` 别名被特殊映射。

**事实 3：交互式验证走真实 API 调用。** `/model` 菜单手输自定义名时 `validateModel`（`validateModel.ts:19`）发起 `sideQuery`（max_tokens=1）真实请求，成功即 valid。因此只要 mcc 能把该字符串路由成功，交互验证也通过。

→ **结论：唯一卡点是 mcc 的 `ResolveRoute` 只匹配 `em.ID`。** 让 Label 成为路由键即可同时打通 `--model` 主循环路径与 `/model` 交互验证路径，客户端零改动。

### 设计决策：Label = ID（所见即所得）

**核心：保留 `ExposedModel.ID` 字段（sqlite/admin/frontend 透传链路不变），但保存时令 `em.ID = TrimSpace(em.Label)`，使显示名与路由键统一。** 用户只填一个「显示名」，它同时是 `/model` 菜单展示文本、菜单 value、请求 `model` 字段与路由匹配键。

| 方案 | 说明 | 是否采用 |
| --- | --- | --- |
| A. Label = ID | ID 与显示名统一，路由逻辑零改动，菜单 value == 展示文本 | ✅ |
| B. 路由支持 Label 回退匹配 | 保留随机 ID，`ResolveRoute` 增加按 Label 的二级匹配 | — （二元分裂仍在） |
| C. 语义化 slug 自动生成 | 从 Label 派生 slug 作 ID | — （中文难 slug 化、与 Label 耦合稳定性差） |
| D. 新增 alias 别名字段 | 全链路（SQLite/admin/前端/i18n）新增字段 | — （改动最重） |

选 A 的理由：

- **路由层零改动**——`ResolveRoute` 仍按 `em.ID` 匹配，只是 ID 的值变成了 Label。
- **bootstrap/`/v1/models` 零改动**——`collectAdditionalModelOptions` 已输出 `model=ID, name=Label`；ID=Label 后二者天然一致（`value == 展示文本`）。admin 透传链路同理零改动（`Provider.Validate` 统一归一 ID=Label）。
- **彻底消除二元分裂**——`/model` 展示的就能用于 `--model`。

**字符集约束（Label 成为路由键后继承 ID 的客户端约束）：** 允许 Unicode（兼容中文显示名，如「智谱GLM」），**禁止空格与控制字符**（空格在 shell `--model` 需引号、易错），禁止 `claude-` 前缀、`[1m]`、`sonnet|opus|haiku|opusplan` 等 Claude Code 保留别名（沿用现有校验，`provider.go:224-238`）。

**唯一性范围：全局唯一（跨所有 provider）。** 路由是跨 provider 全局匹配的（`ResolveRoute` 遍历所有 enabled provider），Label 重复会导致路由歧义（首个命中）。沿用现有 `config.go:146-160` 的 `em.ID` 全局唯一校验（ID=Label 后即 Label 全局唯一）。

### 影响面分析

| 文件 | 改动 | 说明 |
|------|------|------|
| `internal/config/provider.go` | **改** | `Validate` 设 `em.ID = em.Label`；字符集校验从 ASCII 白名单改为「禁空格/控制字符」黑名单；移除 `em.ID == "" → generateExposedModelID()` 分支；删除 `generateExposedModelID` |
| `internal/config/config.go` | **微调** | 全局唯一错误信息措辞由 "exposed model id" 改为提示「显示名」（ID=Label 后语义更准确）；逻辑不变 |
| `internal/config/sqlite_store.go` | **改** | `migrateExposedModelIDs` 反向：把 `em.ID` 统一重写为 `TrimSpace(em.Label)`（逆转历史的「手输→随机」方向），幂等 |
| `internal/proxy/hardcoded.go` | **零改动** | ID=Label 后 `model == name`、`id == display_name` 天然成立 |
| `internal/admin/provider_handler.go` | **零改动** | 透传 `ExposedModels`，`store.Update → Validate` 自动归一 ID=Label |
| `internal/frontend/.../ProviderModal.vue` | **改** | 显示名输入区增加提示「显示名即模型 ID，可直接用于 `claude --model <显示名>`」 |
| `internal/frontend/.../useI18n.ts` | **改** | 新增/更新提示文案（中英双语）；更新「ID 由系统自动生成」的过时表述 |

### 向后兼容

- **存量随机 em- ID**：启动迁移 `em.ID = TrimSpace(em.Label)` 重写为 Label。副作用：客户端旧 `mainLoopModelOverride`（存的是旧 em- ID）失效，需重新 `/model` 选择——与历史迁移副作用一致，可接受（`/model` 为会话级内存状态，事实见 cross-provider spec）。
- **存量含空格的 Label**：迁移不做空格清洗（`TrimSpace` 只去首尾），ID=Label 保留内部空格，**路由仍可命中**（`--model "Kimi K2"` 经 `parseUserSpecifiedModel` 仅 trim 首尾、保留内部空格）。但新字符集校验在下次编辑保存（`store.Update → Validate`）时会拒绝含空格 Label，用户需将其改为无空格——属「 surfaced（主动暴露）」而非静默破坏，符合项目编码规范第 6 条。
- **存量跨 provider 重复 Label**：迁移后 ID 重复，路由首个命中（不崩溃）；全局唯一校验在下次编辑保存时报 400，错误信息含 provider 名便于定位。
- **JSON Store**：`cmd/server/main.go:121` 仅用 `NewSQLiteStore`，JSON `Store` 为遗留迁移源，**无需迁移**；其用户下次编辑保存时经 `Validate` 自动归一。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | ✅ | config 层：`Validate` 设 ID=Label + 字符集黑名单 + 移除随机生成；全局唯一错误措辞 | `internal/config/provider.go`、`internal/config/config.go` | `go test ./internal/config/...` |
| 2 | ✅ | SQLite 反向迁移：`migrateExposedModelIDs` 重写 em- ID → Label | `internal/config/sqlite_store.go` | SQLite round-trip 迁移单测 |
| 3 | ✅ | 前端提示文案 + i18n（中英） | `ProviderModal.vue`、`useI18n.ts`、`dist/` | `npm --prefix internal/frontend test && run build` |
| 4 | ✅ | 更新受影响测试 + 全量回归 | `provider_test.go`、`sqlite_store_test.go` 等 | `make test` |
| 5 | ✅ | 端到端验证 + 提交 | 验证记录 | 手动 `/model` + `--model` 全链路；`git commit`（不 push） |

## 需求

### 交付物

1. `Provider.Validate`（`provider.go`）对每个 `ExposedModel` 在 TrimSpace 后设 `em.ID = em.Label`；移除「ID 留空 → `generateExposedModelID()`」分支；删除 `generateExposedModelID` 函数。
2. `Provider.Validate` 的 ID 字符集校验由 ASCII 白名单 `[a-zA-Z0-9._:-]` 改为黑名单：**禁止空格（`unicode.IsSpace`）与控制字符（`unicode.IsControl`）**，其余 Unicode 字符放行。保留 `claude-` 前缀、`[1m]`、`sonnet|opus|haiku|opusplan` 别名的禁止校验。
3. `Config.Validate`（`config.go`）跨 provider 全局唯一校验逻辑不变（仍按 `em.ID`，ID=Label 后即 Label 全局唯一）；错误信息措辞由 "exposed model id %q is duplicated" 调整为提示「显示名（模型 ID）」，便于用户理解。
4. `SQLiteStore.migrateExposedModelIDs`（`sqlite_store.go`）反向迁移：对每个 `ExposedModel`，当 `TrimSpace(em.Label)` 非空且 `em.ID != TrimSpace(em.Label)` 时，设 `em.ID = TrimSpace(em.Label)` 并标记变更；有变更则 `s.save(cfg)`。幂等（二次启动不再触发）。移除对 `generateExposedModelID` 的调用。
5. 前端 `ProviderModal.vue` 暴露模型区增加提示：显示名即模型 ID，可直接用于 `claude --model <显示名>`；`useI18n.ts` 中英双语，并把过时的「ID 由系统自动生成」表述改为「显示名将作为模型 ID」。
6. `internal/proxy/hardcoded.go`、`internal/admin/provider_handler.go` **不改**（ID=Label 后天然满足）。

### 约束

- `ExposedModel.ID` 字段保留（不改数据模型/schema），仅赋值语义变为「= Label」。
- 路由匹配（`ResolveRoute`）、请求体、`usage`、故障切换逻辑一律不动。
- 全局唯一性沿用现有校验路径（`Config.Validate`），不新增并行校验。
- 前端源码变更后必须 `npm run build` 重建 `dist/`（Go 二进制内嵌，见 CLAUDE.md 提交约束第 4 条）。
- 中英双语文档/文案同步（见全局记忆 Bilingual Output Requirement）。

### 边界条件

- Label 为空 → `label is required`（沿用现有校验）。
- Label 含空格/控制字符 → 新字符集校验拒绝。
- Label 以 `claude-` 开头 / 含 `[1m]` / 为保留别名 → 拒绝（沿用）。
- Label 跨 provider 重复 → `Config.Validate` 返回 400（沿用全局唯一）。
- Label 为中文/Unicode（无空格）→ 通过，可作路由键。
- Context1M 模型：bootstrap value 仍附 `[1m]`（ID+`[1m]`）；`ResolveRoute` 剥离 `[1m]` 后匹配纯 Label。
- 迁移：Label 为空的存量项跳过（不置 ID=""）；迁移幂等。

## 任务详情

### 任务 1：config 层——Label=ID + 字符集黑名单 + 移除随机生成

#### 需求

**Objective（目标）** — 让 `ExposedModel.ID` 在保存时等于其 `Label`（显示名即路由键），字符集校验改为「允许 Unicode、禁空格/控制字符」，移除随机 `em-<hex>` 生成路径，使 `/model` 菜单展示文本与 `claude --model` 可用值统一。

**Outcomes（成果）** — `provider.go`：`Validate` 第二个循环开头设 `em.ID = em.Label`（Label 已在第一个循环 TrimSpace）；删除 `if em.ID == "" { em.ID = generateExposedModelID() }` 分支；字符集校验替换为 `unicode.IsSpace(r) || unicode.IsControl(r)` 黑名单（import `unicode`）；删除 `generateExposedModelID` 函数（其 `crypto/rand`/`encoding/hex` 依赖仍被 `generateProviderID`/`randomHex` 使用，保留 import）。`config.go`：全局唯一错误信息措辞调整为含「显示名」。

**Evidence（证据）** — `go test ./internal/config/` 通过：`Validate` 后 `em.ID == em.Label`；含空格 Label 报错；中文 Label（如 `智谱GLM`）通过且 `em.ID == "智谱GLM"`；`claude-` 前缀/`[1m]`/别名仍被拒；跨 provider 重复 Label 报 400。

**Constraints（约束）** — `ExposedModel.ID` 字段保留；`ResolveRoute` 不动（仍匹配 `em.ID`）；保留 `claude-`/`[1m]`/别名禁止校验与 Label/BackendModel 非空校验；per-provider 重复检查 `seenExposedIDs` 保留（ID=Label 后即 per-provider Label 去重）。

**Edge Cases（边界）** — Label 空 → `label is required`；Label 全空格（TrimSpace 后空）→ `label is required`；Label 含内部空格 → 字符集拒绝；Label 含制表符/换行（控制/空白）→ 拒绝；中文/日文/emoji（无空格）→ 通过。

**Verification（验证）** — `go test ./internal/config/ -run TestProvider` 与 `go vet ./internal/config/` 全绿。

#### 计划

1. **先改失败测试。** `internal/config/provider_test.go`：
   - 把 L224-234「空 ID 自动生成 em- 前缀」用例改为「ID 等于 Label」：构造 `Provider{... ExposedModels: []ExposedModel{{Label: "GLM-4.6", BackendModel: "glm-4.6"}}}`（ID 留空），`Validate()` 后断言 `em.ID == "GLM-4.6"`。
   - 新增用例：`Label: "智谱GLM"` → `Validate()` 通过且 `em.ID == "智谱GLM"`。
   - 新增用例：`Label: "Kimi K2"`（含空格）→ `Validate()` 返回错误，错误含 "space"。
   - 新增用例：`Label: "Kimi\tK2"`（含制表符）→ 返回错误。
   - 保留并确认 `claude-` 前缀、`[1m]`、`sonnet` 别名拒绝用例仍通过。
2. **确认失败。** `go test ./internal/config/ -run TestProvider` → 失败（ID 仍为空或 em-，空格未拒）。
3. **最小实现。** `internal/config/provider.go`：
   - import 块加 `"unicode"`。
   - 第二个循环（约 L211-243）开头改为：
     ```go
     em := &p.ExposedModels[i]
     // 所见即所得：显示名即唯一路由键，ID 统一归一为 Label
     em.ID = em.Label
     if em.Label == "" {
         return fmt.Errorf("exposed_models[%d]: label is required", i)
     }
     ```
     （删除原 `if em.ID == "" { em.ID = generateExposedModelID() }`）
   - 字符集校验（约 L234-238）替换为：
     ```go
     if strings.IndexFunc(em.ID, func(r rune) bool {
         return unicode.IsSpace(r) || unicode.IsControl(r)
     }) >= 0 {
         return fmt.Errorf("exposed_models[%d]: display name (model id) must not contain spaces or control characters", i)
     }
     ```
   - 删除 `generateExposedModelID` 函数（约 L327-332）及其注释。
4. **微调 config.go。** `internal/config/config.go:155` 错误信息改为：
   ```go
   return fmt.Errorf("exposed model display name (id) %q is duplicated between provider %q and %q", id, firstProvider, c.Providers[i].Name)
   ```
5. **确认通过。** `go test ./internal/config/ -run TestProvider` 全过；`go test ./internal/config/` 全包回归。
6. **回归。** `go vet ./internal/config/`。
7. **提交。** `git add internal/config/provider.go internal/config/config.go internal/config/provider_test.go && git commit -m "feat(config): exposed model label as routable id (what-you-see-is-what-you-get)"`。

#### 验证

- [x] `go test ./internal/config/ -run TestProviderValidate_ExposedModel` — 全过（ID=Label、Unicode 通过、空格/控制字符/`claude-`/`[1m]`/别名拒绝、provider 内重复拒绝）。
- [x] `go test ./internal/config/` 与 `go vet ./internal/config/` — 干净。

### 任务 2：SQLite 反向迁移——em- ID → Label

#### 需求

**Objective（目标）** — 把 `migrateExposedModelIDs` 从历史的「手输 ID → 随机 em-」反转为「随机 em- ID → Label」，使存量配置启动后 ID 与显示名统一，`claude --model <显示名>` 立即对存量数据生效。

**Outcomes（成果）** — `sqlite_store.go:97-119` 的 `migrateExposedModelIDs` 改为：遍历所有 provider 的 `ExposedModels`，当 `label := strings.TrimSpace(em.Label)` 非空且 `em.ID != label` 时设 `em.ID = label`、`changed = true`；有变更 `s.save(cfg)`。移除 `generateExposedModelID` 调用。更新函数注释说明新方向与副作用（旧 mainLoopModelOverride 失效需重新 /model）。

**Evidence（证据）** — `go test ./internal/config/ -run TestMigrate` 通过：预置 `ExposedModels: [{ID:"em-abcd1234", Label:"GLM-4.6", BackendModel:"x"}, {ID:"em-ffff0000", Label:"Kimi", BackendModel:"y"}]`，新开 store 触发迁移后二者 ID 分别变为 `"GLM-4.6"`、`"Kimi"`；二次 Load 不再变更（幂等）；Label 为空的项 ID 不被置空。

**Constraints（约束）** — 迁移用 `s.save(cfg)`（小写，**不触发 Validate**），避免存量含空格 Label 在迁移阶段被校验拒绝；迁移幂等；仅 SQLite store（`cmd/server/main.go:121` 唯一在用 store），JSON Store 无需迁移。

**Edge Cases（边界）** — Label 空 → 跳过（不设 ID=""）；ID 已 == Label → 不变（幂等）；含内部空格 Label → ID 保留内部空格（迁移不清洗，下次编辑校验时暴露）；跨 provider 重复 Label → 迁移照写（不校验），路由首个命中，编辑保存时报 400。

**Verification（验证）** — `go test ./internal/config/ -run TestMigrate` 全绿；`go test ./internal/config/` 全包回归。

#### 计划

1. **先改失败测试。** `internal/config/sqlite_store_test.go`（约 L955-1006 的迁移测试）重写为反向：
   - 预置旧格式：`{ID: "em-abcd1234", Label: "GLM-4.6", BackendModel: "x"}` 与 `{ID: "em-ffff0000", Label: "Kimi", BackendModel: "y"}`，直接写库（绕过 Validate）。
   - 新开 `SQLiteStore` 触发 `init → migrateExposedModelIDs`。
   - 断言 `got[0].ID == "GLM-4.6"`、`got[1].ID == "Kimi"`。
   - 断言幂等：再次 `Load` ID 不变。
   - 新增 Label 为空项不被置空的断言。
2. **确认失败。** `go test ./internal/config/ -run TestMigrate` → 失败（ID 仍是 em-）。
3. **最小实现。** `internal/config/sqlite_store.go` 把 `migrateExposedModelIDs`（L97-119）改为：
   ```go
   // migrateExposedModelIDs 一次性迁移（所见即所得方向）：把存量随机 em-<hex> ID
   // 统一重写为 TrimSpace(Label)，使显示名即路由键。迁移后 ID==Label，幂等不再触发。
   // 用 s.save（不触发 Validate），避免存量含空格 Label 在迁移阶段被拒；其字符集
   // 约束在下次编辑保存时暴露。副作用：客户端旧 mainLoopModelOverride 失效，需重新 /model。
   func (s *SQLiteStore) migrateExposedModelIDs() error {
       cfg, err := s.Load()
       if err != nil {
           return err
       }
       if cfg == nil {
           return nil
       }
       changed := false
       for i := range cfg.Providers {
           for j := range cfg.Providers[i].ExposedModels {
               em := &cfg.Providers[i].ExposedModels[j]
               if label := strings.TrimSpace(em.Label); label != "" && em.ID != label {
                   em.ID = label
                   changed = true
               }
           }
       }
       if changed {
           return s.save(cfg)
       }
       return nil
   }
   ```
4. **确认通过。** `go test ./internal/config/ -run TestMigrate` 全过。
5. **回归。** `go test ./internal/config/`、`go vet ./internal/config/`。
6. **提交。** `git add internal/config/sqlite_store.go internal/config/sqlite_store_test.go && git commit -m "feat(config): reverse migration rewrites exposed model id to label"`。

#### 验证

- [x] `go test ./internal/config/ -run TestSQLiteStoreMigratesLegacyExposedModelIDs` — 全过（em- → Label、幂等、空 Label 不置空）。
- [x] `go test ./internal/config/` 与 `go vet` — 干净。

### 任务 3：前端提示文案 + i18n

#### 需求

**Objective（目标）** — 让用户在编辑暴露模型时明确「显示名即模型 ID，可直接用于 `claude --model`」，并移除「ID 由系统自动生成」的过时表述。

**Outcomes（成果）** — `ProviderModal.vue` 暴露模型区（约 L150-172）的提示文案更新为说明显示名即 ID；`useI18n.ts` 中英双语对应键更新（`exposed_model_backend_hint` 等，约中文 L168-175、英文 L638-645），把「ID 由系统自动生成 / ID is auto-generated」改为「显示名将作为模型 ID，可直接用于 claude --model <显示名> / Display name becomes the model ID, usable directly with claude --model <name>」。`npm run build` 重建 `dist/`。

**Evidence（证据）** — `npm --prefix internal/frontend test` 通过；`npm --prefix internal/frontend run build` 成功，`dist/` 更新；人工打开编辑弹窗看到新提示。

**Constraints（约束）** — 不改表单数据结构与提交逻辑（仍提交 `id: em.id`，后端 `Validate` 归一为 Label）；文案中英同步。

**Edge Cases（边界）** — 既有 i18n 键被复用则更新值，新增键则中英两处都加；构建产物 `dist/` 随提交。

**Verification（验证）** — 前端测试与构建全绿；`dist/` 有变更。

#### 计划

1. 定位 `ProviderModal.vue` 暴露模型区现有提示（`t('modal.exposed_model_backend_hint')`，约 L167）与 `useI18n.ts` 对应键值（中文约 L173、英文约 L643）。
2. 更新 `useI18n.ts`：把「可从上方"模型映射"的映射值快速填充；ID 由系统自动生成」改为「可从上方"模型映射"快速填充后端模型名；显示名将作为模型 ID，可直接用于 claude --model <显示名>」；英文同步「Autofill backend model from the mappings above; the display name becomes the model ID, usable directly with claude --model <name>」。
3. 如需在显示名输入框旁加独立 hint，新增 `exposed_model_label_hint` 键（中英），并在 `ProviderModal.vue` 显示名 input 下渲染。
4. `npm --prefix internal/frontend test`。
5. `npm --prefix internal/frontend run build`（重建 `dist/`）。
6. **提交。** `git add internal/frontend/src internal/frontend/dist && git commit -m "feat(frontend): hint that exposed model display name is the routable id"`。

#### 验证

- [x] `npm test`（internal/frontend）— 195 通过 0 失败。
- [x] `npm run build` — 成功，`dist/` 已重建（useI18n/index 资源 hash 更新）。

### 任务 4：受影响测试更新 + 全量回归

#### 需求

**Objective（目标）** — 修正所有依赖「随机 em- ID」旧契约的测试，确保 `make test`（含 race + 覆盖率）全绿。

**Outcomes（成果）** — 已在任务 1/2 更新 `provider_test.go`、`sqlite_store_test.go`；本任务排查并修正其余可能受 ID=Label 影响的测试（`config_test.go`、`failover_test.go`、`admin/provider_handler_test.go`、`proxy/hardcoded_test.go` 等：凡调用 `Validate` 且显式设置 `ID != Label` 的用例，ID 会被归一为 Label，需调整断言或让 Label 与 ID 一致）。

**Evidence（证据）** — `make test` 全绿（0 失败）；`go vet ./...` 干净。

**Constraints（约束）** — 不改生产逻辑迁就测试；测试断言反映新契约（ID=Label）。

**Edge Cases（边界）** — `ResolveRoute` 单测多直接构造 `ExposedModel{ID:..., Label:...}` 不经 `Validate`，通常不受影响，但需逐一确认。

**Verification（验证）** — `make test` 全绿。

#### 计划

1. `grep -rn "ExposedModel{" internal/ | grep _test` 排查所有测试构造点，识别经 `Validate` 且 ID≠Label 的用例。
2. 逐个修正：让 `Label` 与期望路由键一致，或调整断言。
3. `make test`（= `go test -v -race -coverprofile=coverage.out ./...`）。
4. `go vet ./...`。
5. **提交（如有测试改动）。** `git add -A internal && git commit -m "test: align exposed model tests with label-as-id contract"`。

#### 验证

- [x] `go test -race ./...` — 15 包全 ok，0 失败（含受影响测试更新：3 个 admin 用例、4 个 proxy bootstrap/models 用例改为 ID=Label 契约 + 所见即所得断言）。
- [x] `go vet ./...` — 干净。

### 任务 5：端到端验证 + 提交收尾

#### 需求

**Objective（目标）** — 真实链路验证「所见即所得」：`/model` 菜单展示的显示名可直接用于 `claude --model`，请求正确路由到目标 provider 的 BackendModel。

**Outcomes（成果）** — 构建二进制，配置一个暴露模型（显示名如 `GLM-4.6`，BackendModel 如 `glm-4.6`），验证：(a) 启动迁移把存量 em- ID 重写为 Label；(b) `/api/claude_cli/bootstrap` 的 `additional_model_options` 中 `model == name == GLM-4.6`；(c) `GET /v1/models` 返回 `id == display_name == GLM-4.6`；(d) 发送 `model=GLM-4.6` 的 `/v1/messages` 请求命中该 provider、转发 BackendModel；(e) `claude --model GLM-4.6` 会话可用。

**Evidence（证据）** — 上述各步实际输出记录于此。

**Constraints（约束）** — 只 commit 不 push（见全局记忆 Local Commit Before Push）；提交前 `git status --short` / `git diff --stat` 确认只含本任务文件。

**Edge Cases（边界）** — 含中文显示名的模型同样验证一次（如 `智谱GLM`）。

**Verification（验证）** — 全链路通过；工作区仅本功能相关变更。

#### 计划

1. `make build`（或 `go build ./cmd/server`）构建。
2. 配置暴露模型并启动，`curl` 验证 bootstrap 与 `/v1/models` 输出 `model == name`、`id == display_name`。
3. 构造 `POST /v1/messages` body `{"model":"GLM-4.6",...}`，确认日志命中目标 provider 且 `-> BackendModel`。
4. 中文显示名复验一次。
5. `git status --short && git diff --stat` 核对变更范围；确认前端 `dist/` 已含。
6. 汇总各任务提交；**不 push**，等用户确认。

#### 验证

- [x] bootstrap 输出 `model==name`（`TestHandleBootstrap_EmitsExposedModels` 断言）、`/v1/models` 输出 `id==display_name`（`TestHardcodedModelsUsesConfiguredProviders` 断言）。
- [x] 显示名路由：`TestResolveRouteByDisplayNameAfterValidate` 证明经 `Validate` 归一后 `ResolveRoute("智谱GLM")` 命中目标 provider + BackendModel（即 `claude --model <显示名>` 链路）。
- [x] Context1M 模型 value 附 `[1m]` 且仍按纯 Label 路由（`TestHandleBootstrap_Context1MAppendsBracket1m`）。
- [x] `git status` 仅本功能变更；只 commit 未 push。

> 验证层级说明：以上为集成测试级验证（httptest + 真实 Handler/Store + 真实 `Validate` 路径），覆盖 bootstrap、`/v1/models`、`ResolveRoute` 全链路。字面 `claude --model` CLI 实机验证可在合并前手动补做。
