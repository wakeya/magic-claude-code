# 硬编码端点拦截修正与补全规格

**版本**: 1.0
**日期**: 2026-05-13
**状态**: 已实施

---

## 1. 背景

基于 Claude Code 源码（`/home/www/workspace/claude-code-src/src/src/`）的全量分析，发现代理项目中硬编码端点拦截存在两类问题：

1. **字段名/路径错误**：部分已拦截端点的伪造响应与源码期望不匹配
2. **严重遗漏**：多个上报/遥测接口未被拦截，用户行为数据会泄漏到 Anthropic 或被转发到第三方后端产生错误

### 源码分析方法

通过 `grep -rn "api.anthropic.com\|BASE_API_URL" src/ --include="*.ts"` 全量搜索，逐文件阅读确认每个接口的请求格式、响应期望、失败行为。

---

## 2. Bug 修复（2 项）

### Fix-1: `metrics_enabled` 响应字段名错误

| 项目 | 内容 |
|------|------|
| **文件** | `internal/proxy/hardcoded.go` L143 |
| **问题** | 返回 `"metrics_enabled": false`，但源码期望 `"metrics_logging_enabled"` |
| **源码位置** | `services/api/metricsOptOut.ts` L13: `type MetricsEnabledResponse = { metrics_logging_enabled: boolean }` |
| **影响** | 当前碰巧也能工作（`undefined` fallback 到 `false`），但依赖隐式行为 |
| **修复** | `"metrics_enabled"` → `"metrics_logging_enabled"` |

### Fix-2: `roles` 路径缺少 `s`

| 项目 | 内容 |
|------|------|
| **文件** | `internal/proxy/hardcoded.go` L25 |
| **问题** | 拦截 `/api/oauth/claude_cli/role`（单数），源码实际请求 `/api/oauth/claude_cli/roles`（复数） |
| **源码位置** | `constants/oauth.ts` L93: `ROLES_URL: 'https://api.anthropic.com/api/oauth/claude_cli/roles'` |
| **影响** | roles 请求未被拦截，穿透到第三方后端产生错误响应 |
| **修复** | `"/api/oauth/claude_cli/role"` → `"/api/oauth/claude_cli/roles"` |

---

## 3. 新增拦截（6 项，按优先级排序）

### Add-1: `/api/event_logging/batch` — P0 关键遗漏

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/analytics/firstPartyEventLoggingExporter.ts` L120 |
| **用途** | 第一方事件批量上报（命令使用频率、工具调用、AB 实验数据） |
| **频率** | 每 5 秒或积累 200 条触发一次 |
| **请求方法** | POST |
| **请求体格式** | `{"events": [{"event_type": "ClaudeCodeInternalEvent", "event_data": {...}}]}` |
| **源码期望响应** | HTTP 200（只检查状态码，不检查响应体） |
| **失败行为** | 持久化到磁盘 `~/.claude/telemetry/1p_failed_events.*`，指数退避重试最多 8 次（最大间隔 30 秒），跨进程重试 |
| **伪造响应** | `200 OK` + `{}` |
| **不拦截后果** | 用户行为数据泄漏到第三方后端；失败后反复重试消耗资源 |

### Add-2: GrowthBook 特性开关 `/api/feature/` — P1

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/analytics/growthbook.ts` L504-527 |
| **用途** | 远程特性开关、AB 测试、动态配置（模型选择、速率限制、功能开关） |
| **请求路径** | `/api/feature/{clientKey}`（GrowthBook SDK 自动拼接） |
| **请求方法** | GET（SDK 内部 `client.init()` 发起） |
| **源码期望响应** | GrowthBook SDK 标准格式 `{"features": {...}}` |
| **失败行为** | 等待 5 秒超时 → fallback 到磁盘缓存 → fallback 到默认值。不影响功能但影响启动速度 |
| **伪造响应** | `200 OK` + `{"features": {}}` |
| **匹配规则** | 前缀 `/api/feature/` |
| **不拦截后果** | 每次启动等待 5 秒超时；空 feature set 使客户端快速 fallback 到默认值 |

