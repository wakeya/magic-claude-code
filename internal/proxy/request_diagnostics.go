package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"unicode/utf8"
)

const maxDiagnosticSchemaDepth = 32

var diagnosticKnownToolNames = map[string]struct{}{
	"ToolSearch": {},
	"WebFetch":   {},
	"WebSearch":  {},
}

var diagnosticKnownRoles = map[string]struct{}{
	"user":      {},
	"assistant": {},
	"system":    {},
	"tool":      {},
}

var diagnosticKnownContentTypes = map[string]struct{}{
	"text":                   {},
	"image":                  {},
	"document":               {},
	"tool_use":               {},
	"tool_result":            {},
	"thinking":               {},
	"redacted_thinking":      {},
	"server_tool_use":        {},
	"tool_reference":         {},
	"web_search_tool_result": {},
}

var diagnosticTopLevelFields = map[string]struct{}{
	"model":             {},
	"stream":            {},
	"max_tokens":        {},
	"max_output_tokens": {},
	"temperature":       {},
	"top_p":             {},
	"top_k":             {},
	"messages":          {},
	"tools":             {},
	"input":             {},
	"system":            {},
	"metadata":          {},
	"thinking":          {},
}

// summarizeRequestParams returns bounded protocol structure for error logs.
// It never copies prompt-bearing request content into the summary.
func summarizeRequestParams(body []byte) string {
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		return fmt.Sprintf("<%d bytes, not JSON>", len(body))
	}

	summary := map[string]any{
		"body_bytes": len(body),
		"stream":     summarizeStreamPresence(req),
	}
	if model, ok := req["model"].(string); ok {
		summary["model"] = model
	}
	for _, key := range []string{
		"max_tokens",
		"max_output_tokens",
		"temperature",
		"top_p",
		"top_k",
	} {
		if value, ok := req[key].(float64); ok {
			summary[key] = value
		}
	}
	if value, ok := req["messages"]; ok {
		summary["messages"] = summarizeMessages(value)
	}
	if value, ok := req["tools"]; ok {
		summary["tools"] = summarizeTools(value)
	}
	for _, key := range []string{"system", "metadata", "thinking", "input"} {
		if value, ok := req[key]; ok {
			summary[key] = summarizeValueShape(value)
		}
	}

	unknown := 0
	for key := range req {
		if _, ok := diagnosticTopLevelFields[key]; !ok {
			unknown++
		}
	}
	if unknown > 0 {
		summary["unknown_top_level_fields"] = unknown
	}

	out, _ := json.Marshal(summary)
	return string(out)
}

func summarizeStreamPresence(req map[string]any) map[string]any {
	value, present := req["stream"]
	summary := map[string]any{"present": present}
	if !present {
		return summary
	}
	if stream, ok := value.(bool); ok {
		summary["value"] = stream
		return summary
	}
	summary["type"] = diagnosticJSONType(value)
	return summary
}

func summarizeValueShape(value any) map[string]any {
	summary := map[string]any{"type": diagnosticJSONType(value)}
	switch typed := value.(type) {
	case string:
		summary["chars"] = utf8.RuneCountInString(typed)
	case []any:
		summary["items"] = len(typed)
	case map[string]any:
		summary["keys"] = len(typed)
	}
	return summary
}

func diagnosticJSONType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "other"
	}
}

func summarizeMessages(value any) map[string]any {
	messages, ok := value.([]any)
	if !ok {
		return summarizeValueShape(value)
	}

	summary := map[string]any{"count": len(messages)}
	roles := make(map[string]int)
	contentTypes := make(map[string]int)
	for _, value := range messages {
		message, ok := value.(map[string]any)
		if !ok {
			roles["other"]++
			contentTypes["other"]++
			continue
		}
		role, ok := message["role"].(string)
		if !ok {
			roles["other"]++
		} else if _, known := diagnosticKnownRoles[role]; known {
			roles[role]++
		} else {
			roles["other"]++
		}

		switch content := message["content"].(type) {
		case string:
			contentTypes["text"]++
		case []any:
			for _, blockValue := range content {
				block, ok := blockValue.(map[string]any)
				if !ok {
					contentTypes["other"]++
					continue
				}
				blockType, ok := block["type"].(string)
				if !ok {
					contentTypes["other"]++
				} else if _, known := diagnosticKnownContentTypes[blockType]; known {
					contentTypes[blockType]++
				} else {
					contentTypes["other"]++
				}
			}
		default:
			contentTypes["other"]++
		}
	}
	if len(roles) > 0 {
		summary["roles"] = roles
	}
	if len(contentTypes) > 0 {
		summary["content_types"] = contentTypes
	}
	return summary
}

