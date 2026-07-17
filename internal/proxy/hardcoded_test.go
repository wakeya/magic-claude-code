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
		// CC 2.1.211 新增
		{"/v1/design/grants", true},
		{"/v1/ultrareview/preflight", true},
		// CC 2.1.211 新增前缀匹配
		{"/v1/code/triggers", true},
		{"/v1/code/triggers/t1", true},
		{"/v1/code/triggers/t1/run", true},
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

	for _, flag := range []string{
		"tengu_coral_fern", "tengu_glacier_2xr", "tengu_hive_evidence",
		"tengu_permission_friction", "tengu_log_datadog_events",
	} {
		if !strings.Contains(body, flag) {
			t.Errorf("Response should contain feature flag %q", flag)
		}
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
		{"/v1/ultrareview/preflight"},
		{"/v1/design/grants"},
		{"/v1/code/triggers"},
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

func TestHandleBootstrap_EmitsExposedModels(t *testing.T) {
	store := config.NewMockStore(&config.Config{
		Providers: []config.Provider{
			{ID: "a", Name: "智谱", Enabled: true, ExposedModels: []config.ExposedModel{
				{ID: "glm-4.6", Label: "GLM-4.6", Description: "日常编码", BackendModel: "glm-4.6"},
			}},
			{ID: "b", Name: "B", Enabled: false, ExposedModels: []config.ExposedModel{
				{ID: "disabled-model", Label: "Disabled"},
			}},
		},
	})
	handler := &Handler{configStore: store}
	rec := httptest.NewRecorder()
	handler.handleBootstrap(rec)

	var resp struct {
		ClientData             any                 `json:"client_data"`
		AdditionalModelOptions []map[string]string `json:"additional_model_options"`
		CwkCfgKey              any                 `json:"cwk_cfg_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.AdditionalModelOptions) != 1 {
		t.Fatalf("expected 1 option (disabled provider excluded), got %d: %v",
			len(resp.AdditionalModelOptions), resp.AdditionalModelOptions)
	}
	opt := resp.AdditionalModelOptions[0]
	if opt["model"] != "glm-4.6" || opt["name"] != "GLM-4.6" || opt["description"] != "日常编码 · 智谱" {
		t.Fatalf("unexpected option fields (description should auto-append provider name): %v", opt)
	}
}

func TestHandleBootstrap_DescriptionEmptyUsesProviderName(t *testing.T) {
	store := config.NewMockStore(&config.Config{
		Providers: []config.Provider{
			{ID: "a", Name: "月之暗面", Enabled: true, ExposedModels: []config.ExposedModel{
				{ID: "kimi-k2", Label: "Kimi K2", Description: ""}, // description 空
			}},
		},
	})
	handler := &Handler{configStore: store}
	rec := httptest.NewRecorder()
	handler.handleBootstrap(rec)

	var resp struct {
		AdditionalModelOptions []map[string]string `json:"additional_model_options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.AdditionalModelOptions) != 1 {
		t.Fatalf("expected 1 option, got %d", len(resp.AdditionalModelOptions))
	}
	// description 为空时只用 provider 名
	if resp.AdditionalModelOptions[0]["description"] != "月之暗面" {
		t.Fatalf("expected description=月之暗面, got %q", resp.AdditionalModelOptions[0]["description"])
	}
}

func TestHandleBootstrap_NoConfigReturnsEmpty(t *testing.T) {
	handler := &Handler{configStore: nil} // 无 store
	rec := httptest.NewRecorder()
	handler.handleBootstrap(rec)
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	arr, ok := resp["additional_model_options"].([]any)
	if !ok || len(arr) != 0 {
		t.Fatalf("expected empty array, got %v", resp["additional_model_options"])
	}
}

func TestHandleBootstrap_Context1MAppendsBracket1m(t *testing.T) {
	store := config.NewMockStore(&config.Config{
		Providers: []config.Provider{
			{ID: "a", Name: "GLM", Enabled: true, ExposedModels: []config.ExposedModel{
				{ID: "glm-5.2", Label: "GLM-5.2", BackendModel: "glm-5.2", Context1M: true},
				{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"}, // Context1M=false
			}},
		},
	})
	handler := &Handler{configStore: store}
	rec := httptest.NewRecorder()
	handler.handleBootstrap(rec)

	var resp struct {
		AdditionalModelOptions []map[string]string `json:"additional_model_options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.AdditionalModelOptions) != 2 {
		t.Fatalf("expected 2 options, got %d", len(resp.AdditionalModelOptions))
	}
	byModel := map[string]string{}
	for _, opt := range resp.AdditionalModelOptions {
		byModel[opt["name"]] = opt["model"]
	}
	// Context1M=true 的模型，菜单 value 附 [1m]
	if byModel["GLM-5.2"] != "glm-5.2[1m]" {
		t.Fatalf("Context1M model value = %q, want glm-5.2[1m]", byModel["GLM-5.2"])
	}
	// Context1M=false 的模型，菜单 value 保持纯 ID
	if byModel["GLM-4.6"] != "glm-4.6" {
		t.Fatalf("non-1M model value = %q, want glm-4.6", byModel["GLM-4.6"])
	}
}

// TestStaticProbeEndpointsAreLocal 验证浏览器/静态探测路径本地返回 404 空 body，
// 不转发给上游 provider。
func TestStaticProbeEndpointsAreLocal(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)
	probes := []string{
		"/favicon.ico",
		"/robots.txt",
		"/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png",
	}
	for _, path := range probes {
		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			if !handler.handleHardcodedEndpoint(rec, req) {
				t.Fatalf("handleHardcodedEndpoint should handle %s", path)
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("want empty body, got %q", rec.Body.String())
			}
		})
	}
}

// TestHardcodedTelemetryOTLPEndpoints 验证 OTLP 遥测端点本地 204（POST）/ 405（非 POST），
// 且不解析 payload。
func TestHardcodedTelemetryOTLPEndpoints(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)
	endpoints := []string{"/v1/metrics", "/v1/logs", "/v1/traces"}

	t.Run("POST returns 204 empty without parsing body", func(t *testing.T) {
		for _, path := range endpoints {
			largeBody := strings.Repeat("x", 64*1024) // 大体积遥测 body，不应被解析
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(largeBody))
			rec := httptest.NewRecorder()
			if !handler.handleHardcodedEndpoint(rec, req) {
				t.Fatalf("should handle %s", path)
			}
			if rec.Code != http.StatusNoContent {
				t.Errorf("%s status = %d, want 204", path, rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("%s want empty body, got %q", path, rec.Body.String())
			}
		}
	})

	t.Run("non-POST returns 405", func(t *testing.T) {
		for _, path := range endpoints {
			for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
				req := httptest.NewRequest(method, path, nil)
				rec := httptest.NewRecorder()
				handler.handleHardcodedEndpoint(rec, req)
				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("%s %s status = %d, want 405", method, path, rec.Code)
				}
				if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
					t.Errorf("%s %s Allow = %q, want POST", method, path, allow)
				}
			}
		}
	})

	t.Run("POST with oversized body returns 204 via bounded drain", func(t *testing.T) {
		// 超过 maxLocalDrainSize 的遥测 body：有界 drain 后仍返回 204，不挂起（gpt-5.6 审查点）。
		huge := strings.Repeat("x", int(maxLocalDrainSize)+2*1024*1024)
		req := httptest.NewRequest(http.MethodPost, "/v1/metrics", strings.NewReader(huge))
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (bounded drain must not error)", rec.Code)
		}
	})
}

// TestHardcodedModelsUsesConfiguredProviders 验证 /v1/models 从 MCC 现有 provider/model
// 配置派生模型列表（不新建平行 registry），按 id 去重排序，配置为空时返回空列表。
func TestHardcodedModelsUsesConfiguredProviders(t *testing.T) {
	store := config.NewMockStore(&config.Config{
		Providers: []config.Provider{
			{ID: "a", Name: "智谱", Enabled: true, ExposedModels: []config.ExposedModel{
				{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"},
				{ID: "kimi-k2", Label: "Kimi K2", BackendModel: "kimi"},
			}},
			{ID: "b", Name: "Disabled", Enabled: false, ExposedModels: []config.ExposedModel{
				{ID: "disabled-model", Label: "D"},
			}},
		},
	})
	handler := &Handler{configStore: store}

	t.Run("GET returns config-derived models sorted by id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle /v1/models")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Data []struct {
				ID          string `json:"id"`
				Type        string `json:"type"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			HasMore bool `json:"has_more"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		if resp.HasMore {
			t.Errorf("has_more = true, want false")
		}
		// disabled provider 不应出现
		if len(resp.Data) != 2 {
			t.Fatalf("data len = %d, want 2 (disabled provider excluded): %+v", len(resp.Data), resp.Data)
		}
		// 按 id 升序
		if resp.Data[0].ID != "glm-4.6" || resp.Data[1].ID != "kimi-k2" {
			t.Fatalf("unexpected order: %s, %s", resp.Data[0].ID, resp.Data[1].ID)
		}
		for _, m := range resp.Data {
			if m.Type != "model" {
				t.Errorf("model %s type = %q, want model", m.ID, m.Type)
			}
		}
		// display_name 用 Label（GLM-4.6 / Kimi K2），id 保持不变用于模型选择
		byID := map[string]string{}
		for _, m := range resp.Data {
			byID[m.ID] = m.DisplayName
		}
		if byID["glm-4.6"] != "GLM-4.6" {
			t.Errorf("glm-4.6 display_name = %q, want Label 'GLM-4.6'", byID["glm-4.6"])
		}
		if byID["kimi-k2"] != "Kimi K2" {
			t.Errorf("kimi-k2 display_name = %q, want Label 'Kimi K2'", byID["kimi-k2"])
		}
	})

	t.Run("empty Label falls back to id for display_name", func(t *testing.T) {
		// dedup fixture 的 dup/uniq 都没设 Label，display_name 应回退 id
		dupStore := config.NewMockStore(&config.Config{
			Providers: []config.Provider{
				{ID: "a", Name: "A", Enabled: true, ExposedModels: []config.ExposedModel{{ID: "dup"}}},
				{ID: "b", Name: "B", Enabled: true, ExposedModels: []config.ExposedModel{{ID: "uniq"}}},
			},
		})
		h := &Handler{configStore: dupStore}
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()
		h.handleHardcodedEndpoint(rec, req)
		var resp struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		for _, m := range resp.Data {
			if m.DisplayName != m.ID {
				t.Errorf("model %s display_name = %q, want fallback to id", m.ID, m.DisplayName)
			}
		}
	})

	t.Run("dedupes duplicate model ids across providers", func(t *testing.T) {
		dupStore := config.NewMockStore(&config.Config{
			Providers: []config.Provider{
				{ID: "a", Name: "A", Enabled: true, ExposedModels: []config.ExposedModel{{ID: "dup"}}},
				{ID: "b", Name: "B", Enabled: true, ExposedModels: []config.ExposedModel{{ID: "dup"}, {ID: "uniq"}}},
			},
		})
		h := &Handler{configStore: dupStore}
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()
		h.handleHardcodedEndpoint(rec, req)
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if len(resp.Data) != 2 {
			t.Fatalf("data len = %d, want 2 (dup + uniq)", len(resp.Data))
		}
	})

	t.Run("empty config returns empty data", func(t *testing.T) {
		h := &Handler{configStore: config.NewMockStore(nil)}
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()
		h.handleHardcodedEndpoint(rec, req)
		var resp struct {
			Data    []any `json:"data"`
			HasMore bool  `json:"has_more"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if len(resp.Data) != 0 {
			t.Fatalf("data len = %d, want 0", len(resp.Data))
		}
		if resp.HasMore {
			t.Errorf("has_more = true, want false")
		}
	})

	t.Run("nil configStore returns empty data", func(t *testing.T) {
		h := &Handler{configStore: nil}
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		rec := httptest.NewRecorder()
		h.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("non-GET returns 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
		rec := httptest.NewRecorder()
		handler.handleHardcodedEndpoint(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
			t.Errorf("Allow = %q, want GET", allow)
		}
	})
}

// TestHardcodedLowRiskClaudeCodeEndpoints 验证低风险 Claude Code API 端点本地返回
// 兼容空状态，并回归断言 count_tokens 在加入默认拦截 guard 后仍本地处理。
func TestHardcodedLowRiskClaudeCodeEndpoints(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	t.Run("team_usage returns empty aggregates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/claude_code/discovery/team_usage", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle team_usage")
		}
		var resp map[string]json.RawMessage
		json.NewDecoder(rec.Body).Decode(&resp)
		for _, key := range []string{"teams", "usage", "data"} {
			raw, ok := resp[key]
			if !ok {
				t.Fatalf("response missing %q: %v", key, resp)
			}
			if string(raw) != "[]" {
				t.Errorf("%s = %s, want []", key, string(raw))
			}
		}
	})

	t.Run("notification preferences returns disabled state", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/claude_code/notification/preferences", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle notification/preferences")
		}
		var resp struct {
			Preferences          map[string]any `json:"preferences"`
			NotificationsEnabled bool           `json:"notifications_enabled"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if resp.NotificationsEnabled {
			t.Errorf("notifications_enabled = true, want false")
		}
	})

	t.Run("onboarding returns empty object for multiple methods", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch} {
			req := httptest.NewRequest(method, "/api/organizations/org-abc/claude_code/onboarding", nil)
			rec := httptest.NewRecorder()
			if !handler.handleHardcodedEndpoint(rec, req) {
				t.Fatalf("%s onboarding should be handled", method)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("%s status = %d, want 200", method, rec.Code)
			}
			if strings.TrimSpace(rec.Body.String()) != "{}" {
				t.Errorf("%s body = %q, want {}", method, rec.Body.String())
			}
		}
	})

	t.Run("ultrareview preflight returns empty object", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/ultrareview/preflight", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle ultrareview preflight")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if strings.TrimSpace(rec.Body.String()) != "{}" {
			t.Errorf("body = %q, want {}", rec.Body.String())
		}
	})

	t.Run("count_tokens still local after fail-closed guard", func(t *testing.T) {
		body := `{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hello"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(body))
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("count_tokens should be handled locally")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	// 低风险 GET-only 端点：非 GET 应返回 405（gpt-5.6 审查点：补 method checks）。
	for _, path := range []string{
		"/api/claude_code/discovery/team_usage",
		"/api/claude_code/notification/preferences",
		"/api/claude_code/skills",
	} {
		t.Run("POST "+path+" returns 405", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			handler.handleHardcodedEndpoint(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
				t.Errorf("Allow = %q, want GET", allow)
			}
		})
	}
}

// TestHardcodedDesignGrants 验证 CC 2.1.211 的 /v1/design/grants 端点：
// GET 返回空授权禁用 Design 授权；POST 写入门关闭；其它方法 405。
func TestHardcodedDesignGrants(t *testing.T) {
	handler := NewHandler(config.NewMockStore(nil), nil)

	t.Run("GET returns empty grants", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/design/grants", nil)
		rec := httptest.NewRecorder()
		if !handler.handleHardcodedEndpoint(rec, req) {
			t.Fatal("should handle design grants")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Grants []any `json:"grants"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Grants) != 0 {
			t.Errorf("grants = %v, want empty", resp.Grants)
		}
	})

	t.Run("POST returns 403 write_gate_disabled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/design/grants", strings.NewReader(`{"project_id":"p1"}`))
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

	t.Run("PUT returns 405 with Allow GET, POST", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/design/grants", nil)
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
