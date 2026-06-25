# 全局回到顶部悬浮按钮 规格

本地页面：管理面板（`DashboardView.vue`）  
代理入口：无（管理服务 :8442）  
参考源站：内部前端 —— `DashboardView.vue`、`SessionBrowser.vue`、`styles/main.css`、`useI18n.ts`  
技术栈：Vue 3（Composition API）+ Tailwind CSS v4  
最后更新：2026-06-24  
进度：3 / 3 已规划，全部实现并验证

## 整体分析（源站分析）

### 现状

"回到顶部"悬浮按钮目前仅存在于 `SessionBrowser.vue`（117–120 行）：

```html
<button class="session-icon-button fixed bottom-5 right-5 z-50 shadow-md"
        :title="t('sessions.back_to_top')" @click="scrollToTop">
  <ArrowUp class="h-4 w-4" />
</button>
```

`scrollToTop`（334–335 行）调用 `window.scrollTo({ top: 0, behavior: 'smooth' })`。

该按钮使用 `session-icon-button` 样式，已在 `main.css`（285–296 行）全局定义，包含 hover/focus-visible 状态。定位为 `fixed bottom-5 right-5 z-50 shadow-md`，固定在视口右下角。

其他 tab（服务状态 / 供应商管理 / 连接模式 / 证书信息 / 使用统计）内容同样会超出视口高度，但**没有**回到顶部的交互入口。会话记录 tab 的左侧列表有内部滚动容器（`max-h-[calc(100vh-260px)] overflow-y-auto`，SessionBrowser.vue:28），但右侧面板和所有其他 tab 均通过 `window` 滚动。

### 目标

将 back-to-top 按钮从 `SessionBrowser` 移出，在 `DashboardView` 层级作为**全局**悬浮按钮，对**每个** tab 生效——页面滚动到底部时出现在右下角。同时移除 `SessionBrowser` 中已冗余的固定 back-to-top 按钮（保留 outline modal 内部的 back-to-top，那是独立的交互上下文）。

### 设计概要

1. **全局放置** — 在 `DashboardView.vue` 模板中添加 `fixed bottom-5 right-5 z-30` 按钮，位于 `<div v-if="activeTab === ...">` 块之外、弹窗/遮罩之前。
2. **显示条件** — 响应式 `scrolled = window.scrollY > 100`（100px 阈值避免微小滚动闪烁）。通过 `onMounted` 注册 `window` 的 `scroll` 事件监听，`onBeforeUnmount` 清理。
3. **行为** — `window.scrollTo({ top: 0, behavior: 'smooth' })`，与现有实现相同。
4. **样式** — 复用 `session-icon-button` + `fixed bottom-5 right-5 z-30 shadow-md`（`main.css` 已全局定义）。图标：lucide-vue-next 的 `ArrowUp`。
5. **i18n** — 复用 `sessions.back_to_top` 或新建通用 key `common.back_to_top`。现有 key 中英文均已就绪；新建 key 语义更干净但需更新 `useI18n.ts`。
6. **SessionBrowser 清理** — 移除 `SessionBrowser.vue` 中 `fixed bottom-5 right-5 z-50` 的 back-to-top 按钮（117–120 行）。保留 outline modal 内部的 back-to-top 按钮（129–131 行），它服务于不同的 UX 上下文（关闭弹窗 + 滚动到顶）。

### 风险小结

1. 全局按钮出现在**所有** tab，包括证书信息（矮内容 tab）。在证书信息 tab 上 `window.scrollY` 始终为 0 → 按钮始终隐藏 → 无视觉干扰。✓
2. 会话记录的内部滚动容器（`max-h-[calc(100vh-260px)] overflow-y-auto`）独立于 `window` 滚动。全局按钮不响应内部容器的滚动 —— 这是正确且可接受的行为：内部容器有其自己的滚动上下文。
3. `session-icon-button` 已在 `main.css` 全局定义 → 无需新增样式。✓
4. LoginView 在 `DashboardView` 之外 → 不受影响。✓

