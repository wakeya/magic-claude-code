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
