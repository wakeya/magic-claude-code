# NPM Audit 漏洞修复规格

本地页面：管理面板 → 用量统计（Usage overview）标签（echarts 图表）/ 前端构建工具链（vite）  
代理入口：无（仅前端依赖升级，不涉及代理/后端）  
参考源站：`npm audit`、GHSA-fgmj-fm8m-jvvx、GHSA-v6wh-96g9-6wx3、GHSA-fx2h-pf6j-xcff  
技术栈：npm 依赖（`echarts`、`vite`）+ 现有 Vue 3 + Vite 前端  
最后更新：2026-07-10  
进度：3 / 4 已完成（任务 3 推迟到发版；见"实际实现结果"一节）

## 整体分析（源站分析）

### 漏洞清单

在 `internal/frontend/` 下运行 `npm audit`，报告恰好两条告警，已与 `package-lock.json` 核对：

| 包 | 锁定版本 | 受影响范围 | 严重度 | 告警 | 修复目标 |
| --- | --- | --- | --- | --- | --- |
| `echarts` | 6.0.0 | `< 6.1.0` | moderate | GHSA-fgmj-fm8m-jvvx（XSS） | 6.1.0 |
| `vite` | 8.0.8 | `8.0.0 - 8.0.15` | high | GHSA-v6wh-96g9-6wx3 + GHSA-fx2h-pf6j-xcff | 8.0.16+（实际解析为 8.1.4） |

`npm audit fix --dry-run` 确认：两个修复都在**现有 caret 范围内**（`^6.0.0` 和 `^8.0.8`）即可解决，无需 `--force`，不跨 major。

### echarts（moderate XSS，GHSA-fgmj-fm8m-jvvx）

Apache ECharts 6.1.0 之前版本存在跨站脚本（XSS）漏洞。该告警影响将不可信数据传入图表配置项的渲染路径。

项目使用面（`grep -rn echarts src/`）：

- 仅 `src/views/DashboardView.vue` 引入 echarts，采用**按需动态导入**（`echarts/core`、`echarts/charts`、`echarts/components`、`echarts/renderers`）。
- 注册模块：`LineChart`、`GridComponent`、`TooltipComponent`、`LegendComponent`、`GraphicComponent`、`CanvasRenderer`。
- 图表为**双 Y 轴折线图**（`type: 'line'`、`smooth: true`），数据来自项目自有管理 API（`/api/usage/...`）的 `usageTrends`。没有任何用户可控字符串被传入会渲染 HTML 的配置字段。
- 应用注册的是 `LineChart` 而非告警涉及的 `LinesChart`，故现有图表配置**不可达**该 XSS 路径。升级属纵深防御，并清除告警。
- 在本项目中的实际可利用性低（数据源是运营者自有后端，非外部用户输入）。

### vite（high，仅 Windows 开发服务器）

两个子告警，都仅限 Windows + 开发服务器：

1. **GHSA-v6wh-96g9-6wx3** — `launch-editor` 经 UNC 路径泄露 NTLMv2 哈希，仅 Windows。
2. **GHSA-fx2h-pf6j-xcff** — `server.fs.deny` 在 Windows alternate paths 下可绕过。

项目使用面：

- `vite` 是 **devDependency**，只在 `npm run dev` / `npm run build` 时运行。生产产物是内嵌进 Go 二进制的静态 `dist/` 包——根本不携带 vite 开发服务器。
- 开发与 CI 运行在 Linux；生产部署在 Alpine Docker 镜像。两个子告警在这些平台上都不可达。
- 实际威胁仅限 Windows 开发者。但仍值得升级：范围零成本、清除 high 告警、保护 Windows 协作者。

### 修复策略与解析版本

`npm audit fix` 解析到各 caret 范围内的**最新版本**，dry-run 已确认：

```
change zrender  6.0.0 => 6.1.0   (echarts 传递依赖)
change echarts  6.0.0 => 6.1.0
change vite     8.0.8 => 8.1.4   (跨 8.0 -> 8.1 minor，仍在 ^8.0.8 内)
... 另有传递依赖升级（tinyglobby、rolldown、postcss、picomatch、nanoid 等）
```

