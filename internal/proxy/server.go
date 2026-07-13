package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/failover"
)

// Server 代理服务器
type Server struct {
	configStore config.ConfigStore
	server      *http.Server
	transport   *http.Transport
	recorder    UsageRecorder
	// failoverManager 注入后，模型推理端点的默认路由在 >=400 时可自动切换全局默认供应商。
	failoverManager *failover.Manager

	// 统计
	requestsTotal atomic.Int64
	lastRequest   atomic.Value // time.Time
	startTime     time.Time

	// Gateway HTTP server (route mode, non-TLS)
	gatewayMu     sync.Mutex
	gatewayServer *http.Server
	gatewayAddr   string // 当前监听地址，用于 RestartGateway 跳过无变化的重启
}

// NewServer 创建代理服务器
func NewServer(store config.ConfigStore, recorders ...UsageRecorder) *Server {
	server := &Server{
		configStore: store,
		startTime:   time.Now(),
		transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
			},
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	if len(recorders) > 0 {
		server.recorder = recorders[0]
	}
	return server
}

// SetFailoverManager 注入故障切换管理器，使所有 Handler 实例（TLS / gateway / restart）
// 都获得同一管理器。未注入时自动故障切换保持关闭。
func (s *Server) SetFailoverManager(m *failover.Manager) {
	s.failoverManager = m
}

// newConfiguredHandler 创建并配置一个带故障切换管理器的 Handler。
func (s *Server) newConfiguredHandler() *Handler {
	h := NewHandler(s.configStore, s.transport, s.recorder)
	if s.failoverManager != nil {
		h.SetFailoverManager(s.failoverManager)
	}
	return h
}

// Start 启动代理服务器
func (s *Server) Start(addr string, certFile, keyFile string) error {
	handler := s.newConfiguredHandler()

	certPair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load TLS key pair: %w", err)
	}

	sniStore := &sync.Map{}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.Conn != nil && hello.ServerName != "" {
				sniStore.Store(hello.Conn.RemoteAddr().String(), hello.ServerName)
			}
			return &certPair, nil
		},
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	tlsLn := newTLSListener(ln, tlsCfg, sniStore, defaultHandshakeTimeout, defaultMaxHandshakes, log.Default())

	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.withStats(handler),
		ReadTimeout:  5 * time.Minute,  // AI API 请求可能需要较长时间
		WriteTimeout: 10 * time.Minute, // 流式响应可能需要较长时间
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Proxy server starting on %s", addr)
	return s.server.Serve(tlsLn)
}

const (
	defaultHandshakeTimeout = 10 * time.Second
	defaultMaxHandshakes    = 100
)

// tlsListener 异步执行 TLS 握手，每个连接的握手在独立 goroutine 中进行，
// 避免单个慢握手阻塞整个 Accept 循环导致 listener starvation。
// handshakeTimeout 限制单次握手持续时间，maxHandshakes 限制并发握手数量，
// 防止攻击者通过大量半开连接耗尽 goroutine 和内存。
type tlsListener struct {
	ln               net.Listener
	tlsCfg           *tls.Config
	sniStore         *sync.Map
	handshakeTimeout time.Duration
	maxHandshakes    int32

	logger           *log.Logger
	inflight atomic.Int32
	wg       sync.WaitGroup
	acceptMu sync.Mutex
	// connCh intentionally stays open: handleConn goroutines that passed the
	// closed check before Close() may still send to it. Closing connCh would
	// panic those goroutines. Instead, Close() calls wg.Wait() to ensure all
	// handleConn goroutines have exited, then drains connCh synchronously.
	connCh    chan net.Conn
	closeOnce sync.Once
	closed    chan struct{}
}

func newTLSListener(ln net.Listener, tlsCfg *tls.Config, sniStore *sync.Map, handshakeTimeout time.Duration, maxHandshakes int32, logger *log.Logger) *tlsListener {
	if logger == nil {
		logger = log.Default()
	}
	l := &tlsListener{
		ln:               ln,
		tlsCfg:           tlsCfg,
		sniStore:         sniStore,
		handshakeTimeout: handshakeTimeout,
		maxHandshakes:    maxHandshakes,
		logger:           logger,
		connCh:           make(chan net.Conn, 128),
		closed:           make(chan struct{}),
	}
	go l.acceptLoop()
	return l
}

func (l *tlsListener) acceptLoop() {
	defer l.Close()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return
		}
		if l.inflight.Add(1) > l.maxHandshakes {
			l.inflight.Add(-1)
			conn.Close()
			continue
		}
		l.acceptMu.Lock()
		select {
		case <-l.closed:
			l.acceptMu.Unlock()
			l.inflight.Add(-1)
			conn.Close()
			return
		default:
		}
		l.wg.Add(1)
		l.acceptMu.Unlock()
		go l.handleConn(conn)
	}
}

