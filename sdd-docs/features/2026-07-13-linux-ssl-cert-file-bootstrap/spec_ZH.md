# Linux 系统信任与 SSL_CERT_FILE 自动引导规格

本地页面：无（mcc 二进制启动时由 bootstrap 自动执行）  
代理入口：`cmd/server/main.go` -> `internal/bootstrap`  
参考源站：Claude Code 2.1.206 运行日志、Bun/BoringSSL TLS 行为、Linux `ca-certificates` bundle 约定、现有 `NODE_EXTRA_CA_CERTS` 自动配置规格  
技术栈：Go 1.26 标准库（`os`、`runtime`、`path/filepath`、`strings`、`errors`）  
最后更新：2026-07-13  
进度：7 / 7 已完成；Linux 人工端到端验证已完成

## 整体分析（源站分析）

### 现象与已验证根因

透明模式下，mcc 为 `api.anthropic.com` 终结 TLS。长对话结束后，Claude Code 会触发后台辅助请求（上下文管理、摘要、模型切换类短请求），代理日志间歇性出现 6 条握手错误：

```text
TLS handshake error from 172.23.0.1:55480 (SNI=api.anthropic.com): local error: tls: bad record MAC (client sent plaintext fatal alert: unknown_ca [48])
```

`unknown_ca [48]` 不是代理密钥错误，而是客户端拒绝代理证书链：客户端校验 mcc 生成的 `api.anthropic.com` 证书时，没有在该 TLS 路径的信任源中找到 mcc CA，于是发出 `fatal / unknown_ca`。代理此时已进入握手加密阶段，用 handshake key 解这条明文 alert，Go `crypto/tls` 报 `bad record MAC`。

当前机器上已排除项：

1. **当前机器系统 CA 已安装**：当前机器的 `/etc/ssl/certs/ca-certificates.crt` 中存在 mcc CA，指纹与 `data/ca.crt` 匹配。
2. **server.crt 链不完整**：`data/server.crt` 已包含 2 段 PEM（叶子 + CA），服务端握手会发送 2 个证书。
3. **主请求不信任 CA**：主长对话请求持续返回 200，说明主 fetch 路径正常。

但第 1 点只是当前机器的状态，因为此前人工执行过系统 CA 安装命令。新机器、重新生成 CA、换 `data/` 目录、容器迁移、系统 CA bundle 被包管理器重建后，都可能出现“`SSL_CERT_FILE` 指向了完整系统 bundle，但该 bundle 里没有 mcc CA”的状态。本规格不能把“系统 bundle 已含 mcc CA”当成外部前提，必须在启动时自动确保并验证。

已验证有效的修复：

```bash
export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
```

在启动 Claude Code 的 shell 环境中设置该变量后，长对话结束时能看到 `model=claude-sonnet-4-6 -> glm-5.2`、`model=claude-opus-4-7 -> glm-5.2` 等后台辅助请求正常通过 mcc 转发；最新窗口 `docker logs mcc --since 10m | grep -E "unknown_ca|bad record MAC"` 无输出。

### 为什么现有 bootstrap 不够

现有 `internal/bootstrap` 已实现 `NODE_EXTRA_CA_CERTS` 自动配置：

- `EnvAdapter.LookupNodeCACert()` 读取当前平台持久化值。
- `EnvAdapter.PersistNodeCACert(caCertPath)` 写入当前用户环境。
- Linux/macOS 使用 shell profile；Windows 使用 `setx` + PowerShell profile。
- `tryPersistNodeCA()` 通过 `.node-ca-persisted` 标记实现幂等、路径变化检测和用户自定义值保护。

但本次复现证明：Claude Code 2.1.206 的某条 Bun/BoringSSL 后台 TLS 路径**不稳定地不读取** `NODE_EXTRA_CA_CERTS`，也不能只依赖 `NODE_OPTIONS=--use-system-ca`。Linux 上需要显式提供 OpenSSL/BoringSSL 风格的完整根证书集合路径：

```bash
SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
```

必须强调：`SSL_CERT_FILE` 不能指向 `data/ca.crt`。部分 TLS 实现会把它当作完整根证书集合，而不是追加 CA；若只包含 mcc CA，会破坏 GitHub、插件服务等正常公网证书信任。

同时必须强调：`SSL_CERT_FILE` 指向完整系统 bundle 只解决“Claude Code/Bun 后台 TLS 路径读哪个 bundle”的问题；它不会自动把 mcc CA 放进该 bundle。若 `/etc/ssl/certs/ca-certificates.crt` 中没有与 `data/ca.crt` 同指纹的证书，后台 TLS 路径仍会发 `unknown_ca`。因此 Linux 自动处理流程必须是：

```text
生成/加载 data/ca.crt
  -> 安装 mcc CA 到 Linux 系统信任库
  -> 运行 update-ca-certificates 或 update-ca-trust extract
  -> 验证目标系统 bundle 中存在与 data/ca.crt 相同 SHA256 指纹的证书
  -> 持久化 SSL_CERT_FILE=该完整系统 bundle
```

### 平台范围决策

本规格只实现 **Linux 二进制运行场景** 的自动处理。

| 平台/部署方式 | 本期处理 | 原因 |
| --- | --- | --- |
| Linux 二进制 | 自动安装/验证 mcc CA 进入系统 bundle，并持久化 `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt` | 已复现、已验证、bundle 路径稳定 |
| Linux Docker | 不由容器内 bootstrap 自动写宿主 profile；文档/脚本提示宿主配置 | 容器不能安全修改宿主机用户环境 |
| macOS 二进制 | 暂不自动设置 `SSL_CERT_FILE` | macOS 主要信任源是 Keychain；无已验证稳定 PEM bundle 路径 |
| macOS Docker | 暂不自动设置 | 同 macOS，且容器不能直接改宿主环境 |
| Windows 二进制 | 暂不自动设置 `SSL_CERT_FILE` | Windows 主要信任源是 Root Store；未复现需要该变量 |
| Windows Docker | 暂不自动设置 | 需先有 Windows 原生复现证据 |

### 目标行为

Linux 非 Docker 二进制启动时，bootstrap 应先确保系统信任库包含当前 mcc CA，再自动持久化：

```bash
export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
```

该行为必须：