关键决策——vite 目标版本：

- `npm audit fix` 将 vite 拉到 **8.1.4**（`^8.0.8` 下当前最新），即从 8.0.x 的 minor 跨越。这是默认的、semver 兼容的路径。
- 最小侵入的替代方案是把 vite pin 到 `^8.0.16`（仅 patch 修复）。这**不是**默认方案，仅作为 8.1.4 导致构建/运行时回归时的回退。
- 默认计划：接受 `npm audit fix` → vite 8.1.4。回退计划：若 `npm run build` 或仪表盘图表异常，在 `package.json` 中 pin `"vite": "^8.0.16"` 并重新 `npm install`。

### 嵌入式前端约束（为何 `dist/` 必须提交）

`internal/frontend/embed.go` 为 `//go:embed dist/*`，而 `Makefile` 的 `make build` / `make run` 直接调用 Go（`go build` / `go run`），**不重建前端**。因此干净检出后嵌入 Go 二进制的正是被跟踪的 `internal/frontend/dist/`。发版链路（Docker、`scripts/release.sh`、CI）各自独立重建前端，但轻量的 `make build` / `make run` 路径依赖已提交的 `dist/`。

推论：任何前端依赖或源码变更后，必须重新 `npm run build` 并提交重建的 `internal/frontend/dist/`，使内嵌前端与升级后的依赖一致。这取代任何更宽松的"dist 不提交"指引——对本仓库而言，`dist/` 是被跟踪、被嵌入的产物，不是仅发版时的构建输出。

### 风险总结

1. **echarts 6.0 → 6.1 是 minor 升级。** 项目只用了长期稳定的配置 API（折线序列 + grid/tooltip/legend/graphic + canvas 渲染器）。破坏可能性低，但单元测试不渲染像素，因此必须手动检查仪表盘渲染（任务 3）。
2. **vite 8.0 → 8.1 是 minor 升级。** 作为仅开发用的构建工具，项目可见面只有 `npm run build` 和 `npm run dev`。一次干净的生产构建即足够证据；必要时可回退到 `^8.0.16`。
3. **传递依赖变动**（zrender、rolldown、postcss 等）是预期且良性的——它们由 echarts/vite 自身范围拉入，记录在 `package-lock.json` 中。这些不需要在 `package.json` 中直接改动。
4. **`dist/` 必须随 lockfile 一起提交** —— 见上文"嵌入式前端约束"。不提交重建的 `dist/` 会导致 `make build`/`make run` 嵌入过时（修复前的）前端。
5. **未确认不推送**——按项目约定，在 `fix/npm-audit` 上只本地提交，等待明确许可后再推送 / 开 PR。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成 | 运行 `npm audit fix` 升级 echarts + vite | 仅 `package-lock.json`（echarts 6.1.0、vite 8.1.4；`package.json` caret 范围未变） | `npm audit` 显示 0 漏洞 |
| 2 | 已完成 | 构建与单元测试验证 | 构建产物、测试结果 | `npm run build` 成功；`npm test` 通过 158/158（含 echarts 懒加载断言） |
| 3 | 已推迟 | echarts 运行时渲染验证 | 手动仪表盘渲染检查 | Usage overview 双轴折线图正常渲染；空状态 graphic 显示（推迟到发版） |
| 4 | 已完成 | 工作区清理与本地提交 | `fix/npm-audit` 上一次提交 | 提交 = lockfile + 重建的 `dist/` + 双语 spec + README 索引 + 双语审查记录；未推送 |

## 需求

### 交付物

