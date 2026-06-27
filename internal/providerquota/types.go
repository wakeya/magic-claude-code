// Package providerquota implements provider account quota queries.
//
// It is intentionally separate from internal/usage, which tracks proxy request
// statistics. This package queries provider balance/subscription APIs and
// produces normalized snapshots for display on provider cards.
package providerquota

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Known template types.
const (
	TemplateCustom         = "custom"
	TemplateGeneral        = "general"
	TemplateNewAPI         = "newapi"
	TemplateTokenPlan      = "token_plan"
	TemplateOfficialBalance = "official_balance"
)

// Canonical window names.
const (
	WindowFiveHour = "five_hour"
	WindowSevenDay = "seven_day"
	WindowMonthly  = "monthly"
)

// ProviderQuotaConfig holds per-provider quota query settings.
// Stored as JSON in the providers.quota_query_config column.
type ProviderQuotaConfig struct {
	Enabled                  bool   `json:"enabled"`
	TemplateType             string `json:"template_type"`
	TimeoutSeconds           int    `json:"timeout_seconds,omitempty"`
	AutoQueryIntervalMinutes int    `json:"auto_query_interval_minutes,omitempty"`
	Script                   string `json:"script,omitempty"`

	// Credential overrides; when empty the provider's own APIURL/APIToken
	// are used as fallback (for templates that support it).
	BaseURL            string `json:"base_url,omitempty"`
	APIKey             string `json:"api_key,omitempty"`
	AccessToken        string `json:"access_token,omitempty"`
	UserID             string `json:"user_id,omitempty"`
	CodingPlanProvider string `json:"coding_plan_provider,omitempty"`
	AccessKeyID        string `json:"access_key_id,omitempty"`
	SecretAccessKey    string `json:"secret_access_key,omitempty"`
}

// KnownTemplates returns the set of supported template types.
var KnownTemplates = map[string]bool{
	TemplateCustom:          true,
	TemplateGeneral:         true,
	TemplateNewAPI:          true,
	TemplateTokenPlan:       true,
	TemplateOfficialBalance: true,
}

// DefaultQuotaQueryConfig returns the default configuration (disabled).
func DefaultQuotaQueryConfig() *ProviderQuotaConfig {
	return &ProviderQuotaConfig{
		Enabled:                  false,
		TimeoutSeconds:          10,
		AutoQueryIntervalMinutes: 5,
	}
}

// Validate checks the configuration for correctness and fills defaults.
func (c *ProviderQuotaConfig) Validate() error {
	if c == nil {
		return nil
	}

	// Fill defaults for zero-value numeric fields.
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 10
	}
	// Interval 0 means auto-query disabled; do not override here.
	// The default of 5 is set in DefaultQuotaQueryConfig and applied
	// when creating a new config via the admin API.

	// Template validation.
	if c.Enabled && !KnownTemplates[c.TemplateType] {
		return fmt.Errorf("unknown template_type: %q", c.TemplateType)
	}

	// Timeout range.
	if c.TimeoutSeconds < 2 || c.TimeoutSeconds > 30 {
		return fmt.Errorf("timeout_seconds must be between 2 and 30, got %d", c.TimeoutSeconds)
	}

	// Interval: 0 means disabled auto-query; 1–1440 minutes.
	if c.AutoQueryIntervalMinutes < 0 || c.AutoQueryIntervalMinutes > 1440 {
		return fmt.Errorf("auto_query_interval_minutes must be 0 or 1–1440, got %d", c.AutoQueryIntervalMinutes)
	}

	// Script size limit: 64 KiB.
	if len(c.Script) > 64*1024 {
		return fmt.Errorf("script exceeds 64 KiB limit (%d bytes)", len(c.Script))
	}

	return nil
}

// HasSecrets reports whether any secret field is non-empty.
func (c *ProviderQuotaConfig) HasSecrets() bool {
	if c == nil {
		return false
	}
	return c.APIKey != "" || c.AccessToken != "" || c.SecretAccessKey != ""
}

