package usage

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := NewStore(db)
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return store
}

func TestStoreMigratesUsageSchema(t *testing.T) {
	store := newTestStore(t)

	for _, table := range []string{"usage_requests", "usage_tokens", "session_log_sync"} {
		if !sqliteTableExists(t, store.db, table) {
			t.Fatalf("expected table %s to exist", table)
		}
	}
	for _, column := range []string{"upstream_response_header_ms", "time_to_first_byte_ms", "backend_url", "source_entrypoint"} {
		if !sqliteColumnExists(t, store.db, "usage_requests", column) {
			t.Fatalf("expected usage_requests.%s to exist", column)
		}
	}
	for _, index := range []string{
		"idx_usage_requests_started_at",
		"idx_usage_requests_provider",
		"idx_usage_requests_provider_url",
		"idx_usage_requests_entrypoint",
		"idx_usage_requests_path",
		"idx_usage_requests_model",
		"idx_usage_requests_source",
		"idx_usage_requests_status",
		"idx_usage_tokens_source",
		"idx_usage_tokens_parse_status",
		"idx_session_log_sync_synced_at",
	} {
		if !sqliteIndexExists(t, store.db, index) {
			t.Fatalf("expected index %s to exist", index)
		}
	}

	var retention string
	if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = 'usage_retention_days'`).Scan(&retention); err != nil {
		t.Fatalf("query usage_retention_days: %v", err)
	}
	if retention != "90" {
		t.Fatalf("usage_retention_days = %q", retention)
	}
}

func TestRecordRequestAlwaysWritesTokenRow(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)

	if err := store.Record(testUsageRequest("req-1", started), TokenRecord{
		RequestID:        "req-1",
		UsageSource:      UsageSourceNone,
		UsageParseStatus: ParseStatusMissing,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	var tokenRows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_tokens WHERE request_id = 'req-1'`).Scan(&tokenRows); err != nil {
		t.Fatalf("count token rows: %v", err)
	}
	if tokenRows != 1 {
		t.Fatalf("tokenRows = %d", tokenRows)
	}
}

func TestSummaryAggregatesProviderUsageOnly(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "with-usage", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{
		InputTokens:              10,
		OutputTokens:             5,
		CacheCreationInputTokens: 3,
		CacheReadInputTokens:     2,
	})
	seedUsageRecord(t, store, "without-usage", started.Add(time.Hour), 200, "", UsageSourceNone, ParseStatusMissing, UsageValues{})
	seedUsageRecord(t, store, "http-error", started.Add(2*time.Hour), 500, ErrorHTTP, UsageSourceNone, ParseStatusSkippedNon2xx, UsageValues{})

	summary, err := store.Summary(Filter{From: started.Add(-time.Hour), To: started.Add(24 * time.Hour), TZ: "UTC"})
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}

	if summary.ProviderRequestsTotal != 3 {
		t.Fatalf("ProviderRequestsTotal = %d", summary.ProviderRequestsTotal)
	}
	if summary.TokenConsumptionTotal != 20 {
		t.Fatalf("TokenConsumptionTotal = %d", summary.TokenConsumptionTotal)
	}
	if summary.FailedRequests != 1 {
		t.Fatalf("FailedRequests = %d", summary.FailedRequests)
	}
	if summary.UsageCoverage != 1.0/3.0 {
		t.Fatalf("UsageCoverage = %v", summary.UsageCoverage)
	}
}

