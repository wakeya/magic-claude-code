# JavaScript Worker Process Isolation Spec

**Backend entry:** `internal/providerquota/script.go` (`ScriptExecutor.parseRequest`, `ScriptExecutor.runExtractor`)
**Process entry:** `cmd/server/main.go` (internal worker dispatch before flag parsing)
**Test entry:** `internal/providerquota/script_worker_test.go`, `internal/providerquota/script_worker_client_test.go`, `internal/providerquota/script_test.go`
**References:** PR #37 (`797e3f9`, AI script generation and security hardening); PR #40 (`fbacfbd`, regex resource-abuse preflight); MEDIUM 3 follow-up in `sdd-docs/features/2026-07-28-security-fixes/spec_ZH.md`
**Stack:** Go 1.26, `os/exec`, `runtime/debug`, `golang.org/x/sys/unix|windows`, goja
**Last updated:** 2026-07-29
**Status:** implementing
**Progress:** 4 / 5

## Overall Analysis (Source Analysis)

### Symptom and root cause

Custom quota scripts execute JavaScript in two phases:

1. `parseRequest` creates a goja runtime, evaluates the full script, and exports `request`;
2. `runExtractor` creates another goja runtime, evaluates the script again, and calls `extractor(response)`.

The phases already have 200 ms / 500 ms `goja.Interrupt` timers. PR #40 also added regex preflight checks for common oversized `Array` calls, `Array.apply`, infinite loops, and large string literals. These controls mitigate common payloads but do not establish a memory-safety boundary:

- dynamic expressions such as `Array(Number("100000000"))` bypass literal regexes;
- JavaScript may complete a large allocation before interrupt handling runs;
- goja and the service share one Go process and all goroutines share one heap;
- Go's `fatal error: out of memory` normally cannot be recovered and terminates the service.

Goroutines, context cancellation, interrupts, and regexes therefore remain scheduling or fast-rejection tools, not OOM isolation.

### Threat model

The attack input is a saved custom script or an LLM-generated script entering prevalidation. A malicious LLM response, poisoned response sample, or administrator can submit a high-resource script.

The protected asset is the long-running MCC parent service and its memory availability. One worker-backed quota query may fail, but it must not OOM or crash the parent or make the parent consume unbounded worker output.

### Goals

1. Move all goja execution into short-lived child processes so worker OOM/crash does not terminate the parent;
2. preserve the currently supported `({request, extractor})` JavaScript contract;
3. preserve the existing behavior of separately evaluating the script in independent request and extractor runtimes;
4. keep placeholder substitution, secret sanitization, request validation, injected HTTP clients, same-origin redirect checks, and result normalization in the parent;
5. retain a single binary on Linux, macOS, Windows, and Docker;
6. place explicit limits on IPC input, stdout, stderr, elapsed execution, and worker memory.

### Non-goals

- Restricting existing JavaScript through a static AST allowlist;
- replacing extractors with Go expressions or a custom DSL;
- building a persistent worker pool;
- moving HTTP requests into workers;
- removing PR #40's regex fast-rejection layer;
- changing the admin API, frontend editor, or persisted config format.

### Options and decision

| Option | Description | Decision |
| --- | --- | --- |
| A. Two short-lived workers | Re-exec the current binary once for `parseRequest` and once for `runExtractor`; retain HTTP and normalization in the parent | **Adopted**: preserves two runtimes and injected HTTP clients |
| B. Entire `ExecuteScript` in one worker | Parse, request, extract, and normalize in the worker | Rejected: custom `HTTPClient`/Transport cannot be serialized and the secret/network isolation surface grows |
| C. Persistent worker pool | Reuse long-running workers | Rejected: state cleanup, crash recovery, and multi-request blast radius are not justified for low-frequency quota queries |
| D. Static AST allowlist | Extract request statically and restrict extractor syntax | Rejected: conflicts with full JavaScript compatibility |

### Architecture and data flow

