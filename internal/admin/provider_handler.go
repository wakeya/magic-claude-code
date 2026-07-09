package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/providerquota"
)

// providerResponseMap builds the JSON response map for a provider.
func providerResponseMap(p config.Provider, active bool) map[string]interface{} {
	return map[string]interface{}{
		"id":                           p.ID,
		"name":                         p.Name,
		"api_url":                      config.RedactURL(p.APIURL),
		"api_token_mask":               maskToken(p.APIToken),
		"api_format":                   p.APIFormat,
		"openai_extra_params":          p.OpenAIExtraParams,
		"claude_code_compat_hint":      p.UseClaudeCodeCompatHint(),
		"model_mappings":               p.ModelMappings,
		"exposed_models":               p.ExposedModels,
		"supports_thinking":            p.SupportsThinking,
		"multimodal_switch":            p.MultimodalSwitch,
		"multimodal_model":             p.MultimodalModel,
		"strip_unknown_content_blocks": p.StripUnknownContentBlocks,
		"rate_limit_queue_enabled":     p.RateLimitQueueEnabled,
		"max_concurrent_requests":      p.MaxConcurrentRequests,
		"max_queue_size":               p.MaxQueueSize,
		"queue_timeout_ms":             p.QueueTimeoutMS,
		"retry_429_enabled":            p.Retry429Enabled,
		"retry_429_max_attempts":       p.Retry429MaxAttempts,
		"retry_429_initial_delay_ms":   p.Retry429InitialDelayMS,
		"retry_429_max_delay_ms":       p.Retry429MaxDelayMS,
		"enabled":                      p.Enabled,
		"quota_query":                  providerquota.ToPublicConfig(p.QuotaQuery),
		"active":                       active,
		"created_at":                   p.CreatedAt,
		"updated_at":                   p.UpdatedAt,
	}
}

// handleProviders 处理供应商列表
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listProviders(w, r)
	case http.MethodPost:
		s.createProvider(w, r)
	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// listProviders 获取供应商列表
func (s *Server) listProviders(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	// 返回时隐藏 API Token 的敏感部分
	providers := make([]map[string]interface{}, len(cfg.Providers))
	for i, p := range cfg.Providers {
		providers[i] = providerResponseMap(p, p.ID == cfg.ActiveProviderID)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"providers":          providers,
		"active_provider_id": cfg.ActiveProviderID,
	})
}

