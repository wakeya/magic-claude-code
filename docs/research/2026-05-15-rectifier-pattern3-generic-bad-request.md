# 研究报告：第三方供应商非标准 Content Block 兼容性

**日期**: 2026-05-18  
**状态**: 已完成并实施（commit `bbfc0fd`）

---

## 问题背景

Claude Code 使用第三方 API（如 kimi-k2.6）作为后端时，启用 deferred tool（WebSearch、ToolSearch 等）后请求返回 400 错误。GLM-5.1 等宽容型 API（忽略未知字段）则不受影响。

## 根因分析

### 数据流追踪

通过 curl 逐字段测试和 docker 日志抓取完整请求体，排除了以下可疑字段：

| 排除项 | 测试结果 |
|--------|---------|
| `thinking: {"type": "adaptive"}` | 200 ✓ kimi 支持 |
| `context_management` | 200 ✓ kimi 忽略 |
| `output_config` | 200 ✓ kimi 忽略 |
| `metadata` | 200 ✓ kimi 忽略 |
| `system[].cache_control` | 200 ✓ kimi 忽略 |
| 9 个工具定义（含 WebSearch） | 200 ✓ schema 无问题 |
| `$schema` / `additionalProperties` | 200 ✓ kimi 容忍 |

最终定位到 **消息历史中的 `tool_reference` content block**：

```json
{
  "type": "tool_result",
  "tool_use_id": "tool_vctFbjQFH6xZRer",
  "content": [
    {"type": "tool_reference", "tool_name": "WebSearch"}  // ← 罪魁祸首
  ]
}
```

curl 复现确认：

```
含 tool_reference → 400 {"error":{"message":"Invalid request Error"}}
移除 tool_reference → 200 ✓
```

### tool_reference 是什么

Claude Code 客户端的**内部元数据**，标记"这个 tool_result 是加载 deferred tool 的结果"。不在 Anthropic 公开 API 规范中，纯客户端侧机制，LLM 不需要它做推理。

## 修复策略

### 三层防御架构

```
请求进入代理
    │
    ├─ 第 1 层：主动清洗 (transformRequest)
    │   处理 model 映射和 thinking 字段
    │   → 无法预知 tool_reference（在消息历史中，不在请求模板中）
    │
    ├─ 转发到 kimi → 返回 400
    │
    ├─ 第 2 层：模式检测 (matchErrorPattern)
    │   "Invalid request Error" → hasGenericInvalidRequestPhrase → 命中
    │   返回 PatternGenericBadRequest
    │
    └─ 第 3 层：清理 + 重试 (RectifyRequest)
        cleanUnknownContentTypes 递归遍历：
          messages[].content[]                     → 检查每个 block 的 type
          messages[].content[].content[] (嵌套)    → 递归进入 tool_result 内部
          非 "text/image/tool_use/tool_result/thinking/redacted_thinking" → 移除
        清理后重试 → 200 ✓
```

### 关键设计决策

1. **白名单而非黑名单**：列举允许的类型，未知类型一律移除。未来新增任何非标准类型都会被自动处理。

2. **递归进入 tool_result.content**：初次修复只扫了消息顶层 content，漏掉了 tool_result 内部的嵌套 content。`tool_reference` 正是藏在 `tool_result.content` 数组中。

3. **反应式而非主动式**：只在 400 错误发生后才清理，避免对正常请求（发往 Anthropic 官方 API）的干扰。代价是多一次往返（~1-2 秒延迟）。

### 白名单

```go
var knownContentTypes = map[string]bool{
    "text":              true,
    "image":             true,
    "tool_use":          true,
    "tool_result":       true,
    "thinking":          true,
    "redacted_thinking": true,
}
```

## 影响评估

| 层面 | 影响 |
|------|------|
| Claude Code 客户端 | 无影响。客户端维护自己的会话状态，代理只在上游请求中剥离 |
| LLM 推理能力 | 无影响。tool_reference 对 LLM 推理无意义，移除后 LLM 反而能正常处理 |
| tool_result 语义 | 不受影响。tool_reference 只是 content 数组中的一个 block，移除后其他 block 完好 |
| 请求延迟 | 每次触发多一个往返（首次 400 → 清理 → 重试成功） |

## 后续方向

- **主动式清洗**：在 `transformRequest` 阶段就剥离非标准 content block，避免第一次 400 的往返延迟
- **扩展清理范围**：考虑清理 `system[].cache_control` 等字段，进一步减少第三方 API 的拒绝率
- **供应商能力声明**：为 Provider 增加更多能力开关（如 `SupportsCacheControl`），实现细粒度的主动式清理
