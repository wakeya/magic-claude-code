# 监听地址可配置规格

本地页面：管理面板配置页（只读状态展示）
代理入口：`cmd/server/main.go`、`internal/config/config.go`、`internal/config/sqlite_store.go`、`internal/admin/handler.go`、`internal/frontend/src/views/DashboardView.vue`、`internal/frontend/src/composables/useI18n.ts`
参考源站：`sdd-docs/features/2026-06-13-auto-update/spec_ZH.md`（spec 模板与 config/status 暴露模式）、`sdd-docs/features/2026-06-20-transparent-mode-bootstrap-and-fallback/spec_ZH.md`（Gateway 监听配置先例）
技术栈：Go 1.26 标准库（`net`、`net/http`、`flag`、`os`）+ Vue 3 + 内嵌前端
最后更新：2026-06-23
进度：0 / 7 已规划

## 整体分析（源站分析）

### 当前项目状态

代理服务、Admin 服务、Gateway 服务在启动时监听固定地址端口：

- 代理（`proxy.Server.Start`）在 [cmd/server/main.go:222](../../../../cmd/server/main.go) 被硬编码为 `:443`，等价于 `0.0.0.0:443`（IPv4 全接口）+ `[::]:443`（IPv6）。
- Admin（`admin.Server.Start`）在 [cmd/server/main.go:228](../../../../cmd/server/main.go) 被硬编码为 `:8442`。
- Gateway 已有完整的 `GatewayListenAddr` + `GatewayListenPort` 双字段配置，通过 `fmt.Sprintf("%s:%d", ...)` 组装，并有 `RestartGateway` 机制支持改后重启 listener。

### 已存在的配置不一致

`internal/config/config.go` 已定义 `ProxyPort`（默认 443）和 `AdminPort`（默认 8442），`sqlite_store.go` 会持久化它们，`main.go` 启动横幅也会打印它们——但**启动 `Start` 调用时忽略了这两个字段**，端口仍写死 443/8442。这是"字段已配置但未生效"的半成品状态，必须在本次一并修正，否则用户在配置页看到端口值与实际监听不符。

### 为什么监听地址不能像 provider 那样热更新

provider / model mapping 属于热更新配置：代理处理下一个请求时读取最新值，前端保存即生效。

监听地址本质不同：它是进程启动时 `net.Listen` 绑定的，绑定后 listener 不会因 config 变更而换地址。要让变更生效必须重启 listener 或重启整个进程。Gateway 通过专写的 `RestartGateway` 实现了单 listener 热重启；但代理 443 和 Admin 8442 在 `main.go` 顶层 goroutine 中一次性 `Start`，没有等价的重启 API，且代理重启会中断所有活跃请求。

### 为什么不在前端提供修改入口

在前端修改监听地址有两个真实风险：

1. **Admin 监听变更导致前端失联**：用户修改 `admin_listen_addr` 或 `admin_listen_port` 保存后，重启 mcc 进程，Admin 服务在新地址监听，但浏览器仍停留在旧地址——刷新后页面打不开，用户可能误以为服务宕机。
2. **代理端口变更导致 hosts 失效**：hosts 固定将 `api.anthropic.com` 指向 `127.0.0.1:443`。若代理端口改为 8443，客户端请求 443 将无响应。改代理端口必须配合 iptables 转发，仅改 config 无意义。

监听地址属于**基础设施层（部署时决定）**而非业务层（运行时调整）。绝大多数用户在部署时决定一次（默认 `0.0.0.0`，或安全收紧到 `127.0.0.1`），之后不动。因此本功能采用**方案 B**：配置文件 + CLI flag + 环境变量三层覆盖，前端只读展示当前实际监听状态，不提供修改入口。

### 配置优先级

三层覆盖，优先级从高到低：

