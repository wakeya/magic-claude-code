# SSE 标记的 HTTP 错误响应处理规格

本地页面：无
代理入口：`POST /v1/messages`、`POST /anthropic/v1/messages`
参考来源：Docker 运行日志、`data/proxy.db`、`internal/proxy/handler.go`、`internal/proxy/heartbeat.go`
技术栈：Go 1.26 标准库（`net/http`、`io`、`log`）+ SQLite 用量记录器
最后更新：2026-07-01
进度：已验证，4 / 4 已完成

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

安全请求字段白名单部署后，2026-07-01 03:40:57 又出现了一次智谱 1210 失败，暴露出剩余的可运维性缺口：

```text
params: {"max_tokens":64000,"messages":"[82 items]","model":"glm-5.2","tools":"[11 items]"}
resp: {"type":"error","error":{"type":"invalid_request_error","code":"1210","message":"[1210][API 调用参数有误，请检查文档。][...]"}}
```

该摘要只能证明集合数量，无法区分 `stream` 是缺失还是 false、当前有哪些工具，也无法判断工具 schema 是否包含影响兼容性的 JSON Schema 关键字。后续诊断确认：失败发生在 `ToolSearch` 加载 `WebFetch`/`WebSearch` 之后，Claude Code 在 JSON 回退时移除了 14 字节的 `"stream":true,` 成员。定位问题不需要完整 prompt 或消息正文，需要的是协议结构。

同一份运行证据排除了 cc-switch #3090 所述“缺少 `beta=true`”是本次 MCC 请求失败原因。03:40:55 的流式 URL 与 03:40:57 的最终 400 URL 都包含 `?beta=true`。普通 `>>>`/`<<<` 行之所以不显示，是因为 `providerLogFields` 按设计调用 `config.RedactURL` 删除所有 query 后再展示。详细错误行当前直接打印原始 `backendURL`，虽然能看到 beta，也可能泄露其他 query secret。因此任务 4 将增加只允许 beta 状态/数量的摘要，同时让所有 URL 本身都保持 query 脱敏。

Claude Code 会话 `202606302035` 提供了会话级关联证据，其最后三次失败轮次为：

```text
04:50:02 ToolSearch -> matches [WebSearch, WebFetch]
04:50:04 API 400 / 1210
04:58:13 API 400 / 1210
08:50:58 API 400 / 1210
```

最后一次失败在 SQLite 中形成一对请求：`stream=true`、请求 859619 字节、HTTP 200、响应 589 字节、`client_aborted`；随后 stream 缺失、请求 859605 字节、HTTP 400、响应 213 字节。14 字节请求差与移除 `"stream":true,` 一致。这确认了失败与动态加载 Web 工具、stream 到非 stream 重试之间存在持续强关联；但尚未证明供应商拒绝哪个工具 schema 字段，也未确认前置 589 字节 HTTP-200 SSE 响应中的哪个结构事件导致 Claude Code 中断并重试。

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

对于错误日志的请求诊断，继续保持默认安全的内容脱敏，但把仅有集合数量的摘要升级为有界协议结构摘要。记录字段存在性、安全标量控制项、集合数量、消息 role/content-block 直方图、单向工具名称集合指纹、对 `ToolSearch`/`WebFetch`/`WebSearch` 的明确识别，以及聚合 schema 关键字计数。不得记录 prompt、消息正文、metadata 值或键、任意工具名、工具描述、schema 属性名/描述、凭据或未知扩展值。该指纹仅用于相等性比较，不视为加密或保密边界。