// createProvider 创建供应商
func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                      string            `json:"name"`
		APIURL                    string            `json:"api_url"`
		APIToken                  string            `json:"api_token"`
		APIFormat                 config.APIFormat  `json:"api_format"`
		OpenAIExtraParams         map[string]any    `json:"openai_extra_params"`
		ClaudeCodeCompatHint      *bool             `json:"claude_code_compat_hint"`
		ModelMappings             map[string]string         `json:"model_mappings"`
		ExposedModels             []config.ExposedModel     `json:"exposed_models"`
		SupportsThinking          bool                      `json:"supports_thinking"`
		MultimodalSwitch          bool              `json:"multimodal_switch"`
		MultimodalModel           string            `json:"multimodal_model"`
		StripUnknownContentBlocks bool              `json:"strip_unknown_content_blocks"`
		RateLimitQueueEnabled     bool              `json:"rate_limit_queue_enabled"`
		MaxConcurrentRequests     int               `json:"max_concurrent_requests"`
		MaxQueueSize              int               `json:"max_queue_size"`
		QueueTimeoutMS            int               `json:"queue_timeout_ms"`
		Retry429Enabled           bool              `json:"retry_429_enabled"`
		Retry429MaxAttempts       int               `json:"retry_429_max_attempts"`
		Retry429InitialDelayMS    int               `json:"retry_429_initial_delay_ms"`
		Retry429MaxDelayMS        int               `json:"retry_429_max_delay_ms"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	// 验证必填字段
	if req.Name == "" {
		http.Error(w, `{"error": "name is required"}`, http.StatusBadRequest)
		return
	}
	if req.APIURL == "" {
		http.Error(w, `{"error": "api_url is required"}`, http.StatusBadRequest)
		return
	}

	// 验证 URL 格式
	parsedURL, err := url.Parse(req.APIURL)
	if err != nil {
		http.Error(w, `{"error": "invalid api_url format"}`, http.StatusBadRequest)
		return
	}
	if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		http.Error(w, `{"error": "api_url must use http or https scheme"}`, http.StatusBadRequest)
		return
	}
	req.MultimodalModel = strings.TrimSpace(req.MultimodalModel)
	if req.MultimodalSwitch && req.MultimodalModel == "" {
		http.Error(w, `{"error": "multimodal_model is required when multimodal_switch is enabled"}`, http.StatusBadRequest)
		return
	}

	// 加载配置
	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	// 创建新供应商
	now := time.Now()
	provider := config.Provider{
		ID:                        generateProviderID(),
		Name:                      req.Name,
		APIURL:                    req.APIURL,
		APIToken:                  req.APIToken,
		APIFormat:                 req.APIFormat,
		OpenAIExtraParams:         req.OpenAIExtraParams,
		ClaudeCodeCompatHint:      req.ClaudeCodeCompatHint,
		ModelMappings:             req.ModelMappings,
		ExposedModels:             req.ExposedModels,
		SupportsThinking:          req.SupportsThinking,
		MultimodalSwitch:          req.MultimodalSwitch,
		MultimodalModel:           req.MultimodalModel,
		StripUnknownContentBlocks: req.StripUnknownContentBlocks,
		RateLimitQueueEnabled:     req.RateLimitQueueEnabled,
		MaxConcurrentRequests:     req.MaxConcurrentRequests,
		MaxQueueSize:              req.MaxQueueSize,
		QueueTimeoutMS:            req.QueueTimeoutMS,
		Retry429Enabled:           req.Retry429Enabled,
		Retry429MaxAttempts:       req.Retry429MaxAttempts,
		Retry429InitialDelayMS:    req.Retry429InitialDelayMS,
		Retry429MaxDelayMS:        req.Retry429MaxDelayMS,
		Enabled:                   true,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}

	cfg.Providers = append(cfg.Providers, provider)

	// 如果是第一个供应商，自动激活
	if len(cfg.Providers) == 1 {
		cfg.ActiveProviderID = provider.ID
	}

	if err := cfg.Validate(); err != nil {
		jsonErr, _ := json.Marshal(map[string]string{"error": err.Error()})
		http.Error(w, string(jsonErr), http.StatusBadRequest)
		return
	}

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// 用 cfg.Providers 的最后一个元素构造响应：cfg.Validate 会 trim exposed_models
	// 等字段并写回 slice 内的副本（append 后局部 provider 变量与 slice 元素是不同副本）。
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"provider": providerResponseMap(cfg.Providers[len(cfg.Providers)-1], len(cfg.Providers) == 1),
	})
}

// handleProvider 处理单个供应商操作
func (s *Server) handleProvider(w http.ResponseWriter, r *http.Request) {
	// 从 URL 提取 ID: /api/providers/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	id := path

	if id == "" {
		http.Error(w, `{"error": "provider id is required"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getProvider(w, r, id)
	case http.MethodPut:
		s.updateProvider(w, r, id)
	case http.MethodDelete:
		s.deleteProvider(w, r, id)
	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// getProvider 获取单个供应商
func (s *Server) getProvider(w http.ResponseWriter, _ *http.Request, id string) {
	w.Header().Set("Content-Type", "application/json")

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(providerResponseMap(*provider, provider.ID == cfg.ActiveProviderID))
}

// updateProvider 更新供应商
func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Name                      string            `json:"name"`
		APIURL                    string            `json:"api_url"`
		APIToken                  string            `json:"api_token"`
		APIFormat                 *config.APIFormat `json:"api_format"`
		OpenAIExtraParams         map[string]any    `json:"openai_extra_params"`
		ClaudeCodeCompatHint      *bool             `json:"claude_code_compat_hint"`
		ModelMappings             map[string]string         `json:"model_mappings"`
		ExposedModels             *[]config.ExposedModel    `json:"exposed_models"`
		SupportsThinking          *bool                     `json:"supports_thinking"`
		MultimodalSwitch          *bool             `json:"multimodal_switch"`
		MultimodalModel           *string           `json:"multimodal_model"`
		StripUnknownContentBlocks *bool             `json:"strip_unknown_content_blocks"`
		Enabled                   *bool             `json:"enabled"`
		RateLimitQueueEnabled     *bool             `json:"rate_limit_queue_enabled"`
		MaxConcurrentRequests     *int              `json:"max_concurrent_requests"`
		MaxQueueSize              *int              `json:"max_queue_size"`
		QueueTimeoutMS            *int              `json:"queue_timeout_ms"`
		Retry429Enabled           *bool             `json:"retry_429_enabled"`
		Retry429MaxAttempts       *int              `json:"retry_429_max_attempts"`
		Retry429InitialDelayMS    *int              `json:"retry_429_initial_delay_ms"`
		Retry429MaxDelayMS        *int              `json:"retry_429_max_delay_ms"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}
	oldAPIURL := provider.APIURL
	oldAPIToken := provider.APIToken

	// 更新字段
	if req.Name != "" {
		provider.Name = req.Name
	}
	if req.APIURL != "" {
		// 验证 URL 格式
		parsedURL, err := url.Parse(req.APIURL)
		if err != nil {
			http.Error(w, `{"error": "invalid api_url format"}`, http.StatusBadRequest)
			return
		}
		if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
			http.Error(w, `{"error": "api_url must use http or https scheme"}`, http.StatusBadRequest)
			return
		}
		provider.APIURL = req.APIURL
	}
	if req.APIToken != "" {
		provider.APIToken = req.APIToken
	}
	if req.APIFormat != nil {
		provider.APIFormat = *req.APIFormat
	}
	if req.OpenAIExtraParams != nil {
		provider.OpenAIExtraParams = req.OpenAIExtraParams
	}
	if req.ClaudeCodeCompatHint != nil {
		provider.ClaudeCodeCompatHint = req.ClaudeCodeCompatHint
	}
	if req.ModelMappings != nil {
		provider.ModelMappings = req.ModelMappings
	}
	if req.ExposedModels != nil {
		provider.ExposedModels = *req.ExposedModels
	}
	if req.Enabled != nil {
		provider.Enabled = *req.Enabled
	}
	if req.SupportsThinking != nil {
		provider.SupportsThinking = *req.SupportsThinking
	}
	if req.MultimodalModel != nil {
		provider.MultimodalModel = strings.TrimSpace(*req.MultimodalModel)
	}
	if req.MultimodalSwitch != nil {
		provider.MultimodalSwitch = *req.MultimodalSwitch
	}
	if req.StripUnknownContentBlocks != nil {
		provider.StripUnknownContentBlocks = *req.StripUnknownContentBlocks
	}
	if req.RateLimitQueueEnabled != nil {
		provider.RateLimitQueueEnabled = *req.RateLimitQueueEnabled
	}
	if req.MaxConcurrentRequests != nil {
		provider.MaxConcurrentRequests = *req.MaxConcurrentRequests
	}
	if req.MaxQueueSize != nil {
		provider.MaxQueueSize = *req.MaxQueueSize
	}
	if req.QueueTimeoutMS != nil {
		provider.QueueTimeoutMS = *req.QueueTimeoutMS
	}
	if req.Retry429Enabled != nil {
		provider.Retry429Enabled = *req.Retry429Enabled
	}
	if req.Retry429MaxAttempts != nil {
		provider.Retry429MaxAttempts = *req.Retry429MaxAttempts
	}
	if req.Retry429InitialDelayMS != nil {
		provider.Retry429InitialDelayMS = *req.Retry429InitialDelayMS
	}
	if req.Retry429MaxDelayMS != nil {
		provider.Retry429MaxDelayMS = *req.Retry429MaxDelayMS
	}
	if provider.MultimodalSwitch && strings.TrimSpace(provider.MultimodalModel) == "" {
		http.Error(w, `{"error": "multimodal_model is required when multimodal_switch is enabled"}`, http.StatusBadRequest)
		return
	}
	if err := cfg.Validate(); err != nil {
		jsonErr, _ := json.Marshal(map[string]string{"error": err.Error()})
		http.Error(w, string(jsonErr), http.StatusBadRequest)
		return
	}
	provider.UpdatedAt = time.Now()

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}
	if provider.QuotaQuery != nil && s.quotaManager != nil &&
		(oldAPIURL != provider.APIURL || oldAPIToken != provider.APIToken) {
		if err := s.quotaManager.DeleteSnapshot(id); err != nil {
			http.Error(w, `{"error": "config saved but failed to clear quota snapshot"}`, http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"provider": providerResponseMap(*provider, provider.ID == cfg.ActiveProviderID),
	})
}

// deleteProvider 删除供应商
func (s *Server) deleteProvider(w http.ResponseWriter, _ *http.Request, id string) {
	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	// 查找并删除供应商
	found := false
	newProviders := make([]config.Provider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		if p.ID == id {
			found = true
			continue
		}
		newProviders = append(newProviders, p)
	}

	if !found {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	cfg.Providers = newProviders

	// 如果删除的是当前激活的供应商，清除 ActiveProviderID
	if cfg.ActiveProviderID == id {
		cfg.ActiveProviderID = ""
		// 如果还有其他供应商，自动激活第一个
		if len(newProviders) > 0 {
			cfg.ActiveProviderID = newProviders[0].ID
		}
	}

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleProviderActivate 激活供应商
func (s *Server) handleProviderActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 从 URL 提取 ID: /api/providers/{id}/activate
	path := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	path = strings.TrimSuffix(path, "/activate")
	id := path

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	// 查找供应商
	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	// 检查供应商是否已启用
	if !provider.Enabled {
		http.Error(w, `{"error": "provider is not enabled"}`, http.StatusBadRequest)
		return
	}

	cfg.ActiveProviderID = id

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleProviderToggle 启用/禁用供应商
func (s *Server) handleProviderToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 从 URL 提取 ID: /api/providers/{id}/toggle
	path := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	path = strings.TrimSuffix(path, "/toggle")
	id := path

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	// 切换状态
	provider.Enabled = !provider.Enabled
	provider.UpdatedAt = time.Now()

	// 如果禁用的是当前激活的供应商，清除激活状态
	if !provider.Enabled && cfg.ActiveProviderID == id {
		cfg.ActiveProviderID = ""
	}

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"enabled": provider.Enabled,
	})
}

// handleTestProvider 测试供应商连接
func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		APIURL   string `json:"api_url"`
		APIToken string `json:"api_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	// 解析并验证 URL
	parsedURL, err := url.Parse(req.APIURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid URL format",
		})
		return
	}

	// 只允许 HTTP/HTTPS 协议
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "only HTTP/HTTPS protocols are allowed",
		})
		return
	}

	// 防止 SSRF：禁止访问内网地址
	host := parsedURL.Hostname()
	if isInternalIP(host) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "access to internal addresses is not allowed",
		})
		return
	}

	// 测试连接
	client := &http.Client{Timeout: 10 * time.Second}
	req2, _ := http.NewRequest("GET", req.APIURL, nil)
	if req.APIToken != "" {
		req2.Header.Set("Authorization", "Bearer "+req.APIToken)
	}

	resp, err := client.Do(req2)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "connection failed: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"status_code": resp.StatusCode,
	})
}

