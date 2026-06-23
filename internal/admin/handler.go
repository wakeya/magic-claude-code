package admin

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/usage"
	"magic-claude-code/internal/version"
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
			"backend_url":     "https://open.bigmodel.cn/api/anthropic",
			"connection_mode": config.ConnectionModeTransparent,
		})
		return
	}

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"backend_url":         config.RedactURL(cfg.BackendURL),
		"connection_mode":     cfg.ConnectionMode,
		"gateway_listen_addr": cfg.GatewayListenAddr,
		"gateway_listen_port": cfg.GatewayListenPort,
	})
}

// updateConfig 更新配置
func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BackendURL        string `json:"backend_url"`
		ConnectionMode    string `json:"connection_mode"`
		GatewayListenAddr string `json:"gateway_listen_addr"`
		GatewayListenPort int    `json:"gateway_listen_port"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	if req.BackendURL == "" && req.ConnectionMode == "" && req.GatewayListenAddr == "" && req.GatewayListenPort == 0 {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}
	if s.configStore == nil {
		http.Error(w, `{"error": "config store not available"}`, http.StatusInternalServerError)
		return
	}
	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}
	if req.BackendURL != "" {
		cfg.BackendURL = req.BackendURL
	}
	if req.ConnectionMode != "" {
		switch req.ConnectionMode {
		case config.ConnectionModeTransparent, config.ConnectionModeTunnel, config.ConnectionModeGateway:
			cfg.ConnectionMode = req.ConnectionMode
		default:
			http.Error(w, `{"error": "invalid connection_mode"}`, http.StatusBadRequest)
			return
		}
	}
	if req.GatewayListenAddr != "" {
		cfg.GatewayListenAddr = req.GatewayListenAddr
	}
	if req.GatewayListenPort > 0 {
		cfg.GatewayListenPort = req.GatewayListenPort
	}
	if err := cfg.Validate(); err != nil {
		http.Error(w, `{"error": "invalid config"}`, http.StatusBadRequest)
		return
	}
	if err := s.configStore.Save(cfg); err != nil {
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}
	s.modeMu.Lock()
	if s.config != nil {
		s.config.ConfiguredMode = cfg.ConnectionMode
	}
	s.modeMu.Unlock()

	resp := map[string]any{
		"success":             true,
		"backend_url":         config.RedactURL(cfg.BackendURL),
		"connection_mode":     cfg.ConnectionMode,
		"gateway_listen_addr": cfg.GatewayListenAddr,
		"gateway_listen_port": cfg.GatewayListenPort,
	}

	if s.gatewayRestarter != nil {
		addr := net.JoinHostPort(cfg.GatewayListenAddr, strconv.Itoa(cfg.GatewayListenPort))
		if err := s.gatewayRestarter.RestartGateway(addr); err != nil {
			resp["gateway_restart_failed"] = err.Error()
		} else {
			resp["gateway_restarted"] = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleStatus 处理状态请求
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	cfg := config.DefaultConfig()
	if s.configStore != nil {
		if loaded, err := s.configStore.Load(); err == nil {
			cfg = loaded
		}
	}
	if listenCfg := s.listenState(); listenCfg != nil {
		cfg.ProxyListenAddr = listenCfg.ProxyListenAddr
		cfg.ProxyPort = listenCfg.ProxyPort
		cfg.AdminListenAddr = listenCfg.AdminListenAddr
		cfg.AdminPort = listenCfg.AdminPort
	}
	backendURL := config.RedactURL(cfg.BackendURL)

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

	configuredMode, effectiveMode, modeRationale := s.modeState()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"running":                 true,
		"version":                 version.Version,
		"backend_url":             backendURL,
		"proxy_listen_addr":       cfg.ProxyListenAddr,
		"proxy_port":              cfg.ProxyPort,
		"admin_listen_addr":       cfg.AdminListenAddr,
		"admin_port":              cfg.AdminPort,
		"gateway_listen_addr":     cfg.GatewayListenAddr,
		"gateway_listen_port":     cfg.GatewayListenPort,
		"configured_mode":         configuredMode,
		"effective_mode":          effectiveMode,
		"mode_rationale":          modeRationale,
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
	dataDir := "./data"
	if s.config != nil && s.config.ConfigPath != "" {
		dataDir = filepath.Dir(s.config.ConfigPath)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ca_cert_path":      filepath.Join(dataDir, "ca.crt"),
		"server_cert_path":  filepath.Join(dataDir, "server.crt"),
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

// isInternalIP checks whether the host resolves to an internal or loopback address.
// It performs DNS resolution to detect DNS rebinding attacks and covers both IPv4 and IPv6.
func isInternalIP(host string) bool {
	if host == "localhost" {
		return true
	}
	// Try parsing as IP first (fast path, no DNS lookup)
	if ip := net.ParseIP(host); ip != nil {
		return isReservedIP(ip)
	}
	// Hostname: resolve and check all resulting IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		return true // unresolvable host -> block
	}
	for _, ip := range ips {
		if isReservedIP(ip) {
			return true
		}
	}
	return false
}

// isReservedIP reports whether an IP is loopback, private, link-local, or unspecified.
func isReservedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