```text
Parent ExecuteScript
  |
  |-- spawn mcc __script-worker
  |     stdin:  {version, operation:"parse_request", script}
  |     stdout: {version, ok, payload|error}
  |     worker: isolated goja runtime -> ScriptRequest -> exit
  |
  |-- parent: substitute placeholders (secrets injected only here)
  |-- parent: validateScriptRequest + doHTTPRequest
  |
  |-- spawn mcc __script-worker
  |     stdin:  {version, operation:"run_extractor", script, response_body}
  |     stdout: {version, ok, payload|error}
  |     worker: a new isolated goja runtime -> extractor result -> exit
  |
  |-- parent: normalizeExtracted + snapshot
```

Workers do not receive `placeholderValues`. Values for `{{apiKey}}` and similar templates continue to be substituted in Go in the parent and never enter a goja runtime. If an administrator violates the contract by hardcoding a secret in script source, that source still enters the worker; this feature does not change that pre-existing administrator-input behavior.

### Worker entry and protocol

Releases continue to contain only `mcc` / `mcc.exe`. Before locale loading, flag registration, or service initialization, `cmd/server/main.go` recognizes the exact internal argument `__script-worker`, calls `providerquota.RunScriptWorker(os.Stdin, os.Stdout)`, and exits.

The exported `RunScriptWorker` applies real process limits. Protocol unit tests call an internal `runScriptWorker` with an injected limiter and must not lower an irreversible rlimit or create a Windows Job in the parent test process:

```go
func RunScriptWorker(in io.Reader, out io.Writer) int {
    return runScriptWorker(in, out, applyScriptWorkerResourceLimits)
}
```

The protocol has one JSON request and one JSON response:

```go
const ScriptWorkerArg = "__script-worker"
const scriptWorkerProtocolVersion = 1

type scriptWorkerRequest struct {
    Version      int    `json:"version"`
    Operation    string `json:"operation"`
    Script       string `json:"script"`
    ResponseBody string `json:"response_body,omitempty"`
}

type scriptWorkerResponse struct {
    Version int             `json:"version"`
    OK      bool            `json:"ok"`
    Payload json.RawMessage `json:"payload,omitempty"`
    Error   string          `json:"error,omitempty"`
}
```

Only `parse_request` and `run_extractor` are accepted. Invalid versions, operations, field sizes, or JSON fail closed. stdout contains protocol JSON only; diagnostics must not be mixed into stdout.

### Resource boundaries

- Saved scripts retain the existing 64 KiB limit;
- upstream responses retain the existing 2 MiB limit;
- the worker stdin envelope is limited to 3 MiB;
- worker stdout is limited to 4 MiB, covering a supported result derived from a 2 MiB response plus JSON overhead;
- worker stderr collection is limited to 64 KiB and never reflected into API errors;
- goja interrupts remain 200 ms for parse and 500 ms for extraction;
- the parent adds a hard per-worker deadline including startup time and kills workers on context cancellation;
- non-race workers have a 128 MiB hard memory limit and a lower Go soft memory limit;
- race builds use a higher test limit because ThreadSanitizer has fixed shadow/stack allocations; a separate non-race focused test verifies the production 128 MiB boundary.

Linux/macOS workers use `unix.Setrlimit(RLIMIT_DATA, ...)`. Windows workers use a Job Object with `JOB_OBJECT_LIMIT_PROCESS_MEMORY` and assign the current worker to the Job. `debug.SetMemoryLimit` assists GC pressure control but is not the sole security boundary. A worker fails closed without executing script code if resource-limit initialization fails.

### Error behavior

| Scenario | Parent result |
| --- | --- |
| Normal JS syntax/runtime/extractor error | Preserve the current error shape, return `script_error`, and apply `sanitizeError` |
| goja interrupt | `script_error` with existing timeout semantics |
| Worker OOM, signal, or non-zero exit | `script_error` with a fixed worker-termination message |
| Worker hard timeout or context cancellation | `script_error` with a fixed worker timeout/cancel message |
| stdout overflow, malformed JSON, version mismatch | `script_error` with a fixed worker-protocol message |
| stderr contains script, response, or a secret | Never included in API errors or snapshots |

### Compatibility

- The script contract, config JSON, REST API, and frontend do not change;
- request and extractor phases still evaluate the script once each in independent runtimes;
- `ScriptExecutor.HTTPClient` remains in the parent, preserving Manager injection and custom TLS transports;
- request exports and extractor results cross JSON IPC; the supported contract already requires serializable object/array/scalar fields;
- process startup adds quota-query latency, but queries are low frequency and Manager concurrency is bounded; no persistent pool is added;
- `IsScriptWorkerInvocation` performs exact matching, leaving normal `--help`, `--version`, and unknown-flag behavior unchanged.

