package providerquota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScriptExecutorBasic(t *testing.T) {
	// Mock server returning a balance.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"balance": 12.34,
		})
	}))
	defer srv.Close()

	script := `({
		request: {
			url: "{{baseUrl}}/user/balance",
			method: "GET",
			headers: {
				"Authorization": "Bearer {{apiKey}}",
				"Accept": "application/json"
			}
		},
		extractor: function(response) {
			return {
				remaining: response.balance,
				unit: "USD"
			};
		}
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(
		context.Background(),
		script,
		map[string]string{
			"baseUrl": srv.URL,
			"apiKey":  "test-key",
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if len(result.Balances) != 1 {
		t.Fatalf("expected 1 balance, got %d", len(result.Balances))
	}
	if result.Balances[0].Remaining == nil || *result.Balances[0].Remaining != 12.34 {
		t.Errorf("remaining = %v, want 12.34", result.Balances[0].Remaining)
	}
	if result.Balances[0].Unit != "USD" {
		t.Errorf("unit = %q, want USD", result.Balances[0].Unit)
	}
}

func TestScriptExecutorTimeout(t *testing.T) {
	// Mock server that returns quickly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"balance": 1})
	}))
	defer srv.Close()

	// Script with infinite loop in extractor.
	script := `({
		request: { url: "{{baseUrl}}", method: "GET" },
		extractor: function(r) { while(true) {} return {}; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The extractor timeout should cause a script_error.
	if result.Success {
		t.Error("expected failure for infinite loop")
	}
	if result.ErrorCode != "script_error" {
		t.Errorf("error_code = %q, want script_error", result.ErrorCode)
	}
}

func TestScriptExecutorCrossOriginRedirect(t *testing.T) {
	// Server that redirects to a different host.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example.com/", http.StatusFound)
	}))
	defer srv.Close()

	script := `({
		request: { url: "{{baseUrl}}", method: "GET" },
		extractor: function(r) { return { remaining: 1 }; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for cross-origin redirect")
	}
}

func TestScriptExecutorForbidMethod(t *testing.T) {
	script := `({
		request: { url: "http://example.com", method: "DELETE" },
		extractor: function(r) { return {}; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for DELETE method")
	}
	if result.ErrorCode != "invalid_config" {
		t.Errorf("error_code = %q, want invalid_config", result.ErrorCode)
	}
}

func TestScriptExecutorForbidHeader(t *testing.T) {
	script := `({
		request: {
			url: "http://example.com",
			method: "GET",
			headers: { "Host": "evil.com" }
		},
		extractor: function(r) { return {}; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for forbidden Host header")
	}
}

func TestScriptExecutorForbidUserinfo(t *testing.T) {
	script := `({
		request: { url: "http://user:pass@example.com", method: "GET" },
		extractor: function(r) { return {}; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for URL with userinfo")
	}
}

func TestScriptExecutor401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer srv.Close()

	script := `({
		request: { url: "{{baseUrl}}", method: "GET" },
		extractor: function(r) { return {}; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for 401")
	}
	if result.ErrorCode != "invalid_credentials" {
		t.Errorf("error_code = %q, want invalid_credentials", result.ErrorCode)
	}
}

func TestScriptExecutorSecretNotInSource(t *testing.T) {
	// Verify that the script source itself does not contain the secret.
	// The secret should only be in the HTTP request, not in the JS code.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer my-secret-key" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"balance": 100})
	}))
	defer srv.Close()

	script := `({
		request: {
			url: "{{baseUrl}}/balance",
			method: "GET",
			headers: { "Authorization": "Bearer {{apiKey}}" }
		},
		extractor: function(r) { return { remaining: r.balance }; }
	})`

	// The script source must not contain the actual secret.
	if strings.Contains(script, "my-secret-key") {
		t.Error("script source contains secret")
	}

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{
		"baseUrl": srv.URL,
		"apiKey":  "my-secret-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s", result.ErrorMessage)
	}
}

func TestScriptExecutorTierExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"limits": []any{
				map[string]any{
					"name": "5h",
					"detail": map[string]any{
						"used":      2,
						"limit":     100,
						"resetTime": time.Now().Add(2 * time.Hour).Unix(),
					},
				},
			},
		})
	}))
	defer srv.Close()

	script := `({
		request: { url: "{{baseUrl}}/usage", method: "GET" },
		extractor: function(r) {
			var items = [];
			for (var i = 0; i < r.limits.length; i++) {
				var l = r.limits[i];
				items.push({
					window: "five_hour",
					planName: l.name,
					used: l.detail.used,
					total: l.detail.limit,
					resetsAt: l.detail.resetTime,
					unit: "tokens"
				});
			}
			return items;
		}
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if len(result.Tiers) != 1 {
		t.Fatalf("expected 1 tier, got %d", len(result.Tiers))
	}
	if result.Tiers[0].Name != WindowFiveHour {
		t.Errorf("tier name = %q, want %q", result.Tiers[0].Name, WindowFiveHour)
	}
	if result.Tiers[0].Utilization != 2 {
		t.Errorf("utilization = %f, want 2", result.Tiers[0].Utilization)
	}
}

func TestScriptExecutorInvalidScript(t *testing.T) {
	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), "this is not valid javascript!!!", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for invalid script")
	}
	if result.ErrorCode != "script_error" {
		t.Errorf("error_code = %q, want script_error", result.ErrorCode)
	}
}

func TestScriptExecutorMissingExtractor(t *testing.T) {
	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), `({request: {url: "http://example.com", method: "GET"}})`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for missing extractor")
	}
}

func TestScriptExecutorURLSchemeCheck(t *testing.T) {
	script := `({
		request: { url: "ftp://example.com/file", method: "GET" },
		extractor: function(r) { return {}; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for ftp scheme")
	}
}

func TestSubstitutePlaceholders(t *testing.T) {
	tests := []struct {
		input  string
		values map[string]string
		want   string
	}{
		{"{{baseUrl}}/api", map[string]string{"baseUrl": "https://example.com"}, "https://example.com/api"},
		{"Bearer {{apiKey}}", map[string]string{"apiKey": "sk-123"}, "Bearer sk-123"},
		{"no placeholders", map[string]string{"key": "val"}, "no placeholders"},
		{"{{missing}}", map[string]string{}, "{{missing}}"},
	}
	for _, tt := range tests {
		got := substitutePlaceholders(tt.input, tt.values)
		if got != tt.want {
			t.Errorf("substitutePlaceholders(%q, %v) = %q, want %q", tt.input, tt.values, got, tt.want)
		}
	}
}
