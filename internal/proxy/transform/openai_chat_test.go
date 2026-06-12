package transform

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestAnthropicToOpenAIChatConvertsMessagesToolsAndExtraParams(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet-4-6",
		"system":"You are concise.",
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}
		],
		"tools":[{"name":"Bash","description":"run command","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}}],
		"tool_choice":{"type":"tool","name":"Bash"},
		"max_tokens":1024,
		"temperature":0.2,
		"top_p":0.9,
		"stop_sequences":["stop"],
		"stream":true
	}`)
	extra := map[string]any{
		"allowed_openai_params": []any{"thinking", "context_management"},
		"litellm_settings": map[string]any{
			"drop_params": true,
		},
	}

	out, err := AnthropicToOpenAIChat(input, extra)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["model"] != "claude-sonnet-4-6" {
		t.Fatalf("model = %#v", got["model"])
	}
	messages := got["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "system" {
		t.Fatalf("first message = %#v", messages[0])
	}
	if messages[1].(map[string]any)["content"] != "hello" {
		t.Fatalf("user text message = %#v", messages[1])
	}
	assistant := messages[2].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("assistant message = %#v", assistant)
	}
	toolCalls := assistant["tool_calls"].([]any)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	if function["name"] != "Bash" || function["arguments"] != `{"cmd":"pwd"}` {
		t.Fatalf("tool call function = %#v", function)
	}
	toolResult := messages[3].(map[string]any)
	if toolResult["role"] != "tool" || toolResult["tool_call_id"] != "toolu_1" || toolResult["content"] != "ok" {
		t.Fatalf("tool result message = %#v", toolResult)
	}
	if got["max_tokens"] != float64(1024) || got["temperature"] != 0.2 || got["top_p"] != 0.9 {
		t.Fatalf("sampling fields = %#v", got)
	}
	if got["stop"].([]any)[0] != "stop" {
		t.Fatalf("stop = %#v", got["stop"])
	}
	if got["stream"] != true {
		t.Fatalf("stream = %#v", got["stream"])
	}
	streamOptions := got["stream_options"].(map[string]any)
	if streamOptions["include_usage"] != true {
		t.Fatalf("stream_options = %#v", streamOptions)
	}
	tools := got["tools"].([]any)
	if tools[0].(map[string]any)["type"] != "function" {
		t.Fatalf("tools = %#v", tools)
	}
	toolChoice := got["tool_choice"].(map[string]any)
	if toolChoice["type"] != "function" {
		t.Fatalf("tool_choice = %#v", toolChoice)
	}
	if got["allowed_openai_params"] == nil || got["litellm_settings"] == nil {
		t.Fatalf("extra params missing: %#v", got)
	}
}

func TestAnthropicToOpenAIChatAppendsClaudeCodeToolHintToSystem(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"system":"You are concise.",
		"messages":[{"role":"user","content":"update the file"}]
	}`)

	out, err := AnthropicToOpenAIChat(input, nil)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	system := got["messages"].([]any)[0].(map[string]any)
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

func TestAnthropicToOpenAIChatCanDisableClaudeCodeToolHint(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"system":"You are concise.",
		"messages":[{"role":"user","content":"update the file"}]
	}`)

	out, err := AnthropicToOpenAIChatWithOptions(input, nil, Options{ClaudeCodeCompatHint: false})
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChatWithOptions() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	content := got["messages"].([]any)[0].(map[string]any)["content"].(string)
	if strings.Contains(content, "Claude Code tool-use compatibility") {
		t.Fatalf("system hint should be disabled, got:\n%s", content)
	}
	if content != "You are concise." {
		t.Fatalf("system content = %q", content)
	}
}

func TestAnthropicToOpenAIChatKeepsTextAndToolUseInOneAssistantMessage(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"messages":[{
			"role":"assistant",
			"content":[
				{"type":"text","text":"Let me check."},
				{"type":"tool_use","id":"call_123","name":"Bash","input":{"command":"pwd"}}
			]
		}]
	}`)

	out, err := AnthropicToOpenAIChat(input, nil)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	messages := got["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages length = %d, messages = %#v", len(messages), messages)
	}
	message := messages[0].(map[string]any)
	if message["role"] != "assistant" || message["content"] != "Let me check." {
		t.Fatalf("assistant message = %#v", message)
	}
	toolCalls := message["tool_calls"].([]any)
	if toolCalls[0].(map[string]any)["id"] != "call_123" {
		t.Fatalf("tool_calls = %#v", toolCalls)
	}
}

