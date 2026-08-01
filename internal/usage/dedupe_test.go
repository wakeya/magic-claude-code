package usage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const testDedupeBackfillMarker = "usage_dedupe_candidates_backfill_v1"

func TestDedupeMigrationCreatesSchema(t *testing.T) {
	store := newLegacyUsageStore(t)

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if !sqliteTableExists(t, store.db, "usage_dedupe_candidates") {
		t.Fatal("usage_dedupe_candidates table is missing")
	}
	for _, index := range []string{"idx_usage_dedupe_provider", "idx_usage_requests_started_id"} {
		if !sqliteIndexExists(t, store.db, index) {
			t.Fatalf("expected index %s to exist", index)
		}
	}

	assertDedupeCandidatePrimaryKey(t, store.db)
	assertDedupeCandidateForeignKeys(t, store.db)
	assertDedupeBackfillMarker(t, store.db)
}

func TestDedupeBackfillPreservesAllCandidates(t *testing.T) {
	store := newLegacyUsageStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{
		InputTokens:              100,
		OutputTokens:             20,
		CacheCreationInputTokens: 30,
		CacheReadInputTokens:     400,
	}

	recordDedupeHistory(t, store,
		dedupeSessionRequest("session", started, "session-mapped", "session-original"),
		dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("mapped-before", started.Add(-10*time.Minute), "session-mapped", "provider-original"),
		dedupeToken("mapped-before", UsageSourceProvider, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider-original-after", started.Add(10*time.Minute), "other-mapped", "session-mapped"),
		dedupeToken("provider-original-after", UsageSourceProvider, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("session-original", started, "session-original", "session-original"),
		dedupeToken("session-original", UsageSourceProvider, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("outside-window", started.Add(10*time.Minute+time.Nanosecond), "session-mapped", ""),
		dedupeToken("outside-window", UsageSourceProvider, ParseStatusOK, values),
	)
	differentTokens := values
	differentTokens.CacheReadInputTokens++
	recordDedupeHistory(t, store,
		dedupeProviderRequest("different-tokens", started, "session-mapped", ""),
		dedupeToken("different-tokens", UsageSourceProvider, ParseStatusOK, differentTokens),
	)

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	got := dedupeCandidates(t, store.db, "session")
	want := map[string]int{
		"mapped-before":           0,
		"provider-original-after": 0,
		"session-original":        1,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestDedupeBackfillNoMatchBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		changeSession  func(*RequestRecord, *TokenRecord)
		changeProvider func(*RequestRecord, *TokenRecord)
	}{
		{
			name: "empty models",
			changeSession: func(req *RequestRecord, _ *TokenRecord) {
				req.MappedModel = ""
				req.OriginalModel = ""
			},
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.MappedModel = ""
				req.OriginalModel = ""
			},
		},
		{
			name: "different model",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.MappedModel = "different"
				req.OriginalModel = "different"
			},
		},
		{
			name: "outside time window",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.StartedAt = req.StartedAt.Add(10*time.Minute + time.Nanosecond)
			},
		},
		{
			name: "non claude provider",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.SourceApp = "other"
			},
		},
		{
			name: "provider parse failed",
			changeProvider: func(_ *RequestRecord, tok *TokenRecord) {
				tok.UsageParseStatus = ParseStatusParseError
			},
		},
		{
			name: "session parse failed",
			changeSession: func(_ *RequestRecord, tok *TokenRecord) {
				tok.UsageParseStatus = ParseStatusParseError
			},
		},
		{
			name: "provider classified as session",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.ProviderID = "_session"
			},
		},
	}

	tokenChanges := []struct {
		name   string
		change func(*TokenRecord)
	}{
		{"input tokens differ", func(tok *TokenRecord) { tok.InputTokens++ }},
		{"output tokens differ", func(tok *TokenRecord) { tok.OutputTokens++ }},
		{"cache creation tokens differ", func(tok *TokenRecord) { tok.CacheCreationInputTokens++ }},
		{"cache read tokens differ", func(tok *TokenRecord) { tok.CacheReadInputTokens++ }},
	}
	for _, tokenChange := range tokenChanges {
		change := tokenChange.change
		tests = append(tests, struct {
			name           string
			changeSession  func(*RequestRecord, *TokenRecord)
			changeProvider func(*RequestRecord, *TokenRecord)
		}{
			name: tokenChange.name,
			changeProvider: func(_ *RequestRecord, tok *TokenRecord) {
				change(tok)
			},
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newLegacyUsageStore(t)
			started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			values := UsageValues{
				InputTokens:              1,
				OutputTokens:             2,
				CacheCreationInputTokens: 3,
				CacheReadInputTokens:     4,
			}
			sessionReq := dedupeSessionRequest("session", started, "model", "model")
			sessionTok := dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values)
			providerReq := dedupeProviderRequest("provider", started, "model", "model")
			providerTok := dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values)
			if tt.changeSession != nil {
				tt.changeSession(&sessionReq, &sessionTok)
			}
			if tt.changeProvider != nil {
				tt.changeProvider(&providerReq, &providerTok)
			}
			recordDedupeHistory(t, store, sessionReq, sessionTok)
			recordDedupeHistory(t, store, providerReq, providerTok)

			if err := store.Migrate(); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 0 {
				t.Fatalf("candidate count = %d, want 0", got)
			}
		})
	}
}

