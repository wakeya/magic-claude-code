package providerquota

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProcessScriptWorker(t *testing.T) {
	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := `({
		request: {
			url: "https://api.example.com/usage",
			method: "GET",
			headers: {"Authorization": "Bearer {{apiKey}}"}
		},
		extractor: function(response) {
			return {window: "monthly", utilization: response.used};
		}
	})`

	req, err := runner.ParseRequest(ctx, script)
	if err != nil {
		t.Fatalf("parse request through process worker: %v", err)
	}
	if req.URL != "https://api.example.com/usage" {
		t.Errorf("url = %q", req.URL)
	}
	if req.Headers["Authorization"] != "Bearer {{apiKey}}" {
		t.Errorf("authorization = %q", req.Headers["Authorization"])
	}

	extracted, err := runner.RunExtractor(ctx, script, `{"used":37}`)
	if err != nil {
		t.Fatalf("run extractor through process worker: %v", err)
	}
	result, ok := extracted.(map[string]any)
	if !ok {
		t.Fatalf("extractor result type = %T", extracted)
	}
	if result["window"] != "monthly" || result["utilization"] != float64(37) {
		t.Fatalf("extractor result = %#v", result)
	}
}

func TestProcessScriptWorkerCancellation(t *testing.T) {
	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com", method: "GET"},
		extractor: function(response) { return response; }
	})`)
	if err == nil {
		t.Fatal("ParseRequest() error = nil, want cancellation")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("ParseRequest() error = %q, want stable cancellation", err)
	}
}

func TestProcessScriptWorkerRejectsMalformedResponse(t *testing.T) {
	t.Setenv(scriptWorkerTestBehaviorEnv, "malformed")

	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com", method: "GET"},
		extractor: function(response) { return response; }
	})`)
	if err == nil {
		t.Fatal("ParseRequest() error = nil, want malformed response failure")
	}
	if err.Error() != "invalid script worker response" {
		t.Fatalf("ParseRequest() error = %q", err)
	}
}

func TestProcessScriptWorkerRejectsTrailingOutput(t *testing.T) {
	t.Setenv(scriptWorkerTestBehaviorEnv, "trailing")

	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com", method: "GET"},
		extractor: function(response) { return response; }
	})`)
	if err == nil {
		t.Fatal("ParseRequest() error = nil, want trailing output failure")
	}
	if err.Error() != "invalid script worker response" {
		t.Fatalf("ParseRequest() error = %q", err)
	}
}
