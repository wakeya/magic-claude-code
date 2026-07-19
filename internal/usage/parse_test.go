package usage

import (
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestParseRequestMetadataExtractsModelsAndStream(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"stream":true,
		"system":"You are Claude Code. x-anthropic-billing-header: cc_entrypoint=cli",
		"messages":[{"role":"user","content":"hello"}]
	}`)
	headers := http.Header{"User-Agent": {"claude-code/1.2.3"}}

	got := ParseRequestMetadata(body, headers)

	if got.OriginalModel != "claude-sonnet-4-5" {
		t.Fatalf("OriginalModel = %q", got.OriginalModel)
	}
	if !got.Stream {
		t.Fatal("expected Stream to be true")
	}
	if got.SourceApp != "claude_code" {
		t.Fatalf("SourceApp = %q", got.SourceApp)
	}
	if got.SourceEntrypoint != "cli" {
		t.Fatalf("SourceEntrypoint = %q", got.SourceEntrypoint)
	}
}

func TestParseSourceEntryPointFromBillingHeader(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":[
			{"type":"text","text":"x-anthropic-billing-header: cc_entrypoint=claude-vscode"}
		],
		"messages":[{"role":"user","content":"hello"}]
	}`)

	got := ParseRequestMetadata(body, nil)

	if got.SourceApp != "claude_code" {
		t.Fatalf("SourceApp = %q", got.SourceApp)
	}
	if got.SourceEntrypoint != "claude-vscode" {
		t.Fatalf("SourceEntrypoint = %q", got.SourceEntrypoint)
	}
}

func TestParseSourceEntryPointFallsBackToUserAgent(t *testing.T) {
	headers := http.Header{"User-Agent": {"claude-code/1.2.3 claude-vscode"}}

	got := ParseRequestMetadata([]byte(`{"model":"claude-opus-4-6","messages":[]}`), headers)

	if got.SourceApp != "claude_code" {
		t.Fatalf("SourceApp = %q", got.SourceApp)
	}
	if got.SourceEntrypoint != "claude-vscode" {
		t.Fatalf("SourceEntrypoint = %q", got.SourceEntrypoint)
	}
	if !strings.Contains(got.UserAgent, "claude-code") {
		t.Fatalf("UserAgent = %q", got.UserAgent)
	}
}

func TestExtractUsageFromNonStreamingResponse(t *testing.T) {
	body := []byte(`{
		"id":"msg_123",
		"type":"message",
		"usage":{
			"input_tokens":10,
			"output_tokens":20,
			"cache_creation_input_tokens":3,
			"cache_read_input_tokens":7
		}
	}`)

	values, source, status := ExtractUsageFromJSON(body)

	if source != UsageSourceProvider {
		t.Fatalf("source = %q", source)
	}
	if status != ParseStatusOK {
		t.Fatalf("status = %q", status)
	}
	if !values.HasAny {
		t.Fatal("expected HasAny")
	}
	if values.InputTokens != 10 || values.OutputTokens != 20 || values.CacheCreationInputTokens != 3 || values.CacheReadInputTokens != 7 {
		t.Fatalf("unexpected usage values: %#v", values)
	}
}

func TestMissingUsageReturnsMissingStatus(t *testing.T) {
	values, source, status := ExtractUsageFromJSON([]byte(`{"id":"msg_123","type":"message"}`))

	if values.HasAny {
		t.Fatalf("expected empty values, got %#v", values)
	}
	if source != UsageSourceNone {
		t.Fatalf("source = %q", source)
	}
	if status != ParseStatusMissing {
		t.Fatalf("status = %q", status)
	}
}

func TestExtractUsageToleratesNonNumericFields(t *testing.T) {
	// 智谱（bigmodel）真实响应形态：usage 里混入对象（server_tool_use）与
	// 字符串（service_tier），官方 Anthropic API 也会返回这两个字段。
	// 此前 map[string]int64 解析直接失败，导致全部非流式请求 parse_error。
	body := []byte(`{
		"id":"msg_123",
		"type":"message",
		"usage":{
			"input_tokens":6,
			"output_tokens":1,
			"cache_read_input_tokens":0,
			"server_tool_use":{"web_search_requests":0},
			"service_tier":"standard"
		}
	}`)

	values, source, status := ExtractUsageFromJSON(body)

	if source != UsageSourceProvider || status != ParseStatusOK {
		t.Fatalf("source/status = %q/%q", source, status)
	}
	if values.InputTokens != 6 || values.OutputTokens != 1 {
		t.Fatalf("values = %#v", values)
	}
}

