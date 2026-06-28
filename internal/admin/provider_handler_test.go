package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/providerquota"
)

func TestProviderAPIRoundTripsMultimodalConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	body := bytes.NewBufferString(`{
		"name":"Mimo",
		"api_url":"https://token-plan-cn.xiaomimimo.com/anthropic",
		"api_token":"token",
		"model_mappings":{"claude-opus-4-6":"mimo-v2.5-pro"},
		"supports_thinking":true,
		"multimodal_switch":true,
		"multimodal_model":"mimo-vl-pro"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers", body)
	rec := httptest.NewRecorder()

	server.handleProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Provider config.Provider `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !created.Provider.MultimodalSwitch || created.Provider.MultimodalModel != "mimo-vl-pro" {
		t.Fatalf("created multimodal config = %#v", created.Provider)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rec = httptest.NewRecorder()

	server.handleProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Providers []struct {
			MultimodalSwitch bool   `json:"multimodal_switch"`
			MultimodalModel  string `json:"multimodal_model"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Providers) != 1 {
		t.Fatalf("listed providers = %d", len(listed.Providers))
	}
	if !listed.Providers[0].MultimodalSwitch || listed.Providers[0].MultimodalModel != "mimo-vl-pro" {
		t.Fatalf("listed multimodal config = %#v", listed.Providers[0])
	}

	req = httptest.NewRequest(http.MethodGet, "/api/providers/"+created.Provider.ID, nil)
	rec = httptest.NewRecorder()

	server.handleProvider(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		MultimodalSwitch bool   `json:"multimodal_switch"`
		MultimodalModel  string `json:"multimodal_model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if !detail.MultimodalSwitch || detail.MultimodalModel != "mimo-vl-pro" {
		t.Fatalf("detail multimodal config = %#v", detail)
	}
}

func TestProviderAPIRoundTripsAPIFormatAndOpenAIExtraParams(t *testing.T) {
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	body := bytes.NewBufferString(`{
		"name":"Agnes",
		"api_url":"https://apihub.agnes-ai.com/v1",
		"api_token":"token",
		"api_format":"openai_chat",
		"claude_code_compat_hint":false,
		"openai_extra_params":{
			"allowed_openai_params":["thinking","context_management"],
			"litellm_settings":{"drop_params":true}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers", body)
	rec := httptest.NewRecorder()

	server.handleProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Provider config.Provider `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Provider.APIFormat != config.APIFormatOpenAIChat {
		t.Fatalf("created APIFormat = %q, want %q", created.Provider.APIFormat, config.APIFormatOpenAIChat)
	}
	if created.Provider.ClaudeCodeCompatHint == nil || *created.Provider.ClaudeCodeCompatHint {
		t.Fatalf("created ClaudeCodeCompatHint = %#v, want explicit false", created.Provider.ClaudeCodeCompatHint)
	}
	settings, ok := created.Provider.OpenAIExtraParams["litellm_settings"].(map[string]any)
	if !ok || settings["drop_params"] != true {
		t.Fatalf("created OpenAIExtraParams = %#v", created.Provider.OpenAIExtraParams)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	rec = httptest.NewRecorder()

	server.handleProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Providers []struct {
			APIFormat         config.APIFormat `json:"api_format"`
			OpenAIExtraParams map[string]any   `json:"openai_extra_params"`
			ClaudeCodeCompat  bool             `json:"claude_code_compat_hint"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed.Providers) != 1 {
		t.Fatalf("listed providers = %d", len(listed.Providers))
	}
	if listed.Providers[0].APIFormat != config.APIFormatOpenAIChat {
		t.Fatalf("listed APIFormat = %q, want %q", listed.Providers[0].APIFormat, config.APIFormatOpenAIChat)
	}
	if listed.Providers[0].OpenAIExtraParams["allowed_openai_params"] == nil {
		t.Fatalf("listed OpenAIExtraParams = %#v", listed.Providers[0].OpenAIExtraParams)
	}
	if listed.Providers[0].OpenAIExtraParams["claude_code_compat_hint"] != nil {
		t.Fatalf("Claude Code compat hint leaked into OpenAIExtraParams: %#v", listed.Providers[0].OpenAIExtraParams)
	}
	if listed.Providers[0].ClaudeCodeCompat {
		t.Fatalf("listed ClaudeCodeCompat = true, want false")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/providers/"+created.Provider.ID, nil)
	rec = httptest.NewRecorder()

	server.handleProvider(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		APIFormat         config.APIFormat `json:"api_format"`
		OpenAIExtraParams map[string]any   `json:"openai_extra_params"`
		ClaudeCodeCompat  bool             `json:"claude_code_compat_hint"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detail.APIFormat != config.APIFormatOpenAIChat {
		t.Fatalf("detail APIFormat = %q, want %q", detail.APIFormat, config.APIFormatOpenAIChat)
	}
	if detail.OpenAIExtraParams["litellm_settings"] == nil {
		t.Fatalf("detail OpenAIExtraParams = %#v", detail.OpenAIExtraParams)
	}
	if detail.ClaudeCodeCompat {
		t.Fatalf("detail ClaudeCodeCompat = true, want false")
	}
}

func TestCreateProviderRejectsMultimodalSwitchWithoutModel(t *testing.T) {
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	body := bytes.NewBufferString(`{
		"name":"Mimo",
		"api_url":"https://token-plan-cn.xiaomimimo.com/anthropic",
		"api_token":"token",
		"multimodal_switch":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers", body)
	rec := httptest.NewRecorder()

	server.handleProviders(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProviderRejectsUnsupportedAPIFormat(t *testing.T) {
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	body := bytes.NewBufferString(`{
		"name":"Gemini",
		"api_url":"https://gemini.example.com/v1",
		"api_token":"token",
		"api_format":"gemini_native"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers", body)
	rec := httptest.NewRecorder()

	server.handleProviders(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreateProviderRejectsNonObjectOpenAIExtraParams(t *testing.T) {
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	body := bytes.NewBufferString(`{
		"name":"Agnes",
		"api_url":"https://apihub.agnes-ai.com/v1",
		"api_token":"token",
		"api_format":"openai_chat",
		"openai_extra_params":["not-an-object"]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers", body)
	rec := httptest.NewRecorder()

	server.handleProviders(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateProviderPreservesMultimodalConfigWhenOmitted(t *testing.T) {
	provider := config.Provider{
		ID:        "provider-a",
		Name:      "Mimo",
		APIURL:    "https://token-plan-cn.xiaomimimo.com/anthropic",
		APIToken:  "token",
		APIFormat: config.APIFormatOpenAIChat,
		OpenAIExtraParams: map[string]any{
			"litellm_settings": map[string]any{"drop_params": true},
		},
		ModelMappings:    map[string]string{"claude-opus-4-6": "mimo-v2.5-pro"},
		MultimodalSwitch: true,
		MultimodalModel:  "mimo-vl-pro",
		Enabled:          true,
	}
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{provider}
	cfg.ActiveProviderID = provider.ID
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	body := bytes.NewBufferString(`{"name":"Mimo Updated"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/providers/provider-a", body)
	rec := httptest.NewRecorder()

	server.handleProvider(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := loaded.GetProviderByID(provider.ID)
	if got == nil {
		t.Fatal("provider missing after update")
	}
	if !got.MultimodalSwitch || got.MultimodalModel != "mimo-vl-pro" {
		t.Fatalf("multimodal config = %#v", got)
	}
}

func TestDuplicateProviderPreservesMultimodalConfig(t *testing.T) {
	disabled := false
	provider := config.Provider{
		ID:        "provider-a",
		Name:      "Mimo",
		APIURL:    "https://token-plan-cn.xiaomimimo.com/anthropic",
		APIToken:  "token",
		APIFormat: config.APIFormatOpenAIChat,
		OpenAIExtraParams: map[string]any{
			"litellm_settings": map[string]any{"drop_params": true},
		},
		ClaudeCodeCompatHint: &disabled,
		ModelMappings:        map[string]string{"claude-opus-4-6": "mimo-v2.5-pro"},
		MultimodalSwitch:     true,
		MultimodalModel:      "mimo-vl-pro",
		Enabled:              true,
	}
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{provider}
	cfg.ActiveProviderID = provider.ID
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/providers/provider-a/duplicate", nil)
	rec := httptest.NewRecorder()

	server.handleProviderRoutes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var duplicated struct {
		Provider config.Provider `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &duplicated); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if !duplicated.Provider.MultimodalSwitch || duplicated.Provider.MultimodalModel != "mimo-vl-pro" {
		t.Fatalf("duplicated multimodal config = %#v", duplicated.Provider)
	}
	if duplicated.Provider.APIFormat != config.APIFormatOpenAIChat {
		t.Fatalf("duplicated APIFormat = %q", duplicated.Provider.APIFormat)
	}
	if duplicated.Provider.OpenAIExtraParams["litellm_settings"] == nil {
		t.Fatalf("duplicated OpenAIExtraParams = %#v", duplicated.Provider.OpenAIExtraParams)
	}
	if duplicated.Provider.ClaudeCodeCompatHint == nil || *duplicated.Provider.ClaudeCodeCompatHint {
		t.Fatalf("duplicated ClaudeCodeCompatHint = %#v, want explicit false", duplicated.Provider.ClaudeCodeCompatHint)
	}
}

func TestDuplicateProviderMigratesAndCopiesSeparatedQuotaCredentials(t *testing.T) {
	provider := config.Provider{
		ID: "provider-a", Name: "Legacy ZenMux", APIURL: "https://api.zenmux.example/v1", APIToken: "card-token",
		APIFormat: config.APIFormatAnthropic, Enabled: true,
		QuotaQuery: &providerquota.ProviderQuotaConfig{
			Enabled: true, TemplateType: providerquota.TemplateTokenPlan,
			BaseURL: "https://quota.zenmux.example/usage", LegacyAPIKey: "legacy-zenmux-key",
			ScriptAPIKey: "independent-script-key",
		},
	}
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{provider}
	cfg.ActiveProviderID = provider.ID
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/providers/provider-a/duplicate", nil)
	rec := httptest.NewRecorder()
	server.handleProviderRoutes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(loaded.Providers))
	}
	q := loaded.Providers[1].QuotaQuery
	if q == nil {
		t.Fatal("duplicated quota config is nil")
	}
	if q.ZenMuxBaseURL != "https://quota.zenmux.example/usage" || q.ZenMuxAPIKey != "legacy-zenmux-key" {
		t.Fatalf("duplicated ZenMux credentials = %+v", q)
	}
	if q.ScriptAPIKey != "independent-script-key" || q.LegacyAPIKey != "" || q.BaseURL != "" {
		t.Fatalf("duplicated quota config not normalized: %+v", q)
	}
}

func TestProviderUpdateInvalidatesQuotaSnapshotOnlyWhenCardCredentialsChange(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantDelete bool
	}{
		{name: "name only keeps snapshot", body: `{"name":"Renamed"}`},
		{name: "API URL change deletes snapshot", body: `{"api_url":"https://new.example/v1"}`, wantDelete: true},
		{name: "API token change deletes snapshot", body: `{"api_token":"new-card-token"}`, wantDelete: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := config.NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
			if err != nil {
				t.Fatalf("NewSQLiteStore() error = %v", err)
			}
			defer store.Close()

			provider := config.Provider{
				ID: "provider-a", Name: "Provider", APIURL: "https://gateway.example/v1", APIToken: "card-token",
				APIFormat: config.APIFormatAnthropic, Enabled: true,
				QuotaQuery: &providerquota.ProviderQuotaConfig{Enabled: true, TemplateType: providerquota.TemplateGeneral},
				CreatedAt:  time.Now(), UpdatedAt: time.Now(),
			}
			cfg := config.DefaultConfig()
			cfg.Providers = []config.Provider{provider}
			cfg.ActiveProviderID = provider.ID
			if err := store.Save(cfg); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			snapshots := providerquota.NewSnapshotStore(store.DB())
			if err := snapshots.SaveUpsert(provider.ID, &providerquota.ProviderQuotaResult{
				ProviderID: provider.ID, TemplateType: providerquota.TemplateGeneral, Success: true, QueriedAt: time.Now(),
				Balances: []providerquota.BalanceItem{{Unit: "USD"}},
			}); err != nil {
				t.Fatalf("SaveUpsert() error = %v", err)
			}

			server := NewServer(&AdminConfig{Password: "secret"}, store, nil)
			server.SetQuotaManager(providerquota.NewManager(snapshots, nil, 1))
			req := httptest.NewRequest(http.MethodPut, "/api/providers/provider-a", bytes.NewBufferString(tt.body))
			rec := httptest.NewRecorder()
			server.handleProvider(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}

			got, err := snapshots.Get(provider.ID)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if tt.wantDelete && got != nil {
				t.Fatal("snapshot was not deleted")
			}
			if !tt.wantDelete && got == nil {
				t.Fatal("snapshot was deleted for a non-credential change")
			}
		})
	}
}

func TestImportOverwriteInvalidatesSnapshotWhenFallbackCredentialsChange(t *testing.T) {
	dir := t.TempDir()
	store, err := config.NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	original := config.Provider{
		ID: "provider-a", Name: "Provider", APIURL: "https://gateway.example/v1", APIToken: "old-token",
		APIFormat: config.APIFormatAnthropic, Enabled: true,
		QuotaQuery: &providerquota.ProviderQuotaConfig{Enabled: true, TemplateType: providerquota.TemplateGeneral},
		CreatedAt:  time.Now(), UpdatedAt: time.Now(),
	}
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{original}
	cfg.ActiveProviderID = original.ID
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	snapshots := providerquota.NewSnapshotStore(store.DB())
	if err := snapshots.SaveUpsert(original.ID, &providerquota.ProviderQuotaResult{
		ProviderID: original.ID, TemplateType: providerquota.TemplateGeneral, Success: true, QueriedAt: time.Now(),
		Balances: []providerquota.BalanceItem{{Unit: "USD"}},
	}); err != nil {
		t.Fatalf("SaveUpsert() error = %v", err)
	}

	replacement := original
	replacement.APIToken = "new-token"
	body, _ := json.Marshal(map[string]any{
		"version": 1, "strategy": "overwrite", "providers": []config.Provider{replacement},
	})
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)
	server.SetQuotaManager(providerquota.NewManager(snapshots, nil, 1))
	req := httptest.NewRequest(http.MethodPost, "/api/providers/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	got, err := snapshots.Get(original.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != nil {
		t.Fatal("overwrite left a snapshot bound to the old card token")
	}
}

func TestExportProvidersReturnsSelectedWithRealTokens(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "p1", Name: "GLM", APIURL: "https://glm.example.com/api", APIToken: "sk-glm-secret", APIFormat: config.APIFormatAnthropic, Enabled: true},
		{ID: "p2", Name: "Kimi", APIURL: "https://kimi.example.com/api", APIToken: "sk-kimi-secret", APIFormat: config.APIFormatAnthropic, Enabled: true},
		{ID: "p3", Name: "GLM4", APIURL: "https://glm4.example.com/api", APIToken: "sk-glm4-secret", APIFormat: config.APIFormatAnthropic, Enabled: true},
	}
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	body := bytes.NewBufferString(`{"ids":["p1","p3"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/export", body)
	rec := httptest.NewRecorder()
	server.handleExportProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d body = %s", rec.Code, rec.Body.String())
	}
	var exported struct {
		Version    int               `json:"version"`
		ExportedAt string            `json:"exported_at"`
		Providers  []config.Provider `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if exported.Version != 1 {
		t.Errorf("version = %d, want 1", exported.Version)
	}
	if exported.ExportedAt == "" {
		t.Error("exported_at is empty")
	}
	if len(exported.Providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(exported.Providers))
	}
	// Real tokens, not masked
	gotIDs := map[string]string{}
	for _, p := range exported.Providers {
		gotIDs[p.ID] = p.APIToken
	}
	if gotIDs["p1"] != "sk-glm-secret" {
		t.Errorf("p1 token = %q, want sk-glm-secret", gotIDs["p1"])
	}
	if gotIDs["p3"] != "sk-glm4-secret" {
		t.Errorf("p3 token = %q, want sk-glm4-secret", gotIDs["p3"])
	}
}

func TestExportProvidersSkipsUnknownIDs(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "p1", Name: "GLM", APIURL: "https://glm.example.com/api", APIToken: "sk-glm-secret", APIFormat: config.APIFormatAnthropic, Enabled: true},
	}
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	// p1 exists, ghost does not
	body := bytes.NewBufferString(`{"ids":["p1","ghost"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/export", body)
	rec := httptest.NewRecorder()
	server.handleExportProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d body = %s", rec.Code, rec.Body.String())
	}
	var exported struct {
		Providers []config.Provider `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(exported.Providers) != 1 || exported.Providers[0].ID != "p1" {
		t.Fatalf("providers = %#v, want only p1", exported.Providers)
	}
}

func TestExportProvidersEmptyIDsReturnsEmptyArray(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "p1", Name: "GLM", APIURL: "https://glm.example.com/api", APIToken: "sk-glm-secret", APIFormat: config.APIFormatAnthropic, Enabled: true},
	}
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	body := bytes.NewBufferString(`{"ids":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/export", body)
	rec := httptest.NewRecorder()
	server.handleExportProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d body = %s", rec.Code, rec.Body.String())
	}
	var exported struct {
		Providers []config.Provider `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(exported.Providers) != 0 {
		t.Fatalf("providers count = %d, want 0", len(exported.Providers))
	}
}

// --- Import API tests ---

// importRequest is the import endpoint request body.
type importRequest struct {
	Version   int               `json:"version"`
	Providers []config.Provider `json:"providers"`
	Strategy  string            `json:"strategy"`
}

func importResp(t *testing.T, rec *httptest.ResponseRecorder) map[string]int {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("import status = %d body = %s", rec.Code, rec.Body.String())
	}
	var raw struct {
		Success     bool     `json:"success"`
		Imported    int      `json:"imported"`
		Skipped     int      `json:"skipped"`
		Overwritten int      `json:"overwritten"`
		Duplicated  int      `json:"duplicated"`
		Errors      []string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	return map[string]int{
		"imported":    raw.Imported,
		"skipped":     raw.Skipped,
		"overwritten": raw.Overwritten,
		"duplicated":  raw.Duplicated,
		"errors":      len(raw.Errors),
	}
}

func TestImportProvidersSkipStrategy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "existing", Name: "Old", APIURL: "https://old.example.com/api", APIToken: "sk-old", APIFormat: config.APIFormatAnthropic, Enabled: true},
	}
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	// "existing" conflicts; "new" does not
	reqBody := importRequest{
		Version: 1,
		Providers: []config.Provider{
			{ID: "existing", Name: "New Name", APIURL: "https://new.example.com/api", APIToken: "sk-new", APIFormat: config.APIFormatAnthropic, Enabled: true},
			{ID: "fresh", Name: "Fresh", APIURL: "https://fresh.example.com/api", APIToken: "sk-fresh", APIFormat: config.APIFormatAnthropic, Enabled: true},
		},
		Strategy: "skip",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)

	counts := importResp(t, rec)
	if counts["imported"] != 1 || counts["skipped"] != 1 {
		t.Fatalf("counts = %#v, want imported=1 skipped=1", counts)
	}

	// Existing provider unchanged
	loaded, _ := store.Load()
	got := loaded.GetProviderByID("existing")
	if got.Name != "Old" || got.APIToken != "sk-old" {
		t.Fatalf("existing provider was modified: %#v", got)
	}
	// Fresh provider added
	if loaded.GetProviderByID("fresh") == nil {
		t.Fatal("fresh provider not imported")
	}
}

func TestImportProvidersOverwriteStrategy(t *testing.T) {
	now := time.Now()
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "existing", Name: "Old", APIURL: "https://old.example.com/api", APIToken: "sk-old", APIFormat: config.APIFormatAnthropic, Enabled: true, CreatedAt: now.Add(-time.Hour)},
	}
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	reqBody := importRequest{
		Version: 1,
		Providers: []config.Provider{
			{ID: "existing", Name: "New Name", APIURL: "https://new.example.com/api", APIToken: "sk-new", APIFormat: config.APIFormatAnthropic, Enabled: true},
		},
		Strategy: "overwrite",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)

	counts := importResp(t, rec)
	if counts["overwritten"] != 1 {
		t.Fatalf("counts = %#v, want overwritten=1", counts)
	}

	loaded, _ := store.Load()
	got := loaded.GetProviderByID("existing")
	if got.Name != "New Name" || got.APIToken != "sk-new" {
		t.Fatalf("existing provider not overwritten: %#v", got)
	}
	// created_at preserved, updated_at refreshed
	if !got.CreatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("created_at changed: %v", got.CreatedAt)
	}
	if !got.UpdatedAt.After(now.Add(-time.Hour)) {
		t.Fatalf("updated_at not refreshed: %v", got.UpdatedAt)
	}
}

func TestImportProvidersDuplicateStrategy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "existing", Name: "Old", APIURL: "https://old.example.com/api", APIToken: "sk-old", APIFormat: config.APIFormatAnthropic, Enabled: true},
	}
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	reqBody := importRequest{
		Version: 1,
		Providers: []config.Provider{
			{ID: "existing", Name: "Dup", APIURL: "https://dup.example.com/api", APIToken: "sk-dup", APIFormat: config.APIFormatAnthropic, Enabled: true},
		},
		Strategy: "duplicate",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)

	counts := importResp(t, rec)
	if counts["duplicated"] != 1 {
		t.Fatalf("counts = %#v, want duplicated=1", counts)
	}

	loaded, _ := store.Load()
	// Original unchanged
	if got := loaded.GetProviderByID("existing"); got.Name != "Old" {
		t.Fatalf("original modified: %#v", got)
	}
	// New entry appended with different ID
	if len(loaded.Providers) != 2 {
		t.Fatalf("provider count = %d, want 2", len(loaded.Providers))
	}
	appended := loaded.Providers[1]
	if appended.ID == "existing" || appended.Name != "Dup" {
		t.Fatalf("appended provider wrong: %#v", appended)
	}
}

func TestImportProvidersRejectsWrongVersion(t *testing.T) {
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	reqBody := importRequest{
		Version:   2,
		Providers: []config.Provider{},
		Strategy:  "skip",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestImportProvidersSkipsInvalidProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	reqBody := importRequest{
		Version: 1,
		Providers: []config.Provider{
			{ID: "bad", Name: "", APIURL: "not-a-url", APIFormat: config.APIFormatAnthropic, Enabled: true}, // invalid
			{ID: "good", Name: "Good", APIURL: "https://good.example.com/api", APIToken: "sk-good", APIFormat: config.APIFormatAnthropic, Enabled: true},
		},
		Strategy: "skip",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)

	counts := importResp(t, rec)
	if counts["imported"] != 1 || counts["errors"] != 1 {
		t.Fatalf("counts = %#v, want imported=1 errors=1", counts)
	}
	loaded, _ := store.Load()
	if loaded.GetProviderByID("good") == nil {
		t.Fatal("good provider not imported")
	}
	if loaded.GetProviderByID("bad") != nil {
		t.Fatal("bad provider should not be imported")
	}
}

func TestImportProvidersDuplicateStrategyKeepsAllSameID(t *testing.T) {
	// spec: duplicate 策略下，文件内多个相同 ID 的供应商不应被静默丢弃。
	// 第一条 "same" 是新 ID → imported（保留原 ID）；第二条 "same" 与第一条
	// 冲突 → duplicate 生成新 ID。两条都保留，不丢弃。
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	reqBody := importRequest{
		Version: 1,
		Providers: []config.Provider{
			{ID: "same", Name: "First", APIURL: "https://first.example.com/api", APIToken: "sk-1", APIFormat: config.APIFormatAnthropic, Enabled: true},
			{ID: "same", Name: "Second", APIURL: "https://second.example.com/api", APIToken: "sk-2", APIFormat: config.APIFormatAnthropic, Enabled: true},
		},
		Strategy: "duplicate",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)

	counts := importResp(t, rec)
	// 第一条 imported（保留原 ID "same"），第二条 duplicated（冲突 → 新 ID）
	if counts["imported"] != 1 || counts["duplicated"] != 1 {
		t.Fatalf("counts = %#v, want imported=1 duplicated=1", counts)
	}
	loaded, _ := store.Load()
	if len(loaded.Providers) != 2 {
		t.Fatalf("provider count = %d, want 2 (no entry discarded)", len(loaded.Providers))
	}
	// 一条保留原 ID "same"，另一条是新 ID
	ids := map[string]bool{loaded.Providers[0].ID: true, loaded.Providers[1].ID: true}
	if !ids["same"] {
		t.Fatal("first entry did not keep original ID 'same'")
	}
}

func TestImportProvidersDuplicateKeepsNonConflictingOriginalID(t *testing.T) {
	// spec: duplicate 是"冲突处理策略"。非冲突项应保留原 ID 正常导入
	//（imported），只有冲突项才生成新 ID（duplicated）。
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "existing", Name: "Old", APIURL: "https://old.example.com/api", APIToken: "sk-old", APIFormat: config.APIFormatAnthropic, Enabled: true},
	}
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	reqBody := importRequest{
		Version: 1,
		Providers: []config.Provider{
			{ID: "fresh", Name: "Fresh", APIURL: "https://fresh.example.com/api", APIToken: "sk-fresh", APIFormat: config.APIFormatAnthropic, Enabled: true},
			{ID: "existing", Name: "Dup", APIURL: "https://dup.example.com/api", APIToken: "sk-dup", APIFormat: config.APIFormatAnthropic, Enabled: true},
		},
		Strategy: "duplicate",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/import", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)

	counts := importResp(t, rec)
	if counts["imported"] != 1 || counts["duplicated"] != 1 {
		t.Fatalf("counts = %#v, want imported=1 duplicated=1", counts)
	}
	loaded, _ := store.Load()
	// fresh 保留原 ID
	if got := loaded.GetProviderByID("fresh"); got == nil || got.APIToken != "sk-fresh" {
		t.Fatalf("fresh not imported with original ID: %#v", got)
	}
	// existing 原始不变
	if got := loaded.GetProviderByID("existing"); got.Name != "Old" {
		t.Fatalf("existing was modified: %#v", got)
	}
}

func TestImportProvidersRejectsNonPostMethod(t *testing.T) {
	// 导入/导出端点应显式拒绝非 POST 方法（与其他 mutating API 一致）
	cfg := config.DefaultConfig()
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/providers/import", nil)
	rec := httptest.NewRecorder()
	server.handleImportProviders(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("import GET status = %d, want 405", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/providers/export", nil)
	rec2 := httptest.NewRecorder()
	server.handleExportProviders(rec2, req2)
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Errorf("export GET status = %d, want 405", rec2.Code)
	}
}
