package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"claude_code_proxy_dns/internal/config"
)

func TestIsSSEStream(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		expected    bool
	}{
		{
			name:        "SSE stream",
			contentType: "text/event-stream",
			expected:    true,
		},
		{
			name:        "SSE stream with charset",
			contentType: "text/event-stream; charset=utf-8",
			expected:    true,
		},
		{
			name:        "JSON response",
			contentType: "application/json",
			expected:    false,
		},
		{
			name:        "Empty content type",
			contentType: "",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				Header: http.Header{
					"Content-Type": []string{tt.contentType},
				},
			}
			if got := isSSEStream(resp); got != tt.expected {
				t.Errorf("isSSEStream() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHeartbeatWriterWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	hw := newHeartbeatWriter(rec)

	data := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n")
	n, err := hw.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}

	if !bytes.Contains(rec.Body.Bytes(), data) {
		t.Error("expected data to be written to response")
	}
}

func TestHeartbeatWriterFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	hw := newHeartbeatWriter(rec)

	// Flush 不应 panic
	hw.Flush()
}

func TestCopyWithHeartbeat_NoHeartbeatNeeded(t *testing.T) {
	// 测试快速完成的流不需要心跳
	rec := httptest.NewRecorder()
	hw := newHeartbeatWriter(rec)

	data := []byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
	reader := bytes.NewReader(data)

	err := copyWithHeartbeat(hw, reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(rec.Body.Bytes(), data) {
		t.Error("expected data to be written")
	}
}

func TestCopyWithHeartbeat_StopsCleanly(t *testing.T) {
	// 测试完成后 stop channel 被正确关闭
	rec := httptest.NewRecorder()
	hw := newHeartbeatWriter(rec)

	reader := bytes.NewReader([]byte("data"))
	err := copyWithHeartbeat(hw, reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证 stop 被调用（不会 panic 或 deadlock）
	hw.heartbeatMu.Lock()
	stopped := hw.stopped
	hw.heartbeatMu.Unlock()

	if !stopped {
		t.Error("expected heartbeat to be stopped after copyWithHeartbeat returns")
	}
}

func TestSSEStreamProxyWithHeartbeat(t *testing.T) {
	// 创建返回 SSE 流的模拟后端
	sseData := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"Hello\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseData))
	}))
	defer backend.Close()

	cfg := &config.Config{
		BackendURL: backend.URL,
	}
	store := config.NewMockStore(cfg)
	transport := &http.Transport{}
	handler := NewHandler(store, transport)

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// 验证原始 SSE 数据被透传
	if !strings.Contains(body, "message_start") {
		t.Error("expected SSE data to be forwarded")
	}
	if !strings.Contains(body, "content_block_delta") {
		t.Error("expected content_block_delta to be forwarded")
	}
	if !strings.Contains(body, "message_stop") {
		t.Error("expected message_stop to be forwarded")
	}
}

func TestNonSSEStreamProxyWithoutHeartbeat(t *testing.T) {
	// 非 SSE 响应不应注入心跳
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	cfg := &config.Config{
		BackendURL: backend.URL,
	}
	store := config.NewMockStore(cfg)
	transport := &http.Transport{}
	handler := NewHandler(store, transport)

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, "ping") {
		t.Error("non-SSE response should not contain ping events")
	}
	if body != `{"status":"ok"}` {
		t.Errorf("expected original response body, got %s", body)
	}
}

func TestHeartbeatTiming(t *testing.T) {
	// 验证心跳间隔常量合理
	if sseHeartbeatInterval >= 90*time.Second {
		t.Errorf("heartbeat interval %v should be less than Claude Code's 90s timeout", sseHeartbeatInterval)
	}
	if sseHeartbeatInterval < 10*time.Second {
		t.Errorf("heartbeat interval %v is too frequent, minimum 10s recommended", sseHeartbeatInterval)
	}
}

func TestAnthropicPingEventFormat(t *testing.T) {
	// 验证 ping 事件格式符合 Anthropic SSE 规范
	ping := string(anthropicPingEvent)

	if !strings.HasPrefix(ping, "event: ping\n") {
		t.Error("ping event should start with 'event: ping'")
	}
	if !strings.Contains(ping, "data: {\"type\":\"ping\"}") {
		t.Error("ping event should contain data line with type ping")
	}
	if !strings.HasSuffix(ping, "\n\n") {
		t.Error("ping event should end with double newline")
	}
}