1. **CLI flag**（部署最常用，覆盖一切）
2. **环境变量**（Docker 场景最常用）
3. **配置文件**（SQLite / JSON，持久化默认值）
4. **硬编码默认值**（代理 `0.0.0.0:443`、Admin `0.0.0.0:8442`、Gateway 沿用现状）

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已规划 | Config 字段与默认值 | `internal/config/config.go`、`config_test.go` | 默认值与 normalize 单元测试 |
| 2 | 已规划 | 持久化层支持新字段 | `internal/config/sqlite_store.go`、`sqlite_store_test.go` | 新旧数据库迁移测试 |
| 3 | 已规划 | CLI flag + 环境变量覆盖 | `cmd/server/main.go` | flag 解析与 env 覆盖单元测试（`main_test.go`） |
| 4 | 已规划 | 启动接线，消除 `:443`/`:8442` 硬编码 | `cmd/server/main.go` | 启动监听地址与配置一致；横幅打印真实地址 |
| 5 | 已规划 | `/api/status` 暴露代理与 Admin 监听 | `internal/admin/handler.go`、`handler` 相关测试 | 状态接口字段断言测试 |
| 6 | 已规划 | 前端只读展示监听状态 + i18n | `internal/frontend/src/views/DashboardView.vue`、`useI18n.ts`、`useApi.ts` | 前端构建 + 组件断言测试 |
| 7 | 已规划 | CLI 帮助 i18n + 版本 flag | `cmd/server/main.go`、`internal/i18n/i18n.go` | `mcc -h` / `mcc -v` 中英文输出验证 |

## 需求

### 交付物

1. `internal/config/config.go` 新增两个字段并补齐语义：
   - `ProxyListenAddr string`（json `proxy_listen_addr`，默认 `"0.0.0.0"`）
   - `AdminListenAddr string`（json `admin_listen_addr`，默认 `"0.0.0.0"`）
   - `ProxyPort`、`AdminPort`（已存在，本次让其在启动时真正生效）。
2. `NormalizeConfig` 为新字段填默认值；空字符串与 0 端口走默认值；代理端口范围校验 1–65535。
3. 持久化策略对齐 `GatewayListenAddr`/`GatewayListenPort` 先例：新字段**不进 SQLite store**（`saveSettings`/`loadSettings` 不处理它们），由"默认值 + CLI flag + 环境变量"决定。JSON store（`store.go`）通过 `json.MarshalIndent` 自动序列化新字段，零改动。`ProxyPort`/`AdminPort` 沿用现有 SQLite 读写不变。
4. `cmd/server/main.go` 新增 CLI flag：
   - `-proxy-listen`（默认空，空表示用配置文件值）
   - `-proxy-port`
   - `-admin-listen`
   - `-admin-port`
   同时支持环境变量 `MCC_PROXY_LISTEN_ADDR`、`MCC_PROXY_PORT`、`MCC_ADMIN_LISTEN_ADDR`、`MCC_ADMIN_PORT`。flag 非空时覆盖环境变量与配置文件。
5. `main.go` 启动代理与 Admin 时使用 `fmt.Sprintf("%s:%d", cfg.ProxyListenAddr, cfg.ProxyPort)` 组装地址，消除硬编码 `:443` / `:8442`；Gateway 沿用现有逻辑不变。
6. `internal/admin/handler.go` 的 `handleStatus` 在现有 `gateway_listen_addr` / `gateway_listen_port` 之外，补充返回 `proxy_listen_addr` / `proxy_port` / `admin_listen_addr` / `admin_port` 四个字段，反映**当前实际生效**值。
7. 前端配置页新增"监听状态"只读区块（紧邻 Gateway 配置区或状态概览区），展示代理、Admin、Gateway 三个服务的 `地址:端口`；该区块**不提供保存/修改按钮**，文案明确说明"监听地址需在启动参数或配置文件中修改，修改后重启 mcc 生效"。
8. 启动横幅打印的三项端口必须与实际监听地址一致（目前横幅打印 `cfg.ProxyPort` 但实际监听写死 443，本次修正使两者统一）。
9. i18n 在 zh 和 en 两套语言下补齐新区块的所有 label、hint、说明文案。
10. 新增 `-v` / `-version` flag：打印当前二进制版本（`internal/version.Version`，由 ldflags 注入）后立即退出，不启动服务。
11. CLI 帮助做本地化：自定义 `flag.Usage`，使 `mcc -h` / `mcc --help` 输出按 `i18n.ResolveLocale()` 选择的语言显示所有 flag 说明（zh/简体中文→中文，其他→英文）。新增的 `-proxy-listen` / `-proxy-port` / `-admin-listen` / `-admin-port` / `-v` / `-version` 以及既有的 `-data` / `-password` 都走该 i18n 机制。

