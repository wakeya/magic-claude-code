package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"magic-claude-code/internal/config"
)

// orderTestHarness 搭建一个带若干 provider 的 admin Server（真实 SQLite 配置存储）。
func orderTestHarness(t *testing.T, ids ...string) (*Server, *config.SQLiteStore) {
	t.Helper()
	dir := t.TempDir()
	store, err := config.NewSQLiteStore(filepath.Join(dir, "proxy.db"), "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if len(ids) > 0 {
		_, _ = store.Update(func(cfg *config.Config) error {
			cfg.Providers = make([]config.Provider, 0, len(ids))
			for _, id := range ids {
				cfg.Providers = append(cfg.Providers, config.Provider{
					ID: id, Name: id, APIURL: "https://" + id, APIFormat: config.APIFormatAnthropic, Enabled: true,
				})
			}
			return nil
		})
	}
	srv := NewServer(&AdminConfig{Password: "test"}, store, nil)
	return srv, store
}

func orderRequest(srv *Server, body string) *http.Request {
	req := httptest.NewRequest("PUT", "/api/providers/order", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	return req
}

func TestProviderOrderRequiresAuth(t *testing.T) {
	srv, _ := orderTestHarness(t, "a")
	// 未认证（直接走 authMiddlewareFunc，无 cookie）→ 401。
	rec := httptest.NewRecorder()
	srv.authMiddlewareFunc(srv.handleProviderOrder)(rec, httptest.NewRequest("PUT", "/api/providers/order", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	// 非 PUT → 405。
	rec2 := httptest.NewRecorder()
	srv.handleProviderOrder(rec2, authedRequest(srv, "GET", "/api/providers/order", ""))
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec2.Code)
	}
}

func TestProviderOrderRejectsInvalidSets(t *testing.T) {
	srv, store := orderTestHarness(t, "a", "b", "c")

	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"invalid json", `{not json`, http.StatusBadRequest},
		{"missing provider_ids", `{"foo":"bar"}`, http.StatusBadRequest},
		{"provider_ids not array", `{"provider_ids":"a-b-c"}`, http.StatusBadRequest},
		{"provider_ids number", `{"provider_ids":5}`, http.StatusBadRequest},
		{"duplicate id", `{"provider_ids":["a","a","b","c"]}`, http.StatusBadRequest},
		{"unknown id", `{"provider_ids":["a","b","c","x"]}`, http.StatusBadRequest},
		{"empty when non-empty", `{"provider_ids":[]}`, http.StatusConflict},
		{"missing existing id", `{"provider_ids":["a","b"]}`, http.StatusConflict},
		{"length mismatch subset", `{"provider_ids":["a","b","c","a"]}`, http.StatusBadRequest}, // 重复也算非法
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.handleProviderOrder(rec, orderRequest(srv, c.body))
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			// 校验失败不得改动配置顺序。
			loaded, _ := store.Load()
			got := []string{loaded.Providers[0].ID, loaded.Providers[1].ID, loaded.Providers[2].ID}
			if got[0] != "a" || got[1] != "b" || got[2] != "c" {
				t.Fatalf("config order changed on rejected request: %v", got)
			}
		})
	}
}

func TestProviderOrderEmptyWhenEmptyReturns200(t *testing.T) {
	srv, _ := orderTestHarness(t) // 无 provider
	rec := httptest.NewRecorder()
	srv.handleProviderOrder(rec, orderRequest(srv, `{"provider_ids":[]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Success   bool             `json:"success"`
		Providers []map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success || len(resp.Providers) != 0 {
		t.Fatalf("expected success+empty providers, got %+v", resp)
	}
}

func TestProviderOrderPersistsAndReturnsOrdered(t *testing.T) {
	srv, store := orderTestHarness(t, "a", "b", "c")
	rec := httptest.NewRecorder()
	srv.handleProviderOrder(rec, orderRequest(srv, `{"provider_ids":["c","a","b"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Providers []map[string]any `json:"providers"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Providers) != 3 || resp.Providers[0]["id"] != "c" || resp.Providers[1]["id"] != "a" || resp.Providers[2]["id"] != "b" {
		t.Fatalf("response order = %+v, want c,a,b", resp.Providers)
	}
	// 持久化：重新 Load。
	loaded, _ := store.Load()
	got := []string{loaded.Providers[0].ID, loaded.Providers[1].ID, loaded.Providers[2].ID}
	if got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("persisted order = %v, want c,a,b", got)
	}
	// 不得泄露 token：响应不含 api_token 原文（只有 api_token_mask）。
	body := rec.Body.String()
	if strings.Contains(body, "api_token\":") || strings.Contains(strings.ToLower(body), "\"token\":") {
		// api_token_mask 字段名含 api_token，这里只断言没有裸 token 值字段。
	}
	for _, p := range resp.Providers {
		if _, ok := p["api_token"]; ok {
			t.Fatalf("response must not expose raw api_token: %+v", p)
		}
	}
}

func TestProviderOrderDoesNotChangeActiveProvider(t *testing.T) {
	srv, store := orderTestHarness(t, "a", "b", "c")
	// 设 active = a。
	_, _ = store.Update(func(cfg *config.Config) error { cfg.ActiveProviderID = "a"; return nil })
	before, _ := store.Load()
	if before.ActiveProviderID != "a" {
		t.Fatalf("setup active = %q", before.ActiveProviderID)
	}
	rec := httptest.NewRecorder()
	srv.handleProviderOrder(rec, orderRequest(srv, `{"provider_ids":["b","c","a"]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	after, _ := store.Load()
	if after.ActiveProviderID != "a" {
		t.Fatalf("ActiveProviderID changed %q -> %q (order must not change active)", before.ActiveProviderID, after.ActiveProviderID)
	}
}
