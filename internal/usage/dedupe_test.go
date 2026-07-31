package usage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const testDedupeBackfillMarker = "usage_dedupe_candidates_backfill_v1"

func TestDedupeMigrationCreatesSchema(t *testing.T) {
	store := newLegacyUsageStore(t)

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if !sqliteTableExists(t, store.db, "usage_dedupe_candidates") {
		t.Fatal("usage_dedupe_candidates table is missing")
	}
	for _, index := range []string{"idx_usage_dedupe_provider", "idx_usage_requests_started_id"} {
		if !sqliteIndexExists(t, store.db, index) {
			t.Fatalf("expected index %s to exist", index)
		}
	}

	assertDedupeCandidatePrimaryKey(t, store.db)
	assertDedupeCandidateForeignKeys(t, store.db)
	assertDedupeBackfillMarker(t, store.db)
}

func TestDedupeBackfillPreservesAllCandidates(t *testing.T) {
	store := newLegacyUsageStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{
		InputTokens:              100,
		OutputTokens:             20,
		CacheCreationInputTokens: 30,
		CacheReadInputTokens:     400,
	}

	recordDedupeHistory(t, store,
		dedupeSessionRequest("session", started, "session-mapped", "session-original"),
		dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("mapped-before", started.Add(-10*time.Minute), "session-mapped", "provider-original"),
		dedupeToken("mapped-before", UsageSourceProvider, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider-original-after", started.Add(10*time.Minute), "other-mapped", "session-mapped"),
		dedupeToken("provider-original-after", UsageSourceProvider, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("session-original", started, "session-original", "session-original"),
		dedupeToken("session-original", UsageSourceProvider, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("outside-window", started.Add(10*time.Minute+time.Nanosecond), "session-mapped", ""),
		dedupeToken("outside-window", UsageSourceProvider, ParseStatusOK, values),
	)
	differentTokens := values
	differentTokens.CacheReadInputTokens++
	recordDedupeHistory(t, store,
		dedupeProviderRequest("different-tokens", started, "session-mapped", ""),
		dedupeToken("different-tokens", UsageSourceProvider, ParseStatusOK, differentTokens),
	)

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	got := dedupeCandidates(t, store.db, "session")
	want := map[string]int{
		"mapped-before":           0,
		"provider-original-after": 0,
		"session-original":        1,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestDedupeBackfillNoMatchBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		changeSession  func(*RequestRecord, *TokenRecord)
		changeProvider func(*RequestRecord, *TokenRecord)
	}{
		{
			name: "empty models",
			changeSession: func(req *RequestRecord, _ *TokenRecord) {
				req.MappedModel = ""
				req.OriginalModel = ""
			},
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.MappedModel = ""
				req.OriginalModel = ""
			},
		},
		{
			name: "different model",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.MappedModel = "different"
				req.OriginalModel = "different"
			},
		},
		{
			name: "outside time window",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.StartedAt = req.StartedAt.Add(10*time.Minute + time.Nanosecond)
			},
		},
		{
			name: "non claude provider",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.SourceApp = "other"
			},
		},
		{
			name: "provider parse failed",
			changeProvider: func(_ *RequestRecord, tok *TokenRecord) {
				tok.UsageParseStatus = ParseStatusParseError
			},
		},
		{
			name: "session parse failed",
			changeSession: func(_ *RequestRecord, tok *TokenRecord) {
				tok.UsageParseStatus = ParseStatusParseError
			},
		},
		{
			name: "provider classified as session",
			changeProvider: func(req *RequestRecord, _ *TokenRecord) {
				req.ProviderID = "_session"
			},
		},
	}

	tokenChanges := []struct {
		name   string
		change func(*TokenRecord)
	}{
		{"input tokens differ", func(tok *TokenRecord) { tok.InputTokens++ }},
		{"output tokens differ", func(tok *TokenRecord) { tok.OutputTokens++ }},
		{"cache creation tokens differ", func(tok *TokenRecord) { tok.CacheCreationInputTokens++ }},
		{"cache read tokens differ", func(tok *TokenRecord) { tok.CacheReadInputTokens++ }},
	}
	for _, tokenChange := range tokenChanges {
		change := tokenChange.change
		tests = append(tests, struct {
			name           string
			changeSession  func(*RequestRecord, *TokenRecord)
			changeProvider func(*RequestRecord, *TokenRecord)
		}{
			name: tokenChange.name,
			changeProvider: func(_ *RequestRecord, tok *TokenRecord) {
				change(tok)
			},
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newLegacyUsageStore(t)
			started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			values := UsageValues{
				InputTokens:              1,
				OutputTokens:             2,
				CacheCreationInputTokens: 3,
				CacheReadInputTokens:     4,
			}
			sessionReq := dedupeSessionRequest("session", started, "model", "model")
			sessionTok := dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values)
			providerReq := dedupeProviderRequest("provider", started, "model", "model")
			providerTok := dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values)
			if tt.changeSession != nil {
				tt.changeSession(&sessionReq, &sessionTok)
			}
			if tt.changeProvider != nil {
				tt.changeProvider(&providerReq, &providerTok)
			}
			recordDedupeHistory(t, store, sessionReq, sessionTok)
			recordDedupeHistory(t, store, providerReq, providerTok)

			if err := store.Migrate(); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 0 {
				t.Fatalf("candidate count = %d, want 0", got)
			}
		})
	}
}