func TestDedupeBackfillHandlesHistoricalTimeAndModelEdges(t *testing.T) {
	store := newLegacyUsageStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 1}

	recordDedupeHistory(t, store,
		dedupeSessionRequest("session-same-model", started, "same-model", "same-model"),
		dedupeToken("session-same-model", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider-same-time", started, "same-model", "same-model"),
		dedupeToken("provider-same-time", UsageSourceProvider, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeSessionRequest("session-invalid", started.Add(time.Hour), "invalid-model", "invalid-model"),
		dedupeToken("session-invalid", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider-invalid", started.Add(time.Hour), "invalid-model", "invalid-model"),
		dedupeToken("provider-invalid", UsageSourceProvider, ParseStatusOK, values),
	)
	if _, err := store.db.Exec(
		`UPDATE usage_requests SET started_at = 'invalid-history-time'
		 WHERE id IN ('session-invalid', 'provider-invalid')`,
	); err != nil {
		t.Fatalf("make historical timestamps invalid: %v", err)
	}

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if got := dedupeCandidates(t, store.db, "session-same-model"); fmt.Sprint(got) != "map[provider-same-time:0]" {
		t.Fatalf("same timestamp/model candidates = %v", got)
	}
	if got := dedupeCandidates(t, store.db, "session-invalid"); fmt.Sprint(got) != "map[provider-invalid:0]" {
		t.Fatalf("invalid timestamp candidates = %v", got)
	}
}

func TestDedupeBackfillIsIndependentOfHistoricalInsertOrder(t *testing.T) {
	for _, providerFirst := range []bool{true, false} {
		name := "session first"
		if providerFirst {
			name = "provider first"
		}
		t.Run(name, func(t *testing.T) {
			store := newLegacyUsageStore(t)
			started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			values := UsageValues{InputTokens: 10, OutputTokens: 2}
			sessionReq := dedupeSessionRequest("session", started, "model", "model")
			sessionTok := dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values)
			providerReq := dedupeProviderRequest("provider", started.Add(time.Minute), "model", "model")
			providerTok := dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values)

			if providerFirst {
				recordDedupeHistory(t, store, providerReq, providerTok)
				recordDedupeHistory(t, store, sessionReq, sessionTok)
			} else {
				recordDedupeHistory(t, store, sessionReq, sessionTok)
				recordDedupeHistory(t, store, providerReq, providerTok)
			}

			if err := store.Migrate(); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			if got := dedupeCandidates(t, store.db, "session"); fmt.Sprint(got) != "map[provider:0]" {
				t.Fatalf("candidates = %v", got)
			}
		})
	}
}

func TestDedupeMigrationMarkerPreventsRepeatBackfill(t *testing.T) {
	store := newLegacyUsageStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 10}
	recordDedupeHistory(t, store,
		dedupeSessionRequest("session", started, "model", "model"),
		dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider", started, "model", "model"),
		dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
	)

	if err := store.Migrate(); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 1 {
		t.Fatalf("candidate count after repeated migration = %d, want 1", got)
	}

	if _, err := store.db.Exec(`DELETE FROM usage_dedupe_candidates`); err != nil {
		t.Fatalf("delete candidate: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() with completed marker error = %v", err)
	}
	if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 0 {
		t.Fatalf("completed migration unexpectedly reran backfill: count = %d", got)
	}
	assertDedupeBackfillMarker(t, store.db)
}

func TestDedupeMigrationRollsBackBackfillAndMarkerTogether(t *testing.T) {
	store := newLegacyUsageStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 10}
	recordDedupeHistory(t, store,
		dedupeSessionRequest("session", started, "model", "model"),
		dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider", started, "model", "model"),
		dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
	)
	if _, err := store.db.Exec(fmt.Sprintf(`
		CREATE TRIGGER fail_dedupe_marker
		BEFORE INSERT ON settings
		WHEN NEW.key = %q
		BEGIN
			SELECT RAISE(ABORT, 'forced dedupe marker failure');
		END;
	`, testDedupeBackfillMarker)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	err := store.Migrate()
	if err == nil || !strings.Contains(err.Error(), "forced dedupe marker failure") {
		t.Fatalf("Migrate() error = %v, want forced marker failure", err)
	}
	if sqliteTableExists(t, store.db, "usage_dedupe_candidates") {
		t.Fatal("candidate schema was committed despite migration failure")
	}
	if sqliteIndexExists(t, store.db, "idx_usage_requests_started_id") {
		t.Fatal("started/id index was committed despite migration failure")
	}
	if got := sqliteCount(t, store.db, "usage_requests"); got != 2 {
		t.Fatalf("usage request count after rollback = %d, want 2", got)
	}
	assertNoDedupeBackfillMarker(t, store.db)

	if _, err := store.db.Exec(`DROP TRIGGER fail_dedupe_marker`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("retry Migrate() error = %v", err)
	}
	if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 1 {
		t.Fatalf("candidate count after retry = %d, want 1", got)
	}
	assertDedupeBackfillMarker(t, store.db)
}

func TestDedupeIncrementalMaintainsAllCandidatesInEitherOrder(t *testing.T) {
	for _, providerFirst := range []bool{true, false} {
		name := "session first"
		if providerFirst {
			name = "provider first"
		}
		t.Run(name, func(t *testing.T) {
			store := newTestStore(t)
			started := time.Date(2026, 7, 30, 12, 0, 0, 500_000_000, time.UTC)
			values := UsageValues{
				InputTokens:              100,
				OutputTokens:             20,
				CacheCreationInputTokens: 30,
				CacheReadInputTokens:     400,
			}
			sessionReq := dedupeSessionRequest("session", started, "mapped-model", "original-model")
			sessionTok := dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values)
			providers := []struct {
				req RequestRecord
				tok TokenRecord
			}{
				{
					req: dedupeProviderRequest("mapped-direct", started.Add(2*time.Minute), "mapped-model", "provider-original"),
					tok: dedupeToken("mapped-direct", UsageSourceProvider, ParseStatusOK, values),
				},
				{
					req: dedupeProviderRequest("mapped-via-original", started.Add(-2*time.Minute), "other-model", "mapped-model"),
					tok: dedupeToken("mapped-via-original", UsageSourceProvider, ParseStatusOK, values),
				},
				{
					req: dedupeProviderRequest("original-only", started.Add(-5*time.Minute), "original-model", "original-model"),
					tok: dedupeToken("original-only", UsageSourceProvider, ParseStatusOK, values),
				},
				{
					req: dedupeProviderRequest("boundary-before", started.Add(-10*time.Minute), "mapped-model", ""),
					tok: dedupeToken("boundary-before", UsageSourceProvider, ParseStatusOK, values),
				},
				{
					req: dedupeProviderRequest("boundary-after", started.Add(10*time.Minute), "mapped-model", ""),
					tok: dedupeToken("boundary-after", UsageSourceProvider, ParseStatusOK, values),
				},
			}

			writeSession := func() {
				t.Helper()
				if err := store.Record(sessionReq, sessionTok); err != nil {
					t.Fatalf("Record(session) error = %v", err)
				}
			}
			writeProviders := func() {
				t.Helper()
				for _, provider := range providers {
					if err := store.Record(provider.req, provider.tok); err != nil {
						t.Fatalf("Record(%q) error = %v", provider.req.ID, err)
					}
				}
			}
			if providerFirst {
				writeProviders()
				writeSession()
			} else {
				writeSession()
				writeProviders()
			}

			got := dedupeCandidates(t, store.db, "session")
			want := map[string]int{
				"boundary-after":      0,
				"boundary-before":     0,
				"mapped-direct":       0,
				"mapped-via-original": 0,
				"original-only":       1,
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("candidates = %v, want %v", got, want)
			}

			page, err := store.Requests(Filter{StatsScope: StatsScopeRaw, Page: 1, PageSize: 10})
			if err != nil {
				t.Fatalf("Requests() error = %v", err)
			}
			for _, row := range page.Rows {
				if row.ID == "session" {
					if row.DedupeStatus != DedupeStatusDuplicate || row.DedupeRequestID != "boundary-before" {
						t.Fatalf("session dedupe fields = %q/%q", row.DedupeStatus, row.DedupeRequestID)
					}
					return
				}
			}
			t.Fatal("session row is missing from raw requests")
		})
	}
}

