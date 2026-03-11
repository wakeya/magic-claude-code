package admin

import (
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	auth := NewAuth("test-password")

	// 验证正确密码
	if !auth.VerifyPassword("test-password") {
		t.Error("expected password to be verified")
	}

	// 验证错误密码
	if auth.VerifyPassword("wrong-password") {
		t.Error("expected wrong password to be rejected")
	}
}

func TestSessionToken(t *testing.T) {
	auth := NewAuth("test-password")

	// 生成 token
	token := auth.GenerateToken()
	if token == "" {
		t.Error("expected non-empty token")
	}

	// 验证 token
	if !auth.ValidateToken(token) {
		t.Error("expected token to be valid")
	}

	// 验证无效 token
	if auth.ValidateToken("invalid-token") {
		t.Error("expected invalid token to be rejected")
	}
}

func TestLoginAttemptLimit(t *testing.T) {
	auth := NewAuthWithConfig("test-password", 3, 1*time.Minute)

	// 3 次失败后应该被锁定
	for i := 0; i < 3; i++ {
		auth.RecordFailedAttempt()
	}

	if !auth.IsLocked() {
		t.Error("expected account to be locked after 3 failed attempts")
	}

	// 正确密码也应该被拒绝
	if auth.VerifyPassword("test-password") {
		t.Error("expected password verification to fail when locked")
	}
}