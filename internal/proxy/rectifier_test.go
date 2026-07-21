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

func TestMatchErrorPattern_UnsupportedContentType(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","message":"failed to convert tool result content: unsupported content type in ContentBlockParamUnion: tool_reference"}}`)
	got := matchErrorPattern(body)
	if got != PatternGenericBadRequest {
		t.Errorf("expected PatternGenericBadRequest for unsupported content type error, got %v", got)
	}
}

func TestMatchErrorPattern_UnknownContentType(t *testing.T) {
	body := []byte(`{"error":{"message":"unknown content type in request"}}`)
	got := matchErrorPattern(body)
	if got != PatternGenericBadRequest {
		t.Errorf("expected PatternGenericBadRequest for unknown content type error, got %v", got)
	}
}

func TestMatchErrorPattern_Zhipu1210(t *testing.T) {
	tests := []struct {
		name string
		body string
		want ErrorPattern
	}{
		{
			name: "structured code",
			body: `{"type":"error","error":{"type":"invalid_request_error","code":"1210","message":"[1210][API 调用参数有误，请检查文档。][synthetic-id]"}}`,
			want: PatternToolValidation,
		},
		{
			name: "exact message fallback",
			body: `{"error":{"message":"[1210][API 调用参数有误，请检查文档。][synthetic-id]"}}`,
			want: PatternToolValidation,
		},
		{
			name: "unrelated digits",
			body: `{"error":{"message":"request 1210 could not be found"}}`,
			want: PatternNone,
		},
		{
			name: "content block remains higher priority",
			body: `{"error":{"code":"1210","message":"unsupported content type: tool_reference"}}`,
			want: PatternGenericBadRequest,
		},
		{
			name: "structured code wins over generic invalid request phrase",
			body: `{"error":{"code":"1210","message":"Invalid request: [1210][API 调用参数有误，请检查文档。][synthetic-id]"}}`,
			want: PatternToolValidation,
		},
		{
			name: "structured code wins over chinese illegal-request phrase",
			body: `{"error":{"code":"1210","message":"非法请求：[1210][API 调用参数有误][synthetic-id]"}}`,
			want: PatternToolValidation,
		},
		{
			name: "structured code with empty message",
			body: `{"error":{"code":"1210","message":""}}`,
			want: PatternToolValidation,
		},
		{
			name: "structured code with missing message",
			body: `{"error":{"code":"1210"}}`,
			want: PatternToolValidation,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchErrorPattern([]byte(tt.body)); got != tt.want {
				t.Fatalf("matchErrorPattern() = %v, want %v", got, tt.want)
			}
		})
	}
}

// blockTypes 提取 content 数组中各 block 的 type 集合，供清洗测试断言使用。
func blockTypes(content []any) map[string]bool {
	types := make(map[string]bool)
	for _, b := range content {
		if m, ok := b.(map[string]any); ok {
			if t, _ := m["type"].(string); t != "" {
				types[t] = true
			}
		}
	}
	return types
}

// TestProactiveClean_PreservesToolReference 验证主动清洗保留 tool_reference
// （Claude Code deferred tool 的加载标记），同时仍剥离其他真正的非标准类型。
// 对应 2026-07-21-preserve-tool-reference spec 需求 1 与需求 4。
func TestProactiveClean_PreservesToolReference(t *testing.T) {
	req := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_t1",
						"content": []any{
							map[string]any{"type": "tool_reference", "tool_name": "WebSearch"},
							map[string]any{"type": "server_tool_use", "name": "future"},
							map[string]any{"type": "text", "text": "ok"},
						},
					},
				},
			},
		},
	}
	changed := proactiveCleanUnknownContentTypes(req)
	if !changed {
		t.Fatal("expected proactive cleanup to strip server_tool_use (changed=true)")
	}
	content := req["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["content"].([]any)
	types := blockTypes(content)
	if _, ok := types["tool_reference"]; !ok {
		t.Errorf("proactive cleanup must preserve tool_reference; remaining blocks: %v", types)
	}
	if _, ok := types["server_tool_use"]; ok {
		t.Errorf("proactive cleanup must strip other non-standard types (server_tool_use); remaining: %v", types)
	}
	if _, ok := types["text"]; !ok {
		t.Errorf("proactive cleanup must keep standard text blocks; remaining: %v", types)
	}
}

// TestReactiveClean_StripsToolReference 验证反应式清洗在 400 后仍清除 tool_reference，
// 以便 tool_reference 指向未定义工具的异常 400 能通过重试恢复。
// 对应 2026-07-21-preserve-tool-reference spec 需求 2。
func TestReactiveClean_StripsToolReference(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_t1","content":[{"type":"tool_reference","tool_name":"ToolSearch"},{"type":"text","text":"ok"}]}]}]}`)
	out, changed := cleanUnknownContentTypes(body)
	if !changed {
		t.Fatal("expected reactive cleanup to strip tool_reference (changed=true)")
	}
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("cleaned body is not valid JSON: %v", err)
	}
	content := resp["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["content"].([]any)
	types := blockTypes(content)
	if _, ok := types["tool_reference"]; ok {
		t.Errorf("reactive cleanup must strip tool_reference; remaining: %v", types)
	}
	if _, ok := types["text"]; !ok {
		t.Errorf("reactive cleanup must keep text blocks; remaining: %v", types)
	}
}

// TestMatchErrorPattern_KimiCurrentErrors 验证 matchErrorPattern 识别现行 kimi 端点的 400 错误，
// 使反应式 tryRectify 能在 tool_reference 引发 400 时触发 cleanUnknownContentTypes。
// 对应 2026-07-21-preserve-tool-reference spec 任务 2。
func TestMatchErrorPattern_KimiCurrentErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "coding_invalid_request_error",
			body: `{"error":{"type":"invalid_request_error","message":"Invalid request Error"}}`,
		},
		{
			name: "moonshot_tool_reference_not_found",
			body: `{"error":{"type":"invalid_request_error","message":"messages.2.content.0.tool_result.content: Tool reference 'ToolSearch' not found in available tools"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchErrorPattern([]byte(tt.body)); got != PatternGenericBadRequest {
				t.Fatalf("matchErrorPattern() = %v, want PatternGenericBadRequest", got)
			}
		})
	}
}
