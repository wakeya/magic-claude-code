package providerquota

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestDefaultQuotaQueryConfig(t *testing.T) {
	c := DefaultQuotaQueryConfig()
	if c.Enabled {
		t.Error("default should be disabled")
	}
	if c.TimeoutSeconds != 10 {
		t.Errorf("timeout = %d, want 10", c.TimeoutSeconds)
	}
	if c.AutoQueryIntervalMinutes != 5 {
		t.Errorf("interval = %d, want 5", c.AutoQueryIntervalMinutes)
	}
}

func TestValidateQuotaConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ProviderQuotaConfig
		wantErr bool
	}{
		{
			name:    "nil is valid",
			cfg:     nil,
			wantErr: false,
		},
		{
			name: "valid general config",
			cfg: &ProviderQuotaConfig{
				Enabled:      true,
				TemplateType: TemplateGeneral,
			},
			wantErr: false,
		},
		{
			name: "unknown template",
			cfg: &ProviderQuotaConfig{
				Enabled:      true,
				TemplateType: "unknown",
			},
			wantErr: true,
		},
		{
			name: "timeout too low",
			cfg: &ProviderQuotaConfig{
				TemplateType:   TemplateGeneral,
				TimeoutSeconds: 1,
			},
			wantErr: true,
		},
		{
			name: "timeout too high",
			cfg: &ProviderQuotaConfig{
				TemplateType:   TemplateGeneral,
				TimeoutSeconds: 31,
			},
			wantErr: true,
		},
		{
			name: "interval out of range",
			cfg: &ProviderQuotaConfig{
				TemplateType:             TemplateGeneral,
				AutoQueryIntervalMinutes: 1441,
			},
			wantErr: true,
		},
		{
			name: "script too large",
			cfg: &ProviderQuotaConfig{
				TemplateType: TemplateCustom,
				Script:       string(make([]byte, 64*1024+1)),
			},
			wantErr: true,
		},
		{
			name: "interval zero disabled",
			cfg: &ProviderQuotaConfig{
				Enabled:                  false,
				TemplateType:             TemplateGeneral,
				AutoQueryIntervalMinutes: 0,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestKnownTemplates(t *testing.T) {
	expected := []string{
		TemplateCustom, TemplateGeneral, TemplateNewAPI,
		TemplateTokenPlan, TemplateOfficialBalance,
	}
	for _, tmpl := range expected {
		if !KnownTemplates[tmpl] {
			t.Errorf("KnownTemplates missing %q", tmpl)
		}
	}
}

func TestNormalizeWindow(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"weekly_limit", WindowSevenDay},
		{"weekly", WindowSevenDay},
		{WindowFiveHour, WindowFiveHour},
		{WindowSevenDay, WindowSevenDay},
		{WindowMonthly, WindowMonthly},
		{"custom_window", "custom_window"},
	}
	for _, tt := range tests {
		if got := NormalizeWindow(tt.input); got != tt.want {
			t.Errorf("NormalizeWindow(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestClampPercentage(t *testing.T) {
	tests := []struct {
		input, want float64
	}{
		{0, 0},
		{50, 50},
		{100, 100},
		{-5, 0},
		{105, 100},
		{math.NaN(), 0},
		{math.Inf(1), 0},
		{math.Inf(-1), 0},
	}
	for _, tt := range tests {
		got := ClampPercentage(tt.input)
		if math.IsNaN(tt.want) {
			if !math.IsNaN(got) {
				t.Errorf("ClampPercentage(%v) = %v, want NaN", tt.input, got)
			}
			continue
		}
		if got != tt.want {
			t.Errorf("ClampPercentage(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeTierRejectsNaN(t *testing.T) {
	_, err := NormalizeTier(QuotaTier{
		Name:        WindowFiveHour,
		Utilization: math.NaN(),
	})
	if err == nil {
		t.Error("expected error for NaN utilization")
	}
}

// TestNormalizeTierRejectsOutOfRange verifies that utilization below 0 or
// above 100 is rejected rather than silently clamped.
func TestNormalizeTierRejectsOutOfRange(t *testing.T) {
	tests := []struct {
		name string
		util float64
	}{
		{"above 100", 150},
		{"well above 100", 9999},
		{"below 0", -5},
		{"well below 0", -100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeTier(QuotaTier{
				Name:        WindowFiveHour,
				Utilization: tt.util,
			})
			if err == nil {
				t.Errorf("expected error for utilization %v", tt.util)
			}
		})
	}
}

// TestNormalizeTierAcceptsBoundary verifies valid boundary values are not rejected.
func TestNormalizeTierAcceptsBoundary(t *testing.T) {
	for _, util := range []float64{0, 50, 100} {
		tier, err := NormalizeTier(QuotaTier{Name: WindowFiveHour, Utilization: util})
		if err != nil {
			t.Errorf("unexpected error for utilization %v: %v", util, err)
		}
		if tier.Utilization != util {
			t.Errorf("utilization = %v, want %v", tier.Utilization, util)
		}
	}
}

// TestNormalizeResultPercentageOutOfRange verifies that a result containing
// an out-of-range tier produces an error (which callers map to invalid_response).
func TestNormalizeResultPercentageOutOfRange(t *testing.T) {
	r := &ProviderQuotaResult{
		ProviderID:   "test",
		TemplateType: TemplateTokenPlan,
		Success:      true,
		Tiers: []QuotaTier{
			{Name: WindowFiveHour, Utilization: 150},
		},
		QueriedAt: time.Now(),
	}
	err := NormalizeResult(r)
	if err == nil {
		t.Fatal("expected error for utilization=150")
	}
}

func TestNormalizeBalanceRejectsInf(t *testing.T) {
	v := math.Inf(1)
	_, err := NormalizeBalance(BalanceItem{
		Remaining: &v,
	})
	if err == nil {
		t.Error("expected error for Inf remaining")
	}
}

func TestNormalizeResultEmptySuccess(t *testing.T) {
	r := &ProviderQuotaResult{
		ProviderID:   "test",
		TemplateType: TemplateGeneral,
		Success:      true,
		QueriedAt:    time.Now(),
	}
	if err := NormalizeResult(r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Success {
		t.Error("empty success should become failure")
	}
	if r.ErrorCode != "empty_result" {
		t.Errorf("error_code = %q, want empty_result", r.ErrorCode)
	}
}

func TestNormalizeResultKeepsValidSuccess(t *testing.T) {
	r := &ProviderQuotaResult{
		ProviderID:   "test",
		TemplateType: TemplateGeneral,
		Success:      true,
		Balances: []BalanceItem{
			{Remaining: floatPtr(10)},
		},
		QueriedAt: time.Now(),
	}
	if err := NormalizeResult(r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Success {
		t.Error("result with balances should remain success")
	}
}

func TestEncodeDecodeQuotaConfigRoundTrip(t *testing.T) {
	cfg := &ProviderQuotaConfig{
		Enabled:                  true,
		TemplateType:             TemplateCustom,
		TimeoutSeconds:           15,
		AutoQueryIntervalMinutes: 10,
		BaseURL:                  "https://example.com",
		ScriptAPIKey:             "script-secret",
		ZenMuxBaseURL:            "https://quota.zenmux.example/usage",
		ZenMuxAPIKey:             "zenmux-secret",
	}
	encoded, err := EncodeQuotaConfig(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeQuotaConfig(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.TemplateType != cfg.TemplateType {
		t.Errorf("template = %q, want %q", decoded.TemplateType, cfg.TemplateType)
	}
	if decoded.ScriptAPIKey != cfg.ScriptAPIKey {
		t.Errorf("script_api_key = %q, want %q", decoded.ScriptAPIKey, cfg.ScriptAPIKey)
	}
	if decoded.ZenMuxBaseURL != cfg.ZenMuxBaseURL {
		t.Errorf("zenmux_base_url = %q, want %q", decoded.ZenMuxBaseURL, cfg.ZenMuxBaseURL)
	}
	if decoded.ZenMuxAPIKey != cfg.ZenMuxAPIKey {
		t.Errorf("zenmux_api_key = %q, want %q", decoded.ZenMuxAPIKey, cfg.ZenMuxAPIKey)
	}
}

func TestMigrateLegacyQuotaCredentials(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *ProviderQuotaConfig
		cardAPIURL string
		assert     func(*testing.T, *ProviderQuotaConfig)
	}{
		{
			name: "general key becomes script key",
			cfg:  &ProviderQuotaConfig{TemplateType: TemplateGeneral, LegacyAPIKey: "script-old"},
			assert: func(t *testing.T, cfg *ProviderQuotaConfig) {
				if cfg.ScriptAPIKey != "script-old" || cfg.LegacyAPIKey != "" {
					t.Fatalf("migrated config = %+v", cfg)
				}
			},
		},
		{
			name: "structural zenmux override survives card URL change",
			cfg: &ProviderQuotaConfig{
				TemplateType: TemplateTokenPlan,
				BaseURL:      "https://quota.zenmux.example/usage",
				LegacyAPIKey: "zenmux-old",
			},
			cardAPIURL: "https://gateway.example/v1",
			assert: func(t *testing.T, cfg *ProviderQuotaConfig) {
				if cfg.ZenMuxBaseURL != "https://quota.zenmux.example/usage" || cfg.ZenMuxAPIKey != "zenmux-old" {
					t.Fatalf("migrated config = %+v", cfg)
				}
				if cfg.BaseURL != "" || cfg.LegacyAPIKey != "" {
					t.Fatalf("legacy fields remain: %+v", cfg)
				}
			},
		},
		{
			name: "new field wins over legacy value",
			cfg: &ProviderQuotaConfig{
				TemplateType: TemplateGeneral,
				ScriptAPIKey: "script-new",
				LegacyAPIKey: "script-old",
			},
			assert: func(t *testing.T, cfg *ProviderQuotaConfig) {
				if cfg.ScriptAPIKey != "script-new" || cfg.LegacyAPIKey != "" {
					t.Fatalf("migrated config = %+v", cfg)
				}
			},
		},
		{
			name: "unrelated legacy key is discarded",
			cfg:  &ProviderQuotaConfig{TemplateType: TemplateOfficialBalance, LegacyAPIKey: "stale"},
			assert: func(t *testing.T, cfg *ProviderQuotaConfig) {
				if cfg.LegacyAPIKey != "" || cfg.ScriptAPIKey != "" || cfg.ZenMuxAPIKey != "" {
					t.Fatalf("migrated config = %+v", cfg)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			MigrateLegacyCredentials(tt.cfg, tt.cardAPIURL)
			tt.assert(t, tt.cfg)
		})
	}
}

func TestEncodeDecodeNilConfig(t *testing.T) {
	encoded, err := EncodeQuotaConfig(nil)
	if err != nil {
		t.Fatalf("encode nil: %v", err)
	}
	if encoded != "{}" {
		t.Errorf("encoded nil = %q, want {}", encoded)
	}
	decoded, err := DecodeQuotaConfig(encoded)
	if err != nil {
		t.Fatalf("decode {}: %v", err)
	}
	if decoded != nil {
		t.Errorf("decoded {} = %v, want nil", decoded)
	}
}

func TestEncodeDecodeResultRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	r := &ProviderQuotaResult{
		ProviderID:   "test",
		TemplateType: TemplateTokenPlan,
		Success:      true,
		Tiers: []QuotaTier{
			{Name: WindowFiveHour, Utilization: 42.5},
			{Name: WindowSevenDay, Utilization: 7.3, ResetsAt: &now},
		},
		QueriedAt:  now,
		DurationMS: 150,
	}
	encoded, err := EncodeResult(r)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeResult(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.ProviderID != r.ProviderID {
		t.Errorf("provider_id = %q, want %q", decoded.ProviderID, r.ProviderID)
	}
	if len(decoded.Tiers) != 2 {
		t.Errorf("tiers count = %d, want 2", len(decoded.Tiers))
	}
}

func TestToPublicConfig(t *testing.T) {
	cfg := &ProviderQuotaConfig{
		Enabled:         true,
		TemplateType:    TemplateNewAPI,
		TimeoutSeconds:  15,
		ScriptAPIKey:    "script-secret",
		ZenMuxBaseURL:   "https://quota.zenmux.example/usage",
		ZenMuxAPIKey:    "zenmux-secret",
		AccessToken:     "secret-token",
		SecretAccessKey: "secret-sk",
		AccessKeyID:     "AKLT1234",
		BaseURL:         "https://example.com",
	}
	pub := ToPublicConfig(cfg)
	if !pub.ScriptAPIKeyConfigured {
		t.Error("script_api_key_configured should be true")
	}
	if !pub.ZenMuxAPIKeyConfigured {
		t.Error("zenmux_api_key_configured should be true")
	}
	if pub.ZenMuxBaseURL != cfg.ZenMuxBaseURL {
		t.Errorf("zenmux_base_url = %q, want %q", pub.ZenMuxBaseURL, cfg.ZenMuxBaseURL)
	}
	if pub.AccessTokenConfigured != true {
		t.Error("access_token_configured should be true")
	}
	if pub.SecretAccessKeyConfigured != true {
		t.Error("secret_access_key_configured should be true")
	}
	if pub.AccessKeyID != "****" {
		t.Errorf("access_key_id = %q, want **** (masked)", pub.AccessKeyID)
	}

	// Verify secrets are not in the public config.
	data, _ := json.Marshal(pub)
	s := string(data)
	if contains(s, "script-secret") || contains(s, "zenmux-secret") || contains(s, "secret-token") || contains(s, "secret-sk") {
		t.Errorf("public config contains secret: %s", s)
	}
}

func TestToPublicConfigNil(t *testing.T) {
	pub := ToPublicConfig(nil)
	if pub.Enabled {
		t.Error("nil config should return zero-valued public config")
	}
}

func TestHasSecrets(t *testing.T) {
	c := &ProviderQuotaConfig{}
	if c.HasSecrets() {
		t.Error("empty config should have no secrets")
	}
	c.ScriptAPIKey = "key"
	if !c.HasSecrets() {
		t.Error("config with script_api_key should have secrets")
	}
	c.ScriptAPIKey = ""
	c.ZenMuxAPIKey = "zenmux-key"
	if !c.HasSecrets() {
		t.Error("config with zenmux_api_key should have secrets")
	}
}

func TestValidateFillsTimeoutDefault(t *testing.T) {
	c := &ProviderQuotaConfig{
		Enabled:      true,
		TemplateType: TemplateGeneral,
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.TimeoutSeconds != 10 {
		t.Errorf("timeout defaulted to %d, want 10", c.TimeoutSeconds)
	}
}

func TestValidateFillsIntervalDefault(t *testing.T) {
	// DefaultQuotaQueryConfig should set interval to 5.
	c := DefaultQuotaQueryConfig()
	if c.AutoQueryIntervalMinutes != 5 {
		t.Errorf("default interval = %d, want 5", c.AutoQueryIntervalMinutes)
	}
	// Validate should not override a zero interval (0 means auto-query off).
	c2 := &ProviderQuotaConfig{
		Enabled:                  true,
		TemplateType:             TemplateGeneral,
		AutoQueryIntervalMinutes: 0,
	}
	if err := c2.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c2.AutoQueryIntervalMinutes != 0 {
		t.Errorf("interval = %d, want 0 (explicit off)", c2.AutoQueryIntervalMinutes)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func floatPtr(v float64) *float64 { return &v }
