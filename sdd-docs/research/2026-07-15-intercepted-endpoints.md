# MCC 代理拦截接口清单

> 梳理时间：2026-07-15 ｜ 源码位置：`internal/proxy/handler.go`、`hardcoded.go`、`endpoint_policy.go`、`blocked.go`、`frame.go`、`local_catalog.go`、`design_streaming.go`

## 数量总览

| 类别 | 处置方式 | 数量 |
|------|----------|-----:|
| **本地硬编码拦截** | 本地伪造响应，不转发上游 | **53** |
| ↳ 精确匹配端点 | 路径完全相等 | 39 |
| ↳ 前缀匹配端点 | `strings.HasPrefix` | 12 |
| ↳ 模式匹配端点 | 前缀 + 后缀组合 | 2 |
| **模型推理转发** | 转发到配置的 provider | **2** |
| **合计顶层端点** | | **55** |

> 另有兜底规则：未命中以上 55 个端点的任意请求 → 本地 `404 mcc_blocked_unknown_endpoint`（模型端点路径的非 POST 方法 → `405`）。

---

## 架构：三层处置

请求进入 `Handler.ServeHTTP`（`handler.go:59`）后按顺序判定：

```
请求 → ① 根路径 "/"? ──────────────────────→ 200 "OK"
     → ② 命中硬编码端点表（53 条）? ────────→ 本地伪造响应
     → ③ 模型推理端点（2 条）? ─────────────→ 转发上游 provider
     → ④ 其余一切 ─────────────────────────→ 404 / 405 兜底拦截
```

**安全设计（fail-closed guard，`endpoint_policy.go:8-11`）**：默认拒绝转发。只有**显式列入白名单**的模型推理端点允许去往上游，确保 Claude Code 发往 `api.anthropic.com` 的非模型请求（遥测、A/B 测试、权限策略等）绝不泄露到第三方 API。

**域名拦截**：透明模式通过 hosts 将 `api.anthropic.com` → `127.0.0.1`（`bootstrap.go:180`），在 `:443` 用自签 CA 做 TLS 劫持，所有流量落到同一 `Handler`。

---

## 一、模型推理转发端点（2 个）

唯一允许转发到配置 provider 的端点（`endpoint_policy.go:31-34`）：

| # | 方法 | 路径 | 版本 |
|---|------|------|------|
| 1 | `POST` | `/v1/messages` | v1 |
| 2 | `POST` | `/anthropic/v1/messages` | v1（OAuth base_url 前缀变体） |

- 这两个路径的非 POST 方法 → 本地 `405 method_not_allowed`
- 其它一切路径 → 本地 `404`

---

## 二、本地硬编码拦截端点（53 个）

### A. 精确匹配端点（39 个，编号 1–37 + 20b/33b 后缀为 CC 2.1.211 新增）

#### A1. 模型发现与启动引导（4）

| # | 方法 | 路径 | 作用 |
|---|------|------|------|
| 1 | `GET` | `/v1/models` | 从 MCC 配置派生模型列表 |
| 2 | `GET` | `/api/claude_cli/bootstrap` | 启动引导，注入 `additional_model_options`（让 `/model` 菜单出现自定义模型） |
| 3 | `POST` | `/v1/messages/count_tokens` | 本地按 body 估算 token（第三方上游不支持） |
| 4 | `GET` | `/api/claude_code_penguin_mode` | Fast Mode 配置，返回空禁用 |

#### A2. 用户 / 组织 / 认证身份（14）

| # | 方法 | 路径 | 作用 |
|---|------|------|------|
| 5 | `GET` | `/v1/me` | 用户信息 |
| 6 | `GET` | `/api/oauth/profile` | OAuth profile |
| 7 | `GET` | `/api/claude_cli_profile` | CLI profile |
| 8 | `GET` | `/api/oauth/usage` | 用量 |
| 9 | `GET` | `/api/oauth/claude_cli/roles` | 角色信息 |
| 10 | `POST` | `/api/oauth/claude_cli/create_api_key` | 创建 API key（伪造 `sk-ant-api03-local-proxy-*`） |
| 11 | `GET` | `/api/claude_code/organizations/metrics_enabled` | 组织指标开关（返 false） |
| 12 | `GET` | `/api/organization/claude_code_first_token_date` | 首 token 日期 |
| 13 | `GET` | `/api/auth/trusted_devices` | 受信设备 |
| 14 | `GET` | `/api/claude_code/user_settings` | 用户设置 |
| 15 | `GET` | `/api/claude_code/settings` | 远程设置 |
| 16 | `POST` | `/api/oauth/file_upload` | 文件上传 |
| 17 | `GET` | `/api/claude_code/team_memory` | 团队记忆 |
| 18 | `GET` | `/api/claude_code_grove` | grove |

