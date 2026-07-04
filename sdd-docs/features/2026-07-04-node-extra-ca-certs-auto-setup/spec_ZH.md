# Node.js 客户端 CA 信任自动配置规格

本地页面：无（mcc 二进制启动时由 bootstrap 自动执行）
代理入口：`cmd/server/main.go` → `internal/bootstrap`
参考源站：Node.js TLS 文档、Windows 注册表环境变量 API、macOS `launchctl`、POSIX shell profile 约定
技术栈：Go 1.26 标准库（`os`、`os/exec`、`runtime`、`path/filepath`、`syscall`）
最后更新：2026-07-04
进度：0 / 7 已规划

## 整体分析（源码分析）

### 当前项目状态

mcc（magic-claude-code）是 Go 单二进制透明代理。启动时 `internal/bootstrap` 包按以下顺序尝试配置透明模式（`bootstrap.go:161-203` `Executor.Run`）：

1. **tryHosts**（`bootstrap.go:205-213`）—— 改 hosts 把 `api.anthropic.com` → `127.0.0.1`
2. **tryTrustCA**（`bootstrap.go:215-227`）—— 把 mcc 的 CA 装进**操作系统**信任库（Windows `certutil`、macOS `security`、Linux `update-ca-certificates`）
3. **tryPersistEnv**（`bootstrap.go:229-232`）—— 调用 `e.env.PersistRoot(rootDir)` 持久化 `MCC_ROOT`

`EnvAdapter` 接口定义（`bootstrap.go:71-74`）：

```go
// EnvAdapter abstracts environment persistence.
type EnvAdapter interface {
    PersistRoot(rootDir string) error
}
```

唯一实现 `osEnvAdapter.PersistRoot`（`adapters.go:311-354`）：

- Windows：`execWithTimeout("setx", "MCC_ROOT", rootDir)` —— 仅设置 `MCC_ROOT`
- macOS/Linux：往 zsh/bash/fish 的 profile 写 `export MCC_ROOT=...`（`adapters.go:326-348`，通过 `resolveShellProfiles` + `writeProfileEntry`）

**关键结论：当前 bootstrap 完全不设置 `NODE_EXTRA_CA_CERTS`，也不修改任何 pwsh `$PROFILE`。** `NODE_EXTRA_CA_CERTS` 仅出现在 `instructions.go`（`windowsSet("NODE_EXTRA_CA_CERTS", caPath)` 行 112/147、`export NODE_EXTRA_CA_CERTS=...` 行 117/152），且 `windowsSet`（`instructions.go:280-282`）是**字符串拼接函数**，产物经 `fmt.Println`（`bootstrap.go:276-279`）打印给用户自行执行，不是自动执行。

### Node.js CA 信任机制（核心发现）

Node.js 的 TLS 默认使用编译进二进制的 **bundled CA 列表**（Mozilla CA bundle），**不读取操作系统的证书库**——Windows cert store、macOS Keychain、Linux `/etc/ssl/certs` 都不读。这是 Node 的设计（`--use-bundled-ca` 为默认，`--use-openssl-ca` 才走系统库）。

**`NODE_EXTRA_CA_CERTS`** 是 Node.js 在**进程启动的 bootstrap 阶段一次性读取**的环境变量，把指定 CA 文件追加进 TLS 信任库。已在用户环境实测验证：

- 启动时移除该变量、运行时才设 → `SELF_SIGNED_CERT_IN_CHAIN`（Node 不信任 mcc 的 CA）
- 启动时就带该变量 → TLS 握手成功

**这意味着：**

- mcc 透明模式步骤 2「装系统 CA」对浏览器、curl 等有效，但**对 Node.js 客户端（Claude Code）无效**
- Claude Code 必须单独通过 `NODE_EXTRA_CA_CERTS` 指向 mcc 的 CA 文件才能信任 mcc 的 MITM 证书
- 透明模式就绪判断（`bootstrap.go:154-157` `IsTransparentReady`）**不检查 `NODE_EXTRA_CA_CERTS`**，导致 mcc 误认为透明模式对 Node 客户端已就绪，而实际 Node 客户端仍会 `401 Invalid bearer token`

### 三平台环境变量持久化机制

要让 `NODE_EXTRA_CA_CERTS` 对**未来启动的 Node 进程**生效，必须持久化到比"当前进程环境"更广的层级：

| 平台 | 持久化层 | 作用域 | 写入方式 |
| --- | --- | --- | --- |
| Windows | 注册表 `HKCU\Environment` | 当前用户 | `setx VAR value` 或 `[Environment]::SetEnvironmentVariable(...,"User")` |
| Windows | 注册表 `HKLM\SYSTEM\...\Environment` | 全机器（需管理员） | `setx /M VAR value` 或 `SetEnvironmentVariable(...,"Machine")` |
| macOS | `launchctl setenv VAR value` | 当前用户 GUI 会话 | `launchctl setenv`（注销后失效，需配合 plist 持久化） |
| macOS | `~/.zshrc` / `~/.zprofile` / `~/.bash_profile` | 登录 shell | `export VAR=value` |
| Linux | `~/.bashrc` / `~/.profile` / `~/.zshrc` | 登录 shell | `export VAR=value` |
| Linux | `/etc/profile.d/mcc.sh` | 全机器登录 shell（需 root） | `export VAR=value` |

