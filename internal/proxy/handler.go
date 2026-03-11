package proxy

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Handler 代理处理器
type Handler struct {
	config    *Config
	transport *http.Transport
}

// NewHandler 创建代理处理器
func NewHandler(cfg *Config) *Handler {
	return &Handler{
		config: cfg,
		transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			MaxIdleConnsPerHost:   10,
		},
	}
}

// ServeHTTP 处理 HTTP 请求
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 创建后端请求
	backendURL := h.config.BackendURL + r.URL.Path
	if r.URL.RawQuery != "" {
		backendURL += "?" + r.URL.RawQuery
	}

	// 读取请求体
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// 创建后端请求
	backendReq, err := http.NewRequest(r.Method, backendURL, strings.NewReader(string(body)))
	if err != nil {
		log.Printf("Error creating backend request: %v", err)
		http.Error(w, "Error creating backend request", http.StatusInternalServerError)
		return
	}

	// 复制所有 header
	for key, values := range r.Header {
		for _, value := range values {
			backendReq.Header.Add(key, value)
		}
	}

	// 发送请求到后端
	client := &http.Client{
		Transport: h.transport,
		Timeout:   60 * time.Second,
	}

	resp, err := client.Do(backendReq)
	if err != nil {
		log.Printf("Error forwarding request: %v", err)
		http.Error(w, "Backend unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 复制响应 header
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 设置状态码
	w.WriteHeader(resp.StatusCode)

	// 流式复制响应体
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("Error copying response body: %v", err)
	}
}