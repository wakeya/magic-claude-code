# TLS Plaintext Alert Diagnostics Review Notes

Date: 2026-07-11<br>
Reviewers: Codex and Claude Code

## Scope

Reviewed commits `da83af1`, `5f00d56`, and `efb48e7` against `3308aca`, covering the incremental inbound TLS-record parser, handshake-error logging integration, tests, and the `bad record MAC / unknown_ca` operational guidance.

## Key Findings And Resolutions

1. **Logic defect — the TLS alert mapping block from 111 through 117 is shifted or invalid.**
   - `alertName` starts the RFC 6066/TLS 1.3 extension alerts at 113, but the correct assignments are `certificate_unobtainable=111`, `unrecognized_name=112`, `bad_certificate_status_response=113`, `bad_certificate_hash_value=114`, `unknown_psk_identity=115`, and `certificate_required=116`. The current table omits 111–112, shifts the names assigned to 113–116, and incorrectly assigns `unknown_psk_identity` to 117. For example, alert 115 is logged as `bad_certificate_status_response` instead of `unknown_psk_identity`.
   - Resolution required: correct cases 111–116, remove or correctly define case 117 for the intended protocol version, add the corrected values to the table-driven test, and include Go 1.26 alert 121 (`encrypted_client_hello_required`) if the local standard-library mapping is intended to be complete. This does not affect the verified `unknown_ca=48` path.

2. **Security review — no exploitable memory, disclosure, or log-injection defect was found in the parser.**
   - The wrapper retains only fixed-size parser state and two alert bytes, does not retain or log ClientHello/session material, skips payloads by declared record length without allocation, and formats unknown peer-controlled bytes numerically. CPU work is linear in bytes already consumed by the TLS handshake and remains bounded by the existing handshake deadline/concurrency limit.
   - Resolution: acceptable.

3. **Logic review — fragmented records and payload false positives are handled correctly.**
   - The parser maintains the five-byte header and remaining payload length across reads. A record-shaped sequence inside Handshake or AppData payload is skipped as payload rather than reparsed, and encrypted TLS 1.3 alerts remain outer AppData records.
   - Resolution: acceptable for diagnostic enrichment; the original Go handshake error remains authoritative and unchanged.

4. **Test gap — tests exercise `feed` and `hint` separately but not the production log integration.**
   - No test drives `alertDetectingConn.Read` through `tlsListener.handleConn` and asserts that a complete plaintext alert is appended while malformed/truncated input is not.
   - Resolution recommended: add one listener/log integration regression. This is a maintenance gap rather than a demonstrated runtime or security defect.

5. **Operational verification — the CA-side fix remains effective in the observed environment.**
   - Container logs since 2026-07-11 02:45 contain no `bad record MAC`, `unknown_ca`, or plaintext-alert handshake errors while subsequent requests continued successfully. The FAQ correctly warns that `SSL_CERT_FILE` must reference the system CA bundle rather than the standalone MCC CA file.
   - Resolution: observed runtime behavior is consistent with the certificate-trust root cause. The logging enhancement diagnoses future failures; it is not itself the client trust fix.

## Verification

- `go test ./internal/proxy -count=1` — passed.
- `go test -race ./internal/proxy -count=1` — passed.
- `go vet ./internal/proxy` — passed.
- `go test ./... -count=1` — passed for all packages.
- `git diff --check 3308aca..HEAD` — clean.
- Runtime container log scan since 2026-07-11 02:45 — no matching TLS handshake errors.

## Final Review Conclusion

The `unknown_ca(48)` diagnosis and fixed-memory parser are functionally and security-wise sound, and the client-side CA correction is working in the observed runtime. No security blocker was found. One functional accuracy defect remains in the alert-name mapping block 111–117, so the commits should be corrected before pushing or releasing; the missing production log-integration test is a recommended follow-up.

## Residual Notes

