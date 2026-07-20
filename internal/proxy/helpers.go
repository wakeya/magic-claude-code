package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
)

// encodeJSONBody 只编码 JSON body，不设置 Content-Type 或状态码。
// 供已经手动设置了 header/status 的 handler 使用（例如需要同时设置 Allow 头的 405 响应）。
func encodeJSONBody(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// 编码失败极罕见；记日志但不 panic，避免影响连接。
		log.Printf("Error encoding JSON response: %v", err)
	}
}

// writeNoContent 写入 204 空响应（遥测、frame track 等无返回数据的端点）。
func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// methodAllowed 检查请求方法是否在允许列表内；不允许时写 405 + Allow 头并返回 false。
// 调用方应在返回 false 时立即 return，不再继续处理。
func methodAllowed(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	if slices.Contains(allowed, r.Method) {
		return true
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusMethodNotAllowed)
	encodeJSONBody(w, map[string]any{
		"error": map[string]any{
			"type":    "method_not_allowed",
			"message": "Unsupported method for this local endpoint",
		},
	})
	return false
}

// formatModelLog 构造请求/响应日志里的 model 字段。
// 命中 ExposedModel 时优先用人类可读的 Label 替代原始 em-<hex> ID（仅影响日志展示，
// 不改路由/请求体）；mappedModel 与展示模型相同时折叠为单 token，
// 与未做模型映射时的行为一致。
func formatModelLog(originalModel, mappedModel, exposedLabel string) string {
	display := originalModel
	if exposedLabel != "" {
		display = exposedLabel
	}
	if mappedModel == display {
		return display
	}
	return fmt.Sprintf("%s -> %s", display, mappedModel)
}
