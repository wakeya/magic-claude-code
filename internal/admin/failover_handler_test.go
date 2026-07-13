package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/failover"
)

// failoverAdminHarness 搭建一个接入故障切换管理器的 admin Server + 真实 SQLite 配置/事件存储。
func failoverAdminHarness(t *testing.T) (*Server, *config.SQLiteStore, *failover.Manager) {
	t.Helper()
	dir := t.TempDir()
	cfgStore, err := config.NewSQLiteStore(filepath.Join(dir, "proxy.db"), "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { cfgStore.Close() })
	// 种入一个 provider 供 token/test/delete 测试使用。
	_, _ = cfgStore.Update(func(cfg *config.Config) error {
		cfg.Providers = []config.Provider{
			{ID: "p1", Name: "Alpha", APIURL: "https://p1.example", APIFormat: config.APIFormatAnthropic, Enabled: true},
		}
		return nil
	})

	fstore := failover.NewStore(cfgStore.DB())
	if err := fstore.Migrate(); err != nil {
		t.Fatalf("migrate failover: %v", err)
	}
	mgr := failover.NewManager(fstore, cfgStore)
	srv := NewServer(&AdminConfig{Password: "test"}, cfgStore, nil)
	srv.SetFailoverManager(mgr)
	return srv, cfgStore, mgr
}

func authedRequest(srv *Server, method, target string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: srv.GetAuth().GenerateToken()})
	return req
}

func TestFailoverSettingsRequireAuth(t *testing.T) {
	srv, _, _ := failoverAdminHarness(t)
	// 路由在注册时被 authMiddlewareFunc 包裹；直接调用包裹后的 handler 验证未认证返回 401。
	handler := srv.authMiddlewareFunc(srv.handleFailoverSettings)
	req := httptest.NewRequest("GET", "/api/providers/failover", nil) // 无 cookie
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (auth required)", rec.Code)
	}
}