func TestEffectiveScopeExcludesDuplicateSessionLogUsage(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "provider-dup", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 800,
	})
	seedSessionUsageRecord(t, store, "session:dup", started.Add(30*time.Second), "mapped-model", UsageValues{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 800,
	})
	seedSessionUsageRecord(t, store, "session:only", started.Add(time.Hour), "session-only-model", UsageValues{
		InputTokens:  7,
		OutputTokens: 3,
	})

	summary, err := store.Summary(Filter{TZ: "UTC"})
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	if summary.ProviderRequestsTotal != 2 {
		t.Fatalf("ProviderRequestsTotal = %d", summary.ProviderRequestsTotal)
	}
	if summary.TokenConsumptionTotal != 930 {
		t.Fatalf("TokenConsumptionTotal = %d", summary.TokenConsumptionTotal)
	}

	page, err := store.Requests(Filter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("effective Total = %d rows=%#v", page.Total, page.Rows)
	}
	for _, row := range page.Rows {
		if row.ID == "session:dup" {
			t.Fatalf("duplicate session row was included in effective scope: %#v", row)
		}
	}
}

func TestStatsScopesReturnExpectedRows(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "provider-dup", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{
		InputTokens:  10,
		OutputTokens: 5,
	})
	seedUsageRecord(t, store, "provider-none", started.Add(2*time.Minute), 200, "", UsageSourceNone, ParseStatusMissing, UsageValues{})
	seedSessionUsageRecord(t, store, "session:dup", started.Add(30*time.Second), "mapped-model", UsageValues{
		InputTokens:  10,
		OutputTokens: 5,
	})

	raw, err := store.Requests(Filter{StatsScope: StatsScopeRaw, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("raw Requests() error = %v", err)
	}
	if raw.Total != 3 {
		t.Fatalf("raw Total = %d", raw.Total)
	}
	var duplicate RequestRow
	for _, row := range raw.Rows {
		if row.ID == "session:dup" {
			duplicate = row
		}
	}
	if duplicate.DedupeStatus != DedupeStatusDuplicate || duplicate.DedupeRequestID != "provider-dup" {
		t.Fatalf("duplicate row markers = %q/%q", duplicate.DedupeStatus, duplicate.DedupeRequestID)
	}

	provider, err := store.Requests(Filter{StatsScope: StatsScopeProvider, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("provider Requests() error = %v", err)
	}
	if provider.Total != 2 {
		t.Fatalf("provider Total = %d", provider.Total)
	}

	session, err := store.Requests(Filter{StatsScope: StatsScopeSessionLog, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("session Requests() error = %v", err)
	}
	if session.Total != 1 || session.Rows[0].ID != "session:dup" {
		t.Fatalf("session page = %#v", session)
	}
}

func TestStatsScopeDuplicateDetectionScalesWithManyRows(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 250; i++ {
		seedUsageRecord(t, store, "provider-noise-"+strconv.Itoa(i), started.Add(time.Duration(i)*time.Second), 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{
			InputTokens:  int64(i + 1),
			OutputTokens: int64(i + 2),
		})
		seedSessionUsageRecord(t, store, "session:noise-"+strconv.Itoa(i), started.Add(time.Hour+time.Duration(i)*time.Second), "noise-model-"+strconv.Itoa(i), UsageValues{
			InputTokens:  int64(i + 3),
			OutputTokens: int64(i + 4),
		})
	}
	seedUsageRecord(t, store, "provider-target", started.Add(2*time.Hour), 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 800,
	})
	seedSessionUsageRecord(t, store, "session:target", started.Add(2*time.Hour+30*time.Second), "mapped-model", UsageValues{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 800,
	})

	page, err := store.Requests(Filter{StatsScope: StatsScopeRaw, Page: 1, PageSize: 600})
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	var target RequestRow
	for _, row := range page.Rows {
		if row.ID == "session:target" {
			target = row
			break
		}
	}
	if target.DedupeStatus != DedupeStatusDuplicate || target.DedupeRequestID != "provider-target" {
		t.Fatalf("target duplicate markers = %q/%q", target.DedupeStatus, target.DedupeRequestID)
	}
}

func TestCoverageGroupsByProviderURLModelAndEntrypoint(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "coverage-1", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1})
	seedUsageRecord(t, store, "coverage-2", started.Add(time.Minute), 200, "", UsageSourceNone, ParseStatusMissing, UsageValues{})

	rows, err := store.Coverage(Filter{})
	if err != nil {
		t.Fatalf("Coverage() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d", len(rows))
	}
	row := rows[0]
	if row.ProviderName != "Provider A" || row.ProviderAPIURL != "https://provider.example.com/anthropic" || row.MappedModel != "mapped-model" || row.SourceEntrypoint != "cli" {
		t.Fatalf("unexpected coverage row: %#v", row)
	}
	if row.TotalRequests != 2 || row.WithUsageRequests != 1 || row.WithoutUsageRequests != 1 {
		t.Fatalf("unexpected request counts: %#v", row)
	}
	if row.TopUsageParseStatus != ParseStatusMissing {
		t.Fatalf("TopUsageParseStatus = %q", row.TopUsageParseStatus)
	}
}

