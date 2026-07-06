# Legacy pwsh Profile 迁移 — 端到端验证发现与复审

日期：2026-07-06
发现者：Claude Code（端到端验证）

## 触发

在 Windows 真实环境按 `feat/node-extra-ca-certs-auto-setup` 的验证步骤跑端到端，发现**升级兼容性问题**——单元测试未捕获。

## 根因

旧版 mcc 写入的 `Documents\PowerShell\Microsoft.PowerShell_profile.ps1`：

```powershell
$mccCa = "$env:USERPROFILE\mcc-windows-amd64\data\ca.crt"
if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }
```

无新版 `# >>> mcc: ... >>>` / `# <<< mcc <<<` 标记。新版 `pwshProfileHasNodeCAVarOutsideMCCBlock`（adapters.go:731）把第二行（`if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }`）判为"块外自定义" → `scanPwshProfilesForCustomValue` 返回 `ErrUserCustomValue` → `PersistNodeCACert` 永久跳过 → marker + setx 路径不执行。

**后果：** 升级后功能静默失效，依赖旧版残留兜底；旧 profile 被清理后新版不会自动接管 → 断链。

## 关键观察

新版 mcc 块**内部**（writePwshProfileNodeCA:662-665）也用 `$mccCa` 变量 + 同样的 `if (Test-Path $mccCa)` 结构。`$mccCa` 是 mcc 写入 profile 的**专用变量名约定**（旧版/新版都在用），用户自定义极不可能用此名。

## 修复（提交 0cfc3e2 + e916cc0 + d76f53f）

1. **检测层**（`pwshProfileHasNodeCAVarOutsideMCCBlock`）：NECC 赋值行若同时含 `$mccCa`，视为 mcc 旧版管理，不报自定义。
2. **清理层**（新增 `stripLegacyMCCNodeCALines`）：删除 mcc 块外含 `$mccCa` 的非注释行，保留 mcc 块内、注释、用户其他内容。
3. **集成**（`writePwshProfileNodeCA` 阶段 2）：`replaceMarkedBlock` 前先调 strip，一次性把旧版残留迁移到 mcc 块。

## 验证

端到端重跑（Windows，旧版 profile 在位）：
- marker `.node-ca-persisted` 写入（修复前不存在）
- 启动日志无 `⚠ User custom NODE_EXTRA_CA_CERTS detected`
- profile 迁移：旧版 `$mccCa` 行删除、新版 mcc 块写入、L1-3 中文注释完整保留

单测：bootstrap 包新增 9 个测试全过（266→275 passed），无新回归（11 个 Windows 环境噪音失败与本改动无关）。

## 残余 gap（未修）

- **POSIX profile（bash/zsh/fish）侧同类问题未修。** 旧版用裸 `export NODE_EXTRA_CA_CERTS=...`，与用户自定义 export 无法区分，不能照搬 pwsh 的变量名识别。修复方向：基于 export 值是否指向 mcc data 目录的 ca.crt（解析路径 + 对比 dataDir）。跨会话记忆见 `posix-node-ca-legacy-followup`。
- **gateway 端口 17487 无 flag 覆盖**（main.go 缺 `-gateway-port`）：自定义端口隔离测试时 gateway 仍冲突。不影响 NECC 验证（bootstrap 在端口绑定前），但是测试摩擦点——建议补 `-gateway-port` flag。
- **Windows go test 11 个环境噪音失败**（Unix 权限模型 / i18n / shell 持久化假设）：建议 CI 加 Windows runner。

## 引用

- 设计：`legacy-pwsh-profile-migration-design.md`
- 实现计划：`legacy-pwsh-profile-migration-plan.md`
- 代码：`internal/bootstrap/adapters.go`（`pwshProfileHasNodeCAVarOutsideMCCBlock` / `stripLegacyMCCNodeCALines` / `writePwshProfileNodeCA`）
