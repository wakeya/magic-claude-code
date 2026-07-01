# 智谱 Web 工具兼容性恢复规格

代理入口：`POST /v1/messages`、`POST /anthropic/v1/messages`
观测客户端：Claude Code 2.1.196
观测供应商：智谱 Anthropic 兼容端点（`/api/anthropic/v1/messages`）
技术栈：Go 1.26、`net/http`、现有反应式修复器
最后更新：2026-07-01
进度：`fix/sse-error-handling` 已并入 main；全部任务完成——任务 0 安全 SSE 异常诊断（commit `b3931c8`）、任务 1 红灯测试、任务 2 1210 分类（commit `439b1ed`）、任务 3 回归验证通过（`make test -race` 绿）；真实供应商验证按设计未执行。

## 整体分析（源站分析）

### 已观测的失败链路

错误与 Claude Code 动态加载内置 `WebFetch` 或 `WebSearch` 工具相关，但发生在工具真正执行之前：

1. Claude Code 发出正常流式请求，模型成功返回一个 `ToolSearch` 调用。
2. 本地 `ToolSearch` 返回 `WebFetch` 或 `WebSearch` 的 `tool_reference`。
3. 下一次上游请求包含选中工具的定义。
4. 供应商对 `stream: true` 请求返回 HTTP 200、SSE 媒体类型和一个 589-603 字节的短响应；目前未记录其事件结构。
5. Claude Code 自动回退，移除 `stream` 字段后重发同一个请求。观测到的新请求恰好少 14 字节，与移除 `"stream":true,` 一致。
6. 供应商以 HTTP 400 和结构化错误码 `1210` 拒绝非流式请求：

```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "code": "1210",
    "message": "[1210][API 调用参数有误，请检查文档。][request-id]"
  }
}
```

本地伪 Anthropic 端点稳定复现了 Claude Code 的回退行为：对 `stream: true` 请求返回不可用的 HTTP 200 响应后，Claude Code 会发送第二个不含 `stream` 字段的请求。这证明两次上游请求可能属于同一次客户端操作，而不是两轮用户对话；但不能证明真实的 589-603 字节 SSE 标记供应商响应是 JSON，也无法识别触发回退的事件。

### 会话与请求对证据

Claude Code 会话 `202606302035` 以以下结构时间线结束：

```text
04:50:02 ToolSearch -> matches [WebSearch, WebFetch]
04:50:04 API 400 / 1210
04:58:13 API 400 / 1210
08:50:58 API 400 / 1210
```

最后一次失败对应的 SQLite 请求对为：

| 尝试 | Stream 字段 | 请求字节 | HTTP 状态 | 响应字节 | 记录结果 |
| --- | --- | ---: | ---: | ---: | --- |
| 首次 | `true` | 859619 | 200 | 589 | `client_aborted` |
| 回退 | 缺失 | 859605 | 400 | 213 | `http_error`，code 1210 |

14 字节请求差与移除 `"stream":true,` 一致。第一次 400 紧跟两个 Web 工具动态加载；随后两轮没有新的 ToolSearch 仍继续失败，说明加载后的定义持续进入后续请求。更早的独立会话只选择 `WebFetch` 后也失败，因此证据不限于供应商原生 WebSearch 行为。

两次实际上游 URL 均包含 `?beta=true`。普通 `>>>`/`<<<` 日志通过 `config.RedactURL` 隐藏 query，详细错误行确认 beta 实际存在。因此 cc-switch #3090 的 beta 缺失模式不能解释这些 MCC 失败。

当前置信度：

| 陈述 | 状态 |
| --- | --- |
| Web 工具加载发生在失败之前并持续影响后续轮次 | 已确认 |
| Claude Code 移除 14 字节 stream 成员后重试 | 已确认 |
| 失败的 MCC 请求包含 `beta=true` | 已确认 |
| 首次 HTTP-200 SSE 响应结构有效且完整 | 未知 |
| `$schema` 或 `additionalProperties` 是确切被拒字段 | 未知 |

### 捕获到的工具 Schema 形态

