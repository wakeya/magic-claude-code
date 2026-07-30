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

type spyScriptWorkerRunner struct {
	parseCalls   int
	extractCalls int
	parse        func(context.Context, string) (*ScriptRequest, error)
	extract      func(context.Context, string, string) (any, error)
}

func (s *spyScriptWorkerRunner) ParseRequest(ctx context.Context, script string) (*ScriptRequest, error) {
	s.parseCalls++
	return s.parse(ctx, script)
}

func (s *spyScriptWorkerRunner) RunExtractor(ctx context.Context, script, responseBody string) (any, error) {
	s.extractCalls++
	return s.extract(ctx, script, responseBody)
}

func TestScriptExecutorUsesWorkerRunner(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"used": 23})
	}))
	defer server.Close()

	script := `({
		request: {
			url: "{{baseUrl}}/usage",
			method: "GET",
			headers: {"Authorization": "Bearer {{apiKey}}"}
		},
		extractor: function(response) {
			return {window: "monthly", utilization: response.used};
		}
	})`
	spy := &spyScriptWorkerRunner{
		parse: func(_ context.Context, gotScript string) (*ScriptRequest, error) {
			if !strings.Contains(gotScript, "{{apiKey}}") {
				t.Fatalf("parse worker script lost credential placeholder")
			}
			return &ScriptRequest{
				URL:     "{{baseUrl}}/usage",
				Method:  http.MethodGet,
				Headers: map[string]string{"Authorization": "Bearer {{apiKey}}"},
			}, nil
		},
		extract: func(_ context.Context, gotScript, responseBody string) (any, error) {
			if gotScript != script {
				t.Fatalf("extract worker received different script")
			}
			if !strings.Contains(responseBody, `"used":23`) {
				t.Fatalf("extract worker response = %q", responseBody)
			}
			return map[string]any{
				"window":      "monthly",
				"utilization": float64(23),
			}, nil
		},
	}

	executor := NewScriptExecutor(5 * time.Second)
	executor.workerRunner = spy
	result, err := executor.ExecuteScript(
		context.Background(),
		script,
		map[string]string{"baseUrl": server.URL, "apiKey": "parent-secret"},
		server.URL,
	)
	if err != nil {
		t.Fatalf("ExecuteScript() error: %v", err)
	}
	if !result.Success || len(result.Tiers) != 1 || result.Tiers[0].Utilization != 23 {
		t.Fatalf("ExecuteScript() result = %#v", result)
	}
	if spy.parseCalls != 1 || spy.extractCalls != 1 {
		t.Fatalf("worker calls parse=%d extract=%d, want 1/1", spy.parseCalls, spy.extractCalls)
	}
	if authorization != "Bearer parent-secret" {
		t.Fatalf("upstream authorization = %q", authorization)
	}
}

func TestScriptExecutorSkipsExtractorWorkerAfterHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer server.Close()

	spy := &spyScriptWorkerRunner{
		parse: func(context.Context, string) (*ScriptRequest, error) {
			return &ScriptRequest{URL: server.URL, Method: http.MethodGet}, nil
		},
		extract: func(context.Context, string, string) (any, error) {
			t.Fatal("extract worker must not run after HTTP 401")
			return nil, nil
		},
	}
	executor := NewScriptExecutor(5 * time.Second)
	executor.workerRunner = spy

	result, err := executor.ExecuteScript(
		context.Background(),
		`({request:{},extractor:function(response){return response;}})`,
		nil,
		server.URL,
	)
	if err != nil {
		t.Fatalf("ExecuteScript() error: %v", err)
	}
	if result.Success || result.ErrorCode != "invalid_credentials" {
		t.Fatalf("ExecuteScript() result = %#v", result)
	}
	if spy.parseCalls != 1 || spy.extractCalls != 0 {
		t.Fatalf("worker calls parse=%d extract=%d, want 1/0", spy.parseCalls, spy.extractCalls)
	}
}

func TestScriptExecutorMapsWorkerMemoryTerminationToScriptError(t *testing.T) {
	if scriptWorkerRaceBuild {
		t.Skip("production memory limit is verified by a non-race worker")
	}

	t.Run("parse request", func(t *testing.T) {
		executor := NewScriptExecutor(5 * time.Second)
		result, err := executor.ExecuteScript(
			context.Background(),
			`({
				request: {url: "https://api.example.com", method: "GET"},
				bomb: new ArrayBuffer(Number("800000000")),
				extractor: function(response) { return response; }
			})`,
			nil,
			"https://api.example.com",
		)
		if err != nil {
			t.Fatalf("ExecuteScript() error: %v", err)
		}
		if result.Success || result.ErrorCode != "script_error" {
			t.Fatalf("ExecuteScript() result = %#v", result)
		}
		if result.ErrorMessage != "script worker terminated unexpectedly" {
			t.Fatalf("error message = %q", result.ErrorMessage)
		}
	})

	t.Run("extractor", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"value":1}`))
		}))
		defer server.Close()

		executor := NewScriptExecutor(5 * time.Second)
		result, err := executor.ExecuteScript(
			context.Background(),
			`({
				request: {url: "{{baseUrl}}", method: "GET"},
				extractor: function(response) {
					return new ArrayBuffer(Number("800000000"));
				}
			})`,
			map[string]string{"baseUrl": server.URL},
			server.URL,
		)
		if err != nil {
			t.Fatalf("ExecuteScript() error: %v", err)
		}
		if result.Success || result.ErrorCode != "script_error" {
			t.Fatalf("ExecuteScript() result = %#v", result)
		}
		if result.ErrorMessage != "script worker terminated unexpectedly" {
			t.Fatalf("error message = %q", result.ErrorMessage)
		}
	})
}
