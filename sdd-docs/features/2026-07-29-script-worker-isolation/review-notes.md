# JavaScript Worker Process Isolation Review Notes

Date: 2026-07-29
Reviewers: Codex and Claude Code

## Scope

Reviewed branch `feat/script-worker-isolation` through the post-review fixes against the bilingual feature spec. The review covered the worker IPC protocol, re-exec entrypoint, process lifecycle, Linux/macOS/Windows resource-limit design, parent-side script execution flow, secret boundaries, timeout behavior, and targeted production-binary worker probes.

The `codex-security` workflow requested by the security-review skill was not available in this environment, so this note records a manual security review with focused repro commands instead.

## Key Findings And Resolutions

1. High security defect: the macOS worker does not have the claimed hard memory limit.
   - Evidence: `internal/providerquota/script_worker_limit_unix.go` applies `RLIMIT_DATA` on both Linux and Darwin, while the macOS manual defines `RLIMIT_DATA` as the data segment / `sbrk` break limit. Go's Darwin runtime allocates heap pages with anonymous private `mmap`, and `debug.SetMemoryLimit` is documented as a soft runtime limit, not an OS hard boundary.
   - References: https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/getrlimit.2.html, https://go.dev/src/runtime/mem_darwin.go, https://pkg.go.dev/runtime/debug#SetMemoryLimit
   - Resolution: fixed by changing Darwin to fail closed instead of applying an ineffective `RLIMIT_DATA` limit. macOS builds still compile, but worker execution refuses to run until a verified Darwin hard-memory boundary is implemented.

2. High compatibility/availability defect: the Linux production worker cannot process response bodies that are valid under the existing 2 MiB HTTP limit.
   - Evidence: `internal/providerquota/script.go` accepts upstream response bodies up to 2 MiB, but the real Linux production worker aborts before returning a protocol response at 512 KiB and above. Probe matrix with `/tmp/mcc-script-worker-review-linux-amd64 __script-worker`: 64 KiB, 128 KiB, and 256 KiB succeeded; 512 KiB failed with `runtime/cgo: pthread_create failed`; 1 MiB and 1.5 MiB failed with `fatal error: runtime: cannot allocate memory`; 2 MiB failed with `pthread_create failed`.
   - Likely cause: `internal/providerquota/script_worker_limit_unix.go` sets a 128 MiB `RLIMIT_DATA` before reading stdin, and the Go runtime plus JSON/goja processing cannot reliably operate within that data-segment limit for allowed response sizes.
   - Resolution: fixed by raising the non-race worker hard limit to 512 MiB, keeping a lower 384 MiB Go soft limit, and adding real process-worker regressions for the existing 2 MiB response limit.

3. Medium compatibility defect: the 3 MiB JSON stdin envelope is undersized for worst-case supported response bodies.
   - Evidence: `maxResponseBodySize` is 2 MiB, but `script_worker_client.go` JSON-marshals `response_body` into a string before sending it to the worker, while `maxScriptWorkerInputSize` is only 3 MiB. A 2 MiB response made of quote characters produces a 4,194,488 byte request, exceeding the 3,145,728 byte worker input limit even before extractor logic runs.
   - Resolution: fixed by replacing the JSON-string request body with a framed stdin protocol: a bounded JSON header plus raw response-body bytes. A 2 MiB quoted response now stays near 2 MiB on stdin and succeeds through the production worker.

4. Low logic defect: generated-script validation uses the caller context instead of the generation timeout context.
   - Evidence: `internal/providerquota/script_generator.go` creates `callCtx` with the requested timeout and uses it for LLM calls, but both `executor.parseRequest` calls use the original `ctx`. A validation worker can therefore run beyond the configured generation timeout, up to the worker process timeout.
   - Resolution: fixed by passing `callCtx` to both generated-script validation calls and adding a regression where a sleeping validation worker returns within the generation timeout instead of the worker hard timeout.

## Final Review Conclusion

