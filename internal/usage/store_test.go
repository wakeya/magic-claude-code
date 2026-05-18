package usage

import (
	"database/sql"
	"path/filepath"
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

	for _, table := range []string{"usage_requests", "usage_tokens"} {
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
