// Package i18n provides lightweight runtime locale-based message selection
// for user-facing CLI output. No external dependencies — pure standard library.
package i18n

import (
	"os"
	"strings"
)

// Messages holds all user-facing strings that need translation.
type Messages struct {
	// CLI flags
	FlagDataDir      string
	FlagPassword     string
	FlagProxyListen  string
	FlagProxyPort    string
	FlagAdminListen  string
	FlagAdminPort    string

	// Banner
	BannerTop    string
	BannerTitle  string
	BannerBottom string

	// Service info
	ProxyPort      string
	AdminPort      string
	BackendURL     string
	BackendURLNote string

	// Config instructions
	ConfigInstructions string
	HostsCommandUnix   string
	HostsCommandWin    string
	CACertCommandUnix  string
	CACertCommandWin   string
	SourceCommandUnix  string
	SourceCommandWin   string
	RestartHintWin     string

	// Admin panel URLs
	AdminPage    string
	AdminPageURL string // "  https://localhost:%d\n  https://api.anthropic.com:%d"

	// Password
	RandomPassword   string
	PasswordSaveHint string
	PasswordEnvHint  string

	// Docker / update
	DockerUpdateDisabled string
	UpdateDisabledReason string

	// Shutdown / restart
	ShuttingDown            string
	UpdateAppliedRestarting string
	RestartingService       string
	AutoRestartNoExecutable string
	AutoRestartUnsupported  string
	RestartManually         string
	ServerStopped           string

	// Warnings
	WarnRandomFallback string
	WarnNoPassword     string

	// Bootstrap
	BootstrapAttempting    string
	BootstrapTransparentOK string
	BootstrapManualHint    string
	BootstrapReasonDockerTunnel string
	BootstrapReasonHostsFailure  string
	BootstrapReasonTrustFailure   string
	BootstrapReasonGateway        string
	BootstrapReasonTunnelGeneric  string
	ModeTransparent        string
	ModeTunnel             string
	ModeGateway            string
}

var enMessages = Messages{
	FlagDataDir:      "Data directory",
	FlagPassword:     "Admin password",
	FlagProxyListen:  "Proxy listen address (default 0.0.0.0)",
	FlagProxyPort:    "Proxy listen port (default 443)",
	FlagAdminListen:  "Admin listen address (default 0.0.0.0)",
	FlagAdminPort:    "Admin listen port (default 8442)",

	BannerTop:    "========================================",
	BannerTitle:  "Claude Code Transparent Proxy Started",
	BannerBottom: "========================================",

	ProxyPort:      "Proxy port: %d",
	AdminPort:      "Admin port: %d",
	BackendURL:     "Backend URL: %s",
	BackendURLNote: " (configurable to use other Anthropic or OpenAI Chat compatible endpoints)",

	ConfigInstructions: "Please run the following configuration:",
	HostsCommandUnix:   "1. echo '127.0.0.1 api.anthropic.com' | sudo tee -a /etc/hosts",
	HostsCommandWin:    "1. Run as administrator in PowerShell:\n   Add-Content -Path \"$env:WINDIR\\System32\\drivers\\etc\\hosts\" -Value \"127.0.0.1 api.anthropic.com\"",
	CACertCommandUnix:  "2. echo 'NODE_EXTRA_CA_CERTS=%s' >> ~/.bashrc",
	CACertCommandWin:   "2. Set Node.js CA certificate environment variable:\n   [Environment]::SetEnvironmentVariable(\"NODE_EXTRA_CA_CERTS\", \"%s\", \"User\")",
	SourceCommandUnix:  "3. source ~/.bashrc",
	SourceCommandWin:   "3. Close and reopen your terminal",
	RestartHintWin:     "\nNote: Administrator privileges are required to modify the hosts file on Windows.",

	AdminPage:    "Admin panel (both URLs point to the same service):",
	AdminPageURL: "  https://localhost:%d\n  https://api.anthropic.com:%d",

	RandomPassword:   "Randomly generated admin password: %s",
	PasswordSaveHint: "Please save this password; it will be regenerated on next startup if not specified.",
	PasswordEnvHint:  "Password: see environment variable ADMIN_PASSWORD or startup flag --password",

	DockerUpdateDisabled: "Running in Docker container. In-app self-update is disabled; version checks still work. Please update via image pull.",
	UpdateDisabledReason: "In-app self-update is not supported in Docker. Please update by pulling a new image and recreating the container.",

	ShuttingDown:            "Shutting down...",
	UpdateAppliedRestarting: "Update applied, restarting service...",
	RestartingService:       "Restarting service...",
	AutoRestartNoExecutable: "auto-restart: cannot find executable: %v",
	AutoRestartUnsupported:  "auto-restart not supported on this platform: %v",
	RestartManually:         "Please restart the service manually to apply the update.",
	ServerStopped:           "Server stopped",

	WarnRandomFallback: "Warning: random number generation failed, using fallback",
	WarnNoPassword:     "Warning: no password set, using randomly generated password",

	BootstrapAttempting:    "Attempting automatic transparent mode setup...",
	BootstrapTransparentOK: "Transparent mode ready: hosts configured, CA trusted.",
	BootstrapManualHint:    "If automatic setup did not complete, see the mode help and fallback instructions above.",
	BootstrapReasonDockerTunnel: "Docker container cannot modify host; Tunnel Mode is the best available fallback",
	BootstrapReasonHostsFailure:  "hosts modification failed (%s); falling back to Tunnel Mode",
	BootstrapReasonTrustFailure:  "CA trust installation failed (%s); Tunnel Mode still usable with runtime CA trust",
	BootstrapReasonGateway:       "neither hosts nor CA trust available; Route Mode is the only remaining option",
	BootstrapReasonTunnelGeneric: "Transparent Mode is incomplete; Tunnel Mode is the next available fallback",
	ModeTransparent:        "Transparent Mode",
	ModeTunnel:             "Tunnel Mode",
	ModeGateway:            "Route Mode",
}

