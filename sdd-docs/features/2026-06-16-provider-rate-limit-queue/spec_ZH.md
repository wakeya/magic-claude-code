# Provider 限流队列规格

本地页面：管理后台 Provider 编辑弹窗 / 用量与请求日志  
代理入口：`internal/proxy/handler.go` ServeHTTP / `tryRectify` 附近的上游请求路径  
参考源站：用户提供的平台限流说明（错误码 1302、1305）  
技术栈：Go 1.26 标准库（`sync`、`time`、`container/list` 或 channel）+ Vue 3 + SQLite 配置存储  
最后更新：2026-06-16  
进度：6 / 6 已完成

## 整体分析（源站分析）

### 问题

高峰期调用国内模型时，上游经常返回 HTTP 429。平台说明将 429 分成两类：

1. **错误码 1302：用户速率限制**  
   原因通常是同一账户在某个模型上的并发请求数达到上限，或短时间内请求过密。建议降低并发、使用请求队列或并发池。

2. **错误码 1305：平台服务过载**  
   原因是模型整体访问压力过高、底层算力高负载、平台维护或扩容。建议稍后重试、增加重试间隔、避免立即高频重试。

当前代理收到请求后会直接发往上游。Claude Code 的流式请求可能持续很久，多个并发会迅速占满账户并发额度。没有本地并发上限时，用户看到的是“请求快速失败 + 自动重试继续冲击上游”，这会放大 429。

### 设计目标

本功能用**可控等待**替代**不可控 429 失败**：

- 对 1302 类型的账户并发限制，优先通过 Provider 级并发池和队列控制进入上游的同时请求数。
- 对 1305 类型的平台过载，使用有限次数的指数退避重试，避免立即高频重试。
- 默认不改变现有 Provider 行为，所有限流能力必须显式启用。
- 让用户能按 Provider 或模型特性调节队列大小、并发数和等待上限。

### 为什么请求会变慢

启用队列后，超过并发上限的请求不会立即发往上游，而是在本地排队。因此排队中的请求开始时间会变晚。这个变慢是有意的：当上游已经不接受更多并发时，“晚一点发出并成功”通常优于“马上发出并 429 失败”。

必须防止无限等待。每个排队请求都要有最大等待时间，队列也要有最大长度。超过上限时，代理应返回明确错误，避免客户端长时间无反馈。

### 当前项目状态

Provider 配置已包含：

- `APIFormat`：区分 Anthropic、OpenAI Chat Completions、OpenAI Responses。
- `ModelMappings`：将 Claude Code 请求模型映射到上游模型。
- `SupportsThinking`、`MultimodalSwitch` 等能力开关。

限流队列应沿用 Provider 能力开关模式，而不是基于模型 ID 或 URL 猜测。模型映射只决定发往上游的 `model` 字段，不适合作为并发策略的唯一依据。

### 策略

采用两层防护：

1. **请求前并发池 + 队列**
   - 按 Provider 生效，后续可扩展到 Provider+Model。
   - 当 Provider 的限流队列开启且 `max_concurrent_requests > 0` 时，请求必须先获取该 Provider 的执行槽。
   - 获取不到执行槽时进入 FIFO 队列。
   - 队列超长或等待超时，返回明确错误，不发送到上游。
   - 流式请求占用执行槽直到响应 body 完全转发结束。

2. **429 退避重试**
   - 仅在 Provider 显式开启 429 重试时生效。
   - 解析上游 429 错误体，识别 `1302` / `1305`。
   - 优先尊重 `Retry-After` 响应头。
   - 无 `Retry-After` 时使用指数退避 + 小抖动。
   - 重试次数有上限，避免对平台过载形成二次冲击。

### 范围

