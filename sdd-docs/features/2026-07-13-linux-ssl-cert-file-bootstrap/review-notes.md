# Linux System Trust And SSL_CERT_FILE Bootstrap Review Notes

Date: 2026-07-13  
Reviewer: Codex

## Scope

Reviewed the complete working-tree change relative to `v0.15.3`, including Linux system-bundle verification, `SSL_CERT_FILE` profile persistence and markers, bootstrap status/output, and `server.crt` chain repair.

## Key Findings And Resolutions

1. **Medium: known shells do not scan all startup profiles for conflicting `SSL_CERT_FILE` values.**
   - `resolveShellProfiles` returns only `.bashrc` for Bash and only `.zshrc` for zsh. A later value in `.profile`, `.bash_profile`, `.bash_login`, `.zprofile`, or another effective startup file can therefore bypass conflict detection. A temporary review test reproduced the Bash `.profile` case: `writePOSIXProfileSSLCertFile` returned `nil` instead of `ErrUserCustomValue`.
   - Required resolution: separate conflict-scan candidates from the preferred write target and scan every relevant existing startup profile before accepting or writing the MCC block.
2. **Medium: only the first unmanaged assignment in each profile is evaluated.**
   - `profileSSLCertFileOutsideMCCBlockValue` returns immediately on the first assignment. If an earlier assignment equals the MCC bundle and a later assignment points to a custom bundle, persistence returns success even though the shell's effective value is the later conflicting value. A temporary review test reproduced this with two exports in `.bashrc`.
   - Required resolution: scan the complete profile; any differing unmanaged assignment must produce `ErrUserCustomValue`. A same-value result is valid only when every unmanaged assignment found is equivalent to the selected bundle.
3. **Low: required operational documentation and spec verification are incomplete.**
   - `CLAUDE.md` still describes `SSL_CERT_FILE` as a rare fallback, README files do not contain the Linux binary/Docker guidance required by Task 5, and the feature spec remains at `0 / 7 planned` with verification boxes unchecked.
   - Required resolution: align FAQ/README text with the implemented Linux behavior and record the completed verification evidence before release.

## Final Review Conclusion

Not approved for merge yet. Certificate-chain rotation/leaf-only handling is coherent and no new directly exploitable security defect was found, but the two reproducible profile-scanning defects can still report `SSL_CERT_FILE` ready while a later shell startup selects a conflicting bundle, allowing the original `unknown_ca` failure to recur.

## Residual Notes

- Static symlink marker attacks are rejected, but marker/profile writes still use check-then-write path validation and retain the previously documented local TOCTOU window when a privileged process writes inside an untrusted writable directory.
- The server-sent root certificate does not replace client trust-anchor configuration; successful `SSL_CERT_FILE`/system-bundle setup remains the decisive fix for `unknown_ca`.
- Verification passed: `go test ./... -count=1`, `go test -race ./internal/bootstrap ./internal/cert -count=1`, `go vet ./...`, and Windows amd64/macOS arm64 bootstrap test-binary cross-compilation.

## Follow-up Review — 2026-07-13

### Confirmed Resolved

- Conflict scanning and preferred write targets are now separate. Bash `.profile`, `.bash_profile`, and `.bash_login`, plus zsh `.zprofile`, are scanned before mutation.
- All basic POSIX/fish assignments returned by the parser are evaluated, so a later conflicting export is no longer hidden by an earlier matching export.
- A stale MCC-managed block is repaired even when a matching unmanaged value exists in the same write profile.
- `CLAUDE.md`, README, README.en, and the feature checklist now describe the Linux binary/Docker behavior consistently.

### Remaining Findings

1. **Medium: a matching value in a non-write profile can suppress persistence to the preferred profile.**
   - `sameValueFound` is global across all scan profiles. For Bash, a matching value only in `.profile` causes the `.bashrc` write loop to skip creating an MCC block and return success. A non-login interactive Bash that does not inherit/read `.profile` can therefore still launch Claude Code without `SSL_CERT_FILE`.
   - A temporary review test reproduced this: `.profile` contained the correct value, the call returned success, and `.bashrc` was not created.
   - Required resolution: track matching values per profile. A value in another startup file must not suppress ensuring the preferred write profile unless startup inheritance is guaranteed.
