package usage

import (
	"strconv"
	"strings"
	"time"
)

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

// scopedTimeOrderKeyLength 是 scopedTimeOrderKeyExpr 返回的时间排序键前缀固定长度
// （20 位偏置整秒 epoch + 9 位小数秒）。与键表达式同步维护，供 MAX 编码取最晚行后
// 用 substr 剥离前缀、还原 started_at 原值。
const scopedTimeOrderKeyLength = 29

// scopedTimeOrderKeyExpr 返回 started_at 列的定长 29 字符时间排序键表达式：整秒
// epoch 偏置 Go 零值时间的 Unix 秒后恒非负（合法时戳 ≥ 1、非法时戳恰为 0），经
// printf 补零为定长 20 位，再拼接定长 9 位小数秒。键的字典序等价于“(整秒 epoch,
// 小数秒)”元组序，因此可直接用于 MAX/字符串比较复现 Go 时间大小（含非法时戳容错
// 为零值、同整秒按小数秒精确比较）。拼接 started_at 原值后取 MAX，即可在同一次
// 扫描的单个聚合里得到“时间最晚行的 started_at 原值”；同整秒同小数秒的并列行
// （同一时刻的不同字符串表示）由 started_at 字符串降序决胜，与旧差分判定器“按
// started_at DESC 迭代、严格 After 保留首个遇到的行”的并列语义一致。
func scopedTimeOrderKeyExpr(column string) string {
	return `printf('%020d', ` + scopedEpochSecondsExpr(column) + ` + 62135596800) || ` + scopedStartedAtFractionExpr(column)
}

