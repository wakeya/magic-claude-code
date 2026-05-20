# Status

**状态:** shipped
**创建日期:** 2026-05-20
**完成日期:** 2026-05-20

## 说明

拦截 `/v1/messages/count_tokens` 端点，本地估算 token 数后直接返回。避免 Claude Code 会话启动时 68+ 次无意义的上游请求，同时让上下文窗口管理正常工作。
