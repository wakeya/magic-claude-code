package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/providerquota"
)

func TestProviderUsageGetNotFound(t *testing.T) {
	store := config.NewMockStore(config.DefaultConfig())
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	req := httptest.NewRequest("GET", "/api/providers/nonexistent/usage", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()

	srv.handleProviderUsage(w, req)

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestProviderUsagePutAndRetrieve(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID:        "test-p",
			Name:      "Test",
			APIURL:    "https://api.example.com",
			APIToken:  "secret-token",
			Enabled:   true,
			CreatedAt: timeNow(),
			UpdatedAt: timeNow(),
		},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	// Save quota config.
	updateBody, _ := json.Marshal(map[string]any{
		"enabled":       true,
		"template_type": "general",
		"script":        "({request:{url:'{{baseUrl}}',method:'GET'},extractor:function(r){return{remaining:1};}})",
	})
	req := httptest.NewRequest("PUT", "/api/providers/test-p/usage", bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()

	srv.handleProviderUsage(w, req)

	if w.Code != 200 {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	var putResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &putResp)
	if putResp["success"] != true {
		t.Error("expected success=true")
	}

	// Retrieve.
	req2 := httptest.NewRequest("GET", "/api/providers/test-p/usage", nil)
	req2.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w2 := httptest.NewRecorder()

	srv.handleProviderUsage(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("GET status = %d", w2.Code)
	}

	var getResp map[string]any
	json.Unmarshal(w2.Body.Bytes(), &getResp)
	configDTO, ok := getResp["config"].(map[string]any)
	if !ok {
		t.Fatal("expected config in response")
	}
	if configDTO["template_type"] != "general" {
		t.Errorf("template_type = %v, want general", configDTO["template_type"])
	}
	if configDTO["enabled"] != true {
		t.Error("expected enabled=true")
	}
}

func TestProviderUsageSecretRedaction(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID:        "test-p",
			Name:      "Test",
			APIURL:    "https://api.example.com",
			APIToken:  "secret-token",
			Enabled:   true,
			QuotaQuery: &providerquota.ProviderQuotaConfig{
				Enabled:      true,
				TemplateType: "newapi",
				AccessToken:  "super-secret-at",
				APIKey:       "super-secret-key",
				SecretAccessKey: "super-secret-sk",
				AccessKeyID:  "AKLT1234",
			},
			CreatedAt: timeNow(),
			UpdatedAt: timeNow(),
		},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	req := httptest.NewRequest("GET", "/api/providers/test-p/usage", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()

	srv.handleProviderUsage(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	body := w.Body.String()
	// Must not contain raw secrets.
	for _, secret := range []string{"super-secret-at", "super-secret-key", "super-secret-sk"} {
		if containsStr(body, secret) {
			t.Errorf("response contains secret %q", secret)
		}
	}

	// Must contain configured flags.
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	cfgDTO := resp["config"].(map[string]any)
	if cfgDTO["api_key_configured"] != true {
		t.Error("expected api_key_configured=true")
	}
	if cfgDTO["access_token_configured"] != true {
		t.Error("expected access_token_configured=true")
	}
	if cfgDTO["secret_access_key_configured"] != true {
		t.Error("expected secret_access_key_configured=true")
	}
	if cfgDTO["access_key_id"] != "****" {
		t.Errorf("access_key_id = %v, want **** (masked)", cfgDTO["access_key_id"])
	}
}

func TestProviderUsageMethodNotAllowed(t *testing.T) {
	store := config.NewMockStore(config.DefaultConfig())
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	req := httptest.NewRequest("PATCH", "/api/providers/test-p/usage", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()

	srv.handleProviderUsage(w, req)

	if w.Code != 405 {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestProviderUsageTestNoManager(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "test-p", Name: "Test", APIURL: "https://api.example.com", APIToken: "tok", Enabled: true, CreatedAt: timeNow(), UpdatedAt: timeNow()},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	body, _ := json.Marshal(map[string]any{"enabled": true, "template_type": "general"})
	req := httptest.NewRequest("POST", "/api/providers/test-p/usage/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()

	srv.handleProviderUsageTest(w, req)

	if w.Code != 500 {
		t.Errorf("status = %d, want 500 (no manager)", w.Code)
	}
}

func TestProviderUsageQueryNoManager(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID:       "test-p",
			Name:     "Test",
			APIURL:   "https://api.example.com",
			APIToken: "tok",
			Enabled:  true,
			QuotaQuery: &providerquota.ProviderQuotaConfig{
				Enabled:      true,
				TemplateType: "general",
			},
			CreatedAt: timeNow(),
			UpdatedAt: timeNow(),
		},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	req := httptest.NewRequest("POST", "/api/providers/test-p/usage/query", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()

	srv.handleProviderUsageQuery(w, req)

	if w.Code != 500 {
		t.Errorf("status = %d, want 500 (no manager)", w.Code)
	}
}

func TestApplyQuotaUpdateSecretPatch(t *testing.T) {
	// Test: empty value keeps existing (AccessToken belongs to NewAPI, so the
	// fixture sets that template so NormalizeForTemplate treats it as applicable).
	existing := &providerquota.ProviderQuotaConfig{
		TemplateType: providerquota.TemplateNewAPI,
		AccessToken:  "existing-token",
		BaseURL:      "https://panel.example.com",
	}
	req := providerQuotaUpdateRequest{} // No AccessToken set.
	result := applyQuotaUpdate(existing, req)
	if result.AccessToken != "existing-token" {
		t.Errorf("access_token = %q, want existing-token", result.AccessToken)
	}

	// Test: clear flag clears the field.
	req2 := providerQuotaUpdateRequest{ClearAccessToken: true}
	result2 := applyQuotaUpdate(existing, req2)
	if result2.AccessToken != "" {
		t.Errorf("access_token after clear = %q, want empty", result2.AccessToken)
	}
}

// TestSaveNormalizesInapplicableFields verifies the backend safety boundary:
// saving a Kimi token_plan config clears stale ZenMux APIKey / NewAPI
// AccessToken / Volcengine AK-SK left over from a previous configuration.
func TestSaveNormalizesInapplicableFields(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID: "test-p", Name: "Test", APIURL: "https://api.kimi.com/coding/v1",
			APIToken: "tok", Enabled: true,
			QuotaQuery: &providerquota.ProviderQuotaConfig{
				Enabled:            true,
				TemplateType:       "token_plan",
				CodingPlanProvider: "zenmux",
				BaseURL:            "https://quota.zenmux.example/v1",
				APIKey:             "stale-zenmux-key",
				AccessToken:        "stale-newapi-tok",
				UserID:             "stale-u1",
				AccessKeyID:        "stale-ak",
				SecretAccessKey:    "stale-sk",
			},
			CreatedAt: timeNow(), UpdatedAt: timeNow(),
		},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	// Save: switch to Kimi token_plan (no secrets provided → keep applicable;
	// normalize clears inapplicable ones).
	body, _ := json.Marshal(map[string]any{
		"enabled":              true,
		"template_type":        "token_plan",
		"coding_plan_provider": "kimi",
	})
	req := httptest.NewRequest("PUT", "/api/providers/test-p/usage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()
	srv.handleProviderUsage(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	// Reload and verify stale fields were normalized away.
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := loaded.GetProviderByID("test-p")
	if p == nil || p.QuotaQuery == nil {
		t.Fatal("provider/quota missing")
	}
	q := p.QuotaQuery
	if q.APIKey != "" {
		t.Errorf("APIKey = %q, want cleared for kimi", q.APIKey)
	}
	if q.AccessToken != "" {
		t.Errorf("AccessToken = %q, want cleared for kimi", q.AccessToken)
	}
	if q.BaseURL != "" {
		t.Errorf("BaseURL = %q, want cleared for kimi", q.BaseURL)
	}
	if q.AccessKeyID != "" || q.SecretAccessKey != "" {
		t.Errorf("AK/SK = %q/%q, want cleared for kimi", q.AccessKeyID, q.SecretAccessKey)
	}
	if q.CodingPlanProvider != "kimi" {
		t.Errorf("coding_plan_provider = %q, want kimi", q.CodingPlanProvider)
	}
}

func TestIsMaterialQuotaChange(t *testing.T) {
	old := &providerquota.ProviderQuotaConfig{
		TemplateType: "general",
		Script:       "script1",
		APIKey:       "key1",
	}
	tests := []struct {
		name   string
		new    *providerquota.ProviderQuotaConfig
		expect bool
	}{
		{"same config", &providerquota.ProviderQuotaConfig{TemplateType: "general", Script: "script1", APIKey: "key1"}, false},
		{"template change", &providerquota.ProviderQuotaConfig{TemplateType: "custom", Script: "script1", APIKey: "key1"}, true},
		{"script change", &providerquota.ProviderQuotaConfig{TemplateType: "general", Script: "script2", APIKey: "key1"}, true},
		{"key change", &providerquota.ProviderQuotaConfig{TemplateType: "general", Script: "script1", APIKey: "key2"}, true},
		{"interval only", &providerquota.ProviderQuotaConfig{TemplateType: "general", Script: "script1", APIKey: "key1", AutoQueryIntervalMinutes: 10}, false},
		{"nil old", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMaterialQuotaChange(old, tt.new)
			if got != tt.expect {
				t.Errorf("isMaterialQuotaChange = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestSanitizeSnapshot(t *testing.T) {
	snap := &providerquota.QuotaSnapshot{
		ProviderID: "test-p",
		Result: &providerquota.ProviderQuotaResult{
			Success:   false,
			ErrorCode: "invalid_credentials",
		},
		LastSuccess: &providerquota.ProviderQuotaResult{
			Success: true,
		},
	}
	dto := sanitizeSnapshot(snap)
	if !dto.IsStale {
		t.Error("expected IsStale=true for failed result with last success")
	}
	if !dto.HasLastSuccess {
		t.Error("expected HasLastSuccess=true")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func timeNow() time.Time {
	return time.Now()
}
