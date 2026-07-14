# SSL_CERT_FILE Profile Scan Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent Linux bootstrap from reporting `SSL_CERT_FILE` ready when any effective shell profile contains a conflicting unmanaged assignment, and synchronize the feature documentation with the implemented state.

**Architecture:** Keep the existing ordered profile list as the write-target policy so unrelated persistence behavior is unchanged. Add a separate SSL-specific scan-candidate resolver, scan every unmanaged assignment in every candidate before writing, and treat the profile state as acceptable only when all assignments equal the verified system bundle.

**Tech Stack:** Go 1.26 standard library, Go `testing`, Markdown documentation.

---

### Task 1: Add Regression Coverage For All Effective Profiles

**Files:**
- Modify: `internal/bootstrap/bootstrap_test.go`

- [x] **Step 1: Add failing profile-list tests**

Extend `TestResolveSSLCertFileScanProfiles` to require these exact ordered candidates:

```go
[]string{"/home/u/.bashrc", "/home/u/.profile", "/home/u/.bash_profile", "/home/u/.bash_login"}
[]string{"/home/u/.zshrc", "/home/u/.zprofile"}
[]string{"/home/u/.config/fish/config.fish"}
[]string{"/home/u/.profile", "/home/u/.bashrc"}
```

- [x] **Step 2: Add failing behavior tests**

Add table-driven `writePOSIXProfileSSLCertFile` tests proving:

```go
// Bash rejects a conflicting assignment in .profile, .bash_profile, or .bash_login.
// zsh rejects a conflicting assignment in .zprofile.
// A profile whose first assignment matches but later assignment conflicts is rejected.
// Multiple unmanaged assignments are accepted only when every value matches bundlePath.
```

- [x] **Step 3: Verify RED**

Run:

```bash
go test ./internal/bootstrap -run 'TestResolveSSLCertFileScanProfiles|TestWritePOSIXProfileSSLCertFile_(KnownShellScansAllEffectiveProfiles|RejectsLaterConflictingAssignment|AcceptsMultipleMatchingAssignments)' -count=1
```

Expected: FAIL because the new resolver is absent and current scanning misses secondary profiles/later assignments.

### Task 2: Separate Scan Candidates And Scan Every Assignment

**Files:**
- Modify: `internal/bootstrap/adapters.go`
- Test: `internal/bootstrap/bootstrap_test.go`

- [x] **Step 1: Add the SSL-specific resolver**

Implement:

```go
func resolveSSLCertFileScanProfiles(shell, home string) []string {
	switch {
	case strings.Contains(shell, "zsh"):
		return []string{home + "/.zshrc", home + "/.zprofile"}
	case strings.Contains(shell, "fish"):
		return []string{home + "/.config/fish/config.fish"}
	case strings.Contains(shell, "bash"):
		return []string{home + "/.bashrc", home + "/.profile", home + "/.bash_profile", home + "/.bash_login"}
	default:
		return resolveShellProfiles(shell, home)
	}
}
```

- [x] **Step 2: Return every unmanaged value**

Replace the first-match parser with:

```go
func profileSSLCertFileOutsideMCCBlockValues(shell, content string) []string
```

It must preserve the existing MCC-block, comment, POSIX, and fish parsing rules while appending every matching value instead of returning early.

- [x] **Step 3: Use two distinct profile lists**

In `writePOSIXProfileSSLCertFile`, scan `resolveSSLCertFileScanProfiles(shell, home)` completely. Return `ErrUserCustomValue` on the first value not equal to `bundlePath`; return success without adding an MCC block when one or more unmanaged assignments exist and all match. Continue using `resolveShellProfiles(shell, home)` only for the preferred write target.

- [x] **Step 4: Verify GREEN and focused regressions**

Run:

```bash
go test ./internal/bootstrap -run 'TestResolveSSLCertFileScanProfiles|TestWritePOSIXProfileSSLCertFile' -count=1
```