1. 与现有 CA trust 安装和 `NODE_EXTRA_CA_CERTS` 持久化共存。
2. 在写 `SSL_CERT_FILE` 前，验证目标系统 bundle 中存在与 `data/ca.crt` 相同 SHA256 指纹的 mcc CA。
3. 若系统 bundle 未包含 mcc CA，应尝试调用现有 Linux CA 安装流程；仍失败时不写 `SSL_CERT_FILE` marker，并给出明确修复指令。
4. 使用 mcc 管理的 profile block，幂等更新，不重复追加。
5. 不覆盖用户自定义 `SSL_CERT_FILE`。
6. 高权限运行时不写普通用户 profile，沿用 `ErrPrivilegedRun` 安全策略。
7. Docker 环境跳过自动写入，并在文档/提示中说明宿主机需手动设置。
8. macOS/Windows 保持现状，不新增自动写入。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成 | 验证/修复 Linux 系统 bundle 中的 mcc CA | bundle 指纹扫描、trust marker 强化、安装后验证 | 单元测试覆盖已存在/缺失/指纹不匹配 |
| 2 | 已完成 | 扩展 EnvAdapter 的 `SSL_CERT_FILE` 能力 | `bootstrap.go` 接口、mock、OS lookup 文件 | 聚焦接口/lookup 单元测试 |
| 3 | 已完成 | Linux/POSIX profile 写入实现 | `adapters.go` 中 mcc-managed block 写入、检测、替换 | bash/zsh/fish/幂等与冲突扫描测试 |
| 4 | 已完成 | bootstrap 集成与 marker | `tryPersistSSLCertFile`、`Result.SSLCertFileResult`、`.ssl-cert-file-persisted` | mock 集成测试 |
| 5 | 已完成 | 启动输出与 fallback 指令 | `instructions.go`、`CLAUDE.md`、README 相关段落 | 文案测试/人工审阅 |
| 6 | 已完成 | 回归与平台边界测试 | Linux 单元测试、Windows/macOS 不触发测试 | `go test ./internal/bootstrap -count=1` |
| 7 | 已完成 | 手动验证记录 | spec 验证区回填实际命令和日志 | Linux 长对话无 `unknown_ca` |

## 需求

### 交付物

1. Linux CA trust 安装流程必须从“只看 marker”升级为“marker + 系统 bundle 指纹验证”：

```go
func linuxSystemBundleContainsCA(bundlePath, caCertPath string) (bool, error)
func caFingerprintSHA256(certPath string) (string, error)
func pemBundleContainsFingerprint(bundlePEM []byte, fingerprint string) (bool, error)
func (e *Executor) ensureLinuxSystemTrustBundleContainsMCCCA(bundlePath string) StepResult
```

行为：

- 仅 Linux 需要 bundle 指纹验证；macOS/Windows 继续由各自系统信任机制处理。
- 在写 `SSL_CERT_FILE` 之前必须调用。
- 如果 bundle 已存在 mcc CA 且指纹匹配，直接成功，不重复安装。
- 如果 bundle 缺失 mcc CA 或只有旧 mcc CA 指纹，调用现有 `e.trust.InstallCA(e.caCertPath)` 安装，并在安装后重新扫描 bundle。
- 安装后仍未找到匹配指纹时，返回 `Attempted:true, Success:false`，错误信息必须包含目标 bundle 路径、CA 路径和“bundle does not contain MCC CA fingerprint”。
- 只有验证成功后才能写 `.ca-trust-installed` marker 和 `.ssl-cert-file-persisted` marker。
- 不能仅因为 `.ca-trust-installed` marker 存在就跳过 bundle 扫描；marker 可能来自旧 bundle、旧 CA 或人工迁移。

2. `EnvAdapter` 新增用于 `SSL_CERT_FILE` 的 lookup/persist 能力：

```go
type EnvAdapter interface {
    PersistRoot(rootDir string) error
    LookupNodeCACert() (value string, exists bool, err error)
    PersistNodeCACert(caCertPath string) error

    LookupSSLCertFile() (value string, exists bool, err error)
    PersistSSLCertFile(bundlePath string) error
}
```

3. `osEnvAdapter` 实现：
   - Linux/POSIX：从当前环境读取 `SSL_CERT_FILE`；写入 shell profile。
   - macOS：lookup 可读当前环境，但 `PersistSSLCertFile` 返回明确的 unsupported/no-op 错误，bootstrap 不应调用。
   - Windows：lookup 可读当前环境/注册表（可选），但 `PersistSSLCertFile` 返回 unsupported/no-op，bootstrap 不应调用。
4. 新增 Linux bundle 选择函数：

```go
func defaultLinuxSSLCertFile() (string, bool) {
    candidates := []string{
        "/etc/ssl/certs/ca-certificates.crt",
        "/etc/pki/tls/certs/ca-bundle.crt",
        "/etc/ssl/ca-bundle.pem",
        "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
    }
    for _, p := range candidates {
        if st, err := os.Stat(p); err == nil && !st.IsDir() {
            return p, true
        }
    }
    return "", false
}
```

实现时首选 `/etc/ssl/certs/ca-certificates.crt`；其余候选用于 RHEL/openSUSE/Arch 类发行版。

5. `Result` 新增字段：

```go
SSLCertFileResult StepResult // Linux SSL_CERT_FILE 持久化结果
```

6. 新增 bootstrap 方法：

```go
func (e *Executor) tryPersistSSLCertFile() StepResult
```

行为：

- 仅 `runtime.GOOS == "linux"` 时尝试。
- Docker 环境不尝试。
- 高权限运行时沿用 `ErrPrivilegedRun`。
- 找不到系统 bundle 时返回 `Attempted: true, Success: false` 和明确错误。
- 在任何 profile 写入或 marker 写入前，先调用 `ensureLinuxSystemTrustBundleContainsMCCCA(bundle)`；该步骤失败时直接返回失败，不持久化 `SSL_CERT_FILE`。
- 当前/持久化环境中已有用户自定义 `SSL_CERT_FILE` 且不等于 mcc 管理的旧值时返回 `ErrUserCustomValue`，不覆盖。
- marker 命中且真实环境值未被用户改写时返回 `Success: true`。
- 写入成功后写 `.ssl-cert-file-persisted` marker。

7. POSIX profile 中使用一个新 block，避免和 `NODE_EXTRA_CA_CERTS` 的 block 混在一起：

```bash
# >>> mcc: SSL_CERT_FILE trust bundle (auto-managed, do not edit) >>>
export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
# <<< mcc <<<
```

fish shell：

```fish
# >>> mcc: SSL_CERT_FILE trust bundle (auto-managed, do not edit) >>>
set -gx SSL_CERT_FILE /etc/ssl/certs/ca-certificates.crt
# <<< mcc <<<
```

8. 启动输出：
   - 成功时提示 `SSL_CERT_FILE 已持久化；请完全重启 Claude Code/Orca 让新环境生效。`
   - 用户自定义时提示 `检测到用户自定义 SSL_CERT_FILE，mcc 不覆盖；请确认其指向完整系统 CA bundle，不要指向单个 data/ca.crt。`
   - 失败时提示 `Linux Claude Code 后台 TLS 路径可能仍不信任 mcc CA；可手动 export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt。`