func TestFailoverSettingsMethodsAndBody(t *testing.T) {
	srv, _, _ := failoverAdminHarness(t)

	t.Run("method not allowed", func(t *testing.T) {
		req := authedRequest(srv, "DELETE", "/api/providers/failover", "")
		rec := httptest.NewRecorder()
		srv.handleFailoverSettings(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		req := authedRequest(srv, "PUT", "/api/providers/failover", "{not json")
		rec := httptest.NewRecorder()
		srv.handleFailoverSettings(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
	t.Run("missing enabled", func(t *testing.T) {
		req := authedRequest(srv, "PUT", "/api/providers/failover", `{}`)
		rec := httptest.NewRecorder()
		srv.handleFailoverSettings(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestFailoverSettingsRoundTrip(t *testing.T) {
	srv, cfgStore, _ := failoverAdminHarness(t)

	// 默认关闭。
	req := authedRequest(srv, "GET", "/api/providers/failover", "")
	rec := httptest.NewRecorder()
	srv.handleFailoverSettings(rec, req)
	var got map[string]bool
	json.NewDecoder(rec.Body).Decode(&got)
	if got["enabled"] {
		t.Fatalf("default enabled must be false, got %v", got)
	}

	// PUT true。
	putReq := authedRequest(srv, "PUT", "/api/providers/failover", `{"enabled":true}`)
	putRec := httptest.NewRecorder()
	srv.handleFailoverSettings(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", putRec.Code, putRec.Body.String())
	}

	// 重新加载校验持久化。
	loaded, _ := cfgStore.Load()
	if !loaded.AutoFailoverEnabled {
		t.Fatal("AutoFailoverEnabled must persist true after PUT")
	}
}

func TestFailoverEventsLimitAndOrder(t *testing.T) {
	srv, _, mgr := failoverAdminHarness(t)
	// 插入 5 条事件，时间递增。
	for i := 0; i < 5; i++ {
		mgr.RecordExhausted("p1", "claude-opus-4-8", "glm-5.2", failover.Classification{Reason: "x"}, nil)
	}
	// 默认 limit。
	rec := httptest.NewRecorder()
	srv.handleFailoverEvents(rec, authedRequest(srv, "GET", "/api/failover/events", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Events []failover.FailoverEvent `json:"events"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Events) != 5 {
		t.Fatalf("default limit returned %d, want 5", len(resp.Events))
	}

	// limit 钳制：>100 → 100。
	rec2 := httptest.NewRecorder()
	srv.handleFailoverEvents(rec2, authedRequest(srv, "GET", "/api/failover/events?limit=500", ""))
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d", rec2.Code)
	}
	// invalid limit → 400.
	rec3 := httptest.NewRecorder()
	srv.handleFailoverEvents(rec3, authedRequest(srv, "GET", "/api/failover/events?limit=abc", ""))
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d, want 400", rec3.Code)
	}
	rec4 := httptest.NewRecorder()
	srv.handleFailoverEvents(rec4, authedRequest(srv, "GET", "/api/failover/events?limit=0", ""))
	if rec4.Code != http.StatusBadRequest {
		t.Fatalf("limit=0 status = %d, want 400", rec4.Code)
	}
}

func TestFailoverEventsDoNotExposeSecrets(t *testing.T) {
	srv, _, mgr := failoverAdminHarness(t)
	mgr.RecordExhausted("p1", "claude-opus-4-8", "glm-5.2", failover.Classification{Reason: "five_hour_quota_exhausted"}, nil)

	rec := httptest.NewRecorder()
	srv.handleFailoverEvents(rec, authedRequest(srv, "GET", "/api/failover/events", ""))
	body := rec.Body.String()
	// 事件结构本身不含 token/响应体/query 字段。
	for _, secret := range []string{"sk-", "Bearer ", "api_token", "Authorization"} {
		if strings.Contains(body, secret) {
			t.Fatalf("events response leaked %q: %s", secret, body)
		}
	}
}

func TestFailoverEventsBackfillProviderNamesFromCurrentConfig(t *testing.T) {
	srv, _, mgr := failoverAdminHarness(t)
	// RecordExhausted 只记录 provider ID；管理接口应从当前配置回填供应商名称，避免前端只看到 provider-xxx ID。
	mgr.RecordExhausted("p1", "claude-opus-4-8", "glm-5.2", failover.Classification{Reason: "five_hour_quota_exhausted"}, nil)

	rec := httptest.NewRecorder()
	srv.handleFailoverEvents(rec, authedRequest(srv, "GET", "/api/failover/events", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Events []failover.FailoverEvent `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("events len = %d, want 1", len(resp.Events))
	}
	if resp.Events[0].FromProviderID != "p1" {
		t.Fatalf("from_provider_id = %q, want p1", resp.Events[0].FromProviderID)
	}
	if resp.Events[0].FromProviderName != "Alpha" {
		t.Fatalf("from_provider_name = %q, want Alpha", resp.Events[0].FromProviderName)
	}
}

func TestProviderTokenChangeClearsCredentialFailure(t *testing.T) {
	srv, _, mgr := failoverAdminHarness(t)
	mgr.QuarantineFailed("p1", failover.Classification{Eligible: true, Kind: failover.StateKindCredential, Reason: "credential_invalid", UpstreamCode: 401})

	// 更新 Token（非空且变更）→ 清除 401 状态。
	body := `{"api_token":"new-secret-token"}`
	req := authedRequest(srv, "PUT", "/api/providers/p1", body)
	rec := httptest.NewRecorder()
	srv.handleProviderRoutes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	if st, ok := mgr.State("p1"); ok {
		t.Fatalf("credential state must be cleared after token change, got %+v", st)
	}
}

func TestProviderEditWithoutTokenChangeKeepsCredentialFailure(t *testing.T) {
	srv, _, mgr := failoverAdminHarness(t)
	mgr.QuarantineFailed("p1", failover.Classification{Eligible: true, Kind: failover.StateKindCredential, Reason: "credential_invalid", UpstreamCode: 401})

	// 只改名称，不改 Token → 状态保留。
	req := authedRequest(srv, "PUT", "/api/providers/p1", `{"name":"Renamed"}`)
	rec := httptest.NewRecorder()
	srv.handleProviderRoutes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	if st, ok := mgr.State("p1"); !ok || st.Kind != failover.StateKindCredential {
		t.Fatal("credential state must remain when only name/URL/model edited")
	}
}

func TestSuccessfulProviderTestClearsCredentialFailure(t *testing.T) {
	dir := t.TempDir()
	cfgStore, _ := config.NewSQLiteStore(filepath.Join(dir, "proxy.db"), "")
	t.Cleanup(func() { cfgStore.Close() })
	// 用公网域名作为 APIURL 以通过 SSRF 检查；真实请求由注入的 stub transport 接管。
	_, _ = cfgStore.Update(func(cfg *config.Config) error {
		cfg.Providers = []config.Provider{{ID: "p1", Name: "P1", APIURL: "https://example.com/v1", APIFormat: config.APIFormatAnthropic, Enabled: true}}
		return nil
	})
	fstore := failover.NewStore(cfgStore.DB())
	fstore.Migrate()
	mgr := failover.NewManager(fstore, cfgStore)
	srv := NewServer(&AdminConfig{Password: "test"}, cfgStore, nil)
	srv.SetFailoverManager(mgr)
	// 注入 stub 客户端：返回 200，触发非 401 的凭据恢复钩子。
	srv.setProviderTestHTTPClient(&http.Client{Transport: stubTransport{status: http.StatusOK}})

	mgr.QuarantineFailed("p1", failover.Classification{Eligible: true, Kind: failover.StateKindCredential, Reason: "credential_invalid", UpstreamCode: 401})

	req := authedRequest(srv, "POST", "/api/providers/p1/test", "")
	rec := httptest.NewRecorder()
	srv.handleProviderRoutes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("test status = %d body=%s", rec.Code, rec.Body.String())
	}
	if st, ok := mgr.State("p1"); ok {
		t.Fatalf("credential state must be cleared after successful test, got %+v", st)
	}
}

// stubTransport 返回固定状态码的响应，供 handleTestProviderByID 测试注入。
type stubTransport struct{ status int }

func (t stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Body: http.NoBody, Header: make(http.Header)}, nil
}

func TestProviderDeleteLeavesNoDanglingFailoverEventIDs(t *testing.T) {
	srv, cfgStore, mgr := failoverAdminHarness(t)
	// 插入一条引用 p1 的事件。
	mgr.RecordExhausted("p1", "claude-opus-4-8", "glm-5.2", failover.Classification{Reason: "x"}, nil)

	// 删除 p1。
	req := authedRequest(srv, "DELETE", "/api/providers/p1", "")
	rec := httptest.NewRecorder()
	srv.handleProviderRoutes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}

	// 事件列表中不得出现已删除的 p1 ID。
	events := mgr.Events(100, knownProviderIDsFromStore(t, cfgStore))
	for _, e := range events {
		if e.FromProviderID == "p1" || e.ToProviderID == "p1" {
			t.Fatalf("dangling provider ID p1 must be blanked, got event %+v", e)
		}
	}
}

func knownProviderIDsFromStore(t *testing.T, store *config.SQLiteStore) map[string]bool {
	t.Helper()
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := map[string]bool{}
	for i := range cfg.Providers {
		m[cfg.Providers[i].ID] = true
	}
	return m
}
