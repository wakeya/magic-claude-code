package providerquota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BalanceAdapter detects and queries official balance APIs.
type BalanceAdapter struct {
	HTTPClient *http.Client
	// EndpointOverride, if non-nil, replaces the default endpoint lookup.
	// Used for testing.
	EndpointOverride func(provider string) string
}

// NewBalanceAdapter creates a BalanceAdapter with the given timeout.
func NewBalanceAdapter(timeout time.Duration) *BalanceAdapter {
	return &BalanceAdapter{
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

// endpoint returns the URL for the given provider.
func (a *BalanceAdapter) endpoint(provider string) string {
	if a.EndpointOverride != nil {
		return a.EndpointOverride(provider)
	}
	return balanceEndpoint(provider)
}

// DetectBalanceProvider returns the balance provider key for the given API URL host,
// or empty string if no match.
func DetectBalanceProvider(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "api.deepseek.com":
		return "deepseek"
	case host == "api.stepfun.com":
		return "stepfun"
	case host == "api.siliconflow.cn":
		return "siliconflow_cn"
	case host == "api.siliconflow.com":
		return "siliconflow_en"
	case host == "openrouter.ai":
		return "openrouter"
	case host == "api.novita.ai":
		return "novita"
	default:
		return ""
	}
}

// balanceEndpoint returns the fixed endpoint for a balance provider.
func balanceEndpoint(provider string) string {
	switch provider {
	case "deepseek":
		return "https://api.deepseek.com/user/balance"
	case "stepfun":
		return "https://api.stepfun.com/v1/accounts"
	case "siliconflow_cn":
		return "https://api.siliconflow.cn/v1/user/info"
	case "siliconflow_en":
		return "https://api.siliconflow.com/v1/user/info"
	case "openrouter":
		return "https://openrouter.ai/api/v1/credits"
	case "novita":
		return "https://api.novita.ai/v3/user/balance"
	default:
		return ""
	}
}

// Query queries the balance API for the detected provider.
func (a *BalanceAdapter) Query(ctx context.Context, provider string, apiToken string) *ProviderQuotaResult {
	start := time.Now()

	endpoint := a.endpoint(provider)
	if endpoint == "" {
		return errorResult("unsupported_provider", "unknown balance provider: "+provider, start)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return errorResult("internal_error", sanitizeError(err.Error()), start)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		ec := classifyHTTPError(err)
		return errorResult(ec, sanitizeError(err.Error()), start)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize+1))
	if err != nil {
		return errorResult("network_error", sanitizeError(err.Error()), start)
	}
	if len(body) > maxResponseBodySize {
		return errorResult("response_too_large", "response exceeds limit", start)
	}

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", resp.StatusCode), start)
	}
	if resp.StatusCode >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", resp.StatusCode), start)
	}

	// Parse based on provider.
	var result *ProviderQuotaResult
	switch provider {
	case "deepseek":
		result, err = parseDeepSeekBalance(body, start)
	case "stepfun":
		result, err = parseStepFunBalance(body, start)
	case "siliconflow_cn":
		result, err = parseSiliconFlowBalance(body, "CNY", start)
	case "siliconflow_en":
		result, err = parseSiliconFlowBalance(body, "USD", start)
	case "openrouter":
		result, err = parseOpenRouterBalance(body, start)
	case "novita":
		result, err = parseNovitaBalance(body, start)
	default:
		return errorResult("unsupported_provider", "no parser for "+provider, start)
	}

	if err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error()), start)
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error()), start)
	}
	return result
}

// --- DeepSeek ---

type deepSeekResponse struct {
	BalanceInfos []struct {
		Currency      string  `json:"currency"`
		TotalBalance  float64 `json:"total_balance"`
		GrantedBalance float64 `json:"granted_balance"`
		ToppedUpBalance float64 `json:"topped_up_balance"`
	} `json:"balance_infos"`
	IsAvailable bool `json:"is_available"`
}

