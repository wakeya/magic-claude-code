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
