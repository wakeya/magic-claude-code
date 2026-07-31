package usage

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestBuildSummaryQueryAggregatesScopedDataset 验证 Summary 的查询结构：聚合下推到
// scoped SQL（COUNT/SUM），窄字段投影（不读取/解析 URL 等宽行字段），筛选与今日边界
// 全部参数化且顺序稳定。该测试在 SQL 聚合实现缺失时因 buildSummaryQuery 未定义而失败。
func TestBuildSummaryQueryAggregatesScopedDataset(t *testing.T) {
	from := time.Date(2026, 7, 30, 1, 2, 3, 4, time.UTC)
	to := from.Add(time.Hour)
	filter := Filter{
		From:             from,
		To:               to,
		SourceApp:        "source-sentinel",
		SourceEntrypoint: "entrypoint-sentinel",
		ProviderID:       "provider-sentinel",
		Model:            "model-sentinel",
		Status:           "success",
		UsageSource:      "usage-source-sentinel",
		UsageParseStatus: "parse-status-sentinel",
		RequestPath:      "path-sentinel",
		Query:            "query-sentinel_%",
		StatsScope:       StatsScopeRaw,
	}
	start := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)

	query, args := buildSummaryQuery(filter, start, end)

	wantArgs := []any{
		formatTime(from),
		formatTime(to),
		"source-sentinel",
		"entrypoint-sentinel",
		"provider-sentinel",
		"model-sentinel",
		"path-sentinel",
		"usage-source-sentinel",
		"parse-status-sentinel",
		"%query-sentinel_%%",
		"%query-sentinel_%%",
		"%query-sentinel_%%",
		"%query-sentinel_%%",
		"%query-sentinel_%%",
		StatsScopeRaw,
		start.Unix(),
		end.Unix(),
		start.Unix(),
		end.Unix(),
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	if strings.Count(query, "?") != len(args) {
		t.Fatalf("placeholder count = %d, args = %d", strings.Count(query, "?"), len(args))
	}

	for _, want := range []string{"COUNT(*)", "SUM(", "filtered AS", "candidate AS", "scoped AS", "r.error_type", "r.status_code", "t.input_tokens"} {
		if !strings.Contains(query, want) {
			t.Fatalf("summary query missing %q:\n%s", want, query)
		}
	}
	// 窄字段投影：聚合不读取宽行字段，尤其不读取/解析 URL（R5）。
	for _, banned := range []string{"r.user_agent", "r.backend_url", "r.method", "r.duration_ms", "r.request_bytes", "r.response_bytes"} {
		if strings.Contains(query, banned) {
			t.Fatalf("summary query must not project wide field %q:\n%s", banned, query)
		}
	}
	for _, value := range []string{
		"source-sentinel",
		"entrypoint-sentinel",
		"provider-sentinel",
		"model-sentinel",
		"path-sentinel",
		"usage-source-sentinel",
		"parse-status-sentinel",
		"query-sentinel",
	} {
		if strings.Contains(query, value) {
			t.Fatalf("summary query contains unparameterized filter value %q", value)
		}
	}
}

