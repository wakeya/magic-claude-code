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

// TestBuildTrendsQueryAggregatesScopedDataset 验证 Trends 的查询结构：日桶 GROUP BY
// 下推到 scoped SQL（COUNT/SUM），窄字段投影（不读取/解析 URL 等宽行字段），筛选参数化
// 且时区桶表达式参数顺序稳定。该测试在 SQL 桶聚合实现缺失时因 buildTrendsQuery 未定义
// 而失败。
func TestBuildTrendsQueryAggregatesScopedDataset(t *testing.T) {
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
	intervals := []scopedZoneInterval{{start: 1000, offset: 0}, {start: 2000, offset: 3600}}

	query, args := buildTrendsQuery(filter, time.UTC, intervals)

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
		"0001-01-01",
		int64(2000),
		0,
		3600,
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	if strings.Count(query, "?") != len(args) {
		t.Fatalf("placeholder count = %d, args = %d", strings.Count(query, "?"), len(args))
	}

	for _, want := range []string{"COUNT(*)", "SUM(", "filtered AS", "candidate AS", "scoped AS", "GROUP BY bucket", "ORDER BY bucket ASC", "'unixepoch'", "r.error_type", "r.status_code", "t.input_tokens"} {
		if !strings.Contains(query, want) {
			t.Fatalf("trends query missing %q:\n%s", want, query)
		}
	}
	// 窄字段投影：聚合不读取宽行字段，尤其不读取/解析 URL（R5）。
	// 注：r.provider_name/r.provider_api_url/r.error_message 由共享搜索筛选 WHERE
	// 引用（与 Summary 一致），不属于投影字段，不在禁止列表。
	for _, banned := range []string{"r.user_agent", "r.backend_url", "r.method", "r.duration_ms", "r.request_bytes", "r.response_bytes", "r.ended_at", "r.stream", "r.original_model"} {
		if strings.Contains(query, banned) {
			t.Fatalf("trends query must not project wide field %q:\n%s", banned, query)
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
			t.Fatalf("trends query contains unparameterized filter value %q", value)
		}
	}

	rangeQuery, rangeArgs := buildTrendsRangeQuery(filter)
	wantRangeArgs := wantArgs[:15]
	if !reflect.DeepEqual(rangeArgs, wantRangeArgs) {
		t.Fatalf("range args = %#v, want %#v", rangeArgs, wantRangeArgs)
	}
	for _, want := range []string{"MIN(", "MAX(", "filtered AS", "scoped", "r.started_at"} {
		if !strings.Contains(rangeQuery, want) {
			t.Fatalf("trends range query missing %q:\n%s", want, rangeQuery)
		}
	}
}

