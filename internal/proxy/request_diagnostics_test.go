package proxy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestSummarizeRequestParamsProtocolStructure(t *testing.T) {
	body := []byte(`{
		"model":"glm-5.2",
		"max_tokens":64000,
		"messages":[
			{"role":"user","content":"secret-user-text"},
			{"role":"assistant","content":[{"type":"tool_use","name":"secret-call-name","input":{"secret":"value"}}]},
			{"role":"user","content":[{"type":"tool_result","content":"secret-result"},{"type":"tool_reference","tool_name":"WebFetch"}]}
		],
		"tools":[
			{
				"name":"WebFetch",
				"description":"secret-web-description",
				"input_schema":{
					"$schema":"https://json-schema.org/draft/2020-12/schema",
					"type":"object",
					"properties":{"url":{"type":"string","format":"uri"}},
					"required":["url"],
					"additionalProperties":false
				}
			},
			{
				"name":"secret-custom-tool",
				"description":"secret-custom-description",
				"input_schema":{"type":"object","properties":{"secret-property":{"type":"string","description":"secret-schema-description"}}}
			}
		],
		"system":"secret-system-prompt",
		"metadata":{"secret-user-id":"secret-metadata-value"},
		"unknown_secret_extension":{"secret":"secret-extension-value"}
	}`)

	raw := summarizeRequestParams(body)
	got := decodeDiagnosticSummary(t, raw)

	if got["body_bytes"] != float64(len(body)) {
		t.Fatalf("body_bytes = %v, want %d", got["body_bytes"], len(body))
	}
	if got["model"] != "glm-5.2" || got["max_tokens"] != float64(64000) {
		t.Fatalf("safe scalars missing: %s", raw)
	}
	stream := requireDiagnosticMap(t, got, "stream")
	if stream["present"] != false || len(stream) != 1 {
		t.Fatalf("stream = %#v, want present=false only", stream)
	}

	messages := requireDiagnosticMap(t, got, "messages")
	if messages["count"] != float64(3) {
		t.Fatalf("messages.count = %v", messages["count"])
	}
	roles := requireDiagnosticMap(t, messages, "roles")
	if roles["user"] != float64(2) || roles["assistant"] != float64(1) {
		t.Fatalf("roles = %#v", roles)
	}
	contentTypes := requireDiagnosticMap(t, messages, "content_types")
	for key, want := range map[string]float64{
		"text": 1, "tool_use": 1, "tool_result": 1, "tool_reference": 1,
	} {
		if contentTypes[key] != want {
			t.Fatalf("content_types[%q] = %v, want %v; all=%#v", key, contentTypes[key], want, contentTypes)
		}
	}

	tools := requireDiagnosticMap(t, got, "tools")
	if tools["count"] != float64(2) {
		t.Fatalf("tools.count = %v", tools["count"])
	}
	knownNames, ok := tools["known_names"].([]any)
	if !ok || len(knownNames) != 1 || knownNames[0] != "WebFetch" {
		t.Fatalf("known_names = %#v", tools["known_names"])
	}
	digest, ok := tools["names_sha256"].(string)
	if !ok || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
		t.Fatalf("names_sha256 = %q", digest)
	}
	schemas := requireDiagnosticMap(t, tools, "schemas")
	for key, want := range map[string]float64{
		"draft_2020_12":               1,
		"additional_properties_false": 1,
		"format":                      1,
		"root_properties":             2,
		"root_required":               1,
	} {
		if schemas[key] != want {
			t.Fatalf("schemas[%q] = %v, want %v; all=%#v", key, schemas[key], want, schemas)
		}
	}

	system := requireDiagnosticMap(t, got, "system")
	if system["type"] != "string" || system["chars"] != float64(20) {
		t.Fatalf("system = %#v", system)
	}
	metadata := requireDiagnosticMap(t, got, "metadata")
	if metadata["type"] != "object" || metadata["keys"] != float64(1) {
		t.Fatalf("metadata = %#v", metadata)
	}
	if got["unknown_top_level_fields"] != float64(1) {
		t.Fatalf("unknown_top_level_fields = %v", got["unknown_top_level_fields"])
	}

	for _, secret := range []string{
		"secret-user-text",
		"secret-call-name",
		"secret-result",
		"secret-web-description",
		"secret-custom-tool",
		"secret-custom-description",
		"secret-property",
		"secret-schema-description",
		"secret-system-prompt",
		"secret-user-id",
		"secret-metadata-value",
		"unknown_secret_extension",
		"secret-extension-value",
	} {
		if strings.Contains(raw, secret) {
			t.Fatalf("diagnostic summary leaked %q: %s", secret, raw)
		}
	}
}

