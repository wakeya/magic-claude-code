package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestBlockedEndpointLogging 验证被拦截端点产生一条安全日志：
//   - 含 method/path/status/reason/query_present 等定位字段
//   - 绝不含请求体、Authorization、Cookie、X-Api-Key、原始 query token
//   - 单个请求只产生一行拦截日志
func TestBlockedEndpointLogging(t *testing.T) {
	var buf bytes.Buffer
	origOut := log.Default().Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origOut)

	handler := NewHandler(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/complete?token=secret", strings.NewReader("api_key=secret&password=hunter2"))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Cookie", "a=b")
	req.Header.Set("X-Api-Key", "sk-secret-key")
	req.Header.Set("User-Agent", "claude-code/2.1.206")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	out := buf.String()

	// 必须包含定位字段
	for _, want := range []string{"method=", "path=", "status=", "reason=", "query_present=true", "/v1/complete", "Blocking endpoint"} {
		if !strings.Contains(out, want) {
			t.Errorf("blocked log missing %q:\n%s", want, out)
		}
	}

	// query 存在但只记录布尔，不记录原始值
	if strings.Contains(out, "token=secret") {
		t.Errorf("blocked log leaked raw query token:\n%s", out)
	}
	if strings.Contains(out, "token") && strings.Contains(out, "secret") {
		t.Errorf("blocked log leaked token/secret together:\n%s", out)
	}

	// 绝不能包含任何敏感数据
	secrets := []string{"secret", "Bearer", "api_key", "password", "hunter2", "a=b", "sk-secret-key", "sk-secret"}
	for _, s := range secrets {
		if strings.Contains(out, s) {
			t.Errorf("blocked log leaked secret %q:\n%s", s, out)
		}
	}

	// 单个请求只产生一行拦截日志
	if n := strings.Count(out, "[Hardcoded] Blocking endpoint"); n != 1 {
		t.Errorf("expected exactly 1 blocking log line, got %d:\n%s", n, out)
	}

	// user agent 应被记录（非敏感）
	if !strings.Contains(out, "claude-code/2.1.206") {
		t.Errorf("blocked log should record user agent:\n%s", out)
	}
}

// TestBlockedEndpointLogInjectionGuard 验证 path/UA 中的换行（URL 编码 %0a 或字面）
// 被 sanitize 替换，不会在日志中产生额外的物理行（CWE-117 日志注入）。
func TestBlockedEndpointLogInjectionGuard(t *testing.T) {
	var buf bytes.Buffer
	origOut := log.Default().Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origOut)

	handler := NewHandler(nil, nil)
	// path 含 %0a（URL 编码换行），试图伪造第二行日志。
	req := httptest.NewRequest(http.MethodGet, "/v1/evil%0aFAKE_SECOND_LINE", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	out := buf.String()
	// 安全属性：整条日志只有 1 个物理换行（log.Printf 末尾的 \n）。
	// 若 path 的换行未被 sanitize，则会产生嵌入式 \n，导致行数 > 1。
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("expected exactly 1 newline in log, got %d (newline not sanitized, possible injection):\n%s", n, out)
	}
	// sanitize 后换行被替换为 ·，且 FAKE 内容不应作为独立行前缀出现
	if strings.Contains(out, "\nFAKE_SECOND_LINE") {
		t.Errorf("log injection: path newline created a new line:\n%s", out)
	}
}

// TestDrainRequestBodyLimitedStopsAtCap 用计数型 ReadCloser 精确验证有界 drain 的字节契约
// （gpt-5.6 第三轮审查点 2：耗时断言无法防回归，改用计数断言）：
//   - 读取字节数 == maxLocalDrainSize（恰好到上限，不超读）
//   - Close 被调用
//   - 即使底层提供远超上限的数据，也不会无界读取
func TestDrainRequestBodyLimitedStopsAtCap(t *testing.T) {
	const total = maxLocalDrainSize + 4*1024*1024 // 远超上限
	body := &syntheticCountReader{total: total}

	done := make(chan struct{})
	go func() {
		handler := NewHandler(nil, nil)
		// /v1/unknown 触发 handleBlockedEndpoint -> drainRequestBodyLimited
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/unknown", body)
		handler.ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not complete; likely unbounded")
	}

	if !body.closed {
		t.Error("body Close was not called after bounded drain")
	}
	// 关键断言：读取字节数必须恰好等于上限，不得超读（无界 io.Copy 会读完全部 total）
	if body.read != maxLocalDrainSize {
		t.Errorf("drained %d bytes, want exactly %d (maxLocalDrainSize); unbounded drain would read %d",
			body.read, maxLocalDrainSize, total)
	}
}

