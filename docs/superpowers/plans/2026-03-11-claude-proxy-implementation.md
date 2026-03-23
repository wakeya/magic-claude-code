# Claude Code 透明代理实现计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建透明代理服务，让 Claude Code 误以为在与官方 API 通信，提供前端配置页面管理后端地址。

**Architecture:** Go 单进程应用，包含代理服务(:443)和配置服务(:8442)，使用 Gin 框架，JSON 文件存储配置，自动生成 CA 证书。

**Tech Stack:** Go 1.26, Gin, 原生 HTML/CSS/JS, Docker (Alpine)

---

## 文件结构

```
claude-proxy/
├── cmd/
│   └── server/
│       └── main.go                    # 应用入口
│
├── internal/
│   ├── config/
│   │   ├── config.go                  # 配置数据结构和加载
│   │   └── store.go                   # 配置持久化存储
│   │
│   ├── cert/
│   │   ├── ca.go                      # CA 证书生成
│   │   ├── cert.go                    # 服务器证书签发
│   │   └── manager.go                 # 证书生命周期管理
│   │
│   ├── proxy/
│   │   ├── server.go                  # 代理服务器
│   │   └── handler.go                 # 请求处理
│   │
│   ├── admin/
│   │   ├── server.go                  # 配置服务
│   │   ├── handler.go                 # API 处理器
│   │   └── auth.go                    # 认证中间件
│   │
│   └── frontend/
│       ├── embed.go                   # 静态文件嵌入
│       └── dist/
│           ├── index.html             # 配置页面
│           └── login.html             # 登录页面
│
├── data/                              # 数据目录 (Docker 映射)
│
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── go.mod
├── go.sum
├── CLAUDE.md
├── README.md
└── AGENT.md
```

---

## Chunk 1: 项目初始化和配置模块

### Task 1: 项目初始化

**Files:**

- Create: `go.mod`
- Create: `Makefile`

- [ ] **Step 1: 初始化 Go 模块**

```bash
cd /home/www/workspace/claude_code
go mod init claude-proxy
```

- [ ] **Step 2: 创建 Makefile**

```makefile
.PHONY: build run test clean docker

# 默认后端地址
DEFAULT_BACKEND ?= https://coding.dashscope.aliyuncs.com/apps/anthropic

build:
 CGO_ENABLED=0 go build -o bin/claude-proxy ./cmd/server

run:
 go run ./cmd/server

test:
 go test -v -race -coverprofile=coverage.out ./...

clean:
 rm -rf bin/ coverage.out

docker:
 docker build -t claude-proxy .

docker-run:
 docker-compose up -d

docker-stop:
 docker-compose down
```

- [ ] **Step 3: 提交初始化**

```bash
git add go.mod Makefile
git commit -m "chore: initialize Go module and Makefile"
```

---

### Task 2: 配置数据结构

**Files:**

- Create: `internal/config/config.go`

- [ ] **Step 1: 编写配置结构测试**

Create: `internal/config/config_test.go`

```go
package config

import (
 "testing"
)

func TestDefaultConfig(t *testing.T) {
 cfg := DefaultConfig()

 if cfg.BackendURL != "https://open.bigmodel.cn/api/anthropic" {
  t.Errorf("expected default backend URL, got %s", cfg.BackendURL)
 }

 if cfg.ProxyPort != 443 {
  t.Errorf("expected proxy port 443, got %d", cfg.ProxyPort)
 }

 if cfg.AdminPort != 8442 {
  t.Errorf("expected admin port 8442, got %d", cfg.AdminPort)
 }
}

func TestConfigValidation(t *testing.T) {
 tests := []struct {
  name    string
  config  Config
  wantErr bool
 }{
  {
   name: "valid config",
   config: Config{
    BackendURL: "https://example.com/api",
   },
   wantErr: false,
  },
  {
   name: "empty backend URL",
   config: Config{
    BackendURL: "",
   },
   wantErr: true,
  },
  {
   name: "invalid URL",
   config: Config{
    BackendURL: "not-a-url",
   },
   wantErr: true,
  },
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   err := tt.config.Validate()
   if (err != nil) != tt.wantErr {
    t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
   }
  })
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/config/... -v
```

Expected: FAIL (config.go not exists)

- [ ] **Step 3: 实现配置结构**

Create: `internal/config/config.go`

```go
package config

import (
 "fmt"
 "net/url"
)

// Config 应用配置
type Config struct {
 // 后端代理地址
 BackendURL string `json:"backend_url"`

 // 代理服务端口
 ProxyPort int `json:"proxy_port"`

 // 配置服务端口
 AdminPort int `json:"admin_port"`

 // 管理密码 (bcrypt哈希)
 AdminPasswordHash string `json:"admin_password_hash"`

 // 数据目录
 DataDir string `json:"data_dir"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
 return &Config{
  BackendURL:    "https://coding.dashscope.aliyuncs.com/apps/anthropic",
  ProxyPort:     443,
  AdminPort:     8442,
  DataDir:       "./data",
 }
}

