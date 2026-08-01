package usage

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

type scopedOracleRow struct {
	requestID       string
	dedupeStatus    string
	dedupeRequestID string
}

func TestScopedQueryMatchesLegacyOracleAcrossFiltersAndScopes(t *testing.T) {
	store := newTestStore(t)
	started := seedScopedQueryFixture(t, store)
	filters := []struct {
		name   string
		filter Filter
	}{
		{name: "all"},
		{name: "from falls back to next candidate", filter: Filter{From: started.Add(-5 * time.Minute)}},
		{name: "to", filter: Filter{To: started.Add(-7 * time.Minute)}},
		{name: "source app", filter: Filter{SourceApp: "other-app"}},
		{name: "source app all", filter: Filter{SourceApp: "all"}},
		{name: "source entrypoint falls back", filter: Filter{SourceEntrypoint: "shared-entry"}},
		{name: "provider falls back", filter: Filter{ProviderID: "shared-provider"}},
		{name: "model removes provider candidate", filter: Filter{Model: "target-model"}},
		{name: "success", filter: Filter{Status: "success"}},
		{name: "error", filter: Filter{Status: "error"}},
		{name: "provider usage only", filter: Filter{UsageSource: UsageSourceProvider}},
		{name: "session usage only", filter: Filter{UsageSource: UsageSourceSessionLog}},
		{name: "parse ok", filter: Filter{UsageParseStatus: ParseStatusOK}},
		{name: "parse missing", filter: Filter{UsageParseStatus: ParseStatusMissing}},
		{name: "request path falls back", filter: Filter{RequestPath: "/shared"}},
		{name: "search falls back", filter: Filter{Query: "Oracle Shared"}},
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
			t.Run(filterCase.name+"/"+scopeName(scope), func(t *testing.T) {
				got := queryScopedOracleRows(t, store.db, filter)
				want := projectScopedOracleRows(legacyOracleQueryRows(t, store.db, filter))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("scoped rows = %#v, want legacy rows %#v", got, want)
				}
			})
		}
	}
}

func TestScopedQueryCandidatePriorityFallbackAndNoCandidate(t *testing.T) {
	store := newTestStore(t)
	started := seedScopedQueryFixture(t, store)

	tests := []struct {
		name            string
		filter          Filter
		requestID       string
		wantPresent     bool
		wantDedupeID    string
		wantDedupeState string
	}{
		{
			name:            "mapped model priority wins before provider time",
			filter:          Filter{StatsScope: StatsScopeRaw},
			requestID:       "session-priority",
			wantPresent:     true,
			wantDedupeID:    "provider-primary",
			wantDedupeState: DedupeStatusDuplicate,
		},
		{
			name:            "filtered primary falls back to current candidate",
			filter:          Filter{From: started.Add(-5 * time.Minute), StatsScope: StatsScopeRaw},
			requestID:       "session-priority",
			wantPresent:     true,
			wantDedupeID:    "provider-fallback",
			wantDedupeState: DedupeStatusDuplicate,
		},
		{
			name:        "session without filtered provider has no marker",
			filter:      Filter{Model: "target-model", StatsScope: StatsScopeRaw},
			requestID:   "session-original-match",
			wantPresent: true,
		},
		{
			name:        "effective scope keeps session when candidate is filtered out",
			filter:      Filter{Model: "target-model", StatsScope: StatsScopeEffective},
			requestID:   "session-original-match",
			wantPresent: true,
		},
		{
			name:        "effective scope removes duplicate session",
			filter:      Filter{StatsScope: StatsScopeEffective},
			requestID:   "session-priority",
			wantPresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := queryScopedOracleRows(t, store.db, tt.filter)
			row, ok := findScopedOracleRow(rows, tt.requestID)
			if ok != tt.wantPresent {
				t.Fatalf("request %q present = %v, want %v; rows = %#v", tt.requestID, ok, tt.wantPresent, rows)
			}
			if !ok {
				return
			}
			if row.dedupeStatus != tt.wantDedupeState || row.dedupeRequestID != tt.wantDedupeID {
				t.Fatalf(
					"request %q dedupe = %q/%q, want %q/%q",
					tt.requestID,
					row.dedupeStatus,
					row.dedupeRequestID,
					tt.wantDedupeState,
					tt.wantDedupeID,
				)
			}
		})
	}
}

