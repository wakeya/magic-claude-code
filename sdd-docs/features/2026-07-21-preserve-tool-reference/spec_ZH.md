# 主动清洗保留 tool_reference 规格

本地页面：`internal/proxy/rectifier.go`（knownContentTypes、filterContentBlocks、cleanUnknownContentTypes）、`internal/proxy/handler.go`（transformRequest 中的 proactiveCleanUnknownContentTypes）
代理入口：`internal/proxy/handler.go` ServeHTTP / tryRectify
参考源站：`~/workspace/open-software/cc-switch/src-tauri/src/proxy/body_filter.rs`、`sdd-docs/research/2026-05-15-rectifier-pattern3-generic-bad-request.md`、`sdd-docs/features/2026-06-15-proactive-content-block-cleanup/spec.md`、2026-07-21 对 `api.moonshot.cn/anthropic` 与 `api.kimi.com/coding` 的真实 curl 探针
技术栈：Go 1.26 标准库
最后更新：2026-07-21
进度：3 / 3 计划任务（任务 1–3 已完成）

## 整体分析（源站分析）

### 问题

当某个 provider 开启 `strip_unknown_content_blocks` 时，`transformRequest` 会调用 `proactiveCleanUnknownContentTypes`，递归删除所有 `type` 不在 `knownContentTypes` 白名单内的内容块。白名单为 `text, image, tool_use, tool_result, thinking, redacted_thinking, document, file` —— **不含** `tool_reference`。

Claude Code 2.1.x 会把 `tool_reference` 块放进 `tool_result.content` 数组，作为 deferred 工具加载结果（ToolSearch）的客户端侧标记。MCC 把它清洗掉后，上游模型仍能收到外层的 `tool_result`，但丢失了"这个结果加载了 deferred 工具 X"的标记。在 kimi-k3 上（见会话 `f6549d4f-dddf-45a2-ad45-976a72476207.jsonl`，L1719–L1734 轮次），模型随后退化：在 text 里宣布"我直接调用 list_pages"，但实际产生的 `tool_use` 块坍缩成固定占位 `Bash{command:"true",description:"noop"}`，且 `thinking` 为空，陷入死循环（"我又发了一次空调用"、"我一直在发空调用，无法停止"）。同一个 kimi-k3 会话成功调用了 playwright MCP 工具 5 次——唯独 chrome-devtools-mcp（经 ToolSearch 加载、由 `tool_reference` 守护）无法触达。

在 kimi-k3 provider 上关闭 `strip_unknown_content_blocks` 后工具调用恢复，确认清洗即为回归源。

### 根因演进

该清洗是针对与今天**不同的上游行为**引入的：

1. **2026-05-15 研究**（`sdd-docs/research/2026-05-15-...`）：kimi-k2.6 在请求体含 `tool_reference` 块时返回 HTTP 400，错误为 `"unsupported content type: tool_reference"` / `"failed to convert tool result content: unsupported content type in ContentBlockParamUnion: tool_reference"`。当时 curl 复现：含 `tool_reference` → 400，移除 → 200。`bbfc0fd` 加入反应式 rectifier，在 400 后剥离该块；`d9104a1` 又将其提升为由 `StripUnknownContentBlocks` 控制的主动 opt-in 清洗。

2. **2026-06-15 规格**（`sdd-docs/features/2026-06-15-proactive-content-block-cleanup/`）：固化了不含 `tool_reference` 的白名单，并把 cc-switch 的分层清洗列为架构参考。当时对 cc-switch 存在误读——`body_filter.rs` 只剥离 `_` 前缀私有参数，**从不**触碰 `tool_reference` 或任何内容块（`src-tauri/src/proxy/` 中 `tool_reference` 出现 0 次）。

3. **今天（2026-07-21）**：kimi 的 Anthropic 兼容端点已升级。受控真实探针（见实测证据）表明，当 `tool_reference` 引用的工具存在于请求的 `tools` 数组时，三个端点如今都**接受** `tool_reference` 这一内容类型。最初的 400（"unsupported content type"）已无法复现；清洗的前提条件消失，清洗本身沦为纯副作用。