- **纳入范围**：Provider 级队列配置、请求前并发控制、排队超时、429 有限退避重试、管理后台配置、请求日志观测字段、单元测试。
- **排除范围**：跨进程分布式限流；全局多账户调度；Batch API；异步任务系统；自动根据 429 动态调整并发额度。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成 | Provider 限流配置模型 | Provider 字段、SQLite schema、管理 API | 配置 round-trip 测试 |
| 2 | 已完成 | Provider 并发池与队列 | `internal/proxy/ratelimit` 或同等内部组件 | 并发、排队、超时单元测试 |
| 3 | 已完成 | 代理请求路径接线 | ServeHTTP 上游请求前后 acquire/release | 流式请求 release 测试 |
| 4 | 已完成 | 429 退避重试 | 1302/1305 识别、Retry-After、指数退避 | mock 上游 429 重试测试 |
| 5 | 已完成 | 管理后台配置 UI | ProviderModal、useApi、i18n | 前端构建与交互检查 |
| 6 | 已完成 | 观测与安全验证 | 请求日志、队列拒绝错误、测试记录 | `go test ./...`、前端构建 |

## 需求

### 交付物

1. Provider 新增限流配置字段：
   - `rate_limit_queue_enabled`：是否启用本地队列。
   - `max_concurrent_requests`：同一 Provider 同时发往上游的最大请求数；`0` 表示不限流。
   - `max_queue_size`：最多等待中的请求数；`0` 表示不允许排队。
   - `queue_timeout_ms`：单个请求最长排队时间。
   - `retry_429_enabled`：是否启用 429 退避重试。
   - `retry_429_max_attempts`：最多重试次数，不包含首次请求。
   - `retry_429_initial_delay_ms`：无 `Retry-After` 时的初始等待。
   - `retry_429_max_delay_ms`：无 `Retry-After` 时的最大等待。

2. 管理后台 Provider 创建、编辑、复制、列表、详情接口完整保存并返回这些字段。

3. SQLite 配置存储完整持久化这些字段，并为旧数据库添加默认值迁移。

4. 请求发往上游前：
   - 如果 Provider 未启用队列或 `max_concurrent_requests <= 0`，保持现有行为。
   - 如果启用队列，请求必须先获取执行槽。
   - 队列满时返回 HTTP 429 或 503，错误消息说明本地队列已满。
   - 排队超时时返回 HTTP 504 或 429，错误消息说明等待上游并发槽超时。

5. 流式请求的执行槽必须在响应流完全结束后释放，不能在响应头返回后提前释放。

6. 非流式请求的执行槽在上游响应体读取并转发完成后释放。

7. 429 退避重试只对上游 429 生效，不对本地队列满/排队超时错误重试。

8. 429 重试必须保留现有 rectifier 400 恢复逻辑，不得让 429 逻辑吞掉 400 兼容性修复。

9. 请求日志和用量记录应能区分：
   - 正常上游请求。
   - 本地队列等待。
   - 本地队列满拒绝。
   - 排队超时。
   - 上游 429 重试后成功或失败。

10. 默认值必须保持兼容：所有现有 Provider 在升级后不开启本地队列，不改变当前请求行为。

### 数据模型

```go
type Provider struct {
    RateLimitQueueEnabled bool `json:"rate_limit_queue_enabled"`
    MaxConcurrentRequests int  `json:"max_concurrent_requests"`
    MaxQueueSize          int  `json:"max_queue_size"`
    QueueTimeoutMS        int  `json:"queue_timeout_ms"`
    Retry429Enabled       bool `json:"retry_429_enabled"`
    Retry429MaxAttempts   int  `json:"retry_429_max_attempts"`
    Retry429InitialDelayMS int `json:"retry_429_initial_delay_ms"`
    Retry429MaxDelayMS    int  `json:"retry_429_max_delay_ms"`
}
```

建议默认值：

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `rate_limit_queue_enabled` | `false` | 升级后不改变行为 |
| `max_concurrent_requests` | `0` | 不限制并发 |
| `max_queue_size` | `0` | 不排队 |
| `queue_timeout_ms` | `60000` | 显式启用后默认最多等 60 秒 |
| `retry_429_enabled` | `false` | 默认不重试，避免隐藏上游错误 |
| `retry_429_max_attempts` | `2` | 最多额外尝试两次 |
| `retry_429_initial_delay_ms` | `1000` | 初始等待 1 秒 |
| `retry_429_max_delay_ms` | `10000` | 单次等待最多 10 秒 |

