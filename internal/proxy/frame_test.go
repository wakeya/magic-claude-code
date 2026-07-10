package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFrameEndpointCompatibility 覆盖每条 Frame 路由的 status/body 契约，
// 并证明 Frame 端点不命中上游 provider。
func TestFrameEndpointCompatibility(t *testing.T) {
	handler := NewHandler(nil, nil)

	t.Run("frames list returns empty array, query ignored", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/frame/frames?limit=200", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle /api/frame/frames")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Frames []any `json:"frames"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		if len(resp.Frames) != 0 {
			t.Errorf("frames = %v, want empty", resp.Frames)
		}
	})

	t.Run("track returns 204 no content", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/frame/track", strings.NewReader(`{"event":"x"}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("want empty body, got %q", rec.Body.String())
		}
	})

	t.Run("deploy complete returns 204 no content", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/frame/deploy/complete", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("deploy init returns write-gate denied", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/frame/deploy/init", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		var resp struct {
			Error  string `json:"error"`
			Reason string `json:"reason"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Reason != "write_gate_disabled" {
			t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
		}
		if !strings.Contains(resp.Error, "unavailable") {
			t.Errorf("error = %q", resp.Error)
		}
	})

	t.Run("deploy direct returns write-gate denied", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/frame/deploy/direct", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("contract returns local unavailable", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/frame/contract/latest", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		var resp struct {
			Error  string `json:"error"`
			Reason string `json:"reason"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Reason != "local_unavailable" {
			t.Errorf("reason = %q, want local_unavailable", resp.Reason)
		}
	})

	t.Run("unknown slug GET returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/frame/some-artifact-slug", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		var resp struct {
			Error  string `json:"error"`
			Reason string `json:"reason"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.Reason != "not_found" {
			t.Errorf("reason = %q, want not_found", resp.Reason)
		}
	})

	t.Run("unknown slug DELETE returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/frame/some-artifact-slug", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		// POST 到只读 frames 端点
		req := httptest.NewRequest(http.MethodPost, "/api/frame/frames", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST /api/frame/frames status = %d, want 405", rec.Code)
		}
	})
}