func TestAnthropicToOpenAIChatMapsStringToolChoiceAndCleansTools(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{
			"name":"BatchTool",
			"type":"BatchTool",
			"description":"skip",
			"input_schema":{"type":"object"}
		},{
			"name":"Fetch",
			"description":"fetch url",
			"input_schema":{
				"type":"object",
				"properties":{"url":{"type":"string","format":"uri"}}
			}
		}],
		"tool_choice":"any"
	}`)

	out, err := AnthropicToOpenAIChat(input, nil)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %#v", got["tool_choice"])
	}
	tools := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
	params := tools[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	url := params["properties"].(map[string]any)["url"].(map[string]any)
	if _, ok := url["format"]; ok {
		t.Fatalf("schema format was not cleaned: %#v", params)
	}
}

func TestAnthropicToOpenAIChatConvertsImageBlocks(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"messages":[{
			"role":"user",
			"content":[
				{"type":"text","text":"describe"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}
			]
		}]
	}`)

	out, err := AnthropicToOpenAIChat(input, nil)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	message := got["messages"].([]any)[0].(map[string]any)
	content := message["content"].([]any)
	if content[0].(map[string]any)["type"] != "text" || content[0].(map[string]any)["text"] != "describe" {
		t.Fatalf("text content = %#v", content)
	}
	image := content[1].(map[string]any)
	if image["type"] != "image_url" {
		t.Fatalf("image content = %#v", image)
	}
	imageURL := image["image_url"].(map[string]any)
	if imageURL["url"] != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("image_url = %#v", imageURL)
	}
}

func TestAnthropicToOpenAIChatPreservesCompatibleExtensionParams(t *testing.T) {
	input := []byte(`{
		"model":"claude-sonnet",
		"messages":[{"role":"user","content":"hello"}],
		"thinking":{"type":"enabled","budget_tokens":1024},
		"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]}
	}`)

	out, err := AnthropicToOpenAIChat(input, nil)
	if err != nil {
		t.Fatalf("AnthropicToOpenAIChat() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["thinking"] == nil || got["context_management"] == nil {
		t.Fatalf("compatible params were not preserved: %#v", got)
	}
}

func TestOpenAIChatToAnthropicConvertsContentToolCallsAndUsage(t *testing.T) {
	input := []byte(`{
		"id":"chatcmpl_1",
		"model":"gpt-4.1",
		"choices":[{
			"message":{
				"role":"assistant",
				"content":"hello",
				"reasoning_content":"thinking",
				"tool_calls":[{
					"id":"call_1",
					"type":"function",
					"function":{"name":"Bash","arguments":"{\"cmd\":\"pwd\"}"}
				}]
			},
			"finish_reason":"tool_calls"
		}],
		"usage":{
			"prompt_tokens":11,
			"completion_tokens":7,
			"prompt_tokens_details":{"cached_tokens":3}
		}
	}`)

	out, err := OpenAIChatToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatToAnthropic() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["type"] != "message" || got["role"] != "assistant" || got["model"] != "gpt-4.1" {
		t.Fatalf("message metadata = %#v", got)
	}
	content := got["content"].([]any)
	if content[0].(map[string]any)["type"] != "thinking" || content[0].(map[string]any)["thinking"] != "thinking" {
		t.Fatalf("thinking block = %#v", content[0])
	}
	if content[1].(map[string]any)["type"] != "text" || content[1].(map[string]any)["text"] != "hello" {
		t.Fatalf("text block = %#v", content[1])
	}
	toolUse := content[2].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["id"] != "call_1" || toolUse["name"] != "Bash" {
		t.Fatalf("tool_use block = %#v", toolUse)
	}
	inputMap := toolUse["input"].(map[string]any)
	if inputMap["cmd"] != "pwd" {
		t.Fatalf("tool input = %#v", inputMap)
	}
	if got["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %#v", got["stop_reason"])
	}
	usage := got["usage"].(map[string]any)
	if usage["input_tokens"] != float64(8) || usage["output_tokens"] != float64(7) || usage["cache_read_input_tokens"] != float64(3) {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestOpenAIChatToAnthropicHandlesFunctionCallRefusalContentArrayAndObjectArguments(t *testing.T) {
	input := []byte(`{
		"id":"chatcmpl_legacy",
		"model":"gpt-4o",
		"choices":[{
			"message":{
				"role":"assistant",
				"content":[{"type":"text","text":"visible"}],
				"refusal":"blocked",
				"function_call":{"name":"Read","arguments":{"file_path":"/tmp/a.txt"}}
			},
			"finish_reason":"function_call"
		}]
	}`)

	out, err := OpenAIChatToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatToAnthropic() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	content := got["content"].([]any)
	if content[0].(map[string]any)["text"] != "visible" || content[1].(map[string]any)["text"] != "blocked" {
		t.Fatalf("text/refusal content = %#v", content)
	}
	toolUse := content[2].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["name"] != "Read" {
		t.Fatalf("tool use = %#v", toolUse)
	}
	inputMap := toolUse["input"].(map[string]any)
	if inputMap["file_path"] != "/tmp/a.txt" {
		t.Fatalf("tool input = %#v", inputMap)
	}
	if got["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %#v", got["stop_reason"])
	}
}

func TestOpenAIChatFinishReasonContentFilterMapsToEndTurn(t *testing.T) {
	input := []byte(`{
		"id":"chatcmpl_filter",
		"model":"gpt-4o",
		"choices":[{
			"message":{"role":"assistant","content":"blocked"},
			"finish_reason":"content_filter"
		}]
	}`)

	out, err := OpenAIChatToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatToAnthropic() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason = %#v", got["stop_reason"])
	}
}