### 数据模型

```go
// internal/config/config.go
type Config struct {
    // ...existing fields...
    ProxyPort        int    `json:"proxy_port"`         // 已存在；本次让其生效
    AdminPort        int    `json:"admin_port"`         // 已存在；本次让其生效
    ProxyListenAddr  string `json:"proxy_listen_addr"`  // 新增；默认 "0.0.0.0"
    AdminListenAddr  string `json:"admin_listen_addr"`  // 新增；默认 "0.0.0.0"
    // Gateway 字段不变
}
```

### 启动地址组装

```go
// cmd/server/main.go（概念示意）
proxyAddr := fmt.Sprintf("%s:%d", cfg.ProxyListenAddr, cfg.ProxyPort)
adminAddr := fmt.Sprintf("%s:%d", cfg.AdminListenAddr, cfg.AdminPort)
proxyServer.Start(proxyAddr, ...)
adminServer.Start(adminAddr, ...)
```

### 优先级解析

flag 非空 → 覆盖环境变量与配置文件；flag 为空时读环境变量；环境变量为空时读配置文件；都为空时由 `NormalizeConfig` 填默认值。`main.go` 在 `flag.Parse` 后、`Start` 前完成覆盖与归一化。

### 约束

1. 监听地址修改后**不**自动重启 listener，也不在前端提供修改入口。本功能仅做"可配置 + 只读展示"。
2. 默认行为必须与现状一致：代理 `0.0.0.0:443`、Admin `0.0.0.0:8442`。现有用户升级后行为不变。
3. 端口必须为 1–65535；非法值在 `NormalizeConfig` 中回退默认值并记录日志，不阻塞启动。
4. CLI flag 为空字符串 / 0 时表示"不覆盖"，不能把"空"误解为"绑定空地址"。
5. 新字段不进 SQLite store（对齐 Gateway 先例），因此不存在"旧库缺列"问题；SQLite store 保持现状，不新增列、不改 schema。
6. `/api/status` 返回的监听字段必须反映实际生效值（经 flag/env/文件覆盖与归一化之后的值），而非原始配置文件值。
7. 前端只读区块不得复用 Gateway 那套"输入框 + 保存按钮"组件形态，必须是纯展示，避免用户误以为可改。
8. 不引入新的外部依赖；CLI flag 解析沿用标准库 `flag`，env 沿用 `os.Getenv`（与现有 `MCC_ROOT` / `ADMIN_PASSWORD` 风格一致）。
9. `-v` / `-version` 必须在 `flag.Parse` 之后、任何启动逻辑（数据目录、配置加载、网络监听）之前处理，打印版本即 `os.Exit(0)`，绝不启动服务或请求管理员权限。
10. CLI 帮助 i18n 复用现有 `i18n.ResolveLocale()` + `i18n.Load()` 机制，不引入第二套语言探测；`-h` / `-help` / `--help` 三种形式都触发本地化帮助（标准库 `flag` 自动将 `-h` / `-help` / `--help` 路由到 `flag.Usage`）。
11. flag 帮助文案集中在 `internal/i18n/i18n.go` 的 `Messages` 结构（en/zh 两套），不在 main.go 里硬编码字符串，确保与启动日志的语言规则保持一致。
12. 前端只读监听区块定位在页面顶部"状态概览"区（与 Gateway 可编辑配置区物理分离），避免用户误以为代理/Admin 监听也可在页面修改。

### 边界情况

