# 请求日志增强规格

**版本**: 1.1
**日期**: 2026-05-20
**状态**: 已完成

---

## 1. 背景

代理运行时，Docker 日志中出现大量重复的模型映射信息：

```
Model mapping: claude-opus-4-6 -> glm-5.1 (provider: Zhipu GLM 5.1 chanhu)
Model mapping: claude-opus-4-6 -> glm-5.1 (provider: Zhipu GLM 5.1 chanhu)
...（重复 70+ 次）
```

现有日志的问题：

1. **信息不足**：无法区分请求类型（聊天 vs 工具调用 vs 子 agent）
2. **缺少上下文**：看不到请求路径、消息数量、工具数量、请求体大小
3. **重复输出**：模型映射信息在 `transformRequest` 中打印，但入口处没有请求级别的汇总

### 设计决策

在 `ServeHTTP` 入口处统一打印请求摘要，替代 `transformRequest` 中的单独模型映射日志。摘要包含足够的信息来判断每个请求的角色和规模。同时为每个请求分配短 ID（8 字符 hex），在入口和出口日志中关联同一请求，避免并发请求时日志交错难以追踪。

---

## 2. 变更内容

### 2.1 新增 `requestBodySummary()` 函数

| 项目 | 内容 |
|------|------|
| **文件** | `internal/proxy/handler.go` |
| **用途** | 从请求体 JSON 中提取关键统计信息 |
| **返回值** | `msgs int, tools int, stream bool` |
| **实现** | 仅解析 `messages`、`tools`、`stream` 三个字段，使用 `json.RawMessage` 避免完整反序列化 |

### 2.2 请求 ID

| 项目 | 内容 |
|------|------|
| **文件** | `internal/proxy/handler.go` |
| **用途** | 为每个请求生成短 ID，关联入口与出口日志 |
| **生成** | `randomHex(8)`，8 字符十六进制字符串 |
| **命名** | `reqID`，在入口和出口日志中统一使用 |

### 2.3 入口日志

| 项目 | 内容 |
|------|------|
| **位置** | `ServeHTTP()` 方法中，`transformRequest` 之后、创建后端请求之前 |
| **格式** | `[reqID] >>> METHOD /path model=xxx stream=true msgs=N tools=N size=N` |
| **触发** | 每个非硬编码端点的请求都会打印 |

日志字段说明：

| 字段 | 含义 | 示例 |
|------|------|------|
| `reqID` | 请求唯一标识（8 字符 hex） | `a1b2c3d4` |
| `>>>` | 入口日志标记 | — |
| `METHOD` | HTTP 方法 | `POST` |
| `/path` | 请求路径 | `/v1/messages` |
| `model` | 原始模型或映射后的模型（含箭头） | `claude-opus-4-6 -> glm-5.1` |
| `stream` | 是否流式请求 | `true` / `false` |
| `msgs` | messages 数组长度 | `15` |
| `tools` | tools 数组长度 | `8` |
| `size` | 请求体字节数 | `45032` |

### 2.4 出口日志

| 项目 | 内容 |
|------|------|
| **位置** | `ServeHTTP()` 方法中，收到上游响应后、写入客户端响应体之前 |
| **格式** | `[reqID] <<< STATUS model=xxx upstream=Nms` |
| **触发** | 所有代理请求路径均打印（包括网络错误，此时 STATUS 为 `502`） |

出口日志字段说明：

| 字段 | 含义 | 示例 |
|------|------|------|
| `reqID` | 与入口日志相同的请求 ID | `a1b2c3d4` |
| `<<<` | 出口日志标记 | — |
| `STATUS` | 上游 HTTP 状态码 | `200` / `429` / `500` |
| `model` | 与入口日志相同的模型信息 | `claude-opus-4-6 -> glm-5.1` |
| `upstream` | 从发送请求到收到上游响应头的耗时（毫秒） | `1234` |

**网络错误路径**：当上游不可达（DNS 失败、连接拒绝、超时等）时，出口日志格式为 `[reqID] <<< 502 upstream=Nms error=...`，确保每个 `>>>` 都有对应的 `<<<`。

### 2.5 移除 `transformRequest` 中的模型映射日志

`transformRequest()` 中的 `log.Printf("Model mapping: %s -> %s ...")` 被移除，因为入口日志已包含映射信息。

