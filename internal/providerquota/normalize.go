package providerquota

import (
	"encoding/json"
	"fmt"
	"time"
)

// normalizeExtracted converts the extractor's return value into a
// ProviderQuotaResult. It handles both single objects and arrays.
func normalizeExtracted(extracted any, start time.Time) (*ProviderQuotaResult, error) {
	result := &ProviderQuotaResult{
		Success:   true,
		QueriedAt: time.Now(),
		DurationMS: time.Since(start).Milliseconds(),
	}

	switch v := extracted.(type) {
	case map[string]any:
		if err := applyExtractedItem(v, result); err != nil {
			return nil, err
		}
	case []any:
		if len(v) == 0 {
			return nil, fmt.Errorf("extractor returned empty array")
		}
		for i, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("extractor array item %d is not an object", i)
			}
			if err := applyExtractedItem(m, result); err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
		}
	default:
		return nil, fmt.Errorf("extractor returned unsupported type %T", extracted)
	}

	if err := NormalizeResult(result); err != nil {
		return nil, err
	}
	return result, nil
}

// applyExtractedItem applies a single extracted item to the result.
// Items with a window field become QuotaTier; others become BalanceItem.
func applyExtractedItem(item map[string]any, result *ProviderQuotaResult) error {
	window, _ := item["window"].(string)

	if window != "" {
		// Time-window tier.
		tier := QuotaTier{
			Name: NormalizeWindow(window),
		}
		if v, ok := item["planName"].(string); ok {
			tier.Label = v
		}
		if v, ok := toFloat64(item["utilization"]); ok {
			tier.Utilization = v
		}
		if v, ok := item["resetsAt"]; ok {
			t, err := parseResetTime(v)
			if err == nil {
				tier.ResetsAt = &t
			}
		}
		if v, ok := toFloat64(item["used"]); ok {
			tier.Used = &v
		}
		if v, ok := toFloat64(item["total"]); ok {
			tier.Total = &v
		}
		if v, ok := toFloat64(item["remaining"]); ok {
			tier.Remaining = &v
		}
		if v, ok := item["unit"].(string); ok {
			tier.Unit = v
		}
		// Auto-derive utilization from used/total or remaining/total if not set.
		if tier.Utilization == 0 {
			if tier.Used != nil && tier.Total != nil && *tier.Total > 0 {
				tier.Utilization = *tier.Used / *tier.Total * 100
			} else if tier.Remaining != nil && tier.Total != nil && *tier.Total > 0 {
				tier.Utilization = (*tier.Total - *tier.Remaining) / *tier.Total * 100
			}
		}
		result.Tiers = append(result.Tiers, tier)
	} else {
		// Balance item.
		bal := BalanceItem{}
		if v, ok := item["planName"].(string); ok {
			bal.PlanName = v
		}
		if v, ok := toFloat64(item["remaining"]); ok {
			bal.Remaining = &v
		}
		if v, ok := toFloat64(item["used"]); ok {
			bal.Used = &v
		}
		if v, ok := toFloat64(item["total"]); ok {
			bal.Total = &v
		}
		if v, ok := item["unit"].(string); ok {
			bal.Unit = v
		}
		if v, ok := item["isValid"].(bool); ok {
			bal.IsValid = &v
		}
		if v, ok := item["invalidMessage"].(string); ok {
			bal.InvalidMessage = v
		}
		if v, ok := item["extra"].(string); ok {
			bal.Extra = v
		}
		result.Balances = append(result.Balances, bal)
	}

	return nil
}

// toFloat64 converts various numeric types to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// parseResetTime parses various time formats.
func parseResetTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case string:
		// Try RFC3339 first.
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed.UTC(), nil
		}
		// Try RFC3339Nano.
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return parsed.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("cannot parse time %q", t)
	case float64:
		// Assume Unix timestamp.
		if t > 1e12 {
			// Milliseconds.
			return time.UnixMilli(int64(t)).UTC(), nil
		}
		return time.Unix(int64(t), 0).UTC(), nil
	case int64:
		if t > 1e12 {
			return time.UnixMilli(t).UTC(), nil
		}
		return time.Unix(t, 0).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported time type %T", v)
	}
}
