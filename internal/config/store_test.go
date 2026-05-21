package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_SaveAndLoad(t *testing.T) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewStore(filepath.Join(tmpDir, "config.json"))

	// 测试保存
	cfg := &Config{
		BackendURL: "https://test.example.com/api",
		ProxyPort:  443,
		AdminPort:  8442,
		DataDir:    tmpDir,
	}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// 测试加载
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if loaded.BackendURL != cfg.BackendURL {
		t.Errorf("expected backend URL %s, got %s", cfg.BackendURL, loaded.BackendURL)
	}
}

func TestJSONStorePersistsAdminThemeMode(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(filepath.Join(tmpDir, "config.json"))

	cfg := DefaultConfig()
	cfg.AdminThemeMode = ThemeModeDark
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.AdminThemeMode != ThemeModeDark {
		t.Fatalf("AdminThemeMode = %q, want %q", loaded.AdminThemeMode, ThemeModeDark)
	}
}

func TestStore_LoadNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := NewStore(filepath.Join(tmpDir, "nonexistent.json"))

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("expected no error for non-existent file, got: %v", err)
	}

	// 应返回默认配置 - 使用正确的默认后端URL
	if cfg.BackendURL != "https://open.bigmodel.cn/api/anthropic" {
		t.Errorf("expected default backend URL, got %s", cfg.BackendURL)
	}
}
