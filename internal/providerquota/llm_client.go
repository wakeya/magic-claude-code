package providerquota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	llmAPIFormatAnthropic       = "anthropic"
	llmAPIFormatOpenAIChat      = "openai_chat"
	llmAPIFormatOpenAIResponses = "openai_responses"
	maxLLMResponseBodySize      = 256 * 1024
)

// LLMProvider is the subset of provider card configuration needed for AI
// script generation. It intentionally lives in providerquota to avoid a
// package cycle with internal/config.
type LLMProvider struct {
	APIURL    string
	APIToken  string
	APIFormat string
}

// LLMClient calls an LLM provider using the card's APIFormat.
type LLMClient struct {
	HTTPClient *http.Client
}

// NewLLMClient builds an LLMClient with the given timeout.
func NewLLMClient(timeout time.Duration) *LLMClient {
	return &LLMClient{HTTPClient: &http.Client{
		Timeout:       timeout,
		CheckRedirect: disableLLMRedirect,
	}}
}

// LLMCallResult is the text returned by the LLM or a structured error.
type LLMCallResult struct {
	Text         string
	ErrorCode    string
	ErrorMessage string
}

// IsLLMAPIFormat reports whether format is supported by the script generator.
func IsLLMAPIFormat(format string) bool {
	switch format {
	case llmAPIFormatAnthropic, llmAPIFormatOpenAIChat, llmAPIFormatOpenAIResponses:
		return true
	default:
		return false
	}
}

// Call invokes the LLM and returns its text response.
func (c *LLMClient) Call(ctx context.Context, provider LLMProvider, model, systemPrompt, userMessage string) LLMCallResult {
	if strings.TrimSpace(provider.APIToken) == "" {
		return LLMCallResult{ErrorCode: "missing_credentials", ErrorMessage: "provider has no api_token"}
	}
	if strings.TrimSpace(model) == "" {
		return LLMCallResult{ErrorCode: "invalid_config", ErrorMessage: "model is required"}
	}

	endpoint, bodyBytes, err := buildLLMRequest(provider, model, systemPrompt, userMessage)
	if err != nil {
		return LLMCallResult{ErrorCode: "invalid_config", ErrorMessage: sanitizeLLMError(err.Error(), provider.APIToken)}
	}
	endpointURL, err := url.Parse(endpoint)
	if err != nil || endpointURL.Hostname() == "" {
		return LLMCallResult{ErrorCode: "invalid_config", ErrorMessage: "invalid LLM endpoint"}
	}
	if isInternalHost(endpointURL.Hostname()) {
		return LLMCallResult{ErrorCode: "invalid_config", ErrorMessage: "LLM endpoint resolves to an internal address"}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return LLMCallResult{ErrorCode: "invalid_config", ErrorMessage: sanitizeLLMError(err.Error(), provider.APIToken)}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	switch provider.APIFormat {
	case llmAPIFormatAnthropic:
		httpReq.Header.Set("x-api-key", provider.APIToken)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	case llmAPIFormatOpenAIChat, llmAPIFormatOpenAIResponses:
		httpReq.Header.Set("Authorization", "Bearer "+provider.APIToken)
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	clientCopy.CheckRedirect = disableLLMRedirect
	client = &clientCopy
	resp, err := client.Do(httpReq)
	if err != nil {
		code := classifyLLMHTTPError(ctx, err)
		return LLMCallResult{ErrorCode: code, ErrorMessage: sanitizeLLMError(err.Error(), provider.APIToken)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseBodySize+1))
	if err != nil {
		return LLMCallResult{ErrorCode: "network_error", ErrorMessage: sanitizeLLMError(err.Error(), provider.APIToken)}
	}
	if len(body) > maxLLMResponseBodySize {
		return LLMCallResult{ErrorCode: "invalid_response", ErrorMessage: "LLM response exceeds 262144 bytes"}
	}

	if resp.StatusCode >= 300 {
		code := "upstream_http_error"
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			code = "invalid_credentials"
		}
		message := fmt.Sprintf("HTTP %d", resp.StatusCode)
		return LLMCallResult{ErrorCode: code, ErrorMessage: sanitizeLLMError(message, provider.APIToken)}
	}

	text, err := extractLLMText(provider.APIFormat, body)
	if err != nil {
		return LLMCallResult{ErrorCode: "invalid_response", ErrorMessage: sanitizeLLMError(err.Error(), provider.APIToken)}
	}
	return LLMCallResult{Text: text}
}

func buildLLMRequest(provider LLMProvider, model, systemPrompt, userMessage string) (string, []byte, error) {
	base := strings.TrimRight(provider.APIURL, "/")
	if base == "" {
		return "", nil, fmt.Errorf("api_url is required")
	}

	var endpoint string
	var body any
	switch provider.APIFormat {
	case llmAPIFormatAnthropic:
		endpoint = llmEndpoint(base, "/messages")
		body = map[string]any{
			"model":      model,
			"max_tokens": 4096,
			"system":     systemPrompt,
			"messages": []map[string]string{
				{"role": "user", "content": userMessage},
			},
		}
	case llmAPIFormatOpenAIChat:
		endpoint = llmEndpoint(base, "/chat/completions")
		body = map[string]any{
			"model": model,
			"messages": []map[string]string{
				{"role": "system", "content": systemPrompt},
				{"role": "user", "content": userMessage},
			},
		}
	case llmAPIFormatOpenAIResponses:
		endpoint = llmEndpoint(base, "/responses")
		body = map[string]any{
			"model":        model,
			"instructions": systemPrompt,
			"input":        userMessage,
		}
	default:
		return "", nil, fmt.Errorf("unsupported api_format %q for LLM call", provider.APIFormat)
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", nil, err
	}
	return endpoint, bodyBytes, nil
}

func llmEndpoint(base, suffix string) string {
	if strings.HasSuffix(base, "/v1") {
		return base + suffix
	}
	return base + "/v1" + suffix
}

func extractLLMText(format string, body []byte) (string, error) {
	switch format {
	case llmAPIFormatAnthropic:
		var resp struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", err
		}
		if len(resp.Content) == 0 || resp.Content[0].Type != "text" || strings.TrimSpace(resp.Content[0].Text) == "" {
			return "", fmt.Errorf("anthropic response did not include content[0].text")
		}
		return resp.Content[0].Text, nil
	case llmAPIFormatOpenAIChat:
		return extractChatText(body)
	case llmAPIFormatOpenAIResponses:
		if text, err := extractResponsesText(body); err == nil {
			return text, nil
		}
		return extractChatText(body)
	default:
		return "", fmt.Errorf("unsupported api_format %q", format)
	}
}

func extractChatText(body []byte) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("openai_chat response did not include choices[0].message.content")
	}
	return resp.Choices[0].Message.Content, nil
}

func extractResponsesText(body []byte) (string, error) {
	var resp struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.OutputText) != "" {
		return resp.OutputText, nil
	}
	for _, output := range resp.Output {
		for _, content := range output.Content {
			if (content.Type == "output_text" || content.Type == "message") && strings.TrimSpace(content.Text) != "" {
				return content.Text, nil
			}
		}
	}
	return "", fmt.Errorf("openai_responses response did not include output_text")
}

func classifyLLMHTTPError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
		return "request_timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "request_timeout"
	}
	return classifyHTTPError(err)
}

func disableLLMRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func isInternalHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isInternalIP(ip)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return true
	}
	for _, ip := range ips {
		if isInternalIP(ip) {
			return true
		}
	}
	return false
}

func isInternalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func sanitizeLLMError(msg, token string) string {
	return sanitizeError(msg, map[string]string{"api_token": token})
}
