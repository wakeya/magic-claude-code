# Changelog

所有重要变更记录在此文件中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。
版本号对应 git tag（semver，自 v0.1.0 起），与 `release-notes/vX.Y.Z.md` 一一对应；早期条目以日期标识。

---

## v0.16.2 (2026-07-14)

### Fixed
- **各平台 Release 下载包附带完整的宿主机配置脚本与文档**：修复此前打包步骤不分平台只附带 `setup-host.sh` / `setup-host.ps1`，导致 Windows 包缺少 `start-mcc.ps1`、`stop-mcc.ps1`、`register-mcc-task.ps1`，非 Windows 包缺少 `docker-host-helper.sh`，且所有平台都缺少 `README.en.md`、`SCRIPTS.md`、`SCRIPTS.en.md` 的问题（#12）。现在按平台附带对应脚本与双语文档，便于在 bootstrap 自动配置失败时手动完成 hosts + CA 配置与容器宿主维护。
- **消除 providerquota 调度器测试在 `-race` 下偶发的 `SQLITE_BUSY` flaky**：测试 DB 此前以 rollback-journal 模式运行且未设置 `busy_timeout`，`scanAndQuery` 的同步 `store.Get` 与异步 `SaveUpsert` 在写锁上冲突并立即返回 `SQLITE_BUSY`，导致 `TestSchedulerPeriodicScanNoJitter` 在 CI 偶发失败（run 29314320010）。现已将测试 DSN 对齐生产配置（WAL + `synchronous(NORMAL)` + `busy_timeout(5000)`）并补充并发回归测试。本项为测试 / CI 质量修复，不影响运行时行为。

### Docs
- providerquota SQLite BUSY flaky 修复的 feature spec / 审查归档（中英双语）：`sdd-docs/features/2026-07-14-providerquota-sqlite-busy-flaky/`

---

## v0.16.1 (2026-07-14)

### Fixed
- **Linux bootstrap 自动配置 SSL_CERT_FILE 与完整证书链**：修复透明模式下 Claude Code / Bun 的部分后台 TLS 请求不稳定读取 `NODE_EXTRA_CA_CERTS`，导致长对话后辅助请求对 mcc 证书链报 `unknown_ca` / `bad record MAC` 的问题。Linux 二进制启动时现在会确保当前 mcc CA 已安装并验证进完整系统 CA bundle，再持久化 `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`；`server.crt` 同时包含叶子证书和 CA 证书，避免只发送叶子证书造成链不完整。Docker 场景保持不写宿主 profile，并通过文档 / helper 明确宿主机配置边界。
- **自动切换事件页正确显示供应商名称与 ID**：修复 `GET /api/failover/events` 返回的部分历史 / 恢复 / 耗尽事件缺少供应商展示名，导致管理面板“切换事件”页只能显示内部 ID 或空值的问题。接口现在会基于当前仍存在的 Provider 配置回填 `FromProviderName` / `ToProviderName`，前端事件表新增“供应商 ID”列，供应商列优先展示名称、ID 列独立展示源 / 目标 Provider ID，便于区分同名供应商并追踪自动切换路径。

### Docs
- Linux SSL_CERT_FILE 自动引导与证书链修复 feature spec / 审查归档（中英双语）：`sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/`
- 自动切换事件供应商显示修复审查归档（中英双语）：`sdd-docs/features/2026-07-13-failover-event-provider-display/`

---

## v0.16.0 (2026-07-13)

### Added
- **供应商额度自动故障切换（默认关闭）**：新增 `AutoFailoverEnabled` 配置与认证后的 `GET/PUT /api/providers/failover` 开关。开启后，仅对默认路由的模型请求生效：当当前默认 Provider 出现明确的额度耗尽、凭据失效、模型部署不可用或供应商可用性故障时，代理会在响应写回客户端前尝试候选 Provider；第一个返回 `<400` 的候选成为新的全局默认 Provider，并且客户端只收到最终成功响应。会话内 `/model` 选择产生的 `ExposedModel` 路由不会被自动切换，避免覆盖用户在当前会话中的显式模型选择。
- **故障分类与供应商摘除状态**：新增 `internal/failover`，基于结构化错误信号而非裸 HTTP 状态码判断是否可切换。支持识别 1308（5 小时额度耗尽）、1310（周/月额度耗尽）、`quota exhausted`、`no healthy deployments for this model`、invalid API key、Cloudflare 拦截、502/529、连接重置/超时等信号；明确不切换 1210 工具参数错误、tool_reference/工具兼容错误、普通请求错误、404 model_not_found、上下文窗口满等请求/模型问题。额度/部署/可用性故障按重置时间或短冷却自动恢复；凭据失效必须等待 Token 实际变更或供应商测试成功后恢复。
- **默认 Provider 回放与持久化切换**：`ResolveRoute` 标识默认路由与 exposed route，代理在自动切换时从原始客户端输入重新构造候选请求，重新应用候选 Provider 的 URL、Token、header、模型映射与 Anthropic/OpenAI 格式转换；既有同 Provider 429 retry 仍先运行。切换成功通过原子配置更新持久化 `ActiveProviderID`，并处理 `ActiveProviderID` 为空、缺失或指向 disabled Provider 时的 fallback 默认供应商场景。
- **全局切换事件审计**：新增认证后的 `GET /api/failover/events?limit=1..100` 与管理面板“切换事件”一级 tab。事件记录 `switched`、`retry_failed`、`exhausted`、`recovered` 等结果，按最新优先展示，最多保留 1,000 条且最长 30 天；事件是 MCC 全局观测数据，不绑定 Claude Code 会话，不写入 `~/.claude/projects` JSONL，也不进入会话导出。
- **供应商排序作为自动切换优先级**：新增 `PUT /api/providers/order` 与供应商管理页拖拽排序。自动切换候选先按“同映射模型”分组，再按 fallback 分组，两个分组内部均遵循用户拖拽后的 Provider 列表顺序。供应商卡片新增优先级编号、拖拽手柄、上移/下移可达性文案与自动切换说明 tooltip；排序后立即重新编号。