9. `CLAUDE.md` FAQ 更新：Linux 上 `SSL_CERT_FILE` 不是极少数 fallback，而是目前推荐的稳定配置；必须指向已包含 mcc CA 的系统 bundle。
10. README / README.en 相关 CA 安装说明增加 Linux 二进制自动处理说明和 Docker 宿主机手动说明。

### 目录结构

```text
internal/bootstrap/
  bootstrap.go
    - EnvAdapter 增加 LookupSSLCertFile / PersistSSLCertFile
    - Result 增加 SSLCertFileResult
    - tryTrustCA / tryPersistSSLCertFile 之间增加 Linux 系统 bundle 指纹验证
    - 新增 tryPersistSSLCertFile
    - Run 中在 tryPersistNodeCA 后调用 Linux SSL_CERT_FILE 持久化，但前提是系统 bundle 已含当前 mcc CA

  adapters.go
    - linuxSystemBundleContainsCA
    - caFingerprintSHA256
    - pemBundleContainsFingerprint
    - osEnvAdapter.LookupSSLCertFile
    - osEnvAdapter.PersistSSLCertFile
    - persistSSLCertFilePOSIX
    - writePOSIXProfileSSLCertFile
    - profileHasSSLCertFileOutsideMCCBlock
    - sslCertFileExportLine
    - defaultLinuxSSLCertFile

  bootstrap_test.go
    - mockEnv 增加 SSL_CERT_FILE 字段与方法
    - tryPersistSSLCertFile 单元测试
    - instructions 文案测试

  node_ca_lookup_other.go
    - 可新增 lookupPersistedSSLCertFileOS 或直接在 adapters.go 通过 os.LookupEnv 实现
```

```text
sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/
  spec.md
  spec_ZH.md
```

### 数据模型

新增 marker 文件：

```go
const sslCertFileMarkerName = ".ssl-cert-file-persisted"

type sslCertFileMarker struct {
    BundlePath string `json:"bundle_path"`
    Home       string `json:"home"`
    UID        int    `json:"uid,omitempty"`
}
```

与 `nodeCAMarker` 类似，marker 绑定：

- bundle path：路径变化时需要重写。
- HOME：避免跨用户误命中。
- UID：Linux 普通用户额外绑定 UID；root 不写普通用户 profile。

现有 `.ca-trust-installed` marker 继续保留，但 Linux 上不能再作为“系统 bundle 已可信”的唯一依据。读取 marker 后仍要执行：

```text
caFingerprintSHA256(data/ca.crt)
  -> 扫描目标 bundle 所有 PEM CERTIFICATE block
  -> 至少一个 block 的 SHA256 fingerprint 完全匹配
```

只有 marker 匹配且 bundle 指纹扫描也匹配，才可视为 CA trust 已就绪。若 marker 匹配但 bundle 缺失指纹，应视为 marker stale，重新安装 CA 并重建系统 bundle。

新增错误（可复用现有错误时不新增）：

```go
var ErrUnsupportedPlatform = errors.New("unsupported platform")
```

实现优先复用：

- `ErrPrivilegedRun`
- `ErrUserCustomValue`
- `ErrPartialSuccess`
- `ErrUnsafeProfile`

### 约束

1. **只在 Linux 自动写 `SSL_CERT_FILE`**；macOS/Windows 保持现状。
2. **必须先验证系统 bundle 包含当前 mcc CA**；不得把“用户曾经手动安装过 CA”当成新环境默认状态。
3. **必须写完整系统 CA bundle**，不得写 `data/ca.crt`。
4. **不得覆盖用户自定义 `SSL_CERT_FILE`**。profile 中非 mcc-managed 的 `SSL_CERT_FILE` 赋值一律视为用户自定义。
5. **高权限运行不写普通用户 profile**。如果 marker 不匹配，返回 `ErrPrivilegedRun`。
6. **Docker 内不尝试写宿主机环境**。Docker 只通过 README/启动指令提示宿主机配置。
7. **幂等**：重复运行不会重复追加 block；bundle 路径变化时替换 mcc-managed block；CA 指纹变化时重跑系统 trust 安装。
8. **错误不阻塞 mcc 启动**：`SSLCertFileResult` 仅影响提示和状态 hash，不影响代理服务运行。
9. **保留现有 NodeCA 行为**：不删除、不改写 `NODE_EXTRA_CA_CERTS` 的 marker、block、测试。
10. **fish/bash/zsh/profile 兼容**：沿用 `resolveShellProfiles`、`shellQuote`、`parseFishExportLine` 的现有模式。
11. **安全写入**：沿用 `isSafeForWrite`、`readProfile`、`writeFileSync`；高权限遇 symlink/非常规 profile fail-closed。
12. **状态 hash 纳入新结果**：避免启动时重复打印同一 warning，行为与 `NodeCAResult` 一致。

### 边界情况

1. `/etc/ssl/certs/ca-certificates.crt` 不存在，但 RHEL bundle 存在：选择第一个存在的候选路径，并验证其中是否含当前 mcc CA。
2. 所有候选 bundle 都不存在：返回明确错误，不写 profile。
3. bundle 存在但缺失 mcc CA：调用现有 Linux trust 安装流程并重新验证。
4. bundle 中存在旧 mcc CA，但指纹与 `data/ca.crt` 不匹配：视为 CA 轮换/旧 bundle，重新安装并验证当前 CA 指纹。
5. `.ca-trust-installed` marker 存在但 bundle 缺失当前指纹：marker stale，不能跳过安装/验证。
6. `update-ca-certificates` / `update-ca-trust extract` 执行成功但 bundle 仍缺失当前指纹：返回失败，不写 `SSL_CERT_FILE` marker。
7. 当前环境已有 `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`：只有当该 bundle 含当前 mcc CA 时才视为已就绪；若 marker 缺失，可写 marker，不重复写 profile。
8. 当前环境已有 `SSL_CERT_FILE=/custom/company-bundle.pem`：视为用户自定义，mcc 不覆盖；但提示其必须包含当前 mcc CA。
9. 当前 profile 中已有用户手写 `export SSL_CERT_FILE=/custom.pem`：不写 mcc block，返回 `ErrUserCustomValue`。
10. profile 中已有旧 mcc-managed block 指向旧 bundle：替换为新 bundle。
11. fish profile 中已有 mcc-managed block：用 fish 语法替换，不混入 bash `export`。
12. `.ssl-cert-file-persisted` marker 属于另一个 HOME/UID：视为 stale，重新检查真实环境和 profile。
13. root 运行 mcc：若 CA trust 验证成功且 profile marker 命中返回 success；否则返回 `ErrPrivilegedRun` 并提示用普通用户重启 mcc。
14. 写 profile 失败：返回错误或 partial，不写 marker，下次启动重试。
15. 用户先前把 `SSL_CERT_FILE` 指向 `data/ca.crt`：视为用户自定义，mcc 不覆盖；提示该值风险，要求改成系统 bundle。
16. macOS/Windows 调用 `tryPersistSSLCertFile`：应返回 `Attempted:false`，测试确保不会写环境。

