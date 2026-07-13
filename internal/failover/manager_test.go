package failover

import (
	"sync"
	"testing"
	"time"

	"magic-claude-code/internal/config"
)

func newTestManager(t *testing.T, providers []config.Provider, activeID string) (*Manager, *config.MockStore) {
	t.Helper()
	store := newTestStore(t)
	cfg := config.DefaultConfig()
	cfg.Providers = providers
	cfg.ActiveProviderID = activeID
	cfgStore := config.NewMockStore(cfg)
	m := NewManager(store, cfgStore)
	return m, cfgStore
}

func providersForTest() []config.Provider {
	return []config.Provider{
		{ID: "a", Name: "Alpha", Enabled: true, APIURL: "https://a", APIFormat: config.APIFormatAnthropic, ModelMappings: map[string]string{"claude-opus-4-8": "glm-5.2"}},
		{ID: "b", Name: "Bravo", Enabled: true, APIURL: "https://b", APIFormat: config.APIFormatAnthropic, ModelMappings: map[string]string{"claude-opus-4-8": "glm-5.2"}},
		{ID: "c", Name: "Charlie", Enabled: true, APIURL: "https://c", APIFormat: config.APIFormatAnthropic, ModelMappings: map[string]string{"claude-opus-4-8": "kimi"}},
		{ID: "d", Name: "Delta", Enabled: false, APIURL: "https://d", APIFormat: config.APIFormatAnthropic, ModelMappings: map[string]string{"claude-opus-4-8": "glm-5.2"}},
	}
}

func TestSelectCandidatesSameMappedModelFirst(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	// 失败 provider=a，映射模型 glm-5.2：b 同模型优先，c 不同模型其次，d 禁用排除。
	got := m.SelectCandidates("a", "claude-opus-4-8", "glm-5.2", providersForTest())
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (b + c)", len(got))
	}
	if got[0].ID != "b" {
		t.Fatalf("first candidate must be same-mapped-model provider b, got %s", got[0].ID)
	}
	if got[1].ID != "c" {
		t.Fatalf("second candidate must be fallback c, got %s", got[1].ID)
	}
}

func TestSelectCandidatesExcludesQuarantinedDisabledAndFailed(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	// 摘除 b。
	m.QuarantineFailed("b", Classification{Eligible: true, Kind: StateKindAvailability, DisabledUntil: time.Now().Add(time.Minute)})
	got := m.SelectCandidates("a", "claude-opus-4-8", "glm-5.2", providersForTest())
	for _, c := range got {
		if c.ID == "b" {
			t.Fatal("quarantined provider b must not be a candidate")
		}
		if c.ID == "d" {
			t.Fatal("disabled provider d must not be a candidate")
		}
		if c.ID == "a" {
			t.Fatal("failed provider a must not be a candidate")
		}
	}
}

func TestSelectCandidatesEmptyWhenAllExcluded(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	m.QuarantineFailed("b", Classification{Eligible: true, Kind: StateKindAvailability, DisabledUntil: time.Now().Add(time.Minute)})
	m.QuarantineFailed("c", Classification{Eligible: true, Kind: StateKindAvailability, DisabledUntil: time.Now().Add(time.Minute)})
	got := m.SelectCandidates("a", "claude-opus-4-8", "glm-5.2", providersForTest())
	if len(got) != 0 {
		t.Fatalf("expected no candidates, got %v", got)
	}
}

func TestCommitSwitchCompareAndSet(t *testing.T) {
	m, cfgStore := newTestManager(t, providersForTest(), "a")
	cls := Classification{Eligible: true, Kind: StateKindQuota, Reason: "five_hour_quota_exhausted", UpstreamCode: 429, DisabledUntil: time.Now().Add(5 * time.Hour)}

	if !m.CommitSwitch("a", "b", "claude-opus-4-8", "glm-5.2", cls) {
		t.Fatal("first CommitSwitch must switch (active was a)")
	}
	loaded, _ := cfgStore.Load()
	if loaded.ActiveProviderID != "b" {
		t.Fatalf("active = %s, want b", loaded.ActiveProviderID)
	}

	// active 已是 b；再次以 fromID=a 提交不应切换（compare-and-set）。
	if m.CommitSwitch("a", "b", "claude-opus-4-8", "glm-5.2", cls) {
		t.Fatal("second CommitSwitch must NOT switch (active is no longer a)")
	}
}

