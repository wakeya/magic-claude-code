package usage

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

var billingEntrypointPattern = regexp.MustCompile(`cc_entrypoint=([A-Za-z0-9_-]+)`)

func ParseRequestMetadata(body []byte, headers http.Header) RequestMetadata {
	var payload struct {
		Model  string          `json:"model"`
		Stream bool            `json:"stream"`
		System json.RawMessage `json:"system"`
	}
	_ = json.Unmarshal(body, &payload)

	ua := ""
	if headers != nil {
		ua = TruncateUserAgent(headers.Get("User-Agent"))
	}
	entrypoint := parseBillingEntrypoint(payload.System)
	sourceApp := "unknown"
	if entrypoint != "" {
		sourceApp = "claude_code"
	} else if ep := entrypointFromUserAgent(ua); ep != "" {
		sourceApp = "claude_code"
		entrypoint = ep
	}

	return RequestMetadata{
		OriginalModel:    payload.Model,
		Stream:           payload.Stream,
		SourceApp:        sourceApp,
		SourceEntrypoint: entrypoint,
		UserAgent:        ua,
	}
}

func ExtractUsageFromJSON(body []byte) (UsageValues, string, string) {
	var payload struct {
		Usage map[string]int64 `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return UsageValues{}, UsageSourceNone, ParseStatusParseError
	}
	if len(payload.Usage) == 0 {
		return UsageValues{}, UsageSourceNone, ParseStatusMissing
	}

	values := UsageValues{
		InputTokens:              payload.Usage["input_tokens"],
		OutputTokens:             payload.Usage["output_tokens"],
		CacheCreationInputTokens: payload.Usage["cache_creation_input_tokens"],
		CacheReadInputTokens:     payload.Usage["cache_read_input_tokens"],
	}
	values.HasAny = values.InputTokens != 0 ||
		values.OutputTokens != 0 ||
		values.CacheCreationInputTokens != 0 ||
		values.CacheReadInputTokens != 0
	if !values.HasAny {
		return UsageValues{}, UsageSourceNone, ParseStatusMissing
	}
	return values, UsageSourceProvider, ParseStatusOK
}

func parseBillingEntrypoint(system json.RawMessage) string {
	if len(system) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(system, &text); err == nil {
		return extractEntrypoint(text)
	}

	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(system, &blocks); err != nil {
		return ""
	}
	for _, block := range blocks {
		if entrypoint := extractEntrypoint(block.Text); entrypoint != "" {
			return entrypoint
		}
	}
	return ""
}

func extractEntrypoint(text string) string {
	match := billingEntrypointPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func entrypointFromUserAgent(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "claude-vscode"), strings.Contains(lower, "vscode"):
		return "claude-vscode"
	case strings.Contains(lower, "claude-code"):
		return "cli"
	default:
		return ""
	}
}
