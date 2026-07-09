package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"magic-claude-code/internal/providerquota"
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

	// ExposedModels 对外暴露给 Claude Code /model 菜单的模型列表。
	// 用户在 /model 选中某项后，该会话后续请求的 model 字段等于 ExposedModel.ID，
	// mcc 据此路由到此 provider 并把 model 替换为 BackendModel。
	// ID 跨所有 provider 全局唯一（由 Config.Validate 校验）。
	ExposedModels []ExposedModel `json:"exposed_models,omitempty"`

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

	// QuotaQuery 额度查询配置（可选）
	QuotaQuery *providerquota.ProviderQuotaConfig `json:"quota_query,omitempty"`

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

	if u.User != nil {
		return fmt.Errorf("api_url must not contain userinfo (user:pass@); use api_token instead")
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

	// Validate quota query config if present.
	if p.QuotaQuery != nil {
		providerquota.MigrateLegacyCredentials(p.QuotaQuery, p.APIURL)
		if err := p.QuotaQuery.ValidateForCard(p.APIURL, p.APIToken); err != nil {
			return fmt.Errorf("quota_query: %w", err)
		}
	}

	// 校验对外暴露模型
	for i := range p.ExposedModels {
		p.ExposedModels[i].ID = strings.TrimSpace(p.ExposedModels[i].ID)
		p.ExposedModels[i].Label = strings.TrimSpace(p.ExposedModels[i].Label)
		p.ExposedModels[i].Description = strings.TrimSpace(p.ExposedModels[i].Description)
		p.ExposedModels[i].BackendModel = strings.TrimSpace(p.ExposedModels[i].BackendModel)
	}
	seenExposedIDs := make(map[string]bool)
	for i := range p.ExposedModels {
		em := &p.ExposedModels[i]
		// ID 留空时自动生成稳定随机 ID（前端隐藏 ID 输入，用户无需手输）
		if em.ID == "" {
			em.ID = generateExposedModelID()
		}
		if em.Label == "" {
			return fmt.Errorf("exposed_models[%d]: label is required", i)
		}
		if em.BackendModel == "" {
			return fmt.Errorf("exposed_models[%d]: backend_model is required", i)
		}
		if strings.HasPrefix(em.ID, "claude-") {
			return fmt.Errorf("exposed_models[%d]: id must not start with \"claude-\" (conflicts with built-in menu items)", i)
		}
		if strings.Contains(em.ID, "[1m]") {
			return fmt.Errorf("exposed_models[%d]: id must not contain \"[1m]\" (reserved by Claude Code 1M-context handling)", i)
		}
		switch em.ID {
		case "sonnet", "opus", "haiku", "opusplan":
			return fmt.Errorf("exposed_models[%d]: id %q is reserved by Claude Code model aliases", i, em.ID)
		}
		if strings.IndexFunc(em.ID, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == ':' || r == '-')
		}) >= 0 {
			return fmt.Errorf("exposed_models[%d]: id may only contain letters, digits, '.', '_', ':' and '-'", i)
		}
		if seenExposedIDs[em.ID] {
			return fmt.Errorf("exposed_models[%d]: duplicate id %q within provider", i, em.ID)
		}
		seenExposedIDs[em.ID] = true
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

// ExposedModel 声明一个对外暴露给 Claude Code /model 菜单的模型。
type ExposedModel struct {
	// ID 是全局唯一的逻辑模型名，同时是 /model 菜单选项的 value。
	// 用户选中后，Claude Code 把它作为请求的 model 字段。
	// 不得以 "claude-" 开头（会与内置菜单项撞名被忽略），不得含 "[1m]"。
	ID string `json:"id"`

	// Label 是 /model 菜单里显示的名称。
	Label string `json:"label"`

	// Description 是 /model 菜单里的描述文案。
	Description string `json:"description"`

	// BackendModel 是该 provider 后端真实模型名（必填，由 Provider.Validate 校验非空）。
	BackendModel string `json:"backend_model"`

	// Context1M 标记该模型为 1M 上下文窗口。
	// 为 true 时，bootstrap 注入的菜单 value 会附 [1m] 后缀，
	// 让 Claude Code 客户端按 1M 判定上下文窗口（Sy 正则匹配 [1m]）。
	// mcc 路由仍用不含 [1m] 的纯 ID 匹配（Claude Code 发往后端的 model 已剥离 [1m]）。
	// 同时 mcc 会剥离 context-1m beta header，避免透传给不兼容的后端。
	Context1M bool `json:"context_1m,omitempty"`
}

// generateProviderID 生成唯一的供应商 ID
func generateProviderID() string {
	return "provider-" + randomHex(8) + "-" + randomHex(4)
}

// generateExposedModelID 生成对外暴露模型的稳定随机 ID。
// ID 纯内部用（Claude Code /model 菜单 value + mcc 路由键），用户无需感知，
// 故用 em- 前缀 + 随机 hex，无语义、稳定（生成后写回 struct，不随重排变化）。
func generateExposedModelID() string {
	return "em-" + randomHex(8)
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