### 队列语义

1. 队列粒度第一期为 Provider 级别。Provider+Model 粒度作为后续扩展，不在本期实现。
2. FIFO 排队，避免后来的请求插队。
3. 如果请求上下文取消（客户端断开、服务关闭），必须从队列中移除，不得泄漏等待项。
4. 队列长度只统计等待中的请求，不包含正在执行的请求。
5. 正在执行的请求数不能超过 `max_concurrent_requests`。
6. 管理后台修改 Provider 配置后，新请求应使用新配置；已在执行或排队的请求不强制迁移。

### 429 重试语义

1. 只处理上游返回的 HTTP 429。
2. 如果响应头有 `Retry-After`：
   - 秒数格式按秒等待。
   - HTTP 日期格式按目标时间计算等待。
   - 等待超过 `retry_429_max_delay_ms` 时截断到最大值。
3. 如果没有 `Retry-After`，使用指数退避：`initial * 2^(attempt-1)`，并加 0-250ms 抖动。
4. `1302` 可重试，但更主要依赖并发池降低触发概率。
5. `1305` 可重试，但必须使用更保守的退避；不能立即重试。
6. 重试前必须完整关闭前一次响应体，避免连接泄漏。
7. 重试使用同一个 Provider，不触发 Provider 切换。

### 管理后台 UI

Provider 编辑弹窗新增“限流与重试”区域。该区域默认折叠，避免普通 Provider 配置被高级选项打扰。展开后优先显示两个主开关：

- 启用请求队列：checkbox。
- 启用 429 退避重试：checkbox。

只有勾选“启用请求队列”后，才展开请求队列参数：

- 最大并发请求数：数字输入。
- 最大排队请求数：数字输入。
- 排队超时：数字输入，单位秒或毫秒，UI 文案必须清楚。

只有勾选“启用 429 退避重试”后，才展开重试参数：

- 最大重试次数：数字输入。
- 初始重试间隔：数字输入。
- 最大重试间隔：数字输入。

字段校验：

- 数值不得为负数。
- 启用队列时，`max_concurrent_requests` 必须大于 0。
- 启用队列且允许排队时，`max_queue_size` 必须大于 0。
- 启用 429 重试时，`retry_429_max_attempts` 必须大于 0。

### 安全与稳定性约束

1. 队列不能无限增长，必须有长度上限。
2. 排队不能无限等待，必须有超时。
3. 请求上下文取消时必须释放执行槽或移除等待项。
4. 不得在日志中输出 API Token、Authorization、请求正文全文或用户敏感数据。
5. 重试不能绕过现有请求大小限制、header 过滤、模型映射、格式转换和 400 rectifier。
6. 实现不得引入全局锁长时间持有。锁只保护队列状态，不包裹上游网络请求。
7. 流式响应必须正确释放执行槽，避免一次异常断流永久占用并发槽。

### 边界情况

1. 队列关闭：行为完全等同当前版本。
2. `max_concurrent_requests=1`：同一 Provider 严格串行。
3. `max_queue_size=0`：并发满时立即拒绝，不排队。
4. 客户端在排队中断开：等待项被取消，不占队列。
5. 客户端在流式响应中断开：执行槽释放。
6. 上游连续返回 1305：按退避重试到上限后返回最后一次错误。
7. 上游返回 429 但 body 非 JSON：仍按通用 429 退避策略处理。
8. 上游返回 1302 但没有开启队列：只按重试设置处理，不自动启用队列。
9. Provider 被禁用或切换：已有请求按进入时选中的 Provider 完成，新请求使用新配置。
10. 管理后台配置非法值：保存接口返回 400，不写入配置。

### 非目标

