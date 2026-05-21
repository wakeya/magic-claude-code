package config

import (
	"fmt"
	"net/url"
)

const (
	ThemeModeLight = "light"
	ThemeModeDark  = "dark"
)

// Config 应用配置
type Config struct {
	// 后端代理地址（保留用于向后兼容）
	BackendURL string `json:"backend_url"`

	// 代理服务端口
	ProxyPort int `json:"proxy_port"`

	// 配置服务端口
	AdminPort int `json:"admin_port"`

	// 管理密码 (bcrypt哈希)
	AdminPasswordHash string `json:"admin_password_hash"`

	// 数据目录
	DataDir string `json:"data_dir"`

	// Providers API 供应商列表
	Providers []Provider `json:"providers"`

	// ActiveProviderID 当前激活的供应商 ID
	ActiveProviderID string `json:"active_provider_id"`

	// AdminThemeMode 管理端主题模式: light 或 dark
	AdminThemeMode string `json:"admin_theme_mode"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		BackendURL:     "https://open.bigmodel.cn/api/anthropic",
		ProxyPort:      443,
		AdminPort:      8442,
		DataDir:        "./data",
		AdminThemeMode: ThemeModeLight,
	}
}

// NormalizeThemeMode returns a supported admin theme mode.
func NormalizeThemeMode(mode string) string {
	switch mode {
	case ThemeModeDark:
		return ThemeModeDark
	case ThemeModeLight:
		return ThemeModeLight
	default:
		return ThemeModeLight
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	// 如果有供应商配置，则不需要 BackendURL
	if len(c.Providers) == 0 && c.BackendURL == "" {
		return fmt.Errorf("backend_url or providers is required")
	}

	// 验证 BackendURL 格式（如果设置）
	if c.BackendURL != "" {
		u, err := url.Parse(c.BackendURL)
		if err != nil {
			return fmt.Errorf("invalid backend_url: %w", err)
		}

		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("backend_url must use http or https scheme")
		}

		if u.Host == "" {
			return fmt.Errorf("backend_url must have a host")
		}
	}

	// 验证所有供应商配置
	for i := range c.Providers {
		if err := c.Providers[i].Validate(); err != nil {
			return fmt.Errorf("provider[%d]: %w", i, err)
		}
	}

	return nil
}

// GetActiveProvider 获取当前激活的供应商
// 如果没有激活的供应商，返回第一个启用的供应商
// 如果没有启用的供应商，返回 nil
func (c *Config) GetActiveProvider() *Provider {
	// 首先查找激活的供应商
	for i := range c.Providers {
		if c.Providers[i].ID == c.ActiveProviderID && c.Providers[i].Enabled {
			return &c.Providers[i]
		}
	}

	// 如果没有找到，返回第一个启用的供应商
	for i := range c.Providers {
		if c.Providers[i].Enabled {
			return &c.Providers[i]
		}
	}

	return nil
}

// GetProviderByID 根据 ID 获取供应商
func (c *Config) GetProviderByID(id string) *Provider {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return &c.Providers[i]
		}
	}
	return nil
}
