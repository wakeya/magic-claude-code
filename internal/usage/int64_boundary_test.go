package usage

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 受支持数据范围（M-3 int64 聚合边界）——见 spec_ZH.md「受支持数据范围（int64
// 聚合边界）」。本文件在目标 driver（modernc.org/sqlite v1.50.1 + Go 1.26）上锁定
// 该边界的逐字段行为：
//
//	I1 单值：usage_tokens 四个计数器与 duration_ms 均为非负合法 int64（API parser 拒绝
//	   负数、>MaxInt64 整数与 ≥2^63 浮点；session-sync/Record 写入边界同样拒绝负数；
//	   duration 来自 Go time.Since）。
//	I2 行内和：每行四个 token 计数器之和在 int64 内。
//	I3 跨行和：任意聚合分组（Summary 全局、Providers/Models 每组、Trends 每桶）的
//	   token 总和与 duration 总和在 int64 内。
//
// 真实产品数据（单计数器 ≤ ~1e9、行数 ≤ 数十万、duration ≤ 9.2e12 ms/行）距 int64
// 上限富余 5 个以上数量级，恒满足 I2/I3；SQL 聚合结果恒为 INTEGER，与旧 Go 逐行
// int64 累加逐位一致（本文件 A 组用例与 legacyOracle 差分锁定）。
//
// 超出 I2/I3 的数据（如手工编辑数据库写入接近 2^63 的伪造非负计数）不受支持，溢出点
// 行为确定且被文档定义：行内和溢出 → SQLite 整数表达式提升为 REAL → database/sql
// Scan 到 int64 报 "Scan error"；跨行 SUM() 溢出 → SQLite 查询时报 "integer
// overflow"。两者都是显式错误，绝不静默返回失真数值（本文件 B 组用例锁定）；旧 Go
// 实现在这两点静默回绕，新路径选择显式报错，仅对超界数据生效。
func TestInt64BoundarySingleValueNearMax(t *testing.T) {
	// A1：单值接近 MaxInt64（每类计数器各测一次），行内和/跨行和均 ≤ MaxInt64，
	// SQL 聚合路径必须与旧 Go 逐行聚合逐字段一致。
	cases := []struct {
		name   string
		values UsageValues
	}{
		{name: "input", values: UsageValues{InputTokens: math.MaxInt64 - 100}},
		{name: "output", values: UsageValues{OutputTokens: math.MaxInt64 - 100}},
		{name: "cache_creation", values: UsageValues{CacheCreationInputTokens: math.MaxInt64 - 100}},
		{name: "cache_read", values: UsageValues{CacheReadInputTokens: math.MaxInt64 - 100}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			seedBoundaryProviderRow(t, store, "boundary-1", time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC), 120, tc.values)
			now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
			assertBoundaryEndpointsMatchOracle(t, store, now)
		})
	}
}

func TestInt64BoundaryRowSumExactlyAtMax(t *testing.T) {
	// A2：行内四 token 和恰为 MaxInt64（精确边界），以及单值恰为 MaxInt64
	// （非负契约允许的极端单值，其余计数器为 0 时行内和/跨行和仍在 int64 内）。
	cases := []struct {
		name   string
		values UsageValues
	}{
		{name: "half_split", values: UsageValues{InputTokens: math.MaxInt64/2 + 1, OutputTokens: math.MaxInt64 / 2}},
		{name: "single_max", values: UsageValues{InputTokens: math.MaxInt64}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			seedBoundaryProviderRow(t, store, "boundary-2", time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC), 120, tc.values)
			now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
			assertBoundaryEndpointsMatchOracle(t, store, now)
		})
	}
}

func TestInt64BoundaryCrossRowSumNearMax(t *testing.T) {
	// A3：跨行总和接近 MaxInt64（两行各 MaxInt64/2 → 列 SUM 与全局总和 = MaxInt64-1），
	// 仍为 INTEGER，SQL 与旧 Go 逐字段一致。
	store := newTestStore(t)
	seedBoundaryProviderRow(t, store, "near-1", time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC), 120,
		UsageValues{InputTokens: math.MaxInt64 / 2})
	seedBoundaryProviderRow(t, store, "near-2", time.Date(2026, 7, 30, 10, 1, 0, 0, time.UTC), 120,
		UsageValues{InputTokens: math.MaxInt64 / 2})
	now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
	assertBoundaryEndpointsMatchOracle(t, store, now)

	// 锚定绝对期望值：与旧 Go 一致地得到 MaxInt64-1，而不是 REAL 舍入后的值。
	got, err := store.Summary(Filter{TZ: "UTC", Now: now, StatsScope: StatsScopeEffective})
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	if got.TokenConsumptionTotal != math.MaxInt64-1 {
		t.Fatalf("TokenConsumptionTotal = %d, want %d", got.TokenConsumptionTotal, math.MaxInt64-1)
	}
	if got.TodayTokenConsumption != math.MaxInt64-1 {
		t.Fatalf("TodayTokenConsumption = %d, want %d", got.TodayTokenConsumption, math.MaxInt64-1)
	}
}

