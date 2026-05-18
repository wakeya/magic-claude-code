package usage

import (
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
