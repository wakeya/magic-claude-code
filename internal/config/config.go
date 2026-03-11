package config

import (
	"fmt"
	"net/url"
)

// Config 应用配置
type Config struct {
	// 后端代理地址
	BackendURL string `json:"backend_url"`

	// 代理服务端口
	ProxyPort int `json:"proxy_port"`

	// 配置服务端口
	AdminPort int `json:"admin_port"`

	// 管理密码 (bcrypt哈希)
	AdminPasswordHash string `json:"admin_password_hash"`

	// 数据目录
	DataDir string `json:"data_dir"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		BackendURL:    "https://open.bigmodel.cn/api/anthropic",
		ProxyPort:     443,
		AdminPort:     8442,
		DataDir:       "./data",
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	if c.BackendURL == "" {
		return fmt.Errorf("backend_url is required")
	}

	// 验证 URL 格式
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

	return nil
}