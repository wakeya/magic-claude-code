package failover

import (
	"sync"
	"time"

	"magic-claude-code/internal/config"
)

// Manager 协调供应商摘除状态、候选选择与原子默认供应商切换。
//
// 设计要点：
//   - 候选选择与状态变更在同一把 m.mu 下完成，保证并发请求看到一致的摘除视图。
//   - 默认供应商切换用 configStore.Update 的原子 compare-and-set（仅当当前 active 仍是
//     失败供应商时才改），天然产生「单一赢家」——并发请求里只有一个能完成切换并写
//     switched 事件，其余看到 active 已变即放弃。
//   - 只切换全局默认供应商（ActiveProviderID）；绝不影响 ExposedModel（/model 会话路由）。
//   - 事件是 MCC 全局观测数据，不写入 Claude JSONL。
type Manager struct {
	mu          sync.Mutex
	store       *Store
	configStore config.ConfigStore
}

// NewManager 创建故障切换管理器。
func NewManager(store *Store, configStore config.ConfigStore) *Manager {
	if store == nil {
		panic("failover: NewManager requires a non-nil store")
	}
	if configStore == nil {
		panic("failover: NewManager requires a non-nil configStore")
	}
	return &Manager{store: store, configStore: configStore}
}

// Store 返回底层状态/事件存储（供 admin API 直接读取事件列表）。
func (m *Manager) Store() *Store { return m.store }

// QuarantineFailed 把失败供应商写入摘除状态（幂等：重复写只是覆盖）。
// DisabledUntil 零值（凭据失效）表示无时间恢复，需管理员行动清除。
func (m *Manager) QuarantineFailed(failedID string, cls Classification) {
	if failedID == "" || !cls.Eligible {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// 凭据失效优先级最高：已存在凭据状态时不被低优先级（额度/可用性）覆盖，
	// 因为凭据状态只能靠 Token 变更/测试成功清除，时间到期不该误清。
	if st, ok := m.store.GetState(failedID); ok && st.Kind == StateKindCredential && cls.Kind != StateKindCredential {
		return
	}
	_ = m.store.SetState(FailoverState{
		ProviderID:    failedID,
		Kind:          cls.Kind,
		Reason:        cls.Reason,
		UpstreamCode:  cls.UpstreamCode,
		DisabledUntil: cls.DisabledUntil,
		UpdatedAt:     time.Now().UTC(),
	})
}

// SelectCandidates 返回有序候选供应商列表（排除失败来源、摘除中、禁用的）：
// 先匹配相同映射模型的供应商，再按配置顺序追加其余候选。providers 应为当前
// 配置顺序的完整供应商列表（由调用方的不可变 cfg 提供）。
func (m *Manager) SelectCandidates(failedID, originalModel, mappedModel string, providers []config.Provider) []config.Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	quarantined := make(map[string]bool)
	for _, st := range m.store.ListQuarantined(now) {
		quarantined[st.ProviderID] = true
	}

	var sameModel, others []config.Provider
	for i := range providers {
		p := providers[i]
		if p.ID == failedID {
			continue
		}
		if !p.Enabled {
			continue
		}
		if quarantined[p.ID] {
			continue
		}
		cp := p // 值拷贝，避免调用方持有可变引用
		if originalModel != "" && cp.MapModel(originalModel) == mappedModel {
			sameModel = append(sameModel, cp)
		} else {
			others = append(others, cp)
		}
	}
	return append(sameModel, others...)
}