func TestScopedQueryOrdersRFC3339NanoChronologically(t *testing.T) {
	tests := []struct {
		name          string
		earlierOffset time.Duration
		laterOffset   time.Duration
	}{
		{
			name:          "whole second before fractional second",
			earlierOffset: 0,
			laterOffset:   500 * time.Millisecond,
		},
		{
			name:          "short fractional prefix before longer fraction",
			earlierOffset: 100 * time.Millisecond,
			laterOffset:   110 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			values := UsageValues{InputTokens: 10, OutputTokens: 20}

			session := dedupeSessionRequest("session", started.Add(5*time.Second), "model", "model")
			recordScopedQueryFixture(t, store, session, dedupeToken(
				session.ID,
				UsageSourceSessionLog,
				ParseStatusOK,
				values,
			))
			earlier := dedupeProviderRequest(
				"provider-earlier",
				started.Add(tt.earlierOffset),
				"model",
				"model",
			)
			recordScopedQueryFixture(t, store, earlier, dedupeToken(
				earlier.ID,
				UsageSourceProvider,
				ParseStatusOK,
				values,
			))
			later := dedupeProviderRequest(
				"provider-later",
				started.Add(tt.laterOffset),
				"model",
				"model",
			)
			recordScopedQueryFixture(t, store, later, dedupeToken(
				later.ID,
				UsageSourceProvider,
				ParseStatusOK,
				values,
			))

			got := queryScopedOracleRows(t, store.db, Filter{StatsScope: StatsScopeRaw})
			row, ok := findScopedOracleRow(got, session.ID)
			if !ok {
				t.Fatalf("session row is missing: %#v", got)
			}
			if row.dedupeRequestID != earlier.ID {
				t.Fatalf("dedupe request = %q, want chronological first %q", row.dedupeRequestID, earlier.ID)
			}
			want := projectScopedOracleRows(legacyOracleQueryRows(
				t,
				store.db,
				Filter{StatsScope: StatsScopeRaw},
			))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("scoped rows = %#v, want legacy rows %#v", got, want)
			}
		})
	}
}

