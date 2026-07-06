# Node Extra CA Certs Auto Setup Review Notes

Date: 2026-07-06
Reviewers: Codex and Claude Code

## Scope

Reviewed commits `2416c96..0b2cf78`, covering the F-1 profile scan changes, F-4 marker identity binding, privileged Node CA persistence refusal, platform privilege detection, tests, and localized instructions.

## Key Findings And Resolutions

1. The Linux bootstrap suite is not clean.
   - Evidence: `go test ./internal/bootstrap -count=1`, the race run, and `go test ./... -count=1` fail at `TestWritePOSIXProfileNodeCA_SymlinkTargetNotFollowed` because option 1b now follows a POSIX profile symlink for an unprivileged user while the old test still asserts the previous no-follow behavior.
   - Resolution required: update the POSIX test pair to explicitly verify privileged fail-closed behavior and unprivileged symlink compatibility, matching the PowerShell coverage.
2. F-1 remains fail-open on profile read errors.
   - Evidence: both `scanPwshProfilesForCustomValue` and `scanPOSIXProfilesForCustomValue` discard `os.ReadFile` errors. A focused overlay test reproduced `launchctl setenv` being called after an unreadable profile containing a custom value was treated as clean.
   - Resolution required: treat `os.IsNotExist` as an absent profile, but propagate every other read error before `setx` or `launchctl` is allowed.
3. Windows privilege detection fails open when token inspection fails.
   - Evidence: `privilegedByOS` returns false on `OpenProcessToken` failure, and `windows.Token.IsElevated` also returns false when `GetTokenInformation` fails.
   - Resolution required: represent unknown/error separately and refuse Node CA persistence on an indeterminate privilege state. Native Windows fault-injection coverage is still needed.
4. F-4 Unix identity enforcement behaves as claimed.
   - Evidence: the missing/mismatched UID tests and matching-marker test pass on Linux as uid 1000.
5. The privileged refusal closes the normal `PersistNodeCACert` path only.
   - Resolution: retain the disclosed `PersistRoot`, parent-directory symlink/reparse-point, and TOCTOU risks as explicit follow-up scope; do not describe all privileged profile mutation as closed.

## Final Review Conclusion

Not approved for merge yet. No medium-or-higher exploitable security finding survived attack-path calibration, but the submitted verification report is inaccurate: the Linux bootstrap and full Go suites fail, and the F-1 three-state scan still has a reproduced read-error fail-open branch. Fix those issues and make Windows privilege-query errors fail closed before requesting another review.

## Residual Notes

- Native Windows junction/reparse-point and token-query failure tests were not run.
- Native macOS `launchctl` behavior was not run.
- `PersistRoot` and descriptor-relative filesystem hardening remain separate follow-up work.

## Follow-up Review — `df3c96f..8cf4bf8`

### Verified improvements

- POSIX symlink coverage now matches option 1b: privileged runs fail closed and unprivileged runs follow the profile symlink.
- Ordinary profile read failures are propagated before `setx`/`launchctl` on Unix-like systems.
- Privilege detection errors now resolve to the rejecting state. The Windows implementation explicitly checks `OpenProcessToken`, `GetTokenInformation`, and the returned `TOKEN_ELEVATION` length.
- All focused new tests pass, `go vet ./internal/bootstrap` passes, and Windows/macOS test binaries cross-compile.

### Remaining blockers

1. The GLM claim that Linux is clean is false. These commands all exit non-zero and fail the same three tests:
   - `go test ./internal/bootstrap -count=1`
   - `go test -race ./internal/bootstrap -count=1`
   - `go test ./... -count=1`

   Failing tests: `TestPersistNodeCACert_Windows_SetxSuccess_ProfileFails_ReturnsPartial`, `TestPersistNodeCACert_Darwin_LaunchctlSuccess_ProfileFails_ReturnsPartial`, and `TestWritePwshProfileNodeCA_PartialFailure_ReturnsPartial`. Their fixtures put a regular file in an ancestor position. Linux returns `ENOTDIR`, so the new pre-scan correctly stops before environment mutation, while the tests still expect the old partial-success path.

2. `readProfile` is not fully fail-closed on Windows. It treats every `os.IsNotExist` error as a safely creatable absent profile. Go maps Windows `ERROR_PATH_NOT_FOUND` to `ErrNotExist`, and Windows may return that error when a non-leaf component is not a directory. The pre-scan can therefore authorize `setx` before the writer discovers that the profile cannot be created.

   Required fix: distinguish a genuinely absent leaf from an invalid/non-directory ancestor. Validate the parent chain or nearest existing ancestor before treating `IsNotExist` as creatable. Add a native Windows test that creates a file-as-parent path and asserts that `setxEnvVar` is not called. Update the three stale Linux tests so they assert early failure/no global mutation; use an explicit hook if partial-success behavior still needs independent coverage.

### Follow-up conclusion

Still not approved for merge. No reportable security vulnerability survived attack-path analysis because the top-level privileged-run guard limits the Windows defect to same-user state, but the fail-closed contract is still platform-incomplete and the branch's required Linux test suites are red.

## Follow-up Review — `fd388b7..7ec52de`

### Verified resolutions

- The three previously failing Linux tests now use deterministic `writeFileSync` fault injection and pass.
- File-as-parent paths are rejected before `setx`; the new integration test passes.
- `go test ./internal/bootstrap -count=1`, `go test -race ./internal/bootstrap -count=1`, and `go test ./... -count=1` all pass on Linux.
- `go vet ./internal/bootstrap` and Windows/macOS test-binary cross-compilation pass.

