package transform

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// AnthropicToOpenAIChat converts an Anthropic Messages request body to an
// OpenAI Chat Completions request body.
func AnthropicToOpenAIChat(body []byte, extraParams map[string]any) ([]byte, error) {
	return AnthropicToOpenAIChatWithOptions(body, extraParams, Options{ClaudeCodeCompatHint: true})
}

func AnthropicToOpenAIChatWithOptions(body []byte, extraParams map[string]any, options Options) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	out := make(map[string]any)
	copyIfPresent(out, req, "model")
	copyIfPresent(out, req, "max_tokens")
	copyIfPresent(out, req, "temperature")
	copyIfPresent(out, req, "top_p")
	copyIfPresent(out, req, "stream")
	copyIfPresent(out, req, "thinking")
	copyIfPresent(out, req, "context_management")
	if stop, ok := req["stop_sequences"]; ok {
		out["stop"] = stop
	}
	if stream, ok := req["stream"].(bool); ok && stream {
		out["stream_options"] = map[string]any{"include_usage": true}
	}

	messages, err := anthropicMessagesToOpenAI(req, options)
	if err != nil {
		return nil, err
	}
	out["messages"] = messages

	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		if converted := anthropicToolsToOpenAI(tools); len(converted) > 0 {
			out["tools"] = converted
		}
	}
	if toolChoice, ok := req["tool_choice"]; ok {
		out["tool_choice"] = anthropicToolChoiceToOpenAI(toolChoice)
	}
	for key, value := range extraParams {
		out[key] = value
	}

	return json.Marshal(out)
}

// OpenAIChatToAnthropic converts a non-streaming OpenAI Chat Completions
// response body to an Anthropic Messages response body.
func OpenAIChatToAnthropic(body []byte) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	out := map[string]any{
		"id":      stringValue(resp["id"], "msg_openai"),
		"type":    "message",
		"role":    "assistant",
		"model":   resp["model"],
		"content": []any{},
	}

	choices, _ := resp["choices"].([]any)
	if len(choices) > 0 {
		choice, _ := choices[0].(map[string]any)
		message, _ := choice["message"].(map[string]any)
		content := openAIChatMessageContentToAnthropic(message)
		out["content"] = content
		out["stop_reason"] = openAIChatFinishReasonToAnthropic(choice["finish_reason"])
	} else {
		out["stop_reason"] = "end_turn"
	}
	if usage, ok := resp["usage"].(map[string]any); ok {
		out["usage"] = openAIUsageToAnthropic(usage)
	}
	return json.Marshal(out)
}

