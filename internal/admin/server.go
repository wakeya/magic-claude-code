package admin

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/updater"
	"magic-claude-code/internal/usage"
)

// StatsProvider 统计数据提供者接口
type StatsProvider interface {
	Stats() (total int64, last time.Time, uptime time.Duration)
}

// GatewayRestarter 路由模式 HTTP 服务器重启接口
type GatewayRestarter interface {
	RestartGateway(addr string) error
}

// Server 配置服务
type Server struct {
	config                     *AdminConfig
	auth                       *Auth
	server                     *http.Server
	startTime                  time.Time
	configStore                config.ConfigStore
	listenMu                   sync.RWMutex
	effectiveListen            *config.Config
	statsProvider              StatsProvider
	usageHandler               *usage.Handler
	updater                    *updater.Updater
	updateApplyDisabledMessage string
	gatewayRestarter           GatewayRestarter
	modeMu                     sync.RWMutex
}

// AdminConfig 配置服务配置
type AdminConfig struct {
	Password          string
	CertFile          string
	KeyFile           string
	ConfigPath        string
	ClaudeProjectsDir string
	ConfiguredMode    string
	EffectiveMode     string
	ModeRationale     string
}

// NewServer 创建配置服务
func NewServer(cfg *AdminConfig, configStore config.ConfigStore, statsProvider StatsProvider, usageHandlers ...*usage.Handler) *Server {
	server := &Server{
		config:        cfg,
		auth:          NewAuth(cfg.Password),
		startTime:     time.Now(),
		configStore:   configStore,
		statsProvider: statsProvider,
	}
	if len(usageHandlers) > 0 {
		server.usageHandler = usageHandlers[0]
	}
	return server
}

// Start 启动配置服务
func (s *Server) Start(addr string, frontendFS embed.FS) error {
	// 创建路由
	mux := http.NewServeMux()

	// 静态文件
	staticFS, _ := fs.Sub(frontendFS, "dist")
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("/", s.authMiddleware(fileServer))

	// API 路由
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/config", s.authMiddlewareFunc(s.handleConfig))
	mux.HandleFunc("/api/preferences", s.authMiddlewareFunc(s.handlePreferences))
	mux.HandleFunc("/api/status", s.authMiddlewareFunc(s.handleStatus))
	mux.HandleFunc("/api/certificates", s.authMiddlewareFunc(s.handleCertificates))
	mux.HandleFunc("/api/config/test", s.authMiddlewareFunc(s.handleTestBackend))
	mux.HandleFunc("/api/update/check", s.authMiddlewareFunc(s.handleUpdateCheck))
	mux.HandleFunc("/api/update/apply", s.authMiddlewareFunc(s.handleUpdateApply))

	// 供应商 API 路由
	mux.HandleFunc("/api/providers", s.authMiddlewareFunc(s.handleProviders))
	mux.HandleFunc("/api/providers/test", s.authMiddlewareFunc(s.handleTestProvider))
	mux.HandleFunc("/api/providers/export", s.authMiddlewareFunc(s.handleExportProviders))
	// mux.HandleFunc("/api/providers/import", s.authMiddlewareFunc(s.handleImportProviders)) // Task 2
	mux.HandleFunc("/api/providers/", s.authMiddlewareFunc(s.handleProviderRoutes))
	// net/http ServeMux uses longest-pattern matching; keep exact session routes before the subtree handler for readability.
	mux.HandleFunc("/api/sessions", s.authMiddlewareFunc(s.handleSessions))
	mux.HandleFunc("/api/sessions/projects", s.authMiddlewareFunc(s.handleSessionProjects))
	mux.HandleFunc("/api/sessions/", s.authMiddlewareFunc(s.handleSessionRoutes))
	if s.usageHandler != nil {
		s.usageHandler.Register(mux, s.authMiddlewareFunc)
	}

	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Admin server starting on %s", addr)
	return s.server.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
}

// Stop 停止配置服务
func (s *Server) Stop(ctx context.Context) error {
	// 停止 session 清理 goroutine
	s.auth.Close()

	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// authMiddleware 认证中间件（用于静态文件和 SPA 路由）
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API 路由单独处理
		if strings.HasPrefix(r.URL.Path, "/api") {
			next.ServeHTTP(w, r)
			return
		}

		// 静态资源（/assets/* 及根路径下的图片/favicon）无需认证，直接放行
		if strings.HasPrefix(r.URL.Path, "/assets/") ||
			strings.HasSuffix(r.URL.Path, ".png") ||
			strings.HasSuffix(r.URL.Path, ".ico") ||
			strings.HasSuffix(r.URL.Path, ".svg") {
			next.ServeHTTP(w, r)
			return
		}

		// 所有页面路由返回 index.html，由 Vue Router 处理客户端路由和认证跳转
		r.URL.Path = "/"
		next.ServeHTTP(w, r)
	})
}

// authMiddlewareFunc 认证中间件（用于 API）
func (s *Server) authMiddlewareFunc(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil || !s.auth.ValidateToken(cookie.Value) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		next(w, r)
	}
}

// GetAuth 获取认证管理器
func (s *Server) GetAuth() *Auth {
	return s.auth
}

// SetUpdater 注入更新器，使 /api/update/* 端点可用
func (s *Server) SetUpdater(u *updater.Updater) {
	s.updater = u
}

// SetGatewayRestarter 注入路由模式重启器，使配置变更后能平滑重启 gateway HTTP 服务
func (s *Server) SetGatewayRestarter(r GatewayRestarter) {
	s.gatewayRestarter = r
}

// SetEffectiveListenState records the proxy/admin listen addresses/ports that
// are actually used at startup after CLI/env/config resolution.
func (s *Server) SetEffectiveListenState(cfg *config.Config) {
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	if cfg == nil {
		s.effectiveListen = nil
		return
	}
	s.effectiveListen = &config.Config{
		ProxyListenAddr: cfg.ProxyListenAddr,
		ProxyPort:       cfg.ProxyPort,
		AdminListenAddr: cfg.AdminListenAddr,
		AdminPort:       cfg.AdminPort,
	}
}

// DisableUpdateApply keeps update checks available while preventing in-place binary replacement.
func (s *Server) DisableUpdateApply(message string) {
	s.updateApplyDisabledMessage = message
}

func (s *Server) setModeState(configured, effective, rationale string) {
	s.modeMu.Lock()
	defer s.modeMu.Unlock()
	if s.config != nil {
		s.config.ConfiguredMode = configured
		s.config.EffectiveMode = effective
		s.config.ModeRationale = rationale
	}
}

func (s *Server) currentEffectiveMode() string {
	s.modeMu.RLock()
	defer s.modeMu.RUnlock()
	if s.config == nil {
		return ""
	}
	return s.config.EffectiveMode
}

func (s *Server) currentModeRationale() string {
	s.modeMu.RLock()
	defer s.modeMu.RUnlock()
	if s.config == nil {
		return ""
	}
	return s.config.ModeRationale
}

func (s *Server) modeState() (configured, effective, rationale string) {
	s.modeMu.RLock()
	defer s.modeMu.RUnlock()
	if s.config == nil {
		return "", "", ""
	}
	return s.config.ConfiguredMode, s.config.EffectiveMode, s.config.ModeRationale
}

func (s *Server) listenState() *config.Config {
	s.listenMu.RLock()
	defer s.listenMu.RUnlock()
	if s.effectiveListen == nil {
		return nil
	}
	cfg := *s.effectiveListen
	return &cfg
}
