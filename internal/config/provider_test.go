package config

import (
	"testing"

	"magic-claude-code/internal/providerquota"
)

func TestProviderValidateMigratesLegacyQuotaCredentials(t *testing.T) {
	p := NewProvider("Legacy", "https://gateway.example/v1", "card-token")
	p.QuotaQuery = &providerquota.ProviderQuotaConfig{
		Enabled:      true,
		TemplateType: providerquota.TemplateGeneral,
		LegacyAPIKey: "legacy-script",
	}

	if err := p.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if p.QuotaQuery.ScriptAPIKey != "legacy-script" || p.QuotaQuery.LegacyAPIKey != "" {
		t.Fatalf("quota config not migrated: %+v", p.QuotaQuery)
	}
}

func TestProviderValidateUsesCardContextForQuota(t *testing.T) {
	p := NewProvider("Volcengine", "https://ark.cn-shanghai.volces.com/api/v3", "card-token")
	p.QuotaQuery = &providerquota.ProviderQuotaConfig{
		Enabled:      true,
		TemplateType: providerquota.TemplateTokenPlan,
	}

	if err := p.Validate(); err == nil {
		t.Fatal("Validate() accepted auto-detected Volcengine without AK/SK")
	}
}

func TestProviderValidate_RateLimitDisabled(t *testing.T) {
	p := NewProvider("Test", "https://example.com", "token")
	if err := p.Validate(); err != nil {
		t.Fatalf("default provider should validate: %v", err)
	}
	if p.RateLimitQueueEnabled {
		t.Fatal("rate limit should be disabled by default")
	}
	if p.Retry429Enabled {
		t.Fatal("retry 429 should be disabled by default")
	}
}

func TestProviderValidate_RateLimitEnabled(t *testing.T) {
	p := NewProvider("Test", "https://example.com", "token")
	p.RateLimitQueueEnabled = true
	p.MaxConcurrentRequests = 0
	if err := p.Validate(); err == nil {
		t.Fatal("should reject max_concurrent_requests=0 when enabled")
	}

	p.MaxConcurrentRequests = 5
	if err := p.Validate(); err != nil {
		t.Fatalf("should validate with max_concurrent_requests=5: %v", err)
	}
}

func TestProviderValidate_RetryEnabled(t *testing.T) {
	p := NewProvider("Test", "https://example.com", "token")
	p.Retry429Enabled = true
	p.Retry429MaxAttempts = -1
	if err := p.Validate(); err == nil {
		t.Fatal("should reject max_attempts=-1 when retry enabled")
	}

	p.Retry429MaxAttempts = 3
	if err := p.Validate(); err != nil {
		t.Fatalf("should validate with max_attempts=3: %v", err)
	}
}

func TestProviderValidate_NegativeValuesRejected(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Provider)
	}{
		{"max_concurrent_requests", func(p *Provider) { p.MaxConcurrentRequests = -1 }},
		{"max_queue_size", func(p *Provider) { p.MaxQueueSize = -1 }},
		{"queue_timeout_ms", func(p *Provider) { p.QueueTimeoutMS = -1 }},
		{"retry_429_max_attempts", func(p *Provider) { p.Retry429MaxAttempts = -1 }},
		{"retry_429_initial_delay_ms", func(p *Provider) { p.Retry429InitialDelayMS = -1 }},
		{"retry_429_max_delay_ms", func(p *Provider) { p.Retry429MaxDelayMS = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProvider("Test", "https://example.com", "token")
			tc.mut(p)
			if err := p.Validate(); err == nil {
				t.Fatalf("should reject negative %s", tc.name)
			}
		})
	}
}

func TestProviderNormalizeDefaults_RateLimit(t *testing.T) {
	p := NewProvider("Test", "https://example.com", "token")
	p.Retry429Enabled = true
	p.RateLimitQueueEnabled = true
	p.QueueTimeoutMS = 0
	p.Retry429MaxAttempts = 0
	p.Retry429InitialDelayMS = 0
	p.Retry429MaxDelayMS = 0
	p.MaxConcurrentRequests = 3

	if err := p.Validate(); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if p.QueueTimeoutMS != 60000 {
		t.Fatalf("QueueTimeoutMS = %d, want 60000", p.QueueTimeoutMS)
	}
	if p.Retry429MaxAttempts != 2 {
		t.Fatalf("Retry429MaxAttempts = %d, want 2", p.Retry429MaxAttempts)
	}
	if p.Retry429InitialDelayMS != 1000 {
		t.Fatalf("Retry429InitialDelayMS = %d, want 1000", p.Retry429InitialDelayMS)
	}
	if p.Retry429MaxDelayMS != 10000 {
		t.Fatalf("Retry429MaxDelayMS = %d, want 10000", p.Retry429MaxDelayMS)
	}
}

func TestProviderAPIFormatDefaultsAndValidation(t *testing.T) {
	provider := NewProvider("OpenAI Compatible", "https://example.com/v1", "token")

	if provider.APIFormat != APIFormatAnthropic {
		t.Fatalf("APIFormat = %q, want %q", provider.APIFormat, APIFormatAnthropic)
	}

	provider.APIFormat = APIFormatOpenAIChat
	if err := provider.Validate(); err != nil {
		t.Fatalf("Validate() with openai_chat error = %v", err)
	}

	provider.APIFormat = APIFormatOpenAIResponses
	if err := provider.Validate(); err != nil {
		t.Fatalf("Validate() with openai_responses error = %v", err)
	}

	provider.APIFormat = APIFormat("gemini_native")
	if err := provider.Validate(); err == nil {
		t.Fatal("Validate() with unsupported api format returned nil error")
	}
}

func TestProviderClaudeCodeCompatHintDefaultsForOpenAICompatibleOnly(t *testing.T) {
	provider := NewProvider("Anthropic", "https://example.com", "token")
	if provider.UseClaudeCodeCompatHint() {
		t.Fatal("Anthropic provider should not enable Claude Code compat hint by default")
	}

	provider.APIFormat = APIFormatOpenAIChat
	if !provider.UseClaudeCodeCompatHint() {
		t.Fatal("OpenAI Chat provider should enable Claude Code compat hint by default")
	}

	disabled := false
	provider.ClaudeCodeCompatHint = &disabled
	if provider.UseClaudeCodeCompatHint() {
		t.Fatal("explicit false should disable Claude Code compat hint")
	}

	enabled := true
	provider.ClaudeCodeCompatHint = &enabled
	if !provider.UseClaudeCodeCompatHint() {
		t.Fatal("explicit true should enable Claude Code compat hint")
	}
}
