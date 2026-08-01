package usage

// 本文件实现 R1 的“同机同会话 A/B 对照”测量：在同一进程、同一 60,332 行数据集上，
// 交替测量“含 R1 新索引”与“删除新索引（回退到 6C2 索引集）”两套配置的完整 profile，
// 以消除跨次运行的机器负载漂移，把基准差异归因到索引本身。仅由 MCC_USAGE_AB=1 门控，
// CI 默认不执行；不修改 schema/查询语义，DROP/CREATE 仅作用于临时基准库。

import (
	"database/sql"
	"os"
	"reflect"
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
	cte, args := buildScopedCTE(filter)
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