本任务保持现有兼容性请求头白名单不变：保留 Anthropic version/beta 与 content type，凭据继续遮蔽。URL query 诊断只报告 `beta` 是缺失、true、false 或其他值，以及其他参数数量；不记录任何其他 query 名称或值。未来带鉴权、TTL 的全量捕获能力属于独立功能，不能替代安全控制台诊断。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成 | 让 HTTP 错误状态优先于 SSE 媒体类型 | `internal/proxy/handler.go` | 回归测试证明错误体转发、诊断日志和用量持久化正确 |
| 2 | 已完成 | 验证流式处理和修复器无回归 | `internal/proxy/server_test.go` 与现有代理测试 | 代理定向测试和完整 Go 测试通过 |
| 3 | 已完成 | 将请求摘要限制为安全诊断字段 | `internal/proxy/handler.go`、`internal/proxy/server_test.go` | 修复前安全复现失败；修复后白名单、防泄漏断言、状态矩阵和 race 测试通过 |
| 4 | 已完成 | 增加有界协议结构诊断 | `internal/proxy/request_diagnostics.go`、定向测试、Handler 日志断言 | RED 证明现有摘要缺少 stream 存在性/工具/schema 结构；GREEN 保持零敏感标记和有界输出 |

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
9. 详细错误日志必须包含有界、经过类型检查的协议结构摘要：
   - `model`、数值生成控制项、请求字节数，以及明确的 `stream.present`；只有值类型正确时才记录布尔值；
   - `messages` 数量，以及 role/content-block 白名单直方图；
   - 工具数量、排序后工具名称集合的稳定 SHA-256 摘要、仅对 `ToolSearch`/`WebFetch`/`WebSearch` 记录精确名称，以及兼容性相关 schema 关键字的聚合计数；
   - `system`、`metadata`、`thinking` 和 `input` 的类型/大小形态，不记录值或对象键；
   - 未识别顶层字段数量，不记录名称或值。
10. 请求摘要不得包含 prompt/message/input 正文、metadata 键或值、任意工具名、工具描述、schema 属性名/描述、凭据、Authorization 值，或未知扩展名称/值。
11. 无论 message/tool 集合多大，请求诊断输出都必须有界；聚合 map 使用固定白名单，已知 Web 工具名必须去重。
12. 所有日志中的上游 URL 必须保持 query 脱敏。独立 query 诊断只能报告标准化 beta 状态（`absent`、`true`、`false`、`other`）和非 beta 参数数量。

### 范围内文件

```text
internal/proxy/
  handler.go          （修改：按状态分派响应）
  request_diagnostics.go （任务 4 新建：有界安全协议摘要）
  request_diagnostics_test.go （任务 4 新建：定向脱敏与结构测试）
  server_test.go      （修改：SSE 标记错误的回归覆盖）
```

`heartbeat.go` 继续负责媒体类型检测和心跳实现。由于 handler 只会对非错误响应调用它，因此无需修改其行为。

### 约束

1. 不得修改上游请求转换、模型映射、供应商选择、速率限制或 429 重试行为。
2. 不得修改交付给客户端的响应体，包括使用 SSE 媒体类型标记的 JSON 错误体。
3. 不得把 HTTP 错误体解析为 token 用量。
4. 保持现有错误清洗和大小限制：客户端接收完整响应体，持久化和日志中的错误文本保持有界并清洗敏感信息。
5. 保持现有兼容性请求头摘要方式。请求诊断只能记录上文定义的结构聚合；不得记录原始系统提示词、metadata 键/值、消息/input 内容、任意工具名、工具/schema 内容、凭据、授权值、未知扩展，或除标准化 beta 状态之外的原始 URL query 名称/值。
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
7. 增加管理后台控制的完整请求/响应捕获、持久化、导出、保留或删除功能。
8. 诊断状态低于 400 的 SSE 响应事件结构。200/589 字节 `client_aborted` 前置响应交由 `2026-07-01-zhipu-web-tools-compat` 的任务 0 处理，使本分支继续只负责最终 HTTP 错误和安全请求诊断。

## 任务详情

### 内嵌实现计划

