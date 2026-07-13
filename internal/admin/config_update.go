package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"magic-claude-code/internal/config"
)

// 更新流程中用于在 ConfigStore.Update 的 mutator 内部表达特定 HTTP 语义的哨兵错误。
// mutator 只能返回 error，调用方通过这些哨兵把错误映射到合适的 HTTP 状态码。
var (
	// errAdminProviderNotFound 表示在原子更新窗口内未找到目标供应商 → 404。
	errAdminProviderNotFound = errors.New("provider not found")
	// errAdminMultimodalModelRequired 表示更新后启用多模态但未指定模型 → 400。
	errAdminMultimodalModelRequired = errors.New("multimodal_model is required when multimodal_switch is enabled")
	// errAdminInvalidConnectionMode 表示请求的 connection_mode 非法 → 400。
	errAdminInvalidConnectionMode = errors.New("invalid connection_mode")
	// errAdminProviderNotEnabled 表示激活时供应商未启用 → 400。
	errAdminProviderNotEnabled = errors.New("provider is not enabled")
	// errOrderUnknownID 表示排序请求包含当前不存在的供应商 ID → 400。
	errOrderUnknownID = errors.New("unknown provider id")
	// errOrderConflict 表示排序请求与当前供应商集合不一致（漏 ID / 长度不符 / 空数组对非空配置 / 并发增删）→ 409。
	errOrderConflict = errors.New("provider set out of sync")
)

// writeConfigUpdateError 把 ConfigStore.Update 返回的错误映射为 HTTP 响应：
//   - 已知请求级哨兵（errAdminProviderNotFound）→ 404
//   - 请求级校验哨兵 / config.ValidationError → 400（响应体保留原错误文案）
//   - 其他（Load/Save 失败）→ 500
//
// 设计目标：让原子 Update 既能防止并发写互相覆盖，又不破坏既有 handler 的
// 400/404/500 语义与错误文案（既有测试依赖状态码与消息内容）。
func writeConfigUpdateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAdminProviderNotFound):
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
	case errors.Is(err, errAdminMultimodalModelRequired):
		http.Error(w, `{"error": "multimodal_model is required when multimodal_switch is enabled"}`, http.StatusBadRequest)
	case config.IsValidationError(err):
		body, _ := json.Marshal(map[string]string{"error": err.Error()})
		http.Error(w, string(body), http.StatusBadRequest)
	default:
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
	}
}