### Security
- **切换事件脱敏与认证边界**：failover 开关、事件查询和排序 API 均走管理端认证；事件表和 API 响应不保存/返回 API Token、请求体、响应体或带 query 的原始 URL。删除 Provider 后，历史事件保留名称用于展示，但响应中抹空已删除 Provider ID，避免悬空引用继续被当成有效配置。
- **凭据恢复 fail-closed**：额度快照只能清除额度类摘除状态，不能清除 401 凭据失效；编辑名称、模型、Base URL 或测试失败也不会恢复凭据状态。只有已保存的非空 API Token 实际改变，或 `POST /api/providers/{id}/test` 成功，才清除 credential failure。

### Changed
- **配置写入改为原子更新路径**：开关更新、Provider 排序和自动切换持久化 active provider 均走 `configStore.Update(func(*Config) error)`，避免管理端保存与代理自动切换并发时互相覆盖。
- **供应商排序不再静默改变有效默认 Provider**：当 `ActiveProviderID` 为空、缺失或指向 disabled Provider 时，排序前会捕获当前有效默认供应商，并在排序后将其固化为 `ActiveProviderID`，避免单纯拖拽排序导致默认 Provider 被意外切换。
- **额度快照参与摘除/恢复协调**：新鲜的 100% 额度快照可摘除对应 Provider 至重置时间；容量恢复后可清除额度类摘除状态，并写入 `recovered` 事件。

### Docs
- 供应商额度自动故障切换 feature spec 与审查归档（中英双语）：`sdd-docs/features/2026-07-12-provider-quota-failover/`

### 注意
- 自动故障切换默认关闭；开启后只影响全局默认 Provider，不影响会话内 `/model` 显式选择。
- 如果所有候选 Provider 都不可用，客户端仍收到最终失败响应，并在事件页记录 `exhausted` 或 `retry_failed`。

## v0.15.3 (2026-07-11)

### Fixed
- **TLS 握手失败时识别客户端明文 alert（避免 `unknown_ca` 被误报为 `bad record MAC`）**：新增 `alertDetectingConn`——一个增量 TLS record parser，在 `handleConn` 中包装每个入站连接，握手失败时检测客户端发来的明文 `ContentType=21, length=2` alert（如 `unknown_ca`、`bad_certificate`），把真实原因追加到失败日志。修复代理（TLS 终结）场景下，客户端证书校验失败后发明文 alert、代理用 handshake key 解明文 alert 必然 AEAD 失败、日志表现为误导性 `bad record MAC` 的问题。parser 无缓冲（只保留 2 字节 alert）、严格结构化解析 record header+payload（不搜 magic bytes，避免 handshake/AppData payload 内同序列误判）、热路径零分配。注意：这是诊断增强（让日志诚实），不是客户端 CA 信任修复——后者需把代理 CA 装进系统 CA 库（详见 CLAUDE.md 常见问题）。
- **`alertName` 映射修正（111–117 区间错位）**：修正 `alertName` 在 111–117 区间的整体错位——补 111 (`certificate_unobtainable`)、112 (`unrecognized_name`)；修正 113–116；删除错误的 `case 117`；新增 116 (`certificate_required`)、121 (`encrypted_client_hello_required`)。与 Go 1.26 标准库 `crypto/tls` 对齐。

### Docs
- TLS 明文 alert 诊断 feature spec 与审查归档（中英双语）：`sdd-docs/features/2026-07-11-tls-plaintext-alert-diagnostics/`
- 常见问题增加 "bad record MAC / unknown_ca" 条目：根因是客户端某条 TLS 路径不信任代理 CA（非代理/TLS 协议问题），修复是把代理 CA 装进系统 CA 库。

## v0.15.2 (2026-07-10)

### Security
- **更新器下载 URL 脱敏（fail-closed，防凭据 / 签名 URL 泄露到管理 API）**：`Updater.DownloadAndApply` / `downloadFileWithLimit` 的所有错误路径（请求构造、`client.Do`、状态码、body 读取、大小限制）改为返回 origin-only 固定消息，仅暴露 URL origin（scheme + host + port）+ 稳定操作类别（`was canceled` / `timed out` / `failed`）+ HTTP 状态码 + 大小上限，丢弃底层错误文本与 unwrap 链。修复 `client.Do` 网络层失败时 `*url.Error` 嵌套字段泄露完整请求 URL（userinfo / path / query / fragment / redirect `Location`）到 `POST /api/update/apply` JSON 响应的问题（强制 HTTPS_PROXY 复现稳定暴露 query secret）。下载 URL 解析严格化：仅接受带 host 的绝对层级 http/https URL，transport 前拒绝 opaque / relative / 不支持 scheme / 缺 host / 畸形 escape；checksum URL 改用 `url.ResolveReference` 安全派生（替换 `strings.LastIndex` 切片，避免选到 query 内的斜杠）

