package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"claude_code_proxy_dns/internal/usage"
)

// handleLogin 处理登录
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 检查是否被锁定
	if s.auth.IsLocked() {
		time.Sleep(1 * time.Second) // 延迟响应
		http.Error(w, `{"error": "account locked"}`, http.StatusTooManyRequests)
		return
	}

	var req struct {
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	if !s.auth.VerifyPassword(req.Password) {
		s.auth.RecordFailedAttempt()
		time.Sleep(1 * time.Second) // 延迟响应
		http.Error(w, `{"error": "invalid password"}`, http.StatusUnauthorized)
		return
	}

	// 重置失败计数
	s.auth.ResetAttempts()

	// 生成 session token
	token := s.auth.GenerateToken()

	// 设置 cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleLogout 处理登出
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		s.auth.InvalidateToken(cookie.Value)
	}

	// 设置 cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleConfig 处理配置请求
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getConfig(w, r)
	case http.MethodPut:
		s.updateConfig(w, r)
	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// getConfig 获取配置
func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.configStore == nil {
		json.NewEncoder(w).Encode(map[string]string{
			"backend_url": "https://open.bigmodel.cn/api/anthropic",
		})
		return
	}

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"backend_url": cfg.BackendURL,
	})
}

// updateConfig 更新配置
func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BackendURL string `json:"backend_url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	// 验证 URL 格式
	if req.BackendURL != "" {
		if s.configStore == nil {
			http.Error(w, `{"error": "config store not available"}`, http.StatusInternalServerError)
			return
		}
		cfg, err := s.configStore.Load()
		if err != nil {
			http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
			return
		}
		cfg.BackendURL = req.BackendURL
		if err := cfg.Validate(); err != nil {
			http.Error(w, `{"error": "invalid URL format"}`, http.StatusBadRequest)
			return
		}
		if err := s.configStore.Save(cfg); err != nil {
			http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleStatus 处理状态请求
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	backendURL := "https://open.bigmodel.cn/api/anthropic"
	if s.configStore != nil {
		cfg, err := s.configStore.Load()
		if err == nil {
			backendURL = cfg.BackendURL
		}
	}

	// 获取代理服务器统计数据
	var requestsTotal int64
	var lastRequest time.Time
	var uptime time.Duration

	if s.statsProvider != nil {
		requestsTotal, lastRequest, uptime = s.statsProvider.Stats()
	} else {
		uptime = time.Since(s.startTime)
	}
	usageSummary := usage.Summary{}
	if s.usageHandler != nil {
		if summary, err := s.usageHandler.Summary(usage.Filter{TZ: r.URL.Query().Get("tz")}); err == nil {
			usageSummary = summary
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"running":                 true,
		"backend_url":             backendURL,
		"uptime":                  uptime.String(),
		"requests_total":          requestsTotal,
		"last_request":            lastRequest,
		"service_requests_total":  requestsTotal,
		"provider_requests_total": usageSummary.ProviderRequestsTotal,
		"today_provider_requests": usageSummary.TodayProviderRequests,
		"today_token_consumption": usageSummary.TodayTokenConsumption,
		"usage_coverage":          usageSummary.UsageCoverage,
		"last_provider_request":   usageSummary.LastProviderRequest,
	})
}

// handleCertificates 处理证书信息请求
func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ca_cert_path":      "./data/ca.crt",
		"server_cert_path":  "./data/server.crt",
		"ca_expires_at":     time.Now().AddDate(10, 0, 0),
		"server_expires_at": time.Now().AddDate(10, 0, 0),
	})
}

// handleTestBackend 测试后端连接
func (s *Server) handleTestBackend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		BackendURL string `json:"backend_url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	// 解析并验证 URL
	parsedURL, err := url.Parse(req.BackendURL)
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
	resp, err := client.Get(req.BackendURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "connection failed",
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

// isInternalIP 检查是否为内网地址
func isInternalIP(host string) bool {
	// 禁止 localhost 和内网地址
	internalHosts := []string{
		"localhost",
		"127.0.0.1",
		"0.0.0.0",
		"::1",
	}

	for _, h := range internalHosts {
		if host == h {
			return true
		}
	}

	// 检查内网 IP 段
	if strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "192.168.") ||
		strings.HasPrefix(host, "172.16.") ||
		strings.HasPrefix(host, "172.17.") ||
		strings.HasPrefix(host, "172.18.") ||
		strings.HasPrefix(host, "172.19.") ||
		strings.HasPrefix(host, "172.20.") ||
		strings.HasPrefix(host, "172.21.") ||
		strings.HasPrefix(host, "172.22.") ||
		strings.HasPrefix(host, "172.23.") ||
		strings.HasPrefix(host, "172.24.") ||
		strings.HasPrefix(host, "172.25.") ||
		strings.HasPrefix(host, "172.26.") ||
		strings.HasPrefix(host, "172.27.") ||
		strings.HasPrefix(host, "172.28.") ||
		strings.HasPrefix(host, "172.29.") ||
		strings.HasPrefix(host, "172.30.") ||
		strings.HasPrefix(host, "172.31.") ||
		strings.HasPrefix(host, "169.254.") {
		return true
	}

	return false
}
