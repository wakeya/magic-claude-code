# CLI/Desktop 端点补全与 TLS 加固规格

本地页面：无（代理 handler + 证书 + 服务）
代理入口：`internal/proxy/hardcoded.go`、`internal/proxy/server.go`、`internal/proxy/handler.go`、`internal/cert/ca.go`、`internal/cert/cert.go`
参考来源：`mcc-endpoint-patch-plan.md`；MCC v0.5.0 源码及 `magic-claude-code-v0.5.0` 中的代码变更；在 MCC 代理后观察到的 Desktop 请求日志
技术栈：Go 1.26 标准库（`net/http`、`crypto/tls`、`crypto/x509`、`encoding/json`）
最后更新：2026-06-20
进度：9 / 9 已完成

## 整体分析（CLI 基线 + Desktop 新增）

### 问题总览

通过对 Claude Code CLI 流量以及在 MCC 代理后新增的 Desktop 流量的请求日志分析，识别出三类问题：

| 类别 | 端点 | 表现 | 根本原因 |
| --- | --- | --- | --- |
| 路径不匹配 | `POST /api/event_logging/v2/batch` | 404 | MCC 仅注册了 v1 路径 |
| 端点未注册 | `HEAD/GET /api/desktop/{platform}/{arch}/{type}/update` | 404 | Desktop 新增了一个 MCC 之前未拦截的探测路径 |
| 响应不够精确 | `policy_limits`、`bootstrap`、`settings` | 200 但客户端打印警告 | 空 `{}` 缺少必填字段 |

### 端点 1：事件上报 v2

Desktop 调用 `/api/event_logging/v2/batch`（v2），但 MCC 只注册了 `/api/event_logging/batch`（v1）。源码仅检查 `statusCode === 200`，不解析响应体。添加路径即可修复 404。

源码中的重试逻辑：最多 3 次重试（间隔 1s、3s），仅在 5xx/429 时重试。404 会立即停止，因此失败的请求无害但会产生日志噪声。

### 端点 2：桌面更新检查

Desktop 启动时自动检查更新：

```
HEAD /api/desktop/{platform}/{arch}/{type}/update?device_id={UUID}
GET  /api/desktop/{platform}/{arch}/{type}/update?device_id={UUID}
```

`{type}` 为 `msix` 或 `squirrel`，取决于安装方式。

源码解析逻辑（两处消费方）：

1. **版本检查**（`s7t()`）：fetch URL，解析 JSON，从顶层 payload 提取 `currentRelease`。非 OK 时静默返回 `null`。
2. **自动更新器**（`oTn()`）：MSIX 模式使用 Electron `autoUpdater`，`serverType: "json"`，期望 Squirrel/Nuts JSON 格式。

两个消费方都能优雅处理非 OK 响应（返回 `null` 或跳过），因此 404 不会破坏功能——只是每次启动浪费一次请求。返回 `200` + `{"currentRelease": "<当前版本>"}` 可告知 Desktop "已是最新"。

### 端点 3：策略限制

当前响应：`{}`（空对象）。

源码验证逻辑（`k9t`）检查：
1. 值不为 null
2. typeof 为 "object"
3. 不是数组
4. `restrictions` 必须是非 null、非数组的对象
5. `compliance_taints` 必须是 undefined 或数组

空 `{}` 通过了检查 1–3，但在检查 4 处失败（`restrictions` 为 undefined）。验证失败会打印警告日志并降级策略执行。添加 `restrictions: {}` 和 `compliance_taints: []` 可消除警告。

### 端点 4：启动引导

当前响应：`{}`（空对象）。

源码解析两个消费方的字段：
- CLI 期望 `r.client_data`（对象）和 `r.additional_model_options`（数组）
- Desktop（Cowork）期望 `r.cwk_cfg_key`（string 或 null），用于 Cowork system prompt 变体选择

空 `{}` 使三者均为 `undefined`，各消费方降级为默认值。同时返回三字段匹配所有消费者的期望结构。