### Impact

| File | Responsibility |
| --- | --- |
| `internal/providerquota/script.go` | Split current goja code into worker-only in-process functions; route parent calls through a runner |
| `internal/providerquota/script_worker_protocol.go` | Internal argument, protocol version, operations, envelopes |
| `internal/providerquota/script_worker.go` | Single-request worker service, input limit, memory-limit initialization, response encoding |
| `internal/providerquota/script_worker_client.go` | `os.Executable` re-exec, hard deadlines, bounded IPC collection, error mapping |
| `internal/providerquota/script_worker_limit_*.go` | Linux/macOS rlimit, Windows Job Object, race-build differences |
| `internal/providerquota/script_worker_test.go` | Protocol, phase behavior, resource-initialization tests |
| `internal/providerquota/script_worker_client_test.go` | Test-binary re-exec, IPC bounds, crash/timeout/OOM tests |
| `internal/providerquota/script_test.go` | Full compatibility and ExecuteScript regression |
| `internal/providerquota/main_test.go` | Internal worker dispatch in test binaries |
| `cmd/server/main.go`, `cmd/server/main_test.go` | Internal worker dispatch in the production binary |
| `sdd-docs/features/README.md` | Register the bilingual spec |

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | ✅ | Define the protocol and split existing in-process goja operations | protocol + worker server | worker unit tests |
| 2 | ✅ | Implement current-binary re-exec client and hidden entry | client + main/TestMain dispatch | re-exec integration tests |
| 3 | ✅ | Add cross-platform memory, time, and IPC boundaries | limit files + bounded I/O | resource tests and cross-builds |
| 4 | ✅ | Integrate `ScriptExecutor` and prove behavior compatibility | `script.go` + regressions | full providerquota tests |
| 5 | ⬜ | Validate OOM isolation, run full regression, and write back evidence | validation evidence | race, vet, six builds |

## Requirements

### Deliverables

1. The MCC parent no longer creates or executes a goja runtime;
2. the two JavaScript phases run in separate short-lived workers;
3. the single binary can re-exec workers in production and providerquota test binaries;
4. workers expose a versioned, bounded, single-request JSON protocol;
5. Linux, macOS, and Windows all enforce a worker-process memory limit;
6. normal script results, error categories, HTTP-client injection, and secret handling remain compatible;
7. dynamic OOM payloads bypass PR #40's regex but can only terminate workers, after which the parent test process completes another successful query;
8. actual verification evidence is written back to both specs.

### Security invariants

1. No parent-process path calls `goja.New`, `RunString`, or an extractor function;
2. `placeholderValues` are not serialized into worker requests;
3. worker stdout/stderr is never executed, interpolated, or read without a bound;
4. unsuccessful worker responses cannot carry a payload;
5. protocol errors never echo script source, response bodies, stderr, or secrets;
6. a worker does not execute goja if resource-limit setup fails;
7. canceled or timed-out calls leave no worker process behind.

### Constraints

- Do not change public APIs, config schema, or the script contract;
- do not move HTTP transports into workers;
- do not use goroutines as the OOM isolation boundary;
- do not require a shell, external worker file, or platform installation;
- preserve `CGO_ENABLED=0` builds for all six release targets;
- use `apply_patch` for manual changes and follow TDD;
- update progress and evidence in this spec after every task;
- commit locally and do not push.

### Edge cases

- Empty input, oversized input, unknown version, unknown operation;
- worker executable lookup/start failure, non-zero exit, signal, panic;
- empty stdout, stdout overflow, stderr overflow, logs mixed into stdout, truncated response JSON;
- dynamic array/string OOM in parse;
- dynamic array/string OOM in extractor;
- infinite loop stopped by goja interrupt or the parent hard timeout;
- parent context canceled before spawn, during execution, or during output collection;
- upstream non-JSON string;
- a valid extractor result near 2 MiB;
- existing HTTP 307/308 body, same-origin, custom TLS-client behavior;
- race-detector memory overhead does not use the production 128 MiB acceptance value.

## Task Details

### Task 1: Protocol and worker-local goja operations

