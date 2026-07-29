# JavaScript Worker 子进程隔离规格

**后端入口：** `internal/providerquota/script.go`（`ScriptExecutor.parseRequest`、`ScriptExecutor.runExtractor`）
**进程入口：** `cmd/server/main.go`（flag 解析前的内部 worker 分派）
**测试入口：** `internal/providerquota/script_worker_test.go`、`internal/providerquota/script_worker_client_test.go`、`internal/providerquota/script_test.go`
**参考来源：** PR #37（`797e3f9`，AI 生成脚本与安全加固）；PR #40（`fbacfbd`，正则资源滥用预检）；`sdd-docs/features/2026-07-28-security-fixes/spec_ZH.md` Follow-up MEDIUM 3
**技术栈：** Go 1.26、`os/exec`、`runtime/debug`、`golang.org/x/sys/unix|windows`、goja
**最后更新：** 2026-07-29
**状态：** implementing
**进度：** 1 / 5

## 整体分析（源站分析）

### 现象与根因

自定义额度脚本会在两个阶段执行 JavaScript：

1. `parseRequest` 创建 goja runtime，执行整个脚本并导出 `request`；
2. `runExtractor` 创建另一个 goja runtime，再次执行脚本并调用 `extractor(response)`。

两阶段已有 200 ms / 500 ms 的 `goja.Interrupt` 超时。PR #40 还增加了正则预检，拒绝常见的超大 `Array`、`Array.apply`、无限循环和超长字符串字面量。这些措施能缓解常见 payload，但不能形成内存安全边界：

- 动态表达式可绕过字面量正则，例如 `Array(Number("100000000"))`；
- JavaScript 可在 interrupt 生效前完成巨额分配；
- goja 与服务运行在同一 Go 进程，所有 goroutine 共享同一堆；
- Go 的 `fatal error: out of memory` 通常不可由 `recover` 捕获，会终止整个服务。

因此，goroutine、context、interrupt 和正则只能作为调度或快速拒绝机制，不能隔离 OOM。

### 威胁模型

攻击输入是已保存的自定义脚本或 LLM 生成后进入预检的脚本。攻击者可能通过恶意 LLM 输出、被污染的响应样例或拥有管理员权限的操作者提交高资源脚本。

本功能保护的资产是长期运行的 MCC 父服务进程及其内存可用性。worker 内的单次脚本查询允许失败，但不得导致父进程 OOM、崩溃或读取无界 worker 输出。

### 目标

1. 将所有 goja 执行移入短生命周期子进程，worker OOM/崩溃不终止父服务；
2. 完全兼容当前受支持的 `({request, extractor})` 脚本合同；
3. 保持请求阶段与 extractor 阶段分别执行一次独立脚本/runtime 的现有语义；
4. 保持父进程中的占位符替换、秘密脱敏、请求校验、HTTP client 注入、同源重定向检查和结果标准化；
5. 保持单二进制发布，兼容 Linux、macOS、Windows 和 Docker；
6. 对 IPC 输入、stdout、stderr、执行时间和 worker 内存设置明确上限。

### 非目标

- 不以静态 AST 白名单限制现有 JavaScript 语法；
- 不重写 extractor 为 Go 表达式或自定义 DSL；
- 不建立常驻 worker 池；
- 不把 HTTP 请求移入 worker；
- 不删除 PR #40 的快速正则预检；
- 不改变管理 API、前端脚本编辑器或持久化格式。

### 方案比较与决策

| 方案 | 描述 | 结论 |
| --- | --- | --- |
| A. 双短生命周期 worker | `parseRequest` 与 `runExtractor` 各重启当前二进制一次；父进程保留 HTTP 与标准化 | **采用**：维持两次独立 runtime 和 HTTP client 注入 |
| B. 整个 `ExecuteScript` 放入一个 worker | worker 内解析、请求、提取、标准化 | 不采用：无法序列化自定义 `HTTPClient`/Transport，扩大秘密与网络隔离面 |
| C. 常驻 worker 池 | 复用多个长期 worker | 不采用：状态清理、崩溃恢复和多请求影响面更复杂，额度查询频率不足以证明必要性 |
| D. 静态 AST 白名单 | 静态提取 request 并限制 extractor 语法 | 不采用：不符合“完全兼容现有 JavaScript”约束 |

### 架构与数据流

