package failover

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// classifyBody 是测试便捷函数：构造一个最小错误 body 并分类。
func classifyBody(t *testing.T, statusCode int, body string) Classification {
	t.Helper()
	c := ClassifyResponse(statusCode, []byte(body), false)
	return c
}

func TestClassify1308WithReset(t *testing.T) {
	// 1308 = 5 小时额度耗尽；body 含未来 reset 时间 → 摘除至 reset。
	future := time.Now().Add(2 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	c := classifyBody(t, 429, `{"error":{"code":1308,"message":"five hour quota exhausted; resets at `+future+`"}}`)

	if !c.Eligible {
		t.Fatalf("1308 must be eligible for failover")
	}
	if c.Kind != StateKindQuota {
		t.Fatalf("Kind = %q, want quota_exhausted", c.Kind)
	}
	if c.Reason != "five_hour_quota_exhausted" {
		t.Fatalf("Reason = %q, want five_hour_quota_exhausted", c.Reason)
	}
	if !c.DisabledUntil.After(time.Now().Add(time.Hour)) {
		t.Fatalf("DisabledUntil = %v, want near the parsed reset (~2h future)", c.DisabledUntil)
	}
	if c.BusinessCode != "1308" {
		t.Fatalf("BusinessCode = %q, want 1308", c.BusinessCode)
	}
}

func TestClassify1308InvalidResetFallsBackTo15m(t *testing.T) {
	// 无效 reset 字符串 → 回退 15 分钟。
	c := classifyBody(t, 429, `{"error":{"code":1308,"message":"quota exhausted; resets at not-a-date"}}`)
	if !c.Eligible || c.Kind != StateKindQuota {
		t.Fatalf("expected eligible quota, got %+v", c)
	}
	if d := time.Until(c.DisabledUntil); d < 13*time.Minute || d > 17*time.Minute {
		t.Fatalf("DisabledUntil delta = %v, want ~15m fallback", d)
	}
}

func TestClassify1308PastResetFallsBackTo15m(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format("2006-01-02 15:04:05")
	c := classifyBody(t, 429, `{"error":{"code":1308,"message":"resets at `+past+`"}}`)
	if d := time.Until(c.DisabledUntil); d < 13*time.Minute || d > 17*time.Minute {
		t.Fatalf("past reset should fall back to 15m, delta = %v", d)
	}
}

func TestClassify1308FarFutureResetFallsBackTo15m(t *testing.T) {
	// 超过 7 天的 reset 视为不可信，回退 15 分钟。
	far := time.Now().Add(8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	c := classifyBody(t, 429, `{"error":{"code":1308,"message":"resets at `+far+`"}}`)
	if d := time.Until(c.DisabledUntil); d < 13*time.Minute || d > 17*time.Minute {
		t.Fatalf(">7d reset should fall back to 15m, delta = %v", d)
	}
}

func TestClassify1310WithReset(t *testing.T) {
	future := time.Now().Add(36 * time.Hour).UTC().Format(time.RFC3339)
	c := classifyBody(t, 429, `{"error":{"code":1310,"message":"weekly quota exhausted; reset `+future+`"}}`)
	if !c.Eligible || c.Kind != StateKindQuota {
		t.Fatalf("expected eligible quota, got %+v", c)
	}
	if c.Reason != "weekly_quota_exhausted" {
		t.Fatalf("Reason = %q, want weekly_quota_exhausted", c.Reason)
	}
	if !c.DisabledUntil.After(time.Now().Add(30 * time.Hour)) {
		t.Fatalf("DisabledUntil should match the parsed future reset")
	}
}

func TestClassifyQuotaExhaustedText(t *testing.T) {
	// 无 code 但 message 含 "quota exhausted" → 额度耗尽，15m。
	c := classifyBody(t, 429, `{"error":{"message":"quota exhausted for this key"}}`)
	if !c.Eligible || c.Kind != StateKindQuota {
		t.Fatalf("expected eligible quota from text, got %+v", c)
	}
	if d := time.Until(c.DisabledUntil); d < 13*time.Minute || d > 17*time.Minute {
		t.Fatalf("delta = %v, want ~15m", d)
	}
}

func TestClassifyHealthyDeployment400(t *testing.T) {
	c := classifyBody(t, 400, `{"error":{"message":"no healthy deployments for this model"}}`)
	if !c.Eligible || c.Kind != StateKindDeployment {
		t.Fatalf("expected eligible deployment_unavailable, got %+v", c)
	}
	if d := time.Until(c.DisabledUntil); d < 50*time.Second || d > 80*time.Second {
		t.Fatalf("delta = %v, want ~1m", d)
	}
}

func TestClassifyInvalidAPIKey401(t *testing.T) {
	c := classifyBody(t, 401, `{"error":{"message":"invalid x-api-key"}}`)
	if !c.Eligible || c.Kind != StateKindCredential {
		t.Fatalf("expected eligible credential_invalid, got %+v", c)
	}
	if !c.DisabledUntil.IsZero() {
		t.Fatalf("credential failure must have no time-based recovery, got %v", c.DisabledUntil)
	}
}

func TestClassifyCloudflare403(t *testing.T) {
	c := classifyBody(t, 403, `{"error":"client is blocked","headers":{"cf-ray":"abc123"}}`)
	if !c.Eligible || c.Kind != StateKindAvailability {
		t.Fatalf("expected eligible availability for Cloudflare 403, got %+v", c)
	}
	if d := time.Until(c.DisabledUntil); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("Cloudflare delta = %v, want ~5m", d)
	}
}

func TestClassifyNonCloudflare403DoesNotFailover(t *testing.T) {
	// 普通 403（无 Cloudflare 特征）不切换：可能是权限问题，切换无意义。
	c := classifyBody(t, 403, `{"error":"forbidden"}`)
	if c.Eligible {
		t.Fatalf("non-Cloudflare 403 must not fail over, got %+v", c)
	}
}

func TestClassifyAvailabilityFailures(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
	}{
		{"502", 502, `{"error":"bad gateway"}`},
		{"529", 529, `{"error":"service overloaded"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := classifyBody(t, tt.code, tt.body)
			if !c.Eligible || c.Kind != StateKindAvailability {
				t.Fatalf("expected eligible availability, got %+v", c)
			}
			if d := time.Until(c.DisabledUntil); d < 50*time.Second || d > 80*time.Second {
				t.Fatalf("delta = %v, want ~1m", d)
			}
		})
	}
}

func TestClassifyNetworkErrorAvailability(t *testing.T) {
	// ECONNRESET / 连接重置属于可用性失败（1m），没有响应体。
	c := ClassifyError(errConnectionReset("read tcp: connection reset by peer"))
	if !c.Eligible || c.Kind != StateKindAvailability {
		t.Fatalf("expected eligible availability for connection reset, got %+v", c)
	}
	if d := time.Until(c.DisabledUntil); d < 50*time.Second || d > 80*time.Second {
		t.Fatalf("delta = %v, want ~1m", d)
	}
}

func TestBare429DoesNotFailover(t *testing.T) {
	// 裸 429（无 1308/1310/额度文字）：保持同供应商 retry，不切换。
	c := classifyBody(t, 429, `{"error":{"message":"rate limited"}}`)
	if c.Eligible {
		t.Fatalf("bare 429 must not fail over (keep same-provider retry), got %+v", c)
	}
}

func TestClassify1210DoesNotFailover(t *testing.T) {
	c := classifyBody(t, 400, `{"error":{"code":1210,"message":"invalid request"}}`)
	if c.Eligible {
		t.Fatalf("1210 must not fail over, got %+v", c)
	}
}

func TestClassifyGeneric400DoesNotFailover(t *testing.T) {
	c := classifyBody(t, 400, `{"error":{"message":"invalid request body"}}`)
	if c.Eligible {
		t.Fatalf("generic 400 request error must not fail over, got %+v", c)
	}
}

func TestModelNotFoundDoesNotFailover(t *testing.T) {
	c := classifyBody(t, 404, `{"error":{"type":"model_not_found","message":"model does not exist"}}`)
	if c.Eligible {
		t.Fatalf("model_not_found 404 must not fail over, got %+v", c)
	}
}

func TestContextLimitDoesNotFailover(t *testing.T) {
	c := classifyBody(t, 400, `{"error":{"message":"prompt is too long: context length exceeded"}}`)
	if c.Eligible {
		t.Fatalf("context limit must not fail over, got %+v", c)
	}
}

func TestToolCompatibilityErrorsDoNotFailover(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"tool_reference", `{"error":{"message":"unknown tool_reference block"}}`},
		{"tool_validation", `{"error":{"message":"tools.1.input_schema validation failed"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := classifyBody(t, 400, tt.body)
			if c.Eligible {
				t.Fatalf("%s must not fail over, got %+v", tt.name, c)
			}
		})
	}
}

func TestClassifierRestoresNonEligibleBody(t *testing.T) {
	// 非合格响应（model_not_found）的 body 必须逐字节还原，供代理透传给客户端。
	original := []byte(`{"error":{"type":"model_not_found","message":"x","req_id":"abc-123-secret"}}`)
	captured, restored, oversize := CaptureBody(bytes.NewReader(original), 64*1024)
	if oversize {
		t.Fatalf("small body must not be oversize")
	}
	c := ClassifyResponse(404, captured, oversize)
	if c.Eligible {
		t.Fatalf("model_not_found must not be eligible")
	}
	got, err := io.ReadAll(restored)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("restored body mismatch:\n got=%q\nwant=%q", got, original)
	}
}