### 实测证据（真实探针，2026-07-21）

请求形态：最小 Anthropic `/v1/messages`、非流式、含一次 `tool_use`→`tool_result` 往返。"ref" 变体的 `tool_result.content` 为 `[tool_reference, text]`。`tools` 定义了 `WebSearch`。

| 端点 / 模型 | `tool_reference` → **已定义**工具 | `tool_reference` → **未定义**工具 | 无 `tool_reference` |
|---|---|---|---|
| `api.moonshot.cn/anthropic` kimi-k2.6 | **200** | 400 `Tool reference 'ToolSearch' not found in available tools` | 200 |
| `api.kimi.com/coding` kimi-for-coding（k2.7） | **200** | 400 `Invalid request Error` | 200 |
| `api.kimi.com/coding` k3 | **200** | 400 `Invalid request Error` | 200 |

结论：

- `tool_reference` **作为内容类型被三个端点接受**，包括最初触发清洗的 k2.6 moonshot 端点。
- 仍能观察到的 400，是 `tool_reference` 指向了请求 `tools` 数组里**不存在**的工具所致，而非类型本身。Claude Code 正常流程中，被引用的工具会在 ToolSearch 加载后的下一轮被注入 `tools`，因此该 400 属于异常路径，非常见路径。
- 在常见路径上清洗 `tool_reference`，等于移除模型会使用的信息，且并没有 400 可避免。

### 参考实现

**cc-switch**（`src-tauri/src/proxy/body_filter.rs`）：`filter_private_params_with_whitelist` 只递归移除以 `_` 开头的 key（带白名单逃生口与 JSON-schema 属性名保护），**不**检查或过滤内容块的 `type`。2026-06-15 规格引用 cc-switch 是作为分层架构（主动 + 反应式）的参考，而非内容块过滤的依据——后者是 MCC 独有，正是本次要纠正的部分。

### 策略

让两个调用点的清洗行为各自匹配自身前提：

- **主动清洗**（`proactiveCleanUnknownContentTypes`，首次上游请求前执行）：**保留** `tool_reference`。常见路径已不再因 `tool_reference` 而 400，保留它能维持模型的 deferred 工具上下文。
- **反应式清洗**（`cleanUnknownContentTypes`，在 `tryRectify` 内 400 后执行）：**保留清除** `tool_reference` 的能力。异常路径（`tool_reference` 指向未定义工具）仍会 400，剥离该块仍能让重试成功。

实现方式是给 `filterContentBlocks` 增加 `preserveToolReference bool` 参数。主动传 `true`；反应式传 `false`（行为与现状一致）。

后续会有**独立的特性**来弃用 `strip_unknown_content_blocks` provider flag 及前端 checkbox：一旦主动清洗不再剥离 `tool_reference`（实践中唯一出现过的非标准类型），该 flag 变为 no-op，UI 只会让用户困惑（本次事件即是一例）。该工作不在本次范围内，以保证修复可审、可回滚，作为后续任务跟踪。

### 范围

- **在范围内**：参数化 `filterContentBlocks`；验证/扩展反应式错误模式匹配以覆盖当前 kimi 错误串；测试；review notes；在 2026-06-15 规格与 2026-05-15 研究中追加带日期的勘误。
- **不在范围内**：移除 `StripUnknownContentBlocks` flag、DB 列、admin API 字段、前端 checkbox（后续特性）；改动 OpenAI 格式转换；改动 thinking 剥离逻辑。

## 开发检查清单

- [x] 参数化 `filterContentBlocks(msg, preserveToolReference)`；主动传 `true`，反应式传 `false`。
- [x] 验证 `matchErrorPattern` 识别当前 kimi 400 串（`"Invalid request Error"`、`"Tool reference '<name>' not found in available tools"`）；必要时扩展短语表。
- [x] 单元测试：主动保留 `tool_reference`；反应式清除 `tool_reference`；新增的错误模式用例使用真实错误串。
- [x] 运行 `go test ./internal/proxy/...` 与 `go test ./...`。
- [x] 追加 review notes（`review-notes.md` / `review-notes_ZH.md`），并在 2026-06-15 规格与 2026-05-15 研究中追加带日期的勘误说明。