#### Requirements

**Objective** — Define the versioned single-request protocol and turn current goja code into in-process operations callable only by the worker server.

**Outcomes** — Add `script_worker_protocol.go` and `script_worker.go`; rename the current implementations in `script.go` to `parseRequestInProcess` / `runExtractorInProcess` without behavior changes.

**Evidence** — Direct `bytes.Buffer` calls to internal `runScriptWorker` with a no-op limiter export a complete `ScriptRequest` and extractor object/array; invalid protocol requests fail closed. Only re-exec child-process tests call the real `RunScriptWorker`.

**Constraints** — stdout contains exactly one JSON response; bound input before decoding; do not run scripts if limit setup fails; never pass the placeholder map; protocol tests must not alter the parent test process rlimit/Job.

**Edge Cases** — Invalid JSON, protocol versions 0/2, unknown operation, missing script, non-function extractor, non-JSON upstream body.

**Verification** — `go test ./internal/providerquota -run 'TestRunScriptWorker|TestScriptWorkerProtocol'` is green.

#### Plan

- [ ] First add `TestRunScriptWorkerParseRequest`, `TestRunScriptWorkerExtractor`, and `TestScriptWorkerRejectsInvalidProtocol` to `internal/providerquota/script_worker_test.go`; confirm they fail because the entry does not exist.
- [ ] Add `internal/providerquota/script_worker_protocol.go` with `ScriptWorkerArg`, the protocol version, both operations, request/response structs, and exact `IsScriptWorkerInvocation(args []string) bool`.
- [ ] Rename the current direct goja implementations in `internal/providerquota/script.go` to:
  ```go
  func parseRequestInProcess(script string) (*ScriptRequest, error)
  func runExtractorInProcess(script, responseBody string) (any, error)
  ```
  Preserve PR #40's preflight, the 200/500 ms interrupts, request JSON round-trip, and extractor `Export()`.
- [ ] Add `internal/providerquota/script_worker.go` with the real exported entry and an injectable limiter seam:
  ```go
  func RunScriptWorker(in io.Reader, out io.Writer) int
  func runScriptWorker(in io.Reader, out io.Writer, applyLimits func() (func(), error)) int
  ```
  The real entry applies limits before decoding at most 3 MiB, dispatches one operation, pre-marshals its payload, and encodes one response; protocol unit tests inject no-op/failing limiters.
- [ ] Run focused tests and `go test ./internal/providerquota`.
- [ ] Commit `feat(providerquota): add isolated script worker protocol`.

#### Verification

- [x] `go test ./internal/providerquota -run 'TestRunScriptWorker|TestScriptWorker' -count=1` — 7 tests passed.
- [x] `go test ./internal/providerquota -count=1` — 276 tests passed.

### Task 2: Process client and production/test entries

#### Requirements

**Objective** — Re-exec the current executable in `__script-worker` mode and use the same path in production and providerquota test binaries.

**Outcomes** — Add `script_worker_client.go` and `main_test.go`; dispatch workers before flags in `cmd/server/main.go`; expose internal parse/extract runner methods.

**Evidence** — The test process re-execs itself for parse and extract; `mcc --version` remains unchanged; unknown flags are not recognized as workers.

**Constraints** — Exact argument matching; no shell; no unrelated stdin inheritance; separate and bound stdout/stderr.

**Edge Cases** — `os.Executable` failure, spawn failure, pre-canceled context, empty stdout, version mismatch.

**Verification** — Focused providerquota re-exec tests and `go test ./cmd/server` are green.

#### Plan

- [ ] First add successful test-binary re-exec, cancellation, and malformed-response tests to `script_worker_client_test.go`; confirm the missing runner fails.
- [ ] Add:
  ```go
  type scriptWorkerRunner interface {
      ParseRequest(context.Context, string) (*ScriptRequest, error)
      RunExtractor(context.Context, string, string) (any, error)
  }
  ```
  `processScriptWorkerRunner` uses `exec.CommandContext(exe, ScriptWorkerArg)` and the protocol envelope.
