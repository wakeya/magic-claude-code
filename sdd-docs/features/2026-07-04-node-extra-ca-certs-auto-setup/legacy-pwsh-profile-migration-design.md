# 旧版 pwsh profile 迁移 — 设计

## 背景

### 问题

`feat/node-extra-ca-certs-auto-setup` 引入了 pwsh profile 的 mcc 块管理（`# >>> mcc: ... >>>` / `# <<< mcc <<<` 标记）和块外自定义值检测（`pwshProfileHasNodeCAVarOutsideMCCBlock`，F-1 fail-closed 安全要求）。

端到端验证（2026-07-06，Windows）发现**升级兼容性问题**：旧版 mcc 写入的 pwsh profile 残留被新版检测判定为"块外用户自定义"，导致 `PersistNodeCACert` 永久跳过。

### 现状证据

旧版 mcc 写入的 `Documents\PowerShell\Microsoft.PowerShell_profile.ps1`（无新版 begin/end 标记）：

```powershell
$mccCa = "$env:USERPROFILE\mcc-windows-amd64\data\ca.crt"
if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }
```

新版 `pwshProfileHasNodeCAVarOutsideMCCBlock`（adapters.go:731-751）扫描时，`if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }` 这一行——非注释、不在 mcc 块内、匹配 `pwshNodeCAAssignRe`（`$env:NODE_EXTRA_CA_CERTS =`）→ 返回 true（自定义）。

后果链：

1. `persistNodeCACertWindows`（adapters.go:451）`scanPwshProfilesForCustomValue` 返回 true → `ErrUserCustomValue`
2. `tryPersistNodeCA`（bootstrap.go:312）透传错误 → `PersistNodeCACert`（L303）未执行
3. `writeNodeCAMarker`（L305）未调用 → marker 永不存在 → 每次启动重复同样的判定

关键观察：新版 mcc 块**内部**（writePwshProfileNodeCA:662-665）与旧版残留**逻辑模式相同**——都用 `$mccCa` 变量 + `if (Test-Path $mccCa)` + `$env:NODE_EXTRA_CA_CERTS = $mccCa`。区别仅：新版被 begin/end 标记包裹，且 `$mccCa` 值用 `Join-Path $env:USERPROFILE '...'`（新版）vs `"$env:USERPROFILE\..."`（旧版）。

`$mccCa` 是 mcc 写入的专用变量名（新版块内仍在用），用户自定义极不可能用此名。

## 目标

升级场景下，新版 mcc 能认领旧版写入的 pwsh profile 残留并迁移到 mcc 块，使 marker + setx 持久化路径正常执行。

## 非目标

- POSIX profile（bash/zsh/fish）的旧版残留迁移——旧版可能用裸 `export NODE_EXTRA_CA_CERTS=...`，与用户自定义无法区分，需单独设计（基于路径值识别等），留 follow-up。
- 旧版中文注释清理——注释删除风险高于收益，保留。

## 设计

### 1. 检测层：识别旧版 mcc 模式

`pwshProfileHasNodeCAVarOutsideMCCBlock`（adapters.go:731-751）增加例外：块外非注释行匹配 `pwshNodeCAAssignRe` **且同时含 `$mccCa`** → 视为 mcc 旧版管理，跳过（不报自定义）。

```go
if pwshNodeCAAssignRe.MatchString(trimmed) {
    if strings.Contains(trimmed, "$mccCa") {
        continue // mcc 旧版写入模式（块外无标记），视为 mcc 管理
    }
    return true
}
```

理由：`$mccCa` 是 mcc 专用变量名（新版块内也在用），是 mcc 写入的稳定约定。用户自定义碰巧用此名的概率极低；即使发生，最坏后果是 mcc 多写一次（setx 同值，幂等）。

### 2. 清理层：stripLegacyMCCNodeCALines

新增函数：

```go
// stripLegacyMCCNodeCALines 删除 mcc 块外、匹配 $mccCa 的非注释行
// （旧版 mcc 写入的 $mccCa = ... 和 if (Test-Path $mccCa) ... 残留）。
// 保留 mcc 块内、注释行、用户其他内容。changed 表示是否实际改动。
func stripLegacyMCCNodeCALines(content string) (string, bool)
```

实现：逐行扫描，跟踪 inBlock 状态（复用 begin/end 标记逻辑），块外非注释行若 `strings.Contains(line, "$mccCa")` 则删除。

调用点：`writePwshProfileNodeCA` 阶段 2（adapters.go:688-714），每个候选 `replaceMarkedBlock`（L700）**之前**先 `stripLegacyMCCNodeCALines`。

阶段 2 调整后流程：

```
for each candidate:
  read profile → existing
  stripped, stripChanged := stripLegacyMCCNodeCALines(existing)
  updated, blockChanged := replaceMarkedBlock(stripped, begin, end, block)
  if stripChanged || blockChanged: write updated
  wrote = true
```

### 3. 注释保留

旧版 L1-3 中文注释（`# mcc 透明代理...` 等）保留不动。注释不影响检测（检测跳过注释行），功能无影响。

## 测试（TDD，先红后绿）

1. `TestPwshProfileHasNodeCAVar_LegacyMCCPattern_NotCustom`
   - 输入：旧版 `$mccCa = ...` + `if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }`
   - 断言：返回 false（不报自定义）

2. `TestPwshProfileHasNodeCAVar_UserCustomExport_StillDetected`
   - 输入：用户真自定义 `$env:NODE_EXTRA_CA_CERTS = "C:\my-ca.pem"`（不含 `$mccCa`）
   - 断言：返回 true（仍报自定义，未因放宽规则而误放过）

3. `TestStripLegacyMCCNodeCALines`
   - 输入：profile 含【旧版 `$mccCa` 残留 + 新版 mcc 块（块内 `$mccCa`）+ 用户 export + 注释】
   - 断言：只删块外旧版 `$mccCa` 行；mcc 块内 `$mccCa`、用户 export、注释原样保留

4. `TestWritePwshProfileNodeCA_LegacyResidual_MigratesToBlock`（集成）
   - 输入：profile 含旧版 `$mccCa` 残留
   - 断言：写入后只剩【mcc 块 + 原有用户内容】，无旧版 `$mccCa` 行

## 边界与风险

- 用户碰巧用 `$mccCa` 变量名 → 被误识别为 mcc 管理、误删除。接受（`$mccCa` 是 mcc 专用名，概率极低；setx 同值幂等）。
- 旧版残留只有部分行（如孤立 `$mccCa = ...` 无后续 if）→ 只删匹配行，安全。
- POSIX profile 不动（scope 外）。

## 验证

修复后重跑端到端场景（Windows）：旧版 profile 残留在位时启动 mcc-test，预期：

- marker `.node-ca-persisted` 写入
- 日志无 `⚠ User custom NODE_EXTRA_CA_CERTS detected`
- profile 迁移：旧版 `$mccCa` 行被删，mcc 块写入，注释保留

## 引用

- adapters.go:440-441 — `pwshProfileMarkerBegin/End`
- adapters.go:451,518 — `scanPwshProfilesForCustomValue`
- adapters.go:634-722 — `writePwshProfileNodeCA`
- adapters.go:727 — `pwshNodeCAAssignRe`
- adapters.go:731-751 — `pwshProfileHasNodeCAVarOutsideMCCBlock`
- adapters.go:755-772 — `replaceMarkedBlock`
- bootstrap.go:267-318 — `tryPersistNodeCA`