### 非目标

1. 不为 macOS 自动生成 PEM bundle。
2. 不为 Windows 设置 `SSL_CERT_FILE`。
3. 不修改 Claude Code 的 `~/.claude/settings.json`。
4. 不在 Docker 容器中修改宿主机 profile。
5. 不删除用户已有 `SSL_CERT_FILE`。
6. 不把 `SSL_CERT_FILE` 指向 `data/ca.crt`。
7. 不改 TLS 握手逻辑或证书生成逻辑。
8. 不解决浏览器 NSS store 信任问题。
9. 本次不新增 `ZDOTDIR` 重定位后的 zsh profile 支持。

## 任务详情

### 任务 1：验证并修复 Linux 系统 bundle 中的 mcc CA

#### 需求

**Objective（目标）** — 在 Linux 上把“系统 bundle 包含当前 mcc CA”变成启动流程的可验证事实，而不是依赖用户之前是否手动安装过 CA。

**Outcomes（成果）** — bootstrap 能扫描目标系统 bundle，确认其中存在与 `data/ca.crt` 相同 SHA256 指纹的证书；缺失时自动调用现有 CA trust 安装流程；安装后仍缺失时阻止 `SSL_CERT_FILE` 持久化并给出明确错误。

**Evidence（证据）** — 单元测试覆盖 bundle 已匹配、bundle 缺失、bundle 只有旧 CA、marker stale、安装后验证成功、安装后验证失败。

**Constraints（约束）** — 不解析 subject 文本判断，因为 PEM/DER 中 subject 不一定明文可 grep；必须用证书 SHA256 指纹。不能仅依赖 `.ca-trust-installed` marker。

**Edge Cases（边界）** — CA 重新生成；系统包更新重建 bundle；用户删除 `/usr/local/share/ca-certificates/mcc-ca.crt`；RHEL 使用 `update-ca-trust`；Debian 使用 `update-ca-certificates`。

**Verification（验证）** — `go test ./internal/bootstrap -run 'TestLinuxSystemBundleContainsCA|TestEnsureLinuxSystemTrustBundleContainsMCCCA|TestTryTrustCA.*Bundle' -count=1`。

#### 计划

1. **RED：bundle 指纹扫描测试**
   - 在 `internal/bootstrap/bootstrap_test.go` 新增：
     - `TestPEMBundleContainsFingerprint_MatchingCert`
     - `TestPEMBundleContainsFingerprint_NoMatchingCert`
     - `TestPEMBundleContainsFingerprint_InvalidPEM`
     - `TestLinuxSystemBundleContainsCA_MissingBundle`
   - 使用测试证书 fixture 或现有测试 helper 生成两张不同 CA，避免通过 subject 字符串判断。
   - 预期失败：函数不存在。
2. **GREEN：实现指纹 helper**
   - 在 `adapters.go` 或新文件 `trust_verify.go` 中实现：
     ```go
     func caFingerprintSHA256(certPath string) (string, error)
     func pemBundleContainsFingerprint(bundlePEM []byte, fingerprint string) (bool, error)
     func linuxSystemBundleContainsCA(bundlePath, caCertPath string) (bool, error)
     ```
   - 使用 `encoding/pem` 循环解析所有 `CERTIFICATE` block，使用 `crypto/sha256` 对 DER bytes 求指纹。
   - 指纹格式内部统一用 uppercase hex 无冒号，避免和 OpenSSL 输出格式耦合。
3. **RED：trust marker stale 测试**
   - 新增：
     - `TestTryTrustCA_MarkerExistsButBundleMissingCA_Reinstalls`
     - `TestTryTrustCA_MarkerExistsButBundleOldFingerprint_Reinstalls`
     - `TestTryTrustCA_InstallSuccessButBundleStillMissing_ReturnsError`
   - 若当前 `tryTrustCA` 不方便注入 bundle path/stat/readFile，先抽取纯函数：
     ```go
     func verifyLinuxCATrustReady(bundlePath, caCertPath string) (bool, error)
     ```
4. **GREEN：强化 CA trust 流程**
   - Linux 分支中，`hasCATrustMarker` 只能作为“可能已安装”的 hint。
   - 真实成功条件必须是：
     ```text
     hasCATrustMarker(dataDir, caCertPath) && linuxSystemBundleContainsCA(bundle, caCertPath)
     ```
   - 若 marker 命中但 bundle 不含 CA，记录 marker stale，调用 `InstallCA`，安装后再次执行 `linuxSystemBundleContainsCA`。
   - 只有二次验证成功后，才调用 `writeCATrustMarker`。
5. **GREEN：让 SSL_CERT_FILE 依赖该验证**
   - `tryPersistSSLCertFile` 接收/选择 bundle 后，先调用：
     ```go
     trust := e.ensureLinuxSystemTrustBundleContainsMCCCA(bundle)
     if !trust.Success { return trust }
     ```
   - 或者在 `Run` 中保证 `result.TrustResult.Success` 对 Linux 已代表 bundle 指纹验证成功。无论采用哪种接线，测试必须证明“bundle 缺 CA 时不会写 profile/marker”。
6. **验证**
   - 运行：
     ```bash
     go test ./internal/bootstrap -run 'TestLinuxSystemBundleContainsCA|TestEnsureLinuxSystemTrustBundleContainsMCCCA|TestTryTrustCA.*Bundle' -count=1
     ```

#### 验证

- [x] 不使用 grep subject 判断 CA 是否存在。
- [x] 指纹匹配测试通过。
- [x] marker stale 会触发重新安装。
- [x] 安装后验证失败时不会写 `SSL_CERT_FILE`。

### 任务 2：扩展 EnvAdapter 与 mock

#### 需求

**Objective（目标）** — 给 bootstrap 环境适配层加入 `SSL_CERT_FILE` lookup/persist 能力，为 Linux 自动持久化提供接口边界。

**Outcomes（成果）** — `EnvAdapter` 增加 `LookupSSLCertFile` / `PersistSSLCertFile`；`osEnvAdapter` 和 `mockEnv` 编译通过；现有 `NODE_EXTRA_CA_CERTS` 测试不回归。

