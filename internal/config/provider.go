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

	return nil
}

func (p *Provider) normalizeDefaults() {
	if p.APIFormat == "" {
		p.APIFormat = APIFormatAnthropic
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
