# JavaScript Worker Process Isolation 审查记录

日期：2026-07-29
审查者：Codex 和 Claude Code

## Scope（范围）

审查 `feat/script-worker-isolation` 分支完成审查后修复后的实现，并对照中英文 feature spec。覆盖 worker IPC 协议、re-exec 入口、进程生命周期、Linux/macOS/Windows 资源限制设计、父进程脚本执行链、秘密边界、超时行为，以及真实生产二进制 worker 探针。

安全审查 skill 要求优先运行的 `codex-security` workflow 在当前环境不可用；本记录采用人工威胁建模和定向复现命令作为审查证据。

## Key Findings And Resolutions（关键发现与处理）

1. 高危安全缺陷：macOS worker 不具备规格声称的硬内存上限。
   - 证据：`internal/providerquota/script_worker_limit_unix.go` 同时在 Linux 与 Darwin 上应用 `RLIMIT_DATA`；macOS 手册将 `RLIMIT_DATA` 定义为 data segment / `sbrk` break 上限。Go Darwin runtime 使用 anonymous private `mmap` 分配堆页，而 `debug.SetMemoryLimit` 官方文档说明它是 Go runtime 软内存限制，不是 OS 硬边界。
   - 参考：https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/getrlimit.2.html、https://go.dev/src/runtime/mem_darwin.go、https://pkg.go.dev/runtime/debug#SetMemoryLimit
   - 处理：已修复。Darwin 改为 fail closed，不再应用无效的 `RLIMIT_DATA`。macOS 目标仍可编译，但在存在经验证的 Darwin 硬内存边界前，worker 不执行脚本。

2. 高危兼容性/可用性缺陷：Linux 生产 worker 处理不了现有 2 MiB HTTP 上限内的合法响应体。
   - 证据：`internal/providerquota/script.go` 允许上游响应体最大 2 MiB，但真实 Linux 生产 worker 在 512 KiB 及以上就无法返回协议响应。使用 `/tmp/mcc-script-worker-review-linux-amd64 __script-worker` 的探针结果：64 KiB、128 KiB、256 KiB 成功；512 KiB 失败并输出 `runtime/cgo: pthread_create failed`；1 MiB、1.5 MiB 失败并输出 `fatal error: runtime: cannot allocate memory`；2 MiB 失败并输出 `pthread_create failed`。
   - 可能根因：`internal/providerquota/script_worker_limit_unix.go` 在读取 stdin 前设置 128 MiB `RLIMIT_DATA`，Go runtime 加 JSON/goja 处理在该 data-segment 限制下无法稳定处理规格允许的响应尺寸。
   - 处理：已修复。非 race worker 硬上限提高到 512 MiB，Go 软上限保持为较低的 384 MiB，并增加真实进程 worker 回归覆盖现有 2 MiB 响应上限。

3. 中危兼容性缺陷：3 MiB JSON stdin envelope 无法覆盖最坏情况下的受支持响应体。
   - 证据：`maxResponseBodySize` 是 2 MiB，但 `script_worker_client.go` 先把 `response_body` JSON marshal 成字符串再发给 worker，`maxScriptWorkerInputSize` 只有 3 MiB。若 2 MiB 响应体由引号组成，worker 请求会膨胀到 4,194,488 字节，超过 3,145,728 字节输入上限，extractor 尚未执行就会失败。
   - 处理：已修复。stdin 协议改为 bounded JSON header + 原始 response-body bytes 的 framed 格式。2 MiB 全引号响应在 stdin 中保持接近 2 MiB，并已通过生产 worker 验证。

4. 低危逻辑缺陷：生成脚本校验使用了调用方 context，而不是生成超时 context。
   - 证据：`internal/providerquota/script_generator.go` 创建了带 timeout 的 `callCtx`，LLM 调用使用 `callCtx`，但两处 `executor.parseRequest` 仍传入原始 `ctx`。因此校验 worker 可能超出配置的生成超时时间，最多额外运行到 worker process timeout。
   - 处理：已修复。两处生成脚本校验都传入 `callCtx`，并增加 sleeping validation worker 回归，确认返回时间受生成 timeout 控制，而不是拖到 worker hard timeout。

## Final Review Conclusion（最终审查结论）

