# 会话详情增强 — 状态

**日期**: 2026-05-22
**状态**: 已发布

## 实现摘要

全部 21 项需求（R001–R021）已实现并验证。

## 测试结果

- Go 测试套件: 2 个包共 221 个测试通过
- 前端构建: 成功（Vite 550–590ms）
- Accept-Encoding 修复未新增测试文件（通过手动测试 MiniMax API 验证）

## 性能影响

| 指标 | 变更前 | 变更后 |
|------|--------|--------|
| 会话列表扫描 | ~3520 行读取 (head/tail) | ~3520 行读取（无变化） |
| 会话详情加载 | 完整文件解析 | 完整文件解析 + `message_count` 字段（可忽略） |
| 代理上游 SSE | 透传 | 剥离 `Accept-Encoding`；无压缩开销 |

初始的 `countMessages` 实现（扫描时读取全部 41,685 行）已被识别并在同一会话内回退。净性能影响为零。

## 手动验证

- [x] JSONL 文件名显示在正确位置（项目路径和时间戳之间）
- [x] 复制按钮复制完整 `source_path`，显示绿色对勾 1.2 秒
- [x] assistant 消息显示蓝色左边框
- [x] tool/system 消息显示琥珀色左边框
- [x] 选择会话后侧栏消息计数更新为准确值
- [x] 图标按钮在 light 和 dark 主题下均可见
- [x] GitHub 链接出现在登录页（右上角）和 app header（theme toggle 左侧）
- [x] 两处 GitHub 链接均在新标签页打开
- [x] MiniMax SSE usage 提取：所有请求均返回 `usage_source=provider, parse_status=ok`
- [x] Zhipu GLM SSE usage 提取：仍正常工作（无回归）
- [x] 大请求（100+ 消息）正确提取 MiniMax usage 数据