```text
父进程 ExecuteScript
  |
  |-- spawn mcc __script-worker
  |     stdin:  {version, operation:"parse_request", script}
  |     stdout: {version, ok, payload|error}
  |     worker: 独立 goja runtime -> ScriptRequest -> exit
  |
  |-- 父进程：替换占位符（秘密仅在这里注入）
  |-- 父进程：validateScriptRequest + doHTTPRequest
  |
  |-- spawn mcc __script-worker
  |     stdin:  {version, operation:"run_extractor", script, response_body}
  |     stdout: {version, ok, payload|error}
  |     worker: 新的独立 goja runtime -> extractor result -> exit
  |
  |-- 父进程：normalizeExtracted + snapshot
```

worker 不接收 `placeholderValues`。模板中的 `{{apiKey}}` 等值仍由父进程在 Go 层替换，不进入 goja runtime。若用户违反合同在脚本中硬编码秘密，该脚本文本仍会进入 worker；本功能不改变这一既有管理员输入行为。

### Worker 入口与协议

当前发布只包含 `mcc` / `mcc.exe`。`cmd/server/main.go` 必须在加载 locale、注册 flag 和初始化服务前识别内部精确参数 `__script-worker`，调用 `providerquota.RunScriptWorker(os.Stdin, os.Stdout)` 并退出。

导出的 `RunScriptWorker` 使用真实进程资源限制。协议单元测试必须调用带可注入 limiter 的内部 `runScriptWorker`，不得在测试父进程中直接降低不可恢复的 rlimit 或创建 Windows Job：

```go
func RunScriptWorker(in io.Reader, out io.Writer) int {
    return runScriptWorker(in, out, applyScriptWorkerResourceLimits)
}
```

协议使用单个 JSON 请求和单个 JSON 响应：

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

只接受 `parse_request` 与 `run_extractor`。版本、操作、字段大小或 JSON 无效时 fail closed。stdout 只输出协议 JSON；诊断信息不得混入 stdout。

### 资源边界

- 保存脚本继续受现有 64 KiB 限制；
- 上游响应继续受现有 2 MiB 限制；
- worker stdin envelope 上限为 3 MiB；
- worker stdout 上限为 4 MiB，足以容纳受支持的 2 MiB 响应派生结果及 JSON 开销；
- worker stderr 最多读取 64 KiB，且不得回显到 API 错误；
- goja 内部 interrupt 仍为 parse 200 ms、extractor 500 ms；
- 父进程对每个 worker 增加包含进程启动时间的硬超时，并在 context 取消时终止 worker；
- 非 race 构建：worker 硬内存上限 128 MiB，Go 软内存上限低于硬上限；
- race 构建：允许更高的测试上限，避免 ThreadSanitizer 固定 shadow/stack 分配与生产上限冲突；实际 128 MiB 边界另由非 race 定向测试验证。

Linux/macOS worker 使用 `unix.Setrlimit(RLIMIT_DATA, ...)`；Windows worker 使用 Job Object 的 `JOB_OBJECT_LIMIT_PROCESS_MEMORY` 并将当前 worker 分配到该 Job。`debug.SetMemoryLimit` 是辅助 GC 压力控制，不单独承担安全保证。资源限制初始化失败时 worker fail closed，不执行脚本。

### 错误行为

| 场景 | 父进程结果 |
| --- | --- |
| 普通 JS 语法/运行时/extractor 错误 | 保留现有错误文本形态，`script_error`，并经过 `sanitizeError` |
| goja interrupt | `script_error`，保持 timeout 语义 |
| worker OOM、signal、非零退出 | `script_error` + 固定 worker 终止文案 |
| worker 硬超时/context 取消 | `script_error` + 固定 worker timeout/cancel 文案 |
| stdout 超限、畸形 JSON、版本不匹配 | `script_error` + 固定 worker protocol 文案 |
| stderr 含脚本、响应或秘密 | 不进入 API 错误或快照 |

### 兼容性

- 脚本合同、配置 JSON、REST API 和前端无变化；
- request 和 extractor 仍在两个独立 runtime 中分别执行脚本一次；
- `ScriptExecutor.HTTPClient` 仍由父进程使用，Manager 测试注入与自定义 TLS transport 不变；
- request 导出和 extractor 结果通过 JSON IPC；受支持合同本就要求可序列化的对象/数组/标量字段；
- 进程启动会增加额度查询延迟，但查询低频且 Manager 并发已受限；不引入常驻池；
- `IsScriptWorkerInvocation` 使用精确参数匹配，普通 `--help`、`--version` 和未知 flag 行为不变。