隐私安全的本地捕获只记录工具名、对象字段名和 schema 结构，没有持久化真实会话、凭据、system prompt、metadata 或工具描述。

`WebFetch` 包含：

```json
{
  "name": "WebFetch",
  "input_schema": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "type": "object",
    "properties": {
      "url": {"type": "string", "format": "uri"},
      "prompt": {"type": "string"}
    },
    "required": ["url", "prompt"],
    "additionalProperties": false
  }
}
```

`WebSearch` 也包含相同的根级 `$schema` 和 `additionalProperties`，属性为 `query`、`allowed_domains` 和 `blocked_domains`。目前尚未证明供应商具体拒绝其中哪一个字段；但这些公共根级元数据已经包含在仓库既有的工具兼容清理范围中。

### 现有恢复机制的缺口

`internal/proxy/rectifier.go` 已具备所需的安全转换：

- `cleanTools` 会从每个工具的 `input_schema` 删除根级 `$schema`、`$id`、`$comment` 和 `additionalProperties`。
- 它还会删除工具级 `cache_control`，并修复缺失或空 schema。
- 当工具定义没有可修改内容时，`RectifyRequest` 返回 `applied=false`，从而阻止重试。

当前缺口是错误分类。`matchErrorPattern` 不识别智谱结构化错误码 `1210` 或短语 `API 调用参数有误`，因此现有清理逻辑从未执行。

### 选定设计

采用两阶段方案。首先为异常 SSE 响应增加隐私安全的结构诊断，在不记录生成文本或 thinking 的情况下分类 589 字节 HTTP-200 前置响应；取得证据后，再扩展现有反应式恢复。

拟议的恢复阶段继续依赖请求内容：

采用反应式、依赖请求内容的恢复策略：

1. 识别结构化 `error.code == "1210"`；当结构化字段缺失时，以精确的 `[1210]` / `API 调用参数有误` 消息组合兜底。
2. 先让更具体的工具、thinking/signature 和 content-block 模式完成判断，再把该响应归类为工具清理候选。
3. 将原始请求交给现有 `PatternToolValidation` 清理。
4. 只有 `cleanTools` 确实修改请求时才重试。
5. 保持现有最多一次重试和最终响应处理语义。

该方案不依赖供应商 URL，不修改成功请求，也不会仅因 1210 就重试没有可清理工具 schema 的请求。

### 未采用方案

1. **主动清理所有请求。** 虽可避免首次 400，但会改变发送给完整支持 Anthropic schema 的供应商的请求。
2. **匹配 `bigmodel.cn` 后改写请求。** 会把协议行为耦合到主机名，并遗漏兼容网关或智谱其他域名。
3. **把所有中文参数错误都当成通用重试。** 可能在模型、token 或消息参数无效时产生无意义重试。

### 对待审 SSE 分支的依赖

在 `fix/sse-error-handling` 的审查和合并去向未明确前，不得把本实现加入该 worktree。该分支正在修改同一条 400 路径上的状态优先响应分派、错误观察和代理测试。

实现前必须：

1. 明确 `fix/sse-error-handling` 是合并、修订还是放弃。
2. 从处置后的主线状态开始工作。
3. 实施前重新阅读 `handler.go` 和 400 集成测试。
4. 先执行任务 0 并审阅异常 SSE 结构，再启用任务 1-3。
5. 除非任务 0 证明存在独立的 HTTP-200 SSE 缺陷、需要先修改规格，否则恢复阶段仅修改修复器分类和测试。

## 开发检查清单

