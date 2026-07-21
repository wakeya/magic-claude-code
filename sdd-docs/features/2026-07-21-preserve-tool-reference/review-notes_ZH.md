# 审查归档：主动清洗保留 tool_reference

**日期：** 2026-07-21
**分支：** `fix/preserve-tool-reference`
**提交：** `7d8a8ca`（规格）、`13bef71`（任务 1）、`c099e4d`（任务 2）
**状态：** 已验证 —— `go test ./...` 全绿

## 结论

主动内容块清洗（`proactiveCleanUnknownContentTypes`，由 `StripUnknownContentBlocks` 控制）原先剥离 `tool_reference` 块。这在 **2026-05 的 kimi 上游**是正确的（当时上游以 `"unsupported content type"` 拒绝该类型），但对**当前上游**有害——现行上游已接受 `tool_reference`，且 Claude Code 2.1.x 依赖它作为 deferred 工具加载标记。清洗生效时，kimi-k3 退化：工具调用坍缩为固定占位 `Bash{command:"true",description:"noop"}`，`thinking` 为空。

修复：参数化 `filterContentBlocks(preserveToolReference)`。主动清洗传 `true`（保留 `tool_reference`，恢复模型上下文）；反应式（`tryRectify` → `cleanUnknownContentTypes`）传 `false`（保留清除能力，兜底 `tool_reference` 指向未定义工具的 400）。反应式错误模式匹配顺带扩展，使当前 kimi 400 串仍能触发清洗。

## 根因演进

| 阶段 | 来源 | 结论 | 有效 |
|------|------|------|:----:|
| 2026-05-15 | `research/2026-05-15-...` | kimi-k2.6 以 "unsupported content type" 400 拒绝 tool_reference | 当时 |
| 2026-06-15 | `features/2026-06-15-...` | 主动清洗由 `StripUnknownContentBlocks` 控制；白名单不含 tool_reference | 当时 |
| 2026-07-21 | 真实探针（本次审查） | 三个 kimi 端点在 tool_reference 引用已定义工具时**接受**它；仅当引用未定义工具时才 400 | 现在 |

最初诊断混淆了两个变量：把 400 归因于 tool_reference 类型本身，而真正原因是引用的工具未定义。这在 2026-05 无害（无论原因，剥离都能修 400），但在上游开始接受该类型后变成纯副作用。

## 实测证据（2026-07-21 真实探针）

最小 Anthropic `/v1/messages`、非流式、一次 `tool_use`→`tool_result` 往返。`tools` 定义 `WebSearch`。"ref" 变体的 `tool_reference` 指向 `WebSearch`（已定义）或 `ToolSearch`（未定义）。

| 端点 / 模型 | ref→已定义 | ref→未定义 | 无 ref |
|---|---|---|---|
| `api.moonshot.cn/anthropic` kimi-k2.6 | 200 | 400 `Tool reference 'ToolSearch' not found in available tools` | 200 |
| `api.kimi.com/coding` kimi-for-coding（k2.7） | 200 | 400 `Invalid request Error` | 200 |
| `api.kimi.com/coding` k3 | 200 | 400 `Invalid request Error` | 200 |

三个端点都接受 tool_reference 类型。400 源于引用未定义工具，而非类型本身。

## 改动

1. `internal/proxy/rectifier.go`
   - `filterContentBlocks(msg, preserveToolReference bool)`：`preserveToolReference && btype == "tool_reference"` 时保留该块；其他逻辑逐字节不变。递归调用透传该标志。
   - `cleanUnknownContentTypes` 以 `filterContentBlocks(msg, false)` 调用（反应式 —— 行为不变）。
   - `isUnsupportedContentTypePhrase` 新增 `"tool reference"`，匹配现行 moonshot 400 串。
2. `internal/proxy/handler.go`
   - `proactiveCleanUnknownContentTypes` 以 `filterContentBlocks(msg, true)` 调用（主动 —— 现保留 tool_reference）。
3. 测试（`internal/proxy/rectifier_test.go`、`server_test.go`）：
   - `TestProactiveClean_PreservesToolReference`：`tool_reference` + 合成 `server_tool_use` + `text`；保留 `tool_reference` 与 `text`，剥除 `server_tool_use`。
   - `TestReactiveClean_StripsToolReference`：反应式路径仍清除 tool_reference。
   - `TestMatchErrorPattern_KimiCurrentErrors`：coding "Invalid request Error" + moonshot "Tool reference not found"。
   - 既有 `TestProactiveClean_AnthropicStripEnabled_*` 从 `Removes` 改为 `Preserves`。

## 验证

- `go test ./internal/proxy/...` — 571 passed。
- `go test ./...` — 17 个包 1689 passed。
- 旧 `"unsupported content type"` rectifier 测试仍通过（向后兼容）。

## 残余 / 后续

- 本次修复后 `StripUnknownContentBlocks` provider flag 及前端 checkbox 变为 **no-op**（实践中唯一出现过的非标准类型就是 tool_reference，现已保留）。弃用该 flag（DB 列保留读兼容，admin API + 前端移除）作为**独立后续特性**，本次刻意不做，以保证修复可审、可回滚。
- 2026-06-15 规格与 2026-05-15 研究附有带日期的勘误，回链此处。
