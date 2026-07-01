# SSE 标记的 HTTP 错误响应处理规格

本地页面：无
代理入口：`POST /v1/messages`、`POST /anthropic/v1/messages`
参考来源：Docker 运行日志、`data/proxy.db`、`internal/proxy/handler.go`、`internal/proxy/heartbeat.go`
技术栈：Go 1.26 标准库（`net/http`、`io`、`log`）+ SQLite 用量记录器
最后更新：2026-06-30
进度：已验证，2 / 2 已完成

## 整体分析（源站分析）

### 当前项目状态

代理当前根据响应 `Content-Type` 分派上游响应。只要响应包含 `text/event-stream`，即使 HTTP 状态是错误，也会进入 SSE 心跳路径。SSE 路径会观察流式用量，但不会采集错误响应体、设置 HTTP 错误元数据，也不会输出详细的 `[Proxy] Error` 诊断日志。

现有非 SSE 路径已经具备所需的错误处理能力：

1. 将完整上游响应体转发给客户端。
2. 通过 `responseObserver` 采集有大小上限的副本。
3. 记录 `error_type=http_error` 和清洗后的 `error_message`。
4. 记录 `usage_parse_status=skipped_non_2xx`。
5. 记录兼容性请求头、请求参数摘要和清洗后的上游响应。

反应式 400 修复器在响应分派之前运行。它可能使用重试响应替换原始响应，也可能在没有适用清理规则时恢复原始错误体。因此，分派必须依据修复器处理后的最终响应。

### 运行证据

Docker 实例在 2026-06-30 本地时间 21:50 和 21:58 产生了两次代表性失败：

```text
>>> POST ... stream=false ...
<<< 400 ...
[Stream] SSE stream detected ..., enabling heartbeat injection
```

两次响应都向客户端转发了 213 字节，但对应 SQLite 记录的 `error_type` 和 `error_message` 为空，`usage_parse_status=missing`。更早的同样 213 字节 400 响应经过普通路径后，正确记录为 `http_error` 和 `skipped_non_2xx`。

这证明上游错误体实际存在。未记录错误体和请求摘要，是因为带 SSE 媒体类型的 HTTP 错误被送入了面向成功流式响应的分支。

### 根因

`isSSEStream` 只检查响应 `Content-Type` 是否包含 `text/event-stream`。`Handler.ServeHTTP` 随后让这一媒体类型判断优先于 `resp.StatusCode`。该分派错误地把 SSE 当成完整响应结果，而不是传输表示形式。

HTTP 错误状态必须优先于媒体类型。心跳注入和 SSE 用量解析只适用于成功的非错误响应。最终状态为 `>= 400` 的响应必须进入错误观察路径，不受 `Content-Type` 或请求 `stream` 值影响。

### 目标响应矩阵

| 最终响应状态 | 响应媒体类型 | 必须使用的路径 | 心跳 | 错误持久化与详细日志 |
| --- | --- | --- | --- | --- |
| `< 400` | `text/event-stream` | SSE 流式处理 | 启用 | 否 |
| `< 400` | 其他 | 非流式响应处理 | 禁用 | 否 |
| `>= 400` | `text/event-stream` | HTTP 错误观察 | 禁用 | 是 |
| `>= 400` | 其他 | HTTP 错误观察 | 禁用 | 是 |

### 设计决策

在 `Handler.ServeHTTP` 中采用状态优先的分派方式：只有最终响应状态低于 400 且 `isSSEStream(resp)` 为真时，才进入 SSE 分支。所有最终 4xx 和 5xx 响应复用现有非 SSE 观察器和错误记录路径。

该方案优于在 SSE 分支内复制一套错误采集逻辑，因为它保留了唯一、统一的 HTTP 错误路径。本缺陷不需要更大范围的响应管线重构。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成 | 让 HTTP 错误状态优先于 SSE 媒体类型 | `internal/proxy/handler.go` | 回归测试证明错误体转发、诊断日志和用量持久化正确 |
| 2 | 已完成 | 验证流式处理和修复器无回归 | `internal/proxy/server_test.go` 与现有代理测试 | 代理定向测试和完整 Go 测试通过 |

