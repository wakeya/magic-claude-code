package proxy

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"magic-claude-code/internal/cert"
	"magic-claude-code/internal/config"
	"magic-claude-code/internal/usage"
)

func TestProxyHandler(t *testing.T) {
	// 创建模拟后端
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证 header 透传
		if r.Header.Get("X-Custom-Header") != "test-value" {
			t.Error("expected custom header to be forwarded")
		}

		w.Header().Set("X-Backend-Header", "backend-value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	// 创建配置存储
	cfg := &config.Config{
		BackendURL: backend.URL,
	}
	store := config.NewMockStore(cfg)

	// 创建 transport
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
		},
	}

	// 创建代理处理器
	handler := NewHandler(store, transport)

	// 创建测试请求
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	req.Header.Set("X-Custom-Header", "test-value")

	// 执行请求
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 验证响应
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if rec.Header().Get("X-Backend-Header") != "backend-value" {
		t.Error("expected backend header to be returned")
	}

	body, _ := io.ReadAll(rec.Body)
	if string(body) != "backend response" {
		t.Errorf("expected 'backend response', got %s", string(body))
	}
}

func TestProxyLogsIncludeProviderContext(t *testing.T) {
	var buf bytes.Buffer
	oldOutput := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer backend.Close()

	provider := testProxyProvider(backend.URL)
	handler := NewHandler(config.NewMockStore(testProxyConfig(provider)), http.DefaultTransport.(*http.Transport))
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet",
		"stream":false,
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	logs := buf.String()
	if !strings.Contains(logs, `provider_name="Provider A"`) {
		t.Fatalf("expected provider name in logs, got:\n%s", logs)
	}
	if !strings.Contains(logs, `upstream_url="`) {
		t.Fatalf("expected upstream url in logs, got:\n%s", logs)
	}
	if !strings.Contains(logs, backend.URL) {
		t.Fatalf("expected upstream base url in logs, got:\n%s", logs)
	}
}

func TestProxyBackendError(t *testing.T) {
	// 创建模拟后端返回错误
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		BackendURL: backend.URL,
	}
	store := config.NewMockStore(cfg)

	// 创建 transport
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
		},
	}

	handler := NewHandler(store, transport)

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 应透传错误状态码
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

func TestProxyRecordsNonStreamingProviderUsage(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":3,"cache_read_input_tokens":2}}`))
	}))
	defer backend.Close()

	provider := testProxyProvider(backend.URL)
	handler := NewHandler(config.NewMockStore(testProxyConfig(provider)), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet",
		"stream":false,
		"system":"x-anthropic-billing-header: cc_entrypoint=cli",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	req.Header.Set("User-Agent", "claude-code/1.0")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	record := recorder.onlyRecord(t)
	if record.req.ProviderID != provider.ID || record.req.ProviderName != provider.Name {
		t.Fatalf("provider snapshot = %#v", record.req)
	}
	if record.req.OriginalModel != "claude-sonnet" || record.req.MappedModel != "mapped-sonnet" {
		t.Fatalf("models = %q/%q", record.req.OriginalModel, record.req.MappedModel)
	}
	if record.req.SourceEntrypoint != "cli" {
		t.Fatalf("SourceEntrypoint = %q", record.req.SourceEntrypoint)
	}
	if record.tok.UsageSource != usage.UsageSourceProvider || record.tok.UsageParseStatus != usage.ParseStatusOK {
		t.Fatalf("token status = %#v", record.tok)
	}
	if record.tok.InputTokens != 10 || record.tok.OutputTokens != 5 || record.tok.CacheCreationInputTokens != 3 || record.tok.CacheReadInputTokens != 2 {
		t.Fatalf("tokens = %#v", record.tok)
	}
}

func TestProxyOpenAIChatProviderRewritesEndpointAndConvertsResponse(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("backend path = %q, want /v1/chat/completions", r.URL.Path)
		}
		// Verify Anthropic headers are stripped for non-Anthropic providers
		if v := r.Header.Get("Anthropic-Version"); v != "" {
			t.Fatalf("Anthropic-Version should be stripped, got %q", v)
		}
		if v := r.Header.Get("Anthropic-Beta"); v != "" {
			t.Fatalf("Anthropic-Beta should be stripped, got %q", v)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read backend request: %v", err)
		}
		var captured map[string]any
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode backend request: %v body=%s", err, body)
		}
		if captured["model"] != "mapped-sonnet" {
			t.Fatalf("model = %#v, want mapped-sonnet", captured["model"])
		}
		messages := captured["messages"].([]any)
		if messages[0].(map[string]any)["role"] != "user" || messages[0].(map[string]any)["content"] != "hello" {
			t.Fatalf("messages = %#v", messages)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_1",
			"model":"mapped-sonnet",
			"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":4,"completion_tokens":2}
		}`))
	}))
	defer backend.Close()

	provider := testProxyProvider(backend.URL + "/v1")
	provider.APIFormat = config.APIFormatOpenAIChat
	handler := NewHandler(config.NewMockStore(testProxyConfig(provider)), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet",
		"stream":false,
		"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got["type"] != "message" {
		t.Fatalf("response was not converted to Anthropic message: %#v", got)
	}
	content := got["content"].([]any)
	if content[0].(map[string]any)["text"] != "hi" {
		t.Fatalf("content = %#v", content)
	}
	record := recorder.onlyRecord(t)
	if record.tok.InputTokens != 4 || record.tok.OutputTokens != 2 {
		t.Fatalf("tokens = %#v", record.tok)
	}
}

func TestProxyOpenAIChatProviderConvertsStreamingResponse(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	openAISSE := "data: {\"id\":\"chatcmpl_1\",\"model\":\"mapped-sonnet\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n" +
		"data: [DONE]\n\n"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("backend path = %q, want /v1/chat/completions", r.URL.Path)
		}
		// Verify Anthropic headers are stripped for non-Anthropic providers
		if v := r.Header.Get("Anthropic-Version"); v != "" {
			t.Fatalf("Anthropic-Version should be stripped, got %q", v)
		}
		if v := r.Header.Get("Anthropic-Beta"); v != "" {
			t.Fatalf("Anthropic-Beta should be stripped, got %q", v)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(openAISSE))
	}))
	defer backend.Close()

	provider := testProxyProvider(backend.URL + "/v1")
	provider.APIFormat = config.APIFormatOpenAIChat
	handler := NewHandler(config.NewMockStore(testProxyConfig(provider)), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet",
		"stream":true,
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: message_start") || !strings.Contains(body, `"text":"hi"`) || !strings.Contains(body, "event: message_stop") {
		t.Fatalf("response was not converted to Anthropic SSE:\n%s", body)
	}
	record := recorder.onlyRecord(t)
	if record.tok.InputTokens != 4 || record.tok.OutputTokens != 2 {
		t.Fatalf("tokens = %#v", record.tok)
	}
}

func TestProxyOpenAIResponsesProviderRewritesEndpointAndConvertsResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("backend path = %q, want /v1/responses", r.URL.Path)
		}
		// Verify Anthropic headers are stripped for non-Anthropic providers
		if v := r.Header.Get("Anthropic-Version"); v != "" {
			t.Fatalf("Anthropic-Version should be stripped, got %q", v)
		}
		if v := r.Header.Get("Anthropic-Beta"); v != "" {
			t.Fatalf("Anthropic-Beta should be stripped, got %q", v)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read backend request: %v", err)
		}
		var captured map[string]any
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode backend request: %v body=%s", err, body)
		}
		if captured["model"] != "mapped-sonnet" {
			t.Fatalf("model = %#v, want mapped-sonnet", captured["model"])
		}
		if _, ok := captured["input"].([]any); !ok {
			t.Fatalf("input missing from Responses request: %#v", captured)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_1",
			"model":"mapped-sonnet",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],
			"usage":{"input_tokens":4,"output_tokens":2}
		}`))
	}))
	defer backend.Close()

	provider := testProxyProvider(backend.URL + "/v1")
	provider.APIFormat = config.APIFormatOpenAIResponses
	handler := NewHandler(config.NewMockStore(testProxyConfig(provider)), http.DefaultTransport.(*http.Transport))
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet",
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if got["type"] != "message" {
		t.Fatalf("response was not converted to Anthropic message: %#v", got)
	}
	content := got["content"].([]any)
	if content[0].(map[string]any)["text"] != "hi" {
		t.Fatalf("content = %#v", content)
	}
}

