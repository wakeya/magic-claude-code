// Package bootstrap handles automatic transparent-mode setup at startup,
// including hosts modification, CA trust installation, and fallback mode selection.
package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"magic-claude-code/internal/i18n"
)

// Sentinel errors for PersistNodeCACert outcomes.
var (
	// ErrPartialSuccess indicates some operations succeeded but others failed
	// (e.g. setx failed but pwsh profile was written). The caller should NOT
	// write the idempotency marker so the failed part is retried on next launch.
	ErrPartialSuccess = errors.New("partial success: some operations succeeded but others failed")
	// ErrEnvironmentRefresh indicates that the Windows user environment was
	// persisted, but the desktop shell notification failed. Signing out and back
	// in rebuilds the process environment from the persisted registry value.
	ErrEnvironmentRefresh = errors.New("Windows environment refresh failed")
	// ErrUserCustomValue indicates the user has a hand-written NODE_EXTRA_CA_CERTS
	// entry outside mcc-managed blocks. mcc will not overwrite it.
	ErrUserCustomValue = errors.New("user custom NODE_EXTRA_CA_CERTS already exists")
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
	Partial   bool // 部分成功（如 setx 失败但 profile 成功）：需重试失败部分
	Err       error
}

// Result records the full bootstrap outcome.
type Result struct {
	Caps              Capabilities
	HostsResult       StepResult
	TrustResult       StepResult
	EnvResult         StepResult
	NodeCAResult      StepResult // NODE_EXTRA_CA_CERTS 持久化结果
	PreferredMode     Mode
	SelectedMode      Mode
	Rationale         string
	CACertPath        string
	ExecRootDir       string
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
	// LookupNodeCACert reads the current platform's persisted/session value
	// without mutating it, so user-managed CA configuration is not overwritten.
	LookupNodeCACert() (value string, exists bool, err error)
	// PersistNodeCACert 把指向 mcc CA 文件的 NODE_EXTRA_CA_CERTS 持久化到
	// 当前用户的 shell/桌面会话环境，使未来启动的 Node.js 客户端能信任 mcc。
	PersistNodeCACert(caCertPath string) error
}

