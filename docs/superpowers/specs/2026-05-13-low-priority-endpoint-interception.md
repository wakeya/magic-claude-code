# 低优先级端点拦截规格

**版本**: 1.3
**日期**: 2026-05-13
**状态**: 待审核

---

## 1. 背景

前一批修复（[2026-05-13-hardcoded-endpoint-fixes.md](2026-05-13-hardcoded-endpoint-fixes.md)）已处理了遥测上报和关键 Bug。本批拦截 7 个低优先级接口，它们在源码中都有容错（失败不崩溃），不拦截也不会有问题，但拦截可以：

1. 避免请求被代理转发到第三方后端产生无意义的错误日志
2. 避免请求超时等待（通常 5 秒）影响客户端启动或运行速度
3. 减少不必要的网络流量

### 设计决策

这 7 个端点的共同特征是：**源码不依赖响应体中的必填字段**，失败时一律静默跳过。因此统一返回 `{}` 即可，无需为每个端点编写专门的 handler。

---

## 2. 端点清单

### 2.1 `/api/oauth/profile`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/oauth/getOauthProfile.ts` L40 |
| **用途** | 获取 OAuth 用户资料（组织信息、账户 UUID 等） |
| **请求方法** | GET |
| **容错行为** | `catch` → `logError` → 返回 `undefined`，调用方跳过 profile 相关逻辑 |
| **伪造响应** | `{}` |

### 2.2 `/api/claude_cli_profile`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/oauth/getOauthProfile.ts` L19 |
| **用途** | CLI 专用 profile，获取 CLI 用户配置信息 |
| **请求方法** | GET |
| **容错行为** | 同 `/api/oauth/profile`，`catch` → `logError` → 返回 `undefined` |
| **伪造响应** | `{}` |

### 2.3 `/api/oauth/account/settings`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/api/grove.ts` L65, L130 |
| **用途** | 账户隐私设置（Grove 隐私开关） |
| **请求方法** | GET |
| **容错行为** | `catch` → `logError` → 不缓存失败，调用方 fallback 到默认隐私状态 |
| **伪造响应** | `{}` |

### 2.4 `/api/oauth/usage`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/api/usage.ts` L55 |
| **用途** | 查询用户 API/订阅用量 |
| **请求方法** | GET |
| **容错行为** | `catch` → 向调用方抛出错误，调用方静默处理 |
| **伪造响应** | `{}` |

### 2.5 `/api/claude_code/policy_limits`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/policyLimits/index.ts` L127 |
| **用途** | 获取组织策略限制（速率限制等） |
| **请求方法** | GET |
| **容错行为** | `catch` → 跳过，使用本地默认限制 |
| **伪造响应** | `{}` |

### 2.6 `/api/claude_code/settings`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/remoteManagedSettings/index.ts` L106 |
| **用途** | 企业/组织级别的 Claude Code 远程托管设置下发 |
| **请求方法** | GET |
| **容错行为** | `catch` → 跳过，使用本地设置 |
| **伪造响应** | `{}` |

### 2.7 `/api/claude_code/user_settings`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/settingsSync/index.ts` L224 |
| **用途** | 用户设置跨设备同步（下载/上传） |
| **请求方法** | GET / PUT |
| **容错行为** | `catch` → `logEvent('tengu_settings_sync_download_error')` → fail-open，不阻塞启动 |
| **伪造响应** | `{}` |

### 2.8 `/api/claude_code_grove`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/oauth/getOauthProfile.ts` L55 |
| **用途** | Grove 服务配置查询 |
| **请求方法** | GET |
| **容错行为** | `catch` → `logError` → 返回 `undefined`，调用方跳过 Grove 相关逻辑 |
| **伪造响应** | `{}` |

### 2.9 `/api/organization/claude_code_first_token_date`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/api/firstTokenDate.ts` L15 |
| **用途** | 获取组织中首次 token 使用的日期（用于展示功能提示） |
| **请求方法** | GET |
| **容错行为** | `catch` → `logError` → 返回 `null`，调用方 fallback 到默认提示 |
| **伪造响应** | `{}` |

### 2.10 `/v1/ultrareview/quota`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/ultrareview/index.ts` L45 |
| **用途** | 查询 Ultra Review 功能配额 |
| **请求方法** | GET |
| **容错行为** | `catch` → 返回 `null`，调用方显示无配额 |
| **伪造响应** | `{}` |