func TestProxyOpenAIResponsesProviderConvertsStreamingResponse(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	responsesSSE := "event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\n" +
		"event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("backend path = %q, want /v1/responses", r.URL.Path)
		}
		// Verify Anthropic headers are stripped for non-Anthropic providers
		if v := r.Header.Get("Anthropic-Version"); v != "" {
			t.Fatalf("Anthropic-Version should be stripped, got %q", v)
		}
		if v := r.Header.Get("Anthropic-Beta"); v != "" {
			t.Fatalf("Anthropic-Beta should be stripped, got %q", v)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesSSE))
	}))
	defer backend.Close()

	provider := testProxyProvider(backend.URL + "/v1")
	provider.APIFormat = config.APIFormatOpenAIResponses
	handler := NewHandler(config.NewMockStore(testProxyConfig(provider)), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet",
		"stream":true,
		"messages":[{"role":"user","content":"hello"}]
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: message_start") || !strings.Contains(body, `"text":"hi"`) || !strings.Contains(body, "event: message_stop") {
		t.Fatalf("response was not converted to Anthropic SSE:\n%s", body)
	}
	record := recorder.onlyRecord(t)
	if record.tok.InputTokens != 4 || record.tok.OutputTokens != 2 {
		t.Fatalf("tokens = %#v", record.tok)
	}
}

func TestBuildUpstreamURLKeepsFullOpenAICompatibleEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		format     config.APIFormat
		wantSuffix string
	}{
		{
			name:       "openai chat full endpoint",
			baseURL:    "https://example.com/v1/chat/completions",
			format:     config.APIFormatOpenAIChat,
			wantSuffix: "/v1/chat/completions",
		},
		{
			name:       "openai responses full endpoint",
			baseURL:    "https://example.com/v1/responses",
			format:     config.APIFormatOpenAIResponses,
			wantSuffix: "/v1/responses",
		},
		{
			name:       "openai chat base url",
			baseURL:    "https://example.com/v1",
			format:     config.APIFormatOpenAIChat,
			wantSuffix: "/v1/chat/completions",
		},
		{
			name:       "openai responses base url",
			baseURL:    "https://example.com/v1",
			format:     config.APIFormatOpenAIResponses,
			wantSuffix: "/v1/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildUpstreamURL(tt.baseURL, "/v1/messages", tt.format)
			if !strings.HasSuffix(got, tt.wantSuffix) {
				t.Fatalf("buildUpstreamURL() = %q, want suffix %q", got, tt.wantSuffix)
			}
		})
	}
}

func TestProxyRecordsUsageNoneWhenUsageMissing(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_123","type":"message"}`))
	}))
	defer backend.Close()

	handler := NewHandler(config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-sonnet","messages":[]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	record := recorder.onlyRecord(t)
	if record.tok.UsageSource != usage.UsageSourceNone || record.tok.UsageParseStatus != usage.ParseStatusMissing {
		t.Fatalf("token status = %#v", record.tok)
	}
	if record.req.ErrorType != "" {
		t.Fatalf("ErrorType = %q", record.req.ErrorType)
	}
}

func TestProxyRecordsHTTPErrorAndForwardsFullBody(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	errorBody := strings.Repeat("provider-error-", 500)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(errorBody))
	}))
	defer backend.Close()

	handler := NewHandler(config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-sonnet","messages":[]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != errorBody {
		t.Fatalf("client did not receive full provider body: got %d want %d bytes", rec.Body.Len(), len(errorBody))
	}
	record := recorder.onlyRecord(t)
	if record.req.ErrorType != usage.ErrorHTTP {
		t.Fatalf("ErrorType = %q", record.req.ErrorType)
	}
	if record.tok.UsageParseStatus != usage.ParseStatusSkippedNon2xx {
		t.Fatalf("UsageParseStatus = %q", record.tok.UsageParseStatus)
	}
}

