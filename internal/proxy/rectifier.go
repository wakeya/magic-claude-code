package proxy

import (
	"encoding/json"
	"strings"
)

type ErrorPattern int

const (
	PatternNone ErrorPattern = iota
	PatternToolValidation
	PatternThinkingSignature
	PatternGenericBadRequest
)

// RectifyRequest 根据错误模式对请求体执行清理，返回清理后的请求体和是否执行了清理
func RectifyRequest(body []byte, pattern ErrorPattern) ([]byte, bool) {
	if pattern == PatternNone {
		return body, false
	}

	anyChanged := false

	if pattern == PatternToolValidation {
		if cleaned, changed := cleanTools(body); changed {
			body = cleaned
			anyChanged = true
		}
	}

	if pattern == PatternThinkingSignature {
		if cleaned, changed := cleanThinking(body); changed {
			body = cleaned
			anyChanged = true
		}
	}

	if pattern == PatternGenericBadRequest {
		if cleaned, changed := cleanUnknownContentTypes(body); changed {
			body = cleaned
			anyChanged = true
		}
	}

	return body, anyChanged
}

// matchErrorPattern 从上游 400 错误体中检测可恢复的错误模式
func matchErrorPattern(errorBody []byte) ErrorPattern {
	if len(errorBody) == 0 {
		return PatternNone
	}

	msg := extractErrorMessage(errorBody)
	if msg == "" {
		return PatternNone
	}
	lower := strings.ToLower(msg)

	// 模式 1：Tool 定义校验
	if isToolValidationError(lower) {
		return PatternToolValidation
	}

	// 模式 2：Thinking/签名不兼容
	if isThinkingSignatureError(lower) {
		return PatternThinkingSignature
	}

	// 模式 3：通用 400 错误（如 kimi "Invalid request Error"）
	if hasGenericInvalidRequestPhrase(lower) {
		return PatternGenericBadRequest
	}

	return PatternNone
}

func extractErrorMessage(body []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return string(body)
	}

	// 尝试 error.error.message
	if e, ok := raw["error"].(map[string]any); ok {
		if msg, ok := e["message"].(string); ok {
			// 检查 message 本身是否是嵌套 JSON
			var nested map[string]any
			if json.Unmarshal([]byte(msg), &nested) == nil {
				if ne, ok := nested["error"].(map[string]any); ok {
					if nmsg, ok := ne["message"].(string); ok {
						return nmsg
					}
				}
				if nmsg, ok := nested["message"].(string); ok {
					return nmsg
				}
			}
			return msg
		}
	}

	// 尝试 error.message
	if msg, ok := raw["message"].(string); ok {
		return msg
	}

	return string(body)
}

func isThinkingSignatureError(lower string) bool {
	// signature + thinking（如 "Invalid 'signature' in 'thinking' block"）
	if strings.Contains(lower, "signature") && strings.Contains(lower, "thinking") {
		return true
	}

	// signature + field required（如 "messages.1.content.0.signature: Field required"）
	if strings.Contains(lower, "signature") && strings.Contains(lower, "field required") {
		return true
	}

	// signature + extra inputs（如 "xxx.signature: Extra inputs are not permitted"）
	if strings.Contains(lower, "signature") && strings.Contains(lower, "extra inputs") {
		return true
	}

	// must start with a thinking block
	if strings.Contains(lower, "must start with a thinking block") {
		return true
	}

	// Expected thinking or redacted_thinking, but found tool_use
	if strings.Contains(lower, "expected") &&
		(strings.Contains(lower, "thinking") || strings.Contains(lower, "redacted_thinking")) &&
		strings.Contains(lower, "tool_use") {
		return true
	}

	// thinking blocks cannot be modified
	if strings.Contains(lower, "thinking") && strings.Contains(lower, "cannot be modified") {
		return true
	}

	// thinking content must be passed back (DeepSeek: "The content[].thinking in the thinking mode must be passed back")
	if strings.Contains(lower, "passed back") && strings.Contains(lower, "thinking") {
		return true
	}

	// Thought signature is not valid
	if strings.Contains(lower, "thought signature") &&
		(strings.Contains(lower, "not valid") || strings.Contains(lower, "invalid")) {
		return true
	}

	return false
}

func isToolValidationError(lower string) bool {
	if strings.Contains(lower, "function name or parameters is empty") {
		return true
	}
	// DeepSeek: "tool_use ids were found without tool_result blocks immediately after"
	if strings.Contains(lower, "tool_use") && strings.Contains(lower, "without") && strings.Contains(lower, "tool_result") {
		return true
	}
	if strings.Contains(lower, "unsupported content type") || strings.Contains(lower, "unknown content type") {
		return false
	}
	if !hasGenericInvalidRequestPhrase(lower) {
		return false
	}
	return hasToolErrorContext(lower)
}

