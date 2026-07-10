package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"magic-claude-code/internal/config"
)

// TestEndpointPolicy 是端点策略 fail-closed 决策的验收入口（对应 spec 任务 1 验收命令
// `go test -run 'TestEndpointPolicy|TestServeHTTPFailClosed'`）。
// 表格化覆盖：唯一允许转发的路径、方法错误、未知路径拦截、query 不影响决策。
func TestEndpointPolicy(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		wantAction endpointAction
	}{
		{"forward messages post", http.MethodPost, "/v1/messages", endpointActionForwardModel},
		{"forward anthropic messages post", http.MethodPost, "/anthropic/v1/messages", endpointActionForwardModel},
		{"forward messages post with query", http.MethodPost, "/v1/messages?beta=true", endpointActionForwardModel},
		{"method not allowed on messages get", http.MethodGet, "/v1/messages", endpointActionMethodNotAllowed},
		{"method not allowed on anthropic messages put", http.MethodPut, "/anthropic/v1/messages", endpointActionMethodNotAllowed},
		{"block unknown v1", http.MethodPost, "/v1/logs", endpointActionBlock},
		{"block complete", http.MethodGet, "/v1/complete", endpointActionBlock},
		{"block messages batches", http.MethodPost, "/v1/messages/batches", endpointActionBlock},
		{"block count tokens", http.MethodPost, "/v1/messages/count_tokens", endpointActionBlock},
		{"block models", http.MethodGet, "/v1/models", endpointActionBlock},
		{"block favicon", http.MethodGet, "/favicon.ico", endpointActionBlock},
		{"block design mcp", http.MethodPost, "/v1/design/mcp", endpointActionBlock},
		{"block anthropic non-messages", http.MethodPost, "/anthropic/v1/other", endpointActionBlock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := classifyForwardingEndpoint(tc.method, tc.path)
			if d.action != tc.wantAction {
				t.Errorf("classifyForwardingEndpoint(%q,%q) action=%v, want %v (reason=%q)",
					tc.method, tc.path, d.action, tc.wantAction, d.reason)
			}
		})
	}
}

func TestClassifyForwardingEndpointAllowsOnlyMessagePosts(t *testing.T) {
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/messages"},
		{http.MethodPost, "/anthropic/v1/messages"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			d := classifyForwardingEndpoint(tc.method, tc.path)
			if d.action != endpointActionForwardModel {
				t.Errorf("classifyForwardingEndpoint(%q,%q) action=%v, want %v (reason=%q)",
					tc.method, tc.path, d.action, endpointActionForwardModel, d.reason)
			}
		})
	}
}

func TestClassifyForwardingEndpointRejectsNonMessagePaths(t *testing.T) {
	// 任何非模型推理端点都必须被拦截，不得转发到上游 provider。
	blocked := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/logs"},
		{http.MethodPost, "/v1/metrics"},
		{http.MethodPost, "/v1/traces"},
		{http.MethodGet, "/v1/complete"},
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/v1/messages/batches"},
		{http.MethodGet, "/favicon.ico"},
		{http.MethodPost, "/api/frame/track"},
		{http.MethodPost, "/v1/design/mcp"},
		{http.MethodGet, "/api/ws/speech_to_text/voice_stream"},
		{http.MethodGet, "/v1/organizations/spend_limits"},
		{http.MethodGet, "/anthropic/v1/anything-but-messages"},
		{http.MethodPost, "/v1/messages/count_tokens"},
	}
	for _, tc := range blocked {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			d := classifyForwardingEndpoint(tc.method, tc.path)
			if d.action == endpointActionForwardModel {
				t.Errorf("classifyForwardingEndpoint(%q,%q) must NOT be ForwardModel, got %v (reason=%q)",
					tc.method, tc.path, d.action, d.reason)
			}
		})
	}
}