func TestProxyRecordsSSELabeledHTTPError(t *testing.T) {
	var logBuf bytes.Buffer
	oldOutput := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})

	tests := []struct {
		name   string
		status int
		stream bool
	}{
		{name: "400 non-stream request", status: http.StatusBadRequest, stream: false},
		{name: "429 stream request", status: http.StatusTooManyRequests, stream: true},
		{name: "500 stream request", status: http.StatusInternalServerError, stream: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logBuf.Reset()
			recorder := &fakeUsageRecorder{}
			errorBody := `{"type":"error","error":{"type":"provider_error","message":"request rejected"}}`
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(errorBody))
			}))
			defer backend.Close()

			handler := NewHandler(
				config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))),
				http.DefaultTransport.(*http.Transport),
				recorder,
			)
			body := fmt.Sprintf(`{
				"model":"claude-sonnet",
				"stream":%t,
				"max_tokens":64,
				"system":"secret-system-prompt",
				"metadata":{"user_id":"secret-user-id"},
				"api_key":"secret-top-level-key",
				"unknown_extension":{"value":"secret-extension-value"},
				"messages":[{"role":"user","content":"secret-message-content"}]
			}`, tt.stream)
			req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if rec.Body.String() != errorBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), errorBody)
			}

			record := recorder.onlyRecord(t)
			if record.req.StatusCode == nil || *record.req.StatusCode != tt.status {
				t.Fatalf("StatusCode = %v, want %d", record.req.StatusCode, tt.status)
			}
			if record.req.ErrorType != usage.ErrorHTTP {
				t.Fatalf("ErrorType = %q", record.req.ErrorType)
			}
			if record.req.ErrorMessage != errorBody {
				t.Fatalf("ErrorMessage = %q, want %q", record.req.ErrorMessage, errorBody)
			}
			if record.req.ResponseBytes != int64(len(errorBody)) {
				t.Fatalf("ResponseBytes = %d, want %d", record.req.ResponseBytes, len(errorBody))
			}
			if record.tok.UsageSource != usage.UsageSourceNone {
				t.Fatalf("UsageSource = %q", record.tok.UsageSource)
			}
			if record.tok.UsageParseStatus != usage.ParseStatusSkippedNon2xx {
				t.Fatalf("UsageParseStatus = %q", record.tok.UsageParseStatus)
			}

			logs := logBuf.String()
			if !strings.Contains(logs, fmt.Sprintf("[Proxy] Error %d", tt.status)) ||
				!strings.Contains(logs, `"max_tokens":64`) ||
				!strings.Contains(logs, `"messages":"[1 items]"`) ||
				!strings.Contains(logs, `"model":"mapped-sonnet"`) ||
				!strings.Contains(logs, fmt.Sprintf(`"stream":%t`, tt.stream)) ||
				!strings.Contains(logs, "resp: "+errorBody) {
				t.Fatalf("missing detailed HTTP error log:\n%s", logs)
			}
			if strings.Contains(logs, "[Stream] SSE stream detected") {
				t.Fatalf("HTTP error incorrectly entered SSE path:\n%s", logs)
			}
			for _, secret := range []string{
				"secret-system-prompt",
				"secret-user-id",
				"secret-top-level-key",
				"secret-extension-value",
				"secret-message-content",
			} {
				if strings.Contains(logs, secret) {
					t.Fatalf("sensitive request field %q leaked into logs:\n%s", secret, logs)
				}
			}
		})
	}
}

func TestSummarizeRequestParamsAllowsOnlySafeDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "keeps typed safe fields and collection counts",
			body: `{
				"model":"claude-sonnet",
				"stream":true,
				"max_tokens":64,
				"max_output_tokens":128,
				"temperature":0.2,
				"top_p":0.9,
				"top_k":5,
				"messages":[{"content":"secret-message"}],
				"tools":[{"description":"secret-tool"}],
				"input":[{"content":"secret-input"}],
				"system":"secret-system",
				"metadata":{"user_id":"secret-user"},
				"api_key":"secret-key",
				"unknown_extension":{"value":"secret-extension"}
			}`,
			want: `{"input":"[1 items]","max_output_tokens":128,"max_tokens":64,"messages":"[1 items]","model":"claude-sonnet","stream":true,"temperature":0.2,"tools":"[1 items]","top_k":5,"top_p":0.9}`,
		},
		{
			name: "drops allowlisted names with unsafe value types",
			body: `{
				"model":{"value":"secret-model"},
				"stream":"secret-stream",
				"max_tokens":{"value":"secret-max-tokens"},
				"messages":"secret-messages"
			}`,
			want: `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeRequestParams([]byte(tt.body)); got != tt.want {
				t.Fatalf("summarizeRequestParams() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestProxyForwardsLargeNonRecoverable400Body(t *testing.T) {
	errorBody := strings.Repeat("provider-error-", 12000)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(errorBody))
	}))
	defer backend.Close()

	handler := NewHandler(config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))), http.DefaultTransport.(*http.Transport))
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-sonnet","messages":[]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != errorBody {
		t.Fatalf("client did not receive full provider body: got %d want %d bytes", rec.Body.Len(), len(errorBody))
	}
}

func TestProxyRetriesKimiTool400WithCleanedRequestBody(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read backend request: %v", err)
		}

		if requests == 1 {
			if !strings.Contains(string(body), "cache_control") || !strings.Contains(string(body), "additionalProperties") {
				t.Fatalf("first request should be original body, got %s", string(body))
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"Invalid request Error: tools.0.input_schema.additionalProperties is not supported"}}`))
			return
		}

		if strings.Contains(string(body), "cache_control") || strings.Contains(string(body), "additionalProperties") {
			t.Fatalf("retry request was not cleaned: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer backend.Close()

	handler := NewHandler(config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{
			"name":"Bash",
			"description":"run",
			"cache_control":{"type":"ephemeral"},
			"input_schema":{"type":"object","properties":{"cmd":{"type":"string"}},"additionalProperties":false}
		}]
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if requests != 2 {
		t.Fatalf("backend requests = %d, want 2", requests)
	}
	record := recorder.onlyRecord(t)
	if record.req.StatusCode == nil || *record.req.StatusCode != http.StatusOK {
		t.Fatalf("recorded status = %v", record.req.StatusCode)
	}
	if record.tok.InputTokens != 3 || record.tok.OutputTokens != 2 {
		t.Fatalf("tokens = %#v", record.tok)
	}
}

func TestProxyRecordsNetworkError(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	backendURL := backend.URL
	backend.Close()

	handler := NewHandler(config.NewMockStore(testProxyConfig(testProxyProvider(backendURL))), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-sonnet","messages":[]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	record := recorder.onlyRecord(t)
	if record.req.ErrorType != usage.ErrorNetwork {
		t.Fatalf("ErrorType = %q", record.req.ErrorType)
	}
	if record.tok.UsageParseStatus != usage.ParseStatusNetworkError {
		t.Fatalf("UsageParseStatus = %q", record.tok.UsageParseStatus)
	}
}

func TestProxyRecordsStreamingUsage(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	sse := "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":8,\"cache_read_input_tokens\":4}}}\n\n" +
		"event: message_delta\ndata: {\"usage\":{\"output_tokens\":6}}\n\n"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer backend.Close()

	handler := NewHandler(config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-sonnet","stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	record := recorder.onlyRecord(t)
	if record.tok.UsageSource != usage.UsageSourceProvider || record.tok.UsageParseStatus != usage.ParseStatusOK {
		t.Fatalf("token status = %#v", record.tok)
	}
	if record.tok.InputTokens != 8 || record.tok.OutputTokens != 6 || record.tok.CacheReadInputTokens != 4 {
		t.Fatalf("tokens = %#v", record.tok)
	}
	if record.req.ResponseBytes != int64(len(sse)) {
		t.Fatalf("ResponseBytes = %d", record.req.ResponseBytes)
	}
}

func TestProxyRecordsStreamingUsageWhenUpstreamDoesNotCloseAfterMessageStop(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	sse := "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":8,\"cache_read_input_tokens\":4}}}\n\n" +
		"event: message_delta\ndata: {\"usage\":{\"output_tokens\":6}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer backend.Close()

	handler := NewHandler(config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))), http.DefaultTransport.(*http.Transport), recorder)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-sonnet","stream":true,"messages":[]}`))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler did not finish after message_stop")
	}

	record := recorder.onlyRecord(t)
	if record.tok.UsageSource != usage.UsageSourceProvider || record.tok.UsageParseStatus != usage.ParseStatusOK {
		t.Fatalf("token status = %#v", record.tok)
	}
	if record.tok.InputTokens != 8 || record.tok.OutputTokens != 6 || record.tok.CacheReadInputTokens != 4 {
		t.Fatalf("tokens = %#v", record.tok)
	}
	if record.req.ResponseBytes != int64(len(sse)) {
		t.Fatalf("ResponseBytes = %d", record.req.ResponseBytes)
	}
}

