package providerquota

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTokenPlanAdapterRejectsUnsafeAuthenticatedRedirects(t *testing.T) {
	tests := []struct {
		name      string
		newTarget func(http.Handler) *httptest.Server
	}{
		{name: "https to http", newTarget: httptest.NewServer},
		{name: "cross origin https", newTarget: httptest.NewTLSServer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			target := tt.newTarget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"data":{"quota_5_hour":{"usage_percentage":0.1,"used_value_usd":1,"max_value_usd":10},"quota_7_day":{"usage_percentage":0.2,"used_value_usd":2,"max_value_usd":10}}}`))
			}))
			defer target.Close()

			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, http.StatusFound)
			}))
			defer source.Close()

			client := source.Client()
			client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test servers only
			adapter := &TokenPlanAdapter{HTTPClient: client}
			result := adapter.Query(context.Background(), "zenmux", &ProviderQuotaConfig{}, source.URL, "card-secret")

			if gotAuth != "" {
				t.Fatalf("unsafe redirect target received Authorization: %q", gotAuth)
			}
			if result.Success {
				t.Fatal("unsafe redirect unexpectedly succeeded")
			}
		})
	}
}

func TestTokenPlanAdapterAllowsSameOriginHTTPSRedirect(t *testing.T) {
	var gotAuth string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota_5_hour":{"usage_percentage":0.1,"used_value_usd":1,"max_value_usd":10},"quota_7_day":{"usage_percentage":0.2,"used_value_usd":2,"max_value_usd":10}}}`))
	}))
	defer server.Close()

	adapter := &TokenPlanAdapter{HTTPClient: server.Client()}
	result := adapter.Query(context.Background(), "zenmux", &ProviderQuotaConfig{}, server.URL+"/start", "card-secret")
	if !result.Success || gotAuth != "Bearer card-secret" {
		t.Fatalf("result=%+v Authorization=%q", result, gotAuth)
	}
}

func TestDetectTokenPlanProvider(t *testing.T) {
	tests := []struct {
		apiURL   string
		want     string
		wantMiMo bool
	}{
		{"https://api.kimi.com/coding/v1", "kimi", false},
		{"https://open.bigmodel.cn/api/anthropic", "zhipu_cn", false},
		{"https://api.z.ai/v1/chat", "zhipu_en", false},
		{"https://api.minimaxi.com/v1/chat", "minimax_cn", false},
		{"https://api.minimax.io/v1/chat", "minimax_en", false},
		{"https://zenmux.example.com/v1", "zenmux", false},
		{"https://api.volces.com/api/coding/v1", "volcengine", false},
		{"https://token-plan-cn.xiaomimimo.com/v1", "", true},
		{"https://platform.xiaomimimo.com/api", "", true},
		{"https://api.deepseek.com/v1", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, isMiMo := DetectTokenPlanProvider(tt.apiURL)
		if got != tt.want {
			t.Errorf("DetectTokenPlanProvider(%q) provider = %q, want %q", tt.apiURL, got, tt.want)
		}
		if isMiMo != tt.wantMiMo {
			t.Errorf("DetectTokenPlanProvider(%q) isMiMo = %v, want %v", tt.apiURL, isMiMo, tt.wantMiMo)
		}
	}
}

func TestParseKimiResponse(t *testing.T) {
	// Reference: usage is an OBJECT, not array.
	resetTime := time.Now().Add(2 * time.Hour).Unix()
	body := []byte(fmt.Sprintf(`{
		"limits": [{"name": "coding", "detail": {"limit": 1000, "remaining": 800, "resetTime": %d}}],
		"usage": {"limit": 5000, "remaining": 4500, "resetTime": %d}
	}`, resetTime, resetTime+86400*5))

	var resp struct {
		Limits []struct {
			Name   string `json:"name"`
			Detail struct {
				Limit     json.Number `json:"limit"`
				Remaining json.Number `json:"remaining"`
				ResetTime json.Number `json:"resetTime"`
			} `json:"detail"`
		} `json:"limits"`
		Usage struct {
			Limit     json.Number `json:"limit"`
			Remaining json.Number `json:"remaining"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Verify usage is parsed as object.
	usageLimit, _ := resp.Usage.Limit.Float64()
	if usageLimit != 5000 {
		t.Errorf("usage.limit = %f, want 5000", usageLimit)
	}
	// Verify limits array.
	if len(resp.Limits) != 1 {
		t.Fatalf("limits count = %d", len(resp.Limits))
	}
}

func TestKimiIntegration(t *testing.T) {
	resetTime := time.Now().Add(2 * time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kimi-token" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{
			"limits": [{"name": "coding", "detail": {"limit": 1000, "remaining": 800, "resetTime": %d}}],
			"usage": {"limit": 5000, "remaining": 4500, "resetTime": %d}
		}`, resetTime, resetTime+86400*5)))
	}))
	defer srv.Close()

	transport := &urlRewriteTransport{original: "https://api.kimi.com", replaced: srv.URL, inner: http.DefaultTransport}
	adapter := &TokenPlanAdapter{HTTPClient: &http.Client{Transport: transport, Timeout: 5 * time.Second}}

	result := adapter.Query(context.Background(), "kimi", nil, "https://api.kimi.com", "kimi-token")
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if len(result.Tiers) < 2 {
		t.Fatalf("expected at least 2 tiers, got %d", len(result.Tiers))
	}
	if result.Tiers[0].Name != WindowFiveHour {
		t.Errorf("tier[0] name = %q, want %q", result.Tiers[0].Name, WindowFiveHour)
	}
	if result.Tiers[1].Name != WindowSevenDay {
		t.Errorf("tier[1] name = %q, want %q", result.Tiers[1].Name, WindowSevenDay)
	}
	// Verify utilization: (1000-800)/1000*100 = 20%
	if result.Tiers[0].Utilization != 20 {
		t.Errorf("5h utilization = %f, want 20", result.Tiers[0].Utilization)
	}
}

