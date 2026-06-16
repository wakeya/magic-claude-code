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
	"runtime"
	"syscall"
	"time"
	_ "time/tzdata"

	"magic-claude-code/internal/admin"
	"magic-claude-code/internal/cert"
	"magic-claude-code/internal/config"
	"magic-claude-code/internal/frontend"
	"magic-claude-code/internal/i18n"
	"magic-claude-code/internal/proxy"
	"magic-claude-code/internal/updater"
	"magic-claude-code/internal/usage"
)

// generateRandomPassword 生成随机密码
func generateRandomPassword(msg i18n.Messages) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Print(msg.WarnRandomFallback)
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

type adminPasswordState struct {
	Value           string
	RandomGenerated bool
}

func resolveAdminPassword(value string, msg i18n.Messages, generate func(i18n.Messages) string) adminPasswordState {
	if value != "" {
		return adminPasswordState{Value: value}
	}
	return adminPasswordState{Value: generate(msg), RandomGenerated: true}
}

func main() {
	msg := i18n.Load(i18n.ResolveLocale())

	// 命令行参数
	dataDir := flag.String("data", "./data", msg.FlagDataDir)
	adminPassword := flag.String("password", os.Getenv("ADMIN_PASSWORD"), msg.FlagPassword)
	flag.Parse()

	// 设置默认密码
	passwordState := resolveAdminPassword(*adminPassword, msg, generateRandomPassword)
	if passwordState.RandomGenerated {
		log.Println(msg.WarnNoPassword)
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
	fmt.Println("\n" + msg.BannerTop)
	fmt.Println(msg.BannerTitle)
	fmt.Println(msg.BannerTop)
	fmt.Printf(msg.ProxyPort+"\n", cfg.ProxyPort)
	fmt.Printf(msg.AdminPort+"\n", cfg.AdminPort)
	fmt.Printf(msg.BackendURL+msg.BackendURLNote+"\n", cfg.BackendURL)
	fmt.Println()
	fmt.Println(msg.ConfigInstructions)

	if runtime.GOOS == "windows" {
		fmt.Println(msg.HostsCommandWin)
		fmt.Printf(msg.CACertCommandWin+"\n", certManager.GetCACertPath())
		fmt.Println(msg.SourceCommandWin)
		fmt.Println(msg.RestartHintWin)
	} else {
		fmt.Println(msg.HostsCommandUnix)
		fmt.Printf(msg.CACertCommandUnix+"\n", certManager.GetCACertPath())
		fmt.Println(msg.SourceCommandUnix)
	}

	fmt.Println()
	fmt.Println(msg.AdminPage)
	fmt.Printf(msg.AdminPageURL+"\n", cfg.AdminPort, cfg.AdminPort)
	if passwordState.RandomGenerated {
		fmt.Printf(msg.RandomPassword+"\n", passwordState.Value)
		fmt.Println(msg.PasswordSaveHint)
	} else {
		fmt.Println(msg.PasswordEnvHint)
	}
	fmt.Println(msg.BannerTop)

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
		log.Println(msg.DockerUpdateDisabled)
		adminServer.DisableUpdateApply(msg.UpdateDisabledReason)
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
		log.Println(msg.ShuttingDown)
	case <-restartCh:
		log.Println(msg.UpdateAppliedRestarting)
	}

	stopUsageSync()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proxyServer.Stop(ctx)
	adminServer.Stop(ctx)

	if updaterInstance != nil && updaterInstance.ShouldRestart() {
		configStore.Close()
		log.Println(msg.RestartingService)
		exe, err := os.Executable()
		if err != nil {
			log.Printf(msg.AutoRestartNoExecutable, err)
		} else if execErr := syscall.Exec(exe, os.Args, os.Environ()); execErr != nil {
			log.Printf(msg.AutoRestartUnsupported, execErr)
			log.Println(msg.RestartManually)
		}
	}

	log.Println(msg.ServerStopped)
}
