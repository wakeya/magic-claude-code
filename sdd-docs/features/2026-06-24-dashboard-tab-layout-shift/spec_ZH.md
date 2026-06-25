# 管理面板标签页切换布局抖动修复 规格

本地页面：管理面板（`DashboardView.vue`）标签页切换  
代理入口：无（管理服务 :8442）  
参考源站：内部前端 —— `DashboardView.vue`、`SessionBrowser.vue`、`SessionDetail.vue`、`AppHeader.vue`、`styles/main.css`  
技术栈：Vue 3（Composition API）+ Tailwind CSS v4  
最后更新：2026-06-24  
进度：0 / 4 已规划

## 整体分析（源站分析）

### 现象（两类不同的视觉缺陷）

在管理面板的六个标签页（服务状态 / 供应商管理 / 连接模式 / 证书信息 / 使用统计 / 会话记录）之间切换时，用户观察到两类独立的视觉缺陷：

1. **平稳左移一个滚动条宽度** —— 可接受但确实存在。发生在 证书信息/会话记录 与其他 tab 切换时。这是浏览器垂直滚动条出现/消失（Windows/Linux 约 15px）引发的单次 reflow。
2. **明显的"重绘感"抖动** —— 真正的诉求。**每次**从四个稳定 tab（服务状态 / 供应商管理 / 连接模式 / 使用统计）切到 会话记录 时都会出现，视觉上像会话记录页面被重绘了一次。

### 根因一 —— 滚动条 reflow（缺陷 1）

- `body`（`main.css:20`）和 `html` 只设置了字体/背景/颜色。全项目搜索**没有** `overflow-y: scroll`，也**没有** `scrollbar-gutter`。→ 浏览器采用默认的"按需滚动条"行为。
- `AppHeader.vue:2` 是文档流内的 `<header class="flex flex-wrap items-center justify-between">`（非 `fixed`/`sticky`），其宽度直接等于 `视口 − 内边距`，因此视口宽度变化约 15px 时，左/中/右三组重新分布 → 可见位移。`w-fit` 的标签块位于 `mx-auto` 容器内（`DashboardView.vue:5-6`），其 auto 边距也会随视口宽度偏移。
- 按 `html` 是否出现垂直滚动条，六个 tab 分两组：
  - **A 组（无滚动条，内容 ≤ 视口）**：服务状态、供应商管理、连接模式、使用统计、证书信息。
  - **B 组（有滚动条）**：会话记录 —— `SessionDetail.vue:2` **没有** `max-h`/`overflow` 高度限制，全量渲染整段会话，选中任一会话即撑高 `html`、强制出现滚动条。
- 跨组切换（A↔B）使滚动条状态翻转 → reflow → 位移；同组切换不翻转 → 不抖。这完全解释了观察到的分组。

### 根因二 —— 会话记录的异步二次布局（缺陷 2）

- 六个 tab 全部用 `v-if`（`DashboardView.vue:22/123/170/378/405/757`），切走即**销毁**组件，切回即**重建**。
- 四个稳定 tab 的数据在 `DashboardView.onMounted` 通过 `Promise.all([loadStatus(), loadProviders(), loadCerts(), loadConnectionMode()])` + `loadUsageData()` 预加载（`DashboardView.vue:1707-1708`）。激活时内容已就位 → 单帧渲染 → 无二次布局。
- **会话记录是唯一**由子组件自行取数的 tab：`SessionBrowser.onMounted → reload()`（`SessionBrowser.vue:227-229`）发起 `getSessionProjects()` + `loadSessions()`（即 `getSessionList`）（`SessionBrowser.vue:235-236`，两次网络往返）。由于 `v-if`，**每次**激活都是全新挂载 → 全新请求 → 列表从空到满 → 二次布局 → "重绘"抖动。"每次都抖、第二次也抖"正是 `v-if` 销毁重建 + 每次挂载重新取数的标志性特征。

### 会话记录的结构性差异

| 维度 | 四个稳定 tab | 会话记录 |
| --- | --- | --- |
| 数据来源 | `DashboardView.onMounted` 预加载 | 子组件 `SessionBrowser.onMounted` 自行取数 |
| 激活时 | 数据就绪，单帧渲染 | 空 → 取数 → 填充，二次布局 |
| `html` 滚动条 | 由内容高度决定，稳定组不翻转 | 选中会话后强制出现（`SessionDetail` 无高度限制） |

