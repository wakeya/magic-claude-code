package usage

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSSEObserverExtractsUsageFromMessageStart(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"cache_creation_input_tokens\":2}}}\n\n"))

	values, source, status, _ := observer.Result()

	if source != UsageSourceProvider {
		t.Fatalf("source = %q", source)
	}
	if status != ParseStatusOK {
		t.Fatalf("status = %q", status)
	}
	if values.InputTokens != 11 || values.CacheCreationInputTokens != 2 {
		t.Fatalf("values = %#v", values)
	}
}

func TestSSEObserverMergesPartialUsageFields(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte("event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":4}}}\n\n"))
	observer.Observe([]byte("event: message_delta\ndata: {\"usage\":{\"output_tokens\":7}}\n\n"))

	values, source, status, _ := observer.Result()

	if source != UsageSourceProvider || status != ParseStatusOK {
		t.Fatalf("source/status = %q/%q", source, status)
	}
	if values.InputTokens != 10 || values.OutputTokens != 7 || values.CacheReadInputTokens != 4 {
		t.Fatalf("values = %#v", values)
	}
}

func TestSSEObserverExtractsUsageWithCRLFDelimiters(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte("event: message_start\r\ndata: {\"message\":{\"usage\":{\"input_tokens\":12,\"cache_read_input_tokens\":5}}}\r\n\r\n"))
	observer.Observe([]byte("event: message_delta\r\ndata: {\"usage\":{\"output_tokens\":8}}\r\n\r\n"))

	values, source, status, _ := observer.Result()

	if source != UsageSourceProvider || status != ParseStatusOK {
		t.Fatalf("source/status = %q/%q", source, status)
	}
	if values.InputTokens != 12 || values.OutputTokens != 8 || values.CacheReadInputTokens != 5 {
		t.Fatalf("values = %#v", values)
	}
}

func TestSSEObserverIgnoresPingEvents(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte("event: ping\ndata: {\"type\":\"ping\"}\n\n"))

	values, source, status, _ := observer.Result()

	if values.HasAny {
		t.Fatalf("values = %#v", values)
	}
	if source != UsageSourceNone || status != ParseStatusMissing {
		t.Fatalf("source/status = %q/%q", source, status)
	}
}

func TestSSEObserverMarksParseErrorWithoutPanic(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte("event: message_start\ndata: {bad json}\n\n"))

	_, source, status, _ := observer.Result()

	if source != UsageSourceNone || status != ParseStatusParseError {
		t.Fatalf("source/status = %q/%q", source, status)
	}
}

func TestSSEObserverTracksFirstDataChunk(t *testing.T) {
	started := time.Now().Add(-50 * time.Millisecond)
	observer := NewSSEObserver(started)
	observer.Observe([]byte("event: message_delta\ndata: {\"usage\":{\"output_tokens\":1}}\n\n"))

	_, _, _, firstByte := observer.Result()

	if firstByte == nil {
		t.Fatal("expected first byte latency")
	}
	if *firstByte < 0 {
		t.Fatalf("first byte latency = %d", *firstByte)
	}
}

func TestSSEObserverMarksTerminalEventsComplete(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "message_stop event",
			data: []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
		},
		{
			name: "done marker",
			data: []byte("data: [DONE]\n\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := NewSSEObserver(time.Now())
			observer.Observe(tt.data)

			if !observer.IsComplete() {
				t.Fatal("expected observer to mark stream complete")
			}
		})
	}
}

func TestSSEObserverMergesUsageFromMessageStopBeforeCompleting(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte("event: message_stop\ndata: {\"type\":\"message_stop\",\"usage\":{\"output_tokens\":9}}\n\n"))

	values, source, status, _ := observer.Result()

	if !observer.IsComplete() {
		t.Fatal("expected observer to mark stream complete")
	}
	if source != UsageSourceProvider || status != ParseStatusOK {
		t.Fatalf("source/status = %q/%q", source, status)
	}
	if values.OutputTokens != 9 {
		t.Fatalf("values = %#v", values)
	}
}

// --- Diagnostics tests ---

// anomalousFixture 构造一个异常 SSE 流，包含 text 内容、error.message、
// 各种需要被计数但不被保留内容的场景。没有 message_stop/[DONE]。
func anomalousFixture() []byte {
	return []byte(
		"event: message_start\n" +
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11}}}\n\n" +
			"event: content_block_start\n" +
			"data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}\n\n" +
			"event: content_block_delta\n" +
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"SECRET-TEXT-MARKER\"}}\n\n" +
			"event: message_delta\n" +
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"pause_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
			"event: error\n" +
			"data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"code\":\"1210\",\"message\":\"SECRET-ERROR-MARKER\"}}\n\n")
}

