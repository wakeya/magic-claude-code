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
			name:       "MiMo card + no explicit = unsupported",
			cardAPIURL: "https://platform.xiaomimimo.com/api",
			wantMiMo:   true,
		},
		{
			name:             "undetectable card + explicit kimi = use explicit",
			cardAPIURL:       "https://custom-gateway.example.com/v1",
			explicitProvider: "kimi",
			wantProvider:     "kimi",
		},
		{
			name:         "undetectable card + no explicit = empty (caller errors)",
			cardAPIURL:   "https://custom-gateway.example.com/v1",
			wantProvider: "",
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

func TestResolveZenMuxCredentialsAtomic(t *testing.T) {
	tests := []struct {
		name        string
		overrideURL string
		overrideKey string
		cardURL     string
		cardKey     string
		wantURL     string
		wantKey     string
		wantErr     bool
	}{
		{
			name:        "override pair",
			overrideURL: "https://quota.zenmux.example/usage",
			overrideKey: "zenmux-key",
			cardURL:     "https://api.zenmux.example/v1",
			cardKey:     "card-key",
			wantURL:     "https://quota.zenmux.example/usage",
			wantKey:     "zenmux-key",
		},
		{
			name:    "card fallback pair",
			cardURL: "https://api.zenmux.example/usage",
			cardKey: "card-key",
			wantURL: "https://api.zenmux.example/usage",
			wantKey: "card-key",
		},
		{
			name:        "override URL without key is rejected",
			overrideURL: "https://quota.zenmux.example/usage",
			cardURL:     "https://api.zenmux.example/v1",
			cardKey:     "card-key",
			wantErr:     true,
		},
		{
			name:        "override key without URL is rejected",
			overrideKey: "zenmux-key",
			cardURL:     "https://api.zenmux.example/v1",
			cardKey:     "card-key",
			wantErr:     true,
		},
		{
			name:    "fallback without card key is rejected",
			cardURL: "https://api.zenmux.example/v1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ProviderQuotaConfig{ZenMuxBaseURL: tt.overrideURL, ZenMuxAPIKey: tt.overrideKey}
			gotURL, gotKey, err := resolveZenMuxCredentials(cfg, tt.cardURL, tt.cardKey)
			if tt.wantErr {
				if !errors.Is(err, ErrMissingCredentials) {
					t.Fatalf("error = %v, want ErrMissingCredentials", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotURL != tt.wantURL || gotKey != tt.wantKey {
				t.Fatalf("resolved pair = %q/%q, want %q/%q", gotURL, gotKey, tt.wantURL, tt.wantKey)
			}
		})
	}
}

func TestValidateForCardUsesEffectiveProvider(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *ProviderQuotaConfig
		cardURL   string
		cardToken string
		wantErr   error
	}{
		{
			name:      "auto ZenMux card fallback is valid",
			cfg:       &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan},
			cardURL:   "https://api.zenmux.example/usage",
			cardToken: "card-key",
		},
		{
			name:      "explicit ZenMux half override is invalid",
			cfg:       &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "zenmux", ZenMuxAPIKey: "key-only"},
			cardURL:   "https://api.zenmux.example/v1",
			cardToken: "card-key",
			wantErr:   ErrMissingCredentials,
		},
		{
			name:    "auto Volcengine requires AK SK",
			cfg:     &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan},
			cardURL: "https://ark.cn-shanghai.volces.com/api/v3",
			wantErr: ErrMissingCredentials,
		},
		{
			name:      "explicit provider mismatch",
			cfg:       &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "kimi"},
			cardURL:   "https://api.minimaxi.com/v1",
			cardToken: "card-key",
			wantErr:   ErrProviderMismatch,
		},
		{
			name:      "MiMo remains a supported saved configuration",
			cfg:       &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan},
			cardURL:   "https://token-plan-cn.xiaomimimo.com/v1",
			cardToken: "card-key",
		},
		{
			name:    "disabled config does not require credentials",
			cfg:     &ProviderQuotaConfig{Enabled: false, TemplateType: TemplateTokenPlan, CodingPlanProvider: "volcengine"},
			cardURL: "https://ark.cn-shanghai.volces.com/api/v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.ValidateForCard(tt.cardURL, tt.cardToken)
			if tt.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolveQueryPlanCredentials(t *testing.T) {
	cardToken := "card-secret"

	t.Run("general uses ScriptAPIKey override over card token", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateGeneral, BaseURL: "https://gw.example.com", ScriptAPIKey: "script-key", ZenMuxAPIKey: "never-send"}
		plan, err := resolveQueryPlan(cfg, "https://gw.example.com", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != "script-key" {
			t.Errorf("token = %q, want script-key", plan.token)
		}
	})

	t.Run("general falls back to card token when ScriptAPIKey empty", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateGeneral, BaseURL: "https://gw.example.com"}
		plan, err := resolveQueryPlan(cfg, "https://gw.example.com", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.token != cardToken {
			t.Errorf("token = %q, want card token fallback", plan.token)
		}
	})

	t.Run("newapi uses only AccessToken, ignores separated keys and card token", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateNewAPI, BaseURL: "https://panel.example.com", AccessToken: "newapi-tok", ScriptAPIKey: "stale-script", ZenMuxAPIKey: "stale-zenmux", UserID: "u1"}
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

	t.Run("kimi uses card token, ignores stale separated keys and AccessToken", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "kimi", ScriptAPIKey: "stale-script-key", ZenMuxAPIKey: "stale-zenmux-key", AccessToken: "stale-newapi-token"}
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

	t.Run("zenmux uses dedicated override pair", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "zenmux", ZenMuxBaseURL: "https://quota.zenmux.example/v1", ZenMuxAPIKey: "zenmux-key", ScriptAPIKey: "never-send"}
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

	t.Run("zenmux falls back to complete card pair", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "zenmux"}
		plan, err := resolveQueryPlan(cfg, "https://zenmux.example.com/v1", cardToken)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.adapterURL != "https://zenmux.example.com/v1" || plan.token != cardToken {
			t.Fatalf("fallback pair = %q/%q", plan.adapterURL, plan.token)
		}
	})

	t.Run("zenmux half override fails instead of mixing credentials", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateTokenPlan, CodingPlanProvider: "zenmux", ZenMuxBaseURL: "https://quota.zenmux.example/v1"}
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

	t.Run("official balance uses card token, ignores stale separated keys and AccessToken", func(t *testing.T) {
		cfg := &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateOfficialBalance, ScriptAPIKey: "stale-script", ZenMuxAPIKey: "stale-zenmux", AccessToken: "stale-token"}
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
