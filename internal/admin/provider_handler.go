package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"claude_code_proxy_dns/internal/config"
)

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
		providers[i] = map[string]interface{}{
			"id":                p.ID,
			"name":              p.Name,
			"api_url":           p.APIURL,
			"api_token_mask":    maskToken(p.APIToken),
			"supports_thinking": p.SupportsThinking,
			"multimodal_switch": p.MultimodalSwitch,
			"multimodal_model":  p.MultimodalModel,
			"model_mappings":    p.ModelMappings,
			"enabled":           p.Enabled,
			"active":            p.ID == cfg.ActiveProviderID,
			"created_at":        p.CreatedAt,
			"updated_at":        p.UpdatedAt,
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"providers":          providers,
		"active_provider_id": cfg.ActiveProviderID,
	})
}

// createProvider 创建供应商
func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name             string            `json:"name"`
		APIURL           string            `json:"api_url"`
		APIToken         string            `json:"api_token"`
		ModelMappings    map[string]string `json:"model_mappings"`
		SupportsThinking bool              `json:"supports_thinking"`
		MultimodalSwitch bool              `json:"multimodal_switch"`
		MultimodalModel  string            `json:"multimodal_model"`
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
		ID:               generateProviderID(),
		Name:             req.Name,
		APIURL:           req.APIURL,
		APIToken:         req.APIToken,
		ModelMappings:    req.ModelMappings,
		SupportsThinking: req.SupportsThinking,
		MultimodalSwitch: req.MultimodalSwitch,
		MultimodalModel:  req.MultimodalModel,
		Enabled:          true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	cfg.Providers = append(cfg.Providers, provider)

	// 如果是第一个供应商，自动激活
	if len(cfg.Providers) == 1 {
		cfg.ActiveProviderID = provider.ID
	}

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"provider": provider,
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

	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":                provider.ID,
		"name":              provider.Name,
		"api_url":           provider.APIURL,
		"api_token_mask":    maskToken(provider.APIToken),
		"model_mappings":    provider.ModelMappings,
		"supports_thinking": provider.SupportsThinking,
		"multimodal_switch": provider.MultimodalSwitch,
		"multimodal_model":  provider.MultimodalModel,
		"enabled":           provider.Enabled,
		"active":            provider.ID == cfg.ActiveProviderID,
		"created_at":        provider.CreatedAt,
		"updated_at":        provider.UpdatedAt,
	})
}

// updateProvider 更新供应商
func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Name             string            `json:"name"`
		APIURL           string            `json:"api_url"`
		APIToken         string            `json:"api_token"`
		ModelMappings    map[string]string `json:"model_mappings"`
		SupportsThinking *bool             `json:"supports_thinking"`
		MultimodalSwitch *bool             `json:"multimodal_switch"`
		MultimodalModel  *string           `json:"multimodal_model"`
		Enabled          *bool             `json:"enabled"`
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
	if req.ModelMappings != nil {
		provider.ModelMappings = req.ModelMappings
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
	if provider.MultimodalSwitch && strings.TrimSpace(provider.MultimodalModel) == "" {
		http.Error(w, `{"error": "multimodal_model is required when multimodal_switch is enabled"}`, http.StatusBadRequest)
		return
	}
	provider.UpdatedAt = time.Now()

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"provider": provider,
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
// 匹配: /api/providers/{id}, /api/providers/{id}/activate, /api/providers/{id}/toggle, /api/providers/{id}/test
func (s *Server) handleProviderRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

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
		ID:               generateProviderID(),
		Name:             provider.Name + " 复制",
		APIURL:           provider.APIURL,
		APIToken:         provider.APIToken,
		ModelMappings:    provider.ModelMappings,
		SupportsThinking: provider.SupportsThinking,
		MultimodalSwitch: provider.MultimodalSwitch,
		MultimodalModel:  provider.MultimodalModel,
		Enabled:          true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	cfg.Providers = append(cfg.Providers, newProvider)

	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"provider": newProvider,
	})
}

// generateProviderID 生成唯一的供应商 ID
func generateProviderID() string {
	return "provider-" + randomHex(8) + "-" + randomHex(4)
}

// randomHex 生成指定长度的十六进制字符串
func randomHex(length int) string {
	b := make([]byte, (length+1)/2)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", length)
	}
	return hex.EncodeToString(b)[:length]
}
