package usage

// 本文件实现 R1 的“同机同会话 A/B 对照”测量：在同一进程、同一 60,332 行数据集上，
// 交替测量“含 R1 新索引”与“删除新索引（回退到 6C2 索引集）”两套配置的完整 profile，
// 以消除跨次运行的机器负载漂移，把基准差异归因到索引本身。仅由 MCC_USAGE_AB=1 门控，
// CI 默认不执行；不修改 schema/查询语义，DROP/CREATE 仅作用于临时基准库。

import (
	"os"
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