### 影响面

| 文件 | 职责 |
| --- | --- |
| `internal/providerquota/script.go` | 将现有 goja 逻辑拆为仅供 worker 调用的 in-process 函数；父路径改调 runner |
| `internal/providerquota/script_worker_protocol.go` | 内部参数、协议版本、操作和 envelope |
| `internal/providerquota/script_worker.go` | worker 单请求服务、输入限制、内存限制初始化和响应编码 |
| `internal/providerquota/script_worker_client.go` | `os.Executable` 重启、硬超时、有限 IPC 收集和错误映射 |
| `internal/providerquota/script_worker_limit_*.go` | Linux/macOS rlimit、Windows Job Object、race 构建差异 |
| `internal/providerquota/script_worker_test.go` | worker 协议、阶段行为和资源初始化测试 |
| `internal/providerquota/script_worker_client_test.go` | 测试二进制重启、IPC 上限、崩溃/超时/OOM 测试 |
| `internal/providerquota/script_test.go` | 完整兼容与 ExecuteScript 回归 |
| `internal/providerquota/main_test.go` | 测试二进制内部 worker 分派 |
| `cmd/server/main.go`、`cmd/server/main_test.go` | 生产二进制内部 worker 分派 |
| `sdd-docs/features/README.md` | 登记中英文规格 |

## 开发检查清单

| # | 状态 | 任务 | 产物 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | ✅ | 定义协议并拆分现有 goja in-process 操作 | protocol + worker server | worker 单元测试 |
| 2 | ⬜ | 实现当前二进制重启 client 与隐藏入口 | client + main/TestMain dispatch | re-exec 集成测试 |
| 3 | ⬜ | 加入跨平台内存、时间和 IPC 边界 | limit 文件 + bounded I/O | 资源边界测试、交叉编译 |
| 4 | ⬜ | 接入 `ScriptExecutor` 并证明行为兼容 | `script.go` + 回归测试 | providerquota 全包测试 |
| 5 | ⬜ | OOM 攻击验证、全量回归和规格回写 | 验证证据 | race、vet、六平台构建 |

## 需求

### 交付物

1. 父 MCC 服务不再直接创建或执行 goja runtime；
2. 两个 JavaScript 阶段分别由独立短生命周期 worker 执行；
3. 单二进制可在生产和 `go test` 测试二进制中重启 worker；
4. worker 具备版本化、长度受限、单请求 JSON 协议；
5. Linux、macOS、Windows 均具备 worker 进程内存限制；
6. 正常脚本结果、错误分类、HTTP client 注入及秘密处理行为保持兼容；
7. 动态 OOM payload 能绕过 PR #40 正则，但只能终止 worker，父测试进程继续完成后续查询；
8. 中英文规格同步回写实际验证证据。

### 安全不变量

1. 父进程中不存在 `goja.New`、`RunString` 或 extractor 函数调用路径；
2. `placeholderValues` 不序列化到 worker 请求；
3. 不从 worker stdout/stderr 执行、格式化或无界读取内容；
4. 非成功 worker 响应不能携带 payload；
5. 协议错误不得回显脚本、响应 body、stderr 或秘密；
6. worker 资源限制设置失败时不得继续执行 goja；
7. context 取消或超时后不遗留 worker 进程。

### 约束

- 不改变公开 API、配置 schema 或脚本合同；
- 不将 HTTP transport 移入 worker；
- 不使用 goroutine 作为 OOM 隔离边界；
- 不依赖 shell、外部 worker 文件或平台额外安装；
- 保持 `CGO_ENABLED=0` 六平台构建；
- 手工修改使用 `apply_patch`，实现遵循 TDD；
- 每个任务完成后回写本规格进度和验证证据；
- 本地提交，不 push。

### 边界条件

- 空输入、超大输入、未知版本、未知 operation；
- worker 启动失败、可执行文件路径失败、非零退出、signal、panic；
- stdout 空、stdout 超限、stderr 超限、stdout 混入日志、响应 JSON 截断；
- parse 阶段动态数组/字符串 OOM；
- extractor 阶段动态数组/字符串 OOM；
- 无限循环由 goja interrupt 或父硬超时终止；
- 父 context 在启动前、执行中或读取输出时取消；
- 上游返回非 JSON 字符串；
- extractor 返回接近 2 MiB 的合法结果；
- HTTP 307/308 body、同源校验、自定义 TLS client 等既有行为不变；
- race 检测器内存开销不使用生产 128 MiB 验收值。

