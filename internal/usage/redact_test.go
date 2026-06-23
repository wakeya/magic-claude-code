package usage

import (
	"strings"
	"testing"
)

func TestRedactURLRemovesSensitiveQueryValues(t *testing.T) {
	got := RedactURL("https://api.example.com/v1/messages?api_key=secret&token=abc&model=claude&password=pw")

	for _, secret := range []string{"secret", "abc", "pw"} {
		if strings.Contains(got, secret) {
			t.Fatalf("RedactURL leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "model=claude") {
		t.Fatalf("RedactURL removed safe query value: %q", got)
	}
	if strings.Count(got, "[REDACTED]") != 3 {
		t.Fatalf("expected three redacted values, got %q", got)
	}
}

// TestRedactURLStripsUserinfo 验证 https://user:pass@host 形式的凭证被剥离，
// 防止历史脏数据的 userinfo 通过 usage 统计泄露到前端。
func TestRedactURLStripsUserinfo(t *testing.T) {
	cases := []string{
		"https://user:pass@api.example.com/v1",
		"https://token123@host.example/path",
	}
	for _, in := range cases {
		got := RedactURL(in)
		if strings.Contains(got, "user:pass@") {
			t.Errorf("userinfo credentials leaked: %q -> %q", in, got)
		}
		if strings.Contains(got, "token123@") {
			t.Errorf("userinfo leaked: %q -> %q", in, got)
		}
	}
}

func TestTruncateUserAgentLimitsTo512Bytes(t *testing.T) {
	got := TruncateUserAgent(strings.Repeat("a", 600))

	if len(got) != 512 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestSanitizeErrorMessageRedactsTokensAndLimitsTo1024Bytes(t *testing.T) {
	got := SanitizeErrorMessage("Authorization: Bearer secret-token " + strings.Repeat("x", 1200))

	if strings.Contains(got, "secret-token") {
		t.Fatalf("SanitizeErrorMessage leaked bearer token: %q", got)
	}
	if len(got) != 1024 {
		t.Fatalf("len = %d", len(got))
	}
	if !strings.Contains(got, "Bearer [REDACTED]") {
		t.Fatalf("expected bearer token redaction, got %q", got)
	}
}

func TestSanitizeParseErrorLimitsTo512Bytes(t *testing.T) {
	got := SanitizeParseError("token=secret " + strings.Repeat("x", 700))

	if strings.Contains(got, "secret") {
		t.Fatalf("SanitizeParseError leaked token: %q", got)
	}
	if len(got) != 512 {
		t.Fatalf("len = %d", len(got))
	}
}