1. 不实现跨进程或跨机器共享队列。
2. 不实现自动探测账户并发额度。
3. 不实现基于模型 ID 的自动限流规则。
4. 不实现 Batch API 或异步任务系统。
5. 不实现请求优先级或手动取消队列项 UI。
6. 不自动降级到其他模型；降级策略可作为后续独立功能。

## 任务详情

### 任务 1：Provider 限流配置模型

#### 需求

**Objective（目标）** — 为 Provider 增加本地队列和 429 重试配置，并在所有配置通道中持久化。

**Outcomes（成果）** — Provider struct、SQLite schema、JSON store、管理 API create/update/list/detail/duplicate、前端类型均支持新增字段。

**Evidence（证据）** — 单元测试覆盖创建、更新、复制、SQLite 保存/加载和旧库迁移默认值。

**Constraints（约束）** — 默认不开启队列和重试，升级后不改变现有 Provider 行为。

**Edge Cases（边界）** — 旧 SQLite 数据库缺少列；JSON 配置缺少字段；非法负数配置。

**Verification（验证）** — `go test ./internal/config ./internal/admin -run RateLimit -v`

#### 计划

1. 在 `internal/config/provider.go` 添加限流字段与校验。
2. 在 SQLite `providers` 表添加列和 `ensureProviderColumns` 迁移。
3. 更新 load/save SQL。
4. 更新管理 API 请求/响应结构。
5. 更新前端 `Provider` 类型和保存 payload。
6. 添加配置 round-trip 测试。

#### 验证

- [x] 旧数据库自动补列。
- [x] 管理后台创建 Provider 后字段可返回。
- [x] 更新 Provider 后字段持久化。
- [x] 复制 Provider 时保留限流配置。

### 任务 2：Provider 并发池与队列

#### 需求

**Objective（目标）** — 实现 Provider 级并发槽和 FIFO 等待队列，控制同时发往上游的请求数。

**Outcomes（成果）** — 代理可以在本地限制 Provider 并发，超过并发的请求按配置排队、超时或拒绝。

**Evidence（证据）** — 并发测试证明正在执行请求数不超过上限；队列满和超时返回预期错误。

**Constraints（约束）** — 不得在持锁期间执行上游网络请求；客户端取消必须清理等待项。

**Edge Cases（边界）** — 串行并发、队列大小为 0、排队中取消、执行中取消。

**Verification（验证）** — `go test ./internal/proxy/... -run RateLimitQueue -v`

#### 计划

1. 新增内部队列组件，按 Provider ID 管理状态。
2. 提供 `Acquire(ctx, provider)` 和 `Release()` 接口。
3. 对队列满、排队超时、上下文取消返回可区分错误。
4. 编写并发、FIFO、取消、超时测试。

#### 验证

- [x] 并发上限稳定生效。
- [x] 队列 FIFO 顺序稳定。
- [x] 等待超时不泄漏槽位或队列项。
- [x] 客户端取消不泄漏槽位或队列项。

### 任务 3：代理请求路径接线

#### 需求

**Objective（目标）** — 在上游请求发送前获取执行槽，并在响应完全处理后释放。

**Outcomes（成果）** — 非流式和流式请求都正确占用和释放 Provider 并发槽。

**Evidence（证据）** — mock 上游测试显示长流式请求会占用槽位直到流结束。

**Constraints（约束）** — 必须保持现有模型映射、格式转换、header 过滤、usage 记录和 400 rectifier 行为。

**Edge Cases（边界）** — 上游连接失败、响应构造失败、流式响应中途客户端断开、rectifier 重试。

**Verification（验证）** — `go test ./internal/proxy/... -run ProxyRateLimit -v`

#### 计划

1. 在 `Handler` 中注入或创建 Provider 队列管理器。
2. 在构造上游请求前获取执行槽。
3. 非流式路径使用 `defer release()`。
4. 流式路径包装 response body，在 `Close` 或 EOF 时释放。
5. 确认 400 rectifier 重试不重复占用额外槽，或明确以同一槽内重试。

