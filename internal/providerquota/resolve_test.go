package providerquota

import (
	"errors"
	"testing"
)

func TestResolveTokenPlanProvider(t *testing.T) {
	tests := []struct {
		name             string
		cardAPIURL       string
		explicitProvider string
		wantProvider     string
		wantMiMo         bool
		wantMismatch     bool
	}{
		{
			name:         "no explicit, auto-detect kimi",
			cardAPIURL:   "https://api.kimi.com/coding/v1",
			wantProvider: "kimi",
		},
		{
			name:         "no explicit, auto-detect zhipu",
			cardAPIURL:   "https://open.bigmodel.cn/api/anthropic",
			wantProvider: "zhipu_cn",
		},
		{
			name:             "explicit kimi matches detected kimi",
			cardAPIURL:       "https://api.kimi.com/coding/v1",
			explicitProvider: "kimi",
			wantProvider:     "kimi",
		},
		{
			name:             "MiniMax card + explicit kimi = mismatch",
			cardAPIURL:       "https://api.minimaxi.com/v1/chat",
			explicitProvider: "kimi",
			wantMismatch:     true,
		},
		{
			name:             "Kimi card + explicit minimax = mismatch",
			cardAPIURL:       "https://api.kimi.com/coding/v1",
			explicitProvider: "minimax_cn",
			wantMismatch:     true,
		},
		{
			name:             "MiMo card + any explicit = unsupported (not mismatch)",
			cardAPIURL:       "https://token-plan-cn.xiaomimimo.com/v1",
			explicitProvider: "kimi",
			wantMiMo:         true,
		},
		{
			name:             "MiMo card + no explicit = unsupported",
			cardAPIURL:       "https://platform.xiaomimimo.com/api",
			wantMiMo:         true,
		},
		{
			name:             "undetectable card + explicit kimi = use explicit",
			cardAPIURL:       "https://custom-gateway.example.com/v1",
			explicitProvider: "kimi",
			wantProvider:     "kimi",
		},
		{
			name:             "undetectable card + no explicit = empty (caller errors)",
			cardAPIURL:       "https://custom-gateway.example.com/v1",
			wantProvider:     "",
		},
		{
			name:             "explicit zenmux with non-zenmux card = mismatch",
			cardAPIURL:       "https://api.kimi.com/coding/v1",
			explicitProvider: "zenmux",
			wantMismatch:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, isMiMo, err := ResolveTokenPlanProvider(tt.cardAPIURL, tt.explicitProvider)
			if tt.wantMismatch {
				if err == nil {
					t.Fatal("expected mismatch error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if provider != tt.wantProvider {
				t.Errorf("provider = %q, want %q", provider, tt.wantProvider)
			}
			if isMiMo != tt.wantMiMo {
				t.Errorf("isMiMo = %v, want %v", isMiMo, tt.wantMiMo)
			}
		})
	}
}

func TestResolveQueryPlanCredentials(t *testing.T) {
	cardToken := "card-secret"

	t.Run("general uses APIKey override over card token", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateGeneral, BaseURL: "https://gw.example.com", APIKey: "quota-key"}
		plan, err := resolveQueryPlan(cfg, "https://gw.example.com", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != "quota-key" {
			t.Errorf("token = %q, want quota-key", plan.token)
		}
	})

	t.Run("general falls back to card token when APIKey empty", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateGeneral, BaseURL: "https://gw.example.com"}
		plan, err := resolveQueryPlan(cfg, "https://gw.example.com", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != cardToken {
			t.Errorf("token = %q, want card token fallback", plan.token)
		}
	})

	t.Run("newapi uses only AccessToken, ignores APIKey and card token", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateNewAPI, BaseURL: "https://panel.example.com", AccessToken: "newapi-tok", APIKey: "stale-key", UserID: "u1"}
		plan, err := resolveQueryPlan(cfg, "https://panel.example.com", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != "newapi-tok" {
			t.Errorf("token = %q, want newapi-tok", plan.token)
		}
	})

	t.Run("newapi missing AccessToken fails", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateNewAPI, BaseURL: "https://panel.example.com"}
		_, err := resolveQueryPlan(cfg, "https://panel.example.com", cardToken)
		if !errors.Is(err, ErrMissingCredentials) {
			t.Errorf("expected ErrMissingCredentials, got %v", err)
		}
	})

	t.Run("kimi uses card token, ignores stale APIKey/AccessToken", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "kimi", APIKey: "stale-zenmux-key", AccessToken: "stale-newapi-token"}
		plan, err := resolveQueryPlan(cfg, "https://api.kimi.com/coding/v1", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != cardToken {
			t.Errorf("token = %q, want card token", plan.token)
		}
		if plan.adapterURL != "https://api.kimi.com/coding/v1" {
			t.Errorf("adapterURL = %q, want card URL", plan.adapterURL)
		}
	})

	t.Run("zenmux uses dedicated APIKey, no card fallback", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "zenmux", BaseURL: "https://quota.zenmux.example/v1", APIKey: "zenmux-key"}
		plan, err := resolveQueryPlan(cfg, "https://zenmux.example.com/v1", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != "zenmux-key" {
			t.Errorf("token = %q, want zenmux-key", plan.token)
		}
		if plan.adapterURL != "https://quota.zenmux.example/v1" {
			t.Errorf("adapterURL = %q, want zenmux quota URL", plan.adapterURL)
		}
	})

	t.Run("zenmux missing dedicated APIKey fails", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "zenmux", BaseURL: "https://quota.zenmux.example/v1"}
		_, err := resolveQueryPlan(cfg, "https://zenmux.example.com/v1", cardToken)
		if !errors.Is(err, ErrMissingCredentials) {
			t.Errorf("expected ErrMissingCredentials, got %v", err)
		}
	})

	t.Run("volcengine uses AK/SK, no card token", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "volcengine", AccessKeyID: "AKLT", SecretAccessKey: "SK"}
		plan, err := resolveQueryPlan(cfg, "https://ark.cn-shanghai.volces.com/api/v3", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != "" {
			t.Errorf("token = %q, want empty (volcengine uses AK/SK)", plan.token)
		}
		if plan.accessKeyID != "AKLT" || plan.secretKey != "SK" {
			t.Errorf("AK/SK = %q/%q, want AKLT/SK", plan.accessKeyID, plan.secretKey)
		}
		if plan.adapterURL != "https://ark.cn-shanghai.volces.com/api/v3" {
			t.Errorf("adapterURL = %q, want card URL for region derivation", plan.adapterURL)
		}
	})

	t.Run("official balance uses card token, ignores stale APIKey/AccessToken", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateOfficialBalance, APIKey: "stale-key", AccessToken: "stale-token"}
		plan, err := resolveQueryPlan(cfg, "https://api.deepseek.com/v1", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != cardToken {
			t.Errorf("token = %q, want card token", plan.token)
		}
		if plan.provider != "deepseek" {
			t.Errorf("provider = %q, want deepseek", plan.provider)
		}
	})
}