// TestDedupeIncrementalPairsHistoricalOffsetStartedAtText 覆盖 M-1（gpt-5.6 审查）：历史库
// 的 started_at 可能保留带非 UTC 偏移的 RFC3339 文本（如 2026-07-30T12:00:00-07:00，instant
// 为 19:00Z）。迁移后经 Record 写入新对端行时，增量候选查询必须按 instant 解析并在含边界
// ±10 分钟窗口内配对，与旧 Go 去重（legacyOracleMarkDuplicates 的 time.Parse + Before/After
// 窗口判断）逐字段一致；不得因原始 TEXT 字典序与 canonical UTC 边界不在同一 instant 而漏配
// 候选。每个用例先插入历史行（原始偏移 TEXT）→ Migrate（回填无对端，不产生候选）→ Record
// 新行（canonical UTC）触发增量维护，随后断言候选表与 Requests 去重标记同 legacy oracle 差分一致。
func TestDedupeIncrementalPairsHistoricalOffsetStartedAtText(t *testing.T) {
	instant := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
	values := UsageValues{
		InputTokens:              100,
		OutputTokens:             20,
		CacheCreationInputTokens: 30,
		CacheReadInputTokens:     400,
	}

	t.Run("historical session with -07:00 offset pairs with new canonical provider", func(t *testing.T) {
		store := newLegacyUsageStore(t)
		recordDedupeHistoryWithStartedText(t, store, "2026-07-30T12:00:00-07:00",
			dedupeSessionRequest("session", instant, "mapped-model", "original-model"),
			dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
		)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		if err := store.Record(
			dedupeProviderRequest("provider", instant, "mapped-model", "original-model"),
			dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
		); err != nil {
			t.Fatalf("Record(provider) error = %v", err)
		}
		if got, want := dedupeCandidates(t, store.db, "session"), (map[string]int{"provider": 0}); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
		assertDedupeMarksMatchLegacyOracle(t, store)
	})

	t.Run("reverse order historical provider with offset pairs with new canonical session", func(t *testing.T) {
		store := newLegacyUsageStore(t)
		recordDedupeHistoryWithStartedText(t, store, "2026-07-30T12:00:00-07:00",
			dedupeProviderRequest("provider", instant, "mapped-model", "original-model"),
			dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
		)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		if err := store.Record(
			dedupeSessionRequest("session", instant, "mapped-model", "original-model"),
			dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
		); err != nil {
			t.Fatalf("Record(session) error = %v", err)
		}
		if got, want := dedupeCandidates(t, store.db, "session"), (map[string]int{"provider": 0}); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
		assertDedupeMarksMatchLegacyOracle(t, store)
	})

	t.Run("fractional seconds with offset stay inside window", func(t *testing.T) {
		store := newLegacyUsageStore(t)
		// instant 2026-07-30T18:55:00.123456789Z：距新行 19:00:00Z 约 4 分 60 秒内，窗口内。
		recordDedupeHistoryWithStartedText(t, store, "2026-07-30T11:55:00.123456789-07:00",
			dedupeSessionRequest("session", instant, "mapped-model", "original-model"),
			dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
		)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		if err := store.Record(
			dedupeProviderRequest("provider", instant, "mapped-model", "original-model"),
			dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
		); err != nil {
			t.Fatalf("Record(provider) error = %v", err)
		}
		if got, want := dedupeCandidates(t, store.db, "session"), (map[string]int{"provider": 0}); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
		assertDedupeMarksMatchLegacyOracle(t, store)
	})

	t.Run("offset text at inclusive window boundary pairs", func(t *testing.T) {
		store := newLegacyUsageStore(t)
		// instant 2026-07-30T19:10:00Z = 新行 19:00:00Z + 10 分钟整，含边界窗口应配对。
		recordDedupeHistoryWithStartedText(t, store, "2026-07-30T12:10:00-07:00",
			dedupeSessionRequest("session", instant, "mapped-model", "original-model"),
			dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
		)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		if err := store.Record(
			dedupeProviderRequest("provider", instant, "mapped-model", "original-model"),
			dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
		); err != nil {
			t.Fatalf("Record(provider) error = %v", err)
		}
		if got, want := dedupeCandidates(t, store.db, "session"), (map[string]int{"provider": 0}); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
		assertDedupeMarksMatchLegacyOracle(t, store)
	})

	t.Run("offset text just outside window does not pair", func(t *testing.T) {
		store := newLegacyUsageStore(t)
		// instant 2026-07-30T19:10:00.000000001Z = 新行 + 10 分钟 + 1ns，窗口外不得配对，
		// 防止修复把候选范围放宽到超过旧 Go 窗口语义。
		recordDedupeHistoryWithStartedText(t, store, "2026-07-30T12:10:00.000000001-07:00",
			dedupeSessionRequest("session", instant, "mapped-model", "original-model"),
			dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
		)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		if err := store.Record(
			dedupeProviderRequest("provider", instant, "mapped-model", "original-model"),
			dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
		); err != nil {
			t.Fatalf("Record(provider) error = %v", err)
		}
		if got := dedupeCandidates(t, store.db, "session"); len(got) != 0 {
			t.Fatalf("candidates = %v, want empty", got)
		}
		assertDedupeMarksMatchLegacyOracle(t, store)
	})

	t.Run("DST transition offsets pair by instant not wall text", func(t *testing.T) {
		store := newLegacyUsageStore(t)
		// 美洲洛杉矶 DST 切换日：-07:00（PDT）与 -08:00（PST）两种偏移并存。
		// session-pdt instant 2026-11-01T08:30:00Z，距 provider 08:35:00Z 5 分钟，窗口内。
		// session-pst instant 2026-11-01T09:40:00Z，距 provider 65 分钟，窗口外。
		providerAt := time.Date(2026, 11, 1, 8, 35, 0, 0, time.UTC)
		recordDedupeHistoryWithStartedText(t, store, "2026-11-01T01:30:00-07:00",
			dedupeSessionRequest("session-pdt", providerAt, "mapped-model", "original-model"),
			dedupeToken("session-pdt", UsageSourceSessionLog, ParseStatusOK, values),
		)
		recordDedupeHistoryWithStartedText(t, store, "2026-11-01T01:40:00-08:00",
			dedupeSessionRequest("session-pst", providerAt, "mapped-model", "original-model"),
			dedupeToken("session-pst", UsageSourceSessionLog, ParseStatusOK, values),
		)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		if err := store.Record(
			dedupeProviderRequest("provider", providerAt, "mapped-model", "original-model"),
			dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
		); err != nil {
			t.Fatalf("Record(provider) error = %v", err)
		}
		if got, want := dedupeCandidates(t, store.db, "session-pdt"), (map[string]int{"provider": 0}); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("session-pdt candidates = %v, want %v", got, want)
		}
		if got := dedupeCandidates(t, store.db, "session-pst"); len(got) != 0 {
			t.Fatalf("session-pst candidates = %v, want empty", got)
		}
		assertDedupeMarksMatchLegacyOracle(t, store)
	})

	t.Run("same instant under multiple offset spellings all pair", func(t *testing.T) {
		store := newLegacyUsageStore(t)
		// 同一 instant（19:00Z）的三种历史文本表示：canonical Z、-07:00、+08:00（跨日）。
		recordDedupeHistoryWithStartedText(t, store, "2026-07-30T19:00:00Z",
			dedupeSessionRequest("session-z", instant, "mapped-model", "original-model"),
			dedupeToken("session-z", UsageSourceSessionLog, ParseStatusOK, values),
		)
		recordDedupeHistoryWithStartedText(t, store, "2026-07-30T12:00:00-07:00",
			dedupeSessionRequest("session-minus7", instant, "mapped-model", "original-model"),
			dedupeToken("session-minus7", UsageSourceSessionLog, ParseStatusOK, values),
		)
		recordDedupeHistoryWithStartedText(t, store, "2026-07-31T03:00:00+08:00",
			dedupeSessionRequest("session-plus8", instant, "mapped-model", "original-model"),
			dedupeToken("session-plus8", UsageSourceSessionLog, ParseStatusOK, values),
		)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		providerAt := instant.Add(5 * time.Second)
		if err := store.Record(
			dedupeProviderRequest("provider", providerAt, "mapped-model", "original-model"),
			dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
		); err != nil {
			t.Fatalf("Record(provider) error = %v", err)
		}
		for _, sessionID := range []string{"session-z", "session-minus7", "session-plus8"} {
			if got, want := dedupeCandidates(t, store.db, sessionID), (map[string]int{"provider": 0}); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("%s candidates = %v, want %v", sessionID, got, want)
			}
		}
		assertDedupeMarksMatchLegacyOracle(t, store)
	})
}

