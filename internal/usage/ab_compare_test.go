package usage

// 本文件实现“同机同会话 A/B 对照”测量：在同一进程、同一 60,332 行数据集上，交替测量
// 新旧两套查询结构（R1：含/不含索引；R2：Summary 单次扫描；R3：candidate 排名持久化；
// R4：CASE 惰性 candidate + scope 裁剪；R5：返工前 733461a 全套查询 vs HEAD 累计收益），
// 以消除跨次运行的机器负载漂移，把基准差异归因到查询结构本身。仅由 MCC_USAGE_AB=1
// 门控，CI 默认不执行；不修改 schema/查询语义，DROP/CREATE 仅作用于临时基准库。

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestUsageIndexABCompare 在同一数据集上交替测量 with-index / without-index 配置并打印对照表。
func TestUsageIndexABCompare(t *testing.T) {
	if !explainEnabled() || os.Getenv("MCC_USAGE_AB") == "" {
		t.Skip("set MCC_USAGE_EXPLAIN=1 and MCC_USAGE_AB=1 to run the A/B index comparison")
	}
	n := benchRows(t)
	if n == 0 {
		n = 60332
	}
	ds := newBenchDatasetInDir(t, t.TempDir(), n)
	ds.runMigration(t)

	const idx = "idx_usage_dedupe_candidates_session_priority"

	dropIndex := func() {
		if _, err := ds.db.Exec(`DROP INDEX IF EXISTS ` + idx); err != nil {
			t.Fatalf("drop index: %v", err)
		}
	}
	createIndex := func() {
		if _, err := ds.db.Exec(`CREATE INDEX IF NOT EXISTS ` + idx +
			` ON usage_dedupe_candidates(session_request_id, model_priority, provider_request_id)`); err != nil {
			t.Fatalf("create index: %v", err)
		}
	}

	// 先丢弃一次预热 profile，消除纯 Go SQLite 首次冷启动对首个样本的抬高；
	// 随后交替测量 with/without 各两次，取各自中位比较，压低会话内漂移。
	_ = ds.profile(t) // warmup, discarded

	withIdx := make([]benchProfile, 0, 2)
	noIdx := make([]benchProfile, 0, 2)
	for i := 0; i < 2; i++ {
		createIndex()
		withIdx = append(withIdx, ds.profile(t))
		dropIndex()
		noIdx = append(noIdx, ds.profile(t))
	}
	createIndex() // 恢复含索引状态

	med := func(a, b time.Duration) time.Duration {
		if a < b {
			return a
		}
		return b
	}
	withSummary := med(withIdx[0].StatusSummary, withIdx[1].StatusSummary)
	noSummary := med(noIdx[0].StatusSummary, noIdx[1].StatusSummary)

	t.Logf("\n==== A/B index comparison (rows=%d, same session, warmup discarded) ====", n)
	t.Logf("%-28s %14s %14s %10s", "metric", "with-index", "no-index", "speedup")
	logRow := func(name string, w1, w2, n1, n2 time.Duration) {
		w := med(w1, w2)
		no := med(n1, n2)
		speedup := float64(no) / float64(w)
		t.Logf("%-28s %14s %14s %9.2fx", name, w, no, speedup)
	}
	logRow("Summary/status", withIdx[0].StatusSummary, withIdx[1].StatusSummary, noIdx[0].StatusSummary, noIdx[1].StatusSummary)
	logRow("Requests page 50", withIdx[0].RequestsPage50, withIdx[1].RequestsPage50, noIdx[0].RequestsPage50, noIdx[1].RequestsPage50)
	logRow("Trends", withIdx[0].Trends, withIdx[1].Trends, noIdx[0].Trends, noIdx[1].Trends)
	logRow("Providers", withIdx[0].Providers, withIdx[1].Providers, noIdx[0].Providers, noIdx[1].Providers)
	logRow("Models", withIdx[0].Models, withIdx[1].Models, noIdx[0].Models, noIdx[1].Models)
	logRow("Coverage", withIdx[0].Coverage, withIdx[1].Coverage, noIdx[0].Coverage, noIdx[1].Coverage)
	logRow("six parallel wall", withIdx[0].SixParallelWall, withIdx[1].SixParallelWall, noIdx[0].SixParallelWall, noIdx[1].SixParallelWall)
	t.Logf("with-index samples (Summary): %s, %s", withIdx[0].StatusSummary, withIdx[1].StatusSummary)
	t.Logf("no-index samples  (Summary): %s, %s", noIdx[0].StatusSummary, noIdx[1].StatusSummary)
	t.Logf("drift check with vs no Summary median: %s vs %s", withSummary, noSummary)
	t.Logf("==================================================================")
}

// r2SummaryScalars 是 Summary 聚合查询的 7 列原始扫描结果，供 R2 A/B 逐字段比对
// 与计时使用（不经过 Store.Summary，避免 Go 汇总代码干扰纯 SQL 结构差异的墙钟归因）。
type r2SummaryScalars struct {
	total       int64
	withUsage   int64
	tokens      int64
	failed      int64
	todayReqs   int64
	todayTokens int64
	lastStarted sql.NullString
}

