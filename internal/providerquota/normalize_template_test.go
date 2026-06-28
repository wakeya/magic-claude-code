package providerquota

import (
	"testing"
)

func TestNormalizeForTemplate(t *testing.T) {
	t.Run("non-token_plan clears coding_plan_provider", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateGeneral,
			CodingPlanProvider: "kimi", APIKey: "general-key",
		}
		NormalizeForTemplate(cfg)
		if cfg.CodingPlanProvider != "" {
			t.Errorf("coding_plan_provider = %q, want empty", cfg.CodingPlanProvider)
		}
		if cfg.APIKey != "general-key" {
			t.Errorf("APIKey = %q, want retained for general", cfg.APIKey)
		}
	})

	t.Run("non-general/custom/zenmux clears APIKey", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateNewAPI,
			APIKey: "stale-general-key", AccessToken: "newapi-tok", UserID: "u1",
			BaseURL: "https://panel.example.com",
		}
		NormalizeForTemplate(cfg)
		if cfg.APIKey != "" {
			t.Errorf("APIKey = %q, want empty for newapi", cfg.APIKey)
		}
		if cfg.AccessToken != "newapi-tok" {
			t.Errorf("AccessToken = %q, want retained for newapi", cfg.AccessToken)
		}
	})

	t.Run("non-newapi clears AccessToken and UserID", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateGeneral,
			AccessToken: "stale-newapi-tok", UserID: "stale-u1", APIKey: "k", BaseURL: "https://gw.example.com",
		}
		NormalizeForTemplate(cfg)
		if cfg.AccessToken != "" {
			t.Errorf("AccessToken = %q, want empty for general", cfg.AccessToken)
		}
		if cfg.UserID != "" {
			t.Errorf("UserID = %q, want empty for general", cfg.UserID)
		}
	})

	t.Run("kimi token_plan clears BaseURL/APIKey/AccessToken/AK-SK", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateTokenPlan,
			CodingPlanProvider: "kimi",
			BaseURL:           "https://stale-zenmux.example/v1",
			APIKey:            "stale-zenmux-key",
			AccessToken:       "stale-newapi-tok",
			AccessKeyID:       "stale-ak",
			SecretAccessKey:   "stale-sk",
		}
		NormalizeForTemplate(cfg)
		if cfg.CodingPlanProvider != "kimi" {
			t.Errorf("coding_plan_provider = %q, want retained", cfg.CodingPlanProvider)
		}
		if cfg.BaseURL != "" {
			t.Errorf("BaseURL = %q, want empty for kimi", cfg.BaseURL)
		}
		if cfg.APIKey != "" {
			t.Errorf("APIKey = %q, want empty for kimi", cfg.APIKey)
		}
		if cfg.AccessToken != "" {
			t.Errorf("AccessToken = %q, want empty for kimi", cfg.AccessToken)
		}
		if cfg.AccessKeyID != "" {
			t.Errorf("AccessKeyID = %q, want empty for kimi", cfg.AccessKeyID)
		}
		if cfg.SecretAccessKey != "" {
			t.Errorf("SecretAccessKey = %q, want empty for kimi", cfg.SecretAccessKey)
		}
	})

	t.Run("zenmux retains BaseURL and APIKey", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateTokenPlan,
			CodingPlanProvider: "zenmux",
			BaseURL:            "https://quota.zenmux.example/v1",
			APIKey:             "zenmux-key",
			AccessToken:        "stale-tok",
		}
		NormalizeForTemplate(cfg)
		if cfg.BaseURL != "https://quota.zenmux.example/v1" {
			t.Errorf("BaseURL = %q, want retained for zenmux", cfg.BaseURL)
		}
		if cfg.APIKey != "zenmux-key" {
			t.Errorf("APIKey = %q, want retained for zenmux", cfg.APIKey)
		}
		if cfg.AccessToken != "" {
			t.Errorf("AccessToken = %q, want empty for zenmux", cfg.AccessToken)
		}
	})

	t.Run("volcengine retains AK/SK, clears BaseURL/APIKey/AccessToken", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateTokenPlan,
			CodingPlanProvider: "volcengine",
			AccessKeyID:        "AKLT",
			SecretAccessKey:    "SK",
			BaseURL:            "https://stale.example/v1",
			APIKey:             "stale-key",
		}
		NormalizeForTemplate(cfg)
		if cfg.AccessKeyID != "AKLT" || cfg.SecretAccessKey != "SK" {
			t.Errorf("AK/SK = %q/%q, want retained", cfg.AccessKeyID, cfg.SecretAccessKey)
		}
		if cfg.BaseURL != "" {
			t.Errorf("BaseURL = %q, want empty for volcengine", cfg.BaseURL)
		}
		if cfg.APIKey != "" {
			t.Errorf("APIKey = %q, want empty for volcengine", cfg.APIKey)
		}
	})

	t.Run("official_balance clears all secrets, uses card token", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateOfficialBalance,
			APIKey: "stale-key", AccessToken: "stale-tok", BaseURL: "https://stale.example/v1",
			AccessKeyID: "stale-ak", SecretAccessKey: "stale-sk",
		}
		NormalizeForTemplate(cfg)
		if cfg.APIKey != "" || cfg.AccessToken != "" || cfg.BaseURL != "" {
			t.Errorf("expected all quota secrets cleared for official_balance; got BaseURL=%q APIKey=%q AccessToken=%q", cfg.BaseURL, cfg.APIKey, cfg.AccessToken)
		}
		if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
			t.Errorf("expected AK/SK cleared for official_balance")
		}
		if cfg.CodingPlanProvider != "" {
			t.Errorf("coding_plan_provider = %q, want empty for official_balance", cfg.CodingPlanProvider)
		}
	})

	t.Run("newapi retains BaseURL and AccessToken", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateNewAPI,
			BaseURL:     "https://panel.example.com",
			AccessToken: "newapi-tok",
			UserID:      "u1",
		}
		NormalizeForTemplate(cfg)
		if cfg.BaseURL != "https://panel.example.com" {
			t.Errorf("BaseURL = %q, want retained for newapi", cfg.BaseURL)
		}
		if cfg.AccessToken != "newapi-tok" {
			t.Errorf("AccessToken = %q, want retained for newapi", cfg.AccessToken)
		}
		if cfg.UserID != "u1" {
			t.Errorf("UserID = %q, want retained for newapi", cfg.UserID)
		}
	})
}