## 需求

### 需求 1：主动清洗保留 tool_reference

当 `provider.StripUnknownContentBlocks == true` 且 `apiFormat == anthropic` 时，`transformRequest` 不得从 `messages[].content` 或嵌套的 `tool_result.content` 中移除 `tool_reference` 块。其他非标准类型（既不在 `knownContentTypes` 中、也不是 `tool_reference` 的类型）仍照常剥离。

### 需求 2：反应式清洗保留清除 tool_reference 的能力

`cleanUnknownContentTypes`（`tryRectify` 在 400 后使用）必须继续清除 `tool_reference`，以便异常路径（`tool_reference` 指向 `tools` 中不存在的工具）仍能通过重试（不含该块）恢复。

### 需求 3：反应式模式匹配覆盖当前 Kimi 错误

`matchErrorPattern` 必须把当前 kimi 400 消息归类为 `PatternGenericBadRequest`，以便 `tryRectify` 触发。至少覆盖：`"Invalid request Error"`（coding 端点，k2.7/k3）与 `"Tool reference '<name>' not found in available tools"`（moonshot 端点，k2.6）。

### 需求 4：对其他非标准类型无回归

`StripUnknownContentBlocks == true` 时的主动清洗，仍须移除 `tool_reference` 以外、不在 `knownContentTypes` 中的类型（例如假设的未来 `server_tool_use`），为真正的未知类型保留原有防线。

### 需求 5：默认透传不变

`StripUnknownContentBlocks == false`（默认）的 provider 在主动路径上不做任何修改，行为与今天完全一致。

## 任务详情

### 任务 1：参数化 filterContentBlocks

#### 需求

**Objective（目标）** — 给 `filterContentBlocks` 增加 `preserveToolReference bool` 参数，使主动与反应式调用方可以分化：主动保留 `tool_reference`（恢复模型的 deferred 工具上下文），反应式仍清除它（恢复异常路径的 400）。

**Outcomes（成果）** — `internal/proxy/rectifier.go`：`filterContentBlocks(msg map[string]any, preserveToolReference bool) bool`；`proactiveCleanUnknownContentTypes` 以 `true` 调用；`cleanUnknownContentTypes` 以 `false` 调用。`tool_reference` 不加入 `knownContentTypes` map（按 map 定义它仍属"非标准"），而是由参数单独豁免其移除。其他所有类型过滤逻辑逐字节不变。

**Evidence（证据）** — 新单测 `TestProactiveClean_PreservesToolReference` 与 `TestReactiveClean_StripsToolReference` 通过；既有 `TestCleanUnknownContentTypes_*` 在更新调用签名后仍通过。

**Constraints（约束）** — 不要把 `tool_reference` 加入 `knownContentTypes`（那会同时静默关闭反应式清除）。该参数仅豁免 `tool_reference` 的移除；对 `tool_result.content` 的递归保留。`btype == ""` 的透传分支不变。

**Edge Cases（边界）** — 顶层 `content` 中的 `tool_reference`；嵌套在 `tool_result.content` 中的 `tool_reference`；带额外字段（`tool_name`）的 `tool_reference`；同一 `tool_result` 中多个 `tool_reference` 块；`tool_reference` 与真正未知的类型混合（主动路径下仅 `tool_reference` 存活，其余被剥）。

**Verification（验证）** — `go test ./internal/proxy/... -run 'TestProactiveClean|TestReactiveClean|TestCleanUnknownContentTypes|TestFilterContentBlocks' -v`。

#### 计划

1. 在 `internal/proxy/rectifier.go` 修改签名：
   ```go
   func filterContentBlocks(msg map[string]any, preserveToolReference bool) bool {
   ```
