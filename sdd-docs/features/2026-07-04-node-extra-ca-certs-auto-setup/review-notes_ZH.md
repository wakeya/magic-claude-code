# Node Extra CA Certs 自动配置复审记录

日期：2026-07-05
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