| 顺序 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 完成 | 诊断 ToolSearch/WebFetch/WebSearch 链路 | 本规格中的脱敏请求结构和回退证据 | 本地伪端点复现 stream 回退 |
| 2 | 完成 | 批准最小反应式设计 | 错误码分类 + 复用既有工具清理 | 用户于 2026-07-01 批准 |
| 3 | 完成 | 明确待审 SSE 分支去向 | 稳定主线基线 | `fix/sse-error-handling` 已并入 main |
| 4 | 完成 | 安全捕获异常 SSE 结构 | `internal/usage/sse.go`、代理异常日志与测试 | 合成不完整/error stream 输出纯结构证据；无内容泄露（commit `b3931c8`） |
| 5 | 完成 | 审阅任务 0 证据门 | 本规格中的决策记录 | GO —— 见任务 0 证据门决策 |
| 6 | 完成 | 添加失败单元测试和代理回归测试 | `rectifier_test.go`、`server_test.go` | RED 已确认（3 通过 / 6 因正确原因失败）；任务 2 转绿（commit `439b1ed`） |
| 7 | 完成 | 安全识别智谱不透明参数错误 | `rectifier.go` | 新增 `isOpaqueToolCompatibilityError`，位于更高优先级分类之后；仅 `cleanTools` 改变请求时恢复（commit `439b1ed`） |
| 8 | 完成 | 完整回归并记录证据 | 更新进度和验证章节 | `make test -race` 通过（exit 0）；Kimi/thinking/content-type/SSE/usage 套件无回归 |

## 需求

### 功能需求

1. 异常 SSE 响应必须提供有界结构诊断：字节数、完成状态、解析错误数、白名单 event/content-block 计数、白名单 stop reason，以及存在时的纯数字错误码。
2. 只有流被中断、不完整、解析失败或包含明确 error event 时才输出 SSE 诊断；正常完整 SSE 不增加异常日志。
3. 只有任务 0 证据门确认恢复目标仍有效后，结构化 `error.code` 为字符串 `1210` 的 400 响应才成为工具兼容清理候选。
4. 结构化错误码缺失或无法提取时，必须识别同时包含 `[1210]` 和 `API 调用参数有误` 的已知消息格式。
5. 现有明确工具错误、thinking/signature 错误和不支持 content block 的高优先级分类不得改变。
6. 候选请求必须复用现有 `cleanTools`；不得新增主动请求转换。
7. 只有清理报告实际发生变化时才允许重试。
8. 请求没有工具，或工具 schema 不含可清理兼容字段时，不得仅因供应商返回 1210 而重试。
9. 重试必须保留模型映射、messages、核心工具名称/描述/properties/required、headers、认证行为和端点。
10. 每个客户端请求仍最多触发一次修复器重试。
11. 若重试失败，重试响应继续作为最终响应，与当前修复器语义一致。

### 安全与隐私需求

1. 不得记录或持久化原始请求体、system prompt、messages、metadata、工具描述、工具 schema、Authorization 值或凭据。
2. 除现有已清洗的上游响应处理外，不得把供应商 request ID 加入新日志。
3. 本功能不得扩大请求参数日志范围。
4. 错误检测必须复用 `tryRectify` 已读取的有界错误体，不得新增第二次无界读取。
5. 真实供应商验证是可选项，因会消耗额度并向外发送请求，必须先获得明确批准。自动化验收必须使用 `httptest` fixture。
6. SSE 诊断不得保留或记录 text/thinking delta、tool input/result 内容、错误消息、任意 event/type 字符串、现有白名单之外的响应头或原始响应 payload。

### 约束

1. 不得增加供应商主机名判断或配置开关。
2. 不得修改 Claude Code 的 stream 回退行为。
3. 任务 0 可以扩展 SSE 观察元数据，但不得修改心跳时序、响应体转发、usage 值/状态、429 重试、供应商选择或模型映射。
4. 除非独立失败 fixture 证明不兼容，不得删除 `format: "uri"`、`minLength` 等嵌套属性约束。
5. 不得修改首次就成功的请求。
6. 在待审 SSE 分支去向明确前不得实施。

### 边界情况

1. `error.code` 缺失，但存在精确的方括号 1210 消息。
2. 无关错误文本偶然包含数字 `1210`；没有结构化错误码或精确标记时不得匹配。
3. 1210 响应对应的请求没有 `tools` 数组；清理无变化，不重试。
4. 1210 响应对应的工具 schema 只包含受支持核心字段；清理无变化，不重试。
5. 请求同时包含可清理工具元数据和 thinking 历史；1210 兜底只做工具清理，不推测性清理 thinking。
6. 重试返回成功 SSE、成功 JSON、另一个 400 或传输错误；继续采用现有 handler 语义。
7. 首次 400 错误体超过修复器观察上限；完整客户端转发行为由最终处置后的 SSE/error 分支实现决定。
8. HTTP-200 SSE 流未出现 `message_stop` 就关闭、包含无效 JSON、发出明确 error event，或被客户端中断。

