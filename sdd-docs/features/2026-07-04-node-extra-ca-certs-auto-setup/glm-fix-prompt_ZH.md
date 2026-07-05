# GLM 修复指令：Node Extra CA Certs 复审问题

请修复 Codex 对提交范围 `2416c96..0b2cf78` 的复审问题。只处理以下三项，不扩大到 `PersistRoot`、descriptor-relative 写入、Windows junction/reparse point 或完整 TOCTOU 加固。

工作分支：`feat/node-extra-ca-certs-auto-setup`

## 一、修复 Linux 现有测试失败

当前以下命令失败：

```bash
go test ./internal/bootstrap -count=1
go test -race ./internal/bootstrap -count=1
go test ./... -count=1
```

失败测试：`TestWritePOSIXProfileNodeCA_SymlinkTargetNotFollowed`

原因：实现已采用选项 1b——非特权运行跟随 profile symlink，旧测试仍断言“不跟随”。

要求：

1. 不要回退 1b 实现。
2. 将 POSIX 测试调整为两个明确场景：
   - 特权运行：symlink profile 必须返回 `ErrUnsafeProfile`，目标不得被修改。
   - 非特权运行：允许跟随 symlink，并验证 mcc block 正确写入目标。
3. 与现有 PowerShell 测试语义保持对称。
4. 不允许 skip Linux 场景来掩盖失败。

## 二、完整关闭 F-1 的 profile 读取错误 fail-open

当前问题位于：

- `internal/bootstrap/adapters.go:494`
- `internal/bootstrap/adapters.go:512`
- `writePwshProfileNodeCA` / `writePOSIXProfileNodeCA` 内其他 `existing, _ := os.ReadFile(...)`

新三态扫描仍丢弃 `os.ReadFile` 错误。已复现：profile 存在但不可读时，被当作“没有用户自定义值”，随后 `setx` / `launchctl setenv` 仍会执行。

要求：

1. profile 不存在时可以视为空 profile，继续处理。
2. 只有 `os.IsNotExist(err)` 可以忽略。
3. 其他读取错误必须向上传播，并且发生在 `setx` / `launchctl` 前时，禁止调用这些全局环境修改操作。
4. 不要把普通读取错误伪装成 `ErrUnsafeProfile`；返回包含 profile 路径和原始错误的可诊断错误。
5. writer 的扫描/读取阶段也不得把读取失败当作空内容后覆盖文件。
6. 保持非特权 symlink 兼容语义；不要重新禁止正常 dotfiles symlink。
7. TOCTOU 仍可作为已知残余风险，不要求本轮实现 fd-relative 写入。

新增测试至少覆盖：

- `scanPwshProfilesForCustomValue` 遇非 `NotExist` 读取错误时返回 error。
- `scanPOSIXProfilesForCustomValue` 同上。
- Windows persistence：扫描失败时 `setx` 调用次数为 0。
- Darwin persistence：扫描失败时 `launchctlSetenv` 调用次数为 0。
- profile 不存在仍走正常创建路径。
- 正常 symlink profile 在非特权模式下仍兼容。

测试应尽量跨平台稳定。可把候选 profile 设置成目录或其他确定会让 `os.ReadFile` 失败的路径，不要依赖 Windows 上不可靠的 `chmod 000` 语义。

## 三、Windows 权限探测错误必须 fail-closed

当前 `privilegedByOS` 存在两层 fail-open：

1. `OpenProcessToken` 失败返回 false。
2. `windows.Token.IsElevated()` 内部 `GetTokenInformation` 失败也返回 false。

false 会让 `tryPersistNodeCA` 进入允许 profile/HKCU 修改的路径。

要求：

1. 权限状态无法确定时必须按“禁止 Node CA 持久化”处理。
2. 不要继续直接依赖会吞掉错误的 `Token.IsElevated()`。
3. 显式调用能够返回 error 的 token elevation 查询，或封装 `(elevated bool, err error)`。
4. 保持现有 `isPrivilegedRun` 测试注入能力，避免大范围重构。
5. 增加可测试的错误路径：
   - token 打开失败 → fail-closed。
   - elevation 查询失败或返回长度异常 → fail-closed。
   - 明确 elevated → 拒绝。
   - 明确 non-elevated → 允许。
6. Windows 原生测试无法执行时，至少提供可注入的纯逻辑测试，并完成 Windows 交叉编译；不得把“编译通过”描述成“Windows 原生行为已验证”。

## 范围约束

- 不修改 `PersistRoot`。
- 不实现 `openat` / `O_NOFOLLOW` 或 Windows reparse-point walk。
- 不修改 marker 格式，除非修复上述问题确实需要。
- 不改前端。
- 不修改 Codex 复审记录：
  - `review-notes.md`
  - `review-notes_ZH.md`

## 验证命令

完成后必须运行：

```bash
go test ./internal/bootstrap -count=1
go test -race ./internal/bootstrap -count=1
go test ./... -count=1
GOOS=windows GOARCH=amd64 go test -c ./internal/bootstrap -o /tmp/mcc-bootstrap-windows.test.exe
GOOS=darwin GOARCH=amd64 go test -c ./internal/bootstrap -o /tmp/mcc-bootstrap-darwin.test
git diff --check
git status --short
```

Windows PowerShell 下交叉编译输出路径可替换为 `$env:TEMP` 中的文件。

## 验收标准

- 三个 Go 测试命令必须全部退出 0；不得再以“baseline 失败”作为通过。
- 两个交叉编译命令退出 0。
- F-1 读取错误测试证明 `setx` / `launchctl` 均为 0 次调用。
- Linux 特权/非特权 POSIX symlink 两种语义都有明确测试。
- Windows 未知权限状态明确 fail-closed。
- 只提交本轮相关代码和测试，不修改 Codex review notes。

## 最终交付报告

请给出：

1. 每个问题的根因与修复策略。
2. 修改文件和关键行。
3. 新增测试逐项对应的攻击或失败场景。
4. 每条验证命令的真实退出结果。
5. 尚未验证的平台边界。
6. 残余风险。
7. 提交 SHA。
8. `git status --short` 输出。