## 需求

### 交付物

1. 只有当 `resp.StatusCode < 400` 且 `isSSEStream(resp)` 为真时，最终上游响应才进入 SSE 心跳路径。
2. 对启用用量记录的 Messages 路径，所有最终 4xx 或 5xx 响应都必须使用 `responseObserver`，即使上游声明 `Content-Type: text/event-stream`。
3. 代理必须保留上游状态、允许转发的响应头，以及交付给客户端的完整响应体。
4. 最终 HTTP 错误的用量持久化必须记录：
   - `status_code` 等于最终上游状态；
   - `error_type` 等于 `http_error`；
   - `error_message` 等于经过清洗且有大小上限的错误体副本；
   - `response_bytes` 等于完整转发响应体的大小；
   - token `usage_source` 等于 `none`；
   - token `usage_parse_status` 等于 `skipped_non_2xx`。
5. 代理必须为最终 HTTP 错误输出既有详细日志格式：

   ```text
   [Proxy] Error <status> <upstream> | headers: <summary> | params: <summary> | resp: <sanitized error>
   ```

6. 最终错误响应不得启动 SSE 心跳 goroutine，也不得输出 `[Stream] SSE stream detected`。
7. 成功的 SSE 响应必须继续按现有方式流式转发、刷新、注入空闲心跳并记录 SSE 用量。
8. 反应式 400 修复行为保持不变：
   - 重试成功时，根据重试响应选择相应处理路径；
   - 修复失败或不适用时，将恢复后的最终错误转发、记录一次。

### 范围内文件

```text
internal/proxy/
  handler.go          （修改：按状态分派响应）
  server_test.go      （修改：SSE 标记错误的回归覆盖）
```

`heartbeat.go` 继续负责媒体类型检测和心跳实现。由于 handler 只会对非错误响应调用它，因此无需修改其行为。

### 约束

1. 不得修改上游请求转换、模型映射、供应商选择、速率限制或 429 重试行为。
2. 不得修改交付给客户端的响应体，包括使用 SSE 媒体类型标记的 JSON 错误体。
3. 不得把 HTTP 错误体解析为 token 用量。
4. 保持现有错误清洗和大小限制：客户端接收完整响应体，持久化和日志中的错误文本保持有界并清洗敏感信息。
5. 保持现有兼容性请求头和请求参数摘要方式；不得向日志新增原始消息内容、工具 schema、凭据或授权值。
6. 不增加配置开关。正确分派属于协议行为，不是用户偏好。
7. 修改保持局部化，不重构无关的流式处理或用量记录代码。

### 边界情况

1. 请求为 `stream=false`，但 400 响应声明 `text/event-stream`。
2. 请求为 `stream=true`，但 400、429 或 5xx 响应声明 `text/event-stream`。
3. 响应体是有效 JSON，但媒体类型是 SSE。
4. 响应体是 SSE 格式的错误数据。由于状态码具有权威性，仍应原样转发并作为 HTTP 错误处理。
5. 修复器读取 400 响应体前缀后，因没有匹配的规则而恢复响应体。观察器仍必须看到并转发完整恢复后的响应体。
6. 修复器重试 400 后收到成功 SSE 响应。重试响应仍必须使用正常 SSE 处理。
7. 错误响应体超过观察器采集上限。客户端接收完整响应体，只有持久化和日志中的诊断副本受限。
8. 复制错误响应体时客户端断开。保持现有复制错误行为，不启动心跳。

### 非目标

1. 识别导致供应商错误码 1210 的具体上游请求字段。
2. 修改供应商专用请求清理逻辑或增加新的修复器匹配规则。
3. 修改上游响应头或纠正上游声明的媒体类型。
4. 在现有有界诊断信息之外持久化完整请求或响应负载。
5. 重新设计用量数据库 schema 或管理端用量界面。
6. 修改 HTTP 状态低于 400、但 SSE 事件负载表达应用级错误时的行为。

