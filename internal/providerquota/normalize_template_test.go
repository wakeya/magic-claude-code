package providerquota

import "testing"

func TestNormalizeForTemplateKeepsSeparatedCredentialDomains(t *testing.T) {
	prev := &ProviderQuotaConfig{
		Enabled:            true,
		TemplateType:       TemplateTokenPlan,
		CodingPlanProvider: "zenmux",
		ZenMuxBaseURL:      "https://quota.zenmux.example/usage",
		ZenMuxAPIKey:       "zenmux-old",
	}
	cfg := &ProviderQuotaConfig{
		Enabled:            true,
		TemplateType:       TemplateGeneral,
		CodingPlanProvider: "zenmux",
		BaseURL:            "https://gateway.example/v1",
		ScriptAPIKey:       "script-new",
		ZenMuxBaseURL:      "https://quota.zenmux.example/usage",
		ZenMuxAPIKey:       "zenmux-old",
		AccessToken:        "stale-access-token",
		AccessKeyID:        "stale-ak",
		SecretAccessKey:    "stale-sk",
	}

	NormalizeForTemplate(cfg, "https://gateway.example/v1", prev)

	if cfg.CodingPlanProvider != "" {
		t.Fatalf("CodingPlanProvider = %q, want empty", cfg.CodingPlanProvider)
	}
	if cfg.ScriptAPIKey != "script-new" || cfg.ZenMuxAPIKey != "zenmux-old" {
		t.Fatalf("separated keys = %q/%q", cfg.ScriptAPIKey, cfg.ZenMuxAPIKey)
	}
	if cfg.ZenMuxBaseURL != "https://quota.zenmux.example/usage" {
		t.Fatalf("ZenMuxBaseURL = %q", cfg.ZenMuxBaseURL)
	}
	if cfg.AccessToken != "" || cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
		t.Fatalf("unrelated credentials remain: %+v", cfg)
	}
}

func TestNormalizeForTemplateClearsOnlyInapplicableTemplateFields(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *ProviderQuotaConfig
		cardURL    string
		wantBase   string
		wantAccess string
		wantAK     string
		wantSK     string
	}{
		{
			name: "Kimi clears generic URL NewAPI and Volcengine fields",
			cfg: &ProviderQuotaConfig{
				TemplateType: TemplateTokenPlan, CodingPlanProvider: "kimi",
				BaseURL: "https://stale.example", AccessToken: "stale", UserID: "stale",
				AccessKeyID: "stale-ak", SecretAccessKey: "stale-sk",
			},
		},
		{
			name: "ZenMux clears generic URL but retains separated override",
			cfg: &ProviderQuotaConfig{
				TemplateType: TemplateTokenPlan, CodingPlanProvider: "zenmux",
				BaseURL: "https://legacy.example", AccessToken: "stale",
				ZenMuxBaseURL: "https://quota.zenmux.example/usage", ZenMuxAPIKey: "zenmux",
			},
		},
		{
			name: "auto Volcengine retains AK SK",
			cfg: &ProviderQuotaConfig{
				TemplateType: TemplateTokenPlan,
				AccessKeyID:  "AKLT", SecretAccessKey: "SK",
			},
			cardURL: "https://ark.cn-beijing.volces.com/api/v3",
			wantAK:  "AKLT",
			wantSK:  "SK",
		},
		{
			name: "NewAPI retains generic URL and access token",
			cfg: &ProviderQuotaConfig{
				TemplateType: TemplateNewAPI, BaseURL: "https://panel.example",
				AccessToken: "access", UserID: "u1", AccessKeyID: "stale", SecretAccessKey: "stale",
			},
			wantBase:   "https://panel.example",
			wantAccess: "access",
		},
		{
			name: "Official balance clears generic and dedicated template credentials",
			cfg: &ProviderQuotaConfig{
				TemplateType: TemplateOfficialBalance, BaseURL: "https://stale.example",
				AccessToken: "stale", UserID: "stale", AccessKeyID: "stale", SecretAccessKey: "stale",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.ScriptAPIKey = "script-preserved"
			if tt.cfg.ZenMuxBaseURL == "" {
				tt.cfg.ZenMuxBaseURL = "https://quota.zenmux.example/usage"
				tt.cfg.ZenMuxAPIKey = "zenmux-preserved"
			}
			NormalizeForTemplate(tt.cfg, tt.cardURL, nil)
			if tt.cfg.BaseURL != tt.wantBase || tt.cfg.AccessToken != tt.wantAccess {
				t.Fatalf("BaseURL/AccessToken = %q/%q, want %q/%q", tt.cfg.BaseURL, tt.cfg.AccessToken, tt.wantBase, tt.wantAccess)
			}
			if tt.cfg.AccessKeyID != tt.wantAK || tt.cfg.SecretAccessKey != tt.wantSK {
				t.Fatalf("AK/SK = %q/%q, want %q/%q", tt.cfg.AccessKeyID, tt.cfg.SecretAccessKey, tt.wantAK, tt.wantSK)
			}
			if tt.cfg.ScriptAPIKey != "script-preserved" || tt.cfg.ZenMuxAPIKey == "" || tt.cfg.ZenMuxBaseURL == "" {
				t.Fatalf("separated credentials were cleared: %+v", tt.cfg)
			}
		})
	}
}