// TestScopedZoneOffsetIntervalsDetectsDSTTransitions 锚定时区偏移区间推导：UTC 单区间、
// America/New_York 夏令时开始/结束各产生一个精确到秒的区间边界、半小时/45 分钟偏移区
// 返回正确偏移、单日内切换也能定位。
func TestScopedZoneOffsetIntervalsDetectsDSTTransitions(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}

	// UTC：无切换，单区间零偏移。
	utcIntervals := scopedZoneOffsetIntervals(time.UTC, 0, 365*86400)
	if len(utcIntervals) != 1 || utcIntervals[0].offset != 0 {
		t.Fatalf("UTC intervals = %#v, want single zero-offset", utcIntervals)
	}

	// 纽约 2026-03-08T07:00:00Z 夏令时开始（EST -5 → EDT -4）。
	springStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix()
	springEnd := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC).Unix()
	spring := scopedZoneOffsetIntervals(ny, springStart, springEnd)
	wantSpring := []scopedZoneInterval{
		{start: springStart, offset: -18000},
		{start: time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC).Unix(), offset: -14400},
	}
	if !reflect.DeepEqual(spring, wantSpring) {
		t.Fatalf("spring intervals = %#v, want %#v", spring, wantSpring)
	}

	// 纽约 2026-11-01T06:00:00Z 夏令时结束（EDT -4 → EST -5）。
	fallStart := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC).Unix()
	fallEnd := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC).Unix()
	fall := scopedZoneOffsetIntervals(ny, fallStart, fallEnd)
	wantFall := []scopedZoneInterval{
		{start: fallStart, offset: -14400},
		{start: time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC).Unix(), offset: -18000},
	}
	if !reflect.DeepEqual(fall, wantFall) {
		t.Fatalf("fall intervals = %#v, want %#v", fall, wantFall)
	}

	// 切换发生在单日窗口内也能精确定位。
	dayStart := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC).Unix()
	dayOf := scopedZoneOffsetIntervals(ny, dayStart, time.Date(2026, 3, 8, 23, 59, 59, 0, time.UTC).Unix())
	wantDay := []scopedZoneInterval{
		{start: dayStart, offset: -18000},
		{start: time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC).Unix(), offset: -14400},
	}
	if !reflect.DeepEqual(dayOf, wantDay) {
		t.Fatalf("same-day intervals = %#v, want %#v", dayOf, wantDay)
	}

	// 半小时偏移（Lord Howe 三月为 +11）与 45 分钟偏移（Chatham 三月为 +13:45）。
	lordHowe, err := time.LoadLocation("Australia/Lord_Howe")
	if err != nil {
		t.Fatalf("load Australia/Lord_Howe: %v", err)
	}
	chatham, err := time.LoadLocation("Pacific/Chatham")
	if err != nil {
		t.Fatalf("load Pacific/Chatham: %v", err)
	}
	marchStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix()
	marchEnd := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC).Unix()
	if got := scopedZoneOffsetIntervals(lordHowe, marchStart, marchEnd); len(got) != 1 || got[0].offset != 39600 {
		t.Fatalf("Lord Howe intervals = %#v, want single +11h", got)
	}
	if got := scopedZoneOffsetIntervals(chatham, marchStart, marchEnd); len(got) != 1 || got[0].offset != 49500 {
		t.Fatalf("Chatham intervals = %#v, want single +13:45", got)
	}

	// 单秒范围：单区间。
	if got := scopedZoneOffsetIntervals(ny, springStart, springStart); len(got) != 1 || got[0].start != springStart || got[0].offset != -18000 {
		t.Fatalf("single-second intervals = %#v", got)
	}
}

// TestTrendsSQLAggregationMatchesLegacyOracle 在覆盖筛选/口径/去重回退/失败分类/
// 小数秒与跨天时间的差分数据上，逐字段比较 SQL 桶聚合 Trends 与 test-only 旧算法
// 判定器（legacyOracleTrends），保证下推 SQLite 后公开结果与旧实现完全一致。
func TestTrendsSQLAggregationMatchesLegacyOracle(t *testing.T) {
	store := newTestStore(t)
	seedTrendsDifferentialFixture(t, store)

	filters := []struct {
		name   string
		filter Filter
	}{
		{name: "all"},
		{name: "from drops primary candidate", filter: Filter{From: time.Date(2026, 3, 6, 10, 0, 10, 0, time.UTC)}},
		{name: "to", filter: Filter{To: time.Date(2026, 3, 7, 13, 0, 0, 0, time.UTC)}},
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
		{name: "search needle", filter: Filter{Query: "Trends Needle"}},
		{name: "zero results", filter: Filter{Query: "does-not-exist"}},
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
			filter.TZ = "UTC"
			t.Run(filterCase.name+"/"+scopeName(scope), func(t *testing.T) {
				got, err := store.Trends(filter)
				if err != nil {
					t.Fatalf("Trends() error = %v", err)
				}
				want := legacyOracleTrends(t, store.db, filter)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("Trends() = %#v, want legacy %#v", got, want)
				}
			})
		}
	}
}

