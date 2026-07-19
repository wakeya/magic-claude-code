package usage

import (
	"encoding/json"
	"math"
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
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return UsageValues{}, UsageSourceNone, ParseStatusParseError
	}
	if len(payload.Usage) == 0 {
		return UsageValues{}, UsageSourceNone, ParseStatusMissing
	}

	values := extractUsageValues(payload.Usage)
	if !values.HasAny {
		return UsageValues{}, UsageSourceNone, ParseStatusMissing
	}
	return values, UsageSourceProvider, ParseStatusOK
}

// usageCounterKeys 是 mcc 采集的四个 Anthropic usage 计数器键名。
var usageCounterKeys = []string{
	"input_tokens",
	"output_tokens",
	"cache_creation_input_tokens",
	"cache_read_input_tokens",
}

// extractUsageValues 从原始 usage 对象宽松提取四个计数器。
// 值类型容忍 provider 漂移：JSON 数字、浮点（174.0）、数字字符串（"174"）
// 都接受；嵌套对象（server_tool_use）、非数字字符串（service_tier）、null
// 等一律忽略——与 Anthropic SDK 忽略未知 usage 字段的策略一致。
func extractUsageValues(raw json.RawMessage) UsageValues {
	fields := parseUsageFields(raw)
	values := UsageValues{
		InputTokens:              fields["input_tokens"],
		OutputTokens:             fields["output_tokens"],
		CacheCreationInputTokens: fields["cache_creation_input_tokens"],
		CacheReadInputTokens:     fields["cache_read_input_tokens"],
	}
	values.HasAny = values.InputTokens != 0 ||
		values.OutputTokens != 0 ||
		values.CacheCreationInputTokens != 0 ||
		values.CacheReadInputTokens != 0
	return values
}

// parseUsageFields 返回 usage 对象中"存在且为可用数字"的计数器子集。
// 字段存在但值为非数字（对象/非数字字符串）时不入 map，调用方据此区分
// "缺失/垃圾值"与真实的 0。
func parseUsageFields(raw json.RawMessage) map[string]int64 {
	fields := make(map[string]int64, len(usageCounterKeys))
	if len(raw) == 0 {
		return fields
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawFields); err != nil {
		return fields
	}
	for _, key := range usageCounterKeys {
		if v, ok := usageFieldInt64(rawFields[key]); ok {
			fields[key] = v
		}
	}
	return fields
}

// usageFieldInt64 读取单个 usage 计数器。json.Number 同时接受 JSON 数字与
// 合法数字字符串，Float64 往返兼容浮点编码（174.0 → 174）。第二个返回值
// 表示该字段是否持有可用数字。超出 int64 表示范围的值（如 1e300）按垃圾
// 字段忽略——直接 int64(f) 转换溢出会得到平台相关的未定义值，污染统计。
func usageFieldInt64(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	// 整数优先：strconv.ParseInt 精确解析，正确接受 math.MaxInt64、拒绝超范围整数。
	if i, err := n.Int64(); err == nil {
		return i, true
	}
	// 回退：浮点编码（174.0）或 Int64 装不下的值。Float64 兼容浮点；超出 int64 范围
	// （含 2^63，float64 下与 MaxInt64 同值）按垃圾忽略，避免 int64() 溢出成实现相关的
	// 负数。合法 MaxInt64/MinInt64 整数已由上面的 Int64() 精确处理，不会落到这里。
	f, err := n.Float64()
	if err != nil {
		return 0, false
	}
	if f >= float64(math.MaxInt64) || f <= float64(math.MinInt64) {
		return 0, false
	}
	return int64(f), true
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
