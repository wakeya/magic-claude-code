package proxy

import (
	"encoding/json"
	"testing"
)

func TestMatchErrorPattern_ToolValidation(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","message":"invalid params, function name or parameters is empty (2013)"}}`)
	got := matchErrorPattern(body)
	if got != PatternToolValidation {
		t.Errorf("expected PatternToolValidation, got %v", got)
	}
}

func TestMatchErrorPattern_GenericInvalidParams(t *testing.T) {
	// 单独的 "invalid params" 触发通用 400 清理（如移除未知 content block）
	body := []byte(`{"error":{"type":"invalid_request_error","message":"invalid params, some other error"}}`)
	got := matchErrorPattern(body)
	if got != PatternGenericBadRequest {
		t.Errorf("expected PatternGenericBadRequest, got %v", got)
	}
}

func TestMatchErrorPattern_ThinkingSignature(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","message":"Invalid 'signature' in 'thinking' block"}}`)
	got := matchErrorPattern(body)
	if got != PatternThinkingSignature {
		t.Errorf("expected PatternThinkingSignature, got %v", got)
	}
}

func TestMatchErrorPattern_ExpectedThinking(t *testing.T) {
	body := []byte(`{"error":{"message":"Expected thinking or redacted_thinking, but found tool_use"}}`)
	got := matchErrorPattern(body)
	if got != PatternThinkingSignature {
		t.Errorf("expected PatternThinkingSignature, got %v", got)
	}
}

func TestMatchErrorPattern_GenericInvalidRequest(t *testing.T) {
	// kimi 的 "Invalid request Error" 触发通用 400 清理
	body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"Invalid request Error"}}`)
	got := matchErrorPattern(body)
	if got != PatternGenericBadRequest {
		t.Errorf("expected PatternGenericBadRequest for kimi-style generic invalid request, got %v", got)
	}
}

func TestMatchErrorPattern_KimiToolValidation(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","message":"Invalid request Error: tools.0.input_schema.additionalProperties is not supported"}}`)
	got := matchErrorPattern(body)
	if got != PatternToolValidation {
		t.Errorf("expected PatternToolValidation for Kimi tool schema error, got %v", got)
	}
}

func TestMatchErrorPattern_SignatureInvalidWithoutThinking_NoMatch(t *testing.T) {
	// "signature" + "invalid" 但不含 "thinking"，不应误报为 thinking 错误
	body := []byte(`{"error":{"message":"signature is invalid for this operation"}}`)
	got := matchErrorPattern(body)
	if got == PatternThinkingSignature {
		t.Errorf("should not match PatternThinkingSignature for signature+invalid without thinking, got %v", got)
	}
}