func TestSummarizeRequestParamsStreamPresence(t *testing.T) {
	tests := []struct {
		name string
		body string
		want map[string]any
	}{
		{name: "absent", body: `{"model":"m"}`, want: map[string]any{"present": false}},
		{name: "true", body: `{"stream":true}`, want: map[string]any{"present": true, "value": true}},
		{name: "false", body: `{"stream":false}`, want: map[string]any{"present": true, "value": false}},
		{name: "wrong type", body: `{"stream":"secret-stream-value"}`, want: map[string]any{"present": true, "type": "string"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := summarizeRequestParams([]byte(tt.body))
			got := requireDiagnosticMap(t, decodeDiagnosticSummary(t, raw), "stream")
			if !diagnosticMapsEqual(got, tt.want) {
				t.Fatalf("stream = %#v, want %#v", got, tt.want)
			}
			if strings.Contains(raw, "secret-stream-value") {
				t.Fatalf("wrong-type stream value leaked: %s", raw)
			}
		})
	}
}

func TestSummarizeRequestParamsStableToolDigest(t *testing.T) {
	bodyA := []byte(`{"tools":[{"name":"B"},{"name":"WebSearch"},{"name":"A"}]}`)
	bodyB := []byte(`{"tools":[{"name":"A"},{"name":"B"},{"name":"WebSearch"}]}`)

	digestA := requireDiagnosticMap(t, decodeDiagnosticSummary(t, summarizeRequestParams(bodyA)), "tools")["names_sha256"]
	digestB := requireDiagnosticMap(t, decodeDiagnosticSummary(t, summarizeRequestParams(bodyB)), "tools")["names_sha256"]
	if digestA != digestB {
		t.Fatalf("tool digest depends on order: %v != %v", digestA, digestB)
	}
}

func TestSummarizeRequestParamsBoundsLargeCollections(t *testing.T) {
	messages := make([]any, 0, 500)
	tools := make([]any, 0, 500)
	for i := 0; i < 500; i++ {
		secret := fmt.Sprintf("generated-secret-%03d", i)
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": secret,
		})
		tools = append(tools, map[string]any{
			"name":        secret,
			"description": secret,
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{secret: map[string]any{"type": "string"}},
			},
		})
	}
	body, err := json.Marshal(map[string]any{"messages": messages, "tools": tools})
	if err != nil {
		t.Fatal(err)
	}

	raw := summarizeRequestParams(body)
	if len(raw) >= 4096 {
		t.Fatalf("summary is not bounded: %d bytes", len(raw))
	}
	if strings.Contains(raw, "generated-secret-") {
		t.Fatalf("large summary leaked collection content: %s", raw)
	}
}

func TestSummarizeUpstreamQuery(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "no query", url: "https://example.com/v1/messages", want: "beta=absent,other_count=0"},
		{name: "beta true", url: "https://example.com/v1/messages?beta=true", want: "beta=true,other_count=0"},
		{name: "beta false", url: "https://example.com/v1/messages?beta=false", want: "beta=false,other_count=0"},
		{name: "unexpected beta", url: "https://example.com/v1/messages?beta=unexpected", want: "beta=other,other_count=0"},
		{name: "repeated beta", url: "https://example.com/v1/messages?beta=true&beta=true", want: "beta=other,other_count=0"},
		{
			name: "secret non-beta values",
			url:  "https://example.com/v1/messages?beta=true&token=secret-query-value&signature=secret-signature",
			want: "beta=true,other_count=2",
		},
		{name: "invalid URL", url: "https://example.com/%zz?beta=true", want: "beta=other,other_count=0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeUpstreamQuery(tt.url)
			if got != tt.want {
				t.Fatalf("summarizeUpstreamQuery() = %q, want %q", got, tt.want)
			}
			for _, secret := range []string{"token", "signature", "secret-query-value", "secret-signature", "unexpected"} {
				if strings.Contains(got, secret) {
					t.Fatalf("query summary leaked %q: %s", secret, got)
				}
			}
		})
	}
}

func decodeDiagnosticSummary(t *testing.T, raw string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("summary is not JSON: %v; raw=%q", err, raw)
	}
	return got
}

func requireDiagnosticMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, parent[key])
	}
	return value
}

func diagnosticMapsEqual(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			return false
		}
	}
	return true
}
