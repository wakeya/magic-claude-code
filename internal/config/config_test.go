package config

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.BackendURL != "https://open.bigmodel.cn/api/anthropic" {
		t.Errorf("expected default backend URL, got %s", cfg.BackendURL)
	}

	if cfg.ProxyPort != 443 {
		t.Errorf("expected proxy port 443, got %d", cfg.ProxyPort)
	}

	if cfg.AdminPort != 8442 {
		t.Errorf("expected admin port 8442, got %d", cfg.AdminPort)
	}

	if cfg.ProxyListenAddr != "0.0.0.0" {
		t.Errorf("expected proxy listen addr 0.0.0.0, got %q", cfg.ProxyListenAddr)
	}

	if cfg.AdminListenAddr != "0.0.0.0" {
		t.Errorf("expected admin listen addr 0.0.0.0, got %q", cfg.AdminListenAddr)
	}
}

func TestNormalizeDefaultsListenAddr(t *testing.T) {
	t.Run("empty listen addr falls back to 0.0.0.0", func(t *testing.T) {
		cfg := &Config{ProxyListenAddr: "", AdminListenAddr: ""}
		cfg.NormalizeDefaults()
		if cfg.ProxyListenAddr != "0.0.0.0" {
			t.Errorf("expected proxy listen addr 0.0.0.0, got %q", cfg.ProxyListenAddr)
		}
		if cfg.AdminListenAddr != "0.0.0.0" {
			t.Errorf("expected admin listen addr 0.0.0.0, got %q", cfg.AdminListenAddr)
		}
	})

	t.Run("whitespace-only listen addr is trimmed and falls back", func(t *testing.T) {
		cfg := &Config{ProxyListenAddr: "  ", AdminListenAddr: "\t\n"}
		cfg.NormalizeDefaults()
		if cfg.ProxyListenAddr != "0.0.0.0" {
			t.Errorf("expected trimmed proxy listen addr 0.0.0.0, got %q", cfg.ProxyListenAddr)
		}
		if cfg.AdminListenAddr != "0.0.0.0" {
			t.Errorf("expected trimmed admin listen addr 0.0.0.0, got %q", cfg.AdminListenAddr)
		}
	})

	t.Run("custom listen addr is preserved and trimmed", func(t *testing.T) {
		cfg := &Config{ProxyListenAddr: "  127.0.0.1  ", AdminListenAddr: " 192.168.1.10 "}
		cfg.NormalizeDefaults()
		if cfg.ProxyListenAddr != "127.0.0.1" {
			t.Errorf("expected proxy listen addr 127.0.0.1, got %q", cfg.ProxyListenAddr)
		}
		if cfg.AdminListenAddr != "192.168.1.10" {
			t.Errorf("expected admin listen addr 192.168.1.10, got %q", cfg.AdminListenAddr)
		}
	})

	t.Run("IPv6 bracket input is stripped", func(t *testing.T) {
		cfg := &Config{ProxyListenAddr: "[::1]", AdminListenAddr: " [2001:db8::1] "}
		cfg.NormalizeDefaults()
		if cfg.ProxyListenAddr != "::1" {
			t.Errorf("expected proxy listen addr ::1, got %q", cfg.ProxyListenAddr)
		}
		if cfg.AdminListenAddr != "2001:db8::1" {
			t.Errorf("expected admin listen addr 2001:db8::1, got %q", cfg.AdminListenAddr)
		}
	})

	t.Run("gateway listen addr bracket stripping and default", func(t *testing.T) {
		cfg := &Config{GatewayListenAddr: "[::1]"}
		cfg.NormalizeDefaults()
		if cfg.GatewayListenAddr != "::1" {
			t.Errorf("expected gateway listen addr ::1, got %q", cfg.GatewayListenAddr)
		}
		cfg2 := &Config{GatewayListenAddr: "  [2001:db8::1]  "}
		cfg2.NormalizeDefaults()
		if cfg2.GatewayListenAddr != "2001:db8::1" {
			t.Errorf("expected gateway listen addr 2001:db8::1, got %q", cfg2.GatewayListenAddr)
		}
		cfg3 := &Config{GatewayListenAddr: ""}
		cfg3.NormalizeDefaults()
		if cfg3.GatewayListenAddr != "127.0.0.1" {
			t.Errorf("expected gateway default 127.0.0.1, got %q", cfg3.GatewayListenAddr)
		}
	})
}

