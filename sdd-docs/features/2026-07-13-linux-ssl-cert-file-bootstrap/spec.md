# Linux System Trust And SSL_CERT_FILE Bootstrap Spec

Local page: none (runs during `mcc` binary bootstrap)  
Proxy entry: `cmd/server/main.go` -> `internal/bootstrap`  
Reference sources: Claude Code 2.1.206 runtime logs, Bun/BoringSSL TLS behavior, Linux `ca-certificates` bundle conventions, existing `NODE_EXTRA_CA_CERTS` auto-setup spec  
Stack: Go 1.26 standard library (`os`, `runtime`, `path/filepath`, `strings`, `errors`)  
Last updated: 2026-07-13  
Progress: 6 / 7 complete; manual Linux end-to-end validation pending

## Overall Analysis (Source Analysis)

Long Claude Code conversations trigger auxiliary background requests after the main stream finishes. In transparent mode, those requests intermittently fail TLS verification and mcc logs six handshake errors:

```text
TLS handshake error ... local error: tls: bad record MAC (client sent plaintext fatal alert: unknown_ca [48])
```

`unknown_ca [48]` means the client rejected the mcc-generated certificate. The main request path succeeds, so the problem is specific to a Claude Code/Bun auxiliary TLS path. On the current host, the system CA bundle contains the mcc CA because it was previously installed manually; `server.crt` already includes leaf + CA PEM blocks, and `NODE_EXTRA_CA_CERTS` points at the mcc CA. The auxiliary path still fails unless the launching shell provides:

```bash
export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
```

After setting that value and restarting Claude Code, auxiliary requests such as `model=claude-sonnet-4-6 -> glm-5.2` and `model=claude-opus-4-7 -> glm-5.2` are forwarded successfully and no `unknown_ca` errors appear.

This feature therefore extends the binary bootstrap on Linux to first ensure the full system CA bundle contains the current mcc CA, then persist `SSL_CERT_FILE` to that verified bundle. It must never point `SSL_CERT_FILE` at `data/ca.crt`, because many TLS stacks treat `SSL_CERT_FILE` as the complete trust bundle rather than an appended CA file.

`SSL_CERT_FILE` alone only tells the Claude Code/Bun auxiliary TLS path which bundle to read. If `/etc/ssl/certs/ca-certificates.crt` does not contain a certificate with the same SHA256 fingerprint as `data/ca.crt`, the client will still reject the mcc-generated leaf certificate with `unknown_ca`. The required Linux sequence is:

```text
generate/load data/ca.crt
  -> install mcc CA into the Linux trust store
  -> run update-ca-certificates or update-ca-trust extract
  -> verify the target bundle contains the same SHA256 fingerprint as data/ca.crt
  -> persist SSL_CERT_FILE=<that full system bundle>
```

Scope:

| Platform/deployment | This feature | Reason |
| --- | --- | --- |
| Linux binary | Install/verify mcc CA in the system bundle and persist `SSL_CERT_FILE` automatically | Reproduced and verified; bundle path is stable |
| Linux Docker | No in-container host profile writes | Containers cannot safely mutate host user environments |
| macOS binary/Docker | No automatic `SSL_CERT_FILE` | Keychain is the primary trust source; no verified PEM bundle path |
| Windows binary/Docker | No automatic `SSL_CERT_FILE` | Windows Root Store is the primary trust source; no reproduction yet |

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Complete | Verify/fix mcc CA in the Linux system bundle | fingerprint scanning, stale marker handling, post-install verification | tests for present/missing/mismatched CA |
| 2 | Complete | Extend `EnvAdapter` for `SSL_CERT_FILE` | `bootstrap.go`, mocks, lookup methods | Focused interface tests |
| 3 | Complete | Implement Linux/POSIX profile persistence | `adapters.go` block writer and custom-value detection | bash/zsh/fish/idempotency and conflict-scan tests |
| 4 | Complete | Integrate bootstrap marker and result | `tryPersistSSLCertFile`, `Result.SSLCertFileResult`, `.ssl-cert-file-persisted` | mock integration tests |
| 5 | Complete | Update startup instructions and docs | `instructions.go`, `CLAUDE.md`, README files | instruction tests and manual review |
| 6 | Complete | Run regression/platform-boundary tests | bootstrap package and full Go test suite | test commands pass |
| 7 | Pending | Manual Linux validation | verification notes in this spec | long conversation with no `unknown_ca` |

