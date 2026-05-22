# 会话详情增强

**日期**: 2026-05-22
**状态**: 已发布

## 概述

对会话浏览器功能的一组增量改进，聚焦于会话详情可见性、UI 一致性、性能和项目品牌展示。

## 需求

### 1. 会话详情页展示 JSONL 文件路径

- **R001**: 会话详情头部必须在项目路径和时间戳之间显示 JSONL 源文件名。
- **R002**: 文件名使用 CSS `truncate` 防止溢出，hover 时通过 `:title` 属性显示完整路径。
- **R003**: 文件名旁必须有复制按钮，点击后将完整 `source_path` 写入剪贴板。
- **R004**: 点击复制后，按钮图标变为绿色对勾，1.2 秒后恢复。
- **R005**: 切换会话时，复制状态必须重置。

### 2. 按消息角色添加彩色左边框

- **R006**: assistant 消息必须有 4px 蓝色左边框（`--session-accent`），与导出 HTML 风格一致。
- **R007**: system 和 tool 消息必须有 4px 琥珀色左边框（`--session-technical-border: #f59e0b`），与导出 HTML 风格一致。
- **R008**: user 消息保持现有边框样式（绿色边框 `--session-user-border`）。

### 3. 准确的消息计数

- **R009**: 会话列表 API（`/api/sessions`）返回基于 head/tail 行采样的近似消息计数（性能优化）。
- **R010**: 会话详情 API（`/api/sessions/:id`）返回准确的 `message_count` 字段，值为 `len(messages)`。
- **R011**: 用户选择会话时，前端必须用详情 API 返回的准确值更新侧栏显示的消息计数。
- **R012**: 扫描阶段不得为了消息计数读取完整 JSONL 文件——必须保持快速，仅使用 head/tail 采样。

### 4. 图标按钮可见性

- **R013**: `.session-icon-button` 元素（如回到顶部、复制按钮）必须有可见的默认背景，使用 `var(--session-border)`，而非透明。
- **R014**: 背景在 light 和 dark 主题下都必须可辨识。

### 5. GitHub 仓库链接

- **R015**: 登录页右上角必须显示 GitHub 图标链接，指向项目仓库。
- **R016**: 主页面 header 中 theme toggle 左侧必须显示 GitHub 图标链接。
- **R017**: 两处链接必须在新标签页打开（`target="_blank"`, `rel="noopener noreferrer"`）。
- **R018**: GitHub SVG 图标必须内联（除已有的 lucide-vue-next 外，不依赖外部图标库）。

### 6. SSE Usage 提取 — Accept-Encoding 干扰修复

- **R019**: 代理在转发请求到上游 provider 前，必须剥离 `Accept-Encoding` 和 `TE` 请求头。
- **R020**: 剥离后，上游 SSE 响应必须以明文（非 gzip 压缩）形式传递给 SSEObserver。
- **R021**: 所有上游 provider 必须通过 SSEObserver 正确返回 usage 数据，无论它们在收到 `Accept-Encoding` 时是否压缩 SSE 响应。

## 涉及文件

| 文件 | 变更 |
|------|------|
| `internal/proxy/handler.go` | 转发上游前剥离 `Accept-Encoding` 和 `TE` 头 |
| `internal/frontend/src/components/SessionBrowser.vue` | JSONL 路径展示、复制逻辑、侧栏计数更新 |
| `internal/frontend/src/components/SessionDetail.vue` | 无变更（CSS 类已应用） |
| `internal/frontend/src/components/AppHeader.vue` | GitHub 图标链接 |
| `internal/frontend/src/views/LoginView.vue` | GitHub 图标链接 |
| `internal/frontend/src/composables/useApi.ts` | `SessionDetailResponse` 新增 `message_count` |
| `internal/frontend/src/styles/main.css` | 彩色边框、图标按钮背景、技术边框变量 |
| `internal/session/types.go` | `SessionDetail` 新增 `MessageCount` 字段 |
| `internal/session/scanner.go` | 移除 `countMessages`，恢复基于 window 的计数 |
| `internal/admin/session_handler.go` | 在详情和导出 handler 中设置 `MessageCount: len(messages)` |
