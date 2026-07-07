# Node Extra CA Certs 自动配置复审记录

日期：2026-07-06
复审者：Codex 与 Claude Code

## 范围

复审提交范围 `2416c96..0b2cf78`，覆盖 F-1 profile 扫描、F-4 marker 身份绑定、特权运行拒绝 Node CA 持久化、平台权限探测、测试及中英文提示。

## 关键发现与处理要求

1. Linux bootstrap 测试并不干净。
   - 证据：`go test ./internal/bootstrap -count=1`、race 测试和 `go test ./... -count=1` 均失败于 `TestWritePOSIXProfileNodeCA_SymlinkTargetNotFollowed`。选项 1b 已让非特权 POSIX 用户跟随 profile symlink，但旧测试仍要求不跟随。
   - 必须处理：像 PowerShell 一样把 POSIX 测试拆成“特权 fail-closed”和“非特权兼容 symlink”两类明确断言。
2. F-1 在 profile 读取错误时仍然 fail-open。
   - 证据：`scanPwshProfilesForCustomValue` 与 `scanPOSIXProfilesForCustomValue` 都丢弃 `os.ReadFile` 错误。通过 Go overlay 的聚焦测试已复现：含用户自定义值但不可读的 profile 被当作干净，随后仍调用 `launchctl setenv`。
   - 必须处理：仅将 `os.IsNotExist` 视为 profile 不存在；其他读取错误必须在允许 `setx` 或 `launchctl` 前向上传播。
3. Windows 权限探测在 token 查询失败时 fail-open。
   - 证据：`privilegedByOS` 在 `OpenProcessToken` 失败时返回 false；`windows.Token.IsElevated` 在 `GetTokenInformation` 失败时同样返回 false。
   - 必须处理：将“未知/错误”与“非特权”分开；权限状态无法确认时拒绝 Node CA 持久化，并补 Windows 原生故障注入测试。
4. F-4 的 Unix 身份强制符合声明。
   - 证据：Linux uid 1000 环境下，UID 缺失、UID 不匹配和 marker 正常命中测试均通过。
5. 特权拒绝只关闭正常的 `PersistNodeCACert` 路径。
   - 处理：继续把 `PersistRoot`、父目录 symlink/reparse point 与 TOCTOU 作为明确后续项；不能表述为所有特权 profile 修改都已关闭。

## 最终复审结论

暂不批准合并。攻击路径校准后没有中危或更高的可利用安全发现，但本次交付报告中的验证结论不准确：Linux bootstrap 与全量 Go 测试失败，且 F-1 三态扫描仍存在已复现的读取错误 fail-open 分支。修复这些问题，并让 Windows 权限查询错误 fail-closed 后再复审。

## 残余说明

- 未执行 Windows 原生 junction/reparse point 与 token 查询失败测试。
- 未执行 macOS 原生 `launchctl` 行为测试。
- `PersistRoot` 和 descriptor-relative 文件系统加固仍是独立后续工作。

## 后续复审 — `df3c96f..8cf4bf8`

### 已确认改进

- POSIX symlink 测试现已符合选项 1b：特权运行 fail-closed，非特权运行跟随 profile symlink。
- 在类 Unix 系统上，普通 profile 读取错误会在 `setx`/`launchctl` 前向上传播。
- 权限探测错误现统一进入拒绝状态；Windows 实现显式检查 `OpenProcessToken`、`GetTokenInformation` 和返回的 `TOKEN_ELEVATION` 长度。
- 新增聚焦测试全部通过，`go vet ./internal/bootstrap` 通过，Windows/macOS 测试二进制交叉编译通过。

### 尚存阻断项

1. GLM 所称“Linux 全绿”不属实。以下命令均非零退出，并失败于相同的 3 个测试：
   - `go test ./internal/bootstrap -count=1`
   - `go test -race ./internal/bootstrap -count=1`
   - `go test ./... -count=1`

   失败测试为 `TestPersistNodeCACert_Windows_SetxSuccess_ProfileFails_ReturnsPartial`、`TestPersistNodeCACert_Darwin_LaunchctlSuccess_ProfileFails_ReturnsPartial` 和 `TestWritePwshProfileNodeCA_PartialFailure_ReturnsPartial`。它们的 fixture 把普通文件放在祖先目录位置。Linux 返回 `ENOTDIR`，因此新的预扫描会正确地在环境修改前终止，但测试仍期待旧的 partial-success 路径。