Approved after fixes, with one explicit platform limitation: Linux and Windows keep hard worker memory limits, while macOS fails closed instead of executing without a verified hard-memory boundary. The implementation now satisfies the feature's compatibility target for 2 MiB responses on Linux and no longer claims an unverified Darwin security boundary.

## Residual Notes

- A temporary parent-allocation amplification probe was attempted with large dynamic arrays. In this implementation the worker was killed before returning a large structured payload, so this was not escalated as a confirmed defect.
- Linux `RLIMIT_DATA` affects `mmap` only on Linux 4.7 and newer according to the Linux man page; deployments on older kernels would need an explicit compatibility decision: https://man7.org/linux/man-pages/man2/getrlimit.2.html
- Verified current baseline commands: focused RED/GREEN tests passed; `go test ./internal/providerquota -count=1` passed with 309 tests; production Linux worker probes succeeded for 2 MiB ASCII and 2 MiB quoted response bodies; dynamic `ArrayBuffer` OOM still terminates only the worker.

## Follow-up Review: AI Generation And Current Working Tree

Date: 2026-07-29
Reviewer: Codex

### Scope

Reviewed the current worktree's cumulative diff against `main` plus post-commit fixes. Scope covered AI-generation error detail display, loopback/private endpoints for admin-configured LLM providers, Cookie/sec_token dual-secret placeholder auditing, frontend i18n/build output, and the current worker OOM integration-test state.

### Key Findings And Resolutions

1. Medium acceptance defect: the `ExecuteScript`-level OOM mapping test was unstable/failing.
   - Evidence: `go test ./internal/providerquota -run 'TestProcessScriptWorkerMemoryLimit|TestProcessScriptWorkerExtractorMemoryLimit|TestProcessScriptWorkerDynamicStringMemoryLimit|TestScriptExecutorMapsWorkerMemoryTerminationToScriptError' -count=1 -v` passes the lower-level worker memory-limit tests, but both `TestScriptExecutorMapsWorkerMemoryTerminationToScriptError` subtests return `script worker execution timed out` while the test expects `script worker terminated unexpectedly`.
   - Impact: the parent still returns `script_error`, and the worker-isolation security boundary is not bypassed. However, the branch does not currently pass the providerquota package reliably, and the spec's "dynamic OOM maps to terminated" acceptance semantics are not true in this environment.
   - Status: resolved. The `ExecuteScript` test now uses the same deterministic 800 MiB `ArrayBuffer` payload as the lower-level hard-limit probes, which reliably triggers worker hard-memory termination instead of the process hard timeout. `go test ./internal/providerquota -count=1` now passes.

2. Security-boundary note: `NewConfiguredLLMClient` allows loopback/private LLM endpoints explicitly saved by an admin.
   - Evidence: `TestConfiguredLLMClientAllowsLoopbackEndpoint` passes, while `TestConfiguredLLMClientRejectsMetadataEndpoint`, default `TestLLMClientRejectsLoopback`, `TestLLMClientRejectsMetadata`, and `TestLLMClientRejectsDNSRebinding` still pass.
   - Impact: this is an intentional compatibility exception for local/private LLM proxies on the authenticated admin configuration path, not an unauthenticated SSRF opening. Metadata/link-local targets still fail closed, and redirects remain disabled.
   - Status: accepted as product compatibility. The PR description should call this out as an admin-trusted configuration exception, not a general SSRF relaxation.

3. Maintenance risk: `internal/providerquota/script_worker_limit_darwin.go` was an untracked source file.
   - Evidence: `git ls-files --others --exclude-standard internal/providerquota/script_worker_limit_darwin.go` returns the file. The local `GOOS=darwin GOARCH=amd64 go build ./cmd/server` success depends on it.
   - Impact: if this file is omitted from the PR/commit, the Darwin target will miss `applyScriptWorkerHardMemoryLimit`, fail to compile, and lose the macOS fail-closed behavior.
   - Status: resolved. The file is now marked as a new file and appears in `git status --short` / diff; it still must be included in the final commit.

