package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDesignEndpointCompatibility 验证 Claude Design consent 与 MCP bridge 端点的本地契约。
func TestDesignEndpointCompatibility(t *testing.T) {
	handler := NewHandler(nil, nil)

	t.Run("consent GET returns disabled state", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/design/consent", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle /v1/design/consent")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			AgentDesignProjects bool `json:"agent_design_projects"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		if resp.AgentDesignProjects {
			t.Errorf("agent_design_projects = true, want false")
		}
	})

	t.Run("consent POST returns 204", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/design/consent", strings.NewReader(`{"agent_design_projects":true}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("want empty body, got %q", rec.Body.String())
		}
	})

	t.Run("consent DELETE returns 204", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/design/consent", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("consent unsupported method returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/design/consent", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})

	t.Run("design mcp POST returns unsupported error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/design/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":1}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var resp struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		if resp.Error.Type != "unsupported_local_endpoint" {
			t.Errorf("error.type = %q, want unsupported_local_endpoint", resp.Error.Type)
		}
		if !strings.Contains(resp.Error.Message, "Claude Design") {
			t.Errorf("error.message = %q", resp.Error.Message)
		}
	})

	t.Run("design mcp non-POST returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/design/mcp", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})
}

// TestUnsupportedStreamingEndpoints 验证 WebSocket/语音流端点本地 501 拦截，
// 不 upgrade、不 hijack connection。
func TestUnsupportedStreamingEndpoints(t *testing.T) {
	handler := NewHandler(nil, nil)
	paths := []string{
		"/api/ws/speech_to_text/voice_stream",
		"/api/ws/some/other/stream",
	}
	methods := []string{http.MethodGet, http.MethodPost}

	for _, path := range paths {
		for _, method := range methods {
			t.Run(method+" "+path, func(t *testing.T) {
				// 模拟 WebSocket upgrade 请求头
				req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
				req.Header.Set("Connection", "upgrade")
				req.Header.Set("Upgrade", "websocket")
				rec := httptest.NewRecorder()
				if !handler.handleHardcodedEndpoint(rec, req) {
					t.Fatal("should handle streaming endpoint")
				}
				if rec.Code != http.StatusNotImplemented {
					t.Fatalf("status = %d, want 501", rec.Code)
				}
				var resp struct {
					Error struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
					t.Fatalf("decode: %v body=%s", err, rec.Body.String())
				}
				if resp.Error.Type != "unsupported_local_endpoint" {
					t.Errorf("error.type = %q, want unsupported_local_endpoint", resp.Error.Type)
				}
				if !strings.Contains(resp.Error.Message, "Streaming") {
					t.Errorf("error.message = %q", resp.Error.Message)
				}
				// 不应有 WebSocket upgrade 响应头
				if upg := rec.Header().Get("Upgrade"); upg != "" {
					t.Errorf("unexpected Upgrade header %q — must not hijack connection", upg)
				}
			})
		}
	}
}