- [ ] Implement bounded collectors: fail calls at 4 MiB stdout / 64 KiB stderr; map errors to fixed categories without appending stderr, script, or response data.
- [ ] Add `internal/providerquota/main_test.go` with a `TestMain` that calls `RunScriptWorker` only for exact worker invocation and otherwise calls `m.Run()`.
- [ ] Dispatch the same exact internal mode in `cmd/server/main.go` before locale/flags/services; add argument-recognition regressions to `cmd/server/main_test.go`.
- [ ] Run focused re-exec tests and both package suites.
- [ ] Commit `feat(providerquota): re-exec current binary for script workers`.

#### Verification

- [x] `go test ./internal/providerquota -run 'TestProcessScriptWorker|TestScriptWorkerInvocation|TestScriptWorkerRejectsTrailingInput' -count=1` — 11 tests passed.
- [x] `go test ./cmd/server ./internal/providerquota -count=1` — 305 tests passed in total.
- [x] Request and response decoding both require EOF after one JSON value; RED tests for trailing stdout logs and a second stdin object are now GREEN.

### Task 3: Cross-platform resource limits

#### Requirements

**Objective** — Apply production hard and Go soft memory limits before goja execution while retaining all six release builds.

**Outcomes** — Linux/macOS use RLIMIT_DATA; Windows uses a self-assigned Job Object; race builds use a dedicated test value; unsupported platforms fail closed.

**Evidence** — Limit initialization tests pass; a non-race Linux worker runs a normal fixture at 128 MiB; all six cross-builds succeed.

**Constraints** — 128 MiB is the non-race acceptance value; the soft limit is lower; keep the Job handle until exit; setup failures do not execute scripts.

**Edge Cases** — Windows already in an outer Job, Setrlimit platform/permission errors, race shadow memory.

**Verification** — Platform-focused tests, non-race normal/OOM tests, and six cross-builds pass.

#### Plan

