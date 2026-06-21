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
)

// Server 代理服务器
type Server struct {
	configStore config.ConfigStore
	server      *http.Server
	transport   *http.Transport
	recorder    UsageRecorder

	// 统计
	requestsTotal atomic.Int64
	lastRequest   atomic.Value // time.Time
	startTime     time.Time
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

// Start 启动代理服务器
func (s *Server) Start(addr string, certFile, keyFile string) error {
	handler := NewHandler(s.configStore, s.transport, s.recorder)

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

	conn.SetDeadline(time.Now().Add(l.handshakeTimeout))
	tlsConn := tls.Server(conn, l.tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		addr := conn.RemoteAddr().String()
		if sniVal, ok := l.sniStore.LoadAndDelete(addr); ok {
			l.logger.Printf("TLS handshake error from %s (SNI=%s): %v", addr, sniVal, err)
		} else {
			l.logger.Printf("TLS handshake error from %s (no SNI): %v", addr, err)
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
