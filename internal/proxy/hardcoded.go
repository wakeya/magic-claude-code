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
		"/api/event_logging/v2/batch",
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
		"/v1/messages/count_tokens",
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

	// Desktop 更新探测：/api/desktop/**/update
	if strings.HasPrefix(path, "/api/desktop/") && strings.HasSuffix(path, "/update") {
		return true
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

	// count_tokens 需要读取请求体来估算 token 数，单独处理（在 drain 之前）
	if path == "/v1/messages/count_tokens" {
		h.handleCountTokens(w, r)
		return true
	}

	// Desktop 更新探测：方法白名单检查在 drain 之前，避免对非 HEAD/GET 请求 drain body
	if strings.HasPrefix(path, "/api/desktop/") && strings.HasSuffix(path, "/update") {
		if r.Method != http.MethodHead && r.Method != http.MethodGet {
			w.Header().Set("Allow", "HEAD, GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
	}

	// 消耗请求体以确保连接可复用
	drainRequestBody(r)

	log.Printf("[Hardcoded] Handling %s %s", r.Method, path)

	switch {
	// 反馈提交 - POST /api/claude_cli_feedback
	case path == "/api/claude_cli_feedback":
		h.handleFeedback(w)
		return true

	// 指标上报 - POST /api/claude_code/metric
	case strings.HasPrefix(path, "/api/claude_code/metric"):
		h.handleMetric(w)
		return true

	// 组织指标开关 - GET /api/claude_code/organizations/metrics_enabled
	case path == "/api/claude_code/organizations/metrics_enabled":
		h.handleMetricsEnabled(w)
		return true

	// 组织信息 - GET /api/claude_code/organization
	case strings.HasPrefix(path, "/api/claude_code/organization"):
		h.handleOrganization(w)
		return true

	// 会话记录共享 - POST /api/claude_code_shared_session_transcripts
	case path == "/api/claude_code_shared_session_transcripts":
		h.handleSessionTranscripts(w)
		return true

	// 域名信息 - GET /api/web/domain_info?domain=xxx
	case strings.HasPrefix(path, "/api/web/domain_info"):
		h.handleDomainInfo(w)
		return true

	// 创建 API 密钥 - POST /api/oauth/claude_cli/create_api_key
	case path == "/api/oauth/claude_cli/create_api_key":
		h.handleCreateAPIKey(w)
		return true

	// 角色信息 - GET /api/oauth/claude_cli/roles
	case path == "/api/oauth/claude_cli/roles":
		h.handleRole(w)
		return true

	// 用户信息 - GET /v1/me
	case path == "/v1/me":
		h.handleMe(w)
		return true

	// 第一方事件批量上报 - POST /api/event_logging/batch
	case path == "/api/event_logging/batch",
		path == "/api/event_logging/v2/batch":
		h.handleEventLogging(w)
		return true

	// GrowthBook 特性开关 - GET /api/feature/*
	case strings.HasPrefix(path, "/api/feature/"):
		h.handleGrowthBookFeature(w)
		return true

	// 启动引导配置 - GET /api/claude_cli/bootstrap
	case path == "/api/claude_cli/bootstrap":
		h.handleBootstrap(w)
		return true

	// MCP 注册表 - GET /mcp-registry/*
	case strings.HasPrefix(path, "/mcp-registry/"):
		h.handleMCPRegistry(w)
		return true

	// claude.ai MCP 服务器列表 - GET /v1/mcp_servers
	case path == "/v1/mcp_servers":
		h.handleMCPServers(w)
		return true

	// Fast Mode 配置 - GET /api/claude_code_penguin_mode
	case path == "/api/claude_code_penguin_mode":
		h.handlePenguinMode(w)
		return true

	// Desktop 更新探测 - HEAD/GET /api/desktop/**/update
	case strings.HasPrefix(path, "/api/desktop/") && strings.HasSuffix(path, "/update"):
		h.handleDesktopUpdate(w, r)
		return true

	// 策略限制 - GET /api/claude_code/policy_limits
	case path == "/api/claude_code/policy_limits":
		h.handlePolicyLimits(w)
		return true

	// 远程设置 - GET /api/claude_code/settings
	case path == "/api/claude_code/settings":
		h.handleRemoteSettings(w)
		return true

	// 低优先级端点 - 统一返回空 JSON
	case path == "/api/oauth/profile",
			path == "/api/claude_cli_profile",
			path == "/api/oauth/usage",
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
			h.handleEmptyResponse(w)
			return true
	}

	return false
}

// handleFeedback 处理反馈提交
// 响应格式: { "feedback_id": "xxx" }
func (h *Handler) handleFeedback(w http.ResponseWriter) {
	response := map[string]interface{}{
		"feedback_id": generateID(),
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleMetric 处理指标上报
// 响应格式: { "success": true }
func (h *Handler) handleMetric(w http.ResponseWriter) {
	response := map[string]any{
		"success": true,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleMetricsEnabled 处理组织指标开关请求
// 源码期望字段名: metrics_logging_enabled (不是 metrics_enabled)
func (h *Handler) handleMetricsEnabled(w http.ResponseWriter) {
	response := map[string]any{
		"metrics_logging_enabled": false,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleOrganization 处理组织信息请求
// 响应格式: { "metrics_enabled": false } 或空对象
func (h *Handler) handleOrganization(w http.ResponseWriter) {
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
func (h *Handler) handleSessionTranscripts(w http.ResponseWriter) {
	response := map[string]interface{}{
		"success":        true,
		"transcript_id":  generateID(),
		"share_url":      "",
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleDomainInfo 处理域名信息请求
// 响应格式: { "can_fetch": true }
func (h *Handler) handleDomainInfo(w http.ResponseWriter) {
	// 默认允许所有域名
	response := map[string]interface{}{
		"can_fetch": true,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleCreateAPIKey 处理创建 API 密钥请求
// 响应格式: { "api_key": "xxx", "created_at": "..." }
func (h *Handler) handleCreateAPIKey(w http.ResponseWriter) {
	response := map[string]interface{}{
		"api_key":    "sk-ant-api03-local-proxy-" + generateID(),
		"created_at": "2026-03-11T00:00:00Z",
		"type":       "api_key",
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleRole 处理角色信息请求
// 响应格式: { "role": "user", "permissions": [] }
func (h *Handler) handleRole(w http.ResponseWriter) {
	response := map[string]interface{}{
		"role":        "user",
		"permissions": []string{},
		"can_upgrade": false,
	}

	writeJSONResponse(w, http.StatusOK, response)
}

// handleMe 处理用户信息请求
// 响应格式: { "id": "xxx", "type": "user", ... }
func (h *Handler) handleMe(w http.ResponseWriter) {
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
func (h *Handler) handleEmptyResponse(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{})
}

// handleCountTokens 处理 token 计数请求，本地估算后直接返回
// Claude Code 用此端点管理上下文窗口，第三方上游不支持此接口
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize+1))
	r.Body.Close()
	if err != nil {
		log.Printf("[Hardcoded] Error reading count_tokens body: %v", err)
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	if len(body) > maxRequestBodySize {
		log.Printf("[Hardcoded] count_tokens body too large: %d bytes", len(body))
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	estimated := max(1, len(body)/4)
	log.Printf("[Hardcoded] Handling %s %s: size=%d estimated_tokens=%d", r.Method, r.URL.Path, len(body), estimated)

	writeJSONResponse(w, http.StatusOK, map[string]any{
		"input_tokens": estimated,
	})
}

// handleEventLogging 处理第一方事件批量上报
// 源码只检查 HTTP 状态码，不检查响应体
func (h *Handler) handleEventLogging(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{})
}

// handleGrowthBookFeature 处理 GrowthBook 特性开关请求
// 返回优化后的特性配置，启用有益功能、禁用遥测和有害 A/B 测试 flag。
// 非空 features 使 processRemoteEvalPayload 走正常处理路径并缓存到 ~/.claude.json
func (h *Handler) handleGrowthBookFeature(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"features": optimizedGrowthBookFeatures(),
	})
}

// optimizedGrowthBookFeatures 返回基于 Claude Code 源码分析的优化特性配置。
//
// 分三类：
//   - 启用：defaultValue=false 但对用户有益的功能（源码分析得出）
//   - 禁用：服务端 A/B 测试可能推送 true 的有害 flag（GitHub issues #62205/#25141）
//   - 不包含：基础设施/远程/内部专用 flag，让它们走 defaultValue
func optimizedGrowthBookFeatures() map[string]any {
	return map[string]any{
		// 启用有益功能（defaultValue=false，源码分析）
		"tengu_coral_fern":   true, // 记忆上下文搜索
		"tengu_moth_copse":   true, // 附件优化
		"tengu_glacier_2xr":  true, // 工具搜索推荐
		"tengu_copper_panda": true, // 技能改进建议
		"tengu_hive_evidence": true, // 验证代理
		"tengu_basalt_3kr":   true, // MCP 指令增量
		"tengu_amber_prism":  true, // 消息优化

		// 禁用遥测（GitHub issue #25141 证实服务端推送 true）
		"tengu_log_datadog_events":  false,
		"tengu_log_segment_events":  false,
		"enhanced_telemetry_beta":   false,

		// 禁用有害 A/B 测试（GitHub issue #62205 证实服务端推送 true 锁权限）
		"tengu_permission_friction":  false,
		"tengu_harbor":               false,
		"tengu_harbor_permissions":   false,
	}
}

// handleBootstrap 处理启动引导配置
// 源码期望 client_data + additional_model_options + cwk_cfg_key
func (h *Handler) handleBootstrap(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"client_data":            map[string]any{},
		"additional_model_options": []any{},
		"cwk_cfg_key":            nil,
	})
}

// handleMCPRegistry 处理 MCP 注册表请求
// 源码期望 servers 列表，空数组使 isOfficialMcpUrl 返回 false
func (h *Handler) handleMCPRegistry(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"servers": []any{},
	})
}

// handleMCPServers 处理 claude.ai MCP 服务器列表
// 源码期望 data + has_more，空数组使无 claude.ai 连接器加载
func (h *Handler) handleMCPServers(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"data":     []any{},
		"has_more": false,
	})
}

// handlePenguinMode 处理 Fast Mode 配置
// 源码期望配置对象，空响应使 Fast Mode 不可用
func (h *Handler) handlePenguinMode(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{})
}

// desktopCurrentRelease 是 Desktop 更新探测返回的当前版本号
const desktopCurrentRelease = "1.13576.0"

// handleDesktopUpdate 处理 Desktop 更新探测请求
// HEAD 返回 200 空响应；GET 返回顶层 currentRelease 字段告知"已是最新"
func (h *Handler) handleDesktopUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodHead && r.Method != http.MethodGet {
		w.Header().Set("Allow", "HEAD, GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"currentRelease": desktopCurrentRelease,
	})
}

// handlePolicyLimits 处理策略限制请求
// 源码校验 restrictions 为非空对象、compliance_taints 为数组
func (h *Handler) handlePolicyLimits(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"restrictions":      map[string]any{},
		"compliance_taints": []any{},
	})
}

// handleRemoteSettings 处理远程设置请求
// 源码期望 data.settings 为对象
func (h *Handler) handleRemoteSettings(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusOK, map[string]any{
		"settings": map[string]any{},
	})
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