Expected: PASS.

### Task 3: Synchronize Documentation And Feature Status

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `README.en.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec_ZH.md`

- [x] **Step 1: Correct operator guidance**

Document that Linux binary bootstrap automatically installs/verifies the MCC CA in the full system bundle and persists `SSL_CERT_FILE`; Docker cannot mutate the host profile, so the host must run the trust helper and set `SSL_CERT_FILE` to its full system bundle before starting Claude Code/Orca. Explicitly forbid pointing it at `data/ca.crt`.

- [x] **Step 2: Update truthful status**

Set both specs to `6 / 7`; mark Tasks 1–6 and their automated verification items complete after fresh verification. Leave Task 7 and all manual end-to-end checkboxes pending because this change does not provide a restarted-shell/long-conversation validation run.

### Task 4: Full Verification And Self-Review

**Files:**
- Review: all modified files in this worktree

- [x] **Step 1: Format and run focused tests**

```bash
gofmt -w internal/bootstrap/adapters.go internal/bootstrap/bootstrap_test.go
go test ./internal/bootstrap -run 'TestResolveSSLCertFileScanProfiles|TestWritePOSIXProfileSSLCertFile|TestTryPersistSSLCertFile' -count=1
```

- [x] **Step 2: Run required regression checks**

```bash
go test ./... -count=1
go test -race ./internal/bootstrap ./internal/cert -count=1
go vet ./...
GOOS=windows GOARCH=amd64 go test ./internal/bootstrap -run TestNonExistent -count=0
GOOS=darwin GOARCH=arm64 go test ./internal/bootstrap -run TestNonExistent -count=0
```

- [x] **Step 3: Self-review**

Inspect `git diff --check`, `git diff --stat`, and the full relevant diff. Confirm scan/write separation, complete-value semantics, error propagation/symlink behavior, Bash/zsh/fish/unknown-shell coverage, no unrelated profile-write behavior changes, bilingual document parity, and no unsupported manual-validation claim.

---

## Follow-up Review Fixes

### Task 5: Scope Same-Value Suppression To The Write Profile

**Files:**
- Modify: `internal/bootstrap/bootstrap_test.go`
- Modify: `internal/bootstrap/adapters.go`

- [x] **Step 1: Write the failing Bash regression test**

Create a temporary Bash HOME where `.profile` contains the target bundle and `.bashrc` is absent. Call `writePOSIXProfileSSLCertFile` and assert that `.bashrc` is created with `posixSSLBlockBegin` and the target bundle.

- [x] **Step 2: Verify RED**

Run:

```bash
go test ./internal/bootstrap -run TestWritePOSIXProfileSSLCertFile_MatchingSecondaryProfileStillWritesPreferredProfile -count=1
```

Expected: FAIL because the current global `sameValueFound` suppresses `.bashrc` creation.

- [x] **Step 3: Implement per-profile matching**

Replace the global Boolean with a map keyed by scanned profile path:

```go
sameValueProfiles := make(map[string]bool)
```

Record a profile only after its unmanaged values have all been checked. In the write loop, suppress a new block only when `sameValueProfiles[profile]` is true for that exact write candidate; continue repairing an existing stale MCC block in that profile.

- [x] **Step 4: Verify GREEN**

Run the new test plus all `TestWritePOSIXProfileSSLCertFile` tests and expect PASS.

### Task 6: Complete zsh Startup-Profile Scanning

**Files:**
- Modify: `internal/bootstrap/bootstrap_test.go`
- Modify: `internal/bootstrap/adapters.go`

- [x] **Step 1: Write failing resolver and conflict tests**

Require this exact zsh scan order:

```go
[]string{"/home/u/.zshenv", "/home/u/.zprofile", "/home/u/.zshrc", "/home/u/.zlogin"}
```

Add table cases proving a custom value in `.zshenv` or `.zlogin` returns `ErrUserCustomValue` and leaves `.zshrc` unmodified.