func TestProxyDoesNotRecordHardcodedEndpointUsage(t *testing.T) {
	recorder := &fakeUsageRecorder{}
	handler := NewHandler(config.NewMockStore(testProxyConfig(testProxyProvider("https://example.com"))), nil, recorder)
	req := httptest.NewRequest("GET", "/v1/me", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if len(recorder.records) != 0 {
		t.Fatalf("expected no records, got %d", len(recorder.records))
	}
}

func TestProxyRootPathReturnsOK(t *testing.T) {
	cfg := &config.Config{
		BackendURL: "https://example.com/anthropic",
	}
	handler := NewHandler(config.NewMockStore(cfg), nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() == "" {
		t.Fatal("expected non-empty response body")
	}
}

func TestTransformRequestPreservesClaudeCodeContextFields(t *testing.T) {
	provider := config.NewProvider("compatible", "https://example.com/anthropic", "provider-token")
	provider.ModelMappings["claude-sonnet-4-5"] = "provider-model"
	provider.SupportsThinking = true

	body := `{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"use tools"}],
		"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]},
		"metadata":{"user_id":"test"},
		"output_config":{"effort":"medium"},
		"thinking":{"type":"enabled","budget_tokens":1024}
	}`
	handler := NewHandler(config.NewMockStore(nil), nil)
	modified, err := handler.transformRequest([]byte(body), provider)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	var capturedBody map[string]any
	if err := json.Unmarshal(modified, &capturedBody); err != nil {
		t.Fatalf("decode transformed request body: %v", err)
	}

	if got := capturedBody["model"]; got != "provider-model" {
		t.Fatalf("expected mapped model, got %v", got)
	}
	for _, field := range []string{"context_management", "metadata", "output_config", "thinking"} {
		if _, ok := capturedBody[field]; !ok {
			t.Fatalf("expected %s to be preserved", field)
		}
	}
}

func TestTransformRequestUsesMultimodalModelForImageToolResult(t *testing.T) {
	provider := config.NewProvider("mimo", "https://example.com/anthropic", "provider-token")
	provider.ModelMappings["claude-opus-4-6"] = "mimo-v2.5-pro"
	provider.MultimodalSwitch = true
	provider.MultimodalModel = "mimo-vl-pro"
	handler := NewHandler(config.NewMockStore(nil), nil)

	modified, err := handler.transformRequest([]byte(`{
		"model":"claude-opus-4-6",
		"messages":[{
			"role":"user",
			"content":[{
				"type":"tool_result",
				"tool_use_id":"screenshot",
				"content":[
					{"type":"text","text":"Took a screenshot."},
					{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}
				]
			}]
		}]
	}`), provider)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	var capturedBody map[string]any
	if err := json.Unmarshal(modified, &capturedBody); err != nil {
		t.Fatalf("decode transformed request body: %v", err)
	}
	if got := capturedBody["model"]; got != "mimo-vl-pro" {
		t.Fatalf("model = %v, want multimodal model", got)
	}
}

func TestTransformRequestUsesMultimodalModelForNonTextMediaTypes(t *testing.T) {
	tests := []struct {
		name  string
		block string
	}{
		{
			name:  "document block",
			block: `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0="}}`,
		},
		{
			name:  "pdf media type",
			block: `{"type":"file","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0="}}`,
		},
		{
			name:  "audio media type",
			block: `{"type":"file","source":{"type":"base64","media_type":"audio/mpeg","data":"SUQz"}}`,
		},
		{
			name:  "video media type",
			block: `{"type":"file","source":{"type":"base64","media_type":"video/mp4","data":"AAAA"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := config.NewProvider("mimo", "https://example.com/anthropic", "provider-token")
			provider.ModelMappings["claude-opus-4-6"] = "mimo-v2.5-pro"
			provider.MultimodalSwitch = true
			provider.MultimodalModel = "mimo-vl-pro"
			handler := NewHandler(config.NewMockStore(nil), nil)

			body := `{"model":"claude-opus-4-6","messages":[{"role":"user","content":[` + tt.block + `]}]}`
			modified, err := handler.transformRequest([]byte(body), provider)
			if err != nil {
				t.Fatalf("transform request: %v", err)
			}

			var capturedBody map[string]any
			if err := json.Unmarshal(modified, &capturedBody); err != nil {
				t.Fatalf("decode transformed request body: %v", err)
			}
			if got := capturedBody["model"]; got != "mimo-vl-pro" {
				t.Fatalf("model = %v, want multimodal model", got)
			}
		})
	}
}

func TestTransformRequestUsesMappedModelForTextWhenMultimodalSwitchEnabled(t *testing.T) {
	provider := config.NewProvider("mimo", "https://example.com/anthropic", "provider-token")
	provider.ModelMappings["claude-opus-4-6"] = "mimo-v2.5-pro"
	provider.MultimodalSwitch = true
	provider.MultimodalModel = "mimo-vl-pro"
	handler := NewHandler(config.NewMockStore(nil), nil)

	modified, err := handler.transformRequest([]byte(`{
		"model":"claude-opus-4-6",
		"messages":[{"role":"user","content":"hello"}]
	}`), provider)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	var capturedBody map[string]any
	if err := json.Unmarshal(modified, &capturedBody); err != nil {
		t.Fatalf("decode transformed request body: %v", err)
	}
	if got := capturedBody["model"]; got != "mimo-v2.5-pro" {
		t.Fatalf("model = %v, want mapped text model", got)
	}
}

func TestTransformRequestKeepsMappedModelForImageWhenMultimodalSwitchDisabled(t *testing.T) {
	provider := config.NewProvider("compatible", "https://example.com/anthropic", "provider-token")
	provider.ModelMappings["claude-opus-4-6"] = "glm-5.1"
	provider.MultimodalModel = "glm-v"
	handler := NewHandler(config.NewMockStore(nil), nil)

	modified, err := handler.transformRequest([]byte(`{
		"model":"claude-opus-4-6",
		"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]
	}`), provider)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	var capturedBody map[string]any
	if err := json.Unmarshal(modified, &capturedBody); err != nil {
		t.Fatalf("decode transformed request body: %v", err)
	}
	if got := capturedBody["model"]; got != "glm-5.1" {
		t.Fatalf("model = %v, want mapped model when multimodal switch is disabled", got)
	}
}

func TestTransformRequestStripsThinkingWithoutModelMappings(t *testing.T) {
	provider := config.NewProvider("no-thinking", "https://example.com/anthropic", "provider-token")
	provider.SupportsThinking = false
	handler := NewHandler(config.NewMockStore(nil), nil)

	modified, err := handler.transformRequest(
		[]byte(`{"model":"claude-sonnet-4-5","thinking":{"type":"enabled","budget_tokens":1024}}`),
		provider,
	)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}
	var capturedBody map[string]any
	if err := json.Unmarshal(modified, &capturedBody); err != nil {
		t.Fatalf("decode transformed request body: %v", err)
	}
	if _, ok := capturedBody["thinking"]; ok {
		t.Fatal("expected thinking to be stripped when provider does not support it")
	}
}

func TestShouldForwardAnthropicProtocolHeadersToCompatibleProviders(t *testing.T) {
	for _, header := range []string{"Anthropic-Version", "Anthropic-Beta"} {
		if !shouldForwardRequestHeader(header) {
			t.Fatalf("expected %s to be forwarded to Anthropic-compatible provider", header)
		}
	}
}

type recordedUsage struct {
	req usage.RequestRecord
	tok usage.TokenRecord
}

type fakeUsageRecorder struct {
	records []recordedUsage
}

func (f *fakeUsageRecorder) Record(req usage.RequestRecord, tok usage.TokenRecord) error {
	f.records = append(f.records, recordedUsage{req: req, tok: tok})
	return nil
}

func (f *fakeUsageRecorder) onlyRecord(t *testing.T) recordedUsage {
	t.Helper()
	if len(f.records) != 1 {
		t.Fatalf("expected one usage record, got %d", len(f.records))
	}
	return f.records[0]
}

func testProxyConfig(provider *config.Provider) *config.Config {
	return &config.Config{
		ActiveProviderID: provider.ID,
		Providers:        []config.Provider{*provider},
	}
}

func testProxyProvider(apiURL string) *config.Provider {
	provider := config.NewProvider("Provider A", apiURL, "provider-token")
	provider.ID = "provider-a"
	provider.ModelMappings["claude-sonnet"] = "mapped-sonnet"
	return provider
}

func TestStripAnthropicQueryParams(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		apiFormat config.APIFormat
		want      string
	}{
		{"anthropic format keeps all", "beta=true&foo=bar", config.APIFormatAnthropic, "beta=true&foo=bar"},
		{"openai_chat strips beta", "beta=true&foo=bar", config.APIFormatOpenAIChat, "foo=bar"},
		{"openai_responses strips beta", "beta=true", config.APIFormatOpenAIResponses, ""},
		{"only beta removed", "beta=true", config.APIFormatOpenAIChat, ""},
		{"no beta untouched", "foo=bar&baz=1", config.APIFormatOpenAIChat, "foo=bar&baz=1"},
		{"empty query", "", config.APIFormatOpenAIChat, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripAnthropicQueryParams(tt.query, tt.apiFormat)
			if got != tt.want {
				t.Errorf("stripAnthropicQueryParams(%q, %s) = %q, want %q", tt.query, tt.apiFormat, got, tt.want)
			}
		})
	}
}

func TestCopyUpstreamHeadersFiltersAnthropicHeaders(t *testing.T) {
	tests := []struct {
		name         string
		apiFormat    config.APIFormat
		inputHeaders http.Header
		wantPresent  []string
		wantAbsent   []string
	}{
		{
			name:      "anthropic format keeps Anthropic-Version and Anthropic-Beta",
			apiFormat: config.APIFormatAnthropic,
			inputHeaders: http.Header{
				"Anthropic-Version": {"2023-06-01"},
				"Anthropic-Beta":    {"interleaved-thinking-2025-05-14"},
				"Content-Type":      {"application/json"},
				"Accept-Encoding":   {"gzip"},
			},
			wantPresent: []string{"Anthropic-Version", "Anthropic-Beta", "Content-Type"},
			wantAbsent:  []string{"Accept-Encoding"},
		},
		{
			name:      "openai_chat strips Anthropic headers",
			apiFormat: config.APIFormatOpenAIChat,
			inputHeaders: http.Header{
				"Anthropic-Version": {"2023-06-01"},
				"Anthropic-Beta":    {"interleaved-thinking-2025-05-14"},
				"Content-Type":      {"application/json"},
			},
			wantPresent: []string{"Content-Type"},
			wantAbsent:  []string{"Anthropic-Version", "Anthropic-Beta"},
		},
		{
			name:      "openai_responses strips Anthropic headers",
			apiFormat: config.APIFormatOpenAIResponses,
			inputHeaders: http.Header{
				"Anthropic-Version": {"2023-06-01"},
				"Anthropic-Beta":    {"interleaved-thinking-2025-05-14"},
				"Content-Type":      {"application/json"},
			},
			wantPresent: []string{"Content-Type"},
			wantAbsent:  []string{"Anthropic-Version", "Anthropic-Beta"},
		},
		{
			name:      "apiToken replaces Authorization and X-Api-Key",
			apiFormat: config.APIFormatOpenAIChat,
			inputHeaders: http.Header{
				"Authorization": {"Bearer original-token"},
				"X-Api-Key":     {"original-key"},
				"Content-Type":  {"application/json"},
			},
			wantPresent: []string{"Content-Type"},
			wantAbsent:  []string{},
		},
		{
			name:      "Host and TE are always filtered",
			apiFormat: config.APIFormatAnthropic,
			inputHeaders: http.Header{
				"Host":         {"original-host"},
				"TE":           {"trailers"},
				"Content-Type": {"application/json"},
			},
			wantPresent: []string{"Content-Type"},
			wantAbsent:  []string{"Host", "TE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := httptest.NewRequest("POST", "/test", nil)
			copyUpstreamHeaders(dst, tt.inputHeaders, "provider-token", tt.apiFormat)

			for _, h := range tt.wantPresent {
				if got := dst.Header.Get(h); got == "" {
					t.Errorf("expected header %q to be present, but it was absent", h)
				}
			}
			for _, h := range tt.wantAbsent {
				if got := dst.Header.Get(h); got != "" {
					t.Errorf("expected header %q to be absent, got %q", h, got)
				}
			}

			// apiToken cases: provider token replaces original auth
			if tt.inputHeaders.Get("Authorization") != "" || tt.inputHeaders.Get("X-Api-Key") != "" {
				got := dst.Header.Get("Authorization")
				if got == "" {
					got = dst.Header.Get("X-Api-Key")
				}
				if got != "Bearer provider-token" && got != "provider-token" {
					t.Errorf("auth header = %q, want provider token", got)
				}
			}
		})
	}
}

const toolReferenceBody = `{
	"model":"claude-opus-4-6",
	"messages":[{
		"role":"user",
		"content":[{
			"type":"tool_result",
			"tool_use_id":"tool_123",
			"content":[
				{"type":"tool_reference","tool_name":"WebSearch"},
				{"type":"text","text":"Search results here"}
			]
		}]
	}]
}`

func findToolReference(content []any) bool {
	for _, c := range content {
		cb, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cb["type"] == "tool_reference" {
			return true
		}
		if inner, ok := cb["content"].([]any); ok {
			if findToolReference(inner) {
				return true
			}
		}
	}
	return false
}

// 场景 1：anthropic + strip=false → tool_reference 保留，保持透传
func TestProactiveClean_AnthropicDefault_PreservesToolReference(t *testing.T) {
	provider := config.NewProvider("glm", "https://open.bigmodel.cn/api/anthropic", "token")
	provider.APIFormat = config.APIFormatAnthropic

	handler := NewHandler(config.NewMockStore(nil), nil)
	modified, err := handler.transformRequest([]byte(toolReferenceBody), provider)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	var req map[string]any
	json.Unmarshal(modified, &req)
	messages := req["messages"].([]any)
	msg := messages[0].(map[string]any)
	content := msg["content"].([]any)
	if !findToolReference(content) {
		t.Fatalf("tool_reference should be preserved when strip_unknown_content_blocks=false")
	}
}

// 场景 2：anthropic + strip=true → tool_reference 被主动清洗
func TestProactiveClean_AnthropicStripEnabled_RemovesToolReference(t *testing.T) {
	provider := config.NewProvider("kimi", "https://api.moonshot.cn/anthropic", "token")
	provider.APIFormat = config.APIFormatAnthropic
	provider.StripUnknownContentBlocks = true

	handler := NewHandler(config.NewMockStore(nil), nil)
	modified, err := handler.transformRequest([]byte(toolReferenceBody), provider)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	var req map[string]any
	json.Unmarshal(modified, &req)
	messages := req["messages"].([]any)
	msg := messages[0].(map[string]any)
	content := msg["content"].([]any)
	if findToolReference(content) {
		t.Fatalf("tool_reference should have been stripped when strip_unknown_content_blocks=true")
	}
}

// 场景 3：official anthropic（strip=false 默认）→ 不清洗
func TestProactiveClean_OfficialAnthropic_PreservesToolReference(t *testing.T) {
	provider := config.NewProvider("official", "https://api.anthropic.com", "token")
	provider.APIFormat = config.APIFormatAnthropic

	handler := NewHandler(config.NewMockStore(nil), nil)
	modified, err := handler.transformRequest([]byte(toolReferenceBody), provider)
	if err != nil {
		t.Fatalf("transform request: %v", err)
	}

	var req map[string]any
	json.Unmarshal(modified, &req)
	messages := req["messages"].([]any)
	msg := messages[0].(map[string]any)
	content := msg["content"].([]any)
	if !findToolReference(content) {
		t.Fatalf("tool_reference should be preserved for official Anthropic API")
	}
}

// 场景 4：openai_chat + strip=true → 仍不调用 proactiveCleanUnknownContentTypes，由转换层处理
func TestProactiveClean_OpenAIChat_StripFlagHasNoEffect(t *testing.T) {
	provider := config.NewProvider("glm", "https://open.bigmodel.cn/api/paas/v4", "token")
	provider.APIFormat = config.APIFormatOpenAIChat
	provider.ModelMappings["claude-opus-4-6"] = "glm-4-plus"

	handler := NewHandler(config.NewMockStore(nil), nil)
	body := []byte(toolReferenceBody)

	provider.StripUnknownContentBlocks = false
	withoutStrip, err := handler.transformRequest(body, provider)
	if err != nil {
		t.Fatalf("transform without strip: %v", err)
	}

	provider.StripUnknownContentBlocks = true
	withStrip, err := handler.transformRequest(body, provider)
	if err != nil {
		t.Fatalf("transform with strip: %v", err)
	}

	if !bytes.Equal(withoutStrip, withStrip) {
		t.Fatalf("OpenAI Chat output must be identical regardless of strip flag — proactive cleanup should not run for non-anthropic format")
	}
}

// 场景 5：Kimi 错误 "unsupported content type..." 命中 PatternGenericBadRequest
func TestMatchErrorPattern_KimiUnsupportedContentType(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","message":"failed to convert tool result content: unsupported content type in ContentBlockParamUnion: tool_reference"}}`)
	got := matchErrorPattern(body)
	if got != PatternGenericBadRequest {
		t.Errorf("expected PatternGenericBadRequest, got %v", got)
	}
}

func testTLSCertPair(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	tmpDir := t.TempDir()
	certMgr := cert.NewManager(tmpDir)
	caCert, caKey, err := certMgr.GenerateCA()
	if err != nil {
		t.Fatalf("generate CA: %v", err)
	}
	serverCert, serverKey, err := certMgr.GenerateServerCert(caCert, caKey)
	if err != nil {
		t.Fatalf("generate server cert: %v", err)
	}
	if err := certMgr.SaveServerCert(serverCert, caCert, serverKey); err != nil {
		t.Fatalf("save server cert: %v", err)
	}
	if err := certMgr.SaveCA(caCert, caKey); err != nil {
		t.Fatalf("save CA: %v", err)
	}
	return filepath.Join(tmpDir, "server.crt"), filepath.Join(tmpDir, "server.key")
}

type safeLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeLogBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeLogBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *safeLogBuffer) Contains(substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Contains(s.buf.String(), substr)
}

func waitForLog(buf *safeLogBuffer, timeout time.Duration, substr string) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if buf.Contains(substr) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestTLSListenerLogsNoSNIOnGarbageInput(t *testing.T) {
	certPath, keyPath := testTLSCertPair(t)
	certPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	sniStore := &sync.Map{}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.Conn != nil && hello.ServerName != "" {
				sniStore.Store(hello.Conn.RemoteAddr().String(), hello.ServerName)
			}
			return &certPair, nil
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var logBuf safeLogBuffer
	tlsLn := newTLSListener(ln, tlsCfg, sniStore, defaultHandshakeTimeout, defaultMaxHandshakes, log.New(&logBuf, "", log.LstdFlags))
	defer tlsLn.Close()

	rawConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	rawConn.Write([]byte("NOT TLS GARBAGE"))
	rawConn.Close()

	if !waitForLog(&logBuf, 2*time.Second, "no SNI") {
		t.Fatalf("timeout waiting for log; got %q", logBuf.String())
	}

	logStr := logBuf.String()
	if !strings.Contains(logStr, "TLS handshake error") {
		t.Errorf("expected 'TLS handshake error' in log, got %q", logStr)
	}
	if !strings.Contains(logStr, "no SNI") {
		t.Errorf("expected 'no SNI' in log, got %q", logStr)
	}
}

func TestTLSListenerLogsSNIOnUntrustedCert(t *testing.T) {
	certPath, keyPath := testTLSCertPair(t)
	certPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	sniStore := &sync.Map{}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.Conn != nil && hello.ServerName != "" {
				sniStore.Store(hello.Conn.RemoteAddr().String(), hello.ServerName)
			}
			return &certPair, nil
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var logBuf safeLogBuffer
	tlsLn := newTLSListener(ln, tlsCfg, sniStore, defaultHandshakeTimeout, defaultMaxHandshakes, log.New(&logBuf, "", log.LstdFlags))
	defer tlsLn.Close()

	// TLS client sends ClientHello with SNI but doesn't trust the self-signed CA
	conn, dialErr := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		ServerName: "api.anthropic.com",
	})
	if conn != nil {
		conn.Close()
	}
	if dialErr == nil {
		t.Skip("TLS handshake succeeded unexpectedly; cannot test SNI error logging")
	}

	if !waitForLog(&logBuf, 2*time.Second, "SNI=api.anthropic.com") {
		t.Fatalf("timeout waiting for log; got %q", logBuf.String())
	}

	logStr := logBuf.String()
	if !strings.Contains(logStr, "TLS handshake error") {
		t.Errorf("expected 'TLS handshake error' in log, got %q", logStr)
	}
	if !strings.Contains(logStr, "SNI=api.anthropic.com") {
		t.Errorf("expected 'SNI=api.anthropic.com' in log, got %q", logStr)
	}
}

