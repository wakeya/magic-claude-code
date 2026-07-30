package providerquota

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func runScriptWorkerForTest(t *testing.T, req scriptWorkerRequest) (int, scriptWorkerResponse) {
	t.Helper()

	input, err := encodeScriptWorkerRequest(req)
	if err != nil {
		t.Fatalf("marshal worker request: %v", err)
	}

	var output bytes.Buffer
	code := runScriptWorker(bytes.NewReader(input), &output, func() (func(), error) {
		return func() {}, nil
	})

	var resp scriptWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("decode worker response %q: %v", output.String(), err)
	}
	return code, resp
}

func TestRunScriptWorkerParseRequest(t *testing.T) {
	code, resp := runScriptWorkerForTest(t, scriptWorkerRequest{
		Version:   scriptWorkerProtocolVersion,
		Operation: scriptWorkerOperationParseRequest,
		Script: `({
			request: {
				url: "https://api.example.com/usage",
				method: "POST",
				headers: {"Authorization": "Bearer {{apiKey}}"},
				bodyType: "form",
				body: {page: 1}
			},
			extractor: function(response) { return response; }
		})`,
	})
	if code != 0 {
		t.Fatalf("worker exit code = %d, want 0", code)
	}
	if !resp.OK {
		t.Fatalf("worker response failed: %s", resp.Error)
	}

	var req ScriptRequest
	if err := json.Unmarshal(resp.Payload, &req); err != nil {
		t.Fatalf("decode request payload: %v", err)
	}
	if req.URL != "https://api.example.com/usage" {
		t.Errorf("url = %q", req.URL)
	}
	if req.Method != "POST" {
		t.Errorf("method = %q", req.Method)
	}
	if req.Headers["Authorization"] != "Bearer {{apiKey}}" {
		t.Errorf("authorization header = %q", req.Headers["Authorization"])
	}
	if req.BodyType != "form" {
		t.Errorf("body type = %q", req.BodyType)
	}
}

func TestRunScriptWorkerExtractor(t *testing.T) {
	code, resp := runScriptWorkerForTest(t, scriptWorkerRequest{
		Version:      scriptWorkerProtocolVersion,
		Operation:    scriptWorkerOperationRunExtractor,
		ResponseBody: `{"used":42,"total":100}`,
		Script: `({
			request: {url: "https://api.example.com/usage", method: "GET"},
			extractor: function(response) {
				return {window: "monthly", utilization: response.used / response.total * 100};
			}
		})`,
	})
	if code != 0 {
		t.Fatalf("worker exit code = %d, want 0", code)
	}
	if !resp.OK {
		t.Fatalf("worker response failed: %s", resp.Error)
	}

	var result map[string]any
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		t.Fatalf("decode extractor payload: %v", err)
	}
	if result["window"] != "monthly" {
		t.Errorf("window = %#v", result["window"])
	}
	if result["utilization"] != float64(42) {
		t.Errorf("utilization = %#v", result["utilization"])
	}
}

func TestScriptWorkerRejectsInvalidProtocol(t *testing.T) {
	tests := []struct {
		name string
		req  scriptWorkerRequest
	}{
		{
			name: "wrong version",
			req: scriptWorkerRequest{
				Version:   scriptWorkerProtocolVersion + 1,
				Operation: scriptWorkerOperationParseRequest,
				Script:    `({request:{url:"https://example.com",method:"GET"}})`,
			},
		},
		{
			name: "unknown operation",
			req: scriptWorkerRequest{
				Version:   scriptWorkerProtocolVersion,
				Operation: "launch_missiles",
				Script:    `({request:{url:"https://example.com",method:"GET"}})`,
			},
		},
		{
			name: "missing script",
			req: scriptWorkerRequest{
				Version:   scriptWorkerProtocolVersion,
				Operation: scriptWorkerOperationParseRequest,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, resp := runScriptWorkerForTest(t, tt.req)
			if code == 0 {
				t.Fatal("worker exit code = 0, want failure")
			}
			if resp.OK {
				t.Fatal("worker response OK = true, want false")
			}
			if resp.Error == "" {
				t.Fatal("worker error is empty")
			}
			if len(resp.Payload) != 0 {
				t.Fatalf("failed response payload = %q, want empty", resp.Payload)
			}
		})
	}
}

func TestScriptWorkerLimitFailureIsFailClosed(t *testing.T) {
	input, err := encodeScriptWorkerRequest(scriptWorkerRequest{
		Version:   scriptWorkerProtocolVersion,
		Operation: scriptWorkerOperationParseRequest,
		Script:    `({request:{url:"https://example.com",method:"GET"}})`,
	})
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	code := runScriptWorker(bytes.NewReader(input), &output, func() (func(), error) {
		return nil, errors.New("setrlimit secret detail")
	})
	if code == 0 {
		t.Fatal("worker exit code = 0, want failure")
	}

	var resp scriptWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("decode worker response: %v", err)
	}
	if resp.OK {
		t.Fatal("worker response OK = true, want false")
	}
	if !strings.Contains(resp.Error, "resource limit") {
		t.Fatalf("worker error = %q, want stable resource limit message", resp.Error)
	}
	if strings.Contains(resp.Error, "secret detail") {
		t.Fatalf("worker error leaked setup detail: %q", resp.Error)
	}
}

