# Startup Message i18n and Configuration Hint Improvement Spec

Local page: `cmd/server/main.go` startup output / `internal/admin/` admin panel hints  
Proxy entry: None (admin service :8442)  
Reference sources: System locale detection (`LANG`/`LC_ALL`), Claude Code official docs  
Tech stack: Go 1.26 standard library  
Last updated: 2026-06-15  
Progress: 4 / 4 completed

## Overall Analysis

### Problem

All configuration hints and log messages printed by `mcc` at startup are hardcoded in Chinese, with four issues:

1. **Logs only in Chinese**: All `fmt.Println`, `fmt.Printf`, and `log.Printf` calls in `cmd/server/main.go` use Chinese. English-environment users (especially native Claude Code users) cannot read startup hints, reducing usability.
2. **Backend URL example too narrow**: The startup hint only shows `Backend URL: %s` with a default value from legacy config (bigmodel). Missing the note "other Anthropic or OpenAI Chat compatible endpoints can be configured", users may mistakenly believe only bigmodel is supported.
3. **Configuration examples lack Windows**: The `hosts` modification and `NODE_EXTRA_CA_CERTS` example commands are Unix-only (`sudo tee -a /etc/hosts`, `echo '...' >> ~/.bashrc`). Windows users have no corresponding examples.
4. **Admin panel URL incomplete**: Only shows `https://localhost:%d`, but since `hosts` maps `api.anthropic.com` to `127.0.0.1`, `https://api.anthropic.com:%d` is also reachable (and more natural in the Claude Code configuration context). Not showing this URL means users may not know they can access the admin panel via the domain name.

### Current Log Distribution

All i18n-relevant text is concentrated in `cmd/server/main.go`:

| Line range | Content |
|------------|---------|
| 109–128 | Startup banner, port info, config commands, password hints (pure `fmt.Println` output) |
| 31 | `Warning: random number generation failed, using fallback` |
| 58 | `Warning: no password set, using randomly generated password` |
| 100 | `CA certificate: %s` (English, but mixed with Chinese) |
| 149 | `Running in Docker container...` |
| 156 | `Proxy server error: %v` (English) |
| 162 | `Admin server error: %v` (English) |
| 177–202 | Shutdown/restart/update logs (mixed Chinese/English) |

### Existing i18n Infrastructure

- Frontend already has a complete i18n system (`internal/frontend/src/composables/useI18n.ts`), but backend has no internationalization mechanism.
- Go standard library provides `golang.org/x/text/message` (catalog/pipeline), but this project uses only the standard library.
- Simplest approach: runtime system locale detection (`LANG`/`LC_ALL` env vars), select message set based on prefix (`zh`, `en`, etc.). Default fallback to English.

### Strategy

**Single-file message table + runtime locale detection**:

- Create a lightweight message table in `internal/i18n/` (pure Go map, no external dependencies).
- At startup, read `LANG`/`LC_ALL` environment variables: if starts with `zh`, output Chinese; otherwise English.
- Preserve all English log originals, Chinese as optional override.
- Config hints output platform-specific commands (Windows / Unix).
- Admin panel URL shows both `localhost` and `api.anthropic.com` forms.

### Scope

- **In scope**: `cmd/server/main.go` startup log i18n, `internal/admin/` user-facing Chinese hints.
- **Out of scope**: Frontend i18n (already complete), error stack traces (keep English), HTTP log middleware (technical logs keep English).

## Development Checklist

| No. | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Completed | Create `internal/i18n` message table and locale detection | `internal/i18n/i18n.go` | Unit test: various locale prefixes return correct language |
| 2 | Completed | i18n `cmd/server/main.go` startup banner and config hints | `cmd/server/main.go` | Manual verification: Chinese vs English startup log comparison |
| 3 | Completed | Add Windows config examples and dual-domain hints | `cmd/server/main.go` | Manual check: Windows/Unix output difference |
| 4 | Completed | Verify no user-facing Chinese logs remain in `internal/admin`; i18n Docker update disabled hint passed from `cmd/server/main.go` | `cmd/server/main.go`, `internal/admin/` | `grep` check: no Chinese logs; admin panel shows locale-aware update disabled message |

## Requirements

### Requirement 1: Startup Logs Support English