### Changed
- **release.sh 平台连接失败健壮性**：`STATUS=$(curl ...)` 统一加 `|| true`，防止某平台（如关机的自托管 GitLab）连接失败时 curl 退出码经 `set -e` 导致整个发版脚本退出；连接失败时走 warn 分支跳过该平台

### Docs
- updater URL 脱敏 feature spec 与审查归档（中英双语，fail-closed origin-only 设计）：`sdd-docs/features/2026-07-10-updater-url-redaction/`

---

## v0.15.1 (2026-07-10)

### Security
- **升级 echarts 6.0.0 → 6.1.0 修复 moderate XSS**（GHSA-fgmj-fm8m-jvvx）：管理面板图表渲染相关的跨站脚本漏洞，影响生产前端
- **升级 vite 8.0.8 → 8.1.4 修复 high 漏洞**（GHSA-v6wh-96g9-6wx3 + GHSA-fx2h-pf6j-xcff）：仅影响 Windows 开发环境 dev server，不影响生产构建产物
- **npm audit**：2 vulnerabilities → 0

### Changed
- **release.sh 支持 RELEASE_REF 补发历史版本**：新增 `RELEASE_REF` 环境变量，默认 `main`（行为不变），设 `RELEASE_REF=<tag>` 时 checkout 该 tag 构建真正的历史版本二进制。原脚本强制 checkout main，补发历史版本会在最新代码上构建并注入旧版本号，产物不正确

### Docs
- npm audit 修复 feature spec 与审查记录（中英双语）：`sdd-docs/features/2026-07-10-npm-audit-fix/`

---

## v0.15.0 (2026-07-10)

### Added
- **Claude Code 端点兼容本地拦截（fail-closed 守卫）**：新版 Claude Code 客户端（2.1.206）引入大量控制面 / 遥测 / 插件 / design 端点（`/v1/logs`、`/api/frame/*`、`/api/ws/speech_to_text/voice_stream`、`/v1/design/*` 等），旧版 MCC 把这些未识别端点全部转发给上游模型供应商，浪费 token、产生噪音错误、并可能泄露客户端元数据。新版改为 fail-closed 路由：已知本地端点本地响应、已知模型端点（`POST /v1/messages`、`POST /anthropic/v1/messages`）转发配置的 Provider、其余未知非模型端点本地拦截并只记录 method / path / query
- **2.1.206 新端点本地兼容响应**：为客户端新增端点提供 schema 兼容的本地响应——MCP connector search / suggest / list 返回 `{"results":[]}`；plugin / skill search 从本地 marketplace / skill manifests（`~/.claude`，best-effort 读取，缺失回退空数组，兼容 Docker 未挂载场景）填充结果；installed skill health（`GET /api/claude_code/skills`）返回 `{"skills":[...]}`；frame（list / track / deploy / contract）、design consent / MCP、voice stream 返回精确状态码与错误结构，避免客户端解析异常
- **`/v1/models` 本地模型发现**：改用 MCC 配置生成模型列表返回客户端，不再转发给上游供应商

### Security
- **fail-closed 端点守卫（防客户端元数据泄露）**：未知非模型端点（如 `/favicon.ico`、`/v1/logs`、`/v1/metrics`、`/v1/traces` 及各类控制面端点）不再转发给模型供应商，本地拦截并仅记录 method / path / query，避免客户端元数据泄露给第三方模型供应商、浪费上游请求额度与产生噪音错误

### Changed
- **请求路由架构从 permissive 转 fail-closed**：`internal/proxy` 请求处理由「默认转发所有未识别端点」改为「仅已知本地 / 已知模型端点放行，其余拦截」。`POST /v1/messages` 与 `POST /anthropic/v1/messages` 继续作为模型推理端点转发，`POST /v1/messages/count_tokens` 保持本地处理

### Docs
- Claude Code 端点兼容 feature spec 与评审记录（中英双语）：`sdd-docs/features/2026-07-10-claude-code-endpoint-compatibility/`

---

## v0.14.0 (2026-07-09)

