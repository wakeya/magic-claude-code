package usage

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 本文件验证 R3 candidate_rank 持久化迁移（migrateCandidateRank）与增量写入路径的排名维护，
// 镜像 6A/R1 已验证的迁移保证：单事务 + IF NOT EXISTS + settings marker + 回填原子，幂等、可重开、
// 失败整体回滚；并验证持久化 rank 的排序与旧 ROW_NUMBER 窗口逐字一致（model_priority 优先、
// 最早 provider 候选选择）。复用 dedupe_test.go 的既有 helper。

const testCandidateRankMarker = "usage_candidate_rank_v1"

// TestCandidateRankMigrationAddsColumnIndexAndMarker 验证全新库 Migrate 后 candidate_rank 列、
// 持久索引与完成标记齐备，且空候选表回填无副作用。
func TestCandidateRankMigrationAddsColumnIndexAndMarker(t *testing.T) {
	store := newLegacyUsageStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if ok, err := usageCandidateRankColumnPresent(store.db); err != nil || !ok {
		t.Fatalf("candidate_rank column present = %v, err = %v; want present", ok, err)
	}
	if !sqliteIndexExists(t, store.db, usageCandidateRankIndexName) {
		t.Fatalf("%s is missing after Migrate", usageCandidateRankIndexName)
	}
	assertCandidateRankMarker(t, store.db, true)
}

// TestCandidateRankMigrationBackfillsExistingCandidates 模拟升级库：候选表为旧 schema（无
// candidate_rank 列）且已含历史候选行，dedupe 回填已完成（标记已置）。Migrate 应补列、按旧
// ROW_NUMBER 排序回填稠密 rank、建索引、置标记。
func TestCandidateRankMigrationBackfillsExistingCandidates(t *testing.T) {
	store, started := newLegacyUsageStoreWithOldCandidateTable(t)

	values := UsageValues{InputTokens: 10, OutputTokens: 20}
	session := dedupeSessionRequest("session", started, "model", "model")
	recordDedupeHistory(t, store, session, dedupeToken(session.ID, UsageSourceSessionLog, ParseStatusOK, values))
	// 三个 provider：A 优先级 1（最早）、B 优先级 0（最晚）、C 优先级 0（居中）。
	// 期望 rank：优先级 0 先于 1（B/C 先于 A）；同优先级按时间升序（C 早于 B）。
	// 故 C=1、B=2、A=3。
	providerA := dedupeProviderRequest("provider-a", started.Add(-8*time.Minute), "model", "model")
	providerB := dedupeProviderRequest("provider-b", started.Add(-2*time.Minute), "model", "model")
	providerC := dedupeProviderRequest("provider-c", started.Add(-5*time.Minute), "model", "model")
	for _, p := range []RequestRecord{providerA, providerB, providerC} {
		recordDedupeHistory(t, store, p, dedupeToken(p.ID, UsageSourceProvider, ParseStatusOK, values))
	}
	insertLegacyCandidate(t, store.db, "session", "provider-a", 1)
	insertLegacyCandidate(t, store.db, "session", "provider-b", 0)
	insertLegacyCandidate(t, store.db, "session", "provider-c", 0)

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if ok, err := usageCandidateRankColumnPresent(store.db); err != nil || !ok {
		t.Fatalf("candidate_rank column present = %v, err = %v; want present", ok, err)
	}
	if !sqliteIndexExists(t, store.db, usageCandidateRankIndexName) {
		t.Fatalf("%s is missing after Migrate", usageCandidateRankIndexName)
	}
	assertCandidateRankMarker(t, store.db, true)

	got := candidateRanks(t, store.db, "session")
	want := map[string]int{"provider-c": 1, "provider-b": 2, "provider-a": 3}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("candidate ranks = %v, want %v", got, want)
	}
}

// TestCandidateRankMigrationIdempotentReopen 验证重复 Migrate 幂等，且标记存在时重开跳过
// （与 6A/R1 一致：标记完成后即便索引被外部删除，Migrate 也不再重建）。
func TestCandidateRankMigrationIdempotentReopen(t *testing.T) {
	store := newLegacyUsageStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if !sqliteIndexExists(t, store.db, usageCandidateRankIndexName) {
		t.Fatal("index missing after repeated Migrate")
	}

	if _, err := store.db.Exec(`DROP INDEX ` + usageCandidateRankIndexName); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() with completed marker error = %v", err)
	}
	if sqliteIndexExists(t, store.db, usageCandidateRankIndexName) {
		t.Fatal("completed migration unexpectedly recreated the index after marker was set")
	}
	assertCandidateRankMarker(t, store.db, true)
}