func TestClassifierRestoresEligibleBody(t *testing.T) {
	// 合格响应也必须能还原 body（代理透传最终错误）。
	original := []byte(`{"error":{"code":1308,"message":"five hour quota exhausted"}}`)
	captured, restored, oversize := CaptureBody(bytes.NewReader(original), 64*1024)
	c := ClassifyResponse(429, captured, oversize)
	if !c.Eligible {
		t.Fatalf("1308 must be eligible")
	}
	got, _ := io.ReadAll(restored)
	if !bytes.Equal(got, original) {
		t.Fatalf("eligible body must still restore byte-for-byte:\n got=%q\nwant=%q", got, original)
	}
}

func TestOversizedBodyDoesNotFailover(t *testing.T) {
	// 超过 64 KiB 的 body：不解析、不切换，且完整还原。
	big := bytes.Repeat([]byte("a"), 64*1024+50)
	captured, restored, oversize := CaptureBody(bytes.NewReader(big), 64*1024)
	if !oversize {
		t.Fatalf("body > 64KiB must be oversize")
	}
	c := ClassifyResponse(429, captured, oversize)
	if c.Eligible {
		t.Fatalf("oversized body must not fail over")
	}
	got, _ := io.ReadAll(restored)
	if len(got) != len(big) {
		t.Fatalf("restored len = %d, want %d (full body must restore)", len(got), len(big))
	}
	if !bytes.Equal(got, big) {
		t.Fatalf("oversized body must restore byte-for-byte")
	}
}

func TestCaptureBodyExactlyAtLimit(t *testing.T) {
	// 恰好 64 KiB 的 body 不算超限。
	exact := bytes.Repeat([]byte("b"), 64*1024)
	_, _, oversize := CaptureBody(bytes.NewReader(exact), 64*1024)
	if oversize {
		t.Fatalf("body exactly at 64KiB must not be oversize")
	}
}

func TestClassifierDoesNotLeakSecretsIntoReason(t *testing.T) {
	// UpstreamError 摘要不得包含 token / API key 等敏感片段。
	c := classifyBody(t, 401, `{"error":{"message":"invalid key sk-ant-super-secret-value-12345"}}`)
	if strings.Contains(c.UpstreamError, "sk-ant-super-secret-value-12345") {
		t.Fatalf("UpstreamError must not leak the raw key: %q", c.UpstreamError)
	}
}
