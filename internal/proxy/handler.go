package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/usage"
)

// 请求体大小限制 (10MB)
const maxRequestBodySize = 10 * 1024 * 1024

// Handler 代理处理器
type Handler struct {
	configStore config.ConfigStore
	transport   *http.Transport
	recorder    UsageRecorder
}

type UsageRecorder interface {
	Record(req usage.RequestRecord, tok usage.TokenRecord) error
}

// NewHandler 创建代理处理器
func NewHandler(store config.ConfigStore, transport *http.Transport, recorders ...UsageRecorder) *Handler {
	handler := &Handler{
		configStore: store,
		transport:   transport,
	}
	if len(recorders) > 0 {
		handler.recorder = recorders[0]
	}
	return handler
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
	metadata := usage.ParseRequestMetadata(body, r.Header)
	mappedModel := metadata.OriginalModel
	if activeProvider != nil {
		mappedModel = activeProvider.MapModel(metadata.OriginalModel)
		modifiedBody, err = h.transformRequest(body, activeProvider)
		if err != nil {
			log.Printf("Error transforming request: %v", err)
			// 转换失败时使用原始请求体
			modifiedBody = body
		} else {
			mappedModel = usage.ParseRequestMetadata(modifiedBody, r.Header).OriginalModel
		}
	}

	reqID := randomHex(8)

	// 请求入口日志
	msgs, tools, isStream := requestBodySummary(body)
	modelStr := metadata.OriginalModel
	if mappedModel != metadata.OriginalModel {
		modelStr = fmt.Sprintf("%s -> %s", metadata.OriginalModel, mappedModel)
	}
	log.Printf("[%s] >>> %s %s model=%s stream=%v msgs=%d tools=%d size=%d",
		reqID, r.Method, r.URL.Path, modelStr, isStream, msgs, tools, len(body))

	// 创建后端请求
	backendURL = strings.TrimSuffix(backendURL, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		backendURL += "?" + r.URL.RawQuery
	}
	usageReq := h.newUsageRequest(r, activeProvider, backendURL, metadata, mappedModel, len(modifiedBody))
	shouldRecordUsage := h.recorder != nil && shouldRecordUsagePath(r.URL.Path)

	backendReq, err := http.NewRequest(r.Method, backendURL, bytes.NewReader(modifiedBody))
	if err != nil {
		log.Printf("[%s] <<< %d error=request_construct: %v",
			reqID, http.StatusInternalServerError, err)
		http.Error(w, "Error creating backend request", http.StatusInternalServerError)
		return
	}

	// 复制所有 header（跳过 Host 和 Accept-Encoding，防止上游压缩 SSE 响应）
	// Go 的 http.Transport 不会自动解压应用层显式设置的 Accept-Encoding，
	// 导致压缩后的 SSE 数据无法被 SSEObserver 解析
	hasAuth := false
	for key, values := range r.Header {
		if !shouldForwardRequestHeader(key) {
			continue
		}
		// 不转发 Accept-Encoding 和 TE，防止上游压缩 SSE 响应
		if strings.EqualFold(key, "Accept-Encoding") || strings.EqualFold(key, "TE") {
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

	requestStarted := usageReq.StartedAt
	upstreamStarted := time.Now()
	resp, err := client.Do(backendReq)
	headerMS := time.Since(upstreamStarted).Milliseconds()
	usageReq.UpstreamResponseHeaderMS = &headerMS
	if err != nil {
		log.Printf("[%s] <<< %d upstream=%dms error=%v",
			reqID, http.StatusBadGateway, headerMS, err)
		if shouldRecordUsage {
			usageReq.ErrorType = usageErrorType(err)
			usageReq.ErrorMessage = usage.SanitizeErrorMessage(err.Error())
			h.finishUsageRecord(usageReq, usage.TokenRecord{
				UsageSource:      usage.UsageSourceNone,
				UsageParseStatus: usage.ParseStatusNetworkError,
				UsageParseError:  usage.SanitizeParseError(err.Error()),
			})
		}
		http.Error(w, "Backend unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	usageReq.StatusCode = &resp.StatusCode

	// 反应式错误恢复：400 时尝试清理请求并重试
	if resp.StatusCode == 400 && shouldRecordUsagePath(r.URL.Path) && activeProvider != nil {
		retried, restoredBody := h.tryRectify(r, modifiedBody, resp, backendURL, apiToken, client)
		if retried != nil {
			resp = retried
			defer resp.Body.Close()
			usageReq.StatusCode = &resp.StatusCode
			headerMS = time.Since(upstreamStarted).Milliseconds()
			usageReq.UpstreamResponseHeaderMS = &headerMS
			log.Printf("[Rectifier] Retry response status: %d", resp.StatusCode)
		} else if restoredBody != nil {
			resp.Body = restoredBody
		}
	}

	// 复制响应 header
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 响应出口日志
	log.Printf("[%s] <<< %d model=%s upstream=%dms",
		reqID, resp.StatusCode, modelStr, headerMS)

	// 设置状态码
	w.WriteHeader(resp.StatusCode)

	// 检查是否为 SSE 流式响应，如果是则注入心跳
	if isSSEStream(resp) {
		log.Printf("[Stream] SSE stream detected for %s, enabling heartbeat injection", backendURL)
		hw := newHeartbeatWriter(w)
		var observer ChunkObserver
		var streamObserver *streamUsageObserver
		if shouldRecordUsage {
			streamObserver = newStreamUsageObserver(requestStarted)
			observer = streamObserver
		}
		if err := copyWithHeartbeatAndObserver(hw, resp.Body, observer); err != nil {
			log.Printf("Stream interrupted for %s: %v (this is normal if client disconnected)", backendURL, err)
			usageReq.ErrorType = usage.ErrorClientAborted
			usageReq.ErrorMessage = usage.SanitizeErrorMessage(err.Error())
		}
		if shouldRecordUsage {
			values, source, status, firstByte := streamObserver.Result()
			usageReq.ResponseBytes = streamObserver.Bytes()
			usageReq.TimeToFirstByteMS = firstByte
			h.finishUsageRecord(usageReq, tokenRecordFromUsage(values, source, status))
		}
	} else {
		// 非 SSE 响应，直接复制
		observer := newResponseObserver(requestStarted, 4*1024*1024)
		_, err = io.Copy(io.MultiWriter(w, observer), resp.Body)
		if err != nil {
			log.Printf("Stream interrupted for %s: %v (this is normal if client disconnected)", backendURL, err)
			usageReq.ErrorType = usage.ErrorClientAborted
			usageReq.ErrorMessage = usage.SanitizeErrorMessage(err.Error())
		}
		if shouldRecordUsage {
			usageReq.ResponseBytes = observer.Bytes()
			usageReq.TimeToFirstByteMS = observer.FirstByte()
			tok := usage.TokenRecord{}
			if resp.StatusCode >= 400 {
				usageReq.ErrorType = usage.ErrorHTTP
				usageReq.ErrorMessage = usage.SanitizeErrorMessage(string(observer.Body()))
				tok.UsageSource = usage.UsageSourceNone
				tok.UsageParseStatus = usage.ParseStatusSkippedNon2xx
				log.Printf("[Proxy] Error %d %s | headers: %s | params: %s | resp: %s",
					resp.StatusCode, backendURL, summarizeCompatHeaders(r.Header), summarizeRequestParams(modifiedBody), usageReq.ErrorMessage)
			} else {
				values, source, status := usage.ExtractUsageFromJSON(observer.Body())
				tok = tokenRecordFromUsage(values, source, status)
			}
			h.finishUsageRecord(usageReq, tok)
		}
	}
}

func shouldRecordUsagePath(path string) bool {
	return path == "/v1/messages" || path == "/anthropic/v1/messages"
}

func (h *Handler) newUsageRequest(r *http.Request, provider *config.Provider, backendURL string, metadata usage.RequestMetadata, mappedModel string, requestBytes int) usage.RequestRecord {
	record := usage.RequestRecord{
		ID:               generateID(),
		StartedAt:        time.Now().UTC(),
		Method:           r.Method,
		RequestPath:      r.URL.Path,
		BackendURL:       usage.RedactURL(backendURL),
		SourceApp:        metadata.SourceApp,
		SourceEntrypoint: metadata.SourceEntrypoint,
		UserAgent:        metadata.UserAgent,
		OriginalModel:    metadata.OriginalModel,
		MappedModel:      mappedModel,
		Stream:           metadata.Stream,
		RequestBytes:     int64(requestBytes),
	}
	if provider != nil {
		record.ProviderID = provider.ID
		record.ProviderName = provider.Name
		record.ProviderAPIURL = provider.APIURL
	}
	return record
}

func (h *Handler) finishUsageRecord(req usage.RequestRecord, tok usage.TokenRecord) {
	ended := time.Now().UTC()
	duration := ended.Sub(req.StartedAt).Milliseconds()
	req.EndedAt = &ended
	req.DurationMS = &duration
	tok.RequestID = req.ID
	if err := h.recorder.Record(req, tok); err != nil {
		log.Printf("[Usage] Failed to record usage request %s: %v", req.ID, err)
	}
}

func tokenRecordFromUsage(values usage.UsageValues, source, status string) usage.TokenRecord {
	return usage.TokenRecord{
		InputTokens:              values.InputTokens,
		OutputTokens:             values.OutputTokens,
		CacheCreationInputTokens: values.CacheCreationInputTokens,
		CacheReadInputTokens:     values.CacheReadInputTokens,
		UsageSource:              source,
		UsageParseStatus:         status,
	}
}

func usageErrorType(err error) string {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return usage.ErrorUpstreamTimeout
	}
	return usage.ErrorNetwork
}

type responseObserver struct {
	startedAt time.Time
	limit     int
	body      []byte
	bytes     int64
	firstByte *int64
}

func newResponseObserver(startedAt time.Time, limit int) *responseObserver {
	return &responseObserver{startedAt: startedAt, limit: limit}
}

func (o *responseObserver) Write(p []byte) (int, error) {
	if len(p) > 0 && o.firstByte == nil {
		ms := time.Since(o.startedAt).Milliseconds()
		o.firstByte = &ms
	}
	o.bytes += int64(len(p))
	remaining := o.limit - len(o.body)
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		o.body = append(o.body, p[:remaining]...)
	}
	return len(p), nil
}

func (o *responseObserver) Body() []byte {
	return o.body
}

func (o *responseObserver) Bytes() int64 {
	return o.bytes
}

func (o *responseObserver) FirstByte() *int64 {
	return o.firstByte
}

type streamUsageObserver struct {
	usage *usage.SSEObserver
	bytes int64
}

func newStreamUsageObserver(startedAt time.Time) *streamUsageObserver {
	return &streamUsageObserver{usage: usage.NewSSEObserver(startedAt)}
}

func (o *streamUsageObserver) Observe(chunk []byte) {
	o.bytes += int64(len(chunk))
	o.usage.Observe(chunk)
}

func (o *streamUsageObserver) Result() (usage.UsageValues, string, string, *int64) {
	return o.usage.Result()
}

func (o *streamUsageObserver) Bytes() int64 {
	return o.bytes
}

func (o *streamUsageObserver) IsComplete() bool {
	return o.usage.IsComplete()
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
		mapped := provider.MapModel(model)
		if provider.MultimodalSwitch && provider.MultimodalModel != "" && requestContainsNonTextContent(req) {
			mapped = provider.MultimodalModel
		}
		if mapped != model {
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

func requestContainsNonTextContent(req map[string]any) bool {
	for _, key := range []string{"messages", "system"} {
		if containsNonTextContent(req[key]) {
			return true
		}
	}
	return false
}

func containsNonTextContent(value any) bool {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if containsNonTextContent(item) {
				return true
			}
		}
	case map[string]any:
		if isNonTextContentBlock(v) {
			return true
		}
		for _, item := range v {
			if containsNonTextContent(item) {
				return true
			}
		}
	}
	return false
}

func isNonTextContentBlock(block map[string]any) bool {
	switch block["type"] {
	case "image", "input_image", "document":
		return true
	}
	source, ok := block["source"].(map[string]any)
	if !ok {
		return false
	}
	mediaType, ok := source["media_type"].(string)
	if !ok {
		return false
	}
	return strings.HasPrefix(mediaType, "image/") ||
		strings.EqualFold(mediaType, "application/pdf") ||
		strings.HasPrefix(mediaType, "video/") ||
		strings.HasPrefix(mediaType, "audio/")
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

// summarizeCompatHeaders 提取对兼容性排查有用的请求头（用于错误日志）
func summarizeCompatHeaders(header http.Header) string {
	keys := []string{"Anthropic-Version", "Anthropic-Beta", "Content-Type"}
	parts := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		if v := header.Get(k); v != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", k, v))
		}
	}
	if v := header.Get("X-Api-Key"); v != "" {
		parts = append(parts, "X-Api-Key: ***"+v[max(0, len(v)-4):])
	} else if v := header.Get("Authorization"); v != "" {
		parts = append(parts, "Authorization: ***")
	}
	return strings.Join(parts, ", ")
}

// requestBodySummary 从请求体中提取关键统计信息
func requestBodySummary(body []byte) (msgs int, tools int, stream bool) {
	var req struct {
		Messages []json.RawMessage `json:"messages"`
		Tools    []json.RawMessage `json:"tools"`
		Stream   bool              `json:"stream"`
	}
	if json.Unmarshal(body, &req) != nil {
		return 0, 0, false
	}
	return len(req.Messages), len(req.Tools), req.Stream
}

const maxErrorBodySize = 128 * 1024

// tryRectify 尝试对 400 错误进行反应式恢复
// 返回值：重试后的响应（如有），或恢复后的原始响应体（用于直接转发）
func (h *Handler) tryRectify(
	origReq *http.Request,
	origBody []byte,
	resp *http.Response,
	backendURL string,
	apiToken string,
	client *http.Client,
) (*http.Response, io.ReadCloser) {
	// 缓冲错误体
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	restoredBody := func() io.ReadCloser {
		return io.NopCloser(io.MultiReader(bytes.NewReader(errBody), resp.Body))
	}

	pattern := matchErrorPattern(errBody)
	if pattern == PatternNone {
		return nil, restoredBody()
	}

	log.Printf("[Rectifier] Detected error pattern %d, attempting cleanup", pattern)

	cleanedBody, applied := RectifyRequest(origBody, pattern)
	if !applied {
		log.Printf("[Rectifier] Cleanup made no changes, forwarding original error")
		return nil, restoredBody()
	}

	log.Printf("[Rectifier] Cleanup applied, retrying request to %s", backendURL)

	// 重建重试请求
	retryReq, err := http.NewRequest(origReq.Method, backendURL, bytes.NewReader(cleanedBody))
	if err != nil {
		log.Printf("[Rectifier] Failed to create retry request: %v", err)
		return nil, restoredBody()
	}

	// 复制原始请求头
	hasAuth := false
	for key, values := range origReq.Header {
		if !shouldForwardRequestHeader(key) {
			continue
		}
		if apiToken != "" && (strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "X-Api-Key")) {
			if !hasAuth {
				if strings.EqualFold(key, "Authorization") {
					retryReq.Header.Set("Authorization", "Bearer "+apiToken)
				} else {
					retryReq.Header.Set("X-Api-Key", apiToken)
				}
				hasAuth = true
			}
			continue
		}
		for _, value := range values {
			retryReq.Header.Add(key, value)
		}
	}
	if !hasAuth && apiToken != "" {
		retryReq.Header.Set("Authorization", "Bearer "+apiToken)
	}

	retryResp, err := client.Do(retryReq)
	if err != nil {
		log.Printf("[Rectifier] Retry request failed: %v", err)
		return nil, restoredBody()
	}

	resp.Body.Close()
	return retryResp, nil
}
