package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"claude_code_proxy_dns/internal/config"
)

// 请求体大小限制 (10MB)
const maxRequestBodySize = 10 * 1024 * 1024

// Handler 代理处理器
type Handler struct {
	configStore config.ConfigStore
	transport   *http.Transport
}

// NewHandler 创建代理处理器
func NewHandler(store config.ConfigStore, transport *http.Transport) *Handler {
	return &Handler{
		configStore: store,
		transport:   transport,
	}
}

// ServeHTTP 处理 HTTP 请求
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
		return
	}

	// 检查是否为硬编码端点
	if h.handleHardcodedEndpoint(w, r) {
		return
	}

	// 加载配置
	cfg, err := h.configStore.Load()
	if err != nil {
		log.Printf("Error loading config: %v", err)
		http.Error(w, "Config error", http.StatusInternalServerError)
		return
	}

	// 获取活跃的供应商
	activeProvider := cfg.GetActiveProvider()

	// 确定后端 URL 和 API Token
	var backendURL string
	var apiToken string

	if activeProvider != nil {
		backendURL = activeProvider.APIURL
		apiToken = activeProvider.APIToken
	} else if cfg.BackendURL != "" {
		// 向后兼容：使用旧的 BackendURL
		backendURL = cfg.BackendURL
		// 从请求中获取 Authorization header
		apiToken = ""
	} else {
		log.Printf("No active provider configured")
		http.Error(w, "No active provider", http.StatusServiceUnavailable)
		return
	}

	// 读取请求体 (限制大小)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize+1))
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// 检查请求体是否超过限制
	if len(body) > maxRequestBodySize {
		log.Printf("Request body too large: %d bytes", len(body))
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// 转换请求体（模型映射 + 按供应商能力调整）
	modifiedBody := body
	if activeProvider != nil {
		modifiedBody, err = h.transformRequest(body, activeProvider)
		if err != nil {
			log.Printf("Error transforming request: %v", err)
			// 转换失败时使用原始请求体
			modifiedBody = body
		}
	}

	// 创建后端请求
	backendURL = strings.TrimSuffix(backendURL, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		backendURL += "?" + r.URL.RawQuery
	}

	backendReq, err := http.NewRequest(r.Method, backendURL, bytes.NewReader(modifiedBody))
	if err != nil {
		log.Printf("Error creating backend request: %v", err)
		http.Error(w, "Error creating backend request", http.StatusInternalServerError)
		return
	}

	// 复制所有 header（跳过 Host，让 Go 自动设置）
	// 如果有供应商配置的 Token，替换 Authorization
	hasAuth := false
	for key, values := range r.Header {
		if !shouldForwardRequestHeader(key) {
			continue
		}
		// 如果有供应商配置的 Token，替换认证头
		if apiToken != "" && (strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "X-Api-Key")) {
			if !hasAuth {
				if strings.EqualFold(key, "Authorization") {
					backendReq.Header.Set("Authorization", "Bearer "+apiToken)
				} else {
					backendReq.Header.Set("X-Api-Key", apiToken)
				}
				hasAuth = true
			}
			continue
		}
		for _, value := range values {
			backendReq.Header.Add(key, value)
		}
	}

	// 如果没有认证头且供应商有 Token，添加认证
	if !hasAuth && apiToken != "" {
		backendReq.Header.Set("Authorization", "Bearer "+apiToken)
	}

	// 发送请求到后端
	// AI API 请求可能需要较长时间（特别是流式响应），设置较长的超时
	client := &http.Client{
		Transport: h.transport,
		Timeout:   10 * time.Minute, // AI API 可能需要较长时间
	}

	resp, err := client.Do(backendReq)
	if err != nil {
		log.Printf("Error forwarding request to %s: %v", backendURL, err)
		http.Error(w, "Backend unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 非 2xx 响应记录详细错误信息
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("[Proxy] Error %d %s | params: %s | resp: %s",
			resp.StatusCode, backendURL, summarizeRequestParams(modifiedBody), string(respBody))
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
	}

	// 复制响应 header
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 设置状态码
	w.WriteHeader(resp.StatusCode)

	// 检查是否为 SSE 流式响应，如果是则注入心跳
	if isSSEStream(resp) {
		log.Printf("[Stream] SSE stream detected for %s, enabling heartbeat injection", backendURL)
		hw := newHeartbeatWriter(w)
		if err := copyWithHeartbeat(hw, resp.Body); err != nil {
			log.Printf("Stream interrupted for %s: %v (this is normal if client disconnected)", backendURL, err)
		}
	} else {
		// 非 SSE 响应，直接复制
		_, err = io.Copy(w, resp.Body)
		if err != nil {
			log.Printf("Stream interrupted for %s: %v (this is normal if client disconnected)", backendURL, err)
		}
	}
}

// transformRequest 转换请求体（模型映射 + 按供应商能力剥离 thinking）
func (h *Handler) transformRequest(body []byte, provider *config.Provider) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}

	changed := false

	// 模型映射
	if model, ok := req["model"].(string); ok {
		if mapped := provider.MapModel(model); mapped != model {
			log.Printf("Model mapping: %s -> %s (provider: %s)", model, mapped, provider.Name)
			req["model"] = mapped
			changed = true
		}
	}

	if !provider.SupportsThinking {
		if _, ok := req["thinking"]; ok {
			log.Printf("[Compat] Stripping thinking")
			delete(req, "thinking")
			changed = true
		}
	}

	// 非流式请求也尝试转换（兼容性兜底）
	if changed {
		out, err := json.Marshal(req)
		if err != nil {
			return body, nil
		}
		return out, nil
	}
	return body, nil
}

func shouldForwardRequestHeader(key string) bool {
	return !strings.EqualFold(key, "Host")
}

// summarizeRequestParams 生成请求参数摘要（用于错误日志）
func summarizeRequestParams(body []byte) string {
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		return fmt.Sprintf("<%d bytes, not JSON>", len(body))
	}
	summary := make(map[string]any)
	for k, v := range req {
		switch k {
		case "messages":
			if arr, ok := v.([]any); ok {
				summary[k] = fmt.Sprintf("[%d items]", len(arr))
			}
		case "tools":
			if arr, ok := v.([]any); ok {
				summary[k] = fmt.Sprintf("[%d items]", len(arr))
			}
		default:
			summary[k] = v
		}
	}
	out, _ := json.Marshal(summary)
	return string(out)
}