### Verification

- `go test ./internal/providerquota -run 'TestSystemPromptContainsContract|TestAuditScript|TestScriptGenerator' -count=1`: passed, 36 tests.
- `npm --prefix internal/frontend test`: passed, 227 tests.
- `npm --prefix internal/frontend run build`: passed.
- `go test ./internal/admin -count=1`: passed, 175 tests.
- `go test -race ./internal/providerquota ./internal/admin -count=1`: passed, 481 tests.
- `go vet ./...`: passed.
- `git diff --check`: passed.
- `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go test -c ./internal/providerquota`, `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c ./internal/providerquota`, and `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./cmd/server`: compile checks passed.

### Final Review Conclusion

No new directly exploitable security bypass was found in this follow-up review; worker isolation, the secret boundary, default LLM SSRF protection, and metadata rejection still hold. The follow-up OOM mapping instability and untracked Darwin fail-closed file have both been addressed; the final commit still needs to include the new Darwin file and review notes.

## Second Follow-up Review: Post-fix Branch State

Date: 2026-07-29
Reviewer: Codex

### Scope

Reviewed the current branch state after the endpoint-compatibility, generated-script audit, worker memory-test, Darwin fail-closed, and frontend error-display fixes. The review focused on functional regressions and security issues in worker isolation, worker framing, LLM endpoint policy, secret handling, and generated-script validation.

### Findings

1. Medium security defect: the configured LLM endpoint policy claims metadata targets remain blocked, but the allow-configured path only rejects link-local, multicast link-local, and unspecified addresses.
   - Evidence: `NewConfiguredLLMClient` opts into `newLLMTransport(..., true)`, and `isBlockedLLMIP(ip, true)` only rejects `IsLinkLocalUnicast`, `IsLinkLocalMulticast`, and `IsUnspecified`. The configured-client regression test only covers `169.254.169.254`.
   - Impact: cloud metadata endpoints that are not link-local can pass the configured-client guard. Alibaba Cloud documents ECS metadata at `http://100.100.100.200/latest/...`, which is not covered by the current predicate. This matters because the feature explicitly documents that metadata targets remain blocked even while loopback/private LLM proxies are allowed.
   - Status: resolved. Added a shared cloud-metadata IP denylist used by both preflight and dial-time checks, covering `169.254.169.254` and `100.100.100.200`. Added regressions for default/configured clients and configured-client DNS rebinding to the Alibaba Cloud metadata address.

### Residual Notes

- macOS custom quota scripts intentionally fail closed until a verified hard-memory boundary is implemented.
- Admin-configured loopback/private LLM endpoints remain an intentional trusted-admin compatibility exception; metadata IPs, link-local, unspecified addresses, redirects, and default LLM client SSRF protections remain blocked.
- Linux hard memory enforcement still depends on kernel support for `RLIMIT_DATA` over `mmap`; older unsupported kernels need a separate deployment decision if they are in scope.
- The final PR/commit must include the new Darwin source file, frontend dist updates, and review notes.

### Verification

- `go test ./internal/providerquota ./internal/admin -count=1`: passed, 494 tests.
- `go test ./internal/providerquota -run 'TestConfiguredLLMClientRejectsMetadataEndpoint|TestLLMClientRejectsMetadata|TestConfiguredLLMClientRejectsMetadataDNSRebinding|TestConfiguredLLMClientAllowsLoopbackEndpoint|TestLLMClientRejectsDNSRebinding' -count=1 -v`: passed, 9 tests.
- `go test -race ./internal/providerquota ./internal/admin -count=1`: passed, 486 tests.
- `go test -p 1 ./... -count=1`: passed, 1858 tests.
- `npm --prefix internal/frontend test`: passed, 227 tests.
- `npm --prefix internal/frontend run build`: passed.
- `go vet ./...`: passed.
- `git diff --check`: initially failed only because existing review-note date lines used Markdown trailing spaces; this note removes those spaces.