### 非目标

1. 证明所有智谱部署具体拒绝 `$schema` 或 `additionalProperties` 中的哪一个。
2. 阻止 Claude Code 执行 stream 到非 stream 的回退。
3. 支持供应商原生 WebSearch/WebFetch 服务端工具。
4. 对所有 JSON Schema 2020-12 关键字做通用降级。
5. 增加供应商专用 UI 或配置。
6. 实现或合并待审 SSE error-handling 分支。
7. 记录成功 SSE 响应内容，或增加通用响应捕获能力。

### 验收标准

1. 合成不完整/error SSE fixture 输出有界事件结构且不包含内容标记；正常完整 SSE fixture 不输出异常日志。
2. 实施修复器前，在本规格记录任务 0 证据和恢复 go/no-go 决策。
3. 通过证据门后，捕获到的智谱错误 fixture 被分类为工具清理候选。
4. WebFetch 和 WebSearch 请求 fixture 在删除根级 `$schema` 与 `additionalProperties` 后重试一次。
5. WebFetch 的 `format: "uri"`、WebSearch 的 `minLength` 等核心 schema 字段保持不变。
6. 没有可清理工具字段的 1210 fixture 不重试，原样转发。
7. 现有 Kimi、thinking/signature、未知 content、大型 400 body、SSE 状态、心跳和 usage 测试继续通过。
8. 在处置后的主线基线上执行 `make test` 通过。

## 任务详情

### 任务 0：无内容地观察异常 SSE 结构

#### 需求

**Objective（目标）** — 在使 1210 恢复假设可执行前，识别短 HTTP-200 SSE 响应被中断的原因。

**Outcomes（成果）** — 现有 SSE observer 暴露固定、有界的结构诊断；代理只对中断、不完整、解析失败或明确 error 的流输出一条关联异常日志。

**Evidence（证据）** — 合成不完整/error stream 报告 event 计数、完成状态、解析失败、stop reason、数字错误码、字节数和安全请求结构；敏感 text/thinking/error message 均不存在，完整 stream 不输出异常日志。

**Constraints（约束）** — 必须先处置 `fix/sse-error-handling` 的任务 4，以复用其安全请求摘要。不得缓冲或持久化原始 SSE payload，不得改变流转发或 usage 结果。

**Edge Cases（边界）** — event 跨 chunk；CRLF 分隔符；`[DONE]`；无 `message_stop` 的 EOF；无效 data JSON；明确 `event: error`；任意 event/type/error 字符串；数字与非数字供应商错误码；部分输出后的客户端断开。

**Verification（验证）** — 定向 observer 和 handler 测试通过；异常日志有界且无合成敏感标记；现有 SSE 套件和 `make test` 保持通过。

#### 计划

- [x] 确认 SSE 分支已完成处置，并且实施基线包含其安全协议摘要任务 4。
- [x] 在 `internal/usage/sse_test.go` 增加 RED 测试：stream 包含 `message_start`、`content_block_start`、`content_block_delta`、`message_delta` 和明确 `error` event，但没有 `message_stop`。在 text、thinking、tool input 和 `error.message` 中加入唯一字符串，断言期望诊断契约：

```go
type SSEDiagnostics struct {
    Complete          bool           `json:"complete"`
    ParseErrors       int            `json:"parse_errors"`
    Events            map[string]int `json:"events"`
    ContentBlockTypes map[string]int `json:"content_block_types"`
    StopReasons       map[string]int `json:"stop_reasons"`
    ErrorEvents       int            `json:"error_events"`
    ErrorTypes        map[string]int `json:"error_types"`
    NumericErrorCodes map[string]int `json:"numeric_error_codes"`
}
```

  固定类别：