1. 用户同时设置 flag 和环境变量——flag 优先。
2. 端口被占用（`net.Listen` 失败）——启动失败并打印明确错误（现有行为，本次不改）。
3. 配置文件里端口为 0 或超范围——`NormalizeConfig` 回退默认值。
4. 代理端口改为非 443——服务能启动，但 hosts 指向 443 的客户端请求会失败；前端只读区块需在文案中提示"代理端口建议保持 443，否则需配合 iptables 转发"。
5. Admin 监听改为 `127.0.0.1`——重启后仅本机能访问配置页，远程无法访问；前端文案提示这一后果。
6. 旧 SQLite 数据库升级——新列缺失时回退默认值。
7. flag 显式传空串（`-proxy-listen ""`）——视为不覆盖，不当作合法地址。
8. IPv6 监听地址（如 `[::1]`）——`fmt.Sprintf("%s:%d", ...)` 对 IPv6 不够稳健，需用 `net.JoinHostPort`（本次规范：地址组装统一改用 `net.JoinHostPort`，Gateway 一并对齐）。

### 非目标

1. 不实现前端修改监听地址的入口（方案 C 留待将来）。
2. 不实现代理/Admin listener 的热重启（不引入类似 `RestartGateway` 的机制）。
3. 不改变默认监听行为（仍为 `0.0.0.0` 全接口）。
4. 不实现按网卡/按来源 IP 的访问控制（属防火墙职责）。
5. 不实现配置变更后的进程自动重启或一键重启按钮。
6. 不引入 TLS 端口或 Unix socket 监听。

## 任务详情

### 任务 1：Config 字段与默认值

#### 需求

**Objective（目标）** — 为代理与 Admin 服务新增可配置的监听地址字段，并让既有但未生效的端口字段进入归一化流程。

**Outcomes（成果）** — `Config` 新增 `ProxyListenAddr`、`AdminListenAddr`；`NormalizeConfig` 为空值填默认 `"0.0.0.0"`，为空端口填 443/8442；端口范围校验回退。

**Evidence（证据）** — 单元测试覆盖：空字段 → 默认值；非法端口 → 回退；合法值 → 原样保留。

**Constraints（约束）** — 默认值与现状一致；不改变现有字段语义；JSON tag 用 snake_case。

**Edge Cases（边界）** — 端口 0、负数、>65535；地址空串；地址含前后空格（trim）。

**Verification（验证）** — `go test ./internal/config/`。

#### 计划

1. 在 `Config` 结构体添加 `ProxyListenAddr`、`AdminListenAddr`（紧邻现有 `ProxyPort`/`AdminPort`，注释说明默认值）。
2. 在 `defaultConfig()` 设置默认 `"0.0.0.0"`。
3. 在 `NormalizeConfig` 对两个新字段做 trim + 空值回退；对 `ProxyPort`/`AdminPort` 做范围校验（1–65535，越界回退默认）。
4. 扩展 `config_test.go` 覆盖以上分支。

#### 验证

- [ ] 空地址填默认 `0.0.0.0`。
- [ ] 非法端口回退 443/8442。
- [ ] 合法自定义值保留。
- [ ] 地址前后空格被 trim。

### 任务 2：持久化层（对齐 Gateway 先例）

#### 需求

**Objective（目标）** — 确认新字段在现有持久化机制下的行为，与同类 Gateway 监听字段保持一致。

**Outcomes（成果）** — 经核查，`GatewayListenAddr`/`GatewayListenPort` 是同类"监听地址"字段，它们**不进 SQLite store**（`saveSettings`/`loadSettings` 从不处理它们），SQLite 是主存储，`legacyJSONPath` 仅用于一次性遗留迁移。因此本次新增的 `ProxyListenAddr`/`AdminListenAddr` **对齐 Gateway 先例，不进 SQLite**——既不改 `saveSettings` 也不改 `loadSettings`。JSON store（`store.go`）通过 `json.MarshalIndent` 自动序列化所有带 json tag 的字段，新字段零改动即支持。效果：监听地址由"默认值 + CLI flag + 环境变量"决定，与 Gateway 行为一致。

**Evidence（证据）** — 单元测试：JSON store 往返新字段一致；现有 SQLite store 测试套件全绿（确认未因新字段破坏）；显式断言 SQLite store 不持久化新字段（保存后重载，新字段回退默认，与 Gateway 一致）。