// r2LegacyScalarSummaryQuery 重建 R2 之前的 Summary 查询（test-only 性能对照体）：
// last_provider_request 用标量子查询按 epoch/fraction 排序取最晚行，使整套 scoped
// CTE（60k 全扫 + candidate 物化 + 自动索引 + 末级排序）执行两次。与 R1 之前的
// buildSummaryQuery 逐字一致，仅用于同会话 A/B 归因。
func r2LegacyScalarSummaryQuery(filter Filter, startOfToday, endOfToday time.Time) (string, []any) {
	cte, args := buildScopedCTE(filter, true)
	epoch := scopedEpochSecondsExpr("r.started_at")
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

// TestUsageSummaryR2ABCompare 在同一进程、同一 60,332 行数据集上交替测量 R2 前后
// 两套 Summary 查询结构（标量子查询二次物化 vs 单次扫描 MAX 编码）的延迟与六并发
// 墙钟，以消除跨次运行的机器负载漂移，把加速比归因到查询结构本身。先逐字段断言
// 两套查询结果完全一致（差分兼容的附加实数据证明），再交替各取 3 轮样本（轮内顺序
// 交替以抵消趋势性漂移），取各自中位比较。仅由 MCC_USAGE_EXPLAIN=1 + MCC_USAGE_AB=1
// 门控，CI 默认不执行。
func TestUsageSummaryR2ABCompare(t *testing.T) {
	if !explainEnabled() || os.Getenv("MCC_USAGE_AB") == "" {
		t.Skip("set MCC_USAGE_EXPLAIN=1 and MCC_USAGE_AB=1 to run the R2 Summary A/B comparison")
	}
	n := benchRows(t)
	if n == 0 {
		n = 60332
	}
	ds := newBenchDatasetInDir(t, t.TempDir(), n)
	ds.runMigration(t)

	filter := benchFilter()
	startOfToday, endOfToday, err := todayRange(filter)
	if err != nil {
		t.Fatalf("today range: %v", err)
	}
	oldSQL, oldArgs := r2LegacyScalarSummaryQuery(filter, startOfToday, endOfToday)
	newSQL, newArgs := buildSummaryQuery(filter, startOfToday, endOfToday)

	scan := func(query string, args []any) r2SummaryScalars {
		var s r2SummaryScalars
		if err := ds.db.QueryRow(query, args...).Scan(
			&s.total, &s.withUsage, &s.tokens, &s.failed, &s.todayReqs, &s.todayTokens, &s.lastStarted,
		); err != nil {
			t.Fatalf("scan summary: %v", err)
		}
		return s
	}

	// 逐字段等价：两套查询在 60k 基准数据上必须返回完全相同的聚合结果。
	oldResult, newResult := scan(oldSQL, oldArgs), scan(newSQL, newArgs)
	if !reflect.DeepEqual(oldResult, newResult) {
		t.Fatalf("R2 query result differs from legacy scalar-subquery result:\nold = %#v\nnew = %#v", oldResult, newResult)
	}

	runOld := func() error { _ = scan(oldSQL, oldArgs); return nil }
	runNew := func() error { _ = scan(newSQL, newArgs); return nil }

	// sixParallelWith 复现六端点并发负载，仅 Summary 操作替换为指定 SQL（其余五端点
	// R2 未改动，两配置共享同一实现，墙钟差异只来自 Summary 结构）。
	sixParallelWith := func(summaryFn func() error) func() error {
		return func() error {
			pageFilter := filter
			pageFilter.Page = 1
			pageFilter.PageSize = 50
			ops := []func() error{
				summaryFn,
				func() error { _, err := ds.store.Trends(filter); return err },
				func() error { _, err := ds.store.Requests(pageFilter); return err },
				func() error { _, err := ds.store.Providers(filter); return err },
				func() error { _, err := ds.store.Models(filter); return err },
				func() error { _, err := ds.store.Coverage(filter); return err },
			}
			var wg sync.WaitGroup
			errs := make([]error, len(ops))
			for i, op := range ops {
				wg.Add(1)
				go func(i int, op func() error) {
					defer wg.Done()
					errs[i] = op()
				}(i, op)
			}
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					return err
				}
			}
			return nil
		}
	}

	const rounds = 3
	oldLatency := make([]time.Duration, 0, rounds)
	newLatency := make([]time.Duration, 0, rounds)
	oldWall := make([]time.Duration, 0, rounds)
	newWall := make([]time.Duration, 0, rounds)
	for round := 0; round < rounds; round++ {
		// 轮内顺序交替（偶数轮旧先、奇数轮新先），抵消会话内趋势性负载漂移。
		measure := func(fn func() error, runs int) time.Duration {
			d, err := medianDuration(runs, fn)
			if err != nil {
				t.Fatalf("measure: %v", err)
			}
			return d
		}
		var o, w time.Duration
		if round%2 == 0 {
			o = measure(runOld, 5)
			w = measure(runNew, 5)
		} else {
			w = measure(runNew, 5)
			o = measure(runOld, 5)
		}
		oldLatency, newLatency = append(oldLatency, o), append(newLatency, w)
		// 六并发墙钟对机器负载极敏感（R1 已证），单次尖刺可主导小样本中位，
		// 故每个样本取 5 次运行的中位以压低尖刺影响。
		var ow, ww time.Duration
		if round%2 == 0 {
			ow = measure(sixParallelWith(runOld), 5)
			ww = measure(sixParallelWith(runNew), 5)
		} else {
			ww = measure(sixParallelWith(runNew), 5)
			ow = measure(sixParallelWith(runOld), 5)
		}
		oldWall, newWall = append(oldWall, ow), append(newWall, ww)
		t.Logf("round %d: summary old=%s new=%s | six-parallel old=%s new=%s", round+1, o, w, ow, ww)
	}

	sortDurations := func(a []time.Duration) []time.Duration {
		out := append([]time.Duration(nil), a...)
		for i := range out {
			for j := i + 1; j < len(out); j++ {
				if out[j] < out[i] {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		return out
	}
	oldMed, newMed := sortDurations(oldLatency)[rounds/2], sortDurations(newLatency)[rounds/2]
	oldWallMed, newWallMed := sortDurations(oldWall)[rounds/2], sortDurations(newWall)[rounds/2]

	t.Logf("\n==== R2 Summary A/B (rows=%d, same session, warmup discarded, %d rounds) ====", n, rounds)
	t.Logf("field-by-field equivalence on %d rows: IDENTICAL (last_started=%v)", n, newResult.lastStarted.String)
	t.Logf("%-24s %14s %14s %10s", "metric", "R2-new", "R1-legacy", "speedup")
	t.Logf("%-24s %14s %14s %9.2fx", "Summary/status", newMed, oldMed, float64(oldMed)/float64(newMed))
	t.Logf("%-24s %14s %14s %9.2fx", "six parallel wall", newWallMed, oldWallMed, float64(oldWallMed)/float64(newWallMed))
	t.Logf("Summary samples  old: %v", oldLatency)
	t.Logf("Summary samples  new: %v", newLatency)
	t.Logf("six-parallel old: %v", oldWall)
	t.Logf("six-parallel new: %v", newWall)
	t.Logf("==================================================================")
}

// ============================================================================
// R3（P1）同会话 A/B：candidate 排名持久化（消除每查询窗口物化 + 自动索引）前后对照。
//
// 方法：在同一进程、同一 60,332 行数据集（seed=1）上，交替测量“R3 新结构”（buildScopedCTE
// 直接 JOIN 基表 usage_dedupe_candidates 走持久索引 idx_usage_dedupe_candidates_session_rank
// 的相关子查询）与“R3 前旧结构”（test-only 重建的 ROW_NUMBER candidate CTE 每查询物化 + 运行时
// 自动索引）两套查询的六端点延迟与六并发墙钟。先逐字段断言两套查询在全部六端点上结果完全一致
// （差分兼容的附加实数据证明），再交替多轮（轮内顺序交替抵消趋势性漂移）、丢弃预热、各取中位比较。
// 仅由 MCC_USAGE_EXPLAIN=1 + MCC_USAGE_AB=1 门控，CI 默认不执行。绝对值随机器负载漂移，仅同会话
// 比值可归因；建议多次独立运行（多会话）确认加速比稳定。
// ============================================================================

// r3LegacyScopedCTE 重建 R3 之前的 buildScopedCTE（test-only 性能对照体）：candidate 以
// ROW_NUMBER 窗口每查询物化，scoped LEFT JOIN candidate ON candidate_rank=1 在物化结果上触发
// 运行时自动索引。与 R2 末尾的 buildScopedCTE 逐字一致，仅用于同会话 A/B 归因。
func r3LegacyScopedCTE(filter Filter) (string, []any) {
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

// r3ABQueryPair 是一对仅在 scoped CTE 结构上不同（新=持久化 rank JOIN，旧=ROW_NUMBER 窗口物化）、
// 下游投影/参数完全相同的端点子查询。
type r3ABQueryPair struct {
	label  string
	newSQL string
	oldSQL string
	args   []any
}

// r3ABEndpoint 是一个待 A/B 的端点：名称 + 其全部子查询对 + 一个执行闭包（按 useOld 选择新/旧 SQL，
//
//	drain 全部结果行以强制完整执行）。
type r3ABEndpoint struct {
	name    string
	queries []r3ABQueryPair
	run     func(useOld bool) error
}

// r3LegacyPair 由新结构全查询派生旧结构全查询：旧查询 = 旧 CTE + 新查询剥离新 CTE 后的下游后缀，
// 保证两套查询仅在 scoped CTE 结构上不同、下游逐字一致，参数完全相同。
func r3LegacyPair(newCTE, legacyCTE, label, fullQuery string, args []any) r3ABQueryPair {
	suffix := strings.TrimPrefix(fullQuery, newCTE)
	return r3ABQueryPair{label: label, newSQL: fullQuery, oldSQL: legacyCTE + suffix, args: args}
}

// r3ABBuildEndpoints 构造六端点的新/旧查询对，与 store.go 读取路径一一对应。
func r3ABBuildEndpoints(tb testing.TB, filter Filter, now time.Time) []r3ABEndpoint {
	tb.Helper()
	newCTE, _ := buildScopedCTE(filter, true)
	legacyCTE, _ := r3LegacyScopedCTE(filter)

	drain := func(query string, args []any) error {
		_, err := drainRowsMatrix(tb, query, args)
		return err
	}

	var endpoints []r3ABEndpoint
	addEndpoint := func(name string, pairs ...r3ABQueryPair) {
		ep := r3ABEndpoint{name: name, queries: pairs}
		ep.run = func(useOld bool) error {
			for _, q := range pairs {
				sql := q.newSQL
				if useOld {
					sql = q.oldSQL
				}
				if err := drain(sql, q.args); err != nil {
					return err
				}
			}
			return nil
		}
		endpoints = append(endpoints, ep)
	}

	// Summary/status
	startOfToday, endOfToday, err := todayRange(filter)
	if err != nil {
		tb.Fatalf("today range: %v", err)
	}
	summarySQL, summaryArgs := buildSummaryQuery(filter, startOfToday, endOfToday)
	addEndpoint("Summary/status", r3LegacyPair(newCTE, legacyCTE, "summary-aggregate", summarySQL, summaryArgs))

	// Requests（count + page，page=1/size=50）
	countSQL, pageSQL, reqArgs := buildRequestsQueries(filter)
	pageArgs := append(append([]any{}, reqArgs...), 50, 0)
	addEndpoint("Requests",
		r3LegacyPair(newCTE, legacyCTE, "requests-count", countSQL, reqArgs),
		r3LegacyPair(newCTE, legacyCTE, "requests-page", pageSQL, pageArgs),
	)

	// Trends（range + bucket；UTC 单一偏移区间，确定性参数）
	rangeSQL, rangeArgs := buildTrendsRangeQuery(filter)
	loc, err := filterLocation(filter)
	if err != nil {
		tb.Fatalf("location: %v", err)
	}
	intervals := trendsZoneIntervals(loc, sql.NullInt64{Int64: 0, Valid: true}, sql.NullInt64{Int64: now.Unix(), Valid: true})
	bucketSQL, bucketArgs := buildTrendsQuery(filter, loc, intervals)
	addEndpoint("Trends",
		r3LegacyPair(newCTE, legacyCTE, "trends-range", rangeSQL, rangeArgs),
		r3LegacyPair(newCTE, legacyCTE, "trends-bucket", bucketSQL, bucketArgs),
	)

	// Providers / Models
	providersSQL, providersArgs := buildAggregateQuery(filter, "r.provider_id", "r.provider_name")
	addEndpoint("Providers", r3LegacyPair(newCTE, legacyCTE, "providers-aggregate", providersSQL, providersArgs))
	modelsSQL, modelsArgs := buildAggregateQuery(filter, "r.mapped_model", "r.mapped_model")
	addEndpoint("Models", r3LegacyPair(newCTE, legacyCTE, "models-aggregate", modelsSQL, modelsArgs))

	// Coverage（summary + status）
	coverageSummarySQL, coverageStatusSQL, coverageArgs := buildCoverageQueries(filter)
	addEndpoint("Coverage",
		r3LegacyPair(newCTE, legacyCTE, "coverage-summary", coverageSummarySQL, coverageArgs),
		r3LegacyPair(newCTE, legacyCTE, "coverage-status", coverageStatusSQL, coverageArgs),
	)

	return endpoints
}

// drainRowsMatrix 执行查询并将全部结果行规范化为字符串矩阵（[]byte→string、nil→"<nil>"、
// 其余 fmt 默认格式），供逐字段等价比较与计时时的完整 drain。
func drainRowsMatrix(tb testing.TB, query string, args []any) ([][]string, error) {
	tb.Helper()
	rows, err := abDB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("drain query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	var matrix [][]string
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		row := make([]string, len(cols))
		for i, v := range raw {
			switch x := v.(type) {
			case []byte:
				row[i] = string(x)
			case nil:
				row[i] = "<nil>"
			default:
				row[i] = fmt.Sprintf("%v", x)
			}
		}
		matrix = append(matrix, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return matrix, nil
}

// TestUsageR3CandidateRankABCompare 在同会话交替测量 R3 前后六端点 + 六并发延迟并给加速比。
func TestUsageR3CandidateRankABCompare(t *testing.T) {
	if !explainEnabled() || os.Getenv("MCC_USAGE_AB") == "" {
		t.Skip("set MCC_USAGE_EXPLAIN=1 and MCC_USAGE_AB=1 to run the R3 candidate-rank A/B comparison")
	}
	n := benchRows(t)
	if n == 0 {
		n = 60332
	}
	ds := newBenchDatasetInDir(t, t.TempDir(), n)
	ds.runMigration(t)
	setABDB(ds.db)
	defer setABDB(nil)

	filter := benchFilter()
	endpoints := r3ABBuildEndpoints(t, filter, benchFixedNow)

	// 逐字段等价：六端点全部子查询在 60k 基准数据上新旧结果必须完全一致。
	for _, ep := range endpoints {
		for _, q := range ep.queries {
			newRows, err := drainRowsMatrix(t, q.newSQL, q.args)
			if err != nil {
				t.Fatalf("%s/%s new drain: %v", ep.name, q.label, err)
			}
			oldRows, err := drainRowsMatrix(t, q.oldSQL, q.args)
			if err != nil {
				t.Fatalf("%s/%s old drain: %v", ep.name, q.label, err)
			}
			if !reflect.DeepEqual(newRows, oldRows) {
				t.Fatalf("%s/%s R3 result differs from legacy ROW_NUMBER structure: new=%d rows, old=%d rows",
					ep.name, q.label, len(newRows), len(oldRows))
			}
		}
	}

	// 六并发：六端点并发执行（按 useOld 选择新/旧 SQL），返回墙钟。
	sixParallel := func(useOld bool) error {
		var wg sync.WaitGroup
		errs := make([]error, len(endpoints))
		for i, ep := range endpoints {
			wg.Add(1)
			go func(i int, run func(bool) error) {
				defer wg.Done()
				errs[i] = run(useOld)
			}(i, ep.run)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	}

	const rounds = 3
	const runs = 5
	type sample struct{ old, new time.Duration }
	perEndpoint := make(map[string][]sample)
	for _, ep := range endpoints {
		perEndpoint[ep.name] = make([]sample, 0, rounds)
	}
	wallSamples := make([]sample, 0, rounds)

	measure := func(fn func() error) time.Duration {
		d, err := medianDuration(runs, fn)
		if err != nil {
			t.Fatalf("measure: %v", err)
		}
		return d
	}

	// 丢弃一次预热（纯 Go SQLite 冷启动），随后交替多轮。
	_ = measure(func() error { return sixParallel(false) })

	for round := 0; round < rounds; round++ {
		oldFirst := round%2 == 0
		for _, ep := range endpoints {
			var o, w time.Duration
			if oldFirst {
				o = measure(func() error { return ep.run(true) })
				w = measure(func() error { return ep.run(false) })
			} else {
				w = measure(func() error { return ep.run(false) })
				o = measure(func() error { return ep.run(true) })
			}
			perEndpoint[ep.name] = append(perEndpoint[ep.name], sample{old: o, new: w})
		}
		var ow, ww time.Duration
		if oldFirst {
			ow = measure(func() error { return sixParallel(true) })
			ww = measure(func() error { return sixParallel(false) })
		} else {
			ww = measure(func() error { return sixParallel(false) })
			ow = measure(func() error { return sixParallel(true) })
		}
		wallSamples = append(wallSamples, sample{old: ow, new: ww})
	}

	medianSample := func(samples []sample) sample {
		olds := make([]time.Duration, len(samples))
		news := make([]time.Duration, len(samples))
		for i, s := range samples {
			olds[i], news[i] = s.old, s.new
		}
		sortDurationsAsc(olds)
		sortDurationsAsc(news)
		return sample{old: olds[len(olds)/2], new: news[len(news)/2]}
	}

	t.Logf("\n==== R3 candidate-rank A/B (rows=%d, same session, warmup discarded, %d rounds × %d runs) ====", n, rounds, runs)
	t.Logf("field-by-field equivalence on %d rows: all six endpoints IDENTICAL", n)
	t.Logf("%-24s %14s %14s %10s", "metric", "R3-new", "R3-legacy", "speedup")
	for _, ep := range endpoints {
		med := medianSample(perEndpoint[ep.name])
		t.Logf("%-24s %14s %14s %9.2fx", ep.name, med.new, med.old, float64(med.old)/float64(med.new))
	}
	wallMed := medianSample(wallSamples)
	t.Logf("%-24s %14s %14s %9.2fx", "six parallel wall", wallMed.new, wallMed.old, float64(wallMed.old)/float64(wallMed.new))
	for _, ep := range endpoints {
		samples := perEndpoint[ep.name]
		oldList, newList := make([]time.Duration, len(samples)), make([]time.Duration, len(samples))
		for i, s := range samples {
			oldList[i], newList[i] = s.old, s.new
		}
		t.Logf("  %-22s old samples %v | new samples %v", ep.name, oldList, newList)
	}
	t.Logf("==================================================================")
}

// sortDurationsAsc 原地升序排序（小样本，避免额外 import 依赖）。
func sortDurationsAsc(a []time.Duration) {
	for i := range a {
		for j := i + 1; j < len(a); j++ {
			if a[j] < a[i] {
				a[i], a[j] = a[j], a[i]
			}
		}
	}
}

// abDB 是 R3 A/B drain 使用的库句柄（由测试设置/清空），使 drainRowsMatrix 不依赖具体 Store。
var abDB *sql.DB

func setABDB(db *sql.DB) { abDB = db }

// ============================================================================
// R4（P2+P3）同会话 A/B：CASE 惰性 candidate + scope 裁剪 + Summary epoch 单次求值
// 前后对照。
//
// 方法：在同一进程、同一 60,332 行数据集（seed=1）上，交替测量“R4 新结构”（CASE 惰性
// candidate 相关子查询 + Summary epoch 单次投影）与“R4 前旧结构”（test-only 重建的 R3
// LEFT JOIN candidate + 逐行相关子查询 + Summary 三处 epoch 求值）两套查询的六端点延迟
// 与六并发墙钟。先逐字段断言两套查询在全部六端点上结果完全一致，再交替多轮、丢弃预热、
// 各取中位比较。仅由 MCC_USAGE_EXPLAIN=1 + MCC_USAGE_AB=1 门控，CI 默认不执行。
// ============================================================================

// r4LegacySummaryQuery 重建 R4 之前的 buildSummaryQuery（test-only 性能对照体）：
// R3 版 scoped CTE + epoch 三处求值的聚合后缀，与 abeaa1e（R3）逐字一致。
func r4LegacySummaryQuery(filter Filter, startOfToday, endOfToday time.Time) (string, []any) {
	cte, args := r3LegacyScopedCTE(filter)
	epoch := scopedEpochSecondsExpr("r.started_at")
	todayPredicate := epoch + ` >= ? AND ` + epoch + ` < ?`
	lastStarted := `substr(MAX(` + scopedTimeOrderKeyExpr("r.started_at") + ` || r.started_at), 30)`
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

// r5LegacyPreReworkSummaryQuery 重建返工前（733461a，任务 6 验收态）的 buildSummaryQuery
// （test-only 性能对照体）：R3 前 ROW_NUMBER candidate CTE（r3LegacyScopedCTE，与 733461a
// 的 buildScopedCTE 逐字一致）+ last_provider_request 标量子查询。即 R2 前的完整 Summary
// 查询：整套 scoped CTE（60k 全扫 + candidate 物化 + 自动索引 + 末级排序）执行两次。
func r5LegacyPreReworkSummaryQuery(filter Filter, startOfToday, endOfToday time.Time) (string, []any) {
	cte, args := r3LegacyScopedCTE(filter)
	epoch := scopedEpochSecondsExpr("r.started_at")
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

// r4ABBuildEndpoints 构造六端点的新（R4）/旧（R3）查询对。Summary 的旧查询用
// r4LegacySummaryQuery（R3 版聚合后缀）；其余端点 R4 只改 scoped CTE、后缀不变，
// 沿用 r3LegacyPair 的“旧 CTE + 新后缀”派生机制。legacySummaryQuery 参数允许 R5
// 复用本构造器：传入 r5LegacyPreReworkSummaryQuery 即得“返工前 733461a 全套查询”。
func r4ABBuildEndpoints(tb testing.TB, filter Filter, now time.Time, legacySummaryQuery func(Filter, time.Time, time.Time) (string, []any)) []r3ABEndpoint {
	tb.Helper()
	newCTE, _ := buildScopedCTE(filter, true)
	legacyCTE, _ := r3LegacyScopedCTE(filter)

	drain := func(query string, args []any) error {
		_, err := drainRowsMatrix(tb, query, args)
		return err
	}

	var endpoints []r3ABEndpoint
	addEndpoint := func(name string, pairs ...r3ABQueryPair) {
		ep := r3ABEndpoint{name: name, queries: pairs}
		ep.run = func(useOld bool) error {
			for _, q := range pairs {
				sql := q.newSQL
				if useOld {
					sql = q.oldSQL
				}
				if err := drain(sql, q.args); err != nil {
					return err
				}
			}
			return nil
		}
		endpoints = append(endpoints, ep)
	}

	// Summary/status（R4 epoch 单次求值 vs R3 三处求值）
	startOfToday, endOfToday, err := todayRange(filter)
	if err != nil {
		tb.Fatalf("today range: %v", err)
	}
	newSummarySQL, summaryArgs := buildSummaryQuery(filter, startOfToday, endOfToday)
	oldSummarySQL, _ := legacySummaryQuery(filter, startOfToday, endOfToday)
	addEndpoint("Summary/status", r3ABQueryPair{label: "summary-aggregate", newSQL: newSummarySQL, oldSQL: oldSummarySQL, args: summaryArgs})

	// Requests（count + page，page=1/size=50）
	countSQL, pageSQL, reqArgs := buildRequestsQueries(filter)
	pageArgs := append(append([]any{}, reqArgs...), 50, 0)
	addEndpoint("Requests",
		r3LegacyPair(newCTE, legacyCTE, "requests-count", countSQL, reqArgs),
		r3LegacyPair(newCTE, legacyCTE, "requests-page", pageSQL, pageArgs),
	)

	// Trends（range + bucket；UTC 单一偏移区间，确定性参数）
	rangeSQL, rangeArgs := buildTrendsRangeQuery(filter)
	loc, err := filterLocation(filter)
	if err != nil {
		tb.Fatalf("location: %v", err)
	}
	intervals := trendsZoneIntervals(loc, sql.NullInt64{Int64: 0, Valid: true}, sql.NullInt64{Int64: now.Unix(), Valid: true})
	bucketSQL, bucketArgs := buildTrendsQuery(filter, loc, intervals)
	addEndpoint("Trends",
		r3LegacyPair(newCTE, legacyCTE, "trends-range", rangeSQL, rangeArgs),
		r3LegacyPair(newCTE, legacyCTE, "trends-bucket", bucketSQL, bucketArgs),
	)

	// Providers / Models
	providersSQL, providersArgs := buildAggregateQuery(filter, "r.provider_id", "r.provider_name")
	addEndpoint("Providers", r3LegacyPair(newCTE, legacyCTE, "providers-aggregate", providersSQL, providersArgs))
	modelsSQL, modelsArgs := buildAggregateQuery(filter, "r.mapped_model", "r.mapped_model")
	addEndpoint("Models", r3LegacyPair(newCTE, legacyCTE, "models-aggregate", modelsSQL, modelsArgs))

	// Coverage（summary + status）
	coverageSummarySQL, coverageStatusSQL, coverageArgs := buildCoverageQueries(filter)
	addEndpoint("Coverage",
		r3LegacyPair(newCTE, legacyCTE, "coverage-summary", coverageSummarySQL, coverageArgs),
		r3LegacyPair(newCTE, legacyCTE, "coverage-status", coverageStatusSQL, coverageArgs),
	)

	return endpoints
}

// TestUsageR4CandidateABCompare 在同会话交替测量 R4 前后六端点 + 六并发延迟并给加速比。
// 该测试同时用于验证 R4 结构无回归：旧结构即 R3 生产实现（LEFT JOIN candidate +
// 三处 epoch 求值），若 R4 任一端点慢于 R3 即红（在输出表中直接可见）。
func TestUsageR4CandidateABCompare(t *testing.T) {
	if !explainEnabled() || os.Getenv("MCC_USAGE_AB") == "" {
		t.Skip("set MCC_USAGE_EXPLAIN=1 and MCC_USAGE_AB=1 to run the R4 A/B comparison")
	}
	n := benchRows(t)
	if n == 0 {
		n = 60332
	}
	ds := newBenchDatasetInDir(t, t.TempDir(), n)
	ds.runMigration(t)
	setABDB(ds.db)
	defer setABDB(nil)

	filter := benchFilter()
	endpoints := r4ABBuildEndpoints(t, filter, benchFixedNow, r4LegacySummaryQuery)

	// 逐字段等价：六端点全部子查询在 60k 基准数据上新旧结果必须完全一致。
	for _, ep := range endpoints {
		for _, q := range ep.queries {
			newRows, err := drainRowsMatrix(t, q.newSQL, q.args)
			if err != nil {
				t.Fatalf("%s/%s new drain: %v", ep.name, q.label, err)
			}
			oldRows, err := drainRowsMatrix(t, q.oldSQL, q.args)
			if err != nil {
				t.Fatalf("%s/%s old drain: %v", ep.name, q.label, err)
			}
			if !reflect.DeepEqual(newRows, oldRows) {
				t.Fatalf("%s/%s R4 result differs from R3 legacy structure: new=%d rows, old=%d rows",
					ep.name, q.label, len(newRows), len(oldRows))
			}
		}
	}

	// 六并发：六端点并发执行（按 useOld 选择新/旧 SQL），返回墙钟。
	sixParallel := func(useOld bool) error {
		var wg sync.WaitGroup
		errs := make([]error, len(endpoints))
		for i, ep := range endpoints {
			wg.Add(1)
			go func(i int, run func(bool) error) {
				defer wg.Done()
				errs[i] = run(useOld)
			}(i, ep.run)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	}

	const rounds = 3
	const runs = 5
	type sample struct{ old, new time.Duration }
	perEndpoint := make(map[string][]sample)
	for _, ep := range endpoints {
		perEndpoint[ep.name] = make([]sample, 0, rounds)
	}
	wallSamples := make([]sample, 0, rounds)

	measure := func(fn func() error) time.Duration {
		d, err := medianDuration(runs, fn)
		if err != nil {
			t.Fatalf("measure: %v", err)
		}
		return d
	}

	// 丢弃一次预热（纯 Go SQLite 冷启动），随后交替多轮。
	_ = measure(func() error { return sixParallel(false) })

	for round := 0; round < rounds; round++ {
		oldFirst := round%2 == 0
		for _, ep := range endpoints {
			var o, w time.Duration
			if oldFirst {
				o = measure(func() error { return ep.run(true) })
				w = measure(func() error { return ep.run(false) })
			} else {
				w = measure(func() error { return ep.run(false) })
				o = measure(func() error { return ep.run(true) })
			}
			perEndpoint[ep.name] = append(perEndpoint[ep.name], sample{old: o, new: w})
		}
		var ow, ww time.Duration
		if oldFirst {
			ow = measure(func() error { return sixParallel(true) })
			ww = measure(func() error { return sixParallel(false) })
		} else {
			ww = measure(func() error { return sixParallel(false) })
			ow = measure(func() error { return sixParallel(true) })
		}
		wallSamples = append(wallSamples, sample{old: ow, new: ww})
	}

	medianSample := func(samples []sample) sample {
		olds := make([]time.Duration, len(samples))
		news := make([]time.Duration, len(samples))
		for i, s := range samples {
			olds[i], news[i] = s.old, s.new
		}
		sortDurationsAsc(olds)
		sortDurationsAsc(news)
		return sample{old: olds[len(olds)/2], new: news[len(news)/2]}
	}

	t.Logf("\n==== R4 A/B (rows=%d, same session, warmup discarded, %d rounds × %d runs) ====", n, rounds, runs)
	t.Logf("field-by-field equivalence on %d rows: all six endpoints IDENTICAL", n)
	t.Logf("%-24s %14s %14s %10s", "metric", "R4-new", "R3-legacy", "speedup")
	for _, ep := range endpoints {
		med := medianSample(perEndpoint[ep.name])
		t.Logf("%-24s %14s %14s %9.2fx", ep.name, med.new, med.old, float64(med.old)/float64(med.new))
	}
	wallMed := medianSample(wallSamples)
	t.Logf("%-24s %14s %14s %9.2fx", "six parallel wall", wallMed.new, wallMed.old, float64(wallMed.old)/float64(wallMed.new))
	for _, ep := range endpoints {
		samples := perEndpoint[ep.name]
		oldList, newList := make([]time.Duration, len(samples)), make([]time.Duration, len(samples))
		for i, s := range samples {
			oldList[i], newList[i] = s.old, s.new
		}
		t.Logf("  %-22s old samples %v | new samples %v", ep.name, oldList, newList)
	}
	t.Logf("==================================================================")
}

// ============================================================================
// R5 同会话 A/B：返工前（733461a，任务 6 验收态）→ 返工后（HEAD，R1–R4 累计）六端点
// + 六并发延迟。
//
// 方法：在同一进程、同一 60,332 行数据集（seed=1）上，交替测量“HEAD 全套查询”与
// “test-only 重建的 733461a 全套查询”（r5LegacyPreReworkSummaryQuery 复现返工前
// Summary 标量子查询；其余端点沿用 r3LegacyScopedCTE 旧 CTE + 当前下游后缀——R2/R3/R4
// 已证非 Summary 端点的下游投影/聚合/排序未变）两套查询的六端点延迟与六并发墙钟。
// 先逐字段断言两套查询在全部六端点上结果完全一致（差分兼容的附加实数据证明），再交替
// 多轮（轮内顺序交替抵消趋势性漂移）、丢弃预热、各取中位比较。仅由 MCC_USAGE_EXPLAIN=1
// + MCC_USAGE_AB=1 门控，CI 默认不执行。绝对值随机器负载漂移，仅同会话比值可归因；
// 建议多次独立运行（多会话）确认加速比稳定。
// ============================================================================

// TestUsageR5FullReworkABCompare 在同会话交替测量返工前 733461a → 返工后 HEAD 的
// 六端点 + 六并发延迟并给出累计加速比（R1–R4 全部收益叠加）。
func TestUsageR5FullReworkABCompare(t *testing.T) {
	if !explainEnabled() || os.Getenv("MCC_USAGE_AB") == "" {
		t.Skip("set MCC_USAGE_EXPLAIN=1 and MCC_USAGE_AB=1 to run the R5 full-rework A/B comparison")
	}
	n := benchRows(t)
	if n == 0 {
		n = 60332
	}
	ds := newBenchDatasetInDir(t, t.TempDir(), n)
	ds.runMigration(t)
	setABDB(ds.db)
	defer setABDB(nil)

	filter := benchFilter()
	endpoints := r4ABBuildEndpoints(t, filter, benchFixedNow, r5LegacyPreReworkSummaryQuery)

	// 逐字段等价：六端点全部子查询在 60k 基准数据上新旧结果必须完全一致。
	for _, ep := range endpoints {
		for _, q := range ep.queries {
			newRows, err := drainRowsMatrix(t, q.newSQL, q.args)
			if err != nil {
				t.Fatalf("%s/%s new drain: %v", ep.name, q.label, err)
			}
			oldRows, err := drainRowsMatrix(t, q.oldSQL, q.args)
			if err != nil {
				t.Fatalf("%s/%s old drain: %v", ep.name, q.label, err)
			}
			if !reflect.DeepEqual(newRows, oldRows) {
				t.Fatalf("%s/%s R5 result differs from pre-rework 733461a structure: new=%d rows, old=%d rows",
					ep.name, q.label, len(newRows), len(oldRows))
			}
		}
	}

	// 六并发：六端点并发执行（按 useOld 选择新/旧 SQL），返回墙钟。
	sixParallel := func(useOld bool) error {
		var wg sync.WaitGroup
		errs := make([]error, len(endpoints))
		for i, ep := range endpoints {
			wg.Add(1)
			go func(i int, run func(bool) error) {
				defer wg.Done()
				errs[i] = run(useOld)
			}(i, ep.run)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	}

	const rounds = 3
	const runs = 5
	type sample struct{ old, new time.Duration }
	perEndpoint := make(map[string][]sample)
	for _, ep := range endpoints {
		perEndpoint[ep.name] = make([]sample, 0, rounds)
	}
	wallSamples := make([]sample, 0, rounds)

	measure := func(fn func() error) time.Duration {
		d, err := medianDuration(runs, fn)
		if err != nil {
			t.Fatalf("measure: %v", err)
		}
		return d
	}

	// 丢弃一次预热（纯 Go SQLite 冷启动），随后交替多轮。
	_ = measure(func() error { return sixParallel(false) })

	for round := 0; round < rounds; round++ {
		oldFirst := round%2 == 0
		for _, ep := range endpoints {
			var o, w time.Duration
			if oldFirst {
				o = measure(func() error { return ep.run(true) })
				w = measure(func() error { return ep.run(false) })
			} else {
				w = measure(func() error { return ep.run(false) })
				o = measure(func() error { return ep.run(true) })
			}
			perEndpoint[ep.name] = append(perEndpoint[ep.name], sample{old: o, new: w})
		}
		var ow, ww time.Duration
		if oldFirst {
			ow = measure(func() error { return sixParallel(true) })
			ww = measure(func() error { return sixParallel(false) })
		} else {
			ww = measure(func() error { return sixParallel(false) })
			ow = measure(func() error { return sixParallel(true) })
		}
		wallSamples = append(wallSamples, sample{old: ow, new: ww})
	}

	medianSample := func(samples []sample) sample {
		olds := make([]time.Duration, len(samples))
		news := make([]time.Duration, len(samples))
		for i, s := range samples {
			olds[i], news[i] = s.old, s.new
		}
		sortDurationsAsc(olds)
		sortDurationsAsc(news)
		return sample{old: olds[len(olds)/2], new: news[len(news)/2]}
	}

	t.Logf("\n==== R5 full-rework A/B (rows=%d, same session, warmup discarded, %d rounds × %d runs) ====", n, rounds, runs)
	t.Logf("field-by-field equivalence on %d rows: all six endpoints IDENTICAL (pre-rework 733461a vs HEAD)", n)
	t.Logf("%-24s %14s %14s %10s", "metric", "HEAD (R1-R4)", "pre-rework", "speedup")
	for _, ep := range endpoints {
		med := medianSample(perEndpoint[ep.name])
		t.Logf("%-24s %14s %14s %9.2fx", ep.name, med.new, med.old, float64(med.old)/float64(med.new))
	}
	wallMed := medianSample(wallSamples)
	t.Logf("%-24s %14s %14s %9.2fx", "six parallel wall", wallMed.new, wallMed.old, float64(wallMed.old)/float64(wallMed.new))
	for _, ep := range endpoints {
		samples := perEndpoint[ep.name]
		oldList, newList := make([]time.Duration, len(samples)), make([]time.Duration, len(samples))
		for i, s := range samples {
			oldList[i], newList[i] = s.old, s.new
		}
		t.Logf("  %-22s old samples %v | new samples %v", ep.name, oldList, newList)
	}
	t.Logf("==================================================================")
}