func TestOpenAIChatSSEToAnthropicEmitsAnthropicEvents(t *testing.T) {
	input := []byte(
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4.1\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n" +
			"data: [DONE]\n\n",
	)

	out, err := OpenAIChatSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatSSEToAnthropic() error = %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		`"text":"hi"`,
		"event: message_delta",
		`"input_tokens":4`,
		`"output_tokens":2`,
		"event: message_stop",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("converted SSE missing %q:\n%s", want, got)
		}
	}
}

func TestOpenAIChatSSEToAnthropicAlwaysIncludesUsageForClaudeCodeVSCode(t *testing.T) {
	input := []byte(
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4.1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	)

	out, err := OpenAIChatSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatSSEToAnthropic() error = %v", err)
	}

	events := parseSSEEvents(t, string(out))
	start := events["message_start"][0]
	startUsage := start["message"].(map[string]any)["usage"].(map[string]any)
	if startUsage["input_tokens"] != float64(0) || startUsage["output_tokens"] != float64(0) {
		t.Fatalf("message_start usage = %#v", startUsage)
	}
	delta := events["message_delta"][0]
	deltaUsage := delta["usage"].(map[string]any)
	if deltaUsage["input_tokens"] != float64(0) || deltaUsage["output_tokens"] != float64(0) {
		t.Fatalf("message_delta usage = %#v", deltaUsage)
	}
}

func TestOpenAIChatSSEToAnthropicUsesUsageOnlyChunkAfterFinishReason(t *testing.T) {
	input := []byte(
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4.1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n" +
			"data: [DONE]\n\n",
	)

	out, err := OpenAIChatSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatSSEToAnthropic() error = %v", err)
	}

	events := parseSSEEvents(t, string(out))
	deltas := events["message_delta"]
	if len(deltas) != 1 {
		t.Fatalf("message_delta count = %d, output:\n%s", len(deltas), out)
	}
	usage := deltas[0]["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(3) {
		t.Fatalf("message_delta usage = %#v", usage)
	}
}

func TestOpenAIChatSSEToAnthropicSubtractsCacheTokensFromInputUsage(t *testing.T) {
	input := []byte(
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":50,\"prompt_tokens_details\":{\"cached_tokens\":600},\"cache_creation_input_tokens\":300}}\n\n" +
			"data: [DONE]\n\n",
	)

	out, err := OpenAIChatSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatSSEToAnthropic() error = %v", err)
	}

	events := parseSSEEvents(t, string(out))
	usage := events["message_delta"][0]["usage"].(map[string]any)
	if usage["input_tokens"] != float64(100) {
		t.Fatalf("input_tokens = %#v, usage = %#v", usage["input_tokens"], usage)
	}
	if usage["cache_read_input_tokens"] != float64(600) || usage["cache_creation_input_tokens"] != float64(300) {
		t.Fatalf("cache usage = %#v", usage)
	}
}