func TestSSEObserverDiagnosticsAnomalousStream(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe(anomalousFixture())

	diag := observer.Diagnostics()

	// Complete == false (no message_stop / [DONE])
	if diag.Complete {
		t.Fatal("expected Complete=false")
	}

	// Events check
	expectedEvents := map[string]int{
		"message_start":       1,
		"content_block_start": 1,
		"content_block_delta": 1,
		"message_delta":       1,
		"error":               1,
	}
	for ev, want := range expectedEvents {
		if diag.Events[ev] != want {
			t.Errorf("Events[%q] = %d, want %d", ev, diag.Events[ev], want)
		}
	}

	// ContentBlockTypes
	if diag.ContentBlockTypes["text"] != 1 {
		t.Errorf("ContentBlockTypes[text] = %d, want 1", diag.ContentBlockTypes["text"])
	}

	// StopReasons
	if diag.StopReasons["pause_turn"] != 1 {
		t.Errorf("StopReasons[pause_turn] = %d, want 1", diag.StopReasons["pause_turn"])
	}

	// ErrorEvents
	if diag.ErrorEvents != 1 {
		t.Errorf("ErrorEvents = %d, want 1", diag.ErrorEvents)
	}

	// ErrorTypes
	if diag.ErrorTypes["invalid_request_error"] != 1 {
		t.Errorf("ErrorTypes[invalid_request_error] = %d, want 1", diag.ErrorTypes["invalid_request_error"])
	}

	// NumericErrorCodes — code was JSON string "1210"
	if diag.NumericErrorCodes["1210"] != 1 {
		t.Errorf("NumericErrorCodes[1210] = %d, want 1", diag.NumericErrorCodes["1210"])
	}

	// Security: serialization must not contain secret markers or internal type strings
	b, err := json.Marshal(diag)
	if err != nil {
		t.Fatalf("json.Marshal(diag) error: %v", err)
	}
	s := string(b)
	for _, forbidden := range []string{"SECRET-TEXT-MARKER", "SECRET-ERROR-MARKER", "text_delta"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("diagnostics JSON contains forbidden substring %q: %s", forbidden, s)
		}
	}
}

func TestSSEObserverDiagnosticsCompleteStream(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte(
		"event: message_start\n" +
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n" +
			"event: message_stop\n" +
			"data: {\"type\":\"message_stop\"}\n\n" +
			"data: [DONE]\n\n"))

	diag := observer.Diagnostics()

	if !diag.Complete {
		t.Error("expected Complete=true")
	}
	if diag.ErrorEvents != 0 {
		t.Errorf("ErrorEvents = %d, want 0", diag.ErrorEvents)
	}
}

func TestSSEObserverDiagnosticsChunkedInput(t *testing.T) {
	fixture := anomalousFixture()

	// Feed fixture in 7-byte chunks
	observer := NewSSEObserver(time.Now())
	for i := 0; i < len(fixture); i += 7 {
		end := i + 7
		if end > len(fixture) {
			end = len(fixture)
		}
		observer.Observe(fixture[i:end])
	}

	chunked := observer.Diagnostics()

	// Feed fixture all at once
	observer2 := NewSSEObserver(time.Now())
	observer2.Observe(fixture)
	whole := observer2.Diagnostics()

	// Compare all maps
	if !mapsEqual(chunked.Events, whole.Events) {
		t.Errorf("Events differ: chunked=%v whole=%v", chunked.Events, whole.Events)
	}
	if !mapsEqual(chunked.ContentBlockTypes, whole.ContentBlockTypes) {
		t.Errorf("ContentBlockTypes differ: chunked=%v whole=%v", chunked.ContentBlockTypes, whole.ContentBlockTypes)
	}
	if !mapsEqual(chunked.StopReasons, whole.StopReasons) {
		t.Errorf("StopReasons differ: chunked=%v whole=%v", chunked.StopReasons, whole.StopReasons)
	}
	if !mapsEqual(chunked.ErrorTypes, whole.ErrorTypes) {
		t.Errorf("ErrorTypes differ: chunked=%v whole=%v", chunked.ErrorTypes, whole.ErrorTypes)
	}
	if !mapsEqual(chunked.NumericErrorCodes, whole.NumericErrorCodes) {
		t.Errorf("NumericErrorCodes differ: chunked=%v whole=%v", chunked.NumericErrorCodes, whole.NumericErrorCodes)
	}
	if chunked.ErrorEvents != whole.ErrorEvents {
		t.Errorf("ErrorEvents differ: chunked=%d whole=%d", chunked.ErrorEvents, whole.ErrorEvents)
	}
	if chunked.ParseErrors != whole.ParseErrors {
		t.Errorf("ParseErrors differ: chunked=%d whole=%d", chunked.ParseErrors, whole.ParseErrors)
	}
	if chunked.Complete != whole.Complete {
		t.Errorf("Complete differ: chunked=%v whole=%v", chunked.Complete, whole.Complete)
	}
}