// Validate 验证配置
func (c *Config) Validate() error {
 if c.BackendURL == "" {
  return fmt.Errorf("backend_url is required")
 }

 if _, err := url.Parse(c.BackendURL); err != nil {
  return fmt.Errorf("invalid backend_url: %w", err)
 }

 // 验证 URL 格式
 u, err := url.Parse(c.BackendURL)
 if err != nil {
  return fmt.Errorf("invalid backend_url: %w", err)
 }

 if u.Scheme != "https" && u.Scheme != "http" {
  return fmt.Errorf("backend_url must use http or https scheme")
 }

 if u.Host == "" {
  return fmt.Errorf("backend_url must have a host")
 }

 return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/config/... -v
```

Expected: PASS

- [ ] **Step 5: 提交配置模块**

```bash
git add internal/config/
git commit -m "feat(config): add config data structure with validation"
```

---

### Task 3: 配置存储

**Files:**

- Create: `internal/config/store.go`
- Create: `internal/config/store_test.go`

- [ ] **Step 1: 编写存储测试**

Create: `internal/config/store_test.go`

```go
package config

import (
 "os"
 "path/filepath"
 "testing"
)

func TestStore_SaveAndLoad(t *testing.T) {
 // 创建临时目录
 tmpDir, err := os.MkdirTemp("", "config-test")
 if err != nil {
  t.Fatalf("failed to create temp dir: %v", err)
 }
 defer os.RemoveAll(tmpDir)

 store := NewStore(filepath.Join(tmpDir, "config.json"))

 // 测试保存
 cfg := &Config{
  BackendURL:    "https://test.example.com/api",
  ProxyPort:     443,
  AdminPort:     8442,
  DataDir:       tmpDir,
 }

 if err := store.Save(cfg); err != nil {
  t.Fatalf("failed to save config: %v", err)
 }

 // 测试加载
 loaded, err := store.Load()
 if err != nil {
  t.Fatalf("failed to load config: %v", err)
 }

 if loaded.BackendURL != cfg.BackendURL {
  t.Errorf("expected backend URL %s, got %s", cfg.BackendURL, loaded.BackendURL)
 }
}

func TestStore_LoadNonExistent(t *testing.T) {
 tmpDir, err := os.MkdirTemp("", "config-test")
 if err != nil {
  t.Fatalf("failed to create temp dir: %v", err)
 }
 defer os.RemoveAll(tmpDir)

 store := NewStore(filepath.Join(tmpDir, "nonexistent.json"))

 cfg, err := store.Load()
 if err != nil {
  t.Fatalf("expected no error for non-existent file, got: %v", err)
 }

 // 应返回默认配置
 if cfg.BackendURL != "https://coding.dashscope.aliyuncs.com/apps/anthropic" {
  t.Errorf("expected default backend URL, got %s", cfg.BackendURL)
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/config/... -v -run TestStore
```

Expected: FAIL

- [ ] **Step 3: 实现存储**

Create: `internal/config/store.go`

```go
package config

import (
 "encoding/json"
 "os"
 "path/filepath"
)

// Store 配置存储
type Store struct {
 path string
}

// NewStore 创建配置存储
func NewStore(path string) *Store {
 return &Store{path: path}
}

// Load 加载配置，如果文件不存在则返回默认配置
func (s *Store) Load() (*Config, error) {
 data, err := os.ReadFile(s.path)
 if err != nil {
  if os.IsNotExist(err) {
   return DefaultConfig(), nil
  }
  return nil, err
 }

 cfg := &Config{}
 if err := json.Unmarshal(data, cfg); err != nil {
  return nil, err
 }

 // 填充默认值
 if cfg.ProxyPort == 0 {
  cfg.ProxyPort = 443
 }
 if cfg.AdminPort == 0 {
  cfg.AdminPort = 8442
 }
 if cfg.DataDir == "" {
  cfg.DataDir = "./data"
 }

 return cfg, nil
}

// Save 保存配置
func (s *Store) Save(cfg *Config) error {
 // 确保目录存在
 if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
  return err
 }

 data, err := json.MarshalIndent(cfg, "", "  ")
 if err != nil {
  return err
 }

 return os.WriteFile(s.path, data, 0644)
}

// Path 返回配置文件路径
func (s *Store) Path() string {
 return s.path
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/config/... -v
```

Expected: PASS

- [ ] **Step 5: 提交存储模块**

```bash
git add internal/config/store.go internal/config/store_test.go
git commit -m "feat(config): add JSON file store for configuration"
```

---

## Chunk 2: 证书管理模块

### Task 4: CA 证书生成

**Files:**

- Create: `internal/cert/ca.go`
- Create: `internal/cert/ca_test.go`

- [ ] **Step 1: 编写 CA 生成测试**

Create: `internal/cert/ca_test.go`

```go
package cert

import (
 "crypto/x509"
 "encoding/pem"
 "os"
 "path/filepath"
 "testing"
 "time"
)

func TestGenerateCA(t *testing.T) {
 tmpDir, err := os.MkdirTemp("", "cert-test")
 if err != nil {
  t.Fatalf("failed to create temp dir: %v", err)
 }
 defer os.RemoveAll(tmpDir)

 manager := NewManager(tmpDir)

 // 生成 CA
 caCert, caKey, err := manager.GenerateCA()
 if err != nil {
  t.Fatalf("failed to generate CA: %v", err)
 }

 // 验证证书
 cert, err := x509.ParseCertificate(caCert)
 if err != nil {
  t.Fatalf("failed to parse certificate: %v", err)
 }

 // 验证是 CA 证书
 if !cert.IsCA {
  t.Error("expected certificate to be CA")
 }

 // 验证有效期 (10年)
 validFor := cert.NotAfter.Sub(cert.NotBefore)
 expectedDuration := 10 * 365 * 24 * time.Hour
 tolerance := 24 * time.Hour

 if validFor < expectedDuration-tolerance || validFor > expectedDuration+tolerance {
  t.Errorf("expected validity ~10 years, got %v", validFor)
 }

 // 验证私钥
 if caKey == nil {
  t.Error("expected private key to be returned")
 }
}

func TestSaveAndLoadCA(t *testing.T) {
 tmpDir, err := os.MkdirTemp("", "cert-test")
 if err != nil {
  t.Fatalf("failed to create temp dir: %v", err)
 }
 defer os.RemoveAll(tmpDir)

 manager := NewManager(tmpDir)

 // 生成并保存
 caCert, caKey, err := manager.GenerateCA()
 if err != nil {
  t.Fatalf("failed to generate CA: %v", err)
 }

 if err := manager.SaveCA(caCert, caKey); err != nil {
  t.Fatalf("failed to save CA: %v", err)
 }

 // 加载
 loadedCert, loadedKey, err := manager.LoadCA()
 if err != nil {
  t.Fatalf("failed to load CA: %v", err)
 }

 // 验证证书相同
 parsed1, _ := x509.ParseCertificate(caCert)
 parsed2, _ := x509.ParseCertificate(loadedCert)

 if !parsed1.Equal(parsed2) {
  t.Error("loaded certificate does not match saved")
 }

 // 验证文件存在
 if _, err := os.Stat(filepath.Join(tmpDir, "ca.crt")); err != nil {
  t.Error("ca.crt file not found")
 }
 if _, err := os.Stat(filepath.Join(tmpDir, "ca.key")); err != nil {
  t.Error("ca.key file not found")
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/cert/... -v
```

Expected: FAIL

- [ ] **Step 3: 实现 CA 生成**

Create: `internal/cert/ca.go`

```go
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
   Organization: []string{"Claude Proxy Local CA"},
   CommonName:   "Claude Proxy Local CA",
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
```

- [ ] **Step 4: 运行测试确认部分失败**

```bash
go test ./internal/cert/... -v
```

Expected: 部分测试失败 (Manager 和 errors 未定义)

- [ ] **Step 5: 实现证书管理器基础结构**

Create: `internal/cert/manager.go`

```go
package cert

import (
 "errors"
)

// 错误定义
var (
 ErrInvalidPEM       = errors.New("invalid PEM format")
 ErrCANotFound       = errors.New("CA certificate not found")
 ErrCertNotAfter     = errors.New("certificate has expired")
 ErrCertNotYetValid  = errors.New("certificate is not yet valid")
)

// Manager 证书管理器
type Manager struct {
 dataDir string
}

// NewManager 创建证书管理器
func NewManager(dataDir string) *Manager {
 return &Manager{dataDir: dataDir}
}
```

- [ ] **Step 6: 运行测试确认通过**

```bash
go test ./internal/cert/... -v
```

Expected: PASS

- [ ] **Step 7: 提交 CA 模块**

```bash
git add internal/cert/
git commit -m "feat(cert): add CA certificate generation and management"
```

---

### Task 5: 服务器证书签发

**Files:**

- Create: `internal/cert/cert.go`
- Update: `internal/cert/cert_test.go`

- [ ] **Step 1: 编写服务器证书测试**

Update: `internal/cert/cert_test.go` (追加内容)

```go
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
 tolerance := 24 * time.Hour

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
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/cert/... -v -run TestGenerateServerCert
```

Expected: FAIL

- [ ] **Step 3: 实现服务器证书签发**

Create: `internal/cert/cert.go`

```go
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
   Organization: []string{"Claude Proxy"},
   CommonName:   "api.anthropic.com",
  },
  DNSNames:    []string{"api.anthropic.com"},
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

// SaveServerCert 保存服务器证书和私钥
func (m *Manager) SaveServerCert(certDER []byte, privateKey *rsa.PrivateKey) error {
 // 保存证书
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

// LoadServerCert 加载服务器证书和私钥
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

// EnsureServerCert 确保服务器证书存在，不存在则生成
func (m *Manager) EnsureServerCert(caCertDER []byte, caKey *rsa.PrivateKey) ([]byte, *rsa.PrivateKey, error) {
 if m.ServerCertExists() {
  return m.LoadServerCert()
 }

 serverCert, serverKey, err := m.GenerateServerCert(caCertDER, caKey)
 if err != nil {
  return nil, nil, err
 }

 if err := m.SaveServerCert(serverCert, serverKey); err != nil {
  return nil, nil, err
 }

 return serverCert, serverKey, nil
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
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/cert/... -v
```

Expected: PASS

- [ ] **Step 5: 提交服务器证书模块**

```bash
git add internal/cert/cert.go internal/cert/cert_test.go
git commit -m "feat(cert): add server certificate generation and management"
```

---

## Chunk 3: 代理服务模块

### Task 6: 代理服务器

**Files:**

- Create: `internal/proxy/server.go`
- Create: `internal/proxy/handler.go`
- Create: `internal/proxy/server_test.go`

- [ ] **Step 1: 编写代理测试**

Create: `internal/proxy/server_test.go`

```go
package proxy

import (
 "crypto/tls"
 "crypto/x509"
 "io"
 "net/http"
 "net/http/httptest"
 "os"
 "testing"
)

func TestProxyHandler(t *testing.T) {
 // 创建模拟后端
 backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  // 验证 header 透传
  if r.Header.Get("X-Custom-Header") != "test-value" {
   t.Error("expected custom header to be forwarded")
  }

  w.Header().Set("X-Backend-Header", "backend-value")
  w.WriteHeader(http.StatusOK)
  w.Write([]byte("backend response"))
 }))
 defer backend.Close()

 // 创建代理配置
 cfg := &Config{
  BackendURL: backend.URL,
 }

 // 创建代理处理器
 handler := NewHandler(cfg)

 // 创建测试请求
 req := httptest.NewRequest("POST", "/v1/messages", nil)
 req.Header.Set("X-Custom-Header", "test-value")

 // 执行请求
 rec := httptest.NewRecorder()
 handler.ServeHTTP(rec, req)

 // 验证响应
 if rec.Code != http.StatusOK {
  t.Errorf("expected status 200, got %d", rec.Code)
 }

 if rec.Header().Get("X-Backend-Header") != "backend-value" {
  t.Error("expected backend header to be returned")
 }

 body, _ := io.ReadAll(rec.Body)
 if string(body) != "backend response" {
  t.Errorf("expected 'backend response', got %s", string(body))
 }
}

func TestProxyBackendError(t *testing.T) {
 // 创建模拟后端返回错误
 backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusInternalServerError)
  w.Write([]byte("internal error"))
 }))
 defer backend.Close()

 cfg := &Config{
  BackendURL: backend.URL,
 }

 handler := NewHandler(cfg)

 req := httptest.NewRequest("POST", "/v1/messages", nil)
 rec := httptest.NewRecorder()
 handler.ServeHTTP(rec, req)

 // 应透传错误状态码
 if rec.Code != http.StatusInternalServerError {
  t.Errorf("expected status 500, got %d", rec.Code)
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/proxy/... -v
```

Expected: FAIL

- [ ] **Step 3: 实现代理配置和处理器**

Create: `internal/proxy/server.go`

```go
package proxy

import (
 "context"
 "crypto/tls"
 "log"
 "net/http"
 "sync/atomic"
 "time"
)

// Config 代理配置
type Config struct {
 BackendURL string
}

// Server 代理服务器
type Server struct {
 config    *Config
 server    *http.Server
 transport *http.Transport

 // 统计
 requestsTotal atomic.Int64
 lastRequest   atomic.Value // time.Time
 startTime     time.Time
}

// NewServer 创建代理服务器
func NewServer(cfg *Config) *Server {
 return &Server{
  config:    cfg,
  startTime: time.Now(),
  transport: &http.Transport{
   TLSClientConfig: &tls.Config{
    InsecureSkipVerify: false,
   },
   MaxIdleConns:        100,
   MaxIdleConnsPerHost: 10,
   IdleConnTimeout:     90 * time.Second,
  },
 }
}

// Start 启动代理服务器
func (s *Server) Start(addr string, certFile, keyFile string) error {
 handler := NewHandler(s.config)

 s.server = &http.Server{
  Addr:         addr,
  Handler:      s.withStats(handler),
  ReadTimeout:  60 * time.Second,
  WriteTimeout: 60 * time.Second,
  IdleTimeout:  120 * time.Second,
 }

 log.Printf("Proxy server starting on %s", addr)
 return s.server.ListenAndServeTLS(certFile, keyFile)
}

// withStats 添加统计中间件
func (s *Server) withStats(next http.Handler) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  s.requestsTotal.Add(1)
  s.lastRequest.Store(time.Now())
  next.ServeHTTP(w, r)
 })
}

