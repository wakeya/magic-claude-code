package i18n

import (
	"os"
	"testing"
)

func TestResolveLocale(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		expected string
	}{
		{
			name:     "default when no env",
			env:      map[string]string{},
			expected: "en",
		},
		{
			name: "MCC_LANG zh overrides LANG",
			env: map[string]string{
				"MCC_LANG": "zh",
				"LANG":     "en_US.UTF-8",
			},
			expected: "zh",
		},
		{
			name: "MCC_LANG en overrides LANG",
			env: map[string]string{
				"MCC_LANG": "en",
				"LANG":     "zh_CN.UTF-8",
			},
			expected: "en",
		},
		{
			name: "LANG zh_CN",
			env: map[string]string{
				"LANG": "zh_CN.UTF-8",
			},
			expected: "zh",
		},
		{
			name: "LANG zh",
			env: map[string]string{
				"LANG": "zh",
			},
			expected: "zh",
		},
		{
			name: "LANG en_US",
			env: map[string]string{
				"LANG": "en_US.UTF-8",
			},
			expected: "en",
		},
		{
			name: "LC_ALL zh when LANG not set",
			env: map[string]string{
				"LC_ALL": "zh_TW.UTF-8",
			},
			expected: "zh",
		},
		{
			name: "LANG C falls back to en",
			env: map[string]string{
				"LANG": "C",
			},
			expected: "en",
		},
		{
			name: "LANG POSIX falls back to en",
			env: map[string]string{
				"LANG": "POSIX",
			},
			expected: "en",
		},
		{
			name: "unknown locale falls back to en",
			env: map[string]string{
				"LANG": "ja_JP.UTF-8",
			},
			expected: "en",
		},
		{
			name: "MCC_LANG unknown falls back to en",
			env: map[string]string{
				"MCC_LANG": "fr",
			},
			expected: "en",
		},
		{
			name: "LC_ALL zh overrides LANG=C",
			env: map[string]string{
				"LANG":   "C",
				"LC_ALL": "zh_CN.UTF-8",
			},
			expected: "zh",
		},
		{
			name: "LC_ALL en overrides LANG zh",
			env: map[string]string{
				"LANG":   "zh_CN.UTF-8",
				"LC_ALL": "en_US.UTF-8",
			},
			expected: "en",
		},
		{
			name: "LC_ALL=C returns en (explicit, not skipped)",
			env: map[string]string{
				"LC_ALL": "C",
			},
			expected: "en",
		},
		{
			name: "LC_ALL=POSIX returns en (explicit, not skipped)",
			env: map[string]string{
				"LC_ALL": "POSIX",
			},
			expected: "en",
		},
		{
			name: "LC_MESSAGES=C.UTF-8 returns en",
			env: map[string]string{
				"LC_MESSAGES": "C.UTF-8",
			},
			expected: "en",
		},
		{
			name: "LANG=C returns en (explicit, not skipped)",
			env: map[string]string{
				"LANG": "C",
			},
			expected: "en",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := []string{"MCC_LANG", "LC_ALL", "LC_MESSAGES", "LANG"}
			original := make(map[string]string, len(keys))
			for _, key := range keys {
				if v, ok := os.LookupEnv(key); ok {
					original[key] = v
				}
				os.Unsetenv(key)
			}
			defer func() {
				for _, key := range keys {
					if v, ok := original[key]; ok {
						os.Setenv(key, v)
					} else {
						os.Unsetenv(key)
					}
				}
			}()

			for k, v := range tt.env {
				os.Setenv(k, v)
			}

			origSys := systemLocaleFn
			systemLocaleFn = func() string { return "" }
			defer func() { systemLocaleFn = origSys }()

			got := ResolveLocale()
			if got != tt.expected {
				t.Errorf("ResolveLocale() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestResolveLocale_SystemLocaleFallback(t *testing.T) {
	tests := []struct {
		name      string
		sysLocale string
		expected  string
	}{
		{name: "system zh → zh", sysLocale: "zh", expected: "zh"},
		{name: "system zh-CN → zh", sysLocale: "zh", expected: "zh"},
		{name: "system en → en", sysLocale: "en", expected: "en"},
		{name: "system empty → en default", sysLocale: "", expected: "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := []string{"MCC_LANG", "LC_ALL", "LC_MESSAGES", "LANG"}
			original := make(map[string]string, len(keys))
			for _, key := range keys {
				if v, ok := os.LookupEnv(key); ok {
					original[key] = v
				}
				os.Unsetenv(key)
			}
			defer func() {
				for _, key := range keys {
					if v, ok := original[key]; ok {
						os.Setenv(key, v)
					} else {
						os.Unsetenv(key)
					}
				}
			}()

			origSys := systemLocaleFn
			systemLocaleFn = func() string { return tt.sysLocale }
			defer func() { systemLocaleFn = origSys }()

			got := ResolveLocale()
			if got != tt.expected {
				t.Errorf("ResolveLocale() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestResolveLocale_SystemLocaleOverriddenByEnv(t *testing.T) {
	keys := []string{"MCC_LANG", "LANG", "LC_ALL"}
	original := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := os.LookupEnv(key); ok {
			original[key] = v
		}
		os.Unsetenv(key)
	}
	defer func() {
		for _, key := range keys {
			if v, ok := original[key]; ok {
				os.Setenv(key, v)
			} else {
				os.Unsetenv(key)
			}
		}
	}()

	origSys := systemLocaleFn
	systemLocaleFn = func() string { return "zh" }
	defer func() { systemLocaleFn = origSys }()

	os.Setenv("LANG", "en_US.UTF-8")
	got := ResolveLocale()
	if got != "en" {
		t.Errorf("env LANG=en should override system zh, got %q", got)
	}
}

func TestLoad(t *testing.T) {
	t.Run("zh returns Chinese messages", func(t *testing.T) {
		msg := Load("zh_CN")
		if msg.BannerTitle != "Claude Code 透明代理已启动" {
			t.Errorf("expected Chinese banner title, got %q", msg.BannerTitle)
		}
		if msg.ProxyPort != "代理端口: %d" {
			t.Errorf("expected Chinese proxy port label, got %q", msg.ProxyPort)
		}
	})

	t.Run("en returns English messages", func(t *testing.T) {
		msg := Load("en_US")
		if msg.BannerTitle != "Claude Code Transparent Proxy Started" {
			t.Errorf("expected English banner title, got %q", msg.BannerTitle)
		}
		if msg.ProxyPort != "Proxy port: %d" {
			t.Errorf("expected English proxy port label, got %q", msg.ProxyPort)
		}
	})

	t.Run("unknown falls back to English", func(t *testing.T) {
		msg := Load("ja")
		if msg.BannerTitle != "Claude Code Transparent Proxy Started" {
			t.Errorf("expected English fallback, got %q", msg.BannerTitle)
		}
	})

	t.Run("empty falls back to English", func(t *testing.T) {
		msg := Load("")
		if msg.BannerTitle != "Claude Code Transparent Proxy Started" {
			t.Errorf("expected English fallback, got %q", msg.BannerTitle)
		}
	})
}