// maskToken 脱敏显示 Token
func maskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

// handleProviderRoutes 处理带 ID 的供应商路由
// 匹配: /api/providers/{id}, /api/providers/{id}/activate, /api/providers/{id}/toggle, /api/providers/{id}/test, /api/providers/{id}/usage
func (s *Server) handleProviderRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Check usage routes first (must be before generic handler).
	if strings.Contains(path, "/usage") {
		s.handleProviderQuotaRoutes(w, r)
		return
	}

	// 检查是否是激活路由
	if strings.HasSuffix(path, "/activate") {
		s.handleProviderActivate(w, r)
		return
	}

	// 检查是否是切换路由
	if strings.HasSuffix(path, "/toggle") {
		s.handleProviderToggle(w, r)
		return
	}

	// 检查是否是复制路由
	if strings.HasSuffix(path, "/duplicate") {
		s.handleProviderDuplicate(w, r)
		return
	}

	// 检查是否是查看原文路由
	if strings.HasSuffix(path, "/reveal-token") {
		s.handleProviderRevealToken(w, r)
		return
	}

	// 检查是否是测试路由
	if strings.HasSuffix(path, "/test") {
		s.handleTestProviderByID(w, r)
		return
	}

	// 否则是单个供应商操作
	s.handleProvider(w, r)
}

