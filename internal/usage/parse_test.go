package usage

import (
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