**Constraints（约束）** — 不修改 `sqlite_store.go` 的 `saveSettings`/`loadSettings`；不新增 SQLite 列；保持与 `GatewayListenAddr` 的持久化策略完全一致。

**Edge Cases（边界）** — 旧 SQLite 数据库（无任何监听地址列）——天然兼容，因为根本不涉及这些列；JSON store 用户——新字段自动往返。

**Verification（验证）** — `go test ./internal/config/`。

#### 计划

1. 确认 `sqlite_store.go` 的 `saveSettings`/`loadSettings` 不处理 `ProxyListenAddr`/`AdminListenAddr`（与 Gateway 一致，不改动）。
2. 为 JSON store（`store.go`）添加新字段往返测试：Save 自定义监听地址 → Load 回来一致。
3. 添加显式测试：SQLite store Save → Load，新字段回退默认（证明其不持久化，与 Gateway 行为对齐）。
4. 全量 config 包回归测试通过。

#### 验证

- [ ] JSON store 往返 `proxy_listen_addr` / `admin_listen_addr` 一致。
- [ ] SQLite store 不持久化新字段（重载回退默认）。
- [ ] 现有 SQLite store 测试全绿（未破坏）。
- [ ] 与 `GatewayListenAddr` 的持久化行为一致。

### 任务 3：CLI flag 与环境变量覆盖

#### 需求

**Objective（目标）** — 让部署者能通过 CLI flag 和环境变量覆盖配置文件的监听地址与端口。

**Outcomes（成果）** — `main.go` 新增 `-proxy-listen` / `-proxy-port` / `-admin-listen` / `-admin-port` 四个 flag 及对应 `MCC_*` 环境变量；flag 非空覆盖 env 与文件，env 非空覆盖文件。

**Evidence（证据）** — `main_test.go` 验证覆盖优先级：flag > env > file。

**Constraints（约束）** — flag 空串 / 0 表示"不覆盖"；沿用标准库 `flag` 与 `os.Getenv`；不引入 envconfig 类依赖。

**Edge Cases（边界）** — flag 与 env 同时设置；只设 env；只设 flag；都未设（走文件）；flag 显式传空串。

**Verification（验证）** — `go test ./cmd/server/`。

#### 计划

1. 定义四个 flag，默认值为空（表示不覆盖）。
2. 定义 `applyListenOverrides(cfg *config.Config, flags ..., envs ...)` 辅助函数，按 flag→env 顺序覆盖。
3. 在 `flag.Parse` 后、`Start` 前调用。
4. 测试覆盖三种来源的组合。

#### 验证

- [ ] flag 非空时覆盖 env 和文件。
- [ ] flag 空、env 非空时覆盖文件。
- [ ] 都空时保留文件值。
- [ ] flag 显式空串不误判为合法地址。

### 任务 4：启动接线，消除硬编码

#### 需求

**Objective（目标）** — 代理与 Admin 启动时使用配置中的监听地址端口，消除 `:443` / `:8442` 硬编码。

**Outcomes（成果）** — `main.go` 用 `net.JoinHostPort(cfg.ProxyListenAddr, strconv.Itoa(cfg.ProxyPort))` 组装代理地址；Admin 同理；Gateway 一并对齐到 `net.JoinHostPort`。横幅打印的端口与实际监听一致。

**Evidence（证据）** — 启动日志显示 `Proxy server starting on 0.0.0.0:443`（或自定义值），与 `cfg` 一致；手动改 flag 后监听地址变化。

**Constraints（约束）** — 默认值场景行为与现状完全一致；地址组装统一用 `net.JoinHostPort` 以正确处理 IPv6。

**Edge Cases（边界）** — IPv6 地址；端口被占用（启动失败，现有行为）；地址为 `0.0.0.0`（全接口，默认）。

**Verification（验证）** — 本地以默认值启动验证行为不变；以 `-proxy-listen 127.0.0.1` 启动验证仅本机监听。

#### 计划