func TestMiniMaxResponse(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantTiers int
		want5hPct float64
		want7dPct float64
	}{
		{
			name:      "with weekly",
			body:      `{"base_resp": {"status_code": 0}, "model_remains": [{"model_name": "general", "current_interval_remaining_percent": 98, "end_time": 1719500000000, "current_weekly_status": 1, "current_weekly_remaining_percent": 93, "weekly_end_time": 1719600000000}]}`,
			wantTiers: 2,
			want5hPct: 2,  // 100 - 98
			want7dPct: 7,  // 100 - 93
		},
		{
			name:      "no weekly (status 3)",
			body:      `{"base_resp": {"status_code": 0}, "model_remains": [{"model_name": "general", "current_interval_remaining_percent": 50, "end_time": 1719500000000, "current_weekly_status": 3}]}`,
			wantTiers: 1,
			want5hPct: 50,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp struct {
				BaseResp struct {
					StatusCode int64 `json:"status_code"`
				} `json:"base_resp"`
				ModelRemains []struct {
					ModelName                   string  `json:"model_name"`
					CurrentIntervalRemainingPct float64 `json:"current_interval_remaining_percent"`
					CurrentWeeklyStatus         int     `json:"current_weekly_status"`
				} `json:"model_remains"`
			}
			json.Unmarshal([]byte(tt.body), &resp)

			// Simulate the tier creation logic.
			var tiers []QuotaTier
			for _, mr := range resp.ModelRemains {
				if mr.ModelName == "general" {
					used5h := 100 - mr.CurrentIntervalRemainingPct
					tiers = append(tiers, QuotaTier{Name: WindowFiveHour, Utilization: used5h})
					if mr.CurrentWeeklyStatus == 1 {
						used7d := 100 - mr.CurrentIntervalRemainingPct // simplified
						tiers = append(tiers, QuotaTier{Name: WindowSevenDay, Utilization: used7d})
					}
				}
			}
			if len(tiers) != tt.wantTiers {
				t.Errorf("tiers count = %d, want %d", len(tiers), tt.wantTiers)
			}
		})
	}
}

func TestZhipuAuthNoBearer(t *testing.T) {
	// Verify that Zhipu sends raw key without "Bearer " prefix.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			w.WriteHeader(401)
			return
		}
		if auth != "raw-key-123" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success": true, "data": {"level": "free", "limits": [{"type": "TOKENS_LIMIT", "percentage": 42.5, "unit": 3, "nextResetTime": 1719500000000}]}}`))
	}))
	defer srv.Close()

	transport := &urlRewriteTransport{original: "https://open.bigmodel.cn", replaced: srv.URL, inner: http.DefaultTransport}
	adapter := &TokenPlanAdapter{HTTPClient: &http.Client{Transport: transport, Timeout: 5 * time.Second}}

	result := adapter.Query(context.Background(), "zhipu_cn", nil, "https://open.bigmodel.cn", "raw-key-123")
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if len(result.Tiers) != 1 {
		t.Fatalf("expected 1 tier, got %d", len(result.Tiers))
	}
	if result.Tiers[0].Utilization != 42.5 {
		t.Errorf("utilization = %f, want 42.5", result.Tiers[0].Utilization)
	}
}

