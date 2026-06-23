// Package bootstrap handles automatic transparent-mode setup at startup,
// including hosts modification, CA trust installation, and fallback mode selection.
package bootstrap

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"magic-claude-code/internal/i18n"
)

// Mode represents a connection mode.
type Mode string

const (
	ModeTransparent Mode = "transparent"
	ModeTunnel      Mode = "tunnel"
	ModeGateway     Mode = "gateway"
)

// Capabilities records what the runtime environment can do.
type Capabilities struct {
	CanEditHosts  bool
	CanTrustCA    bool
	CanPersistEnv bool
	IsDocker      bool
	HasHostHelper bool
}

// StepResult records the outcome of a single bootstrap step.
type StepResult struct {
	Attempted bool
	Success   bool
	Err       error
}

// Result records the full bootstrap outcome.
type Result struct {
	Caps             Capabilities
	HostsResult      StepResult
	TrustResult      StepResult
	EnvResult        StepResult
	PreferredMode    Mode
	SelectedMode     Mode
	Rationale        string
	CACertPath       string
	ExecRootDir      string
	GatewayListenAddr string
	GatewayListenPort int
}

// HostsAdapter abstracts hosts-file modification.
type HostsAdapter interface {
	EnsureHostMapping(domain, ip string) error
	// HasMapping reports whether the hosts file already contains the correct
	// domain→ip mapping. This is a read-only check that needs no elevated
	// privileges, enabling non-root/non-admin launches to detect a prior
	// privileged configuration.
	HasMapping(domain, ip string) bool
}

// TrustAdapter abstracts CA trust-store installation.
type TrustAdapter interface {
	InstallCA(certPath string) error
}

// EnvAdapter abstracts environment persistence.
type EnvAdapter interface {
	PersistRoot(rootDir string) error
}

// Executor runs the bootstrap sequence.
type Executor struct {
	dataDir            string
	caCertPath         string
	locale             string
	preferredMode      Mode
	gatewayListenAddr  string
	gatewayListenPort  int
	msg                i18n.Messages
	hosts              HostsAdapter
	trust              TrustAdapter
	env                EnvAdapter
}

// Option configures an Executor.
type Option func(*Executor)

// WithPreferredMode sets the requested startup mode preference.
func WithPreferredMode(mode Mode) Option {
	return func(e *Executor) { e.preferredMode = mode }
}

// WithGatewayListen sets the gateway listen address and port for instruction output.
func WithGatewayListen(addr string, port int) Option {
	return func(e *Executor) {
		e.gatewayListenAddr = addr
		e.gatewayListenPort = port
	}
}

// WithHostsAdapter overrides the default hosts adapter.
func WithHostsAdapter(a HostsAdapter) Option {
	return func(e *Executor) { e.hosts = a }
}

// WithTrustAdapter overrides the default trust adapter.
func WithTrustAdapter(a TrustAdapter) Option {
	return func(e *Executor) { e.trust = a }
}

// WithEnvAdapter overrides the default env adapter.
func WithEnvAdapter(a EnvAdapter) Option {
	return func(e *Executor) { e.env = a }
}