2. 在块循环内、`!knownContentTypes[btype] && btype != ""` 判断之前，加入：
   ```go
   if preserveToolReference && btype == "tool_reference" {
       filtered = append(filtered, block)
       continue
   }
   ```
   （`tool_reference` 无嵌套 `content`，无需递归。）
3. 更新 `cleanUnknownContentTypes`（反应式）：其递归调用点 `filterContentBlocks(msg)` → `filterContentBlocks(msg, false)`。行为与今天一致。
4. 在 `internal/proxy/handler.go` 中更新 `proactiveCleanUnknownContentTypes`（确认调用链：若当前经由 `cleanUnknownContentTypes` 的循环间接调用），确保主动路径以 `preserveToolReference=true` 调用。若 `proactiveCleanUnknownContentTypes` 当前复用 `cleanUnknownContentTypes` 的循环，抽出一个接收该参数的共享 helper，或在主动函数内内联该循环并传 `true`。
5. 新增单元测试（见任务 3）。

#### 验证

- [x] 签名变更编译通过；所有调用方已更新（`rectifier.go` cleanUnknownContentTypes → `false`、`handler.go` proactiveCleanUnknownContentTypes → `true`、递归调用透传该参数）。
- [x] `TestProactiveClean_PreservesToolReference` 通过：输入 `[{tool_reference}, {server_tool_use}, {text}]`，保留 `tool_reference` + `text`，剥除 `server_tool_use`。
- [x] `TestReactiveClean_StripsToolReference` 通过：输入 `[{tool_reference}, {text}]`，只剩 `[{text}]`。
- [x] 既有 `server_test.go` 的 `TestProactiveClean_AnthropicStripEnabled_*` 从 `RemovesToolReference` 更新为 `PreservesToolReference`。
- [x] `go test ./internal/proxy/...` — 571 passed；`go test ./...` — 17 个包 1686 passed。

### 任务 2：验证/扩展反应式错误模式匹配

#### 需求

**Objective（目标）** — 确保 `tryRectify` 能在当前 kimi 400 消息上触发，使需求 2 的反应式 `tool_reference` 清洗真正能生效。确认覆盖 `"Invalid request Error"`（coding 端点）与 `"Tool reference '<name>' not found in available tools"`（moonshot 端点）。

**Outcomes（成果）** — `internal/proxy/rectifier.go` 的 `hasGenericInvalidRequestPhrase`（或匹配分发）把这两条串识别为 `PatternGenericBadRequest`。若其中一条已被既有短语覆盖（例如大小写不敏感的 `"invalid request"` 已覆盖 `"Invalid request Error"`），用测试记录即可，不改代码。若 `"Tool reference ... not found"` 未覆盖，则新增短语（如 `"tool reference"` + `"not found"`，或字面量 `"tool reference '"`）。

**Evidence（证据）** — 单测以实测错误 JSON body（见实测证据）为输入，断言 `matchErrorPattern` 返回 `PatternGenericBadRequest`。

**Constraints（约束）** — 不要过度匹配：新短语须表明是内容/工具引用问题，而非一般瞬时错误。短语保持窄（如 `"tool reference"` 且 `"not found"`，或字面量 `"tool reference '"`）。

**Edge Cases（边界）** — 错误体包装变体（`{"error":{"message":...}}`、`{"error":{"code":"1210","message":...}}`、顶层 `{"message":...}`）；大小写混合；旧短语 `"unsupported content type"` 仍被匹配（对旧版上游向后兼容）。

**Verification（验证）** — `go test ./internal/proxy/... -run TestMatchErrorPattern -v`。

#### 计划

1. 阅读当前 `hasGenericInvalidRequestPhrase` 与完整 `matchErrorPattern` 分发逻辑。
2. 新增两个用例，喂入真实 JSON：
   - coding：`{"error":{"type":"invalid_request_error","message":"Invalid request Error"}}`
   - moonshot：`{"error":{"type":"invalid_request_error","message":"messages.2.content.0.tool_result.content: Tool reference 'ToolSearch' not found in available tools"}}`
