package usage

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
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