## 任务详情

### 内嵌实现计划

> **供代理执行者使用：** 必须使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans`，按任务逐项执行本计划。通过更新本规格中的复选框跟踪进度。

**Goal（目标）：** 即使上游错误地把响应标记为 SSE，也要保留并诊断每个最终上游 HTTP 错误。

**Architecture（架构）：** 保持现有响应管线，只让分支条件感知状态码。最终状态低于 400 时可以使用 SSE 心跳处理；所有最终 4xx 或 5xx 响应使用现有非流式观察器，该观察器已经负责错误日志和用量持久化。

**Tech Stack（技术栈）：** Go 1.26、`net/http`、`httptest`、标准 `log`、现有 `fakeUsageRecorder` 测试辅助器。

### 任务 1：将最终 HTTP 错误送入错误观察器

#### 需求

**Objective（目标）** — 即使媒体类型为 `text/event-stream`，也要观察、记录并持久化最终上游 HTTP 错误。

**Outcomes（成果）** — `Handler.ServeHTTP` 让 `>= 400` 状态优先于 SSE 媒体类型；现有非 SSE 错误路径转发响应并记录完整错误元数据。

**Evidence（证据）** — 测试后端返回状态 400、`Content-Type: text/event-stream` 和已知错误体。客户端收到完全一致的响应体，伪用量记录器包含 `http_error` 和 `skipped_non_2xx`，捕获的日志包含请求参数摘要和清洗后的响应。

**Constraints（约束）** — 使用现有观察器、清洗器、日志格式化和记录器。不得在 SSE 分支内复制 HTTP 错误处理逻辑。

**Edge Cases（边界）** — 请求 stream 设置与响应媒体类型不一致；修复器恢复或替换响应；错误响应体大于诊断采集上限。

**Verification（验证）** — 定向 handler 测试证明响应保真、不进入 SSE 心跳、输出详细日志并持久化错误元数据。

#### 计划

**文件：**

- 修改：`internal/proxy/handler.go:262`
- 测试：`internal/proxy/server_test.go:492`

- [x] **步骤 1：增加失败回归测试**

  在 `internal/proxy/server_test.go` 的 `TestProxyRecordsHTTPErrorAndForwardsFullBody` 附近增加以下测试：

  ```go
  func TestProxyRecordsSSELabeledHTTPError(t *testing.T) {
      var logBuf bytes.Buffer
      oldOutput := log.Writer()
      oldFlags := log.Flags()
      oldPrefix := log.Prefix()
      log.SetOutput(&logBuf)
      log.SetFlags(0)
      log.SetPrefix("")
      t.Cleanup(func() {
          log.SetOutput(oldOutput)
          log.SetFlags(oldFlags)
          log.SetPrefix(oldPrefix)
      })

      recorder := &fakeUsageRecorder{}
      errorBody := `{"type":"error","error":{"type":"provider_error","message":"request rejected"}}`
      backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          w.Header().Set("Content-Type", "text/event-stream")
          w.WriteHeader(http.StatusBadRequest)
          _, _ = w.Write([]byte(errorBody))
      }))
      defer backend.Close()

      handler := NewHandler(
          config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))),
          http.DefaultTransport.(*http.Transport),
          recorder,
      )
      req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
          "model":"claude-sonnet",
          "stream":false,
          "max_tokens":64,
          "messages":[{"role":"user","content":"hello"}]
      }`))
      req.Header.Set("Content-Type", "application/json")
      rec := httptest.NewRecorder()

      handler.ServeHTTP(rec, req)

      if rec.Code != http.StatusBadRequest {
          t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
      }
      if rec.Body.String() != errorBody {
          t.Fatalf("body = %q, want %q", rec.Body.String(), errorBody)
      }

      record := recorder.onlyRecord(t)
      if record.req.StatusCode == nil || *record.req.StatusCode != http.StatusBadRequest {
          t.Fatalf("StatusCode = %v", record.req.StatusCode)
      }
      if record.req.ErrorType != usage.ErrorHTTP {
          t.Fatalf("ErrorType = %q", record.req.ErrorType)
      }
      if record.req.ErrorMessage != errorBody {
          t.Fatalf("ErrorMessage = %q, want %q", record.req.ErrorMessage, errorBody)
      }
      if record.req.ResponseBytes != int64(len(errorBody)) {
          t.Fatalf("ResponseBytes = %d, want %d", record.req.ResponseBytes, len(errorBody))
      }
      if record.tok.UsageSource != usage.UsageSourceNone {
          t.Fatalf("UsageSource = %q", record.tok.UsageSource)
      }
      if record.tok.UsageParseStatus != usage.ParseStatusSkippedNon2xx {
          t.Fatalf("UsageParseStatus = %q", record.tok.UsageParseStatus)
      }

      logs := logBuf.String()
      if !strings.Contains(logs, "[Proxy] Error 400") ||
          !strings.Contains(logs, `"max_tokens":64`) ||
          !strings.Contains(logs, "resp: "+errorBody) {
          t.Fatalf("missing detailed HTTP error log:\n%s", logs)
      }
      if strings.Contains(logs, "[Stream] SSE stream detected") {
          t.Fatalf("HTTP error incorrectly entered SSE path:\n%s", logs)
      }
  }
  ```