1. `internal/frontend/package.json` 的 caret 范围 `^6.0.0`（echarts）和 `^8.0.8`（vite）保持**不变**——两个解析版本（6.1.0 / 8.1.4）仍满足它们。`npm audit fix` 不修改该文件本身。
2. `internal/frontend/package-lock.json` 重新生成，锁定 echarts 6.1.0、vite 8.1.4 及其传递依赖更新（zrender 6.1.0 等）。
3. `internal/frontend/dist/` 由 `npm run build` 重建并**提交**（因 `embed.go` 嵌入它，必须提交）。
4. 在 `internal/frontend/` 下运行 `npm audit` 报告 **0 漏洞**。
5. 在 `internal/frontend/` 下运行 `npm run build` 成功并重新生成 `dist/`。
6. 在 `internal/frontend/` 下运行 `npm test` 通过，包括 `src/views/DashboardUsageRequests.test.ts` 中的 echarts 懒加载断言。
7. echarts 6.0 → 6.1 升级后，Usage Overview 仪表盘图表运行时渲染正常（手动验证，因为单元测试不渲染像素）——推迟到发版。
8. 在 `fix/npm-audit` 分支上恰好一次本地提交，包含：重新生成的 `internal/frontend/package-lock.json`、重建的 `internal/frontend/dist/`、双语 spec、双语审查记录、README 索引行。提交**不推送**。

### 目录结构

```text
internal/frontend/
  package.json          （未变：caret 范围 ^6.0.0 / ^8.0.8 仍被解析版本满足）
  package-lock.json     （重新生成：锁定 echarts 6.1.0、vite 8.1.4、传递依赖升级）
  dist/                 （由 `npm run build` 重建并提交——embed.go 嵌入 dist/*，须与升级后的 echarts 一致）
```

不修改其他任何文件。不涉及 Go 源码、代理、证书、配置、CI 文件。

### 升级增量（dry-run 与最终 lockfile 已确认）

| 包 | 起 | 止 | 是否直接依赖 |
| --- | --- | --- | --- |
| echarts | 6.0.0 | 6.1.0 | 是（dependency） |
| zrender | 6.0.0 | 6.1.0 | 传递（echarts） |
| vite | 8.0.8 | 8.1.4 | 是（devDependency） |
| tinyglobby | 0.2.16 | 0.2.17 | 传递（vite） |
| rolldown | 1.0.0-rc.15 | 1.1.5 | 传递（vite） |
| @rolldown/pluginutils | 1.0.0-rc.15 | 1.0.1 | 传递（vite） |
| @rolldown/binding-linux-x64-gnu | 1.0.0-rc.15 | 1.1.5 | 传递（vite，平台相关） |
| @oxc-project/types | 0.124.0 | 0.139.0 | 传递（vite） |
| postcss | 8.5.10 | 8.5.16 | 传递（vite） |
| picomatch | 4.0.4 | 4.0.5 | 传递（vite） |
| nanoid | 3.3.11 | 3.3.15 | 传递（vite） |
| @tybys/wasm-util | 0.10.1 | 0.10.3 | 传递（rolldown） |
| @napi-rs/wasm-runtime | 1.1.4 | 1.1.6 | 传递 |
| @emnapi/wasi-threads | 1.2.1 | 1.2.2 | 传递 |
| @emnapi/runtime | 1.9.2 | 1.11.1 | 传递 |
| @emnapi/core | 1.9.2 | 1.11.1 | 传递 |
| @rolldown/binding-linux-x64-musl | 1.0.0-rc.15 | 1.1.5 | 传递（vite，平台可选依赖） |

### 约束

1. 使用 `npm audit fix`（不加 `--force`）。两个修复都在现有 caret 范围内；不允许跨 major。
2. 不要手改 `package.json` 的 caret 范围，除非触发回退路径（vite `^8.0.16`）。范围 `^6.0.0` 和 `^8.0.8` 对解析版本仍然有效。
3. 不要运行 `npm audit fix --force`——它会将 echarts/vite 推过 major 边界，明确超出范围。
4. 提交 `package-lock.json` **以及重建的 `internal/frontend/dist/`**。`node_modules/` 被 gitignore。**`dist/` 必须提交**——见上文"嵌入式前端约束"。
5. 不要推送提交。在 `fix/npm-audit` 上本地提交并等待明确确认（按项目约定：迭代期间只提交）。
6. 升级不得修改 `src/` 下任何源文件。`DashboardView.vue` 中的 echarts 用法保持原样；若 6.1 引入 API 变化，修复方式是最小源码补丁，而非重写。
7. 所有命令在 `internal/frontend/` 工作目录下执行（或用 `npm --prefix internal/frontend`）。

