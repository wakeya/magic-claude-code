# DeepSeek 模型在 Claude Code 高版本中报 400 错误

**状态**: 待修复
**发现日期**: 2026-05-29
**影响版本**: Claude Code for VS Code >= 2.1.150（2.1.150 ~ 2.1.154 已确认）
**严重程度**: 高 — DeepSeek 模型完全不可用

## 问题描述

在 Claude Code for VS Code 2.1.150 及以上版本中，配置 DeepSeek 模型时会持续报错：

```
API Error: 400 The content[].thinking in the thinking mode must be passed back to the API.
```

同时伴随的另一个错误模式：

```
tool_use ids were found without tool_result blocks immediately after
```

## 根因分析

Claude Code 高版本在发送请求时会：
1. 在请求体中设置 `thinking` 字段（`type: "enabled"` 或 `type: "adaptive"`）
2. 在 assistant 消息的 content blocks 中包含 `redacted_thinking` 块
3. 在请求中包含 `context_management` 字段
4. 在 `anthropic-beta` header 中携带 `interleaved-thinking` 和 `context-management` 标志

DeepSeek API 不支持这些 Anthropic 专有特性，要求：
- 如果开启了 thinking 模式，所有 assistant 消息中的 thinking 内容必须原样回传给 API
- 但 Claude Code 发送的 `redacted_thinking` 块（内容被加密/脱敏）无法被 DeepSeek 理解和接受

## 已尝试的解决方案

### 方案 1: 移除 redacted_thinking 块 + 删除顶层 thinking 字段

**文件**: `internal/proxy/handler.go`

在 `transformRequest` 中过滤掉 `redacted_thinking` 块，并在清除 thinking 块后无条件删除顶层 `thinking` 字段：

```go
// handler.go - stripRedactedThinking
func stripRedactedThinking(req map[string]any) bool {
    messages, ok := req["messages"].([]any)
    if !ok {
        return false
    }
    changed := false
    for _, m := range messages {
        msg, ok := m.(map[string]any)
        if !ok {
            continue
        }
        content, ok := msg["content"].([]any)
        if !ok {
            continue
        }
        filtered := make([]any, 0, len(content))
        for _, block := range content {
            b, ok := block.(map[string]any)
            if !ok {
                filtered = append(filtered, block)
                continue
            }
            btype, _ := b["type"].(string)
            if btype == "redacted_thinking" {
                changed = true
                continue // 跳过 redacted_thinking
            }
            filtered = append(filtered, block)
        }
        msg["content"] = filtered
    }
    return changed
}
```

**结果**: 无效 — 第一轮对话可以成功，但后续轮次仍然报 400 错误。原因是后续轮次的 assistant 消息中还包含 `thinking` 块（非 redacted），DeepSeek 要求这些 thinking 内容必须回传，但 Claude Code 发送的 thinking 内容格式与 DeepSeek 预期不匹配。

### 方案 2: 仅在有 assistant 消息且无 thinking 块时删除 thinking 字段

**文件**: `internal/proxy/handler.go`

```go
// handler.go - transformRequest 中
if thinking, ok := req["thinking"].(map[string]any); ok {
    if ttype, _ := thinking["type"].(string); ttype == "enabled" || ttype == "adaptive" {
        if hasAssistantMessages(req) && !hasThinkingBlocks(req) {
            log.Printf("[Compat] Removing thinking field: mode active but no thinking blocks in messages")
            delete(req, "thinking")
            changed = true
        }
    }
}
```

**结果**: 无效 — 清除 thinking 块后检测到没有 thinking 块，删除顶层 thinking 字段，但 DeepSeek 在后续请求中仍然因为 assistant 消息中缺少 thinking 内容而拒绝。

### 方案 3: 修复缺失的 tool_result 块

**文件**: `internal/proxy/rectifier.go`

添加了 `fixMissingToolResults` 函数，在自动重试时补充缺失的 tool_result 块：

```go
func fixMissingToolResults(body []byte) ([]byte, bool) {
    // 扫描 assistant 消息中的 tool_use id
    // 在下一条 user 消息中检查是否有对应的 tool_result
    // 缺失的补上空 tool_result
}
```

**结果**: 部分有效 — 解决了 `tool_use without tool_result` 错误，但核心的 thinking 回传问题仍未解决。

### 方案 4: 从 anthropic-beta header 中移除 interleaved-thinking

**文件**: `internal/proxy/handler.go`

```go
// 当请求体中没有 thinking 字段时，从 anthropic-beta header 中过滤掉 interleaved-thinking
if strings.EqualFold(key, "anthropic-beta") {
    var peek map[string]any
    if json.Unmarshal(modifiedBody, &peek) == nil {
        if _, hasThinking := peek["thinking"]; !hasThinking {
            parts := strings.Split(value, ",")
            filtered := make([]string, 0, len(parts))
            for _, p := range parts {
                if strings.HasPrefix(strings.TrimSpace(p), "interleaved-thinking") ||
                   strings.HasPrefix(strings.TrimSpace(p), "context-management") {
                    continue
                }
                filtered = append(filtered, strings.TrimSpace(p))
            }
            // ...
        }
    }
}
```

**结果**: 无效 — 即使移除了 header 标志，DeepSeek 仍然根据请求体中的内容判断。

## 核心难点

1. **Claude Code 的 thinking 内容是脱敏的**: assistant 消息中的 thinking 块可能是 `redacted_thinking`（加密后不可读），DeepSeek 无法接受这种格式
2. **thinking 必须回传**: DeepSeek 要求如果开启了 thinking 模式，所有 assistant 消息中的 thinking 内容必须原封不动回传，但代理无法满足这个条件（内容已被 Claude Code 脱敏）
3. **多轮对话累积**: 随着对话轮次增加，每条 assistant 消息都会带 thinking 内容，清理逻辑越来越复杂
4. **Claude Code 版本行为变化**: 2.1.150 之前的版本可能不发送 thinking 相关字段，升级后突然出现兼容性问题

## 当前代码状态

以上方案的代码改动仍在工作区中（未提交），涉及文件：
- `internal/proxy/handler.go` — 新增 stripRedactedThinking、dumpAssistantBlocks、thinking 条件删除等逻辑
- `internal/proxy/rectifier.go` — 新增 fixMissingToolResults、扩展错误模式匹配
- `internal/proxy/rectifier_test.go` — 对应测试用例

## 可能的后续方向

1. **彻底禁用 thinking**: 对 DeepSeek 请求完全移除所有 thinking 相关内容（包括 assistant 消息中的 thinking/redacted_thinking 块和顶层 thinking 字段），但这可能导致 DeepSeek 拒绝处理（因为 API 认为 thinking 模式下内容不完整）
2. **将 thinking 块转为普通 text**: 把 `type: "thinking"` 的内容转为 `type: "text"`，模拟 thinking 内容回传
3. **等待 DeepSeek 侧修复**: 如果 DeepSeek 能够容忍缺失的 thinking 内容，问题自然解决
4. **等待 Claude Code 降级行为**: 在后续版本中 Claude Code 可能对第三方模型自动禁用 thinking