修复后可以合并，但带一个明确平台限制：Linux 与 Windows 保持 worker 硬内存边界，macOS 在没有经验证的硬内存机制前 fail closed。当前实现已满足 Linux 上 2 MiB 响应兼容目标，并且不再声称未验证的 Darwin 安全边界。

## Residual Notes（残余说明）

- 曾临时尝试用大动态数组复现父进程结构化 payload 分配放大；当前实现中 worker 会先被杀死，未能返回大型结构化 payload，因此不作为已确认缺陷升级。
- Linux `RLIMIT_DATA` 根据 Linux man page 只在 Linux 4.7 及以上影响 `mmap`；若部署支持更老内核，需要明确兼容策略：https://man7.org/linux/man-pages/man2/getrlimit.2.html
- 已验证当前基线命令：聚焦 RED/GREEN 测试通过；`go test ./internal/providerquota -count=1` 通过，共 309 个测试；Linux 生产 worker 对 2 MiB ASCII 与 2 MiB quoted response body 的探针均成功；动态 `ArrayBuffer` OOM 仍只终止 worker。

## Follow-up Review: AI Generation And Current Working Tree（后续审查：AI 生成与当前工作区）

日期：2026-07-29
审查者：Codex

### Scope（范围）

复审当前 worktree 相对 `main` 与已提交分支的叠加改动，覆盖：AI 生成错误明细展示、管理员配置 LLM endpoint 的 loopback/private 放行、Cookie/sec_token 双密钥占位符自检、前端 i18n/build 产物，以及 worker OOM 集成测试当前状态。

### Key Findings And Resolutions（关键发现与处理）

1. 中危验收缺陷：`ExecuteScript` 层 OOM 映射测试不稳定/失败。
   - 证据：`go test ./internal/providerquota -run 'TestProcessScriptWorkerMemoryLimit|TestProcessScriptWorkerExtractorMemoryLimit|TestProcessScriptWorkerDynamicStringMemoryLimit|TestScriptExecutorMapsWorkerMemoryTerminationToScriptError' -count=1 -v` 中，底层 worker memory limit 用例通过，但 `TestScriptExecutorMapsWorkerMemoryTerminationToScriptError` 两个子用例返回 `script worker execution timed out`，而测试期望 `script worker terminated unexpectedly`。
   - 影响：父进程仍返回 `script_error`，worker 隔离安全边界未被绕过；但当前分支无法稳定通过 providerquota 全包测试，且规格中“动态 OOM 映射为 terminated”的验收语义不成立。
   - 处理状态：已修复。`ExecuteScript` 层用例改为和底层 hard-limit 探针一致的 800 MiB `ArrayBuffer` payload，稳定触发 worker 硬内存终止而不是进程硬超时；`go test ./internal/providerquota -count=1` 已通过。

2. 安全边界说明：`NewConfiguredLLMClient` 允许管理员显式配置的 loopback/private LLM endpoint。
   - 证据：`TestConfiguredLLMClientAllowsLoopbackEndpoint` 通过，`TestConfiguredLLMClientRejectsMetadataEndpoint`、默认 `TestLLMClientRejectsLoopback`、`TestLLMClientRejectsMetadata`、`TestLLMClientRejectsDNSRebinding` 仍通过。
   - 影响：这是为了支持本机/内网 LLM 代理的有意兼容改动。它扩大的是已认证管理员配置路径，不是未认证 SSRF；metadata/link-local 仍 fail closed，redirect 仍不跟随。
   - 处理状态：接受为产品兼容需求；需在 PR 说明中明确这是管理员可信配置例外，而非通用 SSRF 放宽。

3. 维护风险：`internal/providerquota/script_worker_limit_darwin.go` 曾是未跟踪源码文件。
   - 证据：`git ls-files --others --exclude-standard internal/providerquota/script_worker_limit_darwin.go` 返回该文件。当前本地 `GOOS=darwin GOARCH=amd64 go build ./cmd/server` 通过依赖它。
   - 影响：若创建 PR 或提交时漏掉该文件，Darwin 目标会缺少 `applyScriptWorkerHardMemoryLimit` 实现并编译失败，且 macOS fail-closed 策略不会进入代码库。
   - 处理状态：已修复。该文件已标记为新增文件并出现在 `git status --short` / diff 中；提交时需要包含它。