### 边界情况

1. vite 8.0 → 8.1 minor 升级后 `npm run build` 失败——回退：在 `package.json` 设置 `"vite": "^8.0.16"`，重新 `npm install`，重新验证构建。在任务证据中记录回退。
2. echarts 6.1 对双轴图表引入破坏性配置变化——回退：pin `"echarts": "^6.0.0"` 不够（6.0.0 仍有漏洞）；改为仅在存在非漏洞补丁时 pin 到首个非漏洞 6.1.x，否则保持 6.1.0 并在 `DashboardView.vue` 应用最小源码修复。记录具体变化的配置项。
3. `npm test` 在 echarts 懒加载断言上失败——这些断言匹配源码文本（`import('echarts/core')` 等）。只有在源码编辑改变了导入形态时才会失败；先检查实际 diff 再动测试。
4. `npm audit fix` 报告无法完全修复（如 peer 冲突）——停止，记录确切信息，**不要**继续 `--force`；上报冲突供人工解决。
5. 某个传递依赖升级破坏构建——通过 `npm ls <pkg>` 定位；这些包的传递破坏罕见，但 rolldown rc → stable 是最可能的候选。若 rolldown 1.1.5 破坏，则它卡住了 vite 升级，触发 vite `^8.0.16` 回退。
6. `package-lock.json` 显示比 dry-run 列出的更多变动——只要 `npm audit` 干净且构建/测试通过即可接受；传递依赖树可能因平台而异。
7. 仪表盘图表渲染但 echarts 6.1 后出现视觉回归（颜色、动画、tooltip）——同一主题内的轻微视觉变化可接受；功能性破坏（无序列、轴错误、空白画布）不可接受。

### 非目标

1. 不升级与两条告警无关的任何依赖（`vue`、`vue-router`、`tailwindcss`、`typescript`、`vue-tsc` 等）。
2. 不更换构建工具（不弃用 vite）。
3. 不重构 echarts 集成（懒加载模式、模块注册）——保持原样，除非 6.1 强制要求最小兼容补丁。
4. 不修改任何 Go 源码，包括 `internal/updater`（审查中发现的一个既有 URL 脱敏缺陷另行单独跟踪——见"实际实现结果"）。
5. 不自动推送分支或开 PR——等待明确确认。
6. 不修改 CI 工作流、发布脚本。

## 任务详情

### 任务 1：通过 npm audit fix 升级依赖

#### 需求

**Objective（目标）** — 通过在现有 caret 范围内将 echarts 升级到 6.1.0、vite 升级到 8.1.4，解决两条安全告警。

**Outcomes（成果）** — `internal/frontend/package-lock.json` 重新生成（echarts 6.1.0、vite 8.1.4、zrender 6.1.0 等传递依赖锁定）；`package.json` caret 范围不变；`npm audit` 报告 0 漏洞。

**Evidence（证据）** — `npm audit` 输出显示 "found 0 vulnerabilities"；在 `package-lock.json` 中 `grep` 确认 `echarts@6.1.0` 和 `vite@8.1.4`。

**Constraints（约束）** — 只用 `npm audit fix`，绝不 `--force`。在 `internal/frontend/` 内执行。除非触发回退路径，否则不要手改 `package.json` 范围。

**Edge Cases（边界）** — `npm audit fix` 无法完全解决（peer 冲突）——记录信息并停止。出现意外的 major 升级提议——拒绝并人工检查。

**Verification（验证）** — `npm audit` 报告 0 漏洞；解析版本与增量表一致。

#### 计划

1. 确保工作目录是 `internal/frontend/`（npm 前缀根）。
2. 记录基线确认当前状态：
   ```bash
   npm audit 2>&1 | tail -20          # 预期：2 vulnerabilities（1 moderate, 1 high）
   ```
3. 执行范围内修复：
   ```bash
   npm audit fix
   ```
4. 重新运行 audit 确认已解决：
   ```bash
   npm audit 2>&1 | tail -5           # 预期："found 0 vulnerabilities"
   ```
