package usage

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// 本文件验证 R1 查询性能索引迁移（migrateUsageQueryIndexes）保持 6A 已验证的迁移保证：
// CREATE INDEX IF NOT EXISTS + settings 标记 + 单事务原子提交，幂等、可重开、失败整体回滚。
// 复用 dedupe_test.go / store_test.go 的既有 helper（newLegacyUsageStore / sqliteIndexExists 等）。

const testUsageQueryIndexesMarker = "usage_query_indexes_v1"

// TestUsageQueryIndexesMigrationCreatesIndexAndMarker 验证首次 Migrate 建立索引与完成标记。
func TestUsageQueryIndexesMigrationCreatesIndexAndMarker(t *testing.T) {
	store := newLegacyUsageStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if !sqliteIndexExists(t, store.db, "idx_usage_dedupe_candidates_session_priority") {
		t.Fatal("idx_usage_dedupe_candidates_session_priority is missing after Migrate")
	}
	assertUsageQueryIndexesMarker(t, store.db, true)
}

// TestUsageQueryIndexesMigrationIdempotentReopen 验证重复 Migrate 幂等，且标记存在时重开跳过
// （与 6A 一致：标记完成后即便索引被外部删除，Migrate 也不再重建——标记即“迁移已完成”的权威信号）。
func TestUsageQueryIndexesMigrationIdempotentReopen(t *testing.T) {
	store := newLegacyUsageStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if !sqliteIndexExists(t, store.db, "idx_usage_dedupe_candidates_session_priority") {
		t.Fatal("index missing after repeated Migrate")
	}

	// 标记已存在时重开：删除索引后再 Migrate，应跳过（不重建），复现 6A 标记防重跑语义。
	if _, err := store.db.Exec(`DROP INDEX idx_usage_dedupe_candidates_session_priority`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() with completed marker error = %v", err)
	}
	if sqliteIndexExists(t, store.db, "idx_usage_dedupe_candidates_session_priority") {
		t.Fatal("completed migration unexpectedly recreated the index after marker was set")
	}
	assertUsageQueryIndexesMarker(t, store.db, true)
}

// TestUsageQueryIndexesMigrationRollsBackIndexAndMarkerTogether 验证标记写入失败时索引与标记
// 一并回滚（原子性），移除故障后重试可成功建立两者。
func TestUsageQueryIndexesMigrationRollsBackIndexAndMarkerTogether(t *testing.T) {
	store := newLegacyUsageStore(t)
	if _, err := store.db.Exec(fmt.Sprintf(`
		CREATE TRIGGER fail_usage_query_indexes_marker
		BEFORE INSERT ON settings
		WHEN NEW.key = %q
		BEGIN
			SELECT RAISE(ABORT, 'forced usage query indexes marker failure');
		END;
	`, testUsageQueryIndexesMarker)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	err := store.Migrate()
	if err == nil || !strings.Contains(err.Error(), "forced usage query indexes marker failure") {
		t.Fatalf("Migrate() error = %v, want forced marker failure", err)
	}
	if sqliteIndexExists(t, store.db, "idx_usage_dedupe_candidates_session_priority") {
		t.Fatal("query index was committed despite migration failure")
	}
	assertUsageQueryIndexesMarker(t, store.db, false)

	if _, err := store.db.Exec(`DROP TRIGGER fail_usage_query_indexes_marker`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("retry Migrate() error = %v", err)
	}
	if !sqliteIndexExists(t, store.db, "idx_usage_dedupe_candidates_session_priority") {
		t.Fatal("index missing after successful retry")
	}
	assertUsageQueryIndexesMarker(t, store.db, true)
}

// assertUsageQueryIndexesMarker 断言 R1 索引迁移完成标记的存在性。
func assertUsageQueryIndexesMarker(t *testing.T, db *sql.DB, want bool) {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM settings WHERE key = ? AND value = '1'`,
		testUsageQueryIndexesMarker,
	).Scan(&n); err != nil {
		t.Fatalf("query marker: %v", err)
	}
	if got := n > 0; got != want {
		t.Fatalf("usage query indexes marker present = %v, want %v", got, want)
	}
}