// TestSummarySQLAggregationMatchesLegacyOracle 在覆盖筛选/口径/去重回退/失败分类/
// 小数秒与今日边界的差分数据上，逐字段比较 SQL 聚合 Summary 与 test-only 旧算法
// 判定器（legacyOracleSummary），保证下推 SQLite 后公开结果与旧实现完全一致。
func TestSummarySQLAggregationMatchesLegacyOracle(t *testing.T) {
	store := newTestStore(t)
	seedSummaryDifferentialFixture(t, store)
	now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)

	filters := []struct {
		name   string
		filter Filter
	}{
		{name: "all"},
		{name: "from today", filter: Filter{From: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)}},
		{name: "from drops primary candidate", filter: Filter{From: time.Date(2026, 7, 30, 12, 0, 10, 0, time.UTC)}},
		{name: "to", filter: Filter{To: time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)}},
		{name: "source app claude", filter: Filter{SourceApp: "claude_code"}},
		{name: "source app other", filter: Filter{SourceApp: "other-app"}},
		{name: "provider a", filter: Filter{ProviderID: "provider-a"}},
		{name: "provider b", filter: Filter{ProviderID: "provider-b"}},
		{name: "model mapped", filter: Filter{Model: "mapped-model"}},
		{name: "model b", filter: Filter{Model: "model-b"}},
		{name: "path messages", filter: Filter{RequestPath: "/v1/messages"}},
		{name: "path complete", filter: Filter{RequestPath: "/v1/complete"}},
		{name: "status success", filter: Filter{Status: "success"}},
		{name: "status error", filter: Filter{Status: "error"}},
		{name: "usage provider", filter: Filter{UsageSource: UsageSourceProvider}},
		{name: "usage session", filter: Filter{UsageSource: UsageSourceSessionLog}},
		{name: "parse ok", filter: Filter{UsageParseStatus: ParseStatusOK}},
		{name: "parse missing", filter: Filter{UsageParseStatus: ParseStatusMissing}},
		{name: "search needle", filter: Filter{Query: "Summary Needle"}},
		{name: "zero results", filter: Filter{Query: "does-not-exist"}},
		{name: "shanghai tz", filter: Filter{TZ: "Asia/Shanghai"}},
	}
	scopes := []string{
		"",
		StatsScopeEffective,
		StatsScopeProvider,
		StatsScopeSessionLog,
		StatsScopeRaw,
	}

	for _, filterCase := range filters {
		for _, scope := range scopes {
			filter := filterCase.filter
			filter.StatsScope = scope
			if filter.TZ == "" {
				filter.TZ = "UTC"
			}
			filter.Now = now
			t.Run(filterCase.name+"/"+scopeName(scope), func(t *testing.T) {
				got, err := store.Summary(filter)
				if err != nil {
					t.Fatalf("Summary() error = %v", err)
				}
				want := legacyOracleSummary(t, store.db, filter)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("Summary() = %#v, want legacy %#v", got, want)
				}
			})
		}
	}
}

// TestSummarySQLAggregationAnchorsExpectedTotals 固定一组手工推导的期望值，锚定
// effective 口径下的绝对正确性，防止差分双方（SQL 与判定器）同向偏离旧契约。
func TestSummarySQLAggregationAnchorsExpectedTotals(t *testing.T) {
	store := newTestStore(t)
	seedSummaryDifferentialFixture(t, store)

	got, err := store.Summary(Filter{
		TZ:         "UTC",
		Now:        time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC),
		StatsScope: StatsScopeEffective,
	})
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}

	if got.ProviderRequestsTotal != 12 {
		t.Fatalf("ProviderRequestsTotal = %d, want 12", got.ProviderRequestsTotal)
	}
	if got.TokenConsumptionTotal != 163 {
		t.Fatalf("TokenConsumptionTotal = %d, want 163", got.TokenConsumptionTotal)
	}
	if got.FailedRequests != 3 {
		t.Fatalf("FailedRequests = %d, want 3", got.FailedRequests)
	}
	if got.TodayProviderRequests != 9 {
		t.Fatalf("TodayProviderRequests = %d, want 9", got.TodayProviderRequests)
	}
	if got.TodayTokenConsumption != 62 {
		t.Fatalf("TodayTokenConsumption = %d, want 62", got.TodayTokenConsumption)
	}
	if got.UsageCoverage != 0.75 {
		t.Fatalf("UsageCoverage = %v, want 0.75", got.UsageCoverage)
	}
	wantLast := time.Date(2026, 7, 30, 18, 0, 0, 750000000, time.UTC)
	if got.LastProviderRequest == nil || !got.LastProviderRequest.Equal(wantLast) {
		t.Fatalf("LastProviderRequest = %v, want %v", got.LastProviderRequest, wantLast)
	}
}

// TestSummarySQLAggregationEmptyDataset 确保零结果时 SQL 聚合返回与旧实现一致的
// 零值 Summary（LastProviderRequest 为 nil、UsageCoverage 为 0）。
func TestSummarySQLAggregationEmptyDataset(t *testing.T) {
	store := newTestStore(t)
	seedSummaryDifferentialFixture(t, store)

	filter := Filter{
		TZ:         "UTC",
		Now:        time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC),
		Query:      "does-not-exist",
		StatsScope: StatsScopeEffective,
	}
	got, err := store.Summary(filter)
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	want := Summary{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Summary() = %#v, want zero %#v", got, want)
	}
}