**GUI 进程环境继承陷阱**（已在用户环境实测）：Windows 上 `explorer.exe` 登录时读一次注册表后**不再刷新**；GUI 应用（如 Orca、Cursor）从 explorer 继承环境快照。若 mcc 在用户登录后才写入注册表，已运行的 explorer 拿不到，其派生的 GUI 应用及其子 shell 也拿不到——直到 explorer 重启或用户注销重登。

### PowerShell `$PROFILE` 兜底机制（已实测验证）

针对上述 GUI 继承陷阱，在 pwsh 的 `$PROFILE`（`Documents\PowerShell\Microsoft.PowerShell_profile.ps1`）里设置 `NODE_EXTRA_CA_CERTS` 是可靠兜底：pwsh 每次启动都执行 profile，不依赖父进程继承。

**已在用户环境验证可行的 `$PROFILE` 内容**（实测：清空继承变量后启动 pwsh，变量仍被 profile 设上）：

```powershell
$mccCa = "$env:USERPROFILE\mcc-windows-amd64\data\ca.crt"
if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }
```

并已核实 Orca 启动 pwsh 时不带 `-NoProfile`（`orca/src/main/providers/windows-shell-args.ts:156-167` 的 args 为 `-NoLogo -NoExit -EncodedCommand`），且注释明确「dot-source `$PROFILE`」——所以 GUI 派生的 pwsh 终端会执行 profile，绕开继承断链。

**cmd `AutoRun` 已验证不可行**：`HKCU\Software\Microsoft\Command Processor\AutoRun` 被 Windows Defender / EDR 写保护（实测 `Set-ItemProperty` 返回 `Attempted to perform an unauthorized operation`，而普通 HKCU 键可写），因此本规格不为 cmd 配 AutoRun，cmd 仅靠注册表继承。

### 风险总结

1. Node.js 不读系统 CA 信任库——`NODE_EXTRA_CA_CERTS` 是唯一让 Claude Code 信任 mcc CA 的路径，但当前 mcc 完全不自动设置它。
2. 透明模式就绪判断遗漏 `NODE_EXTRA_CA_CERTS`，导致对 Node 客户端的"假就绪"。
3. GUI 进程环境继承不可靠（explorer 不刷新），单纯写注册表不足以覆盖所有启动场景，需要 pwsh `$PROFILE` 兜底。
4. Windows `setx` 只影响**未来**新进程，不注入当前进程；mcc 自身进程已启动，无法靠 setx 自救（但 mcc 是代理服务，自身不需要 Node CA；需要的是它的客户端 Claude Code）。
5. macOS `launchctl setenv` 注销后失效，需配合 profile 持久化。
6. 重复写入 profile 必须幂等（已有 `MCC_ROOT` 的去重逻辑可复用，`profileHasEquivalentEntry` / `profileHasExactEntry`）。
7. CA 路径变化（升级、迁移）后，过时的 `NODE_EXTRA_CA_CERTS` 指向无效文件，可能让别的 Node 程序启动失败——必须用 fingerprint 标记或路径比对来识别过期并更新。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已规划 | 扩展 `EnvAdapter` 接口 + bootstrap 集成 | `bootstrap.go`（接口、`tryPersistNodeCA`、`Result` 字段） | 单元测试：mock adapter 验证调用与就绪判断 |
| 2 | 已规划 | Windows 实现（注册表 + pwsh `$PROFILE`） | `adapters.go`（`PersistNodeCACert` Windows 分支 + pwsh profile 写入） | 单元测试 + 手动验证新 pwsh 里有变量 |
| 3 | 已规划 | macOS 实现（launchctl + zsh/bash profile） | `adapters.go`（macOS 分支） | 单元测试 + 手动验证 |
| 4 | 已规划 | Linux 实现（profile + 可选 `/etc/profile.d`） | `adapters.go`（Linux 分支） | 单元测试 + 手动验证 |
| 5 | 已规划 | 幂等检测与过期识别（fingerprint 标记） | `bootstrap.go` + `adapters.go`（标记文件读写） | 单元测试：重复运行不重复写、CA 变更触发更新 |
| 6 | 已规划 | 单元测试覆盖三平台 + 幂等 + 过期 | `bootstrap_test.go`、`adapters_test.go` | `go test ./internal/bootstrap/ -v -race` |
| 7 | 已规划 | 端到端手动验证（三平台） | 验证记录 | 在三平台真机运行 mcc，确认 Node 客户端拿到变量 |

## 需求

### 交付物

1. `EnvAdapter` 接口扩展一个新方法 `PersistNodeCACert(caCertPath string) error`，所有实现（OS 适配器 + 现有 mock）必须实现。
2. `osEnvAdapter.PersistNodeCACert` 按平台实现：
   - **Windows**：① `setx NODE_EXTRA_CA_CERTS <caCertPath>`（写 `HKCU\Environment`，REG_EXPAND_SZ 不强求，`setx` 默认 REG_SZ 即可）；② 往 pwsh `$PROFILE`（`%USERPROFILE%\Documents\PowerShell\Microsoft.PowerShell_profile.ps1`）追加幂等块设置该变量。
   - **macOS**：① `launchctl setenv NODE_EXTRA_CA_CERTS <caCertPath>`；② 往 `~/.zshrc`（或检测到的 shell profile）追加 `export NODE_EXTRA_CA_CERTS=...`。
   - **Linux**：往 `~/.bashrc` / `~/.profile` / `~/.zshrc`（按 `$SHELL` 选择）追加 `export NODE_EXTRA_CA_CERTS=...`。
