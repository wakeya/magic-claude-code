package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"magic-claude-code/internal/config"
)

// handleProviderOrder 处理 PUT /api/providers/order —— 调整供应商列表顺序（= 自动切换优先级）。
//
// 校验（严格）：
//   - 未认证 → 401（由 authMiddlewareFunc 保证）；非 PUT → 405。
//   - JSON 解析失败 / provider_ids 缺失或非数组 → 400。
//   - 重复 ID → 400；未知 ID（不在当前配置）→ 400。
//   - 当前配置非空但提交空数组 → 409；漏掉已有 provider 或长度不一致 → 409；并发增删导致集合不匹配 → 409。
//   - 当前为空且提交空数组 → 200，返回空 providers。
//
// 通过 configStore.Update 原子处理；不得改变 ActiveProviderID；返回脱敏 provider 列表。
func (s *Server) handleProviderOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProviderIDs []string `json:"provider_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.ProviderIDs == nil {
		http.Error(w, `{"error": "provider_ids is required"}`, http.StatusBadRequest)
		return
	}
	// 重复 ID（请求自身问题）→ 400。
	seen := make(map[string]bool, len(req.ProviderIDs))
	for _, id := range req.ProviderIDs {
		if seen[id] {
			http.Error(w, `{"error": "duplicate provider id"}`, http.StatusBadRequest)
			return
		}
		seen[id] = true
	}

	bothEmpty := false
	committed, err := s.configStore.Update(func(cfg *config.Config) error {
		currentCount := len(cfg.Providers)
		if currentCount == 0 && len(req.ProviderIDs) == 0 {
			bothEmpty = true
			return nil
		}
		if currentCount > 0 && len(req.ProviderIDs) == 0 {
			return errOrderConflict // 空数组对非空配置 → 409
		}
		// 当前 ID 集合。
		curIDs := make(map[string]bool, currentCount)
		for i := range cfg.Providers {
			curIDs[cfg.Providers[i].ID] = true
		}
		// 未知 ID → 400。
		for _, id := range req.ProviderIDs {
			if !curIDs[id] {
				return errOrderUnknownID
			}
		}
		// 漏掉已有 provider（当前有但请求没有）→ 409；长度不一致也在此体现。
		for i := range cfg.Providers {
			if !seen[cfg.Providers[i].ID] {
				return errOrderConflict
			}
		}
		// 按请求顺序重排。不改 provider 内容字段、不改 ActiveProviderID。
		byID := make(map[string]config.Provider, currentCount)
		for i := range cfg.Providers {
			byID[cfg.Providers[i].ID] = cfg.Providers[i]
		}
		reordered := make([]config.Provider, 0, len(req.ProviderIDs))
		for _, id := range req.ProviderIDs {
			reordered = append(reordered, byID[id])
		}
		cfg.Providers = reordered
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, errOrderUnknownID):
			http.Error(w, `{"error": "unknown provider id"}`, http.StatusBadRequest)
		case errors.Is(err, errOrderConflict):
			http.Error(w, `{"error": "provider set out of sync"}`, http.StatusConflict)
		case config.IsValidationError(err):
			body, _ := json.Marshal(map[string]string{"error": err.Error()})
			http.Error(w, string(body), http.StatusBadRequest)
		default:
			http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if bothEmpty {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "providers": []any{}})
		return
	}
	providers := committed.Providers
	resp := make([]map[string]any, len(providers))
	for i, p := range providers {
		resp[i] = providerResponseMap(p, p.ID == committed.ActiveProviderID)
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "providers": resp})
}
