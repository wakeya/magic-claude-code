package proxy

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// localCatalogItem 是本地插件/skill 搜索返回的单条结果。
// 字段形状匹配 Claude Code 客户端 parser（results[].{id,name,description,enabled}）。
type localCatalogItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source,omitempty"`
	Kind        string `json:"kind,omitempty"`
	// searchText 是用于关键字匹配的小写拼接文本，不序列化。
	searchText string `json:"-"`
}

// maxCatalogResults 限制单次搜索返回条数，避免无界列表。
const maxCatalogResults = 50

// resolveClaudeConfigDir 解析本地 Claude 配置目录：
//   - 优先使用 CLAUDE_CONFIG_DIR 环境变量
//   - 否则回退到 ~/.claude
//   - home 不可解析时返回空串（调用方据此返回空 catalog，不报错）
func resolveClaudeConfigDir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// loadLocalCatalog 按类别从本地 Claude 配置加载尽力而为的 catalog。
// 任何文件缺失/不可读/格式错误都跳过，绝不返回 error（best-effort，缺失返回空切片）。
func loadLocalCatalog(configDir, kind string) []localCatalogItem {
	if configDir == "" {
		return []localCatalogItem{}
	}
	switch kind {
	case "plugin":
		return loadPlugins(configDir)
	case "skill":
		return loadSkills(configDir)
	}
	return []localCatalogItem{}
}

// marketplacePlugin 是 marketplace.json 的 plugins[] 条目的宽松解析结构，忽略未知字段。
type marketplacePlugin struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Keywords    []string `json:"keywords"`
}

// readMarketplace 读取 marketplace.json 的 name 与 plugins[] 数组，失败返回零值（best-effort）。
func readMarketplace(path string) (name string, plugins []marketplacePlugin) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	var mm struct {
		Name    string              `json:"name"`
		Plugins []marketplacePlugin `json:"plugins"`
	}
	if json.Unmarshal(data, &mm) != nil {
		return "", nil // 畸形 JSON 跳过
	}
	return strings.TrimSpace(mm.Name), mm.Plugins
}