All user-facing hint messages printed by `mcc` at startup (banner, ports, config commands, password) must automatically select Chinese or English based on system locale. Technical error logs (e.g. `Proxy server error`) remain English.

**Locale detection rules**:

1. Prefer `MCC_LANG` environment variable (value `zh` or `en`).
2. If not set, read `LANG`: if starts with `zh`, Chinese; otherwise English.
3. If not set, read `LC_ALL`: same rule as above.
4. Default fallback to English.

**Message table structure** (`internal/i18n/i18n.go`):

```go
type Messages struct {
    StartupBanner       string
    ProxyPort           string
    AdminPort           string
    BackendURL          string
    ConfigInstructions  string
    HostsCommandUnix    string
    HostsCommandWindows string
    CACertCommandUnix   string
    CACertCommandWindows string
    SourceCommandUnix   string
    SourceCommandWindows string
    AdminPage           string
    AdminPageAlt        string
    RandomPassword      string
    PasswordSaveHint    string
    PasswordEnvHint     string
    DockerRunningHint   string
    DisableUpdateReason string
    // ... other messages
}

func Load(locale string) Messages { ... }
```

### Requirement 2: Backend URL Hint Includes Compatibility Note

The backend URL line in startup hints, while displaying the actual URL, appends a note about configurable endpoints:

- **Chinese**: `后端地址: %s（可配置其他兼容 Anthropic 或 OpenAI Chat 的接口地址）`
- **English**: `Backend URL: %s (configurable to use other Anthropic or OpenAI Chat compatible endpoints)`

### Requirement 3: Platform-Specific Config Examples

The `hosts` modification and CA certificate environment variable config examples output platform-specific commands based on the runtime platform (Windows / Unix):

**Unix (Linux/macOS)**:

```
1. echo '127.0.0.1 api.anthropic.com' | sudo tee -a /etc/hosts
2. echo 'NODE_EXTRA_CA_CERTS=%s' >> ~/.bashrc
3. source ~/.bashrc
```

**Windows**:

```
1. Run as administrator PowerShell:
   Add-Content -Path "$env:WINDIR\System32\drivers\etc\hosts" -Value "127.0.0.1 api.anthropic.com"
2. Set Node.js CA certificate environment variable:
   [Environment]::SetEnvironmentVariable("NODE_EXTRA_CA_CERTS", "%s", "User")
3. Close and reopen terminal
```

Platform detection uses `runtime.GOOS == "windows"`.

### Requirement 4: Admin Panel URL Dual-Domain Hint

The admin panel URL shows both forms:

- **Chinese**:
  ```
  配置页面（以下两个地址等价）：
    https://localhost:%d
    https://api.anthropic.com:%d
  ```
- **English**:
  ```
  Admin panel (both URLs point to the same service):
    https://localhost:%d
    https://api.anthropic.com:%d
  ```

## Task Details

### Task 1: Create i18n Message Table

#### Requirements

**Objective** — Create `internal/i18n` package providing locale-based message selection and platform-aware output.

**Outcomes** — Backend has lightweight internationalization without external dependencies.

**Evidence** — Unit tests cover `zh_CN`, `zh`, `en_US`, `en`, `ja`, `''` locale inputs, asserting correct language set returned. Additional tests verify `MCC_LANG` priority over `LANG` and `LC_ALL` fallback when `LANG` is absent.

**Constraints** — Do not use `golang.org/x/text` or other external packages; keep pure standard library. Message table uses Go struct + map for compile-time type safety.

**Edge Cases** — Unknown `MCC_LANG` value (e.g. `fr`) falls back to English; `LANG` value `C` or `POSIX` falls back to English; `LANG` may not exist on Windows.

**Verification** — `go test ./internal/i18n/... -v`

#### Plan

1. Create `internal/i18n/i18n.go`:
   - Define `Messages` struct with all fields needing i18n.
   - Define `zhMessages` and `enMessages` constant instances.
   - Implement `ResolveLocale()` function, detecting in order: `MCC_LANG` → `LANG` → `LC_ALL` → default English.
   - Implement `Load(locale string) Messages` function.
