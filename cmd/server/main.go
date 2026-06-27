package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata"

	"magic-claude-code/internal/admin"
	"magic-claude-code/internal/bootstrap"
	"magic-claude-code/internal/cert"
	"magic-claude-code/internal/config"
	"magic-claude-code/internal/frontend"
	"magic-claude-code/internal/i18n"
	"magic-claude-code/internal/providerquota"
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

// resolveDataDir determines the data directory in priority order:
// 1. Explicit -data flag
// 2. MCC_ROOT env var → $MCC_ROOT/data
// 3. Executable directory → <exec_dir>/data
// 4. Fallback: ./data
func resolveDataDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if root := os.Getenv("MCC_ROOT"); root != "" {
		return filepath.Join(root, "data")
	}
	if exe, err := os.Executable(); err == nil {
		return resolveDataDirFromExecutablePath(exe)
	}
	return "./data"
}

func resolveDataDirFromExecutablePath(exePath string) string {
	if realExe, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = realExe
	}
	return filepath.Join(filepath.Dir(exePath), "data")
}

func main() {
	msg := i18n.Load(i18n.ResolveLocale())

	setupFlagUsage()

	// 命令行参数
	showVersion := false
	flag.BoolVar(&showVersion, "v", false, msg.FlagVersion)
	flag.BoolVar(&showVersion, "version", false, msg.FlagVersion)
	dataDir := flag.String("data", "", msg.FlagDataDir)
	adminPassword := flag.String("password", os.Getenv("ADMIN_PASSWORD"), msg.FlagPassword)
	proxyListenFlag := flag.String("proxy-listen", "", msg.FlagProxyListen)
	proxyPortFlag := flag.Int("proxy-port", 0, msg.FlagProxyPort)
	adminListenFlag := flag.String("admin-listen", "", msg.FlagAdminListen)
	adminPortFlag := flag.Int("admin-port", 0, msg.FlagAdminPort)
	flag.Parse()

	// --version / -v：打印版本并退出，不启动任何服务
	if showVersion {
		fmt.Println(versionOutput())
		os.Exit(0)
	}

	// 解析数据目录：优先显式 -data，其次 MCC_ROOT，最后可执行文件目录
	resolvedDataDir := resolveDataDir(*dataDir)

	// 设置默认密码
	passwordState := resolveAdminPassword(*adminPassword, msg, generateRandomPassword)
	if passwordState.RandomGenerated {
		log.Println(msg.WarnNoPassword)
	}

	// 确保数据目录存在
	if err := os.MkdirAll(resolvedDataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// 加载配置
	configPath := filepath.Join(resolvedDataDir, "config.json")
	dbPath := filepath.Join(resolvedDataDir, "proxy.db")
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

	// 覆盖监听地址端口：CLI flag > 环境变量 > 配置文件（含默认值）
	applyListenConfig(cfg,
		listenOverride{
			ProxyAddr: *proxyListenFlag,
			ProxyPort: *proxyPortFlag,
			AdminAddr: *adminListenFlag,
			AdminPort: *adminPortFlag,
		},
		listenOverride{
			ProxyAddr: os.Getenv("MCC_PROXY_LISTEN_ADDR"),
			ProxyPort: envIntOrZero("MCC_PROXY_PORT"),
			AdminAddr: os.Getenv("MCC_ADMIN_LISTEN_ADDR"),
			AdminPort: envIntOrZero("MCC_ADMIN_PORT"),
		},
	)
	cfg.NormalizeDefaults()

	// 设置数据目录
	cfg.DataDir = resolvedDataDir

	// 证书管理
	certManager := cert.NewManager(resolvedDataDir)

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

	// 自动引导：尝试 hosts 修改、CA 信任安装、环境持久化
	log.Println(msg.BootstrapAttempting)
	bootExec := bootstrap.New(
		resolvedDataDir,
		certManager.GetCACertPath(),
		i18n.ResolveLocale(),
		bootstrap.WithPreferredMode(bootstrap.Mode(cfg.ConnectionMode)),
		bootstrap.WithGatewayListen(cfg.GatewayListenAddr, cfg.GatewayListenPort),
	)
	bootResult := bootExec.Run()
	bootExec.LogResult(bootResult)

	// 输出配置提示（仅在引导未完全成功时显示手动指引）
	showManualConfig := !bootstrap.IsTransparentReady(bootResult)

	fmt.Println("\n" + msg.BannerTop)
	fmt.Println(msg.BannerTitle)
	fmt.Println(msg.BannerTop)
	fmt.Printf(msg.ProxyPort+"\n", cfg.ProxyPort)
	fmt.Printf(msg.AdminPort+"\n", cfg.AdminPort)
	fmt.Printf(msg.BackendURL+msg.BackendURLNote+"\n", cfg.BackendURL)
	fmt.Println()

	if showManualConfig {
		if bootResult.SelectedMode == bootstrap.ModeTransparent {
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
		} else {
			fmt.Println(msg.BootstrapManualHint)
		}
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
		Password:       passwordState.Value,
		CertFile:       certManager.GetServerCertPath(),
		KeyFile:        filepath.Join(resolvedDataDir, "server.key"),
		ConfigPath:     configPath,
		ConfiguredMode: cfg.ConnectionMode,
		EffectiveMode:  string(bootResult.SelectedMode),
		ModeRationale:  bootResult.Rationale,
	}, configStore, proxyServer, usageHandler)
	adminServer.SetEffectiveListenState(cfg)

	adminServer.SetGatewayRestarter(proxyServer)

	// 创建额度查询管理器
	quotaSnapshotStore := providerquota.NewSnapshotStore(configStore.DB())
	quotaConfigGet := &quotaConfigGetter{configStore: configStore}
	quotaManager := providerquota.NewManager(quotaSnapshotStore, quotaConfigGet, 4)
	adminServer.SetQuotaManager(quotaManager)
	quotaManagerCtx, stopQuotaManager := context.WithCancel(context.Background())
	quotaManager.Start(quotaManagerCtx)

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
	startupErr := make(chan error, 1)
	proxyAddr := net.JoinHostPort(cfg.ProxyListenAddr, strconv.Itoa(cfg.ProxyPort))
	go func() {
		if err := proxyServer.Start(proxyAddr, certManager.GetServerCertPath(), filepath.Join(resolvedDataDir, "server.key")); err != nil {
			startupErr <- fmt.Errorf("proxy server: %w", err)
		}
	}()

	adminAddr := net.JoinHostPort(cfg.AdminListenAddr, strconv.Itoa(cfg.AdminPort))
	go func() {
		if err := adminServer.Start(adminAddr, frontend.DistFS); err != nil {
			startupErr <- fmt.Errorf("admin server: %w", err)
		}
	}()

	// 启动路由模式 HTTP 服务器
	go func() {
		addr := net.JoinHostPort(cfg.GatewayListenAddr, strconv.Itoa(cfg.GatewayListenPort))
		if err := proxyServer.StartGateway(addr); err != nil && err != http.ErrServerClosed {
			startupErr <- fmt.Errorf("gateway server: %w", err)
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
	case err := <-startupErr:
		log.Fatalf("Startup failed: %v", err)
	case <-quit:
		log.Println(msg.ShuttingDown)
	case <-restartCh:
		log.Println(msg.UpdateAppliedRestarting)
	}

	stopUsageSync()
	stopQuotaManager()

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

// quotaConfigGetter adapts config.ConfigStore to providerquota.ProviderConfigGetter.
type quotaConfigGetter struct {
	configStore config.ConfigStore
}

func (g *quotaConfigGetter) GetProviderByID(id string) *providerquota.ProviderConfig {
	cfg, err := g.configStore.Load()
	if err != nil {
		return nil
	}
	p := cfg.GetProviderByID(id)
	if p == nil {
		return nil
	}
	return &providerquota.ProviderConfig{
		ID:         p.ID,
		Enabled:    p.Enabled,
		APIURL:     p.APIURL,
		APIToken:   p.APIToken,
		QuotaQuery: p.QuotaQuery,
	}
}

func (g *quotaConfigGetter) ListEnabledProviders() []providerquota.ProviderConfig {
	cfg, err := g.configStore.Load()
	if err != nil {
		return nil
	}
	var result []providerquota.ProviderConfig
	for _, p := range cfg.Providers {
		if p.Enabled {
			result = append(result, providerquota.ProviderConfig{
				ID:         p.ID,
				Enabled:    p.Enabled,
				APIURL:     p.APIURL,
				APIToken:   p.APIToken,
				QuotaQuery: p.QuotaQuery,
			})
		}
	}
	return result
}