- [x] **Step 2: Verify RED**

Run:

```bash
go test ./internal/bootstrap -run 'TestResolveSSLCertFileScanProfiles/zsh|TestWritePOSIXProfileSSLCertFile_ZshScansEveryStartupProfile' -count=1
```

Expected: FAIL because `.zshenv` and `.zlogin` are absent from the resolver.

- [x] **Step 3: Implement the complete zsh list**

Return `.zshenv`, `.zprofile`, `.zshrc`, and `.zlogin` in startup order. Keep `resolveShellProfiles` unchanged so the write target remains `.zshrc`. Do not add `ZDOTDIR` support because the feature does not declare relocated-zsh-profile support.

- [x] **Step 4: Verify GREEN**

Run the two focused tests and expect PASS.

### Task 7: Recognize Common Exported Assignment Forms

**Files:**
- Modify: `internal/bootstrap/bootstrap_test.go`
- Modify: `internal/bootstrap/adapters.go`

- [x] **Step 1: Write failing POSIX and fish tests**

Add table cases that require `ErrUserCustomValue` for:

```text
typeset -x SSL_CERT_FILE=/custom/company-bundle.pem
typeset -gx SSL_CERT_FILE=/custom/company-bundle.pem
declare -x SSL_CERT_FILE=/custom/company-bundle.pem
declare -rx SSL_CERT_FILE=/custom/company-bundle.pem
set -Ux SSL_CERT_FILE /custom/company-bundle.pem
set -U -x SSL_CERT_FILE /custom/company-bundle.pem
set --universal --export SSL_CERT_FILE /custom/company-bundle.pem
```

Add matching-value cases for `typeset -x`, `declare -x`, and `set -Ux` to prove they are parsed rather than rejected unconditionally. Add a read-only `echo "$SSL_CERT_FILE"` case proving harmless references do not block persistence.

- [x] **Step 2: Verify RED**

Run the new syntax tests and expect FAIL for the unrecognized declaration/export forms.

- [x] **Step 3: Implement syntax-aware parsing**

Add a small POSIX declaration parser that accepts bare assignments, `export`, and `typeset`/`declare` commands with a short or long export option. Extend `parseFishExportLine` to accept combined or separate `x` plus scope flags (`g`, `U`, `f`, `l`) and the long forms `--export`, `--global`, `--universal`, `--function`, and `--local`. Continue rejecting erase, unexport, query, append/prepend, malformed, or ambiguous list forms.

Use these interfaces:

```go
func parsePOSIXExportedAssignment(line, key string) (value string, found bool)
func posixDeclarationHasExportOption(fields []string) bool
func parseFishSetOptions(tokens []fishToken) (idx int, hasExport bool, ok bool)
```

`parsePOSIXExportedAssignment` must tokenize with `strings.Fields`, accept a direct `key=value`, treat `export` as exported, require an `x` short option or `--export` for `typeset`/`declare`, skip option fields, and return the first exact `key=` assignment token. `parseFishSetOptions` must iterate option tokens before the key, set `hasExport` for `x`/`--export`, accept only the listed scope flags, and return `ok=false` for every other option so ambiguous mutations never look equivalent to the target bundle.

- [x] **Step 4: Verify GREEN**

Run syntax-focused tests, existing fish parser tests, and all SSL profile tests; expect PASS.

### Task 8: Documentation, Verification, And Self-Review