// assertDedupeMarksMatchLegacyOracle 对全量行逐字段比较 SQL 读取路径的 dedupe 标记与
// legacy oracle（旧 Go “先筛选、再去重、后口径”算法）的判定：DedupeStatus 与
// DedupeRequestID 必须一致。用于锁定增量候选修复与旧去重的完全兼容性。
func assertDedupeMarksMatchLegacyOracle(t *testing.T, store *Store) {
	t.Helper()
	wantRows := legacyOracleQueryRows(t, store.db, Filter{StatsScope: StatsScopeRaw})
	page, err := store.Requests(Filter{StatsScope: StatsScopeRaw, Page: 1, PageSize: 1000})
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	gotByID := make(map[string]RequestRow, len(page.Rows))
	for _, row := range page.Rows {
		gotByID[row.ID] = row
	}
	if len(gotByID) != len(wantRows) {
		t.Fatalf("Requests() returned %d rows, want %d", len(gotByID), len(wantRows))
	}
	for _, want := range wantRows {
		got, ok := gotByID[want.ID]
		if !ok {
			t.Fatalf("Requests() missing row %q", want.ID)
		}
		if got.DedupeStatus != want.DedupeStatus || got.DedupeRequestID != want.DedupeRequestID {
			t.Fatalf("row %q dedupe mark = %q/%q, want %q/%q (legacy oracle)",
				want.ID, got.DedupeStatus, got.DedupeRequestID, want.DedupeStatus, want.DedupeRequestID)
		}
	}
}