2. **Medium: zsh startup-profile coverage remains incomplete.**
   - The scan list includes `.zshrc` and `.zprofile`, but not `.zshenv` (read by every zsh) or `.zlogin` (read by login zsh after `.zshrc`). A conflicting value in either file can bypass detection and override or be overridden by the MCC block.
   - A temporary review test reproduced the `.zshenv` case: persistence returned success instead of `ErrUserCustomValue`.
   - Required resolution: include `.zshenv` and `.zlogin`, and account for `ZDOTDIR` if zsh profile relocation is supported.
3. **Medium: common exported-assignment forms are not recognized.**
   - The POSIX parser only recognizes optional `export ` followed directly by `SSL_CERT_FILE=`. Common forms such as zsh `typeset -x`, Bash `declare -x`, and fish `set -Ux` are ignored. MCC can then report success and append a block that changes the user's effective custom value.
   - A temporary review test reproduced `typeset -x SSL_CERT_FILE=/custom/...`: persistence returned success instead of `ErrUserCustomValue`.
   - Required resolution: recognize common exported declaration forms or conservatively reject unrecognized active assignments containing the target key.

### Follow-up Conclusion

Still not approved for merge. The original two reproductions and documentation gap were addressed, but the three remaining profile-boundary/parser defects can still produce a false-ready result or overwrite an effective user CA configuration. No new directly exploitable security vulnerability was found.

Verification passed again: targeted SSL profile tests, `go test ./... -count=1`, `go test -race ./internal/bootstrap ./internal/cert -count=1`, `go vet ./...`, and Windows amd64/macOS arm64 bootstrap test-binary cross-compilation. Native Linux long-conversation verification remains pending in the feature spec.

## Third Review — 2026-07-13

### Confirmed Resolved

- Matching unmanaged values are tracked per profile; a match in `.profile` no longer suppresses the preferred Bash `.bashrc` write.
- zsh conflict scanning covers `.zshenv`, `.zprofile`, `.zshrc`, and `.zlogin` while the write target remains `.zshrc`. `ZDOTDIR` is explicitly out of scope.
- POSIX parsing recognizes exported `typeset`/`declare` short and long options, and fish parsing recognizes combined/separate scope plus export flags. Read-only references such as `echo "$SSL_CERT_FILE"` remain ignored.
- Focused regression coverage exists for these behaviors, and the bilingual spec/fix plan is synchronized.

### Remaining Findings

1. **Medium: an incomplete MCC managed-block marker hides real assignments after it.**
   - `profileSSLCertFileOutsideMCCBlockValues` enters managed-block mode on a begin marker and suppresses every subsequent line until an end marker appears. If the block was truncated or manually damaged, a later unmanaged custom assignment is ignored; `replaceMarkedBlock` then appends a new MCC block and persistence returns success.
   - A temporary review test reproduced a begin marker without an end marker followed by `export SSL_CERT_FILE=/custom/...`; the call returned `nil` instead of `ErrUserCustomValue`.
   - Required resolution: validate complete marker pairs before suppressing their contents. An unmatched/nested marker must fail closed or be repaired without hiding active shell lines.
2. **Medium: post-block mutations can invalidate the effective value without being detected.**
   - The scanner recognizes assignments but not active mutations such as `unset SSL_CERT_FILE`, `export -n`, `typeset +x`, or fish erase/unexport forms. If such a command appears after an unchanged MCC block, persistence returns success although the resulting shell no longer exports the verified bundle.
   - A temporary review test reproduced an MCC block followed by `unset SSL_CERT_FILE`; the call returned `nil` instead of reporting a custom/conflicting state.
   - Required resolution: recognize mutating commands for the exact key and include their ordering in the effective-state decision, or conservatively treat them as user-managed conflicts while continuing to ignore read-only references.

### Third Review Conclusion

The three previously reported Medium findings are fixed, but the branch is still not approved for commit because the two newly reproduced fail-open states can recreate a false-ready `SSL_CERT_FILE` result. No new directly exploitable security vulnerability was found.

