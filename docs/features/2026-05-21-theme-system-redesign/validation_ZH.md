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

## 第二阶段计划验证

全栈推广实现后运行：

```bash
rtk go test ./...
rtk npm --prefix internal/frontend test
rtk npm --prefix internal/frontend run build
rtk docker compose up -d --build
```

后端必须验证：

- [ ] `GET /api/preferences` 返回已持久化的 `theme_mode`。
- [ ] `PUT /api/preferences` 接受 `light` 和 `dark`。
- [ ] `PUT /api/preferences` 对非法值返回 `400`。
- [ ] 偏好接口必须要求现有管理员 session cookie。
- [ ] `admin_theme_mode` 通过 SQLite 配置存储持久化。

前端必须验证：

- [ ] Header 主题开关位于语言/退出登录控件附近。
- [ ] 会话记录页局部主题开关已移除。
- [ ] 切换主题时全局 `data-theme` 更新。
- [ ] 清空 `localStorage` 后仍能从后端恢复主题偏好。
- [ ] 偏好 API 失败时 `localStorage` 兜底可用。
- [ ] 切换主题不会重置当前 Tab、打开弹窗、供应商/会话选择或可滚动面板。
- [ ] 登录页、状态页、供应商、证书、使用统计、会话记录、弹窗、表单、表格、徽标和代码块在两种主题下均可读。

## 人工验证清单

- [x] 刷新后会话记录页根据持久化偏好打开。
- [x] 主题开关可以在 Light 和 Dark 之间切换。
- [x] 刷新后主题选择保持。
- [x] 切换主题不会重置当前项目。
- [x] 切换主题不会重置当前会话。
- [ ] 左侧会话列表仍然可以独立滚动。
- [x] 桌面端大纲仍然可以独立滚动。
- [x] 清理命令弹窗在 Dark 模式下清晰可读。
- [x] 已打开的 Dark 模式详情页中，用户消息块醒目且可读。
- [ ] 导出操作仍能下载 HTML。
- [ ] 窄屏视口仍然可用。

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
- Light 模式完整视觉检查。
- 导出下载行为。
- 窄屏视口检查。
```
