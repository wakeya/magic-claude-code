package providerquota

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// TokenPlanAdapter handles quota queries for time-window plans.
type TokenPlanAdapter struct {
	HTTPClient *http.Client
}

// NewTokenPlanAdapter creates a TokenPlanAdapter with the given timeout.
func NewTokenPlanAdapter(timeout time.Duration) *TokenPlanAdapter {
	return &TokenPlanAdapter{
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

// DetectTokenPlanProvider identifies the token plan provider from an API URL.
// Returns provider key and whether the URL is a Xiaomi MiMo URL (unsupported).
func DetectTokenPlanProvider(apiURL string) (provider string, isMiMo bool) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())

	// Xiaomi MiMo check first.
	if strings.Contains(host, "xiaomimimo.com") {
		return "", true
	}

	switch {
	case strings.Contains(host, "api.kimi.com"):
		return "kimi", false
	case strings.Contains(host, "bigmodel.cn"):
		return "zhipu_cn", false
	case strings.Contains(host, "api.z.ai"):
		return "zhipu_en", false
	case strings.Contains(host, "api.minimaxi.com"):
		return "minimax_cn", false
	case strings.Contains(host, "api.minimax.io"):
		return "minimax_en", false
	case strings.Contains(host, "zenmux"):
		return "zenmux", false
	case strings.Contains(host, "volces.com"):
		return "volcengine", false
	default:
		return "", false
	}
}

// Query dispatches to the appropriate provider-specific query.
func (a *TokenPlanAdapter) Query(ctx context.Context, provider string, cfg *ProviderQuotaConfig, apiToken string) *ProviderQuotaResult {
	start := time.Now()

	switch provider {
	case "kimi":
		return a.queryKimi(ctx, apiToken, start)
	case "zhipu_cn":
		return a.queryZhipu(ctx, "https://open.bigmodel.cn", apiToken, start)
	case "zhipu_en":
		return a.queryZhipu(ctx, "https://api.z.ai", apiToken, start)
	case "minimax_cn":
		return a.queryMiniMax(ctx, "https://api.minimaxi.com", apiToken, start)
	case "minimax_en":
		return a.queryMiniMax(ctx, "https://api.minimax.io", apiToken, start)
	case "zenmux":
		return a.queryZenMux(ctx, cfg, apiToken, start)
	case "volcengine":
		return a.queryVolcengine(ctx, cfg, apiToken, start)
	default:
		return errorResult("unsupported_provider", "unknown token plan provider: "+provider, start)
	}
}

// --- Kimi ---

type kimiResponse struct {
	Limits []struct {
		Name   string `json:"name"`
		Detail struct {
			Limit     int64  `json:"limit"`
			Remaining int64  `json:"remaining"`
			ResetTime int64  `json:"resetTime"`
		} `json:"detail"`
	} `json:"limits"`
	Usage []struct {
		Name     string  `json:"name"`
		Used     float64 `json:"used"`
		Limit    float64 `json:"limit"`
		ResetAt  int64   `json:"resetAt"`
	} `json:"usage"`
}

func (a *TokenPlanAdapter) queryKimi(ctx context.Context, apiToken string, start time.Time) *ProviderQuotaResult {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.kimi.com/coding/v1/usages", nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error()), start)
	}
	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp kimiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error()), start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	// Process limits -> 5h tiers.
	for _, lim := range resp.Limits {
		if lim.Detail.Limit <= 0 {
			continue
		}
		used := float64(lim.Detail.Limit-lim.Detail.Remaining) / float64(lim.Detail.Limit) * 100
		usedF := float64(lim.Detail.Limit - lim.Detail.Remaining)
		totalF := float64(lim.Detail.Limit)
		remainingF := float64(lim.Detail.Remaining)
		resetAt := time.Unix(lim.Detail.ResetTime, 0).UTC()
		result.Tiers = append(result.Tiers, QuotaTier{
			Name:        WindowFiveHour,
			Label:       lim.Name,
			Utilization: used,
			ResetsAt:    &resetAt,
			Used:        &usedF,
			Total:       &totalF,
			Remaining:   &remainingF,
			Unit:        "tokens",
		})
	}

	// Process usage -> 7d tiers.
	for _, u := range resp.Usage {
		if u.Limit <= 0 {
			continue
		}
		used := u.Used / u.Limit * 100
		totalF := u.Limit
		usedF := u.Used
		resetAt := time.Unix(u.ResetAt, 0).UTC()
		result.Tiers = append(result.Tiers, QuotaTier{
			Name:        WindowSevenDay,
			Label:       u.Name,
			Utilization: used,
			ResetsAt:    &resetAt,
			Used:        &usedF,
			Total:       &totalF,
			Unit:        "tokens",
		})
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error()), start)
	}
	return result
}