### 2.11 `/v1/session_ingress/session/{id}`（可选）

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/sessionIngress/index.ts` L30 |
| **用途** | 会话遥测数据上传（session ingress） |
| **请求方法** | POST |
| **容错行为** | `catch` → `logError` → 静默跳过，不影响功能 |
| **伪造响应** | `{}` |
| **备注** | 遥测类端点，拦截可阻止数据上传到第三方后端 |

### 2.12 `/api/oauth/organizations/{orgUUID}/referral/*`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/api/referral.ts` L36, L57 |
| **用途** | 推荐计划资格检查与兑换记录（guest pass 功能） |
| **请求方法** | GET |
| **容错行为** | `catch` → `logError` → 返回 `null`，调用方跳过 passes 相关逻辑 |
| **伪造响应** | `{}` |

### 2.13 `/api/oauth/organizations/{orgUUID}/admin_requests*`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/api/adminRequests.ts` L59, L82, L112 |
| **用途** | 管理员请求（限额提升、席位升级）的创建/查询/资格检查 |
| **请求方法** | POST / GET |
| **容错行为** | 调用方 `try/catch` → 静默处理，不影响核心功能 |
| **伪造响应** | `{}` |

### 2.14 `/api/oauth/organizations/{orgUUID}/overage_credit_grant`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/api/overageCreditGrant.ts` L32 |
| **用途** | 超额信用额度查询（计费相关） |
| **请求方法** | GET |
| **容错行为** | `catch` → `logError` → 返回 `null`，不展示超额提示 |
| **伪造响应** | `{}` |

### 2.15 `/v1/code/sessions/{sessionId}/teleport-events`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/api/sessionIngress.ts` L296 |
| **用途** | 获取远程会话的 teleport 事件（远程协作功能） |
| **请求方法** | GET |
| **容错行为** | `catch` → `logError` → 返回 `null`，远程协作不可用 |
| **伪造响应** | `{}` |

### 2.16 `/api/claude_code/team_memory`

| 项目 | 内容 |
|------|------|
| **源码位置** | `services/teamMemorySync/index.ts` L164 |
| **用途** | 团队记忆同步（跨成员共享 CLAUDE.md 等文件） |
| **请求方法** | GET / PUT |
| **容错行为** | `catch` → `logError` → 静默跳过，本地记忆不受影响 |
| **伪造响应** | `{}` |

### 2.17 `/api/auth/trusted_devices`

| 项目 | 内容 |
|------|------|
| **源码位置** | `bridge/trustedDevice.ts` L142 |
| **用途** | 信任设备注册（登录时标记设备为已信任） |
| **请求方法** | POST |
| **容错行为** | `catch` → `logForDebugging` → return（跳过注册，不影响登录） |
| **伪造响应** | `{}` |

### 2.18 `/api/oauth/file_upload`

| 项目 | 内容 |
|------|------|
| **源码位置** | `tools/BriefTool/upload.ts` L122 |
| **用途** | Brief 附件上传（聊天中的文件/图片上传） |
| **请求方法** | POST |
| **容错行为** | `catch` → return `undefined`（静默跳过，附件不显示但不崩溃） |
| **伪造响应** | `{}` |

---

## 3. 实现方案

### 匹配规则

新增精确匹配：

```
"/api/oauth/profile"
"/api/claude_cli_profile"
"/api/oauth/usage"
"/api/claude_code/policy_limits"
"/api/claude_code/settings"
"/api/claude_code/user_settings"
"/api/claude_code_grove"
"/api/organization/claude_code_first_token_date"
"/v1/ultrareview/quota"
"/api/claude_code/team_memory"                     # v1.2 新增
"/api/auth/trusted_devices"                        # v1.3 新增
"/api/oauth/file_upload"                            # v1.3 新增
```

新增前缀匹配：

```
"/api/oauth/account/"               # 覆盖 /settings 和 /grove_notice_viewed
"/v1/session_ingress/session/"      # 覆盖 /v1/session_ingress/session/{id}
"/api/oauth/organizations/"         # v1.2 新增，覆盖 referral/admin_requests/overage_credit_grant
"/v1/code/sessions/"                # v1.2 新增，覆盖 teleport-events
```

### Handler 设计

统一使用一个通用 handler `handleEmptyResponse`，返回 `200 OK` + `{}`。无需为每个端点编写独立 handler。

```go
func (h *Handler) handleEmptyResponse(w http.ResponseWriter, r *http.Request) {
    log.Printf("[Hardcoded] Handling request: %s %s", r.Method, r.URL.Path)
    writeJSONResponse(w, http.StatusOK, map[string]any{})
}
```

在 `handleHardcodedEndpoint` switch 中，这些端点统一调用 `handleEmptyResponse`。

---

## 4. 不应拦截的路径

`/api/oauth/account/grove_notice_viewed` 虽然也在 `/api/oauth/account/` 前缀下，但拦截它也是安全的（源码 `catch` 静默处理）。前缀匹配会一并覆盖。

`/v1/session_ingress/session/` 前缀匹配会覆盖所有 `{id}` 变体路径，拦截安全。

`/api/oauth/organizations/` 前缀匹配会覆盖所有 `{orgUUID}/referral/*`、`{orgUUID}/admin_requests*`、`{orgUUID}/overage_credit_grant` 变体路径，拦截安全。这些端点都需要有效的 OAuth token 和真实 orgUUID，第三方 API 代理场景下必定失败。

`/v1/code/sessions/` 前缀匹配会覆盖 `{sessionId}/teleport-events` 等变体路径，拦截安全。

---

## 5. 修改后的完整匹配规则（增量）

在已有的精确匹配列表中追加：

```
"/api/oauth/profile"                           # v1.0
"/api/claude_cli_profile"                       # v1.0
"/api/oauth/usage"                              # v1.0
"/api/claude_code/policy_limits"                # v1.0
"/api/claude_code/settings"                     # v1.0
"/api/claude_code/user_settings"                # v1.0
"/api/claude_code_grove"                        # v1.1 新增
"/api/organization/claude_code_first_token_date" # v1.1 新增
"/v1/ultrareview/quota"                         # v1.1 新增
"/api/claude_code/team_memory"                  # v1.2 新增
"/api/auth/trusted_devices"                     # v1.3 新增
"/api/oauth/file_upload"                         # v1.3 新增
```

在已有的前缀匹配列表中追加：

```
"/api/oauth/account/"               # v1.0
"/v1/session_ingress/session/"      # v1.1 新增
"/api/oauth/organizations/"         # v1.2 新增
"/v1/code/sessions/"                # v1.2 新增
```

---

## 6. 影响范围

| 变更 | 修改文件 |
|------|---------|
| 匹配规则 + handler + 路由分发 | `internal/proxy/hardcoded.go` |
| 测试用例 | `internal/proxy/hardcoded_test.go` |
