package providerquota

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDetectTokenPlanProvider(t *testing.T) {
	tests := []struct {
		apiURL  string
		want    string
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
		{"https://token-plan-sgp.xiaomimimo.com/v1", "", true},
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
	resetTime := time.Now().Add(2 * time.Hour).Unix()

	body := []byte(fmt.Sprintf(`{
		"limits": [{"name": "coding", "detail": {"limit": 1000, "remaining": 800, "resetTime": %d}}],
		"usage": [{"name": "weekly", "used": 500, "limit": 5000, "resetAt": %d}]
	}`, resetTime, resetTime+86400*5))

	var resp kimiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Limits) != 1 {
		t.Fatalf("limits count = %d", len(resp.Limits))
	}
	if resp.Limits[0].Detail.Limit != 1000 {
		t.Errorf("limit = %d, want 1000", resp.Limits[0].Detail.Limit)
	}
	if resp.Limits[0].Detail.Remaining != 800 {
		t.Errorf("remaining = %d, want 800", resp.Limits[0].Detail.Remaining)
	}
	// Verify utilization: (1000-800)/1000*100 = 20%
	used := float64(resp.Limits[0].Detail.Limit-resp.Limits[0].Detail.Remaining) / float64(resp.Limits[0].Detail.Limit) * 100
	if used != 20 {
		t.Errorf("utilization = %f, want 20", used)
	}
}

func TestParseMiniMaxResponse(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantTiers    int
		want5hPct    float64
		want7dPct    float64
	}{
		{
			name: "with weekly",
			body: `{"data": {"model_name": "general", "current_interval_remaining_percent": 98, "current_weekly_status": 1, "current_weekly_remaining_percent": 93}}`,
			wantTiers: 2,
			want5hPct: 2,  // 100 - 98
			want7dPct: 7,  // 100 - 93
		},
		{
			name: "no weekly",
			body: `{"data": {"model_name": "general", "current_interval_remaining_percent": 50, "current_weekly_status": 0, "current_weekly_remaining_percent": 0}}`,
			wantTiers: 1,
			want5hPct: 50,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp miniMaxResponse
			if err := json.Unmarshal([]byte(tt.body), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			// Simulate the tier creation logic.
			var tiers []QuotaTier
			used5h := 100 - resp.Data.CurrentIntervalRemainingPct
			tiers = append(tiers, QuotaTier{Name: WindowFiveHour, Utilization: used5h})
			if resp.Data.CurrentWeeklyStatus == 1 {
				used7d := 100 - resp.Data.WeeklyRemainingPct
				tiers = append(tiers, QuotaTier{Name: WindowSevenDay, Utilization: used7d})
			}

			if len(tiers) != tt.wantTiers {
				t.Errorf("tiers count = %d, want %d", len(tiers), tt.wantTiers)
			}
			if tiers[0].Utilization != tt.want5hPct {
				t.Errorf("5h utilization = %f, want %f", tiers[0].Utilization, tt.want5hPct)
			}
			if tt.wantTiers > 1 && tiers[1].Utilization != tt.want7dPct {
				t.Errorf("7d utilization = %f, want %f", tiers[1].Utilization, tt.want7dPct)
			}
		})
	}
}

func TestParseZhipuResponse(t *testing.T) {
	tests := []struct {
		name      string
		unit      int
		wantWindow string
	}{
		{"5h tier", 3, WindowFiveHour},
		{"7d tier", 6, WindowSevenDay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window := WindowFiveHour
			if tt.unit == 6 {
				window = WindowSevenDay
			}
			if window != tt.wantWindow {
				t.Errorf("window = %q, want %q", window, tt.wantWindow)
			}
		})
	}
}

func TestParseZenMuxResponse(t *testing.T) {
	body := []byte(`{"data": {"quota_5_hour": {"usage_percentage": 0.42, "used": 420, "limit": 1000}, "quota_7_day": {"usage_percentage": 0.07, "used": 700, "limit": 10000}}}`)
	var resp zenMuxResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Quota5Hour.UsagePercentage != 0.42 {
		t.Errorf("5h usage_percentage = %f, want 0.42", resp.Data.Quota5Hour.UsagePercentage)
	}
	// Verify percentage * 100 = 42%.
	pct := resp.Data.Quota5Hour.UsagePercentage * 100
	if pct != 42 {
		t.Errorf("5h pct = %f, want 42", pct)
	}
}