**Files:**
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec_ZH.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/fix-plan.md`

- [x] **Step 1: Synchronize the spec**

Document per-write-profile same-value behavior, the four zsh startup files, supported declaration forms, and the explicit `ZDOTDIR` non-goal. Keep manual Linux end-to-end validation pending.

- [x] **Step 2: Run complete verification**

```bash
go test ./... -count=1
go test -race ./internal/bootstrap ./internal/cert -count=1
go vet ./...
GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server
GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap
GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server
GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap
git diff --check
```

- [x] **Step 3: Self-review**

Confirm values are checked globally for conflicts but only suppress writes in the same write candidate; zsh scan order covers `.zshenv`, `.zprofile`, `.zshrc`, `.zlogin`; the parsers recognize supported export declarations without treating read-only references as assignments; existing symlink/read-error behavior remains fail-closed; `resolveShellProfiles` and non-SSL persistence are unchanged; and manual validation remains unclaimed.

---

## Fourth Review Fixes

### Task 9: Fail Closed On Malformed MCC Blocks

**Files:**
- Modify: `internal/bootstrap/bootstrap_test.go`
- Modify: `internal/bootstrap/adapters.go`

- [x] **Step 1: Write failing malformed-marker tests**

Add table cases for an unmatched begin followed by a custom assignment, an unmatched end, and a nested begin. Each call to `writePOSIXProfileSSLCertFile` must return an error satisfying `errors.Is(err, ErrUserCustomValue)` and must leave the profile byte-for-byte unchanged.

- [x] **Step 2: Verify RED**

```bash
go test ./internal/bootstrap -run TestWritePOSIXProfileSSLCertFile_MalformedManagedBlockFailsClosed -count=1
```

Expected: FAIL because the current scanner hides everything after an unmatched begin or accepts invalid marker structure.

- [x] **Step 3: Implement strict marker pairing**

Change the scanner interface to:

```go
func profileSSLCertFileOutsideMCCBlockValues(shell, content string) ([]string, error)
```

Compare trimmed marker lines for exact equality. Enter a managed block only on a begin line while outside; leave only on an end line while inside. Return `ErrUserCustomValue` for nested begin, unmatched end, or EOF while inside. In `writePOSIXProfileSSLCertFile`, propagate the scan error before any write. Remove the unused Boolean wrapper rather than discarding the error.

Because the existing POSIX Node CA block shares the generic MCC end marker, recognize `posixCABlockBegin` as a separate valid block. Preserve that block and scan its body for `SSL_CERT_FILE`; only the SSL-managed block may suppress target-key parsing.

- [x] **Step 4: Verify GREEN**

Run the malformed-marker test and all SSL profile tests; expect PASS.

### Task 10: Detect Commands That Remove Or Unexport SSL_CERT_FILE

**Files:**
- Modify: `internal/bootstrap/bootstrap_test.go`
- Modify: `internal/bootstrap/adapters.go`

- [x] **Step 1: Write failing POSIX/fish mutation tests**

Create valid MCC blocks followed by each exact-key mutation and require `ErrUserCustomValue` without file modification:

```text
unset SSL_CERT_FILE
unset -v SSL_CERT_FILE
export -n SSL_CERT_FILE
typeset +x SSL_CERT_FILE
declare +x SSL_CERT_FILE
set -e SSL_CERT_FILE
set --erase --universal SSL_CERT_FILE
set -u SSL_CERT_FILE
set --unexport SSL_CERT_FILE
```

Add controls proving mutations of `OTHER_VAR` and `echo "$SSL_CERT_FILE"` remain ignored.

- [x] **Step 2: Verify RED**

Run the new mutation tests and expect FAIL for every exact-key mutation.

- [x] **Step 3: Implement conservative mutation detection**

Add:

```go
func posixMutatesEnvKey(line, key string) bool
func fishMutatesEnvKey(line, key string) bool
func tokenTargetsEnvKey(token, key string) bool
```

For POSIX, tokenize with `strings.Fields`: detect `unset` with optional flags, `export` with a `-n` short option, and `typeset`/`declare` with a `+x` short option; require a later token equal to `key` or beginning with `key=`. For fish, reuse `stripFishComment` and `scanFishTokens`; detect short-option groups containing lowercase `e` or `u`, or long `--erase`/`--unexport`, skip scope options, and require the next non-option token to equal `key`. Call the shell-appropriate helper only outside complete MCC blocks; return `ErrUserCustomValue` immediately on a match.

- [x] **Step 4: Verify GREEN**

Run mutation tests, declaration/fish parser tests, and all SSL profile tests; expect PASS.

### Task 11: Document, Verify, And Self-Review

**Files:**
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec_ZH.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/fix-plan.md`