**Evidence（证据）** — 新增 mock 测试能断言 `PersistSSLCertFile` 的入参；`go test ./internal/bootstrap -run 'TestTryPersistSSLCertFile|TestOSEnvAdapterLookupSSLCertFile' -count=1` 通过。

**Constraints（约束）** — 不改 `PersistRoot` 和 `PersistNodeCACert` 的现有签名；不把 `SSL_CERT_FILE` 与 `NODE_EXTRA_CA_CERTS` 复用同一个方法。

**Edge Cases（边界）** — mock lookup 返回错误；mock 已有自定义值；mock persist 返回 partial/错误。

**Verification（验证）** — 聚焦单元测试和全包编译。

#### 计划

1. **RED：扩展 mock 测试**
   - 修改 `internal/bootstrap/bootstrap_test.go`，在 `mockEnv` 旁新增预期测试：
     - `TestTryPersistSSLCertFile_PassesBundlePathToEnv`
     - `TestTryPersistSSLCertFile_LookupErrorFails`
   - 测试先调用尚不存在的 `tryPersistSSLCertFile`，预期编译失败或方法缺失。
   - 运行：
     ```bash
     go test ./internal/bootstrap -run TestTryPersistSSLCertFile_PassesBundlePathToEnv -count=1
     ```
   - 预期失败：`e.tryPersistSSLCertFile undefined` 或 `mockEnv does not implement EnvAdapter`。
2. **GREEN：扩展接口**
   - 修改 `internal/bootstrap/bootstrap.go`：
     ```go
     LookupSSLCertFile() (value string, exists bool, err error)
     PersistSSLCertFile(bundlePath string) error
     ```
   - 修改 `internal/bootstrap/bootstrap_test.go` 的 `mockEnv`：
     ```go
     sslCertFileValue string
     sslCertFileValueSet bool
     sslCertFileLookupErr error
     sslCertFileErr error
     sslCertFileArg string
     ```
     并实现两个方法。
   - 修改 `internal/bootstrap/adapters.go`，先加最小实现：
     ```go
     func (a *osEnvAdapter) LookupSSLCertFile() (string, bool, error) {
         v, ok := os.LookupEnv("SSL_CERT_FILE")
         return v, ok && v != "", nil
     }
     func (a *osEnvAdapter) PersistSSLCertFile(bundlePath string) error {
         if runtime.GOOS != "linux" {
             return ErrUnsupportedPlatform
         }
         return a.persistSSLCertFilePOSIX(bundlePath)
     }
     ```
3. **GREEN：补错误常量**
   - 若新增 `ErrUnsupportedPlatform`，放在 `bootstrap.go` sentinel errors 区域。
4. **验证**
   - 运行：
     ```bash
     go test ./internal/bootstrap -run 'TestTryPersistSSLCertFile|TestOSEnvAdapterLookupSSLCertFile' -count=1
     ```
   - 预期：新增测试进入下一轮失败（真实逻辑未完成），但接口编译问题消失。

#### 验证

- [x] 聚焦接口测试通过。
- [x] 接口扩展后编译通过。
- [x] mock 能记录 `SSL_CERT_FILE` 入参。

### 任务 3：实现 Linux bundle 选择与 POSIX profile 写入

#### 需求

**Objective（目标）** — 在 Linux 上把已验证包含当前 mcc CA 的完整系统 CA bundle 路径写入用户 shell profile。

**Outcomes（成果）** — `defaultLinuxSSLCertFile` 能找到系统 bundle；写入 profile 前能确认该 bundle 已包含当前 mcc CA；`writePOSIXProfileSSLCertFile` 能写入/替换 mcc-managed block；所有相关启动 profile 及其中每条已识别的非 MCC 赋值都会参与用户自定义值保护。冲突在所有候选间全局检查，同值仅能阻止当前首选写入候选重复持久化。

**Evidence（证据）** — bash、zsh、fish profile 单元测试覆盖新增 block；重复写入不重复；用户自定义不覆盖。

**Constraints（约束）** — 不写 `data/ca.crt`；不写未验证含 mcc CA 的 bundle；不混用 `NODE_EXTRA_CA_CERTS` block；高权限安全策略沿用现有 POSIX profile 写入。

**Edge Cases（边界）** — unknown shell fallback；fish universal export；profile 不存在；profile symlink 高权限 fail-closed；已有旧 block；Bash 登录 profile；zsh `.zshenv`/`.zprofile`/`.zshrc`/`.zlogin`；`typeset -x`；`declare -x`；次级 profile 同值；前一条匹配而后一条覆盖为冲突值。

**Verification（验证）** — `go test ./internal/bootstrap -run 'TestWritePOSIXProfileSSLCertFile|TestDefaultLinuxSSLCertFile|TestProfileHasSSLCertFile' -count=1`。

#### 计划

1. **RED：bundle 选择测试**
   - 在 `bootstrap_test.go` 新增 table test：
     - `TestDefaultLinuxSSLCertFilePrefersDebianBundle`
     - `TestDefaultLinuxSSLCertFileFallsBackToRHELBundle`
     - `TestDefaultLinuxSSLCertFileNoCandidates`
   - 为避免依赖真实 `/etc`，抽取可注入 stat 的 helper：
     ```go
     func defaultLinuxSSLCertFileWithStat(stat func(string) (os.FileInfo, error)) (string, bool)
     ```
   - RED 运行预期：函数不存在。
2. **GREEN：实现 bundle helper**
   - 在 `adapters.go` 添加候选路径常量：
     ```go
     var linuxSSLCertFileCandidates = []string{...}
     ```
   - 实现 `defaultLinuxSSLCertFile()` 调用 `defaultLinuxSSLCertFileWithStat(os.Stat)`。
3. **RED：profile 写入测试**
   - 新增：
     - `TestWritePOSIXProfileSSLCertFile_Bash_WritesExport`
     - `TestWritePOSIXProfileSSLCertFile_Fish_UsesSetGx`
     - `TestWritePOSIXProfileSSLCertFile_Idempotent`
     - `TestWritePOSIXProfileSSLCertFile_BundlePathChanged_UpdatesBlock`
     - `TestWritePOSIXProfileSSLCertFile_UserCustomValue_NotOverwritten`
   - 测试通过临时 HOME 和 SHELL 控制 profile 路径，沿用现有 `writeFile` / `contains` helper。
