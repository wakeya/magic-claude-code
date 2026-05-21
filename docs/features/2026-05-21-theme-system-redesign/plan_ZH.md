# 全站主题系统改造计划

**目标：** 为管理端前端构建可复用的全栈 Light/Dark 主题基础能力，第一阶段先以会话记录页作为试点，再把主题系统推广到整个 Dashboard。

**架构：** 第一阶段新增前端主题状态、语义化主题 token，并改造会话记录页组件。后端 API 保持不变。第二阶段将主题开关迁移到应用 Header，通过后端配置存储持久化偏好，应用全局 `data-theme` 属性，并把语义化 token 推广到共享 Dashboard 表面。

**技术栈：** Vue 3、TypeScript、Tailwind CSS v4、Go 管理端 API、现有 SQLite 配置存储、`localStorage` 兜底。

---

## 第一阶段：会话记录页试点

### 文件

修改：

1. `internal/frontend/src/styles/main.css`
2. `internal/frontend/src/components/SessionBrowser.vue`
3. `internal/frontend/src/components/SessionDetail.vue`
4. `internal/frontend/src/components/SessionOutline.vue`
5. `internal/frontend/src/components/SessionBrowserLayout.test.ts`

必要时新增：

1. `internal/frontend/src/composables/useTheme.ts`
2. `internal/frontend/src/utils/theme.test.ts`

### 实现任务

1. 新增轻量主题 composable，支持 `light` 和 `dark`。
2. 使用 `localStorage` 保存用户选择。
3. 在会话记录页根节点应用主题 class 或 data attribute。
4. 在 CSS 中定义会话记录页语义化主题 token。
5. 为 Light/Dark 分别重塑左侧面板、会话卡片、详情头部、大纲、空状态、清理弹窗。
6. 用户消息块在两种主题下都保持绿色高亮，但使用不同的 light/dark token。
7. 清理命令块在两种主题下都保持代码编辑器风格。
8. 在会话记录页头部提供显式主题开关。
9. 验证切换主题不会重置当前项目和当前会话状态。
10. 运行前端测试和构建。

### 验证命令

```bash
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

### 人工验证

1. 打开管理 UI。
2. 进入 `会话记录`。
3. 切换 Light 和 Dark。
4. 确认当前项目和当前会话不会被重置。
5. 确认会话列表滚动、详情滚动、大纲滚动、导出按钮、清理提示仍可用。
6. 刷新页面后确认主题选择保持。
7. 检查桌面和窄屏布局。

## 第二阶段：全栈全局主题系统

### 后端文件

修改：

1. `internal/config/config.go`
2. `internal/config/sqlite_store.go`
3. `internal/config/store.go`
4. `internal/admin/server.go`
5. `internal/admin/handler.go` 或新增聚焦的 `internal/admin/preferences_handler.go`

新增/修改测试：

1. `internal/admin/preferences_handler_test.go`
2. `internal/config/sqlite_store_test.go`
3. `internal/config/store_test.go`

### 前端文件

修改：

1. `internal/frontend/src/composables/useTheme.ts`
2. `internal/frontend/src/composables/useTheme.test.ts`
3. `internal/frontend/src/composables/useApi.ts`
4. `internal/frontend/src/components/AppHeader.vue`
5. `internal/frontend/src/components/SessionBrowser.vue`
6. `internal/frontend/src/views/DashboardView.vue`
7. `internal/frontend/src/views/LoginView.vue`
8. `internal/frontend/src/styles/main.css`
9. 必要的现有前端布局/源码测试

### 后端任务

1. 在 `config.Config` 中增加 `AdminThemeMode string`，字段标记为 `json:"admin_theme_mode"`。
2. 统一规范化主题值，只接受 `light` 或 `dark`，默认 `light`。
3. 在 SQLite `settings` 表中持久化 `admin_theme_mode`。
4. 保持 JSON 配置存储的兼容性。
5. 新增受认证保护的 API：
   - `GET /api/preferences`
   - `PUT /api/preferences`
6. 非法主题值返回 `400`；未登录访问继续通过现有认证中间件返回 `401`。
7. 不改变与主题无关的配置、供应商、会话 API 行为。

### 前端任务

1. 将 `useTheme` 从会话记录页局部状态提升为全局应用主题状态。
2. 在 `document.documentElement` 或顶层 app root 上应用 `data-theme="light|dark"`。
3. 启动时先从 `localStorage` 同步读取主题，避免首屏闪烁。
4. 进入已认证 Dashboard 后请求 `/api/preferences`，应用后端主题，并同步写入 `localStorage`。
5. 用户切换主题时，立即应用选择、写入 `localStorage`，再通过 `PUT /api/preferences` 持久化。
6. 如果后端持久化失败，保留本地选择，并在下次应用加载时重试或重新同步。
7. 将可见主题开关迁移到 `AppHeader.vue` 右侧，靠近语言切换和退出登录按钮。
8. 移除 `SessionBrowser.vue` 中的临时主题开关。
9. 将会话记录页主题样式改为继承全局主题状态。
10. 将共享语义化 token 应用到登录页、Header、Tabs、Dashboard 面板、供应商、证书、使用统计、表单、弹窗、表格、徽标和代码块。
11. 切换主题时保持页面状态不变。

### API 契约

```http
GET /api/preferences
200 OK
{
  "theme_mode": "light"
}
```

```http
PUT /api/preferences
Content-Type: application/json

{
  "theme_mode": "dark"
}

200 OK
{
  "success": true,
  "theme_mode": "dark"
}
```

非法输入：

```http
PUT /api/preferences
{"theme_mode":"system"}

400 Bad Request
{"error":"invalid theme_mode"}
```

### 验证命令

```bash
rtk go test ./...
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
rtk docker compose up -d --build
```

### 人工验证

1. 登录后确认 Header 右侧靠近语言/退出区域有主题开关。
2. 从 Header 将 Light 切换为 Dark。
3. 在所有 Dashboard Tab 之间切换，确认主题持续生效。
4. 刷新后确认恢复后端持久化主题。
5. 清空 `localStorage` 后重新登录，确认仍能从后端恢复主题偏好。
6. 临时模拟偏好 API 失败，确认 `localStorage` 兜底仍能应用最后一次本地主题。
7. 确认切换主题不会重置当前 Tab、打开弹窗、选中供应商、选中会话或可滚动面板。
8. 检查桌面和窄屏布局。