2. `readProfile` 在 Windows 上仍未完全 fail-closed。它将所有满足 `os.IsNotExist` 的错误视为可安全创建的缺失 profile。Go 会把 Windows `ERROR_PATH_NOT_FOUND` 映射为 `ErrNotExist`；当非叶子路径组件不是目录时，Windows 也可能返回该错误。因此预扫描仍可能先允许 `setx`，随后 writer 才发现 profile 无法创建。

   必须修复：在把 `IsNotExist` 视为可创建前，区分“仅叶子文件缺失”和“祖先组件无效/不是目录”。应校验父路径链或最近的现存祖先。补充 Windows 原生 file-as-parent 测试，断言不调用 `setxEnvVar`。同时更新 3 个过期的 Linux 测试，使其断言提前失败且不发生全局环境修改；如仍需独立覆盖 partial-success，应使用明确的故障注入 hook。

### 后续结论

仍不批准合并。攻击路径分析后没有可报告的安全漏洞，因为顶层特权运行拒绝把 Windows 缺陷限制在同一用户状态内；但 fail-closed 契约仍存在平台缺口，且分支要求的 Linux 测试套件仍为红色。

## 后续复审 — `fd388b7..7ec52de`

### 已确认解决

- 之前失败的 3 个 Linux 测试现使用确定性的 `writeFileSync` 故障注入，并已通过。
- file-as-parent 路径会在 `setx` 前被拒绝；新增集成测试通过。
- `go test ./internal/bootstrap -count=1`、`go test -race ./internal/bootstrap -count=1` 和 `go test ./... -count=1` 在 Linux 上全部通过。
- `go vet ./internal/bootstrap` 以及 Windows/macOS 测试二进制交叉编译通过。

### 缺失根路径修复

`validateParentChain` 现委托给可测试的 `validateParentChainWithStat` 遍历。当回溯到根且 stat 结果仍为 `IsNotExist` 时，函数返回错误，不再把根路径视为可创建。这会阻止不存在的 Windows 盘符根目录或 UNC 根目录先行授权 `setx`。

覆盖包含一个跨平台的注入 stat 回归测试，以及一个 Windows 专用集成测试；后者选择未占用盘符并断言不调用 `setxEnvVar`。Windows 测试二进制已交叉编译成功，原生执行仍是最终平台检查。

### 后续结论

Linux 复审环境下批准，待 Windows 原生测试确认。此前报告的 Linux 回归、file-as-parent 缺陷和缺失根路径 F-1 分支均已关闭；本次变更范围内没有可报告的安全漏洞。

## 合并与发版就绪复审 — `main...2bb9d2c`

### 范围

复审从合并基点 `eb96f96` 到 `2bb9d2c` 的全部 23 个分支提交，包含 Node CA 持久化、Windows 环境刷新跟进、安装脚本文案，以及随后 3 个 Windows 图标提交。

### 发版阻断项

1. 相对数据目录会持久化依赖工作目录的 CA 路径。
   - `resolveDataDir` 会原样返回 `./data` 这类显式参数，`cert.Manager.GetCACertPath` 因而得到相对路径 `data/ca.crt`。
   - 新代码把该相对值直接写入 `setx`、`launchctl`、POSIX profile 和幂等 marker。PowerShell 块还用 `Test-Path` 保护赋值，因此客户端从其他目录启动时根本不会设置 `NODE_EXTRA_CA_CERTS`。
   - 必须修复：在持久化及 marker 比较前把 CA 路径规范化为绝对路径，并增加从 `-data ./data` 启动、客户端从不同工作目录启动的回归测试。