### Added
- **跨 Provider 模型路由（`/model` 菜单注入自定义模型）**：Claude Code 的 `/model` 菜单现在可以出现 mcc 配置的自定义模型；用户在会话内切换后，该会话后续请求走切换后的模型（可跨不同 Provider），其他会话保持默认（走 `ActiveProviderID`）。会话级隔离由 Claude Code 客户端原生保证（`mainLoopModelOverride` 纯内存态、不落盘），mcc 无需做会话识别。每个 Provider 新增 `ExposedModels`（Label / Description / BackendModel / Context1M），mcc 通过 `/api/claude_cli/bootstrap` 的 `additional_model_options` 把它们注入 `/model` 菜单；请求处理时 `Config.ResolveModel` 按请求 `model` 字段路由到对应 Provider 并替换为后端真实模型名，未命中则 fallback 到 active Provider 的 `ModelMappings`（向后兼容，未配置 `ExposedModels` 的用户行为不变）。菜单 description 自动拼接 Provider 名（`"{Description} · {Provider.Name}"`，空则只用 Provider 名），零配置即可看到模型归属
- **`ExposedModel.Context1M`（1M 上下文标记）**：勾选后 bootstrap 注入的菜单 value 附 `[1m]` 后缀，让 Claude Code 客户端按 1M 判定上下文窗口（claude-hud 用量、autocompact 阈值正确）；mcc 路由侧统一剥离请求 `model` 的 `[1m]` 后缀（用纯 ID 匹配 `ExposedModel.ID`），并主动剥离 `Anthropic-Beta` 的 `context-1m-*` 条目，避免透传给不兼容的第三方后端（其余 beta 如 `interleaved-thinking` 保留）
- **`ExposedModel.ID` 自动生成 + `BackendModel` 必填**：ID 是纯内部路由键（不展示给用户），改为后端自动生成 `em-<hex>`，前端隐藏 ID 输入框；`BackendModel` 改为必填，前端配 `datalist` 快捷入口（选项取自该 Provider 模型映射的 value，去重），可选填充不强制
- **SQLite 旧 ID 一次性迁移**：启动时把非 `em-` 前缀的旧手输 ID 重新生成为 `em-<hex>`，已是 `em-` 的不变（`ExposedModel` 走 JSON 序列化，`Context1M` 等新字段自动持久化）

### Changed
- **`ResolveModel` 入口 trim**：请求体 `model` 字段可能含前后空白，方法入口统一 trim，避免与已 trim 的 `ExposedModel.ID` 比较不对称
- **`createProvider` 响应用 trim 后的值**：避免 API 响应返回带空白的 `ExposedModel` 字段
- **import / duplicate 清空 `ExposedModels`**：导入的 duplicate 分支与复制 Provider 一致清空 `ExposedModels`，避免与原 Provider 产生全局 ID 冲突导致 `cfg.Validate()` 拒绝整个导入

### Docs
- 跨 Provider 模型路由 feature spec 与评审记录（中英双语）：`sdd-docs/features/2026-07-08-cross-provider-model-routing/`
- README 透明 / 隧道模式 `settings.json` 示例补充 `ANTHROPIC_API_KEY`，并新增 bootstrap 两道门槛说明：bootstrap 只认 OAuth / `ANTHROPIC_API_KEY`（`ANTHROPIC_AUTH_TOKEN` 不被接受）、`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` 会把 bootstrap 当非必要流量跳过；网关模式 3P 不触发 bootstrap，`ExposedModels` 不会出现在 `/model` 菜单（中英文同步）

### 注意
- 旧 `ExposedModel.ID` 迁移为 `em-<hex>` 后，用户 `~/.claude.json` 里已保存的 `mainLoopModelOverride` 失效，需在 `/model` 重新选择
- 透明 / 隧道模式下，若 Claude Code 使用 `ANTHROPIC_AUTH_TOKEN` 而非 `ANTHROPIC_API_KEY`，bootstrap 不会发起，`ExposedModels` 不会出现在 `/model` 菜单（参考 README 配置）

---

## v0.13.0 (2026-07-06)

### Added
- **Node.js 客户端 CA 信任自动配置**：Node.js 忽略操作系统 CA 信任库，系统级 CA 安装无法让 Claude Code（Node 客户端）信任 mcc 的 MITM 证书。新增 bootstrap 步骤将 `NODE_EXTRA_CA_CERTS` 持久化到用户环境——Windows 走 `setx`（用户级注册表）+ pwsh `$PROFILE` 兜底，macOS 走 `launchctl setenv` + shell profile，Linux 走 shell profile。通过 CA 指纹 marker 实现幂等；检测到用户自定义值时保留不覆盖；部分失败（setx/launchctl 失败但 profile 已写入）不标记、下次启动重试；Docker 容器内跳过
- **mcc.exe 应用图标**：Windows 二进制内嵌应用图标，任务栏 / 开始菜单显示品牌图标。新增 `make icon` 目标从 logo 自动重新生成（裁剪透明边距 + 230px 适配 256 画布，预留 5% 安全边距避免边缘裁切）

### Security
- **PowerShell 注入防护（P2-1）**：CA 路径渲染为 PowerShell 单引号字面量（home 相对路径走 `Join-Path`，绝对路径单引号包裹），拒绝 CR/LF 以阻断 `$()` / 反引号 / 换行命令注入
- **symlink 与特权运行防护（CWE-59 / P2-2）**：拒绝符号链接 profile 与 marker，防止跨路径篡改；检测到特权运行（管理员 / root）时拒绝 profile 变更，避免写入错误的用户配置
- **marker 身份绑定（F-4）**：NodeCA marker 绑定证书路径与用户身份，写入 / 读取均校验身份一致性，防止跨用户 marker 误用
- **fail-closed 校验链（F-1 / F-2）**：环境变更前先检查 pwsh / POSIX profile 的用户自定义值，有则跳过 `setx` / `launchctl` 不覆盖；profile 父链与文件系统根校验失败、profile 读取异常时 fail-closed；pwsh profile 部分失败返回 `ErrPartialSuccess`，不再因任一成功而掩盖整体失败