// TestTrendsSQLAggregationMatchesLegacyOracleAcrossTimezones 在 UTC、固定偏移、
// 半小时/45 分钟偏移、含夏令时切换（含服务器本地时区）上差分验证日桶边界：
// 桶标签 = StartedAt.In(loc) 的本地日期，切换日两侧的行必须落入正确本地日期。
func TestTrendsSQLAggregationMatchesLegacyOracleAcrossTimezones(t *testing.T) {
	store := newTestStore(t)
	seedTrendsDifferentialFixture(t, store)

	timezones := []string{
		"",
		"UTC",
		"Asia/Shanghai",
		"America/New_York",
		"Pacific/Chatham",
		"Australia/Lord_Howe",
		"Pacific/Kiritimati",
	}
	filters := []struct {
		name   string
		filter Filter
	}{
		{name: "all"},
		{name: "from drops primary candidate", filter: Filter{From: time.Date(2026, 3, 6, 10, 0, 10, 0, time.UTC)}},
		{name: "status error", filter: Filter{Status: "error"}},
		{name: "usage session", filter: Filter{UsageSource: UsageSourceSessionLog}},
		{name: "search needle", filter: Filter{Query: "Trends Needle"}},
	}
	scopes := []string{
		"",
		StatsScopeEffective,
		StatsScopeProvider,
		StatsScopeSessionLog,
		StatsScopeRaw,
	}

	for _, tz := range timezones {
		for _, filterCase := range filters {
			for _, scope := range scopes {
				filter := filterCase.filter
				filter.StatsScope = scope
				filter.TZ = tz
				name := tz
				if name == "" {
					name = "local"
				}
				t.Run(name+"/"+filterCase.name+"/"+scopeName(scope), func(t *testing.T) {
					got, err := store.Trends(filter)
					if err != nil {
						t.Fatalf("Trends() error = %v", err)
					}
					want := legacyOracleTrends(t, store.db, filter)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("Trends() = %#v, want legacy %#v", got, want)
					}
				})
			}
		}
	}
}

