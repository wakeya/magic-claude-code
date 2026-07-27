# MCC 代理拦截接口清单

> 梳理时间：2026-07-15（基线 CC 2.1.211）｜ 复审：2026-07-26（CC 2.1.220，见 C 节纪要）｜ 源码位置：`internal/proxy/handler.go`、`hardcoded.go`、`endpoint_policy.go`、`blocked.go`、`frame.go`、`local_catalog.go`、`design_streaming.go`
>
> **本清单是活文档，面向未来 CC 版本**：mcc 的拦截设计不锁定特定客户端版本——fail-closed 兜底（未知端点 → 404 不泄露）+ 前缀族匹配（新子路径自动归族处理）+ 客户端自身优雅降级，三者共同保证新 CC 版本「默认安全」（详见下文「版本兼容性设计」）。仅在「某新端点的默认 404/405 会劣化体验」时才需显式收录（如 2.1.220 的 frame prepare/upload）。复审新版本的方法见文末「复审新 CC 版本的方法」。

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

## 版本兼容性设计（面向未来 CC 版本）

mcc 的拦截设计**不锁定特定 CC 版本**，三层机制共同保证「新版本默认安全 + 多数情况默认可用」：

1. **fail-closed 兜底（安全底线）**：未命中白名单的任意请求 → 本地 404/405，**绝不转发上游**。Anthropic 在新版本里加任何新端点，只要不是 `/v1/messages` / `/anthropic/v1/messages`，mcc 都不会把它泄露到第三方 provider。这是版本兼容的安全地基——新版本可能「不顺手」，但绝不会「漏数据」。

2. **前缀族匹配（自动覆盖新子路径）**：CC 的控制面端点成族演进，同族新端点常落在既有前缀下，无需改代码即被族处理器接管（返回族默认响应：空列表/204/403/404）。当前 12 个前缀族：
   - `/api/frame/*`（frame artifact 全族，含 2.1.220 新增的 `deploy/prepare`、`upload`）
   - `/api/oauth/organizations/*`（组织级端点 + `local_catalog.go` 的搜索子端点）
   - `/api/claude_code/metric*`、`/api/claude_code/organization*`、`/api/oauth/account/*`
   - `/api/feature/*`（GrowthBook 特性开关）、`/mcp-registry/*`、`/api/web/domain_info*`
   - `/v1/code/sessions/*`、`/v1/code/triggers*`、`/v1/session_ingress/session/*`
   - `/api/ws/*`（WebSocket/语音流 → 501）
   - 例：Anthropic 新增 `/api/oauth/organizations/{org}/xxx` 或 `/api/frame/yyy`，自动归族，无需改 mcc。

3. **客户端自身优雅降级（体验兜底）**：CC 客户端对控制面端点普遍用 `validateStatus:()=>true`（不因非 2xx 抛错）+ 识别 `write_gate_disabled`/`local_unavailable`/`not_found` 等 reason + 带兜底文案（如 frame 多文件发布的 "server or proxy rejected it"）。故即便某新端点落到 404 兜底，客户端通常**优雅降级**（报「不可用」）而非崩溃/重试风暴。模型推理端点 `/v1/messages` 是唯一例外——它是核心功能，必须显式支持（已白名单）。

**何时需要为新版本显式改 mcc（判断标准）：**

| 情形 | 是否需要改 | 例 |
|------|-----------|-----|
| 新端点落在既有前缀族下，族默认响应够用 | ❌ 不用改 | `/api/oauth/organizations/{org}/billing/*`（211→220 已加多条，均被前缀兜住） |
| 新端点落前缀族下，但默认响应会误导（如 POST 落 GET/DELETE default → 405） | ✅ 收录进显式 case | `/api/frame/deploy/prepare`、`/upload`（本次收敛为 403） |
| 新顶层端点，404 兜底可接受（客户端优雅降级） | ❌ 不用改 | 多数控制面/遥测端点 |
| 新顶层端点，且其 404 会阻塞核心功能或致客户端崩溃/重试风暴 | ✅ 显式收录 | 罕见；需个案分析 |
| 新模型推理端点（非 `/v1/messages`） | ⚠️ 评估是否白名单 | 若 CC 新增并行推理路径，需决定转发或拦截 |