5. 验证解析版本已写入 lockfile：
   ```bash
   grep -A1 '"node_modules/echarts"' package-lock.json   # 预期："version": "6.1.0"
   grep -A1 '"node_modules/vite"' package-lock.json      # 预期："version": "8.1.4"
   ```
6. 检查 diff 范围（预期 `package-lock.json` 被修改；`package.json` 未被修改）：
   ```bash
   git status --short
   git diff --stat
   ```

#### 验证

- [ ] `npm audit` 报告 0 漏洞。
- [ ] `package-lock.json` 中 `echarts` 解析为 6.1.0。
- [ ] `package-lock.json` 中 `vite` 解析为 8.1.4。
- [ ] `package.json` 未被修改（只有 `package-lock.json` 被改）。

### 任务 2：构建与单元测试验证

#### 需求

**Objective（目标）** — 确认升级后的 echarts/vite 不破坏生产构建和现有前端单元测试，并重新生成内嵌的 `dist/`。

**Outcomes（成果）** — `npm run build` 成功并重新生成 `dist/`；`npm test` 全部通过，尤其是 echarts 懒加载断言。

**Evidence（证据）** — 构建输出以成功的包摘要结尾（无错误）；测试运行器报告全部通过，包括 `DashboardUsageRequests.test.ts` 中关于 echarts 懒加载的用例。

**Constraints（约束）** — 在 `internal/frontend/` 内执行。重新生成的 `dist/` 在任务 4 中暂存提交（因 `embed.go` 嵌入它，必须提交）。若构建因 vite minor 升级失败，先触发任务 1 边界情况中的回退（pin vite `^8.0.16`）再重试。

**Edge Cases（边界）** — 升级后 TypeScript/Vue 类型检查失败——检查是否某个传递 `@types` 或 `typescript` 行为变化（typescript 本身不直接升级；仅在传递类型包变动时）。echarts 源码文本断言失败——检查 `src/views/DashboardView.vue` 的 diff（应为空）。

**Verification（验证）** — 构建成功；全部测试通过。

#### 计划

1. 运行生产构建：
   ```bash
   npm run build
   ```
2. 确认 `dist/` 已重新生成：
   ```bash
   ls -la dist/                       # 预期：index.html + assets/ 存在，mtime 较新
   ```
3. 运行单元测试：
   ```bash
   npm test
   ```
4. 专门确认 echarts 懒加载断言通过：
   ```bash
   node --test --experimental-strip-types "src/views/DashboardUsageRequests.test.ts" 2>&1 | tail -20
   ```
   预期断言仍然成立（匹配未变更的源码文本）：
   - 存在 `import type { EChartsType } from 'echarts/core'`
   - 存在 `import('echarts/core')`、`import('echarts/charts')`、`import('echarts/components')`、`import('echarts/renderers')`
   - 不存在 `import * as echarts from 'echarts'`（整包导入）

#### 验证

- [ ] `npm run build` 退出码 0，无错误。
- [ ] `dist/` 已重新生成。
- [ ] `npm test` 全部通过。
- [ ] `DashboardUsageRequests.test.ts` 中 echarts 懒加载断言通过。

### 任务 3：echarts 运行时渲染验证（推迟到发版）

#### 需求

**Objective（目标）** — 验证 echarts 6.0 → 6.1 minor 升级不导致 Usage Overview 双轴折线图运行时回归，因为单元测试断言源码文本但不渲染像素。

**Outcomes（成果）** — Usage Overview 图表在有数据时正常渲染（双 Y 轴 7 条折线序列、tooltip、legend），无数据时正常渲染居中空状态文本 graphic；无 echarts 控制台错误。

**Evidence（证据）** — 运行中的管理面板（本地后端 + 前端）展示 Usage 标签；图表画布正确显示趋势线；切换到空数据范围显示居中空状态文本；浏览器控制台无 echarts 错误。

**Constraints（约束）** — 这是手动验证步骤，因为图表由 canvas 渲染且未做像素测试。验收标准是功能性正确（序列渲染、轴存在、tooltip 可用、空状态显示）；主题内的轻微视觉偏移可接受。

