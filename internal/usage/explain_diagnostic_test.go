package usage

// 本文件实现 R1 返工任务要求的“精确性能诊断”：对 60,332 行确定性数据集（seed=1，
// 与 performance_test.go 共用同一生成器与迁移路径）分别对六个 usage 统计端点当前 SQL
// 执行 EXPLAIN QUERY PLAN，逐行打印计划并汇总开销来源（SCAN / TEMP B-TREE /
// AUTOMATIC INDEX / ROW_NUMBER 窗口 / 子查询次数），定位每端点瓶颈。
//
// 设计原则：
//   - 未设置 MCC_USAGE_EXPLAIN 时全部跳过，CI 默认不执行，不引入任何墙钟断言。
//   - 只读诊断：不修改 schema、不改查询语义，仅 EXPLAIN 当前实现，供返工决策。
//   - 数据集与基准完全一致（同 seed、同行数、同迁移），诊断结论可直接对照基准数字。

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// explainEnabled 返回是否启用 EXPLAIN 诊断（MCC_USAGE_EXPLAIN 非空即启用）。
func explainEnabled() bool {
	return os.Getenv("MCC_USAGE_EXPLAIN") != ""
}

// explainRows 对一条查询执行 EXPLAIN QUERY PLAN，返回缩进后的计划行文本与统计计数。
func explainRows(tb testing.TB, db *sql.DB, query string, args []any) (string, explainCosts) {
	tb.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		tb.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()

	var b strings.Builder
	var costs explainCosts
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			tb.Fatalf("scan explain row: %v", err)
		}
		costs.observe(detail)
		fmt.Fprintf(&b, "        %s\n", detail)
	}
	if err := rows.Err(); err != nil {
		tb.Fatalf("read explain rows: %v", err)
	}
	return b.String(), costs
}

// explainCosts 汇总一条查询计划中各类开销来源的出现次数。
type explainCosts struct {
	Scans       int // 全表 SCAN（未走持久索引）
	TempBTrees  int // USE TEMP B-TREE（排序/分组临时结构）
	AutoIndexes int // AUTOMATIC ... INDEX（运行时自建索引）
	RowNumbers  int // ROW_NUMBER / 窗口函数（CO-Routine / WINDOW）
	Subqueries  int // 子查询 / 物化（CO-Routine / SUBQUERY / LIST SUBQUERY）
	IndexUsed   int // USING INDEX / COVERING INDEX（走持久索引）
	SearchPK    int // SEARCH ... USING INTEGER PK / PRIMARY KEY
}

// observe 根据计划行 detail 累加对应开销计数。
func (c *explainCosts) observe(detail string) {
	upper := strings.ToUpper(detail)
	switch {
	case strings.Contains(upper, "AUTOMATIC"):
		c.AutoIndexes++
	case strings.Contains(upper, "SCAN"):
		c.Scans++
	}
	if strings.Contains(upper, "TEMP B-TREE") {
		c.TempBTrees++
	}
	if strings.Contains(upper, "USING INDEX") || strings.Contains(upper, "COVERING INDEX") {
		c.IndexUsed++
	}
	if strings.Contains(upper, "USING INTEGER PK") || strings.Contains(upper, "PRIMARY KEY") {
		c.SearchPK++
	}
	if strings.Contains(upper, "CO-Routine") || strings.Contains(upper, "SUBQUERY") || strings.Contains(upper, "LIST SUBQUERY") {
		c.Subqueries++
	}
}

// explainEndpoint 描述一个待诊断端点：名称 + 生成 (查询, 参数) 的闭包。
type explainEndpoint struct {
	name  string
	build func(filter Filter, now time.Time) []explainQuery
}

// explainQuery 是一条待 EXPLAIN 的具名查询。
type explainQuery struct {
	label string
	sql   string
	args  []any
}

