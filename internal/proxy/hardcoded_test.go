package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"magic-claude-code/internal/config"
)

func TestIsHardcodedEndpoint(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		// 精确匹配
		{"/api/claude_cli_feedback", true},
		{"/api/claude_code_shared_session_transcripts", true},
		{"/api/oauth/claude_cli/create_api_key", true},
		{"/api/oauth/claude_cli/roles", true},
		{"/api/claude_code/organizations/metrics_enabled", true},
		{"/api/event_logging/batch", true},
		{"/api/event_logging/v2/batch", true},
		{"/api/claude_cli/bootstrap", true},
		{"/v1/mcp_servers", true},
		{"/api/claude_code_penguin_mode", true},
		{"/v1/me", true},
		// 新增低优先级精确匹配
		{"/api/oauth/profile", true},
		{"/api/claude_cli_profile", true},
		{"/api/oauth/usage", true},
		{"/api/claude_code/policy_limits", true},
		{"/api/claude_code/settings", true},
		{"/api/claude_code/user_settings", true},
		// 前缀匹配
		{"/api/claude_code/metric", true},
		{"/api/claude_code/metrics", true},
		{"/api/claude_code/organization", true},
		{"/api/web/domain_info", true},
		{"/api/feature/abc123", true},
		{"/api/feature/", true},
		{"/mcp-registry/v0/servers", true},
		{"/mcp-registry/", true},
		// 新增低优先级前缀匹配
		{"/api/oauth/account/settings", true},
		{"/api/oauth/account/grove_notice_viewed", true},
		// v1.1 新增精确匹配
		{"/api/claude_code_grove", true},
		{"/api/organization/claude_code_first_token_date", true},
		{"/v1/ultrareview/quota", true},
		// v1.1 新增前缀匹配
		{"/v1/session_ingress/session/abc-123", true},
		{"/v1/session_ingress/session/x-y-z", true},
		// v1.2 新增精确匹配
		{"/api/claude_code/team_memory", true},
		// v1.2 新增前缀匹配
		{"/api/oauth/organizations/org-123/referral/eligibility", true},
		{"/api/oauth/organizations/org-123/admin_requests", true},
		{"/api/oauth/organizations/org-123/overage_credit_grant", true},
		{"/v1/code/sessions/sess-123/teleport-events", true},
		// v1.3 新增精确匹配
		{"/api/auth/trusted_devices", true},
		{"/api/oauth/file_upload", true},
		// count_tokens 拦截
		{"/v1/messages/count_tokens", true},
		// Desktop 更新探测
		{"/api/desktop/win32/x64/msix/update", true},
		{"/api/desktop/darwin/arm64/squirrel/update", true},
		// 不匹配
		{"/v1/messages", false},
		{"/v1/complete", false},
		{"/some/other/path", false},
		{"/api/oauth/claude_cli/role", false},  // 单数 role 不应匹配
		{"/api/feature", false},                // 无尾部斜杠不应匹配
		{"/mcp-registry", false},               // 无尾部斜杠不应匹配
		{"/api/oauth/account", false},          // 无尾部斜杠不应匹配
		{"/v1/session_ingress/session", false}, // 无尾部斜杠不应匹配
		{"/api/oauth/organizations", false},    // 无尾部斜杠不应匹配
		{"/v1/code/sessions", false},           // 无尾部斜杠不应匹配
		{"/api/desktop/other", false},          // 非 /update 后缀
		{"/api/desktop/", false},               // 仅有前缀无 /update
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := isHardcodedEndpoint(tt.path)
			if result != tt.expected {
				t.Errorf("isHardcodedEndpoint(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestHandleFeedback(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleFeedback(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	// 验证响应包含 feedback_id
	body := rec.Body.String()
	if body == "" {
		t.Error("Response body is empty")
	}
}

func TestHandleDomainInfo(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleDomainInfo(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if body == "" {
		t.Error("Response body is empty")
	}

	// 验证响应包含 can_fetch
	if !strings.Contains(body, "can_fetch") {
		t.Error("Response should contain 'can_fetch'")
	}
}

func TestHandleOrganization(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleOrganization(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "metrics_enabled") {
		t.Error("Response should contain 'metrics_enabled'")
	}
}

func TestHandleMetricsEnabled(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleMetricsEnabled(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "metrics_logging_enabled") {
		t.Error("Response should contain 'metrics_logging_enabled'")
	}
	if strings.Contains(body, `"metrics_enabled"`) {
		t.Error("Response should NOT contain old field 'metrics_enabled'")
	}
}

func TestHandleEventLogging(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleEventLogging(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestHandleGrowthBookFeature(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleGrowthBookFeature(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "features") {
		t.Error("Response should contain 'features'")
	}
}

func TestHandleBootstrap(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleBootstrap(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	for _, field := range []string{"client_data", "additional_model_options", "cwk_cfg_key"} {
		if !strings.Contains(body, field) {
			t.Errorf("Response should contain %q, got %s", field, body)
		}
	}
}

func TestHandleMCPRegistry(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleMCPRegistry(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "servers") {
		t.Error("Response should contain 'servers'")
	}
}

func TestHandleMCPServers(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleMCPServers(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data") || !strings.Contains(body, "has_more") {
		t.Error("Response should contain 'data' and 'has_more'")
	}
}

func TestHandlePenguinMode(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handlePenguinMode(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestHandlePolicyLimits(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()
	handler.handlePolicyLimits(rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "restrictions") {
		t.Errorf("Response should contain 'restrictions', got %s", body)
	}
	if !strings.Contains(body, "compliance_taints") {
		t.Errorf("Response should contain 'compliance_taints', got %s", body)
	}
}

func TestHandleRemoteSettings(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()
	handler.handleRemoteSettings(rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "settings") {
		t.Errorf("Response should contain 'settings', got %s", body)
	}
}

func TestHandleDesktopUpdate(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	t.Run("HEAD returns 200 empty", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodHead, "/api/desktop/win32/x64/msix/update", nil)
		handler.handleDesktopUpdate(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected status %d, got %d", http.StatusOK, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("Expected empty body, got %d bytes", rec.Body.Len())
		}
	})

	t.Run("GET returns currentRelease JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/desktop/darwin/arm64/squirrel/update", nil)
		handler.handleDesktopUpdate(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected status %d, got %d", http.StatusOK, rec.Code)
		}

		body := rec.Body.String()
		if !strings.Contains(body, "currentRelease") {
			t.Errorf("Response should contain 'currentRelease', got %s", body)
		}
		if !strings.Contains(body, desktopCurrentRelease) {
			t.Errorf("Response should contain version %q, got %s", desktopCurrentRelease, body)
		}
	})

	t.Run("other methods return 405", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/desktop/win32/x64/msix/update", nil)
		handler.handleDesktopUpdate(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("Expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "HEAD, GET" {
			t.Errorf("Expected Allow header %q, got %q", "HEAD, GET", allow)
		}
	})
}

// countingReadCloser 记录 Read 调用次数的 io.ReadCloser
type countingReadCloser struct {
	reads int
	body  []byte
	pos   int
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	c.reads++
	if c.pos >= len(c.body) {
		return 0, io.EOF
	}
	n := copy(p, c.body[c.pos:])
	c.pos += n
	return n, nil
}

func (c *countingReadCloser) Close() error { return nil }

func TestDesktopUpdateNoDrainOn405(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			body := &countingReadCloser{body: []byte("large body that should not be read")}
			req := httptest.NewRequest(method, "/api/desktop/win32/x64/msix/update", nil)
			req.Body = body
			rec := httptest.NewRecorder()

			result := handler.handleHardcodedEndpoint(rec, req)
			if !result {
				t.Fatal("handleHardcodedEndpoint should return true")
			}
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("Expected %d, got %d", http.StatusMethodNotAllowed, rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "HEAD, GET" {
				t.Errorf("Expected Allow %q, got %q", "HEAD, GET", allow)
			}
			if body.reads != 0 {
				t.Errorf("Body should not be read, but Read was called %d times", body.reads)
			}
		})
	}
}

func TestHandleEmptyResponse(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	endpoints := []struct {
		path string
	}{
		{"/api/oauth/profile"},
		{"/api/claude_cli_profile"},
		{"/api/oauth/usage"},
		{"/api/claude_code/user_settings"},
		{"/api/oauth/account/settings"},
		{"/api/claude_code_grove"},
		{"/api/organization/claude_code_first_token_date"},
		{"/v1/ultrareview/quota"},
		{"/v1/session_ingress/session/test-id"},
		{"/api/claude_code/team_memory"},
		{"/api/oauth/organizations/org-123/referral/eligibility"},
		{"/api/oauth/organizations/org-123/overage_credit_grant"},
		{"/v1/code/sessions/sess-123/teleport-events"},
		{"/api/auth/trusted_devices"},
		{"/api/oauth/file_upload"},
	}

	for _, tt := range endpoints {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()

			handler.handleEmptyResponse(rec)

			if rec.Code != http.StatusOK {
				t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
			}

			body := strings.TrimSpace(rec.Body.String())
			if body != "{}" {
				t.Errorf("Expected '{}', got %q", body)
			}
		})
	}
}