func TestRequestsFilterBySourceEntrypointUsageStatusAndSearch(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "cli-usage", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 1})
	vscodeReq := testUsageRequest("vscode-no-usage", started.Add(time.Minute))
	vscodeReq.SourceEntrypoint = "claude-vscode"
	vscodeReq.ProviderName = "Provider B"
	vscodeReq.MappedModel = "other-model"
	if err := store.Record(vscodeReq, TokenRecord{RequestID: vscodeReq.ID, UsageSource: UsageSourceNone, UsageParseStatus: ParseStatusMissing}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	page, err := store.Requests(Filter{
		SourceEntrypoint: "claude-vscode",
		UsageSource:      UsageSourceNone,
		Query:            "Provider B",
		Page:             1,
		PageSize:         10,
	})
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}

	if page.Total != 1 || len(page.Rows) != 1 {
		t.Fatalf("unexpected page: %#v", page)
	}
	if page.Rows[0].ID != "vscode-no-usage" {
		t.Fatalf("row ID = %q", page.Rows[0].ID)
	}
}

func TestTodaySummaryUsesTimezone(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 6, 30, 0, 0, time.UTC)
	seedUsageRecord(t, store, "shanghai-today", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 4})

	summary, err := store.Summary(Filter{
		Now: time.Date(2026, 5, 18, 23, 0, 0, 0, time.UTC),
		TZ:  "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	if summary.TodayProviderRequests != 0 {
		t.Fatalf("TodayProviderRequests = %d", summary.TodayProviderRequests)
	}
	if summary.TodayTokenConsumption != 0 {
		t.Fatalf("TodayTokenConsumption = %d", summary.TodayTokenConsumption)
	}

	summary, err = store.Summary(Filter{
		Now: time.Date(2026, 5, 18, 23, 0, 0, 0, time.UTC),
		TZ:  "UTC",
	})
	if err != nil {
		t.Fatalf("Summary() UTC error = %v", err)
	}
	if summary.TodayProviderRequests != 1 {
		t.Fatalf("UTC TodayProviderRequests = %d", summary.TodayProviderRequests)
	}
	if summary.TodayTokenConsumption != 4 {
		t.Fatalf("UTC TodayTokenConsumption = %d", summary.TodayTokenConsumption)
	}
}

func TestTrendsAggregatesByDay(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "trend-1", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 2, OutputTokens: 3})
	seedUsageRecord(t, store, "trend-2", started.Add(time.Hour), 500, ErrorHTTP, UsageSourceNone, ParseStatusSkippedNon2xx, UsageValues{})

	points, err := store.Trends(Filter{From: started.Add(-time.Hour), To: started.Add(24 * time.Hour), TZ: "UTC"})
	if err != nil {
		t.Fatalf("Trends() error = %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("len(points) = %d", len(points))
	}
	if points[0].Bucket != "2026-05-18" || points[0].ProviderRequestsTotal != 2 || points[0].FailedRequests != 1 || points[0].TokenConsumptionTotal != 5 || points[0].UsageCoverage != 0.5 {
		t.Fatalf("point = %#v", points[0])
	}
}