### 端点 5：远程设置

当前响应：`{}`（空对象）。

源码解析：`data.settings`（期望对象）。空 `{}` 使 `settings` 为 `undefined`，降级为 `{}`。源码对此有容忍（404/204 也视为空设置）。包装为 `{"settings": {}}` 匹配期望结构，消除降级日志。

### TLS 证书链问题

从 v0.5.0 源码分析中识别出三个证书相关问题：

#### 问题：证书命名不一致

CA 证书使用 `Organization: "Claude Proxy Local CA"`，服务器证书使用 `Organization: "Claude Proxy"`。这些名称与产品身份（"MCC"）不匹配。重命名为 `MCC Proxy Local CA` 和 `MCC Proxy` 可使证书身份与项目一致。已有证书不受影响（加载时只校验 PEM 格式）；删除旧证书并重启即可生成新名称的证书。

#### 问题：server.crt 证书链不完整

`SaveServerCert` 只写入服务器证书的单个 PEM 块，不包含签发的 CA 证书。TLS 握手时，未预装 CA 的客户端无法构建完整信任链（`server → CA → root`）。修复方案是在服务器证书后追加 CA 证书 DER，写入同一 PEM 文件。Go 的 `tls.LoadX509KeyPair` 会自动解析所有 CERTIFICATE 块构建证书链。

签名变更：`SaveServerCert(certDER, caCertDER []byte, privateKey)` — 新增 `caCertDER` 参数。调用方 `EnsureServerCert` 传入 `caCertDER`。

#### 问题：TLS 握手错误缺少 SNI 域名

`http.Server.ListenAndServeTLS` 将握手委托给 Go 内部处理，产生如下日志：
```
http: TLS handshake error from 127.0.0.1:14638: remote error: tls: unknown certificate
```

日志中缺少 SNI 域名，无法判断是哪个域名触发的失败。

修复方案是替换 `ListenAndServeTLS` 为自定义 `tlsListener`：
- `GetCertificate` 回调将 SNI（`hello.ServerName`）存入 `sync.Map`，以 `conn.RemoteAddr().String()`（IP:Port）为 key。
- `Accept()` 显式调用 `tlsConn.Handshake()`；失败时从 store 取出 SNI 并记录 `TLS handshake error from <addr> (SNI=<domain>): <err>`。
- 握手成功时清理 SNI store 条目。
- `tls.Config` 显式设置 `MinVersion: tls.VersionTLS12`。
- `GetCertificate` 回调返回预加载的 `*tls.Certificate`，使握手正常完成。
- 握手失败时返回 `handshakeError`（实现 `net.Error.Temporary() = true`），使 `http.Server.Serve` 不因单个握手失败而退出。

SNI 可用性取决于握手阶段：

| 场景 | SNI 是否可用 | 原因 |
| --- | --- | --- |
| 收到 ClientHello | 是 | SNI 在 ClientHello 扩展字段中 |
| 客户端在 ClientHello 前断开 | 否 | `GetCertificate` 未被调用，日志显示 `(no SNI)` |
| TLS 版本/密码套件不匹配 | 否 | 握手在 `GetCertificate` 之前失败 |
| 证书验证失败 | 是 | SNI 已在 `GetCertificate` 阶段捕获 |

### 请求/响应日志缺少目标域名

当前请求入口和响应出口日志仅使用 `r.URL.Path`，缺少 `r.Host`。当代理多个域名（如 `api.anthropic.com` 和其他拦截域名）时，日志无法区分请求的目标域名。

修复方案是在 `handler.go` 的请求入口日志和响应出口日志中加入 `r.Host`：

```
# 修改前
[abc123] >>> POST /v1/messages model=...
[abc123] <<< 200 model=...

# 修改后
[abc123] >>> POST api.anthropic.com/v1/messages model=...
[abc123] <<< 200 api.anthropic.com/v1/messages model=...
```