- The active `codex-security` diff-scan skill was unavailable in this session; equivalent manual source-to-log, resource-exhaustion, disclosure, injection, and concurrency checks were performed before this archive was written.
- The local branch is `main`, two commits ahead of `origin/main`, and the worktree was clean before these review notes were added.

## Resolution Re-review (2026-07-11)

The mapping defect is resolved in commit `efb48e7`:

- Alerts 111–116 now match the Go 1.26 standard-library assignments.
- The invalid `unknown_psk_identity=117` case was removed; `unknown_psk_identity` is correctly assigned to 115.
- Alert 121 is reported as `encrypted_client_hello_required`.
- The table-driven test now covers 111–116, 120, and 121.

`TestReadDrivesFeed` verifies the real `net.Conn.Read → feed → detected` wrapper path with `net.Pipe`. `TestTLSListenerAlertHintOnUntrustedCert` additionally drives `tlsListener.handleConn` end-to-end: by forcing TLS 1.2 it makes the client's certificate-verification-failure alert plaintext (pre-ChangeCipherSpec), and asserts the failure log preserves the original Go handshake error while appending the `client sent plaintext ... alert` hint. The earlier listener/log integration recommendation is now fully resolved.

Fresh verification passed:

- `go test ./internal/proxy -count=1`
- `go test -race ./internal/proxy -count=1`
- `go vet ./internal/proxy`
- `go test ./... -count=1`
- `git diff --check 3308aca..HEAD`

### Re-review Conclusion

No remaining logic or security blocker was found. The alert mapping correction is accurate, fixed-memory and no-raw-byte security properties are preserved, the listener/log integration is now covered by `TestTLSListenerAlertHintOnUntrustedCert`, and all focused/race/full tests pass. The three local commits are suitable to push.

## Independent Documentation Re-review (2026-07-11)

The implementation and its runtime tests remain sound, but the newly archived specification and review evidence introduce documentation blockers:

1. **Recorded verification is false.**
   - Both review notes say `git diff --check 3308aca..HEAD` is clean. A fresh run against the current four-commit branch exits 2 and reports trailing whitespace in both specs and both review notes. Markdown hard breaks may be intentional, but the command/result must not be recorded as clean unless the files or the verification rule are changed.

2. **The raw-prefix-buffer rationale is factually incorrect.**
   - Both specs claim a roughly 1538-byte ClientHello would overflow the previous prefix buffer. The diagnostic implementation under review used a 4096-byte limit, and the captured connection was about 1550 bytes including the alert, so that specific trace fit. The valid objections are that a first-N-byte buffer has no general guarantee that a later alert is retained, the old write did not strictly enforce its nominal cap, and production should not retain/log raw handshake bytes.

3. **Runtime annotation evidence is overstated.**
   - Task 2 says runtime long-conversation failures were observed with the new annotation. After the CA correction, the reviewed runtime logs contain no such failures; the annotation is demonstrated by the TLS 1.2 integration test, while the pre-fix byte dump was produced by temporary raw capture. Replace the runtime claim with the evidence actually available.

4. **The specs do not strictly follow the repository's single-file plan rules.**
   - `sdd-docs/features/README.md` requires each Task Plan to use traceable TDD checklist steps covering failing test, confirmed failure, minimal implementation, passing confirmation, regression verification, exact file paths, and expected command results. The five plans are numbered prose rather than checklists and do not record that required red/green sequence. Either bring the retrospective plans into compliance or explicitly document why this post-implementation archive is an exception.

5. **The integration test can be made more fail-closed.**
   - `TestTLSListenerAlertHintOnUntrustedCert` correctly drives `handleConn → logger`, but `dialErr == nil` calls `t.Skip` and the assertions only check generic `TLS handshake error` / `client sent plaintext` substrings. Prefer `t.Fatal` for unexpected success and assert the expected alert name/number plus a real Go error substring. Existing unit tests separately protect hint formatting, so this is a non-security test-hardening item rather than a demonstrated implementation defect.

