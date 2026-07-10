package proxy

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCatalogFixture 在临时目录下构造 Claude 配置/插件/skill 文件结构（marketplace.json plugins[] 格式）。
// 用真实结构：每个市场一个 marketplace.json（含 plugins 数组），settings.json 用 `plugin@market` 复合键。
// 同时构造 installed_plugins.json + cache 目录，区分 /skills/search（marketplace catalog）与
// /api/claude_code/skills（仅已安装）两个数据源。
func writeCatalogFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// settings.json：enabledPlugins 用复合键 plugin@market
	settings := `{"enabledPlugins":{"active-plugin@my-market":true}}`
	mustWriteFile(t, filepath.Join(dir, "settings.json"), settings)

	// marketplace：my-market，含 active-plugin（已启用，带 displayName/tags）
	activeClaude := filepath.Join(dir, "plugins", "marketplaces", "my-market", ".claude-plugin")
	mustWriteFile(t, filepath.Join(activeClaude, "marketplace.json"),
		`{"name":"my-market","plugins":[`+
			`{"name":"active-plugin","displayName":"Active Plugin","description":"An active coding plugin","category":"coding","tags":["code"]}`+
			`]}`)

	// marketplace：ghost-market，含 ghost-plugin（未启用）
	ghostClaude := filepath.Join(dir, "plugins", "marketplaces", "ghost-market", ".claude-plugin")
	mustWriteFile(t, filepath.Join(ghostClaude, "marketplace.json"),
		`{"name":"ghost-market","plugins":[`+
			`{"name":"ghost-plugin","description":"A disabled ghost plugin"}`+
			`]}`)

	// 用户个人 skill（/skills/search 与 /api/claude_code/skills 都应发现）
	mustWriteFile(t, filepath.Join(dir, "skills", "my-skill", "SKILL.md"),
		"---\nname: my-skill\ndescription: A local skill\n---\n# body\n")

	// 市场级 skill（仅 /skills/search 发现；未安装，不出现在 /api/claude_code/skills）
	mustWriteFile(t, filepath.Join(dir, "plugins", "marketplaces", "my-market", "skills", "plugin-skill", "SKILL.md"),
		"---\nname: plugin-skill\ndescription: A marketplace skill\n---\n# body\n")

	// 已安装插件：installed_plugins.json 指向 cache 目录（仅 /api/claude_code/skills 发现）
	installPath := filepath.Join(dir, "plugins", "cache", "my-market", "installed-plugin", "1.0.0")
	mustWriteFile(t, filepath.Join(dir, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"installed-plugin@my-market":[{"installPath":"`+installPath+`","version":"1.0.0"}]}}`)
	mustWriteFile(t, filepath.Join(installPath, "skills", "installed-skill", "SKILL.md"),
		"---\nname: installed-skill\ndescription: An installed plugin skill\n---\n# body\n")

	return dir
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLocalCatalogDirUsesEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", tmp)
	if got := resolveClaudeConfigDir(); got != tmp {
		t.Errorf("resolveClaudeConfigDir = %q, want %q", got, tmp)
	}

	// 空值时回退到 ~/.claude（只要 home 可解析）
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := resolveClaudeConfigDir(); got == "" {
		// 多数环境下 home 可解析，应得到非空路径；无法解析时允许空。
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			t.Errorf("resolveClaudeConfigDir = empty, want ~/.claude fallback")
		}
	}
}

// TestSkillDedupNormalizesTempMarketplaceDir 验证 marketplace 目录名（含临时目录 temp_xxx）
// 归一化为 manifest 声明的规范名后去重：official/ 与 temp_xxx/ 两个目录都声明 name=claude-plugins-official，
// 同名插件同名 skill 只保留一条，source 为 plugin@claude-plugins-official（gpt-5.6 第三轮 follow-up）。
func TestSkillDedupNormalizesTempMarketplaceDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	for _, marketDir := range []string{"claude-plugins-official", "temp_1772758330775"} {
		// 两个目录的 manifest name 都是规范名
		mustWriteFile(t, filepath.Join(dir, "plugins", "marketplaces", marketDir, ".claude-plugin", "marketplace.json"),
			`{"name":"claude-plugins-official","plugins":[]}`)
		// 同一插件同一 skill，放在各自目录
		mustWriteFile(t, filepath.Join(dir, "plugins", "marketplaces", marketDir, "plugins", "plugin-dev", "skills", "plugin-development", "SKILL.md"),
			"---\nname: plugin-development\ndescription: x\n---\n# body\n")
	}

	items := loadSkills(dir)
	var matches []localCatalogItem
	for _, it := range items {
		if it.ID == "plugin-development" {
			matches = append(matches, it)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 plugin-development skill after temp-dir normalization, got %d: %+v", len(matches), matches)
	}
	if matches[0].Source != "plugin-dev@claude-plugins-official" {
		t.Errorf("source = %q, want plugin-dev@claude-plugins-official (canonical, not temp dir)", matches[0].Source)
	}
}

// TestSkillDedupDisambiguatesSameNameAcrossMarkets 验证不同市场同名插件提供的同名 skill
// 不会被合并（source 含 plugin@market，dedup key 唯一）。
func TestSkillDedupDisambiguatesSameNameAcrossMarkets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	// 两个市场各有一个同名插件 "shared-plugin"，各提供一个同名 skill "build"
	for _, market := range []string{"market-a", "market-b"} {
		mustWriteFile(t, filepath.Join(dir, "plugins", "marketplaces", market, "plugins", "shared-plugin", "skills", "build", "SKILL.md"),
			"---\nname: build\ndescription: build skill in "+market+"\n---\n# body\n")
	}
	items := loadSkills(dir)
	var count int
	for _, it := range items {
		if it.ID == "build" {
			count++
			// source 应为 shared-plugin@market-a / shared-plugin@market-b
			if !strings.HasPrefix(it.Source, "shared-plugin@") {
				t.Errorf("build source = %q, want shared-plugin@<market>", it.Source)
			}
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 'build' skills (one per market), got %d; same-name skill across markets was merged", count)
	}
}

// TestPluginCatalogDedupByCompoundKey 验证响应 id 使用复合键 plugin@market：跨市场同名插件
// 是不同插件，可被客户端唯一定位、不应互相丢弃（gpt-5.6 第二/三轮审查点）。
func TestPluginCatalogDedupByCompoundKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	// 两个市场各有一个同名插件 "shared-name"
	mustWriteFile(t, filepath.Join(dir, "plugins", "marketplaces", "market-a", ".claude-plugin", "marketplace.json"),
		`{"name":"market-a","plugins":[{"name":"shared-name","description":"from market A"}]}`)
	mustWriteFile(t, filepath.Join(dir, "plugins", "marketplaces", "market-b", ".claude-plugin", "marketplace.json"),
		`{"name":"market-b","plugins":[{"name":"shared-name","description":"from market B"}]}`)

	items := loadLocalCatalog(dir, "plugin")
	var count int
	for _, it := range items {
		// 响应 id 是复合 plugin@market，跨市场同名插件各自唯一
		if strings.HasPrefix(it.ID, "shared-name@") {
			count++
			if it.Source != "market-a" && it.Source != "market-b" {
				t.Errorf("shared-name source = %q", it.Source)
			}
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 shared-name plugins (one per market), got %d; compound-key dedup likely dropped one", count)
	}
}

// TestLocalCatalogLoadsMarketplacePluginJSON 验证从 marketplace.json 的 plugins[] 加载插件。
func TestLocalCatalogLoadsMarketplacePluginJSON(t *testing.T) {
	dir := writeCatalogFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	items := loadLocalCatalog(resolveClaudeConfigDir(), "plugin")
	byID := map[string]localCatalogItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if _, ok := byID["active-plugin@my-market"]; !ok {
		t.Fatalf("active-plugin@my-market missing from catalog: %+v", items)
	}
	if _, ok := byID["ghost-plugin@ghost-market"]; !ok {
		t.Fatalf("ghost-plugin@ghost-market missing from catalog: %+v", items)
	}
	ap := byID["active-plugin@my-market"]
	if ap.Kind != "plugin" {
		t.Errorf("active-plugin kind = %q, want plugin", ap.Kind)
	}
	if ap.Source != "my-market" {
		t.Errorf("active-plugin source = %q, want my-market", ap.Source)
	}
	// displayName 优先作为 Name
	if ap.Name != "Active Plugin" {
		t.Errorf("active-plugin name = %q, want displayName 'Active Plugin'", ap.Name)
	}
	if !strings.Contains(ap.Description, "active coding") {
		t.Errorf("active-plugin description = %q", ap.Description)
	}
	// 复合键 enabled 匹配：id 本身就是 plugin@market，enabled[id] 直接命中
	enabled := readEnabledPlugins(resolveClaudeConfigDir())
	if !isPluginEnabled(enabled, ap) {
		t.Errorf("active-plugin should be enabled via compound id plugin@market")
	}
	if isPluginEnabled(enabled, byID["ghost-plugin@ghost-market"]) {
		t.Errorf("ghost-plugin should NOT be enabled")
	}
}

// TestPluginProvidedSkillsLoaded 验证扩展 glob 覆盖插件提供的 skill
// （plugins/marketplaces/*/skills/*/SKILL.md）。
func TestPluginProvidedSkillsLoaded(t *testing.T) {
	dir := writeCatalogFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	items := loadLocalCatalog(resolveClaudeConfigDir(), "skill")
	byID := map[string]localCatalogItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if _, ok := byID["my-skill"]; !ok {
		t.Errorf("personal skill my-skill missing: %+v", items)
	}
	if _, ok := byID["plugin-skill"]; !ok {
		t.Errorf("plugin-provided skill plugin-skill missing: %+v", items)
	}
}

// TestClaudeJSONFallbackWhenNoFiles 验证文件解析无结果时，从 ~/.claude.json 的
// pluginUsage/skillUsage 键做名字兜底（含复合键 plugin@market 拆分）。
func TestClaudeJSONFallbackWhenNoFiles(t *testing.T) {
	parent := t.TempDir()
	configDir := filepath.Join(parent, ".claude") // 不实际创建，glob 对缺失目录返回空
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	// .claude.json 位于 configDir 的父目录（= parent）
	mustWriteFile(t, filepath.Join(parent, ".claude.json"), `{
		"pluginUsage": {"agent-sdk-dev@claude-plugins-official": {"usageCount": 1}},
		"skillUsage": {"superpowers:brainstorming": {"usageCount": 8}, "web-access": {"usageCount": 2}}
	}`)

	t.Run("plugin fallback parses compound keys", func(t *testing.T) {
		items := loadLocalCatalog(configDir, "plugin")
		var found bool
		for _, it := range items {
			// 兜底也用复合 id plugin@market
			if it.ID == "agent-sdk-dev@claude-plugins-official" {
				found = true
			}
		}
		if !found {
			t.Fatalf("plugin fallback did not parse compound key: %+v", items)
		}
	})

	t.Run("skill fallback parses plugin:skill and plain", func(t *testing.T) {
		items := loadLocalCatalog(configDir, "skill")
		byID := map[string]string{}
		for _, it := range items {
			byID[it.ID] = it.Source
		}
		if _, ok := byID["brainstorming"]; !ok {
			t.Errorf("plugin:skill not split: %+v", byID)
		}
		if _, ok := byID["web-access"]; !ok {
			t.Errorf("plain skill not loaded: %+v", byID)
		}
	})
}

func TestLocalCatalogLoadsSkillsDirectory(t *testing.T) {
	dir := writeCatalogFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	items := loadLocalCatalog(resolveClaudeConfigDir(), "skill")
	var found bool
	for _, it := range items {
		if it.ID == "my-skill" {
			found = true
			if it.Kind != "skill" {
				t.Errorf("my-skill kind = %q, want skill", it.Kind)
			}
			if !strings.Contains(it.Description, "local skill") {
				t.Errorf("my-skill description = %q", it.Description)
			}
		}
	}
	if !found {
		t.Fatalf("my-skill not loaded: %+v", items)
	}
}

func TestSearchLocalCatalogHandlesArrayAndStringKeywords(t *testing.T) {
	dir := writeCatalogFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	items := loadLocalCatalog(resolveClaudeConfigDir(), "plugin")
	enabled := readEnabledPlugins(resolveClaudeConfigDir())
	for i := range items {
		items[i].Enabled = isPluginEnabled(enabled, items[i]) // 复合键 plugin@market 查找
	}

	t.Run("array keywords match", func(t *testing.T) {
		res := filterCatalog(items, []string{"active"})
		if len(res) != 1 || res[0].ID != "active-plugin@my-market" {
			t.Fatalf("filter active = %+v, want only active-plugin@my-market", res)
		}
		if !res[0].Enabled {
			t.Errorf("active-plugin should be enabled=true")
		}
	})

	t.Run("string keywords split by whitespace", func(t *testing.T) {
		// "ghost plugin" → 两个 keyword 都需命中
		res := filterCatalog(items, normalizeKeywords("ghost plugin"))
		if len(res) != 1 || res[0].ID != "ghost-plugin@ghost-market" {
			t.Fatalf("filter 'ghost plugin' = %+v", res)
		}
		if res[0].Enabled {
			t.Errorf("ghost-plugin should be enabled=false")
		}
	})

	t.Run("enabled sorts first", func(t *testing.T) {
		// 无 keyword：返回全部，enabled 优先
		res := filterCatalog(items, nil)
		if len(res) != 2 {
			t.Fatalf("filter empty = %+v, want 2", res)
		}
		if !res[0].Enabled {
			t.Errorf("first result should be enabled, got %+v", res[0])
		}
	})

	t.Run("parseSearchKeywords array body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"keywords":["foo","bar"]}`))
		kw := parseSearchKeywords(req)
		if len(kw) != 2 || kw[0] != "foo" || kw[1] != "bar" {
			t.Fatalf("array keywords = %v", kw)
		}
	})

	t.Run("parseSearchKeywords string body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"keywords":"foo bar"}`))
		kw := parseSearchKeywords(req)
		if len(kw) != 2 || kw[0] != "foo" || kw[1] != "bar" {
			t.Fatalf("string keywords = %v", kw)
		}
	})

	t.Run("parseSearchKeywords malformed body returns nil", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{not-json`))
		if kw := parseSearchKeywords(req); kw != nil {
			t.Fatalf("malformed keywords = %v, want nil", kw)
		}
	})
}

func TestPluginSkillSearchReturnsEmptyOnMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	// 畸形 settings.json + 畸形 plugin.json：不得 500，返回空兼容结果
	mustWriteFile(t, filepath.Join(dir, "settings.json"), `{not valid json`)
	pc := filepath.Join(dir, "plugins", "marketplaces", "bad", ".claude-plugin")
	mustWriteFile(t, filepath.Join(pc, "plugin.json"), `{not valid json`)

	handler := NewHandler(nil, nil)

	t.Run("plugin search returns empty results", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost,
			"/api/oauth/organizations/org-1/plugins/search",
			strings.NewReader(`{"keywords":["anything"]}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Results []any `json:"results"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
	})

	t.Run("missing config dir returns empty results", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "/nonexistent/claude/config/dir/xyz")
		req := httptest.NewRequest(http.MethodPost,
			"/api/oauth/organizations/org-1/skills/search",
			strings.NewReader(`{"keywords":["anything"]}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

// TestPluginSkillSearchReturnsConfigDerivedResults 验证具体搜索 handler 在宽泛
// /api/oauth/organizations/ fallback 之前命中，返回 results 数组（而非 {}）。
func TestPluginSkillSearchReturnsConfigDerivedResults(t *testing.T) {
	dir := writeCatalogFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	handler := NewHandler(nil, nil)

	t.Run("plugins/search precedes broad org fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost,
			"/api/oauth/organizations/org-1/plugins/search",
			strings.NewReader(`{"keywords":["active"]}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Results []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"results"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		// 宽泛 fallback 会返回 {}（无 results 字段），decode 后 results 应为 nil/空。
		// 具体 handler 命中时返回 active-plugin@my-market。
		var found bool
		for _, r := range resp.Results {
			if r.ID == "active-plugin@my-market" {
				found = true
				if !r.Enabled {
					t.Errorf("active-plugin enabled=false, want true")
				}
			}
		}
		if !found {
			t.Fatalf("plugins/search did not return active-plugin (broad fallback may have shadowed it): %+v", resp.Results)
		}
	})

	t.Run("broad org fallback still returns {} for non-search suffix", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/api/oauth/organizations/org-1/referral/eligibility", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if strings.TrimSpace(rec.Body.String()) != "{}" {
			t.Errorf("broad org fallback body = %q, want {}", rec.Body.String())
		}
	})
}

func TestMCPConnectorEndpoints(t *testing.T) {
	handler := NewHandler(nil, nil)
	suffixes := []string{"mcp/connectors/list", "mcp/connectors/search", "mcp/connectors/suggest"}
	for _, suffix := range suffixes {
		t.Run(suffix, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost,
				"/api/oauth/organizations/org-1/"+suffix,
				strings.NewReader(`{"query":"x"}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var resp struct {
				Results []any `json:"results"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v body=%s", err, rec.Body.String())
			}
			if len(resp.Results) != 0 {
				t.Errorf("results = %v, want empty array", resp.Results)
			}
		})
	}
}

// TestOrgScopedSearchRejectsNonPost 验证组织级搜索端点按 spec 契约仅接受 POST，
// 非 POST 返回 405 + Allow: POST（gpt-5.6 审查点：补 method checks）。
func TestOrgScopedSearchRejectsNonPost(t *testing.T) {
	dir := writeCatalogFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	handler := NewHandler(nil, nil)
	paths := []string{
		"/api/oauth/organizations/org-1/plugins/search",
		"/api/oauth/organizations/org-1/skills/search",
		"/api/oauth/organizations/org-1/mcp/connectors/search",
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, path := range paths {
			t.Run(method+" "+path, func(t *testing.T) {
				req := httptest.NewRequest(method, path, strings.NewReader(`{"keywords":["x"]}`))
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusMethodNotAllowed {
					t.Fatalf("status = %d, want 405", rec.Code)
				}
				if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
					t.Errorf("Allow = %q, want POST", allow)
				}
			})
		}
	}
}

// TestOrgEndpointsSingleHandlingLog 验证组织级端点（搜索 handler 与宽泛 fallback）
// 都只产生一行 "Handling" 日志，不因 handleOrgScopedSearch 提前返回而重复打印。
func TestOrgEndpointsSingleHandlingLog(t *testing.T) {
	var buf bytes.Buffer
	origOut := log.Default().Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origOut)

	dir := writeCatalogFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	handler := NewHandler(nil, nil)

	// 搜索 handler 路径：handleOrgScopedSearch 命中，应只打一行。
	req := httptest.NewRequest(http.MethodPost,
		"/api/oauth/organizations/org-1/plugins/search",
		strings.NewReader(`{"keywords":["x"]}`))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// 宽泛 fallback 路径：handleOrgScopedSearch 返回 false，应只由 handleHardcodedEndpoint 打一行。
	req2 := httptest.NewRequest(http.MethodGet,
		"/api/oauth/organizations/org-1/referral/eligibility", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req2)

	out := buf.String()
	for _, path := range []string{
		"/api/oauth/organizations/org-1/plugins/search",
		"/api/oauth/organizations/org-1/referral/eligibility",
	} {
		n := strings.Count(out, path)
		if n != 1 {
			t.Errorf("expected exactly 1 Handling log line for %s, got %d:\n%s", path, n, out)
		}
	}
}

func TestClaudeCodeSkillsHealthEndpoint(t *testing.T) {
	dir := writeCatalogFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	handler := NewHandler(nil, nil)

	t.Run("returns only installed + personal skills", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/claude_code/skills", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Skills []struct {
				SkillName string `json:"skill_name"`
				Health    string `json:"health"`
				Source    string `json:"source"`
			} `json:"skills"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		seen := map[string]string{}
		for _, s := range resp.Skills {
			seen[s.SkillName] = s.Source
		}
		// 个人 skill + 已安装插件 skill 必须出现
		if _, ok := seen["my-skill"]; !ok {
			t.Errorf("personal skill my-skill missing: %+v", seen)
		} else if seen["my-skill"] != "local" {
			t.Errorf("my-skill source = %q, want local", seen["my-skill"])
		}
		if _, ok := seen["installed-skill"]; !ok {
			t.Errorf("installed plugin skill installed-skill missing: %+v", seen)
		}
		// 未安装的市场级 skill 不应出现（区别于 /skills/search 的完整 catalog）
		if _, ok := seen["plugin-skill"]; ok {
			t.Errorf("marketplace-only skill plugin-skill should NOT appear in installed skills health: %+v", seen)
		}
		for _, s := range resp.Skills {
			if s.Health != "good" {
				t.Errorf("skill %s health = %q, want good", s.SkillName, s.Health)
			}
		}
	})

	t.Run("empty config returns empty skills", func(t *testing.T) {
		t.Setenv("CLAUDE_CONFIG_DIR", "/nonexistent/claude/empty/xyz")
		req := httptest.NewRequest(http.MethodGet, "/api/claude_code/skills", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Skills []any `json:"skills"`
		}
		json.NewDecoder(rec.Body).Decode(&resp)
		if len(resp.Skills) != 0 {
			t.Errorf("skills = %v, want empty", resp.Skills)
		}
	})
}