`r.Host` 来源于 HTTP 请求的 Host 头，应作为调试元数据对待，不作为可信的安全边界。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成 | event_logging v2 路径支持 | `hardcoded.go`（路径匹配） | 单元测试：v2 路径返回 200 |
| 2 | 已完成 | 桌面更新检查端点 | `hardcoded.go`（精确匹配 + handler） | 单元测试：HEAD 200，GET JSON |
| 3 | 已完成 | 策略限制响应精确化 | `hardcoded.go`（专用 handler） | 单元测试：响应包含 `restrictions` |
| 4 | 已完成 | 启动引导响应精确化 | `hardcoded.go`（handler 更新） | 单元测试：响应包含 `client_data` |
| 5 | 已完成 | 远程设置响应精确化 | `hardcoded.go`（专用 handler） | 单元测试：响应包含 `settings` |
| 6 | 已完成 | 证书命名一致性 | `ca.go`、`cert.go` | 构建；验证生成的证书 subject 名称 |
| 7 | 已完成 | server.crt 完整证书链 | `cert.go`（SaveServerCert + EnsureServerCert） | 单元测试：server.crt 含 2 个 PEM 块 |
| 8 | 已完成 | TLS 握手 SNI 日志 | `server.go`（自定义 tlsListener） | 单元测试：SNI 出现在错误日志中 |
| 9 | 已完成 | 请求/响应日志域名 | `handler.go`（添加 r.Host） | 手动：日志包含域名 |

## 需求

### 交付物

1. `POST /api/event_logging/v2/batch` 被拦截并返回 `200 {}`，与现有 v1 handler 一致。v1 和 v2 路径都路由到 `handleEventLogging`。
2. 匹配 `/api/desktop/**/update` 的 `HEAD` 和 `GET` 请求被拦截。HEAD 返回 `200` 空响应体。GET 返回 `200 {"currentRelease": "<版本>"}`，其中 `<版本>` 为 Desktop 版本常量。`currentRelease` 为顶层 JSON 字段，匹配 Desktop 探测协议契约。
3. `GET /api/claude_code/policy_limits` 返回 `{"restrictions": {}, "compliance_taints": []}`，不再返回 `{}`。
4. `GET /api/claude_cli/bootstrap` 返回 `{"client_data": {}, "additional_model_options": [], "cwk_cfg_key": null}`，不再返回 `{}`。同时覆盖 CLI 和 Desktop（Cowork）两个消费方的期望字段。
5. `GET /api/claude_code/settings` 返回 `{"settings": {}}`，不再返回 `{}`。
6. 单元测试覆盖以上五项变更。
7. 不引入对现有端点行为的回归。
8. CA 证书 `Organization` 和 `CommonName` 为 `MCC Proxy Local CA`；服务器证书 `Organization` 为 `MCC Proxy`。已有证书不会自动重新生成——仅新生成的证书使用新名称。
9. `SaveServerCert` 将服务器证书和 CA 证书 PEM 块同时写入 `server.crt`，构成完整信任链。`EnsureServerCert` 将 CA DER 传给 `SaveServerCert`。
10. TLS 握手错误在可用时包含 SNI 域名（`(SNI=api.anthropic.com)` 或 `(no SNI)`）。
11. `handler.go` 的请求入口和响应出口日志包含 `r.Host` + `r.URL.Path`。`r.Host` 只是来自请求的调试元数据，不是可信安全边界。
12. 单元测试覆盖证书链（多 PEM 块）和 TLS SNI 日志。
13. server.go TLS listener 重构后，不引入对现有测试或端点行为的回归。

### 约束