func TestDedupeBackfillHandlesHistoricalTimeAndModelEdges(t *testing.T) {
	store := newLegacyUsageStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 1}

	recordDedupeHistory(t, store,
		dedupeSessionRequest("session-same-model", started, "same-model", "same-model"),
		dedupeToken("session-same-model", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider-same-time", started, "same-model", "same-model"),
		dedupeToken("provider-same-time", UsageSourceProvider, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeSessionRequest("session-invalid", started.Add(time.Hour), "invalid-model", "invalid-model"),
		dedupeToken("session-invalid", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider-invalid", started.Add(time.Hour), "invalid-model", "invalid-model"),
		dedupeToken("provider-invalid", UsageSourceProvider, ParseStatusOK, values),
	)
	if _, err := store.db.Exec(
		`UPDATE usage_requests SET started_at = 'invalid-history-time'
		 WHERE id IN ('session-invalid', 'provider-invalid')`,
	); err != nil {
		t.Fatalf("make historical timestamps invalid: %v", err)
	}

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if got := dedupeCandidates(t, store.db, "session-same-model"); fmt.Sprint(got) != "map[provider-same-time:0]" {
		t.Fatalf("same timestamp/model candidates = %v", got)
	}
	if got := dedupeCandidates(t, store.db, "session-invalid"); fmt.Sprint(got) != "map[provider-invalid:0]" {
		t.Fatalf("invalid timestamp candidates = %v", got)
	}
}

func TestDedupeBackfillIsIndependentOfHistoricalInsertOrder(t *testing.T) {
	for _, providerFirst := range []bool{true, false} {
		name := "session first"
		if providerFirst {
			name = "provider first"
		}
		t.Run(name, func(t *testing.T) {
			store := newLegacyUsageStore(t)
			started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			values := UsageValues{InputTokens: 10, OutputTokens: 2}
			sessionReq := dedupeSessionRequest("session", started, "model", "model")
			sessionTok := dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values)
			providerReq := dedupeProviderRequest("provider", started.Add(time.Minute), "model", "model")
			providerTok := dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values)

			if providerFirst {
				recordDedupeHistory(t, store, providerReq, providerTok)
				recordDedupeHistory(t, store, sessionReq, sessionTok)
			} else {
				recordDedupeHistory(t, store, sessionReq, sessionTok)
				recordDedupeHistory(t, store, providerReq, providerTok)
			}

			if err := store.Migrate(); err != nil {
				t.Fatalf("Migrate() error = %v", err)
			}
			if got := dedupeCandidates(t, store.db, "session"); fmt.Sprint(got) != "map[provider:0]" {
				t.Fatalf("candidates = %v", got)
			}
		})
	}
}

