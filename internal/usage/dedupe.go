package usage

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

const dedupeCandidatesBackfillMarker = "usage_dedupe_candidates_backfill_v1"

// offsetStartedAtMarkerKey 持久化“库中是否存在非 Z 结尾的历史偏移 started_at 文本”的
// 检测结果，供增量候选查询选择 TEXT 粗滤边界宽度（见 incrementalDedupeSQLBounds）。
const offsetStartedAtMarkerKey = "usage_dedupe_offset_started_at_v1"

const (
	incrementalDedupeProviderWhere = `
		r.source_app = 'claude_code'
		AND t.usage_source = 'provider'
		AND t.usage_parse_status = 'ok'
		AND r.source_entrypoint <> 'session_log'
		AND r.provider_id <> '_session'`
	incrementalDedupeSessionWhere = `
		t.usage_parse_status = 'ok'
		AND (
			t.usage_source = 'session_log'
			OR r.source_entrypoint = 'session_log'
			OR r.provider_id = '_session'
		)`
)

var dedupeMigrationStatements = []string{
	`CREATE TABLE IF NOT EXISTS usage_dedupe_candidates (
		session_request_id TEXT NOT NULL,
		provider_request_id TEXT NOT NULL,
		model_priority INTEGER NOT NULL,
		candidate_rank INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session_request_id, provider_request_id),
		FOREIGN KEY (session_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE,
		FOREIGN KEY (provider_request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
	);`,
	`CREATE INDEX IF NOT EXISTS idx_usage_dedupe_provider
		ON usage_dedupe_candidates(provider_request_id);`,
	`CREATE INDEX IF NOT EXISTS idx_usage_requests_started_id
		ON usage_requests(started_at DESC, id DESC);`,
}

type dedupeBackfillProvider struct {
	id        string
	startedAt time.Time
}

type dedupeBackfillModelKey struct {
	key      duplicateIndexKey
	priority int
}