func TestTLSListenerSlowHandshakeDoesNotBlock(t *testing.T) {
	certPath, keyPath := testTLSCertPair(t)
	certPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	sniStore := &sync.Map{}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.Conn != nil && hello.ServerName != "" {
				sniStore.Store(hello.Conn.RemoteAddr().String(), hello.ServerName)
			}
			return &certPair, nil
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tlsLn := newTLSListener(ln, tlsCfg, sniStore, 500*time.Millisecond, defaultMaxHandshakes, log.Default())
	defer tlsLn.Close()

	// Slow client: TCP connect but never sends ClientHello
	slowConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("slow dial: %v", err)
	}
	defer slowConn.Close()

	// Valid client: trusts CA, completes handshake
	caPEM, err := os.ReadFile(filepath.Join(filepath.Dir(certPath), "ca.crt"))
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	clientConn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		ServerName: "api.anthropic.com",
		RootCAs:    caPool,
	})
	if err != nil {
		t.Fatalf("valid TLS dial failed: %v", err)
	}
	defer clientConn.Close()

	// Accept must return the valid connection despite the slow client blocking its own handshake goroutine
	acceptDone := make(chan net.Conn, 1)
	go func() {
		conn, err := tlsLn.Accept()
		if err != nil {
			close(acceptDone)
			return
		}
		acceptDone <- conn
	}()

	select {
	case accepted := <-acceptDone:
		if accepted == nil {
			t.Fatal("Accept returned nil conn")
		}
		accepted.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("Accept was blocked by slow handshake; listener starvation detected")
	}
}

