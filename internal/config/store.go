package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// ConfigStore 配置存储接口
type ConfigStore interface {
	Load() (*Config, error)
	Save(cfg *Config) error
	// Update 原子地执行「读最新配置 → 应用 mutator → 校验 → 保存 → 返回已提交副本」。
	// 整个读-改-写周期持有存储锁，保证并发写互不覆盖（例如代理自动故障切换改
	// ActiveProviderID 与管理端编辑供应商并发）。mutator 只负责修改 cfg 字段，
	// 不需自行调用 Validate；校验失败返回 *ValidationError，调用方可映射为 HTTP 400。
	Update(mutator func(*Config) error) (*Config, error)
}

// ValidationError 表示 Update 内 cfg.Validate() 的失败。它包裹原始校验错误，
// 使调用方既能用 errors.As 区分「校验失败 → 400」与「存储失败 → 500」，又能通过
// Error() 取得与原来一致的错误文案。
type ValidationError struct {
	Inner error
}

func (e *ValidationError) Error() string {
	if e.Inner == nil {
		return "configuration validation failed"
	}
	return e.Inner.Error()
}

func (e *ValidationError) Unwrap() error { return e.Inner }

// IsValidationError 报告 err 是否为 Update 的校验失败。
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// Store 配置存储
type Store struct {
	mu   sync.Mutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (*Config, error) {
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
	cfg.NormalizeDefaults()

	return cfg, nil
}

// Save 保存配置
func (s *Store) Save(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(cfg)
}

func (s *Store) save(cfg *Config) error {
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

// Update 原子地读-改-写配置。
func (s *Store) Update(mutator func(*Config) error) (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := s.load()
	if err != nil {
		return nil, err
	}
	if err := mutator(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, &ValidationError{Inner: err}
	}
	if err := s.save(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Path 返回配置文件路径
func (s *Store) Path() string {
	return s.path
}

// MockStore 用于测试的模拟存储
type MockStore struct {
	mu  sync.Mutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, nil
}

// Save 保存配置
func (s *MockStore) Save(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	return nil
}

// Update 原子地读-改-写配置（用于需要走原子路径的测试）。
func (s *MockStore) Update(mutator func(*Config) error) (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := mutator(s.cfg); err != nil {
		return nil, err
	}
	if err := s.cfg.Validate(); err != nil {
		return nil, &ValidationError{Inner: err}
	}
	return s.cfg, nil
}