func TestConcurrentFailoverSelectionHasSingleWinner(t *testing.T) {
	m, cfgStore := newTestManager(t, providersForTest(), "a")
	cls := Classification{Eligible: true, Kind: StateKindQuota, Reason: "five_hour_quota_exhausted", UpstreamCode: 429}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			m.QuarantineFailed("a", cls)
			m.CommitSwitch("a", "b", "claude-opus-4-8", "glm-5.2", cls)
		}()
	}
	wg.Wait()

	loaded, _ := cfgStore.Load()
	if loaded.ActiveProviderID != "b" {
		t.Fatalf("active = %s, want b (single winner)", loaded.ActiveProviderID)
	}
	// 仅一条 switched 事件。
	got := m.Events(100, nil)
	var switched int
	for _, e := range got {
		if e.Outcome == OutcomeSwitched {
			switched++
		}
	}
	if switched != 1 {
		t.Fatalf("switched events = %d, want exactly 1 (single winner)", switched)
	}
}

func TestRecordExhaustedEmitsEvent(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	cls := Classification{Eligible: true, Kind: StateKindQuota, Reason: "five_hour_quota_exhausted", UpstreamCode: 429}
	m.RecordExhausted("a", "claude-opus-4-8", "glm-5.2", cls, nil)
	got := m.Events(100, nil)
	if len(got) != 1 || got[0].Outcome != OutcomeExhausted {
		t.Fatalf("expected one exhausted event, got %+v", got)
	}
}

func TestCredentialFailureRequiresTokenChangeOrSuccessfulTest(t *testing.T) {
	t.Run("name only edit does not clear", func(t *testing.T) {
		m, _ := newTestManager(t, providersForTest(), "a")
		m.QuarantineFailed("a", Classification{Eligible: true, Kind: StateKindCredential, Reason: "credential_invalid", UpstreamCode: 401})
		if cleared := m.ClearCredentialFailure("a", false, false); cleared {
			t.Fatal("must not clear without token change or successful test")
		}
		if st, ok := m.State("a"); !ok || st.Kind != StateKindCredential {
			t.Fatal("credential state must remain")
		}
	})
	t.Run("token change clears", func(t *testing.T) {
		m, _ := newTestManager(t, providersForTest(), "a")
		m.QuarantineFailed("a", Classification{Eligible: true, Kind: StateKindCredential, Reason: "credential_invalid", UpstreamCode: 401})
		if !m.ClearCredentialFailure("a", true, false) {
			t.Fatal("token change must clear credential state")
		}
		if _, ok := m.State("a"); ok {
			t.Fatal("credential state must be cleared")
		}
	})
	t.Run("successful test clears", func(t *testing.T) {
		m, _ := newTestManager(t, providersForTest(), "a")
		m.QuarantineFailed("a", Classification{Eligible: true, Kind: StateKindCredential, Reason: "credential_invalid", UpstreamCode: 401})
		if !m.ClearCredentialFailure("a", false, true) {
			t.Fatal("successful test must clear credential state")
		}
	})
	t.Run("does not clear quota state", func(t *testing.T) {
		m, _ := newTestManager(t, providersForTest(), "a")
		m.QuarantineFailed("a", Classification{Eligible: true, Kind: StateKindQuota, Reason: "quota_exhausted", UpstreamCode: 429, DisabledUntil: time.Now().Add(time.Hour)})
		// 即使 token 变更，ClearCredentialFailure 也不应清除 quota 状态。
		m.ClearCredentialFailure("a", true, true)
		if st, ok := m.State("a"); !ok || st.Kind != StateKindQuota {
			t.Fatal("quota state must NOT be cleared by credential recovery")
		}
	})
}

func TestQuotaSnapshotRecoveryDoesNotClearCredentialFailure(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	// 凭据失效状态。
	m.QuarantineFailed("a", Classification{Eligible: true, Kind: StateKindCredential, Reason: "credential_invalid", UpstreamCode: 401})
	// 额度快照显示容量恢复 → 不应清除凭据状态。
	m.ReconcileQuotaSnapshot("a", false, time.Time{})
	if st, ok := m.State("a"); !ok || st.Kind != StateKindCredential {
		t.Fatal("quota snapshot recovery must NOT clear credential state")
	}
}

func TestQuotaSnapshotExhaustionQuarantinesUntilReset(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	reset := time.Now().Add(3 * time.Hour).UTC()
	m.ReconcileQuotaSnapshot("a", true, reset)
	st, ok := m.State("a")
	if !ok || st.Kind != StateKindQuota {
		t.Fatalf("expected quota quarantine from snapshot, got %+v", st)
	}
	if !st.DisabledUntil.Equal(reset) {
		t.Fatalf("DisabledUntil = %v, want %v", st.DisabledUntil, reset)
	}
}