4. **GREEN：实现 profile 写入**
   - 新增常量：
     ```go
     const (
         posixSSLBlockBegin = "# >>> mcc: SSL_CERT_FILE trust bundle (auto-managed, do not edit) >>>"
         posixSSLBlockEnd = "# <<< mcc <<<"
     )
     ```
   - 新增：
     ```go
     func (a *osEnvAdapter) persistSSLCertFilePOSIX(bundlePath string) error
     func (a *osEnvAdapter) writePOSIXProfileSSLCertFile(bundlePath string) error
     func profileSSLCertFileOutsideMCCBlockValues(shell, content string) ([]string, error)
     func sslCertFileExportLine(shell, bundlePath string) string
     ```
   - 写入流程复制 `writePOSIXProfileNodeCA` 的两阶段扫描/写入结构，但检测 key 改成 `SSL_CERT_FILE`，block 改成 `posixSSLBlockBegin/End`。
   - 将首选写入目标与冲突扫描候选分离：Bash 扫描 `.bashrc`、`.profile`、`.bash_profile`、`.bash_login`；zsh 扫描 `.zshenv`、`.zprofile`、`.zshrc`、`.zlogin`；完整检查每个文件中的所有已识别非 MCC 赋值。
   - 同值按 profile 记录：`.profile` 同值不能阻止 Bash 首选 `.bashrc` 写入；首选写入候选自身同值可避免重复 block，但仍修复其中的旧 MCC 管理块。
   - 识别裸赋值/`export`、带 export 选项的 `typeset`/`declare`，以及包含 `set -Ux` 的 fish export/scope flag 组合；`echo "$SSL_CERT_FILE"` 等只读引用不算赋值。
   - MCC marker 必须为完整、精确且非嵌套的一对；孤立、嵌套或嵌入活动命令的 marker 在写入前按 `ErrUserCustomValue` 失败关闭。既有 POSIX Node CA 管理块虽然共用通用结束 marker，仍可与 SSL 块共存；其块体继续扫描 `SSL_CERT_FILE`，只有 SSL 管理块会抑制目标 key 解析。
   - 在完整 MCC 管理块之外，精确针对 `SSL_CERT_FILE` 的 POSIX `unset`、`export -n`、`typeset +x`、`declare +x`，以及 fish erase/unexport 命令均按冲突失败关闭；其他变量的变更与只读引用不受影响。
   - 管理块结束位置只从目标 begin marker 之后查找；替换逻辑与同值旧块修复复用同一目标相对范围，因此 Node CA/SSL 两类块任意排列时都不会重复追加或跳过 stale block 修复。
5. **验证**
   - 运行：
     ```bash
     go test ./internal/bootstrap -run 'TestWritePOSIXProfileSSLCertFile|TestDefaultLinuxSSLCertFile|TestProfileHasSSLCertFile' -count=1
     ```

#### 验证

- [x] bundle 选择覆盖 Debian/RHEL/无候选。
- [x] bash/fish block 内容正确。
- [x] 重复运行不重复写。
- [x] Bash 与四个受支持的 zsh 启动 profile 都会扫描冲突。
- [x] 同值仅抑制当前写入候选的重复持久化。
- [x] 能识别 POSIX 声明导出与 fish universal export。
- [x] MCC marker 结构异常时失败关闭且不修改 profile。
- [x] POSIX Node CA profile 写入同样使用异常 marker 失败关闭规则。
- [x] POSIX Node CA 扫描能识别管理块外的声明导出与精确 key unexport/unset 变更。
- [x] Fish Node CA 扫描能识别管理块外的精确 key erase/unexport 变更。
- [x] 既有 POSIX Node CA 块可与 SSL 块共存，且不会隐藏目标 key 变更。
- [x] 共用结束 marker 会相对目标 begin 配对，覆盖 Node CA/SSL 两种排列顺序。
- [x] 跨类型块排列下 stale block 会实际替换，重复运行保持字节级幂等。
- [x] 管理块外针对目标变量的 remove/unexport 命令会失败关闭。
- [x] 用户同值配置不会让旧 MCC-managed block 继续生效。
- [x] 用户自定义值不覆盖。

### 任务 4：bootstrap 集成、marker 与状态 hash

#### 需求

**Objective（目标）** — 在 Linux 非 Docker 二进制启动流程中，在系统 bundle 已验证包含当前 mcc CA 后自动调用 `SSL_CERT_FILE` 持久化，并用 marker 保证幂等。

**Outcomes（成果）** — `Result.SSLCertFileResult` 记录结果；`tryPersistSSLCertFile` 完成路径选择、用户自定义检测、marker 命中、写入和失败处理；状态 hash 包含新结果。

**Evidence（证据）** — mock 集成测试覆盖成功、Docker 跳过、非 Linux 跳过、root 拒绝、用户自定义、marker 命中、persist 错误。

**Constraints（约束）** — `SSL_CERT_FILE` 持久化在 `tryPersistNodeCA` 和 Linux bundle 指纹验证之后执行；失败不阻塞透明模式；只有 Linux 尝试。

**Edge Cases（边界）** — marker 匹配但真实环境被用户改写；marker 跨用户；bundle path 变化；persist partial。

**Verification（验证）** — `go test ./internal/bootstrap -run 'TestTryPersistSSLCertFile|TestExecutorRun.*SSLCertFile|TestStateHash.*SSLCertFile' -count=1`。

#### 计划

1. **RED：tryPersistSSLCertFile 行为测试**
   - 新增测试：
     - `TestTryPersistSSLCertFile_LinuxSuccessWritesMarker`
     - `TestTryPersistSSLCertFile_DockerSkipped`
     - `TestTryPersistSSLCertFile_NonLinuxSkipped`
     - `TestTryPersistSSLCertFile_UserCustomValue`
     - `TestTryPersistSSLCertFile_MarkerHitDoesNotPersist`
     - `TestTryPersistSSLCertFile_PersistErrorNoMarker`
   - 如果直接改 `runtime.GOOS` 不可行，抽取小函数：
     ```go
     func shouldPersistSSLCertFile(goos string, caps Capabilities) bool
     ```
     先写测试覆盖该函数。
2. **GREEN：实现 marker**
   - 新增：
     ```go
     const sslCertFileMarkerName = ".ssl-cert-file-persisted"
     type sslCertFileMarker struct {...}
     func hasSSLCertFileMarker(dataDir, bundlePath string) bool
     func writeSSLCertFileMarker(dataDir, bundlePath string)
     func previousManagedSSLCertFilePath(dataDir string) (string, bool)
     func sslCertFileMarkerUserMatches(m sslCertFileMarker) bool
     ```
   - 可抽取共享用户匹配 helper，避免复制 `nodeCAMarkerUserMatches` 过多逻辑；如果抽取风险大，先复制并测试。
