package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"claude_code_proxy_dns/internal/usage"
	_ "modernc.org/sqlite"
)

func TestPasswordHashing(t *testing.T) {
	auth := NewAuth("test-password")

	// 验证正确密码
	if !auth.VerifyPassword("test-password") {
		t.Error("expected password to be verified")
	}

	// 验证错误密码
	if auth.VerifyPassword("wrong-password") {
		t.Error("expected wrong password to be rejected")
	}
}

func TestSessionToken(t *testing.T) {
	auth := NewAuth("test-password")

	// 生成 token
	token := auth.GenerateToken()
	if token == "" {
		t.Error("expected non-empty token")
	}

	// 验证 token
	if !auth.ValidateToken(token) {
		t.Error("expected token to be valid")
	}

	// 验证无效 token
	if auth.ValidateToken("invalid-token") {
		t.Error("expected invalid token to be rejected")
	}
}

func TestLoginAttemptLimit(t *testing.T) {
	auth := NewAuthWithConfig("test-password", 3, 1*time.Minute)

	// 3 次失败后应该被锁定
	for i := 0; i < 3; i++ {
		auth.RecordFailedAttempt()
	}

	if !auth.IsLocked() {
		t.Error("expected account to be locked after 3 failed attempts")
	}

	// 正确密码也应该被拒绝
	if auth.VerifyPassword("test-password") {
		t.Error("expected password verification to fail when locked")
	}
}

func TestUsageRoutesRequireSession(t *testing.T) {
	store := newAdminUsageStore(t)
	usageHandler := usage.NewHandler(store)
	server := NewServer(&AdminConfig{Password: "secret"}, nil, nil, usageHandler)
	mux := http.NewServeMux()
	server.usageHandler.Register(mux, server.authMiddlewareFunc)

	req := httptest.NewRequest(http.MethodGet, "/api/usage/summary", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestStatusIncludesUsageSummary(t *testing.T) {
	store := newAdminUsageStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	statusCode := 200
	if err := store.Record(usage.RequestRecord{
		ID:               "status-usage",
		StartedAt:        started,
		StatusCode:       &statusCode,
		ProviderName:     "Provider A",
		ProviderAPIURL:   "https://provider.example.com",
		SourceApp:        "claude_code",
		MappedModel:      "mapped-model",
		SourceEntrypoint: "cli",
	}, usage.TokenRecord{
		RequestID:        "status-usage",
		InputTokens:      4,
		OutputTokens:     6,
		UsageSource:      usage.UsageSourceProvider,
		UsageParseStatus: usage.ParseStatusOK,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	server := NewServer(&AdminConfig{Password: "secret"}, nil, fakeStatsProvider{total: 12}, usage.NewHandler(store))

	req := httptest.NewRequest(http.MethodGet, "/api/status?tz=UTC", nil)
	rec := httptest.NewRecorder()
	server.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got["service_requests_total"].(float64) != 12 {
		t.Fatalf("service_requests_total = %#v", got["service_requests_total"])
	}
	if got["provider_requests_total"].(float64) != 1 || got["today_token_consumption"].(float64) != 10 {
		t.Fatalf("usage summary fields = %#v", got)
	}
}

type fakeStatsProvider struct {
	total int64
}

func (f fakeStatsProvider) Stats() (int64, time.Time, time.Duration) {
	return f.total, time.Time{}, time.Second
}

func newAdminUsageStore(t *testing.T) *usage.Store {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := usage.NewStore(db)
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return store
}
