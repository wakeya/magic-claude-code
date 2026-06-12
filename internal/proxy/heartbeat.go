package proxy

import (
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SSE 心跳间隔：每 30 秒注入一次 ping 事件
// Claude Code 默认 90 秒超时，30 秒间隔留足余量
const sseHeartbeatInterval = 30 * time.Second

// Anthropic 格式的 SSE ping 事件
var anthropicPingEvent = []byte("event: ping\ndata: {\"type\":\"ping\"}\n\n")

// isSSEStream 检查响应是否为 SSE 流式响应
// 通过 Content-Type 和 Transfer-Encoding 判断
func isSSEStream(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	return strings.Contains(contentType, "text/event-stream")
}

// heartbeatWriter 包装 http.ResponseWriter，在 SSE 流中注入心跳
// 当检测到一段时间没有数据传输时，自动发送 Anthropic 格式的 ping 事件
type heartbeatWriter struct {
	http.ResponseWriter
	mu            sync.Mutex
	lastWrite     time.Time
	heartbeatMu   sync.Mutex
	stopHeartbeat chan struct{}
	stopped       bool
}

type ChunkObserver interface {
	Observe(chunk []byte)
}

type TerminalObserver interface {
	ChunkObserver
	IsComplete() bool
}

// newHeartbeatWriter 创建带心跳功能的 ResponseWriter
func newHeartbeatWriter(w http.ResponseWriter) *heartbeatWriter {
	return &heartbeatWriter{
		ResponseWriter: w,
		lastWrite:      time.Now(),
		stopHeartbeat:  make(chan struct{}),
	}
}

// Write 实现 io.Writer 接口
// 每次写入数据时更新最后写入时间
func (hw *heartbeatWriter) Write(p []byte) (int, error) {
	hw.mu.Lock()
	defer hw.mu.Unlock()

	n, err := hw.ResponseWriter.Write(p)
	if err != nil {
		return n, err
	}

	hw.lastWrite = time.Now()
	return n, nil
}

// Flush 实现 http.Flusher 接口，确保数据立即发送
func (hw *heartbeatWriter) Flush() {
	if flusher, ok := hw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// startHeartbeat 启动心跳 goroutine
func (hw *heartbeatWriter) startHeartbeat() {
	go func() {
		ticker := time.NewTicker(sseHeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				hw.sendHeartbeat()
			case <-hw.stopHeartbeat:
				return
			}
		}
	}()
}

// sendHeartbeat 仅在上游空闲超过心跳间隔时才发送 ping
func (hw *heartbeatWriter) sendHeartbeat() {
	hw.mu.Lock()
	defer hw.mu.Unlock()

	// 检查是否已被停止
	hw.heartbeatMu.Lock()
	if hw.stopped {
		hw.heartbeatMu.Unlock()
		return
	}
	hw.heartbeatMu.Unlock()

	// 上游仍在活跃发送数据，跳过心跳
	if time.Since(hw.lastWrite) < sseHeartbeatInterval {
		return
	}

	_, err := hw.ResponseWriter.Write(anthropicPingEvent)
	if err != nil {
		log.Printf("[Heartbeat] Failed to send ping event: %v (client may have disconnected)", err)
		hw.stop()
		return
	}

	hw.Flush()
	hw.lastWrite = time.Now()

	log.Printf("[Heartbeat] Sent SSE ping event (upstream idle)")
}

// stop 停止心跳
func (hw *heartbeatWriter) stop() {
	hw.heartbeatMu.Lock()
	defer hw.heartbeatMu.Unlock()

	if !hw.stopped {
		hw.stopped = true
		close(hw.stopHeartbeat)
	}
}

// copyWithHeartbeat 从 reader 复制数据到 writer，并在空闲时注入心跳
// 此函数会阻塞直到 reader 返回 io.EOF 或发生错误
func copyWithHeartbeat(dst *heartbeatWriter, src io.Reader) error {
	return copyWithHeartbeatAndObserver(dst, src, nil)
}

func copyWithHeartbeatAndObserver(dst *heartbeatWriter, src io.Reader, observer ChunkObserver) error {
	defer dst.stop()

	// 启动心跳
	dst.startHeartbeat()

	// 使用小缓冲区读取，以便尽快检测到新数据
	buf := make([]byte, 32*1024) // 32KB 缓冲区
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if observer != nil {
				observer.Observe(buf[:n])
			}
			_, writeErr := dst.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			// SSE 数据通常需要立即刷新
			dst.Flush()
			if terminal, ok := observer.(TerminalObserver); ok && terminal.IsComplete() {
				return nil
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