Fresh independent verification:

- `TestTLSListenerAlertHintOnUntrustedCert`: 20/20 normal runs passed.
- The same integration test: 10/10 race runs passed.
- `go test ./internal/proxy -count=1`, `go vet ./internal/proxy`, and `go test ./... -count=1` passed.
- `git diff --check origin/main..HEAD` failed with exit 2 on the documented whitespace above.

### Independent Re-review Conclusion

No new proxy logic or security defect was found, and the mapping/log integration fixes are effective. Commit `7f9b2d4` should be amended before push because its specs and archived evidence contain reproducible inaccuracies and do not satisfy the plan-template rules they claim to follow. The integration assertion hardening is recommended and can be included in the same amend.

## Final Independent Re-review (2026-07-11)

Commit `aea8f25` resolves the substantive implementation and evidence items from the independent documentation review:

- Markdown hard breaks now use `<br>` and `git diff --check origin/main..HEAD` is clean.
- The raw-prefix-buffer rationale now uses the correct first-N retention, nominal-cap, and raw-data exposure arguments.
- The Task 2 verification entry now correctly attributes annotation evidence to `TestTLSListenerAlertHintOnUntrustedCert` and states that no post-CA-fix production failure was available.
- The spec explicitly declares itself a retrospective post-implementation archive instead of claiming a forward TDD sequence.
- The integration test now fails closed on unexpected handshake success and asserts both `bad_certificate [42]` and `remote error: tls: bad certificate`.

Two documentation consistency items remain before push:

1. Task 2's **Evidence** line in both specs still says runtime logs show the annotation, contradicting the corrected verification entry immediately below it.
2. Task 4 still documents `t.Skip` as the unexpected-success behavior and generic `client sent plaintext` / `TLS handshake error` assertions, while the final test uses `t.Fatal` and the exact alert/original-error assertions.

Commit hygiene note: `aea8f25` is labeled `docs:` but also changes `internal/proxy/server_test.go`. Either move that test hardening into `efb48e7` and re-amend the documentation commit, or use a commit subject that includes the test scope. This is maintenance-only.

Fresh independent verification:

- `TestTLSListenerAlertHintOnUntrustedCert`: 20/20 normal runs passed.
- The same test: 10/10 race runs passed.
- `go test ./internal/proxy -count=1`, `go vet ./internal/proxy`, and `go test ./... -count=1` passed.
- `git diff --check origin/main..HEAD` passed.
- The worktree was clean before this review-note update.

### Final Independent Conclusion

No remaining proxy logic or security defect was found. The runtime implementation and fail-closed integration test are ready; amend the two stale spec passages and add an explicit closure after the prior independent-review snapshot before pushing. The `docs:` commit-scope mismatch is recommended cleanup, not a blocker.

## Documentation Closure (2026-07-11)

The two remaining specification inconsistencies are resolved in the working tree:

- Task 2 Evidence now identifies `TestTLSListenerAlertHintOnUntrustedCert` as the annotation evidence and explicitly states that the CA fix eliminated the production failure before a post-fix runtime annotation could be observed.
- Task 4 now documents fail-closed `t.Fatal` behavior and the exact `bad_certificate [42]` / `remote error: tls: bad certificate` assertions implemented by the test.

Fresh verification after the bilingual spec update passed:

- `TestTLSListenerAlertHintOnUntrustedCert`: 20/20 normal runs.
- The same test: 10/10 race runs.
- `go test ./internal/proxy -count=1`.
- `go vet ./internal/proxy`.
- `go test ./... -count=1`.
- `git diff --check` on the final working-tree changes.

### Closure Conclusion

All identified functional, security, test, and documentation findings are resolved. No business-code change was made in this closure pass. The existing four commits remain unchanged as selected; these four documentation files are ready to be committed as a separate follow-up change.