### 2.6 硬编码端点日志统一化

| 项目 | 内容 |
|------|------|
| **文件** | `internal/proxy/hardcoded.go` |
| **改动** | 将分散在各 handler 中的 15+ 条日志合并为入口处统一打印 |

**之前**：每个 handler 函数各自打印日志，格式不统一（有的只打印 path，有的打印 method+path，有的带额外参数）。

**现在**：在 `handleHardcodedEndpoint()` 的 switch 之前统一打印一条日志，各 handler 不再关心日志。

- 通用端点格式：`[Hardcoded] Handling METHOD /path`
- count_tokens 特殊格式：`[Hardcoded] Handling METHOD /v1/messages/count_tokens: size=N estimated_tokens=N`（保留额外上下文）

---

## 3. 日志输出示例

### 之前

```
2026/05/20 01:17:03 Model mapping: claude-opus-4-6 -> glm-5.1 (provider: Zhipu GLM 5.1 chanhu)
2026/05/20 01:17:03 Model mapping: claude-opus-4-6 -> glm-5.1 (provider: Zhipu GLM 5.1 chanhu)
...（重复 70 次，无法区分请求）
```

### 之后

```
2026/05/20 01:17:03 [a1b2c3d4] >>> POST /v1/messages model=claude-opus-4-6 -> glm-5.1 stream=true msgs=15 tools=8 size=45032
2026/05/20 01:17:03 [ef567890] >>> POST /v1/messages model=claude-opus-4-6 -> glm-5.1 stream=true msgs=12 tools=3 size=28015
2026/05/20 01:17:03 [ef567890] <<< 200 model=claude-opus-4-6 -> glm-5.1 upstream=342ms
2026/05/20 01:17:03 [a1b2c3d4] <<< 200 model=claude-opus-4-6 -> glm-5.1 upstream=1234ms
2026/05/20 01:17:03 [b3c4d5e6] >>> POST /v1/messages/count_tokens model= stream=false msgs=1 tools=0 size=390
2026/05/20 01:17:03 [b3c4d5e6] <<< 200 model= upstream=56ms
2026/05/20 01:17:03 [Hardcoded] Handling POST /v1/messages/count_tokens: size=390 estimated_tokens=97
2026/05/20 01:17:03 [Hardcoded] Handling GET /v1/me
2026/05/20 01:17:03 [Hardcoded] Handling POST /api/claude_cli_feedback
2026/05/20 01:17:03 [Hardcoded] Handling GET /api/feature/abc
```

---

## 4. 日志解读指南

**关联请求**：通过 `[reqID]` 匹配同一请求的 `>>>`（入口）和 `<<<`（出口）日志。

**入口日志（`>>>`）解读**：

| 请求特征 | 可能的角色 |
|---------|-----------|
| `msgs=大数 tools=大数 size=大` | 主 agent，上下文累积较大 |
| `msgs=中数 tools=1-3` | 子 agent 执行特定任务 |
| `msgs=2 tools=0` | 轻量请求或探针 |
| `stream=false` | 非流式请求（较少见） |
| `size=0` | 无请求体的 GET 请求 |
| 非 `/v1/messages` 路径 | 转发到上游的未知路径，上游可能返回错误 |

**出口日志（`<<<`）解读**：

| 状态码 | 含义 |
|--------|------|
| `200` | 正常响应 |
| `429` | 上游限流，可能需要调整并发 |
| `400` | 请求格式问题，代理会尝试自动恢复（Rectifier） |
| `500/502/503` | 上游服务异常 |
| `502` + `error=` | 网络错误，上游不可达（DNS 失败、连接拒绝、超时） |
| `upstream` 耗时大 | 上游处理慢或网络延迟高 |

**硬编码端点日志（`[Hardcoded]`）解读**：

这些是代理本地直接返回的模拟响应，不转发到上游。`count_tokens` 端点额外显示请求体大小和估算 token 数。

---

## 5. 影响范围

- **修改文件**：`internal/proxy/handler.go`、`internal/proxy/hardcoded.go`
- **不影响**：请求转发逻辑、模型映射逻辑、使用量统计
- **性能影响**：每个代理请求多一次轻量 JSON 解析（仅提取 3 个字段），开销可忽略
