# 低优先级端点拦截验证清单

**版本**: 1.3
**日期**: 2026-05-13

---

## 验证方法

实施完成后逐项检查，每项通过后标记 `[x]`。

---

## 1. 代码完整性

- [ ] `isHardcodedEndpoint()` 精确匹配列表包含 6 个新路径（v1.0）
- [ ] `isHardcodedEndpoint()` 精确匹配列表包含 3 个新路径（v1.1: grove, first_token_date, ultrareview/quota）
- [ ] `isHardcodedEndpoint()` 前缀匹配列表包含 `/api/oauth/account/`
- [ ] `isHardcodedEndpoint()` 精确匹配列表包含 1 个新路径（v1.2: team_memory）
- [ ] `isHardcodedEndpoint()` 精确匹配列表包含 2 个新路径（v1.3: trusted_devices, file_upload）
- [ ] `isHardcodedEndpoint()` 前缀匹配列表包含 `/api/oauth/organizations/`（v1.2）
- [ ] `isHardcodedEndpoint()` 前缀匹配列表包含 `/v1/code/sessions/`（v1.2）
- [ ] `handleEmptyResponse()` 方法存在，返回 `200 OK` + `{}`
- [ ] `handleHardcodedEndpoint()` switch 包含 7 个新 case 分支（v1.0）
- [ ] `handleHardcodedEndpoint()` switch 包含 4 个新 case 分支（v1.1）
- [ ] `handleHardcodedEndpoint()` switch 包含 3 个新 case 分支（v1.2: team_memory, organizations/, code/sessions/）
- [ ] 所有新 case 分支调用 `handleEmptyResponse` 并 `return true`

## 2. 匹配规则正确性

| 路径 | 期望结果 | 验证 |
|------|---------|------|
| `/api/oauth/profile` | `true` | [ ] |
| `/api/claude_cli_profile` | `true` | [ ] |
| `/api/oauth/usage` | `true` | [ ] |
| `/api/claude_code/policy_limits` | `true` | [ ] |
| `/api/claude_code/settings` | `true` | [ ] |
| `/api/claude_code/user_settings` | `true` | [ ] |
| `/api/oauth/account/settings` | `true` | [ ] |
| `/api/oauth/account/grove_notice_viewed` | `true` | [ ] |
| `/api/oauth/account` (无斜杠) | `false` | [ ] |
| `/api/claude_code_grove` | `true` | [ ] |
| `/api/organization/claude_code_first_token_date` | `true` | [ ] |
| `/v1/ultrareview/quota` | `true` | [ ] |
| `/v1/session_ingress/session/abc-123` | `true` | [ ] |
| `/v1/session_ingress/session` (无斜杠) | `false` | [ ] |
| `/api/claude_code/team_memory` | `true` | [ ] |
| `/api/oauth/organizations/org-123/referral/eligibility` | `true` | [ ] |
| `/api/oauth/organizations/org-123/admin_requests` | `true` | [ ] |
| `/api/oauth/organizations/org-123/overage_credit_grant` | `true` | [ ] |
| `/api/oauth/organizations` (无斜杠) | `false` | [ ] |
| `/v1/code/sessions/sess-123/teleport-events` | `true` | [ ] |
| `/v1/code/sessions` (无斜杠) | `false` | [ ] |
| `/api/auth/trusted_devices` | `true` | [ ] |
| `/api/oauth/file_upload` | `true` | [ ] |
| `/v1/messages` | `false` | [ ] |

## 3. 响应格式验证

| 端点 | 期望状态码 | 期望响应体 | 验证 |
|------|-----------|-----------|------|
| `GET /api/oauth/profile` | 200 | `{}` | [ ] |
| `GET /api/claude_cli_profile` | 200 | `{}` | [ ] |
| `GET /api/oauth/usage` | 200 | `{}` | [ ] |
| `GET /api/claude_code/policy_limits` | 200 | `{}` | [ ] |
| `GET /api/claude_code/settings` | 200 | `{}` | [ ] |
| `GET /api/claude_code/user_settings` | 200 | `{}` | [ ] |
| `GET /api/oauth/account/settings` | 200 | `{}` | [ ] |
| `GET /api/claude_code_grove` | 200 | `{}` | [ ] |
| `GET /api/organization/claude_code_first_token_date` | 200 | `{}` | [ ] |
| `GET /v1/ultrareview/quota` | 200 | `{}` | [ ] |
| `POST /v1/session_ingress/session/test-id` | 200 | `{}` | [ ] |
| `GET /api/claude_code/team_memory` | 200 | `{}` | [ ] |
| `GET /api/oauth/organizations/org-123/referral/eligibility` | 200 | `{}` | [ ] |
| `GET /api/oauth/organizations/org-123/overage_credit_grant` | 200 | `{}` | [ ] |
| `GET /v1/code/sessions/sess-123/teleport-events` | 200 | `{}` | [ ] |
| `POST /api/auth/trusted_devices` | 200 | `{}` | [ ] |
| `POST /api/oauth/file_upload` | 200 | `{}` | [ ] |

## 4. 集成路由验证

| 端点 | `handleHardcodedEndpoint` 返回 `true` | 验证 |
|------|---------------------------------------|------|
| `/api/oauth/profile` | [ ] | [ ] |
| `/api/claude_cli_profile` | [ ] | [ ] |
| `/api/oauth/usage` | [ ] | [ ] |
| `/api/claude_code/policy_limits` | [ ] | [ ] |
| `/api/claude_code/settings` | [ ] | [ ] |
| `/api/claude_code/user_settings` | [ ] | [ ] |
| `/api/oauth/account/settings` | [ ] | [ ] |
| `/api/claude_code_grove` | [ ] | [ ] |
| `/api/organization/claude_code_first_token_date` | [ ] | [ ] |
| `/v1/ultrareview/quota` | [ ] | [ ] |
| `/v1/session_ingress/session/test-id` | [ ] | [ ] |
| `/api/claude_code/team_memory` | [ ] | [ ] |
| `/api/oauth/organizations/org-123/referral/eligibility` | [ ] | [ ] |
| `/api/oauth/organizations/org-123/admin_requests` | [ ] | [ ] |
| `/v1/code/sessions/sess-123/teleport-events` | [ ] | [ ] |
| `/api/auth/trusted_devices` | [ ] | [ ] |
| `/api/oauth/file_upload` | [ ] | [ ] |

## 5. 无回归

- [ ] `go test ./internal/proxy/... -v` 全部通过
- [ ] `go test ./...` 全部通过
- [ ] 已有端点拦截行为不变（`/v1/messages` 仍为 false，`/api/event_logging/batch` 仍被拦截等）

## 6. 最终确认

- [ ] 新增代码无编译错误
- [ ] 新增代码无 IDE 诊断警告
- [ ] git diff 仅涉及 `hardcoded.go` 和 `hardcoded_test.go`