1. 引入 `net.JoinHostPort` 组装代理与 Admin 地址。
2. 替换 [main.go:222](../../../../cmd/server/main.go) 与 [main.go:228](../../../../cmd/server/main.go) 的硬编码。
3. Gateway 的 `fmt.Sprintf("%s:%d", ...)` 一并改为 `net.JoinHostPort`。
4. 校验横幅打印值与实际监听值一致。

#### 验证

- [ ] 默认值启动行为与现状一致（`0.0.0.0:443`、`0.0.0.0:8442`）。
- [ ] `-proxy-listen 127.0.0.1` 后仅本机监听 443。
- [ ] IPv6 地址组装正确（`net.JoinHostPort` 输出 `[::1]:443`）。
- [ ] 横幅端口与实际监听一致。

### 任务 5：`/api/status` 暴露监听字段

#### 需求

**Objective（目标）** — 让前端能通过状态接口读到代理与 Admin 的实际监听地址端口。

**Outcomes（成果）** — `handleStatus` 在现有 `gateway_listen_addr` / `gateway_listen_port` 基础上，补充 `proxy_listen_addr` / `proxy_port` / `admin_listen_addr` / `admin_port`。

**Evidence（证据）** — Handler 测试断言四个新字段存在且与 `cfg` 一致。

**Constraints（约束）** — 返回的是 `NormalizeConfig` + 覆盖之后的实际生效值；不暴露敏感信息（地址端口非敏感）。

**Edge Cases（边界）** — 默认值；自定义值；IPv6 地址。

**Verification（验证）** — `go test ./internal/admin/`。

#### 计划

1. 在 `handleStatus` 的 JSON 响应添加四个字段。
2. 如果 `StatusResponse` 有对应 Go struct，补字段；否则 map 直接加键（与现有 `gateway_*` 风格一致）。
3. 测试断言新字段。

#### 验证

- [ ] `GET /api/status` 返回四个新字段。
- [ ] 字段值与实际监听一致。
- [ ] 默认值场景返回 `0.0.0.0` / 443 / 8442。

### 任务 6：前端只读展示与 i18n

#### 需求

**Objective（目标）** — 在配置页以只读形式展示三个服务的监听状态，明确说明修改方式。

**Outcomes（成果）** — `DashboardView.vue` 在页面顶部"状态概览"区新增"监听状态"只读区块，展示代理 / Admin / Gateway 的 `地址:端口`（纯文本，无输入框无保存按钮）；文案说明"监听地址通过启动参数或配置文件修改，改后重启生效"；zh/en 两套 i18n 补齐。

**Evidence（证据）** — 前端构建通过；组件测试断言三个服务的地址端口被渲染为只读文本，且不出现可编辑输入框；i18n 在 zh/en 下文案齐全；区块位于状态概览区而非可编辑配置区。

**Constraints（约束）** — 严禁复用 Gateway 那套输入框组件形态；**区块必须位于页面顶部的"状态概览"区**，与 Gateway 可编辑配置区物理分离，避免用户误以为代理/Admin 监听也可在页面修改；从 `/api/status`（而非 `/api/config`）读取，因为 status 返回的是实际生效值。

**Edge Cases（边界）** — 地址为 `0.0.0.0`（展示 + 说明"所有接口"）；IPv6 地址；端口为默认值；后端尚未返回新字段（旧版本兼容，区块降级展示或隐藏）。

**Verification（验证）** — `npm --prefix internal/frontend test`；手动验证 zh/en 文案与只读展示。

#### 计划

1. `useApi.ts` 的 status 响应类型补充四个字段。
2. `useI18n.ts` 在 zh/en 补齐"监听状态"区块的 label、hint、"所有接口"说明、修改方式提示、IPv6/端口建议提示。
3. `DashboardView.vue` 添加只读区块：三个服务各一行 `地址:端口`，下方一行说明文案。
4. 组件测试断言：渲染只读文本、不渲染输入框、包含修改方式说明。

#### 验证

- [ ] 三个服务的地址端口以只读文本展示。
- [ ] 无输入框、无保存按钮。
- [ ] zh/en 文案齐全。
- [ ] 说明文案明确"需在启动参数或配置文件修改 + 重启生效"。
- [ ] 后端未返回新字段时不崩溃（降级处理）。