### 选定方案（三层）

1. **第一层 —— `html { scrollbar-gutter: stable }`**：消除缺陷 1。预留稳定的 gutter，使 `html` 宽度恒定，与滚动条是否出现无关。
2. **第二层 —— 会话列表数据预加载到 `DashboardView`（B2）**：让会话记录与其他 tab 对齐 —— `projects` + 初始 `sessions` 列表在 `DashboardView.onMounted` 期间取数，通过 props 传给 `SessionBrowser`，激活时单帧渲染列表（无空→满二次布局）。`SessionDetail` 仍按需加载（点击会话时），这是预期的交互。
3. **第三层 —— 会话列表骨架屏**：用轻量骨架（Tailwind `animate-pulse`）替换当前的纯文字加载态（`SessionBrowser.vue:33`），兜底首次加载（预加载未完成）和用户触发的刷新/切换项目，避免空→满跳变。项目当前无骨架屏先例，故为新建但最小化。

### 风险小结

1. `scrollbar-gutter: stable` 会在短内容页（LoginView、稳定 tab）右侧留约 15px 同色 gutter —— 显示 `--app-bg`，与 `.app-shell` 右边缘同色，视觉上几乎不可见。项目无 `100vw`/`w-screen` 用法，不会触发横向溢出陷阱。
2. macOS（overlay 浮动滚动条）下 `scrollbar-gutter` 不起作用（no-op），不改变任何表现。
3. 把会话数据上提到 `DashboardView` 会改变 `SessionBrowser` 的数据归属（自管理 `projects`/`sessions` → props）。按项目共享状态字段规范，这些字段的所有读取方（`reload`、`loadSessions`、`selectProject`、`totalSessions`、模板列表渲染）必须同步一致更新。
4. 预加载失败不得影响其他 tab；会话记录需优雅降级（骨架 → 错误态）。
5. `SessionDetail` 的无高度限制全量渲染**不在本次范围**（不引入虚拟滚动/分页）—— 那是另一个性能话题。其引发的滚动条由第一层处理。

## 开发检查清单

| 顺序 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | Planned | 第一层 —— 稳定滚动条 gutter | `internal/frontend/src/styles/main.css` | 构建；确认无横向滚动条；macOS 无变化 |
| 2 | Planned | 第二层 —— `DashboardView` 预加载会话列表 | `DashboardView.vue`、`SessionBrowser.vue` | 单元测试；手动 稳定tab → 会话记录 无重绘 |
| 3 | Planned | 第三层 —— 会话列表骨架屏 | `SessionBrowser.vue`（或新建 `SessionListSkeleton.vue`） | 构建；首次加载显示骨架，无空→满跳变 |
| 4 | Planned | 测试、构建与手动验证 | 测试/构建通过；验证记录 | `npm test` + `npm run build`；跨 tab 切换矩阵 |

## 需求

### 交付物

1. 在 `internal/frontend/src/styles/main.css` 增加 `html { scrollbar-gutter: stable }`，使文档宽度无论是否出现垂直滚动条都保持恒定。
2. `DashboardView.onMounted` 在现有 `Promise.all` 中预加载会话列表数据（`getSessionProjects()` + `getSessionList({ page: 1, page_size: 100 })`），并暴露 `sessionProjects` / `sessionList` / `sessionsLoading` 响应式状态。
3. `SessionBrowser` 通过 props 接收 `projects` / `sessions` / `loading`（初始数据来自父组件预加载），且**不再**在自身 `onMounted` 自动调用 `reload()`。保留 `reload()`（刷新按钮）、`loadSessions()`、`selectProject()`、`selectSession()`（按需详情）、导出、清理等用户交互逻辑。
4. 用骨架占位（Tailwind `animate-pulse`）替换会话列表区的纯文字加载态，骨架高度接近最终列表高度，避免骨架→列表跳变。
5. 前端单元测试（`npm --prefix internal/frontend test`）与构建（`npm --prefix internal/frontend run build`）通过；现有测试（`SessionBrowserLayout.test.ts`、`DashboardUsageRequests.test.ts` 等）不被破坏。
6. 手动验证确认：稳定tab ↔ 稳定tab 不抖（回归）；稳定tab → 会话记录 无重绘抖动；证书信息 ↔ 会话记录 仅剩（已 gutter 稳定的）过渡；首次加载显示骨架；macOS 无变化。

