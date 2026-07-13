package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/failover"
	"magic-claude-code/internal/proxy/ratelimit"
	apitransform "magic-claude-code/internal/proxy/transform"
	"magic-claude-code/internal/usage"
)

// 请求体大小限制 (10MB)
const maxRequestBodySize = 10 * 1024 * 1024

// Handler 代理处理器
type Handler struct {
	configStore config.ConfigStore
	transport   *http.Transport
	recorder    UsageRecorder
	rateLimiter *ratelimit.Manager
	// failoverManager 为 nil 时自动故障切换关闭（仅当 Config.AutoFailoverEnabled
	// 且路由为默认路由时才生效）。绝不影响 ExposedModel（/model 会话路由）。
	failoverManager *failover.Manager
}

// SetFailoverManager 注入故障切换管理器。
func (h *Handler) SetFailoverManager(m *failover.Manager) { h.failoverManager = m }

type UsageRecorder interface {
	Record(req usage.RequestRecord, tok usage.TokenRecord) error
}

// NewHandler 创建代理处理器
func NewHandler(store config.ConfigStore, transport *http.Transport, recorders ...UsageRecorder) *Handler {
	handler := &Handler{
		configStore: store,
		transport:   transport,
		rateLimiter: ratelimit.NewManager(),
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

	// Fail-closed guard：只有显式模型推理端点（POST /v1/messages、POST /anthropic/v1/messages）
	// 允许进入下方转发流程。所有未识别的非模型端点一律本地拦截，绝不转发给配置的上游 provider。
	// 必须在 handleHardcodedEndpoint 之后、加载配置/provider 解析/请求转换之前。
	decision := classifyForwardingEndpoint(r.Method, r.URL.Path)
	switch decision.action {
	case endpointActionForwardModel:
		// 继续现有转发流程
	case endpointActionMethodNotAllowed:
		h.handleBlockedEndpoint(w, r, http.StatusMethodNotAllowed, decision.reason)
		return
	default:
		h.handleBlockedEndpoint(w, r, http.StatusNotFound, decision.reason)
		return
	}

	// 加载配置
	cfg, err := h.configStore.Load()
	if err != nil {
		log.Printf("Error loading config: %v", err)
		http.Error(w, "Config error", http.StatusInternalServerError)
		return
	}

	// 先读 body 再路由：model 字段在 body 内
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize+1))
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	if len(body) > maxRequestBodySize {
		log.Printf("Request body too large: %d bytes", len(body))
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	metadata := usage.ParseRequestMetadata(body, r.Header)

	// 按 model 路由：命中暴露模型 → 对应 provider（固定路由，不参与故障切换）；
	// 否则 fallback active（默认路由，唯一允许自动切换的路径）。
	route := cfg.ResolveRoute(metadata.OriginalModel)
	selectedProvider := route.Provider
	backendModel := route.BackendModel

	var backendURL string
	var apiToken string
	if selectedProvider != nil {
		backendURL = selectedProvider.APIURL
		apiToken = selectedProvider.APIToken
	} else if cfg.BackendURL != "" {
		backendURL = cfg.BackendURL
		apiToken = ""
	} else {
		log.Printf("No active provider configured")
		http.Error(w, "No active provider", http.StatusServiceUnavailable)
		return
	}

	modifiedBody, err := h.transformRequest(body, selectedProvider, backendModel)
	if err != nil {
		log.Printf("Error transforming request: %v", err)
		http.Error(w, "Error transforming request", http.StatusBadRequest)
		return
	}
	mappedModel := usage.ParseRequestMetadata(modifiedBody, r.Header).OriginalModel

	reqID := randomHex(8)

	// 请求入口日志的静态部分（upstream_url 等最终 URL 确定后再打印，见下方）
	msgs, tools, isStream := requestBodySummary(body)
	modelStr := metadata.OriginalModel
	if mappedModel != metadata.OriginalModel {
		modelStr = fmt.Sprintf("%s -> %s", metadata.OriginalModel, mappedModel)
	}

	// 创建后端请求
	backendURL = buildUpstreamURL(backendURL, r.URL.Path, providerAPIFormat(selectedProvider))
	if r.URL.RawQuery != "" {
		upstreamQuery := stripAnthropicQueryParams(r.URL.RawQuery, providerAPIFormat(selectedProvider))
		if upstreamQuery != "" {
			backendURL += "?" + upstreamQuery
		}
	}

	// 请求入口日志（此时 backendURL 已是最终转发 URL，与出口日志语义一致）
	log.Printf("[%s] >>> %s %s%s model=%s stream=%v msgs=%d tools=%d size=%d%s",
		reqID, r.Method, r.Host, r.URL.Path, modelStr, isStream, msgs, tools, len(body),
		providerLogFields(selectedProvider, backendURL))
	usageReq := h.newUsageRequest(r, selectedProvider, backendURL, metadata, mappedModel, len(modifiedBody))
	shouldRecordUsage := h.recorder != nil && shouldRecordUsagePath(r.URL.Path)

	apiFmt := providerAPIFormat(selectedProvider)
	client := &http.Client{
		Transport: h.transport,
		Timeout:   10 * time.Minute,
	}

	doUpstream := func() (*http.Response, error) {
		upReq, upErr := http.NewRequestWithContext(r.Context(), r.Method, backendURL, bytes.NewReader(modifiedBody))
		if upErr != nil {
			return nil, upErr
		}
		copyUpstreamHeaders(upReq, r.Header, apiToken, apiFmt)
		return client.Do(upReq)
	}

	if selectedProvider != nil && selectedProvider.RateLimitQueueEnabled && selectedProvider.MaxConcurrentRequests > 0 {
		result, acquireErr := h.rateLimiter.Acquire(r.Context(), selectedProvider.ID,
			selectedProvider.MaxConcurrentRequests, selectedProvider.MaxQueueSize, selectedProvider.QueueTimeoutMS)
		if acquireErr != nil {
			statusCode := http.StatusTooManyRequests
			errType := "rate_limit_queue_full"
			if errors.Is(acquireErr, ratelimit.ErrQueueTimeout) {
				statusCode = http.StatusGatewayTimeout
				errType = "rate_limit_queue_timeout"
			}
			log.Printf("[%s] <<< %d rate_limit=%s provider_name=%q",
				reqID, statusCode, errType, selectedProvider.Name)
			if shouldRecordUsage {
				usageReq.ErrorType = errType
				h.finishUsageRecord(usageReq, usage.TokenRecord{
					UsageSource:      usage.UsageSourceNone,
					UsageParseStatus: usage.ParseStatusSkippedNon2xx,
				})
			}
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, errType), statusCode)
			return
		}
		defer result.Release()
		if result.Queued {
			log.Printf("[%s] <<< rate_limit_queue provider_name=%q wait=%v",
				reqID, selectedProvider.Name, result.WaitTime)
		}
	}

	requestStarted := usageReq.StartedAt
	upstreamStarted := time.Now()
	var resp *http.Response
	if selectedProvider != nil && selectedProvider.Retry429Enabled {
		retryLogf := func(format string, args ...any) {
			log.Printf("[%s] "+format, append([]any{reqID}, args...)...)
		}
		resp, err = ratelimit.DoWithRetry429(r.Context(), doUpstream,
			selectedProvider.Retry429Enabled,
			selectedProvider.Retry429MaxAttempts,
			selectedProvider.Retry429InitialDelayMS,
			selectedProvider.Retry429MaxDelayMS,
			retryLogf)
	} else {
		resp, err = doUpstream()
	}
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
	if resp.StatusCode == 400 && shouldRecordUsagePath(r.URL.Path) && selectedProvider != nil {
		retried, restoredBody := h.tryRectify(r, modifiedBody, resp, backendURL, apiToken, client, providerAPIFormat(selectedProvider))
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

	// 自动故障切换：仅对默认路由（非 ExposedModel）+ 模型推理端点 + POST + >=400 响应生效。
	// 在向客户端写入任何字节之前完成，确保失败时不会双写响应。
	if h.shouldFailover(cfg, r, route, selectedProvider, resp) {
		if candResp, candRelease, winner, switched := h.attemptFailover(r, body, metadata.OriginalModel, mappedModel, selectedProvider, resp, cfg, client, reqID); candResp != nil {
			// 已在 attemptFailover 内关闭原 resp.Body；这里接管候选响应。
			resp = candResp
			defer resp.Body.Close()
			if candRelease != nil {
				defer candRelease()
			}
			usageReq.StatusCode = &resp.StatusCode
			if winner != nil {
				// usage / 日志口径切到最终成功供应商。
				selectedProvider = winner
				backendModel = winner.MapModel(metadata.OriginalModel)
				mappedModel = backendModel
				backendURL = buildUpstreamURL(winner.APIURL, r.URL.Path, providerAPIFormat(winner))
				apiToken = winner.APIToken
				usageReq.ProviderID = winner.ID
				usageReq.ProviderName = winner.Name
				usageReq.ProviderAPIURL = usage.RedactURL(winner.APIURL)
				usageReq.BackendURL = usage.RedactURL(backendURL)
				usageReq.MappedModel = mappedModel
				modelStr = mappedModel
				headerMS = time.Since(upstreamStarted).Milliseconds()
				usageReq.UpstreamResponseHeaderMS = &headerMS
				log.Printf("[%s] failover switched to provider=%q (active updated=%v)", reqID, winner.Name, switched)
			}
		}
	}

	// 复制响应 header
	for key, values := range resp.Header {
		if !shouldForwardResponseHeader(key, providerAPIFormat(selectedProvider)) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 响应出口日志
	log.Printf("[%s] <<< %d %s%s model=%s upstream=%dms%s",
		reqID, resp.StatusCode, r.Host, r.URL.Path, modelStr, headerMS, providerLogFields(selectedProvider, backendURL))

	// 设置状态码
	w.WriteHeader(resp.StatusCode)

	// 检查是否为 SSE 流式响应，如果是则注入心跳
	if resp.StatusCode < 400 && isSSEStream(resp) {
		log.Printf("[Stream] SSE stream detected for %s, enabling heartbeat injection", formatUpstreamLogTarget(backendURL))
		hw := newHeartbeatWriter(w)
		var observer ChunkObserver
		var streamObserver *streamUsageObserver
		if shouldRecordUsage {
			streamObserver = newStreamUsageObserver(requestStarted)
			observer = streamObserver
		}
		streamBody := resp.Body
		if resp.StatusCode < 400 && providerAPIFormat(selectedProvider) != config.APIFormatAnthropic {
			streamBody = streamOpenAIStreamingResponse(resp.Body, providerAPIFormat(selectedProvider))
		}
		streamErr := copyWithHeartbeatAndObserver(hw, streamBody, observer)
		if streamErr != nil {
			log.Printf("Stream interrupted for %s: %v (this is normal if client disconnected)", formatUpstreamLogTarget(backendURL), streamErr)
			usageReq.ErrorType = usage.ErrorClientAborted
			usageReq.ErrorMessage = usage.SanitizeErrorMessage(streamErr.Error())
		}
		if shouldRecordUsage {
			values, source, status, firstByte := streamObserver.Result()
			usageReq.ResponseBytes = streamObserver.Bytes()
			usageReq.TimeToFirstByteMS = firstByte
			h.finishUsageRecord(usageReq, tokenRecordFromUsage(values, source, status))
			if diag := streamObserver.Diagnostics(); streamErr != nil || !streamObserver.IsComplete() || diag.ParseErrors > 0 || diag.ErrorEvents > 0 {
				log.Printf("[Stream] anomaly %s", streamAnomalyPayload(reqID, streamObserver.Bytes(), diag, modifiedBody, backendURL))
			}
		}
	} else {
		// 非 SSE 响应，直接复制
		observer := newResponseObserver(requestStarted, 4*1024*1024)
		responseBody := resp.Body
		if resp.StatusCode < 400 && providerAPIFormat(selectedProvider) != config.APIFormatAnthropic {
			converted, convertErr := convertOpenAINonStreamingResponse(resp.Body, providerAPIFormat(selectedProvider))
			if convertErr != nil {
				log.Printf("Error converting OpenAI response: %v", convertErr)
				usageReq.ErrorType = usage.ErrorHTTP
				usageReq.ErrorMessage = usage.SanitizeErrorMessage(convertErr.Error())
				responseBody = io.NopCloser(strings.NewReader(`{"error":"response conversion failed"}`))
			} else {
				responseBody = io.NopCloser(bytes.NewReader(converted))
			}
		}
		_, err = io.Copy(io.MultiWriter(w, observer), responseBody)
		if err != nil {
			log.Printf("Stream interrupted for %s: %v (this is normal if client disconnected)", formatUpstreamLogTarget(backendURL), err)
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
					resp.StatusCode, formatUpstreamLogTarget(backendURL), summarizeCompatHeaders(r.Header), summarizeRequestParams(modifiedBody), usageReq.ErrorMessage)
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

// shouldFailover 报告当前响应是否应进入自动故障切换流程。
// 准入条件：管理器已注入 + 配置开启 + 默认路由（非 ExposedModel）+ 模型推理端点 + POST + 响应 >=400。
// 任何一项不满足都返回 false，确保 ExposedModel /model 路由、非推理端点、安全方法永不切换。
func (h *Handler) shouldFailover(cfg *config.Config, r *http.Request, route config.ModelRoute, provider *config.Provider, resp *http.Response) bool {
	if h.failoverManager == nil || !cfg.AutoFailoverEnabled {
		return false
	}
	if !route.DefaultRouted {
		return false
	}
	if provider == nil || provider.ID == "" {
		return false
	}
	if !shouldRecordUsagePath(r.URL.Path) {
		return false
	}
	if r.Method != http.MethodPost {
		return false
	}
	if resp == nil || resp.StatusCode < 400 {
		return false
	}
	return true
}

// replayToProvider 用不可变的原始客户端输入为指定候选供应商重新构造转换 body、URL、
// Token、header 与格式，并应用该候选自身的并发队列与 429 重试。返回响应 + 队列释放函数。
func (h *Handler) replayToProvider(ctx context.Context, r *http.Request, originalBody []byte, originalModel string, provider *config.Provider, client *http.Client, reqID string) (*http.Response, func(), error) {
	backendModel := provider.MapModel(originalModel)
	if provider.MultimodalSwitch && provider.MultimodalModel != "" {
		var peek map[string]any
		if json.Unmarshal(originalBody, &peek) == nil && requestContainsNonTextContent(peek) {
			backendModel = provider.MultimodalModel
		}
	}
	modifiedBody, err := h.transformRequest(originalBody, provider, backendModel)
	if err != nil {
		return nil, nil, err
	}
	apiFmt := providerAPIFormat(provider)
	backendURL := buildUpstreamURL(provider.APIURL, r.URL.Path, apiFmt)
	if r.URL.RawQuery != "" {
		if q := stripAnthropicQueryParams(r.URL.RawQuery, apiFmt); q != "" {
			backendURL += "?" + q
		}
	}
	doOnce := func() (*http.Response, error) {
		upReq, upErr := http.NewRequestWithContext(ctx, r.Method, backendURL, bytes.NewReader(modifiedBody))
		if upErr != nil {
			return nil, upErr
		}
		copyUpstreamHeaders(upReq, r.Header, provider.APIToken, apiFmt)
		return client.Do(upReq)
	}
	release := func() {}
	if provider.RateLimitQueueEnabled && provider.MaxConcurrentRequests > 0 {
		result, acquireErr := h.rateLimiter.Acquire(ctx, provider.ID, provider.MaxConcurrentRequests, provider.MaxQueueSize, provider.QueueTimeoutMS)
		if acquireErr != nil {
			return nil, nil, acquireErr
		}
		release = result.Release
	}
	log.Printf("[%s] failover replay provider=%q model=%s->%s", reqID, provider.Name, originalModel, backendModel)
	if provider.Retry429Enabled {
		resp, err := ratelimit.DoWithRetry429(ctx, doOnce, provider.Retry429Enabled, provider.Retry429MaxAttempts, provider.Retry429InitialDelayMS, provider.Retry429MaxDelayMS, func(format string, args ...any) {
			log.Printf("[%s] "+format, append([]any{reqID}, args...)...)
		})
		if err != nil {
			release()
			return nil, nil, err
		}
		return resp, release, nil
	}
	resp, err := doOnce()
	if err != nil {
		release()
		return nil, nil, err
	}
	return resp, release, nil
}

// attemptFailover 对已失败的默认路由响应执行故障切换：分类 → 摘除失败供应商 →
// 按候选顺序逐个重放（同映射模型优先）→ 第一个 <400 的响应作为最终客户端响应，
// 并原子提交默认供应商切换。返回最终响应（nil 表示未切换，调用方沿用原响应）。
//
// 资源契约：
//   - 进入时读取并还原 origResp.Body（用 NopCloser 包裹完整重放流），原网络流由
//     调用方既有的 defer resp.Body.Close() 在函数结束时关闭。
//   - 切换成功时关闭原响应体副本，返回候选响应；候选响应的关闭与队列释放由调用方 defer。
//   - 所有被跳过/失败的候选响应在此函数内关闭并释放。
func (h *Handler) attemptFailover(r *http.Request, originalBody []byte, originalModel, mappedModel string, failed *config.Provider, origResp *http.Response, cfg *config.Config, client *http.Client, reqID string) (candResp *http.Response, release func(), winner *config.Provider, switched bool) {
	captured, restored, oversize := failover.CaptureBody(origResp.Body, failover.MaxClassifyBodyBytes)
	cls := failover.ClassifyResponse(origResp.StatusCode, captured, oversize)
	// 还原 body：非合格或全部耗尽时，原响应需完整透传给客户端。
	origResp.Body = io.NopCloser(restored)
	if !cls.Eligible {
		log.Printf("[%s] failover skipped: not eligible (status=%d)", reqID, origResp.StatusCode)
		return nil, nil, nil, false
	}

	h.failoverManager.QuarantineFailed(failed.ID, cls)
	candidates := h.failoverManager.SelectCandidates(failed.ID, originalModel, mappedModel, cfg.Providers)
	if len(candidates) == 0 {
		h.failoverManager.RecordExhausted(failed.ID, originalModel, mappedModel, cls, candidates)
		log.Printf("[%s] failover exhausted: no candidates", reqID)
		return nil, nil, nil, false
	}
	for i := range candidates {
		c := candidates[i]
		resp, rel, err := h.replayToProvider(r.Context(), r, originalBody, originalModel, &c, client, reqID)
		if err != nil {
			log.Printf("[%s] failover candidate %q error: %v", reqID, c.Name, err)
			continue
		}
		if resp.StatusCode < 400 {
			sw := h.failoverManager.CommitSwitch(failed.ID, c.ID, originalModel, mappedModel, cls)
			origResp.Body.Close()
			return resp, rel, &c, sw
		}
		resp.Body.Close()
		rel()
		log.Printf("[%s] failover candidate %q returned %d, trying next", reqID, c.Name, resp.StatusCode)
	}
	h.failoverManager.RecordExhausted(failed.ID, originalModel, mappedModel, cls, candidates)
	log.Printf("[%s] failover exhausted: all candidates failed", reqID)
	return nil, nil, nil, false
}

func providerAPIFormat(provider *config.Provider) config.APIFormat {
	if provider == nil || provider.APIFormat == "" {
		return config.APIFormatAnthropic
	}
	return provider.APIFormat
}

// redactUpstreamURL 去掉 URL 的 userinfo、query 和 fragment，
// 只保留 scheme://host/path。防止 provider URL 中的凭证、签名等敏感信息泄露到日志。
// 逻辑共享自 config.RedactURL，确保日志/管理 API/配置校验三处口径一致。
func redactUpstreamURL(rawURL string) string {
	if _, err := url.Parse(rawURL); err != nil {
		return "<invalid-url>"
	}
	return config.RedactURL(rawURL)
}

func providerLogFields(provider *config.Provider, upstreamURL string) string {
	if provider == nil {
		if upstreamURL == "" {
			upstreamURL = "-"
		}
		return fmt.Sprintf(` provider_name=- upstream_url=%q upstream_query=%q`, redactUpstreamURL(upstreamURL), summarizeUpstreamQuery(upstreamURL))
	}

	providerName := provider.Name
	if providerName == "" {
		providerName = "-"
	}
	apiURL := upstreamURL
	if apiURL == "" {
		apiURL = provider.APIURL
	}
	if apiURL == "" {
		apiURL = "-"
	}
	return fmt.Sprintf(` provider_name=%q upstream_url=%q upstream_query=%q`, providerName, redactUpstreamURL(apiURL), summarizeUpstreamQuery(apiURL))
}

func buildUpstreamURL(baseURL, requestPath string, apiFormat config.APIFormat) string {
	base := strings.TrimSuffix(baseURL, "/")
	switch apiFormat {
	case config.APIFormatOpenAIChat:
		if strings.HasSuffix(base, "/chat/completions") {
			return base
		}
		return base + "/chat/completions"
	case config.APIFormatOpenAIResponses:
		if strings.HasSuffix(base, "/responses") {
			return base
		}
		return base + "/responses"
	default:
		return base + requestPath
	}
}

// stripAnthropicQueryParams removes Anthropic-specific query parameters
// (e.g. beta=true) when forwarding to non-Anthropic upstream providers.
func stripAnthropicQueryParams(query string, apiFormat config.APIFormat) string {
	if apiFormat == config.APIFormatAnthropic {
		return query
	}
	parts := strings.Split(query, "&")
	filtered := parts[:0]
	for _, p := range parts {
		if p != "" && !strings.HasPrefix(p, "beta=") {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, "&")
}

func shouldForwardResponseHeader(key string, apiFormat config.APIFormat) bool {
	if apiFormat == config.APIFormatAnthropic {
		return true
	}
	switch {
	case strings.EqualFold(key, "Content-Length"):
		return false
	case strings.HasPrefix(key, "Openai-"), strings.HasPrefix(key, "X-Ratelimit-"):
		return false
	default:
		return true
	}
}

func convertOpenAINonStreamingResponse(body io.Reader, apiFormat config.APIFormat) ([]byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	switch apiFormat {
	case config.APIFormatOpenAIChat:
		return apitransform.OpenAIChatToAnthropic(data)
	case config.APIFormatOpenAIResponses:
		return apitransform.OpenAIResponsesToAnthropic(data)
	default:
		return data, nil
	}
}

func streamOpenAIStreamingResponse(body io.Reader, apiFormat config.APIFormat) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		var err error
		switch apiFormat {
		case config.APIFormatOpenAIChat:
			err = apitransform.StreamOpenAIChatSSEToAnthropic(body, pw)
		case config.APIFormatOpenAIResponses:
			err = apitransform.StreamOpenAIResponsesSSEToAnthropic(body, pw)
		default:
			_, err = io.Copy(pw, body)
		}
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()
	return pr
}

func convertOpenAIStreamingResponse(body io.Reader, apiFormat config.APIFormat) ([]byte, error) {
	data, err := io.ReadAll(streamOpenAIStreamingResponse(body, apiFormat))
	if err != nil {
		return nil, err
	}
	switch apiFormat {
	case config.APIFormatOpenAIChat, config.APIFormatOpenAIResponses:
		return data, nil
	default:
		return data, nil
	}
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
		record.ProviderAPIURL = usage.RedactURL(provider.APIURL)
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

// Diagnostics 透传 SSE 诊断信息，供异常日志使用。
func (o *streamUsageObserver) Diagnostics() usage.SSEDiagnostics {
	return o.usage.Diagnostics()
}

// streamAnomalyPayload 构建单行可解析 JSON 的 SSE 异常日志负载。
// 安全红线：只 marshal SSEDiagnostics + response_bytes + summarizeRequestParams 的安全输出 + redacted upstream。
// 绝不包含 raw body、text/thinking/error.message 内容或原始 SSE payload。
func streamAnomalyPayload(reqID string, responseBytes int64, diag usage.SSEDiagnostics, requestBody []byte, backendURL string) string {
	// summarizeRequestParams 返回有界 JSON 字符串（不含 prompt/text/schema 值）
	safeParams := json.RawMessage(summarizeRequestParams(requestBody))

	payload := struct {
		RequestID     string               `json:"request_id"`
		ResponseBytes int64                `json:"response_bytes"`
		Upstream      string               `json:"upstream"`
		RequestParams json.RawMessage      `json:"request_params"`
		Diagnostics   usage.SSEDiagnostics `json:"diagnostics"`
	}{
		RequestID:     reqID,
		ResponseBytes: responseBytes,
		Upstream:      formatUpstreamLogTarget(backendURL),
		RequestParams: safeParams,
		Diagnostics:   diag,
	}

	out, err := json.Marshal(payload)
	if err != nil {
		// 万一 marshal 失败，返回最小安全占位
		return fmt.Sprintf(`{"request_id":%q,"error":"payload_marshal_failed","complete":%v,"error_events":%d}`, reqID, diag.Complete, diag.ErrorEvents)
	}
	return string(out)
}

// transformRequest 转换请求体。
// backendModel 是由 Config.ResolveModel 解析出的、应写入后端请求体的模型名
// （暴露模型命中 → BackendModel；fallback → active.MapModel 结果）。
// MultimodalSwitch 触发时覆盖为 MultimodalModel。
func (h *Handler) transformRequest(body []byte, provider *config.Provider, backendModel string) ([]byte, error) {
	if provider == nil {
		return body, nil // 无 provider（BackendURL 兼容模式），不转换
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}

	changed := false

	if providerAPIFormat(provider) == config.APIFormatAnthropic && provider.StripUnknownContentBlocks {
		if cleanChanged := proactiveCleanUnknownContentTypes(req); cleanChanged {
			changed = true
		}
	}

	// 模型替换：以 ResolveModel 的结果为基础，叠加多模态 override
	if model, ok := req["model"].(string); ok {
		finalModel := backendModel
		if provider.MultimodalSwitch && provider.MultimodalModel != "" && requestContainsNonTextContent(req) {
			finalModel = provider.MultimodalModel
		}
		if finalModel != model {
			req["model"] = finalModel
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

	if providerAPIFormat(provider) == config.APIFormatOpenAIChat {
		anthropicBody := body
		if changed {
			out, err := json.Marshal(req)
			if err != nil {
				return body, err
			}
			anthropicBody = out
		}
		return apitransform.AnthropicToOpenAIChatWithOptions(anthropicBody, provider.OpenAIExtraParams, apitransform.Options{
			ClaudeCodeCompatHint: provider.UseClaudeCodeCompatHint(),
		})
	}
	if providerAPIFormat(provider) == config.APIFormatOpenAIResponses {
		anthropicBody := body
		if changed {
			out, err := json.Marshal(req)
			if err != nil {
				return body, err
			}
			anthropicBody = out
		}
		return apitransform.AnthropicToOpenAIResponsesWithOptions(anthropicBody, provider.OpenAIExtraParams, apitransform.Options{
			ClaudeCodeCompatHint: provider.UseClaudeCodeCompatHint(),
		})
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

// copyUpstreamHeaders copies request headers to dst with provider-aware filtering:
//   - Skips Host, Accept-Encoding, TE
//   - Skips Anthropic-Version/Anthropic-Beta for non-Anthropic apiFormat
//   - Replaces Authorization/X-Api-Key with provider token if apiToken is set
func copyUpstreamHeaders(dst *http.Request, src http.Header, apiToken string, apiFormat config.APIFormat) {
	isAnthropic := apiFormat == config.APIFormatAnthropic
	hasAuth := false
	for key, values := range src {
		if !shouldForwardRequestHeader(key) {
			continue
		}
		if !isAnthropic && (strings.EqualFold(key, "Anthropic-Version") || strings.EqualFold(key, "Anthropic-Beta")) {
			continue
		}
		// anthropic 格式：剥离 Anthropic-Beta 里的 context-1m 条目。
		// 设计取舍：mcc 定位为第三方 provider 透明代理（GLM/DeepSeek/MiniMax 等），这些后端
		// 通常不认 Anthropic 的 context-1m beta；mcc 注入 [1m] 仅为让客户端正确判定上下文窗口，
		// 不应让该 beta 到达后端引发兼容问题。其他 beta（如 interleaved-thinking）保留。
		// 若将来需要对接官方 Anthropic 或真正依赖该 beta 的后端，可在此处加 provider 级开关。
		if isAnthropic && strings.EqualFold(key, "Anthropic-Beta") {
			for _, v := range stripContext1MBeta(values) {
				dst.Header.Add(key, v)
			}
			continue
		}
		if strings.EqualFold(key, "Accept-Encoding") || strings.EqualFold(key, "TE") {
			continue
		}
		if apiToken != "" && (strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "X-Api-Key")) {
			if !hasAuth {
				hasAuth = true
				dst.Header.Set("Authorization", "Bearer "+apiToken)
			}
			continue
		}
		for _, value := range values {
			dst.Header.Add(key, value)
		}
	}
	if !hasAuth && apiToken != "" {
		dst.Header.Set("Authorization", "Bearer "+apiToken)
	}
}

// stripContext1MBeta 从 Anthropic-Beta header 值中剥离 context-1m 系列 beta 条目，
// 保留其余 beta。每个 value 可能是单个 beta 或逗号分隔的多个 beta；
// 剥离后若某 value 变空则整体丢弃。
func stripContext1MBeta(values []string) []string {
	var result []string
	for _, v := range values {
		var kept []string
		for _, part := range strings.Split(v, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "context-1m") {
				continue
			}
			kept = append(kept, trimmed)
		}
		if len(kept) > 0 {
			result = append(result, strings.Join(kept, ","))
		}
	}
	return result
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

// proactiveCleanUnknownContentTypes strips non-standard content blocks from messages
// before forwarding to third-party upstreams. Returns true if any blocks were removed.
func proactiveCleanUnknownContentTypes(req map[string]any) bool {
	messages, ok := req["messages"].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if filterContentBlocks(msg) {
			changed = true
		}
	}
	return changed
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
	apiFormat config.APIFormat,
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

	log.Printf("[Rectifier] Cleanup applied, retrying request to %s", formatUpstreamLogTarget(backendURL))

	// 重建重试请求（继承客户端 context，客户端断开时快速取消重试，避免无限占用供应商资源）。
	retryReq, err := http.NewRequestWithContext(origReq.Context(), origReq.Method, backendURL, bytes.NewReader(cleanedBody))
	if err != nil {
		log.Printf("[Rectifier] Failed to create retry request: %v", err)
		return nil, restoredBody()
	}

	// 复制原始请求头
	copyUpstreamHeaders(retryReq, origReq.Header, apiToken, apiFormat)

	retryResp, err := client.Do(retryReq)
	if err != nil {
		log.Printf("[Rectifier] Retry request failed: %v", err)
		return nil, restoredBody()
	}

	resp.Body.Close()
	return retryResp, nil
}
