package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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

	// 创建代理配置
	cfg := &Config{
		BackendURL: backend.URL,
	}

	// 创建代理处理器
	handler := NewHandler(cfg)

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

	cfg := &Config{
		BackendURL: backend.URL,
	}

	handler := NewHandler(cfg)

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// 应透传错误状态码
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}