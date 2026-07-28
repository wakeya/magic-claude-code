package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/providerquota"
)

type adminQuotaConfigGetter struct {
	provider providerquota.ProviderConfig
}

type quotaSaveTrackingStore struct {
	*config.MockStore
	saveCalls int
}

func (s *quotaSaveTrackingStore) Save(cfg *config.Config) error {
	s.saveCalls++
	return s.MockStore.Save(cfg)
}

// Update 同样计数：handler 现在走原子 Update 路径，测试用 saveCalls 断言配置确实落库。
func (s *quotaSaveTrackingStore) Update(mutator func(*config.Config) error) (*config.Config, error) {
	s.saveCalls++
	return s.MockStore.Update(mutator)
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

func TestGenerateScriptSuccess(t *testing.T) {
	script := `({request:{url:"{{baseUrl}}/balance",method:"GET"},extractor:function(r){return {remaining:r.balance,unit:"USD"};}})`
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("LLM path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-llm-secret" {
			t.Fatalf("x-api-key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": script}},
		})
	}))
	defer llmServer.Close()

	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{{
		ID: "test-p", Name: "Test", APIURL: llmServer.URL, APIToken: "sk-llm-secret",
		APIFormat: config.APIFormatAnthropic, Enabled: true, CreatedAt: timeNow(), UpdatedAt: timeNow(),
	}}
	srv := NewServer(&AdminConfig{Password: "test"}, config.NewMockStore(cfg), nil)

	body := bytes.NewBufferString(`{"model":"claude-test","prompt":"query balance","response_sample":"{\"balance\":42}","request_info":"GET /balance"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/test-p/usage/generate-script", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	rec := httptest.NewRecorder()
	srv.authMiddlewareFunc(srv.handleProviderRoutes)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Script       string `json:"script"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ErrorCode != "" || resp.Script != script {
		t.Fatalf("response = %#v", resp)
	}
	if strings.Contains(rec.Body.String(), "sk-llm-secret") {
		t.Fatalf("response leaked APIToken: %s", rec.Body.String())
	}
}

func TestGenerateScriptUsesBodyLLMProviderID(t *testing.T) {
	script := `({request:{url:"{{baseUrl}}/usage",method:"GET"},extractor:function(r){return {remaining:r.remaining,unit:"credits"};}})`
	var urlProviderHits atomic.Int32
	urlProviderServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		urlProviderHits.Add(1)
		http.Error(w, "wrong provider", http.StatusInternalServerError)
	}))
	defer urlProviderServer.Close()

	var selectedProviderHits atomic.Int32
	selectedProviderServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		selectedProviderHits.Add(1)
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("LLM path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-selected-secret" {
			t.Fatalf("x-api-key = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": script}},
		})
	}))
	defer selectedProviderServer.Close()

	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{
			ID: "provider-a", Name: "URL Provider", APIURL: urlProviderServer.URL, APIToken: "sk-url-secret",
			APIFormat: config.APIFormatAnthropic, Enabled: true, CreatedAt: timeNow(), UpdatedAt: timeNow(),
		},
		{
			ID: "provider-b", Name: "Selected Provider", APIURL: selectedProviderServer.URL, APIToken: "sk-selected-secret",
			APIFormat: config.APIFormatAnthropic, Enabled: true, CreatedAt: timeNow(), UpdatedAt: timeNow(),
		},
	}
	srv := NewServer(&AdminConfig{Password: "test"}, config.NewMockStore(cfg), nil)

	body := bytes.NewBufferString(`{"llm_provider_id":"provider-b","model":"claude-test","prompt":"query usage","response_sample":"{\"remaining\":7}"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/provider-a/usage/generate-script", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	rec := httptest.NewRecorder()
	srv.authMiddlewareFunc(srv.handleProviderRoutes)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Script    string `json:"script"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ErrorCode != "" || resp.Script != script {
		t.Fatalf("response = %#v", resp)
	}
	if got := selectedProviderHits.Load(); got != 1 {
		t.Fatalf("selected provider hits = %d, want 1", got)
	}
	if got := urlProviderHits.Load(); got != 0 {
		t.Fatalf("URL provider hits = %d, want 0", got)
	}
}

