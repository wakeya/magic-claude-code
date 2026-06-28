package providerquota

import (
	"bytes"
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
// cardAPIURL is the provider card's API URL; it is used to derive the
// Volcengine region and ignored by providers with fixed endpoints (Kimi,
// Zhipu, MiniMax). ZenMux uses cfg.BaseURL as its dedicated quota endpoint.
// apiToken is the Bearer credential for kimi/zhipu/minimax/zenmux; it is
// intentionally unused by volcengine (which signs with cfg AK/SK) and is
// passed empty by resolveQueryPlan for that provider.
func (a *TokenPlanAdapter) Query(ctx context.Context, provider string, cfg *ProviderQuotaConfig, cardAPIURL, apiToken string) *ProviderQuotaResult {
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
		return a.queryVolcengine(ctx, cfg, cardAPIURL, apiToken, start)
	default:
		return errorResult("unsupported_provider", "unknown token plan provider: "+provider, start)
	}
}

// --- Kimi ---
// Response: { "limits": [{ "detail": { "limit", "remaining", "resetTime" } }], "usage": { "limit", "remaining", "resetTime" } }
// NOTE: usage is an OBJECT, not an array.

func (a *TokenPlanAdapter) queryKimi(ctx context.Context, apiToken string, start time.Time) *ProviderQuotaResult {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.kimi.com/coding/v1/usages", nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error(), nil), start)
	}
	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp struct {
		Limits []struct {
			Name   string `json:"name"`
			Detail struct {
				Limit     json.Number `json:"limit"`
				Remaining json.Number `json:"remaining"`
				ResetTime json.Number `json:"resetTime"`
			} `json:"detail"`
		} `json:"limits"`
		Usage struct {
			Limit     json.Number `json:"limit"`
			Remaining json.Number `json:"remaining"`
			ResetTime json.Number `json:"resetTime"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error(), nil), start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	// limits[] -> five_hour tiers
	for _, lim := range resp.Limits {
		limit, _ := lim.Detail.Limit.Float64()
		remaining, _ := lim.Detail.Remaining.Float64()
		if limit <= 0 {
			continue
		}
		used := (limit - remaining)
		if used < 0 {
			used = 0
		}
		utilization := used / limit * 100
		totalF := limit
		usedF := used
		remainingF := remaining
		resetAt := parseKimiResetTime(lim.Detail.ResetTime)
		tier := QuotaTier{
			Name:        WindowFiveHour,
			Label:       lim.Name,
			Utilization: utilization,
			Used:        &usedF,
			Total:       &totalF,
			Remaining:   &remainingF,
			Unit:        "tokens",
		}
		if !resetAt.IsZero() {
			tier.ResetsAt = &resetAt
		}
		result.Tiers = append(result.Tiers, tier)
	}

	// usage object -> weekly_limit tier
	usageLimit, _ := resp.Usage.Limit.Float64()
	usageRemaining, _ := resp.Usage.Remaining.Float64()
	if usageLimit > 0 {
		usageUsed := usageLimit - usageRemaining
		if usageUsed < 0 {
			usageUsed = 0
		}
		utilization := usageUsed / usageLimit * 100
		totalF := usageLimit
		usedF := usageUsed
		resetAt := parseKimiResetTime(resp.Usage.ResetTime)
		tier := QuotaTier{
			Name:        WindowSevenDay,
			Label:       "usage",
			Utilization: utilization,
			Used:        &usedF,
			Total:       &totalF,
			Unit:        "tokens",
		}
		if !resetAt.IsZero() {
			tier.ResetsAt = &resetAt
		}
		result.Tiers = append(result.Tiers, tier)
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error(), nil), start)
	}
	return result
}

func parseKimiResetTime(v json.Number) time.Time {
	s := v.String()
	if s == "" || s == "null" {
		return time.Time{}
	}
	// Try as number (unix timestamp).
	if f, err := v.Float64(); err == nil {
		if f > 1e12 {
			return time.UnixMilli(int64(f)).UTC()
		}
		return time.Unix(int64(f), 0).UTC()
	}
	// Try as ISO string.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// --- Zhipu (CN and EN) ---
// Response: { "success": bool, "msg": "", "data": { "level": "", "limits": [{ "type": "TOKENS_LIMIT", "percentage", "nextResetTime", "unit" }] } }
// Authorization: raw key, NO "Bearer " prefix.

func (a *TokenPlanAdapter) queryZhipu(ctx context.Context, baseHost string, apiToken string, start time.Time) *ProviderQuotaResult {
	endpoint := baseHost + "/api/monitor/usage/quota/limit"
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("Authorization", apiToken) // NO "Bearer " prefix per reference
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en-US,en")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error(), nil), start)
	}
	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
		Data    struct {
			Level  string `json:"level"`
			Limits []struct {
				Type         string      `json:"type"`
				Percentage   float64     `json:"percentage"`
				NextResetTime json.Number `json:"nextResetTime"`
				Unit         int         `json:"unit"`
				Number       json.Number `json:"number"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error(), nil), start)
	}

	if !resp.Success {
		return errorResult("upstream_business_error", resp.Msg, start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	// Collect TOKENS_LIMIT entries. Classify each into a five_hour or
	// seven_day slot. When unit is explicit (3/6) use it; when unit is
	// missing, fall back to reset-time presence: entries without nextResetTime
	// are the rolling 5-hour window, entries with a reset are the weekly one.
	// At most one tier per window slot.
	type candidate struct {
		pct       float64
		resetTime time.Time
		hasReset  bool
	}
	var fiveHour, sevenDay *candidate
	var unitless []candidate
	for _, lim := range resp.Data.Limits {
		if !strings.EqualFold(lim.Type, "TOKENS_LIMIT") {
			continue
		}
		c := candidate{pct: lim.Percentage}
		if rt, err := lim.NextResetTime.Float64(); err == nil && rt > 0 {
			c.resetTime = time.UnixMilli(int64(rt)).UTC()
			c.hasReset = true
		}
		switch lim.Unit {
		case 3:
			fiveHour = &c
		case 6:
			sevenDay = &c
		default:
			unitless = append(unitless, c)
		}
	}

	// Fill empty slots from unit-less candidates, using the reference reset-time
	// ordering: sort so entries without a reset come first, then by reset time
	// ascending. The first fills five_hour, the second fills seven_day. This
	// keeps the 5-hour bucket even when every unit-less entry has a reset time.
	sort.SliceStable(unitless, func(i, j int) bool {
		ri, rj := unitless[i], unitless[j]
		// Entries without a reset rank before those with one.
		if ri.hasReset != rj.hasReset {
			return !ri.hasReset
		}
		return ri.resetTime.Before(rj.resetTime)
	})
	for _, c := range unitless {
		cc := c
		if fiveHour == nil {
			fiveHour = &cc
		} else if sevenDay == nil {
			sevenDay = &cc
		}
	}

	addTier := func(window string, c *candidate) {
		if c == nil {
			return
		}
		tier := QuotaTier{Name: window, Utilization: c.pct}
		if c.hasReset {
			t := c.resetTime
			tier.ResetsAt = &t
		}
		result.Tiers = append(result.Tiers, tier)
	}
	addTier(WindowFiveHour, fiveHour)
	addTier(WindowSevenDay, sevenDay)

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error(), nil), start)
	}
	return result
}

