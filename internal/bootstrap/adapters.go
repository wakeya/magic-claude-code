package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const externalCmdTimeout = 30 * time.Second

func execWithTimeout(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), externalCmdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

var isDockerEnvFn = isDockerEnv

// --- Testable hooks for Windows pwsh/setx (not part of EnvAdapter interface) ---

// setxEnvVar persists a user-level environment variable via setx.
// Overridable in tests to avoid real registry writes.
var setxEnvVar = func(key, value string) error {
	out, err := execWithTimeout("setx", key, value)
	if err != nil {
		return fmt.Errorf("setx %s: %w: %s", key, err, decodeCmdOutput(out))
	}
	return nil
}

// writeFileSync writes profile content (injectable wrapper around os.WriteFile).
// Tests use it to simulate write failures without relying on chmod semantics
// that are unreliable on Windows.
var writeFileSync = func(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// pwshDetected reports whether any PowerShell is installed.
// Overridable in tests to skip/exec-path probing.
var pwshDetected = func() bool {
	_, err1 := exec.LookPath("pwsh.exe")
	_, err2 := exec.LookPath("powershell.exe")
	return err1 == nil || err2 == nil
}

// pwshProfileCandidates returns the ordered list of PowerShell profile paths.
// Overridable in tests to use temp directories.
var pwshProfileCandidates = func(home string) []string {
	return []string{
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
	}
}

// launchctlSetenv sets an environment variable in the current macOS GUI session.
// Overridable in tests to avoid real launchctl calls.
var launchctlSetenv = func(key, value string) error {
	out, err := execWithTimeout("launchctl", "setenv", key, value)
	if err != nil {
		return fmt.Errorf("launchctl setenv %s: %w: %s", key, err, decodeCmdOutput(out))
	}
	return nil
}

// hasLaunchctl reports whether the macOS launchctl command is available.
// Overridable in tests to simulate macOS on non-macOS platforms.
var hasLaunchctl = func() bool {
	_, err := exec.LookPath("launchctl")
	return err == nil
}

// osHostsAdapter handles real hosts-file modification.
type osHostsAdapter struct{}

func newOSHostsAdapter() HostsAdapter { return &osHostsAdapter{} }

func (a *osHostsAdapter) EnsureHostMapping(domain, ip string) error {
	if isDockerEnvFn() {
		if helperPath := os.Getenv("MCC_HOST_HELPER"); helperPath != "" {
			if err := runHostHelper(helperPath, "hosts", "add", domain, ip); err != nil {
				return fmt.Errorf("host helper hosts add: %w", err)
			}
			return nil
		}
		return fmt.Errorf("docker host helper not configured")
	}

	hostsPath := getHostsPath()
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("read hosts: %w", err)
	}

	newContent, changed := processHostsContent(string(content), domain, ip)
	if !changed {
		return nil
	}

	if err := os.WriteFile(hostsPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("write hosts: %w", err)
	}
	return nil
}

// HasMapping reports whether the hosts file already maps domain to ip.
// Read-only — no elevated privileges required.
// On Docker, the container /etc/hosts is isolated from the host's, so we
// delegate to the host helper (which checks the data-dir marker written by
// setup-host.sh). This keeps HasMapping symmetric with EnsureHostMapping:
// both reflect host state, not container state.
func (a *osHostsAdapter) HasMapping(domain, ip string) bool {
	if isDockerEnvFn() {
		helperPath := os.Getenv("MCC_HOST_HELPER")
		if helperPath == "" {
			return false
		}
		return runHostHelper(helperPath, "hosts", "add", domain, ip) == nil
	}
	content, err := os.ReadFile(getHostsPath())
	if err != nil {
		return false
	}
	_, changed := processHostsContent(string(content), domain, ip)
	return !changed
}

// processHostsContent removes all existing mappings for domain that point to a
// different IP, then appends the correct ip→domain mapping if it was missing.
// Returns the new content and whether it changed (by comparing final vs original).
func processHostsContent(content, domain, ip string) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return ip + " " + domain + "\n", true
	}

	normalized := strings.TrimRight(content, "\n")
	lines := strings.Split(normalized, "\n")
	var result []string
	correctExists := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(result) == 0 {
				continue
			}
			result = append(result, line)
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			result = append(result, line)
			continue
		}

		lineIP := fields[0]
		var remaining []string
		domainInLine := false
		for _, h := range fields[1:] {
			if h == domain {
				domainInLine = true
				if lineIP == ip {
					correctExists = true
				}
			} else {
				remaining = append(remaining, h)
			}
		}

		if domainInLine && lineIP != ip {
			if len(remaining) > 0 {
				result = append(result, lineIP+" "+strings.Join(remaining, " "))
			}
		} else {
			result = append(result, line)
		}
	}

	if !correctExists {
		result = append(result, ip+" "+domain)
	}

	output := strings.Join(result, "\n") + "\n"
	changed := output != normalized+"\n"
	return output, changed
}

// osTrustAdapter handles real CA trust-store installation.
type osTrustAdapter struct{}

