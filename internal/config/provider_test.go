package config

import "testing"

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