### Verification（验证）

- `go test ./internal/providerquota -run 'TestSystemPromptContainsContract|TestAuditScript|TestScriptGenerator' -count=1`：通过，36 tests。
- `npm --prefix internal/frontend test`：通过，227 tests。
- `npm --prefix internal/frontend run build`：通过。
- `go test ./internal/admin -count=1`：通过，175 tests。
- `go test -race ./internal/providerquota ./internal/admin -count=1`：通过，481 tests。
- `go vet ./...`：通过。
- `git diff --check`：通过。
- `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go test -c ./internal/providerquota`、`CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c ./internal/providerquota`、`CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build ./cmd/server`：编译通过。

### Final Review Conclusion（最终审查结论）

本次复审没有发现新的可直接利用的安全绕过；worker 隔离、秘密边界、默认 LLM SSRF 防护和 metadata 拒绝仍成立。后续发现的 OOM 映射测试不稳定和 Darwin fail-closed 文件未跟踪问题均已处理；仍需在最终提交时确认新增 Darwin 文件和 review notes 一并提交。

## Second Follow-up Review: Post-fix Branch State（二次后续审查：修复后的分支状态）

日期：2026-07-29
审查者：Codex

### Scope（范围）

复审 endpoint 兼容、生成脚本审计、worker 内存测试、Darwin fail-closed、前端错误展示等修复后的当前分支状态。重点检查 worker 隔离、worker framing、LLM endpoint 策略、秘密处理和生成脚本校验中的功能回归与安全问题。

### Findings（发现）

1. 中危安全缺陷：configured LLM endpoint 策略声称 metadata 目标仍会被阻断，但 allow-configured 路径只拒绝 link-local、link-local multicast 和 unspecified 地址。
   - 证据：`NewConfiguredLLMClient` 使用 `newLLMTransport(..., true)`，而 `isBlockedLLMIP(ip, true)` 只检查 `IsLinkLocalUnicast`、`IsLinkLocalMulticast`、`IsUnspecified`。当前 configured-client 回归测试只覆盖 `169.254.169.254`。
   - 影响：不属于 link-local 的云 metadata endpoint 可以通过 configured-client guard。阿里云文档说明 ECS metadata 位于 `http://100.100.100.200/latest/...`，当前 predicate 不会覆盖该地址。该问题和本功能文档中的“允许 loopback/private LLM proxy 但 metadata 仍阻断”声明冲突。
   - 状态：已修复。已新增 preflight 与 dial-time 共用的 cloud metadata IP denylist，覆盖 `169.254.169.254` 和 `100.100.100.200`。已补默认/configured client，以及 configured client DNS rebinding 到阿里云 metadata 地址的回归测试。

### Residual Notes（残余说明）

- macOS 自定义 quota 脚本在实现经验证的硬内存边界前有意 fail closed。
- 管理员配置的 loopback/private LLM endpoint 仍是受信管理员兼容例外；metadata IP、link-local、unspecified 地址、redirect，以及默认 LLM client 的 SSRF 防护仍保持阻断。
- Linux 硬内存限制仍依赖内核对 `RLIMIT_DATA` 约束 `mmap` 的支持；如果要覆盖更老的 unsupported kernel，需要单独做部署决策。
- 最终 PR/commit 需要包含新增 Darwin 源文件、前端 dist 更新和 review notes。

### Verification（验证）

- `go test ./internal/providerquota ./internal/admin -count=1`：通过，494 tests。
- `go test ./internal/providerquota -run 'TestConfiguredLLMClientRejectsMetadataEndpoint|TestLLMClientRejectsMetadata|TestConfiguredLLMClientRejectsMetadataDNSRebinding|TestConfiguredLLMClientAllowsLoopbackEndpoint|TestLLMClientRejectsDNSRebinding' -count=1 -v`：通过，9 tests。
- `go test -race ./internal/providerquota ./internal/admin -count=1`：通过，486 tests。
- `go test -p 1 ./... -count=1`：通过，1858 tests。
- `npm --prefix internal/frontend test`：通过，227 tests。
- `npm --prefix internal/frontend run build`：通过。
- `go vet ./...`：通过。
- `git diff --check`：初次失败仅因为既有 review-note 日期行使用了 Markdown 行尾空格；本记录已移除这些空格。

