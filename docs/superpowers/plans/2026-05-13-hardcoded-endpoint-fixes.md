# 硬编码端点拦截修正与补全实施计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修正硬编码端点拦截中的 Bug，补全遗漏的遥测/上报接口拦截，确保用户行为数据不泄漏到第三方后端，客户端收到的伪造响应格式与源码期望完全匹配。

**Spec:** [2026-05-13-hardcoded-endpoint-fixes.md](../specs/2026-05-13-hardcoded-endpoint-fixes.md)

**Impact:** 仅修改 `internal/proxy/hardcoded.go` 和 `internal/proxy/hardcoded_test.go`，不影响代理转发逻辑。

---

## Phase 1: Bug 修复

- [ ] **Fix-1: 修正 `metrics_enabled` 响应字段名**
  - 文件: `internal/proxy/hardcoded.go` → `handleMetricsEnabled()`
  - 修改: `"metrics_enabled": false` → `"metrics_logging_enabled": false`
  - 源码依据: `services/api/metricsOptOut.ts` L13 — `type MetricsEnabledResponse = { metrics_logging_enabled: boolean }`

- [ ] **Fix-2: 修正 `roles` 路径拼写**
  - 文件: `internal/proxy/hardcoded.go` → `isHardcodedEndpoint()` exactMatches
  - 修改: `"/api/oauth/claude_cli/role"` → `"/api/oauth/claude_cli/roles"`
  - 修改: `handleHardcodedEndpoint()` switch case 中的路径同步更新
  - 源码依据: `constants/oauth.ts` L93 — `ROLES_URL: '.../api/oauth/claude_cli/roles'`

---

## Phase 2: 新增拦截匹配规则

- [ ] **在 `isHardcodedEndpoint()` 中添加精确匹配项**
  - 新增: `"/api/event_logging/batch"`
  - 新增: `"/api/claude_cli/bootstrap"`
  - 新增: `"/v1/mcp_servers"`
  - 新增: `"/api/claude_code_penguin_mode"`

- [ ] **在 `isHardcodedEndpoint()` 中添加前缀匹配项**
  - 新增: `"/api/feature/"` (GrowthBook SDK)
  - 新增: `"/mcp-registry/"` (MCP 注册表)

---

## Phase 3: 新增 Handler 实现

- [ ] **Add-1: `handleEventLogging()`**
  - 匹配: `path == "/api/event_logging/batch"`
  - 响应: `200 OK` + `{}`
  - 注意: 源码只检查 HTTP 状态码

- [ ] **Add-2: `handleGrowthBookFeature()`**
  - 匹配: `strings.HasPrefix(path, "/api/feature/")`
  - 响应: `200 OK` + `{"features": {}}`
  - 注意: 空 features 触发客户端 fallback 到默认值

- [ ] **Add-3: `handleBootstrap()`**
  - 匹配: `path == "/api/claude_cli/bootstrap"`
  - 响应: `200 OK` + `{}`

- [ ] **Add-4: `handleMCPRegistry()`**
  - 匹配: `strings.HasPrefix(path, "/mcp-registry/")`
  - 响应: `200 OK` + `{"servers": []}`

- [ ] **Add-5: `handleMCPServers()`**
  - 匹配: `path == "/v1/mcp_servers"`
  - 响应: `200 OK` + `{"data": [], "has_more": false}`

- [ ] **Add-6: `handlePenguinMode()`**
  - 匹配: `path == "/api/claude_code_penguin_mode"`
  - 响应: `200 OK` + `{}`

---

## Phase 4: 更新路由分发

- [ ] **在 `handleHardcodedEndpoint()` switch 中添加新分支**
  - 每个 Add-N 对应一个 case 分支
  - 顺序: 在已有 `/v1/me` case 之后，`return false` 之前
  - 每个 case 调用对应的 handler 方法并 `return true`

---

## Phase 5: 测试

- [ ] **更新 `TestIsHardcodedEndpoint`**
  - 新增精确匹配测试: `/api/oauth/claude_cli/roles`, `/api/event_logging/batch`, `/api/claude_cli/bootstrap`, `/v1/mcp_servers`, `/api/claude_code_penguin_mode`
  - 新增前缀匹配测试: `/api/feature/abc123`, `/api/feature/`, `/mcp-registry/v0/servers`, `/mcp-registry/`
  - 新增反向测试: `/api/oauth/claude_cli/role` (单数) → false, `/api/feature` (无斜杠) → false, `/mcp-registry` (无斜杠) → false

- [ ] **新增 `TestHandleMetricsEnabled`**
  - 验证响应包含 `metrics_logging_enabled`
  - 验证响应不包含旧字段 `metrics_enabled`

- [ ] **新增各 handler 测试**
  - `TestHandleEventLogging`: 验证 200
  - `TestHandleGrowthBookFeature`: 验证 200 + 包含 `features`
  - `TestHandleBootstrap`: 验证 200
  - `TestHandleMCPRegistry`: 验证 200 + 包含 `servers`
  - `TestHandleMCPServers`: 验证 200 + 包含 `data` 和 `has_more`
  - `TestHandlePenguinMode`: 验证 200

- [ ] **更新 `TestHandleHardcodedEndpoint`**
  - 新增所有新端点到集成测试列表

- [ ] **运行全量测试**
  - `go test ./internal/proxy/... -v`
  - `go test ./...` 确认无回归

---

## 完成标准

1. 所有测试通过
2. `/api/event_logging/batch` 请求被拦截，不再泄漏到第三方
3. GrowthBook `/api/feature/` 请求立即返回空 features，不再 5 秒超时
4. `metrics_logging_enabled` 字段名与源码期望一致
5. `roles` 路径正确匹配复数形式