// syntheticCountReader 记录读取字节数与 Close 调用，可提供任意长度数据而不实际占用内存。
// （hardcoded_test.go 的 countingReadCloser 是固定内容读取器，用途不同，故单独命名。）
type syntheticCountReader struct {
	read   int64
	total  int64
	closed bool
}

func (c *syntheticCountReader) Read(p []byte) (int, error) {
	if c.read >= c.total {
		return 0, io.EOF
	}
	n := int64(len(p))
	if c.read+n > c.total {
		n = c.total - c.read
	}
	for i := int64(0); i < n; i++ {
		p[i] = 'x'
	}
	c.read += n
	return int(n), nil
}

func (c *syntheticCountReader) Close() error { c.closed = true; return nil }

// TestBlockedEndpointLargeBodyBoundedDrain 验证 blocked 端点对超大请求体只做有界 drain
// （maxLocalDrainSize），不会无界读取或挂起（gpt-5.6 审查点：blocked 无界 drain DoS）。
func TestBlockedEndpointLargeBodyBoundedDrain(t *testing.T) {
	handler := NewHandler(nil, nil)
	// 远超 maxLocalDrainSize 的 body
	huge := strings.Repeat("x", int(maxLocalDrainSize)+5*1024*1024)
	req := httptest.NewRequest(http.MethodPost, "/v1/unknown-huge", strings.NewReader(huge))
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked endpoint with huge body did not return within 5s; drain likely unbounded")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestLocalEndpointsBoundedDrainOnOversizedBody 验证所有本地 hardcoded/compat 端点
// 对超过 maxLocalDrainSize 的请求体都走有界 drain，快速返回且状态符合契约（gpt-5.6 第二轮审查）。
// 这些端点以前会走共享无界 drainRequestBody，现已统一改为 drainRequestBodyLimited。
func TestLocalEndpointsBoundedDrainOnOversizedBody(t *testing.T) {
	handler := NewHandler(nil, nil)
	huge := strings.Repeat("x", int(maxLocalDrainSize)+2*1024*1024)

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"frame track", http.MethodPost, "/api/frame/track", http.StatusNoContent},
		{"frame deploy init", http.MethodPost, "/api/frame/deploy/init", http.StatusForbidden},
		{"design mcp", http.MethodPost, "/v1/design/mcp", http.StatusForbidden},
		{"design consent post", http.MethodPost, "/v1/design/consent", http.StatusNoContent},
		{"ws voice stream", http.MethodPost, "/api/ws/speech_to_text/voice_stream", http.StatusNotImplemented},
		{"favicon with body", http.MethodPost, "/favicon.ico", http.StatusNotFound}, // 静态探测仍 404
		{"models non-get", http.MethodPost, "/v1/models", http.StatusMethodNotAllowed},
		{"feedback", http.MethodPost, "/api/claude_cli_feedback", http.StatusOK},
		{"bootstrap", http.MethodGet, "/api/claude_cli/bootstrap", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(huge))
			rec := httptest.NewRecorder()
			start := time.Now()
			handler.ServeHTTP(rec, req)
			elapsed := time.Since(start)
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s %s status = %d, want %d (body=%s)", tc.method, tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
			// 有界 drain 应在数秒内返回（远小于读取 2MB+ 的时间窗口）；这里给宽松上限防 CI 抖动。
			if elapsed > 3*time.Second {
				t.Fatalf("%s %s took %v (>3s); drain likely unbounded", tc.method, tc.path, elapsed)
			}
		})
	}
}

// TestBlockedEndpointResponseBody 验证未知端点拦截的稳定 JSON 契约。
func TestBlockedEndpointResponseBody(t *testing.T) {
	handler := NewHandler(nil, nil)

	t.Run("unknown endpoint returns mcc_blocked contract", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/some/unknown/endpoint", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var resp struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
			Path string `json:"path"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		if resp.Error.Type != "mcc_blocked_unknown_endpoint" {
			t.Errorf("error.type = %q, want mcc_blocked_unknown_endpoint", resp.Error.Type)
		}
		if resp.Path != "/v1/some/unknown/endpoint" {
			t.Errorf("path = %q, want /v1/some/unknown/endpoint", resp.Path)
		}
	})

	t.Run("model endpoint wrong method returns 405 with Allow POST", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
			t.Errorf("Allow = %q, want POST", allow)
		}
	})
}