2. Create `internal/i18n/i18n_test.go`:
   - Test various locale prefix parsing.
   - Test unknown locale fallback to English.
   - Test `MCC_LANG` priority over `LANG` and `LC_ALL`.
   - Test `LC_ALL` fallback when `LANG` is absent.

#### Verification

Run `go test ./internal/i18n/... -v`, all cases pass.

### Task 2: Internationalize Startup Logs

#### Requirements

**Objective** — Replace all user-facing `fmt.Println`/`fmt.Printf` output in `cmd/server/main.go` with i18n messages.

**Outcomes** — Startup banner, port info, config commands, password hints all support Chinese/English switching.

**Evidence** — Manual verification: `LANG=zh_CN.UTF-8` outputs Chinese; `LANG=en_US.UTF-8` outputs English.

**Constraints** — Technical error logs (`log.Fatalf`, `log.Printf` error messages) remain English. Docker detection hint and auto-update hint also need i18n.

**Edge Cases** — Random password generation failure fallback hint; Docker container startup hint.

**Verification** — Build and run with `LANG=zh_CN.UTF-8` and `LANG=en_US.UTF-8` respectively, compare output screenshots.

#### Plan

1. Import `magic-claude-code/internal/i18n` in `cmd/server/main.go`.
2. Replace startup banner area (lines 109–128) with `msg := i18n.Load(i18n.ResolveLocale())`.
3. All `fmt.Println`/`fmt.Printf` calls use message table fields.
4. Replace Docker detection hint (line 149) with i18n message.
5. Internationalize the Chinese string passed to `adminServer.DisableUpdateApply()` (line 150).
6. Internationalize user-facing parts of shutdown/restart logs (lines 177–202).

#### Verification

```bash
# Chinese
LANG=zh_CN.UTF-8 ./bin/mcc -data ./data

# English
LANG=en_US.UTF-8 ./bin/mcc -data ./data
```

Compare outputs to confirm correct language.

### Task 3: Add Platform Examples and Dual-Domain Hints

#### Requirements

**Objective** — Add Windows platform examples to startup config hints, and simultaneously show both `localhost` and `api.anthropic.com` admin panel URLs.

**Outcomes** — Windows users don't need to translate commands themselves; all users know they can access the admin panel via domain name.

**Evidence** — Build and run on Windows (or simulate `runtime.GOOS = "windows"`), confirm Windows-style commands output; confirm both URLs shown.

**Constraints** — No external platform detection packages, use `runtime.GOOS`. Windows PowerShell commands require administrator privileges, noted in the hint.

**Edge Cases** — WSL environment returns `linux` for `runtime.GOOS`, outputs Unix examples (correct behavior).

**Verification** — Build and run on target platform, confirm output format.

#### Plan

1. Add platform-related fields to `Messages` struct:
   - `HostsCommandUnix`
   - `HostsCommandWindows`
   - `CACertCommandUnix`
   - `CACertCommandWindows`
   - `SourceCommandUnix`
   - `SourceCommandWindows`
2. Select corresponding command examples based on `runtime.GOOS` in startup output logic.
3. Print both URLs in admin panel hint.

#### Verification

```bash
# Linux/macOS
./bin/mcc -data ./data
# Confirm sudo tee command output

# Windows (or cross-compile)
./bin/mcc.exe -data .\data
# Confirm PowerShell command output
```

### Task 4: Internationalize Admin Panel Hints

#### Requirements

**Objective** — Check and internationalize Chinese hints in `internal/admin/` that are user-facing.

**Outcomes** — Admin panel related Chinese logs (e.g. update disable hint) support English.

**Evidence** — `grep -rn 'fmt.Println|log.Printf' internal/admin/` results show no pure Chinese hardcoding.

**Constraints** — HTTP error JSON responses (e.g. `{"error": "provider not found"}`) remain English, these are API contracts.

**Edge Cases** — Docker update disable hint already covered in previous step.

**Verification** — `grep` check confirms no missed Chinese logs.

#### Plan

1. Search all Chinese hardcoded logs in `internal/admin/`.
2. Replace user-facing hints with i18n messages.
3. Keep API error JSON in English.

#### Verification

```bash
grep -rn 'fmt\.Print\|log\.Print' internal/admin/ | grep -v '_test.go'
# Confirm no pure Chinese output
```