## 任务详情

### 任务 1：协议与 worker 内 goja 操作

#### 需求

**Objective（目标）** — 定义版本化单请求协议，把现有 goja 代码变成只能由 worker server 调用的 in-process 操作。

**Outcomes（成果）** — 新增 `script_worker_protocol.go` 与 `script_worker.go`；`script.go` 中现有实现改名为 `parseRequestInProcess` / `runExtractorInProcess`，行为和 interrupt 不变。

**Evidence（证据）** — 直接用 `bytes.Buffer` 调用注入 no-op limiter 的内部 `runScriptWorker`，parse 可导出完整 `ScriptRequest`，extractor 可导出对象/数组；非法协议 fail closed。真实 `RunScriptWorker` 只在 re-exec 子进程测试中调用。

**Constraints（约束）** — stdout 只有一个 JSON 响应；限制输入后再 decode；资源限制失败不运行脚本；不传 placeholder map；协议单测不得改变测试父进程的 rlimit/Job。

**Edge Cases（边界）** — 非法 JSON、版本 0/2、未知 operation、缺少脚本、extractor 非函数、非 JSON upstream body。

**Verification（验证）** — `go test ./internal/providerquota -run 'TestRunScriptWorker|TestScriptWorkerProtocol'` 全绿。

#### 计划

- [ ] 在 `internal/providerquota/script_worker_test.go` 先写 `TestRunScriptWorkerParseRequest`、`TestRunScriptWorkerExtractor`、`TestScriptWorkerRejectsInvalidProtocol`，确认因入口不存在而失败。
- [ ] 新增 `internal/providerquota/script_worker_protocol.go`，定义 `ScriptWorkerArg`、协议版本、两个 operation 和 request/response 结构；实现精确匹配的 `IsScriptWorkerInvocation(args []string) bool`。
- [ ] 在 `internal/providerquota/script.go` 将当前直接 goja 实现重命名为：
  ```go
  func parseRequestInProcess(script string) (*ScriptRequest, error)
  func runExtractorInProcess(script, responseBody string) (any, error)
  ```
  保留 PR #40 预检、200/500 ms interrupt、request JSON round-trip 和 extractor `Export()`。
- [ ] 新增 `internal/providerquota/script_worker.go`，导出真实入口并保留 limiter 注入 seam：
  ```go
  func RunScriptWorker(in io.Reader, out io.Writer) int
  func runScriptWorker(in io.Reader, out io.Writer, applyLimits func() (func(), error)) int
  ```
  真实入口先设置资源限制，再从最多 3 MiB 输入解码一次，根据 operation 调用对应 in-process 函数，将 payload 预先 `json.Marshal` 后编码一个响应；协议单测注入 no-op/failing limiter。
- [ ] 运行定向测试，确认 parse、extract 和非法协议全部通过；再运行 `go test ./internal/providerquota`。
- [ ] 提交：`feat(providerquota): add isolated script worker protocol`。

#### 验证

- [x] `go test ./internal/providerquota -run 'TestRunScriptWorker|TestScriptWorker' -count=1` —— 7 个测试通过。
- [x] `go test ./internal/providerquota -count=1` —— 276 个测试通过。

### 任务 2：进程 client 与生产/测试入口

#### 需求

**Objective（目标）** — 父进程通过 `os.Executable()` 启动当前二进制的 `__script-worker` 模式，并让生产二进制与 providerquota 测试二进制使用同一路径。

**Outcomes（成果）** — 新增 `script_worker_client.go` 和 `main_test.go`；`cmd/server/main.go` 在 flag 前分派 worker；runner 暴露 parse/extract 两个内部方法。

**Evidence（证据）** — 测试进程可重启自身完成 parse 和 extract；`mcc --version` 行为不变；未知 flag 不被误识别为 worker。

**Constraints（约束）** — 参数必须精确匹配；不经过 shell；不继承无关 stdin；worker stdout/stderr 分开且受限。

**Edge Cases（边界）** — `os.Executable` 失败、启动失败、context 预先取消、空 stdout、版本不匹配。