> **供代理执行者使用：** 必须使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans`，按任务逐项执行本计划。通过更新本规格中的复选框跟踪进度。

**Goal（目标）：** 保留每个最终上游 HTTP 错误，并输出足以诊断兼容性问题的有界协议结构，同时不记录请求内容。

**Architecture（架构）：** 保持状态感知的响应管线。所有最终 4xx 或 5xx 响应继续使用现有非流式观察器；请求摘要由独立的有界结构 summarizer 生成，只聚合白名单协议事实，绝不复制携带内容的值。

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

#### 实际验证证据

日期：2026-06-30
实现提交：`43dd1f0`（`fix(proxy): record SSE-labeled HTTP errors`）

- RED：实现前执行 `go test ./internal/proxy -run '^TestProxyRecordsSSELabeledHTTPError$' -count=1`，测试以 `ErrorType = ""` 失败，证明回归测试命中了缺失的 HTTP 错误路径。
- GREEN：修改为状态优先分支条件后，同一定向回归命令通过。
- SSE 与修复器定向回归通过。
- `go test ./internal/proxy -count=1` 通过，耗时 4.515 秒。
- `go test ./...` 中所有 Go 包通过；无测试的包报告 `[no test files]`。
- `git diff --check` 无输出；检查 `43dd1f0` 确认只包含一个 handler 条件修改和一个聚焦回归测试。

### 任务 3：限制错误日志中的请求摘要

#### 需求

**Objective（目标）** — 防止详细 HTTP 错误日志复制携带提示词、身份信息、凭据或未知内容的顶层请求字段。

**Outcomes（成果）** — `summarizeRequestParams` 使用显式类型白名单；Handler 级测试证明敏感值不会进入日志，同时保留安全诊断字段。

**Evidence（证据）** — 修复前，定向 Handler 测试在进程日志中复现了 `secret-system-prompt`。修复后，同一路径在 SSE 标记的 400、429 和 500 响应下均省略所有敏感标记。

**Constraints（约束）** — 保持状态优先 SSE 条件、完整响应转发、错误持久化、修复器行为和详细响应日志不变。未知字段默认省略。

**Edge Cases（边界）** — 白名单键使用对象或字符串等错误类型；OpenAI Responses 的 `input` 数组；流式和非流式请求；4xx 和 5xx 响应。

**Verification（验证）** — 定向安全测试、完整代理包测试以及带 race detector 的 `make test` 通过。

#### 计划

**文件：**

- 修改：`internal/proxy/handler.go:742`
- 测试：`internal/proxy/server_test.go`
- 归档：`sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/review-notes.md`
- 归档：`sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/review-notes_ZH.md`

- [x] 在 Handler 测试中加入 `system`、`metadata`、凭据形字段、未知扩展和消息内容的唯一标记。
- [x] 修复前执行定向测试，确认测试因 `secret-system-prompt` 出现在 `[Proxy] Error` 日志中而失败。
- [x] 将请求摘要黑名单替换为 `model`、`stream`、数值生成参数和集合数量的类型白名单。
- [x] 使用表格用例覆盖 400/非流式、429/流式和 500/流式 Handler 路径。
- [x] 增加安全输出和错误类型省略的直接辅助函数测试。
- [x] 执行定向测试、完整代理测试和 `make test`。
- [x] 将代码提交为 `dcdc3c4`，直接辅助函数覆盖提交为 `b37030b`。

#### 验证

- [x] 安全字段和集合数量保留在诊断日志中。
- [x] 系统提示词、metadata、凭据、未知扩展、消息内容、工具内容和 input 内容不出现在日志中。
- [x] 白名单名称下类型错误的值被省略。
- [x] SSE 标记的 400、429 和 500 响应保持状态、响应体、HTTP 错误持久化和无心跳行为。
- [x] `go test ./internal/proxy -run 'TestProxyRecordsSSELabeledHTTPError|TestSummarizeRequestParamsAllowsOnlySafeDiagnostics' -count=1`
- [x] `go test ./internal/proxy -count=1`
- [x] `make test`

#### 实际验证证据

- RED 复现 CWE-532 路径，详细错误日志包含 `secret-system-prompt`。
- `dcdc3c4` 修复后 GREEN 定向测试通过；`b37030b` 中的直接白名单覆盖通过。
- `go test ./internal/proxy -count=1` 通过。
- `make test` 在启用 `-race` 和覆盖率时通过。

### 任务 4：增加有界协议结构诊断

#### 需求

**Objective（目标）** — 在不恢复原始请求日志的前提下，让默认控制台日志足以诊断兼容性错误。

**Outcomes（成果）** — `summarizeRequestParams` 能报告 `stream` 是否缺失、安全的 message/tool 结构、已知 Web 工具、schema 兼容特征，以及敏感字段的固定大小形态；URL 日志能在不暴露原始 query 的情况下显示标准化 beta 状态；任何携带内容的值都不可见。

**Evidence（证据）** — 与实际 1210 请求同形的 fixture 生成 `stream.present=false`，识别 `WebFetch`，并报告 `$schema`、`additionalProperties=false` 和嵌套 `format` 计数，同时所有敏感标记均不存在。

**Constraints（约束）** — 摘要只分析内存中已有的转换后上游 body；不持久化原始 payload、不修改 header 策略、不增加配置、不修改修复器行为。

**Edge Cases（边界）** — `stream` 缺失或类型错误；消息 content 为字符串或数组；未知 role/block type；重复已知 Web 工具；任意或携带秘密的自定义工具名；深层 schema；大型 message/tool 数组；无效 JSON；beta query 缺失、重复或非布尔值；携带 secret 的非 beta query 参数。

**Verification（验证）** — RED/GREEN 定向测试、Handler 日志测试、完整代理测试和 `make test` 均通过；大型合成请求的最终摘要小于 4096 字节。

#### 计划

**文件：**

- 新建：`internal/proxy/request_diagnostics.go`
- 新建：`internal/proxy/request_diagnostics_test.go`
- 修改：`internal/proxy/handler.go`（删除旧的仅集合计数 `summarizeRequestParams`，保留调用点）
- 修改：`internal/proxy/server_test.go`（在真实错误路径断言结构输出）
- 验证后更新：`sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec.md`
- 验证后更新：`sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec_ZH.md`

- [x] **步骤 1：编写失败的定向测试**

  新建 `internal/proxy/request_diagnostics_test.go`，请求 fixture 包含：

  ```json
  {
    "model": "glm-5.2",
    "max_tokens": 64000,
    "messages": [
      {"role":"user","content":"secret-user-text"},
      {"role":"assistant","content":[{"type":"tool_use","name":"secret-call-name","input":{"secret":"value"}}]},
      {"role":"user","content":[{"type":"tool_result","content":"secret-result"},{"type":"tool_reference","tool_name":"WebFetch"}]}
    ],
    "tools": [
      {
        "name":"WebFetch",
        "description":"secret-web-description",
        "input_schema":{
          "$schema":"https://json-schema.org/draft/2020-12/schema",
          "type":"object",
          "properties":{"url":{"type":"string","format":"uri"}},
          "required":["url"],
          "additionalProperties":false
        }
      },
      {
        "name":"secret-custom-tool",
        "description":"secret-custom-description",
        "input_schema":{"type":"object","properties":{"secret-property":{"type":"string","description":"secret-schema-description"}}}
      }
    ],
    "system":"secret-system-prompt",
    "metadata":{"secret-user-id":"secret-metadata-value"},
    "unknown_secret_extension":{"secret":"secret-extension-value"}
  }
  ```

  解析返回 JSON，并断言以下精确契约：

  - `body_bytes` 等于输入字节数；
  - 保留 `model` 和 `max_tokens`；
  - `stream` 等于 `{"present":false}`；
  - message 数量为 3，role 为 `user=2`、`assistant=1`，已知 block type 为 `tool_use=1`、`tool_result=1`、`tool_reference=1`；
  - 工具数量为 2，`known_names` 精确等于 `["WebFetch"]`，`names_sha256` 是 64 字符小写十六进制摘要；
  - schema 聚合报告一个 draft-2020-12 标记、一个 `additionalProperties=false`、一个 `format`、两个根级 property 和一个 required 条目；
  - `system` 为 `{"type":"string","chars":20}`，`metadata` 为 `{"type":"object","keys":1}`，`unknown_top_level_fields=1`；
  - 序列化摘要不包含十个 `secret-*` 标记中的任何一个。

  增加用例证明 `stream=true`、`stream=false`、错误类型 stream 分别生成 `{present:true,value:true}`、`{present:true,value:false}`、`{present:true,type:"string"}`，且不复制错误类型的原始值。

  增加稳定摘要测试：反转工具顺序后 `names_sha256` 保持一致。再增加至少 500 条 messages 和 500 个 tools 的大型合成 fixture，断言摘要短于 4096 字节且不包含生成的敏感标记。

  增加定向 query 摘要用例：无 query、`beta=true`、`beta=false`、`beta=unexpected`、重复 beta，以及 `beta=true&token=secret-query-value&signature=secret-signature`。断言输出只包含标准化 beta 状态和 `other_count=2`，绝不包含 `token`、`signature` 或两个 secret 值。

- [x] **步骤 2：运行定向测试并确认 RED**

  ```bash
  go test ./internal/proxy -run '^TestSummarizeRequestParams(ProtocolStructure|StreamPresence|StableToolDigest|BoundsLargeCollections)$' -count=1
  ```

  实现前预期：FAIL，因为现有摘要没有 `body_bytes`、明确的 stream 存在性、role/block 直方图、工具摘要、已知 Web 工具、schema 聚合或敏感字段形态。

- [x] **步骤 3：实现有界结构 summarizer**

  新建 `internal/proxy/request_diagnostics.go`，职责如下：

  ```go
  func summarizeRequestParams(body []byte) string
  func summarizeValueShape(value any) map[string]any
  func summarizeMessages(value any) map[string]any
  func summarizeTools(value any) map[string]any
  func collectSchemaDiagnostics(value any, depth int, stats map[string]int)
  func digestToolNames(names []string) string
  func summarizeUpstreamQuery(rawURL string) string
  ```

  使用固定白名单：

  ```go
  knownToolNames := {"ToolSearch", "WebFetch", "WebSearch"}
  knownRoles := {"user", "assistant", "system", "tool"}
  knownContentTypes := {
      "text", "image", "document", "tool_use", "tool_result",
      "thinking", "redacted_thinking", "server_tool_use", "tool_reference",
  }
  schemaKeywords := {"$schema", "additionalProperties", "format", "minLength", "maxLength", "oneOf", "anyOf", "allOf", "$ref"}
  ```

  实现规则：

  1. 保留当前无效 JSON 的 `<N bytes, not JSON>` 回退格式。
  2. 只保留类型正确的 `model` 和数值生成控制项。
  3. 始终输出 `stream.present`；bool 才输出 `value`，否则只输出 JSON 类型标签。
  4. 只统计白名单 role 和 content-block type；其他字符串统一计入 `other`，不保留原值。
  5. 对排序后的完整工具名列表使用长度前缀条目计算 SHA-256；只对 `knownToolNames` 中存在的名称输出精确值，并排序、去重。
  6. 遍历 schema map/array，最大深度 32。只统计固定关键字类别和根级 `properties`/`required` 大小；不得保留属性名、description、enum 值、default、example、正则或任意字符串。
  7. `system`、`metadata`、`thinking`、`input` 只输出 JSON 类型以及字符串字符数、数组条数或对象键数。
  8. 安全标量/集合/形态白名单之外的所有顶层键统一计入 `unknown_top_level_fields`，不保留名称。
  9. 只使用固定大小 map、计数器、一个摘要和三个已知工具名，使输出大小与输入集合基数无关。
  10. 解析上游 URL，把 query 概括为 `beta=absent|true|false|other` 和 `other_count=N`。非 beta 参数只计数，不保留名称/值；重复或混合 beta 值统一记为 `other`。

  从 `handler.go` 删除旧 `summarizeRequestParams` 定义。详细错误日志改用 `redactUpstreamURL(backendURL)` 加 `summarizeUpstreamQuery(backendURL)`，不再打印原始 `backendURL`。在 `providerLogFields` 中附加相同的标准化 beta/其他参数数量字段，使普通 `>>>`/`<<<` 行能区分 query 脱敏和 query 缺失。

- [x] **步骤 4：格式化并确认 GREEN**

  ```bash
  gofmt -w internal/proxy/request_diagnostics.go internal/proxy/request_diagnostics_test.go internal/proxy/handler.go
  go test ./internal/proxy -run '^TestSummarizeRequestParams(ProtocolStructure|StreamPresence|StableToolDigest|BoundsLargeCollections)$' -count=1
  ```

  预期：`ok magic-claude-code/internal/proxy`。

- [x] **步骤 5：更新 Handler 级安全断言**

  扩展 `internal/proxy/server_test.go` 中的 `TestProxyRecordsSSELabeledHTTPError` 和 `TestSummarizeRequestParamsAllowsOnlySafeDiagnostics`，断言 400、429、500 错误日志包含新的结构字段，同时所有现有敏感标记、自定义工具名、description、schema 属性名、metadata 键和未知扩展名仍不存在。

  测试请求 URL 增加 `?beta=true&token=secret-query-value`。断言普通日志和详细错误日志包含脱敏后的上游 URL、`beta=true`、`other_count=1`，同时原始 query、`token` 名称和值均不存在。

  运行：

  ```bash
  go test ./internal/proxy -run 'TestProxyRecordsSSELabeledHTTPError|TestSummarizeRequestParamsAllowsOnlySafeDiagnostics' -count=1
  ```

  预期：响应状态/body、用量持久化和无心跳行为不变，测试 PASS。

- [x] **步骤 6：执行回归验证**

  ```bash
  go test ./internal/proxy -count=1
  make test
  git diff --check
  ```

  预期：全部通过；`make test` 包含 race detector 和覆盖率。

- [x] **步骤 7：记录证据并提交**

  将两份规格更新为 4 / 4 已完成，勾选任务 4 全部条目，记录 RED/GREEN 命令证据和实现提交，并对扩大的诊断面请求一次新的安全审查。

  ```bash
  git add internal/proxy/request_diagnostics.go \
    internal/proxy/request_diagnostics_test.go \
    internal/proxy/handler.go \
    internal/proxy/server_test.go \
    sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec.md \
    sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/spec_ZH.md
  git commit -m "fix(proxy): add safe protocol diagnostics"
  ```

#### 验证

- [x] 可以区分缺失、false、true 和类型错误的 `stream` 状态。
- [x] 可以通过摘要比较工具组成变化，并显示已知 Web 工具。
- [x] 统计兼容性相关 schema 特征，但不保留 schema 内容。
- [x] 显示 message 结构，但不包含消息正文、tool input 或 tool result 内容。
- [x] system、metadata、thinking、input 和未知字段只暴露形态/数量。
- [x] 大集合的摘要大小保持有界。
- [x] 现有响应、用量、心跳、修复器和安全行为保持不变。
- [x] 日志可以区分 beta 存在和 URL 脱敏，同时不泄露其他 query 参数。
- [x] 定向测试、完整代理测试、`make test` 和新的安全审查通过。

#### 实际验证证据

- RED：`go test ./internal/proxy -run '^TestSummarizeRequestParams(ProtocolStructure|StreamPresence|StableToolDigest|BoundsLargeCollections)$' -count=1` 失败，证明旧摘要缺少 `body_bytes`、stream 存在性、工具摘要与 schema 结构。
- Query RED：`go test ./internal/proxy -run '^TestSummarizeUpstreamQuery$' -count=1` 因摘要函数尚不存在而失败。
- Handler RED：定向测试复现了普通、SSE 与详细错误日志中的原始 query 泄漏及结构字段缺失。
- GREEN：结构摘要、query 摘要和 Handler 安全断言的定向测试全部通过。
- 安全复核额外发现畸形 URL 在解析失败时会由共享 helper 原样回退；先由 `TestRedactUpstreamURL` 复现，再改为固定 `<invalid-url>` 占位符并通过测试。
- `go test ./internal/proxy -count=1`、`go vet ./internal/proxy`、`make test` 和 `git diff --check` 均通过；`make test` 启用了 race detector 和覆盖率。
- 实现提交：`bc28637`（`fix(proxy): add safe protocol diagnostics`）。
- 新的定向安全复核结论记录于 `review-notes_ZH.md` 和 `review-notes.md`；未发现遗留的可复现逻辑或安全缺陷。