## 开发检查清单

| 顺序 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | Planned | 全局 back-to-top 按钮放入 `DashboardView` | `DashboardView.vue` | 构建；滚动 status/providers tab 按钮可见 |
| 2 | Planned | 移除 `SessionBrowser` 中的逐 tab 按钮 | `SessionBrowser.vue` | 构建；无重复按钮；outline modal 按钮保留 |
| 3 | Planned | 测试、构建与手动验证 | 测试/构建通过；验证记录 | `npm test` + `npm run build`；跨 tab 滚动矩阵 |

## 需求

### 交付物

1. `DashboardView.vue` 新增全局 `fixed bottom-5 right-5 z-30` back-to-top 按钮，仅在 `window.scrollY > 100` 时显示。
2. 按钮调用 `window.scrollTo({ top: 0, behavior: 'smooth' })`，使用现有 `session-icon-button` + `ArrowUp` 图标。
3. `SessionBrowser.vue` 移除其 `fixed bottom-5 right-5 z-50` back-to-top 按钮（117–120 行）。outline modal 内部的 back-to-top 按钮（129–131 行）保留。
4. i18n key 决策记录：复用 `sessions.back_to_top` 或在 `useI18n.ts` 新建 `common.back_to_top`（spec 推荐新建，语义更干净）。
5. 前端测试和构建通过；无新增警告。

### 约束

1. 按钮在短页面/短 tab（`window.scrollY` 为 0，如证书信息 tab）上不得出现。
2. `scroll` 事件监听在 `onBeforeUnmount` 中清理，避免内存泄漏。
3. 按钮不得与 `SessionBrowser` outline modal 的移动端 back-to-top 按钮重叠（不同 z-index 上下文：modal `z-40`，全局按钮 `z-30`；modal 作为遮罩打开，全局按钮在其后方）。
4. 100px 阈值用于避免微小滚动时的闪烁；为 UX 决策，后续可调整。

### 边界条件

1. 短 tab（证书信息）→ `scrollY` 保持 0 → 按钮始终不显示。
2. 用户滚动到长 tab 底部（如会话记录）→ 按钮出现；点击平滑滚动到顶部。
3. 用户打开 outline modal（会话记录）→ 全局按钮在 modal 遮罩后方（z-30 按钮 vs z-40 modal 遮罩）；modal 自身的 back-to-top 按钮是当前活跃入口。
4. 从滚动的 tab 切换到短 tab → `scrollY` 重置为 0 → 按钮隐藏（浏览器在 `v-if` 内容切换时自动重置滚动位置；`scroll` 事件触发隐藏按钮）。
5. macOS overlay 滚动条 → `window.scrollTo` 正常工作。

## 任务详情

### 任务 1：全局 back-to-top 按钮放入 DashboardView

#### 需求

**Objective（目标）** — 在 `DashboardView` 新增视口固定的 back-to-top 按钮，对所有 tab 生效，页面滚动到底部时出现。

**Outcomes（成果）** — `DashboardView.vue` 模板包含一个 `fixed bottom-5 right-5 z-50` 按钮，使用 `session-icon-button` + `ArrowUp`，通过 `v-show="scrolled"` 控制显示，`scrolled` 为响应式 ref，由 `window` 的 `scroll` 事件监听更新。按钮调用 `window.scrollTo({ top: 0, behavior: 'smooth' })`。

**Evidence（证据）** — 前端构建通过；手动在 status/providers tab 滚动后按钮出现；点击后平滑滚动到顶部。

**Constraints（约束）** — `onMounted` 注册 `window` 的 `scroll` 监听，`onBeforeUnmount` 移除；阈值 100px；i18n key 复用或新建（记录）。

**Edge Cases（边界）** — 短 tab（按钮隐藏）；tab 切换重置滚动；outline modal 遮罩上下文。