### 任务 7：CLI 帮助 i18n 与版本 flag

#### 需求

**Objective（目标）** — 让 `mcc -h` / `--help` 按系统语言显示本地化的 flag 说明，并提供 `mcc -v` / `--version` 查询版本，使用户无需翻文档即可了解可用 flag 与当前版本。

**Outcomes（成果）** — `cmd/server/main.go` 自定义 `flag.Usage`，通过 `i18n.Load(i18n.ResolveLocale())` 输出 zh/en 之一的所有 flag 说明；新增 `-v` / `-version` flag 打印 `mcc <version>`（程序名 + 空格 + 版本号，如 `mcc v0.8.1`）后 `os.Exit(0)`；`internal/i18n/i18n.go` 的 `Messages` 补齐所有 flag 的 zh/en 帮助文案（含新增的 `-proxy-listen` / `-proxy-port` / `-admin-listen` / `-admin-port` / `-version`）。

**Evidence（证据）** — 手动验证：中文 locale 下 `mcc -h` 输出中文 flag 说明；英文 locale 下输出英文；`mcc -v` 输出 `mcc v0.8.1`（程序名 + 版本）后立即退出，不启动服务；未知 flag 触发本地化 Usage 并以 exit code 2 退出（标准库默认行为）。

**Constraints（约束）** — 复用现有 `i18n.ResolveLocale()` + `i18n.Load()` 语言探测，不引入第二套；flag 说明文案集中在 `Messages` 结构（en/zh 两套），不在 main.go 硬编码；`-version` 处理必须早于数据目录解析、配置加载、网络监听等任何启动副作用；`-h` / `-help` / `--help` 均触发本地化帮助（标准库 `flag` 自动路由）。

**Edge Cases（边界）** — 本地未注入 ldflags 的构建（版本为 `dev`）；`MCC_LANG` 环境变量强制覆盖语言；locale 探测失败（回退英文）；用户传未知 flag（标准库默认打印 Usage，本次让其走本地化）。

**Verification（验证）** — `MCC_LANG=zh` 下 `mcc -h` 输出中文；`MCC_LANG=en` 下输出英文；`mcc -v` 输出版本并退出码 0；不启动任何监听端口。

#### 计划

1. 在 `internal/i18n/i18n.go` 的 `Messages` 结构新增各 flag 的帮助文案字段（en/zh 两套），命名沿用现有 `FlagDataDir` / `FlagPassword` 风格（如 `FlagProxyListen`、`FlagVersion` 等）。
2. 在 `cmd/server/main.go` 的 `main` 顶部（`i18n.Load` 之后、`flag.String` 调用之前）设置 `flag.Usage` 为一个闭包，按 `msg` 输出本地化的 Usage 头部 + `flag.PrintDefaults()`（PrintDefaults 自动使用各 flag 注册时的 usage 字符串，即 `msg.FlagXxx`）。
3. 新增 `-v` / `-version` flag（bool），在 `flag.Parse` 后立即检查：若为 true，打印 `mcc <version>`（如 `mcc v0.8.1`，程序名 + 空格 + `version.Version`）并 `os.Exit(0)`。
4. 确保所有 `flag.String` / `flag.Bool` 的 usage 参数都传 `msg.FlagXxx`，不硬编码字面量。
5. 测试：构造不同 locale 的 `msg`，断言 Usage 输出包含预期语言的 flag 说明关键词；断言 `-version` 路径在启动副作用之前退出。

#### 验证

- [ ] `mcc -h`、`mcc -help`、`mcc --help` 均触发本地化帮助输出。
- [ ] 中文 locale（`zh*`）下帮助为中文；其他 locale 为英文。
- [ ] `mcc -v` 与 `mcc --version` 均打印 `mcc vX.Y.Z`（程序名 + 版本）并退出码 0。
- [ ] `-version` 时不创建数据目录、不加载配置、不监听任何端口。
- [ ] 所有 flag 说明均来自 `Messages` 结构，无 main.go 硬编码字面量。
- [ ] 未知 flag 触发的 Usage 也是本地化的。