3. `bootstrap.go` 在透明模式路径里调用 `tryPersistNodeCA`（紧跟 `tryTrustCA` 之后，因为依赖 CA 文件已生成），并把结果记入 `Result.NodeCAResult`。
4. `IsTransparentReady` 的 Node 客户端就绪判断**不强依赖** `NODE_EXTRA_CA_CERTS`（保持系统级透明模式语义），但 `LogResult` 在 `NodeCAResult` 失败时打印明确提示「Node 客户端（如 Claude Code）需要此变量才能信任 mcc CA」。
5. 幂等性：重复运行不重复写 profile 行（复用 `profileHasExactEntry`）；CA 文件路径变化（fingerprint 不匹配）时更新。
6. 单元测试覆盖三平台分支（用接口注入 mock）、幂等、过期识别、profile 内容正确性。
7. 端到端验证记录：三平台运行 mcc 后，新开的 shell（含 GUI 派生的 pwsh）里 `NODE_EXTRA_CA_CERTS` 有正确值。

### 目录结构

```text
internal/bootstrap/
  bootstrap.go           （修改：EnvAdapter 接口、Result.NodeCAResult、tryPersistNodeCA、LogResult 提示）
  adapters.go            （修改：osEnvAdapter.PersistNodeCACert 三平台实现 + pwsh profile 写入 + 标记文件）
  bootstrap_test.go      （修改：mockEnv 实现 PersistNodeCACert；新增 tryPersistNodeCA 测试）
  adapters_test.go       （新增或修改：PersistNodeCACert 三平台 + 幂等 + 过期 测试）
```

### 数据模型

```go
// bootstrap.go —— EnvAdapter 接口扩展
type EnvAdapter interface {
    PersistRoot(rootDir string) error
    // PersistNodeCACert 把指向 mcc CA 文件的 NODE_EXTRA_CA_CERTS 持久化到
    // 当前用户的 shell/桌面会话环境，使未来启动的 Node.js 客户端能信任 mcc。
    PersistNodeCACert(caCertPath string) error
}

// bootstrap.go —— Result 新增字段
type Result struct {
    // ...既有字段...
    NodeCAResult StepResult // NODE_EXTRA_CA_CERTS 持久化结果
}
```

pwsh `$PROFILE` 追加块（Windows 兜底，已实测验证）：

```powershell
# >>> mcc: Node.js CA trust (auto-managed, do not edit) >>>
$mccCa = "$env:USERPROFILE\<相对 CA 路径>"
if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }
# <<< mcc <<<
```

POSIX shell profile 追加行（macOS/Linux，复用既有 `shellExportEntry` 生成）：

```bash
export NODE_EXTRA_CA_CERTS="<CA 绝对路径>"  # mcc-managed
```

### 约束

1. 不破坏 `EnvAdapter` 现有 `PersistRoot` 语义与现有测试。
2. Windows 写注册表用 `setx`（与 `PersistRoot` 一致），不引入新依赖；写 pwsh `$PROFILE` 用 `os.OpenFile` 追加（与 POSIX profile 写入一致）。
3. 不修改 `HKLM`（机器级）注册表，避免管理员权限要求；用户级 `HKCU` 足够覆盖当前用户的 Node 客户端。
4. 不触碰 `HKCU\Software\Microsoft\Command Processor\AutoRun`（已验证被 Defender 写保护）。
5. macOS 不写 `/Library/LaunchDaemons`（避免 root）；只用用户级 `launchctl setenv` + 用户 profile。
6. profile 写入必须幂等：已有等价行则跳过（复用 `profileHasEquivalentEntry` / `profileHasExactEntry`），且带 mcc 标记注释便于将来清理。
7. CA 路径变化（升级后 CA 文件位置变）必须识别并更新——用 CA fingerprint 标记文件（同 `tryTrustCA` 的 `.ca-trust-installed` 思路），fingerprint 不匹配时重写。
8. 任何步骤失败不 panic、不阻塞代理启动（best-effort，同现有 bootstrap 哲学）；失败信息进 `NodeCAResult.Err` 并在 `LogResult` 打印。
9. `setx` / `launchctl setenv` 只影响未来进程，不影响当前 mcc 进程——这是预期行为（mcc 自身不需要 Node CA）。
10. 不在 Docker 容器内执行宿主机写入（`bootstrap.go:178` 既有 Docker 边界判断保持；Docker 内跳过 `tryPersistNodeCA`，同 `tryHosts`/`tryTrustCA` 的 Docker 处理）。

### 边界情况