**Verification（验证）** — 构建；手动跨 tab 滚动确认按钮行为。

#### 计划

1. 在 `DashboardView.vue` 导入 `ArrowUp`（已有 lucide 导入）。
2. script setup 新增 `const scrolled = ref(false)` 和滚动事件处理函数。
3. `onMounted` 注册 `window.addEventListener('scroll', ...)`，`onBeforeUnmount` 移除。
4. 在模板中 tab 内容容器之后、弹窗之前插入按钮。
5. i18n：复用 `sessions.back_to_top` 或在 `useI18n.ts` 新增 `common.back_to_top`。

#### 验证

- [ ] 滚动 status/providers/connection/usage/sessions tab 时按钮出现。
- [ ] 证书信息 tab 上按钮不出现（内容适配视口）。
- [ ] 点击按钮平滑滚动到页面顶部。
- [ ] 组件卸载时 scroll 监听被清理。

### 任务 2：移除 SessionBrowser 中的逐 tab 按钮

#### 需求

**Objective（目标）** — 移除 `SessionBrowser.vue` 中已冗余的全局 back-to-top 按钮，避免重复入口。

**Outcomes（成果）** — `SessionBrowser.vue` 中 `fixed bottom-5 right-5 z-50` 按钮（117–120 行）被移除。outline modal 内部的 back-to-top 按钮（129–131 行）保留。

**Evidence（证据）** — 前端构建通过；会话记录 tab 不出现重复 back-to-top 按钮；outline modal 的 back-to-top 仍可用。

**Constraints（约束）** — 仅移除固定全局按钮；不移除 outline modal 内部的按钮；`scrollToTop` 函数在 outline modal 仍引用时保留。

**Edge Cases（边界）** — outline modal 打开时其自身的 back-to-top 按钮是活跃入口，不受全局按钮移除影响。

**Verification（验证）** — 构建；手动会话记录 tab 只有全局按钮（来自 DashboardView）；outline modal 按钮正常。

#### 计划

1. 移除 `SessionBrowser.vue` 117–120 行（`<button class="session-icon-button fixed bottom-5 right-5 z-50 shadow-md" ...>`）。
2. 检查 `scrollToTop` 是否仍被 outline modal（129 行）引用 → 若是则保留函数；否则移除。
3. 若 `ArrowUp` 导入在移除后不再被引用（仅被移除的按钮和 outline modal 使用），确认后再决定是否移除导入。

#### 验证

- [ ] 会话记录 tab 不出现重复 back-to-top 按钮。
- [ ] outline modal 的 back-to-top 按钮仍可用。
- [ ] `scrollToTop` 函数在被引用处保留。

### 任务 3：测试、构建与手动验证

#### 需求

**Objective（目标）** — 验证全局 back-to-top 在所有 tab 上正确工作，无回归。

**Outcomes（成果）** — `npm --prefix internal/frontend test` 和 `npm --prefix internal/frontend run build` 通过；手动验证矩阵记录完成。

**Evidence（证据）** — 测试/构建绿色输出；验证清单完成。

**Constraints（约束）** — 现有测试通过；新增源码级断言：`DashboardView` 包含全局按钮、`SessionBrowser` 不再有固定按钮。

**Verification（验证）** — 完整矩阵。

#### 验证矩阵

- [ ] 服务状态 tab：滚动 → 按钮出现 → 点击 → 滚动到顶部。
- [ ] 供应商管理 tab：同上。
- [ ] 连接模式 tab：同上。
- [ ] 使用统计 tab：同上。
- [ ] 证书信息 tab：按钮不出现（内容适配视口）。
- [ ] 会话记录 tab：滚动后按钮出现；无重复按钮；outline modal 按钮正常。
- [ ] `npm --prefix internal/frontend test` 通过。
- [ ] `npm --prefix internal/frontend run build` 通过。