## Requirements

### Deliverables

1. Strengthen Linux CA trust setup from "marker exists" to "marker plus verified system bundle fingerprint":

```go
func linuxSystemBundleContainsCA(bundlePath, caCertPath string) (bool, error)
func caFingerprintSHA256(certPath string) (string, error)
func pemBundleContainsFingerprint(bundlePEM []byte, fingerprint string) (bool, error)
func (e *Executor) ensureLinuxSystemTrustBundleContainsMCCCA(bundlePath string) StepResult
```

Behavior:

- Linux must verify the target bundle before `SSL_CERT_FILE` persistence.
- If the bundle already contains the current mcc CA fingerprint, do not reinstall.
- If the bundle is missing the current fingerprint or contains only an old mcc CA, call the existing `e.trust.InstallCA(e.caCertPath)` path and rescan the bundle.
- If install succeeds but the bundle still lacks the fingerprint, return failure and do not write `.ca-trust-installed` or `.ssl-cert-file-persisted`.
- Do not rely on `.ca-trust-installed` alone; package updates, CA regeneration, or data directory moves can make the marker stale.

2. Extend `EnvAdapter`:

```go
type EnvAdapter interface {
    PersistRoot(rootDir string) error
    LookupNodeCACert() (value string, exists bool, err error)
    PersistNodeCACert(caCertPath string) error
    LookupSSLCertFile() (value string, exists bool, err error)
    PersistSSLCertFile(bundlePath string) error
}
```

3. Add Linux system bundle discovery:

```go
var linuxSSLCertFileCandidates = []string{
    "/etc/ssl/certs/ca-certificates.crt",
    "/etc/pki/tls/certs/ca-bundle.crt",
    "/etc/ssl/ca-bundle.pem",
    "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
}
```

4. Add `Result.SSLCertFileResult StepResult`.
5. Add `tryPersistSSLCertFile()` and call it only for Linux, non-Docker binary bootstrap after `tryPersistNodeCA` and Linux bundle fingerprint verification.
6. Persist a separate POSIX profile block:

```bash
# >>> mcc: SSL_CERT_FILE trust bundle (auto-managed, do not edit) >>>
export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
# <<< mcc <<<
```

Fish shell must use `set -gx SSL_CERT_FILE ...`.

7. Do not overwrite user-managed `SSL_CERT_FILE`. If a non-mcc assignment already exists, return `ErrUserCustomValue` and print a warning.
8. Add `.ssl-cert-file-persisted` marker bound to bundle path, HOME, and UID.
9. Update `CLAUDE.md`, `README.md`, and `README.en.md` to document Linux system trust verification, `SSL_CERT_FILE` stabilization, and Docker host-environment responsibility.

### Constraints

1. Linux only.
2. Verify the system bundle contains the current mcc CA before writing `SSL_CERT_FILE`.
3. Never set `SSL_CERT_FILE` to `data/ca.crt`.
4. Do not block mcc startup on persistence failure.
5. Do not alter existing `NODE_EXTRA_CA_CERTS` behavior.
6. High-privilege runs must not write ordinary user profiles unless a matching marker already exists.
7. Docker must not try to mutate the host user environment from inside the container.
8. macOS/Windows must remain no-op until there is platform-specific reproduction and a verified fix.

### Edge Cases