- [x] **Step 1: Synchronize the bilingual spec**

Document strict marker pairing and conservative conflict handling for exact-key remove/unexport commands. Keep the Linux restarted-shell/long-conversation validation pending.

- [x] **Step 2: Run complete verification**

```bash
go test ./... -count=1
go test -race ./internal/bootstrap ./internal/cert -count=1
go vet ./...
GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server
GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap
GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server
GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap
git diff --check
```

- [x] **Step 3: Self-review**

Confirm malformed markers fail before mutation, complete MCC blocks are still ignored, exact-key mutations outside blocks fail closed, similarly named variables and read-only references remain allowed, parser behavior from previous rounds is unchanged, profile read/symlink safety remains intact, and manual validation is not claimed.

---

## Fifth Review Fixes

### Task 12: Reproduce Cross-Type Shared-End Marker Failures

**Files:**
- Modify: `internal/bootstrap/bootstrap_test.go`

- [x] **Step 1: Add failing SSL replacement and idempotency tests**

Add `TestWritePOSIXProfileSSLCertFile_NodeCABlockBeforeStaleSSLBlock` with a Bash profile containing a valid Node CA block followed by an SSL block using an old bundle path. After `writePOSIXProfileSSLCertFile(newBundlePath)`, require the old path to be absent and `strings.Count(content, posixSSLBlockBegin) == 1`.

Add `TestWritePOSIXProfileSSLCertFile_NodeCABlockBeforeSSLBlockIsIdempotent`. Run the writer once, capture the bytes, run it again, and require the bytes to remain identical with exactly one SSL begin marker.

- [x] **Step 2: Add the failing same-value stale-block test**

Add `TestWritePOSIXProfileSSLCertFile_MatchingUserValueBeforeNodeCAAndStaleSSLBlock`. Arrange a matching unmanaged `SSL_CERT_FILE`, then a Node CA block, then a stale SSL block. Require the writer to replace the stale SSL path with the target bundle rather than returning early; the final profile must contain no old path and exactly one SSL begin marker.

- [x] **Step 3: Add failing Node CA replacement and idempotency tests**

Add `TestWritePOSIXProfileNodeCA_SSLBlockBeforeStaleNodeCABlock` with a valid SSL block followed by an old Node CA block. Require `writePOSIXProfileNodeCA(newCAPath)` to remove the old CA path and leave exactly one `posixCABlockBegin`.

Add `TestWritePOSIXProfileNodeCA_SSLBlockBeforeNodeCABlockIsIdempotent`. Capture bytes after the first call, call again, and require byte equality and exactly one Node CA begin marker.

- [x] **Step 4: Verify RED**

Run:

```bash
go test ./internal/bootstrap -run 'TestWritePOSIXProfileSSLCertFile_(NodeCABlockBeforeStaleSSLBlock|NodeCABlockBeforeSSLBlockIsIdempotent|MatchingUserValueBeforeNodeCAAndStaleSSLBlock)|TestWritePOSIXProfileNodeCA_(SSLBlockBeforeStaleNodeCABlock|SSLBlockBeforeNodeCABlockIsIdempotent)' -count=1
```

Expected: FAIL because the shared generic end marker before the target begin prevents replacement and allows duplicate append or premature success.

### Task 13: Use One Target-Relative Marked-Block Locator

**Files:**
- Modify: `internal/bootstrap/adapters.go`
- Test: `internal/bootstrap/bootstrap_test.go`

- [x] **Step 1: Add the locator helper**

Implement:

```go
func findMarkedBlock(content, begin, end string) (beginIndex, endIndex int, found bool) {
    beginIndex = strings.Index(content, begin)
    if beginIndex < 0 {
        return -1, -1, false
    }
    endSearchStart := beginIndex + len(begin)
    relativeEnd := strings.Index(content[endSearchStart:], end)
    if relativeEnd < 0 {
        return beginIndex, -1, false
    }
    return beginIndex, endSearchStart + relativeEnd, true
}
```

The returned `endIndex` points at the paired target end marker and can never refer to an earlier block's shared end marker.

- [x] **Step 2: Reuse the helper in both consumers**

Change `replaceMarkedBlock` to use `findMarkedBlock` for its existing-block range. Change the `sameValueProfiles` branch in `writePOSIXProfileSSLCertFile` to call the same helper and return early only when the target SSL block is absent; when found, continue into `replaceMarkedBlock` so stale managed content is repaired.

Do not change shell quoting, read/write safety checks, conflict scanning, symlink behavior, or malformed-marker validation.

- [x] **Step 3: Verify GREEN and focused safety regressions**

Run:

```bash
gofmt -w internal/bootstrap/adapters.go internal/bootstrap/bootstrap_test.go
go test ./internal/bootstrap -run 'TestWritePOSIXProfileSSLCertFile|TestWritePOSIXProfileNodeCA|TestReplaceMarkedBlock|TestFindMarkedBlock' -count=1
```

Require all new ordering/idempotency tests plus existing malformed marker, custom-value, Node CA/SSL coexistence, symlink, and byte-preservation tests to pass.

### Task 14: Synchronize Documentation, Verify, And Self-Review

**Files:**
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec_ZH.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/fix-plan.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/review-notes.md`
- Modify: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/review-notes_ZH.md`

- [x] **Step 1: Synchronize the bilingual feature documents**

Document target-relative marker pairing, cross-type block ordering, stale-block repair, and repeated-run idempotency. Record the fifth-review Medium finding as resolved in both review notes. Keep the restarted-shell/long-conversation manual Linux validation pending.

- [x] **Step 2: Run all required verification**

```bash
go test ./internal/bootstrap -count=1
go test ./... -count=1
go test -race ./internal/bootstrap ./internal/cert -count=1
go vet ./...
GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server
GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap
GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server
GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap
git diff --check
```

- [x] **Step 3: Perform the final functional and security self-review**

Inspect both block orders, repeated-run byte idempotency, actual removal of stale paths, `sameValueProfiles` target-range behavior, malformed-marker fail-closed coverage, custom-conflict byte preservation, shell quoting, symlink/non-regular-file checks, and the unchanged TOCTOU boundary. Do not claim the pending manual Linux validation.

## Sixth Review Fixes

### Task 15: Fail Closed On Malformed POSIX Node CA MCC Markers

**Files:**
- Modify: `internal/bootstrap/adapters.go`
- Test: `internal/bootstrap/bootstrap_test.go`
- Docs: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec.md`, `spec_ZH.md`, `fix-plan.md`, `review-notes.md`, `review-notes_ZH.md`

- [x] **Step 1: Add failing regression tests**

Add a write-path regression proving an unterminated Node CA MCC block followed by `export NODE_EXTRA_CA_CERTS=/custom/company-ca.crt` returns `ErrUserCustomValue` and leaves the profile bytes unchanged. Add scanner-level coverage for unmatched begin, unmatched end, nested begin, and command-embedded markers. Add a Darwin preflight regression proving malformed POSIX Node CA markers skip `launchctl` before any session environment update.

- [x] **Step 2: Return scanner errors from the Node CA profile scan**

Change `profileHasNodeCAKeyOutsideMCCBlock` from a boolean scan to `(bool, error)`. Track the active managed block type, only suppress target-key detection inside the Node CA managed block, and return `ErrUserCustomValue` for malformed, nested, isolated, or command-embedded MCC markers. Propagate that error from both `scanPOSIXProfilesForCustomValue` and `writePOSIXProfileNodeCA`.

- [x] **Step 3: Run final verification and self-review**

Run the targeted Node CA/SSL profile tests, the full requested Go/vet/cross-build checks, and `git diff --check`. Re-check that malformed marker failures preserve profile bytes, custom values are not overwritten, symlink/non-regular-file validation and shell quoting are unchanged, and the known TOCTOU boundary is not expanded.

## Seventh Review Fixes

### Task 16: Reuse POSIX Assignment And Mutation Parsing For Node CA

**Files:**
- Modify: `internal/bootstrap/adapters.go`
- Test: `internal/bootstrap/bootstrap_test.go`
- Docs: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec.md`, `spec_ZH.md`, `fix-plan.md`, `review-notes.md`, `review-notes_ZH.md`