// CommitSwitch 原子地把默认供应商从 fromID 切到 toID（仅当当前 active 仍是 fromID），
// 切换成功时记录一条 switched 事件。返回是否实际完成切换（单一赢家）。
func (m *Manager) CommitSwitch(fromID, toID, originalModel, mappedModel string, cls Classification) bool {
	if fromID == "" || toID == "" || fromID == toID {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var switched bool
	var fromName, toName string
	_, _ = m.configStore.Update(func(c *config.Config) error {
		// 与「有效默认供应商」比较，而非存储的 ActiveProviderID 字符串：
		// 当 ActiveProviderID 为空或指向 disabled provider 时，GetActiveProvider 会回退到
		// 第一个 enabled provider；该回退来源失败也必须能把切换持久化（写入 ActiveProviderID）。
		effective := c.GetActiveProvider()
		if effective == nil || effective.ID != fromID {
			return nil // 已被另一个并发请求切换，或失败来源已不是当前默认 → 不是赢家
		}
		to := c.GetProviderByID(toID)
		if to == nil || !to.Enabled {
			return nil
		}
		fromName = effective.Name
		toName = to.Name
		c.ActiveProviderID = toID
		switched = true
		return nil
	})

	if switched {
		disabled := cls.DisabledUntil
		_ = m.store.InsertEvent(EventInput{
			OccurredAt:       time.Now().UTC(),
			FromProviderID:   fromID,
			ToProviderID:     toID,
			FromProviderName: fromName,
			ToProviderName:   toName,
			OriginalModel:    originalModel,
			MappedModel:      mappedModel,
			UpstreamCode:     cls.UpstreamCode,
			BusinessCode:     cls.BusinessCode,
			Reason:           cls.Reason,
			Outcome:          OutcomeSwitched,
			DisabledUntil:    disabled,
		})
	}
	return switched
}

// RecordExhausted 记录「所有候选均失败/不可用」事件，不改默认供应商。
func (m *Manager) RecordExhausted(failedID, originalModel, mappedModel string, cls Classification, candidates []config.Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.store.InsertEvent(EventInput{
		OccurredAt:      time.Now().UTC(),
		FromProviderID:  failedID,
		OriginalModel:   originalModel,
		MappedModel:     mappedModel,
		UpstreamCode:    cls.UpstreamCode,
		BusinessCode:    cls.BusinessCode,
		Reason:          cls.Reason,
		Outcome:         OutcomeExhausted,
		DisabledUntil:   cls.DisabledUntil,
	})
}

// ClearCredentialFailure 在 Token 实际变更或供应商测试成功后清除凭据失效状态。
// 仅当 tokenChanged || testSucceeded 才清除；且只清 credential 类别，绝不清额度状态。
// 清除成功时记录一条 recovered 事件。
func (m *Manager) ClearCredentialFailure(providerID string, tokenChanged, testSucceeded bool) bool {
	if !tokenChanged && !testSucceeded {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.store.GetState(providerID)
	if !ok || st.Kind != StateKindCredential {
		return false
	}
	_ = m.store.DeleteState(providerID)
	_ = m.store.InsertEvent(EventInput{
		OccurredAt:       time.Now().UTC(),
		FromProviderID:   providerID,
		FromProviderName: "",
		Outcome:          OutcomeRecovered,
		Reason:           "credential_invalid",
	})
	return true
}

// ReconcileQuotaSnapshot 根据新鲜额度快照调整状态：100% 耗尽则（额度）摘除至 reset；
// 容量恢复则清除额度状态并记录 recovered。绝不触碰凭据失效状态（额度恢复 ≠ 凭据恢复）。
func (m *Manager) ReconcileQuotaSnapshot(providerID string, exhausted bool, reset time.Time) {
	if providerID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.store.GetState(providerID)
	if exhausted {
		// 已是凭据失效：不覆盖（凭据优先级高于额度）。
		if ok && st.Kind == StateKindCredential {
			return
		}
		disabled := reset
		if disabled.IsZero() {
			disabled = time.Now().Add(cooldownQuotaFallback)
		}
		_ = m.store.SetState(FailoverState{
			ProviderID:    providerID,
			Kind:          StateKindQuota,
			Reason:        "quota_snapshot_exhausted",
			DisabledUntil: disabled,
			UpdatedAt:     time.Now().UTC(),
		})
		return
	}
	// 容量恢复：仅清额度状态。
	if ok && st.Kind == StateKindQuota {
		_ = m.store.DeleteState(providerID)
		_ = m.store.InsertEvent(EventInput{
			OccurredAt:     time.Now().UTC(),
			FromProviderID: providerID,
			Outcome:        OutcomeRecovered,
			Reason:         "quota_snapshot_exhausted",
		})
	}
}

// OnProviderDeleted 清除已删除供应商的摘除状态（事件保留，ID 在列表时抹空）。
func (m *Manager) OnProviderDeleted(providerID string) {
	if providerID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.store.DeleteState(providerID)
}

// State 返回供应商当前摘除状态（供 admin/诊断读取）。
func (m *Manager) State(providerID string) (FailoverState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.GetState(providerID)
}

// Events 返回最近的事件列表（最新优先）。knownProviderIDs 用于抹空已删除供应商的 ID。
func (m *Manager) Events(limit int, knownProviderIDs map[string]bool) []FailoverEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.ListEvents(limit, knownProviderIDs)
}

// ListQuarantined 返回当前仍摘除的供应商状态（供 admin/代理判断候选池）。
func (m *Manager) ListQuarantined() []FailoverState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.ListQuarantined(time.Now())
}