func (l *tlsListener) handleConn(conn net.Conn) {
	defer l.inflight.Add(-1)
	defer l.wg.Done()

	// 增量解析客户端字节流，握手失败时检测客户端发来的明文 alert（如 unknown_ca）。
	// 必要性：客户端校验证书失败后会发明文 fatal alert，代理用 handshake key 解这条
	// 明文 alert 必然 AEAD 失败、日志误报为 "bad record MAC"。alertDetectingConn 让
	// 真实原因（客户端主动拒绝）被如实记录。
	ac := &alertDetectingConn{Conn: conn}
	conn = ac

	conn.SetDeadline(time.Now().Add(l.handshakeTimeout))
	tlsConn := tls.Server(conn, l.tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		addr := conn.RemoteAddr().String()
		extra := ac.hint()
		if sniVal, ok := l.sniStore.LoadAndDelete(addr); ok {
			l.logger.Printf("TLS handshake error from %s (SNI=%s): %v%s", addr, sniVal, err, extra)
		} else {
			l.logger.Printf("TLS handshake error from %s (no SNI): %v%s", addr, err, extra)
		}
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})
	l.sniStore.Delete(conn.RemoteAddr().String())
	select {
	case <-l.closed:
		conn.Close()
	default:
		select {
		case l.connCh <- tlsConn:
		case <-l.closed:
			conn.Close()
		}
	}
}

func (l *tlsListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case conn, ok := <-l.connCh:
		if !ok {
			return nil, net.ErrClosed
		}
		return conn, nil
	}
}

func (l *tlsListener) Close() error {
	l.closeOnce.Do(func() {
		l.acceptMu.Lock()
		close(l.closed)
		l.acceptMu.Unlock()
		l.ln.Close()
		l.wg.Wait()
		l.drainConnCh()
	})
	return nil
}

func (l *tlsListener) drainConnCh() {
	for {
		select {
		case conn := <-l.connCh:
			conn.Close()
		default:
			return
		}
	}
}

func (l *tlsListener) Addr() net.Addr { return l.ln.Addr() }

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
	var err error
	if s.server != nil {
		err = s.server.Shutdown(ctx)
	}
	s.gatewayMu.Lock()
	gw := s.gatewayServer
	s.gatewayServer = nil
	s.gatewayAddr = ""
	s.gatewayMu.Unlock()
	if gw != nil {
		gw.Shutdown(ctx)
	}
	return err
}

// StartGateway 启动路由模式 HTTP 服务器（阻塞调用，应在 goroutine 中运行）
func (s *Server) StartGateway(addr string) error {
	handler := s.newConfiguredHandler()

	s.gatewayMu.Lock()
	s.gatewayServer = &http.Server{
		Addr:         addr,
		Handler:      s.withStats(handler),
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}
	s.gatewayAddr = addr
	s.gatewayMu.Unlock()

	log.Printf("Gateway server starting on %s", addr)
	err := s.gatewayServer.ListenAndServe()
	// ListenAndServe 返回 nil 只可能是 http.ErrServerClosed（Shutdown 触发）；
	// 其他错误说明端口绑定失败，server 未真正运行，清理状态避免 RestartGateway 误判跳过。
	if err != nil && err != http.ErrServerClosed {
		s.gatewayMu.Lock()
		s.gatewayServer = nil
		s.gatewayAddr = ""
		s.gatewayMu.Unlock()
	}
	return err
}

// RestartGateway 平滑重启路由模式 HTTP 服务器。
// 地址未变化时直接返回（避免与正在运行的旧实例撞端口）；
// 地址变化时同步绑定新端口（检测冲突），异步关闭旧服务器。
func (s *Server) RestartGateway(addr string) error {
	s.gatewayMu.Lock()
	current := s.gatewayAddr
	running := s.gatewayServer != nil
	s.gatewayMu.Unlock()

	// 地址未变化且 server 确实在运行：跳过，避免 "address already in use"。
	// gatewayServer == nil 表示从未启动或已 Stop——即使 addr 相同也不能跳过，
	// 否则配置变更会被静默吞掉（状态失真）。
	if addr == current && current != "" && running {
		log.Printf("Gateway server unchanged on %s, skip restart", addr)
		return nil
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	handler := s.newConfiguredHandler()

	s.gatewayMu.Lock()
	old := s.gatewayServer
	s.gatewayServer = &http.Server{
		Handler:      s.withStats(handler),
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}
	s.gatewayAddr = addr
	s.gatewayMu.Unlock()

	if old != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			old.Shutdown(ctx)
		}()
	}

	log.Printf("Gateway server restarting on %s", addr)
	go func() {
		if err := s.gatewayServer.Serve(ln); err != nil {
			log.Printf("Gateway server error: %v", err)
		}
	}()
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