func TestTLSListenerHandshakeTimeout(t *testing.T) {
	certPath, keyPath := testTLSCertPair(t)
	certPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	sniStore := &sync.Map{}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.Conn != nil && hello.ServerName != "" {
				sniStore.Store(hello.Conn.RemoteAddr().String(), hello.ServerName)
			}
			return &certPair, nil
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var logBuf safeLogBuffer
	tlsLn := newTLSListener(ln, tlsCfg, sniStore, 200*time.Millisecond, defaultMaxHandshakes, log.New(&logBuf, "", log.LstdFlags))
	defer tlsLn.Close()

	// TCP connect but never send ClientHello — handshake stalls until deadline fires
	slowConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer slowConn.Close()

	if !waitForLog(&logBuf, 2*time.Second, "TLS handshake error") {
		t.Fatalf("timeout waiting for handshake error log; got %q", logBuf.String())
	}

	logStr := logBuf.String()
	if !strings.Contains(logStr, "no SNI") {
		t.Errorf("expected 'no SNI' in log, got %q", logStr)
	}
}

func TestTLSListenerRejectsExcessHandshakes(t *testing.T) {
	certPath, keyPath := testTLSCertPair(t)
	certPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	sniStore := &sync.Map{}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.Conn != nil && hello.ServerName != "" {
				sniStore.Store(hello.Conn.RemoteAddr().String(), hello.ServerName)
			}
			return &certPair, nil
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tlsLn := newTLSListener(ln, tlsCfg, sniStore, 500*time.Millisecond, 2, log.Default())
	defer tlsLn.Close()

	// Create 4 slow connections — only 2 proceed, the rest are rejected
	conns := make([]net.Conn, 4)
	for i := range conns {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conns[i] = c
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	time.Sleep(200 * time.Millisecond)

	rejected := 0
	for _, c := range conns {
		c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		buf := make([]byte, 1)
		_, err := c.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			rejected++
		}
	}

	if rejected < 2 {
		t.Errorf("expected at least 2 rejected connections, got %d", rejected)
	}
}