// Stop 停止代理服务器
func (s *Server) Stop(ctx context.Context) error {
 if s.server != nil {
  return s.server.Shutdown(ctx)
 }
 return nil
}

// Stats 返回统计信息
func (s *Server) Stats() (total int64, last time.Time, uptime time.Duration) {
 total = s.requestsTotal.Load()
 if v := s.lastRequest.Load(); v != nil {
  last = v.(time.Time)
 }
 uptime = time.Since(s.startTime)
 return
}
```

- [ ] **Step 4: 实现代理处理器**

Create: `internal/proxy/handler.go`

```go
package proxy

import (
 "io"
 "log"
 "net/http"
 "strings"
 "time"
)

// Handler 代理处理器
type Handler struct {
 config    *Config
 transport *http.Transport
}

// NewHandler 创建代理处理器
func NewHandler(cfg *Config) *Handler {
 return &Handler{
  config: cfg,
  transport: &http.Transport{
   TLSHandshakeTimeout:   10 * time.Second,
   ResponseHeaderTimeout: 60 * time.Second,
   MaxIdleConnsPerHost:   10,
  },
 }
}

// ServeHTTP 处理 HTTP 请求
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
 // 创建后端请求
 backendURL := h.config.BackendURL + r.URL.Path
 if r.URL.RawQuery != "" {
  backendURL += "?" + r.URL.RawQuery
 }

 // 读取请求体
 body, err := io.ReadAll(r.Body)
 if err != nil {
  log.Printf("Error reading request body: %v", err)
  http.Error(w, "Error reading request body", http.StatusBadRequest)
  return
 }
 r.Body.Close()

 // 创建后端请求
 backendReq, err := http.NewRequest(r.Method, backendURL, strings.NewReader(string(body)))
 if err != nil {
  log.Printf("Error creating backend request: %v", err)
  http.Error(w, "Error creating backend request", http.StatusInternalServerError)
  return
 }

 // 复制所有 header
 for key, values := range r.Header {
  for _, value := range values {
   backendReq.Header.Add(key, value)
  }
 }

 // 发送请求到后端
 client := &http.Client{
  Transport: h.transport,
  Timeout:   60 * time.Second,
 }

 resp, err := client.Do(backendReq)
 if err != nil {
  log.Printf("Error forwarding request: %v", err)
  http.Error(w, "Backend unavailable", http.StatusBadGateway)
  return
 }
 defer resp.Body.Close()

 // 复制响应 header
 for key, values := range resp.Header {
  for _, value := range values {
   w.Header().Add(key, value)
  }
 }

 // 设置状态码
 w.WriteHeader(resp.StatusCode)

 // 流式复制响应体
 _, err = io.Copy(w, resp.Body)
 if err != nil {
  log.Printf("Error copying response body: %v", err)
 }
}
```

- [ ] **Step 5: 运行测试确认通过**

```bash
go test ./internal/proxy/... -v
```

Expected: PASS

- [ ] **Step 6: 提交代理模块**

```bash
git add internal/proxy/
git commit -m "feat(proxy): add transparent proxy server with header forwarding"
```

---

## Chunk 4: 配置服务模块

### Task 7: 认证中间件

**Files:**

- Create: `internal/admin/auth.go`
- Create: `internal/admin/auth_test.go`

- [ ] **Step 1: 编写认证测试**

Create: `internal/admin/auth_test.go`

```go
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
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/admin/... -v
```

Expected: FAIL

- [ ] **Step 3: 实现认证模块**

Create: `internal/admin/auth.go`

```go
package admin

