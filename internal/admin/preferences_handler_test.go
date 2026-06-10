package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"magic-claude-code/internal/config"
)

func TestPreferencesRequiresAuth(t *testing.T) {
	server := NewServer(&AdminConfig{Password: "secret"}, config.NewMockStore(config.DefaultConfig()), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/preferences", nil)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handlePreferences)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestGetPreferencesReturnsThemeMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AdminThemeMode = config.ThemeModeDark
	server := NewServer(&AdminConfig{Password: "secret"}, config.NewMockStore(cfg), nil)
	req := authenticatedRequest(t, server, http.MethodGet, "/api/preferences", nil)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handlePreferences)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		ThemeMode string `json:"theme_mode"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.ThemeMode != config.ThemeModeDark {
		t.Fatalf("theme_mode = %q, want %q", got.ThemeMode, config.ThemeModeDark)
	}
}

func TestPutPreferencesPersistsThemeMode(t *testing.T) {
	store := config.NewMockStore(config.DefaultConfig())
	server := NewServer(&AdminConfig{Password: "secret"}, store, nil)
	body := bytes.NewBufferString(`{"theme_mode":"dark"}`)
	req := authenticatedRequest(t, server, http.MethodPut, "/api/preferences", body)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handlePreferences)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.AdminThemeMode != config.ThemeModeDark {
		t.Fatalf("AdminThemeMode = %q, want %q", loaded.AdminThemeMode, config.ThemeModeDark)
	}
}

func TestPutPreferencesRejectsInvalidThemeMode(t *testing.T) {
	server := NewServer(&AdminConfig{Password: "secret"}, config.NewMockStore(config.DefaultConfig()), nil)
	body := bytes.NewBufferString(`{"theme_mode":"system"}`)
	req := authenticatedRequest(t, server, http.MethodPut, "/api/preferences", body)
	rec := httptest.NewRecorder()

	server.authMiddlewareFunc(server.handlePreferences)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func authenticatedRequest(t *testing.T, server *Server, method string, target string, body *bytes.Buffer) *http.Request {
	t.Helper()
	if body == nil {
		body = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, target, body)
	req.AddCookie(&http.Cookie{Name: "session", Value: server.auth.GenerateToken()})
	return req
}
