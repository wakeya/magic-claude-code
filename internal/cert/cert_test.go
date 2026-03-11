package cert

import (
	"crypto/x509"
	"os"
	"testing"
	"time"
)

func TestGenerateServerCert(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cert-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	manager := NewManager(tmpDir)

	// 先生成 CA
	caCert, caKey, err := manager.GenerateCA()
	if err != nil {
		t.Fatalf("failed to generate CA: %v", err)
	}

	// 生成服务器证书
	serverCert, serverKey, err := manager.GenerateServerCert(caCert, caKey)
	if err != nil {
		t.Fatalf("failed to generate server cert: %v", err)
	}

	// 解析证书
	cert, err := x509.ParseCertificate(serverCert)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	// 验证域名
	if len(cert.DNSNames) == 0 || cert.DNSNames[0] != "api.anthropic.com" {
		t.Errorf("expected DNS name api.anthropic.com, got %v", cert.DNSNames)
	}

	// 验证 Common Name
	if cert.Subject.CommonName != "api.anthropic.com" {
		t.Errorf("expected CN=api.anthropic.com, got %s", cert.Subject.CommonName)
	}

	// 验证有效期 (10年)
	validFor := cert.NotAfter.Sub(cert.NotBefore)
	expectedDuration := 10 * 365 * 24 * time.Hour
	tolerance := 72 * time.Hour // 使用72小时容差以处理闰年

	if validFor < expectedDuration-tolerance || validFor > expectedDuration+tolerance {
		t.Errorf("expected validity ~10 years, got %v", validFor)
	}

	// 验证私钥
	if serverKey == nil {
		t.Error("expected private key to be returned")
	}
}

func TestEnsureCertificates(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cert-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	manager := NewManager(tmpDir)

	// 确保 CA 证书
	caCert, caKey, err := manager.EnsureCA()
	if err != nil {
		t.Fatalf("failed to ensure CA: %v", err)
	}

	if caCert == nil || caKey == nil {
		t.Error("expected CA cert and key to be returned")
	}

	// 确保服务器证书
	serverCert, serverKey, err := manager.EnsureServerCert(caCert, caKey)
	if err != nil {
		t.Fatalf("failed to ensure server cert: %v", err)
	}

	if serverCert == nil || serverKey == nil {
		t.Error("expected server cert and key to be returned")
	}
}