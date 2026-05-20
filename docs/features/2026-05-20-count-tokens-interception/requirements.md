# count_tokens 端点拦截规格

**版本**: 1.0
**日期**: 2026-05-20
**状态**: 待实施

---

## 1. 背景

代理日志显示 Claude Code 在会话启动时向 `/v1/messages/count_tokens` 发送大量请求（观察到的记录：68 次/秒）。这些请求被转发到第三方上游（如 glm-5.1），而上游不支持此端点。

### 问题

1. **无意义请求**：68 个请求在 1 秒内到达上游，上游大概率返回 404 或错误
2. **浪费带宽**：总计约 500KB 请求体被发送到上游
3. **触发限流风险**：大量并发请求可能触发上游 rate limit
4. **上下文管理失效**：Claude Code 拿不到正确的 token 计数，上下文窗口管理在"盲飞"

### 日志证据

```
2026/05/20 02:01:10 [Proxy] POST /v1/messages/count_tokens model=claude-opus-4-6 -> glm-5.1 stream=false msgs=1 tools=0 size=390
2026/05/20 02:01:10 [Proxy] POST /v1/messages/count_tokens model=claude-opus-4-6 -> glm-5.1 stream=false msgs=1 tools=0 size=337
...（68 条，全部在同一秒内）
```

### 请求模式分析

| 类型 | 数量 | 特征 | 含义 |
|------|------|------|------|
| 单条消息计数 | ~45 | `msgs=1 tools=0 size=132~2436` | 每条历史消息的 token 数 |
| 工具定义计数 | ~20 | `msgs=1 tools=1 size=360~9924` | 每个工具 schema 的 token 数 |
| 全量工具列表 | 1 | `msgs=1 tools=85 size=75705` | 85 个工具的总 token 数 |
| 核心工具列表 | 1 | `msgs=1 tools=8 size=53909` | 8 个核心工具的 token 数 |
| 完整对话上下文 | 1 | `msgs=139 tools=0 size=256462` | 139 条消息的完整对话 |

---

## 2. Anthropic 官方 API 规范

### 端点

```
POST /v1/messages/count_tokens
```

### 请求体

与 `/v1/messages` 相同的格式（`model`、`messages`、`system`、`tools` 等字段），但不会实际执行推理。

### 响应体

```json
{
  "input_tokens": 1234
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `input_tokens` | integer | 输入 token 的估算数量 |

官方说明：
- token 计数应视为**估算值**
- 实际消息创建时的 token 数可能略有差异
- 此端点**免费**但受 rate limit 约束

**参考**：[Anthropic Token Counting 文档](https://docs.anthropic.com/en/docs/build-with-claude/token-counting)

---

## 3. 实施方案

### 3.1 拦截策略

在 `handleHardcodedEndpoint` 中拦截 `/v1/messages/count_tokens`，**本地估算 token 数**后直接返回，不转发到上游。

### 3.2 Token 估算算法

Anthropic 的 tokenizer 大约按 **1 token ≈ 4 字节（英文）/ 1-2 字节（中文）** 计算。对于代理场景，一个合理的估算公式：

```
input_tokens = max(1, request_body_size / 4)
```

- 使用请求体的 JSON 字节长度作为估算基础
- 除以 4 得到大致的 token 数（英文文本的近似比）
- 最少返回 1（避免 0 值导致边界问题）

**为什么不用 tiktoken 或精确 tokenizer？**
- 引入 tokenizer 依赖会增加二进制大小和编译复杂度
- Claude Code 只需要一个粗略估算来做上下文管理
- 估算值偏差 ±20% 在实际使用中不影响功能

### 3.3 响应格式

```json
HTTP/1.1 200 OK
Content-Type: application/json

{
  "input_tokens": <estimated_count>
}
```

---

## 4. 变更内容

### 4.1 修改文件

| 文件 | 变更 |
|------|------|
| `internal/proxy/hardcoded.go` | 添加 `isHardcodedEndpoint` 匹配 + `handleCountTokens` handler |

### 4.2 新增代码

1. **`isHardcodedEndpoint`** 精确匹配列表中添加 `"/v1/messages/count_tokens"`
2. **`handleHardcodedEndpoint`** 在 `drainRequestBody` 之前单独拦截 count_tokens（因为需要读取请求体）
3. **`handleCountTokens`** 新方法：读取请求体长度，估算 token 数，返回 JSON

### 4.3 日志格式

```
[Hardcoded] Handling POST /v1/messages/count_tokens: size=390 estimated_tokens=97
```

### 4.4 不修改

- `ServeHTTP` 主流程不变
- 其他硬编码端点不变
- 代理转发逻辑不变

---

## 5. 预期效果

| 指标 | 修改前 | 修改后 |
|------|--------|--------|
| count_tokens 请求转发到上游 | 是（68 次/会话启动） | 否（本地拦截） |
| 上游 API 调用次数 | +68 无意义请求 | 0 |
| Claude Code 上下文管理 | 盲飞（上游返回错误） | 正常（拿到估算值） |
| 响应延迟 | 依赖上游（可能超时） | <1ms（本地计算） |

---

## 6. 风险与限制

| 风险 | 缓解 |
|------|------|
| 估算不准确 | 官方文档明确说明 token 计数本身就是估算；±20% 偏差不影响 Claude Code 功能 |
| 未来 API 变更 | Claude Code 只使用 `input_tokens` 字段，响应格式稳定 |
| 中文文本估算偏低 | 可调整为 `/3` 作为更保守的估算；但 Claude Code 上下文窗口有足够余量 |
