package config

import (
	"strings"
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

func TestProviderValidate_ExposedModelRejectsClaudePrefix(t *testing.T) {
	p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
		{ID: "claude-opus-4-8", Label: "X", BackendModel: "x"},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for claude- prefix ID")
	}
}

func TestProviderValidate_ExposedModelRejects1mSuffix(t *testing.T) {
	p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
		{ID: "glm-4.6[1m]", Label: "X", BackendModel: "x"},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for [1m] suffix ID")
	}
}

func TestProviderValidate_ExposedModelRejectsAlias(t *testing.T) {
	for _, alias := range []string{"sonnet", "opus", "haiku", "opusplan"} {
		p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
			{ID: alias, Label: "X", BackendModel: "x"},
		}}
		if err := p.Validate(); err == nil {
			t.Fatalf("expected error for alias %q", alias)
		}
	}
}

func TestProviderValidate_ExposedModelRejectsInvalidChars(t *testing.T) {
	p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
		{ID: "glm 4.6", Label: "X", BackendModel: "x"}, // 空格不允许
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for ID with spaces")
	}
}

func TestProviderValidate_ExposedModelDuplicateWithinProvider(t *testing.T) {
	p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
		{ID: "glm-4.6", Label: "X", BackendModel: "x"},
		{ID: "glm-4.6", Label: "Y", BackendModel: "y"},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected duplicate ID error within provider")
	}
}

// 空 ID 自动生成稳定随机 ID（em- 前缀），不报错
func TestProviderValidate_ExposedModelEmptyIDAutoGenerated(t *testing.T) {
	p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
		{ID: "", Label: "X", BackendModel: "x"},
	}}
	if err := p.Validate(); err != nil {
		t.Fatalf("empty ID should be auto-generated, got: %v", err)
	}
	got := p.ExposedModels[0].ID
	if !strings.HasPrefix(got, "em-") || len(got) <= 3 {
		t.Fatalf("auto-generated ID = %q, want em-<hex> prefix", got)
	}
}

func TestProviderValidate_ExposedModelRejectsEmptyLabel(t *testing.T) {
	p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
		{ID: "glm-4.6", Label: "  ", BackendModel: "x"},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for empty label")
	}
}

func TestProviderValidate_ExposedModelRejectsEmptyBackendModel(t *testing.T) {
	p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
		{ID: "glm-4.6", Label: "X", BackendModel: "  "},
	}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for empty backend_model")
	}
}

func TestProviderValidate_ExposedModelTrimWritesBack(t *testing.T) {
	p := &Provider{Name: "A", APIURL: "https://a", ExposedModels: []ExposedModel{
		{ID: "  glm-4.6  ", Label: "  GLM  ", Description: "  desc  ", BackendModel: "  glm-4.6  "},
	}}
	if err := p.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ExposedModels[0].ID != "glm-4.6" {
		t.Fatalf("ID not trimmed: %q", p.ExposedModels[0].ID)
	}
	if p.ExposedModels[0].Label != "GLM" {
		t.Fatalf("Label not trimmed: %q", p.ExposedModels[0].Label)
	}
	if p.ExposedModels[0].Description != "desc" {
		t.Fatalf("Description not trimmed: %q", p.ExposedModels[0].Description)
	}
	if p.ExposedModels[0].BackendModel != "glm-4.6" {
		t.Fatalf("BackendModel not trimmed: %q", p.ExposedModels[0].BackendModel)
	}
}