func TestProvidersAndModelsAggregate(t *testing.T) {
	store := newTestStore(t)
	started := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	seedUsageRecord(t, store, "agg-1", started, 200, "", UsageSourceProvider, ParseStatusOK, UsageValues{InputTokens: 2, OutputTokens: 3})
	seedUsageRecord(t, store, "agg-2", started.Add(time.Hour), 200, "", UsageSourceNone, ParseStatusMissing, UsageValues{})

	providers, err := store.Providers(Filter{})
	if err != nil {
		t.Fatalf("Providers() error = %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("len(providers) = %d", len(providers))
	}
	if providers[0].ProviderName != "Provider A" || providers[0].TotalRequests != 2 || providers[0].TokenConsumptionTotal != 5 || providers[0].UsageCoverage != 0.5 {
		t.Fatalf("provider aggregate = %#v", providers[0])
	}

	models, err := store.Models(Filter{})
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("len(models) = %d", len(models))
	}
	if models[0].MappedModel != "mapped-model" || models[0].TotalRequests != 2 || models[0].AverageDurationMS != 120 {
		t.Fatalf("model aggregate = %#v", models[0])
	}
}

func seedUsageRecord(t *testing.T, store *Store, id string, started time.Time, statusCode int, errorType, usageSource, parseStatus string, values UsageValues) {
	t.Helper()
	req := testUsageRequest(id, started)
	req.StatusCode = &statusCode
	req.ErrorType = errorType
	if err := store.Record(req, TokenRecord{
		RequestID:                id,
		InputTokens:              values.InputTokens,
		OutputTokens:             values.OutputTokens,
		CacheCreationInputTokens: values.CacheCreationInputTokens,
		CacheReadInputTokens:     values.CacheReadInputTokens,
		UsageSource:              usageSource,
		UsageParseStatus:         parseStatus,
		UsageParseError:          "",
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func seedSessionUsageRecord(t *testing.T, store *Store, id string, started time.Time, model string, values UsageValues) {
	t.Helper()
	req := testUsageRequest(id, started)
	req.Method = "SESSION"
	req.RequestPath = "session_log"
	req.ProviderID = "_session"
	req.ProviderName = "Session Log"
	req.ProviderAPIURL = ""
	req.SourceEntrypoint = "session_log"
	req.OriginalModel = model
	req.MappedModel = model
	if err := store.Record(req, TokenRecord{
		RequestID:                id,
		InputTokens:              values.InputTokens,
		OutputTokens:             values.OutputTokens,
		CacheCreationInputTokens: values.CacheCreationInputTokens,
		CacheReadInputTokens:     values.CacheReadInputTokens,
		UsageSource:              UsageSourceSessionLog,
		UsageParseStatus:         ParseStatusOK,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func testUsageRequest(id string, started time.Time) RequestRecord {
	ended := started.Add(120 * time.Millisecond)
	duration := int64(120)
	header := int64(80)
	firstByte := int64(90)
	status := 200
	return RequestRecord{
		ID:                       id,
		StartedAt:                started,
		EndedAt:                  &ended,
		DurationMS:               &duration,
		UpstreamResponseHeaderMS: &header,
		TimeToFirstByteMS:        &firstByte,
		StatusCode:               &status,
		Method:                   "POST",
		RequestPath:              "/v1/messages",
		BackendURL:               "https://provider.example.com/anthropic/v1/messages",
		ProviderID:               "provider-a",
		ProviderName:             "Provider A",
		ProviderAPIURL:           "https://provider.example.com/anthropic",
		SourceApp:                "claude_code",
		SourceEntrypoint:         "cli",
		UserAgent:                "claude-code/1.0",
		OriginalModel:            "claude-sonnet",
		MappedModel:              "mapped-model",
		Stream:                   true,
		RequestBytes:             100,
		ResponseBytes:            200,
	}
}

func sqliteTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("query table %s: %v", table, err)
	}
	return count == 1
}

func sqliteIndexExists(t *testing.T, db *sql.DB, index string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil {
		t.Fatalf("query index %s: %v", index, err)
	}
	return count == 1
}

func sqliteColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	return false
}