**Edge Cases（边界）** — 无用量数据（全新安装）——验证空状态 graphic 路径（`setOption` 中的 `graphic: { type: 'text', ... }`）。后端未运行——本地启动，或跳过并记录说明（构建+测试已通过，运行时检查推迟到发版）。

**Verification（验证）** — 图表在有/无数据时均渲染正常；无 echarts 控制台错误。

#### 计划

1. 本地启动后端（使 `/api/usage/...` 返回趋势数据），然后运行前端开发服务器：
   ```bash
   npm run dev                         # 在 internal/frontend/ 下
   ```
   （若后端已通过项目的运行流程启动，则只需开发服务器。若本地全栈不可用，回退到 `npm run build` 后的 `npm run preview`，但注意 API 数据需要后端。）
2. 在浏览器打开管理面板，进入 **Usage** 标签 → **Overview** 子标签。
3. 有用量数据时确认：
   - 双 Y 轴折线图渲染全部 7 条序列（provider_requests_total、failed_requests、Input、Output、Cache Create、Cache Read、usage_coverage）。
   - 左轴 = provider 请求总量；右轴 = usage coverage（0–100%）。
   - 悬停 tooltip 显示轴值；legend 可切换序列；颜色匹配主题强调色板。
4. 切换时间范围 / provider 筛选以产生空结果集，确认：
   - 画布清空并出现居中"empty"文本 graphic（`t('usage.empty')`）。
5. 打开浏览器控制台，确认渲染或筛选变化期间**无 echarts 错误或告警**。
6. 在任务证据中记录验证结果（通过 / 推迟并说明原因）。

#### 验证

- [ ] 有数据时双轴折线图正常渲染（7 序列、双轴、tooltip、legend）。
- [ ] 无数据时居中空状态文本 graphic 正常渲染。
- [ ] 浏览器控制台无 echarts 错误/告警。
- [ ] 结果已记录（通过，或推迟到发版并说明理由）。

### 任务 4：工作区清理与本地提交

#### 需求

**Objective（目标）** — 暂存交付文件（lockfile + 重建的 dist + 双语 spec + 双语审查记录 + README 索引），并在 `fix/npm-audit` 上创建一次本地提交，不推送。

**Outcomes（成果）** — `fix/npm-audit` 分支上一次提交，diff 为：重新生成的 `internal/frontend/package-lock.json`、重建的 `internal/frontend/dist/`（所有 chunk hash 变化）、双语 spec、双语审查记录、README 索引行。`package.json` 不在提交中。`node_modules/` 未暂存。提交仅本地。

**Evidence（证据）** — `git show --stat HEAD` 列出 lockfile、dist chunk 变化和文档文件；提交后 `git status` 干净；"未执行推送"证明未推送。

**Constraints（约束）** — 按项目约定，本地提交，用户确认前不推送。**提交重建的 `dist/`**（因 `embed.go` 嵌入它——见"嵌入式前端约束"）。不 amend 或 rebase 无关历史。提交信息遵循仓库的 conventional-commit 风格（`fix(deps): ...`）。

**Edge Cases（边界）** — `dist/` 显示为已修改/未跟踪——这是预期的；暂存它（**不要** restore，否则 `make build` 会嵌入过时前端）。误暂存 `package.json`——用 `git restore --staged internal/frontend/package.json` 取消暂存（它不应出现在提交中）。提交误带无关文件——`git reset --soft HEAD~1` 后只重新暂存预期集合。

**Verification（验证）** — `git show --stat HEAD` 显示 lockfile + dist + 文档，且**不含** `package.json`；工作树干净；提交仅本地（未推送）。

#### 计划

1. 确认变更全集：
   ```bash
   git status --short
   git diff --stat
   ```
   预期：
   - `M internal/frontend/package-lock.json`
   - `M internal/frontend/dist/...`（重建的 chunk hash）
   - `M sdd-docs/features/README.md`
   - `sdd-docs/features/2026-07-10-npm-audit-fix/` 下的 spec + review-notes
   - `package.json` 未修改
2. 显式暂存交付物（不要 `git add -A`，避免杂散文件）：
   ```bash
   git add internal/frontend/package-lock.json internal/frontend/dist/ \
       sdd-docs/features/README.md \
       sdd-docs/features/2026-07-10-npm-audit-fix/
   ```
