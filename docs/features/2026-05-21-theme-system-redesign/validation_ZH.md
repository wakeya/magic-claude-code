# 全站主题系统改造验证记录

## 自动化验证

第一阶段实现后运行：

```bash
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
```

2026-05-21 结果：

```text
rtk npm --prefix internal/frontend test
结果：6 passed, 0 failed

rtk npm --prefix internal/frontend run build
结果：Vite production build succeeded
```

容器验证：

```text
rtk docker compose up -d --build
结果：前端生产构建完成，claude_code_proxy_dns 容器成功重启
```

## 第二阶段验证

2026-05-21 第二阶段结果：

```text
rtk go test ./...
结果：288 passed in 8 packages

rtk npm --prefix internal/frontend test
结果：30 passed, 0 failed

rtk npm --prefix internal/frontend run build
结果：Vite production build succeeded

rtk docker compose up -d --build
结果：容器已重新构建并启动
```

后端验证：

- [x] `GET /api/preferences` 返回已持久化的 `theme_mode`。
- [x] `PUT /api/preferences` 接受 `light` 和 `dark`。
- [x] `PUT /api/preferences` 对非法值返回 `400`。
- [x] 偏好接口必须要求现有管理员 session cookie。
- [x] `admin_theme_mode` 通过 SQLite 配置存储持久化。

前端验证：

- [x] Header 主题开关位于语言/退出登录控件附近。
- [x] 会话记录页局部主题开关已移除。
- [x] 切换主题时全局 `data-theme` 更新。
- [x] 清空 `localStorage` 后仍能从后端恢复主题偏好。
- [x] 偏好 API 失败时 `localStorage` 兜底可用。
- [x] 切换主题不会重置当前 Tab、供应商/会话选择或可滚动面板。
- [x] 登录页、状态页、供应商、证书、使用统计、会话记录、弹窗、表单、表格、徽标和代码块在两种主题下均可读。

## 人工验证清单

- [x] 刷新后会话记录页根据持久化偏好打开。
- [x] 主题开关可以在 Light 和 Dark 之间切换。
- [x] 刷新后主题选择保持。
- [x] 切换主题不会重置当前项目。
- [x] 切换主题不会重置当前会话。
- [x] 左侧会话列表仍然可以独立滚动。
- [x] 桌面端大纲仍然可以独立滚动。
- [x] 清理命令弹窗在 Dark 模式下清晰可读。
- [x] 已打开的 Dark 模式详情页中，用户消息块醒目且可读。
- [x] 导出操作仍在会话详情工具栏可用。
- [x] 窄屏视口仍然可用。

## 视觉审核记录

实现试点后记录观察：

```text
2026-05-21 浏览器冒烟验证：
- 登录 https://localhost:8442 并打开会话记录页。
- 确认 Light/Dark 显式开关可见。
- 切换到 Dark 模式，并确认 localStorage 保存 claude-proxy-theme=dark。
- 刷新后重新打开会话记录页，Dark 偏好保持。
- 打开会话详情页，切换主题时当前项目和当前会话保持稳定。
- 在 Dark 模式打开清理命令弹窗，带语法着色的命令块可读。

仍待产品负责人视觉审核：
- Light 模式产品口味完整检查。
```

```text
2026-05-21 第二阶段浏览器验证：
- 容器重新构建后登录 https://localhost:8442。
- 确认 Header 层级的 Light/Dark 开关位于语言/退出登录旁边。
- 切换到 Dark 模式，并确认 documentElement data-theme=dark。
- 确认 localStorage 保存 claude-proxy-theme=dark。
- 清空 localStorage 后刷新，确认后端偏好恢复 Dark 模式并重新写入 localStorage。
- 进入会话记录页并打开会话详情。
- 确认会话记录页局部主题开关已移除。
- 确认 Dark 模式下用户消息块为绿色背景且可读。
- 打开清理提示弹窗，确认命令块为深色代码编辑框风格，并带语法高亮 token。
- 确认桌面端大纲面板具备受限高度和 overflow-y:auto。
- 检查窄屏视口，并调整外壳/Header 避免布局破裂。
```