### 目录结构

```text
internal/frontend/src/
  styles/
    main.css                          （修改：新增 html { scrollbar-gutter: stable }）
  views/
    DashboardView.vue                 （修改：预加载会话列表、传 props、接入骨架）
  components/
    SessionBrowser.vue                （修改：接收 props、移除 onMounted 自动 reload、骨架屏）
    SessionListSkeleton.vue           （新建：轻量列表骨架，可选独立组件）
```

### 数据模型

无持久化数据模型变更。仅在 `DashboardView.vue` 新增前端响应式状态：

```ts
// DashboardView.vue (script setup)
const sessionProjects = ref<SessionProject[]>([])
const sessionList = ref<SessionItem[]>([])
const sessionsLoading = ref(false)

// onMounted：扩展现有 Promise.all
await Promise.all([
  loadStatus(),
  loadProviders(),
  loadCerts(),
  loadConnectionMode(),
  loadSessionsList(),   // 新增：填充 sessionProjects + sessionList
])
```

`SessionBrowser.vue` props：

```ts
const props = defineProps<{
  projects: SessionProject[]
  sessions: SessionItem[]
  loading: boolean
}>()
```

### 约束

1. 第一层对 `html` 使用 `scrollbar-gutter: stable`（而非 `overflow-y: scroll`），避免短页面出现空的滚动条轨道。
2. 第二层只预加载会话**列表**（`projects` + 初始 `sessions`）。`SessionDetail`（完整会话）仍按需加载（用户点击会话时）—— 预加载所有详情不在范围且无必要。
3. 数据归属变更后，`SessionBrowser` 必须保持所有现有交互可用：刷新按钮（`reload`）、切换项目（`selectProject` → 重新加载该项目列表）、选中会话（`getSessionDetail`）、导出、清理提示、复制路径、回到顶部、大纲跳转。
4. 当 `SessionBrowser` 自行刷新/切换项目（用户触发）时，更新其本地响应式副本，并可 emit `refreshed` 事件让父组件保持同步；父组件预加载仅为**初始**数据源。
5. 会话预加载失败须隔离处理：`sessionsLoading=false`，在 会话记录 内显示错误态，不影响其他 tab。
6. 骨架须预留接近真实列表的高度（如在现有 `max-h-[calc(100vh-260px)]` 容器内放若干占位行），使骨架→列表过渡本身不引起布局跳变。
7. 不引入全局状态 store（pinia）；数据经 props/事件流动，与现有 composable + 组件本地状态架构一致。
8. `SessionDetail` 的无高度限制渲染与虚拟滚动明确不在范围。

### 边界条件

1. 用户首次打开 会话记录 时预加载尚未完成 → 骨架显示至数据到达；无空→满跳变。
2. 会话预加载失败（API 错误）→ 会话记录 内显示错误态；不影响其他 tab。
3. 无会话/无项目 → 空态如常渲染。
4. 首次加载网络慢 → 骨架持续；数据到达后单帧填充。
5. 用户在 会话记录 内点击刷新/切换项目 → 本地重新加载并显示骨架/加载态；通过事件同步父组件状态。
6. macOS overlay 滚动条 → `scrollbar-gutter` 不起作用；无视觉变化、无回归。
7. `fixed inset-0` 弹窗（ProviderModal、更新对话框、导入预览）→ 按 CSS 规范，`position: fixed` 的初始包含块不受 `scrollbar-gutter` 影响；弹窗仍铺满视口。
8. LoginView（短页面）→ 右侧 gutter 显示 `--app-bg`，与 shell 背景同色；几乎不可见。
9. 主题切换（浅色/深色）→ gutter 颜色随 `--app-bg` 主题变量；无错配。

### 非目标

1. 不把 `SessionDetail` 改为限高/内部滚动/虚拟滚动 —— 属另一性能话题；其引发的滚动条由第一层处理。
2. 不把各 tab 从 `v-if` 改为 `v-show`/`KeepAlive` —— 第二层（预加载）已在不动共享 tab 机制的前提下解决 会话记录 的根因。
3. 不把 `AppHeader` 改为 `fixed`/`sticky`。
4. 不引入 pinia 或全局 store。
5. 不预加载所有会话的 `SessionDetail`。

