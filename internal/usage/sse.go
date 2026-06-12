package usage

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

type SSEObserver struct {
	startedAt  time.Time
	buffer     []byte
	usage      UsageValues
	parseError bool
	complete   bool
	firstByte  *int64
}

func NewSSEObserver(startedAt time.Time) *SSEObserver {
	return &SSEObserver{startedAt: startedAt}
}

func (o *SSEObserver) Observe(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if o.firstByte == nil {
		ms := time.Since(o.startedAt).Milliseconds()
		o.firstByte = &ms
	}
	o.buffer = append(o.buffer, chunk...)
	for {
		idx, delimiterLen := nextSSEBlockDelimiter(o.buffer)
		if idx < 0 {
			return
		}
		block := string(o.buffer[:idx])
		o.buffer = o.buffer[idx+delimiterLen:]
		o.observeBlock(block)
	}
}

func nextSSEBlockDelimiter(buffer []byte) (int, int) {
	lf := bytes.Index(buffer, []byte("\n\n"))
	crlf := bytes.Index(buffer, []byte("\r\n\r\n"))
	switch {
	case lf < 0:
		return crlf, 4
	case crlf < 0:
		return lf, 2
	case crlf < lf:
		return crlf, 4
	default:
		return lf, 2
	}
}

func (o *SSEObserver) Result() (UsageValues, string, string, *int64) {
	if o.usage.HasAny {
		return o.usage, UsageSourceProvider, ParseStatusOK, o.firstByte
	}
	if o.parseError {
		return UsageValues{}, UsageSourceNone, ParseStatusParseError, o.firstByte
	}
	return UsageValues{}, UsageSourceNone, ParseStatusMissing, o.firstByte
}

func (o *SSEObserver) IsComplete() bool {
	return o.complete
}

func (o *SSEObserver) observeBlock(block string) {
	var event string
	var dataLines []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if event == "ping" || len(dataLines) == 0 {
		return
	}
	data := strings.Join(dataLines, "\n")
	if data == "[DONE]" {
		o.complete = true
		return
	}

	var payload struct {
		Type    string    `json:"type"`
		Usage   usageJSON `json:"usage"`
		Message struct {
			Usage usageJSON `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		o.parseError = true
		return
	}
	o.merge(payload.Message.Usage)
	o.merge(payload.Usage)
	if event == "message_stop" || payload.Type == "message_stop" {
		o.complete = true
	}
}

func (o *SSEObserver) merge(values usageJSON) {
	if values.InputTokens != nil {
		o.usage.InputTokens = *values.InputTokens
		o.usage.HasAny = true
	}
	if values.OutputTokens != nil {
		o.usage.OutputTokens = *values.OutputTokens
		o.usage.HasAny = true
	}
	if values.CacheCreationInputTokens != nil {
		o.usage.CacheCreationInputTokens = *values.CacheCreationInputTokens
		o.usage.HasAny = true
	}
	if values.CacheReadInputTokens != nil {
		o.usage.CacheReadInputTokens = *values.CacheReadInputTokens
		o.usage.HasAny = true
	}
}

type usageJSON struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
}