Independent verification passed: focused SSL/fish tests, `go test ./... -count=1`, `go test -race ./internal/bootstrap ./internal/cert -count=1`, `go vet ./...`, Windows amd64 and macOS arm64 server/test cross-compilation, and `git diff --check`. Native Linux restarted-shell/long-conversation validation remains pending as documented.

## Fourth Review Resolution — 2026-07-13

### Confirmed Resolved

- The profile scanner now accepts only exact, complete, non-nested SSL MCC marker pairs. Unmatched, nested, or command-embedded markers return `ErrUserCustomValue` before any profile mutation.
- Outside a complete SSL-managed block, exact-key POSIX `unset`, `export -n`, `typeset +x`, and `declare +x` commands and fish erase/unexport forms are treated as user-managed conflicts. Quoted POSIX keys and combined fish short options are covered; other variables and read-only references remain allowed.
- Self-review found that the POSIX Node CA and SSL blocks share the same generic end marker. The scanner now tracks the existing Node CA block separately: valid Node CA blocks remain compatible, while their bodies are still scanned so they cannot hide `SSL_CERT_FILE` assignments or mutations.
- Regression tests require every conflict path to preserve the original profile byte-for-byte.

### Verification

- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- Windows amd64 and macOS arm64 server builds and bootstrap test-binary cross-compilation
- `git diff --check`

### Fourth Review Conclusion

The two reported Medium fail-open states are resolved, and no remaining reproducible logic defect or new directly exploitable security defect was found in the reviewed profile-scanning paths. The change is ready for independent re-review; native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.

### Residual Notes

- The previously documented check-then-write TOCTOU window remains for privileged writes into an untrusted writable directory.
- Relocated zsh profiles through `ZDOTDIR` remain explicitly out of scope.

## Fifth Independent Review — 2026-07-13

### Confirmed Resolved

- Malformed, nested, unmatched, and command-embedded MCC markers now fail closed before the SSL profile writer mutates a file.
- Exact-key POSIX and fish remove/unexport forms covered by the spec are detected outside the SSL-managed block.
- A valid Node CA block is accepted by the SSL scanner, and SSL mutations inside that Node CA block are not hidden.

### Remaining Finding

1. **Medium: the shared end marker still breaks managed-block replacement and idempotency.**
   - `replaceMarkedBlock` locates the first `end` occurrence in the entire file rather than the first matching end after the requested begin marker. Because the Node CA and SSL blocks both use `# <<< mcc <<<`, a preceding block supplies the wrong end position.
   - In the normal Node-CA-before-SSL ordering, an existing SSL block is never replaced. Every `writePOSIXProfileSSLCertFile` call appends another SSL block; the Linux bootstrap calls persistence even when the SSL marker matches, so repeated starts can grow the profile indefinitely.
   - If a matching unmanaged `SSL_CERT_FILE` appears before the Node CA block and a stale SSL-managed block appears after it, the same-value early-return path also compares against the preceding Node CA end marker and returns success without repairing the stale SSL block. The stale managed value remains later in the file and wins at shell startup.
   - The inverse ordering reproduces the same defect for `writePOSIXProfileNodeCA`: an SSL block before a stale Node CA block causes duplicate Node CA blocks to be appended instead of replacing the old one.
   - Temporary review tests reproduced all three cases; they were removed after execution. Required resolution: find the end marker starting at `begin + len(begin)` (or use a typed block parser), and use the same matched range for both replacement and the same-value shortcut. Add ordering, stale-value, and repeat-run regression tests for both block types.

### Fifth Review Conclusion

Not approved for merge. The latest scanner fixes are effective, but the writer still violates the spec's idempotency and stale-block repair requirements and can report success while the effective `SSL_CERT_FILE` remains wrong. No new directly exploitable security vulnerability was found.

Independent verification of the repository's existing tests passed: `go test ./... -count=1`, `go test -race ./internal/bootstrap ./internal/cert -count=1`, `go vet ./...`, Windows amd64 and macOS arm64 server/test cross-compilation, and `git diff --check`. These suites currently lack the ordering regression above. Native Linux restarted-shell/long-conversation validation remains pending.

