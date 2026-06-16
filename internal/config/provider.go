package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"
)

// APIFormat describes the upstream provider API protocol.
type APIFormat string

const (
	APIFormatAnthropic       APIFormat = "anthropic"
	APIFormatOpenAIChat      APIFormat = "openai_chat"
	APIFormatOpenAIResponses APIFormat = "openai_responses"
)

// Provider API 供应商配置
type Provider struct {
	// ID 唯一标识符
	ID string `json:"id"`

	// Name 供应商显示名称
	Name string `json:"name"`

	// APIURL 后端 API 地址
	APIURL string `json:"api_url"`

	// APIToken API 密钥
	APIToken string `json:"api_token"`

	// APIFormat 上游 API 协议格式
	APIFormat APIFormat `json:"api_format"`

	// OpenAIExtraParams OpenAI-Compatible 请求额外参数
	OpenAIExtraParams map[string]any `json:"openai_extra_params,omitempty"`

	// ClaudeCodeCompatHint controls whether OpenAI-Compatible requests receive
	// Claude Code tool-use guidance. nil keeps the format-specific default.
	ClaudeCodeCompatHint *bool `json:"claude_code_compat_hint,omitempty"`

	// ModelMappings 模型映射规则
	// key: 客户端请求的模型名（如 claude-sonnet-4）
	// value: 实际转发到后端的模型名（如 glm-5）
	ModelMappings map[string]string `json:"model_mappings"`

	// SupportsThinking 后端是否支持 thinking 字段
	SupportsThinking bool `json:"supports_thinking"`

	// MultimodalSwitch 请求含非文本内容时是否切换到多模态模型
	MultimodalSwitch bool `json:"multimodal_switch"`

	// MultimodalModel 多模态请求使用的模型 ID
	MultimodalModel string `json:"multimodal_model"`

	// StripUnknownContentBlocks controls whether non-standard content blocks
	// (e.g. tool_reference) are proactively stripped before forwarding.
	// Enable for providers with strict content-type validation (e.g. Kimi).
	StripUnknownContentBlocks bool `json:"strip_unknown_content_blocks"`

	// RateLimitQueueEnabled enables local concurrency queue for this provider.
	RateLimitQueueEnabled bool `json:"rate_limit_queue_enabled"`

	// MaxConcurrentRequests caps simultaneous in-flight upstream requests.
	// 0 means unlimited (no concurrency control).
	MaxConcurrentRequests int `json:"max_concurrent_requests"`

	// MaxQueueSize caps waiting requests when concurrency is full.
	// 0 means reject immediately when concurrency is full.
	MaxQueueSize int `json:"max_queue_size"`

	// QueueTimeoutMS is the max wait time in milliseconds for a queued request.
	QueueTimeoutMS int `json:"queue_timeout_ms"`

	// Retry429Enabled enables bounded exponential-backoff retry for upstream 429.
	Retry429Enabled bool `json:"retry_429_enabled"`

	// Retry429MaxAttempts is the max retry count (excluding the initial request).
	Retry429MaxAttempts int `json:"retry_429_max_attempts"`

	// Retry429InitialDelayMS is the initial back-off delay in milliseconds
	// when no Retry-After header is present.
	Retry429InitialDelayMS int `json:"retry_429_initial_delay_ms"`

	// Retry429MaxDelayMS caps each single back-off delay in milliseconds.
	Retry429MaxDelayMS int `json:"retry_429_max_delay_ms"`

	// Enabled 是否启用
	Enabled bool `json:"enabled"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt 更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// NewProvider 创建新的供应商配置
func NewProvider(name, apiURL, apiToken string) *Provider {
	return &Provider{
		ID:            generateProviderID(),
		Name:          name,
		APIURL:        apiURL,
		APIToken:      apiToken,
		APIFormat:     APIFormatAnthropic,
		ModelMappings: make(map[string]string),
		Enabled:       true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

// Validate 验证供应商配置
func (p *Provider) Validate() error {
	p.normalizeDefaults()

	if p.Name == "" {
		return fmt.Errorf("provider name is required")
	}

	if p.APIURL == "" {
		return fmt.Errorf("api_url is required")
	}

	// 验证 URL 格式
	u, err := url.Parse(p.APIURL)
	if err != nil {
		return fmt.Errorf("invalid api_url: %w", err)
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("api_url must use http or https scheme")
	}

	if u.Host == "" {
		return fmt.Errorf("api_url must have a host")
	}

	if !p.APIFormat.IsValid() {
		return fmt.Errorf("unsupported api_format: %s", p.APIFormat)
	}

	if p.MaxConcurrentRequests < 0 {
		return fmt.Errorf("max_concurrent_requests must not be negative")
	}
	if p.MaxQueueSize < 0 {
		return fmt.Errorf("max_queue_size must not be negative")
	}
	if p.QueueTimeoutMS < 0 {
		return fmt.Errorf("queue_timeout_ms must not be negative")
	}
	if p.Retry429MaxAttempts < 0 {
		return fmt.Errorf("retry_429_max_attempts must not be negative")
	}
	if p.Retry429InitialDelayMS < 0 {
		return fmt.Errorf("retry_429_initial_delay_ms must not be negative")
	}
	if p.Retry429MaxDelayMS < 0 {
		return fmt.Errorf("retry_429_max_delay_ms must not be negative")
	}

	if p.RateLimitQueueEnabled {
		if p.MaxConcurrentRequests <= 0 {
			return fmt.Errorf("max_concurrent_requests must be > 0 when rate_limit_queue_enabled")
		}
		if p.MaxQueueSize > 0 && p.QueueTimeoutMS <= 0 {
			return fmt.Errorf("queue_timeout_ms must be > 0 when max_queue_size > 0")
		}
	}

	if p.Retry429Enabled {
		if p.Retry429MaxAttempts <= 0 {
			return fmt.Errorf("retry_429_max_attempts must be > 0 when retry_429_enabled")
		}
	}

	return nil
}

func (p *Provider) normalizeDefaults() {
	if p.APIFormat == "" {
		p.APIFormat = APIFormatAnthropic
	}
	if p.QueueTimeoutMS == 0 && p.RateLimitQueueEnabled {
		p.QueueTimeoutMS = 60000
	}
	if p.Retry429Enabled {
		if p.Retry429MaxAttempts == 0 {
			p.Retry429MaxAttempts = 2
		}
		if p.Retry429InitialDelayMS == 0 {
			p.Retry429InitialDelayMS = 1000
		}
		if p.Retry429MaxDelayMS == 0 {
			p.Retry429MaxDelayMS = 10000
		}
	}
}

// UseClaudeCodeCompatHint reports the effective Claude Code tool-use hint setting.
func (p *Provider) UseClaudeCodeCompatHint() bool {
	if p.ClaudeCodeCompatHint != nil {
		return *p.ClaudeCodeCompatHint
	}
	return p.APIFormat == APIFormatOpenAIChat || p.APIFormat == APIFormatOpenAIResponses
}

// IsValid reports whether the API format is supported by this phase.
func (f APIFormat) IsValid() bool {
	switch f {
	case APIFormatAnthropic, APIFormatOpenAIChat, APIFormatOpenAIResponses:
		return true
	default:
		return false
	}
}

// MapModel 映射模型名称
// 如果存在映射规则，返回映射后的名称；否则返回原名称
func (p *Provider) MapModel(model string) string {
	if p.ModelMappings == nil {
		return model
	}
	if mapped, exists := p.ModelMappings[model]; exists {
		return mapped
	}
	return model
}

// generateProviderID 生成唯一的供应商 ID
func generateProviderID() string {
	return "provider-" + randomHex(8) + "-" + randomHex(4)
}

// randomHex 生成指定长度的十六进制字符串
func randomHex(length int) string {
	b := make([]byte, (length+1)/2)
	if _, err := rand.Read(b); err != nil {
		// 如果随机数生成失败，使用时间戳作为后备
		return fmt.Sprintf("%0*x", length, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:length]
}
