package config

import (
	"fmt"
	"net/url"
	"strings"

	"magic-claude-code/internal/providerquota"
)

const (
	ThemeModeLight = "light"
	ThemeModeDark  = "dark"

	ConnectionModeTransparent = "transparent"
	ConnectionModeTunnel      = "tunnel"
	ConnectionModeGateway     = "gateway"
)

// Config 应用配置
type Config struct {
	// 后端代理地址（保留用于向后兼容）
	BackendURL string `json:"backend_url"`

	// 代理服务端口
	ProxyPort int `json:"proxy_port"`

	// 配置服务端口
	AdminPort int `json:"admin_port"`

	// ProxyListenAddr 代理服务监听地址（默认 0.0.0.0 = 所有接口；127.0.0.1 仅本机）
	ProxyListenAddr string `json:"proxy_listen_addr"`

	// AdminListenAddr 配置服务监听地址（默认 0.0.0.0 = 所有接口；127.0.0.1 仅本机）
	AdminListenAddr string `json:"admin_listen_addr"`

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

	// ConnectionMode 启动首选连接模式: transparent / tunnel / gateway
	ConnectionMode string `json:"connection_mode"`

	// GatewayListenAddr 路由模式监听地址（默认 127.0.0.1）
	GatewayListenAddr string `json:"gateway_listen_addr"`

	// GatewayListenPort 路由模式监听端口（默认 17487）
	GatewayListenPort int `json:"gateway_listen_port"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		BackendURL:       "https://open.bigmodel.cn/api/anthropic",
		ProxyPort:        443,
		AdminPort:        8442,
		ProxyListenAddr:  "0.0.0.0",
		AdminListenAddr:  "0.0.0.0",
		DataDir:          "./data",
		AdminThemeMode:   ThemeModeLight,
		ConnectionMode:   ConnectionModeTransparent,

		GatewayListenAddr: "127.0.0.1",
		GatewayListenPort: 17487,
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

// NormalizeConnectionMode returns a supported startup mode.
func NormalizeConnectionMode(mode string) string {
	switch mode {
	case ConnectionModeTransparent:
		return ConnectionModeTransparent
	case ConnectionModeTunnel:
		return ConnectionModeTunnel
	case ConnectionModeGateway:
		return ConnectionModeGateway
	default:
		return ConnectionModeTransparent
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	c.NormalizeDefaults()

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

		if u.User != nil {
			return fmt.Errorf("backend_url must not contain userinfo (user:pass@); put credentials in provider api_token instead")
		}
	}

	// 验证所有供应商配置
	for i := range c.Providers {
		if err := c.Providers[i].Validate(); err != nil {
			return fmt.Errorf("provider[%d]: %w", i, err)
		}
	}

	// 校验 ExposedModel.ID 跨 provider 全局唯一
	exposedIDs := make(map[string]string) // id -> 首次出现的 provider name
	for i := range c.Providers {
		for _, em := range c.Providers[i].ExposedModels {
			id := strings.TrimSpace(em.ID)
			if id == "" {
				continue // 单项空 ID 由 Provider.Validate 捕获
			}
			if firstProvider, exists := exposedIDs[id]; exists {
				return fmt.Errorf("exposed model id %q is duplicated between provider %q and %q",
					id, firstProvider, c.Providers[i].Name)
			}
			exposedIDs[id] = c.Providers[i].Name
		}
	}

	return nil
}

// RedactURL strips userinfo, query string and fragment from a URL, returning
// only scheme://host/path. Used by log and admin API layers to prevent
// credentials, signatures and other sensitive URL components from leaking.
// On parse failure the original string is returned (rare; preserves debug info).
func RedactURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// NormalizeDefaults fills backward-compatible default values.
func (c *Config) NormalizeDefaults() {
	c.ProxyListenAddr = normalizeListenAddr(c.ProxyListenAddr, "0.0.0.0")
	c.AdminListenAddr = normalizeListenAddr(c.AdminListenAddr, "0.0.0.0")
	c.ProxyPort = normalizeListenPort(c.ProxyPort, 443)
	c.AdminPort = normalizeListenPort(c.AdminPort, 8442)
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	c.AdminThemeMode = NormalizeThemeMode(c.AdminThemeMode)
	c.ConnectionMode = NormalizeConnectionMode(c.ConnectionMode)
	c.GatewayListenAddr = normalizeListenAddr(c.GatewayListenAddr, "127.0.0.1")
	if c.GatewayListenPort == 0 {
		c.GatewayListenPort = 17487
	}
	for i := range c.Providers {
		c.Providers[i].normalizeDefaults()
		providerquota.MigrateLegacyCredentials(c.Providers[i].QuotaQuery, c.Providers[i].APIURL)
	}
}

// normalizeListenAddr trims surrounding whitespace and falls back to the
// provided default when the result is empty. Listen addresses are
// infrastructure-layer values decided at deploy time; an empty value must
// resolve to a concrete address rather than the empty string.
func normalizeListenAddr(addr, fallback string) string {
	addr = strings.TrimSpace(addr)
	// Strip RFC 2732 IPv6 brackets: [::1] → ::1 (net.JoinHostPort adds its own).
	if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		addr = addr[1 : len(addr)-1]
	}
	if addr == "" {
		return fallback
	}
	return addr
}

// normalizeListenPort validates that the port is in the valid range 1–65535
// and falls back to the provided default when it is zero or out of range.
// A zero/out-of-range port must never reach net.Listen, which would emit an
// opaque error; falling back keeps startup robust.
func normalizeListenPort(port, fallback int) int {
	if port < 1 || port > 65535 {
		return fallback
	}
	return port
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

// ResolveModel 根据请求的 model 字段解析出 provider 和应写入后端请求体的模型名。
//
// 查找顺序：
//  1. 扫描所有 enabled provider 的 ExposedModels，命中 ID 匹配项 → 返回该 provider
//     与其 BackendModel（BackendModel 为空则用 ID）。
//  2. 未命中 → 返回 active provider 与 active.MapModel(model)（向后兼容 ModelMappings）。
//  3. 无 active provider → 返回 (nil, model)。
//
// 调用方需处理 provider == nil 的情况（对应"无可用 provider"错误路径）。
func (c *Config) ResolveModel(model string) (*Provider, string) {
	model = strings.TrimSpace(model)
	// Context1M 暴露模型：Claude Code 菜单 value 含 [1m]，但发往后端的 model 通常已剥离。
	// 为兼容两种情况，暴露模型匹配时统一剥离 [1m] 后缀（ID 本身不含 [1m]，由校验保证）。
	pureModel := strings.TrimSuffix(model, "[1m]")
	// 1. 暴露模型命中
	for i := range c.Providers {
		p := &c.Providers[i]
		if !p.Enabled {
			continue
		}
		for _, em := range p.ExposedModels {
			if em.ID == pureModel {
				// BackendModel 由 Provider.Validate 校验非空
				return p, em.BackendModel
			}
		}
	}
	// 2. fallback：active provider + MapModel
	if active := c.GetActiveProvider(); active != nil {
		return active, active.MapModel(model)
	}
	// 3. 无 active
	return nil, model
}
