package transform

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicToOpenAIResponsesConvertsInputToolsAndExtraParams(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"system":"You are concise.",
		"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],
		"tools":[{"name":"Bash","description":"run command","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}}],
		"tool_choice":{"type":"tool","name":"Bash"},
		"max_tokens":512,
		"stream":true
	}`)

	out, err := AnthropicToOpenAIResponses(input, map[string]any{"litellm_settings": map[string]any{"drop_params": true}})
	if err != nil {
		t.Fatalf("AnthropicToOpenAIResponses() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["model"] != "claude-sonnet" || got["max_output_tokens"] != float64(512) || got["stream"] != true {
		t.Fatalf("top-level fields = %#v", got)
	}
	inputItems := got["input"].([]any)
	if inputItems[0].(map[string]any)["role"] != "system" || inputItems[1].(map[string]any)["role"] != "user" {
		t.Fatalf("input = %#v", inputItems)
	}
	tools := got["tools"].([]any)
	if tools[0].(map[string]any)["type"] != "function" || tools[0].(map[string]any)["name"] != "Bash" {
		t.Fatalf("tools = %#v", tools)
	}
	toolChoice := got["tool_choice"].(map[string]any)
	if toolChoice["type"] != "function" || toolChoice["name"] != "Bash" {
		t.Fatalf("tool_choice = %#v", toolChoice)
	}
	if got["litellm_settings"] == nil {
		t.Fatalf("extra params missing: %#v", got)
	}
}

func TestAnthropicToOpenAIResponsesAppendsClaudeCodeToolHintToSystem(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"system":"You are concise.",
		"messages":[{"role":"user","content":"update the file"}]
	}`)

	out, err := AnthropicToOpenAIResponses(input, nil)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIResponses() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	system := got["input"].([]any)[0].(map[string]any)
	content := system["content"].(string)
	for _, want := range []string{
		"You are concise.",
		"Claude Code tool-use compatibility",
		"Edit.old_string",
		"Read",
		"Do not claim a file was updated after an Edit failure",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("system hint missing %q in:\n%s", want, content)
		}
	}
}

func TestAnthropicToOpenAIResponsesCanDisableClaudeCodeToolHint(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"system":"You are concise.",
		"messages":[{"role":"user","content":"update the file"}]
	}`)

	out, err := AnthropicToOpenAIResponsesWithOptions(input, nil, Options{ClaudeCodeCompatHint: false})
	if err != nil {
		t.Fatalf("AnthropicToOpenAIResponsesWithOptions() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	content := got["input"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(content, "Claude Code tool-use compatibility") {
		t.Fatalf("system hint should be disabled, got:\n%s", content)
	}
	if content != "You are concise." {
		t.Fatalf("system content = %q", content)
	}
}

func TestAnthropicToOpenAIResponsesPreservesCompatibleExtensionParams(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"messages":[{"role":"user","content":"hello"}],
		"thinking":{"type":"enabled","budget_tokens":1024},
		"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]}
	}`)

	out, err := AnthropicToOpenAIResponses(input, nil)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIResponses() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["thinking"] == nil || got["context_management"] == nil {
		t.Fatalf("compatible params were not preserved: %#v", got)
	}
}

func TestOpenAIResponsesToAnthropicConvertsOutputAndUsage(t *testing.T) {
	input := []byte(`{
		"id":"resp_1",
		"model":"gpt-4.1",
		"output":[{
			"type":"message",
			"role":"assistant",
			"content":[{"type":"output_text","text":"hello"}]
		},{
			"type":"function_call",
			"call_id":"call_1",
			"name":"Bash",
			"arguments":"{\"cmd\":\"pwd\"}"
		}],
		"usage":{"input_tokens":6,"output_tokens":3}
	}`)

	out, err := OpenAIResponsesToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIResponsesToAnthropic() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	content := got["content"].([]any)
	if content[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("text content = %#v", content)
	}
	if content[1].(map[string]any)["type"] != "tool_use" || content[1].(map[string]any)["name"] != "Bash" {
		t.Fatalf("tool content = %#v", content)
	}
	usage := got["usage"].(map[string]any)
	if usage["input_tokens"] != float64(6) || usage["output_tokens"] != float64(3) {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestOpenAIResponsesSSEToAnthropicEmitsAnthropicEvents(t *testing.T) {
	input := []byte(
		"event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\n" +
			"event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":6,\"output_tokens\":3}}}\n\n",
	)

	out, err := OpenAIResponsesSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIResponsesSSEToAnthropic() error = %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"event: message_start",
		`"text":"hi"`,
		`"input_tokens":6`,
		`"output_tokens":3`,
		"event: message_stop",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("converted SSE missing %q:\n%s", want, got)
		}
	}
}
