package proxy

import (
	"net/http"
	"strings"
)

// endpointAction 描述 classifyForwardingEndpoint 对一个 (method,path) 的处置。
//
// 这是 fail-closed guard 的核心：只有显式模型推理端点才允许转发到上游 provider。
// 所有未识别路径一律落到 endpointActionBlock，由 handleBlockedEndpoint 本地拦截。
type endpointAction int

const (
	// endpointActionBlock 表示该端点不得转发到上游 provider，由本地拦截。
	endpointActionBlock endpointAction = iota
	// endpointActionForwardModel 表示该端点是允许转发的模型推理请求。
	endpointActionForwardModel
	// endpointActionMethodNotAllowed 表示命中模型端点路径但方法不是 POST。
	endpointActionMethodNotAllowed
)

// endpointDecision 是 classifier 的输出，携带 action 与用于日志/诊断的 reason。
type endpointDecision struct {
	action endpointAction
	reason string
}

// modelForwardPaths 是唯一允许转发到配置 provider 的模型推理端点路径。
// 只用路径（不含 query）判定，方法必须为 POST。
var modelForwardPaths = map[string]struct{}{
	"/v1/messages":           {},
	"/anthropic/v1/messages": {},
}

// classifyForwardingEndpoint 对标准化后的 (method, path) 做转发决策。
//
// 规则（spec「端点策略」表）：
//   - POST /v1/messages 或 POST /anthropic/v1/messages -> ForwardModel
//   - 这两个路径的任意非 POST 方法 -> MethodNotAllowed（本地 405，不得转发）
//   - 其他一切 -> Block（本地拦截）
//
// 安全：只使用 path 分类。即便调用方误传含 query 的原始路径，这里也防御性剥离 '?'
// 之后的内容，确保 query 永不影响转发判定（spec 约束：query 不能决定是否转发）。
func classifyForwardingEndpoint(method, path string) endpointDecision {
	// 防御性规范化：剥离 query/fragment（正常流程下 ServeHTTP 传入的是 r.URL.Path，已不含 query）。
	if idx := strings.IndexAny(path, "?#"); idx >= 0 {
		path = path[:idx]
	}

	_, isModelPath := modelForwardPaths[path]
	if !isModelPath {
		return endpointDecision{action: endpointActionBlock, reason: "unknown_non_model_endpoint"}
	}

	// 命中模型端点路径：只有 POST 允许转发，其余方法本地 405。
	if method == http.MethodPost {
		return endpointDecision{action: endpointActionForwardModel, reason: "model_inference"}
	}
	return endpointDecision{action: endpointActionMethodNotAllowed, reason: "method_not_allowed"}
}
