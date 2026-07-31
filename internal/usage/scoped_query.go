package usage

import "time"

const scopedSessionRowPredicate = `(
	t.usage_source = 'session_log'
	OR r.source_entrypoint = 'session_log'
	OR r.provider_id = '_session'
)`

// scopedEpochSecondsExpr 返回将 started_at 列解析为 Unix 秒的 SQL 表达式。
// 非法历史时间戳 strftime 返回 NULL，COALESCE 到 Go 零值时间（0001-01-01T00:00:00Z）
// 的 Unix 秒，与 parseTime 的容错语义保持一致；小数秒被向零截断为整秒。
func scopedEpochSecondsExpr(column string) string {
	return `COALESCE(
	CAST(strftime('%s', ` + column + `) AS INTEGER),
	-62135596800
)`
}

// scopedStartedAtFractionExpr 返回提取 started_at 列小数秒（定长 9 位字符串）的
// SQL 表达式，与整秒 epoch 组合即可在 SQLite 中按时间顺序稳定比较 RFC3339Nano 时戳
// （字符串直接比较会在同整秒内误判小数秒与 'Z' 的顺序）。非法/无小数秒返回全零。
func scopedStartedAtFractionExpr(column string) string {
	return `CASE
	WHEN strftime('%s', ` + column + `) IS NULL THEN '000000000'
	WHEN instr(` + column + `, '.') = 0 THEN '000000000'
	WHEN substr(` + column + `, -1) = 'Z' THEN
		substr(
			substr(
				` + column + `,
				instr(` + column + `, '.') + 1,
				length(` + column + `) - instr(` + column + `, '.') - 1
			) || '000000000',
			1,
			9
		)
	ELSE
		substr(
			substr(
				` + column + `,
				instr(` + column + `, '.') + 1,
				length(` + column + `) - instr(` + column + `, '.') - 6
			) || '000000000',
			1,
			9
		)
END`
}

// scopedHasUsagePredicate 是 hasUsage 的 SQL 等价形式：usage_source 非空且非 'none'、
// usage_parse_status = 'ok'。只在 Summary 聚合中使用，与 Go 判定逐字段一致。
const scopedHasUsagePredicate = `t.usage_source <> '' AND t.usage_source <> 'none' AND t.usage_parse_status = 'ok'`

// scopedIsFailedPredicate 是 isFailed 的 SQL 等价形式：error_type 非空、状态码 NULL
// 或非 2xx。NULL 状态码必须显式 IS NULL（SQL 中 NULL < 200 为 NULL 而非真）。
const scopedIsFailedPredicate = `r.error_type <> '' OR r.status_code IS NULL OR r.status_code < 200 OR r.status_code >= 300`

// scopedTokenSumExpr 是 tokenTotal 的 SQL 等价形式。四类 token 均为 NOT NULL，
// 整数加法与 Go int64 求和逐值一致。
const scopedTokenSumExpr = `t.input_tokens + t.output_tokens + t.cache_creation_input_tokens + t.cache_read_input_tokens`

// buildRequestsQueries 基于 buildScopedCTE 生成 Requests 的总数与分页查询。
// 返回的 args 只含筛选+口径参数；调用方在执行 pageSQL 前追加 LIMIT/OFFSET 两个
// 参数。countSQL 对 scoped 数据集 COUNT(*)，pageSQL 回连 usage_requests/usage_tokens
// 投影完整 RequestRow（含 scoped.dedupe_status/dedupe_request_id），保证总数、排序、
// 筛选、口径与重复标记与旧算法逐字段兼容。
func buildRequestsQueries(filter Filter) (countSQL, pageSQL string, args []any) {
	cte, args := buildScopedCTE(filter)
	countSQL = cte + "\n\tSELECT COUNT(*) FROM scoped"
	pageSQL = cte + `
	SELECT ` + requestRowSelectColumns + `, scoped.dedupe_status, scoped.dedupe_request_id
	FROM scoped
	JOIN usage_requests r ON r.id = scoped.request_id
	JOIN usage_tokens t ON t.request_id = r.id
	ORDER BY scoped.started_at DESC, scoped.request_id DESC
	LIMIT ? OFFSET ?`
	return countSQL, pageSQL, args
}

