package bootstrap

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
)

// generateInstructions produces localized fallback instructions based on the result.
func generateInstructions(r Result, locale string) []string {
	switch r.SelectedMode {
	case ModeTransparent:
		return transparentSuccessInstructions(r, locale)
	case ModeTunnel:
		return tunnelInstructions(r, locale)
	default:
		return gatewayInstructions(r, locale)
	}
}

func transparentSuccessInstructions(r Result, locale string) []string {
	if r.EnvResult.Attempted && !r.EnvResult.Success {
		return transparentEnvFailureInstructions(r, locale)
	}
	if locale == "zh" {
		return []string{
			"✓ 透明模式已就绪。",
			"  - hosts 已配置",
			"  - CA 已信任",
			"  - 客户端可继续使用默认 Claude Code 配置，或在 ~/.claude/settings.json 中显式声明官方端点",
			"    {",
			"      \"env\": {",
			"        \"ANTHROPIC_BASE_URL\": \"https://api.anthropic.com\"",
			"      }",
			"    }",
		}
	}
	return []string{
		"✓ Transparent mode is ready.",
		"  - hosts configured",
		"  - CA trusted",
		"  - The client can keep the default Claude Code config, or explicitly set the official endpoint in ~/.claude/settings.json",
		"    {",
		"      \"env\": {",
		"        \"ANTHROPIC_BASE_URL\": \"https://api.anthropic.com\"",
		"      }",
		"    }",
	}
}

func transparentEnvFailureInstructions(r Result, locale string) []string {
	rootDir := r.ExecRootDir
	if rootDir == "" {
		rootDir = "./"
	}
	if locale == "zh" {
		lines := []string{
			"⚠ 透明模式已就绪，但环境持久化失败。",
		}
		lines = append(lines, stepFailuresZh(r)...)
		lines = append(lines,
			"",
			"请手动将 MCC_ROOT 指向 mcc 可执行文件所在目录，然后重新打开终端或重新运行 mcc:",
		)
		if runtime.GOOS == "windows" {
			lines = append(lines, "  setx MCC_ROOT "+windowsQuote(rootDir))
		} else {
			lines = append(lines, "  export MCC_ROOT="+shellQuote(rootDir))
		}
		lines = append(lines,
			"",
			"如果你仍然无法持久化环境变量，可以改用隧道模式或路由模式。",
		)
		return lines
	}
	lines := []string{
		"⚠ Transparent mode is ready, but environment persistence failed.",
	}
	lines = append(lines, stepFailuresEn(r)...)
	lines = append(lines,
		"",
		"Manually point MCC_ROOT to the mcc executable directory, then reopen your terminal or run mcc again:",
	)
	if runtime.GOOS == "windows" {
		lines = append(lines, "  setx MCC_ROOT "+windowsQuote(rootDir))
	} else {
		lines = append(lines, "  export MCC_ROOT="+shellQuote(rootDir))
	}
	lines = append(lines,
		"",
		"If you still cannot persist the environment, switch to Tunnel Mode or Route Mode.",
	)
	return lines
}

func tunnelInstructions(r Result, locale string) []string {
	caPath := r.CACertPath
	if locale == "zh" {
		lines := []string{
			"⚠ 透明模式未完成，推荐使用隧道模式。",
		}
		lines = append(lines, stepFailuresZh(r)...)
		lines = append(lines,
			"",
			"隧道模式启动步骤:",
		)
		if runtime.GOOS == "windows" {
			lines = append(lines,
				windowsSet("HTTPS_PROXY", "https://127.0.0.1:443"),
				windowsSet("NODE_EXTRA_CA_CERTS", caPath),
			)
		} else {
			lines = append(lines,
				"  export HTTPS_PROXY=https://127.0.0.1:443",
				"  export NODE_EXTRA_CA_CERTS="+shellQuote(caPath),
			)
		}
		lines = append(lines,
			"",
			"客户端 ~/.claude/settings.json 示例:",
			"  {",
			"    \"env\": {",
			"      \"HTTPS_PROXY\": \"https://127.0.0.1:443\",",
			"      \"NODE_EXTRA_CA_CERTS\": "+fmt.Sprintf("%q", caPath),
			"    }",
			"  }",
			"",
			"保存后重启 Claude Code。",
			"",
			"如隧道模式也不可用，可降级到路由模式:",
		)
		return lines
	}
	lines := []string{
		"⚠ Transparent mode incomplete; Tunnel Mode is recommended.",
	}
	lines = append(lines, stepFailuresEn(r)...)
	lines = append(lines,
		"",
		"Tunnel Mode setup:",
	)
	if runtime.GOOS == "windows" {
		lines = append(lines,
			windowsSet("HTTPS_PROXY", "https://127.0.0.1:443"),
			windowsSet("NODE_EXTRA_CA_CERTS", caPath),
		)
	} else {
		lines = append(lines,
			"  export HTTPS_PROXY=https://127.0.0.1:443",
			"  export NODE_EXTRA_CA_CERTS="+shellQuote(caPath),
		)
	}
	lines = append(lines,
		"",
		"Client ~/.claude/settings.json example:",
		"  {",
		"    \"env\": {",
		"      \"HTTPS_PROXY\": \"https://127.0.0.1:443\",",
		"      \"NODE_EXTRA_CA_CERTS\": "+fmt.Sprintf("%q", caPath),
		"    }",
		"  }",
		"",
		"Restart Claude Code after saving.",
		"",
		"If Tunnel Mode is also unavailable, fall back to Route Mode:",
	)
	return lines
}