#### A3. 策略 / 限制 / 合规（3）

| # | 方法 | 路径 | 作用 |
|---|------|------|------|
| 19 | `GET` | `/api/claude_code/policy_limits` | 策略限制（restrictions 空对象） |
| 20 | `GET` | `/v1/ultrareview/quota` | ultrareview 配额（2.1.211 已被 preflight 取代，本地拦截保留） |
| 20b | `GET` | `/v1/ultrareview/preflight` | ultrareview 预检（CC 2.1.211，与 quota 同走 `200 {}`） |

#### A4. 遥测 / 事件 / 反馈（7）

| # | 方法 | 路径 | 版本 | 作用 |
|---|------|------|------|------|
| 21 | `POST` | `/api/claude_cli_feedback` | — | 反馈提交（伪造 feedback_id） |
| 22 | `POST` | `/api/event_logging/batch` | v1 | 事件批量上报 |
| 23 | `POST` | `/api/event_logging/v2/batch` | v2 | 事件批量上报 |
| 24 | `POST` | `/api/claude_code_shared_session_transcripts` | — | 会话记录共享 |
| 25 | `POST` | `/v1/metrics` | v1 | OTLP 遥测 → 204 |
| 26 | `POST` | `/v1/logs` | v1 | OTLP 遥测 → 204 |
| 27 | `POST` | `/v1/traces` | v1 | OTLP 遥测 → 204 |

#### A5. MCP / 技能 / 协作控制面（4）

| # | 方法 | 路径 | 作用 |
|---|------|------|------|
| 28 | `GET` | `/v1/mcp_servers` | claude.ai MCP 服务器列表（空） |
| 29 | `GET` | `/api/claude_code/skills` | 已安装 skill 健康状态 |
| 30 | `GET` | `/api/claude_code/discovery/team_usage` | 团队用量 |
| 31 | `GET` | `/api/claude_code/notification/preferences` | 通知偏好 |

#### A6. Claude Design（3）

| # | 方法 | 路径 | 作用 |
|---|------|------|------|
| 32 | `*` | `/v1/design/consent` | consent 本地状态 |
| 33 | `POST` | `/v1/design/mcp` | MCP bridge → unsupported |
| 33b | `GET`/`POST` | `/v1/design/grants` | GET 空授权禁用 Design；POST `403 write_gate_disabled`（CC 2.1.211） |

#### A7. 浏览器 / 静态探测（4）

| # | 方法 | 路径 | 作用 |
|---|------|------|------|
| 34 | `GET` | `/favicon.ico` | 404 空 body |
| 35 | `GET` | `/robots.txt` | 404 空 body |
| 36 | `GET` | `/apple-touch-icon.png` | 404 空 body |
| 37 | `GET` | `/apple-touch-icon-precomposed.png` | 404 空 body |

> 子节小计：4 + 14 + 3 + 7 + 4 + 3 + 4 = **39** ✓

---

### B. 前缀匹配端点（12 个）

| # | 方法 | 路径前缀 | 作用 |
|---|------|----------|------|
| 1 | `POST` | `/api/claude_code/metric*` | 指标上报 → `{"success":true}` |
| 2 | `GET` | `/api/claude_code/organization*` | 组织信息 |
| 3 | `GET` | `/api/web/domain_info*` | 域名信息（`can_fetch:true`） |
| 4 | `GET` | `/api/feature/*` | **GrowthBook 特性开关**：启用记忆搜索/技能建议/验证代理等有益功能，禁用 datadog/segment 遥测与 `tengu_permission_friction`/`tengu_harbor` 等有害 A/B 测试（项目核心价值，`hardcoded.go:493`） |
| 5 | `GET` | `/mcp-registry/*` | MCP 注册表（空 servers） |
| 6 | `GET` | `/api/oauth/account/*` | 账户 |
| 7 | `*` | `/api/oauth/organizations/*` | 组织（宽泛 fallback，空响应；其下搜索子路径见 D） |
| 8 | `*` | `/v1/session_ingress/session/*` | session ingress |
| 9 | `*` | `/v1/code/sessions/*` | code sessions |
| 9b | `GET`/`POST` | `/v1/code/triggers*` | CCR 触发器（CC 2.1.211）：GET `{data:[]}`、POST `403 write_gate_disabled` |
| 10 | `*` | `/api/frame/*` | Frame artifact（子路径展开见 C） |
| 11 | `*` | `/api/ws/*` | WebSocket / 语音流 → 501 |

---

### C. Frame artifact 子端点展开（`/api/frame/*`，`frame.go`）