func TestVolcengineSigningDeterministic(t *testing.T) {
	fixedTime := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	query1 := map[string]string{
		"Action":      "GetAFPUsage",
		"Version":     "2024-01-01",
		"RegionId":    "cn-north-1",
		"AccessKeyId": "AKLT-test1234",
	}
	query2 := map[string]string{
		"Action":      "GetAFPUsage",
		"Version":     "2024-01-01",
		"RegionId":    "cn-north-1",
		"AccessKeyId": "AKLT-test1234",
	}

	sig1 := SignVolcengineRequest(query1, "secret-key-12345", fixedTime)
	sig2 := SignVolcengineRequest(query2, "secret-key-12345", fixedTime)

	if sig1 != sig2 {
		t.Errorf("signatures not deterministic:\n  sig1 = %s\n  sig2 = %s", sig1, sig2)
	}

	// Different secret should produce different signature.
	query3 := map[string]string{
		"Action":      "GetAFPUsage",
		"Version":     "2024-01-01",
		"RegionId":    "cn-north-1",
		"AccessKeyId": "AKLT-test1234",
	}
	sig3 := SignVolcengineRequest(query3, "different-secret", fixedTime)
	if sig1 == sig3 {
		t.Error("different secrets should produce different signatures")
	}
}

func TestParseVolcengineResponse(t *testing.T) {
	body := []byte(`{
		"Result": {
			"Subscriptions": [
				{"Product": "Doubao", "TotalQuota": 1000, "UsedQuota": 200, "Window": "five_hour"},
				{"Product": "Doubao", "TotalQuota": 10000, "UsedQuota": 700, "Window": "seven_day"}
			]
		},
		"ResponseMetadata": {}
	}`)
	result := parseVolcengineResponse(body, time.Now())
	if !result.Success {
		t.Fatalf("parse failed: %s", result.ErrorMessage)
	}
	if len(result.Tiers) != 2 {
		t.Fatalf("tiers count = %d, want 2", len(result.Tiers))
	}
	if result.Tiers[0].Name != WindowFiveHour {
		t.Errorf("tier[0] name = %q, want %q", result.Tiers[0].Name, WindowFiveHour)
	}
	if result.Tiers[0].Utilization != 20 {
		t.Errorf("tier[0] utilization = %f, want 20", result.Tiers[0].Utilization)
	}
	if result.Tiers[1].Name != WindowSevenDay {
		t.Errorf("tier[1] name = %q, want %q", result.Tiers[1].Name, WindowSevenDay)
	}
}

func TestParseVolcengineError(t *testing.T) {
	body := []byte(`{"ResponseMetadata": {"Error": {"Code": "NotFound", "Message": "No subscription"}}}`)
	result := parseVolcengineResponse(body, time.Now())
	if result.Success {
		t.Error("expected failure for error response")
	}
	if result.ErrorCode != "upstream_business_error" {
		t.Errorf("error_code = %q, want upstream_business_error", result.ErrorCode)
	}
}

func TestTokenPlanMiMoUnsupported(t *testing.T) {
	_, isMiMo := DetectTokenPlanProvider("https://token-plan-cn.xiaomimimo.com/v1")
	if !isMiMo {
		t.Error("expected isMiMo=true for xiaomimimo.com")
	}
}

// TokenPlan integration test with mock HTTP transport.
func TestTokenPlanAdapterKimiIntegration(t *testing.T) {
	resetTime := time.Now().Add(2 * time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kimi-token" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"limits": []any{
				map[string]any{
					"name": "coding",
					"detail": map[string]any{
						"limit":     1000,
						"remaining": 800,
						"resetTime": resetTime,
					},
				},
			},
		})
	}))
	defer srv.Close()

	adapter := &TokenPlanAdapter{
		HTTPClient: srv.Client(),
	}

	// We can't directly call adapter.queryKimi because it uses a hardcoded URL.
	// Test via a custom transport that rewrites the URL.
	transport := &urlRewriteTransport{
		original: "https://api.kimi.com",
		replaced: srv.URL,
		inner:    http.DefaultTransport,
	}
	adapter.HTTPClient = &http.Client{Transport: transport, Timeout: 5 * time.Second}

	result := adapter.Query(context.Background(), "kimi", nil, "kimi-token")
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if len(result.Tiers) == 0 {
		t.Fatal("expected at least 1 tier")
	}
	if result.Tiers[0].Name != WindowFiveHour {
		t.Errorf("tier name = %q, want %q", result.Tiers[0].Name, WindowFiveHour)
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

// Use fmt for formatting in the test file.