func TestQuotaSnapshotRecoveryClearsQuotaState(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	m.QuarantineFailed("a", Classification{Eligible: true, Kind: StateKindQuota, Reason: "quota_exhausted", UpstreamCode: 429, DisabledUntil: time.Now().Add(time.Hour)})
	// 快照显示容量恢复 → 清除 quota 状态。
	m.ReconcileQuotaSnapshot("a", false, time.Time{})
	if _, ok := m.State("a"); ok {
		t.Fatal("quota state must be cleared when capacity recovers")
	}
}

func TestCommitSwitchRecordsBusinessCode(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	cls := Classification{Eligible: true, Kind: StateKindQuota, Reason: "five_hour_quota_exhausted", UpstreamCode: 429, BusinessCode: "1308"}
	m.CommitSwitch("a", "b", "claude-opus-4-8", "glm-5.2", cls)
	got := m.Events(10, nil)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	if got[0].BusinessCode != "1308" || got[0].UpstreamCode != 429 {
		t.Fatalf("event upstream=%d business=%q, want 429/1308", got[0].UpstreamCode, got[0].BusinessCode)
	}
	if got[0].Reason != "five_hour_quota_exhausted" {
		t.Fatalf("reason = %q", got[0].Reason)
	}
}

func TestCommitSwitchPersistsWhenActiveIsEmptyAndFallbackFailed(t *testing.T) {
	// ActiveProviderID 为空时 GetActiveProvider 回退到第一个 enabled provider（b）。
	// b 失败、候选 c 成功后，CommitSwitch 必须把默认供应商持久化为 c，
	// 否则后续默认请求会继续打到失败的 b。
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "b", Name: "Bravo", APIURL: "https://b", APIFormat: config.APIFormatAnthropic, Enabled: true},
		{ID: "c", Name: "Charlie", APIURL: "https://c", APIFormat: config.APIFormatAnthropic, Enabled: true},
	}
	cfg.ActiveProviderID = "" // 空 → GetActiveProvider 回退到 b
	store := newTestStore(t)
	cfgStore := config.NewMockStore(cfg)
	m := NewManager(store, cfgStore)

	// 失败来源是 effective active = b。
	if eff := cfg.GetActiveProvider(); eff == nil || eff.ID != "b" {
		t.Fatalf("setup: effective active should be b, got %+v", eff)
	}
	cls := Classification{Eligible: true, Kind: StateKindQuota, Reason: "five_hour_quota_exhausted", UpstreamCode: 429}
	if !m.CommitSwitch("b", "c", "claude-opus-4-8", "glm-5.2", cls) {
		t.Fatal("CommitSwitch must switch when effective active equals fromID")
	}
	loaded, _ := cfgStore.Load()
	if loaded.ActiveProviderID != "c" {
		t.Fatalf("ActiveProviderID = %q, want c (must persist the switch)", loaded.ActiveProviderID)
	}
}

func TestCommitSwitchPersistsWhenActivePointsToDisabled(t *testing.T) {
	// ActiveProviderID 指向 disabled provider（a）时，effective active 回退到第一个 enabled（b）。
	// b 失败后切到 c 必须持久化。
	cfg := config.DefaultConfig()
	cfg.Providers = []config.Provider{
		{ID: "a", Name: "Alpha", APIURL: "https://a", APIFormat: config.APIFormatAnthropic, Enabled: false},
		{ID: "b", Name: "Bravo", APIURL: "https://b", APIFormat: config.APIFormatAnthropic, Enabled: true},
		{ID: "c", Name: "Charlie", APIURL: "https://c", APIFormat: config.APIFormatAnthropic, Enabled: true},
	}
	cfg.ActiveProviderID = "a" // disabled → effective active 回退到 b
	store := newTestStore(t)
	cfgStore := config.NewMockStore(cfg)
	m := NewManager(store, cfgStore)

	cls := Classification{Eligible: true, Kind: StateKindAvailability, Reason: "bad_gateway", UpstreamCode: 502}
	if !m.CommitSwitch("b", "c", "claude-opus-4-8", "glm-5.2", cls) {
		t.Fatal("CommitSwitch must switch when effective active equals fromID (disabled stored active)")
	}
	loaded, _ := cfgStore.Load()
	if loaded.ActiveProviderID != "c" {
		t.Fatalf("ActiveProviderID = %q, want c", loaded.ActiveProviderID)
	}
}

func TestOnProviderDeletedClearsState(t *testing.T) {
	m, _ := newTestManager(t, providersForTest(), "a")
	m.QuarantineFailed("b", Classification{Eligible: true, Kind: StateKindAvailability, Reason: "bad_gateway", UpstreamCode: 502, DisabledUntil: time.Now().Add(time.Minute)})
	m.OnProviderDeleted("b")
	if _, ok := m.State("b"); ok {
		t.Fatal("state must be removed when provider is deleted")
	}
}