2. 已有的环境层用户配置会被覆盖。
   - Windows 与 macOS 路径只扫描 profile 文本，随后无条件调用 `setx` 或 `launchctl setenv`。聚焦 overlay 测试预设了企业 CA 的 `NODE_EXTRA_CA_CERTS`，Windows 路径仍调用 `setx`，测试按预期失败。
   - 必须修复：检查真实持久化层/当前环境，并保留非 MCC 管理的值。需补 Windows 注册表/会话和 macOS 会话测试，不能只测 profile 文本。
3. 要求的原生平台验收仍未关闭。
   - feature spec 仍记录 `Progress: 0 / 7 planned`，Windows/macOS/Linux 端到端验证清单也未勾选。
   - Windows/macOS 交叉编译不能执行 token、注册表、profile、缺失盘符/UNC 和 `WM_SETTINGCHANGE` 行为。发版前至少要执行并归档 Windows 原生测试以及 Windows Orca/Node 端到端流程。

### 验证证据

- `make test` 通过，包含全仓 race 测试。
- `go vet ./...` 通过。
- `npm --prefix internal/frontend test` 通过：158 项，0 失败。
- `npm --prefix internal/frontend run build` 通过，且构建后工作树保持干净。
- 按发布脚本的 `GOOS`/`GOARCH`、`CGO_ENABLED=0`、`-trimpath` 和 release `ldflags` 形式，6 个发布目标全部构建成功。
- Windows amd64 与 arm64 可执行文件均包含 `.rsrc` 段；按当前 `make icon` 配方重新生成的 `.ico` 与 `.syso` 和提交文件逐字节一致。
- `go mod tidy -diff`、`go mod verify`、`git diff --check` 均通过。
- 本地分支比 `origin/feat/node-extra-ca-certs-auto-setup` 多 3 个提交，均为尚未推送的 Windows 图标改动。

### 安全结论

diff 安全复审后没有可报告的安全漏洞。上述两个阻断项属于同用户配置完整性与正确性缺陷。残余加固项包括：profile 替换并非原子/descriptor-relative 操作，父链检查仍有 TOCTOU 窗口，`make icon` 使用了未锁定版本的开发依赖 `github.com/akavel/rsrc@latest`。

### 最终复审结论

暂不批准合并或发版。修复相对路径持久化和环境层自定义值覆盖、补回归测试、完成 Windows 原生验收并更新 feature 验证记录后，再做发版就绪复审。

## 正确性修复复审 — `a2c0d4b..b3bd435`

### 已确认解决

- `tryPersistNodeCA` 现会在 stat、查询、持久化和 marker 操作前把 CA 路径转为绝对路径。red-green 回归测试证明，由相对 `-data` 产生的 CA 路径到达 adapter 和 marker 时已是绝对路径。
- 执行器现会在修改前读取平台实际值。不同的非 MCC 值会返回 `ErrUserCustomValue`；匹配 marker 也不再掩盖用户后来设置的自定义值。
- 仅当前用户绑定 marker 中记录的原 MCC 路径可以授权该精确旧值迁移，既保留 CA 路径搬迁能力，又拒绝无关自定义值。
- 查询失败会在 `setx`、`launchctl setenv` 或 profile 写入前 fail-closed。
- Windows 先查继承的进程环境再查 HKCU；macOS 先查进程环境再查 `launchctl`；其他平台检查进程环境并继续保留 profile 扫描。

### 验证

- 聚焦 red/green、bootstrap 全量、bootstrap race、`make test`、`go vet ./...`、`go mod tidy -diff` 和 `go mod verify` 均通过。
- 前端测试 158 项全部通过，0 失败；生产前端构建通过且未产生被跟踪的改动。
- Linux/macOS/Windows 的 amd64 与 arm64 共 6 个 release 构建形态全部通过。
- Windows amd64/arm64 与 macOS bootstrap 测试二进制交叉编译成功。

### 后续结论

两个代码层合并阻断项均已解决，本分支在当前 Linux 自动化复审环境下批准合并。发版批准仍需在 Windows 原生执行 token/注册表/profile/缺失根/环境广播测试，并完成已记录的 Orca/Node 端到端流程；macOS/Linux 原生端到端记录仍属于原始全平台验收任务。