### Conclusion

Approved from this review pass with the platform/deployment notes above.

## Third Follow-up Review: Post-metadata-fix Branch State

Date: 2026-07-29
Reviewer: Codex

### Scope

Reviewed the branch after the configured-client metadata denylist fix. Scope covered the LLM endpoint policy, default/configured LLM client behavior, DNS rebinding checks, worker framing and process limits, AI script-generation validation context, frontend generated-script error display, and the current working-tree diff.

### Findings

No new functional logic defect or directly exploitable security defect was found in this review pass.

### Verification Notes

- The configured LLM client still allows authenticated admin-configured loopback/private LLM proxies, while cloud metadata IPs (`169.254.169.254`, `100.100.100.200`), link-local, unspecified addresses, redirects, and default-client internal endpoints remain blocked.
- Worker execution still runs through the subprocess protocol with bounded framed input and bounded stdout/stderr. Darwin remains fail-closed until a verified hard-memory boundary exists.
- One full `go test -race ./internal/providerquota ./internal/admin -count=1` run initially failed in unrelated `TestSchedulerAppliesJitter` with a timing spread assertion. `manager.go` / `manager_test.go` are not changed in this branch; `go test -race ./internal/providerquota -run TestSchedulerAppliesJitter -count=5 -v` then passed 5/5, and a full race rerun passed.

### Verification

- `go test ./internal/providerquota -run 'TestConfiguredLLMClientRejectsMetadataEndpoint|TestLLMClientRejectsMetadata|TestConfiguredLLMClientRejectsMetadataDNSRebinding|TestConfiguredLLMClientAllowsLoopbackEndpoint|TestLLMClientRejectsDNSRebinding|TestScriptWorker|TestProcessScriptWorker' -count=1 -v`: passed, 40 tests.
- `go test ./internal/providerquota ./internal/admin -count=1`: passed, 494 tests.
- `go test -race ./internal/providerquota -run TestSchedulerAppliesJitter -count=5 -v`: passed, 5 tests.
- `go test -race ./internal/providerquota ./internal/admin -count=1`: passed on rerun, 486 tests.
- `go test -p 1 ./... -count=1`: passed, 1858 tests.
- `npm --prefix internal/frontend test`: passed, 227 tests.
- `npm --prefix internal/frontend run build`: passed.
- `go vet ./...`: passed.
- `git diff --check`: passed.

### Conclusion

Approved from this review pass. Remaining notes are explicit platform/deployment constraints, not merge-blocking functional or security findings.

## Fourth Follow-up Review: Frame Header Encoding And Input Limit

Date: 2026-07-29
Reviewer: Codex

### Scope

Reviewed the follow-up fix for the framed worker protocol boundary issues reported by k3: JSON header HTML escaping for `<`, `>`, and `&`, plus the one-byte frame delimiter in `maxScriptWorkerInputSize`.

### Findings

No new functional logic defect or directly exploitable security defect was found in this review pass.

### Verification Notes

- `encodeScriptWorkerRequest` now uses `json.Encoder.SetEscapeHTML(false)` for the trusted parent-to-child JSON header and strips the encoder-added trailing newline before appending the explicit frame delimiter. This preserves the decoder's `bytes.Cut(..., '\n')` framing while avoiding `\uXXXX` expansion for HTML-heavy legal scripts.
- `maxScriptWorkerInputSize` now includes `maxScriptWorkerHeaderSize + 1 + maxResponseBodySize`, matching the actual `header + '\n' + body` frame shape.
- The decoder still enforces bounded total input, bounded header, `DisallowUnknownFields`, JSON EOF, non-negative/maximum response body size, and exact `response_body_size == len(body)` checks.

### Verification