func TestDedupeIncrementalIgnoresIncompatibleRows(t *testing.T) {
	tests := []struct {
		name           string
		changeSession  func(*RequestRecord, *TokenRecord)
		changeProvider func(*RequestRecord, *TokenRecord)
	}{
		{
			name: "provider usage missing",
			changeProvider: func(_ *RequestRecord, tok *TokenRecord) {
				tok.UsageSource = UsageSourceNone
				tok.UsageParseStatus = ParseStatusMissing
			},
		},
		{
			name: "session usage missing",
			changeSession: func(_ *RequestRecord, tok *TokenRecord) {
				tok.UsageSource = UsageSourceNone
				tok.UsageParseStatus = ParseStatusMissing
			},
		},
		{
			name: "non claude provider",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.SourceApp = "other"
			},
		},
		{
			name: "provider parse failed",
			changeProvider: func(_ *RequestRecord, tok *TokenRecord) {
				tok.UsageParseStatus = ParseStatusParseError
			},
		},
		{
			name: "session parse failed",
			changeSession: func(_ *RequestRecord, tok *TokenRecord) {
				tok.UsageParseStatus = ParseStatusParseError
			},
		},
		{
			name: "provider outside time window",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.StartedAt = req.StartedAt.Add(10*time.Minute + time.Nanosecond)
			},
		},
		{
			name: "models differ",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.MappedModel = "different"
				req.OriginalModel = "different"
			},
		},
	}
	tokenChanges := []struct {
		name   string
		change func(*TokenRecord)
	}{
		{"input tokens differ", func(tok *TokenRecord) { tok.InputTokens++ }},
		{"output tokens differ", func(tok *TokenRecord) { tok.OutputTokens++ }},
		{"cache creation tokens differ", func(tok *TokenRecord) { tok.CacheCreationInputTokens++ }},
		{"cache read tokens differ", func(tok *TokenRecord) { tok.CacheReadInputTokens++ }},
	}
	for _, tokenChange := range tokenChanges {
		change := tokenChange.change
		tests = append(tests, struct {
			name           string
			changeSession  func(*RequestRecord, *TokenRecord)
			changeProvider func(*RequestRecord, *TokenRecord)
		}{
			name: tokenChange.name,
			changeProvider: func(_ *RequestRecord, tok *TokenRecord) {
				change(tok)
			},
		})
	}

	for _, providerFirst := range []bool{true, false} {
		for _, tt := range tests {
			name := "session first/" + tt.name
			if providerFirst {
				name = "provider first/" + tt.name
			}
			t.Run(name, func(t *testing.T) {
				store := newTestStore(t)
				started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
				values := UsageValues{
					InputTokens:              1,
					OutputTokens:             2,
					CacheCreationInputTokens: 3,
					CacheReadInputTokens:     4,
				}
				sessionReq := dedupeSessionRequest("session", started, "model", "model")
				sessionTok := dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values)
				providerReq := dedupeProviderRequest("provider", started, "model", "model")
				providerTok := dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values)
				if tt.changeSession != nil {
					tt.changeSession(&sessionReq, &sessionTok)
				}
				if tt.changeProvider != nil {
					tt.changeProvider(&providerReq, &providerTok)
				}

				if providerFirst {
					if err := store.Record(providerReq, providerTok); err != nil {
						t.Fatalf("Record(provider) error = %v", err)
					}
					if err := store.Record(sessionReq, sessionTok); err != nil {
						t.Fatalf("Record(session) error = %v", err)
					}
				} else {
					if err := store.Record(sessionReq, sessionTok); err != nil {
						t.Fatalf("Record(session) error = %v", err)
					}
					if err := store.Record(providerReq, providerTok); err != nil {
						t.Fatalf("Record(provider) error = %v", err)
					}
				}
				if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 0 {
					t.Fatalf("candidate count = %d, want 0", got)
				}
			})
		}
	}
}

func TestDedupeIncrementalRecordIfAbsentIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 10, OutputTokens: 2}
	providerReq := dedupeProviderRequest("provider", started, "model", "model")
	providerTok := dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values)
	sessionReq := dedupeSessionRequest("session", started, "model", "model")
	sessionTok := dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values)

	if err := store.Record(providerReq, providerTok); err != nil {
		t.Fatalf("Record(provider) error = %v", err)
	}
	inserted, err := store.recordIfAbsent(sessionReq, sessionTok)
	if err != nil || !inserted {
		t.Fatalf("first recordIfAbsent() = %v, %v, want true, nil", inserted, err)
	}
	inserted, err = store.recordIfAbsent(sessionReq, sessionTok)
	if err != nil || inserted {
		t.Fatalf("second recordIfAbsent() = %v, %v, want false, nil", inserted, err)
	}
	if got := dedupeCandidates(t, store.db, "session"); fmt.Sprint(got) != "map[provider:0]" {
		t.Fatalf("candidates after repeated recordIfAbsent = %v", got)
	}

	if err := store.Record(sessionReq, sessionTok); err == nil {
		t.Fatal("Record() conflict error = nil")
	}
	if got := dedupeCandidates(t, store.db, "session"); fmt.Sprint(got) != "map[provider:0]" {
		t.Fatalf("candidates after Record conflict = %v", got)
	}
}