// --- Zhipu (CN and EN) ---

type zhipuResponse struct {
	Data struct {
		TokenLimits []struct {
			Unit       int     `json:"unit"`
			Total      int64   `json:"total"`
			Used       int64   `json:"used"`
			Percentage float64 `json:"percentage"`
		} `json:"token_limits"`
	} `json:"data"`
}

func (a *TokenPlanAdapter) queryZhipu(ctx context.Context, baseHost string, apiToken string, start time.Time) *ProviderQuotaResult {
	endpoint := baseHost + "/api/monitor/usage/quota/limit"
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("Authorization", apiToken)
	req.Header.Set("Accept", "application/json")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error()), start)
	}
	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp zhipuResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error()), start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	for _, tl := range resp.Data.TokenLimits {
		window := WindowFiveHour
		if tl.Unit == 6 {
			window = WindowSevenDay
		}
		usedF := float64(tl.Used)
		totalF := float64(tl.Total)
		result.Tiers = append(result.Tiers, QuotaTier{
			Name:        window,
			Utilization: tl.Percentage,
			Used:        &usedF,
			Total:       &totalF,
		})
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error()), start)
	}
	return result
}

// --- MiniMax (CN and EN) ---

type miniMaxResponse struct {
	Data struct {
		ModelName                   string  `json:"model_name"`
		CurrentIntervalRemainingPct float64 `json:"current_interval_remaining_percent"`
		CurrentWeeklyStatus         int     `json:"current_weekly_status"`
		WeeklyRemainingPct          float64 `json:"current_weekly_remaining_percent"`
	} `json:"data"`
}

func (a *TokenPlanAdapter) queryMiniMax(ctx context.Context, baseHost string, apiToken string, start time.Time) *ProviderQuotaResult {
	endpoint := baseHost + "/v1/api/openplatform/coding_plan/remains"
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error()), start)
	}
	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp miniMaxResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error()), start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	// 5-hour tier: invert remaining to get used.
	used5h := 100 - resp.Data.CurrentIntervalRemainingPct
	result.Tiers = append(result.Tiers, QuotaTier{
		Name:        WindowFiveHour,
		Label:       resp.Data.ModelName,
		Utilization: used5h,
	})

	// Weekly tier only if status=1.
	if resp.Data.CurrentWeeklyStatus == 1 {
		used7d := 100 - resp.Data.WeeklyRemainingPct
		result.Tiers = append(result.Tiers, QuotaTier{
			Name:        WindowSevenDay,
			Label:       resp.Data.ModelName,
			Utilization: used7d,
		})
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error()), start)
	}
	return result
}

// --- ZenMux ---

type zenMuxResponse struct {
	Data struct {
		Quota5Hour struct {
			UsagePercentage float64 `json:"usage_percentage"`
			Used            float64 `json:"used"`
			Limit           float64 `json:"limit"`
		} `json:"quota_5_hour"`
		Quota7Day struct {
			UsagePercentage float64 `json:"usage_percentage"`
			Used            float64 `json:"used"`
			Limit           float64 `json:"limit"`
		} `json:"quota_7_day"`
	} `json:"data"`
}

func (a *TokenPlanAdapter) queryZenMux(ctx context.Context, cfg *ProviderQuotaConfig, apiToken string, start time.Time) *ProviderQuotaResult {
	if cfg == nil || cfg.BaseURL == "" {
		return errorResult("missing_credentials", "ZenMux requires a quota URL", start)
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", cfg.BaseURL, nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error()), start)
	}
	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp zenMuxResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error()), start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	if resp.Data.Quota5Hour.Limit > 0 {
		used5h := resp.Data.Quota5Hour.UsagePercentage * 100
		usedF := resp.Data.Quota5Hour.Used
		totalF := resp.Data.Quota5Hour.Limit
		result.Tiers = append(result.Tiers, QuotaTier{
			Name:        WindowFiveHour,
			Utilization: used5h,
			Used:        &usedF,
			Total:       &totalF,
			Unit:        "USD",
		})
	}
	if resp.Data.Quota7Day.Limit > 0 {
		used7d := resp.Data.Quota7Day.UsagePercentage * 100
		usedF := resp.Data.Quota7Day.Used
		totalF := resp.Data.Quota7Day.Limit
		result.Tiers = append(result.Tiers, QuotaTier{
			Name:        WindowSevenDay,
			Utilization: used7d,
			Used:        &usedF,
			Total:       &totalF,
			Unit:        "USD",
		})
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error()), start)
	}
	return result
}