func TestScriptWorkerInvocation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "exact", args: []string{ScriptWorkerArg}, want: true},
		{name: "none"},
		{name: "prefix", args: []string{ScriptWorkerArg + "-evil"}},
		{name: "extra argument", args: []string{ScriptWorkerArg, "--version"}},
		{name: "normal version", args: []string{"--version"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsScriptWorkerInvocation(tt.args); got != tt.want {
				t.Fatalf("IsScriptWorkerInvocation(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestScriptWorkerRejectsTrailingInput(t *testing.T) {
	input, err := encodeScriptWorkerRequest(scriptWorkerRequest{
		Version:   scriptWorkerProtocolVersion,
		Operation: scriptWorkerOperationParseRequest,
		Script:    `({request:{url:"https://example.com",method:"GET"}})`,
	})
	if err != nil {
		t.Fatal(err)
	}
	input = append(input, []byte("{}")...)

	var output bytes.Buffer
	code := runScriptWorker(bytes.NewReader(input), &output, func() (func(), error) {
		return nil, nil
	})
	if code == 0 {
		t.Fatal("worker exit code = 0, want trailing input failure")
	}

	var resp scriptWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("decode worker response: %v", err)
	}
	if resp.OK || resp.Error != "invalid script worker protocol request" {
		t.Fatalf("worker response = %#v", resp)
	}
}

func TestScriptWorkerRejectsOversizedInput(t *testing.T) {
	input := strings.Repeat(" ", int(maxScriptWorkerInputSize)+1)
	var output bytes.Buffer

	code := runScriptWorker(strings.NewReader(input), &output, func() (func(), error) {
		return nil, nil
	})
	if code == 0 {
		t.Fatal("worker exit code = 0, want oversized input failure")
	}

	var resp scriptWorkerResponse
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("decode worker response: %v", err)
	}
	if resp.OK || resp.Error != "invalid script worker protocol request" {
		t.Fatalf("worker response = %#v", resp)
	}
}

func TestEncodeScriptWorkerRequestKeepsHTMLEscapeHeavyHeaderSmall(t *testing.T) {
	// Regression: json.Marshal HTML-escapes '<', '>', '&' to \uXXXX (6x),
	// which inflated a script full of such characters past the 256 KiB header
	// limit. The header is a trusted in-process frame, so HTML escaping is now
	// disabled and a 60 KiB '<'-heavy script must encode within the limit and
	// round-trip through the worker.
	suffix := `({request:{url:"https://example.com",method:"GET"},extractor:function(r){return r;}})`
	script := "/*" + strings.Repeat("<", 60*1024) + "*/" + suffix
	if len(script) > maxScriptSourceSize {
		t.Fatalf("test script = %d bytes, must fit maxScriptSourceSize %d", len(script), maxScriptSourceSize)
	}

	req := scriptWorkerRequest{
		Version:   scriptWorkerProtocolVersion,
		Operation: scriptWorkerOperationParseRequest,
		Script:    script,
	}
	input, err := encodeScriptWorkerRequest(req)
	if err != nil {
		t.Fatalf("encodeScriptWorkerRequest: %v", err)
	}
	header, _, found := bytes.Cut(input, []byte{'\n'})
	if !found {
		t.Fatal("frame delimiter missing")
	}
	if int64(len(header)) > maxScriptWorkerHeaderSize {
		t.Fatalf("header = %d bytes, exceeds %d (HTML escaping not disabled)",
			len(header), maxScriptWorkerHeaderSize)
	}

	code, resp := runScriptWorkerForTest(t, req)
	if code != 0 || !resp.OK {
		t.Fatalf("worker rejected HTML-heavy frame: code=%d resp=%#v", code, resp)
	}
}

func TestEncodeScriptWorkerRequestMaxBodyFitsInputLimit(t *testing.T) {
	// Regression for the frame off-by-one: header + '\n' delimiter + a
	// max-size response body must fit maxScriptWorkerInputSize, which now
	// accounts for the delimiter byte.
	input, err := encodeScriptWorkerRequest(scriptWorkerRequest{
		Version:      scriptWorkerProtocolVersion,
		Operation:    scriptWorkerOperationRunExtractor,
		Script:       `({request:{url:"https://example.com",method:"GET"},extractor:function(r){return r;}})`,
		ResponseBody: strings.Repeat("a", maxResponseBodySize),
	})
	if err != nil {
		t.Fatalf("encodeScriptWorkerRequest: %v", err)
	}
	if int64(len(input)) > maxScriptWorkerInputSize {
		t.Fatalf("encoded frame = %d bytes, exceeds input limit %d",
			len(input), maxScriptWorkerInputSize)
	}
}
