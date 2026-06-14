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
	_ "time/tzdata"

	"magic-claude-code/internal/admin"
	"magic-claude-code/internal/cert"
	"magic-claude-code/internal/config"
	"magic-claude-code/internal/frontend"
	"magic-claude-code/internal/proxy"
	"magic-claude-code/internal/updater"
	"magic-claude-code/internal/usage"
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

type adminPasswordState struct {
	Value           string
	RandomGenerated bool
}

func resolveAdminPassword(value string, generate func() string) adminPasswordState {
	if value != "" {
		return adminPasswordState{Value: value}
	}
	return adminPasswordState{Value: generate(), RandomGenerated: true}
}

func main() {
	// 命令行参数
	dataDir := flag.String("data", "./data", "数据目录")
	adminPassword := flag.String("password", os.Getenv("ADMIN_PASSWORD"), "管理密码")
	flag.Parse()

	// 设置默认密码
	passwordState := resolveAdminPassword(*adminPassword, generateRandomPassword)
	if passwordState.RandomGenerated {
		log.Println("警告: 未设置密码，使用随机生成的密码")
	}

	// 确保数据目录存在
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// 加载配置
	configPath := filepath.Join(*dataDir, "config.json")
	dbPath := filepath.Join(*dataDir, "proxy.db")
	configStore, err := config.NewSQLiteStore(dbPath, configPath)
	if err != nil {
		log.Fatalf("Failed to initialize config store: %v", err)
	}
	defer configStore.Close()

	usageStore := usage.NewStore(configStore.DB())
	if err := usageStore.Migrate(); err != nil {
		log.Fatalf("Failed to initialize usage store: %v", err)
	}
	usageHandler := usage.NewHandler(usageStore)
	usageSyncCtx, stopUsageSync := context.WithCancel(context.Background())
	defer stopUsageSync()
	usage.StartClaudeSessionSync(usageSyncCtx, usageStore, usage.DefaultClaudeProjectsDir(), time.Minute)

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
	if passwordState.RandomGenerated {
		fmt.Printf("随机生成的管理密码: %s\n", passwordState.Value)
		fmt.Println("请保存此密码；下次未指定密码启动时会重新生成。")
	} else {
		fmt.Println("密码: 请查看环境变量 ADMIN_PASSWORD 或启动参数 --password")
	}
	fmt.Println("========================================")

	// 创建代理服务器
	proxyServer := proxy.NewServer(configStore, usageStore)

	// 创建配置服务
	adminServer := admin.NewServer(&admin.AdminConfig{
		Password:   passwordState.Value,
		CertFile:   certManager.GetServerCertPath(),
		KeyFile:    filepath.Join(*dataDir, "server.key"),
		ConfigPath: configPath,
	}, configStore, proxyServer, usageHandler)

	// 配置自动更新器
	updaterInstance := updater.New(
		updater.NewGitHubSource("wakeya", "magic-claude-code"),
		updater.NewGitCodeSource("wakeya", "magic-claude-code", os.Getenv("GITCODE_TOKEN")),
		updater.NewGiteeSource("wakeya", "magic-claude-code", os.Getenv("GITEE_TOKEN")),
	)
	adminServer.SetUpdater(updaterInstance)
	if _, err := os.Stat("/.dockerenv"); err == nil {
		log.Println("运行在 Docker 容器中，应用内自更新已禁用；仍会检查新版本，请通过镜像更新")
		adminServer.DisableUpdateApply("Docker 环境不支持应用内自更新，请通过更新镜像并重新创建容器完成升级。")
	}

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

	// 优雅关闭 / 自动重启
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	var restartCh <-chan struct{}
	if updaterInstance != nil {
		restartCh = updaterInstance.RestartSignal()
	}

	select {
	case <-quit:
		log.Println("Shutting down...")
	case <-restartCh:
		log.Println("Update applied, restarting service...")
	}

	stopUsageSync()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proxyServer.Stop(ctx)
	adminServer.Stop(ctx)

	if updaterInstance != nil && updaterInstance.ShouldRestart() {
		configStore.Close()
		log.Println("Restarting service...")
		exe, err := os.Executable()
		if err != nil {
			log.Printf("auto-restart: cannot find executable: %v", err)
		} else if execErr := syscall.Exec(exe, os.Args, os.Environ()); execErr != nil {
			log.Printf("auto-restart not supported on this platform: %v", execErr)
			log.Println("Please restart the service manually to apply the update.")
		}
	}

	log.Println("Server stopped")
}
