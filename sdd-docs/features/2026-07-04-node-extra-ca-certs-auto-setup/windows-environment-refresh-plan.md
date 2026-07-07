# Windows Environment Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Notify the Windows shell after persisting `NODE_EXTRA_CA_CERTS`, and give users accurate restart guidance without writing user configuration from an elevated setup script.

**Architecture:** Keep `setx` and PowerShell profile persistence in `osEnvAdapter`. Add an injectable cross-platform hook backed by `SendMessageTimeoutW` on Windows, classify notification failures as partial success, and render actionable restart/sign-out instructions. Keep the privileged host setup script limited to hosts and system CA work.

**Tech Stack:** Go 1.26, `golang.org/x/sys/windows`, Win32 `WM_SETTINGCHANGE`, PowerShell, Go tests.

---

### Task 1: Windows environment-change broadcast

**Files:**
- Create: `internal/bootstrap/environment_refresh_windows.go`
- Create: `internal/bootstrap/environment_refresh_other.go`
- Modify: `internal/bootstrap/adapters.go:30-40,432-477`
- Modify: `internal/bootstrap/bootstrap.go:20-28`
- Test: `internal/bootstrap/bootstrap_test.go`

- [ ] **Step 1: Write failing adapter tests**

Add tests that replace `setxEnvVar` and `broadcastEnvironmentChange`, then assert: successful `setx` calls the broadcast once; failed `setx` does not broadcast; failed broadcast still writes the PowerShell profile and returns an error matching both `ErrPartialSuccess` and `ErrEnvironmentRefresh`.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/bootstrap -run 'TestPersistNodeCACert_Windows_.*Broadcast' -count=1`

Expected: compilation failure because `broadcastEnvironmentChange` and `ErrEnvironmentRefresh` do not exist.

- [ ] **Step 3: Add the hook and error classification**

Add to `bootstrap.go`:

```go
ErrEnvironmentRefresh = errors.New("Windows environment refresh failed")
```

Add to `adapters.go`:

```go
var broadcastEnvironmentChange = broadcastEnvironmentChangeOS
```

Call the hook only after `setx` succeeds. If it fails, keep writing profiles and return an error wrapping `ErrPartialSuccess` and `ErrEnvironmentRefresh`.

- [ ] **Step 4: Implement Win32 broadcast**

In the Windows build-tagged file, call `SendMessageTimeoutW(HWND_BROADCAST, WM_SETTINGCHANGE, 0, "Environment", SMTO_ABORTIFHUNG, 5000, ...)` and treat a zero return as an error. In the non-Windows file, return nil so adapter tests remain portable.

- [ ] **Step 5: Verify GREEN**

Run: `go test ./internal/bootstrap -run 'TestPersistNodeCACert_Windows_.*Broadcast' -count=1`

Expected: PASS.

### Task 2: Restart and sign-out guidance

**Files:**
- Modify: `internal/bootstrap/instructions.go:24-79`
- Test: `internal/bootstrap/bootstrap_test.go`

- [ ] **Step 1: Write failing instruction tests**

Add table-driven Chinese and English assertions:

```go
Result{SelectedMode: ModeTransparent, NodeCAResult: StepResult{Attempted: true, Success: true}}
```

must mention fully restarting Orca/Node clients; a partial result wrapping `ErrEnvironmentRefresh` must mention signing out and back in.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/bootstrap -run 'TestGenerateInstructions_.*EnvironmentRefresh' -count=1`

Expected: FAIL because current instructions contain neither restart nor sign-out guidance.

- [ ] **Step 3: Implement messages**

For successful Node CA persistence, append a concise client restart note. Before the generic partial-success branch, detect `errors.Is(r.NodeCAResult.Err, ErrEnvironmentRefresh)` and append a sign-out/sign-in fallback in both locales.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/bootstrap -run 'TestGenerateInstructions_.*EnvironmentRefresh' -count=1`

Expected: PASS.

### Task 3: Correct the elevated setup script guidance

**Files:**
- Modify: `scripts/setup-host.ps1:144-162`
- Create: `scripts/setup_host_ps1_test.go`

- [ ] **Step 1: Write a failing script-content test**

Read `setup-host.ps1` and assert it does not contain `setx NODE_EXTRA_CA_CERTS`, and does contain guidance to start mcc as a normal user and restart Orca.

- [ ] **Step 2: Verify RED**

Run: `go test ./scripts -run TestSetupHostPS1_NodeCAGuidance -count=1`

Expected: FAIL because the script still prints the `setx` command and lacks the normal-user sequence.

- [ ] **Step 3: Update script messages**

Replace the `setx` prompt with:

```powershell
Write-Info "下一步: 关闭管理员 PowerShell，以普通用户启动一次 mcc，让它自动配置 NODE_EXTRA_CA_CERTS。"
Write-Info "mcc 启动完成后，请完全退出并重新启动 Orca；若仍未生效，请注销并重新登录 Windows。"
```

Change the final success text to state that system-level hosts and CA configuration is complete.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./scripts -run TestSetupHostPS1_NodeCAGuidance -count=1`

Expected: PASS.

### Task 4: Full verification

**Files:**
- Verify all changed files.

- [ ] Run `gofmt` on Go files.
- [ ] Run `go test ./internal/bootstrap -count=1`.
- [ ] Run `go test -race ./internal/bootstrap -count=1`.
- [ ] Run `go test ./... -count=1`.
- [ ] Run `go vet ./...`.
- [ ] Run `GOOS=windows GOARCH=amd64 go test ./internal/bootstrap -c -o /tmp/bootstrap-windows.test.exe`.
- [ ] Run `git diff --check` and inspect `git status --short`.
