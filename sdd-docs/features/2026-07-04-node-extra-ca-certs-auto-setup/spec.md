# Node.js Client CA Trust Auto-Setup Spec

## Amendment: Missing Windows Volume/UNC Root Validation (2026-07-05)

`validateParentChain` must fail closed when traversal reaches a filesystem root that still does not exist. On Windows, an absent drive root or unavailable/nonexistent UNC share cannot be created by `os.MkdirAll`; treating it as creatable permits `setx` to run before profile persistence inevitably fails.

Design:

- Keep `validateParentChain` as the production entry point.
- Extract a small `validateParentChainWithStat` helper that accepts the stat operation, allowing Linux tests to deterministically simulate an absent root without adding mutable package-global hooks.
- When `filepath.Dir(dir) == dir` and the current root still returns `IsNotExist`, return an error rather than success.
- Add a platform-independent unit test for the missing-root decision and a Windows-only integration test that selects an unused drive root and asserts `setxEnvVar` is not called.
- Preserve the existing behavior for a missing profile below an existing directory and for the explicitly accepted non-privileged symlink policy.

Verification: focused red/green tests, `go test ./internal/bootstrap -count=1`, race tests, `go test ./... -count=1`, `go vet`, and Windows/macOS test-binary cross-compilation.

Local page: none (executed automatically by bootstrap at mcc startup)
Proxy entry: `cmd/server/main.go` → `internal/bootstrap`
Reference sources: Node.js TLS docs, Windows registry environment-variable API, macOS `launchctl`, POSIX shell profile conventions
Stack: Go 1.26 stdlib (`os`, `os/exec`, `runtime`, `path/filepath`, `syscall`)
Last updated: 2026-07-06
Progress: 6 / 7 implemented and automatically verified; native end-to-end verification remains

## Overall Analysis (Source Analysis)

### Current Project State

mcc (magic-claude-code) is a Go single-binary transparent proxy. At startup, `internal/bootstrap` configures transparent mode in this order (`bootstrap.go:161-203`, `Executor.Run`):

1. **tryHosts** (`bootstrap.go:205-213`) — edits hosts to map `api.anthropic.com` → `127.0.0.1`
2. **tryTrustCA** (`bootstrap.go:215-227`) — installs mcc's CA into the **operating-system** trust store (Windows `certutil`, macOS `security`, Linux `update-ca-certificates`)
3. **tryPersistEnv** (`bootstrap.go:229-232`) — calls `e.env.PersistRoot(rootDir)` to persist `MCC_ROOT`

The `EnvAdapter` interface (`bootstrap.go:71-74`):

```go
// EnvAdapter abstracts environment persistence.
type EnvAdapter interface {
    PersistRoot(rootDir string) error
}
```

The only implementation, `osEnvAdapter.PersistRoot` (`adapters.go:311-354`):

- Windows: `execWithTimeout("setx", "MCC_ROOT", rootDir)` — sets `MCC_ROOT` only
- macOS/Linux: appends `export MCC_ROOT=...` to zsh/bash/fish profiles (`adapters.go:326-348`, via `resolveShellProfiles` + `writeProfileEntry`)

**Key finding: the current bootstrap never sets `NODE_EXTRA_CA_CERTS` and never touches any pwsh `$PROFILE`.** `NODE_EXTRA_CA_CERTS` appears only in `instructions.go` (`windowsSet("NODE_EXTRA_CA_CERTS", caPath)` at lines 112/147, `export NODE_EXTRA_CA_CERTS=...` at lines 117/152), and `windowsSet` (`instructions.go:280-282`) is a **string-formatting helper** whose output is printed via `fmt.Println` (`bootstrap.go:276-279`) for the user to run manually — it is not executed.

### Node.js CA Trust Behavior (Core Finding)

Node.js TLS defaults to a **bundled CA list** compiled into the binary (Mozilla CA bundle) and **does not read the OS certificate store** — not Windows cert store, not macOS Keychain, not Linux `/etc/ssl/certs`. This is by design (`--use-bundled-ca` is the default; only `--use-openssl-ca` consults the system store).

**`NODE_EXTRA_CA_CERTS`** is read by Node.js **once at process bootstrap** and appends the specified CA file to the TLS trust store. Verified empirically in the user's environment:

- Variable removed at startup, set only at runtime → `SELF_SIGNED_CERT_IN_CHAIN` (Node rejects mcc's CA)
- Variable present at startup → TLS handshake succeeds

**Implications:**

- mcc's transparent-mode step 2 (system CA install) benefits browsers and curl but **is ineffective for Node.js clients (Claude Code)**.
- Claude Code must be told via `NODE_EXTRA_CA_CERTS` to trust mcc's CA file.
- The transparent-ready check (`bootstrap.go:154-157`, `IsTransparentReady`) **does not inspect `NODE_EXTRA_CA_CERTS`**, so mcc falsely reports transparent mode ready for Node clients, which in fact fail with `401 Invalid bearer token`.

### Cross-Platform Environment Variable Persistence

For `NODE_EXTRA_CA_CERTS` to reach **future Node processes**, it must be persisted beyond the current process:

| Platform | Persistence layer | Scope | Mechanism |
| --- | --- | --- | --- |
| Windows | Registry `HKCU\Environment` | Current user | `setx VAR value` or `[Environment]::SetEnvironmentVariable(...,"User")` |
| Windows | Registry `HKLM\SYSTEM\...\Environment` | Machine-wide (admin) | `setx /M VAR value` |
| macOS | `launchctl setenv VAR value` | Current user GUI session | `launchctl setenv` (lost on logout; pair with profile for persistence) |
| macOS | `~/.zshrc` / `~/.zprofile` / `~/.bash_profile` | Login shell | `export VAR=value` |
| Linux | `~/.bashrc` / `~/.profile` / `~/.zshrc` | Login shell | `export VAR=value` |
| Linux | `/etc/profile.d/mcc.sh` | Machine-wide login shell (root) | `export VAR=value` |

**GUI process inheritance pitfall** (verified in the user's environment): On Windows, `explorer.exe` reads the registry once at login and **never refreshes**; GUI apps (Orca, Cursor) inherit explorer's snapshot. If mcc writes the registry after login, the running explorer never sees it, and GUI apps (and their child shells) launched from explorer miss the variable — until explorer restarts or the user logs out/in.

### PowerShell `$PROFILE` Fallback (Empirically Verified)

To cover the GUI-inheritance gap, setting `NODE_EXTRA_CA_CERTS` in pwsh's `$PROFILE` (`Documents\PowerShell\Microsoft.PowerShell_profile.ps1`) is a reliable fallback: pwsh executes the profile on every launch, independent of parent-process inheritance.

**Verified `$PROFILE` content** (tested: clearing the inherited variable, then launching pwsh — the variable was still set by the profile):

```powershell
$mccCa = "$env:USERPROFILE\mcc-windows-amd64\data\ca.crt"
if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }
```

Also verified: Orca launches pwsh without `-NoProfile` (`orca/src/main/providers/windows-shell-args.ts:156-167`, args `-NoLogo -NoExit -EncodedCommand`), with a comment stating "dot-source `$PROFILE`" — so GUI-spawned pwsh terminals execute the profile, bypassing the inheritance gap.

**cmd `AutoRun` verified infeasible**: `HKCU\Software\Microsoft\Command Processor\AutoRun` is write-protected by Windows Defender / EDR (measured: `Set-ItemProperty` returns `Attempted to perform an unauthorized operation` while ordinary HKCU keys are writable). This spec therefore does not configure cmd AutoRun; cmd relies on registry inheritance only.

### Risk Summary

1. Node.js ignores the OS CA store — `NODE_EXTRA_CA_CERTS` is the only way for Claude Code to trust mcc's CA, yet mcc never sets it automatically today.
2. The transparent-ready check omits `NODE_EXTRA_CA_CERTS`, producing a false "ready" for Node clients.
3. GUI process env inheritance is unreliable (explorer does not refresh); writing the registry alone is insufficient — a pwsh `$PROFILE` fallback is required.
4. Windows `setx` affects only **future** processes, not the running one; mcc itself cannot be saved by setx (mcc is a Go proxy and does not need the Node CA, but its client Claude Code does).
5. macOS `launchctl setenv` is lost on logout and must be paired with a profile for persistence.
6. Profile writes must be idempotent (the existing `MCC_ROOT` dedup logic, `profileHasEquivalentEntry` / `profileHasExactEntry`, can be reused).
7. If the CA path changes (upgrade, migration), a stale `NODE_EXTRA_CA_CERTS` pointing at a missing file can break other Node programs — a fingerprint marker or path comparison must detect staleness and update.

## Development Checklist

| Order | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Implemented | Extend `EnvAdapter` interface + bootstrap integration | `bootstrap.go` (interface, `tryPersistNodeCA`, `Result` field) | Unit tests with mock adapters pass |
| 2 | Implemented | Windows implementation (registry + pwsh `$PROFILE`) | `adapters.go` + `node_ca_lookup_windows.go` | Unit tests and Windows cross-compilation pass; native verification remains in task 7 |
| 3 | Implemented | macOS implementation (launchctl + zsh/bash profile) | `adapters.go` + `node_ca_lookup_darwin.go` | Unit tests and macOS cross-compilation pass; native verification remains in task 7 |
| 4 | Implemented | Linux implementation (profile; `/etc/profile.d` is non-goal) | `adapters.go` + `node_ca_lookup_other.go` | Linux unit and race tests pass |
| 5 | Implemented | Idempotent detection and staleness via fingerprint marker | `bootstrap.go` + `adapters.go` (marker read/write) | Repeat-run, CA-change, absolute-path, and prior-managed-path migration tests pass |
| 6 | Implemented | Unit tests covering all platforms + idempotency + staleness | `bootstrap_test.go` | Focused, full, and race suites pass |
| 7 | Pending | End-to-end manual verification (all platforms) | Verification record | Run mcc on each native platform; confirm Node client sees the variable |

## Requirements

### Deliverables

1. Extend `EnvAdapter` with `PersistNodeCACert(caCertPath string) error`; all implementations (OS adapter + existing mocks) must implement it.
2. `osEnvAdapter.PersistNodeCACert` per platform:
   - **Windows**: ① `setx NODE_EXTRA_CA_CERTS <caCertPath>` (writes `HKCU\Environment`; REG_EXPAND_SZ not required, `setx` default REG_SZ is fine); ② append an idempotent block to pwsh `$PROFILE` (`%USERPROFILE%\Documents\PowerShell\Microsoft.PowerShell_profile.ps1`) that sets the variable.
   - **macOS**: ① `launchctl setenv NODE_EXTRA_CA_CERTS <caCertPath>`; ② append `export NODE_EXTRA_CA_CERTS=...` to `~/.zshrc` (or the detected shell profile).
   - **Linux**: append `export NODE_EXTRA_CA_CERTS=...` to `~/.bashrc` / `~/.profile` / `~/.zshrc` (selected by `$SHELL`).
3. `bootstrap.go` calls `tryPersistNodeCA` in the transparent-mode path (immediately after `tryTrustCA`, since it depends on the CA file), recording the outcome in `Result.NodeCAResult`.
4. `IsTransparentReady` does **not** hard-depend on `NODE_EXTRA_CA_CERTS` (preserve system-level transparent semantics), but `LogResult` prints a clear notice when `NodeCAResult` fails: "Node clients (e.g., Claude Code) need this variable to trust the mcc CA."
5. Idempotency: repeated runs do not append duplicate profile lines (reuse `profileHasExactEntry`); when the CA path changes (fingerprint mismatch), the entry is updated.
6. Unit tests cover all three platform branches (interface-injected mocks), idempotency, staleness, and profile-content correctness.
7. End-to-end verification record: after running mcc on each platform, a freshly opened shell (including GUI-spawned pwsh) has `NODE_EXTRA_CA_CERTS` set correctly.

### Directory Structure

```text
internal/bootstrap/
  bootstrap.go           (modify: EnvAdapter interface, Result.NodeCAResult, tryPersistNodeCA, LogResult notice)
  adapters.go            (modify: osEnvAdapter.PersistNodeCACert per-platform impl + pwsh profile writer + marker file)
  bootstrap_test.go      (modify: mockEnv implements PersistNodeCACert; add tryPersistNodeCA tests)
  adapters_test.go       (add or modify: PersistNodeCACert per-platform + idempotency + staleness tests)
```

### Data Model

```go
// bootstrap.go — EnvAdapter interface extension
type EnvAdapter interface {
    PersistRoot(rootDir string) error
    // PersistNodeCACert persists NODE_EXTRA_CA_CERTS pointing at mcc's CA file
    // into the current user's shell/desktop-session environment, so that
    // Node.js clients launched later trust mcc.
    PersistNodeCACert(caCertPath string) error
}

// bootstrap.go — Result addition
type Result struct {
    // ...existing fields...
    NodeCAResult StepResult // NODE_EXTRA_CA_CERTS persistence outcome
}
```

pwsh `$PROFILE` block appended on Windows (verified):

```powershell
# >>> mcc: Node.js CA trust (auto-managed, do not edit) >>>
$mccCa = "$env:USERPROFILE\<relative CA path>"
if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }
# <<< mcc <<<
```

POSIX shell profile line appended on macOS/Linux (reuse existing `shellExportEntry`):

```bash
export NODE_EXTRA_CA_CERTS="<absolute CA path>"  # mcc-managed
```

### Constraints

1. Do not break `EnvAdapter.PersistRoot` semantics or existing tests.
2. Use `setx` for the Windows registry (consistent with `PersistRoot`, no new dependency); use `os.OpenFile` append for pwsh `$PROFILE` (consistent with POSIX profile writes).
3. Do not write `HKLM` (machine-level) — avoid admin requirements; user-level `HKCU` covers the current user's Node clients.
4. Do not touch `HKCU\Software\Microsoft\Command Processor\AutoRun` (Defender write-protects it).
5. Do not write `/Library/LaunchDaemons` on macOS (avoid root); user-level `launchctl setenv` + user profile only.
6. Profile writes must be idempotent: skip if an equivalent line exists (reuse `profileHasEquivalentEntry` / `profileHasExactEntry`); include an mcc marker comment for future cleanup.
7. CA path changes (post-upgrade relocation) must be detected and updated — use a CA-fingerprint marker file (same idea as `tryTrustCA`'s `.ca-trust-installed`); rewrite on fingerprint mismatch.
8. No step may panic or block proxy startup (best-effort, matching current bootstrap philosophy); failures go into `NodeCAResult.Err` and surface in `LogResult`.
9. `setx` / `launchctl setenv` affect only future processes, not the running mcc — this is expected (mcc itself does not need the Node CA).
10. Do not execute host writes from inside a Docker container (preserve the `bootstrap.go:178` Docker-boundary check; skip `tryPersistNodeCA` under Docker, as with `tryHosts`/`tryTrustCA`).

### Edge Cases

1. CA file not yet generated (`tryTrustCA` failed) — `tryPersistNodeCA` returns a clear error and does not write a stale path.
2. pwsh not installed (`pwsh.exe` / `pwsh` not in PATH) — skip `$PROFILE`, write registry/launchctl only; record partial success.
3. `$PROFILE` directory does not exist (user never opened pwsh) — `os.MkdirAll` creates `Documents\PowerShell\` (as POSIX `MkdirAll(filepath.Dir(p))` does).
4. Profile already contains an mcc-managed block with a different path (CA upgraded) — fingerprint mismatch triggers update: remove the old mcc-managed block/line, write the new one.
5. Profile contains a user-authored `NODE_EXTRA_CA_CERTS` (not mcc-managed) — do not overwrite; log a warning "detected user-defined value; mcc will not overwrite it — please verify it points at the mcc CA."
6. `setx` fails (registry permission/EDR) — degrade to pwsh `$PROFILE` only; `NodeCAResult` records partial.
7. macOS `launchctl` absent (not macOS) — skip, profile only.
8. `$SHELL` unset or unknown — reuse `resolveShellProfiles` fallback (`~/.profile` → `~/.bashrc`).
9. fish shell — reuse `shellExportEntry` fish syntax (`set -gx NODE_EXTRA_CA_CERTS ...`).
10. Docker environment — skip `tryPersistNodeCA` entirely (in-container profile edits are meaningless for the host).

### Non-Goals

1. No machine-level (`HKLM` / `/etc`) changes — current-user environment only.
2. No cmd `AutoRun` (Defender write-protects it; verified infeasible).
3. No restarting explorer.exe or forcing logout (documentation/log only: "log out/in or restart explorer for the registry to take effect").
4. No edits to Claude Code's `~/.claude/settings.json` (user config; and settings.json env is ineffective for Node's own TLS — verified, runtime injection is too late).
5. No attempt to make mcc's own process read `NODE_EXTRA_CA_CERTS` — mcc is Go, not Node.
6. No additional work for Tunnel/Gateway modes — those modes' instructions already tell users to set `NODE_EXTRA_CA_CERTS`; this spec focuses on auto-filling the gap in transparent mode.

## Task Details

### Task 1: Extend EnvAdapter Interface and Bootstrap Integration

#### Requirements

**Objective** - Add `PersistNodeCACert` to the `EnvAdapter` interface and invoke it in the bootstrap flow, recording the outcome in `Result`.

**Outcomes** - `bootstrap.go`'s `EnvAdapter` interface includes `PersistNodeCACert(caCertPath string) error`; `Result` gains `NodeCAResult StepResult`; a new `tryPersistNodeCA` is called from `Run` (transparent mode, non-Docker, CA ready); `IsTransparentReady` semantics are unchanged (NodeCA is not a hard-ready condition), but `LogResult` prints a notice when NodeCA fails.

**Evidence** - Unit tests: a mock `EnvAdapter` records that `PersistNodeCACert` was called with the correct argument; Docker skips it; CA-absent skips it.

**Constraints** - Preserve `Run`'s best-effort philosophy (no step failure blocks startup); call `tryPersistNodeCA` only after `tryTrustCA` succeeds (depends on the CA file).

**Edge Cases** - Docker (skip); CA file missing (skip and log); mock returns error (recorded in `NodeCAResult.Err`, proxy still starts).

**Verification** - `go test ./internal/bootstrap/ -run TestTryPersistNodeCA -v`.

#### Plan

1. Modify `bootstrap.go`'s `EnvAdapter` interface to add `PersistNodeCACert(caCertPath string) error`.
2. Add `NodeCAResult StepResult` to `Result`.
3. Add the `tryPersistNodeCA` method:

```go
// tryPersistNodeCA persists NODE_EXTRA_CA_CERTS so that Node.js clients
// launched later (e.g., Claude Code) trust mcc's CA. Called only in
// transparent mode, non-Docker, after the CA file is ready.
func (e *Executor) tryPersistNodeCA() StepResult {
    if e.caCertPath == "" {
        return StepResult{Attempted: false}
    }
    if _, err := os.Stat(e.caCertPath); err != nil {
        return StepResult{Attempted: false, Err: err}
    }
    if hasNodeCAMarker(e.dataDir, e.caCertPath) {
        return StepResult{Success: true}
    }
    err := e.env.PersistNodeCACert(e.caCertPath)
    if err == nil {
        writeNodeCAMarker(e.dataDir, e.caCertPath)
    }
    return StepResult{Attempted: true, Success: err == nil, Err: err}
}
```

4. In `Run`'s transparent branch (`bootstrap.go:177-187`), after `tryTrustCA`:

```go
result.HostsResult = e.tryHosts()
result.TrustResult = e.tryTrustCA()
// New: once the CA is ready, persist Node-client CA trust
if result.TrustResult.Success {
    result.NodeCAResult = e.tryPersistNodeCA()
}
```

5. Extend `LogResult` (`bootstrap.go:245-281`) to print the Node CA step:

```go
printStep(e.locale, "NODE_CA", r.NodeCAResult)
```

And extend `transparentSuccessInstructions` (`instructions.go:23-51`) to add a notice when `NodeCAResult.Attempted && !NodeCAResult.Success`:

```
- Note: NODE_EXTRA_CA_CERTS persistence failed; Node.js clients (e.g., Claude Code) may be unable to trust the mcc CA
```

#### Verification

- [ ] `EnvAdapter` interface includes `PersistNodeCACert`.
- [ ] `tryPersistNodeCA` is called when CA is ready; mock records the correct argument.
- [ ] Skipped under Docker / when CA is absent.
- [ ] `NodeCAResult` failure does not block proxy startup.
- [ ] `go test ./internal/bootstrap/ -v` is green.

### Task 2: Windows Implementation (Registry + pwsh `$PROFILE`)

#### Requirements

**Objective** - The Windows branch of `osEnvAdapter.PersistNodeCACert`: ① `setx NODE_EXTRA_CA_CERTS <path>` to write the user registry; ② append an idempotent block to pwsh `$PROFILE`.

**Outcomes** - After running mcc on Windows: new processes (including GUI-spawned) inherit `NODE_EXTRA_CA_CERTS` from the registry; pwsh terminals (including GUI-spawned ones like Orca's) also have it via the `$PROFILE` fallback.

**Evidence** - Verified in the user's environment: appending `$env:NODE_EXTRA_CA_CERTS = $mccCa` to pwsh `$PROFILE` makes the variable appear even when the inherited environment was cleared; after `setx`, an explorer restart reads the correct value.

**Constraints** - Use `setx` (consistent with `PersistRoot`, no Win32 API dependency); make pwsh profile writes idempotent (marker block + path comparison); do not touch `AutoRun` (Defender-protected).

**Edge Cases** - pwsh not installed (skip profile, setx only); `$PROFILE` directory missing (MkdirAll); profile already has an mcc-managed block with a different path (update); profile has a user-defined non-mcc value (do not overwrite, warn); setx fails (degrade to profile only).

**Verification** - Unit tests cover setx command construction, profile-block generation, idempotency, update; manual: after running, `echo $env:NODE_EXTRA_CA_CERTS` in a new pwsh shows the CA path.

#### Plan

1. Add the Windows branch to `PersistNodeCACert` in `adapters.go`:

```go
func (a *osEnvAdapter) PersistNodeCACert(caCertPath string) error {
    switch runtime.GOOS {
    case "windows":
        return a.persistNodeCACertWindows(caCertPath)
    case "darwin":
        return a.persistNodeCACertDarwin(caCertPath)
    default:
        return a.persistNodeCACertPOSIX(caCertPath)
    }
}

func (a *osEnvAdapter) persistNodeCACertWindows(caCertPath string) error {
    var setxErr error
    // ① setx writes the user registry (affects future new processes)
    if out, err := execWithTimeout("setx", "NODE_EXTRA_CA_CERTS", caCertPath); err != nil {
        setxErr = fmt.Errorf("setx NODE_EXTRA_CA_CERTS: %w: %s", err, decodeCmdOutput(out))
        // Do not return; still try the profile fallback.
    }

    // ② pwsh $PROFILE fallback (covers the GUI-inheritance gap)
    profileErr := a.writePwshProfileNodeCA(caCertPath)

    if setxErr != nil && profileErr != nil {
        return fmt.Errorf("setx: %v; profile: %w", setxErr, profileErr)
    }
    return nil
}
```

2. Implement the pwsh `$PROFILE` writer (idempotent, marker-block):

```go
const (
    pwshProfileMarkerBegin = "# >>> mcc: Node.js CA trust (auto-managed, do not edit) >>>"
    pwshProfileMarkerEnd   = "# <<< mcc <<<"
)

func (a *osEnvAdapter) writePwshProfileNodeCA(caCertPath string) error {
    home, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("user home dir: %w", err)
    }
    // Probe pwsh availability
    if _, err := exec.LookPath("pwsh.exe"); err != nil {
        if _, err2 := exec.LookPath("powershell.exe"); err2 != nil {
            return nil // pwsh not installed; skip (not an error)
        }
        // Only Windows PowerShell 5.1: profile path differs (handled below)
    }
    // Two candidate profiles: PowerShell 7 and Windows PowerShell 5.1
    candidates := []string{
        filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
        filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
    }

    // CA path relative to home (profile restores via $env:USERPROFILE; survives machine moves)
    rel := strings.TrimPrefix(caCertPath, home+string(os.PathSeparator))
    block := fmt.Sprintf("%s\n"+
        "$mccCa = \"$env:USERPROFILE\\%s\"\n"+
        "if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n"+
        "%s\n", pwshProfileMarkerBegin, rel, pwshProfileMarkerEnd)

    var lastErr error
    wrote := false
    for _, profile := range candidates {
        existing, _ := os.ReadFile(profile)
        updated, changed := replaceMarkedBlock(string(existing), pwshProfileMarkerBegin, pwshProfileMarkerEnd, block)
        if !changed && hasExactValueInPwshBlock(string(existing), caCertPath) {
            wrote = true // equivalent block already present
            break
        }
        if err := os.MkdirAll(filepath.Dir(profile), 0755); err != nil {
            lastErr = err
            continue
        }
        if err := os.WriteFile(profile, []byte(updated), 0644); err != nil {
            lastErr = err
            continue
        }
        wrote = true
        break
    }
    if !wrote && lastErr != nil {
        return lastErr
    }
    return nil
}

// replaceMarkedBlock replaces content between begin..end markers with newBlock.
// If the markers are absent, it appends. "changed" reports whether a write is needed.
func replaceMarkedBlock(content, begin, end, newBlock string) (string, bool) {
    bi := strings.Index(content, begin)
    ei := strings.Index(content, end)
    if bi >= 0 && ei > bi {
        existing := content[bi : ei+len(end)]
        if existing == strings.TrimRight(newBlock, "\n") {
            return content, false
        }
        return content[:bi] + newBlock + content[ei+len(end):], true
    }
    if content != "" && !strings.HasSuffix(content, "\n") {
        content += "\n"
    }
    return content + newBlock, true
}
```

3. Fingerprint marker (Task 5; placeholder call here):

```go
// hasNodeCAMarker / writeNodeCAMarker — see Task 5.
```

#### Verification

- [ ] `setx` arguments are correct (`NODE_EXTRA_CA_CERTS`, caCertPath).
- [ ] pwsh profile block contains the correct `$env:USERPROFILE` restoration and `Test-Path` guard.
- [ ] Repeated runs do not append duplicates (marker-block detection).
- [ ] CA path change updates the marker block.
- [ ] pwsh absent → profile skipped without error.
- [ ] Manual: after running mcc, `echo $env:NODE_EXTRA_CA_CERTS` in a new pwsh prints the CA path.

### Task 3: macOS Implementation (launchctl + zsh/bash profile)

#### Requirements

**Objective** - macOS branch: ① `launchctl setenv NODE_EXTRA_CA_CERTS <path>` injects into the current GUI session; ② append `export NODE_EXTRA_CA_CERTS=...` to the user's shell profile.

**Outcomes** - After running mcc on macOS: processes spawned within the current GUI session inherit the variable; future login shells read it from the profile.

**Evidence** - `launchctl setenv` is the standard macOS mechanism for user-level GUI-session env (verifiable via `launchctl getenv`); profile writes reuse the existing `shellExportEntry` + `writeProfileEntry` already proven by `PersistRoot`.

**Constraints** - `launchctl setenv` is lost on logout and must be paired with a profile; do not write a LaunchAgent plist (over-configuring; the profile is sufficient).

**Edge Cases** - Not macOS (branch not reached); `launchctl` absent (skip, profile only); `$SHELL` is zsh/bash/fish/unknown (reuse `resolveShellProfiles`); profile already has an equivalent line (skip).

**Verification** - Unit tests cover launchctl command construction and profile-line generation (per-shell syntax); manual: after running, both `launchctl getenv NODE_EXTRA_CA_CERTS` and `echo $NODE_EXTRA_CA_CERTS` in a new shell show the value.

#### Plan

1. Implement the macOS branch:

```go
func (a *osEnvAdapter) persistNodeCACertDarwin(caCertPath string) error {
    // ① launchctl setenv injects into the current GUI session (affects Dock/Launchpad apps)
    if _, err := exec.LookPath("launchctl"); err == nil {
        if out, err := execWithTimeout("launchctl", "setenv", "NODE_EXTRA_CA_CERTS", caCertPath); err != nil {
            // Non-fatal; continue to profile
            log.Printf("[Bootstrap] launchctl setenv failed: %v: %s", err, decodeCmdOutput(out))
        }
    }

    // ② Profile persistence (reuse the POSIX profile path from PersistRoot)
    return a.writePOSIXProfileNodeCA(caCertPath)
}
```

2. POSIX profile writer (shared by macOS/Linux):

```go
func (a *osEnvAdapter) writePOSIXProfileNodeCA(caCertPath string) error {
    shell := os.Getenv("SHELL")
    home, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("user home dir: %w", err)
    }
    // Reuse shellExportEntry (already supports bash/zsh export and fish set -gx)
    entry := shellExportEntry(shell, "NODE_EXTRA_CA_CERTS", caCertPath)
    profiles := resolveShellProfiles(shell, home)

    openProfile := func(p string) (writeCloser, error) {
        if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
            return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
        }
        return os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
    }

    var lastErr error
    for _, profile := range profiles {
        if existing, rErr := os.ReadFile(profile); rErr == nil {
            content := string(existing)
            if profileHasEquivalentEntry(shell, content, "NODE_EXTRA_CA_CERTS", caCertPath) ||
                profileHasExactEntry(content, entry) {
                return nil // equivalent entry already present
            }
        }
        if err := writeProfileEntry(openProfile, profile, entry); err != nil {
            lastErr = err
            continue
        }
        return nil
    }
    if lastErr != nil {
        return lastErr
    }
    return fmt.Errorf("no profile file writable (tried %v)", profiles)
}
```

#### Verification

- [ ] `launchctl setenv` arguments are correct.
- [ ] Profile line uses `shellExportEntry` (zsh/bash `export`, fish `set -gx`).
- [ ] Repeated runs do not append duplicates (`profileHasEquivalentEntry` / `profileHasExactEntry`).
- [ ] Manual: `launchctl getenv NODE_EXTRA_CA_CERTS` returns the CA path; `echo $NODE_EXTRA_CA_CERTS` in a new zsh/bash shows the value.

### Task 4: Linux Implementation (profile; `/etc/profile.d` is a non-goal)

#### Requirements

**Objective** - Linux branch: append `export NODE_EXTRA_CA_CERTS=...` to the user's shell profile (machine-level `/etc/profile.d` is a non-goal; requires root, out of scope).

**Outcomes** - After running mcc on Linux, future login shells see `NODE_EXTRA_CA_CERTS`.

**Evidence** - Reuses `PersistRoot`'s proven POSIX profile-write path (`resolveShellProfiles` + `writeProfileEntry`); existing tests like `TestPersistRoot_DeduplicatesExistingEntry` demonstrate the idempotency works.

**Constraints** - Touch only user-level profiles (`~/.bashrc`, etc.); do not touch `/etc`; fallback to `~/.profile` → `~/.bashrc` for unknown shells (existing `resolveShellProfiles` behavior).

**Edge Cases** - `$SHELL` unset (fallback); fish (`set -gx`); profile missing (create it, including parent dirs); equivalent line present (skip); no write permission (record and continue).

**Verification** - Unit tests cover bash/zsh/fish/unknown profile selection and line generation; manual: after running, a new shell has the variable.

#### Plan

1. Linux branch reuses macOS's `writePOSIXProfileNodeCA`:

```go
// persistNodeCACertPOSIX is the switch default; calls writePOSIXProfileNodeCA from Task 3.
func (a *osEnvAdapter) persistNodeCACertPOSIX(caCertPath string) error {
    return a.writePOSIXProfileNodeCA(caCertPath)
}
```

2. (Optional enhancement, non-goal for this phase) If machine-level is needed later, add root detection + `/etc/profile.d/mcc-node-ca.sh`, but that requires a sudo prompt and is out of scope.

#### Verification

- [ ] bash/zsh/fish profile paths are correct.
- [ ] Line syntax matches the shell (`export` vs `set -gx`).
- [ ] Repeated runs are idempotent.
- [ ] Manual: `echo $NODE_EXTRA_CA_CERTS` in a new bash/zsh shows the value.

### Task 5: Idempotent Detection and Staleness via Fingerprint Marker

#### Requirements

**Objective** - Write a fingerprint marker file in `dataDir` recording the CA content hash from the last persistence; skip on repeated runs when the hash matches; rewrite when it changes.

**Outcomes** - `hasNodeCAMarker(dataDir, caCertPath) bool`, `writeNodeCAMarker(dataDir, caCertPath)`; `tryPersistNodeCA` checks the marker to skip; CA regeneration (fingerprint mismatch) triggers a rewrite.

**Evidence** - Mirrors `tryTrustCA`'s `.ca-trust-installed` marker (`bootstrap.go:215-227`), already proven by existing tests.

**Constraints** - The marker stores the CA file's SHA256 fingerprint (not the path), so content changes with a stable path are still detected; the marker file lives in `dataDir` (where mcc has write access).

**Edge Cases** - Marker missing (first run, proceed); fingerprint matches (skip); mismatch (CA upgraded, rewrite + update marker); marker unreadable (treat as absent, proceed).

**Verification** - Unit tests: first-run writes marker, match skips, CA change rewrites.

#### Plan

1. Implement the fingerprint marker (mirroring `hasCATrustMarker` / `writeCATrustMarker`):

```go
const nodeCAMarkerName = ".node-ca-persisted"

