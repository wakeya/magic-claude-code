package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"claude_code_proxy_dns/internal/admin"
	"claude_code_proxy_dns/internal/cert"
	"claude_code_proxy_dns/internal/config"
	"claude_code_proxy_dns/internal/frontend"
	"claude_code_proxy_dns/internal/proxy"
)

// generateRandomPassword 生成随机密码
func generateRandomPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 如果随机数生成失败，使用时间戳作为后备
		log.Printf("警告: 随机数生成失败，使用后备方案")
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func main() {
	// 命令行参数
	dataDir := flag.String("data", "./data", "数据目录")
	adminPassword := flag.String("password", os.Getenv("ADMIN_PASSWORD"), "管理密码")
	flag.Parse()

	// 设置默认密码
	if *adminPassword == "" {
		log.Println("警告: 未设置密码，使用随机生成的密码")
		*adminPassword = generateRandomPassword()
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
	_, _, err = certManager.EnsureServerCert(caCert, caKey)
	if err != nil {
		log.Fatalf("Failed to ensure server cert: %v", err)
	}

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
	fmt.Println("密码: 请查看环境变量 ADMIN_PASSWORD 或启动参数 --password")
	fmt.Println("========================================")

	// 创建代理服务器
	proxyServer := proxy.NewServer(configStore)

	// 创建配置服务
	adminServer := admin.NewServer(&admin.AdminConfig{
		Password:   *adminPassword,
		CertFile:   certManager.GetServerCertPath(),
		KeyFile:    filepath.Join(*dataDir, "server.key"),
		ConfigPath: configPath,
	}, configStore, proxyServer)

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