### Changed
- **Windows 环境变更广播**：`setx` 写入后广播环境变量变更，确保已运行进程能感知新的 `NODE_EXTRA_CA_CERTS`；Windows 客户端重启序列说明同步更新
- **旧版 pwsh profile 自动迁移**：升级场景下检测并清理旧版 `$mccCa` 残留行，一次性迁移到 mcc marker 块，恢复 marker + `setx` 持久化路径执行

### Docs
- Node.js 客户端 CA 信任自动配置 feature spec 与评审记录（中英双语）：`sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/`
- Node CA 持久化正确性修复、Windows 环境刷新、pwsh profile 迁移的 design / plan / review 文档

---

## v0.12.0 (2026-07-04)

### Added
- **智谱 Web 工具参数错误的反应式 recovery**：上游返回结构化 `error.code == "1210"`（或精确的 `[1210]` + `API 调用参数有误` 消息）时，识别为工具 schema 兼容问题，复用既有 `cleanTools` 删除根级 `$schema` / `$id` / `$comment` / `additionalProperties` 与工具级 `cache_control`，仅在清理实际改变请求时重试一次。修复 Claude Code 动态加载 `WebFetch` / `WebSearch` 后被智谱端点以 400 拒绝的问题（v0.11.0 Known Issues 的 recovery 落地）
- **有界 SSE 异常诊断**：SSE 流被中断、不完整、解析失败或包含明确 error event 时，输出一条 `[Stream] anomaly` 日志，含事件计数、content block 类型、stop reason、数字错误码、字节数与安全请求摘要。仅保留白名单枚举值，不记录 text / thinking / tool input / error.message / 原始 payload

### Security
- **重试请求继承客户端 context（CWE-400）**：rectifier 重试改用 `http.NewRequestWithContext(origReq.Context(), ...)`，客户端断开时立即取消重试，避免无限占用供应商资源
- **SSE 数字错误码内存硬化（CWE-400）**：异常诊断的数字错误码 map 限制不同 key 上限 16 个，超限归入 `other`，防止恶意或被攻陷供应商用大量不同错误码消耗内存

### Changed
- **错误分类优先级明确化**：`unsupported/unknown content type` 短语拆为独立高优先级分支，先于智谱 1210 与通用 invalid-request 兜底判断；结构化 `code == "1210"` 优先于通用 invalid-request 短语，避免误路由到错误的清理路径

### Docs
- 智谱 Web 工具兼容性恢复 feature spec（中英双语）：`sdd-docs/features/2026-07-01-zhipu-web-tools-compat/`
- 评审记录（中英双语，确认 recovery 正确性、安全边界与无遗留缺陷）

---

## v0.11.0 (2026-07-01)

### Security
- **请求参数日志脱敏（CWE-532）**：上游返回 ≥400 时，`[Proxy] Error` 日志的请求摘要改为类型安全白名单——只保留 `model`、`stream`、数值生成参数（`max_tokens`/`max_output_tokens`/`temperature`/`top_p`/`top_k`）与集合计数（`messages`/`tools`/`input`）。`system` 系统提示词、`metadata`、凭据、未知扩展与消息正文不再写入进程日志

### Fixed
- **SSE-labeled HTTP 错误不再静默丢失**：上游以 `Content-Type: text/event-stream` 返回 4xx/5xx 时，原先被 SSE 心跳路径抢先，错误不记日志。现改为先判状态码再判 SSE（`StatusCode < 400 && isSSEStream`），此类错误正确落到 `[Proxy] Error` 日志

### Changed
- **结构化协议诊断**：错误日志的请求摘要升级为结构化协议摘要——`stream` 区分缺失/false/true；`messages` 细化到角色（user/assistant）计数与 content block 类型计数；`tools` 记录名称摘要与 schema 关键字（`$schema`/`additionalProperties`/`format` 等）出现次数。仅保留定位上游协议兼容问题所需的结构信息，不含 prompt、正文或凭据

### Docs
- SSE-labeled HTTP 错误处理 feature spec（中英双语）：`sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/`
- 智谱 Web 工具兼容性恢复 spec（中英双语）：`sdd-docs/features/2026-07-01-zhipu-web-tools-compat/`
- 安全审查记录（中英双语，确认 CWE-532 闭合、无遗留缺陷）

### Known Issues
- 智谱 GLM 上游对 Claude Code 动态加载 `WebFetch`/`WebSearch` 后的请求可能返回 `1210` 参数错误（与 `stream` 字段缺失触发的 fallback 路径相关）。本版提供结构化诊断能力便于定位，recovery 实现留待后续版本

---

## v0.10.0 (2026-06-30)