## Fifth Review Resolution — 2026-07-13

### Confirmed Resolved

- `findMarkedBlock` now locates the requested begin marker first and searches for the shared end marker only after `begin + len(begin)`. A preceding block of another MCC type can no longer supply the target block's end range.
- `replaceMarkedBlock` and the `sameValueProfiles` shortcut now use the same target-relative locator. A matching unmanaged `SSL_CERT_FILE` no longer suppresses repair of a later stale SSL-managed block.
- TDD regressions cover Node CA before stale SSL, SSL before stale Node CA, both repeated-run paths, and the matching-user-value plus intervening Node CA plus stale SSL ordering. They require stale paths to disappear, exactly one target begin marker, and byte-identical content after the second call.
- Existing malformed-marker, custom-conflict, Node CA/SSL coexistence, profile byte-preservation, quoting, and profile-safety tests continue to pass.

### Verification

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- Windows amd64 and macOS arm64 server builds and bootstrap test-binary cross-compilation
- `git diff --check`

### Fifth Review Conclusion

The shared-end-marker Medium finding is resolved for both managed-block orderings, stale-block replacement, the same-value path, and repeated-run idempotency. No new directly exploitable security defect was introduced. Native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.

### Residual Notes

- The existing check-then-write TOCTOU window remains unchanged; this fix does not broaden write targets or relax symlink/non-regular-file validation.
- Relocated zsh profiles through `ZDOTDIR` remain explicitly out of scope.

## Sixth Independent Review — 2026-07-13

### Confirmed Resolved

- The fifth-round shared-end-marker defect is resolved. `findMarkedBlock` searches for the target end marker only after the requested begin marker, and both `replaceMarkedBlock` and the SSL same-value shortcut use that target-relative range.
- Regression tests now cover Node-CA-before-SSL, SSL-before-Node-CA, stale-block replacement, repeated-run idempotency, and the matching unmanaged SSL value plus stale managed SSL block ordering.
- Existing SSL malformed-marker handling, custom-value detection, symlink marker checks, cert-chain regeneration checks, and profile byte-preservation tests continue to pass.

### Remaining Finding

1. **Medium: Node CA profile writes still do not fail closed on malformed MCC markers.**
   - `profileHasNodeCAKeyOutsideMCCBlock` at `internal/bootstrap/adapters.go:1066` still treats `posixCABlockBegin` / `posixCABlockEnd` with a simple boolean and returns only whether it saw a custom assignment. It does not report malformed marker state.
   - With an unmatched Node CA begin marker followed by `export NODE_EXTRA_CA_CERTS=/custom/company-ca.crt`, `writePOSIXProfileNodeCA` at `internal/bootstrap/adapters.go:1000` returns success and appends a new MCC block instead of returning `ErrUserCustomValue` and preserving the file. A temporary review test reproduced both the success return and the mutation.
   - This violates the same fail-closed invariant now enforced for SSL markers and can silently ignore a user's hand-written Node CA value in a malformed profile. It is not a new directly exploitable privilege escalation because the affected path runs as the user for profile persistence, and the privileged path remains guarded, but it is still a correctness and trust-bootstrap safety issue.
   - Required resolution: replace the Node CA boolean scanner with a scanner that returns an error on unmatched, nested, isolated, or command-embedded MCC markers, matching the SSL scanner behavior. Add regression tests for unmatched begin, unmatched end, nested begin, command-embedded marker, and byte-preservation on failure.

### Verification

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- Windows amd64 and macOS arm64 server cross-compilation
- `git diff --check`

### Sixth Review Conclusion

Not approved for merge yet. The specific fifth-round shared-end-marker Medium is fixed, but the current profile writer still has a reproducible Node CA malformed-marker fail-open path. Native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.

## Sixth Review Resolution — 2026-07-13

### Confirmed Resolved