// loadPlugins 从每个市场的 marketplace.json 的 plugins[] 数组加载插件 catalog。
// 这是权威来源：官方市场把全部插件汇总在该数组里（每个市场一个 marketplace.json）。
// source = marketplace name（与 settings.json enabledPlugins 的 `plugin@market` 复合键后缀一致）。
// 任何文件缺失/格式错误都跳过，绝不返回 error。
func loadPlugins(configDir string) []localCatalogItem {
	pattern := filepath.Join(configDir, "plugins", "marketplaces", "*", ".claude-plugin", "marketplace.json")
	matches, _ := filepath.Glob(pattern)
	items := make([]localCatalogItem, 0)
	seen := make(map[string]struct{})
	for _, mp := range matches {
		marketName, plugins := readMarketplace(mp)
		if marketName == "" {
			// marketplace 目录名兜底（= plugins/marketplaces/<dir>/.claude-plugin/marketplace.json 上两级）
			marketName = filepath.Base(filepath.Dir(filepath.Dir(mp)))
		}
		for _, p := range plugins {
			id := strings.TrimSpace(p.Name)
			if id == "" {
				continue
			}
			name := strings.TrimSpace(p.DisplayName)
			if name == "" {
				name = id
			}
			// 响应 id 用复合键 plugin@market：跨市场同名插件可被客户端唯一定位
			// （Claude Code 2.1.206 用 pluginId，不读自定义 source 字段区分）。与 enabledPlugins 键格式一致。
			// 无 market 名时回退纯插件 id。
			responseID := id
			if marketName != "" {
				responseID = id + "@" + marketName
			}
			if _, dup := seen[responseID]; dup {
				continue
			}
			seen[responseID] = struct{}{}
			item := localCatalogItem{
				ID:          responseID,
				Name:        name,
				Description: strings.TrimSpace(p.Description),
				Source:      marketName,
				Kind:        "plugin",
			}
			item.searchText = buildSearchText(item, p.Tags, p.Keywords, p.Category)
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		// 文件解析无结果时，用 ~/.claude.json 的 pluginUsage 键做名字兜底。
		items = loadClaudeJSONFallback(configDir, "plugin")
	}
	return items
}

// loadSkills 扫描本地与插件提供的 SKILL.md（marketplace 完整 catalog）：
//   - skills/*/SKILL.md（用户个人 skill）
//   - plugins/marketplaces/*/skills/*/SKILL.md（市场级 skill，如 karpathy/rust/minimax-skills）
//   - plugins/marketplaces/*/plugins/*/skills/*/SKILL.md（插件内嵌 skill）
//   - plugins/marketplaces/*/external_plugins/*/skills/*/SKILL.md（外部插件内嵌 skill）
//
// 用于 /skills/search（搜索 marketplace 可用 skill）。从 YAML frontmatter 解析 name/description。
// /api/claude_code/skills（已安装 skill 健康）用 loadInstalledSkills，只报真实安装的 skill。
func loadSkills(configDir string) []localCatalogItem {
	patterns := []string{
		filepath.Join(configDir, "skills", "*", "SKILL.md"),
		filepath.Join(configDir, "plugins", "marketplaces", "*", "skills", "*", "SKILL.md"),
		filepath.Join(configDir, "plugins", "marketplaces", "*", "plugins", "*", "skills", "*", "SKILL.md"),
		filepath.Join(configDir, "plugins", "marketplaces", "*", "external_plugins", "*", "skills", "*", "SKILL.md"),
	}
	items := make([]localCatalogItem, 0)
	seen := make(map[string]struct{})
	// 构建一次 目录名 -> 规范 marketplace name 映射，归一化临时目录名，避免重复 source。
	marketNameMap := buildMarketNameMap(configDir)
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, skillPath := range matches {
			data, err := os.ReadFile(skillPath)
			if err != nil {
				continue
			}
			name, desc := parseSKILLFrontmatter(data)
			skillDir := filepath.Base(filepath.Dir(skillPath))
			id := strings.TrimSpace(name)
			if id == "" {
				id = skillDir
			}
			skillName := strings.TrimSpace(name)
			if skillName == "" {
				skillName = skillDir
			}
			// 去重用复合键 id@source：跨插件/市场同名 skill 视为不同 skill。
			source := skillPathSource(skillPath, configDir, marketNameMap)
			dedupKey := id
			if source != "" && source != "local" {
				dedupKey = id + "@" + source
			}
			if _, dup := seen[dedupKey]; dup {
				continue
			}
			seen[dedupKey] = struct{}{}
			item := localCatalogItem{
				ID:          id,
				Name:        skillName,
				Description: strings.TrimSpace(desc),
				Source:      source,
				Kind:        "skill",
			}
			item.searchText = buildSearchText(item, nil, nil, "")
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		// 文件解析无结果时，用 ~/.claude.json 的 skillUsage 键做名字兜底。
		items = loadClaudeJSONFallback(configDir, "skill")
	}
	return items
}

// skillPathSource 从 SKILL.md 路径推断来源标识（用于 source 字段与 dedup）：
//   - 个人 skill（skills/<s>/SKILL.md）-> "local"
//   - 市场级 skill（.../marketplaces/<m>/skills/<s>）-> 规范 market name
//   - 插件内嵌（.../marketplaces/<m>/plugins/<p>/skills/<s> 或 external_plugins/<p>/...）-> "<p>@<规范 market>"
//   - 已安装插件 cache（.../cache/<m>/<p>/<version>/skills/<s>）-> "<p>@<规范 market>"
//
// market 目录名通过 marketNameMap 归一化为 manifest 声明的规范名，避免临时目录
// （如 temp_xxx）与规范目录（claude-plugins-official）指向同一 marketplace 时产生重复 source。
func skillPathSource(skillPath, configDir string, marketNameMap map[string]string) string {
	rel := strings.TrimPrefix(skillPath, configDir+string(filepath.Separator))
	parts := strings.Split(rel, string(filepath.Separator))
	for i, seg := range parts {
		if seg == "marketplaces" && i+1 < len(parts) {
			market := canonicalMarketName(parts[i+1], marketNameMap)
			// plugins/<plugin> 或 external_plugins/<plugin> 紧跟 market 之后
			if i+3 < len(parts) && (parts[i+2] == "plugins" || parts[i+2] == "external_plugins") {
				return parts[i+3] + "@" + market
			}
			return market // 市场级 skill
		}
		if seg == "cache" && i+2 < len(parts) {
			// plugins/cache/<market>/<plugin>/<version>/skills/<skill>
			market := canonicalMarketName(parts[i+1], marketNameMap)
			return parts[i+2] + "@" + market
		}
	}
	return "local"
}

// canonicalMarketName 把 marketplaces 目录名归一化为 manifest 声明的规范 marketplace name。
// 映射缺失或为空时回退目录名。
func canonicalMarketName(dir string, marketNameMap map[string]string) string {
	if name, ok := marketNameMap[dir]; ok && name != "" {
		return name
	}
	return dir
}

// readMarketplaceName 只读取 marketplace.json 的 name 字段（轻量，不解析 plugins 数组）。
func readMarketplaceName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var mm struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &mm) != nil {
		return ""
	}
	return strings.TrimSpace(mm.Name)
}

