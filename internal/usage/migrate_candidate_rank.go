package usage

import (
	"database/sql"
	"fmt"
)

// usageCandidateRankMarker 标记 R3 candidate_rank 持久化迁移已完成。与 6A 候选表迁移、R1 索引
// 迁移一致，标记与 schema 变更/回填/索引在同一事务内原子写入：标记存在即保证列已添加、存量候选
// 已回填排名、持久索引已建立；重开时跳过（可重开/幂等）。
const usageCandidateRankMarker = "usage_candidate_rank_v1"

// usageCandidateRankIndexName 是 R3 新增的持久索引名：按 (session_request_id, candidate_rank)
// 组织，使 scoped 读取“某 session 的最优候选（rank 最小）”可走持久索引有序探测，消除旧实现每查询
// 在物化 candidate CTE 上自建的 AUTOMATIC PARTIAL COVERING INDEX 与 ROW_NUMBER 窗口临时排序。
const usageCandidateRankIndexName = "idx_usage_dedupe_candidates_session_rank"

// migrateCandidateRank 以单事务原子地完成 R3 candidate_rank 持久化迁移，复现 6A/R1 已验证的
// “单事务 + IF NOT EXISTS + settings marker + 回填原子”保证：
//
//  1. 标记已存在 → 空提交直接返回（重开跳过，标记即“迁移已完成”的权威信号）；
//  2. 否则依次：确保 candidate_rank 列存在（旧库 ALTER TABLE 补列，新库已由 CREATE TABLE 引入）→
//     全表回填稠密排名 → CREATE INDEX IF NOT EXISTS 建持久索引 → 写标记 → Commit；
//  3. 任一步失败整体回滚、不写标记，下次启动可安全重跑。
//
// 该迁移必须在 migrateDedupeCandidates 之后运行（依赖候选表与其历史回填产生的候选行）。
func (s *Store) migrateCandidateRank() error {
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
		usageCandidateRankMarker,
	).Scan(&completed); err != nil {
		return fmt.Errorf("query usage candidate rank marker: %w", err)
	}
	if completed == 1 {
		return tx.Commit()
	}

	if err := ensureCandidateRankColumnTx(tx); err != nil {
		return err
	}
	if err := backfillCandidateRankTx(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`CREATE INDEX IF NOT EXISTS ` + usageCandidateRankIndexName +
			` ON usage_dedupe_candidates(session_request_id, candidate_rank)`,
	); err != nil {
		return fmt.Errorf("create usage candidate rank index: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES (?, '1')
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		usageCandidateRankMarker,
	); err != nil {
		return fmt.Errorf("write usage candidate rank marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit usage candidate rank migration: %w", err)
	}
	return nil
}

// ensureCandidateRankColumnTx 确保 usage_dedupe_candidates 含 candidate_rank 列。新建库已由
// dedupeMigrationStatements 的 CREATE TABLE 引入该列，此处幂等跳过；升级库（旧 schema 无此列）
// 经 ALTER TABLE ADD COLUMN 补列（NOT NULL DEFAULT 0，存量行暂为 0，由随后回填赋正确排名）。
// SQLite 不支持 ADD COLUMN IF NOT EXISTS，故先 PRAGMA table_info 探测。探测结果集必须在 ALTER
// 之前关闭，避免同连接上未关闭结果集阻塞 schema 变更。
func ensureCandidateRankColumnTx(tx *sql.Tx) error {
	rows, err := tx.Query(`PRAGMA table_info(usage_dedupe_candidates)`)
	if err != nil {
		return fmt.Errorf("query usage candidate table info: %w", err)
	}
	hasColumn := false
	for rows.Next() {
		var cid, notNull, primaryKeyOrder int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKeyOrder); err != nil {
			rows.Close()
			return fmt.Errorf("scan usage candidate table info: %w", err)
		}
		if name == "candidate_rank" {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read usage candidate table info: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close usage candidate table info: %w", err)
	}
	if hasColumn {
		return nil
	}
	if _, err := tx.Exec(
		`ALTER TABLE usage_dedupe_candidates ADD COLUMN candidate_rank INTEGER NOT NULL DEFAULT 0`,
	); err != nil {
		return fmt.Errorf("add usage candidate_rank column: %w", err)
	}
	return nil
}

// usageCandidateRankColumnPresent 报告 candidate_rank 列是否已存在于给定库，供测试断言迁移效果。
func usageCandidateRankColumnPresent(db *sql.DB) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(usage_dedupe_candidates)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKeyOrder int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKeyOrder); err != nil {
			return false, err
		}
		if name == "candidate_rank" {
			return true, nil
		}
	}
	return false, rows.Err()
}