**Verification（验证）** — `go test ./internal/providerquota -run 'TestProcessScriptWorker|TestScriptWorkerInvocation'` 与 `go test ./cmd/server` 全绿。

#### 计划

- [ ] 在 `internal/providerquota/script_worker_client_test.go` 先写当前测试二进制 re-exec 的成功、取消和畸形响应测试；确认缺少 runner 时失败。
- [ ] 新增内部接口：
  ```go
  type scriptWorkerRunner interface {
      ParseRequest(context.Context, string) (*ScriptRequest, error)
      RunExtractor(context.Context, string, string) (any, error)
  }
  ```
  `processScriptWorkerRunner` 用 `exec.CommandContext(exe, ScriptWorkerArg)` 发送协议 envelope。
- [ ] 实现有限 stdout/stderr 收集器：达到 4 MiB / 64 KiB 时停止收集并使调用失败；任何错误只映射固定分类，不拼接 stderr、脚本或 response body。
- [ ] 新增 `internal/providerquota/main_test.go` 的 `TestMain`：仅在 `IsScriptWorkerInvocation(os.Args[1:])` 为真时调用 `RunScriptWorker`，否则 `m.Run()`。
- [ ] 修改 `cmd/server/main.go`，在 locale/flag/service 初始化前执行相同的精确分派；在 `cmd/server/main_test.go` 增加参数识别回归测试。
- [ ] 运行定向测试与 `go test ./cmd/server ./internal/providerquota`，确认生产入口和测试入口均可重启。
- [ ] 提交：`feat(providerquota): re-exec current binary for script workers`。

#### 验证

- [ ] 规格阶段尚未执行；完成本任务的定向命令后在此记录实际输出。

### 任务 3：跨平台资源限制

#### 需求

**Objective（目标）** — 在 goja 执行前为 worker 设置生产硬内存上限和 Go 软内存上限，并保持六个发布目标可编译。

**Outcomes（成果）** — Linux/macOS 使用 RLIMIT_DATA；Windows 使用 self-assigned Job Object；race 构建使用独立测试值；其他平台只作显式 unsupported/fail-closed。

**Evidence（证据）** — 资源限制初始化单测通过；Linux 非 race worker 在 128 MiB 下执行正常 fixture；六平台 `go build` 成功。

**Constraints（约束）** — 非 race 128 MiB 是安全验收值；软上限低于硬上限；Job handle 保持到 worker 退出；设置失败不执行脚本。

**Edge Cases（边界）** — Windows 已位于外部 Job、Setrlimit 权限/平台错误、race shadow memory。

**Verification（验证）** — 平台定向单测、非 race 128 MiB 正常/OOM 测试和六平台交叉编译通过。

#### 计划

- [ ] 先写资源限制调用顺序与失败 fail-closed 测试，并在非 race Linux 测试中验证正常脚本可在 128 MiB 下运行。
- [ ] 新增 `script_worker_memory_default.go`（`!race`，128 MiB hard、较低 soft）和 `script_worker_memory_race.go`（`race`，仅供 ThreadSanitizer 的较高值）。
- [ ] 新增 `script_worker_limit_linux_darwin.go`（build tags `linux || darwin`），调用 `unix.Setrlimit(unix.RLIMIT_DATA, &unix.Rlimit{Cur: limit, Max: limit})`。
- [ ] 新增 `script_worker_limit_windows.go`：创建 Job Object，设置 `JOB_OBJECT_LIMIT_PROCESS_MEMORY`，赋值 `ProcessMemoryLimit`，再 `AssignProcessToJobObject(job, windows.CurrentProcess())`；保持 handle 到 worker 返回。
- [ ] 新增其他平台 fail-closed 实现；在共同 worker 初始化中先应用硬上限，再调用 `debug.SetMemoryLimit`。
- [ ] 执行 Linux 定向测试；运行 Linux、macOS、Windows amd64/arm64 的 `CGO_ENABLED=0 go build ./cmd/server`。
- [ ] 提交：`feat(providerquota): enforce script worker resource limits`。

#### 验证

- [ ] 规格阶段尚未执行；完成本任务的定向命令后在此记录实际输出。

### 任务 4：接入 ScriptExecutor 与完全兼容回归

#### 需求

**Objective（目标）** — 使生产 `ScriptExecutor` 的两个阶段只能通过 worker runner 执行，同时保持父进程 HTTP 和标准化逻辑不变。