- `go test ./internal/providerquota -run 'TestEncodeScriptWorkerRequestKeepsHTMLEscapeHeavyHeaderSmall|TestEncodeScriptWorkerRequestMaxBodyFitsInputLimit|TestScriptWorkerRejectsInvalidProtocol|TestScriptWorkerRejectsOversizedInput|TestProcessScriptWorkerHandlesMaxResponseBody|TestProcessScriptWorkerHandlesEscapedMaxResponseBody' -count=1 -v`: passed, 9 tests.
- `go test ./internal/providerquota ./internal/admin -count=1`: passed, 496 tests.
- `go test -p 1 ./... -count=1`: passed, 1860 tests.
- `go test -race ./internal/providerquota ./internal/admin -count=1`: passed, 488 tests.
- `go vet ./...`: passed.
- `git diff --check`: passed.

### Conclusion

Approved from this review pass. The fix closes the two low-severity framing compatibility issues without weakening the worker protocol or script isolation boundary.

## Fifth Follow-up Review: Proxy Hardcoded Compatibility Endpoints

Date: 2026-07-29
Reviewer: Codex

### Scope

Reviewed the two new top-of-branch proxy commits:

- `fix(proxy): handle /api/hello connectivity probe`
- `fix(proxy): handle /v1/environment_providers list endpoint`

Scope covered exact hardcoded endpoint matching, method handling, local response shapes, interaction with the proxy fail-closed forwarding guard, and regressions for the existing worker-isolation branch.

### Findings

No new functional logic defect or directly exploitable security defect was found in this review pass.

### Verification Notes

- `/api/hello` is an exact hardcoded endpoint. `HEAD` returns `200` with no body; `GET` returns `200` with `{}`; other methods return `405` with `Allow: HEAD, GET`.
- `/v1/environment_providers` is an exact hardcoded endpoint. `GET` returns `{"environments":[]}`; other methods return `405` with `Allow: GET`.
- `/v1/environment_providers/cloud/create` is not matched by the new exact endpoint and still falls through to the local fail-closed non-model endpoint guard instead of forwarding to a provider.
- The new handlers return local empty compatibility data only. They do not expose secrets, do not read config, and do not expand the model-forwarding surface.

### Verification

- `go test ./internal/proxy -run 'TestIsHardcodedEndpoint|TestHandleHello|TestHandleEnvironmentProviders|TestBlockedEndpoint|TestEndpointPolicy' -count=1 -v`: passed, 90 tests.
- `go test ./internal/proxy -count=1`: passed, 539 tests.
- `go test -race ./internal/proxy -count=1`: passed, 539 tests.
- `go test ./internal/providerquota ./internal/admin -count=1`: passed, 496 tests.
- `go test -p 1 ./... -count=1`: passed, 1870 tests.
- `npm --prefix internal/frontend test`: passed, 227 tests.
- `npm --prefix internal/frontend run build`: passed.
- `go vet ./...`: passed.
- `git diff --check main...HEAD`: passed.

### Conclusion

Approved from this review pass. The new proxy compatibility endpoints are narrow, exact, local-only responses and do not weaken the branch's worker isolation, secret boundary, or proxy fail-closed forwarding policy.

## Deployment Note: Host Memory-Limit Dependencies

Date: 2026-07-29

Recorded as a follow-up to the frame-encoding review. This is a deployment caveat, not a code defect, and is consolidated in `spec.md` -> Deployment constraints. It is recorded here because it is not observable from worker logs: worker stdout is the protocol channel and the parent discards worker stderr content (only its overflow flag is consulted), so the effective limit cannot be surfaced from inside the worker without a protocol change.

- The worker's hard-memory limit silently clamps to the host hard cap (`current.Max`) in `applyScriptWorkerHardMemoryLimit`. A container whose `ulimit -d` (or a Windows nested-Job commit cap) is below the ~512 MiB production value can silently degrade the worker below the 2 MiB-response compatibility target — the same failure mode observed at 128 MiB in Finding #2 of the initial review. Operators must ensure the host hard data/commit limit is at least the production value.
- Linux `< 4.7` (`RLIMIT_DATA` does not cover `mmap`) and macOS fail-closed behavior remain as documented in the earlier residual notes.