1. Debian bundle missing but RHEL/openSUSE bundle present.
2. No bundle candidate exists.
3. Bundle exists but lacks the current mcc CA fingerprint.
4. Bundle contains an old mcc CA fingerprint but not the current `data/ca.crt` fingerprint.
5. `.ca-trust-installed` exists but the bundle no longer contains the current fingerprint.
6. `update-ca-certificates` / `update-ca-trust extract` succeeds but the bundle still lacks the fingerprint.
7. Current environment already has the target bundle; it is ready only if that bundle contains the current fingerprint.
8. Current environment or profile has a user custom `SSL_CERT_FILE`.
9. Existing mcc-managed block points to an old bundle path.
10. Marker belongs to another HOME/UID.
11. Fish shell profile syntax.
12. Root run without marker.
13. Profile write failure.
14. User previously set `SSL_CERT_FILE` to `data/ca.crt`.

### Non-Goals

1. Do not generate a macOS PEM bundle.
2. Do not set `SSL_CERT_FILE` on Windows.
3. Do not edit Claude Code settings.
4. Do not mutate host profiles from Docker.
5. Do not change TLS handshake or certificate generation behavior.
6. Do not add `ZDOTDIR`-relocated zsh profile support in this change.

## Task Details

### Task 1: Verify And Fix mcc CA In The Linux System Bundle

#### Requirements

**Objective** — Make "the system bundle contains the current mcc CA" a verified bootstrap fact on Linux, not an assumption based on prior manual setup.

**Outcomes** — Bootstrap can scan the selected system bundle, confirm it contains a certificate with the same SHA256 fingerprint as `data/ca.crt`, reinstall the CA through the existing trust path when missing, and refuse to persist `SSL_CERT_FILE` when post-install verification still fails.

**Evidence** — Tests cover matching bundle, missing CA, old CA fingerprint, stale trust marker, install-then-verify success, and install-then-verify failure.

**Constraints** — Do not grep certificate subjects. PEM/DER subjects are not reliable text markers. Use SHA256 fingerprints over certificate DER bytes. Do not rely on `.ca-trust-installed` alone.

**Edge Cases** — regenerated CA, package-manager bundle rebuild, deleted `/usr/local/share/ca-certificates/mcc-ca.crt`, Debian `update-ca-certificates`, RHEL `update-ca-trust`.

**Verification** — `go test ./internal/bootstrap -run 'TestLinuxSystemBundleContainsCA|TestEnsureLinuxSystemTrustBundleContainsMCCCA|TestTryTrustCA.*Bundle' -count=1`.

#### Plan

1. Add failing tests for:
   - `TestPEMBundleContainsFingerprint_MatchingCert`
   - `TestPEMBundleContainsFingerprint_NoMatchingCert`
   - `TestPEMBundleContainsFingerprint_InvalidPEM`
   - `TestLinuxSystemBundleContainsCA_MissingBundle`
2. Implement:
   ```go
   func caFingerprintSHA256(certPath string) (string, error)
   func pemBundleContainsFingerprint(bundlePEM []byte, fingerprint string) (bool, error)
   func linuxSystemBundleContainsCA(bundlePath, caCertPath string) (bool, error)
   ```
   Use `encoding/pem` to scan all `CERTIFICATE` blocks and `crypto/sha256` over DER bytes. Normalize fingerprints as uppercase hex without colons.
3. Add failing stale-marker tests:
   - `TestTryTrustCA_MarkerExistsButBundleMissingCA_Reinstalls`
   - `TestTryTrustCA_MarkerExistsButBundleOldFingerprint_Reinstalls`
   - `TestTryTrustCA_InstallSuccessButBundleStillMissing_ReturnsError`
4. Strengthen Linux trust setup so `.ca-trust-installed` is only a hint. The real success condition is marker match plus bundle fingerprint match. If either fails, run `InstallCA`, rescan, and write the marker only after the rescan succeeds.
5. Make `tryPersistSSLCertFile` depend on this verification before profile or marker writes.

#### Verification