```text
events: message_start, content_block_start, content_block_delta,
        content_block_stop, message_delta, message_stop, error, ping, other
content_block_types: text, thinking, redacted_thinking, tool_use,
                     server_tool_use, web_search_tool_result, other
stop_reasons: end_turn, max_tokens, tool_use, stop_sequence,
              pause_turn, refusal, model_context_window_exceeded, other
error_types: invalid_request_error, api_error, authentication_error,
             permission_error, rate_limit_error, overloaded_error, other
```

  只有 1-8 位纯数字错误码可以作为键；非数字或过长错误码只增加 `other` 计数，不保留原值。断言序列化诊断不包含任何提供的正文、任意 event/type 值或错误消息。

- [x] 增加测试：完整 `message_stop`/`[DONE]` stream 设置 `Complete=true`；chunk 边界不改变计数；无效 data 增加 `ParseErrors`；每个计数 map 始终限于固定类别。
- [x] 运行并确认 RED：

```bash
go test ./internal/usage -run '^TestSSEObserverDiagnostics' -count=1
```

预期：FAIL，因为 `SSEDiagnostics` 和 `Diagnostics()` 尚不存在。

- [x] 扩展 `internal/usage/sse.go`，让 `SSEObserver.observeBlock` 在现有 usage 解析同时更新诊断，并新增：

```go
func (o *SSEObserver) Diagnostics() SSEDiagnostics
```

  只解析现有 event 名和以下结构 JSON 字段：顶层 `type`、`content_block.type`、`delta.stop_reason`、`error.type`、`error.code`。所有不在白名单内的字符串统一映射为 `other`。不得保留 `text`、`thinking`、`input`、result 内容、`error.message`、原始 data line 或 headers。返回 map 副本，防止调用方修改 observer 状态。

- [x] 运行定向 observer 测试并确认 GREEN。
- [x] 在 `internal/proxy/server_test.go` 增加 RED 代理测试：
  1. HTTP-200 SSE 后端发出包含 code `1210` 和敏感消息的短明确 error event，随后无 `message_stop` 关闭；断言恰好一条 `[Stream] anomaly` 日志包含请求 ID、响应字节数、`complete=false`、`error_events=1`、数字 code `1210` 和安全请求诊断，但不含敏感消息或 payload；
  2. 正常完整 SSE 响应不输出 anomaly 行；
  3. 客户端中断的流输出相同结构行，同时保持现有 `client_aborted` usage 行为。
- [x] 在 `internal/proxy/handler.go` 为 `streamUsageObserver` 增加：

```go
func (o *streamUsageObserver) Diagnostics() usage.SSEDiagnostics
```

  将 `copyWithHeartbeatAndObserver` 结果保存为 `streamErr`。复制结束后，当 `streamErr != nil`、`!streamObserver.IsComplete()`、`ParseErrors > 0` 或 `ErrorEvents > 0` 时输出一条有界 anomaly 日志。只序列化 `SSEDiagnostics`、响应字节数和处置后 SSE 分支提供的安全请求摘要。保持现有错误赋值：只有真实复制/客户端错误设置 `usage.ErrorClientAborted`；没有终止事件但干净 EOF 只记录诊断，不重新标记为客户端中断。
- [x] 运行定向代理和现有 SSE 测试：

```bash
go test ./internal/proxy -run 'TestProxyLogsAnomalousSSEStructure|TestProxyDoesNotLogCompletedSSEAsAnomaly|TestProxyRecordsStreamingUsage|TestProxyRecordsStreamingUsageWhenUpstreamDoesNotCloseAfterMessageStop' -count=1
```

预期：逐字节响应转发和 usage 结果不变，全部 PASS。

- [x] 运行 `go test ./internal/usage -count=1`、`go test ./internal/proxy -count=1` 和 `make test`。
- [x] 独立提交任务 0：

```bash
git add internal/usage/sse.go internal/usage/sse_test.go \
  internal/proxy/handler.go internal/proxy/server_test.go
git commit -m "feat(proxy): log safe SSE anomaly structure"
```

