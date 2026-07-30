package providerquota

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestBoundedWorkerBufferMarksExceeded(t *testing.T) {
	buffer := newBoundedWorkerBuffer(4)
	if _, err := io.WriteString(buffer, "1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(buffer, "5"); err != nil {
		t.Fatal(err)
	}
	if !buffer.exceeded {
		t.Fatal("buffer exceeded = false")
	}
	if buffer.Len() != 4 {
		t.Fatalf("buffer length = %d, want 4", buffer.Len())
	}
}

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

func TestProcessScriptWorkerHandlesMaxResponseBody(t *testing.T) {
	if scriptWorkerRaceBuild {
		t.Skip("production worker response-body capacity is verified by a non-race worker")
	}

	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := `({
		request: {url: "https://api.example.com/usage", method: "GET"},
		extractor: function(response) {
			return {length: response.length};
		}
	})`
	extracted, err := runner.RunExtractor(ctx, script, strings.Repeat("a", maxResponseBodySize))
	if err != nil {
		t.Fatalf("RunExtractor() with max response body: %v", err)
	}
	result, ok := extracted.(map[string]any)
	if !ok || result["length"] != float64(maxResponseBodySize) {
		t.Fatalf("extractor result = %#v, want length %d", extracted, maxResponseBodySize)
	}
}

func TestProcessScriptWorkerHandlesEscapedMaxResponseBody(t *testing.T) {
	if scriptWorkerRaceBuild {
		t.Skip("production worker response-body capacity is verified by a non-race worker")
	}

	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := `({
		request: {url: "https://api.example.com/usage", method: "GET"},
		extractor: function(response) {
			return {length: response.length, first: response[0]};
		}
	})`
	extracted, err := runner.RunExtractor(ctx, script, strings.Repeat(`"`, maxResponseBodySize))
	if err != nil {
		t.Fatalf("RunExtractor() with escaped max response body: %v", err)
	}
	result, ok := extracted.(map[string]any)
	if !ok || result["length"] != float64(maxResponseBodySize) || result["first"] != `"` {
		t.Fatalf("extractor result = %#v, want quoted string length %d", extracted, maxResponseBodySize)
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

func TestProcessScriptWorkerMemoryLimit(t *testing.T) {
	if scriptWorkerRaceBuild {
		t.Skip("production worker memory limit is verified by a non-race worker")
	}
	if scriptWorkerHardMemoryLimit != 512*1024*1024 {
		t.Fatalf("hard memory limit = %d, want 512 MiB", scriptWorkerHardMemoryLimit)
	}
	if scriptWorkerSoftMemoryLimit != 384*1024*1024 {
		t.Fatalf("soft memory limit = %d, want 384 MiB", scriptWorkerSoftMemoryLimit)
	}
	if scriptWorkerSoftMemoryLimit >= int64(scriptWorkerHardMemoryLimit) {
		t.Fatalf("soft memory limit %d must be below hard limit %d",
			scriptWorkerSoftMemoryLimit, scriptWorkerHardMemoryLimit)
	}

	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com", method: "GET"},
		bomb: new ArrayBuffer(Number("800000000")),
		extractor: function(response) { return response; }
	})`)
	if err == nil {
		t.Fatal("ParseRequest() error = nil, want hard memory termination")
	}
	if err.Error() != "script worker terminated unexpectedly" {
		t.Fatalf("ParseRequest() error = %q, want hard memory termination", err)
	}

	req, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com/healthy", method: "GET"},
		extractor: function(response) { return response; }
	})`)
	if err != nil {
		t.Fatalf("normal worker after memory termination: %v", err)
	}
	if req.URL != "https://api.example.com/healthy" {
		t.Fatalf("normal worker URL = %q", req.URL)
	}
}

func TestProcessScriptWorkerExtractorMemoryLimit(t *testing.T) {
	if scriptWorkerRaceBuild {
		t.Skip("production 128 MiB limit is verified by a non-race worker")
	}

	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := `({
		request: {url: "https://api.example.com", method: "GET"},
		extractor: function(response) {
			return new ArrayBuffer(Number("800000000"));
		}
	})`
	_, err := runner.RunExtractor(ctx, script, `{"value":1}`)
	if err == nil {
		t.Fatal("RunExtractor() error = nil, want hard memory termination")
	}
	if err.Error() != "script worker terminated unexpectedly" {
		t.Fatalf("RunExtractor() error = %q, want hard memory termination", err)
	}

	extracted, err := runner.RunExtractor(ctx, `({
		request: {url: "https://api.example.com", method: "GET"},
		extractor: function(response) { return {remaining: response.value}; }
	})`, `{"value":9}`)
	if err != nil {
		t.Fatalf("normal extractor worker after memory termination: %v", err)
	}
	result, ok := extracted.(map[string]any)
	if !ok || result["remaining"] != float64(9) {
		t.Fatalf("normal extractor result = %#v", extracted)
	}
}