func TestDedupeMigrationMarkerPreventsRepeatBackfill(t *testing.T) {
	store := newLegacyUsageStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 10}
	recordDedupeHistory(t, store,
		dedupeSessionRequest("session", started, "model", "model"),
		dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider", started, "model", "model"),
		dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
	)

	if err := store.Migrate(); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 1 {
		t.Fatalf("candidate count after repeated migration = %d, want 1", got)
	}

	if _, err := store.db.Exec(`DELETE FROM usage_dedupe_candidates`); err != nil {
		t.Fatalf("delete candidate: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() with completed marker error = %v", err)
	}
	if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 0 {
		t.Fatalf("completed migration unexpectedly reran backfill: count = %d", got)
	}
	assertDedupeBackfillMarker(t, store.db)
}

func TestDedupeMigrationRollsBackBackfillAndMarkerTogether(t *testing.T) {
	store := newLegacyUsageStore(t)
	started := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	values := UsageValues{InputTokens: 10}
	recordDedupeHistory(t, store,
		dedupeSessionRequest("session", started, "model", "model"),
		dedupeToken("session", UsageSourceSessionLog, ParseStatusOK, values),
	)
	recordDedupeHistory(t, store,
		dedupeProviderRequest("provider", started, "model", "model"),
		dedupeToken("provider", UsageSourceProvider, ParseStatusOK, values),
	)
	if _, err := store.db.Exec(fmt.Sprintf(`
		CREATE TRIGGER fail_dedupe_marker
		BEFORE INSERT ON settings
		WHEN NEW.key = %q
		BEGIN
			SELECT RAISE(ABORT, 'forced dedupe marker failure');
		END;
	`, testDedupeBackfillMarker)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	err := store.Migrate()
	if err == nil || !strings.Contains(err.Error(), "forced dedupe marker failure") {
		t.Fatalf("Migrate() error = %v, want forced marker failure", err)
	}
	if sqliteTableExists(t, store.db, "usage_dedupe_candidates") {
		t.Fatal("candidate schema was committed despite migration failure")
	}
	if sqliteIndexExists(t, store.db, "idx_usage_requests_started_id") {
		t.Fatal("started/id index was committed despite migration failure")
	}
	if got := sqliteCount(t, store.db, "usage_requests"); got != 2 {
		t.Fatalf("usage request count after rollback = %d, want 2", got)
	}
	assertNoDedupeBackfillMarker(t, store.db)

	if _, err := store.db.Exec(`DROP TRIGGER fail_dedupe_marker`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("retry Migrate() error = %v", err)
	}
	if got := sqliteCount(t, store.db, "usage_dedupe_candidates"); got != 1 {
		t.Fatalf("candidate count after retry = %d, want 1", got)
	}
	assertDedupeBackfillMarker(t, store.db)
}

func newLegacyUsageStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "legacy-usage.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE usage_requests (
			id TEXT PRIMARY KEY,
			started_at TEXT NOT NULL,
			ended_at TEXT,
			duration_ms INTEGER,
			upstream_response_header_ms INTEGER,
			time_to_first_byte_ms INTEGER,
			status_code INTEGER,
			error_type TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL DEFAULT '',
			request_path TEXT NOT NULL DEFAULT '',
			backend_url TEXT NOT NULL DEFAULT '',
			provider_id TEXT NOT NULL DEFAULT '',
			provider_name TEXT NOT NULL DEFAULT '',
			provider_api_url TEXT NOT NULL DEFAULT '',
			source_app TEXT NOT NULL DEFAULT 'unknown',
			source_entrypoint TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			original_model TEXT NOT NULL DEFAULT '',
			mapped_model TEXT NOT NULL DEFAULT '',
			stream INTEGER NOT NULL DEFAULT 0,
			request_bytes INTEGER NOT NULL DEFAULT 0,
			response_bytes INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE usage_tokens (
			request_id TEXT PRIMARY KEY,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
			usage_source TEXT NOT NULL DEFAULT 'none',
			usage_parse_status TEXT NOT NULL DEFAULT 'missing',
			usage_parse_error TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (request_id) REFERENCES usage_requests(id) ON DELETE CASCADE
		);
	`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	return NewStore(db)
}

func dedupeSessionRequest(id string, started time.Time, mappedModel, originalModel string) RequestRecord {
	req := testUsageRequest(id, started)
	req.Method = "SESSION"
	req.RequestPath = "session_log"
	req.ProviderID = "_session"
	req.ProviderName = "Session Log"
	req.ProviderAPIURL = ""
	req.SourceEntrypoint = "session_log"
	req.MappedModel = mappedModel
	req.OriginalModel = originalModel
	return req
}

func dedupeProviderRequest(id string, started time.Time, mappedModel, originalModel string) RequestRecord {
	req := testUsageRequest(id, started)
	req.MappedModel = mappedModel
	req.OriginalModel = originalModel
	return req
}

func dedupeToken(id, source, status string, values UsageValues) TokenRecord {
	return TokenRecord{
		RequestID:                id,
		InputTokens:              values.InputTokens,
		OutputTokens:             values.OutputTokens,
		CacheCreationInputTokens: values.CacheCreationInputTokens,
		CacheReadInputTokens:     values.CacheReadInputTokens,
		UsageSource:              source,
		UsageParseStatus:         status,
	}
}

func recordDedupeHistory(t *testing.T, store *Store, req RequestRecord, tok TokenRecord) {
	t.Helper()
	if err := store.Record(req, tok); err != nil {
		t.Fatalf("Record(%q) error = %v", req.ID, err)
	}
}

func dedupeCandidates(t *testing.T, db *sql.DB, sessionID string) map[string]int {
	t.Helper()
	rows, err := db.Query(
		`SELECT provider_request_id, model_priority
		 FROM usage_dedupe_candidates
		 WHERE session_request_id = ?`,
		sessionID,
	)
	if err != nil {
		t.Fatalf("query candidates: %v", err)
	}
	defer rows.Close()
	got := make(map[string]int)
	for rows.Next() {
		var providerID string
		var priority int
		if err := rows.Scan(&providerID, &priority); err != nil {
			t.Fatalf("scan candidate: %v", err)
		}
		got[providerID] = priority
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("candidate rows: %v", err)
	}
	return got
}

func assertDedupeCandidatePrimaryKey(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(usage_dedupe_candidates)`)
	if err != nil {
		t.Fatalf("candidate table_info: %v", err)
	}
	defer rows.Close()
	got := make(map[string]int)
	for rows.Next() {
		var cid, notNull, primaryKeyOrder int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKeyOrder); err != nil {
			t.Fatalf("scan candidate table_info: %v", err)
		}
		got[name] = primaryKeyOrder
	}
	if got["session_request_id"] != 1 || got["provider_request_id"] != 2 {
		t.Fatalf("candidate primary key order = %v", got)
	}
}

func assertDedupeCandidateForeignKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_list(usage_dedupe_candidates)`)
	if err != nil {
		t.Fatalf("candidate foreign_key_list: %v", err)
	}
	defer rows.Close()
	got := make(map[string]string)
	for rows.Next() {
		var id, sequence int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan candidate foreign key: %v", err)
		}
		if table == "usage_requests" && to == "id" {
			got[from] = onDelete
		}
	}
	if got["session_request_id"] != "CASCADE" || got["provider_request_id"] != "CASCADE" {
		t.Fatalf("candidate foreign keys = %v", got)
	}
}

func assertDedupeBackfillMarker(t *testing.T, db *sql.DB) {
	t.Helper()
	var value string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, testDedupeBackfillMarker).Scan(&value); err != nil {
		t.Fatalf("query dedupe backfill marker: %v", err)
	}
	if value != "1" {
		t.Fatalf("dedupe backfill marker value = %q, want 1", value)
	}
}

func assertNoDedupeBackfillMarker(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ?`, testDedupeBackfillMarker).Scan(&count); err != nil {
		t.Fatalf("count dedupe backfill marker: %v", err)
	}
	if count != 0 {
		t.Fatalf("dedupe backfill marker count = %d, want 0", count)
	}
}
