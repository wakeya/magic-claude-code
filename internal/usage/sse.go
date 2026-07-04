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

	// diagnostics 状态
	diagEvents            map[string]int
	diagContentBlockTypes map[string]int
	diagStopReasons       map[string]int
	diagErrorEvents       int
	diagErrorTypes        map[string]int
	diagNumericErrorCodes map[string]int
	diagParseErrors       int
}

// SSEDiagnostics 是 SSE 流的有界结构化元数据。
// 永不包含生成的 text、thinking、tool input/result 内容、error.message 或任意 event/type 字符串。
type SSEDiagnostics struct {
	Complete          bool           `json:"complete"`
	ParseErrors       int            `json:"parse_errors"`
	Events            map[string]int `json:"events"`
	ContentBlockTypes map[string]int `json:"content_block_types"`
	StopReasons       map[string]int `json:"stop_reasons"`
	ErrorEvents       int            `json:"error_events"`
	ErrorTypes        map[string]int `json:"error_types"`
	NumericErrorCodes map[string]int `json:"numeric_error_codes"`
}

// 事件白名单
var sseValidEvents = map[string]bool{
	"message_start":       true,
	"content_block_start": true,
	"content_block_delta": true,
	"content_block_stop":  true,
	"message_delta":       true,
	"message_stop":        true,
	"error":               true,
	"ping":                true,
}

// content block 类型白名单
var sseValidContentBlockTypes = map[string]bool{
	"text":                   true,
	"thinking":               true,
	"redacted_thinking":      true,
	"tool_use":               true,
	"server_tool_use":        true,
	"web_search_tool_result": true,
}

// stop reason 白名单
var sseValidStopReasons = map[string]bool{
	"end_turn":                      true,
	"max_tokens":                    true,
	"tool_use":                      true,
	"stop_sequence":                 true,
	"pause_turn":                    true,
	"refusal":                       true,
	"model_context_window_exceeded": true,
}

// error type 白名单
var sseValidErrorTypes = map[string]bool{
	"invalid_request_error": true,
	"api_error":             true,
	"authentication_error":  true,
	"permission_error":      true,
	"rate_limit_error":      true,
	"overloaded_error":      true,
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

	// 事件计数器（在 ping 早返回之前）
	o.countEvent(event)

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
		ContentBlock struct {
			Type string `json:"type"`
		} `json:"content_block"`
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Error struct {
			Type string          `json:"type"`
			Code json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		o.parseError = true
		o.diagParseErrors++
		return
	}
	o.merge(payload.Message.Usage)
	o.merge(payload.Usage)

	// content_block.type 计数
	if payload.ContentBlock.Type != "" {
		o.countContentBlockType(payload.ContentBlock.Type)
	}

	// delta.stop_reason 计数
	if payload.Delta.StopReason != "" {
		o.countStopReason(payload.Delta.StopReason)
	}

	// error 计数
	if event == "error" || payload.Type == "error" {
		o.diagErrorEvents++
		if payload.Error.Type != "" {
			o.countErrorType(payload.Error.Type)
		}
		o.classifyErrorCode(payload.Error.Code)
	}

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

// --- diagnostics 辅助方法 ---

func (o *SSEObserver) ensureDiagMaps() {
	if o.diagEvents == nil {
		o.diagEvents = make(map[string]int)
	}
	if o.diagContentBlockTypes == nil {
		o.diagContentBlockTypes = make(map[string]int)
	}
	if o.diagStopReasons == nil {
		o.diagStopReasons = make(map[string]int)
	}
	if o.diagErrorTypes == nil {
		o.diagErrorTypes = make(map[string]int)
	}
	if o.diagNumericErrorCodes == nil {
		o.diagNumericErrorCodes = make(map[string]int)
	}
}

func (o *SSEObserver) countEvent(event string) {
	if event == "" {
		return
	}
	o.ensureDiagMaps()
	if sseValidEvents[event] {
		o.diagEvents[event]++
	} else {
		o.diagEvents["other"]++
	}
}

func (o *SSEObserver) countContentBlockType(typ string) {
	o.ensureDiagMaps()
	if sseValidContentBlockTypes[typ] {
		o.diagContentBlockTypes[typ]++
	} else {
		o.diagContentBlockTypes["other"]++
	}
}

func (o *SSEObserver) countStopReason(reason string) {
	o.ensureDiagMaps()
	if sseValidStopReasons[reason] {
		o.diagStopReasons[reason]++
	} else {
		o.diagStopReasons["other"]++
	}
}

func (o *SSEObserver) countErrorType(typ string) {
	o.ensureDiagMaps()
	if sseValidErrorTypes[typ] {
		o.diagErrorTypes[typ]++
	} else {
		o.diagErrorTypes["other"]++
	}
}

// maxDistinctNumericErrorCodes 限制 NumericErrorCodes map 中不同数字码的最大数量。
// 超过上限的新数字码累计到 "other" key；已存在的码不受影响。
// 防止恶意/被攻陷供应商通过大量不同错误码消耗内存（CWE-400 硬化）。
const maxDistinctNumericErrorCodes = 16

// classifyErrorCode 将 error.code (JSON RawMessage) 分类为数字错误码。
// code 可能是 JSON 字符串 ("1210") 或 JSON 数字 (1210)。
// 只有 1-8 位纯数字码作为原值 key；非数字或超过 8 位 → "other"。
// 空/缺失 → 不动这个 map。不同 key 总数上限为 maxDistinctNumericErrorCodes。
func (o *SSEObserver) classifyErrorCode(code json.RawMessage) {
	if len(code) == 0 {
		return
	}
	o.ensureDiagMaps()

	// 先尝试 unmarshal 成 string
	var s string
	if err := json.Unmarshal(code, &s); err == nil {
		if isAllDigits(s) && len(s) >= 1 && len(s) <= 8 {
			if _, exists := o.diagNumericErrorCodes[s]; !exists && len(o.diagNumericErrorCodes) >= maxDistinctNumericErrorCodes {
				o.diagNumericErrorCodes["other"]++
				return
			}
			o.diagNumericErrorCodes[s]++
			return
		}
		o.diagNumericErrorCodes["other"]++
		return
	}

	// 尝试 unmarshal 成 json.Number
	var n json.Number
	if err := json.Unmarshal(code, &n); err == nil {
		s := string(n)
		if isAllDigits(s) && len(s) >= 1 && len(s) <= 8 {
			if _, exists := o.diagNumericErrorCodes[s]; !exists && len(o.diagNumericErrorCodes) >= maxDistinctNumericErrorCodes {
				o.diagNumericErrorCodes["other"]++
				return
			}
			o.diagNumericErrorCodes[s]++
			return
		}
	}

	o.diagNumericErrorCodes["other"]++
}

// isAllDigits 检查字符串是否全为 ASCII 数字。
func isAllDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Diagnostics 返回 SSE 流的有界结构化诊断元数据的拷贝。
// 调用方无法通过返回值修改观察者内部状态。
func (o *SSEObserver) Diagnostics() SSEDiagnostics {
	o.ensureDiagMaps()
	return SSEDiagnostics{
		Complete:          o.complete,
		ParseErrors:       o.diagParseErrors,
		Events:            copyIntMap(o.diagEvents),
		ContentBlockTypes: copyIntMap(o.diagContentBlockTypes),
		StopReasons:       copyIntMap(o.diagStopReasons),
		ErrorEvents:       o.diagErrorEvents,
		ErrorTypes:        copyIntMap(o.diagErrorTypes),
		NumericErrorCodes: copyIntMap(o.diagNumericErrorCodes),
	}
}

func copyIntMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type usageJSON struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
}