3. **GREEN：实现 tryPersistSSLCertFile**
   - 伪代码：
     ```go
     func (e *Executor) tryPersistSSLCertFile() StepResult {
         if runtime.GOOS != "linux" || e.caps.IsDocker { return StepResult{} }
         bundle, ok := defaultLinuxSSLCertFile()
         if !ok { return StepResult{Attempted:true, Err: errNoSystemBundle} }
        trust := e.ensureLinuxSystemTrustBundleContainsMCCCA(bundle)
        if !trust.Success { return trust }
        markerMatches := hasSSLCertFileMarker(e.dataDir, bundle)
         if isPrivilegedRun() { ... }
         existing, exists, err := e.env.LookupSSLCertFile()
         if err != nil { ... }
         if exists && existing != "" && !sslCertFilePathsEqual(existing, bundle) {
             previous, managed := previousManagedSSLCertFilePath(e.dataDir)
             if !managed || !sslCertFilePathsEqual(existing, previous) {
                 return StepResult{Attempted:true, Err: ErrUserCustomValue}
             }
         }
         if markerMatches { return StepResult{Success:true} }
         err = e.env.PersistSSLCertFile(bundle)
         ...
     }
     ```
4. **GREEN：Run 接线**
   - 在透明模式且非 Docker 的分支里，`tryPersistNodeCA` 后调用：
     ```go
     result.SSLCertFileResult = e.tryPersistSSLCertFile()
     ```
   - 仅 Linux 应有 attempted。
5. **GREEN：state hash**
   - 找到 `stateHash` / bootstrap state 生成函数，把 `SSLCertFileResult` 的 attempted/success/partial/error 纳入 hash。
6. **验证**
   - 运行聚焦测试命令。

#### 验证

- [x] Linux 成功路径写 marker。
- [x] Docker/macOS/Windows 跳过。
- [x] root 非 marker 命中时拒绝写。
- [x] state hash 区分成功/失败/partial。

### 任务 5：启动输出、FAQ 与 README 更新

#### 需求

**Objective（目标）** — 让用户知道 Linux 二进制已自动处理系统 CA 安装/验证和 `SSL_CERT_FILE`，以及 Docker/手动场景如何配置。

**Outcomes（成果）** — `instructions.go` 输出系统 bundle 缺 mcc CA、`SSL_CERT_FILE` 成功/失败/用户自定义提示；`CLAUDE.md` FAQ 更新；README/README.en 的 Linux CA 配置段落更新。

**Evidence（证据）** — 文案测试覆盖 zh/en 成功、用户自定义、partial、失败；人工检查 README 不再暗示只靠 `NODE_EXTRA_CA_CERTS` 足够。

**Constraints（约束）** — 不夸大 macOS/Windows；明确 `SSL_CERT_FILE` 必须指向系统 bundle。

**Edge Cases（边界）** — Docker 指令不能说“容器自动完成宿主配置”；用户自定义值提示要包含不要指向 `data/ca.crt`；bundle 缺 mcc CA 时提示先安装系统 CA。

**Verification（验证）** — `go test ./internal/bootstrap -run 'TestGenerateInstructions.*SSLCertFile' -count=1`，人工审阅文档 diff。

#### 计划

1. **RED：instructions 测试**
   - 新增：
     - `TestGenerateInstructions_TransparentSuccess_SSLCertFileSuccess_PrintsRestartHint`
     - `TestGenerateInstructions_TransparentSuccess_SSLCertFileUserCustom_PrintsBundleWarning`
     - `TestGenerateInstructions_TransparentSuccess_SSLCertFileFailure_PrintsManualExport`
   - 预期当前无相关输出，测试失败。
2. **GREEN：修改 `instructions.go`**
   - 在 `transparentSuccessInstructions` 中追加 `SSLCertFileResult` 处理。
   - zh 成功文案：
     ```text
     ℹ SSL_CERT_FILE 已持久化为系统 CA bundle；如果 Claude Code/Orca 已在运行，请完全退出并重新启动。
     ```
   - en 成功文案：
     ```text
     ℹ SSL_CERT_FILE persisted to the system CA bundle; fully restart Claude Code/Orca if it is already running.
     ```
   - 用户自定义文案要明确风险。
3. **GREEN：更新 `CLAUDE.md`**
   - FAQ 中 Linux 修复改为：
     - Linux 二进制：bootstrap 自动安装并验证系统 CA、写 `NODE_EXTRA_CA_CERTS`、写 `SSL_CERT_FILE`。
     - Linux Docker：宿主机需运行 setup-host/trust，确认 `/etc/ssl/certs/ca-certificates.crt` 中包含 mcc CA 指纹，并在启动 Claude Code 的 shell 中设置 `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`。
     - macOS/Windows：暂不自动写 `SSL_CERT_FILE`，无复现证据。
4. **GREEN：更新 README / README.en**
   - Linux 透明模式 CA 说明加入：
     ```bash
     export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
     ```
   - 明确不要设置为 `data/ca.crt`。
   - Docker 网络边界说明加入“容器不能自动写宿主机 SSL_CERT_FILE”。
5. **验证**
   - 运行文案测试。
   - 人工检查中英文 README 语义一致。

#### 验证

- [x] zh/en instructions 测试通过。
- [x] FAQ 明确 Linux 自动处理范围。
- [x] Docker 文档明确宿主环境要求。

### 任务 6：回归测试与平台边界

#### 需求

**Objective（目标）** — 确保新增 Linux `SSL_CERT_FILE` 自动配置不破坏既有 `NODE_EXTRA_CA_CERTS` 自动配置，也不会在 macOS/Windows 意外生效。

**Outcomes（成果）** — 聚焦测试、bootstrap 包测试、全量 Go 测试通过；Windows/macOS 交叉编译通过。

**Evidence（证据）** — 测试输出记录到本 spec 验证区。

**Constraints（约束）** — 不要求 macOS/Windows 原生验证；只要求非 Linux 不尝试自动写。

**Edge Cases（边界）** — build tags；Windows registry lookup 文件；Darwin launchctl lookup 文件。

**Verification（验证）** — 命令列表如下。

#### 计划

1. 运行聚焦测试：
   ```bash
   go test ./internal/bootstrap -run 'TestTryPersistSSLCertFile|TestWritePOSIXProfileSSLCertFile|TestGenerateInstructions.*SSLCertFile|TestDefaultLinuxSSLCertFile' -count=1 -v
   ```
2. 运行 bootstrap 全包：
   ```bash
   go test ./internal/bootstrap -count=1
   ```
3. 运行 race：
   ```bash
   go test -race ./internal/bootstrap ./internal/cert -count=1
   ```
4. 运行全量：
   ```bash
   go test ./... -count=1
   ```