// TestTrendsSQLAggregationAnchorsExpectedBuckets 固定一组手工推导的期望桶，锚定
// effective 口径下的绝对正确性（UTC 全量桶序列 + 纽约夏令时切换日桶边界），防止
// 差分双方（SQL 与判定器）同向偏离旧契约。
func TestTrendsSQLAggregationAnchorsExpectedBuckets(t *testing.T) {
	store := newTestStore(t)
	seedTrendsDifferentialFixture(t, store)

	got, err := store.Trends(Filter{TZ: "UTC", StatsScope: StatsScopeEffective})
	if err != nil {
		t.Fatalf("Trends() error = %v", err)
	}
	want := []TrendPoint{
		{Bucket: "0001-01-01", InputTokens: 1, OutputTokens: 2, TokenConsumptionTotal: 3, ProviderRequestsTotal: 1, UsageCoverage: 1},
		{Bucket: "2026-03-05", InputTokens: 100, TokenConsumptionTotal: 100, ProviderRequestsTotal: 1, UsageCoverage: 1},
		{Bucket: "2026-03-06", InputTokens: 20, OutputTokens: 10, CacheCreationInputTokens: 6, CacheReadInputTokens: 4, TokenConsumptionTotal: 40, ProviderRequestsTotal: 2, UsageCoverage: 1},
		{Bucket: "2026-03-07", InputTokens: 3, OutputTokens: 1, TokenConsumptionTotal: 4, ProviderRequestsTotal: 3, UsageCoverage: 2.0 / 3.0},
		{Bucket: "2026-03-08", InputTokens: 9, TokenConsumptionTotal: 9, ProviderRequestsTotal: 4, FailedRequests: 3, UsageCoverage: 0.5},
		{Bucket: "2026-03-09", InputTokens: 7, OutputTokens: 3, TokenConsumptionTotal: 10, ProviderRequestsTotal: 1, UsageCoverage: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UTC Trends() = %#v, want %#v", got, want)
	}

	// 纽约：2026-03-08T07:00:00Z 夏令时开始。UTC 03-08T04:59:59Z 的行在纽约仍是
	// 03-07 23:59:59 EST（桶 2026-03-07）；06:59:59.25Z 是切换前最后一小时（EST），
	// 07:00:00.75Z 是切换后第一小时（EDT），两者本地日期都是 2026-03-08。
	nyPoints, err := store.Trends(Filter{TZ: "America/New_York", StatsScope: StatsScopeEffective})
	if err != nil {
		t.Fatalf("Trends(New_York) error = %v", err)
	}
	byBucket := make(map[string]TrendPoint, len(nyPoints))
	for _, point := range nyPoints {
		byBucket[point.Bucket] = point
	}
	wantNY7 := TrendPoint{Bucket: "2026-03-07", InputTokens: 3, OutputTokens: 1, TokenConsumptionTotal: 4, ProviderRequestsTotal: 4, FailedRequests: 1, UsageCoverage: 0.5}
	if !reflect.DeepEqual(byBucket["2026-03-07"], wantNY7) {
		t.Fatalf("NY 2026-03-07 = %#v, want %#v", byBucket["2026-03-07"], wantNY7)
	}
	wantNY8 := TrendPoint{Bucket: "2026-03-08", InputTokens: 9, TokenConsumptionTotal: 9, ProviderRequestsTotal: 3, FailedRequests: 2, UsageCoverage: 2.0 / 3.0}
	if !reflect.DeepEqual(byBucket["2026-03-08"], wantNY8) {
		t.Fatalf("NY 2026-03-08 = %#v, want %#v", byBucket["2026-03-08"], wantNY8)
	}
}

// TestTrendsSQLAggregationEmptyDataset 确保零结果时返回非 nil 空切片（JSON [] 而非
// null），与旧实现 make([]TrendPoint, 0, ...) 的形状一致。
func TestTrendsSQLAggregationEmptyDataset(t *testing.T) {
	store := newTestStore(t)
	seedTrendsDifferentialFixture(t, store)

	filter := Filter{TZ: "UTC", Query: "does-not-exist", StatsScope: StatsScopeEffective}
	got, err := store.Trends(filter)
	if err != nil {
		t.Fatalf("Trends() error = %v", err)
	}
	if got == nil {
		t.Fatalf("Trends() = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("Trends() = %#v, want empty", got)
	}
	want := legacyOracleTrends(t, store.db, filter)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Trends() = %#v, want legacy %#v", got, want)
	}
}

// TestTrendsSQLAggregationLeavesMissingBucketsAbsent 确保缺失桶不被补零：只有有数据
// 的本地日期出现在结果中，且按桶字符串升序排列。
func TestTrendsSQLAggregationLeavesMissingBucketsAbsent(t *testing.T) {
	store := newTestStore(t)
	seedUsageRecord(t, store, "gap-day-1", time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC), 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1})
	seedUsageRecord(t, store, "gap-day-3", time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC), 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 2})

	got, err := store.Trends(Filter{TZ: "UTC", StatsScope: StatsScopeEffective})
	if err != nil {
		t.Fatalf("Trends() error = %v", err)
	}
	if len(got) != 2 || got[0].Bucket != "2026-04-01" || got[1].Bucket != "2026-04-03" {
		t.Fatalf("Trends() buckets = %#v, want only 2026-04-01 and 2026-04-03", got)
	}
}