func hasNodeCAMarker(dataDir, caCertPath string) bool {
    fp, err := caFingerprint(caCertPath)
    if err != nil {
        return false
    }
    markerPath := filepath.Join(dataDir, nodeCAMarkerName)
    data, err := os.ReadFile(markerPath)
    if err != nil {
        return false
    }
    return strings.TrimSpace(string(data)) == fp
}

func writeNodeCAMarker(dataDir, caCertPath string) {
    fp, err := caFingerprint(caCertPath)
    if err != nil {
        return
    }
    markerPath := filepath.Join(dataDir, nodeCAMarkerName)
    _ = os.WriteFile(markerPath, []byte(fp), 0644)
}

func caFingerprint(caCertPath string) (string, error) {
    data, err := os.ReadFile(caCertPath)
    if err != nil {
        return "", err
    }
    sum := sha256.Sum256(data)
    return hex.EncodeToString(sum[:]), nil
}
```

2. (If `internal/bootstrap` already exposes a `caFingerprint` helper, reuse it; otherwise add it here with tests.)

#### Verification

- [ ] First run writes `.node-ca-persisted`.
- [ ] Second run (CA unchanged) hits the marker and skips the actual write.
- [ ] After CA content changes, fingerprint mismatch triggers a rewrite and updates the marker.

### Task 6: Unit Tests

#### Requirements

**Objective** - Cover all three platforms' `PersistNodeCACert`, idempotency, staleness, and profile-content correctness.

**Outcomes** - New tests in `adapters_test.go` (or `bootstrap_test.go`): Windows setx + pwsh profile, macOS launchctl + profile, Linux profile, idempotent skip, CA-change rewrite, pwsh-absent skip, user-custom-value-not-overwritten.

**Evidence** - `go test ./internal/bootstrap/ -v -race` is green.

**Constraints** - Inject mocks via the interface (like the existing `mockEnv`); isolate real setx/launchctl calls via `t.Setenv` or mocked exec; isolate profile writes via `t.TempDir()`.

**Edge Cases** - See each task's Edge Cases.

**Verification** - `go test ./internal/bootstrap/... -v -race -cover`.

#### Plan

1. `mockEnv` implements `PersistNodeCACert`, recording calls and arguments.
2. Add `TestPersistNodeCACert_Windows_SetxAndProfile` (mock exec to verify the setx command + pwsh profile content).
3. Add `TestPersistNodeCACert_Darwin_LaunchctlAndProfile`.
4. Add `TestPersistNodeCACert_POSIX_ProfilePerShell` (bash/zsh/fish/unknown).
5. Add `TestWritePwshProfile_Idempotent` (repeat writes do not append).
6. Add `TestWritePwshProfile_CAPathChanged_UpdatesBlock`.
7. Add `TestWritePwshProfile_UserCustomValue_NotOverwritten`.
8. Add `TestNodeCAMarker_MatchSkip_MismatchRewrite`.

#### Verification

- [ ] All new tests pass.
- [ ] `go test ./internal/bootstrap/ -v -race` is green.
- [ ] Coverage does not decrease.

### Task 7: End-to-End Manual Verification (All Platforms)

#### Requirements

**Objective** - Run mcc on Windows / macOS / Linux and confirm Node clients (Claude Code in pwsh/bash) receive `NODE_EXTRA_CA_CERTS` and trust mcc's CA.

**Outcomes** - A verification record per platform: environment, steps, results (including the GUI-spawned-terminal scenario).

**Evidence** - After running mcc on each platform: ① a new shell's `echo $env:NODE_EXTRA_CA_CERTS` / `echo $NODE_EXTRA_CA_CERTS` outputs the CA path; ② on Windows, a pwsh spawned from a GUI app (e.g., Orca) also has it; ③ Claude Code no longer reports `401 Invalid bearer token`.

**Constraints** - Do not leak API keys; record the CA path and shell type; cover at least Windows pwsh + macOS zsh + Linux bash.

**Edge Cases** - explorer not restarted (registry not yet inherited, but the pwsh profile covers it); pwsh not installed (setx only, no profile fallback); CA upgraded and the old profile line is updated.

**Verification** - At minimum: Windows (including Orca-spawned pwsh) + macOS + Linux, one full-chain run each.

#### Plan

1. **Windows**:
   - Run mcc (first time, or after a CA change).
   - Open a new pwsh (from the Start menu) → `echo $env:NODE_EXTRA_CA_CERTS` should print the CA path.
   - Open a pwsh spawned by Orca → it should also have the value (profile fallback).
   - Run `claude` in that pwsh → no more 401.
   - Record `Get-ItemProperty HKCU:\Environment` for `NODE_EXTRA_CA_CERTS` (type and value).
2. **macOS**:
   - Run mcc.
   - `launchctl getenv NODE_EXTRA_CA_CERTS` should print the value.
   - Open a new Terminal (zsh) → `echo $NODE_EXTRA_CA_CERTS` should print the value.
   - A shell spawned from a Dock-launched GUI app → should have it.
3. **Linux**:
   - Run mcc.
   - Open a new bash → `echo $NODE_EXTRA_CA_CERTS` should print the value.
   - Verify `~/.bashrc` contains `export NODE_EXTRA_CA_CERTS=...` (mcc-managed marker).
4. Record each platform's mcc output (`NODE_CA` step status) and the actual variable value.

#### Verification

- [ ] Windows: new pwsh + Orca-spawned pwsh both have the variable; claude does not 401.
- [ ] macOS: launchctl + new zsh both have the variable.
- [ ] Linux: new bash has the variable; `~/.bashrc` contains the mcc-managed line.
- [ ] Repeated mcc runs do not produce duplicate profile lines.
- [ ] After a CA upgrade, the profile line is updated to the new path.