## 任务详情

### 任务 1：稳定滚动条 gutter

#### 需求

**Objective（目标）** — 消除滚动条 reflow（缺陷 1），使文档宽度与垂直滚动条是否出现无关、保持恒定。

**Outcomes（成果）** — `internal/frontend/src/styles/main.css` 新增 `html { scrollbar-gutter: stable; }`。在 证书信息/会话记录 与任意 tab 之间切换时，头部和标签块不再横向位移。

**Evidence（证据）** — 前端构建通过；DevTools 显示在短内容与长内容 tab 间切换时 `document.documentElement.clientWidth` 不再变化。

**Constraints（约束）** — 使用 `scrollbar-gutter: stable`（而非 `overflow-y: scroll`）以避免空滚动条轨道；不移除现有 `.app-shell { overflow-x: clip }`。

**Edge Cases（边界）** — macOS overlay 滚动条（no-op，无变化）；短页面显示同色 gutter；`fixed inset-0` 弹窗不受影响。

**Verification（验证）** — 构建；手动跨 tab 切换确认无横向位移；macOS 无变化。

#### 计划

1. 在 `internal/frontend/src/styles/main.css` 为 `html` 新增 `scrollbar-gutter: stable;`（放在顶部基础样式区，`.app-shell` 之前）。
2. 确认无 `100vw`/`w-screen` 用法（已验证 —— 无）。
3. 构建前端并目视确认。

#### 验证

- [ ] `main.css` 中存在 `html { scrollbar-gutter: stable }`。
- [ ] 任何 tab 都不出现横向滚动条。
- [ ] 证书信息 ↔ 会话记录 不再使头部/标签块横向位移。
- [ ] macOS 无变化（overlay 滚动条）。

### 任务 2：在 DashboardView 预加载会话列表

#### 需求

**Objective（目标）** — 通过在 `DashboardView.onMounted` 预加载会话列表，消除会话记录激活时的重绘（缺陷 2），使激活 会话记录 时单帧渲染列表。

**Outcomes（成果）** — `DashboardView.vue` 持有 `sessionProjects` / `sessionList` / `sessionsLoading`，并在现有 `onMounted` `Promise.all` 中预加载。`SessionBrowser.vue` 接收 `projects` / `sessions` / `loading` props，移除 `onMounted` 自动 `reload()`，以 props 数据作为初始列表源。稳定tab → 会话记录 不再重绘。

**Evidence（证据）** — 前端单元测试通过；手动从稳定 tab 切到 会话记录，列表即时显示，无空→满二次布局。

**Constraints（约束）** — 仅预加载列表（`projects` + 初始 `sessions`）；`SessionDetail` 保持按需；保留 `SessionBrowser` 全部交互（刷新、切换项目、选中会话、导出、清理、复制、回到顶部、大纲）；一致更新此前自管理 `projects`/`sessions` 字段的所有读取方（`totalSessions`、模板列表、`reload`、`loadSessions`、`selectProject`）；不引入全局 store。

**Edge Cases（边界）** — 首次打开时预加载未完成（由任务 3 骨架兜底）；预加载失败（隔离错误态）；空项目/会话；用户触发的刷新/切换项目更新本地状态并经事件同步父组件。

**Verification（验证）** — 单元测试通过；手动 稳定tab → 会话记录 无重绘；刷新/切换项目仍可用。

#### 计划

1. 在 `DashboardView.vue` script setup 新增 `sessionProjects` / `sessionList` / `sessionsLoading` ref，以及 `loadSessionsList()`：调用 `api.getSessionProjects()` + `api.getSessionList({ project: '', page: 1, page_size: 100 })`，设置加载/错误态。
2. 将 `loadSessionsList()` 加入现有 `onMounted` `Promise.all`（约 1707 行）。
3. 向 `<SessionBrowser />`（约 758 行）传 `:projects="sessionProjects" :sessions="sessionList" :loading="sessionsLoading"`，并监听 `@refreshed` 以更新父组件状态。
4. 在 `SessionBrowser.vue` 中将 `projects`/`sessions` 改为 props（保留从 props 初始化的本地 ref 用于交互），移除 `onMounted(() => void reload())`（227-229 行），保留 `reload()`/`loadSessions()`/`selectProject()` 基于本地状态工作并 emit `refreshed`。
5. 审计此前自管理 `projects`/`sessions` 的每个读取方（`totalSessions` computed、列表 `v-for`、`selectProject`、`loadSessions`），改用基于 props 的本地状态。