import (
 "crypto/rand"
 "crypto/subtle"
 "encoding/hex"
 "sync"
 "time"

 "golang.org/x/crypto/bcrypt"
)

// Auth 认证管理器
type Auth struct {
 passwordHash    []byte
 sessions        map[string]time.Time
 sessionDuration time.Duration
 mu              sync.RWMutex

 // 登录保护
 failedAttempts  int
 maxAttempts     int
 lockDuration    time.Duration
 lockedUntil     time.Time
}

// NewAuth 创建认证管理器
func NewAuth(password string) *Auth {
 hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
 return &Auth{
  passwordHash:    hash,
  sessions:        make(map[string]time.Time),
  sessionDuration: 24 * time.Hour,
  maxAttempts:     5,
  lockDuration:    5 * time.Minute,
 }
}

// NewAuthWithConfig 创建带配置的认证管理器
func NewAuthWithConfig(password string, maxAttempts int, lockDuration time.Duration) *Auth {
 auth := NewAuth(password)
 auth.maxAttempts = maxAttempts
 auth.lockDuration = lockDuration
 return auth
}

// VerifyPassword 验证密码
func (a *Auth) VerifyPassword(password string) bool {
 // 检查是否被锁定
 if a.IsLocked() {
  return false
 }

 err := bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password))
 return err == nil
}

// GenerateToken 生成会话 token
func (a *Auth) GenerateToken() string {
 bytes := make([]byte, 32)
 rand.Read(bytes)
 token := hex.EncodeToString(bytes)

 a.mu.Lock()
 a.sessions[token] = time.Now().Add(a.sessionDuration)
 a.mu.Unlock()

 return token
}

// ValidateToken 验证 token
func (a *Auth) ValidateToken(token string) bool {
 a.mu.RLock()
 defer a.mu.RUnlock()

 expiry, exists := a.sessions[token]
 if !exists {
  return false
 }

 return time.Now().Before(expiry)
}

// InvalidateToken 使 token 失效
func (a *Auth) InvalidateToken(token string) {
 a.mu.Lock()
 delete(a.sessions, token)
 a.mu.Unlock()
}

// RecordFailedAttempt 记录失败尝试
func (a *Auth) RecordFailedAttempt() {
 a.mu.Lock()
 defer a.mu.Unlock()

 a.failedAttempts++
 if a.failedAttempts >= a.maxAttempts {
  a.lockedUntil = time.Now().Add(a.lockDuration)
 }
}

// ResetAttempts 重置失败计数
func (a *Auth) ResetAttempts() {
 a.mu.Lock()
 defer a.mu.Unlock()

 a.failedAttempts = 0
 a.lockedUntil = time.Time{}
}

// IsLocked 检查是否被锁定
func (a *Auth) IsLocked() bool {
 a.mu.RLock()
 defer a.mu.RUnlock()

 if a.lockedUntil.IsZero() {
  return false
 }

 return time.Now().Before(a.lockedUntil)
}

// SetPassword 设置密码
func (a *Auth) SetPassword(password string) error {
 hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
 if err != nil {
  return err
 }

 a.mu.Lock()
 a.passwordHash = hash
 a.mu.Unlock()

 return nil
}

// constantTimeCompare 常量时间比较
func constantTimeCompare(a, b string) bool {
 return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
```

- [ ] **Step 4: 安装依赖并运行测试**

```bash
go get golang.org/x/crypto/bcrypt
go test ./internal/admin/... -v
```

Expected: PASS

- [ ] **Step 5: 提交认证模块**

```bash
git add internal/admin/auth.go internal/admin/auth_test.go go.mod go.sum
git commit -m "feat(admin): add authentication with bcrypt and session tokens"
```

---

### Task 8: 配置服务 API

**Files:**

- Create: `internal/admin/server.go`
- Create: `internal/admin/handler.go`

- [ ] **Step 1: 实现配置服务**

Create: `internal/admin/server.go`

```go
package admin

import (
 "context"
 "crypto/tls"
 "embed"
 "io/fs"
 "log"
 "net/http"
 "time"
)

// Server 配置服务
type Server struct {
 config    *AdminConfig
 auth      *Auth
 server    *http.Server
 startTime time.Time
}

// AdminConfig 配置服务配置
type AdminConfig struct {
 Password   string
 CertFile   string
 KeyFile    string
 ConfigPath string
}

// NewServer 创建配置服务
func NewServer(cfg *AdminConfig) *Server {
 return &Server{
  config:    cfg,
  auth:      NewAuth(cfg.Password),
  startTime: time.Now(),
 }
}

// Start 启动配置服务
func (s *Server) Start(addr string, frontendFS embed.FS) error {
 // 创建路由
 mux := http.NewServeMux()

 // 静态文件
 staticFS, _ := fs.Sub(frontendFS, "internal/frontend/dist")
 fileServer := http.FileServer(http.FS(staticFS))
 mux.Handle("/", s.authMiddleware(fileServer))

 // API 路由
 mux.HandleFunc("/api/login", s.handleLogin)
 mux.HandleFunc("/api/logout", s.handleLogout)
 mux.HandleFunc("/api/config", s.authMiddlewareFunc(s.handleConfig))
 mux.HandleFunc("/api/status", s.authMiddlewareFunc(s.handleStatus))
 mux.HandleFunc("/api/certificates", s.authMiddlewareFunc(s.handleCertificates))
 mux.HandleFunc("/api/config/test", s.authMiddlewareFunc(s.handleTestBackend))

 s.server = &http.Server{
  Addr:         addr,
  Handler:      mux,
  ReadTimeout:  30 * time.Second,
  WriteTimeout: 30 * time.Second,
  IdleTimeout:  120 * time.Second,
 }

 log.Printf("Admin server starting on %s", addr)
 return s.server.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
}

// Stop 停止配置服务
func (s *Server) Stop(ctx context.Context) error {
 if s.server != nil {
  return s.server.Shutdown(ctx)
 }
 return nil
}

// authMiddleware 认证中间件（用于静态文件）
func (s *Server) authMiddleware(next http.Handler) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  // 登录页面不需要认证
  if r.URL.Path == "/login.html" || r.URL.Path == "/login" {
   next.ServeHTTP(w, r)
   return
  }

  // API 路由单独处理
  if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
   next.ServeHTTP(w, r)
   return
  }

  // 检查 session cookie
  cookie, err := r.Cookie("session")
  if err != nil || !s.auth.ValidateToken(cookie.Value) {
   http.Redirect(w, r, "/login.html", http.StatusFound)
   return
  }

  next.ServeHTTP(w, r)
 })
}

