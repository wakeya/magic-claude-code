package proxy

import "net/http"

// handleTriggersEndpoint 处理 CCR 触发器端点（CC 2.1.211），全部本地响应，不转发上游。
//
// 路由契约（源码 yi client，auth:"teleport-org"，anthropic-beta: ccr-triggers-2026-01-30）：
//   - GET /v1/code/triggers 或 /v1/code/triggers/{id}[/run] -> 200 {"data":[]}
//     GET 列表失败会 throw "triggers unavailable"，空列表让客户端静默拿到空结果，
//     避免启动/加载流程中被 throw 中断。GET 单个返回列表形态，语义不完美但客户端不抛异常。
//   - POST（create/update/run）-> 403 write_gate_disabled
//     POST 失败 throw "Remote triggers unavailable"，写入门关闭语义与 frame deploy 一致。
//   - 其它方法 -> 405
//
// 请求体已在 handleHardcodedEndpoint 中 drain（drainRequestBodyLimited）。
// 第三方 provider 场景不使用 CCR 触发器，目标仅是避免 GET throw 中断、POST 写入明确失败，
// 不实现真实触发器语义。
func (h *Handler) handleTriggersEndpoint(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSONResponse(w, http.StatusOK, map[string]any{"data": []any{}})
	case http.MethodPost:
		writeJSONResponse(w, http.StatusForbidden, map[string]any{
			"error":  "Triggers write is unavailable in MCC local mode",
			"reason": "write_gate_disabled",
		})
	default:
		methodAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}