// handleTestProviderByID 通过供应商 ID 测试连接
func (s *Server) handleTestProviderByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 从 URL 提取 ID
	path := strings.TrimSuffix(r.URL.Path, "/test")
	id := strings.TrimPrefix(path, "/api/providers/")

	cfg, err := s.configStore.Load()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "failed to load config",
		})
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "provider not found",
		})
		return
	}

	// 解析并验证 URL
	parsedURL, err := url.Parse(provider.APIURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "invalid URL format",
		})
		return
	}

	// 只允许 HTTP/HTTPS 协议
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "only HTTP/HTTPS protocols are allowed",
		})
		return
	}

	// 防止 SSRF：禁止访问内网地址
	host := parsedURL.Hostname()
	if isInternalIP(host) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "access to internal addresses is not allowed",
		})
		return
	}

	// 测试连接
	client := &http.Client{Timeout: 10 * time.Second}
	req2, _ := http.NewRequest("GET", provider.APIURL, nil)
	if provider.APIToken != "" {
		req2.Header.Set("Authorization", "Bearer "+provider.APIToken)
	}

	resp, err := client.Do(req2)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "connection failed: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"status_code": resp.StatusCode,
	})
}

// handleProviderRevealToken 获取供应商 API Token 原文
func (s *Server) handleProviderRevealToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimSuffix(r.URL.Path, "/reveal-token")
	id := strings.TrimPrefix(path, "/api/providers/")

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"api_token": provider.APIToken,
	})
}