// buildSummaryQuery 基于 buildScopedCTE 生成 Summary 的聚合查询。计数/求和/最新
// 时间全部下推到 scoped 数据集上的 SQLite 聚合，只投影聚合所需字段（不物化完整
// RequestRow，不读取/解析 URL）。last_provider_request 与主聚合共享同一次 scoped
// 扫描（R2 P0）：在同一 SELECT 内取 MAX(定长时间键 || started_at)，由 substr 剥离
// 定长 29 字符前缀得到时间最晚行的 started_at 原值，由调用方 parseTime，与旧算法
// 的 Go 时间最大值语义逐字段一致；scoped 数据集为空时 MAX 返回 NULL，对应 nil 指针。
// 旧实现的标量子查询会使整套 scoped CTE（60k 全扫 + candidate 物化 + 运行时自动
// 索引 + 末级排序）执行两次；本结构将其降为一次（计划中主表全扫/自动索引/临时
// 排序树各 1 份），由 TestSummaryQueryPlanScansScopedOnce 锁定。
// startOfToday/endOfToday 为本地今日区间转 UTC 后的整秒边界，以 Unix 秒参数传入；
// 今日判定在整秒边界上与 Go 的 start <= t < end 完全等价（含小数秒行与非法时戳行）。
func buildSummaryQuery(filter Filter, startOfToday, endOfToday time.Time) (string, []any) {
	cte, args := buildScopedCTE(filter)
	epoch := scopedEpochSecondsExpr("r.started_at")
	// todayPredicate 在主 SELECT 中出现两次（今日计数与今日 token），各消耗 start/end 两个参数。
	todayPredicate := epoch + ` >= ? AND ` + epoch + ` < ?`
	// 定长前缀（29 字符）保证 MAX 的字典序等价于时间序；substr 第 30 位起即 started_at 原值。
	lastStarted := `substr(MAX(` + scopedTimeOrderKeyExpr("r.started_at") + ` || r.started_at), ` +
		strconv.Itoa(scopedTimeOrderKeyLength+1) + `)`
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

// scopedZoneInterval 描述一个时区偏移区间：从 start（Unix 秒，含）开始偏移为 offset
// 秒；最后一个区间隐式延伸到 +∞。区间内任意整数秒 t 满足
// time.Unix(t, 0).In(loc).Zone() 的偏移 == offset。
type scopedZoneInterval struct {
	start  int64
	offset int
}

// scopedZoneOffsetIntervals 将 [minEpoch, maxEpoch] 切分为时区偏移区间。tzdata 切换
// 至少相隔数天，因此按天步进检测偏移变化，再在变化窗口内二分定位精确切换秒；区间数
// 只随切换数增长（O(切换数)），百年范围也只产生少量区间。二分前提“单日窗口内至多
// 一次切换”对真实 tzdata 恒成立。
func scopedZoneOffsetIntervals(loc *time.Location, minEpoch, maxEpoch int64) []scopedZoneInterval {
	offsetAt := func(epoch int64) int {
		_, offset := time.Unix(epoch, 0).In(loc).Zone()
		return offset
	}
	intervals := []scopedZoneInterval{{start: minEpoch, offset: offsetAt(minEpoch)}}
	for lower := minEpoch; lower < maxEpoch; {
		upper := lower + 86400
		if upper > maxEpoch {
			upper = maxEpoch
		}
		current := intervals[len(intervals)-1].offset
		if offsetAt(upper) != current {
			// 在 (lower, upper] 二分首个偏移不等于 current 的秒。
			lo, hi := lower+1, upper
			for lo < hi {
				mid := lo + (hi-lo)/2
				if offsetAt(mid) == current {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			intervals = append(intervals, scopedZoneInterval{start: lo, offset: offsetAt(lo)})
		}
		lower = upper
	}
	return intervals
}

// scopedTrendsBucketExpr 返回将 started_at 列映射为本地日期桶（YYYY-MM-DD）的 SQL CASE
// 表达式及其绑定参数。非法时间戳（strftime 返回 NULL）落入 Go 零值时间的本地日期标签
// （与 parseTime 容错后 StartedAt.In(loc).Format("2006-01-02") 一致）；有效时间戳按偏移
// 区间用 strftime('%Y-%m-%d', epoch+offset, 'unixepoch') 换算，逐秒等价于旧算法的本地
// 日期（含夏令时、半小时/45 分钟偏移与负偏移）。参数顺序：零值日期标签，随后每个后续
// 区间边界与前一区间偏移成对出现，最后是 ELSE 分支的末区间偏移。
func scopedTrendsBucketExpr(column string, loc *time.Location, intervals []scopedZoneInterval) (string, []any) {
	epoch := `CAST(strftime('%s', ` + column + `) AS INTEGER)`
	localDate := `strftime('%Y-%m-%d', ` + epoch + ` + ?, 'unixepoch')`
	var b strings.Builder
	var args []any
	b.WriteString("CASE\n")
	b.WriteString("\tWHEN strftime('%s', " + column + ") IS NULL THEN ?\n")
	args = append(args, time.Time{}.In(loc).Format("2006-01-02"))
	for i := 1; i < len(intervals); i++ {
		b.WriteString("\tWHEN " + epoch + " < ? THEN " + localDate + "\n")
		args = append(args, intervals[i].start, intervals[i-1].offset)
	}
	b.WriteString("\tELSE " + localDate + "\nEND")
	args = append(args, intervals[len(intervals)-1].offset)
	return b.String(), args
}

// buildTrendsRangeQuery 返回 scoped 数据集上有效时间戳的最小/最大整秒查询，用于推导
// 时区偏移区间。MIN/MAX 忽略 NULL，因此全部时间戳非法时返回 NULL/NULL；数据集为空时
// 同样返回 NULL/NULL，调用方据此退化为单一占位区间。
func buildTrendsRangeQuery(filter Filter) (string, []any) {
	cte, args := buildScopedCTE(filter)
	query := cte + `
	SELECT
		MIN(CAST(strftime('%s', r.started_at) AS INTEGER)),
		MAX(CAST(strftime('%s', r.started_at) AS INTEGER))
	FROM scoped
	JOIN usage_requests r ON r.id = scoped.request_id`
	return query, args
}

// buildTrendsQuery 基于 buildScopedCTE 与时区偏移区间生成 Trends 的日桶聚合查询。
// 内层子查询只投影桶表达式与聚合所需的 0/1 判定列和 token 列（不物化完整 RequestRow，
// 不读取/解析 URL）；外层 GROUP BY bucket 将 COUNT/SUM 下推到 SQLite，ORDER BY bucket ASC
// 与旧算法的桶字符串升序一致，缺失桶自然不出现。UsageCoverage 由调用方用
// withUsage/total 在 Go 中相除，保留旧实现的浮点语义。两次查询（区间+聚合）之间若有
// 并发写入，新行落入 ELSE 末区间偏移桶，属可忽略的瞬时偏差。
func buildTrendsQuery(filter Filter, loc *time.Location, intervals []scopedZoneInterval) (string, []any) {
	cte, args := buildScopedCTE(filter)
	bucketExpr, bucketArgs := scopedTrendsBucketExpr("r.started_at", loc, intervals)
	args = append(args, bucketArgs...)
	query := cte + `
SELECT
	bucket,
	COUNT(*),
	COALESCE(SUM(has_usage), 0),
	COALESCE(SUM(CASE WHEN has_usage = 1 THEN input_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN has_usage = 1 THEN output_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN has_usage = 1 THEN cache_creation_input_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN has_usage = 1 THEN cache_read_input_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN has_usage = 1 THEN token_total ELSE 0 END), 0),
	COALESCE(SUM(is_failed), 0)
FROM (
	SELECT
		` + bucketExpr + ` AS bucket,
		CASE WHEN ` + scopedHasUsagePredicate + ` THEN 1 ELSE 0 END AS has_usage,
		CASE WHEN ` + scopedIsFailedPredicate + ` THEN 1 ELSE 0 END AS is_failed,
		t.input_tokens AS input_tokens,
		t.output_tokens AS output_tokens,
		t.cache_creation_input_tokens AS cache_creation_input_tokens,
		t.cache_read_input_tokens AS cache_read_input_tokens,
		` + scopedTokenSumExpr + ` AS token_total
	FROM scoped
	JOIN usage_requests r ON r.id = scoped.request_id
	JOIN usage_tokens t ON t.request_id = r.id
)
GROUP BY bucket
ORDER BY bucket ASC`
	return query, args
}

// buildAggregateQuery 基于 buildScopedCTE 生成 Providers/Models 的维度分组聚合查询。
// groupColumn/nameColumn 是内部分组键与名称列（Providers 为 r.provider_id/r.provider_name，
// Models 为 r.mapped_model/r.mapped_model），非用户输入，直接拼接而非参数化。
//
// 内层子查询只投影分组键、“首行”维度字段与聚合所需的 0/1 判定列和 token/duration 列
// （不物化完整 RequestRow，不读取/解析 URL，R5）；ROW_NUMBER() 按与旧 queryRows 一致的
// “scoped.started_at DESC, scoped.request_id DESC” 字符串序在组内编号（同整秒内 'Z' 整秒行
// 排在 '.5Z' 小数秒行之前），外层 GROUP BY 用 MAX(CASE WHEN rn=1 ...) 取编号 1 行的
// provider_id/provider_name/mapped_model/name，复现旧算法“首个遇到的行（最新行）决定维度
// 字段”语义。COUNT/SUM 将总请求、失败请求、token 总和、有 usage 计数与 duration 总和下推到
// SQLite；duration 用 COALESCE(r.duration_ms, 0) 复现旧实现“NULL 不计入”的求和语义。
// UsageCoverage 与 AverageDurationMS 由调用方用 withUsage/durationTotal 与 total 在 Go 中
// 相除，保留旧实现的浮点语义与 total 为 0 时两者为 0 的空值行为。
//
// 排序为 total_requests DESC, group_key ASC：主排序键与旧算法一致，分组同数由分组键升序
// 决胜（旧不稳定排序下同数组顺序未定义，R1 明确允许将其确定化）。筛选、去重、口径由
// buildScopedCTE 统一下推，逐字段兼容由 legacyOracleAggregate 差分测试保证。
func buildAggregateQuery(filter Filter, groupColumn, nameColumn string) (string, []any) {
	cte, args := buildScopedCTE(filter)
	query := cte + `
SELECT
	group_key,
	MAX(CASE WHEN rn = 1 THEN provider_id END),
	MAX(CASE WHEN rn = 1 THEN provider_name END),
	MAX(CASE WHEN rn = 1 THEN mapped_model END),
	MAX(CASE WHEN rn = 1 THEN name_value END),
	COUNT(*) AS total_requests,
	COALESCE(SUM(is_failed), 0),
	COALESCE(SUM(token_total), 0),
	COALESCE(SUM(has_usage), 0),
	COALESCE(SUM(duration_ms), 0)
FROM (
	SELECT
		` + groupColumn + ` AS group_key,
		` + nameColumn + ` AS name_value,
		r.provider_id AS provider_id,
		r.provider_name AS provider_name,
		r.mapped_model AS mapped_model,
		ROW_NUMBER() OVER (
			PARTITION BY ` + groupColumn + `
			ORDER BY scoped.started_at DESC, scoped.request_id DESC
		) AS rn,
		CASE WHEN ` + scopedIsFailedPredicate + ` THEN 1 ELSE 0 END AS is_failed,
		CASE WHEN ` + scopedHasUsagePredicate + ` THEN 1 ELSE 0 END AS has_usage,
		CASE WHEN ` + scopedHasUsagePredicate + ` THEN ` + scopedTokenSumExpr + ` ELSE 0 END AS token_total,
		COALESCE(r.duration_ms, 0) AS duration_ms
	FROM scoped
	JOIN usage_requests r ON r.id = scoped.request_id
	JOIN usage_tokens t ON t.request_id = r.id
)
GROUP BY group_key
ORDER BY total_requests DESC, group_key ASC`
	return query, args
}

// buildCoverageQueries 基于 buildScopedCTE 生成 Coverage 的分组聚合查询对。两条查询
// 共享同一份筛选+口径参数：summarySQL 按原始 (provider_name, provider_api_url,
// mapped_model, source_entrypoint) GROUP BY，返回每组总数/失败数/有 usage 数与
// last_seen 代表行的 started_at 原值及 request_id；statusSQL 只在无 usage 行上按
// 分组键 + usage_parse_status GROUP BY，返回每状态计数（top_usage_parse_status 的
// 同数决胜由调用方用 topStatus 在 Go 中完成）。
//
// 内层子查询只投影分组键、last_seen 选取与 0/1 判定所需字段（不物化完整 RequestRow，
// 不读取 user_agent/backend_url/token 计数/duration 等宽字段，R4）；ROW_NUMBER() 按
// “整秒 epoch DESC + 9 位小数秒 DESC + started_at 字符串 DESC + request_id DESC”在组内
// 编号，rn=1 即旧算法按 started_at DESC, id DESC 迭代且严格 After 保留的 last_seen 行
// （非法时间戳落入 Go 零值时间，与 parseTime 容错一致）。输出边界 URL 脱敏与脱敏后
// 键相同的分组合并在调用方 Go 中完成（R5）：历史脏数据中仅 userinfo/敏感 query 不同
// 的 URL 脱敏后同组，与旧算法先脱敏再分组的语义逐字段一致。筛选、去重、口径由
// buildScopedCTE 统一下推，逐字段兼容由 legacyOracleCoverage 差分测试保证。
func buildCoverageQueries(filter Filter) (summarySQL, statusSQL string, args []any) {
	cte, args := buildScopedCTE(filter)
	summarySQL = cte + `
SELECT
	provider_name,
	provider_api_url,
	mapped_model,
	source_entrypoint,
	COUNT(*),
	COALESCE(SUM(is_failed), 0),
	COALESCE(SUM(has_usage), 0),
	MAX(CASE WHEN rn = 1 THEN started_at END),
	MAX(CASE WHEN rn = 1 THEN request_id END)
FROM (
	SELECT
		r.provider_name AS provider_name,
		r.provider_api_url AS provider_api_url,
		r.mapped_model AS mapped_model,
		r.source_entrypoint AS source_entrypoint,
		r.started_at AS started_at,
		scoped.request_id AS request_id,
		ROW_NUMBER() OVER (
			PARTITION BY r.provider_name, r.provider_api_url, r.mapped_model, r.source_entrypoint
			ORDER BY ` + scopedEpochSecondsExpr("r.started_at") + ` DESC, ` + scopedStartedAtFractionExpr("r.started_at") + ` DESC, r.started_at DESC, scoped.request_id DESC
		) AS rn,
		CASE WHEN ` + scopedIsFailedPredicate + ` THEN 1 ELSE 0 END AS is_failed,
		CASE WHEN ` + scopedHasUsagePredicate + ` THEN 1 ELSE 0 END AS has_usage
	FROM scoped
	JOIN usage_requests r ON r.id = scoped.request_id
	JOIN usage_tokens t ON t.request_id = r.id
)
GROUP BY provider_name, provider_api_url, mapped_model, source_entrypoint`

	statusSQL = cte + `
SELECT
	r.provider_name,
	r.provider_api_url,
	r.mapped_model,
	r.source_entrypoint,
	t.usage_parse_status,
	COUNT(*)
FROM scoped
JOIN usage_requests r ON r.id = scoped.request_id
JOIN usage_tokens t ON t.request_id = r.id
WHERE NOT (` + scopedHasUsagePredicate + `)
GROUP BY r.provider_name, r.provider_api_url, r.mapped_model, r.source_entrypoint, t.usage_parse_status`
	return summarySQL, statusSQL, args
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