// OpenAIChatSSEToAnthropic converts an OpenAI Chat Completions SSE stream into
// Anthropic Messages SSE events.
func OpenAIChatSSEToAnthropic(stream []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := StreamOpenAIChatSSEToAnthropic(bytes.NewReader(stream), &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// StreamOpenAIChatSSEToAnthropic converts an OpenAI Chat Completions SSE stream
// to Anthropic Messages SSE events as upstream chunks arrive.
func StreamOpenAIChatSSEToAnthropic(reader io.Reader, writer io.Writer) error {
	var out bytes.Buffer
	messageStarted := false
	finalized := false
	pendingStopReason := "end_turn"
	hasPendingStopReason := false
	latestUsage := defaultAnthropicUsage()
	model := ""
	messageID := "msg_openai"
	nextContentIndex := 0
	currentNonToolBlockType := ""
	currentNonToolBlockIndex := -1
	toolBlocksByIndex := map[int]*openAIStreamToolBlockState{}
	openToolBlockIndices := map[int]bool{}

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

	closeCurrentNonToolBlock := func() {
		if currentNonToolBlockIndex < 0 {
			return
		}
		writeSSE(&out, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": currentNonToolBlockIndex,
		})
		currentNonToolBlockIndex = -1
		currentNonToolBlockType = ""
	}

	startNonToolBlock := func(blockType string) int {
		if currentNonToolBlockType == blockType && currentNonToolBlockIndex >= 0 {
			return currentNonToolBlockIndex
		}
		closeCurrentNonToolBlock()
		index := nextContentIndex
		nextContentIndex++
		contentBlock := map[string]any{"type": blockType}
		if blockType == "thinking" {
			contentBlock["thinking"] = ""
		} else {
			contentBlock["text"] = ""
		}
		writeSSE(&out, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": contentBlock,
		})
		currentNonToolBlockType = blockType
		currentNonToolBlockIndex = index
		return index
	}

	writeInputJSONDelta := func(index int, partialJSON string) {
		if partialJSON == "" {
			return
		}
		writeSSE(&out, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": partialJSON,
			},
		})
	}

	closeOpenToolBlocks := func() {
		if len(openToolBlockIndices) == 0 {
			return
		}
		indices := make([]int, 0, len(openToolBlockIndices))
		for index := range openToolBlockIndices {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		for _, index := range indices {
			writeSSE(&out, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": index,
			})
			delete(openToolBlockIndices, index)
		}
	}

	startPendingToolBlocks := func() {
		if len(toolBlocksByIndex) == 0 {
			return
		}
		toolIndices := make([]int, 0, len(toolBlocksByIndex))
		for toolIndex := range toolBlocksByIndex {
			toolIndices = append(toolIndices, toolIndex)
		}
		sort.Ints(toolIndices)
		for _, toolIndex := range toolIndices {
			state := toolBlocksByIndex[toolIndex]
			if state == nil || state.started {
				continue
			}
			if state.pendingArgs == "" && state.id == "" && state.name == "" {
				continue
			}
			if state.id == "" {
				state.id = fmt.Sprintf("tool_call_%d", toolIndex)
			}
			if state.name == "" {
				state.name = "unknown_tool"
			}
			state.started = true
			writeSSE(&out, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": state.anthropicIndex,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    state.id,
					"name":  state.name,
					"input": map[string]any{},
				},
			})
			openToolBlockIndices[state.anthropicIndex] = true
			if state.pendingArgs != "" {
				writeInputJSONDelta(state.anthropicIndex, state.pendingArgs)
				state.pendingArgs = ""
			}
		}
	}

	finalize := func() error {
		if !messageStarted || finalized {
			return nil
		}
		closeCurrentNonToolBlock()
		startPendingToolBlocks()
		closeOpenToolBlocks()
		writeSSE(&out, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": pendingStopReason,
			},
			"usage": latestUsage,
		})
		writeSSE(&out, "message_stop", map[string]any{"type": "message_stop"})
		finalized = true
		return flush()
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 128*1024), 128*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			if err := finalize(); err != nil {
				return err
			}
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return err
		}
		if usage, ok := chunk["usage"].(map[string]any); ok {
			latestUsage = openAIUsageToAnthropic(usage)
		}
		if id, _ := chunk["id"].(string); id != "" {
			messageID = id
		}
		if m, _ := chunk["model"].(string); m != "" {
			model = m
		}
		if !messageStarted {
			writeSSE(&out, "message_start", map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":            messageID,
					"type":          "message",
					"role":          "assistant",
					"model":         model,
					"content":       []any{},
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage":         defaultAnthropicUsage(),
				},
			})
			messageStarted = true
			if err := flush(); err != nil {
				return err
			}
		}

		choices, _ := chunk["choices"].([]any)
		if len(choices) > 0 {
			choice, _ := choices[0].(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if reasoning := firstString(delta, "reasoning", "reasoning_content"); reasoning != "" {
				index := startNonToolBlock("thinking")
				writeSSE(&out, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": index,
					"delta": map[string]any{
						"type":     "thinking_delta",
						"thinking": reasoning,
					},
				})
				if err := flush(); err != nil {
					return err
				}
			}
			if text, _ := delta["content"].(string); text != "" {
				index := startNonToolBlock("text")
				writeSSE(&out, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": index,
					"delta": map[string]any{
						"type": "text_delta",
						"text": text,
					},
				})
				if err := flush(); err != nil {
					return err
				}
			}
			if toolCalls, _ := delta["tool_calls"].([]any); len(toolCalls) > 0 {
				closeCurrentNonToolBlock()
				for _, rawToolCall := range toolCalls {
					toolCall, ok := rawToolCall.(map[string]any)
					if !ok {
						continue
					}
					toolIndex := intValue(toolCall["index"], 0)
					state := toolBlocksByIndex[toolIndex]
					if state == nil {
						state = &openAIStreamToolBlockState{
							anthropicIndex: nextContentIndex,
						}
						nextContentIndex++
						toolBlocksByIndex[toolIndex] = state
					}
					if id, _ := toolCall["id"].(string); id != "" {
						state.id = id
					}
					function, _ := toolCall["function"].(map[string]any)
					if name, _ := function["name"].(string); name != "" {
						state.name = name
					}

					shouldStart := !state.started && state.id != "" && state.name != ""
					if shouldStart {
						state.started = true
						writeSSE(&out, "content_block_start", map[string]any{
							"type":  "content_block_start",
							"index": state.anthropicIndex,
							"content_block": map[string]any{
								"type":  "tool_use",
								"id":    state.id,
								"name":  state.name,
								"input": map[string]any{},
							},
						})
						openToolBlockIndices[state.anthropicIndex] = true
						if state.pendingArgs != "" {
							writeInputJSONDelta(state.anthropicIndex, state.pendingArgs)
							state.pendingArgs = ""
						}
					}

					if args := toolArgumentsToString(function["arguments"]); args != "" {
						if state.started {
							writeInputJSONDelta(state.anthropicIndex, args)
						} else {
							state.pendingArgs += args
						}
					}
				}
				if err := flush(); err != nil {
					return err
				}
			}
			if functionCall, ok := delta["function_call"].(map[string]any); ok {
				closeCurrentNonToolBlock()
				state := toolBlocksByIndex[0]
				if state == nil {
					state = &openAIStreamToolBlockState{anthropicIndex: nextContentIndex}
					nextContentIndex++
					toolBlocksByIndex[0] = state
				}
				if id, _ := functionCall["id"].(string); id != "" {
					state.id = id
				}
				if name, _ := functionCall["name"].(string); name != "" {
					state.name = name
				}
				if state.id == "" {
					state.id = "tool_call_0"
				}
				if state.name == "" {
					state.name = "unknown_tool"
				}
				if !state.started {
					state.started = true
					writeSSE(&out, "content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": state.anthropicIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    state.id,
							"name":  state.name,
							"input": map[string]any{},
						},
					})
					openToolBlockIndices[state.anthropicIndex] = true
					if state.pendingArgs != "" {
						writeInputJSONDelta(state.anthropicIndex, state.pendingArgs)
						state.pendingArgs = ""
					}
				}
				if args := toolArgumentsToString(functionCall["arguments"]); args != "" {
					writeInputJSONDelta(state.anthropicIndex, args)
				}
				if err := flush(); err != nil {
					return err
				}
			}
			if finish := openAIChatFinishReasonToAnthropic(choice["finish_reason"]); choice["finish_reason"] != nil && !hasPendingStopReason {
				pendingStopReason = finish
				hasPendingStopReason = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return finalize()
}