func (s *Store) migrateDedupeCandidates() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range dedupeMigrationStatements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create usage dedupe schema: %w", err)
		}
	}

	var completed int
	if err := tx.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM settings WHERE key = ? AND value = '1'
		)`,
		dedupeCandidatesBackfillMarker,
	).Scan(&completed); err != nil {
		return fmt.Errorf("query usage dedupe backfill marker: %w", err)
	}
	if completed == 1 {
		return tx.Commit()
	}

	if err := backfillDedupeCandidatesTx(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO settings(key, value) VALUES (?, '1')
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		dedupeCandidatesBackfillMarker,
	); err != nil {
		return fmt.Errorf("write usage dedupe backfill marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit usage dedupe migration: %w", err)
	}
	return nil
}

func backfillDedupeCandidatesTx(tx *sql.Tx) error {
	rows, err := tx.Query(
		`SELECT
			r.id, r.started_at, r.source_app, r.source_entrypoint, r.provider_id,
			r.original_model, r.mapped_model,
			t.input_tokens, t.output_tokens, t.cache_creation_input_tokens, t.cache_read_input_tokens,
			t.usage_source, t.usage_parse_status
		 FROM usage_requests r
		 JOIN usage_tokens t ON t.request_id = r.id
		 WHERE t.usage_parse_status = ?
		   AND (
				t.usage_source = ?
				OR r.source_entrypoint = 'session_log'
				OR r.provider_id = '_session'
				OR (r.source_app = 'claude_code' AND t.usage_source = ?)
		   )`,
		ParseStatusOK,
		UsageSourceSessionLog,
		UsageSourceProvider,
	)
	if err != nil {
		return fmt.Errorf("query usage dedupe backfill rows: %w", err)
	}

	var providers []RequestRow
	var sessions []RequestRow
	for rows.Next() {
		var row RequestRow
		var startedAt string
		if err := rows.Scan(
			&row.ID,
			&startedAt,
			&row.SourceApp,
			&row.SourceEntrypoint,
			&row.ProviderID,
			&row.OriginalModel,
			&row.MappedModel,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationInputTokens,
			&row.CacheReadInputTokens,
			&row.UsageSource,
			&row.UsageParseStatus,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan usage dedupe backfill row: %w", err)
		}
		row.StartedAt = parseTime(startedAt)
		switch {
		case isSessionLogRow(row):
			sessions = append(sessions, row)
		case isProviderUsageRow(row):
			providers = append(providers, row)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read usage dedupe backfill rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close usage dedupe backfill rows: %w", err)
	}

	providerIndex := make(map[duplicateIndexKey][]dedupeBackfillProvider)
	for _, provider := range providers {
		for _, key := range duplicateKeys(provider) {
			providerIndex[key] = append(providerIndex[key], dedupeBackfillProvider{
				id:        provider.ID,
				startedAt: provider.StartedAt,
			})
		}
	}
	for key := range providerIndex {
		candidates := providerIndex[key]
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].startedAt.Equal(candidates[j].startedAt) {
				return candidates[i].id < candidates[j].id
			}
			return candidates[i].startedAt.Before(candidates[j].startedAt)
		})
	}

	insert, err := tx.Prepare(
		`INSERT INTO usage_dedupe_candidates(
			session_request_id, provider_request_id, model_priority
		 ) VALUES (?, ?, ?)
		 ON CONFLICT(session_request_id, provider_request_id) DO UPDATE SET
			model_priority = MIN(model_priority, excluded.model_priority)`,
	)
	if err != nil {
		return fmt.Errorf("prepare usage dedupe candidate insert: %w", err)
	}
	defer insert.Close()

	for _, session := range sessions {
		for _, modelKey := range dedupeBackfillModelKeys(session) {
			candidates := providerIndex[modelKey.key]
			start := session.StartedAt.Add(-10 * time.Minute)
			end := session.StartedAt.Add(10 * time.Minute)
			first := sort.Search(len(candidates), func(i int) bool {
				return !candidates[i].startedAt.Before(start)
			})
			for i := first; i < len(candidates) && !candidates[i].startedAt.After(end); i++ {
				if _, err := insert.Exec(session.ID, candidates[i].id, modelKey.priority); err != nil {
					return fmt.Errorf("insert usage dedupe candidate: %w", err)
				}
			}
		}
	}
	return nil
}

func dedupeBackfillModelKeys(row RequestRow) []dedupeBackfillModelKey {
	keys := make([]dedupeBackfillModelKey, 0, 2)
	if row.MappedModel != "" {
		keys = append(keys, dedupeBackfillModelKey{
			key: duplicateIndexKey{
				model:                    row.MappedModel,
				inputTokens:              row.InputTokens,
				outputTokens:             row.OutputTokens,
				cacheCreationInputTokens: row.CacheCreationInputTokens,
				cacheReadInputTokens:     row.CacheReadInputTokens,
			},
			priority: 0,
		})
	}
	if row.OriginalModel != "" && row.OriginalModel != row.MappedModel {
		keys = append(keys, dedupeBackfillModelKey{
			key: duplicateIndexKey{
				model:                    row.OriginalModel,
				inputTokens:              row.InputTokens,
				outputTokens:             row.OutputTokens,
				cacheCreationInputTokens: row.CacheCreationInputTokens,
				cacheReadInputTokens:     row.CacheReadInputTokens,
			},
			priority: 1,
		})
	}
	return keys
}

// maintainDedupeCandidatesTx 在写入事务内增量维护候选表。wideCoarseBounds 由 Store 的
// 迁移期检测缓存给出：库中存在非 Z 结尾的历史偏移文本时放宽 TEXT 粗滤边界。
func (s *Store) maintainDedupeCandidatesTx(tx *sql.Tx, req RequestRecord, tok TokenRecord) error {
	current := RequestRow{RequestRecord: req, TokenRecord: tok}
	current.SourceApp = defaultString(current.SourceApp, "unknown")
	current.UsageSource = defaultString(current.UsageSource, UsageSourceNone)
	current.UsageParseStatus = defaultString(current.UsageParseStatus, ParseStatusMissing)

	var oppositeWhere string
	switch {
	case isSessionLogRow(current) && current.UsageParseStatus == ParseStatusOK:
		oppositeWhere = incrementalDedupeProviderWhere
	case isProviderUsageRow(current):
		oppositeWhere = incrementalDedupeSessionWhere
	default:
		return nil
	}

	// Unknown（Migrate 未完成）保守按宽边界处理，正确性优先。
	wideCoarseBounds := s.hasOffsetStartedAt.Load() != offsetStartedAtAllCanonical
	opposite, err := queryDedupeOppositeRowsTx(tx, current, oppositeWhere, wideCoarseBounds)
	if err != nil {
		return err
	}
	insert, err := tx.Prepare(
		`INSERT INTO usage_dedupe_candidates(
			session_request_id, provider_request_id, model_priority
		 ) VALUES (?, ?, ?)
		 ON CONFLICT(session_request_id, provider_request_id) DO UPDATE SET
			model_priority = MIN(model_priority, excluded.model_priority)`,
	)
	if err != nil {
		return fmt.Errorf("prepare incremental usage dedupe candidate insert: %w", err)
	}
	defer insert.Close()

	// affected 收集本次写入触及的全部 session_request_id：插入新候选或经 ON CONFLICT 下调
	// 既有候选的 model_priority 都会改变该 session 的候选排序，故在循环结束后对其持久化
	// candidate_rank 统一重排（R3）。每个 session 的候选数极少（±10 分钟窗口内同指纹），重排
	// 成本可忽略，且与 usage 写入处于同一事务，失败一并回滚。
	affected := make(map[string]struct{})
	for _, candidate := range opposite {
		session, provider := current, candidate
		if isProviderUsageRow(current) {
			session, provider = candidate, current
		}
		if provider.StartedAt.Before(session.StartedAt.Add(-10*time.Minute)) ||
			provider.StartedAt.After(session.StartedAt.Add(10*time.Minute)) {
			continue
		}
		priority, ok := dedupeCandidateModelPriority(session, provider)
		if !ok {
			continue
		}
		if _, err := insert.Exec(session.ID, provider.ID, priority); err != nil {
			return fmt.Errorf("insert incremental usage dedupe candidate: %w", err)
		}
		affected[session.ID] = struct{}{}
	}
	for sessionID := range affected {
		if err := rerankCandidateSessionTx(tx, sessionID); err != nil {
			return err
		}
	}
	return nil
}

// candidateRankOrderExpr 返回 candidate_rank 的 ORDER BY 表达式主体（不含 "ORDER BY" 前缀）。
// 排序与 buildScopedCTE 旧 ROW_NUMBER 窗口逐字一致：model_priority 升序优先，随后 provider 行
// started_at 的整秒 epoch 升序、9 位小数秒升序，最后 provider_request_id 升序。candidateAlias
// 为候选表别名（提供 model_priority / provider_request_id），providerAlias 为已 JOIN 的 provider
// usage_requests 别名（其 started_at 决定时间序）。持久化 rank 与查询期 MIN(candidate_rank)
// 共用此排序，保证“最早 provider 候选选择”语义在写入/回填/读取三处完全一致。
func candidateRankOrderExpr(candidateAlias, providerAlias string) string {
	return candidateAlias + `.model_priority ASC, ` +
		scopedEpochSecondsExpr(providerAlias+`.started_at`) + ` ASC, ` +
		scopedStartedAtFractionExpr(providerAlias+`.started_at`) + ` ASC, ` +
		candidateAlias + `.provider_request_id ASC`
}

// rerankCandidateSessionTx 在事务内重排单个 session 的候选持久化 candidate_rank 为稠密 1..N，
// 排序由 candidateRankOrderExpr 给出。供增量写入路径在候选集合变化后维护排名；候选数极少，
// 单会话重排开销可忽略。ROW_NUMBER 的 PARTITION 退化为单一 session（WHERE 已限定）。
func rerankCandidateSessionTx(tx *sql.Tx, sessionRequestID string) error {
	_, err := tx.Exec(`
		UPDATE usage_dedupe_candidates
		SET candidate_rank = ranked.rn
		FROM (
			SELECT d2.provider_request_id AS pid,
				ROW_NUMBER() OVER (
					ORDER BY `+candidateRankOrderExpr("d2", "p2")+`
				) AS rn
			FROM usage_dedupe_candidates d2
			JOIN usage_requests p2 ON p2.id = d2.provider_request_id
			WHERE d2.session_request_id = ?
		) AS ranked
		WHERE usage_dedupe_candidates.session_request_id = ?
		  AND usage_dedupe_candidates.provider_request_id = ranked.pid`,
		sessionRequestID, sessionRequestID)
	if err != nil {
		return fmt.Errorf("rerank usage candidate session %q: %w", sessionRequestID, err)
	}
	return nil
}

// backfillCandidateRankTx 在事务内为全表候选批量计算持久化 candidate_rank（按 session 分区稠密
// 1..N）。供历史回填迁移一次性升级存量候选；与 6A 候选回填同事务原子提交。ROW_NUMBER 排序与
// 增量重排、查询期 MIN 完全一致。
func backfillCandidateRankTx(tx *sql.Tx) error {
	_, err := tx.Exec(`
		UPDATE usage_dedupe_candidates
		SET candidate_rank = ranked.rn
		FROM (
			SELECT d2.session_request_id AS sid, d2.provider_request_id AS pid,
				ROW_NUMBER() OVER (
					PARTITION BY d2.session_request_id
					ORDER BY ` + candidateRankOrderExpr("d2", "p2") + `
				) AS rn
			FROM usage_dedupe_candidates d2
			JOIN usage_requests p2 ON p2.id = d2.provider_request_id
		) AS ranked
		WHERE usage_dedupe_candidates.session_request_id = ranked.sid
		  AND usage_dedupe_candidates.provider_request_id = ranked.pid`)
	if err != nil {
		return fmt.Errorf("backfill usage candidate rank: %w", err)
	}
	return nil
}

func queryDedupeOppositeRowsTx(tx *sql.Tx, current RequestRow, oppositeWhere string, wideCoarseBounds bool) ([]RequestRow, error) {
	query, args := incrementalDedupeCandidateQuery(current, oppositeWhere, wideCoarseBounds)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query incremental usage dedupe candidates: %w", err)
	}

	var candidates []RequestRow
	for rows.Next() {
		var candidate RequestRow
		var startedAt string
		if err := rows.Scan(
			&candidate.ID,
			&startedAt,
			&candidate.OriginalModel,
			&candidate.MappedModel,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan incremental usage dedupe candidate: %w", err)
		}
		candidate.StartedAt = parseTime(startedAt)
		candidate.InputTokens = current.InputTokens
		candidate.OutputTokens = current.OutputTokens
		candidate.CacheCreationInputTokens = current.CacheCreationInputTokens
		candidate.CacheReadInputTokens = current.CacheReadInputTokens
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read incremental usage dedupe candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close incremental usage dedupe candidates: %w", err)
	}
	return candidates, nil
}

func incrementalDedupeCandidateQuery(current RequestRow, oppositeWhere string, wideCoarseBounds bool) (string, []any) {
	query := `SELECT
			r.id, r.started_at, r.original_model, r.mapped_model
		 FROM usage_requests r INDEXED BY idx_usage_requests_started_id
		 JOIN usage_tokens t ON t.request_id = r.id
		 WHERE ` + oppositeWhere + `
		   AND r.started_at >= ?
		   AND r.started_at < ?
		   AND (
				(r.started_at >= ? AND r.started_at < ?)
				OR substr(r.started_at, -1) <> 'Z'
		   )
		   AND ` + scopedEpochSecondsExpr("r.started_at") + ` >= ?
		   AND ` + scopedEpochSecondsExpr("r.started_at") + ` <= ?
		   AND t.input_tokens = ?
		   AND t.output_tokens = ?
		   AND t.cache_creation_input_tokens = ?
		   AND t.cache_read_input_tokens = ?
		   AND (
				(r.mapped_model <> '' AND (r.mapped_model = ? OR r.mapped_model = ?))
				OR
				(r.original_model <> '' AND (r.original_model = ? OR r.original_model = ?))
		   )`
	coarseLower, coarseUpper, narrowLower, narrowUpper, epochLower, epochUpper := incrementalDedupeSQLBounds(current.StartedAt, wideCoarseBounds)
	args := []any{
		coarseLower,
		coarseUpper,
		narrowLower,
		narrowUpper,
		epochLower,
		epochUpper,
		current.InputTokens,
		current.OutputTokens,
		current.CacheCreationInputTokens,
		current.CacheReadInputTokens,
		current.MappedModel,
		current.OriginalModel,
		current.MappedModel,
		current.OriginalModel,
	}
	return query, args
}

// maxHistoricalUTCOffsetSkew 是 Go time.Parse(RFC3339Nano) 可接受的时区偏移上限（实测
// ±24:59 可解析、±25:00 拒绝；取 25 小时为上界）叠加 1 秒小数秒字典序余量。历史库
// started_at 若保留带偏移文本（如 2026-07-30T12:00:00-07:00），其墙上时间文本相对同一
// instant 的 canonical UTC 文本最多偏离该量；增量候选 TEXT 粗滤边界按此放宽，保证任何
// 窗口内候选的原始文本都落在粗滤区间内（严格超集，不参与候选判定）。
const maxHistoricalUTCOffsetSkew = 25*time.Hour + time.Second

// incrementalDedupeSQLBounds 计算增量候选查询的三组时间过滤边界：
//
//   - epochLower/epochUpper（决定性过滤）：±10 分钟窗口各加 1 秒整秒余量后的 Unix 秒，
//     与 SQL 中 scopedEpochSecondsExpr（strftime('%s') 解析后的整秒 epoch，非法时戳容错为
//     Go 零值）比较。strftime 按 instant 解析带任意合法偏移的 RFC3339 文本，故该过滤对
//     历史偏移文本与 canonical UTC 文本给出同一 instant 判定；±1 秒余量吸收小数秒截断与
//     负 epoch（1970 年前）截断方向差异，使过滤结果构成 Go 含边界窗口的严格超集。
//     原始时间 TEXT 字典序比较不决定候选（M-1）：带偏移文本与 canonical 边界不在同一
//     instant，直接做字符串范围比较会漏配窗口内候选；候选语义完全由 epoch 过滤与
//     maintainDedupeCandidatesTx 的 Go Before/After 含边界窗口判定（小数秒精度）决定，
//     与旧 Go 去重逐字段一致。
//   - coarseLower/coarseUpper（TEXT 索引粗滤）：仅为 idx_usage_requests_started_id 索引
//     加速，边界在窗口外再按 maxHistoricalUTCOffsetSkew 放宽，保证任何窗口内候选（无论
//     偏移多大）的原始文本都落在区间内（严格超集）。
//   - narrowLower/narrowUpper（canonical 行快速通道）：等价旧 ±10 分钟±1 秒 TEXT 边界。
//     本系统写入（formatTime）恒输出以 'Z' 结尾的 canonical UTC 文本，其字典序即时序，
//     窄范围对 Z 行构成 epoch 窗口的严格超集；查询中 Z 行仅需满足窄范围即可进入 JOIN，
//     使全 canonical 库（常态）的 JOIN 行数与旧实现相同，非 Z 的历史偏移行则经宽粗滤
//     范围进入、由 epoch 过滤决定。两分支 OR 后再统一过 epoch 过滤，语义不变。
// wideCoarseBounds 为 false（迁移期检测确认全库 started_at 均为 Z 结尾 canonical 文本）
// 时，粗滤边界收敛为窄边界：Z 文本字典序即时序，窄范围对 Z 行构成 epoch 窗口的严格
// 超集，索引扫描范围与旧实现相同（常态库写路径性能不变）；为 true（库中存在历史偏移
// 文本，或检测状态未知）时按 maxHistoricalUTCOffsetSkew 放宽，覆盖任意合法偏移行。
func incrementalDedupeSQLBounds(startedAt time.Time, wideCoarseBounds bool) (coarseLower, coarseUpper, narrowLower, narrowUpper string, epochLower, epochUpper int64) {
	lower := startedAt.Add(-10 * time.Minute).Truncate(time.Second).Add(-time.Second)
	upper := startedAt.Add(10 * time.Minute).Truncate(time.Second).Add(time.Second)
	coarseLow, coarseHigh := lower, upper
	if wideCoarseBounds {
		coarseLow = lower.Add(-maxHistoricalUTCOffsetSkew)
		coarseHigh = upper.Add(maxHistoricalUTCOffsetSkew)
	}
	return formatTime(coarseLow),
		formatTime(coarseHigh),
		formatTime(lower),
		formatTime(upper),
		lower.Unix(),
		upper.Unix()
}

// migrateOffsetStartedAtMarker 检测并持久化“库中是否存在非 Z 结尾的 started_at 文本”，
// 并加载进内存缓存。本系统所有写入经 Record → formatTime 恒输出 'Z' 结尾的 canonical
// UTC 文本，运行期写入不会引入非 Z 行，故该状态只需迁移期检测一次（后续启动直接读
// 取 marker）：检测以迁移时的库快照为准，与 M-1 复现路径“历史脏行 + 迁移后新增对端”
// 的时序一致。检测结果只影响增量候选查询的索引扫描范围，不影响候选语义（始终由
// epoch 过滤与 Go 窗口判定决定）。
func (s *Store) migrateOffsetStartedAtMarker() error {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, offsetStartedAtMarkerKey).Scan(&value)
	switch {
	case err == nil:
		if value == "1" {
			s.hasOffsetStartedAt.Store(offsetStartedAtPresent)
		} else {
			s.hasOffsetStartedAt.Store(offsetStartedAtAllCanonical)
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("query usage offset started_at marker: %w", err)
	}
	var present int
	if err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM usage_requests WHERE substr(started_at, -1) <> 'Z')`,
	).Scan(&present); err != nil {
		return fmt.Errorf("detect usage offset started_at rows: %w", err)
	}
	marker := "0"
	state := int32(offsetStartedAtAllCanonical)
	if present == 1 {
		marker = "1"
		state = offsetStartedAtPresent
	}
	if _, err := s.db.Exec(
		`INSERT INTO settings(key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		offsetStartedAtMarkerKey, marker,
	); err != nil {
		return fmt.Errorf("write usage offset started_at marker: %w", err)
	}
	s.hasOffsetStartedAt.Store(state)
	return nil
}

func dedupeCandidateModelPriority(session, provider RequestRow) (int, bool) {
	providerKeys := make(map[duplicateIndexKey]struct{})
	for _, key := range duplicateKeys(provider) {
		providerKeys[key] = struct{}{}
	}
	for _, modelKey := range dedupeBackfillModelKeys(session) {
		if _, ok := providerKeys[modelKey.key]; ok {
			return modelKey.priority, true
		}
	}
	return 0, false
}