// --- Volcengine ---

func (a *TokenPlanAdapter) queryVolcengine(ctx context.Context, cfg *ProviderQuotaConfig, apiToken string, start time.Time) *ProviderQuotaResult {
	if cfg == nil || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return errorResult("missing_credentials", "Volcengine requires AK/SK", start)
	}

	// Try GetAFPUsage first, then GetCodingPlanUsage.
	result := a.queryVolcengineAPI(ctx, "GetAFPUsage", cfg, start)
	if result.Success || result.ErrorCode != "upstream_business_error" {
		return result
	}
	return a.queryVolcengineAPI(ctx, "GetCodingPlanUsage", cfg, start)
}

func (a *TokenPlanAdapter) queryVolcengineAPI(ctx context.Context, action string, cfg *ProviderQuotaConfig, start time.Time) *ProviderQuotaResult {
	query := map[string]string{
		"Action":    action,
		"Version":   "2024-01-01",
		"RegionId":  "cn-north-1",
		"AccessKeyId": cfg.AccessKeyID,
	}

	signedQuery := SignVolcengineRequest(query, cfg.SecretAccessKey, time.Now())
	endpoint := "https://open.volcengineapi.com/?" + signedQuery

	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("Accept", "application/json")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error()), start)
	}

	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	return parseVolcengineResponse(body, start)
}

func parseVolcengineResponse(body []byte, start time.Time) *ProviderQuotaResult {
	var resp struct {
		Result struct {
			Subscriptions []struct {
				Product   string  `json:"Product"`
				TotalQuota float64 `json:"TotalQuota"`
				UsedQuota  float64 `json:"UsedQuota"`
				Window     string  `json:"Window"`
			} `json:"Subscriptions"`
		} `json:"Result"`
		ResponseMetadata struct {
			Error struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error()), start)
	}

	if resp.ResponseMetadata.Error.Code != "" {
		return errorResult("upstream_business_error",
			resp.ResponseMetadata.Error.Code+": "+resp.ResponseMetadata.Error.Message, start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	for _, sub := range resp.Result.Subscriptions {
		if sub.TotalQuota <= 0 {
			continue
		}
		usedPct := sub.UsedQuota / sub.TotalQuota * 100
		window := NormalizeWindow(sub.Window)
		if window == "" {
			window = WindowMonthly
		}
		result.Tiers = append(result.Tiers, QuotaTier{
			Name:        window,
			Label:       sub.Product,
			Utilization: usedPct,
			Used:        &sub.UsedQuota,
			Total:       &sub.TotalQuota,
		})
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error()), start)
	}
	return result
}

// --- Volcengine Signing ---

// SignVolcengineRequest produces a signed query string for the Volcengine API.
// It uses HMAC-SHA256 with the specified region, service, and date.
func SignVolcengineRequest(query map[string]string, secretKey string, now time.Time) string {
	// Add standard parameters.
	dateStamp := now.UTC().Format("20060102")
	amzDate := now.UTC().Format("20060102T150405Z")

	query["Timestamp"] = amzDate
	query["DateFormat"] = "ISO8601"

	// Sort query parameters.
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(query[k]))
	}
	canonicalQuery := strings.Join(parts, "&")

	// Canonical request.
	service := "open"
	region := "cn-north-1"
	credentialScope := dateStamp + "/" + region + "/" + service + "/request"

	canonicalRequest := "GET\n/\n" + canonicalQuery + "\nhost:open.volcengineapi.com\n\nhost\n"
	hashedRequest := sha256Hex(canonicalRequest)

	stringToSign := "HMAC-SHA256\n" + amzDate + "\n" + credentialScope + "\n" + hashedRequest

	// HMAC-SHA256 signing.
	kDate := hmacSHA256([]byte(secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	return canonicalQuery + "&X-Date=" + amzDate + "&X-Algorithm=HMAC-SHA256&X-Credential=" + url.QueryEscape(query["AccessKeyId"]+"/"+credentialScope) + "&X-SignedHeaders=host&X-Signature=" + signature
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// doRequest is a helper that performs an HTTP request and returns the body.
func (a *TokenPlanAdapter) doRequest(req *http.Request) ([]byte, int, error) {
	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
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