1. 任务 1–5 限于 `internal/proxy/hardcoded.go`。
2. 桌面更新版本常量（`1.13576.0`）应定义为命名常量，不使用魔法字符串，便于维护。
3. `policy_limits` 和 `settings` 需从 `handleEmptyResponse` 分支组中提取为专用 handler。
4. 桌面更新匹配不可过于宽泛——在 `isHardcodedEndpoint` 中使用 `HasPrefix("/api/desktop/") && HasSuffix("/update")` 精确匹配，避免拦截无关的桌面 API 流量，同时避免对未命中路径触发 `drainRequestBody` 副作用。
5. 响应体使用 `map[string]any` 并通过现有的 `writeJSONResponse` 辅助函数写入。
6. `SaveServerCert` 签名变更（`caCertDER` 参数）必须更新所有调用点；唯一调用方是 `EnsureServerCert`。
7. 自定义 `tlsListener` 必须保留现有 `http.Server` 配置（超时、handler、统计中间件）。仅 TLS 层被重构。
8. SNI store 使用 `sync.Map` 避免锁竞争；key 为 `conn.RemoteAddr().String()`（IP:Port）；条目在成功和失败路径上均被清理。
9. `tls.Config` 显式设置 `MinVersion: tls.VersionTLS12`；`GetCertificate` 回调返回预加载的 `*tls.Certificate`。
10. 握手失败时返回 `handshakeError`（实现 `net.Error.Temporary() = true`），使 `http.Server.Serve` 不退出。
11. 已有证书不迁移——仅新生成的证书使用更新后的名称和链格式。迁移步骤为 `rm data/ca.crt data/ca.key data/server.crt data/server.key` 后重启，并在客户端 OS 重新安装新 CA 到信任链。

### 边界情况

1. Desktop 发送 `GET /api/desktop/win32/x64/msix/update?device_id=...` — 被精确匹配，返回版本 JSON。
2. Desktop 发送 `GET /api/desktop/darwin/arm64/squirrel/update` — 同样被匹配。
3. 无关路径如 `/api/desktop/something_else` — 不被拦截（不满足 `HasSuffix("/update")`），不进入 `handleHardcodedEndpoint`，不会触发 `drainRequestBody`。
4. 添加 v2 后 v1 路径仍正常工作。
5. HEAD 请求到桌面更新端点 — 返回 200 无响应体。
6. 客户端在发送 ClientHello 前断开 — TLS 错误日志显示 `(no SNI)`。
7. 客户端发送含 SNI 的 ClientHello 后证书验证失败 — 日志包含 `(SNI=<域名>)`。
8. 现有 `server.crt` 是单个 PEM 块 — `LoadServerCert` 使用 `pem.Decode` 只读第一个块；修复后新证书有两个块，但现有加载逻辑仍正常（第一个块是服务器证书）。
9. `SaveServerCert` 仅在初始证书生成时调用；已有证书被加载而非重新保存。

### 非目标

1. 不为 Claude Desktop 实现实际的更新分发（MCC 不是 Desktop 更新服务器）。
2. 不修改 hosts 重定向。
3. 不为端点响应添加配置 UI。
4. 不实现完整的 Squirrel/Nuts 发布清单——只返回 `currentRelease` 字段，用于"无更新"信号。
5. 不自动迁移或重新生成已有证书。
6. 不实现客户端 OS 的自动 CA 信任安装（作为手动步骤文档说明）。
7. 不实现 mTLS 或客户端证书验证。

## 任务详情

### 任务 1：事件上报 v2 路径

#### 需求

**目标** — 拦截 Desktop 使用的 v2 事件上报端点（替代 v1）。

**成果** — `/api/event_logging/v2/batch` 返回 `200 {}`，与 v1 完全一致；两个路径路由到同一 handler。

**证据** — 单元测试发送 POST 到 v2 路径，断言 200 响应。

**约束** — 不得移除 v1 路径；两者共存。

**边界** — 预期仅 POST 方法；其他方法仍返回 200（现有行为，无害）。

**验证** — v2 路径单元测试；手动检查日志不再出现 v2 的 404。

#### 计划

1. 在 `isHardcodedEndpoint` 的 `exactMatches` 中添加 `"/api/event_logging/v2/batch"`。
2. 在事件上报的 switch case 中添加 `path == "/api/event_logging/v2/batch"`（与现有 v1 case 用逗号分隔）。

#### 验证

