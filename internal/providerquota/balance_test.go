package providerquota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDetectBalanceProvider(t *testing.T) {
	tests := []struct {
		apiURL string
		want   string
	}{
		{"https://api.deepseek.com/anthropic", "deepseek"},
		{"https://api.stepfun.com/v1/chat/completions", "stepfun"},
		{"https://api.siliconflow.cn/v1/chat", "siliconflow_cn"},
		{"https://api.siliconflow.com/v1/chat", "siliconflow_en"},
		{"https://openrouter.ai/api/v1/chat", "openrouter"},
		{"https://api.novita.ai/v3/openai", "novita"},
		{"https://api.kimi.com/coding/v1", ""},
		{"https://custom.example.com/v1", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := DetectBalanceProvider(tt.apiURL)
		if got != tt.want {
			t.Errorf("DetectBalanceProvider(%q) = %q, want %q", tt.apiURL, got, tt.want)
		}
	}
}

func TestParseDeepSeekBalance(t *testing.T) {
	// Reference fixture: multi-currency with is_available.
	body := []byte(`{
		"balance_infos": [
			{"currency": "CNY", "total_balance": 100.50, "granted_balance": 80, "topped_up_balance": 50},
			{"currency": "USD", "total_balance": 12.34, "granted_balance": 10, "topped_up_balance": 5}
		],
		"is_available": true
	}`)
	result, err := parseDeepSeekBalance(body, time.Now())
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(result.Balances) != 2 {
		t.Fatalf("expected 2 balances, got %d", len(result.Balances))
	}
	if result.Balances[0].PlanName != "CNY" {
		t.Errorf("plan_name = %q, want CNY", result.Balances[0].PlanName)
	}
	if *result.Balances[0].Remaining != 100.50 {
		t.Errorf("remaining = %f, want 100.50", *result.Balances[0].Remaining)
	}
}

func TestParseDeepSeekUnavailable(t *testing.T) {
	body := []byte(`{
		"balance_infos": [{"currency": "CNY", "total_balance": 0}],
		"is_available": false
	}`)
	result, err := parseDeepSeekBalance(body, time.Now())
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if result.Balances[0].IsValid == nil || *result.Balances[0].IsValid {
		t.Error("expected is_valid=false")
	}
	if result.Balances[0].InvalidMessage != "Insufficient balance" {
		t.Errorf("invalid_message = %q", result.Balances[0].InvalidMessage)
	}
}

func TestParseStepFunBalance(t *testing.T) {
	body := []byte(`{"balance": 88.88}`)
	result, err := parseStepFunBalance(body, time.Now())
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if *result.Balances[0].Remaining != 88.88 {
		t.Errorf("remaining = %f, want 88.88", *result.Balances[0].Remaining)
	}
}

func TestParseSiliconFlowBalance(t *testing.T) {
	body := []byte(`{"data": {"totalBalance": 50.25}}`)
	result, err := parseSiliconFlowBalance(body, "CNY", time.Now())
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if *result.Balances[0].Total != 50.25 {
		t.Errorf("total = %f, want 50.25", *result.Balances[0].Total)
	}
}

func TestParseOpenRouterBalance(t *testing.T) {
	body := []byte(`{"data": {"total_credits": 100, "total_usage": 65.5}}`)
	result, err := parseOpenRouterBalance(body, time.Now())
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if *result.Balances[0].Remaining != 34.5 {
		t.Errorf("remaining = %f, want 34.5", *result.Balances[0].Remaining)
	}
}

func TestParseOpenRouterNoCredits(t *testing.T) {
	body := []byte(`{"data": {"total_credits": 10, "total_usage": 15}}`)
	result, err := parseOpenRouterBalance(body, time.Now())
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if result.Balances[0].IsValid == nil || *result.Balances[0].IsValid {
		t.Error("expected is_valid=false for zero credits")
	}
}

func TestParseNovitaBalance(t *testing.T) {
	// Novita returns TOP-LEVEL availableBalance in 0.0001 USD units.
	body := []byte(`{"availableBalance": 123456}`)
	result, err := parseNovitaBalance(body, time.Now())
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	expected := 123456.0 / 10000
	if *result.Balances[0].Remaining != expected {
		t.Errorf("remaining = %f, want %f", *result.Balances[0].Remaining, expected)
	}
}

func TestBalanceAdapterWithMockServer(t *testing.T) {
	servers := map[string]*httptest.Server{
		"deepseek": httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer dk-token" {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"balance_infos": []any{
					map[string]any{"currency": "CNY", "total_balance": 50.25},
				},
				"is_available": true,
			})
		})),
		"stepfun": httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"balance": 88.88})
		})),
		"siliconflow_cn": httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"totalBalance": 100}})
		})),
		"openrouter": httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"total_credits": 200, "total_usage": 120}})
		})),
		"novita": httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"availableBalance": 99999})
		})),
	}
	for _, s := range servers {
		defer s.Close()
	}

	tests := []struct {
		name     string
		provider string
		token    string
		wantOK   bool
		wantUnit string
		wantErr  string
	}{
		{"deepseek auth ok", "deepseek", "dk-token", true, "CNY", ""},
		{"deepseek auth fail", "deepseek", "wrong", false, "", "invalid_credentials"},
		{"stepfun", "stepfun", "tok", true, "CNY", ""},
		{"siliconflow", "siliconflow_cn", "tok", true, "CNY", ""},
		{"openrouter", "openrouter", "tok", true, "USD", ""},
		{"novita", "novita", "tok", true, "USD", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := servers[tt.provider]
			adapter := &BalanceAdapter{
				HTTPClient: &http.Client{Timeout: 5 * time.Second},
				EndpointOverride: func(p string) string {
					return srv.URL
				},
			}
			result := adapter.Query(context.Background(), tt.provider, tt.token)
			if tt.wantErr != "" {
				if result.Success {
					t.Errorf("expected failure, got success")
				}
				if result.ErrorCode != tt.wantErr {
					t.Errorf("error_code = %q, want %q", result.ErrorCode, tt.wantErr)
				}
				return
			}
			if !result.Success {
				t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
			}
			if len(result.Balances) == 0 {
				t.Fatal("no balances returned")
			}
			if result.Balances[0].Unit != tt.wantUnit {
				t.Errorf("unit = %q, want %q", result.Balances[0].Unit, tt.wantUnit)
			}
		})
	}
}