// TestTrendsSQLAggregationTreatsInvalidHistoricalTimeAsGoZeroTime 确保非法历史时间戳
// 被 parseTime 容错为 Go 零值时间后，SQL 桶聚合与旧算法一致：桶标签为零值时间的本地
// 日期（UTC 为 0001-01-01），计数与 token 总和仍包含该行。
func TestTrendsSQLAggregationTreatsInvalidHistoricalTimeAsGoZeroTime(t *testing.T) {
	filter := Filter{TZ: "UTC", StatsScope: StatsScopeEffective}

	store := newTestStore(t)
	valid := dedupeProviderRequest("prov-valid", time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC), "model", "model")
	if err := store.Record(valid, dedupeToken(valid.ID, UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 4, OutputTokens: 6})); err != nil {
		t.Fatalf("Record(valid) error = %v", err)
	}
	invalid := dedupeProviderRequest("prov-invalid", time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC), "model", "model")
	if err := store.Record(invalid, dedupeToken(invalid.ID, UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1, OutputTokens: 2})); err != nil {
		t.Fatalf("Record(invalid) error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE usage_requests SET started_at = 'invalid-history-time' WHERE id = ?`, invalid.ID); err != nil {
		t.Fatalf("make timestamp invalid: %v", err)
	}

	got, err := store.Trends(filter)
	if err != nil {
		t.Fatalf("Trends() error = %v", err)
	}
	want := legacyOracleTrends(t, store.db, filter)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Trends() = %#v, want legacy %#v", got, want)
	}
	wantPoints := []TrendPoint{
		{Bucket: "0001-01-01", InputTokens: 1, OutputTokens: 2, TokenConsumptionTotal: 3, ProviderRequestsTotal: 1, UsageCoverage: 1},
		{Bucket: "2026-07-30", InputTokens: 4, OutputTokens: 6, TokenConsumptionTotal: 10, ProviderRequestsTotal: 1, UsageCoverage: 1},
	}
	if !reflect.DeepEqual(got, wantPoints) {
		t.Fatalf("Trends() = %#v, want %#v", got, wantPoints)
	}

	// 全部行时戳非法：单桶为零值时间本地日期（负偏移时区为 0000-12-31），差分一致。
	onlyStore := newTestStore(t)
	only := dedupeProviderRequest("prov-only-invalid", time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC), "model", "model")
	if err := onlyStore.Record(only, dedupeToken(only.ID, UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1})); err != nil {
		t.Fatalf("Record(only) error = %v", err)
	}
	if _, err := onlyStore.db.Exec(`UPDATE usage_requests SET started_at = 'invalid-history-time' WHERE id = ?`, only.ID); err != nil {
		t.Fatalf("make only timestamp invalid: %v", err)
	}
	for _, tz := range []string{"UTC", "America/New_York"} {
		onlyFilter := Filter{TZ: tz, StatsScope: StatsScopeEffective}
		gotOnly, err := onlyStore.Trends(onlyFilter)
		if err != nil {
			t.Fatalf("Trends(%s) error = %v", tz, err)
		}
		wantOnly := legacyOracleTrends(t, onlyStore.db, onlyFilter)
		if !reflect.DeepEqual(gotOnly, wantOnly) {
			t.Fatalf("only-invalid Trends(%s) = %#v, want legacy %#v", tz, gotOnly, wantOnly)
		}
		if len(gotOnly) != 1 || gotOnly[0].ProviderRequestsTotal != 1 {
			t.Fatalf("only-invalid Trends(%s) = %#v, want single bucket", tz, gotOnly)
		}
	}
}