- [x] **Step 1: Add failing regression tests**

Add scanner-level tests proving `declare -x NODE_EXTRA_CA_CERTS=...` and `typeset -x NODE_EXTRA_CA_CERTS=...` are detected as user custom values, and `unset NODE_EXTRA_CA_CERTS` plus `export -n NODE_EXTRA_CA_CERTS` fail closed. Add a write-path test proving a valid Node CA managed block followed by `unset NODE_EXTRA_CA_CERTS` returns `ErrUserCustomValue` and leaves the profile bytes unchanged.

- [x] **Step 2: Reuse existing POSIX parsers**

Update the POSIX branch of `profileHasNodeCAKeyOutsideMCCBlock` to call `posixMutatesEnvKey(trimmed, "NODE_EXTRA_CA_CERTS")` before `parsePOSIXExportedAssignment(trimmed, "NODE_EXTRA_CA_CERTS")`. Keep the Node CA marker state machine, fish parsing, shell quoting, and profile safety checks unchanged.

- [x] **Step 3: Run final verification and self-review**

Run the targeted Node CA/SSL profile tests, the full requested Go/vet/cross-build checks, and `git diff --check`. Re-check that declaration exports are detected, later unexport/unset commands do not survive after a managed block, malformed marker fail-closed behavior is unchanged, and the fifth-round shared-marker idempotency behavior remains intact.

## Eighth Review Fixes

### Task 17: Reuse Fish Mutation Parsing For Node CA

**Files:**
- Modify: `internal/bootstrap/adapters.go`
- Test: `internal/bootstrap/bootstrap_test.go`
- Docs: `sdd-docs/features/2026-07-13-linux-ssl-cert-file-bootstrap/spec.md`, `spec_ZH.md`, `fix-plan.md`, `review-notes.md`, `review-notes_ZH.md`

- [x] **Step 1: Add failing regression tests**

Add scanner-level tests proving `set -e NODE_EXTRA_CA_CERTS`, `set --erase NODE_EXTRA_CA_CERTS`, and `set --unexport NODE_EXTRA_CA_CERTS` fail closed. Add a fish write-path test proving a valid Node CA managed block followed by `set -e NODE_EXTRA_CA_CERTS` returns `ErrUserCustomValue` and leaves the profile bytes unchanged.

- [x] **Step 2: Reuse existing fish mutation parser**

Update the fish branch of `profileHasNodeCAKeyOutsideMCCBlock` to call `fishMutatesEnvKey(trimmed, "NODE_EXTRA_CA_CERTS")` before `parseFishExportLine`. Keep marker handling, POSIX parsing, shell quoting, and profile safety checks unchanged.

- [x] **Step 3: Run final verification and self-review**

Run the targeted Node CA/SSL profile tests, the full requested Go/vet/cross-build checks, and `git diff --check`. Re-check that fish erase/unexport commands do not survive after a managed block, POSIX declaration/mutation coverage remains intact, malformed marker fail-closed behavior is unchanged, and shared-marker idempotency remains intact.