- [x] No subject-grep based CA detection.
- [x] Fingerprint scan tests pass.
- [x] Stale marker triggers reinstall.
- [x] Failed post-install verification blocks `SSL_CERT_FILE` persistence.

### Task 2: Extend EnvAdapter And Mocks

#### Requirements

**Objective** — Add an explicit environment adapter boundary for `SSL_CERT_FILE`.

**Outcomes** — `EnvAdapter`, `osEnvAdapter`, and `mockEnv` support lookup and persistence of `SSL_CERT_FILE`.

**Evidence** — Focused tests prove the bundle path reaches `PersistSSLCertFile`.

**Constraints** — Do not reuse `PersistNodeCACert`; keep `NODE_EXTRA_CA_CERTS` and `SSL_CERT_FILE` separate.

**Edge Cases** — lookup errors, custom values, persist errors.

**Verification** — `go test ./internal/bootstrap -run 'TestTryPersistSSLCertFile|TestOSEnvAdapterLookupSSLCertFile' -count=1`.

#### Plan

1. Write failing tests for `tryPersistSSLCertFile` and mock env argument capture.
2. Extend `EnvAdapter` with `LookupSSLCertFile` and `PersistSSLCertFile`.
3. Extend `mockEnv` with value, error, and captured-argument fields.
4. Add minimal `osEnvAdapter.LookupSSLCertFile` using `os.LookupEnv`.
5. Add `osEnvAdapter.PersistSSLCertFile` that dispatches to Linux/POSIX and returns unsupported elsewhere.

#### Verification

- [x] Focused interface tests pass.
- [x] Interface compiles.
- [x] Mock captures bundle path.

### Task 3: Implement Linux Bundle Selection And POSIX Profile Writer

#### Requirements

**Objective** — Persist the verified full Linux CA bundle to shell profiles.

**Outcomes** — Bundle discovery, verification that the bundle contains the current mcc CA, bash/zsh/fish profile block writing, idempotency, and user custom-value protection across every relevant startup profile and every recognized unmanaged assignment or exact-key environment mutation in each profile. Conflicts are checked globally, while a matching value suppresses persistence only for the same preferred write candidate.

**Evidence** — Unit tests cover Debian/RHEL bundle choice and profile output.

**Constraints** — Do not write `data/ca.crt`; do not write an unverified bundle path; use a separate mcc-managed block.

**Edge Cases** — fish syntax including universal exports, profile missing, old or malformed managed blocks, Node CA and SSL blocks in either order with a shared end marker, symlink safety, Bash login profiles, zsh `.zshenv`/`.zprofile`/`.zshrc`/`.zlogin`, `typeset -x`, `declare -x`, exact-key remove/unexport commands, a matching value in a secondary profile, and a later assignment overriding an earlier matching value.

**Verification** — `go test ./internal/bootstrap -run 'TestWritePOSIXProfileSSLCertFile|TestDefaultLinuxSSLCertFile|TestProfileHasSSLCertFile' -count=1`.

#### Plan

