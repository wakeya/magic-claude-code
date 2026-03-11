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