// authMiddlewareFunc 认证中间件（用于 API）
func (s *Server) authMiddlewareFunc(next http.HandlerFunc) http.HandlerFunc {
 return func(w http.ResponseWriter, r *http.Request) {
  cookie, err := r.Cookie("session")
  if err != nil || !s.auth.ValidateToken(cookie.Value) {
   http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
   return
  }
  next(w, r)
 }
}

// GetAuth 获取认证管理器
func (s *Server) GetAuth() *Auth {
 return s.auth
}
```

- [ ] **Step 2: 实现 API 处理器**

Create: `internal/admin/handler.go`

```go
package admin

import (
 "encoding/json"
 "io"
 "net/http"
 "time"
)

// handleLogin 处理登录
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
 if r.Method != http.MethodPost {
  http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
  return
 }

 // 检查是否被锁定
 if s.auth.IsLocked() {
  time.Sleep(1 * time.Second) // 延迟响应
  http.Error(w, `{"error": "account locked"}`, http.StatusTooManyRequests)
  return
 }

 var req struct {
  Password string `json:"password"`
 }

 if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
  http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
  return
 }

 if !s.auth.VerifyPassword(req.Password) {
  s.auth.RecordFailedAttempt()
  time.Sleep(1 * time.Second) // 延迟响应
  http.Error(w, `{"error": "invalid password"}`, http.StatusUnauthorized)
  return
 }

 // 重置失败计数
 s.auth.ResetAttempts()

 // 生成 session token
 token := s.auth.GenerateToken()

 // 设置 cookie
 http.SetCookie(w, &http.Cookie{
  Name:     "session",
  Value:    token,
  Path:     "/",
  HttpOnly: true,
  Secure:   true,
  Expires:  time.Now().Add(24 * time.Hour),
 })

 w.Header().Set("Content-Type", "application/json")
 json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleLogout 处理登出
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
 cookie, err := r.Cookie("session")
 if err == nil {
  s.auth.InvalidateToken(cookie.Value)
 }

 http.SetCookie(w, &http.Cookie{
  Name:     "session",
  Value:    "",
  Path:     "/",
  HttpOnly: true,
  Secure:   true,
  MaxAge:   -1,
 })

 w.Header().Set("Content-Type", "application/json")
 json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleConfig 处理配置请求
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
 switch r.Method {
 case http.MethodGet:
  s.getConfig(w, r)
 case http.MethodPut:
  s.updateConfig(w, r)
 default:
  http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
 }
}

// getConfig 获取配置
func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
 // TODO: 从配置管理器获取
 w.Header().Set("Content-Type", "application/json")
 json.NewEncoder(w).Encode(map[string]string{
  "backend_url": "https://coding.dashscope.aliyuncs.com/apps/anthropic",
 })
}

// updateConfig 更新配置
func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request) {
 var req struct {
  BackendURL string `json:"backend_url"`
 }

 if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
  http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
  return
 }

 // TODO: 更新配置管理器

 w.Header().Set("Content-Type", "application/json")
 json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleStatus 处理状态请求
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
 w.Header().Set("Content-Type", "application/json")
 json.NewEncoder(w).Encode(map[string]interface{}{
  "running":        true,
  "backend_url":    "https://coding.dashscope.aliyuncs.com/apps/anthropic",
  "uptime":         time.Since(s.startTime).String(),
  "requests_total": 0,
 })
}

// handleCertificates 处理证书信息请求
func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
 w.Header().Set("Content-Type", "application/json")
 json.NewEncoder(w).Encode(map[string]interface{}{
  "ca_cert_path":      "./data/ca.crt",
  "server_cert_path":  "./data/server.crt",
  "ca_expires_at":     time.Now().AddDate(10, 0, 0),
  "server_expires_at": time.Now().AddDate(10, 0, 0),
 })
}

// handleTestBackend 测试后端连接
func (s *Server) handleTestBackend(w http.ResponseWriter, r *http.Request) {
 if r.Method != http.MethodPost {
  http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
  return
 }

 var req struct {
  BackendURL string `json:"backend_url"`
 }

 if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
  http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
  return
 }

 // 测试连接
 client := &http.Client{Timeout: 10 * time.Second}
 resp, err := client.Get(req.BackendURL)
 if err != nil {
  w.Header().Set("Content-Type", "application/json")
  json.NewEncoder(w).Encode(map[string]interface{}{
   "success": false,
   "error":   err.Error(),
  })
  return
 }
 defer resp.Body.Close()

 w.Header().Set("Content-Type", "application/json")
 json.NewEncoder(w).Encode(map[string]interface{}{
  "success":      true,
  "status_code":  resp.StatusCode,
 })
}

// SetConfigManager 设置配置管理器
func (s *Server) SetConfigManager(cm interface{}) {
 // TODO: 实现配置管理器集成
}
- [ ] **Step 3: 提交配置服务模块**

```bash
git add internal/admin/
git commit -m "feat(admin): add configuration server with REST API"
```

---

## Chunk 5: 前端页面

### Task 9: 前端静态文件

**Files:**

- Create: `internal/frontend/embed.go`
- Create: `internal/frontend/dist/login.html`
- Create: `internal/frontend/dist/index.html`

- [ ] **Step 1: 创建嵌入文件定义**

Create: `internal/frontend/embed.go`

```go
package frontend

import "embed"

//go:embed dist/*
var DistFS embed.FS
```

- [ ] **Step 2: 创建登录页面**