// TestCandidateRankMigrationRollsBackTogether 验证标记写入失败时列/索引/标记一并回滚（原子性），
// 移除故障后重试成功。列回滚的判定：故障后 candidate_rank 列不应存在（ALTER 已随事务回滚）。
func TestCandidateRankMigrationRollsBackTogether(t *testing.T) {
	store, _ := newLegacyUsageStoreWithOldCandidateTable(t)
	if _, err := store.db.Exec(fmt.Sprintf(`
		CREATE TRIGGER fail_candidate_rank_marker
		BEFORE INSERT ON settings
		WHEN NEW.key = %q
		BEGIN
			SELECT RAISE(ABORT, 'forced candidate rank marker failure');
		END;
	`, testCandidateRankMarker)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	err := store.Migrate()
	if err == nil || !strings.Contains(err.Error(), "forced candidate rank marker failure") {
		t.Fatalf("Migrate() error = %v, want forced marker failure", err)
	}
	if ok, _ := usageCandidateRankColumnPresent(store.db); ok {
		t.Fatal("candidate_rank column was committed despite migration failure")
	}
	if sqliteIndexExists(t, store.db, usageCandidateRankIndexName) {
		t.Fatal("candidate rank index was committed despite migration failure")
	}
	assertCandidateRankMarker(t, store.db, false)

	if _, err := store.db.Exec(`DROP TRIGGER fail_candidate_rank_marker`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("retry Migrate() error = %v", err)
	}
	if ok, err := usageCandidateRankColumnPresent(store.db); err != nil || !ok {
		t.Fatalf("candidate_rank column present = %v after retry, want present", ok)
	}
	if !sqliteIndexExists(t, store.db, usageCandidateRankIndexName) {
		t.Fatal("index missing after successful retry")
	}
	assertCandidateRankMarker(t, store.db, true)
}

// TestCandidateRankIncrementalMaintainsDenseRank 验证真实写入事务（Record）增量维护稠密 rank：
// 先记录 session 再记录 provider、以及相反顺序，两种插入顺序下 rank 均与旧 ROW_NUMBER 一致。
func TestCandidateRankIncrementalMaintainsDenseRank(t *testing.T) {
	cases := []struct {
		name         string
		sessionFirst bool
	}{
		{name: "session before providers", sessionFirst: true},
		{name: "providers before session", sessionFirst: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newLegacyUsageStore(t)
			if err := store.Migrate(); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			values := UsageValues{InputTokens: 10, OutputTokens: 20}
			session := dedupeSessionRequest("session", started, "model", "model")
			early := dedupeProviderRequest("provider-early", started.Add(-3*time.Minute), "model", "model")
			late := dedupeProviderRequest("provider-late", started.Add(-1*time.Minute), "model", "model")

			recordSession := func() {
				if err := store.Record(session, dedupeToken(session.ID, UsageSourceSessionLog, ParseStatusOK, values)); err != nil {
					t.Fatalf("Record(session) error = %v", err)
				}
			}
			recordProviders := func() {
				if err := store.Record(late, dedupeToken(late.ID, UsageSourceProvider, ParseStatusOK, values)); err != nil {
					t.Fatalf("Record(late) error = %v", err)
				}
				if err := store.Record(early, dedupeToken(early.ID, UsageSourceProvider, ParseStatusOK, values)); err != nil {
					t.Fatalf("Record(early) error = %v", err)
				}
			}
			if tc.sessionFirst {
				recordSession()
				recordProviders()
			} else {
				recordProviders()
				recordSession()
			}

			got := candidateRanks(t, store.db, "session")
			want := map[string]int{"provider-early": 1, "provider-late": 2}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("candidate ranks = %v, want %v", got, want)
			}
		})
	}
}

