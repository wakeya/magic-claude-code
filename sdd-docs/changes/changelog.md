# Changelog

所有重要变更记录在此文件中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号为发布日期（无语义化版本，因为项目没有发布 tag）。

---

## 2026-05-29

### Added
- 识别 DeepSeek "thinking must be passed back" 和 "tool_use without tool_result" 两种新的 400 错误模式，支持被动重试恢复
- 非流式 >= 400 错误日志增加兼容性相关 headers 信息（`Anthropic-Version`、`Anthropic-Beta`、`Content-Type`）

### Fixed
- 修正待修复问题文档结构，新增 `sdd-docs/known-issues/` 目录记录未解决的兼容性问题

### Docs
- 新增 DeepSeek 模型在 Claude Code >= 2.1.150 中报 400 错误的待修复记录
- 将 `docs/superpowers/plans/` 合并到 `sdd-docs/superpowers/plans/`，清理空 `docs/` 目录
- 新增 `sdd-docs/changes/changelog.md`

---

## 2026-05-28

### Added
- 导出 HTML 增加右侧大纲导航面板（基于 IntersectionObserver 的滚动高亮）
- 导出 HTML 增加返回顶部按钮
- 导出 HTML 小屏幕下大纲面板切换为底部浮动按钮 + 弹窗模式
- Session Browser 大纲标题增加条目数量显示
- Session Browser 大纲标题支持中英文本地化

### Fixed
- Session Browser 返回顶部按钮在所有屏幕尺寸下可见
- Session Browser 返回顶部按钮改为 fixed 定位

---

## 2026-05-21

### Added
- Session Browser 项目目录折叠、标题重命名、UI 优化、跨平台路径修复
- 管理后台全局亮/暗色主题系统（CSS 变量 + 偏好持久化）
- 导出 HTML 主题与管理面板一致（暗色模式下导出暗色 HTML）
- Session Browser 大纲面板返回顶部按钮

### Fixed
- 主题 tooltip 溢出和 Session Browser 滚动问题
- Usage 覆盖率提示样式统一
- 移除冗余的 Session Browser 选择提示

---

## 2026-05-20

### Added
- 非标准 content block（如 `tool_reference`）反应式清理，修复第三方供应商 400 错误
- 请求日志分页总条数、状态页问号提示

### Fixed
- Usage 日期过滤和请求行展示修复

---

## 2026-05-18

### Added
- Claude Session Browser（浏览、搜索、导出会话）
- 管理后台前端重构为 Vue 3 + TypeScript + Tailwind CSS（Flat Design）
- 前端国际化支持，默认中文，支持中英文切换
- SSE 流心跳机制，防止上游空闲时连接超时

---

## 2026-05-15

### Added
- SQLite 配置存储（替代 JSON 文件）
- Usage 统计 dashboard（API + 前端图表）
- 流式 usage 解析与记录
- 反应式供应商兼容性错误恢复（400 错误自动清理重试）
- 供应商 thinking 支持开关

---

## 2026-05-13

### Added
- 硬编码端点拦截，覆盖 Claude Code 源码中所有 API 端点
- 供应商复制配置、Token 明文查看
- 统一代理请求日志格式（reqID 关联入口/出口）

### Fixed
- 保留 Claude Code Anthropic 协议字段
- 低优先级端点拦截增强

---

## 2026-05-11

### Added
- 管理后台 REST API 配置服务
- bcrypt 认证 + Session Token

---

## 2026-05-10

### Added
- 透明代理服务（Header 转发）
- CA 证书生成与管理
- 服务器证书生成与管理
- JSON 配置存储
- Docker 部署（Alpine）