1. CA 文件还未生成（`tryTrustCA` 失败）——`tryPersistNodeCA` 应在 CA 不存在时返回明确错误，不写无效路径。
2. pwsh 未安装（`pwsh.exe` / `pwsh` 不在 PATH）——跳过 `$PROFILE` 写入，只写注册表/launchctl，记录为 partial success。
3. `$PROFILE` 目录不存在（用户从未开过 pwsh）——`os.MkdirAll` 创建 `Documents\PowerShell\`（同 POSIX `MkdirAll(filepath.Dir(p))`）。
4. profile 已含等价行但路径不同（CA 升级）——fingerprint 标记不匹配触发更新：先删旧 mcc-managed 块/行，再写新的。
5. profile 已含用户手写的 `NODE_EXTRA_CA_CERTS`（非 mcc-managed）——不覆盖，记录警告「检测到用户自定义值，mcc 不覆盖，请确认指向 mcc CA」。
6. `setx` 失败（注册表权限/EDR）——降级为只写 pwsh `$PROFILE`，`NodeCAResult` 记录 partial。
7. macOS `launchctl` 不存在（非 macOS）——跳过，只写 profile。
8. `$SHELL` 未设置或未知 shell——复用 `resolveShellProfiles` 的 fallback（`~/.profile` → `~/.bashrc`）。
9. fish shell——复用 `shellExportEntry` 的 fish 语法（`set -gx NODE_EXTRA_CA_CERTS ...`）。
10. Docker 环境——跳过整个 `tryPersistNodeCA`（容器内 profile 改动对宿主无意义）。

### 非目标

1. 不修改机器级（`HKLM` / `/etc`）配置——只动当前用户环境。
2. 不为 cmd 配 `AutoRun`（Defender 写保护，已验证不可行）。
3. 不重启 explorer.exe 或强制用户注销（只在文档/日志里提示「注销重登或重启 explorer 让注册表生效」）。
4. 不修改 Claude Code 的 `~/.claude/settings.json`（那是用户配置，且 settings.json 的 env 对 Node 自身 TLS 无效——已实测运行时注入太晚）。
5. 不实现「让 mcc 自身进程也读到 `NODE_EXTRA_CA_CERTS`」——mcc 是 Go 程序不是 Node，不需要。
6. 不在 Tunnel/Gateway 模式下额外做什么——这两种模式的 instructions 已经提示用户设 `NODE_EXTRA_CA_CERTS`；本规格聚焦透明模式自动补齐。

## 任务详情

### 任务 1：扩展 EnvAdapter 接口与 bootstrap 集成

#### 需求

**Objective（目标）** — 给 `EnvAdapter` 接口加 `PersistNodeCACert` 方法，并在 bootstrap 流程里调用它，把结果记入 `Result`。

**Outcomes（成果）** — `bootstrap.go` 的 `EnvAdapter` 接口含 `PersistNodeCACert(caCertPath string) error`；`Result` 新增 `NodeCAResult StepResult`；新增 `tryPersistNodeCA` 方法在 `Run` 里被调用（透明模式、非 Docker、CA 已就绪时）；`IsTransparentReady` 语义不变（不把 NodeCA 计入硬性就绪），但 `LogResult` 在 NodeCA 失败时打印提示。

**Evidence（证据）** — 单元测试：mock `EnvAdapter` 记录 `PersistNodeCACert` 被调用、参数正确；Docker 环境下不调用；CA 未就绪时不调用。

**Constraints（约束）** — 保持 `Run` 的 best-effort 哲学（任何步骤失败不阻塞）；`tryPersistNodeCA` 只在 `tryTrustCA` 成功后调用（依赖 CA 文件存在）。

**Edge Cases（边界）** — Docker 环境（跳过）；CA 文件不存在（跳过并记录）；mock adapter 返回错误（记入 `NodeCAResult.Err`，不影响代理启动）。

**Verification（验证）** — `go test ./internal/bootstrap/ -run TestTryPersistNodeCA -v`。

#### 计划

1. 修改 `bootstrap.go` 的 `EnvAdapter` 接口，新增 `PersistNodeCACert(caCertPath string) error`。
2. 修改 `Result` 结构，新增 `NodeCAResult StepResult` 字段。
3. 新增 `tryPersistNodeCA` 方法：

```go
// tryPersistNodeCA 持久化 NODE_EXTRA_CA_CERTS，使未来启动的 Node.js 客户端
// （如 Claude Code）能信任 mcc 的 CA。仅在透明模式、非 Docker、CA 已就绪时调用。
func (e *Executor) tryPersistNodeCA() StepResult {
    if e.caCertPath == "" {
        return StepResult{Attempted: false}
    }
    if _, err := os.Stat(e.caCertPath); err != nil {
        // CA 文件不存在，依赖未满足，不尝试
        return StepResult{Attempted: false, Err: err}
    }
    // 先检查标记：CA fingerprint 未变则视为已持久化（幂等）
    if hasNodeCAMarker(e.dataDir, e.caCertPath) {
        return StepResult{Success: true}
    }
    err := e.env.PersistNodeCACert(e.caCertPath)
    if err == nil {
        writeNodeCAMarker(e.dataDir, e.caCertPath)
    }
    return StepResult{Attempted: true, Success: err == nil, Err: err}
}
```

4. 在 `Run` 的透明模式分支（`bootstrap.go:177-187`），`tryTrustCA` 之后插入：

```go
result.HostsResult = e.tryHosts()
result.TrustResult = e.tryTrustCA()
// 新增：CA 已就绪后，持久化 Node 客户端 CA 信任
if result.TrustResult.Success {
    result.NodeCAResult = e.tryPersistNodeCA()
}
```

5. 修改 `LogResult`（`bootstrap.go:245-281`），在打印步骤区追加 Node CA 步骤：

```go
printStep(e.locale, "NODE_CA", r.NodeCAResult)
```

并在 `transparentSuccessInstructions`（`instructions.go:23-51`）追加提示行（当 `NodeCAResult.Attempted && !NodeCAResult.Success` 时）：

```
- 提示：NODE_EXTRA_CA_CERTS 持久化失败，Node.js 客户端（如 Claude Code）可能无法信任 mcc CA
```

#### 验证

- [ ] `EnvAdapter` 接口含 `PersistNodeCACert`。
- [ ] `tryPersistNodeCA` 在 CA 就绪时被调用，mock 记录到正确参数。
- [ ] Docker/CA 不存在时跳过。
- [ ] `NodeCAResult` 失败不阻塞代理启动。
- [ ] `go test ./internal/bootstrap/ -v` 全绿。

### 任务 2：Windows 实现（注册表 + pwsh `$PROFILE`）

#### 需求

**Objective（目标）** — `osEnvAdapter.PersistNodeCACert` 的 Windows 分支：① `setx NODE_EXTRA_CA_CERTS <path>` 写用户级注册表；② 往 pwsh `$PROFILE` 追加幂等块。

**Outcomes（成果）** — Windows 上运行 mcc 后：新进程（含 GUI 派生）从注册表继承到 `NODE_EXTRA_CA_CERTS`；pwsh 终端（含 Orca 等 GUI 派生的）通过 `$PROFILE` 兜底也有该变量。

**Evidence（证据）** — 已在用户环境实测：pwsh `$PROFILE` 追加 `$env:NODE_EXTRA_CA_CERTS = $mccCa` 后，清空继承变量启动 pwsh，变量仍被设上；`setx` 写注册表后 explorer 重启读到的值正确。

**Constraints（约束）** — 用 `setx`（与 `PersistRoot` 一致，不引入 Win32 API 依赖）；pwsh profile 写入幂等（标记块 + 路径比对）；不碰 `AutoRun`（Defender 保护）。

**Edge Cases（边界）** — pwsh 未安装（跳过 profile，只 setx）；`$PROFILE` 目录不存在（MkdirAll）；profile 已含 mcc-managed 块但路径不同（更新）；profile 含用户自定义非 mcc 值（不覆盖，警告）；setx 失败（降级为只写 profile）。

**Verification（验证）** — 单元测试覆盖 setx 命令构造、profile 块生成、幂等、更新；手动：运行后新 pwsh 里 `echo $env:NODE_EXTRA_CA_CERTS` 有值。

#### 计划

1. 在 `adapters.go` 的 `PersistNodeCACert` 加 Windows 分支：

```go
func (a *osEnvAdapter) PersistNodeCACert(caCertPath string) error {
    switch runtime.GOOS {
    case "windows":
        return a.persistNodeCACertWindows(caCertPath)
    case "darwin":
        return a.persistNodeCACertDarwin(caCertPath)
    default:
        return a.persistNodeCACertPOSIX(caCertPath)
    }
}

