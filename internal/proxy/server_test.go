package proxy

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"claude_code_proxy_dns/internal/config"
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