// buildMarketNameMap 构建 marketplaces 目录名 -> 规范 marketplace name 的映射（每个市场读一次 manifest）。
// 用于把临时目录名（temp_xxx）归一化为规范名，避免同一 marketplace 的 skill 因目录名不同而重复。
func buildMarketNameMap(configDir string) map[string]string {
	m := make(map[string]string)
	pattern := filepath.Join(configDir, "plugins", "marketplaces", "*", ".claude-plugin", "marketplace.json")
	matches, _ := filepath.Glob(pattern)
	for _, mp := range matches {
		// marketplaces/<dir>/.claude-plugin/marketplace.json -> dir = 上两级 basename
		dir := filepath.Base(filepath.Dir(filepath.Dir(mp)))
		if name := readMarketplaceName(mp); name != "" {
			m[dir] = name
		}
	}
	return m
}

// loadInstalledSkills 只加载真实安装的 skill + 个人 skill（用于 /api/claude_code/skills 健康）。
// 读 plugins/installed_plugins.json 的 plugins[plugin@market][].installPath，扫描各 installPath 下的
// skills/*/SKILL.md；再加个人 skills/*/SKILL.md。不返回整个 marketplace catalog。
func loadInstalledSkills(configDir string) []localCatalogItem {
	items := make([]localCatalogItem, 0)
	seen := make(map[string]struct{})
	marketNameMap := buildMarketNameMap(configDir)

	// 个人 skill
	personalPattern := filepath.Join(configDir, "skills", "*", "SKILL.md")
	appendSkillsFromGlob(&items, seen, personalPattern, "local", configDir, marketNameMap)

	// 已安装插件的 skill（从 installPath 扫描）
	for _, installPath := range readInstalledPluginPaths(configDir) {
		pattern := filepath.Join(installPath, "skills", "*", "SKILL.md")
		appendSkillsFromGlob(&items, seen, pattern, "plugin", configDir, marketNameMap)
	}

	if len(items) == 0 {
		items = loadClaudeJSONFallback(configDir, "skill")
	}
	return items
}

// appendSkillsFromGlob 对一个 glob 匹配的 SKILL.md 解析并追加到 items（按 dedupKey 去重）。
func appendSkillsFromGlob(items *[]localCatalogItem, seen map[string]struct{}, pattern, fallbackSource, configDir string, marketNameMap map[string]string) {
	matches, _ := filepath.Glob(pattern)
	for _, skillPath := range matches {
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		name, desc := parseSKILLFrontmatter(data)
		skillDir := filepath.Base(filepath.Dir(skillPath))
		id := strings.TrimSpace(name)
		if id == "" {
			id = skillDir
		}
		skillName := strings.TrimSpace(name)
		if skillName == "" {
			skillName = skillDir
		}
		source := skillPathSource(skillPath, configDir, marketNameMap)
		if source == "" {
			source = fallbackSource
		}
		dedupKey := id + "@" + source
		if _, dup := seen[dedupKey]; dup {
			continue
		}
		seen[dedupKey] = struct{}{}
		item := localCatalogItem{
			ID:          id,
			Name:        skillName,
			Description: strings.TrimSpace(desc),
			Source:      source,
			Kind:        "skill",
		}
		item.searchText = buildSearchText(item, nil, nil, "")
		*items = append(*items, item)
	}
}