func (a *osEnvAdapter) persistNodeCACertWindows(caCertPath string) error {
    var setxErr error
    // ① setx 写用户级注册表（影响未来新进程）
    if out, err := execWithTimeout("setx", "NODE_EXTRA_CA_CERTS", caCertPath); err != nil {
        setxErr = fmt.Errorf("setx NODE_EXTRA_CA_CERTS: %w: %s", err, decodeCmdOutput(out))
        // 不 return，继续尝试 profile 兜底
    }

    // ② pwsh $PROFILE 兜底（覆盖 GUI 继承断链场景）
    profileErr := a.writePwshProfileNodeCA(caCertPath)

    if setxErr != nil && profileErr != nil {
        return fmt.Errorf("setx: %v; profile: %w", setxErr, profileErr)
    }
    return nil
}
```

2. 实现 pwsh `$PROFILE` 写入（幂等 + 标记块）：

```go
const (
    pwshProfileMarkerBegin = "# >>> mcc: Node.js CA trust (auto-managed, do not edit) >>>"
    pwshProfileMarkerEnd   = "# <<< mcc <<<"
)

func (a *osEnvAdapter) writePwshProfileNodeCA(caCertPath string) error {
    home, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("user home dir: %w", err)
    }
    // 探测 pwsh 是否安装
    if _, err := exec.LookPath("pwsh.exe"); err != nil {
        if _, err2 := exec.LookPath("powershell.exe"); err2 != nil {
            return nil // pwsh 未安装，跳过（非错误）
        }
        // 仅有 Windows PowerShell 5.1：profile 路径不同
    }
    // 两个候选 profile：pwsh 7 与 Windows PowerShell 5.1
    candidates := []string{
        filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
        filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
    }

    // CA 相对 home 的路径（profile 里用 $env:USERPROFILE 还原，换机器不破）
    rel := strings.TrimPrefix(caCertPath, home+string(os.PathSeparator))
    block := fmt.Sprintf("%s\n"+
        "$mccCa = \"$env:USERPROFILE\\%s\"\n"+
        "if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n"+
        "%s\n", pwshProfileMarkerBegin, rel, pwshProfileMarkerEnd)

    var lastErr error
    wrote := false
    for _, profile := range candidates {
        existing, _ := os.ReadFile(profile)
        updated, changed := replaceMarkedBlock(string(existing), pwshProfileMarkerBegin, pwshProfileMarkerEnd, block)
        if !changed && hasExactValueInPwshBlock(string(existing), caCertPath) {
            // 已含等价块且路径一致，跳过
            wrote = true
            break
        }
        if err := os.MkdirAll(filepath.Dir(profile), 0755); err != nil {
            lastErr = err
            continue
        }
        if err := os.WriteFile(profile, []byte(updated), 0644); err != nil {
            lastErr = err
            continue
        }
        wrote = true
        break
    }
    if !wrote && lastErr != nil {
        return lastErr
    }
    return nil
}