### Added
- **供应商额度查询**：供应商管理页新增额度查询能力，支持手动查询与自动定时刷新，结果以快照形式缓存，并在供应商卡片与服务状态页展示
  - **多供应商适配**：内置 Token Plan（Kimi、智谱、MiniMax、ZenMux、火山方舟）与官方余额（DeepSeek、StepFun、SiliconFlow、OpenRouter、Novita）适配器，按各供应商真实协议解析用量；另支持自定义脚本、通用余额、NewAPI 三种通用模板
  - **凭证安全**：脚本与 ZenMux 凭证独立存储并按供应商绑定，供应商卡片密钥不会被路由到其他供应商或 ZenMux 端点；AccessKeyID 回显时掩码；自动迁移历史凭证格式
  - **定时与并发**：生产查询按供应商去重，避免重复请求；启动按供应商抖动分散首次查询；可配置自动查询间隔与并发上限
  - **配置编辑**：额度配置以模态框形式编辑，自动检测供应商类型并支持显式选择，MiMo 显示延期提示，保存前可草稿测试
  - **脚本安全边界**：自定义脚本请求强制同源校验（含重定向与 HTTPS→HTTP 降级防护）、禁止 userinfo、错误信息对密钥逐字脱敏、JS 沙箱解析/提取超时限制、请求与响应体大小上限
- **服务状态页增强**：当前供应商卡片右上角展示 5 小时/7 天用量利用率与余额（与供应商管理卡片一致），数据复用已加载的额度快照；监听状态块的代理/配置/路由三个地址改为单行三列等分展示

### Fixed
- 供应商导入后同步刷新额度快照状态，并校验导入响应状态码，正确识别部分失败
- CNY 余额显示符号修正为 ¥（原先误用 $）
- 额度快照加载与清理失败不再被静默吞掉，显式上报给前端

### Docs
- 供应商额度查询功能 spec、设计与实现计划（中英双语）：`sdd-docs/features/2026-06-27-provider-quota-query/`
- 复审记录：`review-notes`（针对初始实现 `33af2ed`）、`review-followup`（针对当前 HEAD，确认全部阻断/高危问题闭环）

---

## v0.9.3 (2026-06-24)

### Added
- 全局回到顶部悬浮按钮：从 `SessionBrowser` 提升到 `DashboardView` 层级，所有 tab 页共享；页面滚动超过 100px 时在右下角显示，点击平滑回到顶部；`z-30` 层级低于所有 modal（outline `z-40`、cleanup `z-50`），不干扰弹窗交互；短内容 tab（如证书信息）自动隐藏

### Docs
- 全局回到顶部功能 spec（中英双语，3/3 任务已实现并验证）：`sdd-docs/features/2026-06-24-global-back-to-top/`

---

## v0.9.2 (2026-06-24)

### Fixed
- 管理面板标签页切换布局抖动：`html { scrollbar-gutter: stable; overflow-y: auto }` 消除滚动条 reflow（约 15px 横向位移）；会话列表数据预加载到 `DashboardView.onMounted`，消除 `SessionBrowser` 每次激活的异步空→满二次布局；sessions 列表骨架屏兜底首次加载；加载失败优先显示错误信息

### Docs
- 管理面板标签页切换布局抖动修复 spec（中英双语，4/4 任务已实现并验证）：`sdd-docs/features/2026-06-24-dashboard-tab-layout-shift/`

---

## v0.9.1 (2026-06-24)

### Added
- 供应商导入导出：供应商页新增 JSON 导入导出，支持多主机批量迁移供应商配置。每卡片左上角复选框，工具栏全选/导出/导入按钮；导出含真实 token 的 JSON（下载前确认风险），导入带预览（新增/冲突计数）和冲突策略（skip/overwrite/duplicate，默认 skip）
- 后端 API：`POST /api/providers/export` 按 ID 导出含真实 token；`POST /api/providers/import` 单次 Load→Save，version 校验，无效供应商跳过计入 errors，显式拒绝非 POST（405）
- 全选控件：标题左侧三态复选框（全选/部分选中 indeterminate/未选）

### Fixed
- 导入版本校验：前端解析校验 `version === 1`，预览前拒绝非 1 或缺失版本
- duplicate 策略语义：仅冲突项生成新 ID，非冲突项保留原 ID 正常导入
- 导出失败提示：新增 `providers.export_failed` 文案，不再误用导入格式错误文案

### Docs
- 新增供应商导入导出功能 spec（中英双语）：`sdd-docs/features/2026-06-23-provider-import-export/`

---

## v0.9.0 (2026-06-23)

### Added
- 监听地址可配置：proxy/admin 监听地址和端口支持 CLI flag（`-proxy-listen`/`-proxy-port`/`-admin-listen`/`-admin-port`）、环境变量（`MCC_*`）和配置文件三层覆盖，默认行为不变；前端只读展示实际监听地址
- CLI 本地化帮助：`mcc -h` 按系统语言显示 flag 说明；`mcc -v` 打印版本并退出
- `/api/status` 新增 6 个监听字段（proxy/admin/gateway addr+port），反映 CLI/env/config 解析后的实际生效值
- 前端"监听状态"只读区块，附操作风险提示（非 443 需端口转发、127.0.0.1 仅本机可达）
- IPv6 地址归一化：`normalizeListenAddr` 统一剥离 RFC 2732 方括号

### Fixed
- 启动失败立即退出：服务监听失败通过 `startupErr` 通道触发 `log.Fatalf`，不再以"部分服务可用"状态继续运行
- Gateway 热重启不误杀进程：gateway goroutine 过滤 `http.ErrServerClosed`
- IPv6 地址拼接全面修复：`fmt.Sprintf` 全部替换为 `net.JoinHostPort`（启动、handler restart、bootstrap 指令、前端展示）
- 前端 IPv6 防御性格式化：`formatListenAddress` 先剥离已有括号再按需添加