1. Add failing tests for bundle discovery with injectable stat.
2. Implement `defaultLinuxSSLCertFileWithStat` and `defaultLinuxSSLCertFile`.
3. Add failing tests for bash/fish block writing, idempotency, path update, and user custom value.
4. Implement `writePOSIXProfileSSLCertFile`, `profileSSLCertFileOutsideMCCBlockValues`, and `sslCertFileExportLine`.
5. Keep preferred write targets separate from conflict-scan candidates. Scan `.bashrc`, `.profile`, `.bash_profile`, and `.bash_login` for Bash; scan `.zshenv`, `.zprofile`, `.zshrc`, and `.zlogin` for zsh; and scan every recognized unmanaged assignment within each candidate.
6. Track matching values by profile. A match in `.profile` must not suppress writing Bash's preferred `.bashrc`; a match in the current write candidate may avoid a duplicate block, while an existing stale MCC block is still repaired.
7. Recognize bare/export assignments, exported `typeset`/`declare` declarations, and fish export/scope flag combinations including `set -Ux`. Read-only references such as `echo "$SSL_CERT_FILE"` are not assignments.
8. Require exact, complete, non-nested MCC marker pairs. Treat unmatched, nested, or command-embedded markers as `ErrUserCustomValue` before writing. Keep the existing POSIX Node CA block compatible despite its shared generic end marker, while continuing to scan that block's body for `SSL_CERT_FILE`; only the SSL block suppresses target-key parsing.
9. Outside complete MCC blocks, conservatively reject exact-key POSIX `unset`, `export -n`, `typeset +x`, and `declare +x` commands and fish `set --erase`/`-e` or `set --unexport`/`-u` commands. Mutations of other variables and read-only references remain allowed.
10. Locate a managed block's end only after its requested begin marker. Both replacement and same-value stale-block handling must reuse this target-relative range, so Node CA and SSL blocks can appear in either order without duplicate appends or skipped repairs.
11. Reuse existing profile safety, shell quoting, and marked-block replacement helpers.

#### Verification

- [x] Bundle selection tests pass.
- [x] POSIX profile tests pass.
- [x] Bash and all four supported zsh startup profiles are scanned for conflicts.
- [x] Same-value suppression is scoped to the current write candidate.
- [x] POSIX declaration exports and fish universal exports are recognized.
- [x] Malformed MCC marker structure fails closed without modifying the profile.
- [x] POSIX Node CA profile writes use the same malformed-marker fail-closed rule.
- [x] POSIX Node CA scanning recognizes declaration exports and exact-key unexport/unset mutations outside the managed block.
- [x] Fish Node CA scanning recognizes exact-key erase/unexport mutations outside the managed block.
- [x] Existing POSIX Node CA and SSL blocks coexist without hiding target-key changes.
- [x] Shared end markers are paired relative to the requested block begin in either Node CA/SSL ordering.
- [x] Cross-type ordering preserves stale-block repair and byte-identical repeated-run idempotency.
- [x] Exact-key remove/unexport commands outside managed blocks fail closed.
- [x] A matching user value cannot leave a stale MCC-managed block effective.
- [x] User custom value is protected.

### Task 4: Integrate Bootstrap Result, Marker, And State Hash

#### Requirements

**Objective** — Run Linux `SSL_CERT_FILE` persistence during bootstrap after the system bundle has been verified, and make it idempotent.

**Outcomes** — `Result.SSLCertFileResult`, `tryPersistSSLCertFile`, marker read/write, and state hash integration.

**Evidence** — Tests cover success, Docker skip, non-Linux skip, privileged skip, user custom value, marker hit, and persist error.

**Constraints** — Failure is advisory; transparent mode can still start. Persistence must run after Linux bundle fingerprint verification.

**Edge Cases** — marker with wrong user, bundle path change, partial persistence.

**Verification** — `go test ./internal/bootstrap -run 'TestTryPersistSSLCertFile|TestExecutorRun.*SSLCertFile|TestStateHash.*SSLCertFile' -count=1`.

#### Plan

1. Add failing behavior tests.
2. Add `.ssl-cert-file-persisted` marker type and helper functions.
3. Implement `tryPersistSSLCertFile`.
4. Ensure it calls `ensureLinuxSystemTrustBundleContainsMCCCA(bundle)` or receives an already-verified bundle before any profile/marker writes.
5. Call it from transparent-mode bootstrap only on Linux and non-Docker.
6. Include the new result in state hashing.

#### Verification

- [x] Linux success writes marker.
- [x] Docker/macOS/Windows skip.
- [x] State hash changes across result states.

### Task 5: Update Instructions And Documentation

#### Requirements

**Objective** — Tell users what was configured and how to handle Docker/manual cases.

**Outcomes** — Startup instructions, FAQ, and READMEs describe Linux system trust verification and `SSL_CERT_FILE` behavior.

**Evidence** — Instruction tests and documentation review.

