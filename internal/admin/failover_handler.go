package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/failover"
)

// handleFailoverSettings 处理 GET/PUT /api/providers/failover —— 读取/设置自动故障切换开关。
// 开关经 Task 1 的原子 Update 持久化，避免与代理并发改 ActiveProviderID 互相覆盖。
func (s *Server) handleFailoverSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getFailoverSettings(w, r)
	case http.MethodPut:
		s.putFailoverSettings(w, r)
	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) getFailoverSettings(w http.ResponseWriter, _ *http.Request) {
	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"enabled": cfg.AutoFailoverEnabled})
}

func (s *Server) putFailoverSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.Enabled == nil {
		http.Error(w, `{"error": "enabled is required"}`, http.StatusBadRequest)
		return
	}
	if _, err := s.configStore.Update(func(cfg *config.Config) error {
		cfg.AutoFailoverEnabled = *req.Enabled
		return nil
	}); err != nil {
		// 开关字段无校验失败路径；任何错误都是存储失败 → 500。
		http.Error(w, `{"error": "failed to save config"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"enabled": *req.Enabled})
}

// handleFailoverEvents 处理 GET /api/failover/events?limit=1..100 —— 返回全局切换事件列表。
// 事件是 MCC 全局观测数据，不关联 Claude Code 会话，不写入 JSONL。已删除供应商的 ID
// 在响应中抹空（名字保留用于历史展示）。
func (s *Server) handleFailoverEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.failoverManager == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"events": []any{}})
		return
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			http.Error(w, `{"error": "invalid limit"}`, http.StatusBadRequest)
			return
		}
		limit = n
	}

	// 已知供应商 ID 集合：用于抹空已删除供应商的 ID（保留名字）。
	// providerNames 用于给早期/恢复/耗尽事件回填当前仍存在供应商的展示名称。
	known := map[string]bool{}
	providerNames := map[string]string{}
	if cfg, err := s.configStore.Load(); err == nil {
		for i := range cfg.Providers {
			p := cfg.Providers[i]
			known[p.ID] = true
			providerNames[p.ID] = p.Name
		}
	}

	events := s.failoverManager.Events(limit, known)
	if events == nil {
		events = []failover.FailoverEvent{}
	}
	backfillFailoverEventProviderNames(events, providerNames)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"events": events})
}

func backfillFailoverEventProviderNames(events []failover.FailoverEvent, providerNames map[string]string) {
	for i := range events {
		if events[i].FromProviderName == "" && events[i].FromProviderID != "" {
			if name := providerNames[events[i].FromProviderID]; name != "" {
				events[i].FromProviderName = name
			}
		}
		if events[i].ToProviderName == "" && events[i].ToProviderID != "" {
			if name := providerNames[events[i].ToProviderID]; name != "" {
				events[i].ToProviderName = name
			}
		}
	}
}