#### 验证

- [x] 非流式成功、失败都释放。
- [x] 流式 EOF 后释放。
- [x] 客户端中断后释放。
- [x] 400 rectifier 仍工作。

### 任务 4：429 退避重试

#### 需求

**Objective（目标）** — 对上游 429 执行有限退避重试，降低高峰期瞬时失败率。

**Outcomes（成果）** — 代理能识别 1302/1305，按 `Retry-After` 或指数退避等待后重试。

**Evidence（证据）** — mock 上游先返回 429 后返回 200，代理最终成功；连续 429 到上限后返回最后错误。

**Constraints（约束）** — 不重试本地队列错误；不影响 HTTP 400 rectifier；不无限重试。

**Edge Cases（边界）** — 非 JSON 429、缺少错误码、Retry-After 秒数/日期格式、客户端取消。

**Verification（验证）** — `go test ./internal/proxy/... -run Retry429 -v`

#### 计划

1. 添加 429 错误解析函数，提取错误码和消息。
2. 添加 Retry-After 解析函数。
3. 实现指数退避 + 抖动。
4. 在上游请求路径中对 429 调用重试逻辑。
5. 确保每次重试前关闭响应体。

#### 验证

- [x] 1302 按配置重试。
- [x] 1305 按退避重试。
- [x] Retry-After 优先级高于本地计算。
- [x] 达到最大次数后停止。

### 任务 5：管理后台配置 UI

#### 需求

**Objective（目标）** — 让用户能在 Provider 编辑弹窗配置队列和 429 重试参数。

**Outcomes（成果）** — 管理后台显示“限流与重试”区域，保存后配置生效。

**Evidence（证据）** — 前端构建通过；手动创建/编辑 Provider 后 API 返回正确字段。

**Constraints（约束）** — “限流与重试”区域默认折叠；展开后只显示两个主开关；队列参数仅在“启用请求队列”勾选后显示；重试参数仅在“启用 429 退避重试”勾选后显示；所有字段有清晰单位。

**Edge Cases（边界）** — 负数、空值、启用但缺少并发数、编辑旧 Provider。

**Verification（验证）** — `npm`/前端构建命令按项目现有脚本执行。

#### 计划

1. 更新 `useApi.ts` Provider 类型。
2. 更新 `ProviderModal.vue` 表单、初始化和保存 payload。
3. 更新 `useI18n.ts` 中英文文案。
4. 添加前端表单校验。

#### 验证

- [x] 新建 Provider 能保存限流字段。
- [x] 编辑 Provider 能回显限流字段。
- [x] 默认只显示折叠入口或主开关，不直接展示所有高级数字字段。
- [x] 勾选“启用请求队列”后才显示队列数字字段。
- [x] 勾选“启用 429 退避重试”后才显示重试数字字段。
- [x] 非法配置被前端或后端阻止。
- [x] 前端构建通过。

### 任务 6：观测与安全验证

#### 需求

**Objective（目标）** — 确保限流队列可观测、可排障，且不会引入资源泄漏或敏感信息泄露。

**Outcomes（成果）** — 日志能说明排队、拒绝、超时和重试；测试证明无槽位泄漏。

**Evidence（证据）** — 自动化测试通过；手动 mock 场景日志可读。

**Constraints（约束）** — 不记录敏感请求体和 token；日志量不能随排队等待频繁刷屏。

**Edge Cases（边界）** — 高并发压测、长流式请求、客户端断开、上游连续 429。

**Verification（验证）** — `go test ./...`；必要时使用 race 测试检查队列组件。

#### 计划

1. 为本地队列拒绝和超时设置清晰错误类型。
2. 在请求日志中添加排队耗时和 429 重试次数。
3. 添加资源释放测试。
4. 执行全量 Go 测试。

#### 验证

- [x] `go test ./...` 通过。
- [x] 无敏感信息日志。
- [x] 队列满、超时、重试失败都能定位原因。
