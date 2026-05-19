package proxy

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude_code_proxy_dns/internal/config"
	"claude_code_proxy_dns/internal/usage"
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
