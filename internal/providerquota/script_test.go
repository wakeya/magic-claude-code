package providerquota

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		srv.URL,
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
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL}, srv.URL)
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
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL}, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for cross-origin redirect")
	}
}

func TestScriptExecutorRedirectPreservesBody(t *testing.T) {
	type redirectedRequest struct {
		body        string
		contentType string
	}
	finalReq := make(chan redirectedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
		case "/final":
			if r.Method != http.MethodPost {
				t.Errorf("redirect method = %q, want POST", r.Method)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read redirected body: %v", err)
			}
			finalReq <- redirectedRequest{
				body:        string(body),
				contentType: r.Header.Get("Content-Type"),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"balance": 9})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	script := `({
		request: {
			url: "{{baseUrl}}/start",
			method: "POST",
			bodyType: "form",
			body: {alpha: "one", beta: "{{apiKey}}"}
		},
		extractor: function(r) { return {remaining: r.balance}; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script,
		map[string]string{"baseUrl": srv.URL, "apiKey": "two"}, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}

	select {
	case got := <-finalReq:
		if !strings.HasPrefix(got.contentType, "application/x-www-form-urlencoded") {
			t.Fatalf("redirected content-type = %q, want form-urlencoded", got.contentType)
		}
		form, err := url.ParseQuery(got.body)
		if err != nil {
			t.Fatalf("redirected body is not form data: %v (raw %q)", err, got.body)
		}
		if form.Get("alpha") != "one" {
			t.Errorf("alpha = %q, want one", form.Get("alpha"))
		}
		if form.Get("beta") != "two" {
			t.Errorf("beta = %q, want two", form.Get("beta"))
		}
	default:
		t.Fatal("redirect target was not called")
	}
}

func TestParseRequestRejectsLargeArray(t *testing.T) {
	exec := NewScriptExecutor(5 * time.Second)
	_, err := exec.parseRequest(`({
		request: {url: "http://example.com", method: "GET"},
		bomb: new Array(1e9),
		extractor: function(r) { return {}; }
	})`)
	if err == nil {
		t.Fatal("expected large array script to be rejected")
	}
	if !strings.Contains(err.Error(), "potential resource abuse") {
		t.Fatalf("error = %v, want potential resource abuse", err)
	}
}

func TestParseRequestRejectsInfiniteLoop(t *testing.T) {
	exec := NewScriptExecutor(5 * time.Second)
	_, err := exec.parseRequest(`({
		request: {url: "http://example.com", method: "GET"},
		spin: (function() { while(true) {} })(),
		extractor: function(r) { return {}; }
	})`)
	if err == nil {
		t.Fatal("expected infinite loop script to be rejected")
	}
	if !strings.Contains(err.Error(), "potential resource abuse") {
		t.Fatalf("error = %v, want potential resource abuse", err)
	}
}

func TestParseRequestAllowsNormalExtractorScript(t *testing.T) {
	exec := NewScriptExecutor(5 * time.Second)
	req, err := exec.parseRequest(`({
		request: {
			url: "http://example.com/balance",
			method: "POST",
			bodyType: "json",
			body: {token: "{{apiKey}}"}
		},
		extractor: function(response) {
			return {remaining: response.balance, unit: "USD"};
		}
	})`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.URL != "http://example.com/balance" {
		t.Errorf("url = %q, want http://example.com/balance", req.URL)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
}

func TestScriptExecutorForbidMethod(t *testing.T) {
	script := `({
		request: { url: "http://example.com", method: "DELETE" },
		extractor: function(r) { return {}; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, nil, "")
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
	result, err := exec.ExecuteScript(context.Background(), script, nil, "")
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
	result, err := exec.ExecuteScript(context.Background(), script, nil, "")
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
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL}, srv.URL)
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
	}, srv.URL)
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
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL}, srv.URL)
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
	result, err := exec.ExecuteScript(context.Background(), "this is not valid javascript!!!", nil, "")
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
	result, err := exec.ExecuteScript(context.Background(), `({request: {url: "http://example.com", method: "GET"}})`, nil, "")
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
	result, err := exec.ExecuteScript(context.Background(), script, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for ftp scheme")
	}
}

func TestScriptExecutorOriginCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"balance": 1})
	}))
	defer srv.Close()

	script := `({
		request: { url: "http://evil.example.com/balance", method: "GET" },
		extractor: function(r) { return { remaining: r.balance }; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, nil, "https://safe.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for cross-origin request")
	}
	if result.ErrorCode != "invalid_config" {
		t.Errorf("error_code = %q, want invalid_config", result.ErrorCode)
	}
}

// TestScriptRejectsHTTPSSchemeDowngrade verifies that a script request using
// HTTP against an HTTPS effective Base URL is rejected, preventing credential
// leakage over an insecure downgrade to the same host.
func TestScriptRejectsHTTPSSchemeDowngrade(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"balance": 1})
	}))
	defer srv.Close()

	// Effective Base URL is HTTPS; script requests plain HTTP on same host.
	// srv.URL from NewTLSServer is already https:// — derive the http variant.
	httpURL := strings.Replace(srv.URL, "https://", "http://", 1)
	tlsURL := srv.URL

	script := `({
		request: { url: "` + httpURL + `/balance", method: "GET", headers: { "Authorization": "Bearer secret" } },
		extractor: function(r) { return { remaining: r.balance }; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, nil, tlsURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for HTTPS→HTTP scheme downgrade")
	}
	if result.ErrorCode != "invalid_config" {
		t.Errorf("error_code = %q, want invalid_config", result.ErrorCode)
	}
}

// TestScriptAllowsSameOriginHTTP verifies that HTTP is allowed when the
// provider's effective Base URL itself is HTTP (same-origin HTTP provider).
func TestScriptAllowsSameOriginHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"balance": 5})
	}))
	defer srv.Close()

	script := `({
		request: { url: "` + srv.URL + `/balance", method: "GET" },
		extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, nil, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("same-origin HTTP should be allowed: %s", result.ErrorMessage)
	}
}

// TestScriptExtractorBusinessError verifies that when an extractor returns an
// object with __error_code, ExecuteScript classifies it as a structured
// business error rather than a generic script_error.
func TestScriptExtractorBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "permission denied"})
	}))
	defer srv.Close()

	script := `({
		request: { url: "{{baseUrl}}/api", method: "GET" },
		extractor: function(response) {
			if (response.success === false) {
				return {
					__error_code: "upstream_business_error",
					__error_message: response.message || "API error"
				};
			}
			return { remaining: 1 };
		}
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script, map[string]string{"baseUrl": srv.URL}, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for business error")
	}
	if result.ErrorCode != "upstream_business_error" {
		t.Errorf("error_code = %q, want upstream_business_error", result.ErrorCode)
	}
	if result.ErrorMessage != "permission denied" {
		t.Errorf("error_message = %q, want 'permission denied'", result.ErrorMessage)
	}
}