func TestSSEObserverDiagnosticsParseError(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte("event: message_start\ndata: {bad json}\n\n"))

	diag := observer.Diagnostics()
	if diag.ParseErrors < 1 {
		t.Errorf("ParseErrors = %d, want >= 1", diag.ParseErrors)
	}
}

func TestSSEObserverDiagnosticsNonNumericErrorCode(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte(
		"event: error\n" +
			"data: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"code\":\"abc\"}}\n\n"))

	diag := observer.Diagnostics()

	if diag.NumericErrorCodes["other"] != 1 {
		t.Errorf("NumericErrorCodes[other] = %d, want 1", diag.NumericErrorCodes["other"])
	}
	if _, exists := diag.NumericErrorCodes["abc"]; exists {
		t.Error("NumericErrorCodes should not contain key \"abc\"")
	}
}

func TestSSEObserverDiagnosticsNumericErrorCodeAsNumber(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe([]byte(
		"event: error\n" +
			"data: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"code\":1210}}\n\n"))

	diag := observer.Diagnostics()

	if diag.NumericErrorCodes["1210"] != 1 {
		t.Errorf("NumericErrorCodes[1210] = %d, want 1", diag.NumericErrorCodes["1210"])
	}
}

func TestSSEObserverDiagnosticsReturnsCopy(t *testing.T) {
	observer := NewSSEObserver(time.Now())
	observer.Observe(anomalousFixture())

	diag := observer.Diagnostics()
	diag.Events["message_start"] = 999

	diag2 := observer.Diagnostics()
	if diag2.Events["message_start"] == 999 {
		t.Error("Diagnostics() did not return a copy; caller modified internal state")
	}
}

// TestSSEObserverDiagnosticsNumericErrorCodeCardinalityBound 验证：
// - 不同数字错误码 map 大小有界（≤ cap + 1，含 "other"）
// - 已存在的码继续正常累加
// - 超过上限的新码累计到 "other"
// - 总计数未丢失
func TestSSEObserverDiagnosticsNumericErrorCodeCardinalityBound(t *testing.T) {
	observer := NewSSEObserver(time.Now())

	// 发送 100 个不同的 4 位数字码，每个出现 1 次。
	// 前 16 个应各自成为独立 key；第 17-100 个应累计到 "other"。
	for i := 0; i < 100; i++ {
		code := fmt.Sprintf("%04d", 1000+i) // 1000..1099
		event := fmt.Sprintf(
			"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"code\":\"%s\"}}\n\n",
			code,
		)
		observer.Observe([]byte(event))
	}

	diag := observer.Diagnostics()

	if len(diag.NumericErrorCodes) > maxDistinctNumericErrorCodes+1 {
		t.Errorf("NumericErrorCodes map size = %d, want ≤ %d (cap + other)",
			len(diag.NumericErrorCodes), maxDistinctNumericErrorCodes+1)
	}

	// "other" 应包含第 17-100 个码的计数（共 84 个溢出码）。
	expectedOther := 100 - maxDistinctNumericErrorCodes
	if diag.NumericErrorCodes["other"] != expectedOther {
		t.Errorf("NumericErrorCodes[other] = %d, want %d", diag.NumericErrorCodes["other"], expectedOther)
	}

	// 总计数应为 100（无丢失）。
	total := 0
	for _, v := range diag.NumericErrorCodes {
		total += v
	}
	if total != 100 {
		t.Errorf("NumericErrorCodes total = %d, want 100", total)
	}

	// 重放前 16 个码，验证已存在码正常累加，map 大小不变。
	for i := 0; i < maxDistinctNumericErrorCodes; i++ {
		code := fmt.Sprintf("%04d", 1000+i)
		event := fmt.Sprintf(
			"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"code\":\"%s\"}}\n\n",
			code,
		)
		observer.Observe([]byte(event))
	}

	diag2 := observer.Diagnostics()

	if len(diag2.NumericErrorCodes) > maxDistinctNumericErrorCodes+1 {
		t.Errorf("NumericErrorCodes map size = %d after replay, want ≤ %d",
			len(diag2.NumericErrorCodes), maxDistinctNumericErrorCodes+1)
	}

	total2 := 0
	for _, v := range diag2.NumericErrorCodes {
		total2 += v
	}
	// 总计数 = 初始 100 + 重放 16 = 116
	if total2 != 116 {
		t.Errorf("NumericErrorCodes total = %d after replay, want 116", total2)
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
