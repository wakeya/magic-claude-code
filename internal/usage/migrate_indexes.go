package usage

import (
	"database/sql"
	"fmt"
)

// usageQueryIndexesMarker 标记 R1 查询性能索引迁移已完成。与 6A 候选表迁移一致，标记
// 与索引在同一事务内原子写入：标记存在即保证索引已持久化，重开时跳过（可重开/幂等）。
const usageQueryIndexesMarker = "usage_query_indexes_v1"

// usageQueryIndexStatements 是 R1 新增的持久索引。目标是在不改变任何查询语义/结构的前提下，
// 消除 scoped CTE 的两类可索引开销：
//
//  1. candidate CTE 对 usage_dedupe_candidates 的读取与 ROW_NUMBER 窗口排序：
//     现有 PK 自动索引为 (session_request_id, provider_request_id)，窗口还需 model_priority，
//     故候选表读取非覆盖、窗口需对 (model_priority, epoch, fraction, request_id) 建临时 B-TREE。
//     新增 (session_request_id, model_priority, provider_request_id) 为覆盖索引（含候选 CTE 从 d
//     读取的全部列），并按 PARTITION BY 键 + 首个 ORDER BY 项（model_priority）预排序，缩小窗口
//     临时排序规模、避免回表。
//
// 说明：scoped LEFT JOIN candidate 上的 AUTOMATIC PARTIAL COVERING INDEX 建在“物化后的 candidate
// CTE 结果”上（非基表），基表索引无法消除它；其根因是 candidate 以 ROW_NUMBER CTE 形式每查询物化，
// 属查询结构问题，仅在 R2+ 重写中解决（见诊断报告建议）。Requests 末级 ORDER BY (started_at DESC,
// id DESC) 已由 6A 候选表迁移引入的 idx_usage_requests_started_id 消除（EXPLAIN 验证 requests-page
// 无末级 TEMP B-TREE FOR ORDER BY），故不再重复新增 ASC 复合索引以免拖累写入。
var usageQueryIndexStatements = []string{
	`CREATE INDEX IF NOT EXISTS idx_usage_dedupe_candidates_session_priority
		ON usage_dedupe_candidates(session_request_id, model_priority, provider_request_id);`,
}

// migrateUsageQueryIndexes 以单事务原子地创建 R1 查询性能索引并写入完成标记，复现 6A 已验证的
// “CREATE INDEX IF NOT EXISTS + marker + 原子提交”迁移保证：标记已存在则空提交直接返回（重开跳过）；
// 否则建索引→写标记→Commit，任一失败整体回滚、不写标记、下次启动可安全重跑。
func (s *Store) migrateUsageQueryIndexes() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var completed int
	if err := tx.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM settings WHERE key = ? AND value = '1'
		)`,
		usageQueryIndexesMarker,
	).Scan(&completed); err != nil {
		return fmt.Errorf("query usage query indexes marker: %w", err)
	}
	if completed == 1 {
		return tx.Commit()
	}

	for _, stmt := range usageQueryIndexStatements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create usage query index: %w", err)
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES (?, '1')
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		usageQueryIndexesMarker,
	); err != nil {
		return fmt.Errorf("write usage query indexes marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit usage query indexes migration: %w", err)
	}
	return nil
}

// usageQueryIndexPresent 报告某个 R1 索引是否已存在于给定库上，供测试断言迁移效果。
func usageQueryIndexPresent(db *sql.DB, indexName string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`,
		indexName,
	).Scan(&n)
	return n > 0, err
}