func TestTLSListenerCloseDrainsQueued(t *testing.T) {
	certPath, keyPath := testTLSCertPair(t)
	certPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	sniStore := &sync.Map{}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.Conn != nil && hello.ServerName != "" {
				sniStore.Store(hello.Conn.RemoteAddr().String(), hello.ServerName)
			}
			return &certPair, nil
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tlsLn := newTLSListener(ln, tlsCfg, sniStore, defaultHandshakeTimeout, defaultMaxHandshakes, log.Default())

	caPEM, err := os.ReadFile(filepath.Join(filepath.Dir(certPath), "ca.crt"))
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	clientConn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		ServerName: "api.anthropic.com",
		RootCAs:    caPool,
	})
	if err != nil {
		t.Fatalf("TLS dial: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	tlsLn.Close()

	_, err = tlsLn.Accept()
	if err == nil {
		t.Fatal("expected error from Accept after Close, got nil")
	}

	clientConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = clientConn.Read(buf)
	if err == nil {
		t.Error("expected queued connection to be closed after listener Close")
	}
	clientConn.Close()
}

func TestTLSListenerCloseSynchronous(t *testing.T) {
	certPath, keyPath := testTLSCertPair(t)
	certPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load key pair: %v", err)
	}

	sniStore := &sync.Map{}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.Conn != nil && hello.ServerName != "" {
				sniStore.Store(hello.Conn.RemoteAddr().String(), hello.ServerName)
			}
			return &certPair, nil
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tlsLn := newTLSListener(ln, tlsCfg, sniStore, 200*time.Millisecond, defaultMaxHandshakes, log.Default())

	slowConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer slowConn.Close()

	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		tlsLn.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not return within 2s — wg.Wait() may be stuck")
	}

	_, err = tlsLn.Accept()
	if err == nil {
		t.Fatal("expected error from Accept after Close")
	}
}