// explainEndpoints 返回六个端点当前实现的查询构造器，与 store.go 读取路径一一对应。
func explainEndpoints() []explainEndpoint {
	return []explainEndpoint{
		{
			name: "Summary/status",
			build: func(filter Filter, now time.Time) []explainQuery {
				startOfToday, endOfToday, _ := todayRange(filter)
				q, args := buildSummaryQuery(filter, startOfToday, endOfToday)
				return []explainQuery{{label: "summary-aggregate", sql: q, args: args}}
			},
		},
		{
			name: "Requests",
			build: func(filter Filter, now time.Time) []explainQuery {
				countSQL, pageSQL, args := buildRequestsQueries(filter)
				pageArgs := append(append([]any{}, args...), 50, 2450)
				return []explainQuery{
					{label: "requests-count", sql: countSQL, args: args},
					{label: "requests-page", sql: pageSQL, args: pageArgs},
				}
			},
		},
		{
			name: "Trends",
			build: func(filter Filter, now time.Time) []explainQuery {
				rangeQ, rangeArgs := buildTrendsRangeQuery(filter)
				loc, _ := filterLocation(filter)
				intervals := trendsZoneIntervals(loc, sql.NullInt64{Int64: 0, Valid: true}, sql.NullInt64{Int64: now.Unix(), Valid: true})
				bucketQ, bucketArgs := buildTrendsQuery(filter, loc, intervals)
				return []explainQuery{
					{label: "trends-range", sql: rangeQ, args: rangeArgs},
					{label: "trends-bucket", sql: bucketQ, args: bucketArgs},
				}
			},
		},
		{
			name: "Providers",
			build: func(filter Filter, now time.Time) []explainQuery {
				q, args := buildAggregateQuery(filter, "r.provider_id", "r.provider_name")
				return []explainQuery{{label: "providers-aggregate", sql: q, args: args}}
			},
		},
		{
			name: "Models",
			build: func(filter Filter, now time.Time) []explainQuery {
				q, args := buildAggregateQuery(filter, "r.mapped_model", "r.mapped_model")
				return []explainQuery{{label: "models-aggregate", sql: q, args: args}}
			},
		},
		{
			name: "Coverage",
			build: func(filter Filter, now time.Time) []explainQuery {
				summarySQL, statusSQL, args := buildCoverageQueries(filter)
				return []explainQuery{
					{label: "coverage-summary", sql: summarySQL, args: args},
					{label: "coverage-status", sql: statusSQL, args: args},
				}
			},
		},
	}
}

// TestUsageExplainDiagnostic 对六个端点当前 SQL 执行 EXPLAIN QUERY PLAN，打印计划与
// 开销汇总。无断言；未设置 MCC_USAGE_EXPLAIN 时跳过。数据集行数由 MCC_USAGE_BENCH_ROWS
// 控制（缺省 60332，与生产一致）。
func TestUsageExplainDiagnostic(t *testing.T) {
	if !explainEnabled() {
		t.Skip("set MCC_USAGE_EXPLAIN=1 to run EXPLAIN diagnostics")
	}
	n := benchRows(t)
	if n == 0 {
		n = 60332
	}
	ds := newBenchDatasetInDir(t, t.TempDir(), n)
	ds.runMigration(t)

	filter := benchFilter()
	now := benchFixedNow

	t.Logf("\n==== EXPLAIN QUERY PLAN diagnostics (rows=%d, seed=1) ====", n)
	for _, ep := range explainEndpoints() {
		t.Logf("\n---- endpoint: %s ----", ep.name)
		var total explainCosts
		for _, q := range ep.build(filter, now) {
			plan, costs := explainRows(t, ds.db, q.sql, q.args)
			total.add(costs)
			t.Logf("  [%s]\n%s", q.label, plan)
			t.Logf("  [%s] costs: %s", q.label, costs.String())
		}
		t.Logf("  >> %s TOTAL: %s", ep.name, total.String())
	}
	t.Logf("\n==============================================================")
}

// TestUsageQueryPlansEliminateCandidateWindowAndAutoIndex 是 R3 的 CI 防回归 EXPLAIN 断言：对六个
// usage 统计端点当前 SQL 执行 EXPLAIN QUERY PLAN，断言 candidate 选取不再每查询物化 ROW_NUMBER
// 窗口（无 LAST 3 TERMS 临时排序）、不再建运行时自动索引（无 AUTOMATIC INDEX），且 LEFT JOIN 候选
// 走持久索引 idx_usage_dedupe_candidates_session_rank。计划形态只取决于查询结构与可用索引；为使
// 计划器统计信息贴近生产，在一个含候选关系的小迁移库上验证，CI 默认执行（不受 MCC_USAGE_EXPLAIN
// 门控）。任一端点回归为“candidate 窗口物化 + 自动索引”即红。
func TestUsageQueryPlansEliminateCandidateWindowAndAutoIndex(t *testing.T) {
	store := newTestStore(t)
	seedScopedQueryFixture(t, store)
	filter := benchFilter()
	now := benchFixedNow
	for _, ep := range explainEndpoints() {
		for _, q := range ep.build(filter, now) {
			rows, err := store.db.Query("EXPLAIN QUERY PLAN "+q.sql, q.args...)
			if err != nil {
				t.Fatalf("%s/%s EXPLAIN QUERY PLAN: %v", ep.name, q.label, err)
			}
			var plan []string
			var autoIndexes, candidateWindows, persistentIndexHits int
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					rows.Close()
					t.Fatalf("%s/%s scan explain row: %v", ep.name, q.label, err)
				}
				plan = append(plan, detail)
				upper := strings.ToUpper(detail)
				if strings.Contains(upper, "AUTOMATIC") {
					autoIndexes++
				}
				if strings.Contains(upper, "LAST 3 TERMS") {
					candidateWindows++
				}
				if strings.Contains(detail, usageCandidateRankIndexName) {
					persistentIndexHits++
				}
			}
			rows.Close()
			if autoIndexes != 0 {
				t.Errorf("%s/%s plan builds %d automatic indexes, want 0 (R3 应消除 candidate 自动索引):\n%s",
					ep.name, q.label, autoIndexes, strings.Join(plan, "\n"))
			}
			if candidateWindows != 0 {
				t.Errorf("%s/%s plan contains %d candidate window sorts (LAST 3 TERMS), want 0 (R3 应消除 candidate 窗口物化):\n%s",
					ep.name, q.label, candidateWindows, strings.Join(plan, "\n"))
			}
			if persistentIndexHits == 0 {
				t.Errorf("%s/%s plan does not use %s, want persistent index engagement:\n%s",
					ep.name, q.label, usageCandidateRankIndexName, strings.Join(plan, "\n"))
			}
		}
	}
}