**Outcomes（成果）** — `ScriptExecutor` 增加内部 runner；`parseRequest` / `runExtractor` 成为 runner wrapper；现有脚本测试无业务行为变化。

**Evidence（证据）** — 现有 `script_test.go` 全绿；Manager 注入 client、Qianwen form fixture、重定向 body、TLS、错误脱敏和 business error 用例均通过。

**Constraints（约束）** — 父路径不得回退到 in-process goja；worker 不接收 placeholder map；`HTTPClient` 仍在父进程。

**Edge Cases（边界）** — parse 成功后 HTTP 失败时不启动 extractor worker；401/403 不启动 extractor；normalize 失败仍是 `invalid_response`。

**Verification（验证）** — `go test -race ./internal/providerquota -count=1` 全绿，并用 `rg` 确认父路径不直接调用 goja。

#### 计划

- [ ] 在 `script_test.go` 增加 spy runner 测试：parse/extract 各调用一次、HTTP 失败只调用 parse、runner 收不到 placeholder map；先确认现有结构不满足测试。
- [ ] 修改 `ScriptExecutor`：
  ```go
  type ScriptExecutor struct {
      HTTPClient   *http.Client
      workerRunner scriptWorkerRunner
  }
  ```
  `NewScriptExecutor` 注入 process runner；测试可在同包注入 spy/fake runner。
- [ ] 将 `parseRequest` / `runExtractor` 改为 runner 调用；仅 `RunScriptWorker` 可到达 in-process goja 函数。
- [ ] 保持 `ExecuteScript` 中 placeholder 替换、validation、HTTP、sanitizeError、normalize 顺序不变。
- [ ] 依次运行 spy 定向测试、`script_test.go`、Manager 测试和 providerquota race 全包测试。
- [ ] 提交：`refactor(providerquota): execute javascript only in workers`。

#### 验证

- [ ] 规格阶段尚未执行；完成本任务的定向命令后在此记录实际输出。

### 任务 5：OOM 验收、全量验证与规格回写

#### 需求

**Objective（目标）** — 用实际动态内存 payload 证明 PR #40 正则不再承担最终边界，并完成全仓及发布目标验证。

**Outcomes（成果）** — parse/extractor OOM 只能导致 worker 固定 `script_error`；同一父测试随后执行正常脚本成功；IPC 和错误不泄密；规格状态与证据完整。

**Evidence（证据）** — 定向 OOM 测试 exit 0；`make test`、vet、前端测试/build 和六平台构建通过。

**Constraints（约束）** — OOM payload 只能在受硬限制 worker 中执行；测试不得直接在父进程构造巨额对象；不向 API 回显 fatal stderr。

**Edge Cases（边界）** — 动态数组、动态 repeat、parse 与 extractor 两阶段、worker fatal stderr、父进程后续查询。

**Verification（验证）** — 全部命令 exit 0，工作树只包含本功能文件，不 push。

#### 计划

- [ ] 在 `script_worker_client_test.go` 增加能绕过现有字面量正则的 payload，例如 `Array(Number("100000000")).fill(0)` 和动态 `"x".repeat(...)`；分别覆盖 parse 与 extractor。
- [ ] 每个 OOM 用例断言：调用在硬超时内返回 `script_error`、错误不含脚本/response/stderr；紧接着使用同一父测试进程执行正常脚本并成功。
- [ ] 增加 stdout/stderr 超限、worker panic/非零退出、context cancel 和协议版本异常测试。
- [ ] 运行：
  ```bash
  go test ./internal/providerquota -run 'TestScriptWorker.*(OOM|Memory|Output|Cancel)' -count=1 -v
  go test -race ./internal/providerquota ./internal/admin -count=1
  make test
  go vet ./...
  npm --prefix internal/frontend test
  npm --prefix internal/frontend run build
  ```
- [ ] 用临时输出目录执行六个 `CGO_ENABLED=0 GOOS=<linux|darwin|windows> GOARCH=<amd64|arm64> go build ./cmd/server`，不向仓库写入二进制。
- [ ] `git status --short && git diff --stat` 核对范围；同步回写 `spec.md` / `spec_ZH.md` 的状态、进度、检查清单和实际证据。
- [ ] 提交：`test(providerquota): verify script worker OOM isolation` 与 `docs(spec): record script worker isolation verification`。

#### 验证

- [ ] 规格阶段尚未执行；完成本任务的定向命令后在此记录实际输出。