func TestMatchErrorPattern_NoMatch(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"rate limit", []byte(`{"error":{"message":"rate limit exceeded"}}`)},
		{"timeout", []byte(`{"error":{"message":"Request timeout"}}`)},
		{"server error", []byte(`{"error":{"message":"internal server error"}}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchErrorPattern(tt.body)
			if got != PatternNone {
				t.Errorf("expected PatternNone, got %v", got)
			}
		})
	}
}

func TestMatchErrorPattern_NilBody(t *testing.T) {
	got := matchErrorPattern(nil)
	if got != PatternNone {
		t.Errorf("expected PatternNone for nil body, got %v", got)
	}
	got = matchErrorPattern([]byte{})
	if got != PatternNone {
		t.Errorf("expected PatternNone for empty body, got %v", got)
	}
}

func TestMatchErrorPattern_NestedJSON(t *testing.T) {
	body := []byte(`{"error":{"message":"{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"signature field required\"}}"}}`)
	got := matchErrorPattern(body)
	if got != PatternThinkingSignature {
		t.Errorf("expected PatternThinkingSignature for nested JSON, got %v", got)
	}
}

func TestMatchErrorPattern_ChineseInvalidRequest(t *testing.T) {
	body := []byte(`{"error":{"message":"非法请求：参数格式错误"}}`)
	got := matchErrorPattern(body)
	if got != PatternGenericBadRequest {
		t.Errorf("expected PatternGenericBadRequest for Chinese generic error, got %v", got)
	}
}

// ============================================================
// 任务 2：cleanTools 测试
// ============================================================

func TestCleanTools_移除CacheControl(t *testing.T) {
	body := []byte(`{"tools":[{"name":"Bash","description":"run","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}},"cache_control":{"type":"ephemeral"}}]}`)
	got, changed := cleanTools(body)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	tools := result["tools"].([]any)
	tool := tools[0].(map[string]any)
	if _, ok := tool["cache_control"]; ok {
		t.Error("cache_control should be removed")
	}
	if tool["name"] != "Bash" {
		t.Error("name should be preserved")
	}
}

func TestCleanTools_填充空InputSchema(t *testing.T) {
	body := []byte(`{"tools":[{"name":"Bash","description":"run","input_schema":{}}]}`)
	got, changed := cleanTools(body)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	tools := result["tools"].([]any)
	tool := tools[0].(map[string]any)
	schema := tool["input_schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("expected type=object, got %v", schema["type"])
	}
	props, ok := schema["properties"]
	if !ok {
		t.Error("expected properties key")
	}
	propsMap := props.(map[string]any)
	if len(propsMap) != 0 {
		t.Errorf("expected empty properties, got %v", propsMap)
	}
}

func TestCleanTools_填充缺失InputSchema(t *testing.T) {
	body := []byte(`{"tools":[{"name":"Bash","description":"run"}]}`)
	got, changed := cleanTools(body)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	tools := result["tools"].([]any)
	tool := tools[0].(map[string]any)
	if _, ok := tool["input_schema"]; !ok {
		t.Error("expected input_schema to be added")
	}
}

func TestCleanTools_移除Schema元数据(t *testing.T) {
	body := []byte(`{"tools":[{"name":"T","input_schema":{"$schema":"http://json-schema.org/draft-07/schema#","$id":"test","$comment":"meta","type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}}]}`)
	got, changed := cleanTools(body)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	schema := result["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any)
	for _, key := range []string{"$schema", "$id", "$comment", "additionalProperties"} {
		if _, ok := schema[key]; ok {
			t.Errorf("%s should be removed", key)
		}
	}
	if schema["type"] != "object" {
		t.Error("type should be preserved")
	}
}

func TestCleanTools_保留核心字段(t *testing.T) {
	original := `{"tools":[{"name":"Bash","description":"Run a command","input_schema":{"type":"object","properties":{"command":{"type":"string","description":"The command"}},"required":["command"]}}]}`
	got, changed := cleanTools([]byte(original))
	if changed {
		t.Error("valid schema should not be changed")
	}
	if string(got) != original {
		t.Errorf("body should be identical:\ngot:  %s\nwant: %s", got, original)
	}
}

func TestCleanTools_保留有效Schema(t *testing.T) {
	body := []byte(`{"tools":[{"name":"Read","input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]}`)
	_, changed := cleanTools(body)
	if changed {
		t.Error("valid input_schema should not be changed")
	}
}

func TestCleanTools_无Tools字段(t *testing.T) {
	body := []byte(`{"model":"test","messages":[]}`)
	got, changed := cleanTools(body)
	if changed {
		t.Error("body without tools should not be changed")
	}
	if string(got) != string(body) {
		t.Error("body should be unchanged")
	}
}

func TestCleanTools_无效JSON(t *testing.T) {
	body := []byte(`not json`)
	got, changed := cleanTools(body)
	if changed {
		t.Error("invalid JSON should not be changed")
	}
	if string(got) != string(body) {
		t.Error("body should be unchanged")
	}
}

// ============================================================
// 任务 3：cleanThinking 测试
// ============================================================

func TestCleanThinking_移除Thinking块(t *testing.T) {
	body := []byte(`{"model":"test","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"s"},{"type":"text","text":"hello"}]}]}`)
	got, changed := cleanThinking(body)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var result map[string]any
	json.Unmarshal(got, &result)
	content := result["messages"].([]any)[0].(map[string]any)["content"].([]any)
	for _, block := range content {
		btype := block.(map[string]any)["type"].(string)
		if btype == "thinking" {
			t.Error("thinking block should be removed")
		}
	}
}

func TestCleanThinking_移除RedactedThinking块(t *testing.T) {
	body := []byte(`{"model":"test","messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"abc"},{"type":"text","text":"hi"}]}]}`)
	got, changed := cleanThinking(body)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var result map[string]any
	json.Unmarshal(got, &result)
	content := result["messages"].([]any)[0].(map[string]any)["content"].([]any)
	for _, block := range content {
		btype := block.(map[string]any)["type"].(string)
		if btype == "redacted_thinking" {
			t.Error("redacted_thinking block should be removed")
		}
	}
}

func TestCleanThinking_移除Signature字段(t *testing.T) {
	body := []byte(`{"model":"test","messages":[{"role":"assistant","content":[{"type":"text","text":"hello","signature":"sig1"},{"type":"tool_use","id":"t1","name":"Bash","input":{},"signature":"sig2"}]}]}`)
	got, changed := cleanThinking(body)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var result map[string]any
	json.Unmarshal(got, &result)
	content := result["messages"].([]any)[0].(map[string]any)["content"].([]any)
	for _, block := range content {
		if _, ok := block.(map[string]any)["signature"]; ok {
			t.Error("signature field should be removed from all blocks")
		}
	}
}

func TestCleanThinking_移除顶层Thinking(t *testing.T) {
	body := []byte(`{"model":"test","thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`)
	got, changed := cleanThinking(body)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var result map[string]any
	json.Unmarshal(got, &result)
	if _, ok := result["thinking"]; ok {
		t.Error("top-level thinking should be removed when last assistant doesn't start with thinking")
	}
}

func TestCleanThinking_保留非Thinking消息(t *testing.T) {
	body := []byte(`{"model":"test","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	_, changed := cleanThinking(body)
	if changed {
		t.Error("body without thinking blocks should not be changed")
	}
}

func TestCleanThinking_保留Adaptive类型(t *testing.T) {
	body := []byte(`{"model":"test","thinking":{"type":"adaptive"},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`)
	got, changed := cleanThinking(body)
	if changed {
		t.Error("adaptive thinking type should not trigger top-level removal")
	}
	var result map[string]any
	json.Unmarshal(got, &result)
	thinking := result["thinking"].(map[string]any)
	if thinking["type"] != "adaptive" {
		t.Error("adaptive thinking should be preserved")
	}
}

func TestCleanThinking_无Messages字段(t *testing.T) {
	body := []byte(`{"model":"test"}`)
	_, changed := cleanThinking(body)
	if changed {
		t.Error("body without messages should not be changed")
	}
}

// ============================================================
// 任务 4：RectifyRequest 组合测试
// ============================================================

func TestRectifyRequest_Tool校验(t *testing.T) {
	body := []byte(`{"model":"test","tools":[{"name":"Bash","description":"run","input_schema":{}}]}`)
	got, applied := RectifyRequest(body, PatternToolValidation)
	if !applied {
		t.Fatal("expected applied=true")
	}
	var result map[string]any
	json.Unmarshal(got, &result)
	schema := result["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Error("tool cleanup should fill input_schema")
	}
}

func TestRectifyRequest_Thinking签名(t *testing.T) {
	body := []byte(`{"model":"test","thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"s"},{"type":"text","text":"hello"}]}]}`)
	got, applied := RectifyRequest(body, PatternThinkingSignature)
	if !applied {
		t.Fatal("expected applied=true")
	}
	var result map[string]any
	json.Unmarshal(got, &result)
	if _, ok := result["thinking"]; ok {
		// thinking block 被移除后首块变成 text，应删除顶层 thinking
		t.Error("top-level thinking should be removed after block cleanup")
	}
}

func TestRectifyRequest_无匹配(t *testing.T) {
	body := []byte(`{"model":"test","messages":[]}`)
	got, applied := RectifyRequest(body, PatternNone)
	if applied {
		t.Error("PatternNone should not apply cleanup")
	}
	if string(got) != string(body) {
		t.Error("body should be unchanged for PatternNone")
	}
}

func TestRectifyRequest_与现有Transform共存(t *testing.T) {
	// 模型映射已在 transformRequest 中完成，rectify 不应改变 model 字段
	body := []byte(`{"model":"mapped-model","tools":[{"name":"Bash","input_schema":{}}]}`)
	got, _ := RectifyRequest(body, PatternToolValidation)
	var result map[string]any
	json.Unmarshal(got, &result)
	if result["model"] != "mapped-model" {
		t.Error("rectify should preserve existing model mapping")
	}
}

func TestMatchErrorPattern_MustStartWithThinkingBlock(t *testing.T) {
	body := []byte(`{"error":{"message":"a final assistant message must start with a thinking block"}}`)
	got := matchErrorPattern(body)
	if got != PatternThinkingSignature {
		t.Errorf("expected PatternThinkingSignature, got %v", got)
	}
}

func TestMatchErrorPattern_SignatureFieldRequired(t *testing.T) {
	body := []byte(`{"error":{"message":"messages.1.content.0.signature: Field required"}}`)
	got := matchErrorPattern(body)
	if got != PatternThinkingSignature {
		t.Errorf("expected PatternThinkingSignature, got %v", got)
	}
}

func TestMatchErrorPattern_SignatureExtraInputs(t *testing.T) {
	body := []byte(`{"error":{"message":"xxx.signature: Extra inputs are not permitted"}}`)
	got := matchErrorPattern(body)
	if got != PatternThinkingSignature {
		t.Errorf("expected PatternThinkingSignature, got %v", got)
	}
}

func TestMatchErrorPattern_ThinkingCannotBeModified(t *testing.T) {
	body := []byte(`{"error":{"message":"thinking or redacted_thinking blocks cannot be modified"}}`)
	got := matchErrorPattern(body)
	if got != PatternThinkingSignature {
		t.Errorf("expected PatternThinkingSignature, got %v", got)
	}
}

func TestMatchErrorPattern_ThoughtSignatureNotValid(t *testing.T) {
	body := []byte(`{"error":{"message":"Unable to submit request because Thought signature is not valid"}}`)
	got := matchErrorPattern(body)
	if got != PatternThinkingSignature {
		t.Errorf("expected PatternThinkingSignature, got %v", got)
	}
}