- [x] 仅在明确批准消耗额度的真实验证后，复现一次 Web 工具失败轮次，并在本规格中只记录 anomaly JSON、成对请求大小/状态和 commit hash。若未批准真实运行，标记证据门仍待处理，不开始任务 1。
- [x] 记录 go/no-go 决策：只有捕获结构支持可由 rectifier 恢复的工具/请求不兼容时才继续任务 1-3；否则先围绕观测到的 HTTP-200 SSE 缺陷修订本规格，再编写恢复代码。

#### 验证

- [x] 诊断是固定大小结构元数据，不是响应捕获。
- [x] 异常流只记录一次，正常完整流保持安静。
- [x] text、thinking、tool data、任意字符串和错误消息绝不出现。
- [x] 响应转发、心跳完成、usage 值/状态和客户端中断分类保持不变。
- [x] 在任务 1 开始前记录任务 0 证据门。

#### 证据门决策（2026-07-01，commit `b3931c8`）

**真实验证：** 按设计未执行。真实供应商验证需明确授权（消耗额度且向外发送请求）；未获批准，故按任务边界使用合成 `httptest` fixture。

**决策：GO —— 继续任务 1-3。**

恢复目标是结构化 `error.code == "1210"` 的 400 响应（回退请求），而非 HTTP-200 SSE 前置响应。该 400 body 与成对请求大小/状态已在前述源站分析中捕获；携带 WebFetch/WebSearch 定义的请求包含根级 `$schema` + `additionalProperties`（捕获到的工具 Schema 形态），现有 `cleanTools` 会将其删除，同时保留 `format`、`minLength`、`properties`、`required`。任务 0 增加了安全诊断基础设施，并以合成 fixture 验证：

- 含 code `1210` 的明确 `event: error` SSE 流恰好产生一条有界 `[Stream] anomaly` 日志，包含 `complete=false`、`error_events=1`、数字 code `1210` 与安全请求摘要，且无 text/thinking/error message/raw payload 泄露；
- 正常完整流保持安静；
- 模拟客户端断开仍输出 anomaly 并保留 `ErrorClientAborted`；
- `make test -race` 通过；响应转发、心跳、usage 值/状态与客户端中断分类保持不变。

**剩余限制：** 真实 589-603 字节 HTTP-200 SSE 前置响应结构仍未捕获。若后续批准真实运行，新诊断将以安全方式记录它。这不阻塞 1210 恢复——恢复目标针对的是已记录的 400 响应。

### 任务 1：添加智谱 1210 工具恢复的红灯测试

#### 需求

**Objective（目标）** — 在修改生产代码前复现缺失的错误分类和端到端重试行为。

**Outcomes（成果）** — 单元 fixture 覆盖结构化和仅消息 1210；代理 fixture 覆盖 WebFetch、WebSearch 和无清理保护条件。

**Evidence（证据）** — 定向测试因 `matchErrorPattern` 返回 `PatternNone`、可恢复 fixture 只发出一次上游请求而失败。

**Constraints（约束）** — 只使用合成 prompt、schema、凭据和 request ID，不调用真实供应商。

**Edge Cases（边界）** — 精确消息兜底、无关数字、无工具、已清理 schema。

**Verification（验证）** — 修改 `rectifier.go` 前运行具名测试并记录预期失败。

#### 计划

- [x] 确认待审 SSE 分支已完成处置，并在选定实施基线上运行 `git status --short --branch`。
- [x] 修改 `internal/proxy/rectifier_test.go`，添加等价的表驱动测试：