func newOSTrustAdapter() TrustAdapter { return &osTrustAdapter{} }

// caTrustMarkerName is the marker file written to dataDir after successful CA
// installation. Bootstrap reads it to skip re-installation on subsequent
// non-privileged launches (first run as admin, later runs as normal user).
// docker-host-helper.sh also checks this marker for Docker transparent mode.
const caTrustMarkerName = ".ca-trust-installed"

// caTrustMarker is the on-disk JSON shape of the marker file.
type caTrustMarker struct {
	Action      string `json:"action"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
}

// caFingerprint returns the hex-encoded SHA256 of the raw cert file bytes.
// Used to detect CA regeneration: if the cert changes but the marker still
// references the old fingerprint, the marker is stale and CA must be reinstalled.
func caFingerprint(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// hasCATrustMarker reports whether the CA-installation marker exists in dataDir
// AND its recorded fingerprint matches the current CA cert. Fingerprint mismatch
// (e.g. after cert regeneration) yields false so the caller reinstalls.
// On Docker, setup-host.sh writes the marker without a fingerprint; in that case
// hasCATrustMarker returns false and the caller falls through to the host helper.
func hasCATrustMarker(dataDir, caCertPath string) bool {
	if dataDir == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, caTrustMarkerName))
	if err != nil {
		return false
	}
	var m caTrustMarker
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	if m.Fingerprint == "" {
		return false // legacy marker without fingerprint — fall through to install/helper
	}
	current, err := caFingerprint(caCertPath)
	if err != nil {
		return false // cert unreadable — cannot confirm, fall through
	}
	return m.Fingerprint == current
}

// writeCATrustMarker records that CA installation succeeded, embedding the
// current cert fingerprint so future launches can detect staleness.
// Best-effort — failure to write the marker does not block the current run.
func writeCATrustMarker(dataDir, caCertPath string) {
	if dataDir == "" {
		return
	}
	fp, err := caFingerprint(caCertPath)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return
	}
	m := caTrustMarker{
		Action:      "ca-trust-installed",
		Fingerprint: fp,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dataDir, caTrustMarkerName), data, 0644)
}

func (a *osTrustAdapter) InstallCA(certPath string) error {
	if isDockerEnvFn() {
		if helperPath := os.Getenv("MCC_HOST_HELPER"); helperPath != "" {
			if err := runHostHelper(helperPath, "trust", "install", certPath); err != nil {
				return fmt.Errorf("host helper trust install: %w", err)
			}
			return nil
		}
		return fmt.Errorf("docker host helper not configured")
	}

	switch runtime.GOOS {
	case "darwin":
		return a.installDarwin(certPath)
	case "windows":
		return a.installWindows(certPath)
	default:
		return a.installLinux(certPath)
	}
}

func (a *osTrustAdapter) installDarwin(certPath string) error {
	if _, err := exec.LookPath("security"); err != nil {
		return fmt.Errorf("security command not found: %w", err)
	}
	out, err := execWithTimeout("security", "add-trusted-cert", "-d",
		"-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", certPath)
	if err != nil {
		return fmt.Errorf("security add-trusted-cert (permission denied or not root): %w: %s", err, string(out))
	}
	return nil
}

func (a *osTrustAdapter) installWindows(certPath string) error {
	out, err := execWithTimeout("certutil", "-addstore", "-f", "ROOT", certPath)
	if err != nil {
		return fmt.Errorf("certutil -addstore: %w: %s", err, decodeCmdOutput(out))
	}
	return nil
}

func (a *osTrustAdapter) installLinux(certPath string) error {
	if _, err := exec.LookPath("update-ca-certificates"); err == nil {
		destDir := "/usr/local/share/ca-certificates"
		dest := destDir + "/mcc-proxy-ca.crt"
		data, err := os.ReadFile(certPath)
		if err != nil {
			return fmt.Errorf("read ca cert: %w", err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("write ca cert to %s (permission denied?): %w", dest, err)
		}
		out, err := execWithTimeout("update-ca-certificates")
		if err != nil {
			return fmt.Errorf("update-ca-certificates (permission denied or not root): %w: %s", err, string(out))
		}
		return nil
	}
	if _, err := exec.LookPath("update-ca-trust"); err == nil {
		dest := "/etc/pki/ca-trust/source/anchors/mcc-proxy-ca.pem"
		data, err := os.ReadFile(certPath)
		if err != nil {
			return fmt.Errorf("read ca cert: %w", err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return fmt.Errorf("write ca cert to %s (permission denied?): %w", dest, err)
		}
		out, err := execWithTimeout("update-ca-trust", "extract")
		if err != nil {
			return fmt.Errorf("update-ca-trust (permission denied or not root): %w: %s", err, string(out))
		}
		return nil
	}
	return fmt.Errorf("no supported CA trust tool found (tried update-ca-certificates, update-ca-trust)")
}

// osEnvAdapter handles real environment persistence.
type osEnvAdapter struct{}

func newOSEnvAdapter() EnvAdapter { return &osEnvAdapter{} }

func (a *osEnvAdapter) PersistRoot(rootDir string) error {
	switch runtime.GOOS {
	case "windows":
		out, err := execWithTimeout("setx", "MCC_ROOT", rootDir)
		if err != nil {
			return fmt.Errorf("setx MCC_ROOT: %w: %s", err, decodeCmdOutput(out))
		}
		return nil
	default:
		shell := os.Getenv("SHELL")
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("user home dir: %w", err)
		}

		entry := shellExportEntry(shell, "MCC_ROOT", rootDir)
		profiles := resolveShellProfiles(shell, home)
		openProfile := func(p string) (writeCloser, error) {
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
			}
			return os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		}
		var lastErr error
		for _, profile := range profiles {
			if existing, rErr := os.ReadFile(profile); rErr == nil {
				content := string(existing)
				if profileHasEquivalentEntry(shell, content, "MCC_ROOT", rootDir) ||
					profileHasExactEntry(content, entry) {
					return nil
				}
			}
			if err := writeProfileEntry(openProfile, profile, entry); err != nil {
				lastErr = err
				continue
			}
			return nil
		}
		if lastErr != nil {
			return lastErr
		}
		return fmt.Errorf("no profile file writable (tried %v)", profiles)
	}
}

// PersistNodeCACert 把 NODE_EXTRA_CA_CERTS 持久化到当前用户的 shell/桌面会话环境。
// 平台实现由 persistNodeCACertWindows / persistNodeCACertDarwin / persistNodeCACertPOSIX 提供。
func (a *osEnvAdapter) PersistNodeCACert(caCertPath string) error {
	switch runtime.GOOS {
	case "windows":
		return a.persistNodeCACertWindows(caCertPath)
	case "darwin":
		return a.persistNodeCACertDarwin(caCertPath)
	default:
		return a.persistNodeCACertPOSIX(caCertPath)
	}
}

// --- Windows implementation ---

const (
	pwshProfileMarkerBegin = "# >>> mcc: Node.js CA trust (auto-managed, do not edit) >>>"
	pwshProfileMarkerEnd   = "# <<< mcc <<<"
)

func (a *osEnvAdapter) persistNodeCACertWindows(caCertPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	// F-1 fail-closed: setx 前预检查所有 profile。高权限运行遇 symlink/非常规
	// profile 时返回 ErrUnsafeProfile（不 setx）；非特权运行跟随 symlink 读取。
	custom, scanErr := scanPwshProfilesForCustomValue(home, isPrivilegedRun())
	if scanErr != nil {
		return scanErr
	}
	if custom {
		return ErrUserCustomValue
	}
	var setxErr error
	// ① setx 写用户级注册表（影响未来新进程）
	if err := setxEnvVar("NODE_EXTRA_CA_CERTS", caCertPath); err != nil {
		setxErr = err
	}

	// ② pwsh $PROFILE 兜底（覆盖 GUI 继承断链场景）
	profileErr := a.writePwshProfileNodeCA(caCertPath)

	// 5 步判定（严格按此顺序，ErrUserCustomValue 最优先不被 partial 吞掉）
	// 1. 用户自定义值：原样返回，不包装为 partial
	if errors.Is(profileErr, ErrUserCustomValue) {
		return ErrUserCustomValue
	}
	// 2. 都失败：合并错误
	if setxErr != nil && profileErr != nil {
		return fmt.Errorf("setx: %v; profile: %w", setxErr, profileErr)
	}
	// 3. setx 失败 + profile 成功：partial
	if setxErr != nil {
		return fmt.Errorf("%w: setx: %v", ErrPartialSuccess, setxErr)
	}
	// 4. setx 成功 + profile 失败：partial（pwsh 兜底缺失）
	if profileErr != nil {
		if errors.Is(profileErr, ErrPartialSuccess) {
			return profileErr // writePwshProfileNodeCA 已包装为 partial，避免双重包装
		}
		return fmt.Errorf("%w: profile: %w", ErrPartialSuccess, profileErr)
	}
	// 5. 全成功
	return nil
}

// pwshSingleQuote 把字符串编码为 PowerShell 单引号字面量：单引号包裹、内部单引号双写。
// 单引号字符串里 $、反引号、\ 都是字面量，杜绝 $()/反引号注入（P2-1）。CR/LF 被拒绝，
// 防止换行断开字面量后注入新命令。
func pwshSingleQuote(s string) (string, error) {
	if strings.ContainsAny(s, "\r\n") {
		return "", fmt.Errorf("reject newline in pwsh literal: %q", s)
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'", nil
}

// scanPwshProfilesForCustomValue 扫描所有 pwsh profile，返回是否含用户自定义值。
// 高权限运行时 symlink/非常规 profile 视为不安全（fail-closed），返回包装了
// ErrUnsafeProfile 的错误；非特权运行时跟随 symlink 读取（用户自己 home 自己负责）。
// 用于 setx 覆盖前的预检查（F-1）：扫描完全成功前不得调用 setx，避免环境被改而
// profile 未改。
func scanPwshProfilesForCustomValue(home string, privileged bool) (custom bool, err error) {
	for _, profile := range pwshProfileCandidates(home) {
		if privileged {
			if e := isSafeForWrite(profile); e != nil {
				return false, fmt.Errorf("%w: %s", ErrUnsafeProfile, profile)
			}
		}
		existing, err := readProfile(profile) // 非特权跟随 symlink；非 NotExist 错误上抛
		if err != nil {
			return false, err
		}
		if pwshProfileHasNodeCAVarOutsideMCCBlock(string(existing)) {
			return true, nil
		}
	}
	return false, nil
}

// scanPOSIXProfilesForCustomValue 扫描所有 POSIX profile，返回是否含用户自定义值。
// 语义同 scanPwshProfilesForCustomValue：高权限 fail-closed，非特权跟随 symlink。
// 用于 launchctl setenv 覆盖前的预检查（F-1）。
func scanPOSIXProfilesForCustomValue(shell, home string, privileged bool) (custom bool, err error) {
	for _, profile := range resolveShellProfiles(shell, home) {
		if privileged {
			if e := isSafeForWrite(profile); e != nil {
				return false, fmt.Errorf("%w: %s", ErrUnsafeProfile, profile)
			}
		}
		existing, err := readProfile(profile)
		if err != nil {
			return false, err
		}
		if profileHasNodeCAKeyOutsideMCCBlock(shell, string(existing)) {
			return true, nil
		}
	}
	return false, nil
}

// readProfile 读取 profile 内容用于扫描/写入决策。
// 不存在视为空 profile（将创建），返回空内容 + nil。其他读取错误（权限、I/O、
// 路径是目录等）原样返回，调用方必须上抛——否则会在 setx/launchctl 之前误判
// "无用户自定义值"，导致环境被改而 profile 未改（F-1 fail-open）。
func readProfile(profile string) ([]byte, error) {
	b, err := os.ReadFile(profile)
	if err == nil {
		return b, nil
	}
	if !os.IsNotExist(err) {
		// 非 NotExist 错误（权限、是目录、Linux ENOTDIR 等）原样上抛。
		return nil, fmt.Errorf("read profile %s: %w", profile, err)
	}
	// NotExist：必须区分"叶子缺失（可创建）"和"祖先路径无效"。Windows 的
	// ERROR_PATH_NOT_FOUND（祖先是文件/路径断裂）也被 os.IsNotExist 视为 true，
	// 若直接当空 profile 会让 setx/launchctl 先执行，之后 MkdirAll 才失败（F-1）。
	// validateParentChain 校验父链：叶子缺失才视为空，祖先无效则上抛。
	if err := validateParentChain(profile); err != nil {
		return nil, fmt.Errorf("read profile %s: %w", profile, err)
	}
	return nil, nil
}

// validateParentChain 确认 profile 的父目录链可被 MkdirAll 创建，关闭
// Windows ERROR_PATH_NOT_FOUND / Linux 中间组件无效的 F-1 缺口。从直接父目录
// 逐级向上 Stat（跟随 symlink，匹配 MkdirAll 语义），第一个存在的祖先必须是目录；
// 若它是文件，MkdirAll 必失败 → 返回错误。若回溯到仍不存在的文件系统根（Windows
// 未挂载盘符或不存在的 UNC share），同样返回错误，因为 MkdirAll 无法创建该根。
// TOCTOU（校验与 MkdirAll 之间父链被替换）仍为残余风险。
func validateParentChain(profile string) error {
	return validateParentChainWithStat(profile, os.Stat)
}

// validateParentChainWithStat 承载父链遍历；stat 参数让测试可在任意平台确定性模拟
// Windows 缺失卷根/UNC 根，不引入可变的包级测试 hook。
func validateParentChainWithStat(profile string, stat func(string) (os.FileInfo, error)) error {
	dir := filepath.Dir(profile)
	for {
		fi, err := stat(dir)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("parent %s is not a directory", dir)
			}
			return nil // 最近现存祖先是目录，叶子缺失可创建
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat parent %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("filesystem root %s does not exist: %w", dir, err)
		}
		dir = parent
	}
}

// isSafeForWrite 检查路径可安全写入：拒绝符号链接和非常规文件（CWE-59）。
// 不存在视为安全（将创建）。高权限启动 + 用户可控路径（HOME/dataDir）时，符号链接
// 可能指向高权限文件，os.ReadFile/WriteFile 会跟随链接越权读写，故读/写前都需先过此检查。
// 用于 shell profile 和 bootstrap marker（.node-ca-persisted）。
func isSafeForWrite(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 不存在 → 安全，将创建
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse symlink path (CWE-59): %s", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("refuse non-regular path: %s", path)
	}
	return nil
}

func (a *osEnvAdapter) writePwshProfileNodeCA(caCertPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	if !pwshDetected() {
		return nil // pwsh 未安装，跳过
	}
	candidates := pwshProfileCandidates(home)

	// P2-1: CA 路径必须渲染为 PowerShell 单引号字面量，杜绝 $() / 反引号注入。
	// home 内用 Join-Path $env:USERPROFILE 保留可移植性；home 外用绝对路径单引号字面量。
	var caRef string
	sep := string(os.PathSeparator)
	if strings.HasPrefix(caCertPath, home+sep) {
		rel := strings.TrimPrefix(caCertPath, home+sep)
		encoded, err := pwshSingleQuote(rel)
		if err != nil {
			return err
		}
		caRef = "Join-Path $env:USERPROFILE " + encoded
	} else {
		encoded, err := pwshSingleQuote(caCertPath)
		if err != nil {
			return err
		}
		caRef = encoded
	}
	block := fmt.Sprintf("%s\n"+
		"$mccCa = %s\n"+
		"if (Test-Path $mccCa) { $env:NODE_EXTRA_CA_CERTS = $mccCa }\n"+
		"%s\n", pwshProfileMarkerBegin, caRef, pwshProfileMarkerEnd)

	// 1b 策略：高权限严格（symlink fail-closed），非特权跟随 symlink（dotfiles 兼容）
	privileged := isPrivilegedRun()
	// 阶段 1：扫描所有候选，任一有用户自定义值则全部放弃
	for _, profile := range candidates {
		if privileged {
			if e := isSafeForWrite(profile); e != nil {
				return fmt.Errorf("%w: %s", ErrUnsafeProfile, profile)
			}
		}
		existing, err := readProfile(profile)
		if err != nil {
			return err
		}
		if pwshProfileHasNodeCAVarOutsideMCCBlock(string(existing)) {
			return ErrUserCustomValue
		}
	}

	// 阶段 2：全部无自定义，逐个写入
	var lastErr error
	wrote := false
	for _, profile := range candidates {
		if privileged {
			if e := isSafeForWrite(profile); e != nil {
				lastErr = fmt.Errorf("%w: %s", ErrUnsafeProfile, profile)
				continue
			}
		}
		existing, err := readProfile(profile)
		if err != nil {
			lastErr = err
			continue
		}
		updated, changed := replaceMarkedBlock(string(existing), pwshProfileMarkerBegin, pwshProfileMarkerEnd, block)
		if !changed {
			wrote = true
			continue
		}
		if err := os.MkdirAll(filepath.Dir(profile), 0755); err != nil {
			lastErr = err
			continue
		}
		if err := writeFileSync(profile, []byte(updated), 0644); err != nil {
			lastErr = err
			continue
		}
		wrote = true
	}
	if wrote && lastErr != nil {
		return fmt.Errorf("%w: some pwsh profiles failed: %w", ErrPartialSuccess, lastErr)
	}
	if !wrote && lastErr != nil {
		return lastErr
	}
	return nil
}

// pwshNodeCAAssignRe matches PowerShell assignment patterns for NODE_EXTRA_CA_CERTS:
// $env:NODE_EXTRA_CA_CERTS = ..., $NODE_EXTRA_CA_CERTS = ...
// Requires = after optional whitespace to distinguish assignments from reads/checks.
var pwshNodeCAAssignRe = regexp.MustCompile(`(?i)\$(?:env:)?NODE_EXTRA_CA_CERTS\s*=`)

// pwshProfileHasNodeCAVarOutsideMCCBlock 检测 profile 中是否存在非 mcc 管理的
// NODE_EXTRA_CA_CERTS 赋值（用户手写的 $env:NODE_EXTRA_CA_CERTS = ... 行）。
func pwshProfileHasNodeCAVarOutsideMCCBlock(content string) bool {
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, pwshProfileMarkerBegin) {
			inBlock = true
			continue
		}
		if strings.Contains(trimmed, pwshProfileMarkerEnd) {
			inBlock = false
			continue
		}
		if inBlock || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if pwshNodeCAAssignRe.MatchString(trimmed) {
			return true
		}
	}
	return false
}

// replaceMarkedBlock 在 content 里替换 begin..end 标记之间的内容为 newBlock。
// 若标记不存在则追加。changed 表示是否实际改动。
func replaceMarkedBlock(content, begin, end, newBlock string) (string, bool) {
	bi := strings.Index(content, begin)
	ei := strings.Index(content, end)
	if bi >= 0 && ei > bi {
		// 已有标记块：比较内容，相同则不改
		existing := content[bi : ei+len(end)]
		if existing == strings.TrimRight(newBlock, "\n") {
			return content, false
		}
		// 不同（路径变了）：替换
		return content[:bi] + newBlock + content[ei+len(end):], true
	}
	// 无标记块：追加
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + newBlock, true
}

// --- macOS implementation ---

func (a *osEnvAdapter) persistNodeCACertDarwin(caCertPath string) error {
	// F-1 fail-closed: launchctl 前预检查所有 profile（同 Windows scan 语义）。
	shell := os.Getenv("SHELL")
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	custom, scanErr := scanPOSIXProfilesForCustomValue(shell, home, isPrivilegedRun())
	if scanErr != nil {
		return scanErr
	}
	if custom {
		return ErrUserCustomValue
	}
	// ① launchctl setenv 注入当前 GUI 会话
	var launchctlErr error
	if hasLaunchctl() {
		if err := launchctlSetenv("NODE_EXTRA_CA_CERTS", caCertPath); err != nil {
			launchctlErr = err
			log.Printf("[Bootstrap] launchctl setenv failed: %v", launchctlErr)
		}
	}

	// ② profile 持久化
	profileErr := a.writePOSIXProfileNodeCA(caCertPath)

	// 5 步判定（同 Windows 结构）
	if errors.Is(profileErr, ErrUserCustomValue) {
		return ErrUserCustomValue
	}
	if launchctlErr != nil && profileErr != nil {
		return fmt.Errorf("launchctl: %v; profile: %w", launchctlErr, profileErr)
	}
	if launchctlErr != nil {
		return fmt.Errorf("%w: launchctl: %v", ErrPartialSuccess, launchctlErr)
	}
	if profileErr != nil {
		if errors.Is(profileErr, ErrPartialSuccess) {
			return profileErr // writePOSIXProfileNodeCA 已包装为 partial，避免双重包装
		}
		return fmt.Errorf("%w: profile: %w", ErrPartialSuccess, profileErr)
	}
	return nil
}

// --- POSIX (macOS/Linux) shared implementation ---

const (
	posixCABlockBegin = "# >>> mcc: Node.js CA trust >>>"
	posixCABlockEnd   = "# <<< mcc <<<"
)

func (a *osEnvAdapter) persistNodeCACertPOSIX(caCertPath string) error {
	return a.writePOSIXProfileNodeCA(caCertPath)
}

func (a *osEnvAdapter) writePOSIXProfileNodeCA(caCertPath string) error {
	shell := os.Getenv("SHELL")
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}

	exportLine := nodeCAExportLine(shell, caCertPath)
	block := fmt.Sprintf("%s\n%s\n%s\n", posixCABlockBegin, exportLine, posixCABlockEnd)

	profiles := resolveShellProfiles(shell, home)

	// 1b 策略：高权限严格（symlink fail-closed），非特权跟随 symlink（dotfiles 兼容）
	privileged := isPrivilegedRun()
	// 阶段 1：扫描所有候选，任一有用户自定义值则全部放弃
	for _, profile := range profiles {
		if privileged {
			if e := isSafeForWrite(profile); e != nil {
				return fmt.Errorf("%w: %s", ErrUnsafeProfile, profile)
			}
		}
		existing, err := readProfile(profile)
		if err != nil {
			return err
		}
		if profileHasNodeCAKeyOutsideMCCBlock(shell, string(existing)) {
			return ErrUserCustomValue
		}
	}

	// 阶段 2：全部无自定义，逐个写入
	var lastErr error
	for _, profile := range profiles {
		if privileged {
			if e := isSafeForWrite(profile); e != nil {
				lastErr = fmt.Errorf("%w: %s", ErrUnsafeProfile, profile)
				continue
			}
		}
		existing, err := readProfile(profile)
		if err != nil {
			lastErr = err
			continue
		}
		updated, changed := replaceMarkedBlock(string(existing), posixCABlockBegin, posixCABlockEnd, block)
		if !changed {
			return nil // 已含相同块
		}
		if err := os.MkdirAll(filepath.Dir(profile), 0755); err != nil {
			lastErr = err
			continue
		}
		if err := writeFileSync(profile, []byte(updated), 0644); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no profile file writable (tried %v)", profiles)
}

// profileHasNodeCAKeyOutsideMCCBlock 检测 profile 中是否存在非 mcc 管理的
// NODE_EXTRA_CA_CERTS 赋值（用户手写的 export/set 行，不在 mcc 标记块内）。
func profileHasNodeCAKeyOutsideMCCBlock(shell, content string) bool {
	inBlock := false
	isFish := strings.Contains(shell, "fish")
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, posixCABlockBegin) {
			inBlock = true
			continue
		}
		if strings.Contains(trimmed, posixCABlockEnd) {
			inBlock = false
			continue
		}
		if inBlock || trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if isFish {
			parsed, ok := parseFishExportLine(trimmed)
			if ok && parsed.hasExport && parsed.key == "NODE_EXTRA_CA_CERTS" {
				return true
			}
		} else {
			rest := strings.TrimPrefix(trimmed, "export ")
			rest = strings.TrimSpace(rest)
			if strings.HasPrefix(rest, "NODE_EXTRA_CA_CERTS=") {
				return true
			}
		}
	}
	return false
}

func nodeCAExportLine(shell, caCertPath string) string {
	if strings.Contains(shell, "fish") {
		return fmt.Sprintf("set -gx NODE_EXTRA_CA_CERTS %s", shellQuote(caCertPath))
	}
	return fmt.Sprintf("export NODE_EXTRA_CA_CERTS=%s", shellQuote(caCertPath))
}

// writeCloser is the minimal interface writeProfileEntry needs from a profile
// file. *os.File satisfies it; tests inject a fake to simulate Close errors.
type writeCloser interface {
	Write(p []byte) (n int, err error)
	Close() error
}

// writeProfileEntry opens profile via open, appends entry, and checks both the
// Write and Close errors so a failed flush is never silently treated as success.
func writeProfileEntry(open func(string) (writeCloser, error), profile, entry string) error {
	f, err := open(profile)
	if err != nil {
		return fmt.Errorf("open %s: %w", profile, err)
	}
	if _, err := f.Write([]byte(entry)); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", profile, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", profile, err)
	}
	return nil
}

// profileHasExactEntry reports whether content already contains a full line
// that equals entry after both sides are TrimSpace'd. A commented-out line
// (e.g. "# export MCC_ROOT=...") is intentionally NOT treated as a match.
func profileHasExactEntry(content, entry string) bool {
	target := strings.TrimSpace(entry)
	if target == "" {
		return false
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

// profileHasEquivalentEntry reports whether content already contains a
// shell-appropriate line that exports key with the given value, treating
// `export KEY=v`, `export KEY="v"`, `KEY=v`, `KEY="v"`, `KEY='v'` (and the
// fish equivalents `set -x KEY v`, `set -gx KEY v`, `set --export KEY v`) as
// duplicates of each other. Comment lines and blank lines are skipped.
func profileHasEquivalentEntry(shell, content, key, value string) bool {
	if key == "" {
		return false
	}
	isFish := strings.Contains(shell, "fish")
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isFish {
			if fishLineMatches(line, key, value) {
				return true
			}
			continue
		}
		if shLineMatches(line, key, value) {
			return true
		}
	}
	return false
}

// shLineMatches handles POSIX-shell (bash/zsh/unknown) assignments:
//
//	export KEY=value
//	export KEY="value"
//	export KEY='value'
//	KEY=value
//	KEY="value"
func shLineMatches(line, key, value string) bool {
	rest := strings.TrimPrefix(line, "export ")
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, key+"=") {
		return false
	}
	return unquoteValue(strings.TrimPrefix(rest, key+"=")) == value
}

// fishLineMatches handles fish shell exports such as:
//
//	set -x KEY value
//	set -gx KEY value
//	set --export KEY value
//
// A small scanner is used instead of strings.Fields so quoted values, escaped
// characters, and trailing comments can be handled without losing value
// boundaries. Explicit export flags are required; local / erase / unexport
// forms are rejected. Ambiguous fish list syntax is still treated as non-match.
func fishLineMatches(line, key, value string) bool {
	parsed, ok := parseFishExportLine(line)
	if !ok || !parsed.hasExport || parsed.key != key {
		return false
	}
	if parsed.valueTokenCount != 1 {
		return false
	}
	return parsed.value == value
}

type fishToken struct {
	text   string
	quoted bool
}

type fishExportLine struct {
	hasExport       bool
	key             string
	value           string
	valueQuoted     bool
	valueTokenCount int
}

// parseFishExportLine scans the subset of fish syntax that matters for MCC
// profile de-duplication. It preserves quoted values and escape characters,
// strips trailing inline comments, and fails closed on malformed input.
func parseFishExportLine(line string) (fishExportLine, bool) {
	line = stripFishComment(line)
	tokens := scanFishTokens(line)
	if len(tokens) < 4 || tokens[0].text != "set" {
		return fishExportLine{}, false
	}

	out := fishExportLine{}
	idx := 1
	for idx < len(tokens) {
		switch tokens[idx].text {
		case "-x", "-gx", "--export":
			out.hasExport = true
			idx++
		default:
			if strings.HasPrefix(tokens[idx].text, "-") {
				return fishExportLine{}, false
			}
			goto key
		}
	}

key:
	if !out.hasExport || idx >= len(tokens) {
		return fishExportLine{}, false
	}
	out.key = tokens[idx].text
	idx++
	if idx >= len(tokens) {
		return fishExportLine{}, false
	}

	out.valueTokenCount = len(tokens) - idx
	valueTokens := tokens[idx:]
	var b strings.Builder
	for i, tok := range valueTokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(tok.text)
		if tok.quoted {
			out.valueQuoted = true
		}
	}
	out.value = b.String()
	return out, true
}

// scanFishTokens tokenizes a fish line while preserving quoted spans and
// backslash escapes inside the returned token text.
func scanFishTokens(line string) []fishToken {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var tokens []fishToken
	var buf strings.Builder
	inSingle := false
	inDouble := false
	tokenQuoted := false
	tokenStarted := false

	flush := func() {
		if !tokenStarted {
			tokenQuoted = false
			return
		}
		tokens = append(tokens, fishToken{text: buf.String(), quoted: tokenQuoted})
		buf.Reset()
		tokenQuoted = false
		tokenStarted = false
	}

	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inSingle:
			if c == '\\' && i+1 < len(line) && (line[i+1] == '\'' || line[i+1] == '\\') {
				tokenStarted = true
				buf.WriteByte(line[i+1])
				i++
				continue
			}
			if c == '\'' {
				inSingle = false
				continue
			}
			tokenStarted = true
			buf.WriteByte(c)
		case inDouble:
			if c == '\\' && i+1 < len(line) {
				next := line[i+1]
				if next == '\\' || next == '"' || next == '$' {
					tokenStarted = true
					buf.WriteByte(next)
					i++
					tokenQuoted = true
					continue
				}
				tokenStarted = true
				buf.WriteByte(c)
				continue
			}
			if c == '"' {
				inDouble = false
				continue
			}
			tokenStarted = true
			buf.WriteByte(c)
		default:
			if c == ' ' || c == '\t' {
				flush()
				continue
			}
			if c == '\'' {
				inSingle = true
				tokenQuoted = true
				tokenStarted = true
				continue
			}
			if c == '"' {
				inDouble = true
				tokenQuoted = true
				tokenStarted = true
				continue
			}
			if c == '\\' && i+1 < len(line) {
				tokenStarted = true
				i++
				buf.WriteByte(line[i])
				continue
			}
			tokenStarted = true
			buf.WriteByte(c)
		}
	}

	if inSingle || inDouble {
		return nil
	}
	flush()
	return tokens
}

// stripFishComment removes a trailing fish inline comment from line. A `#`
// starts a comment only when it is at the beginning of the line or preceded
// by whitespace, and is not inside a single- or double-quoted span.
//
// Escape handling models fish's three quoting modes:
//   - Single quotes: `\'` and `\\` are escape pairs.
//   - Double quotes: only `\\` and `\"` are escape pairs; other `\x` are
//     preserved verbatim by fish, so we leave them for the normal loop.
//   - Unquoted: `\` escapes (consumes) the next character, so `\#` is not
//     treated as a comment start.
//
// This is still a conservative approximation (no variable expansion, command
// substitution, or line continuation), but it is enough for the simple export
// lines we generate and need to dedup.
func stripFishComment(line string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' {
			if inSingle {
				if i+1 < len(line) && (line[i+1] == '\'' || line[i+1] == '\\') {
					i++
				}
				continue
			}
			if inDouble {
				if i+1 < len(line) && (line[i+1] == '\\' || line[i+1] == '"') {
					i++
				}
				continue
			}
			i++
			continue
		}
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') && !isEscapedWhitespace(line, i-1) {
				return line[:i]
			}
		}
	}
	return line
}