- `profileHasNodeCAKeyOutsideMCCBlock` now returns both a custom-value result and a scanner error. Malformed POSIX MCC markers in the Node CA profile path now return `ErrUserCustomValue` instead of hiding later `NODE_EXTRA_CA_CERTS` assignments.
- The scanner now tracks the active managed block type. It only suppresses `NODE_EXTRA_CA_CERTS` detection inside the Node CA managed block and fails closed on unmatched begin, unmatched end, nested begin, or command-embedded markers.
- `scanPOSIXProfilesForCustomValue` and `writePOSIXProfileNodeCA` propagate the scanner error before any environment update or profile write.
- New regressions cover the write-path byte-preservation failure, scanner-level unmatched begin, unmatched end, nested begin, embedded-marker cases, and the Darwin preflight path that must skip `launchctl` on malformed POSIX Node CA markers.

### Verification

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### Sixth Review Conclusion

The sixth-round Node CA malformed-marker Medium finding is resolved in code and regression tests. The automated verification suite passed, and no new directly exploitable security defect was found in this self-review. Native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.

## Seventh Independent Review — 2026-07-13

### Confirmed Resolved

- The sixth-round malformed Node CA marker finding is resolved. `profileHasNodeCAKeyOutsideMCCBlock` now returns `(bool, error)`, and both `scanPOSIXProfilesForCustomValue` and `writePOSIXProfileNodeCA` propagate the error before `launchctl` or profile writes.
- Regression tests cover unmatched Node CA begin, isolated end, nested begin, embedded marker text, write-path byte preservation, and Darwin preflight blocking `launchctl`.
- The fifth-round shared-end-marker fix remains intact for both Node CA and SSL managed block orderings.

### Remaining Finding

1. **Medium: Node CA POSIX scanning still misses exported declarations and later mutation/unset forms.**
   - `profileHasNodeCAKeyOutsideMCCBlock` at `internal/bootstrap/adapters.go:1074` still only recognizes `export NODE_EXTRA_CA_CERTS=...` for POSIX shells. It does not use the stricter parsing helpers already used for `SSL_CERT_FILE`.
   - Temporary review tests confirmed that `declare -x NODE_EXTRA_CA_CERTS=/custom/company-ca.crt`, `typeset -x NODE_EXTRA_CA_CERTS=/custom/company-ca.crt`, `unset NODE_EXTRA_CA_CERTS`, and `export -n NODE_EXTRA_CA_CERTS` all return `(false, nil)`.
   - A full write-path temporary test also confirmed that a profile containing a valid MCC Node CA block followed by `unset NODE_EXTRA_CA_CERTS` returns success and leaves the later unset in place. On shell startup, that later line can clear the value and make mcc report Node CA persistence as ready while the effective environment is not ready.
   - Required resolution: reuse or generalize the existing `parsePOSIXExportedAssignment` and `posixMutatesEnvKey` logic for `NODE_EXTRA_CA_CERTS`, and add regression tests for `declare -x`, `typeset -x`, `unset`, `export -n`, and byte-preservation on failure.

### Verification

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### Seventh Review Conclusion

Not approved for merge yet. The sixth-round malformed-marker issue is fixed, but Node CA profile persistence still has a reproducible fail-open path for POSIX declaration and mutation forms. Native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.

## Seventh Review Resolution — 2026-07-13

### Confirmed Resolved

- `profileHasNodeCAKeyOutsideMCCBlock` now reuses the existing POSIX helpers for `NODE_EXTRA_CA_CERTS`: `posixMutatesEnvKey` catches exact-key unset/unexport forms, and `parsePOSIXExportedAssignment` catches bare/export/declaration assignments.
- Regression tests cover `declare -x NODE_EXTRA_CA_CERTS=...`, `typeset -x NODE_EXTRA_CA_CERTS=...`, `unset NODE_EXTRA_CA_CERTS`, `export -n NODE_EXTRA_CA_CERTS`, and the write path where a valid Node CA managed block is followed by `unset NODE_EXTRA_CA_CERTS`.
- The write-path regression requires `ErrUserCustomValue` and byte-identical profile preservation, so a later mutation cannot remain effective after a reported-success persistence call.

### Verification

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### Seventh Review Conclusion

The seventh-round Node CA POSIX declaration and mutation Medium finding is resolved in code and regression tests. The automated verification suite passed, and no new directly exploitable security defect was found in this self-review. Native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.