func TestExtractUsageToleratesNumericStringsAndFloats(t *testing.T) {
	body := []byte(`{
		"usage":{
			"input_tokens":"10",
			"output_tokens":20.0,
			"cache_creation_input_tokens":"3",
			"cache_read_input_tokens":7
		}
	}`)

	values, source, status := ExtractUsageFromJSON(body)

	if source != UsageSourceProvider || status != ParseStatusOK {
		t.Fatalf("source/status = %q/%q", source, status)
	}
	if values.InputTokens != 10 || values.OutputTokens != 20 || values.CacheCreationInputTokens != 3 || values.CacheReadInputTokens != 7 {
		t.Fatalf("values = %#v", values)
	}
}

func TestExtractUsageNullAndZeroAndJunkUsage(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"null usage", `{"usage":null}`},
		{"empty usage object", `{"usage":{}}`},
		{"all zero", `{"usage":{"input_tokens":0,"output_tokens":0}}`},
		{"usage wrong type", `{"usage":"n/a"}`},
		{"only non-numeric fields", `{"usage":{"service_tier":"standard"}}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			values, source, status := ExtractUsageFromJSON([]byte(tt.body))
			if values.HasAny {
				t.Fatalf("expected empty values, got %#v", values)
			}
			if source != UsageSourceNone || status != ParseStatusMissing {
				t.Fatalf("source/status = %q/%q, want none/missing", source, status)
			}
		})
	}
}

func TestExtractUsageInvalidJSONReturnsParseError(t *testing.T) {
	_, source, status := ExtractUsageFromJSON([]byte(`{bad json`))

	if source != UsageSourceNone || status != ParseStatusParseError {
		t.Fatalf("source/status = %q/%q, want none/parse_error", source, status)
	}
}

func TestExtractUsageRejectsOutOfRangeNumbers(t *testing.T) {
	// 超出 int64 范围的值按垃圾字段忽略，而不是存一个溢出后的未定义值。
	// 唯一字段越界 -> missing。
	values, source, status := ExtractUsageFromJSON([]byte(`{"usage":{"input_tokens":1e300}}`))
	if values.HasAny || source != UsageSourceNone || status != ParseStatusMissing {
		t.Fatalf("huge-only: source/status = %q/%q values = %#v", source, status, values)
	}

	// 越界字段被忽略，其余正常字段仍提取。
	values, source, status = ExtractUsageFromJSON([]byte(`{"usage":{"input_tokens":1e300,"output_tokens":5}}`))
	if source != UsageSourceProvider || status != ParseStatusOK {
		t.Fatalf("mixed: source/status = %q/%q", source, status)
	}
	if values.InputTokens != 0 || values.OutputTokens != 5 {
		t.Fatalf("mixed: values = %#v", values)
	}
}

func TestExtractUsageInt64Boundary(t *testing.T) {
	// math.MaxInt64 作为整数必须精确接受：不能因 float64 比较（float64(MaxInt64)==2^63）
	// 被误拒，也不能 int64() 后溢出。旧实现的 f > MaxInt64 检查两者都做不到。
	values, source, status := ExtractUsageFromJSON([]byte(`{"usage":{"input_tokens":9223372036854775807}}`))
	if source != UsageSourceProvider || status != ParseStatusOK {
		t.Fatalf("MaxInt64: source/status = %q/%q", source, status)
	}
	if values.InputTokens != math.MaxInt64 {
		t.Fatalf("MaxInt64: InputTokens = %d, want %d", values.InputTokens, math.MaxInt64)
	}

	// 2^63 超出 int64 范围，按垃圾忽略，不得溢出成负数污染统计。
	values, _, status = ExtractUsageFromJSON([]byte(`{"usage":{"input_tokens":9223372036854775808}}`))
	if status != ParseStatusMissing || values.HasAny {
		t.Fatalf("2^63: status = %q values = %#v, want ignored as junk", status, values)
	}
}
