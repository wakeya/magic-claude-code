package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"magic-claude-code/internal/config"
)

// TestHardcodedTriggers 验证 CC 2.1.211 的 /v1/code/triggers 端点：
// GET（list/子路径）返回空 data 避免 throw "triggers unavailable"；
// POST 写入门关闭；其它方法 405。
func TestHardcodedTriggers(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	for _, path := range []string{
		"/v1/code/triggers",
		"/v1/code/triggers/t1",
		"/v1/code/triggers/t1/run",
	} {
		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			if !handler.handleHardcodedEndpoint(rec, req) {
				t.Fatalf("should handle %s", path)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var resp struct {
				Data []any `json:"data"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Data) != 0 {
				t.Errorf("data = %v, want empty", resp.Data)
			}
		})
	}

	t.Run("POST returns 403 write_gate_disabled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/code/triggers", strings.NewReader(`{"name":"t"}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var resp struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Reason != "write_gate_disabled" {
			t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
		}
	})

	t.Run("DELETE returns 405 with Allow GET, POST", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/code/triggers/t1", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, POST" {
			t.Errorf("Allow = %q, want GET, POST", got)
		}
	})
}
