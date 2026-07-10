package proxy

import "net/http"

// handleDesignConsent 处理 /v1/design/consent：
//   - GET    -> 200 {"agent_design_projects":false}
//   - POST   -> 204（接受修改，本地 no-op）
//   - DELETE -> 204（撤销，本地 no-op）
//   - 其它   -> 405
func (h *Handler) handleDesignConsent(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSONResponse(w, http.StatusOK, map[string]any{
			"agent_design_projects": false,
		})
	case http.MethodPost, http.MethodDelete:
		writeNoContent(w)
	default:
		methodAllowed(w, r, http.MethodGet, http.MethodPost, http.MethodDelete)
	}
}

// handleDesignMCP 处理 POST /v1/design/mcp：返回受控 unsupported 错误，
// 不实现 JSON-RPC MCP bridge，不发起外部请求。其它方法返回 405。
func (h *Handler) handleDesignMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodAllowed(w, r, http.MethodPost)
		return
	}
	writeJSONResponse(w, http.StatusForbidden, map[string]any{
		"error": map[string]any{
			"type":    "unsupported_local_endpoint",
			"message": "Claude Design is unavailable in MCC local mode",
		},
	})
}

// handleUnsupportedStreamingEndpoint 拦截 WebSocket/语音流端点。
// 任意方法返回 501，不 upgrade、不 hijack connection、不读取流。
// 请求体已在 handleHardcodedEndpoint 中 drain。
func (h *Handler) handleUnsupportedStreamingEndpoint(w http.ResponseWriter) {
	writeJSONResponse(w, http.StatusNotImplemented, map[string]any{
		"error": map[string]any{
			"type":    "unsupported_local_endpoint",
			"message": "Streaming endpoint is unavailable in MCC local mode",
		},
	})
}