### Changed
- 推荐 hook 改用原生 `rtk hook claude`（跨平台，去除 jq 依赖）；config_path_note 移除"Windows hook 可能不生效"的过时结论

### Docs
- 新增监听地址配置功能 spec（中英双语）

---

## v0.8.1 (2026-06-23)

### Fixed
- Windows 引导乱码修复：`certutil`/`setx` 子进程输出按 GBK/CP936 解码为 UTF-8（`decodeCmdOutput`），错误信息不再乱码；已是 UTF-8 的输出不误转，解码失败回退原始字节

---

## v0.8.0 (2026-06-22)

### Security
- URL 凭证脱敏：`RedactURL` 剥离 `https://user:pass@host` 的 userinfo；代理入口/出口日志、usage 读取路径统一走脱敏，防止 provider URL 凭证或签名泄露
- usage 读取二次脱敏：防御历史脏数据，Coverage/Requests 两条输出路径均不泄露

### Added
- 透明模式自动引导：启动时自动尝试 hosts 修改、CA 信任安装、MCC_ROOT 环境持久化；失败不阻塞启动，按优先级降级
- 三连接模式与自动降级：透明 > 隧道 > 网关；header 模式按钮可持久化到后端，`/api/config`、`/api/status` 暴露首选/实际模式
- i18n 系统语言检测：`zh*` 默认中文，其他默认英文；`MCC_LANG` 可手动覆盖
- 首运行 MCC_ROOT 持久化：从任意工作目录启动均可自动定位证书
- fish shell profile 去重增强：导出行匹配更语义化，避免重复追加
- Docker 宿主机 helper 机制：`MCC_HOST_HELPER` 支持挂载 helper 检测/修改宿主机 hosts 与 CA 信任
- 宿主机一键配置脚本：`setup-host.sh`（Linux/macOS）、`setup-host.ps1`（Windows）；`docker-host-helper.sh` 作为容器内默认状态检测器
- docker-compose 部署：集成端口映射、数据卷、usage 同步目录、NET_BIND_SERVICE
- 前端连接模式入口与三模式说明弹窗，含 i18n
- CI 测试工作流：`.github/workflows/test.yml`，push/PR 跑 `make test`（含 race detector）
- Release archive 附带 `setup-host.sh`/`setup-host.ps1`

### Changed
- 请求日志增强：入口日志延后到 backendURL 确定后打印，附带 `provider_name` 与脱敏 `upstream_url`；rate-limit 日志改用 `provider_name` 替代 `provider`（ID）
- bootstrap 结果模型：hosts/CA/环境持久化独立记录，状态持久化到 data 目录抑制重复失败日志
- `AGENT.md` 重命名为 `AGENTS.md`

### Docs
- 新增透明模式自动引导 feature spec：`sdd-docs/features/2026-06-20-transparent-mode-bootstrap-and-fallback/`
- 新增 fish profile 去重 feature spec：`sdd-docs/features/2026-06-21-fish-profile-dedup-scanner/`
- README 新增英文版 `README.en.md` 并双语互链
- `CLAUDE.md` 关键文件表补充 `internal/bootstrap/` 和 `internal/i18n/`
- `sdd-docs/features/README.md` 索引补登记两个新 feature

---

## 2026-06-12

### Fixed
- 修复 Windows 二进制缺少 IANA 时区数据导致 `tz=Asia/Shanghai` 等浏览器时区查询失败，进而使服务状态和使用统计页面显示 0 的问题
- 修复部分上游 SSE 在 `message_stop` 后不关闭连接时，代理等待 EOF 导致流式 usage 迟迟不落库的问题
- 修复兼容 provider 将 usage 放在 `message_stop` payload 中时，终止事件 usage 可能被跳过的问题

### Docs
- 新增 Windows 使用统计可靠性修复 feature specs：`sdd-docs/features/2026-06-12-windows-usage-statistics-fixes/`
- 更新 `sdd-docs/features/README.md`，说明新的 `spec.md` / `spec_zh.md` 双语单文件规格格式

---

## 2026-06-11

### Added
- 使用统计页新增快捷日期范围：今日、近 7 天、近 30 天；默认近 7 天，近 7/30 天不包含今天
- 使用统计页新增 `清除数据` 操作，支持默认保留 `session_log_sync`，也支持勾选后重置 Session Log 同步状态
- Admin API 新增 `POST /api/usage/clear`，用于清除 usage 统计数据并可选重置 Session Log 同步状态
- 会话记录清理提示新增 Windows 预览命令和 Windows 交互清理命令
- 统计口径筛选项新增问号提示，解释有效统计、实时请求、Session Log、全部原始
- 编辑供应商弹窗桌面端宽度加宽至约供应商列表内容区的四分之三，提升模型映射和多模态配置编辑体验

### Fixed
- 修复 Usage 覆盖率表格横向内容被遮挡且缺少底部横向滚动条的问题
- 修复 Usage 覆盖率表头提示框被表格区域遮挡的问题，表格内提示改为向下弹出
- 修复 Windows 清理命令路径提示：原生 `C:\Users\<user>\...` 路径保留盘符并替换用户名为 `用户名代理`
- 修复 Windows 清理命令路径中的双引号、控制字符和 Windows 非法路径字符清洗问题，降低复制提示命令后的解析风险
- 修复二进制未配置管理密码时生成随机密码但启动输出不展示的问题；随机密码现在会在启动输出中打印一次，显式密码不会回显