→ **结论：mcc 默认支持未来 CC 版本**；新版本发布后跑一次文末的「复审方法」，仅对「会劣化体验」的少数新端点显式收录即可。

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
| 20 | `GET` | `/v1/ultrareview/quota` | ultrareview 配额（≤2.1.206 客户端使用，保留以兼容旧版本；2.1.211 起 preflight 取代） |
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
| `POST` | `/api/frame/deploy/prepare`、`/api/frame/upload`（CC 2.1.220 多文件发布） | 403 `write_gate_disabled` |
| `GET` | `/api/frame/contract/*` | 404 `local_unavailable` |
| `GET/DELETE` | `/api/frame/{slug}` | 404 `not_found` |

> **2026-07-26 复审 CC 2.1.220**：对 `claude_code_src_2.1.211.js` 与 `2.1.220.js` 做路径字面量 diff，仅 2 条新增——`POST /api/frame/deploy/prepare` 与 `POST /api/frame/upload`（多文件 frame 发布的预检+上传），均在本族、已被 `/api/frame/` 前缀覆盖。客户端对两者用 `validateStatus:()=>true` + 非 2xx `break`（不重试）+ 兜底文案 "multi-file publish is not available here yet (server or proxy rejected it: …)"，故 mcc 即便返回拒绝也被优雅降级（无泄露/无重试风暴/无崩溃）。初版 default 返回的 405 对 POST 端点语义误导（`Allow: GET, DELETE`），本次收敛为与同族 `deploy/init|direct` 一致的 403 `write_gate_disabled`（`internal/proxy/frame.go`，feature `2026-07-26-frame-multifile-publish-write-gate`）。**结论：无新增未拦截泄露端点。**

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

## 复审新 CC 版本的方法

新 CC 版本发布后，用**路径字面量 diff** 把审查面收敛到「真正变化的端点」，再按上文判断标准分类。步骤：

1. **取新版本源码**：`/home/www/workspace/open-software/claude_code/073_claude_spy/claude_code_src_<ver>.js`（各版本一份）。

2. **抽路径字面量**（三引号统一处理，Python 最稳）：
   ```bash
   cat > /tmp/extract_paths.py << 'PYEOF'
   import re, sys
   src = open(sys.argv[1], encoding='utf-8', errors='replace').read()
   pat = re.compile(r'''["'`](/(?:api|v\d+|anthropic|mcp-registry|oauth)/[A-Za-z0-9_./${}:\-]+)["'`]''')
   for p in sorted(set(m.group(1) for m in pat.finditer(src))): print(p)
   PYEOF
   python3 /tmp/extract_paths.py claude_code_src_<旧>.js | LC_ALL=C sort -u > /tmp/old.txt
   python3 /tmp/extract_paths.py claude_code_src_<新>.js | LC_ALL=C sort -u > /tmp/new.txt
   ```
   （动态拼接的后缀片段可用更宽正则 `/(api|v\d+|...)/` → `/[A-Za-z0-9_./${}:\-]+` 再 diff 一次，捕捉 `/${x}` 类片段。）

3. **双向 diff**（`LC_ALL=C` 保证 comm 排序一致）：
   ```bash
   LC_ALL=C comm -13 /tmp/old.txt /tmp/new.txt   # 新版本新增
   LC_ALL=C comm -23 /tmp/old.txt /tmp/new.txt   # 新版本移除
   ```

4. **分类每条新增**：
   - **落在既有前缀族下**（如 `/api/frame/xxx`、`/api/oauth/organizations/{org}/xxx`）→ 已被族处理器接管，**默认不用改**；仅当族默认响应（如 POST 落 GET/DELETE default → 405）会误导时显式收录（参见 C 节 frame prepare/upload）。
   - **新顶层端点** → grep 其调用上下文（`Pi.post`/`_client.get`/`host:"xxx"` 分发 → 判断是否一阶 api.anthropic.com）；再查客户端响应处理（`validateStatus`、reason 识别、兜底文案）判断 404 是否优雅降级。优雅降级 → 不用改；阻塞核心/崩溃/重试风暴 → 显式收录。
   - **新模型推理端点**（非 `/v1/messages`）→ 评估是否加入 `endpoint_policy.go` 白名单（谨慎，仅在确认是新推理路径时）。

5. **验证**：`go test ./internal/proxy/...`（含 `TestFrameEndpointCompatibility`、`TestHandleHardcodedEndpoint`、`TestIsHardcodedEndpoint`）。

6. **归档**：在本清单对应小节补行 + 文末加复审纪要（版本号、新增数、结论）。

> 历次复审：2026-07-26 CC 2.1.220（相对 2.1.211）—— 仅 2 条新增（frame prepare/upload，均族内），无新增未拦截泄露端点；本次把 default-405 收敛为显式 403（feature `2026-07-26-frame-multifile-publish-write-gate`）。

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