// readInstalledPluginPaths 读取 plugins/installed_plugins.json，返回所有已安装插件的 installPath。
// 文件缺失/格式错误返回空（best-effort）。结构：{"version":2,"plugins":{"plugin@market":[{installPath,...}]}}。
func readInstalledPluginPaths(configDir string) []string {
	data, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	paths := make([]string, 0, len(doc.Plugins))
	for _, installs := range doc.Plugins {
		for _, inst := range installs {
			if p := strings.TrimSpace(inst.InstallPath); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// parseSKILLFrontmatter 用最小行解析提取 SKILL.md frontmatter 的 name/description。
// 不引入 YAML 依赖；frontmatter 是首行 "---" 到下一个 "---" 之间的块。
func parseSKILLFrontmatter(data []byte) (name, description string) {
	text := string(data)
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			break
		}
		switch {
		case strings.HasPrefix(line, "name:"):
			name = stripValueQuotes(strings.TrimSpace(line[len("name:"):]))
		case strings.HasPrefix(line, "description:"):
			description = stripValueQuotes(strings.TrimSpace(line[len("description:"):]))
		}
	}
	return name, description
}

// stripValueQuotes 去除 frontmatter 值两侧的成对引号。
func stripValueQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// buildSearchText 构造用于大小写不敏感子串匹配的小写文本。
func buildSearchText(item localCatalogItem, tags, keywords []string, category string) string {
	parts := []string{item.ID, item.Name, item.Description, item.Source, item.Kind, category}
	parts = append(parts, tags...)
	parts = append(parts, keywords...)
	return strings.ToLower(strings.Join(parts, " "))
}

// isPluginEnabled 判断插件是否启用。settings.json 的 enabledPlugins 使用复合键
// `plugin@market`（如 agent-sdk-dev@claude-plugins-official），故同时尝试直接 id 与复合键。
func isPluginEnabled(enabled map[string]bool, item localCatalogItem) bool {
	if enabled[item.ID] {
		return true
	}
	if item.Source != "" {
		return enabled[item.ID+"@"+item.Source]
	}
	return false
}

// loadClaudeJSONFallback 当文件解析无结果时，从 ~/.claude.json 的 pluginUsage/skillUsage
// 键做名字兜底。键格式：plugin 为 `name@market`（拆出 name+market），skill 为 `name` 或 `plugin:skill`。
// 仅提供名字（无 description），best-effort，文件缺失/格式错误返回空。
func loadClaudeJSONFallback(configDir, kind string) []localCatalogItem {
	path := claudeJSONPath(configDir)
	if path == "" {
		return []localCatalogItem{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []localCatalogItem{}
	}
	var doc struct {
		PluginUsage map[string]any `json:"pluginUsage"`
		SkillUsage  map[string]any `json:"skillUsage"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return []localCatalogItem{}
	}
	items := make([]localCatalogItem, 0)
	seen := make(map[string]struct{})
	switch kind {
	case "plugin":
		for k := range doc.PluginUsage {
			name, market := splitCompoundKey(k)
			if name == "" {
				continue
			}
			// 响应 id 用复合键 plugin@market，与主 catalog 一致。
			responseID := name
			if market != "" {
				responseID = name + "@" + market
			}
			if _, dup := seen[responseID]; dup {
				continue
			}
			seen[responseID] = struct{}{}
			item := localCatalogItem{ID: responseID, Name: name, Source: market, Kind: "plugin"}
			item.searchText = buildSearchText(item, nil, nil, "")
			items = append(items, item)
		}
	case "skill":
		for k := range doc.SkillUsage {
			name := k
			// `plugin:skill` 形式只取 skill 段做 id，source 标 plugin 名
			source := "local"
			if idx := strings.IndexByte(k, ':'); idx >= 0 {
				source = k[:idx]
				name = k[idx+1:]
			}
			if name == "" {
				continue
			}
			dedupKey := name + "@" + source
			if _, dup := seen[dedupKey]; dup {
				continue
			}
			seen[dedupKey] = struct{}{}
			item := localCatalogItem{ID: name, Name: name, Source: source, Kind: "skill"}
			item.searchText = buildSearchText(item, nil, nil, "")
			items = append(items, item)
		}
	}
	return items
}

// claudeJSONPath 推断 ~/.claude.json 的路径：configDir 的同级 .claude.json。
// configDir 通常为 ~/.claude，故 .claude.json 在其父目录。
func claudeJSONPath(configDir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configDir), ".claude.json")
}

// splitCompoundKey 把 `name@market` 拆成 name 与 market。无 @ 时 market 为空。
func splitCompoundKey(k string) (name, market string) {
	if idx := strings.IndexByte(k, '@'); idx >= 0 {
		return k[:idx], k[idx+1:]
	}
	return k, ""
}

// readEnabledPlugins 读取 settings.json.enabledPlugins，返回值为 true 的插件 id 集合。
// 文件缺失/格式错误返回空 map（best-effort）。
func readEnabledPlugins(configDir string) map[string]bool {
	result := make(map[string]bool)
	if configDir == "" {
		return result
	}
	data, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return result
	}
	var settings struct {
		EnabledPlugins map[string]any `json:"enabledPlugins"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return result
	}
	for k, v := range settings.EnabledPlugins {
		if b, ok := v.(bool); ok && b {
			result[k] = true
		}
	}
	return result
}

// parseSearchKeywords 从请求体解析搜索关键字。
// 支持 {"keywords":["a","b"]} 与 {"keywords":"a b"}；body 缺失/格式错误返回 nil（=未过滤）。
// 读取并关闭请求体，保证连接可复用。
func parseSearchKeywords(r *http.Request) []string {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize+1))
	r.Body.Close()
	if err != nil || len(body) > maxRequestBodySize {
		return nil
	}
	if len(body) == 0 {
		return nil
	}
	var req struct {
		Keywords any `json:"keywords"`
	}
	if json.Unmarshal(body, &req) != nil {
		return nil
	}
	return normalizeKeywords(req.Keywords)
}

