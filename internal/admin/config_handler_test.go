package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"magic-claude-code/internal/config"
)

func TestGetConfigReturnsConnectionMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ConnectionMode = config.ConnectionModeGateway
	server := NewServer(&AdminConfig{Password: "secret"}, config.NewMockStore(cfg), nil)
	req := authenticatedRequest(t, server, http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handleConfig)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		BackendURL     string `json:"backend_url"`
		ConnectionMode string `json:"connection_mode"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.ConnectionMode != config.ConnectionModeGateway {
		t.Fatalf("connection_mode = %q, want %q", got.ConnectionMode, config.ConnectionModeGateway)
	}
}

// TestGetConfigRedactsLegacyUserinfoURL 验证历史存量数据（带 userinfo 的 URL）
// 通过 admin API 返回时被 redact，不会把凭证泄露到前端。
func TestGetConfigRedactsLegacyUserinfoURL(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.BackendURL = "https://user:secret-token@legacy-host.example/v1?sign=abc"
	store := config.NewMockStore(cfg)
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)

	req := authenticatedRequest(t, server, http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	server.authMiddlewareFunc(server.handleConfig)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "secret-token") {
		t.Errorf("userinfo credential leaked into config response: %s", body)
	}
	if strings.Contains(body, "sign=abc") {
		t.Errorf("query string leaked into config response: %s", body)
	}
}

func TestPutConfigPersistsConnectionMode(t *testing.T) {
	store := config.NewMockStore(config.DefaultConfig())
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)
	body := bytes.NewBufferString(`{"connection_mode":"tunnel"}`)
	req := authenticatedRequest(t, server, http.MethodPut, "/api/config", body)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handleConfig)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.ConnectionMode != config.ConnectionModeTunnel {
		t.Fatalf("ConnectionMode = %q, want %q", loaded.ConnectionMode, config.ConnectionModeTunnel)
	}
}

func TestGetStatusIncludesModeState(t *testing.T) {
	server := NewServer(&AdminConfig{
		Password:       "secret",
		ConfiguredMode: config.ConnectionModeGateway,
		EffectiveMode:  config.ConnectionModeTunnel,
		ModeRationale:  "fallback",
	}, config.NewMockStore(config.DefaultConfig()), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()

	server.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ConfiguredMode string `json:"configured_mode"`
		EffectiveMode  string `json:"effective_mode"`
		ModeRationale  string `json:"mode_rationale"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.ConfiguredMode != config.ConnectionModeGateway || got.EffectiveMode != config.ConnectionModeTunnel || got.ModeRationale != "fallback" {
		t.Fatalf("unexpected mode state: %#v", got)
	}
}

func TestGetCertificatesUsesConfiguredDataDir(t *testing.T) {
	dir := t.TempDir()
	server := NewServer(&AdminConfig{
		Password:   "secret",
		ConfigPath: filepath.Join(dir, "config.json"),
	}, config.NewMockStore(config.DefaultConfig()), nil)
	req := authenticatedRequest(t, server, http.MethodGet, "/api/certificates", nil)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handleCertificates)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		CACertPath     string `json:"ca_cert_path"`
		ServerCertPath string `json:"server_cert_path"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.CACertPath != filepath.Join(dir, "ca.crt") || got.ServerCertPath != filepath.Join(dir, "server.crt") {
		t.Fatalf("unexpected cert paths: %#v", got)
	}
}