func TestInt64BoundaryDurationNearMax(t *testing.T) {
	// A4：duration 总和接近 MaxInt64（单行 MaxInt64-1；两行各 (MaxInt64-1)/2 →
	// 和 = MaxInt64-2），SUM(duration_ms) 仍为 INTEGER，AverageDurationMS 与旧 Go 一致。
	t.Run("single", func(t *testing.T) {
		store := newTestStore(t)
		seedBoundaryProviderRow(t, store, "dur-1", time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
			math.MaxInt64-1, UsageValues{InputTokens: 1})
		assertBoundaryEndpointsMatchOracle(t, store, time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC))
	})
	t.Run("two_rows", func(t *testing.T) {
		store := newTestStore(t)
		seedBoundaryProviderRow(t, store, "dur-1", time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
			(math.MaxInt64-1)/2, UsageValues{InputTokens: 1})
		seedBoundaryProviderRow(t, store, "dur-2", time.Date(2026, 7, 30, 10, 1, 0, 0, time.UTC),
			(math.MaxInt64-1)/2, UsageValues{InputTokens: 1})
		now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
		assertBoundaryEndpointsMatchOracle(t, store, now)

		got, err := store.Providers(Filter{TZ: "UTC", Now: now, StatsScope: StatsScopeEffective})
		if err != nil {
			t.Fatalf("Providers() error = %v", err)
		}
		if len(got) != 1 || got[0].AverageDurationMS != float64(math.MaxInt64-2)/2 {
			t.Fatalf("AverageDurationMS = %v, want %v", got, float64(math.MaxInt64-2)/2)
		}
	})
}

func TestInt64BoundaryRowSumOverflowFailsDeterministically(t *testing.T) {
	// B1：行内四 token 和超过 MaxInt64（MaxInt64 + 1）。SQLite 整数
	// 表达式溢出提升为 REAL，database/sql 无法把科学计数法 REAL 扫描成 int64，聚合
	// 端点以显式错误失败（文档定义的溢出点行为），绝不静默返回失真数值。旧 Go 实现
	// 在此静默回绕（断言其回绕值仅作文档对照）。负向形态已由非负写入边界拒绝，
	// 因而不可达，不再作为 SQL 溢出路径测试。
	cases := []struct {
		name          string
		values        UsageValues
		legacyWrapped int64
	}{
		{name: "positive", values: UsageValues{InputTokens: math.MaxInt64, OutputTokens: 1}, legacyWrapped: math.MinInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			seedBoundaryProviderRow(t, store, "overflow-1", time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC), 120, tc.values)
			now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
			filter := Filter{TZ: "UTC", Now: now, StatsScope: StatsScopeEffective}

			for _, endpoint := range []string{"Summary", "Providers", "Models", "Trends"} {
				t.Run(endpoint, func(t *testing.T) {
					var err error
					switch endpoint {
					case "Summary":
						_, err = store.Summary(filter)
					case "Providers":
						_, err = store.Providers(filter)
					case "Models":
						_, err = store.Models(filter)
					case "Trends":
						_, err = store.Trends(filter)
					}
					assertScanOverflowError(t, err)
				})
			}

			// 文档对照：旧 Go 逐行累加在此静默回绕，输出负数垃圾。
			legacy := legacyOracleSummary(t, store.db, filter)
			if legacy.TokenConsumptionTotal != tc.legacyWrapped {
				t.Fatalf("legacy wrapped TokenConsumptionTotal = %d, want %d", legacy.TokenConsumptionTotal, tc.legacyWrapped)
			}
		})
	}
}