// handleProviderDuplicate 复制供应商配置
func (s *Server) handleProviderDuplicate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 从 URL 提取 ID: /api/providers/{id}/duplicate
	path := strings.TrimSuffix(r.URL.Path, "/duplicate")
	id := strings.TrimPrefix(path, "/api/providers/")

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	now := time.Now()
	newProvider := config.Provider{
		ID:                        generateProviderID(),
		Name:                      provider.Name + " 复制",
		APIURL:                    provider.APIURL,
		APIToken:                  provider.APIToken,
		APIFormat:                 provider.APIFormat,
		OpenAIExtraParams:         provider.OpenAIExtraParams,
		ClaudeCodeCompatHint:      provider.ClaudeCodeCompatHint,
		ModelMappings:             provider.ModelMappings,
		SupportsThinking:          provider.SupportsThinking,
		MultimodalSwitch:          provider.MultimodalSwitch,
		MultimodalModel:           provider.MultimodalModel,
		StripUnknownContentBlocks: provider.StripUnknownContentBlocks,
		RateLimitQueueEnabled:     provider.RateLimitQueueEnabled,
		MaxConcurrentRequests:     provider.MaxConcurrentRequests,
		MaxQueueSize:              provider.MaxQueueSize,
		QueueTimeoutMS:            provider.QueueTimeoutMS,
		Retry429Enabled:           provider.Retry429Enabled,
		Retry429MaxAttempts:       provider.Retry429MaxAttempts,
		Retry429InitialDelayMS:    provider.Retry429InitialDelayMS,
		Retry429MaxDelayMS:        provider.Retry429MaxDelayMS,
		QuotaQuery:                copyQuotaQueryConfig(provider.QuotaQuery, provider.APIURL),
		Enabled:                   true,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
	cfg.Providers = append(cfg.Providers, newProvider)

	if err := cfg.Validate(); err != nil {
		jsonErr, _ := json.Marshal(map[string]string{"error": err.Error()})
		http.Error(w, string(jsonErr), http.StatusBadRequest)
		return
	}

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"provider": providerResponseMap(newProvider, false),
	})
}

// generateProviderID 生成唯一的供应商 ID
func generateProviderID() string {
	return "provider-" + randomHex(8) + "-" + randomHex(4)
}

// providerExportFile 是供应商导出文件的 JSON 结构。
// version 字段用于后续格式演进；当前固定为 1。
type providerExportFile struct {
	Version    int               `json:"version"`
	ExportedAt time.Time         `json:"exported_at"`
	Providers  []config.Provider `json:"providers"`
}

