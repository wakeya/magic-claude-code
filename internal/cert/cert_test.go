package cert

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
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

	// 验证 Organization
	if len(cert.Subject.Organization) == 0 || cert.Subject.Organization[0] != "MCC Proxy" {
		t.Errorf("expected Organization=MCC Proxy, got %v", cert.Subject.Organization)
	}

	// 验证 CA 证书命名
	caCertificate, err := x509.ParseCertificate(caCert)
	if err != nil {
		t.Fatalf("failed to parse CA certificate: %v", err)
	}
	if len(caCertificate.Subject.Organization) == 0 || caCertificate.Subject.Organization[0] != "MCC Proxy Local CA" {
		t.Errorf("expected CA Organization=MCC Proxy Local CA, got %v", caCertificate.Subject.Organization)
	}
	if caCertificate.Subject.CommonName != "MCC Proxy Local CA" {
		t.Errorf("expected CA CN=MCC Proxy Local CA, got %s", caCertificate.Subject.CommonName)
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

func TestSaveServerCertWritesChain(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cert-chain-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	manager := NewManager(tmpDir)

	caCert, caKey, err := manager.GenerateCA()
	if err != nil {
		t.Fatalf("failed to generate CA: %v", err)
	}

	serverCert, serverKey, err := manager.GenerateServerCert(caCert, caKey)
	if err != nil {
		t.Fatalf("failed to generate server cert: %v", err)
	}

	if err := manager.SaveServerCert(serverCert, caCert, serverKey); err != nil {
		t.Fatalf("failed to save server cert: %v", err)
	}

	certPEM, err := os.ReadFile(filepath.Join(tmpDir, "server.crt"))
	if err != nil {
		t.Fatalf("failed to read server.crt: %v", err)
	}

	var blocks []*pem.Block
	rest := certPEM
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		rest = remaining
	}

	if len(blocks) != 2 {
		t.Fatalf("expected 2 PEM blocks (server + CA), got %d", len(blocks))
	}

	if blocks[0].Type != "CERTIFICATE" || blocks[1].Type != "CERTIFICATE" {
		t.Errorf("expected both blocks to be CERTIFICATE type, got %s and %s", blocks[0].Type, blocks[1].Type)
	}

	if !equalBytes(blocks[0].Bytes, serverCert) {
		t.Error("first PEM block should be server certificate")
	}
	if !equalBytes(blocks[1].Bytes, caCert) {
		t.Error("second PEM block should be CA certificate")
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}