```go
func TestMatchErrorPattern_Zhipu1210(t *testing.T) {
    tests := []struct {
        name string
        body string
        want ErrorPattern
    }{
        {
            name: "structured code",
            body: `{"type":"error","error":{"type":"invalid_request_error","code":"1210","message":"[1210][API 调用参数有误，请检查文档。][synthetic-id]"}}`,
            want: PatternToolValidation,
        },
        {
            name: "exact message fallback",
            body: `{"error":{"message":"[1210][API 调用参数有误，请检查文档。][synthetic-id]"}}`,
            want: PatternToolValidation,
        },
        {
            name: "unrelated digits",
            body: `{"error":{"message":"request 1210 could not be found"}}`,
            want: PatternNone,
        },
        {
            name: "content block remains higher priority",
            body: `{"error":{"code":"1210","message":"unsupported content type: tool_reference"}}`,
            want: PatternGenericBadRequest,
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := matchErrorPattern([]byte(tt.body)); got != tt.want {
                t.Fatalf("matchErrorPattern() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

- [x] 修改 `internal/proxy/server_test.go`，添加表驱动 WebFetch/WebSearch fixture。后端第一次收到请求时返回精确的合成 1210 JSON；重试时断言根级 `$schema`、`additionalProperties` 和工具级 `cache_control` 已删除，同时 `name`、`properties`、`required`、WebFetch `format` 与 WebSearch `minLength` 仍保留，然后返回 HTTP 200。
- [x] 添加独立代理 fixture，其工具 schema 不含可清理字段。返回 1210 后断言后端只收到一次请求，客户端原样收到初始 400 body。
- [x] 运行：

```bash
go test ./internal/proxy -run 'TestMatchErrorPattern_Zhipu1210|TestProxy(RetriesZhipu1210WebTools|DoesNotRetryZhipu1210WhenToolCleanupMakesNoChanges)' -count=1
```

预期：修改生产代码前 FAIL。结构化/消息 1210 返回 `PatternNone`，可恢复代理 fixture 只观测到一次请求而不是两次。

#### 验证

- [x] 红灯测试因目标行为缺失而失败，不是 fixture 语法或初始化错误。
- [x] 测试数据不包含真实凭据、prompt、会话或供应商 request ID。

### 任务 2：添加最小 1210 分类

#### 需求

**Objective（目标）** — 将窄范围智谱参数错误路由到既有工具清理，不改变正常请求处理。

**Outcomes（成果）** — `matchErrorPattern` 识别结构化 1210 和精确消息兜底；`RectifyRequest`、`tryRectify` 保持不变。

**Evidence（证据）** — 局部修改 `rectifier.go` 后，任务 1 测试通过。

**Constraints（约束）** — 明确的工具、thinking/signature 和 unsupported/unknown content-type 分类保持高优先级；不得匹配任意 `1210` 文本。

**Edge Cases（边界）** — 无效 JSON、数字或无关文本、不含 code 的精确中文短语。

**Verification（验证）** — 运行定向修复器和代理测试。

#### 计划

- [x] 修改 `internal/proxy/rectifier.go`，新增以下窄范围 helper：

```go
func isOpaqueToolCompatibilityError(errorBody []byte, lowerMessage string) bool
```

该 helper 只反序列化 `error.code` 所需的结构化字符串字段，并在以下任一条件成立时返回 true：

```go
code == "1210"
```

或同时存在两个精确消息标记：

```go
strings.Contains(lowerMessage, "[1210]") &&
    strings.Contains(lowerMessage, "api 调用参数有误")