**Constraints** — Do not claim macOS/Windows are fixed by `SSL_CERT_FILE`.

**Edge Cases** — user custom `SSL_CERT_FILE`, system bundle missing the mcc CA, partial failure, Docker host setup.

**Verification** — `go test ./internal/bootstrap -run 'TestGenerateInstructions.*SSLCertFile' -count=1`.

#### Plan

1. Add failing instruction tests for success, custom value, and failure.
2. Update `instructions.go` messages in zh/en.
3. Update `CLAUDE.md` FAQ.
4. Update `README.md` and `README.en.md`.
5. Review wording for Linux-only scope and `data/ca.crt` warning.

#### Verification

- [x] Instruction tests pass.
- [x] Docs mention Linux binary auto handling and Docker host responsibility.

### Task 6: Regression And Platform Boundary Tests

#### Requirements

**Objective** — Verify no regressions in bootstrap and no accidental macOS/Windows behavior.

**Outcomes** — Focused tests, bootstrap tests, race tests, full Go tests, and cross-compile checks pass.

**Evidence** — Command output recorded back into this spec.

**Constraints** — No native macOS/Windows validation required for this Linux-only change.

**Edge Cases** — build tags and platform-specific lookup files.

**Verification** — Commands listed below.

#### Plan

Run:

```bash
go test ./internal/bootstrap -run 'TestTryPersistSSLCertFile|TestWritePOSIXProfileSSLCertFile|TestGenerateInstructions.*SSLCertFile|TestDefaultLinuxSSLCertFile' -count=1 -v
go test ./internal/bootstrap -run 'TestLinuxSystemBundleContainsCA|TestEnsureLinuxSystemTrustBundleContainsMCCCA|TestTryTrustCA.*Bundle' -count=1 -v
go test ./internal/bootstrap -count=1
go test -race ./internal/bootstrap ./internal/cert -count=1
go test ./... -count=1
go vet ./...
GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server
GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap
GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server
GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap
```

#### Verification

Verified on 2026-07-13 with the commands above; all exited successfully.

- [x] Focused tests pass.
- [x] Bootstrap package passes.
- [x] Bootstrap/cert race tests pass.
- [x] Full Go tests pass.
- [x] `go vet ./...` passes.
- [x] Windows amd64/macOS arm64 server and bootstrap-test compile checks pass.

### Task 7: Manual Linux Validation

#### Requirements

**Objective** — Prove the binary bootstrap installs/verifies the system CA, persists `SSL_CERT_FILE`, and fixes the original long-conversation failure.

**Outcomes** — The system bundle contains the current `data/ca.crt` fingerprint, new shell/Claude Code processes inherit `SSL_CERT_FILE`, and long conversations no longer emit `unknown_ca`.

**Evidence** — Profile grep, environment check, mcc logs, and auxiliary request logs.

**Constraints** — Restart Claude Code; do not rely on the current process environment.

**Edge Cases** — already-running Orca/Claude Code process; old profile blocks.

**Verification** — manual command sequence.

#### Plan

1. Build/run binary bootstrap.
2. Confirm the selected system bundle contains the current `data/ca.crt` fingerprint.
3. Confirm profile contains the mcc SSL_CERT_FILE block.
4. Open a new shell and confirm `echo "$SSL_CERT_FILE"` prints the system bundle.
5. Start Claude Code from that shell.
6. Trigger a long conversation.
7. Verify:

```bash
docker logs mcc --since 10m 2>&1 | grep -E 'unknown_ca|bad record MAC'
docker logs mcc --since 10m 2>&1 | grep -E 'model=claude-sonnet-4-6|model=claude-opus-4-7'
```

#### Verification

- [ ] Profile block exists.
- [ ] System bundle contains the current `data/ca.crt` fingerprint.
- [ ] New process inherits `SSL_CERT_FILE`.
- [ ] No `unknown_ca|bad record MAC`.
- [ ] Auxiliary requests succeed.
