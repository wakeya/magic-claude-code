package proxy

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// Config 代理配置
type Config struct {
	BackendURL string
}

// Server 代理服务器
type Server struct {
	config    *Config
	server    *http.Server
	transport *http.Transport

	// 统计
	requestsTotal atomic.Int64
	lastRequest   atomic.Value // time.Time
	startTime     time.Time
}

// NewServer 创建代理服务器
func NewServer(cfg *Config) *Server {
	return &Server{
		config:    cfg,
		startTime: time.Now(),
		transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
			},
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// Start 启动代理服务器
func (s *Server) Start(addr string, certFile, keyFile string) error {
	handler := NewHandler(s.config)

	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.withStats(handler),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Proxy server starting on %s", addr)
	return s.server.ListenAndServeTLS(certFile, keyFile)
}

// withStats 添加统计中间件
func (s *Server) withStats(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requestsTotal.Add(1)
		s.lastRequest.Store(time.Now())
		next.ServeHTTP(w, r)
	})
}

// Stop 停止代理服务器
func (s *Server) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// Stats 返回统计信息
func (s *Server) Stats() (total int64, last time.Time, uptime time.Duration) {
	total = s.requestsTotal.Load()
	if v := s.lastRequest.Load(); v != nil {
		last = v.(time.Time)
	}
	uptime = time.Since(s.startTime)
	return
}