// buildSummaryQuery 基于 buildScopedCTE 生成 Summary 的聚合查询。计数/求和/最新
// 时间全部下推到 scoped 数据集上的 SQLite 聚合，只投影聚合所需字段（不物化完整
// RequestRow，不读取/解析 URL）。last_provider_request 用标量子查询按“整秒 epoch +
// 9 位小数秒”两级排序取时间最晚的 started_at 原值，由调用方 parseTime，与旧算法
// 的 Go 时间最大值语义逐字段一致；scoped 数据集为空时子查询返回 NULL，对应 nil 指针。
// startOfToday/endOfToday 为本地今日区间转 UTC 后的整秒边界，以 Unix 秒参数传入；
// 今日判定在整秒边界上与 Go 的 start <= t < end 完全等价（含小数秒行与非法时戳行）。
func buildSummaryQuery(filter Filter, startOfToday, endOfToday time.Time) (string, []any) {
	cte, args := buildScopedCTE(filter)
	epoch := scopedEpochSecondsExpr("r.started_at")
	// todayPredicate 在主 SELECT 中出现两次（今日计数与今日 token），各消耗 start/end 两个参数。
	todayPredicate := epoch + ` >= ? AND ` + epoch + ` < ?`
	lastStarted := `(
		SELECT r2.started_at
		FROM scoped sc2
		JOIN usage_requests r2 ON r2.id = sc2.request_id
		ORDER BY ` + scopedEpochSecondsExpr("r2.started_at") + ` DESC, ` + scopedStartedAtFractionExpr("r2.started_at") + ` DESC
		LIMIT 1
	)`
	query := cte + `
	SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN ` + scopedHasUsagePredicate + ` THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN ` + scopedHasUsagePredicate + ` THEN ` + scopedTokenSumExpr + ` ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN ` + scopedIsFailedPredicate + ` THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN ` + todayPredicate + ` THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN ` + todayPredicate + ` AND ` + scopedHasUsagePredicate + ` THEN ` + scopedTokenSumExpr + ` ELSE 0 END), 0),
		` + lastStarted + `
	FROM scoped
	JOIN usage_requests r ON r.id = scoped.request_id
	JOIN usage_tokens t ON t.request_id = r.id`
	args = append(args, startOfToday.Unix(), endOfToday.Unix(), startOfToday.Unix(), endOfToday.Unix())
	return query, args
}

// buildScopedCTE returns the common filtered/candidate/scoped datasets used by
// SQL-backed usage reads. Callers append their own projection, aggregation,
// ordering, and pagination and pass the returned arguments unchanged.
func buildScopedCTE(filter Filter) (string, []any) {
	where, args := filterWhere(filter)
	filteredWhere := ""
	if where != "" {
		filteredWhere = "\n\tWHERE " + where
	}

	scope := filter.StatsScope
	if scope == "" {
		scope = StatsScopeEffective
	}
	args = append(args, scope)

	return `WITH filtered AS (
	SELECT
		r.id AS request_id,
		r.started_at,
		CASE WHEN ` + scopedSessionRowPredicate + ` THEN 1 ELSE 0 END AS is_session_log,
		CASE
			WHEN t.usage_parse_status = 'ok'
				AND ` + scopedSessionRowPredicate + `
			THEN 1
			ELSE 0
		END AS is_dedupe_session,
		CASE
			WHEN NOT ` + scopedSessionRowPredicate + `
				AND r.source_app = 'claude_code'
				AND t.usage_source = 'provider'
				AND t.usage_parse_status = 'ok'
			THEN 1
			ELSE 0
		END AS is_provider_usage
	FROM usage_requests r
	JOIN usage_tokens t ON t.request_id = r.id` + filteredWhere + `
),
candidate AS (
	SELECT
		d.session_request_id,
		d.provider_request_id,
		ROW_NUMBER() OVER (
			PARTITION BY d.session_request_id
			ORDER BY
				d.model_priority ASC,
				` + scopedEpochSecondsExpr("provider.started_at") + ` ASC,
				` + scopedStartedAtFractionExpr("provider.started_at") + ` ASC,
				provider.request_id ASC
		) AS candidate_rank
	FROM usage_dedupe_candidates d
	JOIN filtered session
		ON session.request_id = d.session_request_id
		AND session.is_dedupe_session = 1
	JOIN filtered provider
		ON provider.request_id = d.provider_request_id
		AND provider.is_provider_usage = 1
),
scoped AS (
	SELECT
		filtered.request_id,
		filtered.started_at,
		filtered.is_session_log,
		CASE
			WHEN candidate.provider_request_id IS NOT NULL THEN 'duplicate'
			ELSE ''
		END AS dedupe_status,
		COALESCE(candidate.provider_request_id, '') AS dedupe_request_id
	FROM filtered
	LEFT JOIN candidate
		ON candidate.session_request_id = filtered.request_id
		AND candidate.candidate_rank = 1
	WHERE CASE ?
		WHEN 'raw' THEN 1
		WHEN 'provider' THEN filtered.is_session_log = 0
		WHEN 'session_log' THEN filtered.is_session_log = 1
		ELSE (
			filtered.is_session_log = 0
			OR candidate.provider_request_id IS NULL
		)
	END
)`, args
}
