package usage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestExtractUsageRejectsNegativeTokenCounters(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{name: "input", key: "input_tokens"},
		{name: "output", key: "output_tokens"},
		{name: "cache_creation", key: "cache_creation_input_tokens"},
		{name: "cache_read", key: "cache_read_input_tokens"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"usage":{"` + tc.key + `":-1}}`)
			values, source, status := ExtractUsageFromJSON(body)
			if values.HasAny || values != (UsageValues{}) {
				t.Fatalf("values = %#v, want empty", values)
			}
			if source != UsageSourceNone || status != ParseStatusInvalidValue {
				t.Fatalf("source/status = %q/%q, want none/invalid_value", source, status)
			}
		})
	}

	// A negative field invalidates the whole provider usage object; no partial
	// positive usage may make the intermediate-overflow shape reachable.
	values, source, status := ExtractUsageFromJSON([]byte(`{"usage":{"input_tokens":9223372036854775807,"output_tokens":1,"cache_creation_input_tokens":-1}}`))
	if values != (UsageValues{}) || source != UsageSourceNone || status != ParseStatusInvalidValue {
		t.Fatalf("counter-cancellation values/source/status = %#v/%q/%q, want empty/none/invalid_value", values, source, status)
	}
}

func TestRecordRejectsNegativeTokenCountersBeforeInsert(t *testing.T) {
	cases := []struct {
		name string
		set  func(*TokenRecord)
	}{
		{name: "input", set: func(tok *TokenRecord) { tok.InputTokens = -1 }},
		{name: "output", set: func(tok *TokenRecord) { tok.OutputTokens = -1 }},
		{name: "cache_creation", set: func(tok *TokenRecord) { tok.CacheCreationInputTokens = -1 }},
		{name: "cache_read", set: func(tok *TokenRecord) { tok.CacheReadInputTokens = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			req := testUsageRequest("negative-token-"+tc.name, time.Now().UTC())
			tok := TokenRecord{RequestID: req.ID, UsageSource: UsageSourceProvider, UsageParseStatus: ParseStatusOK}
			tc.set(&tok)
			err := store.Record(req, tok)
			assertRejectedValueError(t, err, ErrNegativeTokenCount, "token count")
			assertUsageRowsAbsent(t, store, req.ID)
		})
	}
}

func TestRecordIfAbsentRejectsNegativeTokenCountersBeforeInsert(t *testing.T) {
	store := newTestStore(t)
	req := testUsageRequest("negative-token-if-absent", time.Now().UTC())
	err := func() error {
		_, err := store.recordIfAbsent(req, TokenRecord{
			RequestID: req.ID, InputTokens: math.MinInt64,
			UsageSource: UsageSourceSessionLog, UsageParseStatus: ParseStatusOK,
		})
		return err
	}()
	assertRejectedValueError(t, err, ErrNegativeTokenCount, "token count")
	assertUsageRowsAbsent(t, store, req.ID)
}

func TestRecordIfAbsentRejectsNegativeDurationBeforeInsert(t *testing.T) {
	store := newTestStore(t)
	req := testUsageRequest("negative-duration-if-absent", time.Now().UTC())
	duration := int64(-1)
	req.DurationMS = &duration
	inserted, err := store.recordIfAbsent(req, TokenRecord{RequestID: req.ID})
	if inserted {
		t.Fatal("recordIfAbsent inserted a negative duration")
	}
	assertRejectedValueError(t, err, ErrNegativeDuration, "duration")
	assertUsageRowsAbsent(t, store, req.ID)
}

func TestSSEObserverRejectsNegativeTokenCounters(t *testing.T) {
	observer := NewSSEObserver(time.Now().UTC())
	observer.Observe([]byte("event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":-1}}}\n\n"))
	values, source, status, _ := observer.Result()
	if values != (UsageValues{}) || source != UsageSourceNone || status != ParseStatusInvalidValue {
		t.Fatalf("SSE values/source/status = %#v/%q/%q, want empty/none/invalid_value", values, source, status)
	}
}

func TestRecordRejectsNegativeDurationsBeforeInsert(t *testing.T) {
	cases := []struct {
		name string
		set  func(*RequestRecord)
	}{
		{name: "duration", set: func(req *RequestRecord) { v := int64(-1); req.DurationMS = &v }},
		{name: "upstream_header", set: func(req *RequestRecord) { v := int64(-1); req.UpstreamResponseHeaderMS = &v }},
		{name: "first_byte", set: func(req *RequestRecord) { v := int64(-1); req.TimeToFirstByteMS = &v }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			req := testUsageRequest("negative-duration-"+tc.name, time.Now().UTC())
			tc.set(&req)
			err := store.Record(req, TokenRecord{RequestID: req.ID})
			assertRejectedValueError(t, err, ErrNegativeDuration, "duration")
			assertUsageRowsAbsent(t, store, req.ID)
		})
	}
}

func TestHistoricalNegativeUsageValuesAreNormalizedOnMigrate(t *testing.T) {
	store := newTestStore(t)
	started := time.Now().UTC()
	if _, err := store.db.Exec(`
		INSERT INTO usage_requests(id, started_at, duration_ms, upstream_response_header_ms, time_to_first_byte_ms)
		VALUES (?, ?, ?, ?, ?)`, "legacy-negative", formatTime(started), -3, -4, -5); err != nil {
		t.Fatalf("insert legacy request: %v", err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO usage_tokens(request_id, input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens)
		VALUES (?, ?, ?, ?, ?)`, "legacy-negative", -1, -2, -3, -4); err != nil {
		t.Fatalf("insert legacy tokens: %v", err)
	}

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate() normalization: %v", err)
	}
	var duration, header, firstByte, input, output, creation, read int64
	if err := store.db.QueryRow(`
		SELECT r.duration_ms, r.upstream_response_header_ms, r.time_to_first_byte_ms,
		       t.input_tokens, t.output_tokens, t.cache_creation_input_tokens, t.cache_read_input_tokens
		FROM usage_requests r JOIN usage_tokens t ON t.request_id = r.id
		WHERE r.id = ?`, "legacy-negative").Scan(&duration, &header, &firstByte, &input, &output, &creation, &read); err != nil {
		t.Fatalf("scan normalized row: %v", err)
	}
	if duration != 0 || header != 0 || firstByte != 0 || input != 0 || output != 0 || creation != 0 || read != 0 {
		t.Fatalf("normalized values = %d/%d/%d/%d/%d/%d/%d, want all zero", duration, header, firstByte, input, output, creation, read)
	}
}