// replaceMarkedBlock 在 content 里替换 begin..end 标记之间的内容为 newBlock。
// 若标记不存在则追加。changed 表示是否实际改动。
func replaceMarkedBlock(content, begin, end, newBlock string) (string, bool) {
    bi := strings.Index(content, begin)
    ei := strings.Index(content, end)
    if bi >= 0 && ei > bi {
        // 已有标记块：比较内容，相同则不改
        existing := content[bi : ei+len(end)]
        if existing == strings.TrimRight(newBlock, "\n") {
            return content, false
        }
        // 不同（路径变了）：替换
        return content[:bi] + newBlock + content[ei+len(end):], true
    }
    // 无标记块：追加
    if content != "" && !strings.HasSuffix(content, "\n") {
        content += "\n"
    }
    return content + newBlock, true
}
```

3. 标记文件（任务 5 的 fingerprint 机制，此处先占位调用）：

```go
// hasNodeCAMarker / writeNodeCAMarker 见任务 5。
```

#### 验证

- [ ] `setx` 命令参数正确（`NODE_EXTRA_CA_CERTS`, caCertPath）。
- [ ] pwsh profile 块含正确的 `$env:USERPROFILE` 还原与 `Test-Path` 守卫。
- [ ] 重复运行不重复追加（标记块识别）。
- [ ] CA 路径变化时更新标记块内容。
- [ ] pwsh 未安装时跳过 profile 不报错。
- [ ] 手动：运行 mcc 后新 pwsh 里 `echo $env:NODE_EXTRA_CA_CERTS` 输出 CA 路径。

### 任务 3：macOS 实现（launchctl + zsh/bash profile）

#### 需求

**Objective（目标）** — macOS 分支：① `launchctl setenv NODE_EXTRA_CA_CERTS <path>` 注入当前 GUI 会话；② 往用户 shell profile 追加 `export NODE_EXTRA_CA_CERTS=...`。

**Outcomes（成果）** — macOS 上运行 mcc 后：当前 GUI 会话里 GUI 应用派生的进程拿到变量；未来登录 shell 从 profile 拿到变量。

**Evidence（证据）** — `launchctl setenv` 是 macOS 注入用户级 GUI 会话环境的标准方式（`launchctl getenv` 可验证）；profile 写入复用既有 `shellExportEntry` + `writeProfileEntry`（已被 `PersistRoot` 验证可行）。

**Constraints（约束）** — `launchctl setenv` 注销后失效，必须配合 profile 持久化；不写 LaunchAgent plist（避免过度配置，profile 已够）。

**Edge Cases（边界）** — 非 macOS（不进入此分支）；`launchctl` 不存在（跳过，只写 profile）；`$SHELL` 是 zsh/bash/fish/未知（复用 `resolveShellProfiles`）；profile 已含等价行（跳过）。

**Verification（验证）** — 单元测试覆盖 launchctl 命令构造、profile 行生成（含各 shell 语法）；手动：运行后 `launchctl getenv NODE_EXTRA_CA_CERTS` 与新 shell 里 `echo $NODE_EXTRA_CA_CERTS` 都有值。

#### 计划

1. 实现 macOS 分支：

```go
func (a *osEnvAdapter) persistNodeCACertDarwin(caCertPath string) error {
    // ① launchctl setenv 注入当前 GUI 会话（影响从 Dock/Launchpad 启动的应用）
    if _, err := exec.LookPath("launchctl"); err == nil {
        if out, err := execWithTimeout("launchctl", "setenv", "NODE_EXTRA_CA_CERTS", caCertPath); err != nil {
            // 非致命，继续写 profile
            log.Printf("[Bootstrap] launchctl setenv failed: %v: %s", err, decodeCmdOutput(out))
        }
    }

    // ② profile 持久化（复用 PersistRoot 的 POSIX profile 写入路径）
    return a.writePOSIXProfileNodeCA(caCertPath)
}
```

2. POSIX profile 写入（macOS/Linux 共用）：

```go
func (a *osEnvAdapter) writePOSIXProfileNodeCA(caCertPath string) error {
    shell := os.Getenv("SHELL")
    home, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("user home dir: %w", err)
    }
    // 复用既有 shellExportEntry（已支持 bash/zsh export 与 fish set -gx）
    entry := shellExportEntry(shell, "NODE_EXTRA_CA_CERTS", caCertPath)
    profiles := resolveShellProfiles(shell, home)

    openProfile := func(p string) (writeCloser, error) {
        if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
            return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
        }
        return os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
    }

    var lastErr error
    for _, profile := range profiles {
        if existing, rErr := os.ReadFile(profile); rErr == nil {
            content := string(existing)
            if profileHasEquivalentEntry(shell, content, "NODE_EXTRA_CA_CERTS", caCertPath) ||
                profileHasExactEntry(content, entry) {
                return nil // 已有等价项
            }
        }
        if err := writeProfileEntry(openProfile, profile, entry); err != nil {
            lastErr = err
            continue
        }
        return nil
    }
    if lastErr != nil {
        return lastErr
    }
    return fmt.Errorf("no profile file writable (tried %v)", profiles)
}
```

#### 验证

- [ ] `launchctl setenv` 参数正确。
- [ ] profile 行用 `shellExportEntry` 生成（zsh/bash `export`、fish `set -gx`）。
- [ ] 重复运行不重复追加（`profileHasEquivalentEntry` / `profileHasExactEntry`）。
- [ ] 手动：`launchctl getenv NODE_EXTRA_CA_CERTS` 返回 CA 路径；新 zsh/bash 里 `echo $NODE_EXTRA_CA_CERTS` 有值。

### 任务 4：Linux 实现（profile + 可选 `/etc/profile.d`）

#### 需求

**Objective（目标）** — Linux 分支：往用户 shell profile 追加 `export NODE_EXTRA_CA_CERTS=...`（机器级 `/etc/profile.d` 列为非目标，需 root，本期不做）。

**Outcomes（成果）** — Linux 上运行 mcc 后，未来登录 shell 拿到 `NODE_EXTRA_CA_CERTS`。

**Evidence（证据）** — 复用 `PersistRoot` 已验证的 POSIX profile 写入路径（`resolveShellProfiles` + `writeProfileEntry`）；现有 `TestPersistRoot_DeduplicatesExistingEntry` 等测试证明幂等机制工作。

**Constraints（约束）** — 只动用户级 profile（`~/.bashrc` 等），不动 `/etc`；shell 未知时 fallback `~/.profile` → `~/.bashrc`（既有 `resolveShellProfiles` 行为）。

**Edge Cases（边界）** — `$SHELL` 未设置（fallback）；fish shell（`set -gx` 语法）；profile 不存在（创建，含父目录）；profile 已含等价行（跳过）；无写权限（记录错误，继续）。

**Verification（验证）** — 单元测试覆盖 bash/zsh/fish/未知 shell 的 profile 选择与行生成；手动：运行后新 shell 里有变量。

#### 计划

1. Linux 分支直接复用 macOS 的 `writePOSIXProfileNodeCA`：

```go
// persistNodeCACertPOSIX 已在任务 3 的 PersistNodeCACert switch default 分支调用 writePOSIXProfileNodeCA
func (a *osEnvAdapter) persistNodeCACertPOSIX(caCertPath string) error {
    return a.writePOSIXProfileNodeCA(caCertPath)
}
```

2.（可选增强，本期非目标）若未来需要机器级，新增 root 检测 + `/etc/profile.d/mcc-node-ca.sh` 写入，但需 sudo 提示，不在本期。

#### 验证

- [ ] bash/zsh/fish 的 profile 路径正确。
- [ ] 行语法符合 shell（`export` vs `set -gx`）。
- [ ] 重复运行幂等。
- [ ] 手动：新 bash/zsh 里 `echo $NODE_EXTRA_CA_CERTS` 有值。

### 任务 5：幂等检测与过期识别（fingerprint 标记）

#### 需求

**Objective（目标）** — 在 `dataDir` 写 fingerprint 标记文件，记录上次持久化的 CA 内容指纹；重复运行且指纹未变则跳过；指纹变化（CA 重新生成）则重写。

**Outcomes（成果）** — `hasNodeCAMarker(dataDir, caCertPath) bool`、`writeNodeCAMarker(dataDir, caCertPath)`；`tryPersistNodeCA` 先查标记跳过；CA 变更后标记不匹配触发更新。

**Evidence（证据）** — 同 `tryTrustCA` 的 `.ca-trust-installed` 标记机制（`bootstrap.go:215-227`），已被现有测试验证有效。

**Constraints（约束）** — 标记文件用 CA 文件的 SHA256 fingerprint（不是路径），这样路径不变但 CA 内容变（重新生成）也能识别；标记文件写在 `dataDir`（mcc 有写权限）。

**Edge Cases（边界）** — 标记文件不存在（首次运行，执行）；标记 fingerprint 匹配（跳过）；不匹配（CA 升级，重写 + 更新标记）；标记文件读不了（视为未标记，执行）。

**Verification（验证）** — 单元测试：首次写标记、匹配跳过、CA 变更重写。

#### 计划

1. 实现 fingerprint 标记（仿 `hasCATrustMarker` / `writeCATrustMarker`）：

```go
const nodeCAMarkerName = ".node-ca-persisted"

