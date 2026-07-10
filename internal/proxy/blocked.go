package proxy

import (
	"log"
	"net/http"
	"strings"
)

// maxBlockedUserAgentLog 限制被拦截请求 User-Agent 在日志中的长度，避免超长 UA 污染日志。
const maxBlockedUserAgentLog = 200

// handleBlockedEndpoint 是 fail-closed guard 的兜底出口：对不允许转发到上游 provider 的
// 请求返回稳定的本地 JSON 错误，并打印一条安全日志（仅 method/host/path/query 是否存在/
// user agent/status/reason），绝不记录请求体、Authorization、Cookie、API key 或原始 query。
//
// status 取 http.StatusNotFound（未知非模型端点）或 http.StatusMethodNotAllowed（模型端点
// 路径但方法非 POST）；reason 用于日志与错误体定位。
func (h *Handler) handleBlockedEndpoint(w http.ResponseWriter, r *http.Request, status int, reason string) {
	// 有界消耗并关闭请求体（最多 maxLocalDrainSize），防止未知端点大体积 body 造成无界 drain DoS。
	drainRequestBodyLimited(r, maxLocalDrainSize)

	path := r.URL.Path

	// 安全日志：只记录定位信息，不记录任何敏感数据。
	logBlockedEndpoint(r, status, reason)

	w.Header().Set("Content-Type", "application/json")
	switch status {
	case http.StatusMethodNotAllowed:
		// 模型端点路径只接受 POST。
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(status)
		writeBlockedJSONError(w, "method_not_allowed", "Only POST is allowed for model inference on this endpoint", "")
	default:
		status = http.StatusNotFound
		w.WriteHeader(status)
		writeBlockedJSONError(w, "mcc_blocked_unknown_endpoint",
			"MCC blocked an unrecognized non-model endpoint", path)
	}
}

// writeBlockedJSONError 写出统一形状的错误体。
// extraPath 非空时附加顶层 "path" 字段（仅 r.URL.Path，无 query，无敏感信息）。
func writeBlockedJSONError(w http.ResponseWriter, errType, message, extraPath string) {
	body := map[string]any{
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	}
	if extraPath != "" {
		body["path"] = extraPath
	}
	encodeJSONBody(w, body)
}

// logBlockedEndpoint 打印单行拦截日志。安全红线：
//   - 只记录 method / host / path(r.URL.Path) / query 是否存在 / 截断 UA / status / reason
//   - 绝不记录 RawQuery 原始值、请求体、Authorization、Cookie、X-Api-Key
//   - 对所有字符串字段做控制字符 sanitize，防止 URL 编码换行（%0a）造成的日志注入（CWE-117）
func logBlockedEndpoint(r *http.Request, status int, reason string) {
	ua := r.UserAgent()
	if len(ua) > maxBlockedUserAgentLog {
		ua = ua[:maxBlockedUserAgentLog] + "…"
	}
	log.Printf("[Hardcoded] Blocking endpoint method=%s host=%s path=%s query_present=%t status=%d reason=%s ua=%q",
		sanitizeLogField(r.Method), sanitizeLogField(r.Host), sanitizeLogField(r.URL.Path),
		r.URL.RawQuery != "", status, sanitizeLogField(reason), sanitizeLogField(ua))
}

// sanitizeLogField 把控制字符（U+0000–U+001F、U+007F）替换为可见占位符，
// 防止请求路径/UA/Host 中的换行等控制字符伪造日志行（日志注入）。
func sanitizeLogField(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '·'
		}
		return r
	}, s)
}
