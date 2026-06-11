# Changelog

所有重要变更记录在此文件中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号为发布日期（无语义化版本，因为项目没有发布 tag）。

---

## 2026-06-11

### Added
- 使用统计页新增快捷日期范围：今日、近 7 天、近 30 天；默认近 7 天，近 7/30 天不包含今天
- 使用统计页新增 `清除数据` 操作，支持默认保留 `session_log_sync`，也支持勾选后重置 Session Log 同步状态
- Admin API 新增 `POST /api/usage/clear`，用于清除 usage 统计数据并可选重置 Session Log 同步状态
- 会话记录清理提示新增 Windows 预览命令和 Windows 交互清理命令
- 统计口径筛选项新增问号提示，解释有效统计、实时请求、Session Log、全部原始
- 编辑供应商弹窗桌面端宽度加宽至约供应商列表内容区的四分之三，提升模型映射和多模态配置编辑体验

### Fixed
- 修复 Usage 覆盖率表格横向内容被遮挡且缺少底部横向滚动条的问题
- 修复 Usage 覆盖率表头提示框被表格区域遮挡的问题，表格内提示改为向下弹出
- 修复 Windows 清理命令路径提示：原生 `C:\Users\<user>\...` 路径保留盘符并替换用户名为 `用户名代理`
- 修复 Windows 清理命令路径中的双引号、控制字符和 Windows 非法路径字符清洗问题，降低复制提示命令后的解析风险

### Docs
- 更新 Usage statistics specs，补充清除统计数据 API、前端交互、迁移场景和验证项
- 更新 Session Browser specs，补充清理提示的 Linux/macOS 与 Windows 双平台命令、Windows 路径转换和安全清洗约束
- 更新 Multimodal Model Switch specs，补充 Provider 弹窗宽度约束

---

## 2026-06-09

### Added
- Provider 配置新增“多模态切换”和“多模态模型 ID”，请求含图片、PDF、音频或视频等非文本内容时可自动切换到指定多模态模型
- 代理请求转换支持递归检测 `messages` / `system` 中的非文本内容，覆盖截图工具返回的 `tool_result.content` 图片
- SQLite Provider 表新增多模态配置字段，并支持旧数据库自动补列
- Admin API 创建、查询、更新、复制 Provider 时保留多模态配置，并校验开启开关时必须填写多模态模型 ID
- Provider 弹窗和卡片增加多模态配置 UI 与提示文案

### Fixed
- 修复 Mimo 文本模型收到截图图片时返回 `No endpoints found that support image input` 的配置层解决路径
- 修复 Session Browser 移动端大纲返回顶部按钮缺少 sticky 底部定位的问题

### Docs
- 新增多模态模型切换 feature specs：`sdd-docs/features/2026-06-09-multimodal-model-switch/`
- 同步英文主文档和中文 `_ZH` 文档：requirements、plan、decisions、validation、status

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