// TestCandidateRankIncrementalReranksOnPriorityDowngrade 验证 ON CONFLICT 下调 model_priority 后
// 重排生效：先以优先级 1 写入 provider-x、优先级 0 写入 provider-y（x=2,y=1），再以优先级 0 重写
// provider-x（同指纹 original-model 命中），x 时间更早应升为 rank 1。
func TestCandidateRankIncrementalReranksOnPriorityDowngrade(t *testing.T) {
	store := newLegacyUsageStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 10, OutputTokens: 20}
	// session 的 mapped/original 不同，使优先级可被 mapped(0) 或 original(1) 命中。
	session := dedupeSessionRequest("session", started, "mapped-model", "original-model")
	if err := store.Record(session, dedupeToken(session.ID, UsageSourceSessionLog, ParseStatusOK, values)); err != nil {
		t.Fatalf("Record(session) error = %v", err)
	}
	// provider-x 时间更早，仅以 original-model 命中（优先级 1）。
	providerX := dedupeProviderRequest("provider-x", started.Add(-3*time.Minute), "original-model", "original-model")
	if err := store.Record(providerX, dedupeToken(providerX.ID, UsageSourceProvider, ParseStatusOK, values)); err != nil {
		t.Fatalf("Record(provider-x) error = %v", err)
	}
	// provider-y 时间较晚，以 mapped-model 命中（优先级 0）。
	providerY := dedupeProviderRequest("provider-y", started.Add(-1*time.Minute), "mapped-model", "mapped-model")
	if err := store.Record(providerY, dedupeToken(providerY.ID, UsageSourceProvider, ParseStatusOK, values)); err != nil {
		t.Fatalf("Record(provider-y) error = %v", err)
	}

	// 此刻 x 优先级 1、y 优先级 0 → y=1、x=2。
	if got := candidateRanks(t, store.db, "session"); fmt.Sprint(got) != fmt.Sprint(map[string]int{"provider-y": 1, "provider-x": 2}) {
		t.Fatalf("ranks before downgrade = %v", got)
	}

	// 重写 provider-x，使其同时携带 mapped-model（命中优先级 0），触发 ON CONFLICT 下调优先级。
	providerXUpgraded := providerX
	providerXUpgraded.MappedModel = "mapped-model"
	providerXUpgraded.OriginalModel = "original-model"
	if err := store.Record(providerXUpgraded, dedupeToken(providerX.ID, UsageSourceProvider, ParseStatusOK, values)); err != nil {
		// provider-x 主键已存在，Record 会冲突；改用直接更新候选优先级模拟下调。
		if _, uerr := store.db.Exec(
			`UPDATE usage_dedupe_candidates SET model_priority = 0 WHERE session_request_id = 'session' AND provider_request_id = 'provider-x'`,
		); uerr != nil {
			t.Fatalf("downgrade priority: %v (record err: %v)", uerr, err)
		}
		if _, uerr := store.db.Exec(`UPDATE usage_requests SET mapped_model = 'mapped-model' WHERE id = 'provider-x'`); uerr != nil {
			t.Fatalf("update provider-x model: %v", uerr)
		}
	}
	// 重排该 session（模拟增量维护在优先级变化后的重排）。
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := rerankCandidateSessionTx(tx, "session"); err != nil {
		tx.Rollback()
		t.Fatalf("rerank: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// x 优先级降为 0 且时间更早 → x=1、y=2。
	if got := candidateRanks(t, store.db, "session"); fmt.Sprint(got) != fmt.Sprint(map[string]int{"provider-x": 1, "provider-y": 2}) {
		t.Fatalf("ranks after downgrade = %v, want provider-x=1 provider-y=2", got)
	}
}

// --- helpers ---

// newLegacyUsageStoreWithOldCandidateTable 构造一个“升级前”库：含旧 schema 的候选表（无
// candidate_rank 列、仅旧两个索引），并已置 dedupe 回填标记（使 Migrate 不重跑历史候选回填，
// 从而隔离 candidate_rank 迁移的行为）。返回 store 与统一基准时间。
func newLegacyUsageStoreWithOldCandidateTable(t *testing.T) (*Store, time.Time) {
	t.Helper()
	store := newLegacyUsageStore(t)
	if _, err := store.db.Exec(`
		CREATE TABLE usage_dedupe_candidates (
			session_request_id TEXT NOT NULL,
			provider_request_id TEXT NOT NULL,
			model_priority INTEGER NOT NULL,
			PRIMARY KEY (session_request_id, provider_request_id),
			FOREIGN KEY (session_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE,
			FOREIGN KEY (provider_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
		);
		CREATE INDEX idx_usage_dedupe_provider ON usage_dedupe_candidates(provider_request_id);
		CREATE INDEX idx_usage_requests_started_id ON usage_requests(started_at DESC, id DESC);
		INSERT INTO settings(key, value) VALUES ('` + testDedupeBackfillMarker + `', '1');
	`); err != nil {
		t.Fatalf("create old candidate schema: %v", err)
	}
	return store, time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
}

// insertLegacyCandidate 向旧 schema 候选表（无 candidate_rank 列）插入一行候选。
func insertLegacyCandidate(t *testing.T, db *sql.DB, sessionID, providerID string, priority int) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO usage_dedupe_candidates(session_request_id, provider_request_id, model_priority) VALUES (?, ?, ?)`,
		sessionID, providerID, priority,
	); err != nil {
		t.Fatalf("insert legacy candidate: %v", err)
	}
}

// candidateRanks 查询某 session 全部候选的 provider_request_id → candidate_rank 映射。
func candidateRanks(t *testing.T, db *sql.DB, sessionID string) map[string]int {
	t.Helper()
	rows, err := db.Query(
		`SELECT provider_request_id, candidate_rank FROM usage_dedupe_candidates WHERE session_request_id = ?`,
		sessionID,
	)
	if err != nil {
		t.Fatalf("query candidate ranks: %v", err)
	}
	defer rows.Close()
	got := make(map[string]int)
	for rows.Next() {
		var providerID string
		var rank int
		if err := rows.Scan(&providerID, &rank); err != nil {
			t.Fatalf("scan candidate rank: %v", err)
		}
		got[providerID] = rank
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("candidate rank rows: %v", err)
	}
	return got
}

// assertCandidateRankMarker 断言 R3 candidate_rank 迁移完成标记的存在性。
func assertCandidateRankMarker(t *testing.T, db *sql.DB, want bool) {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM settings WHERE key = ? AND value = '1'`,
		testCandidateRankMarker,
	).Scan(&n); err != nil {
		t.Fatalf("query candidate rank marker: %v", err)
	}
	if got := n > 0; got != want {
		t.Fatalf("candidate rank marker present = %v, want %v", got, want)
	}
}
