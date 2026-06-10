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

func TestUpdateProviderPreservesMultimodalConfigWhenOmitted(t *testing.T) {
	provider := config.Provider{
		ID:               "provider-a",
		Name:             "Mimo",
		APIURL:           "https://token-plan-cn.xiaomimimo.com/anthropic",
		APIToken:         "token",
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
	provider := config.Provider{
		ID:               "provider-a",
		Name:             "Mimo",
		APIURL:           "https://token-plan-cn.xiaomimimo.com/anthropic",
		APIToken:         "token",
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
}
