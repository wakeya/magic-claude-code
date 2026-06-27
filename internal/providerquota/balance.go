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
	EndpointOverride func(provider string) string
}

// NewBalanceAdapter creates a BalanceAdapter with the given timeout.
func NewBalanceAdapter(timeout time.Duration) *BalanceAdapter {
	return &BalanceAdapter{
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

// DetectBalanceProvider returns the balance provider key for the given API URL host.
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

func (a *BalanceAdapter) endpoint(provider string) string {
	if a.EndpointOverride != nil {
		return a.EndpointOverride(provider)
	}
	return balanceEndpoint(provider)
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
		return errorResult("internal_error", sanitizeError(err.Error(), nil), start)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		ec := classifyHTTPError(err)
		return errorResult(ec, sanitizeError(err.Error(), nil), start)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize+1))
	if err != nil {
		return errorResult("network_error", sanitizeError(err.Error(), nil), start)
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
		return errorResult("invalid_response", sanitizeError(err.Error(), nil), start)
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error(), nil), start)
	}
	return result
}

// --- DeepSeek ---
// Response: { "is_available": bool, "balance_infos": [{ "currency", "total_balance" }] }

func parseDeepSeekBalance(body []byte, start time.Time) (*ProviderQuotaResult, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse DeepSeek: %w", err)
	}

	isAvailable := true // default true per reference
	if v, ok := raw["is_available"]; ok {
		json.Unmarshal(v, &isAvailable)
	}

	var infos []map[string]any
	if v, ok := raw["balance_infos"]; ok {
		if err := json.Unmarshal(v, &infos); err != nil {
			return nil, fmt.Errorf("parse balance_infos: %w", err)
		}
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	for _, info := range infos {
		currency, _ := info["currency"].(string)
		if currency == "" {
			currency = "CNY"
		}
		total := toFloat64FromAny(info["total_balance"])
		bal := BalanceItem{
			PlanName:  currency,
			Remaining: &total,
			Unit:      currency,
		}
		if !isAvailable {
			msg := "Insufficient balance"
			bal.IsValid = &isAvailable
			bal.InvalidMessage = msg
		}
		result.Balances = append(result.Balances, bal)
	}

	if len(result.Balances) == 0 {
		zero := 0.0
		bal := BalanceItem{Remaining: &zero, Unit: "CNY"}
		if !isAvailable {
			bal.IsValid = &isAvailable
			bal.InvalidMessage = "Insufficient balance"
		}
		result.Balances = append(result.Balances, bal)
	}

	return result, nil
}

// --- StepFun ---
// Response: { "balance": float|string }

func parseStepFunBalance(body []byte, start time.Time) (*ProviderQuotaResult, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse StepFun: %w", err)
	}

	balance := toFloat64FromAny(raw["balance"])
	return &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
		Balances: []BalanceItem{
			{Remaining: &balance, Unit: "CNY"},
		},
	}, nil
}

// --- SiliconFlow ---
// Response: { "data": { "totalBalance": float|string } }

func parseSiliconFlowBalance(body []byte, unit string, start time.Time) (*ProviderQuotaResult, error) {
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			TotalBalance json.Number `json:"totalBalance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse SiliconFlow: %w", err)
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("SiliconFlow: missing data field")
	}

	total, _ := resp.Data.TotalBalance.Float64()
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
// Response: { "data": { "total_credits": float, "total_usage": float } }

func parseOpenRouterBalance(body []byte, start time.Time) (*ProviderQuotaResult, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse OpenRouter: %w", err)
	}

	// Defensive: try data wrapper, fallback to root.
	dataRaw := raw
	if d, ok := raw["data"].(map[string]any); ok {
		dataRaw = d
	}

	total := toFloat64FromAny(dataRaw["total_credits"])
	used := toFloat64FromAny(dataRaw["total_usage"])
	remaining := total - used

	bal := BalanceItem{
		Total:     &total,
		Used:      &used,
		Remaining: &remaining,
		Unit:      "USD",
	}
	if remaining <= 0 {
		msg := "No credits remaining"
		f := false
		bal.IsValid = &f
		bal.InvalidMessage = msg
	}

	return &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
		Balances:  []BalanceItem{bal},
	}, nil
}

// --- Novita AI ---
// Response: { "availableBalance": float|string } (top-level, NOT in data)
// Value is in 0.0001 USD units; divide by 10000.

func parseNovitaBalance(body []byte, start time.Time) (*ProviderQuotaResult, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse Novita: %w", err)
	}

	// availableBalance is TOP-LEVEL, not under "data".
	rawBalance := toFloat64FromAny(raw["availableBalance"])
	remaining := rawBalance / 10000.0

	bal := BalanceItem{
		Remaining: &remaining,
		Unit:      "USD",
	}
	if remaining <= 0 {
		msg := "No balance remaining"
		f := false
		bal.IsValid = &f
		bal.InvalidMessage = msg
	}

	return &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
		Balances:  []BalanceItem{bal},
	}, nil
}

// toFloat64FromAny converts various numeric types to float64.
func toFloat64FromAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