### Docs
- 更新 Usage statistics specs，补充清除统计数据 API、前端交互、迁移场景和验证项
- 更新 Session Browser specs，补充清理提示的 Linux/macOS 与 Windows 双平台命令、Windows 路径转换和安全清洗约束
- 更新 Multimodal Model Switch specs，补充 Provider 弹窗宽度约束
- 更新 Claude Proxy specs 和 README，说明二进制默认 `./data`、随机密码打印和 Windows 后台日志查看方式

---

## 2026-06-09

### Added
- Provider 配置新增“多模态切换”和“多模态模型 ID”，请求含图片、PDF、音频或视频等非文本内容时可自动切换到指定多模态模型
- 代理请求转换支持递归检测 `messages` / `system` 中的非文本内容，覆盖截图工具返回的 `tool_result.content` 图片
- SQLite Provider 表新增多模态配置字段，并支持旧数据库自动补列
- Admin API 创建、查询、更新、复制 Provider 时保留多模态配置，并校验开启开关时必须填写多模态模型 ID
- Provider 弹窗和卡片增加多模态配置 UI 与提示文案

### Fixed
- 修复 Mimo 文本模型收到截图图片时返回 `No endpoints found that support image input` 的配置层解决路径
- 修复 Session Browser 移动端大纲返回顶部按钮缺少 sticky 底部定位的问题

### Docs
- 新增多模态模型切换 feature specs：`sdd-docs/features/2026-06-09-multimodal-model-switch/`
- 同步英文主文档和中文 `_ZH` 文档：requirements、plan、decisions、validation、status

---

## 2026-05-29

### Added
- 识别 DeepSeek "thinking must be passed back" 和 "tool_use without tool_result" 两种新的 400 错误模式，支持被动重试恢复
- 非流式 >= 400 错误日志增加兼容性相关 headers 信息（`Anthropic-Version`、`Anthropic-Beta`、`Content-Type`）

### Fixed
- 修正待修复问题文档结构，新增 `sdd-docs/known-issues/` 目录记录未解决的兼容性问题

### Docs
- 新增 DeepSeek 模型在 Claude Code >= 2.1.150 中报 400 错误的待修复记录
- 将 `docs/superpowers/plans/` 合并到 `sdd-docs/superpowers/plans/`，清理空 `docs/` 目录
- 新增 `sdd-docs/changes/changelog.md`

---

## 2026-05-28

### Added
- 导出 HTML 增加右侧大纲导航面板（基于 IntersectionObserver 的滚动高亮）
- 导出 HTML 增加返回顶部按钮
- 导出 HTML 小屏幕下大纲面板切换为底部浮动按钮 + 弹窗模式
- Session Browser 大纲标题增加条目数量显示
- Session Browser 大纲标题支持中英文本地化

### Fixed
- Session Browser 返回顶部按钮在所有屏幕尺寸下可见
- Session Browser 返回顶部按钮改为 fixed 定位

---

## 2026-05-21

### Added
- Session Browser 项目目录折叠、标题重命名、UI 优化、跨平台路径修复
- 管理后台全局亮/暗色主题系统（CSS 变量 + 偏好持久化）
- 导出 HTML 主题与管理面板一致（暗色模式下导出暗色 HTML）
- Session Browser 大纲面板返回顶部按钮

### Fixed
- 主题 tooltip 溢出和 Session Browser 滚动问题
- Usage 覆盖率提示样式统一
- 移除冗余的 Session Browser 选择提示

---

## 2026-05-20

### Added
- 非标准 content block（如 `tool_reference`）反应式清理，修复第三方供应商 400 错误
- 请求日志分页总条数、状态页问号提示

### Fixed
- Usage 日期过滤和请求行展示修复

---

## 2026-05-18

### Added
- Claude Session Browser（浏览、搜索、导出会话）
- 管理后台前端重构为 Vue 3 + TypeScript + Tailwind CSS（Flat Design）
- 前端国际化支持，默认中文，支持中英文切换
- SSE 流心跳机制，防止上游空闲时连接超时

---

## 2026-05-15

### Added
- SQLite 配置存储（替代 JSON 文件）
- Usage 统计 dashboard（API + 前端图表）
- 流式 usage 解析与记录
- 反应式供应商兼容性错误恢复（400 错误自动清理重试）
- 供应商 thinking 支持开关

---

## 2026-05-13

### Added
- 硬编码端点拦截，覆盖 Claude Code 源码中所有 API 端点
- 供应商复制配置、Token 明文查看
- 统一代理请求日志格式（reqID 关联入口/出口）

### Fixed
- 保留 Claude Code Anthropic 协议字段
- 低优先级端点拦截增强

---

## 2026-05-11

### Added
- 管理后台 REST API 配置服务
- bcrypt 认证 + Session Token

---

## 2026-05-10

### Added
- 透明代理服务（Header 转发）
- CA 证书生成与管理
- 服务器证书生成与管理
- JSON 配置存储
- Docker 部署（Alpine）