func TestOpenAIChatSSEToAnthropicStreamsToolCallsByIndex(t *testing.T) {
	input := []byte(
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_0\",\"type\":\"function\",\"function\":{\"name\":\"first_tool\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"second_tool\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"{\\\"b\\\":2}\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"a\\\":1}\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4}}\n\n" +
			"data: [DONE]\n\n",
	)

	out, err := OpenAIChatSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatSSEToAnthropic() error = %v", err)
	}

	events := flattenSSEEvents(parseSSEEvents(t, string(out)))
	toolIndexByCall := map[string]float64{}
	for _, event := range events {
		if event["type"] != "content_block_start" {
			continue
		}
		block, _ := event["content_block"].(map[string]any)
		if block["type"] != "tool_use" {
			continue
		}
		toolIndexByCall[block["id"].(string)] = event["index"].(float64)
	}
	if len(toolIndexByCall) != 2 {
		t.Fatalf("tool starts = %#v, output:\n%s", toolIndexByCall, out)
	}
	if toolIndexByCall["call_0"] == toolIndexByCall["call_1"] {
		t.Fatalf("tool calls share one content block index: %#v", toolIndexByCall)
	}

	argumentIndexByPayload := map[string]float64{}
	for _, event := range events {
		if event["type"] != "content_block_delta" {
			continue
		}
		delta, _ := event["delta"].(map[string]any)
		if delta["type"] != "input_json_delta" {
			continue
		}
		argumentIndexByPayload[delta["partial_json"].(string)] = event["index"].(float64)
	}
	if argumentIndexByPayload[`{"a":1}`] != toolIndexByCall["call_0"] {
		t.Fatalf("first tool args routed to wrong block: args=%#v starts=%#v", argumentIndexByPayload, toolIndexByCall)
	}
	if argumentIndexByPayload[`{"b":2}`] != toolIndexByCall["call_1"] {
		t.Fatalf("second tool args routed to wrong block: args=%#v starts=%#v", argumentIndexByPayload, toolIndexByCall)
	}
}

func TestOpenAIChatSSEToAnthropicDelaysToolStartUntilIDAndNameReady(t *testing.T) {
	input := []byte(
		"data: {\"id\":\"chatcmpl_2\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl_2\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_0\",\"type\":\"function\",\"function\":{\"name\":\"first_tool\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl_2\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl_2\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":6,\"completion_tokens\":2}}\n\n" +
			"data: [DONE]\n\n",
	)

	out, err := OpenAIChatSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatSSEToAnthropic() error = %v", err)
	}

	events := flattenSSEEvents(parseSSEEvents(t, string(out)))
	var starts []map[string]any
	var argDeltas []string
	for _, event := range events {
		if event["type"] == "content_block_start" {
			block, _ := event["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				starts = append(starts, event)
			}
		}
		if event["type"] == "content_block_delta" {
			delta, _ := event["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				argDeltas = append(argDeltas, delta["partial_json"].(string))
			}
		}
	}
	if len(starts) != 1 {
		t.Fatalf("tool start count = %d, output:\n%s", len(starts), out)
	}
	block := starts[0]["content_block"].(map[string]any)
	if block["id"] != "call_0" || block["name"] != "first_tool" {
		t.Fatalf("tool start block = %#v", block)
	}
	if strings.Join(argDeltas, "") != `{"a":1}` {
		t.Fatalf("argument deltas = %#v", argDeltas)
	}
}

