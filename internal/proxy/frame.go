package proxy

import (
	"net/http"
	"strings"
)

// handleFrameEndpoint 处理 Frame artifact 相关端点，全部本地响应，不转发上游。
//
// 路由顺序（见 spec「本地响应契约」Frame 行）：
//  1. GET /api/frame/frames        -> 200 {"frames":[]}
//  2. POST /api/frame/track        -> 204
//  3. POST /api/frame/deploy/complete -> 204
//  4. POST /api/frame/deploy/init|direct -> 403 write_gate_disabled
//  5. GET /api/frame/contract/*    -> 404 local_unavailable
//  6. GET|DELETE /api/frame/{slug} -> 404 not_found
//  7. 其它方法                      -> 405
//
// 请求体已在 handleHardcodedEndpoint 中 drain；匹配时忽略 query string。
func (h *Handler) handleFrameEndpoint(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	// 列表 - GET only，query（如 ?limit=200）已由 r.URL.Path 剥离
	case path == "/api/frame/frames":
		if !methodAllowed(w, r, http.MethodGet) {
			return
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{"frames": []any{}})

	// track - POST 204 no-op
	case path == "/api/frame/track":
		if !methodAllowed(w, r, http.MethodPost) {
			return
		}
		writeNoContent(w)

	// deploy complete - POST 204 no-op
	case path == "/api/frame/deploy/complete":
		if !methodAllowed(w, r, http.MethodPost) {
			return
		}
		writeNoContent(w)

	// deploy init/direct - POST 403，客户端发布路径能识别 write_gate_disabled
	case path == "/api/frame/deploy/init" || path == "/api/frame/deploy/direct":
		if !methodAllowed(w, r, http.MethodPost) {
			return
		}
		writeJSONResponse(w, http.StatusForbidden, map[string]any{
			"error":  "Frame publishing is unavailable in MCC local mode",
			"reason": "write_gate_disabled",
		})

	// contract - GET 404，不伪造 contract 数据（客户端会校验 version）
	case strings.HasPrefix(path, "/api/frame/contract/"):
		if !methodAllowed(w, r, http.MethodGet) {
			return
		}
		writeJSONResponse(w, http.StatusNotFound, map[string]any{
			"error":  "Frame contract service is unavailable in MCC local mode",
			"reason": "local_unavailable",
		})

	// 其它 slug - GET/DELETE 404 not_found
	default:
		if methodAllowed(w, r, http.MethodGet, http.MethodDelete) {
			writeJSONResponse(w, http.StatusNotFound, map[string]any{
				"error":  "Artifact not found in MCC local mode",
				"reason": "not_found",
			})
		}
	}
}