type openAIStreamToolBlockState struct {
	anthropicIndex int
	id             string
	name           string
	started        bool
	pendingArgs    string
}

func anthropicMessagesToOpenAI(req map[string]any, options Options) ([]any, error) {
	var out []any
	if system, ok := req["system"]; ok {
		text, err := anthropicContentToText(system)
		if err != nil {
			return nil, fmt.Errorf("convert system: %w", err)
		}
		text = appendClaudeCodeToolUseCompatibilityHint(text, options.ClaudeCodeCompatHint)
		if text != "" {
			out = append(out, map[string]any{
				"role":    "system",
				"content": text,
			})
		}
	}

	rawMessages, _ := req["messages"].([]any)
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("message must be object")
		}
		role, _ := msg["role"].(string)
		blocks, isBlocks := msg["content"].([]any)
		if !isBlocks {
			text, err := anthropicContentToText(msg["content"])
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": role, "content": text})
			continue
		}
		converted, err := anthropicContentBlocksToOpenAIMessages(role, blocks)
		if err != nil {
			return nil, err
		}
		out = append(out, converted...)
	}
	return out, nil
}

func anthropicContentBlocksToOpenAIMessages(role string, blocks []any) ([]any, error) {
	var out []any
	var textParts []any
	var richContent []any
	var toolCalls []any
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			if text, _ := block["text"].(string); text != "" {
				if richContent != nil {
					richContent = append(richContent, map[string]any{"type": "text", "text": text})
				} else {
					textParts = append(textParts, text)
				}
			}
		case "image":
			if richContent == nil {
				for _, text := range textParts {
					richContent = append(richContent, map[string]any{"type": "text", "text": fmt.Sprint(text)})
				}
				textParts = nil
			}
			if image := anthropicImageBlockToOpenAI(block); image != nil {
				richContent = append(richContent, image)
			}
		case "tool_use":
			arguments, err := json.Marshal(valueOrEmptyObject(block["input"]))
			if err != nil {
				return nil, err
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   block["id"],
				"type": "function",
				"function": map[string]any{
					"name":      block["name"],
					"arguments": string(arguments),
				},
			})
		case "tool_result":
			if len(toolCalls) > 0 || richContent != nil || len(textParts) > 0 {
				out = append(out, buildOpenAIAssistantMessage(role, textParts, richContent, toolCalls))
				textParts = nil
				richContent = nil
				toolCalls = nil
			}
			content, err := anthropicContentToText(block["content"])
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": block["tool_use_id"],
				"content":      content,
			})
		}
	}
	if len(toolCalls) > 0 || richContent != nil || len(textParts) > 0 {
		out = append(out, buildOpenAIAssistantMessage(role, textParts, richContent, toolCalls))
	}
	return out, nil
}