#### 验证

- [ ] `DashboardView.onMounted` 预加载会话列表。
- [ ] `SessionBrowser` 不再在挂载时自动 `reload()`。
- [ ] 稳定tab → 会话记录 单帧渲染列表（无重绘）。
- [ ] 刷新按钮、切换项目、选中会话、导出、清理均仍可用。
- [ ] `totalSessions` 与列表渲染使用基于 props 的状态。

### 任务 3：会话列表骨架屏

#### 需求

**Objective（目标）** — 在残留的首次加载窗口及用户触发的刷新期间，用骨架占位防止空→满列表跳变。

**Outcomes（成果）** — 用 Tailwind `animate-pulse` 骨架替换会话列表区的纯文字加载态（`SessionBrowser.vue:33`）。骨架在现有 `max-h-[calc(100vh-260px)]` 容器内预留接近真实列表的高度。

**Evidence（证据）** — 前端构建通过；首次加载（或刷新）时列表区显示脉动占位行而非空白，随后单帧填充。

**Constraints（约束）** — 骨架保持轻量（无新依赖）；预留高度避免骨架→列表跳变；初始加载、刷新、切换项目显示同一骨架；保留现有空态/错误态。

**Edge Cases（边界）** — 网络慢（骨架持续后填充）；空结果（骨架 → 空态，因骨架高度有界不跳变）；网络极快（骨架可能一闪而过 —— 可接受）。

**Verification（验证）** — 构建；手动首次加载与刷新显示骨架且无空→满跳变。

#### 计划

1. 在会话列表容器内新增骨架块（如 6–8 个 `animate-pulse` 圆角条），在 `loading` 为真时显示，替换当前 `<div v-if="loading" class="session-empty-compact">`。
2. 给骨架容器一个接近真实列表的 min-height（复用 `max-h-[calc(100vh-260px)]` 边界）。
3. （可选）将骨架抽到 `components/SessionListSkeleton.vue` 以便复用/清晰。

#### 验证

- [ ] 加载态显示脉动骨架，而非纯文字。
- [ ] 骨架高度接近真实列表（无骨架→列表跳变）。
- [ ] 加载完成后空态与错误态仍正确渲染。

### 任务 4：测试、构建与手动验证

#### 需求

**Objective（目标）** — 确保变更有测试覆盖、构建干净，并按完整切换矩阵手动验证。

**Outcomes（成果）** — `npm --prefix internal/frontend test` 与 `npm --prefix internal/frontend run build` 通过；一份覆盖切换矩阵与边界条件的手动验证记录。

**Evidence（证据）** — 测试/构建绿色输出；验证清单完成。

**Constraints（约束）** — 不破坏现有测试（`SessionBrowserLayout.test.ts`、`DashboardUsageRequests.test.ts`、`DashboardViewImportExport.test.ts`、`DashboardViewListenStatus.test.ts`）；为新的预加载流程与 props 契约新增或调整测试；按项目提交规范只提交相关文件（只 commit，确认前不 push）。

**Edge Cases（边界）** — 预加载失败路径；空会话；macOS overlay 滚动条；主题切换。

**Verification（验证）** — 完整测试 + 构建 + 下方手动矩阵。

#### 计划

1. 运行 `npm --prefix internal/frontend test`；修复 `SessionBrowser` props 变更导致的任何破坏。
2. 新增/调整单元测试，断言 `DashboardView` 预加载会话、`SessionBrowser` 挂载时不再自动取数（如源码级断言 `onMounted` 不再调用 `reload`，参照现有源码断言测试）。
3. 运行 `npm --prefix internal/frontend run build`。
4. 执行手动验证矩阵并记录结果。

#### 验证

- [ ] `npm --prefix internal/frontend test` 通过。
- [ ] `npm --prefix internal/frontend run build` 通过。
- [ ] 手动矩阵：稳定↔稳定 不抖；稳定→会话记录 无重绘；证书信息↔会话记录 gutter 已稳定；首次加载骨架；刷新/切换项目 可用。
- [ ] macOS 无回归。
- [ ] `git status` 仅暂存任务相关文件。