func TestNormalizeDefaultsListenPortRange(t *testing.T) {
	t.Run("proxy port out of range falls back to 443", func(t *testing.T) {
		for _, port := range []int{0, -1, 99999, 65536} {
			cfg := &Config{ProxyPort: port}
			cfg.NormalizeDefaults()
			if cfg.ProxyPort != 443 {
				t.Errorf("port %d: expected fallback 443, got %d", port, cfg.ProxyPort)
			}
		}
	})

	t.Run("admin port out of range falls back to 8442", func(t *testing.T) {
		for _, port := range []int{0, -1, 99999, 65536} {
			cfg := &Config{AdminPort: port}
			cfg.NormalizeDefaults()
			if cfg.AdminPort != 8442 {
				t.Errorf("port %d: expected fallback 8442, got %d", port, cfg.AdminPort)
			}
		}
	})

	t.Run("valid custom ports are preserved", func(t *testing.T) {
		cfg := &Config{ProxyPort: 8443, AdminPort: 9000}
		cfg.NormalizeDefaults()
		if cfg.ProxyPort != 8443 {
			t.Errorf("expected proxy port 8443 preserved, got %d", cfg.ProxyPort)
		}
		if cfg.AdminPort != 9000 {
			t.Errorf("expected admin port 9000 preserved, got %d", cfg.AdminPort)
		}
	})

	t.Run("boundary ports 1 and 65535 are accepted", func(t *testing.T) {
		cfg := &Config{ProxyPort: 1, AdminPort: 65535}
		cfg.NormalizeDefaults()
		if cfg.ProxyPort != 1 {
			t.Errorf("expected proxy port 1 preserved, got %d", cfg.ProxyPort)
		}
		if cfg.AdminPort != 65535 {
			t.Errorf("expected admin port 65535 preserved, got %d", cfg.AdminPort)
		}
	})
}


func TestNormalizeThemeMode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults light", in: "", want: ThemeModeLight},
		{name: "light accepted", in: "light", want: ThemeModeLight},
		{name: "dark accepted", in: "dark", want: ThemeModeDark},
		{name: "invalid defaults light", in: "system", want: ThemeModeLight},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeThemeMode(tt.in); got != tt.want {
				t.Fatalf("NormalizeThemeMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDefaultConfigThemeMode(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.AdminThemeMode != ThemeModeLight {
		t.Fatalf("AdminThemeMode = %q, want %q", cfg.AdminThemeMode, ThemeModeLight)
	}
	if cfg.ConnectionMode != ConnectionModeTransparent {
		t.Fatalf("ConnectionMode = %q, want %q", cfg.ConnectionMode, ConnectionModeTransparent)
	}
}

func TestNormalizeConnectionMode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults transparent", in: "", want: ConnectionModeTransparent},
		{name: "transparent accepted", in: ConnectionModeTransparent, want: ConnectionModeTransparent},
		{name: "tunnel accepted", in: ConnectionModeTunnel, want: ConnectionModeTunnel},
		{name: "gateway accepted", in: ConnectionModeGateway, want: ConnectionModeGateway},
		{name: "invalid defaults transparent", in: "system", want: ConnectionModeTransparent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeConnectionMode(tt.in); got != tt.want {
				t.Fatalf("NormalizeConnectionMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				BackendURL: "https://example.com/api",
			},
			wantErr: false,
		},
		{
			name: "empty backend URL",
			config: Config{
				BackendURL: "",
			},
			wantErr: true,
		},
		{
			name: "invalid URL",
			config: Config{
				BackendURL: "not-a-url",
			},
			wantErr: true,
		},
		{
			name: "backend_url with userinfo rejected",
			config: Config{
				BackendURL: "https://user:pass@example.com/api",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
