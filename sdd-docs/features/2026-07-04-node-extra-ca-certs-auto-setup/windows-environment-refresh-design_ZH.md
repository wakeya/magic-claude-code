# Windows Node CA 环境刷新跟进设计

日期：2026-07-05

## 目标

让普通用户启动 mcc 并成功持久化 `NODE_EXTRA_CA_CERTS` 后，随后从 Windows Shell 启动的 Orca 更可靠地继承新值；同时明确已经运行的 Orca 必须完全退出后重启。

## 已确认根因

- `NODE_EXTRA_CA_CERTS` 只在 Node.js 进程启动时读取。
- Windows 子进程默认继承父进程环境块；仅写入 `HKCU\Environment` 不会修改已经运行进程的环境。
- `scripts/setup-host.ps1` 当前只打印 `setx` 提示，没有执行该命令。
- 当前 bootstrap 已在非提权 Windows 进程中执行用户级 `setx` 并写 PowerShell profile；提权进程会拒绝修改 HKCU/profile。

## 设计

1. 保留现有权限分层：管理员流程只负责 hosts 与系统 CA；用户级环境仍由普通用户启动的 mcc 配置。
2. Windows `setx NODE_EXTRA_CA_CERTS` 成功后，显式广播 `WM_SETTINGCHANGE`，`lParam` 为 `Environment`，通知 Explorer 等 Shell 重新读取环境设置。
3. 广播使用带超时的 `SendMessageTimeoutW`，不能无限阻塞 mcc 启动。
   - Win32 调用放在带 `windows` build tag 的独立文件中。
   - `adapters.go` 通过可替换的 `broadcastEnvironmentChange` hook 调用，便于 Linux 单元测试确定性覆盖成功与失败。
4. 广播失败不撤销已经成功的注册表写入；返回 partial success，并提示用户注销重登。profile 写入仍继续执行。
5. 启动提示明确区分：
   - 持久化成功：完全退出并重启 Orca；
   - 广播失败：注销并重新登录后再启动 Orca；
   - 管理员启动：随后以普通用户启动一次 mcc。
6. `setup-host.ps1` 删除“执行 setx”式提示，改为说明普通用户启动一次 mcc，再重启 Orca；脚本本身不写 HKCU，避免 UAC 使用其他管理员凭据时写错用户配置。
7. 不在本次变更中自动创建计划任务，也不修改代理监听地址默认值；两者属于独立的生命周期和部署策略。

## 数据流

```text
管理员首次配置 hosts/系统 CA
              ↓
普通用户启动 mcc
              ↓
setx 写 HKCU + 写 pwsh profile
              ↓
广播 WM_SETTINGCHANGE("Environment")
              ↓
完全退出并重新启动 Orca
              ↓
Orca → Claude Code/Node 继承 NODE_EXTRA_CA_CERTS
```

## 错误处理

- `setx` 失败：沿用现有 partial-success 语义，profile 成功时仍提供 shell 兜底。
- profile 写入失败：沿用现有 partial-success 语义，注册表值保留。
- 广播失败：注册表写入已完成，不回滚；标记为 partial success，并给出注销重登提示。
- 已运行的 Orca：不尝试注入或终止，避免修改用户进程状态。

## 测试

1. 先增加失败测试，证明 Windows 持久化在 `setx` 成功后必须调用广播 hook。
2. 覆盖广播成功、广播失败、`setx` 失败时不广播，以及广播失败但 profile 仍写入。
3. 覆盖中英文提示和 `setup-host.ps1` 文案。
4. 运行 `go test ./internal/bootstrap -count=1`、`go test -race ./internal/bootstrap -count=1`、`go test ./... -count=1`、`go vet ./...`，并交叉编译 Windows测试二进制。

## 验收标准

- 普通用户启动 mcc 后会持久化变量并发送有限时环境刷新广播。
- 已运行 Orca 的重启要求在日志和脚本文案中明确可见。
- 管理员进程不写用户级 HKCU/profile。
- 广播失败不会阻止 mcc 代理启动，用户获得可执行的注销重登提示。