func parseDeepSeekBalance(body []byte, start time.Time) (*ProviderQuotaResult, error) {
	var resp deepSeekResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse DeepSeek response: %w", err)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	isValid := resp.IsAvailable
	for _, info := range resp.BalanceInfos {
		total := info.TotalBalance
		bal := BalanceItem{
			Total:   &total,
			Unit:    info.Currency,
			IsValid: &isValid,
		}
		if info.GrantedBalance > 0 || info.ToppedUpBalance > 0 {
			used := info.GrantedBalance + info.ToppedUpBalance - info.TotalBalance
			if used >= 0 {
				bal.Used = &used
			}
			remaining := info.TotalBalance
			bal.Remaining = &remaining
		} else {
			remaining := info.TotalBalance
			bal.Remaining = &remaining
		}
		result.Balances = append(result.Balances, bal)
	}

	if len(result.Balances) == 0 {
		// DeepSeek sometimes returns empty balance_infos; treat as zero balance.
		zero := 0.0
		result.Balances = append(result.Balances, BalanceItem{
			Remaining: &zero,
			Unit:      "CNY",
			IsValid:   &isValid,
		})
	}

	return result, nil
}

// --- StepFun ---

type stepFunResponse struct {
	Balance float64 `json:"balance"`
}

func parseStepFunBalance(body []byte, start time.Time) (*ProviderQuotaResult, error) {
	var resp stepFunResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse StepFun response: %w", err)
	}

	remaining := resp.Balance
	return &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
		Balances: []BalanceItem{
			{Remaining: &remaining, Unit: "CNY"},
		},
	}, nil
}

// --- SiliconFlow ---

type siliconFlowResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		TotalBalance float64 `json:"totalBalance"`
	} `json:"data"`
}

func parseSiliconFlowBalance(body []byte, unit string, start time.Time) (*ProviderQuotaResult, error) {
	var resp siliconFlowResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse SiliconFlow response: %w", err)
	}
	if resp.Code != 0 && resp.Code != 200 {
		return nil, fmt.Errorf("SiliconFlow error: code=%d msg=%s", resp.Code, resp.Msg)
	}

	total := resp.Data.TotalBalance
	return &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
		Balances: []BalanceItem{
			{Total: &total, Remaining: &total, Unit: unit},
		},
	}, nil
}

// --- OpenRouter ---

type openRouterResponse struct {
	Data struct {
		TotalCredits float64 `json:"total_credits"`
		TotalUsage   float64 `json:"total_usage"`
	} `json:"data"`
}

func parseOpenRouterBalance(body []byte, start time.Time) (*ProviderQuotaResult, error) {
	var resp openRouterResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse OpenRouter response: %w", err)
	}

	total := resp.Data.TotalCredits
	used := resp.Data.TotalUsage
	remaining := total - used
	return &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
		Balances: []BalanceItem{
			{Total: &total, Used: &used, Remaining: &remaining, Unit: "USD"},
		},
	}, nil
}

// --- Novita AI ---

type novitaResponse struct {
	Data struct {
		AvailableBalance float64 `json:"availableBalance"`
	} `json:"data"`
}

func parseNovitaBalance(body []byte, start time.Time) (*ProviderQuotaResult, error) {
	var resp novitaResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse Novita response: %w", err)
	}

	// Novita returns balance in 1/10000 units; convert to USD.
	remaining := resp.Data.AvailableBalance / 10000
	return &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
		Balances: []BalanceItem{
			{Remaining: &remaining, Unit: "USD"},
		},
	}, nil
}

// errorResult creates a ProviderQuotaResult with an error.
func errorResult(code, msg string, start time.Time) *ProviderQuotaResult {
	return &ProviderQuotaResult{
		Success:      false,
		ErrorCode:    code,
		ErrorMessage: msg,
		QueriedAt:    time.Now(),
		DurationMS:   time.Since(start).Milliseconds(),
	}
}