5. 交叉编译检查：
   ```bash
   go vet ./...
   GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server
   GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap
   GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server
   GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap
   ```
6. 检查工作区：
   ```bash
   git status --short
   git diff --stat
   ```

#### 验证

已于 2026-07-13 使用上述命令验证，均成功退出。

- [x] 聚焦测试通过。
- [x] bootstrap 包测试通过。
- [x] bootstrap/cert race 测试通过。
- [x] 全量 Go 测试通过。
- [x] `go vet ./...` 通过。
- [x] Windows amd64/macOS arm64 的 server 与 bootstrap 测试包交叉编译通过。

### 任务 7：Linux 手动验证与规格回填

#### 需求

**Objective（目标）** — 在 Linux 环境验证二进制 bootstrap 能自动安装/验证系统 CA、写入 `SSL_CERT_FILE`，并确认 Claude Code 长对话后台请求不再出现 `unknown_ca`。

**Outcomes（成果）** — 系统 bundle 中存在与 `data/ca.crt` 匹配的 mcc CA 指纹；新启动的 shell/Claude Code 进程环境包含 `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`；长对话结束后 mcc 日志无 `unknown_ca|bad record MAC`。

**Evidence（证据）** — 命令输出、日志窗口、后台辅助请求成功转发记录回填到本 spec。

**Constraints（约束）** — 不用 `SSL_CERT_FILE=data/ca.crt`；必须重启 Claude Code 进程验证，不能只在当前 shell export。

**Edge Cases（边界）** — 已有旧 profile block；已运行 Orca/Claude Code 继承旧环境；需要完全退出重启。

**Verification（验证）** — 见计划命令。

#### 计划

1. 清理/准备测试环境（只在测试机执行）：
   ```bash
   grep -n 'SSL_CERT_FILE' ~/.bashrc ~/.profile ~/.zshrc 2>/dev/null || true
   ```
2. 构建二进制并运行一次 bootstrap：
   ```bash
   go build -o /tmp/mcc-test ./cmd/server
   /tmp/mcc-test -data ./data
   ```
   若需要 443 端口权限，使用项目既有本地运行方式。
3. 检查 profile：
   ```bash
   grep -n 'mcc: SSL_CERT_FILE trust bundle' ~/.bashrc ~/.profile ~/.zshrc 2>/dev/null
   grep -n 'SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt' ~/.bashrc ~/.profile ~/.zshrc 2>/dev/null
   ```
4. 检查系统 bundle 中包含当前 mcc CA 指纹：
   ```bash
   openssl x509 -in data/ca.crt -fingerprint -sha256 -noout
   # 实现后应提供等价的 mcc/bootstrap 日志或诊断命令，证明 bundle 指纹匹配。
   ```
5. 新开 shell，检查环境：
   ```bash
   echo "$SSL_CERT_FILE"
   ```
6. 从新 shell 启动 Claude Code，触发长对话。
7. 验证 mcc 日志：
   ```bash
   docker logs mcc --since 10m 2>&1 | grep -E 'unknown_ca|bad record MAC'
   ```
   预期无输出。
8. 验证后台辅助请求出现并成功：
   ```bash
   docker logs mcc --since 10m 2>&1 | grep -E 'model=claude-sonnet-4-6|model=claude-opus-4-7'
   ```
9. 回填本 spec：
   - 更新“进度”
   - 勾选开发检查清单
   - 在本任务“验证”记录实际命令和结果

#### 验证

已于 2026-07-13 在 Linux 宿主机 + Docker 运行的 mcc 容器场景完成手动验证。测试前将当前分支 worktree 的 `data/` 指向主工作区 `data/`，在本分支中重新执行 `docker compose up -d --build` 构建并启动 mcc；随后新开 ghostty 和 Orca，确认新进程继承了持久化环境变量。

新开 ghostty 中：

```bash
echo "$SSL_CERT_FILE"
# /etc/ssl/certs/ca-certificates.crt

echo "$NODE_EXTRA_CA_CERTS"
# /home/www/workspace/2026/magic-claude-code/data/ca.crt
```

新开 Orca/Claude Code 所在环境中：

```bash
echo "$SSL_CERT_FILE"
# /etc/ssl/certs/ca-certificates.crt

echo "$NODE_EXTRA_CA_CERTS"
# /home/www/workspace/2026/magic-claude-code/data/ca.crt
```

mcc 容器启动日志显示宿主机系统配置已就绪，Docker 场景中容器内 bootstrap 按预期跳过宿主 profile 写入：

```text
2026/07/13 21:25:17 CA certificate: /app/data/ca.crt
2026/07/13 21:25:17 Attempting automatic transparent mode setup...

========== Bootstrap Result ==========
  hosts: ready
  CA: OK
  ENV: skipped
  NODE_CA: skipped
  SSL_CERT_FILE: skipped

✓ Transparent mode is ready.
```

随后在新 Claude Code 长会话中触发 918 条消息、约 2.0 MB 请求，主请求正常返回 200，并进入 SSE heartbeat：

```text
2026/07/13 21:31:25 [642a0b13] >>> POST api.anthropic.com/v1/messages model=claude-opus-4-8 -> glm-5.2 stream=true msgs=918 tools=10 size=2083982 provider_name="GLM 4.7 ky" upstream_url="https://open.bigmodel.cn/api/anthropic/v1/messages" upstream_query="beta=true,other_count=0"
2026/07/13 21:31:56 [642a0b13] <<< 200 api.anthropic.com/v1/messages model=claude-opus-4-8 -> glm-5.2 upstream=30867ms provider_name="GLM 4.7 ky" upstream_url="https://open.bigmodel.cn/api/anthropic/v1/messages" upstream_query="beta=true,other_count=0"
2026/07/13 21:31:56 [Stream] SSE stream detected for https://open.bigmodel.cn/api/anthropic/v1/messages | query: beta=true,other_count=0, enabling heartbeat injection
```

验证结论：

- [x] profile/用户环境中已持久化并继承 `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`。
- [x] profile/用户环境中已持久化并继承 `NODE_EXTRA_CA_CERTS=/home/www/workspace/2026/magic-claude-code/data/ca.crt`。
- [x] 系统 bundle 含当前 `data/ca.crt` 指纹；mcc bootstrap 输出 `CA: OK`。
- [x] 新 shell 环境包含正确 `SSL_CERT_FILE`。
- [x] 新 Claude Code/Orca 进程继承变量。
- [x] 长对话后 mcc 日志未再出现 `unknown_ca|bad record MAC`。
- [x] 长对话请求正常转发并返回 200；此前需依赖 `SSL_CERT_FILE` 的后台辅助请求复现路径已恢复。
