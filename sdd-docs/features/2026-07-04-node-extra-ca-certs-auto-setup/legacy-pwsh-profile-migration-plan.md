# 旧版 pwsh profile 迁移 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让新版 mcc 在升级场景下认领并迁移旧版写入的 pwsh profile `$mccCa` 残留到 mcc 块，恢复 marker + setx 持久化路径。

**Architecture:** 两层改动——(1) 检测层 `pwshProfileHasNodeCAVarOutsideMCCBlock` 把"NECC 赋值行引用 `$mccCa`"识别为 mcc 管理（不报自定义）；(2) 清理层新增 `stripLegacyMCCNodeCALines`，在 `writePwshProfileNodeCA` 阶段 2 写 mcc 块前删除块外旧版 `$mccCa` 行。识别规则利用 `$mccCa` 是 mcc 专用变量名（新版块内也在用）这一稳定约定。

**Tech Stack:** Go 1.26, 标准库 `strings`/`regexp`, 既有 bootstrap 包测试框架（`t.TempDir` + `withPwshHooks` + `writeFile`）。

**Spec:** `sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/legacy-pwsh-profile-migration-design.md`

---

## 文件结构

| 文件 | 职责 | 改动 |
|---|---|---|
| `internal/bootstrap/adapters.go` | 检测 + 写入逻辑 | 改 `pwshProfileHasNodeCAVarOutsideMCCBlock`（加 `$mccCa` 例外）；新增 `stripLegacyMCCNodeCALines`；改 `writePwshProfileNodeCA` 阶段 2 调用 strip |
| `internal/bootstrap/bootstrap_test.go` | 单元 + 集成测试 | 表驱动加 case；新增 `TestStripLegacyMCCNodeCALines`；新增 `TestWritePwshProfileNodeCA_LegacyResidual_MigratesToBlock` |

---

## Task 1: 检测层识别旧版 mcc `$mccCa` 模式

**Files:**
- Modify: `internal/bootstrap/adapters.go:731-751` (`pwshProfileHasNodeCAVarOutsideMCCBlock`)
- Test: `internal/bootstrap/bootstrap_test.go:3072-3129` (`TestPwshProfileHasNodeCAVarOutsideMCCBlock` 表)

- [ ] **Step 1: 写失败测试（在既有表驱动里加 2 个 case）**

在 `bootstrap_test.go` 的 `TestPwshProfileHasNodeCAVarOutsideMCCBlock` 的 `tests` 切片里，紧跟 `"suffix variable name NODE_EXTRA_CA_CERTS_BACKUP"` case 之后，加入：

```go
// 旧版 mcc 写入模式（块外无 begin/end 标记）：$mccCa 是 mcc 专用变量，
// 视为 mcc 管理，不报自定义（升级兼容，见 legacy-pwsh-profile-migration-design.md）。
{
    name:    "legacy mcc $mccCa pattern outside block not detected",
    content: "$mccCa = \"$env:USERPROFILE\\mcc-windows-amd64\\data\\ca.crt\"\nif (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n",
    want:    false,
},
// 同结构但变量名非 $mccCa：仍判为用户自定义，证明规则只认 $mccCa
{
    name:    "similar pattern with non-mcc variable still detected",
    content: "$myCa = 'C:\\ca.crt'\nif (Test-Path $myCa) { $env:NODE_EXTRA_CA_CERTS = $myCa }\n",
    want:    true,
},
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/bootstrap -run TestPwshProfileHasNodeCAVarOutsideMCCBlock -v -count=1
```

Expected: FAIL。`legacy mcc $mccCa pattern outside block not detected` case 报 `got true, want false`（当前代码对块外 `$env:NODE_EXTRA_CA_CERTS = $mccCa` 报 true）。

- [ ] **Step 3: 实现 `$mccCa` 例外**

在 `adapters.go` 的 `pwshProfileHasNodeCAVarOutsideMCCBlock`（L746-748）把：

```go
if pwshNodeCAAssignRe.MatchString(trimmed) {
    return true
}
```