func TestProcessScriptWorkerDynamicStringMemoryLimit(t *testing.T) {
	if scriptWorkerRaceBuild {
		t.Skip("production 128 MiB limit is verified by a non-race worker")
	}

	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com", method: "GET"},
		bomb: "x".repeat(Number("800000000")),
		extractor: function(response) { return response; }
	})`)
	if err == nil {
		t.Fatal("ParseRequest() error = nil, want hard memory termination")
	}
	if err.Error() != "script worker terminated unexpectedly" {
		t.Fatalf("ParseRequest() error = %q, want hard memory termination", err)
	}

	req, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com/healthy-string", method: "GET"},
		extractor: function(response) { return response; }
	})`)
	if err != nil {
		t.Fatalf("normal worker after dynamic string termination: %v", err)
	}
	if req.URL != "https://api.example.com/healthy-string" {
		t.Fatalf("normal worker URL = %q", req.URL)
	}
}

func TestProcessScriptWorkerOutputLimit(t *testing.T) {
	t.Setenv(scriptWorkerTestBehaviorEnv, "stdout_overflow")
	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com", method: "GET"},
		extractor: function(response) { return response; }
	})`)
	if err == nil {
		t.Fatal("ParseRequest() error = nil, want output limit")
	}
	if err.Error() != "script worker output limit exceeded" {
		t.Fatalf("ParseRequest() error = %q", err)
	}
}

func TestProcessScriptWorkerAbnormalFailuresDoNotLeak(t *testing.T) {
	tests := []struct {
		behavior string
		want     string
	}{
		{behavior: "nonzero", want: "script worker terminated unexpectedly"},
		{behavior: "panic", want: "script worker terminated unexpectedly"},
		{behavior: "stderr_overflow", want: "script worker output limit exceeded"},
		{behavior: "wrong_version", want: "invalid script worker response"},
		{behavior: "failed_with_payload", want: "invalid script worker response"},
	}

	for _, tt := range tests {
		t.Run(tt.behavior, func(t *testing.T) {
			t.Setenv(scriptWorkerTestBehaviorEnv, tt.behavior)
			runner := newProcessScriptWorkerRunner()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := runner.ParseRequest(ctx, `({
				request: {url: "https://api.example.com", method: "GET"},
				extractor: function(response) { return response; }
			})`)
			if err == nil {
				t.Fatal("ParseRequest() error = nil, want abnormal worker failure")
			}
			if err.Error() != tt.want {
				t.Fatalf("ParseRequest() error = %q, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "fatal-worker-secret") {
				t.Fatalf("ParseRequest() leaked worker stderr: %q", err)
			}
		})
	}
}

func TestProcessScriptWorkerCancellationDuringExecution(t *testing.T) {
	t.Setenv(scriptWorkerTestBehaviorEnv, "sleep")
	runner := newProcessScriptWorkerRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := runner.ParseRequest(ctx, `({
		request: {url: "https://api.example.com", method: "GET"},
		extractor: function(response) { return response; }
	})`)
	if err == nil || err.Error() != "script worker canceled" {
		t.Fatalf("ParseRequest() error = %v, want stable cancellation", err)
	}
}

func TestProcessScriptWorkerHardTimeout(t *testing.T) {
	t.Setenv(scriptWorkerTestBehaviorEnv, "sleep")
	runner := newProcessScriptWorkerRunner()

	start := time.Now()
	_, err := runner.ParseRequest(context.Background(), `({
		request: {url: "https://api.example.com", method: "GET"},
		extractor: function(response) { return response; }
	})`)
	if err == nil || err.Error() != "script worker execution timed out" {
		t.Fatalf("ParseRequest() error = %v, want hard timeout", err)
	}
	if elapsed := time.Since(start); elapsed > scriptWorkerProcessTimeout+time.Second {
		t.Fatalf("hard timeout took %v", elapsed)
	}
}