- [x] POST `/api/event_logging/v2/batch` 返回 200。
- [x] POST `/api/event_logging/batch`（v1）仍返回 200。

### 任务 2：桌面更新检查端点

#### 需求

**目标** — 拦截桌面更新检查请求，响应"无可用更新"。

**成果** — HEAD `/api/desktop/**/update` 返回 200；GET 返回 `{"currentRelease": "1.13576.0"}` 作为顶层 JSON 字段。

**证据** — 单元测试发送 HEAD 和 GET 到桌面更新路径，断言正确响应。

**约束** — 仅匹配 `/api/desktop/` 下以 `/update` 结尾的路径；不拦截其他桌面 API 路径。版本定义为命名常量 `desktopCurrentRelease = "1.13576.0"`。GET 响应保持顶层 `currentRelease` 以匹配 Desktop 探测协议契约，不改变 payload 结构。

**边界** — `/api/desktop/win32/x64/msix/update?device_id=...`（带查询字符串）；`/api/desktop/darwin/arm64/squirrel/update`；HEAD vs GET。

**验证** — 两种方法的单元测试；验证查询参数不影响匹配。

#### 计划

1. 在 `isHardcodedEndpoint` 中新增专用分支：`strings.HasPrefix(path, "/api/desktop/") && strings.HasSuffix(path, "/update")`。
2. 在 switch 中添加相同 `HasPrefix + HasSuffix` 条件的 case。
3. 实现 `handleDesktopUpdate(w, r)`：HEAD → 200；GET → 含 `currentRelease` 的 JSON。
4. 定义常量 `desktopCurrentRelease`。

#### 验证

- [x] HEAD `/api/desktop/win32/x64/msix/update` 返回 200 空响应体。
- [x] GET 返回 `{"currentRelease": "1.13576.0"}`。
- [x] 路径 `/api/desktop/other` 不被拦截（不满足 `HasSuffix("/update")`，不进入硬编码处理）。

### 任务 3：策略限制响应精确化

#### 需求

**目标** — 返回能通过客户端策略限制验证且无警告的响应。

**成果** — `GET /api/claude_code/policy_limits` 返回 `{"restrictions": {}, "compliance_taints": []}`。

**证据** — 单元测试断言响应体包含 `restrictions` 和 `compliance_taints`。

**约束** — 从 `handleEmptyResponse` 分支组中提取为专用 handler。

**边界** — 标准 GET 处理，无特殊边界。

**验证** — 响应体验证。

#### 计划

1. 从 `handleEmptyResponse` case 组中移除 `path == "/api/claude_code/policy_limits"`。
2. 添加专用 switch case 调用 `handlePolicyLimits(w)`。
3. 实现 `handlePolicyLimits(w)`，返回增强后的响应。

#### 验证

- [x] 响应体包含 `"restrictions": {}` 和 `"compliance_taints": []`。

### 任务 4：启动引导响应精确化

#### 需求

**目标** — 返回具有期望字段结构的启动引导响应，同时覆盖 CLI 和 Desktop 两个消费方。

**成果** — `GET /api/claude_cli/bootstrap` 返回 `{"client_data": {}, "additional_model_options": [], "cwk_cfg_key": null}`。

**证据** — 单元测试断言响应体包含 `client_data`、`additional_model_options` 和 `cwk_cfg_key`。

**约束** — 更新现有 `handleBootstrap` 函数体。

**边界** — 无。

**验证** — 响应体验证。

#### 计划

1. 更新 `handleBootstrap`，返回 `{"client_data": {}, "additional_model_options": [], "cwk_cfg_key": null}`。

#### 验证

- [x] 响应体包含 `"client_data": {}`、`"additional_model_options": []` 和 `"cwk_cfg_key": null`。

### 任务 5：远程设置响应精确化

#### 需求

**目标** — 返回具有期望嵌套结构的设置响应。

**成果** — `GET /api/claude_code/settings` 返回 `{"settings": {}}`。

