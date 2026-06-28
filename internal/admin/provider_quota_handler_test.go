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

type adminQuotaConfigGetter struct {
	provider providerquota.ProviderConfig
}

func (g *adminQuotaConfigGetter) GetProviderByID(id string) *providerquota.ProviderConfig {
	if g.provider.ID != id {
		return nil
	}
	p := g.provider
	return &p
}

func (g *adminQuotaConfigGetter) ListEnabledProviders() []providerquota.ProviderConfig {
	if !g.provider.Enabled {
		return nil
	}
	return []providerquota.ProviderConfig{g.provider}
}

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
			ID:       "test-p",
			Name:     "Test",
			APIURL:   "https://api.example.com",
			APIToken: "secret-token",
			Enabled:  true,
			QuotaQuery: &providerquota.ProviderQuotaConfig{
				Enabled:         true,
				TemplateType:    "newapi",
				AccessToken:     "super-secret-at",
				ScriptAPIKey:    "super-secret-script-key",
				ZenMuxBaseURL:   "https://quota.zenmux.example/usage",
				ZenMuxAPIKey:    "super-secret-zenmux-key",
				SecretAccessKey: "super-secret-sk",
				AccessKeyID:     "AKLT1234",
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
	for _, secret := range []string{"super-secret-at", "super-secret-script-key", "super-secret-zenmux-key", "super-secret-sk"} {
		if containsStr(body, secret) {
			t.Errorf("response contains secret %q", secret)
		}
	}

	// Must contain configured flags.
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	cfgDTO := resp["config"].(map[string]any)
	if cfgDTO["script_api_key_configured"] != true || cfgDTO["zenmux_api_key_configured"] != true {
		t.Error("expected separated configured flags")
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
	result := applyQuotaUpdate(existing, req, "")
	if result.AccessToken != "existing-token" {
		t.Errorf("access_token = %q, want existing-token", result.AccessToken)
	}

	// Test: clear flag clears the field.
	req2 := providerQuotaUpdateRequest{ClearAccessToken: true}
	result2 := applyQuotaUpdate(existing, req2, "")
	if result2.AccessToken != "" {
		t.Errorf("access_token after clear = %q, want empty", result2.AccessToken)
	}
}

func TestApplyQuotaUpdateSeparatesCredentialPurposes(t *testing.T) {
	str := func(v string) *string { return &v }

	t.Run("General to ZenMux preserves replacement and existing script key", func(t *testing.T) {
		existing := &providerquota.ProviderQuotaConfig{
			Enabled:      true,
			TemplateType: providerquota.TemplateGeneral,
			ScriptAPIKey: "script-old",
		}
		result := applyQuotaUpdate(existing, providerQuotaUpdateRequest{
			TemplateType:       str(providerquota.TemplateTokenPlan),
			CodingPlanProvider: str("zenmux"),
			ZenMuxBaseURL:      str("https://quota.zenmux.example/usage"),
			ZenMuxAPIKey:       str("zenmux-new"),
		}, "https://api.zenmux.example/v1")

		if result.ScriptAPIKey != "script-old" || result.ZenMuxAPIKey != "zenmux-new" {
			t.Fatalf("separated keys = %q/%q", result.ScriptAPIKey, result.ZenMuxAPIKey)
		}
		if result.ZenMuxBaseURL != "https://quota.zenmux.example/usage" {
			t.Fatalf("ZenMuxBaseURL = %q", result.ZenMuxBaseURL)
		}
	})

	t.Run("ZenMux to General preserves replacement and existing ZenMux key", func(t *testing.T) {
		existing := &providerquota.ProviderQuotaConfig{
			Enabled:            true,
			TemplateType:       providerquota.TemplateTokenPlan,
			CodingPlanProvider: "zenmux",
			ZenMuxBaseURL:      "https://quota.zenmux.example/usage",
			ZenMuxAPIKey:       "zenmux-old",
		}
		result := applyQuotaUpdate(existing, providerQuotaUpdateRequest{
			TemplateType: str(providerquota.TemplateGeneral),
			ScriptAPIKey: str("script-new"),
		}, "https://gateway.example/v1")

		if result.ScriptAPIKey != "script-new" || result.ZenMuxAPIKey != "zenmux-old" {
			t.Fatalf("separated keys = %q/%q", result.ScriptAPIKey, result.ZenMuxAPIKey)
		}
	})

	t.Run("clear flags are independent", func(t *testing.T) {
		existing := &providerquota.ProviderQuotaConfig{
			TemplateType:  providerquota.TemplateGeneral,
			ScriptAPIKey:  "script",
			ZenMuxBaseURL: "https://quota.zenmux.example/usage",
			ZenMuxAPIKey:  "zenmux",
		}
		result := applyQuotaUpdate(existing, providerQuotaUpdateRequest{ClearScriptAPIKey: true}, "")
		if result.ScriptAPIKey != "" || result.ZenMuxAPIKey != "zenmux" {
			t.Fatalf("separated keys = %q/%q", result.ScriptAPIKey, result.ZenMuxAPIKey)
		}
	})
}

func TestApplyQuotaUpdateRoutesLegacyAPIKeyByEffectivePurpose(t *testing.T) {
	str := func(v string) *string { return &v }

	general := applyQuotaUpdate(nil, providerQuotaUpdateRequest{
		TemplateType: str(providerquota.TemplateGeneral),
		APIKey:       str("legacy-script"),
	}, "https://gateway.example/v1")
	if general.ScriptAPIKey != "legacy-script" || general.ZenMuxAPIKey != "" {
		t.Fatalf("general legacy route = %+v", general)
	}

	zenmux := applyQuotaUpdate(nil, providerQuotaUpdateRequest{
		TemplateType:       str(providerquota.TemplateTokenPlan),
		CodingPlanProvider: str("zenmux"),
		BaseURL:            str("https://quota.zenmux.example/usage"),
		APIKey:             str("legacy-zenmux"),
	}, "https://api.zenmux.example/v1")
	if zenmux.ZenMuxBaseURL != "https://quota.zenmux.example/usage" || zenmux.ZenMuxAPIKey != "legacy-zenmux" {
		t.Fatalf("ZenMux legacy route = %+v", zenmux)
	}
	if zenmux.BaseURL != "" || zenmux.ScriptAPIKey != "" {
		t.Fatalf("legacy fields routed to wrong purpose: %+v", zenmux)
	}
}

func TestProviderUsageEffectiveValidationRejectsBeforeSaveOrTest(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		cardURL string
		body    map[string]any
	}{
		{
			name:    "PUT rejects auto ZenMux half override",
			method:  http.MethodPut,
			path:    "/api/providers/test-p/usage",
			cardURL: "https://api.zenmux.example/v1",
			body: map[string]any{
				"enabled": true, "template_type": "token_plan",
				"zenmux_base_url": "https://quota.zenmux.example/usage",
			},
		},
		{
			name:    "test rejects auto ZenMux half override",
			method:  http.MethodPost,
			path:    "/api/providers/test-p/usage/test",
			cardURL: "https://api.zenmux.example/v1",
			body: map[string]any{
				"enabled": true, "template_type": "token_plan",
				"zenmux_api_key": "key-only",
			},
		},
		{
			name:    "PUT rejects auto Volcengine without AK SK",
			method:  http.MethodPut,
			path:    "/api/providers/test-p/usage",
			cardURL: "https://ark.cn-shanghai.volces.com/api/v3",
			body: map[string]any{
				"enabled": true, "template_type": "token_plan",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Providers = []config.Provider{{
				ID: "test-p", Name: "Test", APIURL: tt.cardURL, APIToken: "card-token", Enabled: true,
				QuotaQuery: &providerquota.ProviderQuotaConfig{Enabled: false, TemplateType: providerquota.TemplateGeneral, ScriptAPIKey: "sentinel"},
				CreatedAt:  timeNow(), UpdatedAt: timeNow(),
			}}
			store := config.NewMockStore(cfg)
			srv := NewServer(&AdminConfig{Password: "test"}, store, nil)
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
			w := httptest.NewRecorder()
			if tt.method == http.MethodPut {
				srv.handleProviderUsage(w, req)
			} else {
				srv.handleProviderUsageTest(w, req)
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}

			loaded, err := store.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			stored := loaded.GetProviderByID("test-p").QuotaQuery
			if stored.TemplateType != providerquota.TemplateGeneral || stored.ScriptAPIKey != "sentinel" {
				t.Fatalf("invalid update mutated storage: %+v", stored)
			}
		})
	}
}

