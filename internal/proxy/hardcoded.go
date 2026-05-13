package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

// HardcodedEndpoint 硬编码端点处理
// 这些端点在 Claude Code 二进制中硬编码指向 api.anthropic.com
// 需要返回正确的响应格式，避免客户端报错或长时间等待

// isHardcodedEndpoint 检查是否为硬编码端点
func isHardcodedEndpoint(path string) bool {
	// 精确匹配的端点
	exactMatches := []string{
		"/v1/me",
		"/api/claude_cli_feedback",
		"/api/claude_code_shared_session_transcripts",
		"/api/oauth/claude_cli/create_api_key",
		"/api/oauth/claude_cli/roles",
		"/api/claude_code/organizations/metrics_enabled",
		"/api/event_logging/batch",
		"/api/claude_cli/bootstrap",
		"/v1/mcp_servers",
		"/api/claude_code_penguin_mode",
		"/api/oauth/profile",
		"/api/claude_cli_profile",
		"/api/oauth/usage",
		"/api/claude_code/policy_limits",
		"/api/claude_code/settings",
		"/api/claude_code/user_settings",
		"/api/claude_code_grove",
		"/api/organization/claude_code_first_token_date",
		"/v1/ultrareview/quota",
		"/api/claude_code/team_memory",
		"/api/auth/trusted_devices",
		"/api/oauth/file_upload",
	}

	for _, match := range exactMatches {
		if path == match {
			return true
		}
	}

	// 前缀匹配的端点
	prefixMatches := []string{
		"/api/claude_code/metric",
		"/api/claude_code/organization",
		"/api/web/domain_info",
		"/api/feature/",
		"/mcp-registry/",
		"/api/oauth/account/",
		"/v1/session_ingress/session/",
		"/api/oauth/organizations/",
		"/v1/code/sessions/",
	}

	for _, prefix := range prefixMatches {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	return false
}

// handleHardcodedEndpoint 处理硬编码端点请求
func (h *Handler) handleHardcodedEndpoint(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path

	// 快速检查是否为硬编码端点
	if !isHardcodedEndpoint(path) {
		return false
	}

	// 消耗请求体以确保连接可复用
	drainRequestBody(r)

	switch {
	// 反馈提交 - POST /api/claude_cli_feedback
	case path == "/api/claude_cli_feedback":
		h.handleFeedback(w, r)
		return true

	// 指标上报 - POST /api/claude_code/metric
	case strings.HasPrefix(path, "/api/claude_code/metric"):
		h.handleMetric(w, r)
		return true

	// 组织指标开关 - GET /api/claude_code/organizations/metrics_enabled
	case path == "/api/claude_code/organizations/metrics_enabled":
		h.handleMetricsEnabled(w, r)
		return true

	// 组织信息 - GET /api/claude_code/organization
	case strings.HasPrefix(path, "/api/claude_code/organization"):
		h.handleOrganization(w, r)
		return true

	// 会话记录共享 - POST /api/claude_code_shared_session_transcripts
	case path == "/api/claude_code_shared_session_transcripts":
		h.handleSessionTranscripts(w, r)
		return true

	// 域名信息 - GET /api/web/domain_info?domain=xxx
	case strings.HasPrefix(path, "/api/web/domain_info"):
		h.handleDomainInfo(w, r)
		return true

	// 创建 API 密钥 - POST /api/oauth/claude_cli/create_api_key
	case path == "/api/oauth/claude_cli/create_api_key":
		h.handleCreateAPIKey(w, r)
		return true

	// 角色信息 - GET /api/oauth/claude_cli/roles
	case path == "/api/oauth/claude_cli/roles":
		h.handleRole(w, r)
		return true

	// 用户信息 - GET /v1/me
	case path == "/v1/me":
		h.handleMe(w, r)
		return true

	// 第一方事件批量上报 - POST /api/event_logging/batch
	case path == "/api/event_logging/batch":
		h.handleEventLogging(w, r)
		return true

	// GrowthBook 特性开关 - GET /api/feature/*
	case strings.HasPrefix(path, "/api/feature/"):
		h.handleGrowthBookFeature(w, r)
		return true

	// 启动引导配置 - GET /api/claude_cli/bootstrap
	case path == "/api/claude_cli/bootstrap":
		h.handleBootstrap(w, r)
		return true

	// MCP 注册表 - GET /mcp-registry/*
	case strings.HasPrefix(path, "/mcp-registry/"):
		h.handleMCPRegistry(w, r)
		return true

	// claude.ai MCP 服务器列表 - GET /v1/mcp_servers
	case path == "/v1/mcp_servers":
		h.handleMCPServers(w, r)
		return true

	// Fast Mode 配置 - GET /api/claude_code_penguin_mode
	case path == "/api/claude_code_penguin_mode":
		h.handlePenguinMode(w, r)
		return true

		// 低优先级端点 - 统一返回空 JSON
		case path == "/api/oauth/profile",
			path == "/api/claude_cli_profile",
			path == "/api/oauth/usage",
			path == "/api/claude_code/policy_limits",
			path == "/api/claude_code/settings",
			path == "/api/claude_code/user_settings",
			strings.HasPrefix(path, "/api/oauth/account/"),
			path == "/api/claude_code_grove",
			path == "/api/organization/claude_code_first_token_date",
			path == "/v1/ultrareview/quota",
			strings.HasPrefix(path, "/v1/session_ingress/session/"),
			path == "/api/claude_code/team_memory",
			path == "/api/auth/trusted_devices",
			path == "/api/oauth/file_upload",
			strings.HasPrefix(path, "/api/oauth/organizations/"),
			strings.HasPrefix(path, "/v1/code/sessions/"):
			h.handleEmptyResponse(w, r)
			return true
	}

	return false
}

// handleFeedback 处理反馈提交
// 响应格式: { "feedback_id": "xxx" }
func (h *Handler) handleFeedback(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling feedback request: %s", r.URL.Path)

	response := map[string]interface{}{
		"feedback_id": generateID(),
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleMetric 处理指标上报
// 响应格式: { "success": true }
func (h *Handler) handleMetric(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling metric request: %s", r.URL.Path)

	response := map[string]any{
		"success": true,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleMetricsEnabled 处理组织指标开关请求
// 源码期望字段名: metrics_logging_enabled (不是 metrics_enabled)
func (h *Handler) handleMetricsEnabled(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling metrics_enabled request: %s", r.URL.Path)

	response := map[string]any{
		"metrics_logging_enabled": false,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleOrganization 处理组织信息请求
// 响应格式: { "metrics_enabled": false } 或空对象
func (h *Handler) handleOrganization(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling organization request: %s", r.URL.Path)

	// 默认组织信息响应
	response := map[string]any{
		"organization_id":  "local-proxy",
		"metrics_enabled":  false,
		"can_use_otel":     false,
		"has_subscription": false,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleSessionTranscripts 处理会话记录共享
// 响应格式: { "success": true, "transcript_id": "xxx" }
func (h *Handler) handleSessionTranscripts(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling session transcripts request: %s", r.URL.Path)

	response := map[string]interface{}{
		"success":        true,
		"transcript_id":  generateID(),
		"share_url":      "",
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleDomainInfo 处理域名信息请求
// 响应格式: { "can_fetch": true }
func (h *Handler) handleDomainInfo(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	log.Printf("[Hardcoded] Handling domain info request: domain=%s", domain)

	// 默认允许所有域名
	response := map[string]interface{}{
		"can_fetch": true,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleCreateAPIKey 处理创建 API 密钥请求
// 响应格式: { "api_key": "xxx", "created_at": "..." }
func (h *Handler) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling create API key request: %s", r.URL.Path)

	response := map[string]interface{}{
		"api_key":    "sk-ant-api03-local-proxy-" + generateID(),
		"created_at": "2026-03-11T00:00:00Z",
		"type":       "api_key",
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleRole 处理角色信息请求
// 响应格式: { "role": "user", "permissions": [] }
func (h *Handler) handleRole(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling role request: %s", r.URL.Path)

	response := map[string]interface{}{
		"role":        "user",
		"permissions": []string{},
		"can_upgrade": false,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleMe 处理用户信息请求
// 响应格式: { "id": "xxx", "type": "user", ... }
func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling /v1/me request: %s", r.URL.Path)

	response := map[string]interface{}{
		"id":           "user-local-proxy",
		"type":         "user",
		"email":        "local@proxy.dev",
		"display_name": "Local Proxy User",
		"created_at":   "2026-01-01T00:00:00Z",
		"organization": map[string]interface{}{
			"id":   "org-local-proxy",
			"name": "Local Proxy Organization",
		},
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleEmptyResponse 低优先级端点通用处理，返回空 JSON
func (h *Handler) handleEmptyResponse(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling request: %s %s", r.Method, r.URL.Path)
	writeJSONResponse(w, http.StatusOK, map[string]any{})
}

// handleEventLogging 处理第一方事件批量上报
// 源码只检查 HTTP 状态码，不检查响应体
func (h *Handler) handleEventLogging(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling event_logging/batch request: %s %s", r.Method, r.URL.Path)
	writeJSONResponse(w, http.StatusOK, map[string]any{})
}

// handleGrowthBookFeature 处理 GrowthBook 特性开关请求
// 源码期望 GrowthBook SDK 标准响应格式，空 features 触发 fallback 到默认值
func (h *Handler) handleGrowthBookFeature(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling GrowthBook feature request: %s", r.URL.Path)
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"features": map[string]any{},
	})
}

// handleBootstrap 处理启动引导配置
// 源码期望 client_data + additional_model_options，空响应静默跳过
func (h *Handler) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling bootstrap request: %s", r.URL.Path)
	writeJSONResponse(w, http.StatusOK, map[string]any{})
}

// handleMCPRegistry 处理 MCP 注册表请求
// 源码期望 servers 列表，空数组使 isOfficialMcpUrl 返回 false
func (h *Handler) handleMCPRegistry(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling MCP registry request: %s", r.URL.Path)
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"servers": []any{},
	})
}

// handleMCPServers 处理 claude.ai MCP 服务器列表
// 源码期望 data + has_more，空数组使无 claude.ai 连接器加载
func (h *Handler) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling MCP servers request: %s", r.URL.Path)
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"data":     []any{},
		"has_more": false,
	})
}

// handlePenguinMode 处理 Fast Mode 配置
// 源码期望配置对象，空响应使 Fast Mode 不可用
func (h *Handler) handlePenguinMode(w http.ResponseWriter, r *http.Request) {
	log.Printf("[Hardcoded] Handling penguin mode request: %s", r.URL.Path)
	writeJSONResponse(w, http.StatusOK, map[string]any{})
}

// writeJSONResponse 写入 JSON 响应
func writeJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Error encoding JSON response: %v", err)
	}
}

// generateID 生成唯一的 ID 字符串
// 注意：这不是标准 UUID，仅用于生成唯一标识符
func generateID() string {
	return "proxy-" + randomHex(8) + "-" + randomHex(4) + "-" + randomHex(4) + "-" + randomHex(4) + "-" + randomHex(12)
}

// randomHex 生成指定长度的十六进制字符串
func randomHex(length int) string {
	b := make([]byte, (length+1)/2)
	if _, err := rand.Read(b); err != nil {
		// 如果随机数生成失败，使用时间戳作为后备
		return strings.Repeat("0", length)
	}
	return hex.EncodeToString(b)[:length]
}

// drainRequestBody 消耗并关闭请求体，确保 HTTP 连接可复用
func drainRequestBody(r *http.Request) {
	if r.Body != nil {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			log.Printf("Warning: failed to drain request body: %v", err)
		}
		r.Body.Close()
	}
}