func TestGenerateScriptProviderNotFound(t *testing.T) {
	srv := NewServer(&AdminConfig{Password: "test"}, config.NewMockStore(config.DefaultConfig()), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/missing/usage/generate-script", bytes.NewBufferString(`{"model":"m","prompt":"p","response_sample":"{}"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	rec := httptest.NewRecorder()

	srv.authMiddlewareFunc(srv.handleProviderRoutes)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestGenerateScriptUnauthorized(t *testing.T) {
	srv := NewServer(&AdminConfig{Password: "test"}, config.NewMockStore(config.DefaultConfig()), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/test-p/usage/generate-script", bytes.NewBufferString(`{"model":"m","prompt":"p","response_sample":"{}"}`))
	rec := httptest.NewRecorder()

	srv.authMiddlewareFunc(srv.handleProviderRoutes)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestGenerateScriptNonLLMProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider config.Provider
	}{
		{
			name: "missing token",
			provider: config.Provider{
				ID: "test-p", Name: "Test", APIURL: "https://api.example.com", APIFormat: config.APIFormatAnthropic, Enabled: true,
			},
		},
		{
			name: "unknown format",
			provider: config.Provider{
				ID: "test-p", Name: "Test", APIURL: "https://api.example.com", APIToken: "sk-test", APIFormat: config.APIFormat("unknown"), Enabled: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Providers = []config.Provider{tt.provider}
			srv := NewServer(&AdminConfig{Password: "test"}, config.NewMockStore(cfg), nil)
			req := httptest.NewRequest(http.MethodPost, "/api/providers/test-p/usage/generate-script", bytes.NewBufferString(`{"model":"m","prompt":"p","response_sample":"{}"}`))
			req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
			rec := httptest.NewRecorder()

			srv.authMiddlewareFunc(srv.handleProviderRoutes)(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				ErrorCode string `json:"error_code"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.ErrorCode != "invalid_config" {
				t.Fatalf("error_code = %q, want invalid_config", resp.ErrorCode)
			}
		})
	}
}

func TestGenerateScriptMissingFields(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{{
		ID: "test-p", Name: "Test", APIURL: "https://api.example.com", APIToken: "sk-test",
		APIFormat: config.APIFormatOpenAIChat, Enabled: true,
	}}
	srv := NewServer(&AdminConfig{Password: "test"}, config.NewMockStore(cfg), nil)
	tests := []struct {
		name string
		body string
	}{
		{name: "model", body: `{"prompt":"p","response_sample":"{}"}`},
		{name: "prompt", body: `{"model":"m","response_sample":"{}"}`},
		{name: "response_sample", body: `{"model":"m","prompt":"p"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/providers/test-p/usage/generate-script", bytes.NewBufferString(tt.body))
			req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
			rec := httptest.NewRecorder()

			srv.authMiddlewareFunc(srv.handleProviderRoutes)(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"error_code":"invalid_config"`) {
				t.Fatalf("body = %s, want invalid_config", rec.Body.String())
			}
		})
	}
}

func TestGenerateScriptLLMUpstreamError(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "token rejected", http.StatusUnauthorized)
	}))
	defer llmServer.Close()

	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{{
		ID: "test-p", Name: "Test", APIURL: llmServer.URL, APIToken: "sk-llm-secret",
		APIFormat: config.APIFormatOpenAIChat, Enabled: true,
	}}
	srv := NewServer(&AdminConfig{Password: "test"}, config.NewMockStore(cfg), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/test-p/usage/generate-script", bytes.NewBufferString(`{"model":"m","prompt":"p","response_sample":"{}"}`))
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	rec := httptest.NewRecorder()

	srv.authMiddlewareFunc(srv.handleProviderRoutes)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Script       string `json:"script"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Script != "" || resp.ErrorCode != "invalid_credentials" {
		t.Fatalf("response = %#v, want invalid_credentials", resp)
	}
	if strings.Contains(rec.Body.String(), "sk-llm-secret") {
		t.Fatalf("response leaked APIToken: %s", rec.Body.String())
	}
}

func TestProviderUsageGetReportsSnapshotLoadFailure(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{{
		ID: "test-p", Name: "Test", APIURL: "https://api.example.com", APIToken: "secret-token", Enabled: true,
		CreatedAt: timeNow(), UpdatedAt: timeNow(),
	}}
	configStore := config.NewMockStore(cfg)

	dir := t.TempDir()
	snapshotDB, err := config.NewSQLiteStore(filepath.Join(dir, "snapshots.db"), filepath.Join(dir, "unused.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	snapshots := providerquota.NewSnapshotStore(snapshotDB.DB())
	if err := snapshotDB.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	srv := NewServer(&AdminConfig{Password: "test"}, configStore, nil)
	srv.SetQuotaManager(providerquota.NewManager(snapshots, nil, 1))
	req := httptest.NewRequest(http.MethodGet, "/api/providers/test-p/usage", nil)
	w := httptest.NewRecorder()

	srv.handleProviderUsage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GET status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var response struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Decode() error = %v; body = %s", err, w.Body.String())
	}
	if response.Error != "failed to load quota snapshot" {
		t.Fatalf("error = %q, want snapshot load error", response.Error)
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

func TestProviderUsagePutDisabledDeletesExistingSnapshot(t *testing.T) {
	dir := t.TempDir()
	store, err := config.NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	provider := config.Provider{
		ID: "test-p", Name: "Test", APIURL: "https://api.example.com", APIToken: "secret-token", Enabled: true,
		QuotaQuery: &providerquota.ProviderQuotaConfig{Enabled: true, TemplateType: providerquota.TemplateGeneral},
		CreatedAt:  timeNow(), UpdatedAt: timeNow(),
	}
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{provider}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	remaining := 42.50
	snapshots := providerquota.NewSnapshotStore(store.DB())
	if err := snapshots.SaveUpsert(provider.ID, &providerquota.ProviderQuotaResult{
		ProviderID: provider.ID, TemplateType: providerquota.TemplateGeneral, Success: true, QueriedAt: time.Now(),
		Balances: []providerquota.BalanceItem{{Remaining: &remaining, Unit: "USD"}},
	}); err != nil {
		t.Fatalf("SaveUpsert() error = %v", err)
	}

	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)
	srv.SetQuotaManager(providerquota.NewManager(snapshots, nil, 1))
	req := httptest.NewRequest(http.MethodPut, "/api/providers/test-p/usage", bytes.NewBufferString(`{"enabled":false}`))
	w := httptest.NewRecorder()
	srv.handleProviderUsage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", w.Code, w.Body.String())
	}

	got, err := snapshots.Get(provider.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != nil {
		t.Fatal("disabling quota query left the existing snapshot in storage")
	}
}

func TestProviderUsagePutReportsSnapshotDeleteFailureAfterConfigSave(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{{
		ID: "test-p", Name: "Test", APIURL: "https://api.example.com", APIToken: "secret-token", Enabled: true,
		QuotaQuery: &providerquota.ProviderQuotaConfig{Enabled: true, TemplateType: providerquota.TemplateGeneral},
		CreatedAt:  timeNow(), UpdatedAt: timeNow(),
	}}
	configStore := &quotaSaveTrackingStore{MockStore: config.NewMockStore(cfg)}

	dir := t.TempDir()
	snapshotDB, err := config.NewSQLiteStore(filepath.Join(dir, "snapshots.db"), filepath.Join(dir, "unused.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	snapshots := providerquota.NewSnapshotStore(snapshotDB.DB())
	if err := snapshotDB.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	srv := NewServer(&AdminConfig{Password: "test"}, configStore, nil)
	srv.SetQuotaManager(providerquota.NewManager(snapshots, nil, 1))
	req := httptest.NewRequest(http.MethodPut, "/api/providers/test-p/usage", bytes.NewBufferString(`{"enabled":false}`))
	w := httptest.NewRecorder()
	srv.handleProviderUsage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("PUT status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "config saved but failed to clear quota snapshot") {
		t.Fatalf("PUT body = %q, want partial-success cleanup error", w.Body.String())
	}
	if configStore.saveCalls != 1 {
		t.Fatalf("config Save() calls = %d, want 1", configStore.saveCalls)
	}
	saved, err := configStore.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if saved.Providers[0].QuotaQuery.Enabled {
		t.Fatal("quota config was not saved before snapshot cleanup failed")
	}
}

func TestProviderUsagePutDisabledRetriesSnapshotDeleteAfterFailure(t *testing.T) {
	provider := config.Provider{
		ID: "test-p", Name: "Test", APIURL: "https://api.example.com", APIToken: "secret-token", Enabled: true,
		QuotaQuery: &providerquota.ProviderQuotaConfig{Enabled: true, TemplateType: providerquota.TemplateGeneral},
		CreatedAt:  timeNow(), UpdatedAt: timeNow(),
	}
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{provider}
	configStore := config.NewMockStore(cfg)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snapshots.db")
	snapshotDB, err := config.NewSQLiteStore(dbPath, filepath.Join(dir, "unused.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	if err := snapshotDB.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	snapshots := providerquota.NewSnapshotStore(snapshotDB.DB())
	remaining := 42.50
	if err := snapshots.SaveUpsert(provider.ID, &providerquota.ProviderQuotaResult{
		ProviderID: provider.ID, TemplateType: providerquota.TemplateGeneral, Success: true, QueriedAt: time.Now(),
		Balances: []providerquota.BalanceItem{{Remaining: &remaining, Unit: "USD"}},
	}); err != nil {
		t.Fatalf("SaveUpsert() error = %v", err)
	}
	if err := snapshotDB.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	srv := NewServer(&AdminConfig{Password: "test"}, configStore, nil)
	srv.SetQuotaManager(providerquota.NewManager(snapshots, nil, 1))
	firstReq := httptest.NewRequest(http.MethodPut, "/api/providers/test-p/usage", bytes.NewBufferString(`{"enabled":false}`))
	firstRec := httptest.NewRecorder()
	srv.handleProviderUsage(firstRec, firstReq)
	if firstRec.Code != http.StatusInternalServerError {
		t.Fatalf("first PUT status = %d, want %d; body = %s", firstRec.Code, http.StatusInternalServerError, firstRec.Body.String())
	}

	recoveredDB, err := config.NewSQLiteStore(dbPath, filepath.Join(dir, "unused.json"))
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore() error = %v", err)
	}
	defer recoveredDB.Close()
	recoveredSnapshots := providerquota.NewSnapshotStore(recoveredDB.DB())
	srv.SetQuotaManager(providerquota.NewManager(recoveredSnapshots, nil, 1))
	secondReq := httptest.NewRequest(http.MethodPut, "/api/providers/test-p/usage", bytes.NewBufferString(`{"enabled":false}`))
	secondRec := httptest.NewRecorder()
	srv.handleProviderUsage(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, want %d; body = %s", secondRec.Code, http.StatusOK, secondRec.Body.String())
	}
	snapshot, err := recoveredSnapshots.Get(provider.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if snapshot != nil {
		t.Fatal("repeated disabled PUT left the stale snapshot in storage")
	}

	thirdReq := httptest.NewRequest(http.MethodPut, "/api/providers/test-p/usage", bytes.NewBufferString(`{"enabled":false}`))
	thirdRec := httptest.NewRecorder()
	srv.handleProviderUsage(thirdRec, thirdReq)
	if thirdRec.Code != http.StatusOK {
		t.Fatalf("idempotent PUT status = %d, want %d; body = %s", thirdRec.Code, http.StatusOK, thirdRec.Body.String())
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
				ScriptAPIKey2:   "super-secret-script2-key",
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
	for _, secret := range []string{"super-secret-at", "super-secret-script-key", "super-secret-script2-key", "super-secret-zenmux-key", "super-secret-sk"} {
		if containsStr(body, secret) {
			t.Errorf("response contains secret %q", secret)
		}
	}

	// Must contain configured flags.
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	cfgDTO := resp["config"].(map[string]any)
	if cfgDTO["script_api_key_configured"] != true || cfgDTO["script_api_key_2_configured"] != true || cfgDTO["zenmux_api_key_configured"] != true {
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

func TestProviderUsageRejectsConflictingSecretPatches(t *testing.T) {
	secretFields := []struct {
		valueField string
		clearField string
	}{
		{valueField: "script_api_key", clearField: "clear_script_api_key"},
		{valueField: "zenmux_api_key", clearField: "clear_zenmux_api_key"},
		{valueField: "access_token", clearField: "clear_access_token"},
		{valueField: "secret_access_key", clearField: "clear_secret_access_key"},
	}
	endpoints := []struct {
		name   string
		method string
		path   string
		serve  func(*Server, http.ResponseWriter, *http.Request)
	}{
		{
			name:   "save",
			method: http.MethodPut,
			path:   "/api/providers/test-p/usage",
			serve: func(server *Server, w http.ResponseWriter, r *http.Request) {
				server.handleProviderUsage(w, r)
			},
		},
		{
			name:   "test",
			method: http.MethodPost,
			path:   "/api/providers/test-p/usage/test",
			serve: func(server *Server, w http.ResponseWriter, r *http.Request) {
				server.handleProviderUsageTest(w, r)
			},
		},
	}

	for _, endpoint := range endpoints {
		for _, fields := range secretFields {
			t.Run(endpoint.name+"/"+fields.valueField, func(t *testing.T) {
				cfg := config.DefaultConfig()
				cfg.Providers = []config.Provider{{
					ID:        "test-p",
					Name:      "Test",
					APIURL:    "https://api.example.com",
					APIToken:  "card-token",
					Enabled:   true,
					CreatedAt: timeNow(),
					UpdatedAt: timeNow(),
				}}
				store := config.NewMockStore(cfg)
				server := NewServer(&AdminConfig{Password: "test"}, store, nil)

				body, err := json.Marshal(map[string]any{
					fields.valueField: "replacement",
					fields.clearField: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(endpoint.method, endpoint.path, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()

				endpoint.serve(server, w, req)

				if w.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
				}
				if !containsStr(w.Body.String(), fields.valueField) {
					t.Fatalf("body = %q, want field %q", w.Body.String(), fields.valueField)
				}
				loaded, err := store.Load()
				if err != nil {
					t.Fatal(err)
				}
				if loaded.Providers[0].QuotaQuery != nil {
					t.Fatalf("conflicting request mutated quota config: %+v", loaded.Providers[0].QuotaQuery)
				}
			})
		}
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

func TestApplyQuotaUpdateScriptAPIKey2(t *testing.T) {
	str := func(v string) *string { return &v }

	t.Run("replace", func(t *testing.T) {
		existing := &providerquota.ProviderQuotaConfig{TemplateType: providerquota.TemplateCustom, ScriptAPIKey2: "old"}
		result := applyQuotaUpdate(existing, providerQuotaUpdateRequest{ScriptAPIKey2: str("new-sec")}, "")
		if result.ScriptAPIKey2 != "new-sec" {
			t.Fatalf("ScriptAPIKey2 = %q, want new-sec", result.ScriptAPIKey2)
		}
	})

	t.Run("preserve when field omitted", func(t *testing.T) {
		existing := &providerquota.ProviderQuotaConfig{TemplateType: providerquota.TemplateCustom, ScriptAPIKey2: "old"}
		result := applyQuotaUpdate(existing, providerQuotaUpdateRequest{TemplateType: str(providerquota.TemplateCustom)}, "")
		if result.ScriptAPIKey2 != "old" {
			t.Fatalf("ScriptAPIKey2 = %q, want old (preserved)", result.ScriptAPIKey2)
		}
	})

	t.Run("clear", func(t *testing.T) {
		existing := &providerquota.ProviderQuotaConfig{TemplateType: providerquota.TemplateCustom, ScriptAPIKey2: "old"}
		result := applyQuotaUpdate(existing, providerQuotaUpdateRequest{ClearScriptAPIKey2: true}, "")
		if result.ScriptAPIKey2 != "" {
			t.Fatalf("ScriptAPIKey2 = %q, want empty (cleared)", result.ScriptAPIKey2)
		}
	})

	t.Run("clear is independent from script_api_key", func(t *testing.T) {
		existing := &providerquota.ProviderQuotaConfig{TemplateType: providerquota.TemplateCustom, ScriptAPIKey: "k1", ScriptAPIKey2: "k2"}
		result := applyQuotaUpdate(existing, providerQuotaUpdateRequest{ClearScriptAPIKey2: true}, "")
		if result.ScriptAPIKey != "k1" || result.ScriptAPIKey2 != "" {
			t.Fatalf("ScriptAPIKey/2 = %q/%q, want k1/empty", result.ScriptAPIKey, result.ScriptAPIKey2)
		}
	})
}

func TestValidateProviderQuotaSecretPatchesScriptAPIKey2(t *testing.T) {
	str := func(v string) *string { return &v }
	if err := validateProviderQuotaSecretPatches(providerQuotaUpdateRequest{ScriptAPIKey2: str("x"), ClearScriptAPIKey2: true}); err == nil {
		t.Fatal("expected error when script_api_key_2 is both replaced and cleared")
	}
	if err := validateProviderQuotaSecretPatches(providerQuotaUpdateRequest{ScriptAPIKey2: str("x")}); err != nil {
		t.Fatalf("replace only: %v", err)
	}
	if err := validateProviderQuotaSecretPatches(providerQuotaUpdateRequest{ClearScriptAPIKey2: true}); err != nil {
		t.Fatalf("clear only: %v", err)
	}
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

func TestIsMaterialQuotaChangeWhenEnabledChanges(t *testing.T) {
	old := &providerquota.ProviderQuotaConfig{Enabled: true, TemplateType: "general"}
	newCfg := &providerquota.ProviderQuotaConfig{Enabled: false, TemplateType: "general"}

	if !isMaterialQuotaChange(old, newCfg) {
		t.Fatal("enabled true -> false should invalidate snapshots")
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