func TestProviderUsageTestUsesSeparatedCredentialForActivePurpose(t *testing.T) {
	tests := []struct {
		name         string
		body         func(string) map[string]any
		responseBody string
		wantAuth     string
	}{
		{
			name: "General uses script_api_key",
			body: func(serverURL string) map[string]any {
				return map[string]any{
					"enabled": true, "template_type": "general",
					"base_url": serverURL, "script_api_key": "script-secret",
					"zenmux_api_key": "must-not-leak",
					"script":         `({request:{url:"{{baseUrl}}",method:"GET",headers:{"Authorization":"Bearer {{apiKey}}"}},extractor:function(r){return{remaining:r.balance};}})`,
				}
			},
			responseBody: `{"balance":5}`,
			wantAuth:     "Bearer script-secret",
		},
		{
			name: "ZenMux uses zenmux_api_key",
			body: func(serverURL string) map[string]any {
				return map[string]any{
					"enabled": true, "template_type": "token_plan", "coding_plan_provider": "zenmux",
					"zenmux_base_url": serverURL, "zenmux_api_key": "zenmux-secret",
					"script_api_key": "must-not-leak",
				}
			},
			responseBody: `{"success":true,"data":{"quota_5_hour":{"usage_percentage":0.1,"max_value_usd":100}}}`,
			wantAuth:     "Bearer zenmux-secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer upstream.Close()

			cfg := config.DefaultConfig()
			cfg.Providers = []config.Provider{{
				ID: "test-p", Name: "Test", APIURL: upstream.URL, APIToken: "card-token", Enabled: true,
				CreatedAt: timeNow(), UpdatedAt: timeNow(),
			}}
			store := config.NewMockStore(cfg)
			srv := NewServer(&AdminConfig{Password: "test"}, store, nil)
			mgr := providerquota.NewManager(nil, &adminQuotaConfigGetter{provider: providerquota.ProviderConfig{
				ID: "test-p", Enabled: true, APIURL: upstream.URL, APIToken: "card-token",
			}}, 1)
			srv.SetQuotaManager(mgr)

			body, _ := json.Marshal(tt.body(upstream.URL))
			req := httptest.NewRequest(http.MethodPost, "/api/providers/test-p/usage/test", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
			w := httptest.NewRecorder()
			srv.handleProviderUsageTest(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
			}
			if gotAuth != tt.wantAuth {
				t.Fatalf("Authorization = %q, want %q", gotAuth, tt.wantAuth)
			}
		})
	}
}