// normalizeKeywords 把 keywords 字段（数组或字符串）归一化为关键字切片。
func normalizeKeywords(v any) []string {
	switch kw := v.(type) {
	case []any:
		out := make([]string, 0, len(kw))
		for _, k := range kw {
			if s, ok := k.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case string:
		return stringFields(kw)
	}
	return nil
}

// stringFields 按空白切分字符串并去空。
func stringFields(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// filterCatalog 按关键字过滤并稳定排序（enabled 优先 → 小写 name → id），最多返回 50 条。
func filterCatalog(items []localCatalogItem, keywords []string) []localCatalogItem {
	filtered := make([]localCatalogItem, 0, len(items))
	kwLower := make([]string, 0, len(keywords))
	for _, k := range keywords {
		kwLower = append(kwLower, strings.ToLower(k))
	}
	for _, it := range items {
		if matchesAllKeywords(it.searchText, kwLower) {
			filtered = append(filtered, it)
		}
	}
	sortCatalog(filtered)
	if len(filtered) > maxCatalogResults {
		filtered = filtered[:maxCatalogResults]
	}
	return filtered
}

// matchesAllKeywords 要求所有关键字都在 searchText 中命中（AND，大小写不敏感）。
func matchesAllKeywords(searchText string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	for _, k := range keywords {
		if k == "" {
			continue
		}
		if !strings.Contains(searchText, k) {
			return false
		}
	}
	return true
}

// sortCatalog 按 enabled 优先、小写 name、id 升序稳定排序。
func sortCatalog(items []localCatalogItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Enabled != b.Enabled {
			return a.Enabled // enabled 优先
		}
		if al, bl := strings.ToLower(a.Name), strings.ToLower(b.Name); al != bl {
			return al < bl
		}
		return a.ID < b.ID
	})
}

// orgScopedSuffix 从 /api/oauth/organizations/{orgUUID}/{suffix} 提取 per-org suffix。
// 用于在宽泛 /api/oauth/organizations/ fallback 之前匹配具体搜索端点。
// 返回 ("", false) 表示不是组织级端点或缺少 suffix 段。
func orgScopedSuffix(path string) (string, bool) {
	const prefix = "/api/oauth/organizations/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := path[len(prefix):]
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return "", false
	}
	return rest[idx+1:], true
}

