package failover

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxClassifyBodyBytes 是分类器最多读取的响应体字节数。超过即视为非合格（不切换）。
	// 导出供代理（handler）与测试复用，避免 64KiB 阈值在多处定义产生漂移。
	MaxClassifyBodyBytes = 64 * 1024

	cooldownQuotaFallback = 15 * time.Minute // 额度耗尽但无可信 reset 时间时的回退冷却
	cooldownDeployment    = 1 * time.Minute  // 模型部署不可用
	cooldownAvailability  = 1 * time.Minute  // 502/529/ECONNRESET 等可用性故障
	cooldownCloudflare    = 5 * time.Minute  // Cloudflare 403
	maxResetHorizon       = 7 * 24 * time.Hour // 超过此范围的 reset 时间视为不可信
)

// CaptureBody reads up to maxBytes+1 bytes from r and returns the captured
// prefix, a restored reader that replays the full original stream byte-for-byte,
// and whether the body exceeded maxBytes (oversize → caller treats as
// non-eligible). restored is always non-nil.
//
// 读-还原契约：无论是否超限，restored 都能完整重放原始 body——代理据此把上游
// 错误响应原样透传给客户端，分类绝不破坏响应体。
func CaptureBody(r io.Reader, maxBytes int) (captured []byte, restored io.Reader, oversize bool) {
	captured, _ = io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	oversize = len(captured) > maxBytes
	if oversize {
		// 已读 maxBytes+1 字节前缀；剩余未读部分仍在 r 中。
		restored = io.MultiReader(bytes.NewReader(captured), r)
	} else {
		// body ≤ maxBytes，已全部读入 captured。
		restored = bytes.NewReader(captured)
	}
	return captured, restored, oversize
}

// ClassifyResponse classifies an upstream HTTP response. captured is the already
// read body prefix (≤ maxBytes or maxBytes+1); oversize indicates the body
// exceeded the read cap (→ never eligible). The classifier inspects status code
// plus parsed error fields and never treats a bare status code as sufficient.
func ClassifyResponse(statusCode int, captured []byte, oversize bool) Classification {
	if oversize {
		return Classification{Eligible: false, UpstreamCode: statusCode}
	}
	pe := parseErrorBody(captured)
	msg := strings.ToLower(pe.message)
	now := time.Now()

	switch statusCode {
	case httpStatusTooManyRequests: // 429
		if codeIs(pe, "1308") {
			return eligible(StateKindQuota, "five_hour_quota_exhausted", statusCode, pe, resetTime(pe.message, now.Add(cooldownQuotaFallback)))
		}
		if codeIs(pe, "1310") {
			return eligible(StateKindQuota, "weekly_quota_exhausted", statusCode, pe, resetTime(pe.message, now.Add(cooldownQuotaFallback)))
		}
		if containsQuotaExhausted(msg) {
			return eligible(StateKindQuota, "quota_exhausted", statusCode, pe, now.Add(cooldownQuotaFallback))
		}
		// 裸 429 / 普通限速：优先保持同供应商 retry，不切换。
		return Classification{Eligible: false, UpstreamCode: statusCode}
	case httpStatusBadRequest: // 400
		if containsHealthyDeployment(msg) {
			return eligible(StateKindDeployment, "deployment_unavailable", statusCode, pe, now.Add(cooldownDeployment))
		}
		// 1210、tool 校验、tool_reference、上下文超限、普通请求错误：均不切换。
		return Classification{Eligible: false, UpstreamCode: statusCode}
	case httpStatusUnauthorized: // 401
		return eligible(StateKindCredential, "credential_invalid", statusCode, pe, time.Time{})
	case httpStatusForbidden: // 403
		if isCloudflare(captured) {
			return eligible(StateKindAvailability, "cloudflare_blocked", statusCode, pe, now.Add(cooldownCloudflare))
		}
		// 非 Cloudflare 的 403 多为权限/策略问题，切换无意义。
		return Classification{Eligible: false, UpstreamCode: statusCode}
	case httpStatusBadGateway, 529: // 502 / 529
		return eligible(StateKindAvailability, availabilityReason(statusCode), statusCode, pe, now.Add(cooldownAvailability))
	}
	return Classification{Eligible: false, UpstreamCode: statusCode}
}

func eligible(kind StateKind, reason string, statusCode int, pe parsedError, disabledUntil time.Time) Classification {
	return Classification{
		Eligible:      true,
		Kind:          kind,
		Reason:        reason,
		UpstreamCode:  statusCode,
		BusinessCode:  businessCodeFrom(pe),
		UpstreamError: safeSummary(pe),
		DisabledUntil: disabledUntil,
	}
}

// businessCodeFrom 返回脱敏后的业务错误码字符串（来自 error.code），无则为空。
func businessCodeFrom(pe parsedError) string {
	if pe.code != 0 {
		return strconv.Itoa(pe.code)
	}
	return pe.codeStr
}

func availabilityReason(statusCode int) string {
	switch statusCode {
	case httpStatusBadGateway:
		return "bad_gateway"
	case 529:
		return "service_overloaded"
	default:
		return "provider_unavailable"
	}
}