```

- [x] 在 `matchErrorPattern` 中保持以下分类顺序：明确工具错误；thinking/signature 错误；unsupported/unknown content type（`PatternGenericBadRequest`）；不透明 1210 兼容错误；其余通用 invalid request。1210 匹配时返回 `PatternToolValidation`。这样复用 `cleanTools`，且只有 `RectifyRequest` 报告发生变化时 `tryRectify` 才会重试。
- [x] 不修改 `cleanTools`、`RectifyRequest` 或 `Handler.tryRectify`，除非处置后的主线已经改变这些契约；若契约变化，必须先更新本规格再实施。
- [x] 运行任务 1 的定向命令。

预期：PASS。

- [x] 运行全部修复器相关测试：

```bash
go test ./internal/proxy -run 'TestMatchErrorPattern|TestCleanTools|TestRectifyRequest|TestProxyRetries' -count=1
```

预期：PASS。

- [x] 审查定向 diff 后再提交：

```bash
git diff --check
git diff -- internal/proxy/rectifier.go internal/proxy/rectifier_test.go internal/proxy/server_test.go
git add internal/proxy/rectifier.go internal/proxy/rectifier_test.go internal/proxy/server_test.go
git commit -m "fix(proxy): recover Zhipu web tool parameter errors"
```

#### 验证

- [x] 分类仅依赖结构化错误码或精确标记。
- [x] 未增加供应商主机名判断或主动请求转换。
- [x] 不可清理的请求不会重试。

### 任务 3：回归验证并关闭规格

#### 需求

**Objective（目标）** — 证明兼容性修改不会破坏待审分支的状态优先错误处理或既有代理行为。

**Outcomes（成果）** — 在中英文规格中记录完整测试结果和最终文件/提交证据。

**Evidence（证据）** — 全新定向测试和完整测试套件输出。

**Constraints（约束）** — 真实智谱验证仍为可选且需明确授权。

**Edge Cases（边界）** — 如遇已知无关的 updater 网络 flaky，必须单独重跑并明确记录，不得静默视为成功。

**Verification（验证）** — 在干净实施 worktree 中运行仓库完整测试目标。

#### 计划

- [x] 运行：

```bash
make test
```

预期：PASS，包括仓库配置的 race detector。

- [x] 运行：

```bash
git status --short
git diff --check HEAD^ HEAD
```

预期：只包含本功能相关文件，且无空白错误。

- [x] 只有用户明确批准真实验证时，才通过已配置供应商发送一个最小合成请求；仅记录状态码、请求次数、工具名和是否发生清理，不记录原始 body 或凭据。否则明确记录“按设计未执行真实验证”。
- [x] 在 `spec.md` 与 `spec_ZH.md` 中更新 `Progress`、开发检查清单和本任务验证章节，写入精确命令、结果、commit hash 与剩余限制。

#### 验证

- [x] 定向测试通过。
- [x] `make test` 通过。
- [x] 中英文规格语义一致。
- [x] 明确记录真实验证状态。

#### 关闭证据（2026-07-01）

**`zhipu-web-compat` 分支提交（未推送）：**
- `b3931c8` —— `feat(proxy): log safe SSE anomaly structure`（任务 0）
- `344ab54` —— `docs(zhipu-web): record Task 0 evidence gate decision`
- `439b1ed` —— `fix(proxy): recover Zhipu web tool parameter errors`（任务 1+2）

**命令与结果：**
- `go test ./internal/usage -count=1` → 56 通过。
- `go test ./internal/proxy -run 'TestMatchErrorPattern|TestCleanTools|TestRectifyRequest|TestProxyRetries' -count=1` → 43 通过。
- `go test ./internal/proxy -run 'TestMatchErrorPattern_Zhipu1210|TestProxy(RetriesZhipu1210WebTools|DoesNotRetryZhipu1210WhenToolCleanupMakesNoChanges)' -count=1` → 9 通过（`439b1ed` 前 RED，之后 GREEN）。
- `make test`（= `go test -v -race -coverprofile=coverage.out ./...`）→ exit 0，无 `FAIL`。
- `git status --short` → 提交后工作区干净。
- `git diff --check HEAD^ HEAD` → 无空白错误。

**验收标准状态：** 全部 8 项以合成 `httptest` fixture 通过——（1）异常 fixture 输出有界结构且无内容标记；（2）恢复前已记录任务 0 证据与 GO 决策；（3）智谱 1210 fixture 归类为工具清理候选；（4）WebFetch/WebSearch 在删除 `$schema`/`additionalProperties` 后重试一次；（5）`format`/`minLength`/`properties`/`required` 保留；（6）无可清理字段的 1210 fixture 不重试原样转发；（7）现有 Kimi/thinking/未知 content/大型 400/SSE 状态/心跳/usage 测试通过；（8）`make test` 通过。

**真实供应商验证：** 按设计未执行（未获明确批准；消耗额度且向外发送请求）。合成 fixture 确定性地复现了 1210 错误形态与清理/重试语义。

**剩余限制：** 真实 589-603 字节 HTTP-200 SSE 前置响应结构仍未捕获；若后续批准真实运行，任务 0 诊断将以安全方式记录它。这不影响 1210 恢复——恢复目标针对的是已记录的 400 响应。