### Conclusion（结论）

本轮审查通过，保留以上平台和部署说明。

## Third Follow-up Review: Post-metadata-fix Branch State（三次后续审查：metadata 修复后的分支状态）

日期：2026-07-29
审查者：Codex

### Scope（范围）

复审 configured-client metadata denylist 修复后的分支状态。覆盖 LLM endpoint 策略、默认/configured LLM client 行为、DNS rebinding 检查、worker framing 与进程限制、AI 生成脚本校验 context、前端生成脚本错误展示，以及当前工作区 diff。

### Findings（发现）

本轮未发现新的功能逻辑缺陷或可直接利用的安全缺陷。

### Verification Notes（验证说明）

- configured LLM client 仍允许已认证管理员配置的 loopback/private LLM proxy；cloud metadata IP（`169.254.169.254`、`100.100.100.200`）、link-local、unspecified 地址、redirect，以及默认 client 的 internal endpoint 仍保持阻断。
- worker 执行仍通过子进程协议，framed input、stdout/stderr 都有边界。Darwin 在存在经验证的硬内存边界前仍 fail closed。
- 首次完整 `go test -race ./internal/providerquota ./internal/admin -count=1` 曾在无关的 `TestSchedulerAppliesJitter` 时间窗口断言处失败；本分支没有修改 `manager.go` / `manager_test.go`。随后 `go test -race ./internal/providerquota -run TestSchedulerAppliesJitter -count=5 -v` 连续 5 次通过，完整 race 重跑也通过。

### Verification（验证）

- `go test ./internal/providerquota -run 'TestConfiguredLLMClientRejectsMetadataEndpoint|TestLLMClientRejectsMetadata|TestConfiguredLLMClientRejectsMetadataDNSRebinding|TestConfiguredLLMClientAllowsLoopbackEndpoint|TestLLMClientRejectsDNSRebinding|TestScriptWorker|TestProcessScriptWorker' -count=1 -v`：通过，40 tests。
- `go test ./internal/providerquota ./internal/admin -count=1`：通过，494 tests。
- `go test -race ./internal/providerquota -run TestSchedulerAppliesJitter -count=5 -v`：通过，5 tests。
- `go test -race ./internal/providerquota ./internal/admin -count=1`：重跑通过，486 tests。
- `go test -p 1 ./... -count=1`：通过，1858 tests。
- `npm --prefix internal/frontend test`：通过，227 tests。
- `npm --prefix internal/frontend run build`：通过。
- `go vet ./...`：通过。
- `git diff --check`：通过。

### Conclusion（结论）

本轮审查通过。剩余内容是明确的平台/部署约束，不是阻断合并的功能或安全 finding。

## Fourth Follow-up Review: Frame Header Encoding And Input Limit（四次后续审查：帧 header 编码与输入上限）

日期：2026-07-29
审查者：Codex

### Scope（范围）

复审 k3 针对 framed worker protocol 边界问题的后续修复：JSON header 中 `<`、`>`、`&` 的 HTML 转义膨胀，以及 `maxScriptWorkerInputSize` 未计入 1 字节帧分隔符。

### Findings（发现）

本轮未发现新的功能逻辑缺陷或可直接利用的安全缺陷。

### Verification Notes（验证说明）

- `encodeScriptWorkerRequest` 现在对受信任的父子进程 JSON header 使用 `json.Encoder.SetEscapeHTML(false)`，并在追加显式帧分隔符前移除 encoder 自带的尾部换行。这保持了 decoder 端 `bytes.Cut(..., '\n')` framing，同时避免 HTML-heavy 合法脚本被 `\uXXXX` 膨胀误拒。
- `maxScriptWorkerInputSize` 现在等于 `maxScriptWorkerHeaderSize + 1 + maxResponseBodySize`，与实际 `header + '\n' + body` 帧结构一致。
- decoder 仍保留总输入上限、header 上限、`DisallowUnknownFields`、JSON EOF、非负/最大 response body size、以及 `response_body_size == len(body)` 的精确校验。

### Verification（验证）

