package main

import (
	"testing"

	"magic-claude-code/internal/config"
)

func TestOverrideString_PrefersFlag(t *testing.T) {
	got := overrideString("flag-val", "env-val", "config-val")
	if got != "flag-val" {
		t.Errorf("overrideString(flag,env,config) = %q, want flag-val", got)
	}
}

func TestOverrideString_FallsBackToEnv(t *testing.T) {
	got := overrideString("", "env-val", "config-val")
	if got != "env-val" {
		t.Errorf("overrideString('',env,config) = %q, want env-val", got)
	}
}

func TestOverrideString_FallsBackToConfig(t *testing.T) {
	got := overrideString("", "", "config-val")
	if got != "config-val" {
		t.Errorf("overrideString('','',config) = %q, want config-val", got)
	}
}

func TestOverrideInt_PrefersFlag(t *testing.T) {
	got := overrideInt(8080, 9090, 443)
	if got != 8080 {
		t.Errorf("overrideInt(8080,9090,443) = %d, want 8080", got)
	}
}

func TestOverrideInt_FallsBackToEnv(t *testing.T) {
	got := overrideInt(0, 9090, 443)
	if got != 9090 {
		t.Errorf("overrideInt(0,9090,443) = %d, want 9090", got)
	}
}

func TestOverrideInt_FallsBackToConfig(t *testing.T) {
	got := overrideInt(0, 0, 443)
	if got != 443 {
		t.Errorf("overrideInt(0,0,443) = %d, want 443", got)
	}
}

func TestApplyListenConfig_FullOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	flag := listenOverride{ProxyAddr: "127.0.0.1", ProxyPort: 8443, AdminAddr: "127.0.0.1", AdminPort: 9000}
	env := listenOverride{}
	applyListenConfig(cfg, flag, env)
	if cfg.ProxyListenAddr != "127.0.0.1" || cfg.ProxyPort != 8443 {
		t.Errorf("proxy: addr=%q port=%d, want 127.0.0.1:8443", cfg.ProxyListenAddr, cfg.ProxyPort)
	}
	if cfg.AdminListenAddr != "127.0.0.1" || cfg.AdminPort != 9000 {
		t.Errorf("admin: addr=%q port=%d, want 127.0.0.1:9000", cfg.AdminListenAddr, cfg.AdminPort)
	}
}

func TestApplyListenConfig_EnvOverridesConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	flag := listenOverride{}
	env := listenOverride{ProxyAddr: "192.168.1.10", ProxyPort: 8080, AdminAddr: "192.168.1.10", AdminPort: 8081}
	applyListenConfig(cfg, flag, env)
	if cfg.ProxyListenAddr != "192.168.1.10" || cfg.ProxyPort != 8080 {
		t.Errorf("proxy: addr=%q port=%d, want 192.168.1.10:8080", cfg.ProxyListenAddr, cfg.ProxyPort)
	}
	if cfg.AdminListenAddr != "192.168.1.10" || cfg.AdminPort != 8081 {
		t.Errorf("admin: addr=%q port=%d, want 192.168.1.10:8081", cfg.AdminListenAddr, cfg.AdminPort)
	}
}

func TestApplyListenConfig_FlagsOverrideEnv(t *testing.T) {
	cfg := config.DefaultConfig()
	flag := listenOverride{ProxyAddr: "10.0.0.1", ProxyPort: 443}
	env := listenOverride{ProxyAddr: "192.168.1.10", ProxyPort: 8080}
	applyListenConfig(cfg, flag, env)
	if cfg.ProxyListenAddr != "10.0.0.1" || cfg.ProxyPort != 443 {
		t.Errorf("proxy: addr=%q port=%d, want flag values", cfg.ProxyListenAddr, cfg.ProxyPort)
	}
	// admin: no flag, env should win
	if cfg.AdminListenAddr != "0.0.0.0" || cfg.AdminPort != 8442 {
		t.Errorf("admin: addr=%q port=%d, want defaults (no flag or env)", cfg.AdminListenAddr, cfg.AdminPort)
	}
}

func TestApplyListenConfig_NothingOverridesPreservesDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	flag := listenOverride{}
	env := listenOverride{}
	applyListenConfig(cfg, flag, env)
	if cfg.ProxyListenAddr != "0.0.0.0" || cfg.ProxyPort != 443 {
		t.Errorf("proxy: addr=%q port=%d, want defaults", cfg.ProxyListenAddr, cfg.ProxyPort)
	}
	if cfg.AdminListenAddr != "0.0.0.0" || cfg.AdminPort != 8442 {
		t.Errorf("admin: addr=%q port=%d, want defaults", cfg.AdminListenAddr, cfg.AdminPort)
	}
}