`/api/frame/` 前缀（B-10）下的具体处置：

| 方法 | 子路径 | 响应 |
|------|--------|------|
| `GET` | `/api/frame/frames` | 200 `{"frames":[]}` |
| `POST` | `/api/frame/track` | 204 |
| `POST` | `/api/frame/deploy/complete` | 204 |
| `POST` | `/api/frame/deploy/init`、`/api/frame/deploy/direct` | 403 `write_gate_disabled` |
| `GET` | `/api/frame/contract/*` | 404 `local_unavailable` |
| `GET/DELETE` | `/api/frame/{slug}` | 404 `not_found` |

---

### D. 组织级搜索子端点展开（`/api/oauth/organizations/{org}/*`，`local_catalog.go`）

该前缀（B-7）下的具体搜索端点（在宽泛 fallback 之前优先匹配）：

| 方法 | suffix（拼接在 `/api/oauth/organizations/{org}/` 后） | 作用 |
|------|------|------|
| `POST` | `mcp/connectors/list` | MCP connector 列表（本地空） |
| `POST` | `mcp/connectors/search` | MCP connector 搜索（本地空） |
| `POST` | `mcp/connectors/suggest` | MCP connector 建议（本地空） |
| `POST` | `plugins/search` | 插件搜索（读本地 marketplace） |
| `POST` | `skills/search` | skill 搜索（读本地） |

---

### E. 模式匹配端点（2 个）

`isHardcodedEndpoint` 中用 `HasPrefix + HasSuffix` 组合判定的端点：

| # | 方法 | 路径模式 | 作用 |
|---|------|----------|------|
| 1 | `HEAD/GET` | `/api/desktop/**/update` | Desktop 更新探测，返 `currentRelease: 1.13576.0` 阻止自动更新 |
| 2 | `*` | `/api/organizations/{org}/claude_code/onboarding` | onboarding → 空响应 |

---

## 三、兜底拦截规则（`blocked.go`）

| 场景 | 响应 | reason |
|------|------|--------|
| 未命中 55 个端点的任意非模型请求 | `404` | `mcc_blocked_unknown_endpoint` |
| 模型端点路径但方法非 POST | `405` + `Allow: POST` | `method_not_allowed` |

**日志安全红线**（`blocked.go:57-68`）：只记录 `method/host/path/query 是否存在/截断 UA/status/reason`，**绝不记录请求体、Authorization、Cookie、X-Api-Key、原始 query**；所有字段经控制字符 sanitize（防日志注入 CWE-117）。

---

## 四、API 版本标识汇总

| 版本 | 含义 | 涉及端点 |
|------|------|----------|
| **v1** | Anthropic 主 API 版本 | `/v1/messages`、`/anthropic/v1/messages`、`/v1/models`、`/v1/me`、`/v1/mcp_servers`、`/v1/messages/count_tokens`、`/v1/metrics`、`/v1/logs`、`/v1/traces`、`/v1/ultrareview/quota`、`/v1/ultrareview/preflight`、`/v1/design/consent`、`/v1/design/mcp`、`/v1/design/grants`、`/v1/session_ingress/session/*`、`/v1/code/sessions/*`、`/v1/code/triggers*` |
| **v2** | 事件上报新版本 | `/api/event_logging/v2/batch`（同时保留无版本号的 v1 路径 `/api/event_logging/batch`） |
| **`/anthropic/v1/`** | OAuth base_url 前缀变体 | `/anthropic/v1/messages`（与 `/v1/messages` 等价对待） |
| **Desktop `1.13576.0`** | Claude Desktop 版本号 | `desktopCurrentRelease` 常量（`hardcoded.go:689`），伪造为最新以阻断自动更新 |

---

## 附录：数量核对脚本

```bash
# 精确匹配端点数（exactMatches 切片内的字面量路径）
awk '/exactMatches := \[\]string{/,/^	\}/' internal/proxy/hardcoded.go | grep -cE '^\s*"/'
# → 39

# 前缀匹配端点数（prefixMatches 切片内的字面量路径）
awk '/prefixMatches := \[\]string{/,/^	\}/' internal/proxy/hardcoded.go | grep -cE '^\s*"/'
# → 12

# 模型转发端点数（modelForwardPaths map）
grep -cE '^\s*"/' internal/proxy/endpoint_policy.go
# → 2

# 模式匹配端点数（isHardcodedEndpoint 内 HasPrefix+HasSuffix 组合，2 个独立端点）
#   /api/desktop/**/update
#   /api/organizations/{org}/claude_code/onboarding

# 合计：39 + 12 + 2（本地拦截）+ 2（模型转发）= 55
```
