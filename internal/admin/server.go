package admin

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"claude_code_proxy_dns/internal/config"
	"claude_code_proxy_dns/internal/usage"
)

// StatsProvider 统计数据提供者接口
type StatsProvider interface {
	Stats() (total int64, last time.Time, uptime time.Duration)
}

// Server 配置服务
type Server struct {
	config        *AdminConfig
	auth          *Auth
	server        *http.Server
	startTime     time.Time
	configStore   config.ConfigStore
	statsProvider StatsProvider
	usageHandler  *usage.Handler
}

// AdminConfig 配置服务配置
type AdminConfig struct {
	Password          string
	CertFile          string
	KeyFile           string
	ConfigPath        string
	ClaudeProjectsDir string
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

	// 供应商 API 路由
	mux.HandleFunc("/api/providers", s.authMiddlewareFunc(s.handleProviders))
	mux.HandleFunc("/api/providers/test", s.authMiddlewareFunc(s.handleTestProvider))
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

		// 静态资源（/assets/*）无需认证，直接放行
		if strings.HasPrefix(r.URL.Path, "/assets/") {
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