var zhMessages = Messages{
	FlagDataDir:      "数据目录",
	FlagPassword:     "管理密码",
	FlagProxyListen:  "代理监听地址（默认 0.0.0.0）",
	FlagProxyPort:    "代理监听端口（默认 443）",
	FlagAdminListen:  "配置监听地址（默认 0.0.0.0）",
	FlagAdminPort:    "配置监听端口（默认 8442）",

	BannerTop:    "========================================",
	BannerTitle:  "Claude Code 透明代理已启动",
	BannerBottom: "========================================",

	ProxyPort:      "代理端口: %d",
	AdminPort:      "配置端口: %d",
	BackendURL:     "后端地址: %s",
	BackendURLNote: "（可配置其他兼容 Anthropic 或 OpenAI Chat 的接口地址）",

	ConfigInstructions: "请执行以下配置:",
	HostsCommandUnix:   "1. echo '127.0.0.1 api.anthropic.com' | sudo tee -a /etc/hosts",
	HostsCommandWin:    "1. 以管理员身份运行 PowerShell:\n   Add-Content -Path \"$env:WINDIR\\System32\\drivers\\etc\\hosts\" -Value \"127.0.0.1 api.anthropic.com\"",
	CACertCommandUnix:  "2. echo 'NODE_EXTRA_CA_CERTS=%s' >> ~/.bashrc",
	CACertCommandWin:   "2. 设置 Node.js CA 证书环境变量:\n   [Environment]::SetEnvironmentVariable(\"NODE_EXTRA_CA_CERTS\", \"%s\", \"User\")",
	SourceCommandUnix:  "3. source ~/.bashrc",
	SourceCommandWin:   "3. 关闭并重新打开终端",
	RestartHintWin:     "\n注意: Windows 修改 hosts 文件需要管理员权限。",

	AdminPage:    "配置页面（以下两个地址等价）:",
	AdminPageURL: "  https://localhost:%d\n  https://api.anthropic.com:%d",

	RandomPassword:   "随机生成的管理密码: %s",
	PasswordSaveHint: "请保存此密码；下次未指定密码启动时会重新生成。",
	PasswordEnvHint:  "密码: 请查看环境变量 ADMIN_PASSWORD 或启动参数 --password",

	DockerUpdateDisabled: "运行在 Docker 容器中，应用内自更新已禁用；仍会检查新版本，请通过镜像更新。",
	UpdateDisabledReason: "Docker 环境不支持应用内自更新，请通过更新镜像并重新创建容器完成升级。",

	ShuttingDown:            "正在关闭...",
	UpdateAppliedRestarting: "更新已应用，正在重启服务...",
	RestartingService:       "正在重启服务...",
	AutoRestartNoExecutable: "自动重启: 找不到可执行文件: %v",
	AutoRestartUnsupported:  "当前平台不支持自动重启: %v",
	RestartManually:         "请手动重启服务以应用更新。",
	ServerStopped:           "服务已停止",

	WarnRandomFallback: "警告: 随机数生成失败，使用后备方案",
	WarnNoPassword:     "警告: 未设置密码，使用随机生成的密码",

	BootstrapAttempting:    "正在尝试自动透明模式配置...",
	BootstrapTransparentOK: "透明模式已就绪: hosts 已配置，CA 已信任。",
	BootstrapManualHint:    "如果自动配置未完成，请参考上方的模式说明和降级指引。",
	BootstrapReasonDockerTunnel: "Docker 容器无法修改宿主机；隧道模式是当前最佳后备",
	BootstrapReasonHostsFailure:  "hosts 修改失败（%s）；已降级到隧道模式",
	BootstrapReasonTrustFailure:  "CA 信任安装失败（%s）；隧道模式仍可在运行时信任 CA",
	BootstrapReasonGateway:       "hosts 和 CA 信任都不可用；路由模式是唯一剩余选项",
	BootstrapReasonTunnelGeneric: "透明模式未完成；隧道模式是下一可用后备",
	ModeTransparent:        "透明模式",
	ModeTunnel:             "隧道模式",
	ModeGateway:            "路由模式",
}

// ResolveLocale determines the effective locale.
// Priority: MCC_LANG > LC_ALL > LC_MESSAGES > LANG > system locale > default (en).
var systemLocaleFn = systemLocale

func ResolveLocale() string {
	for _, key := range []string{"MCC_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" {
			return normalize(v)
		}
	}
	if sys := systemLocaleFn(); sys != "" {
		return sys
	}
	return "en"
}

func normalize(v string) string {
	v = strings.ToLower(v)
	if strings.HasPrefix(v, "zh") {
		return "zh"
	}
	return "en"
}

// Load returns the Messages for the given locale.
// Supports "zh" for Chinese; anything else falls back to English.
func Load(locale string) Messages {
	if normalize(locale) == "zh" {
		return zhMessages
	}
	return enMessages
}