// orgSearchKind 把组织级搜索 suffix 映射到处理类别。
// 返回 ("", false) 表示不是搜索端点（交给宽泛 organization fallback）。
func orgSearchKind(suffix string) (kind string, ok bool) {
	switch suffix {
	case "mcp/connectors/list", "mcp/connectors/search", "mcp/connectors/suggest":
		return "connectors", true
	case "plugins/search":
		return "plugin", true
	case "skills/search":
		return "skill", true
	}
	return "", false
}

// handleOrgScopedSearch 处理 /api/oauth/organizations/{orgUUID}/(plugins|skills|mcp)/...
// 等具体搜索端点。必须先于宽泛 organization fallback 与 drain 命中（POST 时读取请求体做关键字搜索）。
// 这些端点按 spec 契约为 POST-only：非 POST 先有界 drain/close body，再返回 405。
// 只在匹配到具体 handler 时打日志，避免与 handleHardcodedEndpoint 的 "Handling" 日志重复。
func (h *Handler) handleOrgScopedSearch(w http.ResponseWriter, r *http.Request) bool {
	suffix, ok := orgScopedSuffix(r.URL.Path)
	if !ok {
		return false
	}
	kind, isSearch := orgSearchKind(suffix)
	if !isSearch {
		return false // 非搜索 suffix → 宽泛 organization fallback 处理
	}
	log.Printf("[Hardcoded] Handling %s %s", r.Method, r.URL.Path)
	// spec 契约：组织级搜索端点仅接受 POST。
	if r.Method != http.MethodPost {
		drainRequestBodyLimited(r, maxLocalDrainSize) // 有界 drain/close，避免非 POST 大 body DoS
		w.Header().Set("Allow", http.MethodPost)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		encodeJSONBody(w, map[string]any{
			"error": map[string]any{
				"type":    "method_not_allowed",
				"message": "Only POST is allowed for organization search endpoints",
			},
		})
		return true
	}
	switch kind {
	case "connectors":
		h.handleMCPConnectors(w, r)
	case "plugin":
		h.handleCatalogSearch(w, r, "plugin")
	case "skill":
		h.handleCatalogSearch(w, r, "skill")
	}
	return true
}

// handleMCPConnectors 本地返回空 MCP connector 结果。不发起远程 marketplace 联邦。
func (h *Handler) handleMCPConnectors(w http.ResponseWriter, r *http.Request) {
	drainRequestBodyLimited(r, maxLocalDrainSize) // 有界 drain，connector body 通常很小
	writeJSONResponse(w, http.StatusOK, map[string]any{"results": []localCatalogItem{}})
}

// handleCatalogSearch 处理插件/skill 搜索，返回客户端兼容的 results 数组。
// 缺失/畸形配置时返回空数组（不 500）。
func (h *Handler) handleCatalogSearch(w http.ResponseWriter, r *http.Request, kind string) {
	configDir := resolveClaudeConfigDir()
	items := loadLocalCatalog(configDir, kind)
	enabled := readEnabledPlugins(configDir)
	for i := range items {
		items[i].Enabled = isPluginEnabled(enabled, items[i])
	}
	keywords := parseSearchKeywords(r) // 读取并消费请求体
	results := filterCatalog(items, keywords)
	writeJSONResponse(w, http.StatusOK, map[string]any{"results": results})
}

// handleClaudeCodeSkills 处理 GET /api/claude_code/skills，返回**已安装** skill 的健康状态。
// 只报真实安装的 skill（plugins/installed_plugins.json 的 installPath）+ 个人 skills，
// 不返回整个 marketplace catalog。source 反映 skill 归属（local / 插件名）。
func (h *Handler) handleClaudeCodeSkills(w http.ResponseWriter) {
	items := loadInstalledSkills(resolveClaudeConfigDir())
	skills := make([]map[string]string, 0, len(items))
	for _, it := range items {
		source := it.Source
		if source == "" {
			source = "local"
		}
		skills = append(skills, map[string]string{
			"skill_name": it.Name,
			"health":     "good",
			"source":     source,
		})
	}
	writeJSONResponse(w, http.StatusOK, map[string]any{"skills": skills})
}