func TestHandleMe(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleMe(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "id") {
		t.Error("Response should contain 'id'")
	}
}

func TestHandleMetric(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	rec := httptest.NewRecorder()

	handler.handleMetric(rec)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "success") {
		t.Error("Response should contain 'success'")
	}
}

func TestHandleHardcodedEndpoint(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	tests := []struct {
		path string
	}{
		{"/api/claude_cli_feedback"},
		{"/api/claude_code/metric"},
		{"/api/claude_code/organization"},
		{"/api/web/domain_info?domain=test.com"},
		{"/api/oauth/claude_cli/roles"},
		{"/api/claude_code/organizations/metrics_enabled"},
		{"/api/claude_code_shared_session_transcripts"},
		{"/v1/me"},
		{"/api/event_logging/batch"},
		{"/api/event_logging/v2/batch"},
		{"/api/feature/some-key"},
		{"/api/claude_cli/bootstrap"},
		{"/mcp-registry/v0/servers"},
		{"/v1/mcp_servers"},
		{"/api/claude_code_penguin_mode"},
		{"/api/oauth/profile"},
		{"/api/claude_cli_profile"},
		{"/api/oauth/usage"},
		{"/api/claude_code/policy_limits"},
		{"/api/claude_code/settings"},
		{"/api/claude_code/user_settings"},
		{"/api/oauth/account/settings"},
		{"/api/claude_code_grove"},
		{"/api/organization/claude_code_first_token_date"},
		{"/v1/ultrareview/quota"},
		{"/v1/session_ingress/session/test-id"},
		{"/api/claude_code/team_memory"},
		{"/api/oauth/organizations/org-123/referral/eligibility"},
		{"/api/auth/trusted_devices"},
		{"/api/oauth/file_upload"},
		{"/api/oauth/organizations/org-123/admin_requests"},
		{"/v1/code/sessions/sess-123/teleport-events"},
		{"/api/desktop/win32/x64/msix/update"},
		{"/api/desktop/darwin/arm64/squirrel/update"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			result := handler.handleHardcodedEndpoint(rec, req)
			if !result {
				t.Errorf("handleHardcodedEndpoint should return true for %s", tt.path)
			}
		})
	}
}
func TestHandleCountTokens(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	tests := []struct {
		name     string
		body     string
		expected int
	}{
		{"empty body", "", 1},
		{"small body", strings.Repeat("a", 400), 100},
		{"tiny body", "hi", 1},
		{"medium body", strings.Repeat("hello world ", 100), 300},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", body)
			rec := httptest.NewRecorder()

			handler.handleCountTokens(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}

			var resp struct {
				InputTokens int `json:"input_tokens"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.InputTokens != tt.expected {
				t.Errorf("input_tokens = %d, want %d", resp.InputTokens, tt.expected)
			}
		})
	}
}

func TestHandleCountTokensIntegration(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	body := `{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(body))
	rec := httptest.NewRecorder()

	result := handler.handleHardcodedEndpoint(rec, req)
	if !result {
		t.Fatal("handleHardcodedEndpoint should return true for count_tokens")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.InputTokens < 1 {
		t.Errorf("input_tokens = %d, want >= 1", resp.InputTokens)
	}
}

func TestHandleCountTokensTooLarge(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	oversized := strings.Repeat("a", maxRequestBodySize+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(oversized))
	rec := httptest.NewRecorder()

	handler.handleCountTokens(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}
