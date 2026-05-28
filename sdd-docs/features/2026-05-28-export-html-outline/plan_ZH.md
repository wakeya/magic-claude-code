# Plan

## Step 1: 修改 Go 数据结构

**文件：** `internal/session/export.go`

在 `ExportHTML()` 中：

1. 新增 `OutlineItem` 结构体：`Index int`, `Preview string`, `Timestamp int64`
2. 新增 `outlineItems` 辅助函数：遍历 `Messages`，筛选 role 为 `user` 的消息，生成 `OutlineItem` 列表
3. 修改 `outlineItems` 的 `Preview` 逻辑：`strings.ReplaceAll(strings.Join(strings.Fields(content), " "), ...)`，截取前 50 字符
4. 在模板数据 `map[string]any` 中注入 `"OutlineItems": outlineItems(detail.Messages)`
5. 在模板函数中添加 `previewText` 函数
6. 在模板函数中添加 `formatTime` 函数（格式 `YYYY-MM-DD HH:mm:ss`）

**Commit:** `feat: add OutlineItem struct and template data for export outline`

---

## Step 2: 修改 HTML 模板 — 结构与样式

**文件：** `internal/session/export.go`（`exportTemplate` 常量）

1. 在 `<body>` 中，`<header>` 后增加 `<div class="layout">` 包裹 `<main>`
2. 将现有 `<main>` 部分包裹在 `<main>` 标签内
3. 在 `<main>` 后增加 `<aside class="outline-panel">`（大屏可见）
4. 在 `<header>` 后增加 `<button class="outline-toggle">`（小屏浮动按钮）
5. 在 `<aside>` 后增加 `<dialog class="outline-modal">`（小屏弹窗）
6. 在 `</body>` 前增加 `<script>` 标签
7. 在 CSS 部分增加大纲相关样式（outline-panel、outline-title、outline-item、outline-active、back-to-top、outline-toggle、outline-modal 等）

**Commit:** `feat: add outline panel HTML and CSS to export template`

---

## Step 3: 修改消息 section 添加锚点 ID

**文件：** `internal/session/export.go`（`exportTemplate` 常量）

在 `{{range .Messages}}` 循环的 `<section class="message {{.Role}}">` 中添加 `id="msg-{{.Index}}"`。

注意：Go 模板中 `range` 会改变 `.` 的作用域，需要使用 `$index` 变量记录原始索引。

**Commit:** `feat: add anchor IDs to exported message sections`

---

## Step 4: 修改模板添加大纲数据渲染

**文件：** `internal/session/export.go`（`exportTemplate` 常量）

在 `<aside class="outline-panel">` 中使用 Go 模板 `{{range .OutlineItems}}` 渲染大纲条目，每个条目：
- `onclick` 调用 `jumpToMsg('msg-{Index}')`
- 显示 `{{previewText .Preview}}`（前 50 字符）
- 显示时间戳

在 `<dialog class="outline-modal">` 中同样渲染大纲条目。

**Commit:** `feat: render outline items in export template`

---

## Step 5: 添加交互 JS

**文件：** `internal/session/export.go`（`exportTemplate` 常量，`<script>` 标签内）

实现三个功能：

1. **点击跳转** — `jumpToMsg(id)`：调用 `document.getElementById(id).scrollIntoView({ behavior: 'smooth' })`，跳转后关闭小屏弹窗
2. **滚动高亮** — `IntersectionObserver` 监听所有 `section[id^="msg-"]`，当元素进入视口时在对应大纲条目添加 `.outline-active` 类
3. **回到顶部** — `backToTop()`：调用 `window.scrollTo({ top: 0, behavior: 'smooth' })`
4. **小屏弹窗** — 浮动按钮点击切换 `<dialog>` 的 `showModal()` / `close()`

**Commit:** `feat: add interactive JS for outline navigation and back-to-top`

---

## Step 6: 测试验证

**操作：**

1. 运行 `go test ./internal/session/...` 确保现有测试通过
2. 手动导出几个不同大小的会话 HTML 文件
3. 在浏览器中打开，验证：
   - 大纲面板显示正确
   - 点击跳转、平滑滚动正常
   - 滚动高亮正常
   - 回到顶部正常
   - 响应式（小屏弹窗）正常
   - 亮色/暗色主题正常
4. 检查文件大小增长是否在预期范围内

**Commit:** `test: verify exported HTML outline functionality`

---

## 涉及文件

| 文件 | 操作 |
|------|------|
| `internal/session/export.go` | 修改 |