### Missing-root resolution

`validateParentChain` now delegates to a testable `validateParentChainWithStat` traversal. When traversal reaches a root whose stat result is still `IsNotExist`, it returns an error instead of treating the root as creatable. This prevents an absent Windows drive or UNC root from authorizing `setx`.

Coverage includes a platform-independent injected-stat regression test and a Windows-only integration test that selects an unused drive letter and asserts `setxEnvVar` is not called. The Windows test binary cross-compiles successfully; native execution remains the final platform check.

### Follow-up conclusion

Approved from the Linux review environment, pending the native Windows test run. The reported Linux regression, file-as-parent defect, and missing-root F-1 branch are closed; no reportable security vulnerability remains within this change scope.

## Merge And Release Readiness Review — `main...2bb9d2c`

### Scope

Reviewed all 23 branch commits from merge base `eb96f96` through `2bb9d2c`, including the Node CA persistence work, Windows environment refresh follow-up, setup-script guidance, and the three later Windows icon commits.

### Release Blockers

1. Relative data directories persist a working-directory-dependent CA path.
   - `resolveDataDir` returns an explicit value such as `./data` unchanged, and `cert.Manager.GetCACertPath` therefore returns a relative `data/ca.crt` path.
   - The new persistence code writes that relative value directly to `setx`, `launchctl`, POSIX profiles, and the idempotency marker. The PowerShell block additionally guards the assignment with `Test-Path`, so a client started from another directory does not set `NODE_EXTRA_CA_CERTS` at all.
   - Required resolution: canonicalize the CA path to an absolute path before persistence and marker comparison, with regression tests starting from `-data ./data` and a different client working directory.
2. Existing environment-level user configuration is overwritten.
   - The Windows and macOS paths scan profile text only, then unconditionally call `setx` or `launchctl setenv`. A focused overlay test set an existing corporate `NODE_EXTRA_CA_CERTS`; the Windows path still called `setx` and the test failed as expected.
   - Required resolution: inspect the actual persistence layer/current environment and preserve a non-MCC value. Add Windows registry/session and macOS session regression tests, not only profile-content tests.
3. Required native platform acceptance remains open.
   - The feature spec still records `Progress: 0 / 7 planned`, and its end-to-end Windows/macOS/Linux verification checklist is unchecked.
   - Windows/macOS cross-compilation is useful but does not execute token, registry, profile, missing-volume/UNC, or `WM_SETTINGCHANGE` behavior. At minimum, run and archive the Windows-native suite and the Windows Orca/Node end-to-end flow before release.

### Verification Evidence

- `make test` passed, including the full repository race suite.
- `go vet ./...` passed.
- `npm --prefix internal/frontend test` passed: 158 tests, 0 failures.
- `npm --prefix internal/frontend run build` passed and left the worktree clean.
- All six release targets built with the release script's `GOOS`/`GOARCH`, `CGO_ENABLED=0`, `-trimpath`, and release `ldflags` shape.
- Windows amd64 and arm64 executables both contain a `.rsrc` section. The committed `.ico` and `.syso` regenerated byte-for-byte from the current `make icon` recipe.
- `go mod tidy -diff`, `go mod verify`, and `git diff --check` passed.
- The local branch is three commits ahead of `origin/feat/node-extra-ca-certs-auto-setup`; those unpushed commits are the Windows icon changes.

### Security Conclusion

No reportable security vulnerability survived the diff security review. The two blockers above are same-user configuration integrity and correctness defects. Residual hardening notes remain: profile replacement is not atomic/descriptor-relative, parent-chain checks retain a TOCTOU window, and `make icon` uses the unpinned developer dependency `github.com/akavel/rsrc@latest`.

### Final Review Conclusion

Not approved for merge or release. Fix the relative-path persistence and environment-level custom-value overwrite, add regression coverage, complete native Windows acceptance, and update the feature verification record before requesting another release-readiness review.

## Correctness Fix Follow-up — `a2c0d4b..b3bd435`

### Verified Resolutions

- `tryPersistNodeCA` now resolves the CA path to an absolute path before stat, lookup, persistence, and marker operations. A red-green regression test proves that a relative `-data`-derived CA path reaches the adapter and marker as an absolute path.
- The executor now reads the existing platform value before mutation. A different non-MCC value returns `ErrUserCustomValue`; a matching marker no longer hides a user value set later.
- A prior marker path bound to the current user authorizes only that exact old MCC-managed value to migrate, preserving CA relocation while rejecting unrelated custom values.
- Lookup errors fail closed before `setx`, `launchctl setenv`, or profile writes.
- Windows checks inherited process state and then HKCU; macOS checks process state and then `launchctl`; other platforms check process state while retaining profile scanning.

### Verification

- Focused red/green tests, full bootstrap tests, bootstrap race tests, `make test`, `go vet ./...`, `go mod tidy -diff`, and `go mod verify` passed.
- Frontend tests passed: 158 tests, 0 failures; the production frontend build passed and produced no tracked changes.
- Linux/macOS/Windows amd64 and arm64 release build shapes all passed.
- Windows amd64/arm64 and macOS bootstrap test binaries cross-compiled successfully.

### Follow-up Conclusion

The two code-level merge blockers are resolved and the branch is approved for merge from this automated Linux review environment. Release approval still requires native Windows execution of the token/registry/profile/missing-root/environment-broadcast tests and the documented Orca/Node end-to-end flow; macOS/Linux native end-to-end records remain part of the original all-platform acceptance task.
