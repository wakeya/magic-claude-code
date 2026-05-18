package usage

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	bearerPattern     = regexp.MustCompile(`(?i)bearer\s+[-._~+/A-Za-z0-9]+=*`)
	secretKVPattern   = regexp.MustCompile(`(?i)(key|token|secret|auth|password|cookie)([=:])\S+`)
	sensitiveKeyParts = []string{"key", "token", "secret", "auth", "password", "cookie"}
)

func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return sanitizeSecrets(raw, 0)
	}
	q := u.Query()
	for key, values := range q {
		if isSensitiveKey(key) {
			for i := range values {
				values[i] = "[REDACTED]"
			}
			q[key] = values
		}
	}
	u.RawQuery = strings.ReplaceAll(q.Encode(), "%5BREDACTED%5D", "[REDACTED]")
	return u.String()
}

func TruncateUserAgent(ua string) string {
	return truncateBytes(ua, 512)
}

func SanitizeErrorMessage(msg string) string {
	return sanitizeSecrets(msg, 1024)
}

func SanitizeParseError(msg string) string {
	return sanitizeSecrets(msg, 512)
}

func sanitizeSecrets(msg string, limit int) string {
	msg = bearerPattern.ReplaceAllString(msg, "Bearer [REDACTED]")
	msg = secretKVPattern.ReplaceAllString(msg, "$1$2[REDACTED]")
	if limit <= 0 {
		return msg
	}
	return truncateBytes(msg, limit)
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, part := range sensitiveKeyParts {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