// TestZhipuMissingUnitClassification verifies that when unit is absent, the
// parser falls back to reset-time ordering instead of labeling every limit as
// five_hour. Two unit-less entries must produce one five_hour and one seven_day.
func TestZhipuMissingUnitClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Two TOKENS_LIMIT entries, neither has "unit".
		w.Write([]byte(`{"success": true, "data": {"limits": [
			{"type": "TOKENS_LIMIT", "percentage": 15},
			{"type": "TOKENS_LIMIT", "percentage": 40, "nextResetTime": 1719600000000}
		]}}`))
	}))
	defer srv.Close()

	transport := &urlRewriteTransport{original: "https://open.bigmodel.cn", replaced: srv.URL, inner: http.DefaultTransport}
	adapter := &TokenPlanAdapter{HTTPClient: &http.Client{Transport: transport, Timeout: 5 * time.Second}}

	result := adapter.Query(context.Background(), "zhipu_cn", nil, "https://open.bigmodel.cn", "raw-key-123")
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if len(result.Tiers) != 2 {
		t.Fatalf("expected 2 tiers, got %d", len(result.Tiers))
	}
	names := map[string]bool{}
	for _, tier := range result.Tiers {
		names[tier.Name] = true
	}
	if !names[WindowFiveHour] || !names[WindowSevenDay] {
		t.Errorf("expected one %s and one %s; got %v", WindowFiveHour, WindowSevenDay, names)
	}
}

// TestZhipuMissingUnitBothWithReset verifies the fallback when BOTH unit-less
// TOKENS_LIMIT entries carry a reset time: the earlier reset fills five_hour
// and the later fills seven_day, instead of dropping the 5-hour bucket.
func TestZhipuMissingUnitBothWithReset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Two unit-less entries, BOTH with reset times (earlier → 5h, later → 7d).
		w.Write([]byte(`{"success": true, "data": {"limits": [
			{"type": "TOKENS_LIMIT", "percentage": 30, "nextResetTime": 1719500000000},
			{"type": "TOKENS_LIMIT", "percentage": 60, "nextResetTime": 1720000000000}
		]}}`))
	}))
	defer srv.Close()

	transport := &urlRewriteTransport{original: "https://open.bigmodel.cn", replaced: srv.URL, inner: http.DefaultTransport}
	adapter := &TokenPlanAdapter{HTTPClient: &http.Client{Transport: transport, Timeout: 5 * time.Second}}

	result := adapter.Query(context.Background(), "zhipu_cn", nil, "https://open.bigmodel.cn", "raw-key-123")
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if len(result.Tiers) != 2 {
		t.Fatalf("expected 2 tiers, got %d", len(result.Tiers))
	}
	names := map[string]bool{}
	for _, tier := range result.Tiers {
		names[tier.Name] = true
	}
	if !names[WindowFiveHour] || !names[WindowSevenDay] {
		t.Errorf("expected one %s and one %s; got %v", WindowFiveHour, WindowSevenDay, names)
	}
}

func TestVolcengineSigningDeterministic(t *testing.T) {
	fixedTime := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	query1 := map[string]string{"Action": "GetAFPUsage", "Version": "2024-01-01", "Region": "cn-beijing"}
	query2 := map[string]string{"Action": "GetAFPUsage", "Version": "2024-01-01", "Region": "cn-beijing"}

	sig1 := SignVolcengineRequestV4(query1, "AKLT-test", "secret-key", "ark", "cn-beijing", fixedTime)
	sig2 := SignVolcengineRequestV4(query2, "AKLT-test", "secret-key", "ark", "cn-beijing", fixedTime)

	if sig1.authHeader != sig2.authHeader {
		t.Errorf("signatures not deterministic:\n  sig1 = %s\n  sig2 = %s", sig1.authHeader, sig2.authHeader)
	}
	if sig1.bodyHash != sig2.bodyHash {
		t.Errorf("body hashes not deterministic")
	}
}