func TestScopedQueryTreatsInvalidHistoricalTimeAsGoZeroTime(t *testing.T) {
	store := newLegacyUsageStore(t)
	values := UsageValues{InputTokens: 10, OutputTokens: 20}
	zero := time.Time{}

	session := dedupeSessionRequest("session", zero, "model", "model")
	recordDedupeHistory(t, store, session, dedupeToken(
		session.ID,
		UsageSourceSessionLog,
		ParseStatusOK,
		values,
	))
	validZero := dedupeProviderRequest("provider-a-valid-zero", zero, "model", "model")
	recordDedupeHistory(t, store, validZero, dedupeToken(
		validZero.ID,
		UsageSourceProvider,
		ParseStatusOK,
		values,
	))
	invalid := dedupeProviderRequest("provider-z-invalid", zero, "model", "model")
	recordDedupeHistory(t, store, invalid, dedupeToken(
		invalid.ID,
		UsageSourceProvider,
		ParseStatusOK,
		values,
	))
	if _, err := store.db.Exec(
		`UPDATE usage_requests SET started_at = 'invalid-history-time' WHERE id = ?`,
		invalid.ID,
	); err != nil {
		t.Fatalf("make provider timestamp invalid: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	got := queryScopedOracleRows(t, store.db, Filter{StatsScope: StatsScopeRaw})
	row, ok := findScopedOracleRow(got, session.ID)
	if !ok {
		t.Fatalf("session row is missing: %#v", got)
	}
	if row.dedupeRequestID != validZero.ID {
		t.Fatalf("dedupe request = %q, want zero-time ID winner %q", row.dedupeRequestID, validZero.ID)
	}
	want := projectScopedOracleRows(legacyOracleQueryRows(
		t,
		store.db,
		Filter{StatsScope: StatsScopeRaw},
	))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped rows = %#v, want legacy rows %#v", got, want)
	}
}

func TestBuildScopedCTEParameterizesFiltersInStableOrder(t *testing.T) {
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

	cte, args := buildScopedCTE(filter, true)
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
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	if strings.Count(cte, "?") != len(args) {
		t.Fatalf("placeholder count = %d, args = %d", strings.Count(cte, "?"), len(args))
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
		if strings.Contains(cte, value) {
			t.Fatalf("CTE contains unparameterized filter value %q", value)
		}
	}
	for _, name := range []string{"filtered", "scoped"} {
		if !strings.Contains(cte, name+" AS") {
			t.Fatalf("CTE is missing %s dataset:\n%s", name, cte)
		}
	}
	// R3：candidate 不再以 ROW_NUMBER CTE 每查询物化，改为相关子查询在“过滤后候选集”中取最优
	// （走持久 candidate_rank 索引）。
	// R4：candidate 从逐行 LEFT JOIN 基表改为 CASE 惰性子查询——非会话行不执行候选查找；
	// 无去重标记需求的 scope 完全跳过 candidate 计算（P2）。
	for _, want := range []string{"FROM usage_dedupe_candidates d", "MIN(d2.candidate_rank)", "CASE WHEN filtered.is_dedupe_session = 1 THEN"} {
		if !strings.Contains(cte, want) {
			t.Fatalf("CTE is missing persisted-rank candidate selection %q:\n%s", want, cte)
		}
	}
	if strings.Contains(cte, "ROW_NUMBER() OVER (\n\t\t\tPARTITION BY d.session_request_id") {
		t.Fatalf("CTE still materializes candidate ROW_NUMBER window:\n%s", cte)
	}
	if strings.Contains(cte, "LEFT JOIN usage_dedupe_candidates d") {
		t.Fatalf("CTE still LEFT JOINs candidate base table per row (R4 应改为 CASE 惰性子查询):\n%s", cte)
	}

	// R4（P2）：无去重标记需求的 scope（needDedupe=false）完全跳过 candidate 计算，
	// scoped 恒输出空标记列保持投影兼容；参数顺序与完整结构一致。
	cteNoDedupe, argsNoDedupe := buildScopedCTE(filter, false)
	if strings.Contains(cteNoDedupe, "usage_dedupe_candidates") {
		t.Fatalf("needDedupe=false CTE must skip candidate computation entirely:\n%s", cteNoDedupe)
	}
	if !strings.Contains(cteNoDedupe, "'' AS dedupe_status") || !strings.Contains(cteNoDedupe, "'' AS dedupe_request_id") {
		t.Fatalf("needDedupe=false CTE must still expose empty dedupe marker columns:\n%s", cteNoDedupe)
	}
	if !reflect.DeepEqual(argsNoDedupe, args) {
		t.Fatalf("needDedupe=false args = %#v, want %#v", argsNoDedupe, args)
	}
}

func queryScopedOracleRows(t *testing.T, db *sql.DB, filter Filter) []scopedOracleRow {
	t.Helper()
	cte, args := buildScopedCTE(filter, true)
	rows, err := db.Query(
		cte+`
		SELECT request_id, dedupe_status, dedupe_request_id
		FROM scoped
		ORDER BY started_at DESC, request_id DESC`,
		args...,
	)
	if err != nil {
		t.Fatalf("query scoped rows: %v", err)
	}
	defer rows.Close()

	var out []scopedOracleRow
	for rows.Next() {
		var row scopedOracleRow
		if err := rows.Scan(&row.requestID, &row.dedupeStatus, &row.dedupeRequestID); err != nil {
			t.Fatalf("scan scoped row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read scoped rows: %v", err)
	}
	return out
}

func seedScopedQueryFixture(t *testing.T, store *Store) time.Time {
	t.Helper()
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{
		InputTokens:              10,
		OutputTokens:             20,
		CacheCreationInputTokens: 30,
		CacheReadInputTokens:     40,
	}

	session := dedupeSessionRequest("session-priority", started, "mapped-model", "original-model")
	session.ProviderID = "shared-provider"
	session.ProviderName = "Oracle Shared Session"
	session.SourceEntrypoint = "shared-entry"
	session.RequestPath = "/shared"
	recordScopedQueryFixture(t, store, session, dedupeToken(
		session.ID,
		UsageSourceSessionLog,
		ParseStatusOK,
		values,
	))

	priorityOne := dedupeProviderRequest(
		"provider-priority-one",
		started.Add(-8*time.Minute),
		"original-model",
		"original-model",
	)
	priorityOne.ProviderID = "shared-provider"
	priorityOne.ProviderName = "Oracle Shared Priority One"
	priorityOne.SourceEntrypoint = "shared-entry"
	priorityOne.RequestPath = "/shared"
	recordScopedQueryFixture(t, store, priorityOne, dedupeToken(
		priorityOne.ID,
		UsageSourceProvider,
		ParseStatusOK,
		values,
	))

	primary := dedupeProviderRequest(
		"provider-primary",
		started.Add(-6*time.Minute),
		"mapped-model",
		"mapped-model",
	)
	primary.ProviderID = "primary-provider"
	primary.ProviderName = "Primary Candidate"
	primary.SourceEntrypoint = "primary-entry"
	primary.RequestPath = "/primary"
	recordScopedQueryFixture(t, store, primary, dedupeToken(
		primary.ID,
		UsageSourceProvider,
		ParseStatusOK,
		values,
	))

	fallback := dedupeProviderRequest(
		"provider-fallback",
		started.Add(-4*time.Minute),
		"mapped-model",
		"mapped-model",
	)
	fallback.ProviderID = "shared-provider"
	fallback.ProviderName = "Oracle Shared Fallback"
	fallback.SourceEntrypoint = "shared-entry"
	fallback.RequestPath = "/shared"
	recordScopedQueryFixture(t, store, fallback, dedupeToken(
		fallback.ID,
		UsageSourceProvider,
		ParseStatusOK,
		values,
	))

	originalMatchSession := dedupeSessionRequest(
		"session-original-match",
		started.Add(30*time.Minute),
		"target-model",
		"target-model",
	)
	recordScopedQueryFixture(t, store, originalMatchSession, dedupeToken(
		originalMatchSession.ID,
		UsageSourceSessionLog,
		ParseStatusOK,
		UsageValues{InputTokens: 3, OutputTokens: 4},
	))
	originalMatchProvider := dedupeProviderRequest(
		"provider-original-match",
		started.Add(29*time.Minute),
		"different-mapped-model",
		"target-model",
	)
	recordScopedQueryFixture(t, store, originalMatchProvider, dedupeToken(
		originalMatchProvider.ID,
		UsageSourceProvider,
		ParseStatusOK,
		UsageValues{InputTokens: 3, OutputTokens: 4},
	))

	errorRow := testUsageRequest("other-error", started.Add(time.Hour))
	errorRow.SourceApp = "other-app"
	errorRow.SourceEntrypoint = "other-entry"
	errorRow.ProviderID = "other-provider"
	errorRow.ProviderName = "Search Needle"
	errorRow.MappedModel = "other-model"
	errorRow.RequestPath = "/other"
	errorRow.ErrorType = ErrorHTTP
	errorRow.ErrorMessage = "searchable error"
	status := 500
	errorRow.StatusCode = &status
	recordScopedQueryFixture(t, store, errorRow, TokenRecord{
		RequestID:        errorRow.ID,
		UsageSource:      UsageSourceNone,
		UsageParseStatus: ParseStatusMissing,
	})

	return started
}

func recordScopedQueryFixture(t *testing.T, store *Store, req RequestRecord, tok TokenRecord) {
	t.Helper()
	if err := store.Record(req, tok); err != nil {
		t.Fatalf("Record(%q) error = %v", req.ID, err)
	}
}

func projectScopedOracleRows(rows []RequestRow) []scopedOracleRow {
	var out []scopedOracleRow
	for _, row := range rows {
		out = append(out, scopedOracleRow{
			requestID:       row.ID,
			dedupeStatus:    row.DedupeStatus,
			dedupeRequestID: row.DedupeRequestID,
		})
	}
	return out
}

func findScopedOracleRow(rows []scopedOracleRow, requestID string) (scopedOracleRow, bool) {
	for _, row := range rows {
		if row.requestID == requestID {
			return row, true
		}
	}
	return scopedOracleRow{}, false
}

func scopeName(scope string) string {
	if scope == "" {
		return "default"
	}
	return scope
}