3. 若 `"Invalid request Error"` 已被既有的大小写不敏感短语 `"invalid request"` 覆盖，第一例无需改码即通过——在测试注释中记录。若 `"Tool reference ... not found"` 未匹配，新增短语（如 `"tool reference"`）。
4. 重跑既有 `rectifier_test.go:434` 与 `:472`（旧 "unsupported content type" 串）确认无回归。

#### 验证

- [x] coding `"Invalid request Error"` 已被 `hasGenericInvalidRequestPhrase` 覆盖（短语 `"invalid request"`）；测试记录，无需改码。
- [x] moonshot `"Tool reference '<name>' not found in available tools"` 经 `isUnsupportedContentTypePhrase` 扩展（新增 `"tool reference"` 短语）后命中。
- [x] 旧 `"unsupported content type"` 用例仍归类（向后兼容）；`TestMatchErrorPattern` 全套 34 passed。
- [x] `go test ./...` — 17 个包 1689 passed。

### 任务 3：测试、review notes 与规格勘误

#### 需求

**Objective（目标）** — 用测试锁定新行为，归档审查结论，并在 2026-06-15 规格与 2026-05-15 研究中勘误，让后续读者知道上游行为已变、清洗前提不再成立。

**Outcomes（成果）** — `internal/proxy/rectifier_test.go`（或专门 `_test.go`）新增：`TestProactiveClean_PreservesToolReference`、`TestReactiveClean_StripsToolReference`、以及任务 2 的两个 `TestMatchErrorPattern` 用例。本特性目录新增 review notes。在 `sdd-docs/features/2026-06-15-proactive-content-block-cleanup/spec.md`（+ ZH）与 `sdd-docs/research/2026-05-15-rectifier-pattern3-generic-bad-request.md` 末尾追加带日期的勘误段，指向本特性。

**Evidence（证据）** — `go test ./...` 通过；review notes 引用实测矩阵；2026-06-15 规格与 2026-05-15 研究带有"2026-07-21 勘误"说明。

**Constraints（约束）** — 测试不得嵌入真实 API token，使用合成 fixture。勘误为追加（不重写历史），记录上游行为变更并链接到本特性。

**Edge Cases（边界）** — 确保 `tool_reference` 保留不泄漏到反应式路径测试（两者须分化）；确保主动测试仍剥离真正的未知类型（如 `server_tool_use`），以证明需求 4。

**Verification（验证）** — `go test ./...` 通过；`git diff --stat` 仅涉及 `internal/proxy/rectifier.go`、`internal/proxy/handler.go`（若有改动）、测试文件、review-notes 文件，以及两条带日期的勘误说明。

#### 计划

1. 新增上述四个测试。在主动测试中加入 `tool_reference` 与合成 `server_tool_use` 块混合的用例——断言 `tool_reference` 存活、`server_tool_use` 被剥。
2. 撰写 `sdd-docs/features/2026-07-21-preserve-tool-reference/review-notes.md` 与 `review-notes_ZH.md`，包含：实测矩阵、根因演进摘要、所选策略（主动保留 / 反应式清除）、并明确说明 `StripUnknownContentBlocks` flag 的弃用作为后续任务。
3. 在 `sdd-docs/features/2026-06-15-proactive-content-block-cleanup/spec.md` 与 `spec_ZH.md` 追加 "## 2026-07-21 勘误" 段，并在 `sdd-docs/research/2026-05-15-rectifier-pattern3-generic-bad-request.md` 末尾追加对应说明，各自回链本特性目录，概述 kimi 上游现已接受 `tool_reference`。
4. `go test ./...`；确认通过；仅在本分支提交。

#### 验证

- [x] `go test ./...` 通过 —— 17 个包 1689 passed。
- [x] review notes 齐备：`review-notes.md` + `review-notes_ZH.md`。
- [x] 2026-06-15 规格（EN+ZH）与 2026-05-15 研究附有"2026-07-21 勘误"段并回链此处。
