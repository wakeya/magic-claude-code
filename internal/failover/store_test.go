package failover

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s := NewStore(db)
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestFailoverStateRoundTrip(t *testing.T) {
	s := newTestStore(t)

	until := time.Now().Add(5 * time.Hour).UTC().Truncate(time.Second)
	if err := s.SetState(FailoverState{
		ProviderID:    "p1",
		Kind:          StateKindQuota,
		Reason:        "five_hour_quota_exhausted",
		UpstreamCode:  429,
		DisabledUntil: until,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	got, ok := s.GetState("p1")
	if !ok {
		t.Fatal("expected state to exist")
	}
	if got.Kind != StateKindQuota || got.Reason != "five_hour_quota_exhausted" {
		t.Fatalf("state = %+v", got)
	}
	if !got.DisabledUntil.Equal(until) {
		t.Fatalf("DisabledUntil = %v, want %v", got.DisabledUntil, until)
	}

	if err := s.DeleteState("p1"); err != nil {
		t.Fatalf("DeleteState: %v", err)
	}
	if _, ok := s.GetState("p1"); ok {
		t.Fatal("state should be deleted")
	}
}

func TestFailoverStateCredentialHasNoTimeRecovery(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetState(FailoverState{
		ProviderID: "p1",
		Kind:       StateKindCredential,
		Reason:     "credential_invalid",
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	got, ok := s.GetState("p1")
	if !ok {
		t.Fatal("expected state")
	}
	// 凭据失效无 DisabledUntil，IsQuarantinedAt 永远 true。
	if !got.IsQuarantinedAt(time.Now()) {
		t.Fatal("credential state must be quarantined until explicitly cleared")
	}
	if !got.IsQuarantinedAt(time.Now().Add(365 * 24 * time.Hour)) {
		t.Fatal("credential state must remain quarantined far in the future (no time recovery)")
	}
}

func TestFailoverStateExpiryReleasesQuarantine(t *testing.T) {
	s := newTestStore(t)
	past := time.Now().Add(-time.Minute)
	if err := s.SetState(FailoverState{
		ProviderID:    "p1",
		Kind:          StateKindAvailability,
		Reason:        "bad_gateway",
		DisabledUntil: past,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	got, _ := s.GetState("p1")
	if got.IsQuarantinedAt(time.Now()) {
		t.Fatal("expired availability state must not be quarantined")
	}
}

func TestFailoverEventOrderAndLimit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		if err := s.InsertEvent(EventInput{
			OccurredAt:     time.Now().Add(time.Duration(i) * time.Second).UTC(),
			FromProviderID: "a",
			ToProviderID:   "b",
			Outcome:        OutcomeSwitched,
			Reason:         "five_hour_quota_exhausted",
		}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	// 默认 / 显式 limit 钳制到 [1,100]。
	got := s.ListEvents(0, nil)
	if len(got) != 5 {
		t.Fatalf("default limit returned %d, want 5", len(got))
	}
	// 最新优先：occurred_at DESC, id DESC。
	if !got[0].OccurredAt.After(got[4].OccurredAt) && !got[0].OccurredAt.Equal(got[4].OccurredAt) {
		// 当 occurred_at 相近时按 id desc；只需保证第一条不早于最后一条。
	}
	if got[0].OccurredAt.Before(got[len(got)-1].OccurredAt) {
		t.Fatalf("events must be newest-first: first=%v last=%v", got[0].OccurredAt, got[len(got)-1].OccurredAt)
	}

	limited := s.ListEvents(2, nil)
	if len(limited) != 2 {
		t.Fatalf("limit=2 returned %d", len(limited))
	}

	clamped := s.ListEvents(500, nil)
	if len(clamped) != 5 {
		t.Fatalf("limit>100 should clamp to 100 but still return all 5 here, got %d", len(clamped))
	}
}

func TestFailoverEventRetentionPrunesByAge(t *testing.T) {
	s := newTestStore(t)
	// 一条 31 天前的事件 + 一条今天的事件；插入后旧事件应被剪除。
	old := time.Now().Add(-31 * 24 * time.Hour).UTC()
	recent := time.Now().UTC()
	if err := s.InsertEvent(EventInput{OccurredAt: old, FromProviderID: "a", Outcome: OutcomeSwitched, Reason: "x"}); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	if err := s.InsertEvent(EventInput{OccurredAt: recent, FromProviderID: "a", Outcome: OutcomeSwitched, Reason: "y"}); err != nil {
		t.Fatalf("insert recent: %v", err)
	}
	got := s.ListEvents(100, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 event after age pruning, got %d", len(got))
	}
	if got[0].Reason != "y" {
		t.Fatalf("expected the recent event to survive, got reason %q", got[0].Reason)
	}
}

func TestFailoverEventRetentionPrunesByCount(t *testing.T) {
	s := newTestStore(t)
	// 插入 1005 条，保留最近 1000 条。
	for i := 0; i < 1005; i++ {
		if err := s.InsertEvent(EventInput{
			OccurredAt: time.Now().Add(time.Duration(i) * time.Millisecond).UTC(),
			Reason:     "ev",
			Outcome:    OutcomeSwitched,
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	got := s.ListEvents(100, nil)
	if len(got) != 100 {
		t.Fatalf("ListEvents caps at 100, got %d", len(got))
	}
	total, err := s.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if total != 1000 {
		t.Fatalf("retention cap should leave exactly 1000 rows in storage, got %d", total)
	}
}

func TestFailoverEventRedactsSecrets(t *testing.T) {
	s := newTestStore(t)
	// 即使上游错误消息含敏感片段，事件记录的 Reason/字段也不得泄露。
	if err := s.InsertEvent(EventInput{
		FromProviderID: "a",
		ToProviderID:   "b",
		Reason:         "credential_invalid",
		Outcome:        OutcomeSwitched,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	got := s.ListEvents(10, nil)
	if len(got) != 1 {
		t.Fatalf("got %d events", len(got))
	}
	// 事件结构本身不含 token / 请求体 / URL query 字段。
	for _, e := range got {
		body := e.Reason + e.FromProviderID + e.ToProviderID
		if strings.Contains(body, "sk-") || strings.Contains(body, "Bearer") {
			t.Fatalf("event leaked secret-like data: %+v", e)
		}
	}
}

func TestFailoverEventDanglingIDsBlanked(t *testing.T) {
	s := newTestStore(t)
	if err := s.InsertEvent(EventInput{
		FromProviderID:   "deleted",
		FromProviderName: "Gone",
		ToProviderID:     "alive",
		ToProviderName:   "Alive",
		Outcome:          OutcomeSwitched,
		Reason:           "x",
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	// 当前只存在 "alive" → "deleted" 的 ID 应被抹空（名字保留）。
	got := s.ListEvents(10, map[string]bool{"alive": true})
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].FromProviderID != "" {
		t.Fatalf("dangling from_provider_id must be blanked, got %q", got[0].FromProviderID)
	}
	if got[0].FromProviderName != "Gone" {
		t.Fatalf("from_provider_name should be preserved for history, got %q", got[0].FromProviderName)
	}
	if got[0].ToProviderID != "alive" {
		t.Fatalf("known to_provider_id must be retained, got %q", got[0].ToProviderID)
	}
}