- [ ] First test limit setup order and setup-failure fail-closed behavior; verify a normal non-race Linux fixture under 128 MiB.
- [ ] Add `script_worker_memory_default.go` (`!race`, 128 MiB hard and lower soft) and `script_worker_memory_race.go` (`race`, higher ThreadSanitizer-only values).
- [ ] Add `script_worker_limit_unix.go` (`linux || darwin`; use a neutral filename to avoid Go's implicit `_darwin.go` suffix constraint) calling `unix.Setrlimit(unix.RLIMIT_DATA, &unix.Rlimit{Cur: limit, Max: limit})`.
- [ ] Add `script_worker_limit_windows.go`: create a Job Object, set `JOB_OBJECT_LIMIT_PROCESS_MEMORY`, set `ProcessMemoryLimit`, and call `AssignProcessToJobObject(job, windows.CurrentProcess())`; retain the handle until return.
- [ ] Add an explicit fail-closed implementation for other platforms; apply the hard limit before `debug.SetMemoryLimit`.
- [ ] Run Linux tests and `CGO_ENABLED=0` builds for Linux/macOS/Windows amd64/arm64.
- [ ] Commit `feat(providerquota): enforce script worker resource limits`.

#### Verification

- [x] `go test ./internal/providerquota -run TestProcessScriptWorkerMemoryLimit -count=1 -v` — one non-race production-boundary test passed; after the dynamic array terminated its worker, the parent started a healthy worker.
- [x] `go test -race ./internal/providerquota -run 'TestProcessScriptWorker$|TestProcessScriptWorkerMemoryLimit' -count=1 -v` — the re-exec happy path passed and the production 128 MiB case skipped as designed in race builds.
- [x] `CGO_ENABLED=0` cross-builds for Linux/macOS/Windows amd64/arm64 — all six builds succeeded.
- [x] Debug record: Go filename rules implicitly constrained `script_worker_limit_linux_darwin.go` to Darwin; a neutral `script_worker_limit_unix.go` filename plus the explicit build tag restored the Linux implementation.

### Task 4: ScriptExecutor integration and full compatibility regression

#### Requirements

**Objective** — Route both production `ScriptExecutor` phases only through workers while leaving parent HTTP and normalization unchanged.

**Outcomes** — `ScriptExecutor` gains an internal runner; `parseRequest` / `runExtractor` become runner wrappers; existing script tests retain business behavior.

**Evidence** — Existing tests pass, including injected Manager clients, Qianwen form fixtures, redirect bodies, TLS, secret sanitization, and business errors.

**Constraints** — No in-process parent fallback; workers do not receive the placeholder map; `HTTPClient` stays in the parent.

**Edge Cases** — HTTP failure after parse does not spawn extract; 401/403 do not spawn extract; normalization failures remain `invalid_response`.

**Verification** — `go test -race ./internal/providerquota -count=1` passes and source checks prove only workers reach goja.

#### Plan

- [ ] Add spy-runner tests to `script_test.go`: parse/extract called once, HTTP failure calls parse only, and no placeholder map enters the runner; confirm current code fails them.
- [ ] Change:
  ```go
  type ScriptExecutor struct {
      HTTPClient   *http.Client
      workerRunner scriptWorkerRunner
  }
  ```
  `NewScriptExecutor` installs the process runner; same-package tests may inject spies/fakes.
- [ ] Route `parseRequest` / `runExtractor` through the runner; only `RunScriptWorker` may call the in-process goja functions.
- [ ] Preserve placeholder substitution, validation, HTTP, `sanitizeError`, and normalization ordering in `ExecuteScript`.
- [ ] Run focused spies, script tests, Manager tests, and the race-enabled providerquota suite.
- [ ] Commit `refactor(providerquota): execute javascript only in workers`.

#### Verification

- [x] Spy-runner RED failed to build because `ScriptExecutor.workerRunner` was absent; after integration, both focused ScriptExecutor worker tests passed.
- [x] `go test ./internal/providerquota -count=1` — 290 tests passed.
- [x] `go test -race ./internal/providerquota -count=1` — 289 tests passed (the production 128 MiB case skipped as designed).
- [x] Source call check — only `script_worker.go` calls `parseRequestInProcess` / `runExtractorInProcess` in production; parent `ExecuteScript` calls only the runner.
- [x] Debug record: `GenerateScript` constructed a zero-value `&ScriptExecutor{}` and therefore had no runner; it now uses `NewScriptExecutor(timeout)` without an in-process parent fallback.
- [x] Race performance regression: multi-round generation tests use budgets consistent with the production 30-second budget; the no-jitter scheduler threshold moved from one to two seconds while remaining clearly below the tested five-second jitter; focused and full race reruns passed.

### Task 5: OOM acceptance, full validation, and spec write-back

#### Requirements

**Objective** — Use real dynamic-memory payloads to prove PR #40 is no longer the final boundary and complete repository/release validation.

**Outcomes** — Parse/extractor OOM causes a fixed worker `script_error`; the same parent test then executes a normal script; IPC/errors do not leak secrets; both specs contain final evidence.

**Evidence** — Focused OOM tests exit 0; `make test`, vet, frontend test/build, and six release builds pass.

**Constraints** — Run OOM payloads only inside hard-limited workers; never construct large objects in the parent; never reflect fatal stderr.

**Edge Cases** — Dynamic arrays, dynamic repeat, both phases, fatal worker stderr, subsequent parent query.

**Verification** — Every command exits 0, the worktree contains only feature changes, and nothing is pushed.

#### Plan

- [ ] Add payloads such as `Array(Number("100000000")).fill(0)` and dynamic `"x".repeat(...)` to `script_worker_client_test.go`; cover both parse and extractor.
- [ ] For every OOM case, assert a bounded `script_error`, no script/response/stderr in the message, then run a normal successful script in the same parent process.
- [ ] Add stdout/stderr overflow, panic/non-zero exit, context cancellation, and protocol-version tests.
- [ ] Run:
  ```bash
  go test ./internal/providerquota -run 'TestScriptWorker.*(OOM|Memory|Output|Cancel)' -count=1 -v
  go test -race ./internal/providerquota ./internal/admin -count=1
  make test
  go vet ./...
  npm --prefix internal/frontend test
  npm --prefix internal/frontend run build
  ```
- [ ] Cross-build `./cmd/server` with `CGO_ENABLED=0` for Linux/macOS/Windows amd64/arm64 into a temporary output directory.
- [ ] Check `git status --short && git diff --stat`; update status, progress, checklist, and actual evidence in both specs.
- [ ] Commit `test(providerquota): verify script worker OOM isolation` and `docs(spec): record script worker isolation verification`.

#### Verification

- [ ] Not run during the specification stage; record actual focused-command output here after this task.