// TestSummarySQLAggregationTreatsInvalidHistoricalTimeAsGoZeroTime 确保非法历史时间戳
// 被 parseTime 容错为 Go 零值时间后，SQL 聚合与旧算法一致：不计入今日、不作为最新
// 请求，但计数与 token 总和仍包含；全部行非法时 LastProviderRequest 为非 nil 零值。
func TestSummarySQLAggregationTreatsInvalidHistoricalTimeAsGoZeroTime(t *testing.T) {
	filter := Filter{TZ: "UTC", Now: time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC), StatsScope: StatsScopeEffective}

	store := newLegacyUsageStore(t)
	valid := dedupeProviderRequest("prov-valid", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC), "model", "model")
	recordDedupeHistory(t, store, valid, dedupeToken(valid.ID, UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 4, OutputTokens: 6}))
	invalid := dedupeProviderRequest("prov-invalid", time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC), "model", "model")
	recordDedupeHistory(t, store, invalid, dedupeToken(invalid.ID, UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1, OutputTokens: 2}))
	if _, err := store.db.Exec(`UPDATE usage_requests SET started_at = 'invalid-history-time' WHERE id = ?`, invalid.ID); err != nil {
		t.Fatalf("make timestamp invalid: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	got, err := store.Summary(filter)
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	want := legacyOracleSummary(t, store.db, filter)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Summary() = %#v, want legacy %#v", got, want)
	}
	if got.ProviderRequestsTotal != 2 {
		t.Fatalf("ProviderRequestsTotal = %d, want 2", got.ProviderRequestsTotal)
	}
	if got.TokenConsumptionTotal != 13 {
		t.Fatalf("TokenConsumptionTotal = %d, want 13", got.TokenConsumptionTotal)
	}
	if got.TodayProviderRequests != 1 || got.TodayTokenConsumption != 10 {
		t.Fatalf("today = %d/%d, want 1/10 (only the valid row)", got.TodayProviderRequests, got.TodayTokenConsumption)
	}
	if got.LastProviderRequest == nil || !got.LastProviderRequest.Equal(time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("LastProviderRequest = %v, want valid row time", got.LastProviderRequest)
	}

	// 全部行时戳非法：LastProviderRequest 为非 nil 的 Go 零值时间（旧实现“有行即非 nil”）。
	onlyStore := newLegacyUsageStore(t)
	only := dedupeProviderRequest("prov-only-invalid", time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC), "model", "model")
	recordDedupeHistory(t, onlyStore, only, dedupeToken(only.ID, UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1}))
	if _, err := onlyStore.db.Exec(`UPDATE usage_requests SET started_at = 'invalid-history-time' WHERE id = ?`, only.ID); err != nil {
		t.Fatalf("make only timestamp invalid: %v", err)
	}
	if err := onlyStore.Migrate(); err != nil {
		t.Fatalf("Migrate() only-store error = %v", err)
	}
	gotOnly, err := onlyStore.Summary(filter)
	if err != nil {
		t.Fatalf("Summary() only-store error = %v", err)
	}
	wantOnly := legacyOracleSummary(t, onlyStore.db, filter)
	if !reflect.DeepEqual(gotOnly, wantOnly) {
		t.Fatalf("only-invalid Summary() = %#v, want legacy %#v", gotOnly, wantOnly)
	}
	if gotOnly.LastProviderRequest == nil || !gotOnly.LastProviderRequest.IsZero() {
		t.Fatalf("only-invalid LastProviderRequest = %v, want non-nil zero time", gotOnly.LastProviderRequest)
	}
}