// handleExportProviders 导出选中的供应商为 JSON（含真实 api_token）。
// 请求体 {"ids": [...]} 指定要导出的供应商 ID；未知 ID 静默跳过。
func (s *Server) handleExportProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	idSet := make(map[string]bool, len(req.IDs))
	for _, id := range req.IDs {
		idSet[id] = true
	}

	exported := make([]config.Provider, 0, len(req.IDs))
	for _, p := range cfg.Providers {
		if idSet[p.ID] {
			exported = append(exported, p)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(providerExportFile{
		Version:    1,
		ExportedAt: time.Now(),
		Providers:  exported,
	})
}

// handleImportProviders 导入供应商，按 strategy 处理 ID 冲突。
// 请求体 {"version":1, "providers":[...], "strategy":"skip|overwrite|duplicate"}。
// 整个导入在一次 Load→合并→Save 周期内完成；Save 失败则不更改任何供应商。
func (s *Server) handleImportProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Version   int               `json:"version"`
		Providers []config.Provider `json:"providers"`
		Strategy  string            `json:"strategy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.Version != 1 {
		http.Error(w, `{"error": "unsupported export version"}`, http.StatusBadRequest)
		return
	}
	strategy := req.Strategy
	if strategy != "skip" && strategy != "overwrite" && strategy != "duplicate" {
		strategy = "skip" // 未知策略默认 skip
	}

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	// 建立现有 ID 索引
	existingIdx := make(map[string]int, len(cfg.Providers))
	for i, p := range cfg.Providers {
		existingIdx[p.ID] = i
	}

	now := time.Now()
	summary := struct {
		Success     bool     `json:"success"`
		Imported    int      `json:"imported"`
		Skipped     int      `json:"skipped"`
		Overwritten int      `json:"overwritten"`
		Duplicated  int      `json:"duplicated"`
		Errors      []string `json:"errors"`
	}{Success: true, Errors: []string{}}
	invalidateSnapshots := make(map[string]bool)

	// 文件内去重：skip/overwrite 策略下同一 ID 只处理首次出现；
	// duplicate 策略下每条都生成新 ID，无需去重。
	seenInFile := make(map[string]bool)
	dedup := strategy != "duplicate"

	for _, p := range req.Providers {
		if dedup && seenInFile[p.ID] {
			continue
		}
		seenInFile[p.ID] = true

		// 校验；无效则跳过并记录
		cp := p
		if err := cp.Validate(); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", p.Name, err))
			continue
		}

		if _, conflict := existingIdx[p.ID]; conflict {
			switch strategy {
			case "skip":
				summary.Skipped++
			case "overwrite":
				orig := cfg.Providers[existingIdx[p.ID]]
				cp.CreatedAt = orig.CreatedAt // 保留原创建时间
				cp.UpdatedAt = now
				if orig.APIURL != cp.APIURL || orig.APIToken != cp.APIToken ||
					isMaterialQuotaChange(orig.QuotaQuery, cp.QuotaQuery) {
					invalidateSnapshots[p.ID] = true
				}
				cfg.Providers[existingIdx[p.ID]] = cp
				summary.Overwritten++
			case "duplicate":
				// 冲突项生成新 ID 追加，原供应商不变。
				// 清空 ExposedModels：ID 需要全局唯一，保留会导致与原 provider 冲突。
				// 与 handleProviderDuplicate 语义一致。
				cp.ID = generateProviderID()
				cp.ExposedModels = nil
				cp.CreatedAt = now
				cp.UpdatedAt = now
				cfg.Providers = append(cfg.Providers, cp)
				summary.Duplicated++
			}
		} else {
			if cp.CreatedAt.IsZero() {
				cp.CreatedAt = now
			}
			if cp.UpdatedAt.IsZero() {
				cp.UpdatedAt = now
			}
			cfg.Providers = append(cfg.Providers, cp)
			existingIdx[cp.ID] = len(cfg.Providers) - 1
			summary.Imported++
		}
	}

	// 全局校验（含跨 provider ExposedModel ID 唯一性）
	if err := cfg.Validate(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusBadRequest)
		return
	}

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}
	if s.quotaManager != nil {
		for id := range invalidateSnapshots {
			if err := s.quotaManager.DeleteSnapshot(id); err != nil {
				log.Printf("admin: failed to clear quota snapshot after importing provider %q: %v", id, err)
				summary.Success = false
				summary.Errors = append(summary.Errors,
					fmt.Sprintf("%s: config saved but failed to clear quota snapshot", id))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if !summary.Success {
		w.WriteHeader(http.StatusInternalServerError)
	}
	json.NewEncoder(w).Encode(summary)
}

// randomHex 生成指定长度的十六进制字符串
func randomHex(length int) string {
	b := make([]byte, (length+1)/2)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", length)
	}
	return hex.EncodeToString(b)[:length]
}

// copyQuotaQueryConfig returns a deep copy of the quota config,
// including secret fields. Used for provider duplication where
// secrets should be copied but the snapshot is not.
func copyQuotaQueryConfig(c *providerquota.ProviderQuotaConfig, cardAPIURL string) *providerquota.ProviderQuotaConfig {
	if c == nil {
		return nil
	}
	cp := *c
	providerquota.MigrateLegacyCredentials(&cp, cardAPIURL)
	return &cp
}
