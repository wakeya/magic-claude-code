package i18n

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// systemLocale detects the OS-level locale when shell environment variables
// (LANG, LC_ALL, etc.) are absent. Returns "" if detection fails.
func systemLocale() string {
	switch runtime.GOOS {
	case "darwin":
		return readCommandLocale("defaults", "read", ".GlobalPreferences", "AppleLocale")
	case "windows":
		return readWindowsLocale()
	default:
		return readLinuxLocale()
	}
}

func readLinuxLocale() string {
	for _, path := range []string{"/etc/default/locale", "/etc/locale.conf"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			for _, key := range []string{"LANG", "LC_ALL", "LC_MESSAGES"} {
				prefix := key + "="
				if strings.HasPrefix(line, prefix) {
					val := strings.Trim(line[len(prefix):], "\"'")
					if val != "" && val != "C" && val != "POSIX" {
						return normalize(val)
					}
				}
			}
		}
	}
	return ""
}

func readWindowsLocale() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "reg", "query",
		`HKCU\Control Panel\International`, "/v", "LocaleName").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "REG_SZ"); idx >= 0 {
			val := strings.TrimSpace(line[idx+len("REG_SZ"):])
			if val != "" {
				return normalize(val)
			}
		}
	}
	return ""
}

func readCommandLocale(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	val := strings.TrimSpace(string(out))
	if val != "" {
		return normalize(val)
	}
	return ""
}