**证据** — 单元测试断言响应体包含 `settings`。

**约束** — 从 `handleEmptyResponse` 分支组中提取为专用 handler。

**边界** — 无。

**验证** — 响应体验证。

#### 计划

1. 从 `handleEmptyResponse` case 组中移除 `path == "/api/claude_code/settings"`。
2. 添加专用 switch case 调用 `handleRemoteSettings(w)`。
3. 实现 `handleRemoteSettings(w)`，返回 `{"settings": {}}`。

#### 验证

- [x] 响应体包含 `"settings": {}`。

### 任务 6：证书命名一致性

#### 需求

**目标** — 将证书 subject 名称与产品身份（"MCC"）对齐，替代遗留的 "Claude Proxy" 命名。

**成果** — CA 证书 `Organization` 和 `CommonName` 变为 `MCC Proxy Local CA`；服务器证书 `Organization` 变为 `MCC Proxy`。服务器证书 `CommonName` 保持 `api.anthropic.com` 不变。

**证据** — 删除旧证书并重启后，`openssl x509 -in data/ca.crt -noout -subject` 显示 `O=MCC Proxy Local CA, CN=MCC Proxy Local CA`。

**约束** — 已有证书不自动重新生成；仅新生成的证书使用新名称。迁移步骤为 `rm data/ca.crt data/ca.key data/server.crt data/server.key` 后重启。删除 CA 后需要在客户端 OS 重新安装新 CA 到信任链。

**边界** — 使用已有证书的用户在重新生成前看不到变化；这是预期行为。

**验证** — 生成全新证书；通过 openssl 或 Go 的 `x509.ParseCertificate` 验证 subject 名称。

#### 计划

1. 在 `ca.go` 的 `GenerateCA` 中，将 `Organization` 和 `CommonName` 从 `"Claude Proxy Local CA"` 改为 `"MCC Proxy Local CA"`。
2. 在 `cert.go` 的 `GenerateServerCert` 中，将 `Organization` 从 `"Claude Proxy"` 改为 `"MCC Proxy"`。`CommonName` 保持 `api.anthropic.com`。

#### 验证

- [x] CA 证书 subject 包含 `MCC Proxy Local CA`。
- [x] 服务器证书 subject 包含 `MCC Proxy`。
- [x] 服务器证书 CN 保持 `api.anthropic.com`。

### 任务 7：server.crt 完整证书链

#### 需求

**目标** — 在 `server.crt` 中服务器证书后追加 CA 证书，使 TLS 客户端无需预装 CA 即可构建完整信任链。

**成果** — `server.crt` 包含两个 CERTIFICATE PEM 块（服务器证书 + CA 证书）。`tls.LoadX509KeyPair` 自动解析两者。

**证据** — 单元测试调用 `SaveServerCert` 传入 mock DER，然后读取 `server.crt` 断言有两个 PEM 块。`openssl crl2pkcs7 -nocrl -certfile data/server.crt | openssl pkcs7 -print_certs` 显示两份证书。

**约束** — `SaveServerCert` 签名新增 `caCertDER []byte` 参数。唯一调用方是 `EnsureServerCert`，其作用域内有 `caCertDER`。已有的单块证书仍可正确加载（第一个块是服务器证书）。

**边界** — 现有 `server.crt` 文件是单块；`LoadServerCert` 使用 `pem.Decode` 只读第一个块——仍然正确。新证书将有两个块。

**验证** — 多 PEM 块输出的单元测试；手动 openssl 检查。

#### 计划

1. 为 `SaveServerCert` 添加 `caCertDER []byte` 参数。
2. 在编码服务器证书 PEM 块后，编码第二个 PEM 块写入 `caCertDER`。
3. 更新 `EnsureServerCert` 调用点：`m.SaveServerCert(serverCert, caCertDER, serverKey)`。

#### 验证

- [x] `server.crt` 包含两个 CERTIFICATE PEM 块。
- [x] `LoadServerCert` 仍返回服务器证书 DER（第一个块）。
- [x] `tls.LoadX509KeyPair` 在新文件上成功。