func summarizeTools(value any) map[string]any {
	tools, ok := value.([]any)
	if !ok {
		return summarizeValueShape(value)
	}

	summary := map[string]any{"count": len(tools)}
	names := make([]string, 0, len(tools))
	knownNames := make(map[string]struct{})
	stats := make(map[string]int)
	cacheControlCount := 0
	for _, value := range tools {
		tool, ok := value.(map[string]any)
		if !ok {
			names = append(names, "")
			continue
		}
		name, _ := tool["name"].(string)
		names = append(names, name)
		if _, known := diagnosticKnownToolNames[name]; known {
			knownNames[name] = struct{}{}
		}
		if _, present := tool["cache_control"]; present {
			cacheControlCount++
		}
		if schema, ok := tool["input_schema"].(map[string]any); ok {
			if properties, ok := schema["properties"].(map[string]any); ok {
				stats["root_properties"] += len(properties)
			}
			if required, ok := schema["required"].([]any); ok {
				stats["root_required"] += len(required)
			}
			collectSchemaDiagnostics(schema, 0, stats)
		}
	}

	summary["names_sha256"] = digestToolNames(names)
	if len(knownNames) > 0 {
		sorted := make([]string, 0, len(knownNames))
		for name := range knownNames {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		summary["known_names"] = sorted
	}
	if cacheControlCount > 0 {
		summary["cache_control"] = cacheControlCount
	}
	if len(stats) > 0 {
		summary["schemas"] = stats
	}
	return summary
}

func collectSchemaDiagnostics(value any, depth int, stats map[string]int) {
	if depth > maxDiagnosticSchemaDepth {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch key {
			case "$schema":
				stats["schema"]++
				if schema, ok := child.(string); ok && schema == "https://json-schema.org/draft/2020-12/schema" {
					stats["draft_2020_12"]++
				}
			case "additionalProperties":
				switch child.(type) {
				case bool:
					if child == false {
						stats["additional_properties_false"]++
					} else {
						stats["additional_properties_true"]++
					}
				case map[string]any:
					stats["additional_properties_schema"]++
				default:
					stats["additional_properties_other"]++
				}
			case "format":
				stats["format"]++
			case "minLength":
				stats["min_length"]++
			case "maxLength":
				stats["max_length"]++
			case "oneOf":
				stats["one_of"]++
			case "anyOf":
				stats["any_of"]++
			case "allOf":
				stats["all_of"]++
			case "$ref":
				stats["ref"]++
			}
			collectSchemaDiagnostics(child, depth+1, stats)
		}
	case []any:
		for _, child := range typed {
			collectSchemaDiagnostics(child, depth+1, stats)
		}
	}
}

func digestToolNames(names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	hash := sha256.New()
	for _, name := range sorted {
		_, _ = fmt.Fprintf(hash, "%d:", len(name))
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func summarizeUpstreamQuery(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "beta=other,other_count=0"
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "beta=other,other_count=0"
	}

	betaState := "absent"
	if values, present := query["beta"]; present {
		if len(values) != 1 {
			betaState = "other"
		} else {
			switch values[0] {
			case "true":
				betaState = "true"
			case "false":
				betaState = "false"
			default:
				betaState = "other"
			}
		}
	}

	otherCount := 0
	for key, values := range query {
		if key != "beta" {
			otherCount += len(values)
		}
	}
	return fmt.Sprintf("beta=%s,other_count=%d", betaState, otherCount)
}

func formatUpstreamLogTarget(rawURL string) string {
	return fmt.Sprintf("%s | query: %s", redactUpstreamURL(rawURL), summarizeUpstreamQuery(rawURL))
}