// seedSummaryDifferentialFixture 构造覆盖去重/口径/失败分类/有无 usage/小数秒今日
// 边界与跨天时间的差分数据。effective 口径共 12 行（session-dup 被去重排除），
// 与 TestSummarySQLAggregationAnchorsExpectedTotals 的手工期望值绑定。
func seedSummaryDifferentialFixture(t *testing.T, store *Store) {
	t.Helper()
	base := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	record := func(req RequestRecord, tok TokenRecord) {
		t.Helper()
		if err := store.Record(req, tok); err != nil {
			t.Fatalf("Record(%q) error = %v", req.ID, err)
		}
	}

	// 1. 有 usage 的成功 provider 行（基准时间）。
	record(testUsageRequest("prov-usage", base), dedupeToken(
		"prov-usage", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 3, CacheReadInputTokens: 2},
	))

	// 2. 全库最新行（小数秒），兼作搜索 needle 与 provider-b/model-b 维度。
	latest := testUsageRequest("prov-usage-fraction-max", time.Date(2026, 7, 30, 18, 0, 0, 750000000, time.UTC))
	latest.ProviderID = "provider-b"
	latest.ProviderName = "Summary Needle Provider"
	latest.ProviderAPIURL = "https://provider-b.example.com"
	latest.MappedModel = "model-b"
	latest.OriginalModel = "model-b"
	latest.RequestPath = "/v1/complete"
	record(latest, dedupeToken(
		"prov-usage-fraction-max", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 1, OutputTokens: 1},
	))

	// 3. 与 #2 同整秒但更早的整秒行，验证小数秒排序取最新。
	record(testUsageRequest("prov-usage-whole-second", time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)), dedupeToken(
		"prov-usage-whole-second", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 2, OutputTokens: 2},
	))

	// 4. 无 usage（none/missing），other-app 维度。
	noUsage := testUsageRequest("prov-no-usage", base.Add(time.Hour))
	noUsage.SourceApp = "other-app"
	noUsage.SourceEntrypoint = "other-entry"
	record(noUsage, dedupeToken("prov-no-usage", UsageSourceNone, ParseStatusMissing, UsageValues{}))

	// 5. 失败：error_type + 非 2xx。
	failedErr := testUsageRequest("prov-failed-errortype", base.Add(2*time.Hour))
	statusFailed := 500
	failedErr.StatusCode = &statusFailed
	failedErr.ErrorType = ErrorHTTP
	record(failedErr, dedupeToken("prov-failed-errortype", UsageSourceNone, ParseStatusSkippedNon2xx, UsageValues{}))

	// 6. 失败：NULL 状态码，但 usage 解析成功（失败与有 usage 独立）。
	failedNull := testUsageRequest("prov-failed-null-status", base.Add(3*time.Hour))
	failedNull.StatusCode = nil
	record(failedNull, dedupeToken(
		"prov-failed-null-status", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 5},
	))

	// 7. 前一天非 2xx。
	failedNon2xx := testUsageRequest("prov-failed-non2xx", time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	statusNon2xx := 404
	failedNon2xx.StatusCode = &statusNon2xx
	record(failedNon2xx, dedupeToken("prov-failed-non2xx", UsageSourceNone, ParseStatusMissing, UsageValues{}))

	// 8. 与 #1 重复的会话行（effective 排除）。
	record(dedupeSessionRequest("session-dup", base.Add(30*time.Second), "mapped-model", "mapped-model"), dedupeToken(
		"session-dup", UsageSourceSessionLog, ParseStatusOK,
		UsageValues{InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 3, CacheReadInputTokens: 2},
	))

	// 9. 独立会话行（无候选）。
	record(dedupeSessionRequest("session-unique", base.Add(4*time.Hour), "session-only-model", "session-only-model"), dedupeToken(
		"session-unique", UsageSourceSessionLog, ParseStatusOK,
		UsageValues{InputTokens: 7, OutputTokens: 3},
	))

	// 10. 前一天有 usage 行。
	record(testUsageRequest("prov-prev-day-usage", time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)), dedupeToken(
		"prov-prev-day-usage", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 100},
	))

	// 11. 午夜后小数秒（今日）。
	record(testUsageRequest("prov-midnight-plus", time.Date(2026, 7, 30, 0, 0, 0, 500000000, time.UTC)), dedupeToken(
		"prov-midnight-plus", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 1},
	))

	// 12. 午夜前小数秒（前一天）。
	record(testUsageRequest("prov-midnight-minus", time.Date(2026, 7, 29, 23, 59, 59, 500000000, time.UTC)), dedupeToken(
		"prov-midnight-minus", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 1},
	))

	// 13. session-dup 的备选候选（provider-b，晚 5 分钟），用于筛选后候选回退。
	fallback := testUsageRequest("prov-usage-fallback", base.Add(5*time.Minute))
	fallback.ProviderID = "provider-b"
	fallback.ProviderName = "Provider B"
	fallback.ProviderAPIURL = "https://provider-b.example.com"
	record(fallback, dedupeToken(
		"prov-usage-fallback", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 3, CacheReadInputTokens: 2},
	))
}
