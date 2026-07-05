# Node Extra CA Certs Auto Setup Review Notes

Date: 2026-07-05
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
