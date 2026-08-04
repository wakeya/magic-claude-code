# DeepSeek 缓存 Usage 统计解析修复规格

本地页面：`internal/proxy/handler.go`（上游响应转换入口）、`internal/proxy/transform/openai_chat.go`、`internal/proxy/transform/openai_responses.go`
代理入口：`internal/proxy/handler.go` 的 `ServeHTTP`、上游 SSE/非流式响应转换路径
参考源站：[DeepSeek KV Cache](https://api-docs.deepseek.com/guides/kv_cache)、[DeepSeek Chat Completion API](https://api-docs.deepseek.com/zh-cn/api/create-chat-completion/)、pi 的 `packages/ai/src/api/openai-completions.ts`
技术栈：Go 1.26 标准库、现有 `internal/proxy/transform` 测试体系
最后更新：2026-08-04
进度：2 / 2 计划任务（实现完成，测试通过，规格已回写）
状态：implemented

## 整体分析（源站分析）

### 问题

DeepSeek 的 OpenAI 兼容接口在 usage 中使用 `prompt_cache_hit_tokens` 与 `prompt_cache_miss_tokens` 表示缓存命中和未命中的输入 token。MCC 将 OpenAI Chat Completions 或 Responses 响应转换成 Anthropic 响应后，Claude Code 和 MCC 的 usage 统计依赖转换后的 `cache_read_input_tokens` 与 `input_tokens`。

当前 Chat Completions 转换函数 `openAIUsageToAnthropic` 只读取 `prompt_tokens_details.cached_tokens`，没有读取 DeepSeek 的 `prompt_cache_hit_tokens`。当前 Responses 转换函数 `responsesUsageToAnthropic` 只复制 `cached_tokens`，同样没有 DeepSeek 字段的兼容回退。结果可能是上游真实命中缓存，但 MCC 返回 `cache_read_input_tokens = 0`，并把全部 prompt token 计入未缓存输入。

pi 已经兼容 DeepSeek 的 `prompt_cache_hit_tokens`，因此会出现 pi 显示高命中，而同一个上游响应经 MCC 转换后显示低命中的现象。该问题是 usage 统计映射错误，不等价于上游 KV cache 实际失效。

### 现有代码证据

- `openAIUsageToAnthropic` 从 `prompt_tokens_details.cached_tokens` 计算 `cache_read_input_tokens`，再从 `prompt_tokens` 扣除命中和写入 token；没有 `prompt_cache_hit_tokens` 回退。
- `responsesUsageToAnthropic` 只复制 `input_tokens`、`output_tokens` 和 `cached_tokens`，没有 DeepSeek 命中字段回退，也没有把缓存命中 token 从未缓存输入中扣除。
- 现有 Chat 普通响应与 SSE 测试只覆盖 `prompt_tokens_details.cached_tokens`，没有 DeepSeek 的命中/未命中字段测试。
- 本修复不改变请求协议转换，也不承诺不同 endpoint、模型路由或请求前缀之间共享缓存；它只保证 MCC 正确呈现上游已经返回的缓存 usage。

### 目标行为

对缓存读取 token 采用以下优先级，避免重复计数：

1. Chat Completions 优先使用 `prompt_tokens_details.cached_tokens`；若该字段缺失或不可解析，再使用 `prompt_cache_hit_tokens`。
2. Responses 保留当前已支持的 `cached_tokens` 表达；若缺失，再使用 `prompt_cache_hit_tokens`，并兼容与 Chat 相同的 `prompt_tokens_details.cached_tokens` 嵌套表达。
3. 明确存在且值为 `0` 的字段视为有效值，不再被后备字段覆盖。
4. 未缓存输入 token 按总输入 token减去缓存读取 token和缓存写入 token计算；若总输入字段缺失但同时存在 hit/miss 字段，则使用 `prompt_cache_miss_tokens` 作为未缓存输入 token。
5. 结果继续映射为 Anthropic usage：`cache_read_input_tokens` 表示命中 token，`input_tokens` 表示未缓存输入 token，`output_tokens` 保持现有映射。

### 范围

- **在范围内**：OpenAI Chat Completions 和 OpenAI Responses 的普通响应、SSE usage 事件；DeepSeek hit/miss 字段解析；缓存字段优先级；输入 token 计算；合成单元测试；回归验证。
- **不在范围内**：修改 Anthropic→OpenAI 请求转换、保留 `cache_control`、增加 `prompt_cache_key`、session affinity、缓存 endpoint 路由、模型映射、缓存 TTL、数据库 schema 或前端命中率展示算法。

## 关联问题/后续特性

### 协议转换导致的真实缓存下降（独立后续特性，不在本分支实现）

- MCC 将 Anthropic Messages 请求转换为 OpenAI Chat Completions / Responses 请求，不保留 Anthropic 的 `cache_control` 标记，而 DeepSeek 的 KV 缓存以精确 prompt 前缀为 key。本解析修复完成后，pi 与 MCC 对上游返回的 usage 将保持一致；若命中率仍然偏低，则是请求协议转换导致的真实上游缓存下降（缺少 `cache_control`、prompt 结构变化、无 session affinity），并非 usage 统计解析缺陷。
- 修复该下降（如保留 `cache_control`、增加 prompt cache key、session affinity 或缓存感知的 endpoint 路由）属于独立后续特性，需要单独设计，明确不在本分支（`fix/deepseek-cache-usage-parsing`）实现。本分支只修复上游已返回缓存 usage 的解析与转换。

## 开发检查清单

- [x] 为 Chat Completions 普通响应和 SSE 响应增加 `prompt_cache_hit_tokens` 回退测试。
- [x] 为 Responses 普通响应和 SSE 响应增加 `prompt_cache_hit_tokens` 回退测试。
- [x] 覆盖 `cached_tokens` 与 `prompt_cache_hit_tokens` 同时存在时的优先级，以及显式零值。
- [x] 覆盖 `prompt_cache_miss_tokens` 用于未缓存输入计算，以及无缓存字段时的旧行为。
- [x] 运行 `go test ./internal/proxy/transform -count=1`。
- [x] 运行 `go test ./...`。
- [x] 实现后回写本文件和 `spec.md` 的进度、检查清单和验证证据。

## 需求

### 需求 1：Chat Completions 识别 DeepSeek 缓存命中

`openAIUsageToAnthropic` 必须在没有有效 `prompt_tokens_details.cached_tokens` 时读取 `prompt_cache_hit_tokens`，并将其映射为 `cache_read_input_tokens`。

### 需求 2：Responses 识别 DeepSeek 缓存命中

`responsesUsageToAnthropic` 必须在没有有效既有缓存字段时读取 `prompt_cache_hit_tokens`，并将缓存读取 token 从未缓存输入 token 中扣除。

### 需求 3：正确计算未缓存输入

当上游返回总输入 token、缓存命中 token和缓存写入 token时：

```text
input_tokens = max(total_input_tokens - cache_read_input_tokens - cache_creation_input_tokens, 0)
```

当总输入 token缺失但返回 `prompt_cache_hit_tokens` 与 `prompt_cache_miss_tokens` 时，`input_tokens` 使用 `prompt_cache_miss_tokens`。不得把 hit token重复计入 `input_tokens`。

### 需求 4：兼容既有 usage 格式

现有 `prompt_tokens_details.cached_tokens`、Responses `cached_tokens`、`cache_read_input_tokens`、`cache_creation_input_tokens` 和没有任何缓存字段的响应必须继续得到正确结果。兼容逻辑不得改变正常 `output_tokens` 与错误响应处理。

### 需求 5：流式与非流式结果一致

同一份 usage 数据通过非流式转换和 SSE 最终 usage 事件转换后，必须产生相同的 Anthropic 缓存字段和输入 token数。

### 需求 6：不伪造真实缓存命中

只有上游明确返回有效缓存字段时才设置 `cache_read_input_tokens`。字段缺失、类型无法解析或值为负数时，不得凭请求长度推断缓存命中。

## 任务详情

### 任务 1：统一缓存 Usage 解析并修复 Chat Completions

#### 需求

**Objective（目标）** — 增加可复用的缓存 usage 解析逻辑，优先保持现有标准字段语义，并让 Chat Completions 普通/SSE 转换识别 DeepSeek 的 `prompt_cache_hit_tokens` 与 `prompt_cache_miss_tokens`。

**Outcomes（成果）** — `internal/proxy/transform/openai_chat.go` 能输出正确的 `cache_read_input_tokens` 与 `input_tokens`；`internal/proxy/transform/openai_chat_test.go` 包含标准字段、DeepSeek 字段、字段优先级、显式零值、缺失总输入和流式场景。

**Evidence（证据）** — 合成响应 `prompt_tokens=1000`、`prompt_cache_hit_tokens=600`、`prompt_cache_miss_tokens=400` 转换后得到 `cache_read_input_tokens=600`、`input_tokens=400`；标准 `cached_tokens=300` 与 hit 字段同时存在时使用 300；显式 `cached_tokens=0` 时不回退到 600。

**Constraints（约束）** — 不改变 Anthropic usage 字段名；不把 hit token同时放入 `input_tokens`；不通过输入长度猜测命中；解析失败按字段缺失处理；保持现有缓存写入 token和输出 token映射。

**Edge Cases（边界）** — `prompt_tokens_details` 缺失；`cached_tokens` 为 0；hit/miss 字段只有一项；总输入 token 小于 hit + write；缓存字段为字符串或非数字值；普通响应和 SSE usage 数据在不同事件中到达。

**Verification（验证）** — 运行 Chat 转换相关测试，确认标准格式旧测试不变，并确认 DeepSeek fixture 的未缓存输入为 400、缓存读取为 600。

#### 计划

1. 在 `internal/proxy/transform/openai_chat_test.go` 先新增失败测试：
   - `TestOpenAIChatToAnthropicUsesPromptCacheHitTokens`：普通响应只提供 `prompt_cache_hit_tokens=600` 与 `prompt_cache_miss_tokens=400`，断言 Anthropic usage 为 `input_tokens=400`、`cache_read_input_tokens=600`。
   - `TestOpenAIChatToAnthropicPrefersCachedTokens`：同时提供 `prompt_tokens_details.cached_tokens=300` 与 `prompt_cache_hit_tokens=600`，断言使用 300。
   - `TestOpenAIChatToAnthropicPreservesExplicitZeroCache`：`cached_tokens=0` 与 hit=600 同时存在，断言命中为 0。
   - `TestOpenAIChatSSEToAnthropicUsesPromptCacheHitTokens`：SSE usage 事件包含同样的 hit/miss 字段，断言最终 `message_delta.usage` 与普通响应一致。
2. 运行 `go test ./internal/proxy/transform -run 'TestOpenAIChat.*PromptCache|TestOpenAIChatSSEToAnthropicUsesPromptCacheHitTokens' -count=1`，确认新增测试在实现前失败。
3. 在 `internal/proxy/transform` 增加共享的缓存 usage 解析辅助逻辑，至少提供以下明确行为：
   - 按“有效且存在的标准缓存字段 → DeepSeek hit 字段”的顺序选择 cache read；显式零值保留。
   - 从 `prompt_tokens` 或等价总输入字段扣除 cache read 与 cache creation；总输入缺失时使用 `prompt_cache_miss_tokens`。
   - 负值和不可解析值按缺失处理，最终输入 token不低于 0。
4. 让 `openAIUsageToAnthropic` 使用该辅助逻辑，保留当前 `prompt_tokens_details.cached_tokens`、`cache_read_input_tokens` 和 `cache_creation_input_tokens` 兼容行为。
5. 运行上述定向测试，确认新增用例通过。
6. 提交本任务的实现与测试，提交信息使用 `fix: parse DeepSeek cache usage`。任务 1 与任务 2 合并为同一次提交完成（见下文“实现记录”）。

#### 验证

- [x] 新增测试先失败，证明现有实现确实遗漏 DeepSeek 字段。
- [x] Chat 普通响应和 SSE 测试通过。
- [x] 既有 `openai_chat_test.go` usage 测试通过。
- [x] `go test ./internal/proxy/transform -count=1` 通过。

### 任务 2：修复 Responses usage、回归验证并回写规格

#### 需求

**Objective（目标）** — 让 OpenAI Responses 普通响应和 SSE 响应使用同一缓存字段优先级，确认无缓存和旧格式行为不变，并完成全量验证。

**Outcomes（成果）** — `internal/proxy/transform/openai_responses.go` 能识别现有 `cached_tokens` 与 DeepSeek hit 字段；普通和流式 Responses usage 与 Chat usage语义一致；双语 spec 的进度和验证证据完成回写。

**Evidence（证据）** — Responses fixture `input_tokens=1000`、`prompt_cache_hit_tokens=600`、`prompt_cache_miss_tokens=400` 转换后得到 `input_tokens=400`、`cache_read_input_tokens=600`；无缓存字段的现有 fixture 仍得到原始输入 token；Chat/Responses 流式和非流式结果一致。

**Constraints（约束）** — 不修改 Responses 请求体转换；不改变非缓存响应的现有结果；不新增数据库字段；所有测试使用合成数据，不使用真实 token或网络请求。

**Edge Cases（边界）** — Responses 使用 `cached_tokens`；Responses 使用 `prompt_tokens_details.cached_tokens`；hit/miss 同时缺失；总输入和 miss 同时存在时优先按总输入扣除；输入 token扣除后小于 0 时钳制为 0。

**Verification（验证）** — 运行 Responses 定向测试、整个 transform 包测试和 `go test ./...`；确认工作树 diff 只包含转换实现、相关测试和本功能双语 spec。

#### 计划

1. 在 `internal/proxy/transform/openai_responses_test.go` 先新增失败测试：
   - `TestOpenAIResponsesToAnthropicUsesPromptCacheHitTokens`：非流式 usage 包含总输入、hit、miss，断言输入和缓存读取正确。
   - `TestOpenAIResponsesSSEToAnthropicUsesPromptCacheHitTokens`：`response.completed` usage 包含 hit/miss，断言 `message_delta.usage` 正确。
   - `TestOpenAIResponsesToAnthropicKeepsUncachedUsageWithoutCacheFields`：无缓存字段时断言既有 `input_tokens` 和 `output_tokens` 保持不变。
2. 运行 `go test ./internal/proxy/transform -run 'TestOpenAIResponses.*PromptCache|TestOpenAIResponses.*UncachedUsage' -count=1`，确认新增测试在实现前失败。
3. 修改 `responsesUsageToAnthropic` 使用任务 1 的缓存 usage 辅助逻辑：保留已支持的 `cached_tokens`，缺失时回退到 `prompt_tokens_details.cached_tokens` 和 `prompt_cache_hit_tokens`，并把命中/写入 token从未缓存输入中扣除；无总输入时使用 `prompt_cache_miss_tokens`。
4. 运行 Responses 定向测试，随后运行：
   ```bash
   go test ./internal/proxy/transform -count=1
   go test ./...
   git diff --check
   git status --short
   ```
   预期：transform 测试通过、全量 Go 测试通过、`git diff --check` 无输出，未出现无关文件。
5. 在 `spec.md` 与本文件回写实际测试命令、结果、完成任务和提交信息；提交信息使用 `test: cover DeepSeek cache usage mapping` 或与实际提交保持一致。实际提交为单次提交 `fix: parse DeepSeek cache usage`（见下文“实现记录”）。

#### 验证

- [x] Responses 普通响应和 SSE 测试先失败后通过。
- [x] `go test ./internal/proxy/transform -count=1` 通过。
- [x] `go test ./...` 通过。
- [x] `git diff --check` 通过，且 diff 不包含请求转换、数据库或前端改动。
- [x] 实现完成后回写 `spec.md` 与 `spec_ZH.md` 的进度、检查清单和实际验证证据。

## 实现记录（2026-08-04）

分支：`fix/deepseek-cache-usage-parsing`；单次提交 `fix: parse DeepSeek cache usage`。

变更文件：
- `internal/proxy/transform/usage.go`（新增）：共享 `normalizeOpenAIUsage` 辅助函数，实现缓存字段优先级（标准字段优先、DeepSeek `prompt_cache_hit_tokens` 回退、显式零值保留）、输入 token 计算（`total - cache_read - cache_creation`、`prompt_cache_miss_tokens` 回退、钳制为 0）以及有效性处理（负数/不可解析值按缺失处理）。
- `internal/proxy/transform/usage_test.go`（新增）：辅助函数级测试，覆盖无总输入时的 miss 回退、钳制为 0、不可解析/负值缓存字段、缓存写入扣除。
- `internal/proxy/transform/openai_chat.go`：`openAIUsageToAnthropic` 改用辅助函数，路径为 `prompt_tokens_details.cached_tokens` → `cache_read_input_tokens` → `prompt_cache_hit_tokens`。
- `internal/proxy/transform/openai_chat_test.go`：DeepSeek hit/miss 普通响应与 SSE 测试、标准字段优先级、显式零值。
- `internal/proxy/transform/openai_responses.go`：`responsesUsageToAnthropic` 改用辅助函数，路径为 `cached_tokens` / 嵌套 cached-token 字段 → `prompt_cache_hit_tokens`。
- `internal/proxy/transform/openai_responses_test.go`：DeepSeek hit/miss 普通响应与 SSE 测试、无缓存字段行为保持、优先级、显式零值。
- `sdd-docs/features/2026-08-03-deepseek-cache-usage-parsing/spec.md` 与本文件：本记录。

命令与结果：
- `go test ./internal/proxy/transform -count=1` → `ok`
- `go test ./...` → `ok`
- `git diff --check` → 无输出
- `git status --short` → 仅包含上述文件

任务 1 与任务 2 合并为上述单次提交完成。