func buildOpenAIAssistantMessage(role string, textParts []any, richContent []any, toolCalls []any) map[string]any {
	message := map[string]any{"role": role}
	if richContent != nil {
		for _, text := range textParts {
			richContent = append(richContent, map[string]any{"type": "text", "text": fmt.Sprint(text)})
		}
		message["content"] = richContent
	} else if len(textParts) > 0 {
		message["content"] = joinTextParts(textParts)
	} else if len(toolCalls) > 0 {
		message["content"] = nil
	} else {
		message["content"] = ""
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	return message
}

func anthropicImageBlockToOpenAI(block map[string]any) map[string]any {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return nil
	}
	mediaType, _ := source["media_type"].(string)
	data, _ := source["data"].(string)
	if mediaType == "" || data == "" {
		return nil
	}
	return map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url": "data:" + mediaType + ";base64," + data,
		},
	}
}

func anthropicContentToText(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []any:
		var parts []any
		for _, raw := range v {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "text" {
				if text, _ := block["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return joinTextParts(parts), nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func anthropicToolsToOpenAI(tools []any) []any {
	out := make([]any, 0, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if tool["type"] == "BatchTool" || tool["name"] == "BatchTool" {
			continue
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool["name"],
				"description": tool["description"],
				"parameters":  cleanOpenAISchema(valueOrEmptyObject(tool["input_schema"])),
			},
		})
	}
	return out
}

func anthropicToolChoiceToOpenAI(choice any) any {
	if text, ok := choice.(string); ok {
		if text == "any" {
			return "required"
		}
		return text
	}
	choiceMap, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	switch choiceMap["type"] {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "any":
		return "required"
	case "tool":
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": choiceMap["name"],
			},
		}
	default:
		return choice
	}
}