// QuotaTier represents a time-window quota tier (e.g. 5-hour, 7-day, monthly).
type QuotaTier struct {
	Name        string     `json:"name"`
	Label       string     `json:"label,omitempty"`
	Utilization float64    `json:"utilization"`
	ResetsAt   *time.Time `json:"resets_at,omitempty"`
	Used        *float64   `json:"used,omitempty"`
	Total       *float64   `json:"total,omitempty"`
	Remaining   *float64   `json:"remaining,omitempty"`
	Unit        string     `json:"unit,omitempty"`
}

// BalanceItem represents a balance entry (e.g. USD remaining).
type BalanceItem struct {
	PlanName       string   `json:"plan_name,omitempty"`
	Remaining      *float64 `json:"remaining,omitempty"`
	Used           *float64 `json:"used,omitempty"`
	Total          *float64 `json:"total,omitempty"`
	Unit           string   `json:"unit,omitempty"`
	IsValid        *bool    `json:"is_valid,omitempty"`
	InvalidMessage string   `json:"invalid_message,omitempty"`
	Extra          string   `json:"extra,omitempty"`
}

// ProviderQuotaResult is the normalized output of any quota query.
type ProviderQuotaResult struct {
	ProviderID       string        `json:"provider_id"`
	TemplateType     string        `json:"template_type"`
	Success          bool          `json:"success"`
	CredentialStatus string        `json:"credential_status,omitempty"`
	Tiers            []QuotaTier   `json:"tiers,omitempty"`
	Balances         []BalanceItem `json:"balances,omitempty"`
	ErrorCode        string        `json:"error_code,omitempty"`
	ErrorMessage     string        `json:"error_message,omitempty"`
	QueriedAt        time.Time     `json:"queried_at"`
	DurationMS       int64         `json:"duration_ms"`
}

// NormalizeWindow maps legacy window names to canonical ones.
func NormalizeWindow(w string) string {
	switch w {
	case "weekly_limit", "weekly":
		return WindowSevenDay
	case WindowFiveHour, WindowSevenDay, WindowMonthly:
		return w
	default:
		return w
	}
}

