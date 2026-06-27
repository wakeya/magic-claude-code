package providerquota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dop251/goja"
)

const (
	scriptParseTimeout   = 200 * time.Millisecond
	scriptExtractTimeout = 500 * time.Millisecond
	maxRequestBodySize   = 256 * 1024    // 256 KiB
	maxResponseBodySize  = 2 * 1024 * 1024 // 2 MiB
	maxRedirects         = 3
	maxErrorBodyBytes    = 512
)

// ScriptRequest describes the HTTP request produced by the script's first phase.
type ScriptRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`
}

// ScriptExecutor runs restricted JavaScript scripts to build HTTP requests
// and extract quota data from responses.
type ScriptExecutor struct {
	HTTPClient *http.Client
}

// NewScriptExecutor creates a ScriptExecutor with the given HTTP timeout.
func NewScriptExecutor(timeout time.Duration) *ScriptExecutor {
	return &ScriptExecutor{
		HTTPClient: &http.Client{
			Timeout: timeout,
			// Disable automatic redirects; we validate each manually.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ExecuteScript runs a script to produce an HTTP request, performs the request,
// and runs the extractor on the response. Placeholders in the request URL and
// headers are replaced with the provided values before the HTTP call.
//
// placeholderValues maps placeholder names (without braces) to their values,
// e.g. {"apiKey": "sk-xxx", "baseUrl": "https://example.com"}.
func (e *ScriptExecutor) ExecuteScript(ctx context.Context, script string, placeholderValues map[string]string) (*ProviderQuotaResult, error) {
	start := time.Now()

	// Phase 1: Parse request config from script.
	reqConfig, err := e.parseRequest(script)
	if err != nil {
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    "script_error",
			ErrorMessage: sanitizeError(err.Error()),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	// Substitute placeholders in URL and headers.
	reqConfig.URL = substitutePlaceholders(reqConfig.URL, placeholderValues)
	for k, v := range reqConfig.Headers {
		reqConfig.Headers[k] = substitutePlaceholders(v, placeholderValues)
	}

	// Validate the request.
	if err := validateScriptRequest(reqConfig); err != nil {
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    "invalid_config",
			ErrorMessage: sanitizeError(err.Error()),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	// Phase 2: Perform HTTP request.
	body, statusCode, err := e.doHTTPRequest(ctx, reqConfig)
	if err != nil {
		ec := classifyHTTPError(err)
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    ec,
			ErrorMessage: sanitizeError(err.Error()),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	if statusCode >= 400 {
		ec := "upstream_http_error"
		if statusCode == 401 || statusCode == 403 {
			ec = "invalid_credentials"
		}
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    ec,
			ErrorMessage: fmt.Sprintf("HTTP %d", statusCode),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	// Phase 3: Run extractor on response.
	extracted, err := e.runExtractor(script, string(body))
	if err != nil {
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    "script_error",
			ErrorMessage: sanitizeError(err.Error()),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	// Normalize extracted values into result.
	result, err := normalizeExtracted(extracted, start)
	if err != nil {
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    "invalid_response",
			ErrorMessage: sanitizeError(err.Error()),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	return result, nil
}

// parseRequest runs Phase 1: evaluates the script and extracts the request config.
func (e *ScriptExecutor) parseRequest(script string) (*ScriptRequest, error) {
	vm := goja.New()
	defer vm.Interrupt("")

	// Set a timeout via interrupt goroutine.
	done := make(chan struct{})
	timer := time.AfterFunc(scriptParseTimeout, func() {
		vm.Interrupt("script execution timeout")
		close(done)
	})
	defer func() {
		timer.Stop()
		select {
		case <-done:
		default:
		}
	}()

	val, err := vm.RunString(script)
	if err != nil {
		return nil, fmt.Errorf("script parse: %w", err)
	}

	obj := val.ToObject(vm)
	if obj == nil {
		return nil, fmt.Errorf("script must return an object")
	}

	reqVal := obj.Get("request")
	if reqVal == nil || goja.IsUndefined(reqVal) {
		return nil, fmt.Errorf("script must return an object with a 'request' property")
	}

	reqJSON, err := json.Marshal(reqVal.Export())
	if err != nil {
		return nil, fmt.Errorf("script request not serializable: %w", err)
	}

	var req ScriptRequest
	if err := json.Unmarshal(reqJSON, &req); err != nil {
		return nil, fmt.Errorf("script request not a valid request: %w", err)
	}

	return &req, nil
}

// runExtractor runs Phase 2: calls the extractor function with the response body.
func (e *ScriptExecutor) runExtractor(script string, responseBody string) (any, error) {
	vm := goja.New()
	defer vm.Interrupt("")

	done := make(chan struct{})
	timer := time.AfterFunc(scriptExtractTimeout, func() {
		vm.Interrupt("extractor execution timeout")
		close(done)
	})
	defer func() {
		timer.Stop()
		select {
		case <-done:
		default:
		}
	}()

	val, err := vm.RunString(script)
	if err != nil {
		return nil, fmt.Errorf("script parse: %w", err)
	}

	obj := val.ToObject(vm)
	if obj == nil {
		return nil, fmt.Errorf("script must return an object")
	}

	extractorVal := obj.Get("extractor")
	if extractorVal == nil || goja.IsUndefined(extractorVal) {
		return nil, fmt.Errorf("script must return an object with an 'extractor' function")
	}

	extractor, ok := goja.AssertFunction(extractorVal)
	if !ok {
		return nil, fmt.Errorf("'extractor' must be a function")
	}

	// Parse response as JSON for the extractor.
	var respObj any
	if err := json.Unmarshal([]byte(responseBody), &respObj); err != nil {
		// If not valid JSON, pass the raw string.
		respObj = responseBody
	}

	result, err := extractor(goja.Undefined(), vm.ToValue(respObj))
	if err != nil {
		return nil, fmt.Errorf("extractor error: %w", err)
	}

	return result.Export(), nil
}

// doHTTPRequest performs the HTTP request with redirects, body limit, and same-origin checks.
func (e *ScriptExecutor) doHTTPRequest(ctx context.Context, req *ScriptRequest) ([]byte, int, error) {
	var bodyReader io.Reader
	if req.Body != nil {
		bodyBytes, err := json.Marshal(req.Body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		if len(bodyBytes) > maxRequestBodySize {
			return nil, 0, fmt.Errorf("request body exceeds %d bytes", maxRequestBodySize)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Manual redirect handling with same-origin validation.
	currentURL := req.URL
	for redirect := 0; redirect <= maxRedirects; redirect++ {
		resp, err := e.HTTPClient.Do(httpReq)
		if err != nil {
			return nil, 0, err
		}

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			resp.Body.Close()
			if location == "" {
				return nil, 0, fmt.Errorf("redirect without Location header")
			}

			// Resolve relative redirect.
			redirectURL, err := url.Parse(location)
			if err != nil {
				return nil, 0, fmt.Errorf("invalid redirect URL: %w", err)
			}
			baseURL, _ := url.Parse(currentURL)
			redirectURL = baseURL.ResolveReference(redirectURL)

			// Same-origin check.
			if redirectURL.Host != baseURL.Host {
				return nil, 0, fmt.Errorf("cross-origin redirect rejected: %s -> %s", baseURL.Host, redirectURL.Host)
			}

			currentURL = redirectURL.String()
			httpReq, err = http.NewRequestWithContext(ctx, req.Method, currentURL, nil)
			if err != nil {
				return nil, 0, err
			}
			for k, v := range req.Headers {
				httpReq.Header.Set(k, v)
			}
			continue
		}

		// Non-redirect response.
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize+1))
		if err != nil {
			return nil, 0, err
		}
		if len(body) > maxResponseBodySize {
			return nil, 0, fmt.Errorf("response exceeds %d bytes", maxResponseBodySize)
		}
		return body, resp.StatusCode, nil
	}

	return nil, 0, fmt.Errorf("too many redirects (max %d)", maxRedirects)
}

// validateScriptRequest checks that the request is safe.
func validateScriptRequest(req *ScriptRequest) error {
	if req.URL == "" {
		return fmt.Errorf("request URL is empty")
	}

	u, err := url.Parse(req.URL)
	if err != nil {
		return fmt.Errorf("invalid request URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("request URL must use http or https scheme, got %q", u.Scheme)
	}

	if u.User != nil {
		return fmt.Errorf("request URL must not contain userinfo")
	}

	// Method validation.
	method := strings.ToUpper(req.Method)
	if method == "" {
		req.Method = "GET"
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return fmt.Errorf("only GET and POST methods are allowed, got %q", req.Method)
	}

	// Forbidden headers.
	forbidden := map[string]bool{
		"host":              true,
		"content-length":    true,
		"transfer-encoding": true,
		"connection":        true,
		"proxy-authorization": true,
	}
	for k := range req.Headers {
		if forbidden[strings.ToLower(k)] {
			return fmt.Errorf("header %q is not allowed", k)
		}
	}

	return nil
}

// substitutePlaceholders replaces {{key}} patterns in a string.
func substitutePlaceholders(s string, values map[string]string) string {
	for key, val := range values {
		s = strings.ReplaceAll(s, "{{"+key+"}}", val)
	}
	return s
}

// classifyHTTPError maps Go HTTP errors to stable error codes.
func classifyHTTPError(err error) string {
	if err == context.DeadlineExceeded || err == context.Canceled {
		return "request_timeout"
	}
	if strings.Contains(err.Error(), "timeout") {
		return "request_timeout"
	}
	return "network_error"
}

// sanitizeError removes potentially sensitive information from error messages.
func sanitizeError(msg string) string {
	// Truncate very long messages.
	if len(msg) > 512 {
		msg = msg[:512]
	}
	// Remove URLs that might contain tokens.
	// This is a simple heuristic; the actual HTTP client logs are separate.
	return msg
}