func hasNodeCAMarker(dataDir, caCertPath string) bool {
    fp, err := caFingerprint(caCertPath)
    if err != nil {
        return false
    }
    markerPath := filepath.Join(dataDir, nodeCAMarkerName)
    data, err := os.ReadFile(markerPath)
    if err != nil {
        return false
    }
    return strings.TrimSpace(string(data)) == fp
}

func writeNodeCAMarker(dataDir, caCertPath string) {
    fp, err := caFingerprint(caCertPath)
    if err != nil {
        return
    }
    markerPath := filepath.Join(dataDir, nodeCAMarkerName)
    _ = os.WriteFile(markerPath, []byte(fp), 0644)
}

func caFingerprint(caCertPath string) (string, error) {
    data, err := os.ReadFile(caCertPath)
    if err != nil {
        return "", err
    }
    sum := sha256.Sum256(data)
    return hex.EncodeToString(sum[:]), nil
}
```

2.（若 `internal/bootstrap` 已有 `caFingerprint` 公共化的实现，复用之；否则在本任务内新增并补测试。）

#### 验证

- [ ] 首次运行写 `.node-ca-persisted`。
- [ ] 第二次运行（CA 不变）命中标记，跳过实际写入。
- [ ] CA 文件内容变化后，fingerprint 不匹配，重写 profile 并更新标记。

### 任务 6：单元测试

#### 需求

**Objective（目标）** — 覆盖三平台 `PersistNodeCACert`、幂等、过期识别、profile 内容正确性。

**Outcomes（成果）** — `adapters_test.go`（或 `bootstrap_test.go`）新增测试：Windows setx + pwsh profile、macOS launchctl + profile、Linux profile、幂等跳过、CA 变更重写、pwsh 未安装跳过、用户自定义值不覆盖。

**Evidence（证据）** — `go test ./internal/bootstrap/ -v -race` 全绿。

**Constraints（约束）** — 用接口注入 mock（同现有 `mockEnv`）；实际 setx/launchctl 调用用 `t.Setenv` 或 mock `exec` 隔离；profile 写入用 `t.TempDir()` 隔离。

**Edge Cases（边界）** — 见各任务的 Edge Cases。

**Verification（验证）** — `go test ./internal/bootstrap/... -v -race -cover`。

#### 计划

1. `mockEnv` 实现 `PersistNodeCACert`，记录调用与参数。
2. 新增 `TestPersistNodeCACert_Windows_SetxAndProfile`（mock exec 验证 setx 命令 + 写 pwsh profile 内容）。
3. 新增 `TestPersistNodeCACert_Darwin_LaunchctlAndProfile`。
4. 新增 `TestPersistNodeCACert_POSIX_ProfilePerShell`（bash/zsh/fish/未知）。
5. 新增 `TestWritePwshProfile_Idempotent`（重复写不追加）。
6. 新增 `TestWritePwshProfile_CAPathChanged_UpdatesBlock`。
7. 新增 `TestWritePwshProfile_UserCustomValue_NotOverwritten`。
8. 新增 `TestNodeCAMarker_MatchSkip_MismatchRewrite`。

#### 验证

- [ ] 所有新增测试通过。
- [ ] `go test ./internal/bootstrap/ -v -race` 全绿。
- [ ] 覆盖率不下降。

### 任务 7：端到端手动验证（三平台）

#### 需求

**Objective（目标）** — 在 Windows / macOS / Linux 真机上运行 mcc，确认 Node 客户端（pwsh/bash 里的 Claude Code）拿到 `NODE_EXTRA_CA_CERTS` 且能信任 mcc CA。

**Outcomes（成果）** — 一份验证记录，记录每平台的测试环境、步骤、结果（含 GUI 派生终端场景）。

**Evidence（证据）** — 各平台运行 mcc 后：① 新 shell 里 `echo $env:NODE_EXTRA_CA_CERTS` / `echo $NODE_EXTRA_CA_CERTS` 输出 CA 路径；② Windows 上从 GUI 应用（如 Orca）派生的 pwsh 也有该变量；③ Claude Code 不再报 `401 Invalid bearer token`。

**Constraints（约束）** — 不泄露 API 密钥；记录 CA 路径与 shell 类型；至少覆盖 Windows pwsh + macOS zsh + Linux bash。

**Edge Cases（边界）** — explorer 未重启（注册表未继承，但 pwsh profile 兜底）；pwsh 未安装（仅 setx，无 profile 兜底）；CA 升级后旧 profile 被更新。

**Verification（验证）** — 至少完成：Windows（含 Orca 派生 pwsh）+ macOS + Linux 各一次全链路。

#### 计划

1. **Windows**：
   - 运行 mcc（首次或 CA 变更后）。
   - 新开 pwsh（从开始菜单）→ `echo $env:NODE_EXTRA_CA_CERTS` 应有值。
   - 从 Orca 派生 pwsh → 同样应有值（profile 兜底）。
   - 在该 pwsh 里跑 `claude` → 不再 401。
   - 记录 `Get-ItemProperty HKCU:\Environment` 的 `NODE_EXTRA_CA_CERTS` 类型与值。
2. **macOS**：
   - 运行 mcc。
   - `launchctl getenv NODE_EXTRA_CA_CERTS` 应有值。
   - 新开 Terminal（zsh）→ `echo $NODE_EXTRA_CA_CERTS` 应有值。
   - 从 Dock 启动的 GUI 应用派生 shell → 应有值。
3. **Linux**：
   - 运行 mcc。
   - 新开 bash → `echo $NODE_EXTRA_CA_CERTS` 应有值。
   - 验证 `~/.bashrc` 含 `export NODE_EXTRA_CA_CERTS=...`（mcc-managed 标记）。
4. 记录每平台的 mcc 输出（`NODE_CA` 步骤状态）与实际变量值。

#### 验证

- [ ] Windows：新 pwsh + Orca 派生 pwsh 都有变量；claude 不 401。
- [ ] macOS：launchctl + 新 zsh 都有变量。
- [ ] Linux：新 bash 有变量；`~/.bashrc` 含 mcc-managed 行。
- [ ] 重复运行 mcc 不产生重复 profile 行。
- [ ] CA 升级后 profile 行被更新为新路径。
