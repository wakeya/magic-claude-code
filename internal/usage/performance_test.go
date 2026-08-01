package usage

// 本文件实现规格任务 6 要求的“确定性基准”：通过环境变量 MCC_USAGE_BENCH_ROWS 选择
// 数据量（生产代表性的 60000，或可选的 1000000 合成压力），测量并记录关键延迟指标。
//
// 设计原则（对应 spec R7 / 任务 6 约束）：
//   - 未设置 MCC_USAGE_BENCH_ROWS 时全部跳过，因此 CI 默认不执行，绝不引入易波动的
//     墙钟硬断言；正确性/查询结构/分页下推/迁移行为由其它专项测试保证。
//   - 数据集由固定随机种子（seed=1）生成，完全可重复：相同的行数总是产生相同的
//     usage_requests / usage_tokens / 候选关系分布。
//   - 基准只在临时目录中的独立 SQLite 库上运行，不读取、不修改、不暴露任何生产数据库
//     内容或凭证；输出仅含计数与耗时。
//
// 数据集形态参照 spec_ZH.md“整体分析”：生产 60,332 条 usage_requests + 等量
// usage_tokens。生成器按约 82% 供应商行 / 18% 会话行的比例混合，并让约一半会话行
// 镜像某个邻近供应商行（相同模型 + 相同四类 token 计数 + ±5 分钟窗口），从而确定性地
// 产生候选关系，复现生产去重负载。
//
// 测量项与 spec 目标/基线表一一对应：
//   - 迁移（含候选回填）耗时            目标 ≤ 2s
//   - GET /api/status 单次延迟（Summary）目标 ≤ 100ms（基线 760-780ms）
//   - Requests 第 50 页 LIMIT/OFFSET     目标 ≤ 100ms
//   - 六个 usage 统计接口并发墙钟        目标 ≤ 300ms（基线约 1.90s）

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// benchRows 返回 MCC_USAGE_BENCH_ROWS 指定的行数；未设置或非法时返回 0（表示跳过）。
func benchRows(tb testing.TB) int {
	tb.Helper()
	raw := os.Getenv("MCC_USAGE_BENCH_ROWS")
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		tb.Fatalf("MCC_USAGE_BENCH_ROWS must be a positive integer, got %q", raw)
	}
	return n
}

// benchFixedNow 是数据集时间窗内的固定“当前时刻”，用于 Summary 的今日区间等计算，
// 保证跨次运行的查询路径完全一致（可重复）。
var benchFixedNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

// benchDataset 是一个已构造好的基准数据库句柄。
type benchDataset struct {
	store *Store
	db    *sql.DB
	rows  int
}

// benchProfile 汇总一次性能剖析的测量结果。
type benchProfile struct {
	Rows            int
	Candidates      int
	Migration       time.Duration
	StatusSummary   time.Duration
	RequestsPage50  time.Duration
	Trends          time.Duration
	Providers       time.Duration
	Models          time.Duration
	Coverage        time.Duration
	SixParallelWall time.Duration
}

// newBenchDatasetInDir 在指定目录构造一个含 n 行的基准库。返回时 dedupe 候选已被清空、
// 回填标记已被移除，即处于“升级前的生产状态”，以便调用方计时一次完整的 Migrate（含回填）。
func newBenchDatasetInDir(tb testing.TB, dir string, n int) *benchDataset {
	tb.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "usage-bench.db"))
	if err != nil {
		tb.Fatalf("open sqlite: %v", err)
	}
	// 与生产一致的 WAL + NORMAL 同步，避免基准被默认 rollback journal 的 fsync 放大。
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			tb.Fatalf("set pragma %q: %v", pragma, err)
		}
	}
	store := NewStore(db)
	if err := store.Migrate(); err != nil {
		tb.Fatalf("Migrate() on empty db: %v", err)
	}
	seedBenchRows(tb, db, n)

	// 还原为“升级前生产状态”：清空候选关系并移除回填标记，使下一次 Migrate 执行完整回填。
	if _, err := db.Exec(`DELETE FROM usage_dedupe_candidates`); err != nil {
		tb.Fatalf("reset dedupe candidates: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM settings WHERE key = ?`, dedupeCandidatesBackfillMarker); err != nil {
		tb.Fatalf("reset dedupe marker: %v", err)
	}
	// 同时移除 R3 candidate_rank 迁移标记：使下一次 Migrate 在 dedupe 回填重建候选后重跑
	// 排名回填，模拟“生产升级：候选已存在但尚未持久化 rank”的真实迁移路径。
	if _, err := db.Exec(`DELETE FROM settings WHERE key = ?`, usageCandidateRankMarker); err != nil {
		tb.Fatalf("reset candidate rank marker: %v", err)
	}
	return &benchDataset{store: store, db: db, rows: n}
}