func TestInt64BoundaryCrossRowSumOverflowFailsDeterministically(t *testing.T) {
	// B2：跨行 SUM 超过 MaxInt64（两行各 MaxInt64 → 列 SUM = 2×MaxInt64）。每行
	// 行内和仍为合法 int64，SQLite SUM() 整数累计溢出时报
	// "integer overflow" 查询错误（文档定义的溢出点行为），绝不静默返回失真数值。
	// 旧 Go 实现在此静默回绕（断言其回绕值仅作文档对照）。负向形态已由非负写入边界拒绝，
	// 因而不可达，不再作为 SQL 溢出路径测试。
	cases := []struct {
		name          string
		values        UsageValues
		legacyWrapped int64
	}{
		{name: "positive", values: UsageValues{InputTokens: math.MaxInt64}, legacyWrapped: -2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			seedBoundaryProviderRow(t, store, "sum-1", time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC), 120, tc.values)
			seedBoundaryProviderRow(t, store, "sum-2", time.Date(2026, 7, 30, 10, 1, 0, 0, time.UTC), 120, tc.values)
			now := time.Date(2026, 7, 30, 20, 0, 0, 0, time.UTC)
			filter := Filter{TZ: "UTC", Now: now, StatsScope: StatsScopeEffective}

			for _, endpoint := range []string{"Summary", "Providers", "Models", "Trends"} {
				t.Run(endpoint, func(t *testing.T) {
					var err error
					switch endpoint {
					case "Summary":
						_, err = store.Summary(filter)
					case "Providers":
						_, err = store.Providers(filter)
					case "Models":
						_, err = store.Models(filter)
					case "Trends":
						_, err = store.Trends(filter)
					}
					assertIntegerOverflowError(t, err)
				})
			}

			// 文档对照：旧 Go 逐行累加在此静默回绕。
			legacy := legacyOracleSummary(t, store.db, filter)
			if legacy.TokenConsumptionTotal != tc.legacyWrapped {
				t.Fatalf("legacy wrapped TokenConsumptionTotal = %d, want %d", legacy.TokenConsumptionTotal, tc.legacyWrapped)
			}
		})
	}
}

// seedBoundaryProviderRow 写入一条 provider 口径、hasUsage=true 的 usage 行，
// 便于 token/duration 聚合边界测试（与 dedupe 无关，行内四计数器和与
// duration_ms 精确可控）。
func seedBoundaryProviderRow(t *testing.T, store *Store, id string, started time.Time, duration int64, values UsageValues) {
	t.Helper()
	req := testUsageRequest(id, started)
	req.DurationMS = &duration
	if err := store.Record(req, TokenRecord{
		RequestID:                id,
		InputTokens:              values.InputTokens,
		OutputTokens:             values.OutputTokens,
		CacheCreationInputTokens: values.CacheCreationInputTokens,
		CacheReadInputTokens:     values.CacheReadInputTokens,
		UsageSource:              UsageSourceProvider,
		UsageParseStatus:         ParseStatusOK,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

// assertBoundaryEndpointsMatchOracle 对 Summary/Providers/Models/Trends 四个聚合
// 端点逐字段比较 SQL 路径与旧 Go 逐行聚合（legacyOracle*），锁定 I1–I3 范围内
// SQL 聚合与旧语义逐位一致（含恰为 MaxInt64 的精确值，不产生 REAL 舍入）。
func assertBoundaryEndpointsMatchOracle(t *testing.T, store *Store, now time.Time) {
	t.Helper()
	filter := Filter{TZ: "UTC", Now: now, StatsScope: StatsScopeEffective}

	gotSummary, err := store.Summary(filter)
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	wantSummary := legacyOracleSummary(t, store.db, filter)
	if !reflect.DeepEqual(gotSummary, wantSummary) {
		t.Fatalf("Summary() = %#v, want legacy %#v", gotSummary, wantSummary)
	}

	gotProviders, err := store.Providers(filter)
	if err != nil {
		t.Fatalf("Providers() error = %v", err)
	}
	wantProviders := legacyOracleAggregate(t, store.db, filter, aggregateProviderKey)
	if !reflect.DeepEqual(gotProviders, wantProviders) {
		t.Fatalf("Providers() = %#v, want legacy %#v", gotProviders, wantProviders)
	}

	gotModels, err := store.Models(filter)
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	wantModels := legacyOracleAggregate(t, store.db, filter, aggregateModelKey)
	if !reflect.DeepEqual(gotModels, wantModels) {
		t.Fatalf("Models() = %#v, want legacy %#v", gotModels, wantModels)
	}

	gotTrends, err := store.Trends(filter)
	if err != nil {
		t.Fatalf("Trends() error = %v", err)
	}
	wantTrends := legacyOracleTrends(t, store.db, filter)
	if !reflect.DeepEqual(gotTrends, wantTrends) {
		t.Fatalf("Trends() = %#v, want legacy %#v", gotTrends, wantTrends)
	}
}

// assertScanOverflowError 断言行内和溢出（SQLite REAL）在目标 driver 上表现为
// database/sql Scan 错误：driver 把 REAL 交给 database/sql 后，float64→int64
// 转换失败，绝无静默截断。
func assertScanOverflowError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected Scan overflow error, got nil")
	}
	if !strings.Contains(err.Error(), "Scan error") || !strings.Contains(err.Error(), "float64") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertIntegerOverflowError 断言跨行 SUM 溢出在目标 driver 上表现为 SQLite
// "integer overflow" 查询错误。
func assertIntegerOverflowError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected integer overflow error, got nil")
	}
	if !strings.Contains(err.Error(), "integer overflow") {
		t.Fatalf("unexpected error: %v", err)
	}
}
