package usage

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// offsetStartedAtUnknown/AllCanonical/Present 是 Store.hasOffsetStartedAt 的三态值：
// 库中是否存在不以 'Z' 结尾的 started_at 文本（历史脏库可能保留带时区偏移的
// RFC3339 文本）。Unknown 仅在 Migrate 未完成时出现，增量候选查询保守按宽边界处理。
const (
	offsetStartedAtUnknown = iota
	offsetStartedAtAllCanonical
	offsetStartedAtPresent
)

type Store struct {
	db *sql.DB
	// hasOffsetStartedAt 缓存“库中是否存在非 Z 结尾的历史偏移 started_at 文本”的
	// 迁移期检测结果（settings 持久化）。本系统所有写入经 Record → formatTime 恒输出
	// canonical UTC（'Z' 结尾）文本，故该状态在运行期不会由 false 变 true：检测为
	// 全 canonical 时增量候选查询可用窄 TEXT 粗滤边界（旧性能），否则放宽至覆盖任意
	// 合法偏移。候选语义始终由 epoch 过滤与 Go 窗口判定决定，该缓存只影响索引扫描范围。
	hasOffsetStartedAt atomic.Int32
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS usage_requests (
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
		);`,
		`CREATE TABLE IF NOT EXISTS usage_tokens (
			request_id TEXT PRIMARY KEY,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
			usage_source TEXT NOT NULL DEFAULT 'none',
			usage_parse_status TEXT NOT NULL DEFAULT 'missing',
			usage_parse_error TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS session_log_sync (
			file_path TEXT PRIMARY KEY,
			last_modified INTEGER NOT NULL DEFAULT 0,
			last_line_offset INTEGER NOT NULL DEFAULT 0,
			last_synced_at TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_started_at ON usage_requests(started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_provider ON usage_requests(provider_id, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_provider_url ON usage_requests(provider_api_url, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_entrypoint ON usage_requests(source_entrypoint, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_path ON usage_requests(request_path, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_model ON usage_requests(mapped_model, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_source ON usage_requests(source_app, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_status ON usage_requests(status_code, error_type, started_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_tokens_source ON usage_tokens(usage_source);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_tokens_parse_status ON usage_tokens(usage_parse_status);`,
		`CREATE INDEX IF NOT EXISTS idx_session_log_sync_synced_at ON session_log_sync(last_synced_at);`,
		`INSERT OR IGNORE INTO settings(key, value) VALUES ('usage_retention_days', '90');`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.migrateNonNegativeUsageValues(); err != nil {
		return err
	}
	if err := s.migrateDedupeCandidates(); err != nil {
		return err
	}
	if err := s.migrateCandidateRank(); err != nil {
		return err
	}
	if err := s.migrateOffsetStartedAtMarker(); err != nil {
		return err
	}
	return s.migrateUsageQueryIndexes()
}

// migrateNonNegativeUsageValues repairs rows written by versions that did not
// enforce the usage value contract. It is idempotent and runs before candidate
// backfill so historical negative values cannot participate in dedupe or SQL
// aggregation.
func (s *Store) migrateNonNegativeUsageValues() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`UPDATE usage_tokens SET input_tokens = 0 WHERE input_tokens < 0`,
		`UPDATE usage_tokens SET output_tokens = 0 WHERE output_tokens < 0`,
		`UPDATE usage_tokens SET cache_creation_input_tokens = 0 WHERE cache_creation_input_tokens < 0`,
		`UPDATE usage_tokens SET cache_read_input_tokens = 0 WHERE cache_read_input_tokens < 0`,
		`UPDATE usage_requests SET duration_ms = 0 WHERE duration_ms < 0`,
		`UPDATE usage_requests SET upstream_response_header_ms = 0 WHERE upstream_response_header_ms < 0`,
		`UPDATE usage_requests SET time_to_first_byte_ms = 0 WHERE time_to_first_byte_ms < 0`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateNonNegativeUsage(req RequestRecord, tok TokenRecord) error {
	if tok.InputTokens < 0 || tok.OutputTokens < 0 ||
		tok.CacheCreationInputTokens < 0 || tok.CacheReadInputTokens < 0 {
		return ErrNegativeTokenCount
	}
	if (req.DurationMS != nil && *req.DurationMS < 0) ||
		(req.UpstreamResponseHeaderMS != nil && *req.UpstreamResponseHeaderMS < 0) ||
		(req.TimeToFirstByteMS != nil && *req.TimeToFirstByteMS < 0) {
		return ErrNegativeDuration
	}
	return nil
}

func (s *Store) Record(req RequestRecord, tok TokenRecord) error {
	if err := validateNonNegativeUsage(req, tok); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if tok.RequestID == "" {
		tok.RequestID = req.ID
	}
	_, err = tx.Exec(
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
	)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
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
	)
	if err != nil {
		return err
	}
	if err := s.maintainDedupeCandidatesTx(tx, req, tok); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recordIfAbsent(req RequestRecord, tok TokenRecord) (bool, error) {
	if err := validateNonNegativeUsage(req, tok); err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if tok.RequestID == "" {
		tok.RequestID = req.ID
	}
	result, err := tx.Exec(
		`INSERT OR IGNORE INTO usage_requests(
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
	)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return false, nil
	}
	_, err = tx.Exec(
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
	)
	if err != nil {
		return false, err
	}
	if err := s.maintainDedupeCandidatesTx(tx, req, tok); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) ClearUsageData(resetSessionSync bool) (ClearResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return ClearResult{}, err
	}
	defer tx.Rollback()

	tokenResult, err := tx.Exec(`DELETE FROM usage_tokens`)
	if err != nil {
		return ClearResult{}, err
	}
	requestResult, err := tx.Exec(`DELETE FROM usage_requests`)
	if err != nil {
		return ClearResult{}, err
	}
	if resetSessionSync {
		if _, err := tx.Exec(`DELETE FROM session_log_sync`); err != nil {
			return ClearResult{}, err
		}
	}

	clearedTokens, _ := tokenResult.RowsAffected()
	clearedRequests, _ := requestResult.RowsAffected()
	result := ClearResult{
		Success:          true,
		ClearedRequests:  clearedRequests,
		ClearedTokens:    clearedTokens,
		ResetSessionSync: resetSessionSync,
	}
	if err := tx.Commit(); err != nil {
		return ClearResult{}, err
	}
	return result, nil
}

// Summary 在 scoped SQL 数据集上用 COUNT/SUM/MAX 聚合计算状态页/摘要所需统计，
// 不再物化全宽请求行或在 Go 中逐行汇总。筛选、去重、口径由 buildScopedCTE 统一
// 下推；hasUsage/isFailed/今日区间/覆盖率与最新请求时间的数值与空值语义与旧算法
// 逐字段兼容（由 legacyOracleSummary 差分测试保证）。
func (s *Store) Summary(filter Filter) (Summary, error) {
	startOfToday, endOfToday, err := todayRange(filter)
	if err != nil {
		return Summary{}, err
	}
	query, args := buildSummaryQuery(filter, startOfToday, endOfToday)

	var summary Summary
	var withUsage int64
	var lastStarted sql.NullString
	if err := s.db.QueryRow(query, args...).Scan(
		&summary.ProviderRequestsTotal,
		&withUsage,
		&summary.TokenConsumptionTotal,
		&summary.FailedRequests,
		&summary.TodayProviderRequests,
		&summary.TodayTokenConsumption,
		&lastStarted,
	); err != nil {
		return Summary{}, err
	}
	// scoped 数据集非空时 MAX 聚合必返回一个 started_at（可能为非法历史值，parseTime
	// 容错为 Go 零值时间），对应旧实现“有行即非 nil”；空数据集 MAX 为 NULL 对应 nil。
	if lastStarted.Valid {
		started := parseTime(lastStarted.String)
		summary.LastProviderRequest = &started
	}
	if summary.ProviderRequestsTotal > 0 {
		summary.UsageCoverage = float64(withUsage) / float64(summary.ProviderRequestsTotal)
	}
	return summary, nil
}

// Trends 在 scoped SQL 数据集上用 GROUP BY 本地日期桶聚合计算每日趋势，不再物化
// 全宽请求行或在 Go 中逐行分桶。桶边界先查询数据集有效时间戳的最小/最大整秒，再
// 在 Go 中推导时区偏移区间（含夏令时切换，精确到秒），最后渲染为 SQL CASE：非法
// 时间戳落入 Go 零值时间的本地日期标签，有效时间戳按偏移区间用
// strftime('%Y-%m-%d', epoch+offset, 'unixepoch') 换算，与旧算法
// StartedAt.In(loc).Format("2006-01-02") 逐秒等价。筛选、去重、口径由 buildScopedCTE
// 统一下推；缺失桶不补零、桶字符串升序、UsageCoverage 由 withUsage/total 在 Go 中
// 相除、空数据集返回非 nil 空切片（JSON []）的数值与空值语义与旧算法逐字段兼容
// （由 legacyOracleTrends 差分测试保证）。
func (s *Store) Trends(filter Filter) ([]TrendPoint, error) {
	loc, err := filterLocation(filter)
	if err != nil {
		return nil, err
	}
	rangeQuery, rangeArgs := buildTrendsRangeQuery(filter)
	var minEpoch, maxEpoch sql.NullInt64
	if err := s.db.QueryRow(rangeQuery, rangeArgs...).Scan(&minEpoch, &maxEpoch); err != nil {
		return nil, err
	}
	intervals := trendsZoneIntervals(loc, minEpoch, maxEpoch)
	query, args := buildTrendsQuery(filter, loc, intervals)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TrendPoint, 0)
	for rows.Next() {
		var point TrendPoint
		var withUsage int64
		if err := rows.Scan(
			&point.Bucket,
			&point.ProviderRequestsTotal,
			&withUsage,
			&point.InputTokens,
			&point.OutputTokens,
			&point.CacheCreationInputTokens,
			&point.CacheReadInputTokens,
			&point.TokenConsumptionTotal,
			&point.FailedRequests,
		); err != nil {
			return nil, err
		}
		if point.ProviderRequestsTotal > 0 {
			point.UsageCoverage = float64(withUsage) / float64(point.ProviderRequestsTotal)
		}
		out = append(out, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// trendsZoneIntervals 由区间查询结果推导时区偏移区间：数据集没有有效时间戳时返回
// 单一占位区间（桶表达式只会命中非法时间戳分支，偏移仅用于防御两次查询之间新插
// 入的行）；上界延伸到当前时间，使区间覆盖两次查询之间可能出现的最新行。
func trendsZoneIntervals(loc *time.Location, minEpoch, maxEpoch sql.NullInt64) []scopedZoneInterval {
	if !minEpoch.Valid || !maxEpoch.Valid {
		_, offset := time.Now().In(loc).Zone()
		return []scopedZoneInterval{{offset: offset}}
	}
	upper := maxEpoch.Int64
	if now := time.Now().Unix(); now > upper {
		upper = now
	}
	return scopedZoneOffsetIntervals(loc, minEpoch.Int64, upper)
}

func (s *Store) Requests(filter Filter) (RequestPage, error) {
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	// 总数与分页都下推到 scoped SQL 数据集：COUNT(*) 计算去重+口径后的总数，
	// LIMIT/OFFSET 只取当前页。禁止全量加载后在 Go 中切片。
	countSQL, pageSQL, baseArgs := buildRequestsQueries(filter)

	var total int64
	if err := s.db.QueryRow(countSQL, baseArgs...).Scan(&total); err != nil {
		return RequestPage{}, err
	}

	args := make([]any, 0, len(baseArgs)+2)
	args = append(args, baseArgs...)
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := s.db.Query(pageSQL, args...)
	if err != nil {
		return RequestPage{}, err
	}
	defer rows.Close()

	var pageRows []RequestRow
	for rows.Next() {
		var dedupeStatus, dedupeRequestID string
		row, err := scanRequestRow(rows, &dedupeStatus, &dedupeRequestID)
		if err != nil {
			return RequestPage{}, err
		}
		row.DedupeStatus = dedupeStatus
		row.DedupeRequestID = dedupeRequestID
		pageRows = append(pageRows, row)
	}
	if err := rows.Err(); err != nil {
		return RequestPage{}, err
	}
	// 与旧“全量加载后切片”路径保持 JSON 形状一致：scoped 数据集非空时，越界空页
	// 仍返回 []（而非 null），只有真正零结果才返回 nil。
	if total > 0 && pageRows == nil {
		pageRows = []RequestRow{}
	}
	return RequestPage{Rows: pageRows, Total: total, Page: page, PageSize: pageSize}, nil
}

// Providers 在 scoped SQL 数据集上用 GROUP BY provider_id 聚合计算供应商维度统计，不再
// 物化全宽请求行或在 Go 中逐行汇总。分组键、筛选、去重、口径由 buildScopedCTE 统一下推；
// “首行”维度字段（provider_name/mapped_model）由 ROW_NUMBER 按旧 queryRows 的字符串序
// （started_at DESC, id DESC）选取，失败分类/token 总和/覆盖率/平均耗时的数值与空值语义、
// 主排序键（TotalRequests 降序）与旧算法逐字段兼容，分组同数由 provider_id 升序决胜
// （旧不稳定排序下未定义，R1 允许确定化）；由 legacyOracleAggregate 差分测试保证。
func (s *Store) Providers(filter Filter) ([]AggregateRow, error) {
	return s.aggregateSQL(filter, "r.provider_id", "r.provider_name")
}

// Models 在 scoped SQL 数据集上用 GROUP BY mapped_model 聚合计算模型维度统计，语义与
// Providers 对称：分组键为 mapped_model，“首行”提供 provider_id/provider_name 维度字段。
func (s *Store) Models(filter Filter) ([]AggregateRow, error) {
	return s.aggregateSQL(filter, "r.mapped_model", "r.mapped_model")
}

// aggregateSQL 执行 buildAggregateQuery 生成的维度分组聚合查询并扫描为 []AggregateRow。
// UsageCoverage 与 AverageDurationMS 在 Go 中用 withUsage/durationTotal 除以 total 计算，
// 保留旧实现的浮点语义；total 为 0 时两者保持零值。空数据集返回非 nil 空切片（JSON []）。
func (s *Store) aggregateSQL(filter Filter, groupColumn, nameColumn string) ([]AggregateRow, error) {
	query, args := buildAggregateQuery(filter, groupColumn, nameColumn)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AggregateRow, 0)
	for rows.Next() {
		var row AggregateRow
		var groupKey string
		var withUsage, durationTotal int64
		if err := rows.Scan(
			&groupKey,
			&row.ProviderID,
			&row.ProviderName,
			&row.MappedModel,
			&row.Name,
			&row.TotalRequests,
			&row.FailedRequests,
			&row.TokenConsumptionTotal,
			&withUsage,
			&durationTotal,
		); err != nil {
			return nil, err
		}
		if row.TotalRequests > 0 {
			row.UsageCoverage = float64(withUsage) / float64(row.TotalRequests)
			row.AverageDurationMS = float64(durationTotal) / float64(row.TotalRequests)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Coverage 在 scoped SQL 数据集上用 GROUP BY (provider_name, provider_api_url,
// mapped_model, source_entrypoint) 聚合计算覆盖率分组统计，不再物化全宽请求行或在 Go
// 中逐行汇总。分组键、筛选、去重、口径由 buildScopedCTE 统一下推；聚合只投影分组键与
// 0/1 判定所需字段（不读取 user_agent/backend_url/token 计数/duration 等宽字段，R4）。
// 输出边界 URL 脱敏（R5）在 Go 中完成：SQL 按原始 provider_api_url 分组后，Go 将脱敏
// 后键相同的分组合并（历史脏数据中仅 userinfo/敏感 query 不同的 URL 脱敏后同组，与
// 旧算法先脱敏再分组的语义逐字段一致），再汇总计数、解析状态分布与 last_seen 代表行。
// last_seen 由 ROW_NUMBER 按“整秒 epoch + 9 位小数秒 + 原始 started_at 字符串 +
// request_id”四级降序选取，复现旧算法按 started_at DESC, id DESC 迭代且严格 After 保留
// 首个最大值的语义（含非法时间戳落入 Go 零值时间）。top_usage_parse_status 由 SQL 返回
// 每组每状态计数、Go 用 topStatus 决胜（同数取字典序最小）。排序主键 LastSeenAt 降序
// 与旧算法一致，同时间分组由分组键升序决胜（旧不稳定排序下未定义，R1 允许确定化）。
// 空数据集返回非 nil 空切片（JSON []）。逐字段兼容由 legacyOracleCoverage 差分测试保证。
func (s *Store) Coverage(filter Filter) ([]CoverageRow, error) {
	summarySQL, statusSQL, args := buildCoverageQueries(filter)

	// M-2：summary 与 status 两条查询必须在同一只读事务内执行（事务结束回滚，只发
	// SELECT、不升级写锁、不阻塞并发 writer）。否则 WAL 下两次独立 db.Query 各自取得
	// 不同 reader snapshot：写入恰在两次查询之间提交时，Total/WithoutUsage 分子分母
	// 来自旧快照、usage_parse_status 状态分布混入新快照行（或新分组的解析状态被
	// groups[key] 查找静默丢弃），返回结构不对应同一筛选结果。同一事务内 SQLite 为
	// 所有语句共享同一 reader snapshot；快照在事务开始时建立，事务结束（Rollback）
	// 即释放，不跨调用泄漏。由 TestCoverageSummaryStatusShareSnapshotUnderConcurrent
	// WALWrite 锁定。
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	summaryRows, err := tx.Query(summarySQL, args...)
	if err != nil {
		return nil, err
	}
	defer summaryRows.Close()

	groups := make(map[string]*coverageMergeGroup)
	for summaryRows.Next() {
		var providerName, providerAPIURL, mappedModel, sourceEntrypoint string
		var total, failed, withUsage int64
		var lastSeenRaw, lastSeenID string
		if err := summaryRows.Scan(
			&providerName, &providerAPIURL, &mappedModel, &sourceEntrypoint,
			&total, &failed, &withUsage, &lastSeenRaw, &lastSeenID,
		); err != nil {
			return nil, err
		}
		key := coverageSortKey(CoverageRow{
			ProviderName:     providerName,
			ProviderAPIURL:   RedactURL(providerAPIURL),
			MappedModel:      mappedModel,
			SourceEntrypoint: sourceEntrypoint,
		})
		group := groups[key]
		if group == nil {
			group = &coverageMergeGroup{
				row: CoverageRow{
					ProviderName:     providerName,
					ProviderAPIURL:   RedactURL(providerAPIURL),
					MappedModel:      mappedModel,
					SourceEntrypoint: sourceEntrypoint,
				},
				parseStatuses: make(map[string]int64),
			}
			groups[key] = group
		}
		group.row.TotalRequests += total
		group.row.ErrorRequests += failed
		group.row.SuccessRequests += total - failed
		group.row.WithUsageRequests += withUsage
		group.row.WithoutUsageRequests += total - withUsage
		lastSeen := parseTime(lastSeenRaw)
		if group.lastSeenID == "" || coverageLastSeenAfter(lastSeen, lastSeenRaw, lastSeenID, group.row.LastSeenAt, group.lastSeenRaw, group.lastSeenID) {
			group.row.LastSeenAt = lastSeen
			group.lastSeenRaw = lastSeenRaw
			group.lastSeenID = lastSeenID
		}
	}
	if err := summaryRows.Err(); err != nil {
		return nil, err
	}

	statusRows, err := tx.Query(statusSQL, args...)
	if err != nil {
		return nil, err
	}
	defer statusRows.Close()
	for statusRows.Next() {
		var providerName, providerAPIURL, mappedModel, sourceEntrypoint, status string
		var count int64
		if err := statusRows.Scan(&providerName, &providerAPIURL, &mappedModel, &sourceEntrypoint, &status, &count); err != nil {
			return nil, err
		}
		key := coverageSortKey(CoverageRow{
			ProviderName:     providerName,
			ProviderAPIURL:   RedactURL(providerAPIURL),
			MappedModel:      mappedModel,
			SourceEntrypoint: sourceEntrypoint,
		})
		if group, ok := groups[key]; ok {
			group.parseStatuses[status] += count
		}
	}
	if err := statusRows.Err(); err != nil {
		return nil, err
	}

	out := make([]CoverageRow, 0, len(groups))
	for _, group := range groups {
		row := group.row
		if row.TotalRequests > 0 {
			row.UsageCoverage = float64(row.WithUsageRequests) / float64(row.TotalRequests)
		}
		row.TopUsageParseStatus = topStatus(group.parseStatuses)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeenAt.Equal(out[j].LastSeenAt) {
			return out[i].LastSeenAt.After(out[j].LastSeenAt)
		}
		return coverageSortKey(out[i]) < coverageSortKey(out[j])
	})
	return out, nil
}

func filterWhere(filter Filter) (string, []any) {
	var parts []string
	var args []any
	add := func(sql string, arg any) {
		parts = append(parts, sql)
		args = append(args, arg)
	}
	if !filter.From.IsZero() {
		add("r.started_at >= ?", formatTime(filter.From))
	}
	if !filter.To.IsZero() {
		add("r.started_at < ?", formatTime(filter.To))
	}
	if filter.SourceApp != "" && filter.SourceApp != "all" {
		add("r.source_app = ?", filter.SourceApp)
	}
	if filter.SourceEntrypoint != "" && filter.SourceEntrypoint != "all" {
		add("r.source_entrypoint = ?", filter.SourceEntrypoint)
	}
	if filter.ProviderID != "" && filter.ProviderID != "all" {
		add("r.provider_id = ?", filter.ProviderID)
	}
	if filter.Model != "" && filter.Model != "all" {
		add("r.mapped_model = ?", filter.Model)
	}
	if filter.RequestPath != "" && filter.RequestPath != "all" {
		add("r.request_path = ?", filter.RequestPath)
	}
	if filter.UsageSource != "" && filter.UsageSource != "all" {
		add("t.usage_source = ?", filter.UsageSource)
	}
	if filter.UsageParseStatus != "" && filter.UsageParseStatus != "all" {
		add("t.usage_parse_status = ?", filter.UsageParseStatus)
	}
	switch filter.Status {
	case "success":
		parts = append(parts, "(r.error_type = '' AND r.status_code >= 200 AND r.status_code < 300)")
	case "error":
		parts = append(parts, "(r.error_type != '' OR r.status_code IS NULL OR r.status_code < 200 OR r.status_code >= 300)")
	}
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		parts = append(parts, "(r.provider_name LIKE ? OR r.provider_api_url LIKE ? OR r.mapped_model LIKE ? OR r.id LIKE ? OR r.error_message LIKE ?)")
		args = append(args, like, like, like, like, like)
	}
	return strings.Join(parts, " AND "), args
}

// rowScanner 是 *sql.Rows 与 *sql.Row 共同满足的最小扫描接口，便于 scanRequestRow
// 在分页（多行）与单行查询间复用。
type rowScanner interface {
	Scan(dest ...any) error
}

// requestRowSelectColumns 列出 scanRequestRow 读取的 usage_requests + usage_tokens
// 字段，顺序与 scanRequestRow 的 Scan 一致。Requests 分页查询复用它，避免投影漂移。
// Summary/Trends/Providers/Models/Coverage 均已改为 scoped SQL 专用窄投影聚合：
// Coverage 只投影分组键与失败/有无 usage 判定字段（buildCoverageQueries），其余接口
// 不读取宽行字段。
const requestRowSelectColumns = `r.id, r.started_at, r.ended_at, r.duration_ms, r.upstream_response_header_ms, r.time_to_first_byte_ms,
	r.status_code, r.error_type, r.error_message, r.method, r.request_path, r.backend_url,
	r.provider_id, r.provider_name, r.provider_api_url, r.source_app, r.source_entrypoint, r.user_agent,
	r.original_model, r.mapped_model, r.stream, r.request_bytes, r.response_bytes,
	t.input_tokens, t.output_tokens, t.cache_creation_input_tokens, t.cache_read_input_tokens,
	t.usage_source, t.usage_parse_status, t.usage_parse_error`

// scanRequestRow 读取 requestRowSelectColumns 对应的核心字段并完成时间/状态/脱敏转换。
// extras 追加在核心字段之后，用于在同一次 Scan 中读取调用方附加的列（如 scoped 重复标记）；
// 不传 extras 时行为与历史聚合路径完全一致。
func scanRequestRow(scanner rowScanner, extras ...any) (RequestRow, error) {
	var row RequestRow
	var startedAt string
	var endedAt sql.NullString
	var duration, header, firstByte, status sql.NullInt64
	var stream int
	dest := []any{
		&row.ID, &startedAt, &endedAt, &duration, &header, &firstByte,
		&status, &row.ErrorType, &row.ErrorMessage, &row.Method, &row.RequestPath, &row.BackendURL,
		&row.ProviderID, &row.ProviderName, &row.ProviderAPIURL, &row.SourceApp, &row.SourceEntrypoint, &row.UserAgent,
		&row.OriginalModel, &row.MappedModel, &stream, &row.RequestBytes, &row.ResponseBytes,
		&row.InputTokens, &row.OutputTokens, &row.CacheCreationInputTokens, &row.CacheReadInputTokens,
		&row.UsageSource, &row.UsageParseStatus, &row.UsageParseError,
	}
	dest = append(dest, extras...)
	if err := scanner.Scan(dest...); err != nil {
		return RequestRow{}, err
	}
	row.StartedAt = parseTime(startedAt)
	row.EndedAt = parseOptionalTime(endedAt)
	row.DurationMS = optionalInt64(duration)
	row.UpstreamResponseHeaderMS = optionalInt64(header)
	row.TimeToFirstByteMS = optionalInt64(firstByte)
	if status.Valid {
		v := int(status.Int64)
		row.StatusCode = &v
	}
	row.Stream = stream == 1
	row.RequestID = row.ID
	// 防御历史脏数据：即使旧记录存了未脱敏的 URL（带 userinfo 或敏感 query），
	// 读取时也统一走 redact，确保 Coverage/Requests 两条输出路径都不泄露。
	row.BackendURL = RedactURL(row.BackendURL)
	row.ProviderAPIURL = RedactURL(row.ProviderAPIURL)
	return row, nil
}

type duplicateIndexKey struct {
	model                    string
	inputTokens              int64
	outputTokens             int64
	cacheCreationInputTokens int64
	cacheReadInputTokens     int64
}

func duplicateKeys(row RequestRow) []duplicateIndexKey {
	models := dedupeModels(row.MappedModel, row.OriginalModel)
	keys := make([]duplicateIndexKey, 0, len(models))
	for _, model := range models {
		keys = append(keys, duplicateIndexKey{
			model:                    model,
			inputTokens:              row.InputTokens,
			outputTokens:             row.OutputTokens,
			cacheCreationInputTokens: row.CacheCreationInputTokens,
			cacheReadInputTokens:     row.CacheReadInputTokens,
		})
	}
	return keys
}

func dedupeModels(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isProviderUsageRow(row RequestRow) bool {
	return !isSessionLogRow(row) &&
		row.SourceApp == "claude_code" &&
		row.UsageSource == UsageSourceProvider &&
		row.UsageParseStatus == ParseStatusOK
}

func isSessionLogRow(row RequestRow) bool {
	return row.UsageSource == UsageSourceSessionLog || row.SourceEntrypoint == "session_log" || row.ProviderID == "_session"
}

// coverageMergeGroup 是 Coverage 在 Go 中合并脱敏后分组键的中间状态：SQL 按原始
// provider_api_url 分组，脱敏后键相同的原始分组（历史脏数据）在此汇总计数、解析状态
// 分布与 last_seen 代表行。
type coverageMergeGroup struct {
	row           CoverageRow
	parseStatuses map[string]int64
	lastSeenRaw   string
	lastSeenID    string
}

// coverageSortKey 返回 Coverage 分组键（脱敏后的 provider_api_url 参与），与旧算法
// \x00 连接分组键的语义一致，同时用作同 LastSeenAt 分组的确定性排序决胜键。
func coverageSortKey(row CoverageRow) string {
	return strings.Join([]string{row.ProviderName, row.ProviderAPIURL, row.MappedModel, row.SourceEntrypoint}, "\x00")
}

// coverageLastSeenAfter 返回候选代表行 (candTime, candRaw, candID) 是否应替换当前代表行
// (curTime, curRaw, curID)：时间更晚者胜出；同一瞬时按原始 started_at 字符串降序、再按
// request_id 降序决胜，复现旧算法在 started_at DESC, id DESC 迭代中以严格 After 保留
// 首个最大值的行为。
func coverageLastSeenAfter(candTime time.Time, candRaw, candID string, curTime time.Time, curRaw, curID string) bool {
	if !candTime.Equal(curTime) {
		return candTime.After(curTime)
	}
	if candRaw != curRaw {
		return candRaw > curRaw
	}
	return candID > curID
}

func todayRange(filter Filter) (time.Time, time.Time, error) {
	loc, err := filterLocation(filter)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	now := filter.Now
	if now.IsZero() {
		now = time.Now()
	}
	localNow := now.In(loc)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	return start.UTC(), start.AddDate(0, 0, 1).UTC(), nil
}

func filterLocation(filter Filter) (*time.Location, error) {
	if filter.TZ == "" {
		return time.Local, nil
	}
	return time.LoadLocation(filter.TZ)
}

func isFailed(req RequestRecord) bool {
	if req.ErrorType != "" {
		return true
	}
	if req.StatusCode == nil {
		return true
	}
	return *req.StatusCode < 200 || *req.StatusCode >= 300
}

func tokenTotal(tok TokenRecord) int64 {
	return tok.InputTokens + tok.OutputTokens + tok.CacheCreationInputTokens + tok.CacheReadInputTokens
}

func hasUsage(tok TokenRecord) bool {
	return tok.UsageSource != "" && tok.UsageSource != UsageSourceNone && tok.UsageParseStatus == ParseStatusOK
}

func topStatus(counts map[string]int64) string {
	var top string
	var topCount int64
	for status, count := range counts {
		if count > topCount || count == topCount && status < top {
			top = status
			topCount = count
		}
	}
	return top
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func parseTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	t := parseTime(value.String)
	return &t
}

func optionalInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