func TestOpenAIChatSSEToAnthropicStartsMalformedToolCallAtFinishWithFallbacks(t *testing.T) {
	input := []byte(
		"data: {\"id\":\"chatcmpl_3\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":2,\"function\":{\"arguments\":\"{\\\"command\\\":\\\"pwd\\\"}\"}}]}}]}\n\n" +
			"data: {\"id\":\"chatcmpl_3\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":6,\"completion_tokens\":2}}\n\n" +
			"data: [DONE]\n\n",
	)

	out, err := OpenAIChatSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatSSEToAnthropic() error = %v", err)
	}

	events := flattenSSEEvents(parseSSEEvents(t, string(out)))
	var toolStart map[string]any
	for _, event := range events {
		if event["type"] != "content_block_start" {
			continue
		}
		block, _ := event["content_block"].(map[string]any)
		if block["type"] == "tool_use" {
			toolStart = block
			break
		}
	}
	if toolStart == nil {
		t.Fatalf("missing fallback tool_use start, output:\n%s", out)
	}
	if toolStart["id"] != "tool_call_2" || toolStart["name"] != "unknown_tool" {
		t.Fatalf("fallback tool start = %#v", toolStart)
	}
}

func TestOpenAIChatSSEToAnthropicFirstFinishReasonWinsAndMapsLegacyFunctionCall(t *testing.T) {
	input := []byte(
		"data: {\"id\":\"chatcmpl_4\",\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"function_call\":{\"name\":\"Read\",\"arguments\":{\"file_path\":\"/tmp/a.txt\"}}},\"finish_reason\":\"function_call\"}]}\n\n" +
			"data: {\"id\":\"chatcmpl_4\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":6,\"completion_tokens\":2}}\n\n" +
			"data: [DONE]\n\n",
	)

	out, err := OpenAIChatSSEToAnthropic(input)
	if err != nil {
		t.Fatalf("OpenAIChatSSEToAnthropic() error = %v", err)
	}

	events := flattenSSEEvents(parseSSEEvents(t, string(out)))
	var toolStart map[string]any
	var partialJSON string
	var stopReason any
	for _, event := range events {
		if event["type"] == "content_block_start" {
			block, _ := event["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolStart = block
			}
		}
		if event["type"] == "content_block_delta" {
			delta, _ := event["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partialJSON += delta["partial_json"].(string)
			}
		}
		if event["type"] == "message_delta" {
			stopReason = event["delta"].(map[string]any)["stop_reason"]
		}
	}
	if toolStart == nil || toolStart["name"] != "Read" {
		t.Fatalf("tool start = %#v, output:\n%s", toolStart, out)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(partialJSON), &args); err != nil {
		t.Fatalf("partial_json is not valid JSON %q: %v", partialJSON, err)
	}
	if args["file_path"] != "/tmp/a.txt" {
		t.Fatalf("args = %#v", args)
	}
	if stopReason != "tool_use" {
		t.Fatalf("stop_reason = %#v, output:\n%s", stopReason, out)
	}
}

func TestStreamOpenAIChatSSEToAnthropicEmitsBeforeUpstreamCloses(t *testing.T) {
	upstreamReader, upstreamWriter := io.Pipe()
	convertedWriter := &sseChunkWriter{chunks: make(chan string, 8)}
	errCh := make(chan error, 1)
	go func() {
		errCh <- StreamOpenAIChatSSEToAnthropic(upstreamReader, convertedWriter)
	}()

	if _, err := upstreamWriter.Write([]byte("data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-4.1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")); err != nil {
		t.Fatalf("write upstream: %v", err)
	}

	select {
	case got := <-convertedWriter.chunks:
		if !strings.Contains(got, "event: message_start") && !strings.Contains(got, "event: content_block_start") {
			t.Fatalf("converted stream did not emit Anthropic events: %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("converter did not emit before upstream closed")
	}

	_ = upstreamWriter.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("stream converter error = %v", err)
	}
}

func flattenSSEEvents(grouped map[string][]map[string]any) []map[string]any {
	var events []map[string]any
	for _, group := range grouped {
		events = append(events, group...)
	}
	return events
}

func parseSSEEvents(t *testing.T, stream string) map[string][]map[string]any {
	t.Helper()
	events := map[string][]map[string]any{}
	for _, block := range strings.Split(stream, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var eventName string
		var dataLine string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				dataLine = strings.TrimPrefix(line, "data: ")
			}
		}
		if eventName == "" || dataLine == "" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(dataLine), &data); err != nil {
			t.Fatalf("decode SSE data %q: %v", dataLine, err)
		}
		events[eventName] = append(events[eventName], data)
	}
	return events
}

type sseChunkWriter struct {
	chunks chan string
}

func (w *sseChunkWriter) Write(p []byte) (int, error) {
	w.chunks <- string(p)
	return len(p), nil
}
