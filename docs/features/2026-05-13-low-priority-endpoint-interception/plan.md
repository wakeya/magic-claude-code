# 低优先级端点拦截实施计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 拦截 18 个低优先级端点（v1.0: 7 + v1.1: 4 + v1.2: 5 + v1.3: 2），避免请求被代理转发到第三方后端产生超时和错误日志。统一返回空 JSON。

**Spec:** [2026-05-13-low-priority-endpoint-interception.md](../specs/2026-05-13-low-priority-endpoint-interception.md)

**Impact:** 仅修改 `internal/proxy/hardcoded.go` 和 `internal/proxy/hardcoded_test.go`。

---

## Phase 1: 添加通用 Handler

- [ ] **新增 `handleEmptyResponse()` 方法**
  - 统一返回 `200 OK` + `{}`
  - 日志记录请求方法和路径
  - 所有端点共用此 handler（v1.0-v1.3 共 18 个端点）

---

## Phase 2: 更新匹配规则

- [ ] **在 `isHardcodedEndpoint()` 精确匹配列表中追加 10 项**
  - `"/api/oauth/profile"`
  - `"/api/claude_cli_profile"`
  - `"/api/oauth/usage"`
  - `"/api/claude_code/policy_limits"`
  - `"/api/claude_code/settings"`
  - `"/api/claude_code/user_settings"`
  - `"/api/claude_code_grove"`                          # v1.1 新增
  - `"/api/organization/claude_code_first_token_date"`  # v1.1 新增
  - `"/v1/ultrareview/quota"`                           # v1.1 新增
  - `"/api/claude_code/team_memory"`                    # v1.2 新增

- [ ] **在 `isHardcodedEndpoint()` 前缀匹配列表中追加 4 项**
  - `"/api/oauth/account/"`
  - `"/v1/session_ingress/session/"`                    # v1.1 新增
  - `"/api/oauth/organizations/"`                       # v1.2 新增，覆盖 referral/admin_requests/overage_credit_grant
  - `"/v1/code/sessions/"`                              # v1.2 新增，覆盖 teleport-events

---

## Phase 3: 更新路由分发

- [ ] **在 `handleHardcodedEndpoint()` switch 中添加新分支**
  - 6 个 v1.0 精确匹配路径各一个 case
  - 1 个 v1.0 前缀匹配 case（`/api/oauth/account/`）
  - 3 个 v1.1 精确匹配 case（`/api/claude_code_grove`, `/api/organization/claude_code_first_token_date`, `/v1/ultrareview/quota`）
  - 1 个 v1.1 前缀匹配 case（`/v1/session_ingress/session/`）
  - 1 个 v1.2 精确匹配 case（`/api/claude_code/team_memory`）
  - 2 个 v1.2 前缀匹配 case（`/api/oauth/organizations/`, `/v1/code/sessions/`）
  - 2 个 v1.3 精确匹配 case（`/api/auth/trusted_devices`, `/api/oauth/file_upload`）
  - 全部调用 `handleEmptyResponse`

---

## Phase 4: 测试（TDD 方式）

> **流程:** 先编写测试（预期失败）→ 实现（使测试通过）→ 验证

- [ ] **更新 `TestIsHardcodedEndpoint`**
  - 新增精确匹配正向测试 6 条（v1.0）
  - 新增前缀匹配正向测试 2 条（`/api/oauth/account/settings`, `/api/oauth/account/grove_notice_viewed`）
  - 新增反向测试 1 条（`/api/oauth/account` 无尾部斜杠 → false）
  - 新增精确匹配正向测试 3 条（v1.1: `/api/claude_code_grove`, `/api/organization/claude_code_first_token_date`, `/v1/ultrareview/quota`）
  - 新增前缀匹配正向测试 2 条（v1.1: `/v1/session_ingress/session/abc123`, `/v1/session_ingress/session/x-y-z`）
  - 新增反向测试 1 条（`/v1/session_ingress/session` 无尾部斜杠 → false）
  - 新增精确匹配正向测试 1 条（v1.2: `/api/claude_code/team_memory`）
  - 新增前缀匹配正向测试 3 条（v1.2: `/api/oauth/organizations/org-123/referral/eligibility`, `/api/oauth/organizations/org-123/overage_credit_grant`, `/v1/code/sessions/sess-123/teleport-events`）
  - 新增反向测试 2 条（`/api/oauth/organizations` 无尾部斜杠 → false, `/v1/code/sessions` 无尾部斜杠 → false）
  - 新增精确匹配正向测试 2 条（v1.3: `/api/auth/trusted_devices`, `/api/oauth/file_upload`）

- [ ] **新增 `TestHandleEmptyResponse`**
  - 验证返回 200
  - 验证响应体为 `{}`
  - v1.1: 追加 4 个新端点子测试
  - v1.2: 追加 4 个新端点子测试（team_memory, org referral, org overage_credit_grant, code sessions teleport-events）
  - v1.3: 追加 2 个新端点子测试（trusted_devices, file_upload）

- [ ] **更新 `TestHandleHardcodedEndpoint`**
  - 新增所有新端点到集成测试列表
  - v1.1: 追加 `/api/claude_code_grove`, `/api/organization/claude_code_first_token_date`, `/v1/ultrareview/quota`, `/v1/session_ingress/session/test-id`
  - v1.2: 追加 `/api/claude_code/team_memory`, `/api/oauth/organizations/org-123/referral/eligibility`, `/api/oauth/organizations/org-123/admin_requests`, `/v1/code/sessions/sess-123/teleport-events`
  - v1.3: 追加 `/api/auth/trusted_devices`, `/api/oauth/file_upload`

- [ ] **运行全量测试**
  - `go test ./... -v -count=1`