// TestSaveNormalizesInapplicableFields verifies template-specific stale fields
// are cleared while independently bound ZenMux credentials remain stored.
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
				LegacyAPIKey:       "stale-zenmux-key",
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
	if q.ZenMuxBaseURL != "https://quota.zenmux.example/v1" || q.ZenMuxAPIKey != "stale-zenmux-key" {
		t.Errorf("separated ZenMux override not migrated/preserved: %+v", q)
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

// TestSaveAutoDetectedZenMuxRetainsCredentials verifies backward-compatible
// base_url/api_key input is routed to the separated ZenMux fields.
func TestSaveAutoDetectedZenMuxRetainsCredentials(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID: "test-p", Name: "Test", APIURL: "https://zenmux.example.com/v1",
			APIToken: "tok", Enabled: true, CreatedAt: timeNow(), UpdatedAt: timeNow(),
		},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	body, _ := json.Marshal(map[string]any{
		"enabled":       true,
		"template_type": "token_plan",
		"base_url":      "https://quota.zenmux.example/v1",
		"api_key":       "zenmux-dedicated-key",
	})
	req := httptest.NewRequest("PUT", "/api/providers/test-p/usage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()
	srv.handleProviderUsage(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	loaded, _ := store.Load()
	q := loaded.GetProviderByID("test-p").QuotaQuery
	if q.ZenMuxBaseURL != "https://quota.zenmux.example/v1" {
		t.Errorf("ZenMuxBaseURL = %q, want retained for auto-detected zenmux", q.ZenMuxBaseURL)
	}
	if q.ZenMuxAPIKey != "zenmux-dedicated-key" {
		t.Errorf("ZenMuxAPIKey = %q, want retained for auto-detected zenmux", q.ZenMuxAPIKey)
	}
}

// TestSaveAutoDetectedVolcengineRetainsAKSK verifies auto-detected Volcengine
// retains AK/SK.
func TestSaveAutoDetectedVolcengineRetainsAKSK(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID: "test-p", Name: "Test", APIURL: "https://ark.cn-beijing.volces.com/api/v3",
			APIToken: "tok", Enabled: true, CreatedAt: timeNow(), UpdatedAt: timeNow(),
		},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	body, _ := json.Marshal(map[string]any{
		"enabled":           true,
		"template_type":     "token_plan",
		"access_key_id":     "AKLT1234",
		"secret_access_key": "secret-sk",
	})
	req := httptest.NewRequest("PUT", "/api/providers/test-p/usage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()
	srv.handleProviderUsage(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	loaded, _ := store.Load()
	q := loaded.GetProviderByID("test-p").QuotaQuery
	if q.AccessKeyID != "AKLT1234" || q.SecretAccessKey != "secret-sk" {
		t.Errorf("AK/SK = %q/%q, want retained for auto-detected volcengine", q.AccessKeyID, q.SecretAccessKey)
	}
}

// TestSaveGeneralToZenMuxKeepsScriptKeySeparated verifies switching to ZenMux
// without an override uses the card fallback and does not reinterpret the
// existing script key.
func TestSaveGeneralToZenMuxKeepsScriptKeySeparated(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID: "test-p", Name: "Test", APIURL: "https://zenmux.example.com/v1",
			APIToken: "tok", Enabled: true,
			QuotaQuery: &providerquota.ProviderQuotaConfig{
				Enabled:      true,
				TemplateType: "general",
				ScriptAPIKey: "general-override-secret",
			},
			CreatedAt: timeNow(), UpdatedAt: timeNow(),
		},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	body, _ := json.Marshal(map[string]any{
		"enabled":              true,
		"template_type":        "token_plan",
		"coding_plan_provider": "zenmux",
	})
	req := httptest.NewRequest("PUT", "/api/providers/test-p/usage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()
	srv.handleProviderUsage(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	loaded, _ := store.Load()
	q := loaded.GetProviderByID("test-p").QuotaQuery
	if q.ScriptAPIKey != "general-override-secret" || q.ZenMuxAPIKey != "" {
		t.Errorf("separated keys = %q/%q", q.ScriptAPIKey, q.ZenMuxAPIKey)
	}
}

// TestSaveZenMuxToGeneralKeepsZenMuxKeySeparated verifies the reverse switch.
func TestSaveZenMuxToGeneralKeepsZenMuxKeySeparated(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID: "test-p", Name: "Test", APIURL: "https://gw.example.com/v1",
			APIToken: "tok", Enabled: true,
			QuotaQuery: &providerquota.ProviderQuotaConfig{
				Enabled:            true,
				TemplateType:       "token_plan",
				CodingPlanProvider: "zenmux",
				ZenMuxBaseURL:      "https://quota.zenmux.example/v1",
				ZenMuxAPIKey:       "zenmux-dedicated-secret",
			},
			CreatedAt: timeNow(), UpdatedAt: timeNow(),
		},
	}
	store := config.NewMockStore(cfg)
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)

	body, _ := json.Marshal(map[string]any{
		"enabled":       true,
		"template_type": "general",
	})
	req := httptest.NewRequest("PUT", "/api/providers/test-p/usage", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	w := httptest.NewRecorder()
	srv.handleProviderUsage(w, req)
	if w.Code != 200 {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	loaded, _ := store.Load()
	q := loaded.GetProviderByID("test-p").QuotaQuery
	if q.ZenMuxAPIKey != "zenmux-dedicated-secret" || q.ScriptAPIKey != "" {
		t.Errorf("separated keys = %q/%q", q.ScriptAPIKey, q.ZenMuxAPIKey)
	}
}

func TestIsMaterialQuotaChange(t *testing.T) {
	old := &providerquota.ProviderQuotaConfig{
		TemplateType: "general",
		Script:       "script1",
		ScriptAPIKey: "key1",
	}
	tests := []struct {
		name   string
		new    *providerquota.ProviderQuotaConfig
		expect bool
	}{
		{"same config", &providerquota.ProviderQuotaConfig{TemplateType: "general", Script: "script1", ScriptAPIKey: "key1"}, false},
		{"template change", &providerquota.ProviderQuotaConfig{TemplateType: "custom", Script: "script1", ScriptAPIKey: "key1"}, true},
		{"script change", &providerquota.ProviderQuotaConfig{TemplateType: "general", Script: "script2", ScriptAPIKey: "key1"}, true},
		{"key change", &providerquota.ProviderQuotaConfig{TemplateType: "general", Script: "script1", ScriptAPIKey: "key2"}, true},
		{"interval only", &providerquota.ProviderQuotaConfig{TemplateType: "general", Script: "script1", ScriptAPIKey: "key1", AutoQueryIntervalMinutes: 10}, false},
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