func openAIChatMessageContentToAnthropic(message map[string]any) []any {
	var content []any
	if reasoning, _ := message["reasoning_content"].(string); reasoning != "" {
		content = append(content, map[string]any{
			"type":     "thinking",
			"thinking": reasoning,
		})
	}
	if text, _ := message["content"].(string); text != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": text,
		})
	} else if parts, ok := message["content"].([]any); ok {
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			switch part["type"] {
			case "text", "output_text":
				if text, _ := part["text"].(string); text != "" {
					content = append(content, map[string]any{"type": "text", "text": text})
				}
			case "refusal":
				if text, _ := part["refusal"].(string); text != "" {
					content = append(content, map[string]any{"type": "text", "text": text})
				}
			}
		}
	}
	if refusal, _ := message["refusal"].(string); refusal != "" {
		content = append(content, map[string]any{
			"type": "text",
			"text": refusal,
		})
	}
	if toolCalls, ok := message["tool_calls"].([]any); ok {
		for _, raw := range toolCalls {
			toolCall, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			function, _ := toolCall["function"].(map[string]any)
			input := map[string]any{}
			switch arguments := function["arguments"].(type) {
			case string:
				if arguments != "" {
					_ = json.Unmarshal([]byte(arguments), &input)
				}
			case map[string]any:
				input = arguments
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    stringValue(toolCall["id"], "tool_call_0"),
				"name":  stringValue(function["name"], "unknown_tool"),
				"input": input,
			})
		}
	}
	if functionCall, ok := message["function_call"].(map[string]any); ok {
		input := map[string]any{}
		switch arguments := functionCall["arguments"].(type) {
		case string:
			if arguments != "" {
				_ = json.Unmarshal([]byte(arguments), &input)
			}
		case map[string]any:
			input = arguments
		}
		if functionCall["name"] != nil || functionCall["arguments"] != nil {
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    stringValue(functionCall["id"], "tool_call_0"),
				"name":  stringValue(functionCall["name"], "unknown_tool"),
				"input": input,
			})
		}
	}
	return content
}

func openAIChatFinishReasonToAnthropic(value any) string {
	switch value {
	case "tool_calls", "function_call":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}

func openAIUsageToAnthropic(usage map[string]any) map[string]any {
	out := defaultAnthropicUsage()
	promptTokens := numberValue(usage["prompt_tokens"])
	cacheReadTokens := numberValue(usage["cache_read_input_tokens"])
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		cacheReadTokens = numberValue(details["cached_tokens"])
	}
	cacheCreationTokens := numberValue(usage["cache_creation_input_tokens"])
	inputTokens := promptTokens - cacheReadTokens - cacheCreationTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	out["input_tokens"] = inputTokens
	copyUsageField(out, usage, "completion_tokens", "output_tokens")
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		copyUsageField(out, details, "cached_tokens", "cache_read_input_tokens")
	}
	if cacheReadTokens > 0 {
		out["cache_read_input_tokens"] = cacheReadTokens
	}
	if cacheCreationTokens > 0 {
		out["cache_creation_input_tokens"] = cacheCreationTokens
	}
	return out
}

func defaultAnthropicUsage() map[string]any {
	return map[string]any{
		"input_tokens":  0,
		"output_tokens": 0,
	}
}

func copyUsageField(dst, src map[string]any, from, to string) {
	if value, ok := src[from]; ok {
		dst[to] = value
	}
}

func writeSSE(out *bytes.Buffer, event string, data map[string]any) {
	encoded, _ := json.Marshal(data)
	out.WriteString("event: ")
	out.WriteString(event)
	out.WriteString("\n")
	out.WriteString("data: ")
	out.Write(encoded)
	out.WriteString("\n\n")
}

func copyIfPresent(dst, src map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func firstString(src map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, _ := src[key].(string); value != "" {
			return value
		}
	}
	return ""
}

func intValue(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return fallback
	}
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	default:
		return 0
	}
}

func toolArgumentsToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any, []any:
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(data)
	default:
		return ""
	}
}

func cleanOpenAISchema(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if key == "format" && child == "uri" {
				continue
			}
			out[key] = cleanOpenAISchema(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			out = append(out, cleanOpenAISchema(child))
		}
		return out
	default:
		return value
	}
}

func joinTextParts(parts []any) string {
	if len(parts) == 0 {
		return ""
	}
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += "\n"
		}
		out += fmt.Sprint(part)
	}
	return out
}

func valueOrEmptyObject(value any) any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func stringValue(value any, fallback string) string {
	if s, ok := value.(string); ok && s != "" {
		return s
	}
	return fallback
}