Create: `internal/frontend/dist/login.html`

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>登录 - Claude Code 透明代理</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .login-container {
            background: white;
            padding: 40px;
            border-radius: 12px;
            box-shadow: 0 10px 40px rgba(0,0,0,0.2);
            width: 100%;
            max-width: 400px;
        }
        h1 { text-align: center; margin-bottom: 30px; color: #333; }
        .form-group { margin-bottom: 20px; }
        label { display: block; margin-bottom: 8px; color: #555; font-weight: 500; }
        input[type="password"] {
            width: 100%;
            padding: 12px;
            border: 2px solid #e0e0e0;
            border-radius: 8px;
            font-size: 16px;
            transition: border-color 0.3s;
        }
        input[type="password"]:focus {
            outline: none;
            border-color: #667eea;
        }
        button {
            width: 100%;
            padding: 14px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            border: none;
            border-radius: 8px;
            font-size: 16px;
            font-weight: 600;
            cursor: pointer;
            transition: transform 0.2s, box-shadow 0.2s;
        }
        button:hover { transform: translateY(-2px); box-shadow: 0 4px 12px rgba(102,126,234,0.4); }
        button:disabled { opacity: 0.7; cursor: not-allowed; transform: none; }
        .error { color: #e74c3c; text-align: center; margin-top: 15px; }
    </style>
</head>
<body>
    <div class="login-container">
        <h1>🔐 管理员登录</h1>
        <form id="loginForm">
            <div class="form-group">
                <label for="password">密码</label>
                <input type="password" id="password" required autofocus>
            </div>
            <button type="submit" id="submitBtn">登录</button>
            <p class="error" id="error"></p>
        </form>
    </div>
    <script>
        document.getElementById('loginForm').addEventListener('submit', async (e) => {
            e.preventDefault();
            const btn = document.getElementById('submitBtn');
            const error = document.getElementById('error');
            const password = document.getElementById('password').value;

            btn.disabled = true;
            btn.textContent = '登录中...';
            error.textContent = '';

            try {
                const res = await fetch('/api/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ password })
                });

                if (res.ok) {
                    window.location.href = '/';
                } else {
                    const data = await res.json();
                    error.textContent = data.error || '登录失败';
                }
            } catch (err) {
                error.textContent = '网络错误';
            } finally {
                btn.disabled = false;
                btn.textContent = '登录';
            }
        });
    </script>
</body>
</html>
```

- [ ] **Step 3: 创建配置页面**

Create: `internal/frontend/dist/index.html`

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Claude Code 透明代理配置</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #f5f7fa;
            min-height: 100vh;
            padding: 20px;
        }
        .container { max-width: 800px; margin: 0 auto; }
        header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 30px;
            padding: 20px;
            background: white;
            border-radius: 12px;
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
        }
        h1 { color: #333; font-size: 24px; }
        .logout-btn {
            padding: 10px 20px;
            background: #e74c3c;
            color: white;
            border: none;
            border-radius: 8px;
            cursor: pointer;
        }
        .card {
            background: white;
            padding: 25px;
            border-radius: 12px;
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
            margin-bottom: 20px;
        }
        .card h2 { color: #333; margin-bottom: 20px; font-size: 18px; }
        .status-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 15px; }
        .status-item {
            padding: 15px;
            background: #f8f9fa;
            border-radius: 8px;
            text-align: center;
        }
        .status-value { font-size: 24px; font-weight: bold; color: #667eea; }
        .status-label { font-size: 14px; color: #666; margin-top: 5px; }
        .form-group { margin-bottom: 15px; }
        label { display: block; margin-bottom: 8px; color: #555; font-weight: 500; }
        input[type="text"], input[type="password"] {
            width: 100%;
            padding: 12px;
            border: 2px solid #e0e0e0;
            border-radius: 8px;
            font-size: 14px;
        }
        .btn-group { display: flex; gap: 10px; margin-top: 15px; }
        .btn {
            padding: 12px 24px;
            border: none;
            border-radius: 8px;
            font-size: 14px;
            font-weight: 600;
            cursor: pointer;
        }
        .btn-primary { background: #667eea; color: white; }
        .btn-secondary { background: #95a5a6; color: white; }
        .btn-success { background: #27ae60; color: white; }
        .btn-danger { background: #e74c3c; color: white; }
        .code-block {
            background: #2c3e50;
            color: #ecf0f1;
            padding: 15px;
            border-radius: 8px;
            font-family: monospace;
            margin: 10px 0;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .copy-btn { background: #3498db; color: white; border: none; padding: 5px 10px; border-radius: 4px; cursor: pointer; }
        .message { padding: 10px; border-radius: 8px; margin-top: 10px; }
        .success { background: #d4edda; color: #155724; }
        .error { background: #f8d7da; color: #721c24; }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>🚀 Claude Code 透明代理配置</h1>
            <button class="logout-btn" onclick="logout()">退出登录</button>
        </header>

        <div class="card">
            <h2>📊 服务状态</h2>
            <div class="status-grid">
                <div class="status-item">
                    <div class="status-value" id="status">运行中</div>
                    <div class="status-label">状态</div>
                </div>
                <div class="status-item">
                    <div class="status-value" id="uptime">-</div>
                    <div class="status-label">运行时长</div>
                </div>
                <div class="status-item">
                    <div class="status-value" id="requests">0</div>
                    <div class="status-label">总请求数</div>
                </div>
            </div>
        </div>

        <div class="card">
            <h2>⚙️ 后端配置</h2>
            <div class="form-group">
                <label for="backendUrl">后端地址</label>
                <input type="text" id="backendUrl" placeholder="https://your-backend.com/api">
            </div>
            <div class="btn-group">
                <button class="btn btn-secondary" onclick="testConnection()">测试连接</button>
                <button class="btn btn-primary" onclick="saveConfig()">保存配置</button>
            </div>
            <div id="configMessage"></div>
        </div>

        <div class="card">
            <h2>📜 证书信息</h2>
            <div class="form-group">
                <label>CA 证书路径</label>
                <div id="caCertPath">-</div>
            </div>
            <div class="form-group">
                <label>CA 有效期</label>
                <div id="caExpires">-</div>
            </div>
            <div class="form-group">
                <label>NODE_EXTRA_CA_CERTS 配置</label>
                <div class="code-block">
                    <span id="caPath">NODE_EXTRA_CA_CERTS=/path/to/ca.crt</span>
                    <button class="copy-btn" onclick="copyPath()">复制</button>
                </div>
            </div>
            <button class="btn btn-danger" onclick="renewCerts()">重新生成证书</button>
        </div>

        <div class="card">
            <h2>🔐 系统设置</h2>
            <div class="form-group">
                <label for="newPassword">新密码</label>
                <input type="password" id="newPassword" placeholder="留空则不修改">
            </div>
            <button class="btn btn-success" onclick="changePassword()">修改密码</button>
        </div>
    </div>

    <script>
        // 加载配置
        async function loadConfig() {
            try {
                const [configRes, statusRes, certRes] = await Promise.all([
                    fetch('/api/config'),
                    fetch('/api/status'),
                    fetch('/api/certificates')
                ]);

                const config = await configRes.json();
                const status = await statusRes.json();
                const certs = await certRes.json();

                document.getElementById('backendUrl').value = config.backend_url || '';
                document.getElementById('uptime').textContent = status.uptime || '-';
                document.getElementById('requests').textContent = status.requests_total || 0;
                document.getElementById('caCertPath').textContent = certs.ca_cert_path || '-';
                document.getElementById('caExpires').textContent = certs.ca_expires_at ? new Date(certs.ca_expires_at).toLocaleDateString() : '-';
                document.getElementById('caPath').textContent = 'NODE_EXTRA_CA_CERTS=' + (certs.ca_cert_path || '/path/to/ca.crt');
            } catch (err) {
                console.error('Failed to load config:', err);
            }
        }

        async function saveConfig() {
            const backendUrl = document.getElementById('backendUrl').value;
            const msgEl = document.getElementById('configMessage');

            try {
                const res = await fetch('/api/config', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ backend_url: backendUrl })
                });

                const data = await res.json();
                if (res.ok) {
                    msgEl.innerHTML = '<div class="message success">配置已保存</div>';
                } else {
                    msgEl.innerHTML = '<div class="message error">' + (data.error || '保存失败') + '</div>';
                }
            } catch (err) {
                msgEl.innerHTML = '<div class="message error">网络错误</div>';
            }
        }

        async function testConnection() {
            const backendUrl = document.getElementById('backendUrl').value;
            const msgEl = document.getElementById('configMessage');

            try {
                const res = await fetch('/api/config/test', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ backend_url: backendUrl })
                });

                const data = await res.json();
                if (data.success) {
                    msgEl.innerHTML = '<div class="message success">连接成功 (HTTP ' + data.status_code + ')</div>';
                } else {
                    msgEl.innerHTML = '<div class="message error">连接失败: ' + data.error + '</div>';
                }
            } catch (err) {
                msgEl.innerHTML = '<div class="message error">网络错误</div>';
            }
        }

        function copyPath() {
            const text = document.getElementById('caPath').textContent;
            navigator.clipboard.writeText(text);
            alert('已复制');
        }

        async function logout() {
            await fetch('/api/logout', { method: 'POST' });
            window.location.href = '/login.html';
        }

        // 初始化
        loadConfig();
        setInterval(loadConfig, 30000);
    </script>
</body>
</html>
```

- [ ] **Step 4: 提交前端文件**

```bash
git add internal/frontend/
git commit -m "feat(frontend): add login and configuration pages"
```

---

## Chunk 6: 应用入口

### Task 10: 主程序入口

**Files:**

- Create: `cmd/server/main.go`

- [ ] **Step 1: 创建主程序**

Create: `cmd/server/main.go`

```go
package main

import (
 "context"
 "flag"
 "fmt"
 "log"
 "os"
 "os/signal"
 "path/filepath"
 "syscall"
 "time"

 "claude-proxy/internal/admin"
 "claude-proxy/internal/cert"
 "claude-proxy/internal/config"
 "claude-proxy/internal/frontend"
 "claude-proxy/internal/proxy"
)

func main() {
 // 命令行参数
 dataDir := flag.String("data", "./data", "数据目录")
 adminPassword := flag.String("password", os.Getenv("ADMIN_PASSWORD"), "管理密码")
 flag.Parse()

 // 设置默认密码
 if *adminPassword == "" {
  *adminPassword = "admin123"
 }

 // 确保数据目录存在
 if err := os.MkdirAll(*dataDir, 0755); err != nil {
  log.Fatalf("Failed to create data directory: %v", err)
 }

 // 加载配置
 configPath := filepath.Join(*dataDir, "config.json")
 configStore := config.NewStore(configPath)
 cfg, err := configStore.Load()
 if err != nil {
  log.Fatalf("Failed to load config: %v", err)
 }

 // 设置数据目录
 cfg.DataDir = *dataDir

 // 证书管理
 certManager := cert.NewManager(*dataDir)

 // 确保 CA 证书存在
 caCert, caKey, err := certManager.EnsureCA()
 if err != nil {
  log.Fatalf("Failed to ensure CA: %v", err)
 }
 log.Printf("CA certificate: %s", certManager.GetCACertPath())

 // 确保服务器证书存在
 serverCert, serverKey, err := certManager.EnsureServerCert(caCert, caKey)
 if err != nil {
  log.Fatalf("Failed to ensure server cert: %v", err)
 }
 _ = serverCert
 _ = serverKey

 // 输出配置提示
 fmt.Println("\n========================================")
 fmt.Println("Claude Code 透明代理已启动")
 fmt.Println("========================================")
 fmt.Printf("代理端口: %d\n", cfg.ProxyPort)
 fmt.Printf("配置端口: %d\n", cfg.AdminPort)
 fmt.Printf("后端地址: %s\n", cfg.BackendURL)
 fmt.Println()
 fmt.Println("请执行以下配置:")
 fmt.Printf("1. echo '127.0.0.1 api.anthropic.com' | sudo tee -a /etc/hosts\n")
 fmt.Printf("2. echo 'NODE_EXTRA_CA_CERTS=%s' >> ~/.bashrc\n", certManager.GetCACertPath())
 fmt.Printf("3. source ~/.bashrc\n")
 fmt.Println()
 fmt.Printf("配置页面: https://localhost:%d\n", cfg.AdminPort)
 fmt.Printf("默认密码: %s\n", *adminPassword)
 fmt.Println("========================================\n")

 // 创建代理服务器
 proxyServer := proxy.NewServer(&proxy.Config{
  BackendURL: cfg.BackendURL,
 })

 // 创建配置服务
 adminServer := admin.NewServer(&admin.AdminConfig{
  Password:   *adminPassword,
  CertFile:   certManager.GetServerCertPath(),
  KeyFile:    filepath.Join(*dataDir, "server.key"),
  ConfigPath: configPath,
 })

 // 启动服务
 go func() {
  if err := proxyServer.Start(":443", certManager.GetServerCertPath(), filepath.Join(*dataDir, "server.key")); err != nil {
   log.Printf("Proxy server error: %v", err)
  }
 }()

 go func() {
  if err := adminServer.Start(":8442", frontend.DistFS); err != nil {
   log.Printf("Admin server error: %v", err)
  }
 }()

 // 优雅关闭
 quit := make(chan os.Signal, 1)
 signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
 <-quit

 log.Println("Shutting down...")

 ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
 defer cancel()

 proxyServer.Stop(ctx)
 adminServer.Stop(ctx)

 log.Println("Server stopped")
}
```

- [ ] **Step 2: 编译测试**

```bash
go build -o bin/claude-proxy ./cmd/server
```

Expected: 编译成功

- [ ] **Step 3: 提交主程序**

```bash
git add cmd/server/main.go
git commit -m "feat: add main entry point for claude proxy"
```

---

## Chunk 7: Docker 配置

### Task 11: Dockerfile 和 docker-compose

**Files:**

- Create: `Dockerfile`
- Create: `docker-compose.yml`

- [ ] **Step 1: 创建 Dockerfile**

Create: `Dockerfile`

```dockerfile
# 构建阶段
FROM golang:1.26-alpine AS builder

WORKDIR /app

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o claude-proxy ./cmd/server

# 运行阶段
FROM alpine:latest

# 安装 CA 证书和时区数据
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 复制二进制文件
COPY --from=builder /app/claude-proxy .

# 创建数据目录
RUN mkdir -p /app/data

# 暴露端口
EXPOSE 443 8442

# 设置环境变量
ENV ADMIN_PASSWORD=admin123

# 启动
ENTRYPOINT ["./claude-proxy"]
CMD ["-data", "/app/data"]
```

- [ ] **Step 2: 创建 docker-compose.yml**

Create: `docker-compose.yml`

```yaml
version: '3.8'

services:
  claude-proxy:
    build: .
    container_name: claude-proxy
    ports:
      - "443:443"
      - "8442:8442"
    volumes:
      - ./data:/app/data
    environment:
      - ADMIN_PASSWORD=${ADMIN_PASSWORD:-admin123}
    restart: unless-stopped
    cap_add:
      - NET_BIND_SERVICE
```

- [ ] **Step 3: 创建 .dockerignore**

Create: `.dockerignore`

```
bin/
data/
*.test
*.out
.git/
.github/
.vscode/
.idea/
*.md
!README.md
docs/
```

- [ ] **Step 4: 提交 Docker 配置**

```bash
git add Dockerfile docker-compose.yml .dockerignore
git commit -m "feat: add Docker configuration for deployment"
```

---

## Chunk 8: 项目文档

### Task 12: README.md

**Files:**

- Create: `README.md`

- [ ] **Step 1: 创建 README.md**

Create: `README.md`

```markdown
# Claude Code 透明代理

让 Claude Code 误以为在与官方 API 通信的透明代理服务。

## 功能特性

- ✅ 透明代理所有 Claude Code API 请求
- ✅ 自动生成 CA 证书（10年有效期）
- ✅ 前端配置页面管理后端地址
- ✅ 密码保护配置页面
- ✅ Docker 单容器部署
- ✅ 热更新配置无需重启

## 快速开始

### 1. 使用 Docker 部署

```bash
# 克隆项目
git clone <repo-url>
cd claude-proxy

# 启动服务
docker-compose up -d

# 查看日志获取配置提示
docker logs claude-proxy
```

### 2. 配置系统

```bash
# 添加 hosts 映射
echo "127.0.0.1 api.anthropic.com" | sudo tee -a /etc/hosts

# 配置 Node.js 信任 CA 证书
echo 'NODE_EXTRA_CA_CERTS=/path/to/data/ca.crt' >> ~/.bashrc
source ~/.bashrc
```

### 3. 访问配置页面

打开浏览器访问: `https://localhost:8442`

默认密码: `admin123`

## 配置说明

| 环境变量 | 说明 | 默认值 |
|---------|------|--------|
| ADMIN_PASSWORD | 管理密码 | admin123 |

## 端口说明

| 端口 | 用途 |
|------|------|
| 443 | 代理服务入口 |
| 8442 | 配置页面 |

## 项目结构

```
claude-proxy/
├── cmd/server/          # 应用入口
├── internal/
│   ├── config/          # 配置管理
│   ├── cert/            # 证书管理
│   ├── proxy/           # 代理服务
│   ├── admin/           # 配置服务
│   └── frontend/        # 前端页面
├── data/                # 数据目录
├── Dockerfile
└── docker-compose.yml
```

## 开发

```bash
# 运行测试
make test

# 本地运行
make run

# 构建
make build
```

## 许可证

MIT License

```

- [ ] **Step 2: 提交 README**

```bash
git add README.md
git commit -m "docs: add README.md"
```

---

### Task 13: CLAUDE.md

**Files:**

- Create: `CLAUDE.md`

- [ ] **Step 1: 创建 CLAUDE.md**

Create: `CLAUDE.md`

```markdown
# CLAUDE.md - 项目指南

## 项目概述

Claude Code 透明代理，用于解决使用第三方 API 时 Claude Code 禁用优化功能的问题。

## 技术栈

- Go 1.26
- 原生 HTTP 标准库
- bcrypt 密码哈希
- Docker (Alpine)

## 架构

```

┌─────────────────────────────────────────┐
│            Go Application               │
│                                         │
│  :443 (代理服务)    :8442 (配置服务)    │
│       ↓                   ↓             │
│  [透明代理]         [配置API + 前端]    │
│       ↓                   ↓             │
│  [证书管理器] ←──────────────┘          │
│       ↓                                 │
│  [配置管理器]                           │
└─────────────────────────────────────────┘

```

## 关键文件

| 文件 | 职责 |
|------|------|
| cmd/server/main.go | 应用入口，启动服务 |
| internal/proxy/ | 透明代理逻辑 |
| internal/admin/ | 配置服务和认证 |
| internal/cert/ | CA 和服务器证书管理 |
| internal/config/ | 配置存储和管理 |

## 编码规范

1. 使用 Go 标准错误处理模式
2. 所有公开函数需要注释
3. 单元测试覆盖核心逻辑
4. 使用 context.Context 进行取消和超时控制

## 测试

```bash
# 运行所有测试
go test ./...

# 运行特定包测试
go test ./internal/cert/... -v

# 查看覆盖率
go test -cover ./...
```

## 常见问题

### Q: 证书过期怎么办？

A: 证书有效期 10 年，启动时会自动检查并续期。也可在配置页面手动重新生成。

### Q: 忘记密码怎么办？

A: 重启容器时通过环境变量 ADMIN_PASSWORD 重新设置。

```

- [ ] **Step 2: 提交 CLAUDE.md**

```bash
git add CLAUDE.md
git commit -m "docs: add CLAUDE.md project guide"
```

---

### Task 14: AGENT.md

**Files:**

- Create: `AGENT.md`

- [ ] **Step 1: 创建 AGENT.md**

Create: `AGENT.md`

```markdown
# AGENT.md - 开发指南

## 环境要求

- Go 1.26+
- Docker (可选，用于部署)
- Make

## 开发流程

### 1. 克隆项目

```bash
git clone <repo-url>
cd claude-proxy
```

### 2. 安装依赖

```bash
go mod download
```

### 3. 运行测试

```bash
make test
```

### 4. 本地开发

```bash
# 创建数据目录
mkdir -p data

# 运行服务 (需要 root 权限绑定 443 端口)
sudo make run

# 或者使用非特权端口测试
sudo go run ./cmd/server -data ./data
```

## 模块说明

### internal/config

配置管理模块，负责：

- 加载和保存配置到 JSON 文件
- 配置验证
- 默认配置管理

### internal/cert

证书管理模块，负责：

- CA 证书生成（10年有效期）
- 服务器证书签发（api.anthropic.com）
- 证书加载和保存

### internal/proxy

代理服务模块，负责：

- TLS 终结
- HTTP 请求转发
- Header 透传
- 统计收集

### internal/admin

配置服务模块，负责：

- REST API
- 密码认证
- Session 管理
- 静态文件服务

## API 端点

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | /api/login | 登录 |
| POST | /api/logout | 登出 |
| GET | /api/config | 获取配置 |
| PUT | /api/config | 更新配置 |
| GET | /api/status | 服务状态 |
| GET | /api/certificates | 证书信息 |
| POST | /api/config/test | 测试后端连接 |

## 调试

### 查看日志

```bash
# Docker 部署
docker logs -f claude-proxy

# 本地运行
# 日志直接输出到控制台
```

### 检查证书

```bash
# 查看 CA 证书信息
openssl x509 -in data/ca.crt -text -noout

# 查看服务器证书信息
openssl x509 -in data/server.crt -text -noout
```

## 发布流程

1. 更新版本号
2. 运行完整测试套件
3. 构建发布镜像
4. 推送到镜像仓库

```bash
# 构建发布镜像
docker build -t claude-proxy:latest .

# 运行发布镜像
docker run -d \
  -p 443:443 \
  -p 8442:8442 \
  -v ./data:/app/data \
  -e ADMIN_PASSWORD=your-password \
  claude-proxy:latest
```

```

- [ ] **Step 2: 提交 AGENT.md**

```bash
git add AGENT.md
git commit -m "docs: add AGENT.md development guide"
```

---

## 完成检查清单

- [ ] 所有测试通过: `make test`
- [ ] 编译成功: `make build`
- [ ] Docker 构建成功: `make docker`
- [ ] 功能验证完成
- [ ] 文档完整

## 最终提交

```bash
git add .
git commit -m "feat: complete Claude Code transparent proxy implementation"
```
