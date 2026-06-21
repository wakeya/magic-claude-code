package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	// CA 证书有效期：10 年
	caValidYears = 10
)

// GenerateCA 生成 CA 证书
func (m *Manager) GenerateCA() ([]byte, *rsa.PrivateKey, error) {
	// 生成私钥
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}

	// 证书模板
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"MCC Proxy Local CA"},
			CommonName:   "MCC Proxy Local CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(caValidYears, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	// 自签名
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}

	return certDER, privateKey, nil
}

// SaveCA 保存 CA 证书和私钥
func (m *Manager) SaveCA(certDER []byte, privateKey *rsa.PrivateKey) error {
	// 保存证书
	certPath := filepath.Join(m.dataDir, "ca.crt")
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

	// 保存私钥
	keyPath := filepath.Join(m.dataDir, "ca.key")
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

// LoadCA 加载 CA 证书和私钥
func (m *Manager) LoadCA() ([]byte, *rsa.PrivateKey, error) {
	// 加载证书
	certPath := filepath.Join(m.dataDir, "ca.crt")
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, ErrInvalidPEM
	}

	// 加载私钥
	keyPath := filepath.Join(m.dataDir, "ca.key")
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

// CAExists 检查 CA 是否存在
func (m *Manager) CAExists() bool {
	certPath := filepath.Join(m.dataDir, "ca.crt")
	keyPath := filepath.Join(m.dataDir, "ca.key")

	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)

	return certErr == nil && keyErr == nil
}