### Add-3: `/api/claude_cli/bootstrap` — P1

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/api/bootstrap.ts` L63 |
| **用途** | 启动时获取额外模型选项列表等引导配置 |
| **请求方法** | GET |
| **源码期望响应** | `{"client_data": {...}, "additional_model_options": [...]}` |
| **失败行为** | `catch` 后 `return null`，静默跳过 |
| **伪造响应** | `200 OK` + `{}` |

### Add-4: `/mcp-registry/` — P2

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/mcp/officialRegistry.ts` L40 |
| **用途** | 启动时预拉取官方 MCP 服务器 URL 列表，用于判断 MCP 连接是否为官方服务 |
| **请求路径** | `/mcp-registry/v0/servers?version=latest&visibility=commercial` |
| **请求方法** | GET |
| **源码期望响应** | `{"servers": [{server: {remotes: [{url: "..."}]}}]}` |
| **失败行为** | 静默处理，`isOfficialMcpUrl()` 始终返回 `false` |
| **伪造响应** | `200 OK` + `{"servers": []}` |
| **匹配规则** | 前缀 `/mcp-registry/` |

### Add-5: `/v1/mcp_servers` — P2

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/mcp/claudeai.ts` L78 |
| **用途** | 获取用户在 claude.ai 上配置的 MCP 连接器列表 |
| **请求路径** | `/v1/mcp_servers?limit=1000` |
| **请求方法** | GET |
| **请求头** | `Authorization: Bearer {token}`, `anthropic-beta: mcp-servers-2025-12-04` |
| **源码期望响应** | `{"data": [...], "has_more": false}` |
| **失败行为** | 静默返回空 map |
| **伪造响应** | `200 OK` + `{"data": [], "has_more": false}` |

### Add-6: `/api/claude_code_penguin_mode` — P3

| 项目 | 内容 |
|------|------|
| **源码位置** | `utils/fastMode.ts` L370 |
| **用途** | Fast Mode (Penguin Mode) 配置获取 |
| **请求方法** | GET |
| **失败行为** | 静默降级，Fast Mode 不可用 |
| **伪造响应** | `200 OK` + `{}` |

---

## 4. 不应拦截的接口（必须正确代理转发）

| 端点 | 原因 |
|------|------|
| `/v1/messages` | 核心对话接口，必须转发到第三方后端 |
| `/v1/files`, `/v1/files/{id}/content` | 文件上传/下载（Beta 接口） |
| `/v1/sessions`, `/v1/code/sessions/*` | 远程会话管理 |
| `/v1/environments/*` | 远程环境管理 |
| `/v1/code/triggers` | 远程触发器 |
| `wss://*/v1/sessions/ws/*` | WebSocket 远程会话 |

---

## 5. 低优先级可选拦截

这些接口在源码中都有容错（失败不崩溃），不拦截也不会有问题，但拦截可以避免无意义超时：

| 路径 | 用途 | 伪造响应 |
|------|------|---------|
| `/api/oauth/profile` | 用户 profile | `{}` |
| `/api/claude_cli_profile` | CLI profile | `{}` |
| `/api/oauth/account/settings` | 账户设置 | `{}` |
| `/api/oauth/usage` | 用量查询 | `{}` |
| `/api/claude_code/policy_limits` | 策略限制 | `{}` |
| `/api/claude_code/settings` | 远程托管设置 | `{}` |
| `/api/claude_code/user_settings` | 用户设置同步 | `{}` |

---

## 6. 已知不存在但保留的端点

| 路径 | 源码中是否存在 | 处理 |
|------|---------------|------|
| `/v1/me` | 不存在 | 保留无害 |
| `/api/claude_cli_feedback` | 不存在 | 保留无害 |

---

## 7. 修改后的完整匹配规则

```
精确匹配:
  /v1/me                                      # 保留
  /api/claude_cli_feedback                    # 保留
  /api/claude_code_shared_session_transcripts  # 保留
  /api/oauth/claude_cli/create_api_key        # 保留
  /api/oauth/claude_cli/roles                 # Fix-2: role → roles
  /api/claude_code/organizations/metrics_enabled  # Fix-1: 响应字段修正
  /api/event_logging/batch                    # Add-1: 新增
  /api/claude_cli/bootstrap                   # Add-3: 新增
  /v1/mcp_servers                             # Add-5: 新增
  /api/claude_code_penguin_mode               # Add-6: 新增

前缀匹配:
  /api/claude_code/metrics                    # 保留
  /api/claude_code/organization               # 保留
  /api/web/domain_info                        # 保留
  /api/feature/                               # Add-2: 新增 (GrowthBook)
  /mcp-registry/                              # Add-4: 新增 (MCP 注册表)
```

---

## 8. 影响范围

| 变更 | 修改文件 |
|------|---------|
| 匹配规则 + 所有 handler | `internal/proxy/hardcoded.go` |
| 测试用例 | `internal/proxy/hardcoded_test.go` |

`handler.go` 中的 `handleHardcodedEndpoint` 调用逻辑无需变更。

---

## 9. 源码接口完整参考

以下是 Claude Code 源码中通过 `api.anthropic.com` (`BASE_API_URL`) 发起的所有已知接口，供后续维护参考：

### 核心对话
| 路径 | 方法 | 源码文件 |
|------|------|---------|
| `/v1/messages` | POST | `services/api/client.ts` |

### 遥测与上报
| 路径 | 方法 | 源码文件 |
|------|------|---------|
| `/api/claude_code/metrics` | POST | `utils/telemetry/bigqueryExporter.ts` |
| `/api/claude_code/organizations/metrics_enabled` | GET | `services/api/metricsOptOut.ts` |
| `/api/event_logging/batch` | POST | `services/analytics/firstPartyEventLoggingExporter.ts` |

### 功能开关与配置
| 路径 | 方法 | 源码文件 |
|------|------|---------|
| `/api/feature/{key}` | GET | `services/analytics/growthbook.ts` (SDK) |
| `/api/claude_cli/bootstrap` | GET | `services/api/bootstrap.ts` |
| `/api/claude_code_penguin_mode` | GET | `utils/fastMode.ts` |
| `/api/claude_code/policy_limits` | GET | `services/policyLimits/index.ts` |
| `/api/claude_code/settings` | GET | `services/remoteManagedSettings/index.ts` |
| `/api/claude_code/user_settings` | GET/PUT | `services/settingsSync/index.ts` |

### 认证与用户
| 路径 | 方法 | 源码文件 |
|------|------|---------|
| `/api/oauth/claude_cli/create_api_key` | POST | `constants/oauth.ts` |
| `/api/oauth/claude_cli/roles` | GET | `constants/oauth.ts` |
| `/api/oauth/profile` | GET | `services/oauth/getOauthProfile.ts` |
| `/api/claude_cli_profile` | GET | `services/oauth/getOauthProfile.ts` |
| `/api/oauth/account/settings` | GET | `services/api/grove.ts` |
| `/api/oauth/usage` | GET | `services/api/usage.ts` |

### MCP
| 路径 | 方法 | 源码文件 |
|------|------|---------|
| `/mcp-registry/v0/servers` | GET | `services/mcp/officialRegistry.ts` |
| `/v1/mcp_servers` | GET | `services/mcp/claudeai.ts` |

### 文件
| 路径 | 方法 | 源码文件 |
|------|------|---------|
| `/v1/files` | POST | `services/api/filesApi.ts` |
| `/v1/files/{id}/content` | GET | `services/api/filesApi.ts` |

### 会话与远程
| 路径 | 方法 | 源码文件 |
|------|------|---------|
| `/v1/sessions` | GET/POST | `utils/teleport/api.ts` |
| `/v1/sessions/{id}` | GET/DELETE | `utils/teleport/api.ts` |
| `/v1/sessions/{id}/events` | POST | `utils/teleport/api.ts` |
| `/v1/sessions/{id}/archive` | POST | `bridge/createSession.ts` |
| `/v1/sessions/ws/{id}/subscribe` | WebSocket | `remote/SessionsWebSocket.ts` |
| `/v1/code/sessions` | POST | `bridge/createSession.ts` |
| `/v1/code/sessions/{id}/bridge` | POST | `bridge/codeSessionApi.ts` |
| `/v1/code/sessions/{id}/teleport-events` | GET | `services/api/sessionIngress.ts` |
| `/v1/code/triggers` | GET/POST | `tools/RemoteTriggerTool/RemoteTriggerTool.ts` |
| `/v1/code/github/import-token` | GET | `commands/remote-setup/api.ts` |
| `/v1/environments/bridge` | POST | `bridge/bridgeApi.ts` |
| `/v1/environment_providers` | GET | `utils/teleport/environments.ts` |

### 其他
| 路径 | 方法 | 源码文件 |
|------|------|---------|
| `/api/claude_code_shared_session_transcripts` | POST | `components/FeedbackSurvey/submitTranscriptShare.ts` |
| `/api/web/domain_info` | GET | `tools/WebFetchTool/utils.ts` |
| `/api/oauth/file_upload` | POST | `tools/BriefTool/upload.ts` |
| `/api/organization/claude_code_first_token_date` | GET | `services/api/firstTokenDate.ts` |
| `/api/ws/speech_to_text/voice_stream` | WebSocket | `services/voiceStreamSTT.ts` |
| `/api/session_ingress/session/{id}` | POST | `services/api/sessionIngress.ts` |
| `/v1/ultrareview/quota` | GET | `services/api/ultrareviewQuota.ts` |