func gatewayInstructions(r Result, locale string) []string {
	addr := r.GatewayListenAddr
	if addr == "" {
		addr = "127.0.0.1"
	}
	port := r.GatewayListenPort
	if port == 0 {
		port = 17487
	}
	baseURL := "http://" + net.JoinHostPort(addr, strconv.Itoa(port))
	if locale == "zh" {
		lines := []string{
			"⚠ 透明模式和隧道模式均不可用，已降级到路由模式。",
		}
		lines = append(lines, stepFailuresZh(r)...)
		lines = append(lines, "", "路由模式启动步骤:")
		if runtime.GOOS == "windows" {
			lines = append(lines, windowsSet("ANTHROPIC_BASE_URL", baseURL))
		} else {
			lines = append(lines, "  export ANTHROPIC_BASE_URL="+shellQuote(baseURL))
		}
		lines = append(lines,
			"",
			"注意: 路由模式无法拦截硬编码 api.anthropic.com 流量。",
			"仅覆盖尊重 ANTHROPIC_BASE_URL 的客户端。",
			"",
			"客户端 ~/.claude/settings.json 示例:",
			"  {",
			"    \"env\": {",
			"      \"ANTHROPIC_BASE_URL\": \""+baseURL+"\"",
			"    }",
			"  }",
			"",
			"保存后重启 Claude Code。",
		)
		return lines
	}
	lines := []string{
		"⚠ Transparent and Tunnel modes unavailable; falling back to Route Mode.",
	}
	lines = append(lines, stepFailuresEn(r)...)
	lines = append(lines, "", "Route Mode setup:")
	if runtime.GOOS == "windows" {
		lines = append(lines, windowsSet("ANTHROPIC_BASE_URL", baseURL))
	} else {
		lines = append(lines, "  export ANTHROPIC_BASE_URL="+shellQuote(baseURL))
	}
	lines = append(lines,
		"",
		"Note: Route Mode cannot intercept hardcoded api.anthropic.com traffic.",
		"It only covers clients that honor ANTHROPIC_BASE_URL.",
		"",
		"Client ~/.claude/settings.json example:",
		"  {",
		"    \"env\": {",
		"      \"ANTHROPIC_BASE_URL\": \""+baseURL+"\"",
		"    }",
		"  }",
		"",
		"Restart Claude Code after saving.",
	)
	return lines
}

func stepFailuresZh(r Result) []string {
	var lines []string
	if r.HostsResult.Attempted && !r.HostsResult.Success {
		lines = append(lines, fmt.Sprintf("  失败: hosts 修改 (%v)", r.HostsResult.Err))
	}
	if r.TrustResult.Attempted && !r.TrustResult.Success {
		lines = append(lines, fmt.Sprintf("  失败: CA 信任安装 (%v)", r.TrustResult.Err))
	}
	if r.EnvResult.Attempted && !r.EnvResult.Success {
		lines = append(lines, fmt.Sprintf("  失败: 环境持久化 (%v)", r.EnvResult.Err))
	}
	if r.Caps.IsDocker && !r.Caps.HasHostHelper {
		lines = append(lines, "  限制: Docker 容器无法修改宿主机（未检测到 helper）")
	}
	if r.CACertPath != "" {
		lines = append(lines, fmt.Sprintf("  CA 证书路径: %s", r.CACertPath))
	}
	return lines
}

func stepFailuresEn(r Result) []string {
	var lines []string
	if r.HostsResult.Attempted && !r.HostsResult.Success {
		lines = append(lines, fmt.Sprintf("  Failed: hosts modification (%v)", r.HostsResult.Err))
	}
	if r.TrustResult.Attempted && !r.TrustResult.Success {
		lines = append(lines, fmt.Sprintf("  Failed: CA trust installation (%v)", r.TrustResult.Err))
	}
	if r.EnvResult.Attempted && !r.EnvResult.Success {
		lines = append(lines, fmt.Sprintf("  Failed: environment persistence (%v)", r.EnvResult.Err))
	}
	if r.Caps.IsDocker && !r.Caps.HasHostHelper {
		lines = append(lines, "  Limitation: Docker cannot modify host (no helper detected)")
	}
	if r.CACertPath != "" {
		lines = append(lines, fmt.Sprintf("  CA cert path: %s", r.CACertPath))
	}
	return lines
}

func windowsQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func windowsSet(key, value string) string {
	return `  set "` + key + "=" + strings.ReplaceAll(value, `"`, `""`) + `"`
}