// TestNewAPIScriptBusinessError verifies the default NewAPI script maps a
// success===false response to upstream_business_error (not script_error).
func TestNewAPIScriptBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "no permission"})
	}))
	defer srv.Close()

	script := defaultNewAPIScript()
	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(
		context.Background(),
		script,
		map[string]string{"baseUrl": srv.URL, "accessToken": "tok", "userId": "1"},
		srv.URL,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for NewAPI success=false")
	}
	if result.ErrorCode != "upstream_business_error" {
		t.Errorf("error_code = %q, want upstream_business_error", result.ErrorCode)
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

func TestSubstitutePlaceholdersInBody(t *testing.T) {
	values := map[string]string{"apiKey": "k", "apiKey2": "sec"}
	// string
	if got := substitutePlaceholdersInBody("{{apiKey2}}", values); got != "sec" {
		t.Errorf("string = %v, want sec", got)
	}
	// nested object; input must not be mutated
	objIn := map[string]any{"a": map[string]any{"b": "{{apiKey2}}"}}
	objOut := substitutePlaceholdersInBody(objIn, values)
	m, ok := objOut.(map[string]any)
	if !ok {
		t.Fatalf("object got %T", objOut)
	}
	inner, ok := m["a"].(map[string]any)
	if !ok {
		t.Fatalf("a got %T", m["a"])
	}
	if inner["b"] != "sec" {
		t.Errorf("nested = %v, want sec", inner["b"])
	}
	if objIn["a"].(map[string]any)["b"] != "{{apiKey2}}" {
		t.Error("input map was mutated")
	}
	// array
	arrOut := substitutePlaceholdersInBody([]any{"{{apiKey}}", 1}, values)
	arr, ok := arrOut.([]any)
	if !ok || arr[0] != "k" || arr[1] != 1 {
		t.Errorf("array = %v, want [k 1]", arrOut)
	}
	// number/bool unchanged
	if substitutePlaceholdersInBody(42, values) != 42 {
		t.Error("number changed")
	}
	if substitutePlaceholdersInBody(true, values) != true {
		t.Error("bool changed")
	}
	// nil unchanged
	if substitutePlaceholdersInBody(nil, values) != nil {
		t.Error("nil changed")
	}
}

func TestEncodeRequestBodyForm(t *testing.T) {
	// simple fields, url.Values.Encode sorts keys
	b, err := encodeRequestBody(&ScriptRequest{BodyType: "form", Body: map[string]any{"a": "1", "b": "2"}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(b) != "a=1&b=2" {
		t.Errorf("simple form = %q, want a=1&b=2", string(b))
	}
	// object value JSON-marshaled then URL-encoded
	b, err = encodeRequestBody(&ScriptRequest{BodyType: "form", Body: map[string]any{"params": map[string]any{"x": 1}}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(b) != `params=%7B%22x%22%3A1%7D` {
		t.Errorf("object value form = %q, want params=%%7B%%22x%%22%%3A1%%7D", string(b))
	}
	// nil value skipped
	b, err = encodeRequestBody(&ScriptRequest{BodyType: "form", Body: map[string]any{"skip": nil, "keep": "y"}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(b) != "keep=y" {
		t.Errorf("nil skip = %q, want keep=y", string(b))
	}
	// non-object body errors
	if _, err := encodeRequestBody(&ScriptRequest{BodyType: "form", Body: "string"}); err == nil {
		t.Error("expected error for non-object form body")
	}
	// JSON body (default BodyType) unaffected
	b, err = encodeRequestBody(&ScriptRequest{Body: map[string]any{"a": "1"}})
	if err != nil {
		t.Fatalf("json encode: %v", err)
	}
	if string(b) != `{"a":"1"}` {
		t.Errorf("json = %q, want {\"a\":\"1\"}", string(b))
	}
}

func TestExecuteScriptFormBodyQianwenFixture(t *testing.T) {
	var gotMethod, gotBody, gotCT, gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		gotCookie = r.Header.Get("Cookie")
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": "200",
			"data": map[string]any{
				"DataV2": map[string]any{
					"data": map[string]any{
						"data": map[string]any{
							"per5HourPercentage": 0.0,
							"per1WeekResetTime":  1785462900000,
							"per1WeekPercentage": 1.0,
						},
						"success": true,
					},
				},
				"success":  true,
				"errorMsg": "",
			},
			"successResponse": true,
		})
	}))
	defer srv.Close()

	script := `({
		request: {
			url: "{{baseUrl}}/data/api.json",
			method: "POST",
			bodyType: "form",
			headers: {
				"Cookie": "{{apiKey}}",
				"Content-Type": "application/x-www-form-urlencoded"
			},
			body: {
				product: "sfm_bailian",
				action: "BroadScopeAspnGateway",
				sec_token: "{{apiKey2}}",
				region: "cn-beijing",
				params: {Api: "usage", V: "1.0"}
			}
		},
		extractor: function(response) {
			if (response.code !== "200" || response.successResponse !== true) {
				return {__error_code: "upstream_business_error", __error_message: "fail"};
			}
			var inner = response.data.DataV2.data.data;
			return [
				{window: "five_hour", utilization: inner.per5HourPercentage * 100},
				{window: "seven_day", utilization: inner.per1WeekPercentage * 100, resetsAt: inner.per1WeekResetTime}
			];
		}
	})`

	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script,
		map[string]string{"baseUrl": srv.URL, "apiKey": "cookie-val", "apiKey2": "sec-tok"}, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasPrefix(gotCT, "application/x-www-form-urlencoded") {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotCookie != "cookie-val" {
		t.Errorf("cookie = %q, want cookie-val", gotCookie)
	}
	form, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatalf("parse form body: %v (raw %q)", err, gotBody)
	}
	if form.Get("sec_token") != "sec-tok" {
		t.Errorf("sec_token = %q, want sec-tok", form.Get("sec_token"))
	}
	if form.Get("product") != "sfm_bailian" {
		t.Errorf("product = %q", form.Get("product"))
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(form.Get("params")), &params); err != nil {
		t.Fatalf("params not JSON: %v (raw %q)", err, form.Get("params"))
	}
	if params["Api"] != "usage" {
		t.Errorf("params.Api = %v", params["Api"])
	}
	if len(result.Tiers) != 2 {
		t.Fatalf("expected 2 tiers, got %d", len(result.Tiers))
	}
	var five, seven *QuotaTier
	for i := range result.Tiers {
		switch result.Tiers[i].Name {
		case "five_hour":
			five = &result.Tiers[i]
		case "seven_day":
			seven = &result.Tiers[i]
		}
	}
	if five == nil || five.Utilization != 0 {
		t.Errorf("five_hour tier = %+v", five)
	}
	if seven == nil || seven.Utilization != 100 {
		t.Errorf("seven_day tier = %+v", seven)
	}
	if seven == nil || seven.ResetsAt == nil || !seven.ResetsAt.Equal(time.UnixMilli(1785462900000).UTC()) {
		t.Errorf("seven_day resetsAt = %v, want 1785462900000ms", seven)
	}
}

func TestExecuteScriptJSONBodyBackwardCompat(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"balance": 5})
	}))
	defer srv.Close()

	// JSON body (no BodyType) with {{apiKey}} inside body — previously not
	// substituted, now substituted (intentional enhancement). Asserts the
	// placeholder reaches the body and existing JSON shape is preserved.
	script := `({
		request: {
			url: "{{baseUrl}}/balance",
			method: "POST",
			headers: {"Content-Type": "application/json"},
			body: {key: "{{apiKey}}", n: 1}
		},
		extractor: function(r) {return {remaining: r.balance, unit: "USD"};}
	})`
	exec := NewScriptExecutor(5 * time.Second)
	result, err := exec.ExecuteScript(context.Background(), script,
		map[string]string{"baseUrl": srv.URL, "apiKey": "REAL"}, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("failed: %s", result.ErrorMessage)
	}
	var bodyMap map[string]any
	if err := json.Unmarshal([]byte(gotBody), &bodyMap); err != nil {
		t.Fatalf("body not JSON: %v (raw %q)", err, gotBody)
	}
	if bodyMap["key"] != "REAL" {
		t.Errorf("body.key = %v, want REAL (placeholder must be substituted in body)", bodyMap["key"])
	}
	if bodyMap["n"] != float64(1) {
		t.Errorf("body.n = %v, want 1", bodyMap["n"])
	}
}