func TestHistoricalNegativeUsageMigrationIsAtomicAndReaderConsistent(t *testing.T) {
	barrier := newMigrationUpdateBarrier()
	dbPath := filepath.Join(t.TempDir(), "usage-migration.db")
	dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db := sql.OpenDB(&migrationBarrierConnector{dsn: dsn, barrier: barrier})
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	if err := store.Migrate(); err != nil {
		t.Fatalf("initial Migrate() error = %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO usage_requests(id, started_at, duration_ms, upstream_response_header_ms, time_to_first_byte_ms)
		VALUES ('legacy-negative-atomic', ?, -3, -4, -5)`, formatTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert legacy request: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO usage_tokens(request_id, input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens)
		VALUES ('legacy-negative-atomic', -1, -2, -3, -4)`); err != nil {
		t.Fatalf("insert legacy tokens: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER fail_negative_usage_migration
		BEFORE UPDATE OF duration_ms ON usage_requests
		BEGIN
			SELECT RAISE(ABORT, 'forced negative usage migration failure');
		END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	barrier.enable()

	migrateDone := make(chan error, 1)
	go func() { migrateDone <- store.Migrate() }()
	barrier.waitFirstUpdate(t)

	// The first UPDATE has run on the migration connection, but the migration is
	// paused before the next statement. A concurrent reader must still see the
	// pre-migration snapshot, never the first column partially normalized.
	assertHistoricalNegativeValues(t, db, "legacy-negative-atomic", -3, -4, -5, -1, -2, -3, -4)
	barrier.release()
	if err := <-migrateDone; err == nil || !strings.Contains(err.Error(), "forced negative usage migration failure") {
		t.Fatalf("Migrate() error = %v, want forced migration failure", err)
	}
	// The failed migration must roll back every UPDATE, not only the UPDATE that
	// hit the trigger.
	assertHistoricalNegativeValues(t, db, "legacy-negative-atomic", -3, -4, -5, -1, -2, -3, -4)

	if _, err := db.Exec(`DROP TRIGGER fail_negative_usage_migration`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("retry Migrate() error = %v", err)
	}
	assertHistoricalNegativeValues(t, db, "legacy-negative-atomic", 0, 0, 0, 0, 0, 0, 0)
}

func assertHistoricalNegativeValues(t *testing.T, db *sql.DB, requestID string, want ...int64) {
	t.Helper()
	var got [7]int64
	err := db.QueryRow(`
		SELECT r.duration_ms, r.upstream_response_header_ms, r.time_to_first_byte_ms,
		       t.input_tokens, t.output_tokens, t.cache_creation_input_tokens, t.cache_read_input_tokens
		FROM usage_requests r JOIN usage_tokens t ON t.request_id = r.id
		WHERE r.id = ?`, requestID).Scan(
		&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6],
	)
	if err != nil {
		t.Fatalf("scan historical values: %v", err)
	}
	if len(want) != len(got) {
		t.Fatalf("want %d historical values, got %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("historical values = %v, want %v", got, want)
		}
	}
}

type migrationUpdateBarrier struct {
	enabled       bool
	firstUpdate   chan struct{}
	releaseUpdate chan struct{}
	once          sync.Once
}

func newMigrationUpdateBarrier() *migrationUpdateBarrier {
	return &migrationUpdateBarrier{
		firstUpdate:   make(chan struct{}),
		releaseUpdate: make(chan struct{}),
	}
}

func (b *migrationUpdateBarrier) enable() { b.enabled = true }

func (b *migrationUpdateBarrier) waitFirstUpdate(t *testing.T) {
	t.Helper()
	select {
	case <-b.firstUpdate:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout: migration first UPDATE was never reached")
	}
}

func (b *migrationUpdateBarrier) release() { close(b.releaseUpdate) }

func (b *migrationUpdateBarrier) blockAfterFirstUpdate(query string) {
	if !b.enabled || !strings.HasPrefix(strings.TrimSpace(query), "UPDATE usage_tokens SET input_tokens = 0") {
		return
	}
	b.once.Do(func() {
		close(b.firstUpdate)
		<-b.releaseUpdate
	})
}

type migrationBarrierConnector struct {
	dsn     string
	barrier *migrationUpdateBarrier
}

func (c *migrationBarrierConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &migrationBarrierConn{Conn: conn, barrier: c.barrier}, nil
}

func (c *migrationBarrierConnector) Driver() driver.Driver { return migrationBarrierDriver{} }

type migrationBarrierDriver struct{}

func (migrationBarrierDriver) Open(string) (driver.Conn, error) {
	panic("migrationBarrierDriver.Open must not be called")
}

type migrationBarrierConn struct {
	driver.Conn
	barrier *migrationUpdateBarrier
}

func (c *migrationBarrierConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &migrationBarrierStmt{Stmt: stmt, query: query, barrier: c.barrier}, nil
}

func (c *migrationBarrierConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &migrationBarrierStmt{Stmt: stmt, query: query, barrier: c.barrier}, nil
}

func (c *migrationBarrierConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	result, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
	c.barrier.blockAfterFirstUpdate(query)
	return result, err
}

func (c *migrationBarrierConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *migrationBarrierConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *migrationBarrierConn) Ping(ctx context.Context) error {
	return c.Conn.(driver.Pinger).Ping(ctx)
}

func (c *migrationBarrierConn) IsValid() bool { return c.Conn.(driver.Validator).IsValid() }

func (c *migrationBarrierConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}

type migrationBarrierStmt struct {
	driver.Stmt
	query   string
	barrier *migrationUpdateBarrier
}

func (s *migrationBarrierStmt) Exec(args []driver.Value) (driver.Result, error) {
	result, err := s.Stmt.Exec(args)
	s.barrier.blockAfterFirstUpdate(s.query)
	return result, err
}

func (s *migrationBarrierStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	result, err := s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
	s.barrier.blockAfterFirstUpdate(s.query)
	return result, err
}

func TestNegativeCounterCancellationCannotReachAggregation(t *testing.T) {
	store := newTestStore(t)
	req := testUsageRequest("counter-cancellation", time.Now().UTC())
	err := store.Record(req, TokenRecord{
		RequestID:                req.ID,
		InputTokens:              math.MaxInt64,
		OutputTokens:             1,
		CacheCreationInputTokens: -1,
		UsageSource:              UsageSourceProvider,
		UsageParseStatus:         ParseStatusOK,
	})
	assertRejectedValueError(t, err, ErrNegativeTokenCount, "token count")
	assertUsageRowsAbsent(t, store, req.ID)
}

func assertRejectedValueError(t *testing.T, err, target error, classification string) {
	t.Helper()
	if err == nil || !errors.Is(err, target) || !strings.Contains(err.Error(), classification) {
		t.Fatalf("error = %v, want classification %q", err, classification)
	}
	if strings.Contains(err.Error(), "INSERT") || strings.Contains(err.Error(), "usage_tokens") || strings.Contains(err.Error(), "usage_requests") {
		t.Fatalf("error leaks storage details: %v", err)
	}
}

func assertUsageRowsAbsent(t *testing.T, store *Store, requestID string) {
	t.Helper()
	var requests, tokens int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_requests WHERE id = ?`, requestID).Scan(&requests); err != nil {
		t.Fatalf("count usage_requests: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM usage_tokens WHERE request_id = ?`, requestID).Scan(&tokens); err != nil {
		t.Fatalf("count usage_tokens: %v", err)
	}
	if requests != 0 || tokens != 0 {
		t.Fatalf("rows = usage_requests:%d usage_tokens:%d, want zero", requests, tokens)
	}
}