// seedTrendsDifferentialFixture 构造覆盖多日/跨天、夏令时切换日两侧、小数秒桶边界、
// 去重与候选回退、失败分类、有无 usage 与非法时戳的差分数据。effective 口径共 12 行
// （trend-session-dup 被去重排除），与 TestTrendsSQLAggregationAnchorsExpectedBuckets
// 的手工期望桶绑定。
func seedTrendsDifferentialFixture(t *testing.T, store *Store) {
	t.Helper()
	record := func(req RequestRecord, tok TokenRecord) {
		t.Helper()
		if err := store.Record(req, tok); err != nil {
			t.Fatalf("Record(%q) error = %v", req.ID, err)
		}
	}

	// 1. 有 usage 的成功 provider 行（基准时间）。
	record(testUsageRequest("trend-usage-a", time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)), dedupeToken(
		"trend-usage-a", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 3, CacheReadInputTokens: 2},
	))

	// 2. 小数秒行，兼作搜索 needle 与 provider-b/model-b/complete 维度。
	needle := testUsageRequest("trend-usage-b-fraction", time.Date(2026, 3, 7, 15, 59, 59, 750000000, time.UTC))
	needle.ProviderID = "provider-b"
	needle.ProviderName = "Trends Needle Provider"
	needle.ProviderAPIURL = "https://provider-b.example.com"
	needle.MappedModel = "model-b"
	needle.OriginalModel = "model-b"
	needle.RequestPath = "/v1/complete"
	record(needle, dedupeToken(
		"trend-usage-b-fraction", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 1, OutputTokens: 1},
	))

	// 3. 上海时区午夜后小数秒（UTC 仍为 03-07，+08 为 03-08 00:00:00.25）。
	record(testUsageRequest("trend-usage-c-midnight", time.Date(2026, 3, 7, 16, 0, 0, 250000000, time.UTC)), dedupeToken(
		"trend-usage-c-midnight", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 2},
	))

	// 4. 无 usage（none/missing），other-app 维度。
	noUsage := testUsageRequest("trend-no-usage", time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC))
	noUsage.SourceApp = "other-app"
	noUsage.SourceEntrypoint = "other-entry"
	record(noUsage, dedupeToken("trend-no-usage", UsageSourceNone, ParseStatusMissing, UsageValues{}))

	// 5. 失败：error_type + 500（纽约 03-08 00:00 EST）。
	failedErr := testUsageRequest("trend-failed-errortype", time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC))
	statusFailed := 500
	failedErr.StatusCode = &statusFailed
	failedErr.ErrorType = ErrorHTTP
	record(failedErr, dedupeToken("trend-failed-errortype", UsageSourceNone, ParseStatusSkippedNon2xx, UsageValues{}))

	// 6. 失败：NULL 状态码但 usage 解析成功；纽约夏令时切换前最后一小时（01:59:59.25 EST）。
	failedNull := testUsageRequest("trend-failed-null-status", time.Date(2026, 3, 8, 6, 59, 59, 250000000, time.UTC))
	failedNull.StatusCode = nil
	record(failedNull, dedupeToken(
		"trend-failed-null-status", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 5},
	))

	// 7. 纽约夏令时切换后第一小时（03:00:00.75 EDT），有 usage。
	record(testUsageRequest("trend-dst-after", time.Date(2026, 3, 8, 7, 0, 0, 750000000, time.UTC)), dedupeToken(
		"trend-dst-after", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 4},
	))

	// 8. UTC 为 03-08 但纽约仍为 03-07 23:59:59 EST 的非 2xx 行（跨日桶边界）。
	failedNon2xx := testUsageRequest("trend-failed-non2xx-prevday", time.Date(2026, 3, 8, 4, 59, 59, 0, time.UTC))
	statusNon2xx := 404
	failedNon2xx.StatusCode = &statusNon2xx
	record(failedNon2xx, dedupeToken("trend-failed-non2xx-prevday", UsageSourceNone, ParseStatusMissing, UsageValues{}))

	// 9. 与 #1 重复的会话行（effective 排除）。
	record(dedupeSessionRequest("trend-session-dup", time.Date(2026, 3, 6, 10, 0, 30, 0, time.UTC), "mapped-model", "mapped-model"), dedupeToken(
		"trend-session-dup", UsageSourceSessionLog, ParseStatusOK,
		UsageValues{InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 3, CacheReadInputTokens: 2},
	))

	// 10. 独立会话行（无候选）。
	record(dedupeSessionRequest("trend-session-unique", time.Date(2026, 3, 9, 20, 0, 0, 0, time.UTC), "session-only-model", "session-only-model"), dedupeToken(
		"trend-session-unique", UsageSourceSessionLog, ParseStatusOK,
		UsageValues{InputTokens: 7, OutputTokens: 3},
	))

	// 11. 前一天小数秒有 usage 行。
	record(testUsageRequest("trend-prev-day-usage", time.Date(2026, 3, 5, 23, 59, 59, 500000000, time.UTC)), dedupeToken(
		"trend-prev-day-usage", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 100},
	))

	// 12. #9 的备选候选（provider-b，晚 5 分钟），用于筛选后候选回退。
	fallback := testUsageRequest("trend-fallback-candidate", time.Date(2026, 3, 6, 10, 5, 0, 0, time.UTC))
	fallback.ProviderID = "provider-b"
	fallback.ProviderName = "Provider B"
	fallback.ProviderAPIURL = "https://provider-b.example.com"
	record(fallback, dedupeToken(
		"trend-fallback-candidate", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 3, CacheReadInputTokens: 2},
	))

	// 13. 非法时间戳行（读取时容错为零值时间桶）。
	invalid := testUsageRequest("trend-invalid-time", time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC))
	record(invalid, dedupeToken(
		"trend-invalid-time", UsageSourceProvider, ParseStatusOK,
		UsageValues{InputTokens: 1, OutputTokens: 2},
	))
	if _, err := store.db.Exec(`UPDATE usage_requests SET started_at = 'invalid-history-time' WHERE id = ?`, invalid.ID); err != nil {
		t.Fatalf("make timestamp invalid: %v", err)
	}
}