func TestClassifyForwardingEndpointRejectsWrongMethod(t *testing.T) {
	// 模型端点路径但方法错误：返回 405，不得转发。
	wrongMethods := []string{
		http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead,
	}
	for _, path := range []string{"/v1/messages", "/anthropic/v1/messages"} {
		for _, method := range wrongMethods {
			t.Run(method+" "+path, func(t *testing.T) {
				d := classifyForwardingEndpoint(method, path)
				if d.action != endpointActionMethodNotAllowed {
					t.Errorf("classifyForwardingEndpoint(%q,%q) action=%v, want %v",
						method, path, d.action, endpointActionMethodNotAllowed)
				}
			})
		}
	}
}

func TestClassifyForwardingEndpointIgnoresQueryBecausePathIsNormalized(t *testing.T) {
	// 代理必须只使用标准化后的 r.URL.Path 分类；query 不能决定是否转发。
	// Go 的 http.Request 已把 query 从 Path 分离，但 classifier 仍须防御性剥离，
	// 防止误用导致 ?beta=true 之类 query 绕过白名单。
	t.Run("POST messages with beta query forwards", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", nil)
		d := classifyForwardingEndpoint(req.Method, req.URL.Path)
		if d.action != endpointActionForwardModel {
			t.Errorf("normalized path %q action=%v, want ForwardModel", req.URL.Path, d.action)
		}
	})

	t.Run("classifier defensively strips raw query suffix", func(t *testing.T) {
		// 即便传入未规范化的原始路径，classifier 也只看 ? 之前的部分。
		for _, raw := range []string{"/v1/messages?beta=true", "/v1/messages?x=1&y=2"} {
			d := classifyForwardingEndpoint(http.MethodPost, raw)
			if d.action != endpointActionForwardModel {
				t.Errorf("classifyForwardingEndpoint raw=%q action=%v, want ForwardModel", raw, d.action)
			}
		}
		// 非 messages 路径带 query 仍被拦截。
		d := classifyForwardingEndpoint(http.MethodPost, "/v1/logs?foo=bar")
		if d.action != endpointActionBlock {
			t.Errorf("classifyForwardingEndpoint /v1/logs?foo=bar action=%v, want Block", d.action)
		}
	})
}

// TestServeHTTPFailClosed 证明：未知/非模型端点不会命中上游 provider，
// 而唯一允许转发的 POST /v1/messages 仍进入转发路径。
// 只断言返回码不够——必须断言上游计数器保持 0（spec 实现审查重点 #4）。
func TestServeHTTPFailClosed(t *testing.T) {
	var upstreamHits int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer backend.Close()

	handler := NewHandler(
		config.NewMockStore(testProxyConfig(testProxyProvider(backend.URL))),
		http.DefaultTransport.(*http.Transport),
	)

	// 这些端点绝不能命中上游：静态探测、遥测、未知 /v1/*、未知 /anthropic/v1/*。
	mustNotForward := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/favicon.ico"},
		{http.MethodGet, "/robots.txt"},
		{http.MethodPost, "/v1/logs"},
		{http.MethodPost, "/v1/metrics"},
		{http.MethodPost, "/v1/traces"},
		{http.MethodGet, "/v1/complete"},
		{http.MethodPost, "/v1/messages/batches"},
		{http.MethodGet, "/v1/organizations/spend_limits"},
		{http.MethodGet, "/anthropic/v1/anything-but-messages"},
		{http.MethodGet, "/v1/messages"}, // 方法错误 -> 405
	}
	for _, tc := range mustNotForward {
		t.Run("blocked "+tc.method+" "+tc.path, func(t *testing.T) {
			before := atomic.LoadInt32(&upstreamHits)
			var body io.Reader
			if tc.method == http.MethodPost {
				body = strings.NewReader(`{"telemetry":"x"}`)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			after := atomic.LoadInt32(&upstreamHits)
			if after != before {
				t.Fatalf("%s %s hit upstream %d times; blocked endpoints must never reach provider",
					tc.method, tc.path, after-before)
			}
		})
	}

	// 正面对照：POST /v1/messages 必须命中上游（进入转发路径）。
	t.Run("POST /v1/messages forwards to provider", func(t *testing.T) {
		before := atomic.LoadInt32(&upstreamHits)
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		after := atomic.LoadInt32(&upstreamHits)
		if after-before < 1 {
			t.Fatalf("POST /v1/messages did not reach upstream (hits delta=%d); forwarding path broken", after-before)
		}
	})
}