3. 验证已暂存集合：
   ```bash
   git diff --cached --stat             # 预期 lockfile + dist + 文档；不含 package.json
   ```
4. 创建提交（conventional-commit 风格；参考近期历史）：
   ```bash
   git commit -m "fix(deps): 升级 echarts 6.1.0、vite 8.1.4 修复 npm audit 漏洞"
   ```
5. 确认提交内容及其为本地提交：
   ```bash
   git show --stat HEAD
   git status --short                   # 预期：干净
   ```
6. **不要推送。** 向用户报告提交哈希和摘要，等待确认后再推送 / 开 PR。

#### 验证

- [ ] `git show --stat HEAD` 列出 `package-lock.json` + `dist/` + spec + review-notes + README。
- [ ] `package.json` 不在提交中。
- [ ] 提交后 `git status --short` 干净。
- [ ] 提交仅本地；未执行 `git push`。
- [ ] 分支 `fix/npm-audit` 待用户明确确认后即可推送/开 PR。

---

## 实际实现结果

2026-07-10 执行。记录实际结果与验证证据。

### 实际结果

- **任务 1——已完成。** `npm audit fix` 将 echarts 6.0.0 → 6.1.0（zrender 6.0.0 → 6.1.0）、vite 8.0.8 → 8.1.4。`npm audit` 报告 **0 漏洞**。`package.json` 未被修改。
- **任务 2——已完成。** `npm run build` 成功（Vite 8.1.4）；`npm test` 通过 **158/158**，含 `DashboardUsageRequests.test.ts` 的 echarts 懒加载断言。新鲜 `npm ci` + `npm run build` 产生零 git diff，证明提交的 `dist/` 可由 lockfile 复现。
- **任务 3——推迟到发版。** 本轮未执行（需完整本地栈）。生产构建正常产出 echarts chunk（charts/components/graphic/renderers），源码形态断言通过；canvas 渲染 / tooltip / legend / 空状态仍为发版前浏览器检查项。
- **任务 4——已完成。** 当前分支 tip 的提交含 `package-lock.json` + 重建的 `dist/` + 双语 spec + 双语审查记录 + README 索引。`package.json` 不在提交中。未推送。

### Go 测试验证

`go test ./... -count=1` 通过（1358 项），`make test`（带 `-race`）通过。运行此步以确认新嵌入的 `dist/` 未引入 Go 侧回归（因 `embed.go` 嵌入 `dist/*`）。

一个测试 `internal/updater.TestDownloadAndApplyRedactsInvalidDownloadURL` 在完整并发 `go test ./...` 中偶发失败。调查：这是**既有缺陷，非本 PR 引入**（`git diff e901d2a..HEAD -- internal/updater/` 为空）。它单独跟踪如下——**不是**本次依赖升级的回归。

### 单独跟踪：既有 updater URL 脱敏缺陷

审查中发现 `internal/updater.TestDownloadAndApplyRedactsInvalidDownloadURL` 既是非 hermetic 的 flaky 测试，**也是**真实的 URL 脱敏缺陷：`updater.go` 直接返回 `http.Client.Do` 的原始错误，下载在网络层失败时该错误可能包含请求的 query string（`?token=secret`）。强制使用不可达 HTTPS 代理可稳定复现。这不属于本次纯依赖 PR 的范围（`internal/updater` 无 diff），但必须在**独立**的安全变更中修复：返回/记录网络层错误前统一脱敏，并把测试改为 hermetic。见 `review-notes.md` 发现 5。已记入记忆，避免被误当成可忽略的普通 flaky。

### 补充说明

- lockfile 将升级后的 Vite/Rolldown 子树下载源从 `registry.npmmirror.com` 改为 `registry.npmjs.org`。安装成功；这是分发/维护注意事项，不是安全发现。
- 应用注册的是 `LineChart` 而非告警涉及的 `LinesChart`，故现有图表配置不可达该 ECharts XSS 路径——升级属纵深防御。
- 双语审查归档（`review-notes.md` / `review-notes_ZH.md`）随 spec 一并提交。
