package transform

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// AnthropicToOpenAIResponses converts an Anthropic Messages request to an
// OpenAI Responses API request.
func AnthropicToOpenAIResponses(body []byte, extraParams map[string]any) ([]byte, error) {
	return AnthropicToOpenAIResponsesWithOptions(body, extraParams, Options{ClaudeCodeCompatHint: true})
}

func AnthropicToOpenAIResponsesWithOptions(body []byte, extraParams map[string]any, options Options) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	out := map[string]any{}
	copyIfPresent(out, req, "model")
	copyIfPresent(out, req, "stream")
	copyIfPresent(out, req, "temperature")
	copyIfPresent(out, req, "top_p")
	copyIfPresent(out, req, "thinking")
	copyIfPresent(out, req, "context_management")
	if maxTokens, ok := req["max_tokens"]; ok {
		out["max_output_tokens"] = maxTokens
	}
	input, err := anthropicMessagesToResponsesInput(req, options)
	if err != nil {
		return nil, err
	}
	out["input"] = input
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		out["tools"] = anthropicToolsToResponses(tools)
	}
	if choice, ok := req["tool_choice"].(map[string]any); ok {
		if converted := anthropicToolChoiceToResponses(choice); converted != nil {
			out["tool_choice"] = converted
		}
	}
	for key, value := range extraParams {
		out[key] = value
	}
	return json.Marshal(out)
}

// OpenAIResponsesToAnthropic converts a non-streaming OpenAI Responses API
// response to an Anthropic Messages response.
func OpenAIResponsesToAnthropic(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := map[string]any{
		"id":          stringValue(resp["id"], "msg_openai"),
		"type":        "message",
		"role":        "assistant",
		"model":       resp["model"],
		"content":     []any{},
		"stop_reason": "end_turn",
	}
	var content []any
	if outputs, ok := resp["output"].([]any); ok {
		for _, raw := range outputs {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch item["type"] {
			case "message":
				content = append(content, responsesMessageContentToAnthropic(item)...)
			case "function_call":
				content = append(content, responsesFunctionCallToAnthropic(item))
				out["stop_reason"] = "tool_use"
			}
		}
	}
	out["content"] = content
	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = responsesUsageToAnthropic(usage)
	}
	return json.Marshal(out)
}

// OpenAIResponsesSSEToAnthropic converts OpenAI Responses API SSE events into
// Anthropic Messages SSE events.
func OpenAIResponsesSSEToAnthropic(stream []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := StreamOpenAIResponsesSSEToAnthropic(bytes.NewReader(stream), &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// StreamOpenAIResponsesSSEToAnthropic converts Responses API SSE events to
// Anthropic Messages SSE events as upstream chunks arrive.
func StreamOpenAIResponsesSSEToAnthropic(reader io.Reader, writer io.Writer) error {
	var out bytes.Buffer
	writeSSE(&out, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            "msg_openai",
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         defaultAnthropicUsage(),
		},
	})
	if _, err := writer.Write(out.Bytes()); err != nil {
		return err
	}
	out.Reset()
	contentStarted := false
	flush := func() error {
		if out.Len() == 0 {
			return nil
		}
		if _, err := writer.Write(out.Bytes()); err != nil {
			return err
		}
		out.Reset()
		return nil
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 128*1024), 128*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		if delta, _ := event["delta"].(string); delta != "" {
			if !contentStarted {
				writeSSE(&out, "content_block_start", map[string]any{
					"type":          "content_block_start",
					"index":         0,
					"content_block": map[string]any{"type": "text", "text": ""},
				})
				contentStarted = true
			}
			writeSSE(&out, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": delta},
			})
			if err := flush(); err != nil {
				return err
			}
		}
		if response, ok := event["response"].(map[string]any); ok {
			if contentStarted {
				writeSSE(&out, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			}
			messageDelta := map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": "end_turn"},
				"usage": defaultAnthropicUsage(),
			}
			if usage, ok := response["usage"].(map[string]any); ok {
				messageDelta["usage"] = responsesUsageToAnthropic(usage)
			}
			writeSSE(&out, "message_delta", messageDelta)
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	writeSSE(&out, "message_stop", map[string]any{"type": "message_stop"})
	return flush()
}

func anthropicMessagesToResponsesInput(req map[string]any, options Options) ([]any, error) {
	var input []any
	if system, ok := req["system"]; ok {
		text, err := anthropicContentToText(system)
		if err != nil {
			return nil, err
		}
		text = appendClaudeCodeToolUseCompatibilityHint(text, options.ClaudeCodeCompatHint)
		if text != "" {
			input = append(input, map[string]any{"role": "system", "content": text})
		}
	}
	rawMessages, _ := req["messages"].([]any)
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("message must be object")
		}
		text, err := anthropicContentToText(msg["content"])
		if err != nil {
			return nil, err
		}
		input = append(input, map[string]any{"role": msg["role"], "content": text})
	}
	return input, nil
}

func anthropicToolsToResponses(tools []any) []any {
	out := make([]any, 0, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"type":        "function",
			"name":        tool["name"],
			"description": tool["description"],
			"parameters":  tool["input_schema"],
		})
	}
	return out
}

func anthropicToolChoiceToResponses(choice map[string]any) map[string]any {
	if choice["type"] == "tool" {
		return map[string]any{"type": "function", "name": choice["name"]}
	}
	if choice["type"] == "any" {
		return map[string]any{"type": "required"}
	}
	if choice["type"] == "auto" {
		return map[string]any{"type": "auto"}
	}
	return nil
}

func responsesMessageContentToAnthropic(item map[string]any) []any {
	var content []any
	blocks, _ := item["content"].([]any)
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch block["type"] {
		case "output_text", "text":
			if text, _ := block["text"].(string); text != "" {
				content = append(content, map[string]any{"type": "text", "text": text})
			}
		case "reasoning":
			if text, _ := block["text"].(string); text != "" {
				content = append(content, map[string]any{"type": "thinking", "thinking": text})
			}
		}
	}
	return content
}

func responsesFunctionCallToAnthropic(item map[string]any) map[string]any {
	input := map[string]any{}
	if arguments, _ := item["arguments"].(string); arguments != "" {
		_ = json.Unmarshal([]byte(arguments), &input)
	}
	return map[string]any{
		"type":  "tool_use",
		"id":    item["call_id"],
		"name":  item["name"],
		"input": input,
	}
}

func responsesUsageToAnthropic(usage map[string]any) map[string]any {
	out := defaultAnthropicUsage()
	copyUsageField(out, usage, "input_tokens", "input_tokens")
	copyUsageField(out, usage, "output_tokens", "output_tokens")
	copyUsageField(out, usage, "cached_tokens", "cache_read_input_tokens")
	return out
}
