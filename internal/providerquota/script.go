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
	maxRequestBodySize   = 256 * 1024     // 256 KiB
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
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ExecuteScript runs a script to produce an HTTP request, performs the request,
// and runs the extractor on the response. effectiveBaseURL is used for origin
// validation — the request URL must share scheme+host with it.
func (e *ScriptExecutor) ExecuteScript(ctx context.Context, script string, placeholderValues map[string]string, effectiveBaseURL string) (*ProviderQuotaResult, error) {
	start := time.Now()

	// Phase 1: Parse request config from script.
	reqConfig, err := e.parseRequest(script)
	if err != nil {
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    "script_error",
			ErrorMessage: sanitizeError(err.Error(), placeholderValues),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	// Substitute placeholders in URL and headers.
	reqConfig.URL = substitutePlaceholders(reqConfig.URL, placeholderValues)
	for k, v := range reqConfig.Headers {
		reqConfig.Headers[k] = substitutePlaceholders(v, placeholderValues)
	}

	// Validate the request (including origin check).
	if err := validateScriptRequest(reqConfig, effectiveBaseURL); err != nil {
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    "invalid_config",
			ErrorMessage: sanitizeError(err.Error(), placeholderValues),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	// Phase 2: Perform HTTP request.
	body, statusCode, err := e.doHTTPRequest(ctx, reqConfig, effectiveBaseURL)
	if err != nil {
		ec := classifyHTTPError(err)
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    ec,
			ErrorMessage: sanitizeError(err.Error(), placeholderValues),
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
			ErrorMessage: sanitizeError(err.Error(), placeholderValues),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	result, err := normalizeExtracted(extracted, start)
	if err != nil {
		// A business error from the extractor is a structured upstream failure,
		// not an invalid_response — surface it with its own error code.
		if be, ok := err.(*businessError); ok {
			return &ProviderQuotaResult{
				Success:      false,
				ErrorCode:    be.code,
				ErrorMessage: sanitizeError(be.message, placeholderValues),
				QueriedAt:    time.Now(),
				DurationMS:   time.Since(start).Milliseconds(),
			}, nil
		}
		return &ProviderQuotaResult{
			Success:      false,
			ErrorCode:    "invalid_response",
			ErrorMessage: sanitizeError(err.Error(), placeholderValues),
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil
	}

	return result, nil
}

func (e *ScriptExecutor) parseRequest(script string) (*ScriptRequest, error) {
	vm := goja.New()
	defer vm.Interrupt("")

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

	var respObj any
	if err := json.Unmarshal([]byte(responseBody), &respObj); err != nil {
		respObj = responseBody
	}

	result, err := extractor(goja.Undefined(), vm.ToValue(respObj))
	if err != nil {
		return nil, fmt.Errorf("extractor error: %w", err)
	}

	return result.Export(), nil
}

// doHTTPRequest performs the HTTP request with redirect origin validation.
func (e *ScriptExecutor) doHTTPRequest(ctx context.Context, req *ScriptRequest, effectiveBaseURL string) ([]byte, int, error) {
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

	// Resolve the allowed origin from effective base URL.
	var allowedHost string
	if effectiveBaseURL != "" {
		if baseU, err := url.Parse(effectiveBaseURL); err == nil {
			allowedHost = baseU.Host
		}
	}

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

			redirectURL, err := url.Parse(location)
			if err != nil {
				return nil, 0, fmt.Errorf("invalid redirect URL: %w", err)
			}
			baseURL, _ := url.Parse(currentURL)
			redirectURL = baseURL.ResolveReference(redirectURL)

			// Same-origin check against both current URL and effective base.
			if redirectURL.Host != baseURL.Host {
				return nil, 0, fmt.Errorf("cross-origin redirect rejected: %s -> %s", baseURL.Host, redirectURL.Host)
			}
			if allowedHost != "" && redirectURL.Host != allowedHost {
				return nil, 0, fmt.Errorf("redirect target not in allowed origin: %s", redirectURL.Host)
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
// effectiveBaseURL constrains the allowed origin: the request URL must
// share scheme and host with it (unless effectiveBaseURL is empty, in
// which case only basic validation is applied).
func validateScriptRequest(req *ScriptRequest, effectiveBaseURL string) error {
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

	// Origin check: request URL must match effective base URL host.
	if effectiveBaseURL != "" {
		baseU, err := url.Parse(effectiveBaseURL)
		if err == nil && baseU.Host != "" {
			if u.Host != baseU.Host {
				return fmt.Errorf("request URL host %q does not match effective base URL host %q", u.Host, baseU.Host)
			}
		}
	}

	method := strings.ToUpper(req.Method)
	if method == "" {
		req.Method = "GET"
		method = "GET"
	}
	if method != "GET" && method != "POST" {
		return fmt.Errorf("only GET and POST methods are allowed, got %q", req.Method)
	}

	forbidden := map[string]bool{
		"host":                true,
		"content-length":      true,
		"transfer-encoding":   true,
		"connection":          true,
		"proxy-authorization": true,
	}
	for k := range req.Headers {
		if forbidden[strings.ToLower(k)] {
			return fmt.Errorf("header %q is not allowed", k)
		}
	}

	return nil
}

func substitutePlaceholders(s string, values map[string]string) string {
	for key, val := range values {
		s = strings.ReplaceAll(s, "{{"+key+"}}", val)
	}
	return s
}

func classifyHTTPError(err error) string {
	if err == context.DeadlineExceeded || err == context.Canceled {
		return "request_timeout"
	}
	if strings.Contains(err.Error(), "timeout") {
		return "request_timeout"
	}
	return "network_error"
}

// sanitizeError removes secret values and truncates error messages.
// placeholderValues contains the secret values that were substituted;
// any occurrence of these values in the error message is redacted.
func sanitizeError(msg string, placeholderValues map[string]string) string {
	// Redact all secret values that were substituted into the request.
	for _, val := range placeholderValues {
		if val == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, val, "[REDACTED]")
	}
	if len(msg) > 512 {
		msg = msg[:512]
	}
	return msg
}