// seedBenchRows 以固定种子确定性地向 usage_requests / usage_tokens 批量插入 n 行。
// 直接走 SQL 批量插入（而非 store.Record），以模拟历史存量数据：候选关系不由增量路径
// 产生，而由随后的迁移回填统一建立。
func seedBenchRows(tb testing.TB, db *sql.DB, n int) {
	tb.Helper()
	rng := rand.New(rand.NewSource(1))

	providers := []struct {
		id, name, url string
	}{
		{"prov-anthropic", "Anthropic", "https://api.anthropic.com/v1/messages"},
		{"prov-openai", "OpenAI", "https://api.openai.com/v1/chat/completions"},
		{"prov-bedrock", "AWS Bedrock", "https://bedrock-runtime.us-east-1.amazonaws.com/model/invoke"},
		{"prov-vertex", "Google Vertex", "https://us-central1-aiplatform.googleapis.com/v1/predict"},
		{"prov-gateway", "Internal Gateway", "https://llm-gateway.internal.example.com/v1/messages"},
	}
	models := []string{
		"claude-opus-4-1", "claude-sonnet-4-5", "claude-sonnet-4", "claude-haiku-4-5",
		"claude-3-7-sonnet", "claude-3-5-haiku", "gpt-5", "gemini-2-5-pro",
	}
	entrypoints := []string{"cli", "ide", "sdk", "web"}
	paths := []string{"/v1/messages", "/v1/chat/completions", "/v1/complete"}
	// token 轮廓调色板：会话行镜像供应商行时复用同一轮廓，确保四类 token 计数完全相等，
	// 从而在 ±10 分钟窗口内命中去重指纹。
	type tokenProfile struct{ in, out, cacheCreate, cacheRead int64 }
	profiles := make([]tokenProfile, 0, 64)
	for i := 0; i < 64; i++ {
		profiles = append(profiles, tokenProfile{
			in:         int64(rng.Intn(8000) + 100),
			out:        int64(rng.Intn(4000) + 50),
			cacheCreate: int64(rng.Intn(2000)),
			cacheRead:  int64(rng.Intn(10000)),
		})
	}

	// 时间窗：以 benchFixedNow 为终点向前铺开 90 天，行按时间大致升序分布。
	windowStart := benchFixedNow.Add(-90 * 24 * time.Hour)
	windowSpan := 90 * 24 * time.Hour

	nSession := n * 18 / 100
	nProvider := n - nSession

	type providerRef struct {
		startedAt time.Time
		model     string
		profile   int
	}
	providerRefs := make([]providerRef, 0, nProvider)

	tx, err := db.Begin()
	if err != nil {
		tb.Fatalf("begin seed tx: %v", err)
	}
	reqStmt, err := tx.Prepare(
		`INSERT INTO usage_requests(
			id, started_at, ended_at, duration_ms, upstream_response_header_ms, time_to_first_byte_ms,
			status_code, error_type, error_message, method, request_path, backend_url,
			provider_id, provider_name, provider_api_url, source_app, source_entrypoint, user_agent,
			original_model, mapped_model, stream, request_bytes, response_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		tb.Fatalf("prepare request insert: %v", err)
	}
	tokStmt, err := tx.Prepare(
		`INSERT INTO usage_tokens(
			request_id, input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens,
			usage_source, usage_parse_status, usage_parse_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		tb.Fatalf("prepare token insert: %v", err)
	}

	insertProvider := func(id string, startedAt time.Time, provIdx, modelIdx, profileIdx int, failed bool) {
		prov := providers[provIdx]
		model := models[modelIdx]
		p := profiles[profileIdx]
		statusCode := 200
		errType := ""
		errMsg := ""
		parseStatus := ParseStatusOK
		usageSource := UsageSourceProvider
		if failed {
			statusCode = 529
			errType = ErrorUpstreamTimeout
			errMsg = "upstream timeout"
			parseStatus = ParseStatusSkippedNon2xx
			usageSource = UsageSourceNone
		}
		dur := int64(rng.Intn(4000) + 50)
		ended := startedAt.Add(time.Duration(dur) * time.Millisecond)
		if _, err := reqStmt.Exec(
			id, formatTime(startedAt), formatTime(ended), dur, int64(rng.Intn(300)), int64(rng.Intn(800)),
			statusCode, errType, errMsg, "POST", paths[rng.Intn(len(paths))], prov.url,
			prov.id, prov.name, prov.url, "claude_code", entrypoints[rng.Intn(len(entrypoints))], "claude-cli/1.0",
			model, model, 1, int64(rng.Intn(5000)+200), int64(rng.Intn(20000)+500),
		); err != nil {
			tb.Fatalf("insert provider request: %v", err)
		}
		if _, err := tokStmt.Exec(id, p.in, p.out, p.cacheCreate, p.cacheRead, usageSource, parseStatus, ""); err != nil {
			tb.Fatalf("insert provider token: %v", err)
		}
	}

	insertSession := func(id string, startedAt time.Time, model string, p tokenProfile, provIdx int) {
		prov := providers[provIdx]
		dur := int64(rng.Intn(4000) + 50)
		ended := startedAt.Add(time.Duration(dur) * time.Millisecond)
		if _, err := reqStmt.Exec(
			id, formatTime(startedAt), formatTime(ended), dur, int64(rng.Intn(300)), int64(rng.Intn(800)),
			200, "", "", "POST", "/v1/messages", prov.url,
			"_session", "Session Log", "", "claude_code", "session_log", "claude-cli/1.0",
			model, model, 1, int64(rng.Intn(5000)+200), int64(rng.Intn(20000)+500),
		); err != nil {
			tb.Fatalf("insert session request: %v", err)
		}
		if _, err := tokStmt.Exec(id, p.in, p.out, p.cacheCreate, p.cacheRead, UsageSourceSessionLog, ParseStatusOK, ""); err != nil {
			tb.Fatalf("insert session token: %v", err)
		}
	}

	// 供应商行：约 8% 失败（无 usage），其余为 ok 供应商候选。
	for i := 0; i < nProvider; i++ {
		startedAt := windowStart.Add(time.Duration(int64(float64(windowSpan) * float64(i) / float64(nProvider))))
		startedAt = startedAt.Add(time.Duration(rng.Intn(int(time.Second))) )
		modelIdx := rng.Intn(len(models))
		profileIdx := rng.Intn(len(profiles))
		provIdx := rng.Intn(len(providers))
		failed := rng.Intn(100) < 8
		id := fmt.Sprintf("bench-prov-%08d", i)
		insertProvider(id, startedAt, provIdx, modelIdx, profileIdx, failed)
		if !failed {
			providerRefs = append(providerRefs, providerRef{startedAt: startedAt, model: models[modelIdx], profile: profileIdx})
		}
	}

	// 会话行：约一半镜像某个供应商行（相同模型 + 相同 token 轮廓 + 邻近时间），确定性产生候选；
	// 另一半独立（不同 token 轮廓），不产生候选。
	for i := 0; i < nSession; i++ {
		var startedAt time.Time
		var model string
		var p tokenProfile
		provIdx := rng.Intn(len(providers))
		if len(providerRefs) > 0 && rng.Intn(100) < 50 {
			ref := providerRefs[rng.Intn(len(providerRefs))]
			// 偏移 ±5 分钟，落在 ±10 分钟去重窗口内。
			offset := time.Duration(rng.Intn(int(10*time.Minute))) - 5*time.Minute
			startedAt = ref.startedAt.Add(offset)
			model = ref.model
			p = profiles[ref.profile]
		} else {
			startedAt = windowStart.Add(time.Duration(rng.Int63n(int64(windowSpan))))
			model = models[rng.Intn(len(models))]
			p = profiles[rng.Intn(len(profiles))]
		}
		id := fmt.Sprintf("bench-sess-%08d", i)
		insertSession(id, startedAt, model, p, provIdx)
	}

	if err := reqStmt.Close(); err != nil {
		tb.Fatalf("close request stmt: %v", err)
	}
	if err := tokStmt.Close(); err != nil {
		tb.Fatalf("close token stmt: %v", err)
	}
	if err := tx.Commit(); err != nil {
		tb.Fatalf("commit seed tx: %v", err)
	}
}

// runMigration 计时一次完整的 Migrate（含候选回填），返回耗时与建立的候选数。
func (d *benchDataset) runMigration(tb testing.TB) (time.Duration, int) {
	tb.Helper()
	start := time.Now()
	if err := d.store.Migrate(); err != nil {
		tb.Fatalf("Migrate() with backfill: %v", err)
	}
	elapsed := time.Since(start)
	var candidates int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM usage_dedupe_candidates`).Scan(&candidates); err != nil {
		tb.Fatalf("count candidates: %v", err)
	}
	return elapsed, candidates
}

// benchFilter 返回基准读取使用的固定筛选（全量、UTC、固定 Now）。
func benchFilter() Filter {
	return Filter{TZ: "UTC", Now: benchFixedNow, StatsScope: StatsScopeEffective}
}

// medianDuration 返回 fn 多次运行耗时的中位数（去掉首次冷启动预热）。
func medianDuration(runs int, fn func() error) (time.Duration, error) {
	if runs < 1 {
		runs = 1
	}
	// 预热一次，填充页缓存与查询计划缓存，使测量贴近稳态控制面行为。
	if err := fn(); err != nil {
		return 0, err
	}
	samples := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		if err := fn(); err != nil {
			return 0, err
		}
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[len(samples)/2], nil
}

// profile 对已迁移的基准库执行完整测量，返回与 spec 目标表对应的各项指标。
func (d *benchDataset) profile(tb testing.TB) benchProfile {
	tb.Helper()
	filter := benchFilter()
	pageFilter := filter
	pageFilter.Page = 50
	pageFilter.PageSize = 50

	result := benchProfile{Rows: d.rows}

	summaryLat, err := medianDuration(7, func() error { _, err := d.store.Summary(filter); return err })
	if err != nil {
		tb.Fatalf("Summary: %v", err)
	}
	result.StatusSummary = summaryLat

	reqLat, err := medianDuration(7, func() error { _, err := d.store.Requests(pageFilter); return err })
	if err != nil {
		tb.Fatalf("Requests: %v", err)
	}
	result.RequestsPage50 = reqLat

	trendsLat, err := medianDuration(5, func() error { _, err := d.store.Trends(filter); return err })
	if err != nil {
		tb.Fatalf("Trends: %v", err)
	}
	result.Trends = trendsLat

	providersLat, err := medianDuration(5, func() error { _, err := d.store.Providers(filter); return err })
	if err != nil {
		tb.Fatalf("Providers: %v", err)
	}
	result.Providers = providersLat

	modelsLat, err := medianDuration(5, func() error { _, err := d.store.Models(filter); return err })
	if err != nil {
		tb.Fatalf("Models: %v", err)
	}
	result.Models = modelsLat

	coverageLat, err := medianDuration(5, func() error { _, err := d.store.Coverage(filter); return err })
	if err != nil {
		tb.Fatalf("Coverage: %v", err)
	}
	result.Coverage = coverageLat

	wall, err := medianDuration(5, func() error { return d.sixParallel(filter) })
	if err != nil {
		tb.Fatalf("six parallel: %v", err)
	}
	result.SixParallelWall = wall

	return result
}

// sixParallel 并发执行六个 usage 统计接口（Summary/Trends/Requests/Providers/Models/Coverage），
// 返回墙钟耗时，复现使用统计页首屏的并发负载。
func (d *benchDataset) sixParallel(filter Filter) error {
	pageFilter := filter
	pageFilter.Page = 1
	pageFilter.PageSize = 50
	ops := []func() error{
		func() error { _, err := d.store.Summary(filter); return err },
		func() error { _, err := d.store.Trends(filter); return err },
		func() error { _, err := d.store.Requests(pageFilter); return err },
		func() error { _, err := d.store.Providers(filter); return err },
		func() error { _, err := d.store.Models(filter); return err },
		func() error { _, err := d.store.Coverage(filter); return err },
	}
	var wg sync.WaitGroup
	errs := make([]error, len(ops))
	start := time.Now()
	for i, op := range ops {
		wg.Add(1)
		go func(i int, op func() error) {
			defer wg.Done()
			errs[i] = op()
		}(i, op)
	}
	wg.Wait()
	_ = time.Since(start)
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// TestUsagePerformanceProfile 构造 MCC_USAGE_BENCH_ROWS 行数据集，测量并以表格记录
// 迁移耗时、status(Summary) 单次延迟、Requests 第 50 页延迟、六个统计接口并发墙钟，
// 以及各接口单独延迟。无墙钟硬断言（非易波动）；未设置环境变量时跳过。
func TestUsagePerformanceProfile(t *testing.T) {
	n := benchRows(t)
	if n == 0 {
		t.Skip("set MCC_USAGE_BENCH_ROWS (e.g. 60000) to run the usage performance profile")
	}
	ds := newBenchDatasetInDir(t, t.TempDir(), n)

	migration, candidates := ds.runMigration(t)
	prof := ds.profile(t)
	prof.Migration = migration
	prof.Candidates = candidates

	t.Logf("\n==== Usage Performance Profile (rows=%d, deterministic seed=1) ====", n)
	t.Logf("candidates backfilled           : %d", prof.Candidates)
	t.Logf("migration incl. backfill        : %s", prof.Migration)
	t.Logf("GET /api/status (Summary) median: %s", prof.StatusSummary)
	t.Logf("Requests page 50 (LIMIT/OFFSET) : %s", prof.RequestsPage50)
	t.Logf("Trends median                   : %s", prof.Trends)
	t.Logf("Providers median                : %s", prof.Providers)
	t.Logf("Models median                   : %s", prof.Models)
	t.Logf("Coverage median                 : %s", prof.Coverage)
	t.Logf("six usage ops parallel wall     : %s", prof.SixParallelWall)
	t.Logf("==================================================================")
}

// 以下为 go test -bench 形式的基准，复用共享缓存数据集（已迁移至稳态），报告 ns/op。
// 数据集在首个基准触发时构造一次，避免重复构造主导耗时。同样由 MCC_USAGE_BENCH_ROWS 门控。
var (
	sharedBenchOnce sync.Once
	sharedBenchDS   *benchDataset
)

func sharedBenchDataset(b *testing.B, n int) *benchDataset {
	sharedBenchOnce.Do(func() {
		dir, err := os.MkdirTemp("", "usage-bench-shared")
		if err != nil {
			b.Fatalf("mkdir temp: %v", err)
		}
		ds := newBenchDatasetInDir(b, dir, n)
		// 迁移至稳态（含候选回填），后续基准只测读取路径。
		ds.runMigration(b)
		sharedBenchDS = ds
	})
	return sharedBenchDS
}

func BenchmarkUsageStatusSummary(b *testing.B) {
	n := benchRows(b)
	if n == 0 {
		b.Skip("set MCC_USAGE_BENCH_ROWS to benchmark")
	}
	ds := sharedBenchDataset(b, n)
	filter := benchFilter()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ds.store.Summary(filter); err != nil {
			b.Fatalf("Summary: %v", err)
		}
	}
}

func BenchmarkUsageRequestsPage50(b *testing.B) {
	n := benchRows(b)
	if n == 0 {
		b.Skip("set MCC_USAGE_BENCH_ROWS to benchmark")
	}
	ds := sharedBenchDataset(b, n)
	filter := benchFilter()
	filter.Page = 50
	filter.PageSize = 50
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ds.store.Requests(filter); err != nil {
			b.Fatalf("Requests: %v", err)
		}
	}
}

func BenchmarkUsageSixParallel(b *testing.B) {
	n := benchRows(b)
	if n == 0 {
		b.Skip("set MCC_USAGE_BENCH_ROWS to benchmark")
	}
	ds := sharedBenchDataset(b, n)
	filter := benchFilter()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ds.sixParallel(filter); err != nil {
			b.Fatalf("sixParallel: %v", err)
		}
	}
}