// --- MiniMax (CN and EN) ---
// Response: { "base_resp": { "status_code": 0 }, "model_remains": [{ "model_name": "general", "current_interval_remaining_percent", "end_time", "current_weekly_status", "current_weekly_remaining_percent", "weekly_end_time" }] }

func (a *TokenPlanAdapter) queryMiniMax(ctx context.Context, baseHost string, apiToken string, start time.Time) *ProviderQuotaResult {
	endpoint := baseHost + "/v1/api/openplatform/coding_plan/remains"
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error(), nil), start)
	}
	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp struct {
		BaseResp struct {
			StatusCode int64  `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
		ModelRemains []struct {
			ModelName                      string      `json:"model_name"`
			CurrentIntervalRemainingPct    float64     `json:"current_interval_remaining_percent"`
			EndTime                        json.Number `json:"end_time"`
			CurrentWeeklyStatus            int         `json:"current_weekly_status"`
			CurrentWeeklyRemainingPct      float64     `json:"current_weekly_remaining_percent"`
			WeeklyEndTime                  json.Number `json:"weekly_end_time"`
		} `json:"model_remains"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error(), nil), start)
	}

	if resp.BaseResp.StatusCode != 0 {
		return errorResult("upstream_business_error", resp.BaseResp.StatusMsg, start)
	}

	// Find the "general" model.
	var generalModelName string
	var general5hRemaining, general7dRemaining float64
	var generalEndTime, generalWeeklyEndTime json.Number
	var generalWeeklyStatus int
	found := false

	for _, mr := range resp.ModelRemains {
		if mr.ModelName == "general" {
			generalModelName = mr.ModelName
			general5hRemaining = mr.CurrentIntervalRemainingPct
			generalEndTime = mr.EndTime
			generalWeeklyStatus = mr.CurrentWeeklyStatus
			general7dRemaining = mr.CurrentWeeklyRemainingPct
			generalWeeklyEndTime = mr.WeeklyEndTime
			found = true
			break
		}
	}
	if !found {
		return errorResult("invalid_response", "no general model in response", start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	// 5-hour: invert remaining to get used.
	used5h := 100 - general5hRemaining
	tier5h := QuotaTier{
		Name:        WindowFiveHour,
		Label:       generalModelName,
		Utilization: used5h,
	}
	if et, err := generalEndTime.Float64(); err == nil && et > 0 {
		t := time.UnixMilli(int64(et)).UTC()
		tier5h.ResetsAt = &t
	}
	result.Tiers = append(result.Tiers, tier5h)

	// Weekly only if status == 1.
	if generalWeeklyStatus == 1 {
		used7d := 100 - general7dRemaining
		tier7d := QuotaTier{
			Name:        WindowSevenDay,
			Label:       generalModelName,
			Utilization: used7d,
		}
		if wt, err := generalWeeklyEndTime.Float64(); err == nil && wt > 0 {
			t := time.UnixMilli(int64(wt)).UTC()
			tier7d.ResetsAt = &t
		}
		result.Tiers = append(result.Tiers, tier7d)
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error(), nil), start)
	}
	return result
}