// New creates an Executor for the given data directory and locale.
func New(dataDir, caCertPath, locale string, opts ...Option) *Executor {
	msg := i18n.Load(locale)
	e := &Executor{
		dataDir:            dataDir,
		caCertPath:         caCertPath,
		locale:             locale,
		preferredMode:      ModeTransparent,
		gatewayListenAddr:  "127.0.0.1",
		gatewayListenPort:  17487,
		msg:                msg,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.hosts == nil {
		e.hosts = newOSHostsAdapter()
	}
	if e.trust == nil {
		e.trust = newOSTrustAdapter()
	}
	if e.env == nil {
		e.env = newOSEnvAdapter()
	}
	return e
}

const (
	targetDomain = "api.anthropic.com"
	targetIP     = "127.0.0.1"
)

// IsTransparentReady reports whether transparent mode is fully configured.
func IsTransparentReady(r Result) bool {
	return r.SelectedMode == ModeTransparent &&
		r.HostsResult.Success && r.TrustResult.Success && r.EnvResult.Success
}

// Run executes the bootstrap sequence and returns the result.
// It never panics or fatals — every step is best-effort.
func (e *Executor) Run() Result {
	caps := detectCapabilities()
	result := Result{
		Caps:               caps,
		CACertPath:         e.caCertPath,
		PreferredMode:      normalizeMode(e.preferredMode),
		GatewayListenAddr:  e.gatewayListenAddr,
		GatewayListenPort:  e.gatewayListenPort,
	}

	exe, err := os.Executable()
	if err == nil {
		result.ExecRootDir = filepath.Dir(exe)
	}

	switch result.PreferredMode {
	case ModeTransparent:
		if caps.IsDocker && !caps.HasHostHelper {
			e.logDockerBoundary(&result)
		} else {
			// 总是调用：tryHosts/tryTrustCA 内部先做只读检测（HasMapping / CA 标记），
			// 已配置则跳过实际操作。这让非特权启动（首次 sudo/admin 配置后）
			// 也能正确识别透明模式，不因 CanEditHosts/CanTrustCA 为 false 而跳过检测。
			result.HostsResult = e.tryHosts()
			result.TrustResult = e.tryTrustCA()
		}
		result.SelectedMode, result.Rationale = resolveModeLocalized(result.PreferredMode, result.Caps, result.HostsResult, result.TrustResult, e.locale)
	case ModeTunnel:
		result.SelectedMode = ModeTunnel
		result.Rationale = ""
	case ModeGateway:
		result.SelectedMode = ModeGateway
		result.Rationale = ""
	default:
		result.SelectedMode = ModeTransparent
	}

	if caps.CanPersistEnv && result.ExecRootDir != "" {
		result.EnvResult = e.tryPersistEnv(result.ExecRootDir)
	}

	return result
}

func (e *Executor) tryHosts() StepResult {
	// 先做只读检测：hosts 已含正确映射则直接成功，跳过写入。
	// 这让首次 sudo/admin 配置后的非特权启动也能正确报告透明模式。
	if e.hosts.HasMapping(targetDomain, targetIP) {
		return StepResult{Success: true}
	}
	err := e.hosts.EnsureHostMapping(targetDomain, targetIP)
	return StepResult{Attempted: true, Success: err == nil, Err: err}
}

func (e *Executor) tryTrustCA() StepResult {
	// 先检测标记文件（只需读权限）：首次安装成功后写入的 .ca-trust-installed，
	// 含 CA 证书 fingerprint。fingerprint 不匹配（证书被重新生成）则视为未安装。
	// 后续非特权启动据此跳过安装，避免因无写权限而误判降级。
	if hasCATrustMarker(e.dataDir, e.caCertPath) {
		return StepResult{Success: true}
	}
	err := e.trust.InstallCA(e.caCertPath)
	if err == nil {
		writeCATrustMarker(e.dataDir, e.caCertPath)
	}
	return StepResult{Attempted: true, Success: err == nil, Err: err}
}

func (e *Executor) tryPersistEnv(rootDir string) StepResult {
	err := e.env.PersistRoot(rootDir)
	return StepResult{Attempted: true, Success: err == nil, Err: err}
}

func (e *Executor) logDockerBoundary(result *Result) {
	if e.locale == "zh" {
		log.Println("[Bootstrap] 运行在 Docker 容器中，无法修改宿主机 hosts 或 CA 信任库。")
		log.Println("[Bootstrap] 如需自动修改宿主机，请配置宿主机侧 helper（设置 MCC_HOST_HELPER 环境变量）。")
	} else {
		log.Println("[Bootstrap] Running inside Docker; cannot modify host hosts file or CA trust store.")
		log.Println("[Bootstrap] To automate host setup, configure a host-side helper (set MCC_HOST_HELPER env var).")
	}
}

// LogResult prints the bootstrap result using localized messages.
func (e *Executor) LogResult(r Result) {
	statePath := filepath.Join(e.dataDir, ".bootstrap-state")
	if shouldSuppress(statePath, r) {
		// 状态没变时跳过长篇输出，但仍打印一行简短确认，
		// 让用户知道当前生效的模式（避免完全静默）。
		printModeSummary(r, e.locale)
		return
	}
	saveState(statePath, r)

	if IsTransparentReady(r) {
		if e.locale == "zh" {
			log.Println("[Bootstrap] 透明模式配置完成：hosts 已更新，CA 已安装。")
		} else {
			log.Println("[Bootstrap] Transparent mode configured: hosts updated, CA installed.")
		}
		return
	}

	fmt.Println()
	if e.locale == "zh" {
		fmt.Println("========== 引导结果 ==========")
	} else {
		fmt.Println("========== Bootstrap Result ==========")
	}

	printStep(e.locale, "hosts", r.HostsResult)
	printStep(e.locale, "CA", r.TrustResult)
	printStep(e.locale, "ENV", r.EnvResult)

	fmt.Println()
	instr := generateInstructions(r, e.locale)
	for _, line := range instr {
		fmt.Println(line)
	}
	fmt.Println("======================================")
}

// printModeSummary prints a one-line status for suppressed runs, so users
// still see which mode is active without the full instruction block.
func printModeSummary(r Result, locale string) {
	if r.SelectedMode == ModeTransparent && r.HostsResult.Success && r.TrustResult.Success {
		if locale == "zh" {
			log.Printf("[Bootstrap] 透明模式就绪（hosts/CA 已配置，状态未变化，跳过详细输出）")
		} else {
			log.Printf("[Bootstrap] Transparent mode ready (hosts/CA configured; state unchanged, details suppressed)")
		}
		return
	}
	names := map[Mode]string{
		ModeTransparent: "Transparent",
		ModeTunnel:      "Tunnel",
		ModeGateway:     "Route",
	}
	if locale == "zh" {
		names = map[Mode]string{
			ModeTransparent: "透明模式",
			ModeTunnel:      "隧道模式",
			ModeGateway:     "路由模式",
		}
		log.Printf("[Bootstrap] 当前生效模式：%s", names[r.SelectedMode])
	} else {
		log.Printf("[Bootstrap] Effective mode: %s", names[r.SelectedMode])
	}
}

func printStep(locale, name string, sr StepResult) {
	statusZh := map[bool]string{true: "成功", false: "失败"}
	statusEn := map[bool]string{true: "OK", false: "FAILED"}
	if !sr.Attempted {
		// 区分两种未尝试的情况：
		//   Success=true → 已就绪（HasMapping/CA 标记命中，无需操作）
		//   Success=false → 真正跳过（如 ENV 步骤在 Docker 下不可用）
		if sr.Success {
			if locale == "zh" {
				fmt.Printf("  %s: 就绪\n", name)
			} else {
				fmt.Printf("  %s: ready\n", name)
			}
		} else {
			if locale == "zh" {
				fmt.Printf("  %s: 跳过\n", name)
			} else {
				fmt.Printf("  %s: skipped\n", name)
			}
		}
		return
	}
	if locale == "zh" {
		fmt.Printf("  %s: %s", name, statusZh[sr.Success])
		if sr.Err != nil {
			fmt.Printf(" (%v)", sr.Err)
		}
		fmt.Println()
	} else {
		fmt.Printf("  %s: %s", name, statusEn[sr.Success])
		if sr.Err != nil {
			fmt.Printf(" (%v)", sr.Err)
		}
		fmt.Println()
	}
}

func detectCapabilities() Capabilities {
	caps := Capabilities{
		IsDocker: isDockerEnvFn(),
	}

	helperPath := os.Getenv("MCC_HOST_HELPER")
	if helperPath != "" {
		if validateHostHelperPath(helperPath) == nil {
			caps.HasHostHelper = true
		}
	}

	if caps.IsDocker && !caps.HasHostHelper {
		return caps
	}
	if caps.IsDocker && caps.HasHostHelper {
		caps.CanEditHosts = true
		caps.CanTrustCA = true
		return caps
	}

	caps.CanEditHosts = canWriteHosts()
	caps.CanTrustCA = canTrustCAStore()
	caps.CanPersistEnv = true
	return caps
}

func isDockerEnv() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

func canWriteHosts() bool {
	hostsPath := getHostsPath()
	f, err := os.OpenFile(hostsPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func getHostsPath() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("WINDIR") + `\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

func canTrustCAStore() bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := exec.LookPath("security")
		return err == nil
	case "windows":
		_, err := exec.LookPath("certutil")
		return err == nil
	default:
		for _, tool := range []string{"update-ca-certificates", "update-ca-trust"} {
			if _, err := exec.LookPath(tool); err == nil {
				return true
			}
		}
		return false
	}
}