## Eighth Independent Review — 2026-07-13

### Confirmed Resolved

- The seventh-round POSIX declaration and mutation finding is resolved. `profileHasNodeCAKeyOutsideMCCBlock` now reuses `posixMutatesEnvKey` and `parsePOSIXExportedAssignment` for `NODE_EXTRA_CA_CERTS`, so `declare -x`, `typeset -x`, `unset`, and `export -n` are covered on POSIX shells.
- The write-path regression for a valid Node CA managed block followed by `unset NODE_EXTRA_CA_CERTS` now returns `ErrUserCustomValue` and preserves profile bytes.
- Prior shared-marker/idempotency and malformed-marker regressions remain covered.

### Remaining Finding

1. **Medium: Node CA fish scanning still misses later erase/unexport mutations.**
   - `profileHasNodeCAKeyOutsideMCCBlock` at `internal/bootstrap/adapters.go:1074` still only calls `parseFishExportLine` for fish. Unlike the SSL scanner, it does not call `fishMutatesEnvKey(trimmed, "NODE_EXTRA_CA_CERTS")`.
   - A temporary write-path review test reproduced this: a fish profile containing a valid MCC Node CA block followed by `set -e NODE_EXTRA_CA_CERTS` returns success and leaves the later erase command in place. On fish startup, that later command can clear the variable and make mcc report Node CA persistence as ready while the effective environment is not ready.
   - Required resolution: mirror the SSL fish path by checking `fishMutatesEnvKey` before `parseFishExportLine` for `NODE_EXTRA_CA_CERTS`. Add scanner-level regressions for `set -e NODE_EXTRA_CA_CERTS` and `set --erase NODE_EXTRA_CA_CERTS`, plus a write-path byte-preservation test for a managed block followed by fish erase/unexport.

### Verification

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### Eighth Review Conclusion

Not approved for merge yet. The seventh-round POSIX issue is fixed, but Node CA profile persistence still has a reproducible fish erase/unexport fail-open path. Native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.

## Eighth Review Resolution — 2026-07-13

### Confirmed Resolved

- `profileHasNodeCAKeyOutsideMCCBlock` now mirrors the SSL fish scanner by calling `fishMutatesEnvKey(trimmed, "NODE_EXTRA_CA_CERTS")` before `parseFishExportLine`.
- Regression tests cover `set -e NODE_EXTRA_CA_CERTS`, `set --erase NODE_EXTRA_CA_CERTS`, `set --unexport NODE_EXTRA_CA_CERTS`, and the write path where a valid fish Node CA managed block is followed by `set -e NODE_EXTRA_CA_CERTS`.
- The write-path regression requires `ErrUserCustomValue` and byte-identical profile preservation, so a later fish erase/unexport command cannot remain effective after a reported-success persistence call.

### Verification

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### Eighth Review Conclusion

The eighth-round Node CA fish erase/unexport Medium finding is resolved in code and regression tests. The automated verification suite passed, and no new directly exploitable security defect was found in this self-review. Native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.

## Ninth Independent Review — 2026-07-13

### Confirmed Resolved

- The eighth-round fish erase/unexport finding is resolved. `profileHasNodeCAKeyOutsideMCCBlock` now checks `fishMutatesEnvKey(trimmed, "NODE_EXTRA_CA_CERTS")` before parsing fish exports.
- Temporary review tests confirmed `set -e NODE_EXTRA_CA_CERTS`, `set --erase NODE_EXTRA_CA_CERTS`, and `set --unexport NODE_EXTRA_CA_CERTS` now fail closed, and a managed fish Node CA block followed by `set -e NODE_EXTRA_CA_CERTS` returns `ErrUserCustomValue` without mutating the profile.
- The earlier POSIX declaration/mutation, malformed marker, shared marker/idempotency, symlink marker, and cert-chain regeneration paths remain covered by automated tests.

### Findings

No new reproducible logic defect or directly exploitable security defect was found in this review.

### Verification

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### Ninth Review Conclusion

Approved for merge from the reviewed logic/security scope. Native Linux restarted-shell/long-conversation validation remains pending and is not claimed complete.
