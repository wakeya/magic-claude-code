package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"magic-claude-code/internal/config"
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