### 任务 8：TLS 握手 SNI 日志

#### 需求

**目标** — 在 TLS 握手阶段捕获 SNI 域名，并将其包含在握手错误日志中以便调试。

**成果** — TLS 握手错误记录为 `TLS handshake error from <addr> (SNI=<域名>): <err>` 或 `TLS handshake error from <addr> (no SNI): <err>`。

**证据** — 单元测试创建带 mock TLS 配置的 `tlsListener`，触发握手失败，断言日志输出包含 `SNI=` 或 `no SNI`。

**约束** — 替换 `ListenAndServeTLS` 为 `tls.LoadX509KeyPair` + 自定义 `net.Listener` + `server.Serve`。保留所有现有 `http.Server` 配置（超时、handler、统计中间件）。`tls.Config` 显式设置 `MinVersion: tls.VersionTLS12`。`GetCertificate` 回调将 `hello.ServerName` 存入 `sync.Map`（key 为 `conn.RemoteAddr().String()`），并返回预加载的 `*tls.Certificate`。在成功和失败路径上均清理条目。握手失败时返回 `handshakeError`（`Temporary() = true`）使 Serve 不退出。

**边界** — 客户端在 ClientHello 前断开（日志 `(no SNI)`）；客户端发送 SNI 后证书验证失败（日志 `SNI=<域名>`）；成功握手清理 SNI store。

**验证** — SNI 捕获的单元测试；使用不受信任客户端的手动日志观察。

#### 计划

1. 在 `server.go` 的 `Start` 中，用 `tls.LoadX509KeyPair` 加载证书对。
2. 构建 `tls.Config`，设置 `MinVersion: tls.VersionTLS12`，`GetCertificate` 回调存储 `hello.ServerName` 到 `sync.Map`（以 `conn.RemoteAddr().String()` 为 key）并返回 `&certPair`。
3. 创建 `net.Listen("tcp", addr)`，包装为 `tlsListener` 结构体。
4. 实现 `tlsListener.Accept`：接受连接，用 `tls.Server` 包装，调用 `Handshake()`，失败时 `LoadAndDelete` 取 SNI 并记录日志，返回 `handshakeError`；成功时 `Delete` 清理 SNI store。
5. 调用 `s.server.Serve(tlsLn)`。

#### 验证

- [x] 握手错误日志包含 `(SNI=<域名>)` 或 `(no SNI)`。
- [x] 成功握手不产生错误日志。
- [x] 服务正常启动并处理请求。
- [x] 现有 `http.Server` 超时和 handler 保留。

### 任务 9：请求/响应日志域名

#### 需求

**目标** — 在请求入口和响应出口日志中包含 `r.Host` + `r.URL.Path`，提升可追踪性。

**成果** — 请求日志显示 `>>> POST api.anthropic.com/v1/messages ...`；响应日志显示 `<<< 200 api.anthropic.com/v1/messages ...`。

**证据** — 通过代理发送请求后手动观察日志，域名前缀出现在路径前。

**约束** — 修改 `handler.go` 中的请求入口和响应出口两个 `log.Printf` 调用。`r.Host` 和 `r.URL.Path` 以 `%s%s` 格式拼接，形成完整 URL 路径。`r.Host` 来源于 HTTP 请求 Host 头，应作为调试元数据对待，不作为可信的安全边界。

**边界** — `r.Host` 对于无 Host 头的 HTTP/1.0 请求可能为空；实际中极少出现。

**验证** — 手动测试：通过代理发送请求并检查日志。

#### 计划

1. 在 `handler.go` 请求入口日志中，在 `r.Method` 后添加 `r.Host` + `r.URL.Path`。
2. 在 `handler.go` 响应出口日志中，新增 `r.Host` + `r.URL.Path`（原日志无路径字段）。

#### 验证

- [x] 请求入口日志包含域名。
- [x] 响应出口日志包含域名。