// isEscapedWhitespace reports whether the whitespace byte at idx is escaped by
// an odd-length backslash run immediately before it. This keeps `\ #` and
// similar fish tokens from being misclassified as inline comments.
func isEscapedWhitespace(line string, idx int) bool {
	if idx <= 0 {
		return false
	}
	count := 0
	for j := idx - 1; j >= 0 && line[j] == '\\'; j-- {
		count++
	}
	return count%2 == 1
}

// unquoteValue strips a single layer of surrounding double or single quotes
// and trims surrounding whitespace.
func unquoteValue(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func resolveShellProfile(shell, home string) string {
	return resolveShellProfiles(shell, home)[0]
}

// resolveShellProfiles returns the ordered list of shell profile candidates
// that PersistRoot should try. Known shells keep their single dedicated file;
// for unknown shells we fall back to ~/.profile first, then ~/.bashrc, since
// many login shells source one of these two.
func resolveShellProfiles(shell, home string) []string {
	switch {
	case strings.Contains(shell, "zsh"):
		return []string{home + "/.zshrc"}
	case strings.Contains(shell, "fish"):
		return []string{home + "/.config/fish/config.fish"}
	case strings.Contains(shell, "bash"):
		return []string{home + "/.bashrc"}
	default:
		return []string{home + "/.profile", home + "/.bashrc"}
	}
}

func shellExportEntry(shell, key, value string) string {
	if strings.Contains(shell, "fish") {
		return fmt.Sprintf("\nset -gx %s %s\n", key, shellQuote(value))
	}
	return fmt.Sprintf("\nexport %s=%s\n", key, shellQuote(value))
}

func runHostHelper(helperPath string, args ...string) error {
	if err := validateHostHelperPath(helperPath); err != nil {
		return err
	}
	out, err := execWithTimeout(helperPath, args...)
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", helperPath, args, err, truncateHelperOutput(out))
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validateHostHelperPath(helperPath string) error {
	if !filepath.IsAbs(helperPath) {
		return fmt.Errorf("helper path must be absolute: %q", helperPath)
	}
	info, err := os.Lstat(helperPath)
	if err != nil {
		return fmt.Errorf("stat helper %q: %w", helperPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("helper path must not be a symlink: %q", helperPath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("helper path is not a regular file: %q", helperPath)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("helper path is writable by group or others: %q (mode %04o)", helperPath, info.Mode().Perm())
	}
	return nil
}

const maxHelperOutputBytes = 512

func truncateHelperOutput(out []byte) string {
	if len(out) <= maxHelperOutputBytes {
		return string(out)
	}
	return fmt.Sprintf("%s... (truncated to %d bytes)", string(out[:maxHelperOutputBytes]), maxHelperOutputBytes)
}
