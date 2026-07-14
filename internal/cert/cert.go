package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// 服务器证书有效期：10 年
	serverValidYears = 10
)

// GenerateServerCert 生成服务器证书
func (m *Manager) GenerateServerCert(caCertDER []byte, caKey *rsa.PrivateKey) ([]byte, *rsa.PrivateKey, error) {
	// 解析 CA 证书
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, nil, err
	}

	// 生成私钥
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	// 证书序列号
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}

	// 证书模板
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"MCC Proxy"},
			CommonName:   "api.anthropic.com",
		},
		DNSNames:    []string{"api.anthropic.com", "localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(serverValidYears, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// 使用 CA 签名
	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}

	return certDER, privateKey, nil
}

// SaveServerCert 保存服务器证书（含 CA 证书链）和私钥
func (m *Manager) SaveServerCert(certDER []byte, caCertDER []byte, privateKey *rsa.PrivateKey) error {
	// 保存证书（服务器证书 + CA 证书）
	certPath := filepath.Join(m.dataDir, "server.crt")
	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	}); err != nil {
		return err
	}

	if err := pem.Encode(certFile, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caCertDER,
	}); err != nil {
		return err
	}

	// 保存私钥
	keyPath := filepath.Join(m.dataDir, "server.key")
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyFile.Close()

	return pem.Encode(keyFile, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
}

// LoadServerCert 加载服务器证书（仅首个 PEM block，即叶子证书 DER）和私钥。
// server.crt 由 SaveServerCert 写入"叶子 + CA"两段 PEM，本函数只取第一段，
// 不返回完整证书链。启动 TLS 应直接使用 tls.LoadX509KeyPair 加载文件。
func (m *Manager) LoadServerCert() ([]byte, *rsa.PrivateKey, error) {
	// 加载证书
	certPath := filepath.Join(m.dataDir, "server.crt")
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, ErrInvalidPEM
	}

	// 加载私钥
	keyPath := filepath.Join(m.dataDir, "server.key")
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, ErrInvalidPEM
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	return block.Bytes, privateKey, nil
}

// ServerCertExists 检查服务器证书是否存在
func (m *Manager) ServerCertExists() bool {
	certPath := filepath.Join(m.dataDir, "server.crt")
	keyPath := filepath.Join(m.dataDir, "server.key")

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	return certErr == nil && keyErr == nil
}

// EnsureCA 确保 CA 存在，不存在则生成
func (m *Manager) EnsureCA() ([]byte, *rsa.PrivateKey, error) {
	if m.CAExists() {
		return m.LoadCA()
	}

	caCert, caKey, err := m.GenerateCA()
	if err != nil {
		return nil, nil, err
	}

	if err := m.SaveCA(caCert, caKey); err != nil {
		return nil, nil, err
	}

	return caCert, caKey, nil
}

// ensureServerCertChain 确保 server.crt 包含完整证书链（叶子 + 当前 CA）。
// f168de9 之前生成的存量 server.crt 只含叶子证书，TLS 握手时不发 CA，
// 导致 Bun 后台辅助请求（不读本地 CA 库）验证失败发 unknown_ca。
// 检测到链不完整且 leaf 由当前 CA 签发时，追加当前 CA；检测到链属于旧 CA 时，要求调用方重签。
func (m *Manager) ensureServerCertChain(caCertDER []byte) (bool, error) {
	certPath := m.GetServerCertPath()
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return false, err
	}

	blocks := certificatePEMBlocks(certPEM)
	if len(blocks) == 0 {
		return true, nil
	}
	leaf, err := x509.ParseCertificate(blocks[0].Bytes)
	if err != nil {
		return true, nil
	}
	ca, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return false, err
	}
	if len(blocks) >= 2 {
		if !equalBytes(blocks[1].Bytes, caCertDER) {
			return true, nil
		}
		if err := leaf.CheckSignatureFrom(ca); err != nil {
			return true, nil
		}
		return false, nil
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		return true, nil
	}

	// 修复：追加调用方传入的当前 CA DER，避免磁盘 ca.crt 与入参不一致。
	f, err := os.OpenFile(certPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if len(certPEM) > 0 && certPEM[len(certPEM)-1] != '\n' {
		if _, err := f.Write([]byte("\n")); err != nil {
			return false, err
		}
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: caCertDER}); err != nil {
		return false, err
	}
	return false, nil
}

func countCertificatePEMBlocks(data []byte) int {
	return len(certificatePEMBlocks(data))
}

func certificatePEMBlocks(data []byte) []*pem.Block {
	var blocks []*pem.Block
	rest := data
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			return blocks
		}
		if block.Type == "CERTIFICATE" {
			blocks = append(blocks, block)
		}
		rest = remaining
	}
}

// EnsureServerCert 确保服务器证书存在，不存在则生成
func (m *Manager) EnsureServerCert(caCertDER []byte, caKey *rsa.PrivateKey) ([]byte, *rsa.PrivateKey, error) {
	if m.ServerCertExists() {
		regenerate, err := m.ensureServerCertChain(caCertDER)
		if err != nil {
			return nil, nil, fmt.Errorf("ensure server cert chain: %w", err)
		}
		if !regenerate {
			return m.LoadServerCert()
		}
	}

	serverCert, serverKey, err := m.GenerateServerCert(caCertDER, caKey)
	if err != nil {
		return nil, nil, err
	}

	if err := m.SaveServerCert(serverCert, caCertDER, serverKey); err != nil {
		return nil, nil, err
	}

	return serverCert, serverKey, nil
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

// GetCACertPath 返回 CA 证书路径
func (m *Manager) GetCACertPath() string {
	return filepath.Join(m.dataDir, "ca.crt")
}

// GetServerCertPath 返回服务器证书路径
func (m *Manager) GetServerCertPath() string {
	return filepath.Join(m.dataDir, "server.crt")
}

// GetCertExpiry 获取证书过期时间
func (m *Manager) GetCertExpiry(certDER []byte) (time.Time, error) {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}