// Executor runs the bootstrap sequence.
type Executor struct {
	dataDir           string
	caCertPath        string
	locale            string
	preferredMode     Mode
	gatewayListenAddr string
	gatewayListenPort int
	msg               i18n.Messages
	hosts             HostsAdapter
	trust             TrustAdapter
	env               EnvAdapter
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
		dataDir:           dataDir,
		caCertPath:        caCertPath,
		locale:            locale,
		preferredMode:     ModeTransparent,
		gatewayListenAddr: "127.0.0.1",
		gatewayListenPort: 17487,
		msg:               msg,
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
		Caps:              caps,
		CACertPath:        e.caCertPath,
		PreferredMode:     normalizeMode(e.preferredMode),
		GatewayListenAddr: e.gatewayListenAddr,
		GatewayListenPort: e.gatewayListenPort,
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
			// CA 已就绪后，持久化 Node 客户端 CA 信任
			// Docker 内跳过：容器内 profile 改动对宿主无意义（spec 约束 10）
			if result.TrustResult.Success && !caps.IsDocker {
				result.NodeCAResult = e.tryPersistNodeCA()
			}
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

// tryPersistNodeCA 持久化 NODE_EXTRA_CA_CERTS，使未来启动的 Node.js 客户端
// （如 Claude Code）能信任 mcc 的 CA。仅在透明模式、非 Docker、CA 已就绪时调用。
func (e *Executor) tryPersistNodeCA() StepResult {
	if e.caCertPath == "" {
		return StepResult{Attempted: false}
	}
	caCertPath, err := filepath.Abs(e.caCertPath)
	if err != nil {
		return StepResult{Attempted: true, Success: false, Err: fmt.Errorf("absolute CA cert path: %w", err)}
	}
	if _, err := os.Stat(caCertPath); err != nil {
		// CA 文件不存在，依赖未满足
		return StepResult{Attempted: true, Success: false, Err: err}
	}
	markerMatches := hasNodeCAMarker(e.dataDir, caCertPath)
	// P2-2: 高权限运行（root/administrator）时拒绝写用户 profile/HKCU/session。
	// 真实用户的 Node 客户端读自己的 profile，root/admin 写的它读不到（功能无效）；
	// 且 HOME 等用户可控路径在高权限下可能被重定向越权（CWE-59）。让用户非特权重启 mcc。
	if isPrivilegedRun() {
		if markerMatches {
			return StepResult{Success: true}
		}
		return StepResult{Attempted: true, Success: false, Err: ErrPrivilegedRun}
	}
	existing, exists, err := e.env.LookupNodeCACert()
	if err != nil {
		return StepResult{Attempted: true, Success: false, Err: fmt.Errorf("lookup persisted NODE_EXTRA_CA_CERTS: %w", err)}
	}
	if exists && existing != "" && !nodeCAPathsEqual(existing, caCertPath) {
		previous, managed := previousManagedNodeCAPath(e.dataDir)
		if !managed || !nodeCAPathsEqual(existing, previous) {
			return StepResult{Attempted: true, Success: false, Err: ErrUserCustomValue}
		}
	}
	// Marker 命中仍需先检查真实环境值，避免用户后续设置的自定义值被误报为 MCC 就绪。
	if markerMatches {
		return StepResult{Success: true}
	}
	err = e.env.PersistNodeCACert(caCertPath)
	if err == nil {
		writeNodeCAMarker(e.dataDir, caCertPath)
		return StepResult{Attempted: true, Success: true}
	}
	if errors.Is(err, ErrPartialSuccess) {
		// profile 已写但 setx/launchctl 失败：不写 marker，下次重试
		return StepResult{Attempted: true, Success: false, Partial: true, Err: err}
	}
	if errors.Is(err, ErrUserCustomValue) {
		// 不写 marker：用户清除自定义值后 mcc 应自动接管。
		// 重复警告由 stateHash 抑制（NodeCAErr 已纳入 hash，状态不变会 suppress）。
		return StepResult{Attempted: true, Success: false, Err: err}
	}
	return StepResult{Attempted: true, Success: false, Err: err}
}

const nodeCAMarkerName = ".node-ca-persisted"

// nodeCAMarker 是 .node-ca-persisted 的 JSON 格式。除指纹外还记录证书路径和用户标识
// (HOME/UID)，避免"证书内容没变但路径/用户变了"时错误跳过重新持久化（F-3/F-4）。
type nodeCAMarker struct {
	Fingerprint string `json:"fp"`
	CertPath    string `json:"cert_path"`
	Home        string `json:"home"`
	UID         int    `json:"uid,omitempty"` // unix 普通用户；root/windows 不写
}

// hasNodeCAMarker reports whether the NodeCA marker matches the current cert AND
// user identity. Returns false (triggering re-persistence) when the marker is
// missing/corrupt/legacy-plain-text, the cert fingerprint changed, the cert path
// changed (F-3), or the HOME/UID changed across users (F-4).
func hasNodeCAMarker(dataDir, caCertPath string) bool {
	if dataDir == "" {
		return false
	}
	markerPath := filepath.Join(dataDir, nodeCAMarkerName)
	if err := isSafeForWrite(markerPath); err != nil {
		return false // marker 是符号链接/非常规 → 视为 stale（CWE-59）
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return false
	}
	var m nodeCAMarker
	if err := json.Unmarshal(raw, &m); err != nil {
		return false // 旧纯文本格式或损坏 → stale
	}
	if m.Fingerprint == "" {
		return false
	}
	current, err := caFingerprint(caCertPath)
	if err != nil {
		return false
	}
	if m.Fingerprint != current {
		return false // 证书重新生成
	}
	if m.CertPath != caCertPath {
		return false // F-3: 路径变化
	}
	if !nodeCAMarkerUserMatches(m) {
		return false // F-4: 跨用户
	}
	return true
}

// previousManagedNodeCAPath returns the prior CA path only when the marker is a
// safe MCC marker bound to the current user. Fingerprint equality is deliberately
// not required: a moved or regenerated CA still needs permission to replace the
// exact old path that MCC previously persisted.
func previousManagedNodeCAPath(dataDir string) (string, bool) {
	if dataDir == "" {
		return "", false
	}
	markerPath := filepath.Join(dataDir, nodeCAMarkerName)
	if err := isSafeForWrite(markerPath); err != nil {
		return "", false
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return "", false
	}
	var m nodeCAMarker
	if json.Unmarshal(raw, &m) != nil || m.Fingerprint == "" || m.CertPath == "" || !nodeCAMarkerUserMatches(m) {
		return "", false
	}
	return m.CertPath, true
}

func nodeCAPathsEqual(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// nodeCAMarkerUserMatches 检查 marker 记录的 HOME/UID 是否与当前进程一致。
// HOME 是跨平台主标识：marker 必须记录非空 HOME 且与当前进程一致，缺 HOME 的
// marker（旧格式或手构造）一律视为 stale，避免任意用户命中（F-4）。
// UID 在 Unix 普通用户额外强制校验：marker 必须记录匹配的 UID，缺 UID 也视为
// stale；root(uid=0)/Windows(uid=-1) 不持久化 UID，仅靠 HOME 绑定。
func nodeCAMarkerUserMatches(m nodeCAMarker) bool {
	if m.Home == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home != m.Home {
		return false
	}
	if uid := os.Getuid(); uid > 0 {
		// Unix 普通用户：marker 必须记录匹配的 UID。
		if m.UID != uid {
			return false
		}
	} else if m.UID != 0 {
		// root(uid=0)/Windows(uid=-1)：writeNodeCAMarker 不持久化 UID，marker
		// 不应记录 UID；读到非 0 UID 说明被构造/篡改，一律视为 stale。
		return false
	}
	return true
}

// writeNodeCAMarker records the cert fingerprint, path, and current user identity
// (HOME/UID) so future launches can detect staleness from cert regen, path change,
// or cross-user launch. Best-effort — failure is silent.
func writeNodeCAMarker(dataDir, caCertPath string) {
	if dataDir == "" {
		return
	}
	fp, err := caFingerprint(caCertPath)
	if err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return // 无法确定用户身份 → 不写可跨用户命中的 marker（F-4）
	}
	m := nodeCAMarker{
		Fingerprint: fp,
		CertPath:    caCertPath,
		Home:        home,
	}
	if uid := os.Getuid(); uid > 0 {
		m.UID = uid // unix 普通用户；root(0)/windows(-1) 不写
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return
	}
	markerPath := filepath.Join(dataDir, nodeCAMarkerName)
	if err := isSafeForWrite(markerPath); err != nil {
		return // marker 是符号链接 → 拒绝写，避免越权（CWE-59）
	}
	_ = os.WriteFile(markerPath, data, 0644)
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

	if IsTransparentReady(r) && !(r.NodeCAResult.Attempted && (!r.NodeCAResult.Success || r.NodeCAResult.Partial)) {
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
	printStep(e.locale, "NODE_CA", r.NodeCAResult)

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
		// NodeCA 持续异常时追加简短提示（不破坏 suppress 设计）
		if r.NodeCAResult.Attempted && (!r.NodeCAResult.Success || r.NodeCAResult.Partial) {
			if locale == "zh" {
				log.Printf("[Bootstrap] ⚠ NODE_EXTRA_CA_CERTS 未完全就绪，详见上次启动输出（删除 .bootstrap-state 可重新查看）")
			} else {
				log.Printf("[Bootstrap] ⚠ NODE_EXTRA_CA_CERTS not fully ready; see previous launch output (delete .bootstrap-state to show again)")
			}
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
	if sr.Partial {
		if locale == "zh" {
			fmt.Printf("  %s: 部分成功", name)
		} else {
			fmt.Printf("  %s: PARTIAL", name)
		}
		if sr.Err != nil {
			fmt.Printf(" (%v)", sr.Err)
		}
		fmt.Println()
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