改为：

```go
if pwshNodeCAAssignRe.MatchString(trimmed) {
    // $mccCa 是 mcc 写入 profile 的专用变量名（新版块内也在用）。
    // 块外出现引用 $mccCa 的旧版赋值，视为 mcc 旧版管理，不报自定义。
    if strings.Contains(trimmed, "$mccCa") {
        continue
    }
    return true
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/bootstrap -run TestPwshProfileHasNodeCAVarOutsideMCCBlock -v -count=1
```

Expected: PASS（所有 case，含 2 个新 case）。

- [ ] **Step 5: 跑整个 bootstrap 包确认无回归**

```bash
go test ./internal/bootstrap -count=1
```

Expected: 之前通过的用例继续通过（注：环境噪音失败的 11 个用例如前述，与本改动无关）。

- [ ] **Step 6: 提交**

```bash
git add internal/bootstrap/adapters.go internal/bootstrap/bootstrap_test.go
git commit -m "fix(bootstrap): recognize legacy mcc \$mccCa pattern in pwsh profile scan

旧版 mcc 写入的 pwsh profile 用 \$mccCa 变量 + if (Test-Path) 模式，
无新版 begin/end 标记。新版扫描把它误判为块外用户自定义，导致
NODE_EXTRA_CA_CERTS 持久化永久跳过。识别 \$mccCa 为 mcc 专用变量，
视为 mcc 管理。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: 清理层 `stripLegacyMCCNodeCALines`

**Files:**
- Modify: `internal/bootstrap/adapters.go`（新增函数，放在 `pwshProfileHasNodeCAVarOutsideMCCBlock` 之后、`replaceMarkedBlock` 之前，约 L752 处）
- Test: `internal/bootstrap/bootstrap_test.go`（新增 `TestStripLegacyMCCNodeCALines`）

- [ ] **Step 1: 写失败测试**

在 `bootstrap_test.go` 的 `TestPwshProfileHasNodeCAVarOutsideMCCBlock` 函数之后，新增：

```go
func TestStripLegacyMCCNodeCALines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		changed bool
	}{
		{
			name:    "empty profile unchanged",
			content: "",
			want:    "",
			changed: false,
		},
		{
			name: "legacy $mccCa lines stripped, comment kept",
			content: "# mcc 透明代理\n" +
				"$mccCa = \"$env:USERPROFILE\\mcc-windows-amd64\\data\\ca.crt\"\n" +
				"if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n",
			want:    "# mcc 透明代理\n",
			changed: true,
		},
		{
			name: "block-internal $mccCa preserved, outside legacy stripped",
			content: "$mccCa = \"old\"\n" +
				"if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n" +
				"# >>> mcc: Node.js CA trust (auto-managed, do not edit) >>>\n" +
				"$mccCa = Join-Path $env:USERPROFILE 'ca.crt'\n" +
				"if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n" +
				"# <<< mcc <<<\n",
			want: "# >>> mcc: Node.js CA trust (auto-managed, do not edit) >>>\n" +
				"$mccCa = Join-Path $env:USERPROFILE 'ca.crt'\n" +
				"if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n" +
				"# <<< mcc <<<\n",
			changed: true,
		},
		{
			name:    "user export preserved (no $mccCa)",
			content: "$env:OTHER = 'x'\n",
			want:    "$env:OTHER = 'x'\n",
			changed: false,
		},
		{
			name:    "user custom NECC export preserved (no $mccCa)",
			content: "$env:NODE_EXTRA_CA_CERTS = 'C:\\user\\custom\\ca.crt'\n",
			want:    "$env:NODE_EXTRA_CA_CERTS = 'C:\\user\\custom\\ca.crt'\n",
			changed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := stripLegacyMCCNodeCALines(tt.content)
			if got != tt.want || changed != tt.changed {
				t.Errorf("stripLegacyMCCNodeCALines(%q) = (%q, %v), want (%q, %v)",
					tt.content, got, changed, tt.want, tt.changed)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/bootstrap -run TestStripLegacyMCCNodeCALines -v -count=1
```

Expected: FAIL，编译错误 `undefined: stripLegacyMCCNodeCALines`。

- [ ] **Step 3: 实现 `stripLegacyMCCNodeCALines`**

在 `adapters.go` 的 `pwshProfileHasNodeCAVarOutsideMCCBlock`（结束于 L751）之后、`replaceMarkedBlock`（L753 的注释）之前，插入：

```go
// stripLegacyMCCNodeCALines 删除 mcc 块外、匹配 $mccCa 的非注释行
// （旧版 mcc 写入的 $mccCa = ... 和 if (Test-Path $mccCa) ... 残留）。
// 保留 mcc 块内、注释行、用户其他内容。changed 表示是否实际改动。
//
// 升级场景：旧版 mcc 写入的 profile 没有新版 begin/end 标记，新版本
// 检测会判为"块外自定义"。识别后由 writePwshProfileNodeCA 调用本函数
// 清理旧版残留，再用 replaceMarkedBlock 写入新 mcc 块，完成一次性迁移。
func stripLegacyMCCNodeCALines(content string) (string, bool) {
	var kept []string
	inBlock := false
	changed := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, pwshProfileMarkerBegin) {
			inBlock = true
			kept = append(kept, line)
			continue
		}
		if strings.Contains(trimmed, pwshProfileMarkerEnd) {
			inBlock = false
			kept = append(kept, line)
			continue
		}
		// 块外、非注释、含 $mccCa（mcc 专用变量）→ 旧版残留，删除
		if !inBlock && trimmed != "" && !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "$mccCa") {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	if !changed {
		return content, false
	}
	return strings.Join(kept, "\n"), true
}

```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/bootstrap -run TestStripLegacyMCCNodeCALines -v -count=1
```

Expected: PASS（所有 case）。

- [ ] **Step 5: 提交**

```bash
git add internal/bootstrap/adapters.go internal/bootstrap/bootstrap_test.go
git commit -m "feat(bootstrap): add stripLegacyMCCNodeCALines for pwsh profile cleanup

新增 stripLegacyMCCNodeCALines：删除 mcc 块外含 \$mccCa 的非注释行，
保留 mcc 块内、注释、用户其他内容。供 writePwshProfileNodeCA 升级
迁移使用。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: 集成到 `writePwshProfileNodeCA` 阶段 2

**Files:**
- Modify: `internal/bootstrap/adapters.go:688-714` (`writePwshProfileNodeCA` 阶段 2)
- Test: `internal/bootstrap/bootstrap_test.go`（新增 `TestWritePwshProfileNodeCA_LegacyResidual_MigratesToBlock`）

- [ ] **Step 1: 写失败测试**

在 `bootstrap_test.go` 的 `TestWritePwshProfileNodeCA_UserCustomValue_NotOverwritten` 函数之后，新增：

```go
func TestWritePwshProfileNodeCA_LegacyResidual_MigratesToBlock(t *testing.T) {
	home := t.TempDir()
	caPath := writeFile(t, filepath.Join(home, "ca.crt"), "cert")
	withPwshHooks(t, home)

	profile := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	// 预写旧版 mcc 残留：$mccCa 模式 + 中文注释，无 begin/end 标记
	legacy := "# mcc 透明代理：兜底\n" +
		"$mccCa = \"$env:USERPROFILE\\mcc-windows-amd64\\data\\ca.crt\"\n" +
		"if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n"
	writeFile(t, profile, legacy)

	a := &osEnvAdapter{}
	if err := a.writePwshProfileNodeCA(caPath); err != nil {
		t.Fatalf("writePwshProfileNodeCA: %v", err)
	}

	content, _ := os.ReadFile(profile)
	s := string(content)

	// mcc 块已写入
	if !strings.Contains(s, pwshProfileMarkerBegin) || !strings.Contains(s, pwshProfileMarkerEnd) {
		t.Fatal("mcc block should be written")
	}

	// 块外不应再有 $mccCa（旧版残留已迁移）
	begin := strings.Index(s, pwshProfileMarkerBegin)
	end := strings.Index(s, pwshProfileMarkerEnd)
	if begin < 0 || end <= begin {
		t.Fatalf("cannot locate mcc block markers in %q", s)
	}
	outside := s[:begin] + s[end+len(pwshProfileMarkerEnd):]
	if strings.Contains(outside, "$mccCa") {
		t.Errorf("legacy $mccCa should be stripped outside mcc block; outside=%q", outside)
	}

	// 旧版中文注释保留（注释不被 strip）
	if !strings.Contains(outside, "# mcc 透明代理") {
		t.Errorf("legacy comment should be preserved; outside=%q", outside)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/bootstrap -run TestWritePwshProfileNodeCA_LegacyResidual_MigratesToBlock -v -count=1
```

Expected: FAIL。当前 `writePwshProfileNodeCA` 阶段 1（L680）调用 `pwshProfileHasNodeCAVarOutsideMCCBlock` 对旧版残留返回 true（注：Task 1 已让它对 `$mccCa` 模式返回 false，所以阶段 1 不再拦截）。但阶段 2 的 `replaceMarkedBlock` 只追加 mcc 块，不删旧版 `$mccCa` 行 → 测试 `outside contains $mccCa` 失败。

如果 Task 1 尚未合入，会先在阶段 1 因 `ErrUserCustomValue` 失败 —— 确认 Task 1 已完成。

- [ ] **Step 3: 改 `writePwshProfileNodeCA` 阶段 2 调用 strip**

在 `adapters.go` 的 `writePwshProfileNodeCA` 阶段 2 循环（L695-704），把：

```go
		existing, err := readProfile(profile)
		if err != nil {
			lastErr = err
			continue
		}
		updated, changed := replaceMarkedBlock(string(existing), pwshProfileMarkerBegin, pwshProfileMarkerEnd, block)
		if !changed {
			wrote = true
			continue
		}
```

改为：

```go
		existing, err := readProfile(profile)
		if err != nil {
			lastErr = err
			continue
		}
		// 先清理旧版 mcc $mccCa 残留（升级迁移），再写入/更新 mcc 块
		stripped, stripChanged := stripLegacyMCCNodeCALines(string(existing))
		updated, blockChanged := replaceMarkedBlock(stripped, pwshProfileMarkerBegin, pwshProfileMarkerEnd, block)
		if !stripChanged && !blockChanged {
			wrote = true
			continue
		}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/bootstrap -run TestWritePwshProfileNodeCA_LegacyResidual_MigratesToBlock -v -count=1
```

Expected: PASS。

- [ ] **Step 5: 跑既有 `writePwshProfileNodeCA` 全部测试确认无回归**

```bash
go test ./internal/bootstrap -run "TestWritePwshProfileNodeCA|TestReplaceMarkedBlock" -v -count=1
```

Expected: PASS（含 `Idempotent`、`CAPathChanged_UpdatesBlock`、`UserCustomValue_NotOverwritten` 等；特别注意 `Idempotent`：第二次调用时 strip 无残留（changed=false），replaceMarkedBlock 块内容相同（blockChanged=false）→ `!stripChanged && !blockChanged` → `wrote=true; continue`，不重写文件，幂等性保持）。

- [ ] **Step 6: 提交**

```bash
git add internal/bootstrap/adapters.go internal/bootstrap/bootstrap_test.go
git commit -m "fix(bootstrap): migrate legacy pwsh profile to mcc block on write

writePwshProfileNodeCA 阶段 2 在 replaceMarkedBlock 前先调
stripLegacyMCCNodeCALines 清理旧版 \$mccCa 残留。升级场景下
profile 一次性迁移到 mcc 块，marker + setx 持久化路径恢复执行。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: 端到端验证（重跑 verify 场景）

**Files:** 无代码改动；使用既有 `mcc-test.exe` + 真实 profile

- [ ] **Step 1: 构建新版 mcc-test.exe**

```bash
go build -o mcc-test.exe ./cmd/server
```

Expected: 成功，生成 `mcc-test.exe`。

- [ ] **Step 2: 还原 profile 到"含旧版残留"状态**

确认 `~/Documents/PowerShell/Microsoft.PowerShell_profile.ps1` 含旧版 `$mccCa` 残留（即本次验证前的原始状态：L1-3 注释 + L4-5 `$mccCa` 行）。如果已被迁移，从 git 或备份还原。

- [ ] **Step 3: 后台启动 mcc-test（自定义端口避开占用）**

```bash
cp mcc-test.exe /c/Users/wakeya2/mcc-windows-amd64/mcc-test.exe
( cd /c/Users/wakeya2/mcc-windows-amd64 && ./mcc-test.exe -data data -proxy-port 1443 -admin-port 8543 > mcc-test-verify.log 2>&1 )
```

后台运行（`run_in_background=true`）。mcc-test 仍会因 gateway 17487 冲突 `log.Fatalf`，但 bootstrap 在前。

- [ ] **Step 4: 等 ~8s 后观察**

```bash
pwsh -NoProfile -Command 'Start-Sleep 8; Test-Path "C:\Users\wakeya2\mcc-windows-amd64\data\.node-ca-persisted"; Get-Content "C:\Users\wakeya2\mcc-windows-amd64\mcc-test-verify.log" | Select-String "NODE_CA|custom"'
pwsh -NoProfile -Command 'Get-Content "$env:USERPROFILE\Documents\PowerShell\Microsoft.PowerShell_profile.ps1"'
```

Expected（与修复前对比）：
- `.node-ca-persisted` marker **存在**（修复前不存在）
- 日志**无** `⚠ User custom NODE_EXTRA_CA_CERTS detected`
- profile 已迁移：含 `# >>> mcc: ... >>>` / `# <<< mcc <<<` 块；块外**无** `$mccCa` 行；旧版中文注释保留

- [ ] **Step 5: 还原环境**

```bash
rm -f /c/Users/wakeya2/mcc-windows-amd64/mcc-test.exe
rm -f /c/Users/wakeya2/mcc-windows-amd64/mcc-test-verify.log
rm -f /c/Users/wakeya2/mcc-windows-amd64/data/.node-ca-persisted
rm -f /c/Users/wakeya2/mcc-windows-amd64/data/.bootstrap-state
rm -f mcc-test.exe
# profile 已被 mcc-test 迁移到 mcc 块——这是新版期望状态，可选择保留或从备份还原
```

- [ ] **Step 6: 不提交（验证步骤，无代码改动）**

---

## Self-Review

**1. Spec coverage：**
- 设计 §1（检测层 `$mccCa` 例外）→ Task 1 ✅
- 设计 §2（stripLegacyMCCNodeCALines + 阶段 2 调用）→ Task 2 + Task 3 ✅
- 设计 §3（注释保留）→ Task 2 case "legacy $mccCa lines stripped, comment kept" + Task 3 测试断言 `# mcc 透明代理` 保留 ✅
- 设计 测试 1-4 → Task 1 (1,2) + Task 2 (3) + Task 3 (4) ✅
- 设计 验证 → Task 4 ✅

**2. Placeholder scan：** 无 TBD/TODO；所有代码 step 含完整代码；所有 run step 含命令 + expected。

**3. Type consistency：**
- `stripLegacyMCCNodeCALines(content string) (string, bool)` —— Task 2 定义，Task 3 调用签名一致 ✅
- `pwshProfileMarkerBegin/End` —— 既有常量，Task 2/3 复用 ✅
- `replaceMarkedBlock` 返回 `(string, bool)` —— Task 3 用 `blockChanged` 接，匹配 ✅
- Task 3 的 `stripped, stripChanged := stripLegacyMCCNodeCALines(...)` 与 Task 2 签名一致 ✅

**4. 边界：** 既有 `TestWritePwshProfileNodeCA_Idempotent` 在 Task 3 后仍应通过（strip 无残留 + 块未变 → 不重写）—— Step 5 已显式验证。
