# Status

**状态:** shipped
**创建日期:** 2026-05-15
**完成日期:** 2026-05-18

## 说明

新增用量统计系统，包含请求日志记录、SSE 流式解析、Session Log 同步、前端统计页面。

## 2026-06-11 更新

- 使用统计页新增 `清除数据` 操作。
- 新增 `POST /api/usage/clear`，默认清除 `usage_requests` 和 `usage_tokens`，保留 `session_log_sync`。
- 清除弹窗新增 `同时重置 Session Log 同步状态` 选项，用于迁移 data 目录、更换系统或更换 session 日志目录后重新建立当前机器的 Session Log 同步状态。
- 清除操作不会删除本地 Claude Code JSONL 文件，不影响会话记录页读取 JSONL。
- 自动化验证通过：`go test ./... -count=1`、`npm --prefix internal/frontend test`、`npm --prefix internal/frontend run build`。

## 生命周期

```text
draft -> approved -> planned -> implementing -> implemented -> validating -> validated -> shipped
```

当前位置：

```text
shipped
```