// ClampPercentage clamps a percentage value to [0, 100].
func ClampPercentage(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// NormalizeTier validates and normalizes a QuotaTier, returning an error if
// the tier contains NaN/Inf values.
func NormalizeTier(t QuotaTier) (QuotaTier, error) {
	if math.IsNaN(t.Utilization) || math.IsInf(t.Utilization, 0) {
		return t, fmt.Errorf("tier %q: utilization is NaN/Inf", t.Name)
	}
	t.Name = NormalizeWindow(t.Name)
	t.Utilization = ClampPercentage(t.Utilization)
	return t, nil
}

// NormalizeBalance validates a BalanceItem and rejects NaN/Inf.
func NormalizeBalance(b BalanceItem) (BalanceItem, error) {
	if b.Remaining != nil && (math.IsNaN(*b.Remaining) || math.IsInf(*b.Remaining, 0)) {
		return b, fmt.Errorf("balance %q: remaining is NaN/Inf", b.PlanName)
	}
	if b.Used != nil && (math.IsNaN(*b.Used) || math.IsInf(*b.Used, 0)) {
		return b, fmt.Errorf("balance %q: used is NaN/Inf", b.PlanName)
	}
	if b.Total != nil && (math.IsNaN(*b.Total) || math.IsInf(*b.Total, 0)) {
		return b, fmt.Errorf("balance %q: total is NaN/Inf", b.PlanName)
	}
	if b.Extra != "" && len(b.Extra) > 256 {
		b.Extra = b.Extra[:256]
	}
	return b, nil
}

// NormalizeResult validates all tiers and balances in the result.
// If both tiers and balances are empty and success is true, it changes to
// empty_result failure.
func NormalizeResult(r *ProviderQuotaResult) error {
	for i, tier := range r.Tiers {
		normalized, err := NormalizeTier(tier)
		if err != nil {
			return err
		}
		r.Tiers[i] = normalized
	}
	for i, bal := range r.Balances {
		normalized, err := NormalizeBalance(bal)
		if err != nil {
			return err
		}
		r.Balances[i] = normalized
	}
	// Empty success result is a failure.
	if r.Success && len(r.Tiers) == 0 && len(r.Balances) == 0 {
		r.Success = false
		r.ErrorCode = "empty_result"
		r.ErrorMessage = "query returned no tiers or balances"
	}
	return nil
}

// QuotaSnapshot is the persisted latest query state for a provider.
type QuotaSnapshot struct {
	ProviderID      string              `json:"provider_id"`
	Result          *ProviderQuotaResult `json:"result"`
	LastSuccess     *ProviderQuotaResult `json:"last_success,omitempty"`
	QueriedAt       time.Time           `json:"queried_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

// SanitizedSnapshot is the DTO returned by the admin API.
// It strips sensitive fields and provides convenience booleans.
type SanitizedSnapshot struct {
	ProviderID        string              `json:"provider_id"`
	Result            *ProviderQuotaResult `json:"result,omitempty"`
	LastSuccess       *ProviderQuotaResult `json:"last_success,omitempty"`
	QueriedAt         time.Time           `json:"queried_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	HasLastSuccess    bool                `json:"has_last_success"`
	IsStale           bool                `json:"is_stale"`
}

// PublicQuotaConfig is the DTO returned by the admin API for a provider's
// quota configuration. Secret fields are replaced with *_configured booleans.
type PublicQuotaConfig struct {
	Enabled                  bool   `json:"enabled"`
	TemplateType             string `json:"template_type"`
	TimeoutSeconds           int    `json:"timeout_seconds"`
	AutoQueryIntervalMinutes int    `json:"auto_query_interval_minutes"`
	Script                   string `json:"script,omitempty"`

	BaseURL            string `json:"base_url,omitempty"`
	APIKeyConfigured   bool   `json:"api_key_configured"`
	AccessTokenConfigured bool `json:"access_token_configured"`
	UserID             string `json:"user_id,omitempty"`
	CodingPlanProvider string `json:"coding_plan_provider,omitempty"`
	AccessKeyID        string `json:"access_key_id,omitempty"`
	SecretAccessKeyConfigured bool `json:"secret_access_key_configured"`
}

// ToPublicConfig converts a ProviderQuotaConfig to a PublicQuotaConfig,
// stripping all secret values.
func ToPublicConfig(c *ProviderQuotaConfig) PublicQuotaConfig {
	if c == nil {
		return PublicQuotaConfig{}
	}
	return PublicQuotaConfig{
		Enabled:                  c.Enabled,
		TemplateType:             c.TemplateType,
		TimeoutSeconds:           c.TimeoutSeconds,
		AutoQueryIntervalMinutes: c.AutoQueryIntervalMinutes,
		Script:                   c.Script,
		BaseURL:                  c.BaseURL,
		APIKeyConfigured:         c.APIKey != "",
		AccessTokenConfigured:    c.AccessToken != "",
		UserID:                   c.UserID,
		CodingPlanProvider:       c.CodingPlanProvider,
		AccessKeyID:              c.AccessKeyID,
		SecretAccessKeyConfigured: c.SecretAccessKey != "",
	}
}

// EncodeQuotaConfig serializes a config to JSON for SQLite storage.
// Returns "{}" for nil.
func EncodeQuotaConfig(c *ProviderQuotaConfig) (string, error) {
	if c == nil {
		return "{}", nil
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode quota config: %w", err)
	}
	return string(data), nil
}

// DecodeQuotaConfig deserializes a JSON string into a ProviderQuotaConfig.
// Returns nil for empty or "{}".
func DecodeQuotaConfig(s string) (*ProviderQuotaConfig, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var c ProviderQuotaConfig
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return nil, fmt.Errorf("decode quota config: %w", err)
	}
	return &c, nil
}

// EncodeResult serializes a result to JSON for SQLite storage.
func EncodeResult(r *ProviderQuotaResult) (string, error) {
	if r == nil {
		return "null", nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encode quota result: %w", err)
	}
	return string(data), nil
}

// DecodeResult deserializes a JSON string into a ProviderQuotaResult.
func DecodeResult(s string) (*ProviderQuotaResult, error) {
	if s == "" || s == "null" {
		return nil, nil
	}
	var r ProviderQuotaResult
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return nil, fmt.Errorf("decode quota result: %w", err)
	}
	return &r, nil
}