// add 将另一组开销计数累加到当前组。
func (c *explainCosts) add(o explainCosts) {
	c.Scans += o.Scans
	c.TempBTrees += o.TempBTrees
	c.AutoIndexes += o.AutoIndexes
	c.RowNumbers += o.RowNumbers
	c.Subqueries += o.Subqueries
	c.IndexUsed += o.IndexUsed
	c.SearchPK += o.SearchPK
}

// String 返回开销计数的紧凑文本表示。
func (c explainCosts) String() string {
	return fmt.Sprintf(
		"SCAN=%d TEMP_BTREE=%d AUTO_INDEX=%d SUBQUERY/CO-Routine=%d USING_INDEX=%d PK_SEARCH=%d",
		c.Scans, c.TempBTrees, c.AutoIndexes, c.Subqueries, c.IndexUsed, c.SearchPK,
	)
}

// TestUsageQueryPlansUseStartedIDIndexOnTimeRange 是 R4（任务 3）的 CI 防回归 EXPLAIN
// 断言：带 started_at 时间范围过滤（最高频的常见过滤组合）时，六端点 filtered 主循环
// 必须走 idx_usage_requests_started_id 的范围 SEARCH，而非 60k 全表 SCAN。计划形态只取
// 决于查询结构与可用索引，故在一个小迁移库上验证（CI 默认执行，不受 MCC_USAGE_EXPLAIN
// 门控；计划器统计信息足够即可稳定选索引）。无过滤/其他过滤组合的全扫仍不可避免（需
// 统计全部行），不属于本断言范围。
func TestUsageQueryPlansUseStartedIDIndexOnTimeRange(t *testing.T) {
	// 用确定性基准生成器铺一个 5000 行库（含候选关系），使计划器统计信息贴近生产。
	ds := newBenchDatasetInDir(t, t.TempDir(), 5000)
	ds.runMigration(t)

	filter := benchFilter()
	filter.From = benchFixedNow.Add(-7 * 24 * time.Hour)
	filter.To = benchFixedNow.Add(-24 * time.Hour)
	now := benchFixedNow

	for _, ep := range explainEndpoints() {
		for _, q := range ep.build(filter, now) {
			rows, err := ds.db.Query("EXPLAIN QUERY PLAN "+q.sql, q.args...)
			if err != nil {
				t.Fatalf("%s/%s EXPLAIN QUERY PLAN: %v", ep.name, q.label, err)
			}
			var plan []string
			var startedIndexSearches, fullScans int
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					rows.Close()
					t.Fatalf("%s/%s scan explain row: %v", ep.name, q.label, err)
				}
				plan = append(plan, detail)
				upper := strings.ToUpper(detail)
				// 6A 基础索引 idx_usage_requests_started_at（单列）与 R1 复合索引
				// idx_usage_requests_started_id 都可用于时间范围 SEARCH；计划器按代价任选其一。
				if strings.Contains(upper, "SEARCH R USING INDEX IDX_USAGE_REQUESTS_STARTED_AT") ||
					strings.Contains(upper, "SEARCH R USING INDEX IDX_USAGE_REQUESTS_STARTED_ID") {
					startedIndexSearches++
				}
				// 仅统计 usage_requests 主循环的裸全扫（不含相关子查询按 PK 反查）。
				if strings.Contains(upper, "SCAN R") && !strings.Contains(upper, "USING INDEX") {
					fullScans++
				}
			}
			rows.Close()
			if startedIndexSearches == 0 {
				t.Errorf("%s/%s plan does not SEARCH a started_at index on time range (R4 任务 3 应走索引避免 60k 全扫):\n%s",
					ep.name, q.label, strings.Join(plan, "\n"))
			}
			if fullScans != 0 {
				t.Errorf("%s/%s plan full-scans usage_requests despite time range (%d SCAN r), want index SEARCH:\n%s",
					ep.name, q.label, fullScans, strings.Join(plan, "\n"))
			}
		}
	}
}