// TestVolcengineCanonicalQueryFormat verifies the signed query string matches
// the Volcengine protocol: it must contain Region (not RegionId), Action,
// Version; and must NOT contain X-Date (X-Date is a signed header, not a query
// parameter).
func TestVolcengineCanonicalQueryFormat(t *testing.T) {
	fixedTime := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	query := map[string]string{"Action": "GetAFPUsage", "Version": "2024-01-01", "Region": "cn-beijing"}

	sig := SignVolcengineRequestV4(query, "AKLT-test", "secret-key", "ark", "cn-beijing", fixedTime)
	qs := sig.queryString

	if !strings.Contains(qs, "Region=cn-beijing") {
		t.Errorf("query string missing Region=cn-beijing: %s", qs)
	}
	if strings.Contains(qs, "RegionId") {
		t.Errorf("query string must not contain RegionId (use Region): %s", qs)
	}
	if strings.Contains(qs, "X-Date") {
		t.Errorf("query string must not contain X-Date (it is a header): %s", qs)
	}
	if !strings.Contains(qs, "Action=GetAFPUsage") {
		t.Errorf("query string missing Action: %s", qs)
	}
}

// TestVolcengineRegionFromBaseURL verifies the region is derived from the
// provider Base URL host (e.g. ark.cn-beijing.volces.com → cn-beijing) rather
// than being hardcoded.
func TestVolcengineRegionFromBaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    string
	}{
		{"https://ark.cn-beijing.volces.com/api/v3", "cn-beijing"},
		{"https://ark.cn-shanghai.volces.com/api/v3", "cn-shanghai"},
		{"https://ark.ap-southeast-1.volces.com/api/v3", "ap-southeast-1"},
		{"https://ark.volces.com/api/v3", "cn-beijing"}, // default when undetectable
		{"", "cn-beijing"},                               // default
	}
	for _, tt := range tests {
		got := volcRegionFromBaseURL(tt.baseURL)
		if got != tt.want {
			t.Errorf("volcRegionFromBaseURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
		}
	}
}

func TestVolcengineAFPResponse(t *testing.T) {
	body := []byte(`{
		"Result": {
			"AFPFiveHour": {"Quota": 1000, "Used": 200, "ResetTime": 1719500000000},
			"AFPWeekly": {"Quota": 10000, "Used": 700, "ResetTime": 1719600000000},
			"AFPMonthly": {"Quota": 100000, "Used": 5000, "ResetTime": 1720000000000},
			"PlanType": "Large"
		},
		"ResponseMetadata": {}
	}`)

	var resp struct {
		Result json.RawMessage `json:"Result"`
	}
	json.Unmarshal(body, &resp)
	result := parseVolcengineAFP(resp.Result, time.Now())
	if !result.Success {
		t.Fatalf("parse failed: %s", result.ErrorMessage)
	}
	if len(result.Tiers) != 3 {
		t.Fatalf("tiers count = %d, want 3", len(result.Tiers))
	}
	if result.Tiers[0].Name != WindowFiveHour {
		t.Errorf("tier[0] = %q, want %q", result.Tiers[0].Name, WindowFiveHour)
	}
	if result.Tiers[0].Utilization != 20 {
		t.Errorf("5h utilization = %f, want 20", result.Tiers[0].Utilization)
	}
}

func TestNormalizeVolcWindow(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"session", WindowFiveHour},
		{"5h", WindowFiveHour},
		{"five_hour", WindowFiveHour},
		{"weekly", WindowSevenDay},
		{"7d", WindowSevenDay},
		{"monthly", WindowMonthly},
		{"month", WindowMonthly},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := normalizeVolcWindow(tt.input)
		if got != tt.want {
			t.Errorf("normalizeVolcWindow(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMiMoUnsupported(t *testing.T) {
	_, isMiMo := DetectTokenPlanProvider("https://token-plan-cn.xiaomimimo.com/v1")
	if !isMiMo {
		t.Error("expected isMiMo=true")
	}
}

// urlRewriteTransport rewrites requests to a different host for testing.
type urlRewriteTransport struct {
	original string
	replaced string
	inner    http.RoundTripper
}

func (t *urlRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.replaced, "http://")
	return t.inner.RoundTrip(req)
}