// TestRedactUpstreamURL 验证日志 redact：query 和 fragment 必须被剥离，
// 防止 provider URL 的签名/凭证参数泄露。
func TestRedactUpstreamURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://host.example/v1/messages?sign=secret&token=x", "https://host.example/v1/messages"},
		{"https://host.example/v1/messages#frag", "https://host.example/v1/messages"},
		{"https://user:pass@host.example/v1/messages?sign=x", "https://host.example/v1/messages"},
		{"https://userinfo@host.example/v1", "https://host.example/v1"},
		{"https://host.example/v1/messages", "https://host.example/v1/messages"},
		{"", ""},
		{"not-a-url", "not-a-url"},
	}
	for _, c := range cases {
		if got := redactUpstreamURL(c.in); got != c.want {
			t.Errorf("redactUpstreamURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRestartGateway_SameAddrSkips 验证地址未变化且 server 正在运行时跳过重启，
// 避免 "address already in use"（旧实例仍在监听）。
func TestRestartGateway_SameAddrSkips(t *testing.T) {
	s := NewServer(config.NewMockStore(nil))
	addr := "127.0.0.1:0"
	original := &http.Server{}
	s.gatewayMu.Lock()
	s.gatewayAddr = addr
	s.gatewayServer = original // 模拟 gateway 正在运行
	s.gatewayMu.Unlock()

	if err := s.RestartGateway(addr); err != nil {
		t.Fatalf("RestartGateway with same addr should skip, got error: %v", err)
	}
	s.gatewayMu.Lock()
	defer s.gatewayMu.Unlock()
	// 跳过：gatewayServer 应保持原引用，未被替换
	if s.gatewayServer != original {
		t.Errorf("expected gatewayServer unchanged on skip, got replaced")
	}
}

// TestRestartGateway_StoppedServerRetries 验证 server 已停（gatewayServer=nil）时，
// 即使 gatewayAddr 残留旧值也不跳过——必须重新启动恢复监听。
func TestRestartGateway_StoppedServerRetries(t *testing.T) {
	s := NewServer(config.NewMockStore(nil))
	// 模拟 Stop() 后状态：addr 残留但 server 已清
	s.gatewayMu.Lock()
	s.gatewayAddr = "127.0.0.1:0"
	s.gatewayServer = nil
	s.gatewayMu.Unlock()

	if err := s.RestartGateway("127.0.0.1:0"); err != nil {
		t.Fatalf("RestartGateway after stop should retry (not skip), got: %v", err)
	}
	s.gatewayMu.Lock()
	gw := s.gatewayServer
	s.gatewayMu.Unlock()
	if gw == nil {
		t.Fatal("expected gatewayServer recreated after stop, got nil (skip wrongly applied)")
	}
	t.Cleanup(func() { gw.Close() })
}

// TestProxyLogsRedactQuery 验证入口和出口日志的 upstream_url 不含 query。
func TestProxyLogsRedactQuery(t *testing.T) {
	var buf bytes.Buffer
	oldOutput := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer backend.Close()

	// provider URL 带一个"敏感" query，验证它不出现在日志里
	provider := testProxyProvider(backend.URL)
	provider.APIURL = backend.URL + "/v1?sign=SECRET-TOKEN"
	handler := NewHandler(config.NewMockStore(testProxyConfig(provider)), http.DefaultTransport.(*http.Transport))
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{
		"model":"claude-sonnet","stream":false,
		"messages":[{"role":"user","content":"hi"}]
	}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logs := buf.String()
	if strings.Contains(logs, "sign=SECRET-TOKEN") {
		t.Errorf("sensitive query leaked into logs:\n%s", logs)
	}
}
