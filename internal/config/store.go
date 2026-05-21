package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ConfigStore 配置存储接口
type ConfigStore interface {
	Load() (*Config, error)
	Save(cfg *Config) error
}

// Store 配置存储
type Store struct {
	path string
}

// 确保 Store 实现 ConfigStore 接口
var _ ConfigStore = (*Store)(nil)

// NewStore 创建配置存储
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load 加载配置，如果文件不存在则返回默认配置
func (s *Store) Load() (*Config, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, err
	}

	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// 填充默认值
	if cfg.ProxyPort == 0 {
		cfg.ProxyPort = 443
	}
	if cfg.AdminPort == 0 {
		cfg.AdminPort = 8442
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	cfg.AdminThemeMode = NormalizeThemeMode(cfg.AdminThemeMode)

	return cfg, nil
}

// Save 保存配置
func (s *Store) Save(cfg *Config) error {
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0644)
}

// Path 返回配置文件路径
func (s *Store) Path() string {
	return s.path
}

// MockStore 用于测试的模拟存储
type MockStore struct {
	cfg *Config
}

// 确保 MockStore 实现 ConfigStore 接口
var _ ConfigStore = (*MockStore)(nil)

// NewMockStore 创建模拟存储
func NewMockStore(cfg *Config) *MockStore {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &MockStore{cfg: cfg}
}

// Load 加载配置
func (s *MockStore) Load() (*Config, error) {
	return s.cfg, nil
}

// Save 保存配置
func (s *MockStore) Save(cfg *Config) error {
	s.cfg = cfg
	return nil
}