- [x] **步骤 2：运行回归测试并确认当前失败**

  执行：

  ```bash
  go test ./internal/proxy -run '^TestProxyRecordsSSELabeledHTTPError$' -count=1
  ```

  修复前预期：`FAIL`；记录的 `ErrorType` 为空，`UsageParseStatus` 为 `missing`，日志包含 `[Stream] SSE stream detected`，而不是详细 HTTP 错误行。

- [x] **步骤 3：实现最小的状态优先分派修改**

  在 `internal/proxy/handler.go` 中将 SSE 分支条件替换为：

  ```go
  if resp.StatusCode < 400 && isSSEStream(resp) {
  ```

  不移动或复制现有响应观察器、错误字段赋值或 `[Proxy] Error` 日志块。

- [x] **步骤 4：格式化涉及的 Go 文件**

  执行：

  ```bash
  gofmt -w internal/proxy/handler.go internal/proxy/server_test.go
  ```

  预期：两个文件均完成格式化，没有无关内容变化。

- [x] **步骤 5：运行回归测试并确认通过**

  执行：

  ```bash
  go test ./internal/proxy -run '^TestProxyRecordsSSELabeledHTTPError$' -count=1
  ```

  修复后预期：`ok magic-claude-code/internal/proxy`；精确转发错误体，日志和用量断言全部通过。

- [x] **步骤 6：执行 SSE 和修复器定向回归**

  执行：

  ```bash
  go test ./internal/proxy -run 'TestProxyRecordsStreamingUsage|TestProxyRecordsStreamingUsageWhenUpstreamDoesNotCloseAfterMessageStop|TestProxyRetriesKimiTool400WithCleanedRequestBody|TestProxyForwardsLargeNonRecoverable400Body' -count=1
  ```

  预期：`ok magic-claude-code/internal/proxy`；成功 SSE 用量、终止事件处理、400 修复和大响应体完整转发保持不变。

- [x] **步骤 7：提交实现和回归测试**

  ```bash
  git add internal/proxy/handler.go internal/proxy/server_test.go
  git commit -m "fix(proxy): record SSE-labeled HTTP errors"
  ```

#### 验证