- `go test ./internal/providerquota -run 'TestEncodeScriptWorkerRequestKeepsHTMLEscapeHeavyHeaderSmall|TestEncodeScriptWorkerRequestMaxBodyFitsInputLimit|TestScriptWorkerRejectsInvalidProtocol|TestScriptWorkerRejectsOversizedInput|TestProcessScriptWorkerHandlesMaxResponseBody|TestProcessScriptWorkerHandlesEscapedMaxResponseBody' -count=1 -v`：通过，9 tests。
- `go test ./internal/providerquota ./internal/admin -count=1`：通过，496 tests。
- `go test -p 1 ./... -count=1`：通过，1860 tests。
- `go test -race ./internal/providerquota ./internal/admin -count=1`：通过，488 tests。
- `go vet ./...`：通过。
- `git diff --check`：通过。

### Conclusion（结论）

本轮审查通过。该修复消除了两个低危 framing 兼容性问题，同时没有削弱 worker 协议或脚本隔离边界。

## Fifth Follow-up Review: Proxy Hardcoded Compatibility Endpoints（五次后续审查：代理硬编码兼容端点）

日期：2026-07-29
审查者：Codex

### Scope（范围）

复审分支顶部新增的两个 proxy 提交：

- `fix(proxy): handle /api/hello connectivity probe`
- `fix(proxy): handle /v1/environment_providers list endpoint`

覆盖精确 hardcoded endpoint 匹配、方法处理、本地响应格式、与 proxy fail-closed 转发守卫的交互，以及当前 worker-isolation 分支回归。

### Findings（发现）

本轮未发现新的功能逻辑缺陷或可直接利用的安全缺陷。

### Verification Notes（验证说明）

- `/api/hello` 是精确 hardcoded endpoint。`HEAD` 返回 `200` 且无 body；`GET` 返回 `200` 与 `{}`；其他方法返回 `405` 与 `Allow: HEAD, GET`。
- `/v1/environment_providers` 是精确 hardcoded endpoint。`GET` 返回 `{"environments":[]}`；其他方法返回 `405` 与 `Allow: GET`。
- `/v1/environment_providers/cloud/create` 不会被新的精确 endpoint 匹配，仍会落到本地 fail-closed 非模型端点守卫，而不是转发给 provider。
- 新 handler 只返回本地空兼容数据，不暴露 secret，不读取配置，也不扩大模型请求转发表面。

### Verification（验证）

- `go test ./internal/proxy -run 'TestIsHardcodedEndpoint|TestHandleHello|TestHandleEnvironmentProviders|TestBlockedEndpoint|TestEndpointPolicy' -count=1 -v`：通过，90 tests。
- `go test ./internal/proxy -count=1`：通过，539 tests。
- `go test -race ./internal/proxy -count=1`：通过，539 tests。
- `go test ./internal/providerquota ./internal/admin -count=1`：通过，496 tests。
- `go test -p 1 ./... -count=1`：通过，1870 tests。
- `npm --prefix internal/frontend test`：通过，227 tests。
- `npm --prefix internal/frontend run build`：通过。
- `go vet ./...`：通过。
- `git diff --check main...HEAD`：通过。

### Conclusion（结论）

本轮审查通过。新增 proxy 兼容端点是窄范围、精确匹配、本地响应，不削弱该分支的 worker 隔离、秘密边界或 proxy fail-closed 转发策略。

## 部署说明：宿主机内存上限依赖

日期：2026-07-29

作为帧编码复审的后续记录。以下为部署约束，并非代码缺陷，已统一写入 `spec_ZH.md` -> 部署约束。这里单独记录是因为它无法从 worker 日志观测：worker 的 stdout 是协议通道，worker 的 stderr 内容被父进程丢弃（仅查看其溢出标志），因此除非改协议，否则无法从 worker 内部回传生效上限。

- worker 的硬内存上限会在 `applyScriptWorkerHardMemoryLimit` 中静默 clamp 到宿主机硬上限（`current.Max`）。若容器的 `ulimit -d`（或 Windows 嵌套 Job 的 commit 上限）低于生产值 ~512 MiB，可能静默退化到无法满足 2 MiB 响应兼容目标——与初次 review 中 Finding #2 在 128 MiB 下观察到的失败同源。运维须确保宿主机硬上限不低于生产值。
- Linux `< 4.7`（`RLIMIT_DATA` 不覆盖 `mmap`）与 macOS fail-closed 行为，与此前遗留说明一致。
