# SSE 标记的 HTTP 错误响应处理规格

本地页面：无
代理入口：`POST /v1/messages`、`POST /anthropic/v1/messages`
参考来源：Docker 运行日志、`data/proxy.db`、`internal/proxy/handler.go`、`internal/proxy/heartbeat.go`
技术栈：Go 1.26 标准库（`net/http`、`io`、`log`）+ SQLite 用量记录器
最后更新：2026-06-30
进度：草稿，0 / 2 已规划

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
| 1 | 已规划 | 让 HTTP 错误状态优先于 SSE 媒体类型 | `internal/proxy/handler.go` | 回归测试证明错误体转发、诊断日志和用量持久化正确 |
| 2 | 已规划 | 验证流式处理和修复器无回归 | `internal/proxy/server_test.go` 与现有代理测试 | 代理定向测试和完整 Go 测试通过 |

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

### 任务 1：将最终 HTTP 错误送入错误观察器

#### 需求

**Objective（目标）** — 即使媒体类型为 `text/event-stream`，也要观察、记录并持久化最终上游 HTTP 错误。

**Outcomes（成果）** — `Handler.ServeHTTP` 让 `>= 400` 状态优先于 SSE 媒体类型；现有非 SSE 错误路径转发响应并记录完整错误元数据。

**Evidence（证据）** — 测试后端返回状态 400、`Content-Type: text/event-stream` 和已知的 213 字节错误体。客户端收到完全一致的响应体，伪用量记录器包含 `http_error` 和 `skipped_non_2xx`，捕获的日志包含请求参数摘要和清洗后的响应。

**Constraints（约束）** — 使用现有观察器、清洗器、日志格式化和记录器。不得在 SSE 分支内复制 HTTP 错误处理逻辑。

**Edge Cases（边界）** — 请求 stream 设置与响应媒体类型不一致；修复器恢复或替换响应；错误响应体大于诊断采集上限。

**Verification（验证）** — 定向 handler 测试证明响应保真、不进入 SSE 心跳、输出详细日志并持久化错误元数据。

#### 计划

1. 修改 SSE 分支条件，使其同时要求最终响应为非错误状态且媒体类型为 SSE。
2. 保持响应头转发和状态码写入行为不变。
3. 对所有最终状态 `>= 400` 复用现有非 SSE `responseObserver` 路径。
4. 确认 SSE 标记的 400 会执行现有错误日志和用量记录赋值。

#### 验证

- [ ] `Content-Type: text/event-stream` 的 400 响应逐字节转发。
- [ ] 该响应不产出 SSE 检测日志，也不启用心跳行为。
- [ ] 详细错误日志包含 `headers`、`params` 和清洗后的 `resp` 字段。
- [ ] 用量请求记录 `http_error`、清洗后的错误消息、最终状态和完整响应字节数。
- [ ] 用量 token 记录使用 `none` 和 `skipped_non_2xx`。

### 任务 2：增加回归覆盖并执行验证

#### 需求

**Objective（目标）** — 防止后续响应分派变更再次造成错误诊断缺失，或破坏成功 SSE 流式处理。

**Outcomes（成果）** — 自动化测试覆盖本次报告的失败，并保留正常 SSE 响应和反应式 400 处理覆盖。

**Evidence（证据）** — 从干净工作树执行代理定向测试和 `go test ./...` 均通过；`git diff --check` 不报告空白错误。

**Constraints（约束）** — 测试使用 `httptest.Server`、现有伪用量记录器和有界日志捕获，不需要真实供应商凭据或网络调用。

**Edge Cases（边界）** — 必须覆盖使用 SSE 媒体类型的 HTTP 400。若不模糊原始失败，可增加 429 和 5xx 表格测试。

**Verification（验证）** — 执行定向和完整 Go 测试命令，并在实现后记录实际结果。

#### 计划

1. 增加回归测试：后端返回 SSE 标记的 400 响应体，并使用 Messages 请求调用代理。
2. 断言状态码和响应体精确转发。
3. 断言用量请求和 token 错误字段。
4. 捕获日志，断言详细错误行存在且 SSE 检测行不存在。
5. 执行现有成功 SSE 和修复器测试。
6. 执行完整 Go 测试套件和空白校验。

#### 验证

- [ ] `go test ./internal/proxy -run 'Test.*SSE.*Error|TestProxyRecordsStreamingUsage|TestProxyRetries.*400' -count=1`
- [ ] `go test ./...`
- [ ] `git diff --check`