func hasGenericInvalidRequestPhrase(lower string) bool {
	return strings.Contains(lower, "invalid request") ||
		strings.Contains(lower, "invalid_request_error") ||
		strings.Contains(lower, "invalid params") ||
		strings.Contains(lower, "非法请求") ||
		strings.Contains(lower, "illegal request") ||
		strings.Contains(lower, "unsupported content type") ||
		strings.Contains(lower, "unknown content type")
}

func hasToolErrorContext(lower string) bool {
	for _, marker := range []string{
		"tool",
		"function",
		"parameters",
		"input_schema",
		"input schema",
		"schema",
		"additionalproperties",
		"additional properties",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// cleanTools 清理 tool 定义中的不兼容字段，返回清理后的请求体和是否修改
func cleanTools(body []byte) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, false
	}

	tools, ok := req["tools"].([]any)
	if !ok {
		return body, false
	}

	changed := false
	for i, t := range tools {
		tool, ok := t.(map[string]any)
		if !ok {
			continue
		}

		// 移除 cache_control
		if _, has := tool["cache_control"]; has {
			delete(tool, "cache_control")
			changed = true
		}

		// 处理 input_schema
		schema, hasSchema := tool["input_schema"].(map[string]any)
		if !hasSchema {
			// 缺失 input_schema，添加空 schema
			tool["input_schema"] = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
			changed = true
			continue
		}

		if len(schema) == 0 {
			// 空 input_schema，填充
			tool["input_schema"] = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
			changed = true
			continue
		}

		// 移除 JSON Schema 元数据
		for _, key := range []string{"$schema", "$id", "$comment", "additionalProperties"} {
			if _, has := schema[key]; has {
				delete(schema, key)
				changed = true
			}
		}

		tools[i] = tool
	}

	if !changed {
		return body, false
	}

	out, err := json.Marshal(req)
	if err != nil {
		return body, false
	}
	return out, true
}

// cleanThinking 移除 thinking/redacted_thinking 块和 signature 字段
func cleanThinking(body []byte) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, false
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		return body, false
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

		var filtered []any
		for _, block := range content {
			b, ok := block.(map[string]any)
			if !ok {
				filtered = append(filtered, block)
				continue
			}
			btype, _ := b["type"].(string)
			if btype == "thinking" || btype == "redacted_thinking" {
				changed = true
				continue
			}
			if _, has := b["signature"]; has {
				delete(b, "signature")
				changed = true
			}
			filtered = append(filtered, b)
		}
		msg["content"] = filtered
	}

	// 若 thinking.type=enabled 且最后 assistant 消息不以 thinking 块开头，移除顶层 thinking
	if thinking, ok := req["thinking"].(map[string]any); ok {
		if ttype, _ := thinking["type"].(string); ttype == "enabled" {
			lastAssistant := findLastAssistantMessage(messages)
			if lastAssistant != nil {
				content, _ := lastAssistant["content"].([]any)
				if len(content) > 0 {
					first, _ := content[0].(map[string]any)
					ftype, _ := first["type"].(string)
					if ftype != "thinking" && ftype != "redacted_thinking" {
						delete(req, "thinking")
						changed = true
					}
				}
			}
		}
	}

	if !changed {
		return body, false
	}

	out, err := json.Marshal(req)
	if err != nil {
		return body, false
	}
	return out, true
}

func findLastAssistantMessage(messages []any) map[string]any {
	for i := len(messages) - 1; i >= 0; i-- {
		if msg, ok := messages[i].(map[string]any); ok {
			if role, _ := msg["role"].(string); role == "assistant" {
				return msg
			}
		}
	}
	return nil
}

var knownContentTypes = map[string]bool{
	"text": true, "image": true, "tool_use": true, "tool_result": true,
	"thinking": true, "redacted_thinking": true,
	"document": true, "file": true,
}

// cleanUnknownContentTypes removes non-standard content blocks (e.g. tool_reference)
// from messages and nested tool_result.content that upstream providers may not recognize
func cleanUnknownContentTypes(body []byte) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, false
	}

	messages, ok := req["messages"].([]any)
	if !ok {
		return body, false
	}

	changed := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if filterContentBlocks(msg) {
			changed = true
		}
	}

	if !changed {
		return body, false
	}

	out, err := json.Marshal(req)
	if err != nil {
		return body, false
	}
	return out, true
}

// filterContentBlocks cleans a message's content array and recurses into tool_result.content
func filterContentBlocks(msg map[string]any) bool {
	content, ok := msg["content"]
	if !ok {
		return false
	}

	arr, ok := content.([]any)
	if !ok {
		return false
	}

	changed := false
	filtered := make([]any, 0, len(arr))
	for _, block := range arr {
		b, ok := block.(map[string]any)
		if !ok {
			filtered = append(filtered, block)
			continue
		}
		btype, _ := b["type"].(string)
		if !knownContentTypes[btype] && btype != "" {
			changed = true
			continue
		}
		// recurse into tool_result.content
		if btype == "tool_result" {
			if filterContentBlocks(b) {
				changed = true
			}
		}
		filtered = append(filtered, block)
	}
	msg["content"] = filtered
	return changed
}