- [x] `Content-Type: text/event-stream` 的 400 响应逐字节转发。
- [x] 该响应不产出 SSE 检测日志，也不启用心跳行为。
- [x] 详细错误日志包含 `headers`、`params` 和清洗后的 `resp` 字段。
- [x] 用量请求记录 `http_error`、清洗后的错误消息、最终状态和完整响应字节数。
- [x] 用量 token 记录使用 `none` 和 `skipped_non_2xx`。

### 任务 2：增加回归覆盖并执行验证

#### 需求

**Objective（目标）** — 防止后续响应分派变更再次造成错误诊断缺失，或破坏成功 SSE 流式处理。

**Outcomes（成果）** — 自动化测试覆盖本次报告的失败，并保留正常 SSE 响应和反应式 400 处理覆盖。

**Evidence（证据）** — 从干净工作树执行代理定向测试和 `go test ./...` 均通过；`git diff --check` 不报告空白错误。

**Constraints（约束）** — 测试使用 `httptest.Server`、现有伪用量记录器和有界日志捕获，不需要真实供应商凭据或网络调用。

**Edge Cases（边界）** — 必须覆盖使用 SSE 媒体类型的 HTTP 400。若不模糊原始失败，可增加 429 和 5xx 表格测试。

**Verification（验证）** — 执行定向和完整 Go 测试命令，并在实现后记录实际结果。

#### 计划

**文件：**

- 验证：`internal/proxy/handler.go`
- 验证：`internal/proxy/server_test.go`
- 验证成功后更新：`sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec.md`
- 验证成功后更新：`sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec_ZH.md`

- [x] **步骤 1：禁用缓存执行完整代理包测试**

  执行：

  ```bash
  go test ./internal/proxy -count=1
  ```

  预期：`ok magic-claude-code/internal/proxy`。

- [x] **步骤 2：执行完整 Go 测试套件**

  执行：

  ```bash
  go test ./...
  ```

  预期：所有 Go 包通过，无失败。

- [x] **步骤 3：检查空白错误**

  执行：

  ```bash
  git diff --check
  ```

  预期：`git diff --check` 无输出。

- [x] **步骤 4：检查范围内代码差异**

  执行：

  ```bash
  git show --format= HEAD -- internal/proxy/handler.go internal/proxy/server_test.go
  ```

  预期：实现提交只包含一个 handler 条件修改和一个聚焦回归测试，没有无关修改。

- [x] **步骤 5：在本单文件规格对中记录验证结果**

  所有命令通过后，在同一次提交中更新两份规格：

  - 将进度改为 `validated, 2 / 2 complete` / `已验证，2 / 2 已完成`；
  - 将开发检查清单两行改为 `Completed` / `已完成`；
  - 勾选已完成的计划与验证复选框；
  - 添加实际命令结果和实现提交哈希，不创建独立 validation 或 plan 文件。

- [x] **步骤 6：提交验证记录**

  ```bash
  git add \
    sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec.md \
    sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec_ZH.md
  git commit -m "docs: record SSE error handling verification"
  ```

#### 验证

- [x] `go test ./internal/proxy -run 'Test.*SSE.*Error|TestProxyRecordsStreamingUsage|TestProxyRetries.*400' -count=1`
- [x] `go test ./...`
- [x] `git diff --check`

### 实际验证证据

日期：2026-06-30
实现提交：`43dd1f0`（`fix(proxy): record SSE-labeled HTTP errors`）

- RED：实现前执行 `go test ./internal/proxy -run '^TestProxyRecordsSSELabeledHTTPError$' -count=1`，测试以 `ErrorType = ""` 失败，证明回归测试命中了缺失的 HTTP 错误路径。
- GREEN：修改为状态优先分支条件后，同一定向回归命令通过。
- SSE 与修复器定向回归通过。
- `go test ./internal/proxy -count=1` 通过，耗时 4.515 秒。
- `go test ./...` 中所有 Go 包通过；无测试的包报告 `[no test files]`。
- `git diff --check` 无输出；检查 `43dd1f0` 确认只包含一个 handler 条件修改和一个聚焦回归测试。