func TestDedupeIncrementalRollsBackUsageWhenCandidateWriteFails(t *testing.T) {
	tests := []struct {
		name   string
		insert func(*Store, RequestRecord, TokenRecord) error
	}{
		{
			name: "Record",
			insert: func(store *Store, req RequestRecord, tok TokenRecord) error {
				return store.Record(req, tok)
			},
		},
		{
			name: "recordIfAbsent",
			insert: func(store *Store, req RequestRecord, tok TokenRecord) error {
				inserted, err := store.recordIfAbsent(req, tok)
				if err == nil && !inserted {
					return fmt.Errorf("recordIfAbsent did not insert")
				}
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			values := UsageValues{InputTokens: 10, OutputTokens: 2}
			providerReq := dedupeProviderRequest("provider", started, "model", "model")
			providerTok := dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values)
			if err := store.Record(providerReq, providerTok); err != nil {
				t.Fatalf("Record(provider) error = %v", err)
			}
			if _, err := store.db.Exec(`
				CREATE TRIGGER fail_incremental_candidate
				BEFORE INSERT ON usage_dedupe_candidates
				WHEN NEW.session_request_id = 'session-fail'
				BEGIN
					SELECT RAISE(ABORT, 'forced incremental candidate failure');
				END;
			`); err != nil {
				t.Fatalf("create candidate failure trigger: %v", err)
			}

			sessionReq := dedupeSessionRequest("session-fail", started, "model", "model")
			sessionTok := dedupeToken("session-fail", UsageSourceSessionLog, ParseStatusOK, values)
			err := tt.insert(store, sessionReq, sessionTok)
			if err == nil || !strings.Contains(err.Error(), "forced incremental candidate failure") {
				t.Fatalf("insert error = %v, want forced candidate failure", err)
			}
			assertUsageRecordCounts(t, store.db, "session-fail", 0, 0)
			if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 0 {
				t.Fatalf("candidate count after rollback = %d, want 0", got)
			}
		})
	}
}

func TestDedupeIncrementalConcurrentWALWrites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wal-usage.db")
	dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { db.Close() })
	store := NewStore(db)
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 10, OutputTokens: 2}
	if err := store.Record(
		dedupeSessionRequest("session", started, "model", "model"),
		dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
	); err != nil {
		t.Fatalf("Record(session) error = %v", err)
	}

	const writers = 8
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("provider-%02d", i)
			errs <- store.Record(
				dedupeProviderRequest(id, started.Add(time.Duration(i)*time.Second), "model", "model"),
				dedupeToken(id, UsageSourceProvider, ParseStatusOK, values),
			)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Record() error = %v", err)
		}
	}
	if got := len(dedupeCandidates(t, store.db, "session")); got != writers {
		t.Fatalf("candidate count = %d, want %d", got, writers)
	}
}

func TestDedupeIncrementalCandidateQueryUsesStartedAtIndex(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 500_000_000, time.UTC)
	values := UsageValues{InputTokens: 10, OutputTokens: 2}
	current := RequestRow{
		RequestRecord: dedupeSessionRequest("session", started, "model", "model"),
		TokenRecord:   dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
	}
	for _, wide := range []bool{false, true} {
		assertIncrementalCandidatePlanUsesIndex(t, store, current, wide)
	}
}

func assertIncrementalCandidatePlanUsesIndex(t *testing.T, store *Store, current RequestRow, wide bool) {
	t.Helper()
	query, args := incrementalDedupeCandidateQuery(current, incrementalDedupeProviderWhere, wide)
	rows, err := store.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain incremental candidate query: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan incremental candidate query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("incremental candidate query plan rows: %v", err)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "SEARCH r USING INDEX idx_usage_requests_started_id") ||
		!strings.Contains(plan, "(started_at>? AND started_at<?)") {
		t.Fatalf("query plan does not use started_at index:\n%s", plan)
	}
}

func assertUsageRecordCounts(t *testing.T, db *sql.DB, id string, wantRequests, wantTokens int) {
	t.Helper()
	var requests, tokens int
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_requests WHERE id = ?`, id).Scan(&requests); err != nil {
		t.Fatalf("count usage request %q: %v", id, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_tokens WHERE request_id = ?`, id).Scan(&tokens); err != nil {
		t.Fatalf("count usage token %q: %v", id, err)
	}
	if requests != wantRequests || tokens != wantTokens {
		t.Fatalf("usage record counts for %q = requests:%d tokens:%d, want requests:%d tokens:%d",
			id, requests, tokens, wantRequests, wantTokens)
	}
}

func newLegacyUsageStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy-usage.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE usage_requests (
			id TEXT PRIMARY KEY,
			started_at TEXT NOT NULL,
			ended_at TEXT,
			duration_ms INTEGER,
			upstream_response_header_ms INTEGER,
			time_to_first_byte_ms INTEGER,
			status_code INTEGER,
			error_type TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			request_path TEXT NOT NULL DEFAULT '',
			backend_url TEXT NOT NULL DEFAULT '',
			provider_id TEXT NOT NULL DEFAULT '',
			provider_name TEXT NOT NULL DEFAULT '',
			provider_api_url TEXT NOT NULL DEFAULT '',
			source_app TEXT NOT NULL DEFAULT 'unknown',
			source_entrypoint TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			original_model TEXT NOT NULL DEFAULT '',
			mapped_model TEXT NOT NULL DEFAULT '',
			stream INTEGER NOT NULL DEFAULT 0,
			request_bytes INTEGER NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE usage_tokens (
			request_id TEXT PRIMARY KEY,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
			usage_source TEXT NOT NULL DEFAULT 'none',
			usage_parse_status TEXT NOT NULL DEFAULT 'missing',
			usage_parse_error TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
		);
	`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	return NewStore(db)
}

func dedupeSessionRequest(id string, started time.Time, mappedModel, originalModel string) RequestRecord {
	req := testUsageRequest(id, started)
	req.Method = "SESSION"
	req.RequestPath = "session_log"
	req.ProviderID = "_session"
	req.ProviderName = "Session Log"
	req.ProviderAPIURL = ""
	req.SourceEntrypoint = "session_log"
	req.MappedModel = mappedModel
	req.OriginalModel = originalModel
	return req
}

func dedupeProviderRequest(id string, started time.Time, mappedModel, originalModel string) RequestRecord {
	req := testUsageRequest(id, started)
	req.MappedModel = mappedModel
	req.OriginalModel = originalModel
	return req
}

func dedupeToken(id, source, status string, values UsageValues) TokenRecord {
	return TokenRecord{
		RequestID:                id,
		InputTokens:              values.InputTokens,
		OutputTokens:             values.OutputTokens,
		CacheCreationInputTokens: values.CacheCreationInputTokens,
		CacheReadInputTokens:     values.CacheReadInputTokens,
		UsageSource:              source,
		UsageParseStatus:         status,
	}
}

func recordDedupeHistory(t *testing.T, store *Store, req RequestRecord, tok TokenRecord) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin legacy history insert: %v", err)
	}
	defer tx.Rollback()
	if tok.RequestID == "" {
		tok.RequestID = req.ID
	}
	if _, err := tx.Exec(
		`INSERT INTO usage_requests(
			id, started_at, ended_at, duration_ms, upstream_response_header_ms, time_to_first_byte_ms,
			status_code, error_type, error_message, method, request_path, backend_url,
			provider_id, provider_name, provider_api_url, source_app, source_entrypoint, user_agent,
			original_model, mapped_model, stream, request_bytes, response_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID,
		formatTime(req.StartedAt),
		formatOptionalTime(req.EndedAt),
		req.DurationMS,
		req.UpstreamResponseHeaderMS,
		req.TimeToFirstByteMS,
		req.StatusCode,
		req.ErrorType,
		req.ErrorMessage,
		req.Method,
		req.RequestPath,
		req.BackendURL,
		req.ProviderID,
		req.ProviderName,
		req.ProviderAPIURL,
		defaultString(req.SourceApp, "unknown"),
		req.SourceEntrypoint,
		req.UserAgent,
		req.OriginalModel,
		req.MappedModel,
		boolToInt(req.Stream),
		req.RequestBytes,
		req.ResponseBytes,
	); err != nil {
		t.Fatalf("insert legacy usage request %q: %v", req.ID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO usage_tokens(
			request_id, input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
			usage_source, usage_parse_status, usage_parse_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tok.RequestID,
		tok.InputTokens,
		tok.OutputTokens,
		tok.CacheCreationInputTokens,
		tok.CacheReadInputTokens,
		defaultString(tok.UsageSource, UsageSourceNone),
		defaultString(tok.UsageParseStatus, ParseStatusMissing),
		tok.UsageParseError,
	); err != nil {
		t.Fatalf("insert legacy usage token %q: %v", tok.RequestID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit legacy history insert %q: %v", req.ID, err)
	}
}

// recordDedupeHistoryWithStartedText 与 recordDedupeHistory 相同，但按原样写入调用方
// 给定的 started_at TEXT（不做 canonical UTC 规范化），用于构造保留历史偏移文本的脏库行。
func recordDedupeHistoryWithStartedText(t *testing.T, store *Store, startedText string, req RequestRecord, tok TokenRecord) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin legacy history insert: %v", err)
	}
	defer tx.Rollback()
	if tok.RequestID == "" {
		tok.RequestID = req.ID
	}
	if _, err := tx.Exec(
		`INSERT INTO usage_requests(
			id, started_at, ended_at, duration_ms, upstream_response_header_ms, time_to_first_byte_ms,
			status_code, error_type, error_message, method, request_path, backend_url,
			provider_id, provider_name, provider_api_url, source_app, source_entrypoint, user_agent,
			original_model, mapped_model, stream, request_bytes, response_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID,
		startedText,
		formatOptionalTime(req.EndedAt),
		req.DurationMS,
		req.UpstreamResponseHeaderMS,
		req.TimeToFirstByteMS,
		req.StatusCode,
		req.ErrorType,
		req.ErrorMessage,
		req.Method,
		req.RequestPath,
		req.BackendURL,
		req.ProviderID,
		req.ProviderName,
		req.ProviderAPIURL,
		defaultString(req.SourceApp, "unknown"),
		req.SourceEntrypoint,
		req.UserAgent,
		req.OriginalModel,
		req.MappedModel,
		boolToInt(req.Stream),
		req.RequestBytes,
		req.ResponseBytes,
	); err != nil {
		t.Fatalf("insert legacy usage request %q: %v", req.ID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO usage_tokens(
			request_id, input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
			usage_source, usage_parse_status, usage_parse_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tok.RequestID,
		tok.InputTokens,
		tok.OutputTokens,
		tok.CacheCreationInputTokens,
		tok.CacheReadInputTokens,
		defaultString(tok.UsageSource, UsageSourceNone),
		defaultString(tok.UsageParseStatus, ParseStatusMissing),
		tok.UsageParseError,
	); err != nil {
		t.Fatalf("insert legacy usage token %q: %v", tok.RequestID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit legacy history insert %q: %v", req.ID, err)
	}
}

func dedupeCandidates(t *testing.T, db *sql.DB, sessionID string) map[string]int {
	t.Helper()
	rows, err := db.Query(
		`SELECT provider_request_id, model_priority
		 FROM usage_dedupe_candidates
		 WHERE session_request_id = ?`,
		sessionID,
	)
	if err != nil {
		t.Fatalf("query candidates: %v", err)
	}
	defer rows.Close()
	got := make(map[string]int)
	for rows.Next() {
		var providerID string
		var priority int
		if err := rows.Scan(&providerID, &priority); err != nil {
			t.Fatalf("scan candidate: %v", err)
		}
		got[providerID] = priority
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("candidate rows: %v", err)
	}
	return got
}

func assertDedupeCandidatePrimaryKey(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(usage_dedupe_candidates)`)
	if err != nil {
		t.Fatalf("candidate table_info: %v", err)
	}
	defer rows.Close()
	got := make(map[string]int)
	for rows.Next() {
		var cid, notNull, primaryKeyOrder int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKeyOrder); err != nil {
			t.Fatalf("scan candidate table_info: %v", err)
		}
		got[name] = primaryKeyOrder
	}
	if got["session_request_id"] != 1 || got["provider_request_id"] != 2 {
		t.Fatalf("candidate primary key order = %v", got)
	}
}

func assertDedupeCandidateForeignKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_list(usage_dedupe_candidates)`)
	if err != nil {
		t.Fatalf("candidate foreign_key_list: %v", err)
	}
	defer rows.Close()
	got := make(map[string]string)
	for rows.Next() {
		var id, sequence int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan candidate foreign key: %v", err)
		}
		if table == "usage_requests" && to == "id" {
			got[from] = onDelete
		}
	}
	if got["session_request_id"] != "CASCADE" || got["provider_request_id"] != "CASCADE" {
		t.Fatalf("candidate foreign keys = %v", got)
	}
}

func assertDedupeBackfillMarker(t *testing.T, db *sql.DB) {
	t.Helper()
	var value string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, testDedupeBackfillMarker).Scan(&value); err != nil {
		t.Fatalf("query dedupe backfill marker: %v", err)
	}
	if value != "1" {
		t.Fatalf("dedupe backfill marker value = %q, want 1", value)
	}
}

func assertNoDedupeBackfillMarker(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ?`, testDedupeBackfillMarker).Scan(&count); err != nil {
		t.Fatalf("count dedupe backfill marker: %v", err)
	}
	if count != 0 {
		t.Fatalf("dedupe backfill marker count = %d, want 0", count)
	}
}

// TestDedupeOffsetStartedAtMarkerDetectsHistoricalText 验证迁移期检测并持久化
// “库中是否存在非 Z 结尾 started_at 文本”的 marker：空库/全 canonical 库检测为 '0'，
// 含历史偏移文本的库检测为 '1'；marker 一旦写入，后续 Migrate 直接读取而不重新检测
// （运行期写入恒经 formatTime 输出 Z 结尾文本，快照语义安全）。
func TestDedupeOffsetStartedAtMarkerDetectsHistoricalText(t *testing.T) {
	t.Run("empty store detects all canonical", func(t *testing.T) {
		store := newTestStore(t)
		var value string
		if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, offsetStartedAtMarkerKey).Scan(&value); err != nil {
			t.Fatalf("query marker: %v", err)
		}
		if value != "0" {
			t.Fatalf("marker = %q, want 0", value)
		}
		if got := store.hasOffsetStartedAt.Load(); got != offsetStartedAtAllCanonical {
			t.Fatalf("hasOffsetStartedAt = %d, want %d", got, offsetStartedAtAllCanonical)
		}
	})

	t.Run("historical offset text detects present", func(t *testing.T) {
		store := newLegacyUsageStore(t)
		instant := time.Date(2026, 7, 30, 19, 0, 0, 0, time.UTC)
		values := UsageValues{InputTokens: 1, OutputTokens: 2}
		recordDedupeHistoryWithStartedText(t, store, "2026-07-30T12:00:00-07:00",
			dedupeSessionRequest("session", instant, "model", "model"),
			dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
		)
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		var value string
		if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, offsetStartedAtMarkerKey).Scan(&value); err != nil {
			t.Fatalf("query marker: %v", err)
		}
		if value != "1" {
			t.Fatalf("marker = %q, want 1", value)
		}
		if got := store.hasOffsetStartedAt.Load(); got != offsetStartedAtPresent {
			t.Fatalf("hasOffsetStartedAt = %d, want %d", got, offsetStartedAtPresent)
		}
	})

	t.Run("marker is snapshot not re-detected on later migrate", func(t *testing.T) {
		store := newTestStore(t)
		// 首次 Migrate（newTestStore 内）已检测空库为 '0'。运行期外部直接插入非 Z 行
		// 不属于系统写入契约；再次 Migrate 应读快照而不重新检测。
		if _, err := store.db.Exec(
			`INSERT INTO usage_requests(id, started_at) VALUES ('external', '2026-07-30T12:00:00-07:00')`,
		); err != nil {
			t.Fatalf("insert external row: %v", err)
		}
		if err := store.Migrate(); err != nil {
			t.Fatalf("Migrate() again error = %v", err)
		}
		var value string
		if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, offsetStartedAtMarkerKey).Scan(&value); err != nil {
			t.Fatalf("query marker: %v", err)
		}
		if value != "0" {
			t.Fatalf("marker = %q, want snapshot 0", value)
		}
	})
}

// TestIncrementalDedupeSQLBoundsCoarseWidth 验证粗滤边界宽度开关：窄模式（全 canonical
// 库）粗滤收敛为 ±10 分钟±1 秒窄边界（旧实现索引扫描范围）；宽模式按合法偏移上限放宽。
// 两种模式的 epoch 决定性边界相同。
func TestIncrementalDedupeSQLBoundsCoarseWidth(t *testing.T) {
	started := time.Date(2026, 7, 30, 19, 0, 0, 500_000_000, time.UTC)
	coarseLowNarrow, coarseHighNarrow, narrowLow, narrowHigh, epochLowNarrow, epochHighNarrow := incrementalDedupeSQLBounds(started, false)
	if coarseLowNarrow != narrowLow || coarseHighNarrow != narrowHigh {
		t.Fatalf("narrow mode coarse bounds = [%s, %s), want equal to narrow [%s, %s)",
			coarseLowNarrow, coarseHighNarrow, narrowLow, narrowHigh)
	}
	coarseLowWide, coarseHighWide, narrowLowWide, narrowHighWide, epochLowWide, epochHighWide := incrementalDedupeSQLBounds(started, true)
	if narrowLowWide != narrowLow || narrowHighWide != narrowHigh {
		t.Fatalf("narrow bounds differ between modes: %s/%s vs %s/%s", narrowLowWide, narrowHighWide, narrowLow, narrowHigh)
	}
	if epochLowWide != epochLowNarrow || epochHighWide != epochHighNarrow {
		t.Fatalf("epoch bounds differ between modes: [%d, %d] vs [%d, %d]",
			epochLowWide, epochHighWide, epochLowNarrow, epochHighNarrow)
	}
	wantCoarseLow := formatTime(started.Add(-10*time.Minute).Truncate(time.Second).Add(-time.Second).Add(-maxHistoricalUTCOffsetSkew))
	wantCoarseHigh := formatTime(started.Add(10*time.Minute).Truncate(time.Second).Add(time.Second).Add(maxHistoricalUTCOffsetSkew))
	if coarseLowWide != wantCoarseLow || coarseHighWide != wantCoarseHigh {
		t.Fatalf("wide coarse bounds = [%s, %s), want [%s, %s)",
			coarseLowWide, coarseHighWide, wantCoarseLow, wantCoarseHigh)
	}
}