// --- ZenMux ---
// Response: { "success": bool, "message": "", "data": { "quota_5_hour": { "usage_percentage" (0-1), "resets_at", "used_value_usd", "max_value_usd" }, "quota_7_day": {...} } }

func (a *TokenPlanAdapter) queryZenMux(ctx context.Context, cfg *ProviderQuotaConfig, apiToken string, start time.Time) *ProviderQuotaResult {
	if cfg == nil || cfg.BaseURL == "" {
		return errorResult("missing_credentials", "ZenMux requires a quota URL", start)
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", cfg.BaseURL, nil)
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Accept", "application/json")

	body, status, err := a.doRequest(req)
	if err != nil {
		return errorResult(classifyHTTPError(err), sanitizeError(err.Error(), nil), start)
	}
	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}
	if status >= 400 {
		return errorResult("upstream_http_error", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Quota5Hour struct {
				UsagePercentage float64     `json:"usage_percentage"`
				ResetsAt        string      `json:"resets_at"`
				UsedValueUSD    float64     `json:"used_value_usd"`
				MaxValueUSD     float64     `json:"max_value_usd"`
			} `json:"quota_5_hour"`
			Quota7Day struct {
				UsagePercentage float64     `json:"usage_percentage"`
				ResetsAt        string      `json:"resets_at"`
				UsedValueUSD    float64     `json:"used_value_usd"`
				MaxValueUSD     float64     `json:"max_value_usd"`
			} `json:"quota_7_day"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error(), nil), start)
	}

	if !resp.Success {
		return errorResult("upstream_business_error", resp.Message, start)
	}

	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	if resp.Data.Quota5Hour.MaxValueUSD > 0 {
		used5h := resp.Data.Quota5Hour.UsagePercentage * 100 // 0-1 -> 0-100
		usedF := resp.Data.Quota5Hour.UsedValueUSD
		totalF := resp.Data.Quota5Hour.MaxValueUSD
		tier := QuotaTier{
			Name:        WindowFiveHour,
			Utilization: used5h,
			Used:        &usedF,
			Total:       &totalF,
			Unit:        "USD",
		}
		if resp.Data.Quota5Hour.ResetsAt != "" {
			if t, err := time.Parse(time.RFC3339, resp.Data.Quota5Hour.ResetsAt); err == nil {
				tier.ResetsAt = &t
			}
		}
		result.Tiers = append(result.Tiers, tier)
	}

	if resp.Data.Quota7Day.MaxValueUSD > 0 {
		used7d := resp.Data.Quota7Day.UsagePercentage * 100
		usedF := resp.Data.Quota7Day.UsedValueUSD
		totalF := resp.Data.Quota7Day.MaxValueUSD
		tier := QuotaTier{
			Name:        WindowSevenDay,
			Utilization: used7d,
			Used:        &usedF,
			Total:       &totalF,
			Unit:        "USD",
		}
		if resp.Data.Quota7Day.ResetsAt != "" {
			if t, err := time.Parse(time.RFC3339, resp.Data.Quota7Day.ResetsAt); err == nil {
				tier.ResetsAt = &t
			}
		}
		result.Tiers = append(result.Tiers, tier)
	}

	if err := NormalizeResult(result); err != nil {
		return errorResult("invalid_response", sanitizeError(err.Error(), nil), start)
	}
	return result
}

// --- Volcengine ---
// POST to open.volcengineapi.com with V4 signing (service=ark).
// Tries GetAFPUsage first, then GetCodingPlanUsage.

func (a *TokenPlanAdapter) queryVolcengine(ctx context.Context, cfg *ProviderQuotaConfig, cardAPIURL, apiToken string, start time.Time) *ProviderQuotaResult {
	if cfg == nil || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return errorResult("missing_credentials", "Volcengine requires AK/SK", start)
	}

	// Try GetAFPUsage first, then GetCodingPlanUsage.
	result := a.queryVolcengineAPI(ctx, "GetAFPUsage", cfg, cardAPIURL, start)
	if result.Success || result.ErrorCode != "upstream_business_error" {
		return result
	}
	return a.queryVolcengineAPI(ctx, "GetCodingPlanUsage", cfg, cardAPIURL, start)
}

func (a *TokenPlanAdapter) queryVolcengineAPI(ctx context.Context, action string, cfg *ProviderQuotaConfig, cardAPIURL string, start time.Time) *ProviderQuotaResult {
	// Derive region from the provider card URL (e.g. ark.cn-shanghai.volces.com
	// → cn-shanghai). cfg.BaseURL is typically empty for Volcengine.
	region := volcRegionFromBaseURL(cardAPIURL)
	service := "ark"

	query := map[string]string{
		"Action":  action,
		"Version": "2024-01-01",
		"Region":  region,
	}

	signedReq := SignVolcengineRequestV4(query, cfg.AccessKeyID, cfg.SecretAccessKey, service, region, time.Now())
	endpoint := "https://open.volcengineapi.com/?" + signedReq.queryString

	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	req.Header.Set("X-Date", signedReq.xDate)
	req.Header.Set("X-Content-Sha256", signedReq.bodyHash)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", signedReq.authHeader)

	body, status, err := a.doRequest(req)
	if err != nil {
		ec := classifyHTTPError(err)
		if isVolcengineAuthError(err.Error()) {
			ec = "invalid_credentials"
		}
		return errorResult(ec, sanitizeError(err.Error(), nil), start)
	}

	if status == 401 || status == 403 {
		return errorResult("invalid_credentials", fmt.Sprintf("HTTP %d", status), start)
	}

	var resp struct {
		ResponseMetadata struct {
			Error struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Error struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
		Result json.RawMessage `json:"Result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error(), nil), start)
	}

	errCode := resp.ResponseMetadata.Error.Code
	errMsg := resp.ResponseMetadata.Error.Message
	if errCode == "" {
		errCode = resp.Error.Code
		errMsg = resp.Error.Message
	}
	if errCode != "" {
		if isVolcengineAuthError(errCode + " " + errMsg) {
			return errorResult("invalid_credentials", errMsg, start)
		}
		return errorResult("upstream_business_error", errCode+": "+errMsg, start)
	}

	if action == "GetAFPUsage" {
		return parseVolcengineAFP(resp.Result, start)
	}
	return parseVolcengineCodingPlan(resp.Result, start)
}

func parseVolcengineAFP(result json.RawMessage, start time.Time) *ProviderQuotaResult {
	var r struct {
		AFPFiveHour struct {
			Quota     float64 `json:"Quota"`
			Used      float64 `json:"Used"`
			ResetTime int64   `json:"ResetTime"`
		} `json:"AFPFiveHour"`
		AFPWeekly struct {
			Quota     float64 `json:"Quota"`
			Used      float64 `json:"Used"`
			ResetTime int64   `json:"ResetTime"`
		} `json:"AFPWeekly"`
		AFPMonthly struct {
			Quota     float64 `json:"Quota"`
			Used      float64 `json:"Used"`
			ResetTime int64   `json:"ResetTime"`
		} `json:"AFPMonthly"`
		PlanType string `json:"PlanType"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error(), nil), start)
	}

	res := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	addVolcTier := func(name string, quota, used float64, resetTime int64) {
		if quota <= 0 {
			return
		}
		utilization := used / quota * 100
		t := time.UnixMilli(resetTime).UTC()
		res.Tiers = append(res.Tiers, QuotaTier{
			Name:        name,
			Label:       r.PlanType,
			Utilization: utilization,
			Used:        &used,
			Total:       &quota,
			ResetsAt:    &t,
		})
	}

	addVolcTier(WindowFiveHour, r.AFPFiveHour.Quota, r.AFPFiveHour.Used, r.AFPFiveHour.ResetTime)
	addVolcTier(WindowSevenDay, r.AFPWeekly.Quota, r.AFPWeekly.Used, r.AFPWeekly.ResetTime)
	addVolcTier(WindowMonthly, r.AFPMonthly.Quota, r.AFPMonthly.Used, r.AFPMonthly.ResetTime)

	if len(res.Tiers) == 0 {
		// Empty AFP -> trigger fallback.
		return errorResult("upstream_business_error", "no AFP tiers", start)
	}
	return res
}

func parseVolcengineCodingPlan(result json.RawMessage, start time.Time) *ProviderQuotaResult {
	// Try QuotaUsage[], Usages[], Details[] in order.
	var r struct {
		QuotaUsage []map[string]any `json:"QuotaUsage"`
		Usages     []map[string]any `json:"Usages"`
		Details    []map[string]any `json:"Details"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return errorResult("invalid_json", sanitizeError(err.Error(), nil), start)
	}

	items := r.QuotaUsage
	if len(items) == 0 {
		items = r.Usages
	}
	if len(items) == 0 {
		items = r.Details
	}

	res := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	for _, item := range items {
		level := getStringField(item, "Level", "Type", "Period", "Label", "Window")
		window := normalizeVolcWindow(level)
		if window == "" {
			continue
		}

		pct := getFloatField(item, "Percent", "UsedPercent", "UsagePercent")
		resetTs := getIntField(item, "ResetTimestamp")
		tier := QuotaTier{
			Name:        window,
			Utilization: pct,
		}
		if resetTs > 0 {
			t := time.Unix(resetTs, 0).UTC() // seconds, not milliseconds
			tier.ResetsAt = &t
		}
		res.Tiers = append(res.Tiers, tier)
	}

	return res
}

func normalizeVolcWindow(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "session", "5h", "fivehour", "five_hour", "rolling_5h":
		return WindowFiveHour
	case "weekly", "week", "7d":
		return WindowSevenDay
	case "monthly", "month":
		return WindowMonthly
	default:
		return ""
	}
}

func getStringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func getFloatField(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return toFloat64FromAny(v)
		}
	}
	return 0
}

func getIntField(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int64(n)
			case json.Number:
				i, _ := n.Int64()
				return i
			case int64:
				return n
			}
		}
	}
	return 0
}

func isVolcengineAuthError(msg string) bool {
	lower := strings.ToLower(msg)
	for _, kw := range []string{"auth", "signature", "accessdenied", "denied", "unauthorized", "forbidden", "credential", "token"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// volcRegionFromBaseURL derives the Volcengine region from the provider Base
// URL host. The Ark gateway hosts follow the pattern ark.<region>.volces.com,
// e.g. ark.cn-beijing.volces.com → cn-beijing. Falls back to cn-beijing when
// the host does not match the pattern.
func volcRegionFromBaseURL(baseURL string) string {
	if baseURL == "" {
		return "cn-beijing"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "cn-beijing"
	}
	host := strings.ToLower(u.Hostname())
	// Expected pattern: ark.<region>.volces.com. Extract the <region> segment.
	parts := strings.Split(host, ".")
	// parts == ["ark", "<region>", "volces", "com"]
	if len(parts) == 4 && parts[0] == "ark" && parts[2] == "volces" && parts[3] == "com" {
		region := strings.TrimSpace(parts[1])
		if region != "" {
			return region
		}
	}
	return "cn-beijing"
}

// --- Volcengine V4 Signing ---

type volcSignedRequest struct {
	queryString string
	xDate       string
	bodyHash    string
	authHeader  string
}

// SignVolcengineRequestV4 produces a Volcengine V4 signed request.
// Key differences from AWS V4: no AWS4 prefix, scope ends in "request" (not "aws4_request"),
// fixed header order: host;x-date;x-content-sha256;content-type.
func SignVolcengineRequestV4(query map[string]string, accessKeyID, secretKey, service, region string, now time.Time) volcSignedRequest {
	dateStamp := now.UTC().Format("20060102")
	amzDate := now.UTC().Format("20060102T150405Z")
	bodyHash := sha256Hex("")

	// X-Date is a signed header only — it must NOT appear in the query string
	// (the reference protocol puts it in the x-date header, not as a param).

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

	// Canonical request with FIXED header order.
	canonicalHeaders := "host:open.volcengineapi.com\n" +
		"x-date:" + amzDate + "\n" +
		"x-content-sha256:" + bodyHash + "\n" +
		"content-type:application/json; charset=utf-8\n"
	signedHeaders := "host;x-date;x-content-sha256;content-type"

	canonicalRequest := "POST\n/\n" + canonicalQuery + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + bodyHash
	hashedRequest := sha256Hex(canonicalRequest)

	credentialScope := dateStamp + "/" + region + "/" + service + "/request"
	stringToSign := "HMAC-SHA256\n" + amzDate + "\n" + credentialScope + "\n" + hashedRequest

	// Signing key derivation: kDate = HMAC(SK, date), no AWS4 prefix.
	kDate := hmacSHA256([]byte(secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	authHeader := fmt.Sprintf("HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKeyID, credentialScope, signedHeaders, signature)

	return volcSignedRequest{
		queryString: canonicalQuery,
		xDate:       amzDate,
		bodyHash:    bodyHash,
		authHeader:  authHeader,
	}
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

// Ensure bytes is used (for POST body).
var _ = bytes.NewReader