// ClassifyError classifies a network/transport error (no HTTP response).
// 连接重置、拒绝连接、DNS 失败、超时等归为可用性故障（1m 摘除）。
func ClassifyError(err error) Classification {
	if err == nil {
		return Classification{}
	}
	if isNetworkUnavailable(err) {
		return Classification{
			Eligible:      true,
			Kind:          StateKindAvailability,
			Reason:        "network_error",
			UpstreamError: redactMessage(err.Error()),
			DisabledUntil: time.Now().Add(cooldownAvailability),
		}
	}
	return Classification{}
}

// parsedError 是从错误响应体解析出的字段集合。
type parsedError struct {
	code    int    // 数值 error.code（如 1308）
	codeStr string // 字符串 error.code
	message string // 合并后的 message（error.message 或顶级 message 或字符串 error）
}

// parseErrorBody 只解析 error.code / error.message / code / message / 字符串 error，
// 不解析也不保留其它字段，避免误吞敏感信息。
func parseErrorBody(body []byte) parsedError {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return parsedError{}
	}
	var pe parsedError
	if errObj, ok := root["error"].(map[string]any); ok {
		pe.code, pe.codeStr = extractCode(errObj["code"])
		if msg, ok := errObj["message"].(string); ok {
			pe.message = strings.TrimSpace(msg)
		}
	} else if errStr, ok := root["error"].(string); ok {
		pe.message = strings.TrimSpace(errStr)
	}
	if pe.code == 0 && pe.codeStr == "" {
		pe.code, pe.codeStr = extractCode(root["code"])
	}
	if pe.message == "" {
		if msg, ok := root["message"].(string); ok {
			pe.message = strings.TrimSpace(msg)
		}
	}
	return pe
}

func extractCode(v any) (int, string) {
	switch c := v.(type) {
	case float64:
		return int(c), ""
	case int:
		return c, ""
	case string:
		return 0, c
	}
	return 0, ""
}

// codeIs 报告业务码是否匹配 want（同时接受数值与字符串形式，如 1308 与 "1308"）。
// 不同供应商对 error.code 的序列化方式不一，必须等价处理。
func codeIs(pe parsedError, want string) bool {
	if pe.codeStr == want {
		return true
	}
	if n, err := strconv.Atoi(want); err == nil && pe.code == n {
		return true
	}
	return false
}

func containsQuotaExhausted(lowerMsg string) bool {
	return strings.Contains(lowerMsg, "quota exhausted") ||
		strings.Contains(lowerMsg, "quota_exhausted") ||
		strings.Contains(lowerMsg, "额度耗尽") ||
		strings.Contains(lowerMsg, "额度已耗尽")
}

func containsHealthyDeployment(lowerMsg string) bool {
	return strings.Contains(lowerMsg, "no healthy deployments") ||
		strings.Contains(lowerMsg, "no healthy deployment") ||
		strings.Contains(lowerMsg, "model is not deployed") ||
		strings.Contains(lowerMsg, "no deployment available")
}

func isCloudflare(body []byte) bool {
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "cf-ray") ||
		strings.Contains(lower, "cloudflare") ||
		strings.Contains(lower, "error 1015") || // Cloudflare rate-limit page
		strings.Contains(lower, "error reference ")
}

// HTTP status constants (avoid importing net/http here to keep the package small).
const (
	httpStatusBadRequest      = 400
	httpStatusUnauthorized    = 401
	httpStatusForbidden       = 403
	httpStatusTooManyRequests = 429
	httpStatusBadGateway      = 502
)

// resetTime scans message for a parseable future timestamp within maxResetHorizon.
// Returns fallback when no timestamp is found or it is past/>7d (不可信)。
func resetTime(message string, fallback time.Time) time.Time {
	t, ok := findTimestamp(message)
	if !ok {
		return fallback
	}
	now := time.Now()
	if t.After(now) && t.Before(now.Add(maxResetHorizon)) {
		return t
	}
	return fallback
}

// timestampRe 匹配 RFC3339 与 "2006-01-02 15:04:05" 形式的时间戳子串。
var timestampRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)

func findTimestamp(message string) (time.Time, bool) {
	for _, m := range timestampRe.FindAllString(message, -1) {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05", "2006-01-02 15:04:05Z", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, m); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// safeSummary 返回脱敏后的上游错误摘要：优先用数值/字符串 code（无敏感信息），
// 否则对 message 做密钥脱敏 + 长度截断。绝不原样回吐可能含 token 的 message。
func safeSummary(pe parsedError) string {
	if pe.code != 0 {
		return strconv.Itoa(pe.code)
	}
	if pe.codeStr != "" {
		return pe.codeStr
	}
	return redactMessage(pe.message)
}

var keyRedactRe = regexp.MustCompile(`(?i)(sk-[a-z0-9._-]{4,}|bearer\s+[a-z0-9._-]{4,}|[a-f0-9]{32,}|[A-Za-z0-9_-]{40,})`)

func redactMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	msg = keyRedactRe.ReplaceAllString(msg, "[redacted]")
	// 截断长消息，避免事件记录超大文本。
	if len(msg) > 120 {
		msg = msg[:120] + "..."
	}
	return msg
}

func isNetworkUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection reset") ||
		strings.Contains(s, "connreset") ||
		strings.Contains(s, "econnreset") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "server closed connection")
}

// errConnectionReset 是测试用的网络错误类型，模拟 ECONNRESET。
type errConnectionReset string

func (e errConnectionReset) Error() string { return string(e) }
