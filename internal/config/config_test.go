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

func TestResolveModel_HitExposedModel(t *testing.T) {
	cfg := &Config{
		Providers: []Provider{
			{ID: "a", Name: "A", Enabled: true, ExposedModels: []ExposedModel{
				{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"},
			}},
		},
	}
	p, backend := cfg.ResolveModel("glm-4.6")
	if p == nil || p.ID != "a" || backend != "glm-4.6" {
		t.Fatalf("expected provider a + glm-4.6, got %v %q", p, backend)
	}
}

func TestResolveModel_BackendModelEmptyFallsBackToID(t *testing.T) {
	cfg := &Config{Providers: []Provider{
		{ID: "a", Name: "A", Enabled: true, ExposedModels: []ExposedModel{
			{ID: "kimi-k2", Label: "Kimi K2"}, // BackendModel 空
		}},
	}}
	p, backend := cfg.ResolveModel("kimi-k2")
	if backend != "kimi-k2" {
		t.Fatalf("expected backend=kimi-k2, got %q", backend)
	}
	if p == nil || p.ID != "a" {
		t.Fatalf("expected provider a, got %v", p)
	}
}

func TestResolveModel_FallbackToActive(t *testing.T) {
	cfg := &Config{
		ActiveProviderID: "a",
		Providers: []Provider{
			{ID: "a", Name: "A", Enabled: true, ModelMappings: map[string]string{
				"claude-opus-4-8": "glm-5.2",
			}},
		},
	}
	p, backend := cfg.ResolveModel("claude-opus-4-8")
	if p == nil || p.ID != "a" {
		t.Fatalf("expected active provider a, got %v", p)
	}
	if backend != "glm-5.2" {
		t.Fatalf("expected mapped glm-5.2, got %q", backend)
	}
}

func TestResolveModel_SkipsDisabledProvider(t *testing.T) {
	cfg := &Config{Providers: []Provider{
		{ID: "disabled", Name: "Disabled", Enabled: false, ExposedModels: []ExposedModel{
			{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"},
		}},
		{ID: "active", Name: "Active", Enabled: true},
	}}
	cfg.ActiveProviderID = "active"
	// 命中项在 disabled provider，应跳过并 fallback
	p, backend := cfg.ResolveModel("glm-4.6")
	if p == nil || p.ID != "active" {
		t.Fatalf("expected fallback to active, got %v", p)
	}
	// fallback 走 MapModel，无映射则原样
	if backend != "glm-4.6" {
		t.Fatalf("expected original model glm-4.6, got %q", backend)
	}
}

func TestResolveModel_NoActiveReturnsNil(t *testing.T) {
	cfg := &Config{} // 无 provider
	p, backend := cfg.ResolveModel("anything")
	if p != nil {
		t.Fatalf("expected nil provider, got %v", p)
	}
	if backend != "anything" {
		t.Fatalf("expected original model, got %q", backend)
	}
}

func TestResolveModel_TrimsModelWhitespace(t *testing.T) {
	cfg := &Config{Providers: []Provider{
		{ID: "a", Name: "A", Enabled: true, ExposedModels: []ExposedModel{
			{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"},
		}},
	}}
	p, backend := cfg.ResolveModel("  glm-4.6  ")
	if p == nil || p.ID != "a" || backend != "glm-4.6" {
		t.Fatalf("expected provider a + glm-4.6, got %v %q", p, backend)
	}
}

// Context1M 模型：Claude Code 发往后端的 model 已剥离 [1m]（纯 ID），
// ResolveModel 仍用纯 ID 匹配，[1m] 不污染路由键。
func TestResolveModel_Context1MModelRoutesByPureID(t *testing.T) {
	cfg := &Config{Providers: []Provider{
		{ID: "a", Name: "A", Enabled: true, ExposedModels: []ExposedModel{
			{ID: "glm-5.2", Label: "GLM-5.2", BackendModel: "glm-5.2", Context1M: true},
		}},
	}}
	// Claude Code 剥离 [1m] 后发纯 ID "glm-5.2" → 命中
	p, backend := cfg.ResolveModel("glm-5.2")
	if p == nil || p.ID != "a" || backend != "glm-5.2" {
		t.Fatalf("Context1M model should route by pure ID, got %v %q", p, backend)
	}
	// 容错：即使 Claude Code 发含 [1m] 的 model，也应剥离后命中纯 ID
	p2, backend2 := cfg.ResolveModel("glm-5.2[1m]")
	if p2 == nil || p2.ID != "a" || backend2 != "glm-5.2" {
		t.Fatalf("Context1M model should tolerate [1m] suffix, got %v %q", p2, backend2)
	}
}

func TestValidate_DuplicateExposedModelIDAcrossProviders(t *testing.T) {
	cfg := &Config{Providers: []Provider{
		{ID: "a", Name: "A", Enabled: true, APIURL: "https://a", ExposedModels: []ExposedModel{
			{ID: "glm-4.6", Label: "GLM-4.6"},
		}},
		{ID: "b", Name: "B", Enabled: true, APIURL: "https://b", ExposedModels: []ExposedModel{
			{ID: "glm-4.6", Label: "GLM-4.6"}, // 跨 provider 重复
		}},
	}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected duplicate ID error, got nil")
	}
}

func TestValidate_SameIDAcrossDisabledAndEnabled(t *testing.T) {
	cfg := &Config{Providers: []Provider{
		{ID: "a", Name: "A", Enabled: true, APIURL: "https://a", ExposedModels: []ExposedModel{
			{ID: "glm-4.6", Label: "GLM-4.6"},
		}},
		{ID: "b", Name: "B", Enabled: false, APIURL: "https://b", ExposedModels: []ExposedModel{
			{ID: "glm-4.6", Label: "GLM-4.6"},
		}},
	}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected duplicate ID error across enabled/disabled, got nil")
	}
}
