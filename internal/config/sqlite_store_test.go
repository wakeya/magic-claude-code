package config

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"magic-claude-code/internal/providerquota"
)

func testProvider(id, name, apiURL, token string, enabled bool) Provider {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	return Provider{
		ID:               id,
		Name:             name,
		APIURL:           apiURL,
		APIToken:         token,
		ModelMappings:    map[string]string{"claude-opus-4-6": name + "-model"},
		SupportsThinking: true,
		MultimodalSwitch: true,
		MultimodalModel:  name + "-vision-model",
		Enabled:          enabled,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func assertConfigEqual(t *testing.T, want, got *Config) {
	t.Helper()
	if got.BackendURL != want.BackendURL {
		t.Fatalf("BackendURL mismatch: want %q, got %q", want.BackendURL, got.BackendURL)
	}
	if got.ProxyPort != want.ProxyPort {
		t.Fatalf("ProxyPort mismatch: want %d, got %d", want.ProxyPort, got.ProxyPort)
	}
	if got.AdminPort != want.AdminPort {
		t.Fatalf("AdminPort mismatch: want %d, got %d", want.AdminPort, got.AdminPort)
	}
	if got.AdminPasswordHash != want.AdminPasswordHash {
		t.Fatalf("AdminPasswordHash mismatch: want %q, got %q", want.AdminPasswordHash, got.AdminPasswordHash)
	}
	if got.DataDir != want.DataDir {
		t.Fatalf("DataDir mismatch: want %q, got %q", want.DataDir, got.DataDir)
	}
	if got.ActiveProviderID != want.ActiveProviderID {
		t.Fatalf("ActiveProviderID mismatch: want %q, got %q", want.ActiveProviderID, got.ActiveProviderID)
	}
	if got.AdminThemeMode != NormalizeThemeMode(want.AdminThemeMode) {
		t.Fatalf("AdminThemeMode mismatch: want %q, got %q", NormalizeThemeMode(want.AdminThemeMode), got.AdminThemeMode)
	}
	if got.ConnectionMode != NormalizeConnectionMode(want.ConnectionMode) {
		t.Fatalf("ConnectionMode mismatch: want %q, got %q", NormalizeConnectionMode(want.ConnectionMode), got.ConnectionMode)
	}
	if len(got.Providers) != len(want.Providers) {
		t.Fatalf("Providers length mismatch: want %d, got %d", len(want.Providers), len(got.Providers))
	}
	for i := range want.Providers {
		wp := want.Providers[i]
		gp := got.Providers[i]
		if gp.ID != wp.ID || gp.Name != wp.Name || gp.APIURL != wp.APIURL || gp.APIToken != wp.APIToken || gp.SupportsThinking != wp.SupportsThinking || gp.MultimodalSwitch != wp.MultimodalSwitch || gp.MultimodalModel != wp.MultimodalModel || gp.Enabled != wp.Enabled {
			t.Fatalf("provider[%d] mismatch: want %#v, got %#v", i, wp, gp)
		}
		if gp.ModelMappings["claude-opus-4-6"] != wp.ModelMappings["claude-opus-4-6"] {
			t.Fatalf("provider[%d] model mapping mismatch: want %#v, got %#v", i, wp.ModelMappings, gp.ModelMappings)
		}
	}
}

func TestSQLiteStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	cfg := &Config{
		BackendURL:        "https://fallback.example.com/anthropic",
		ProxyPort:         443,
		AdminPort:         8442,
		AdminPasswordHash: "hash",
		DataDir:           dir,
		ConnectionMode:    ConnectionModeGateway,
		ActiveProviderID:  "provider-b",
		Providers: []Provider{
			testProvider("provider-a", "GLM", "https://glm.example.com/anthropic", "token-a", true),
			testProvider("provider-b", "MiniMax", "https://minimax.example.com/anthropic", "token-b", true),
		},
	}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertConfigEqual(t, cfg, loaded)
}

func TestSQLiteStorePersistsConnectionMode(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	cfg := DefaultConfig()
	cfg.ConnectionMode = ConnectionModeTunnel
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.ConnectionMode != ConnectionModeTunnel {
		t.Fatalf("ConnectionMode = %q, want %q", loaded.ConnectionMode, ConnectionModeTunnel)
	}
}

func TestSQLiteStoreDoesNotPersistListenAddrs(t *testing.T) {
	// 监听地址对齐 Gateway 先例：不进 SQLite。Save 自定义值后 Load 应回到默认值，
	// 证明 SQLite store 不持久化 proxy_listen_addr / admin_listen_addr。
	// 这与 GatewayListenAddr 的行为完全一致。
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	cfg := DefaultConfig()
	cfg.ProxyListenAddr = "127.0.0.1"
	cfg.AdminListenAddr = "192.168.1.10"
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.ProxyListenAddr != "0.0.0.0" {
		t.Errorf("ProxyListenAddr = %q, want default 0.0.0.0 (SQLite must not persist listen addrs)", loaded.ProxyListenAddr)
	}
	if loaded.AdminListenAddr != "0.0.0.0" {
		t.Errorf("AdminListenAddr = %q, want default 0.0.0.0 (SQLite must not persist listen addrs)", loaded.AdminListenAddr)
	}
}

func TestSQLiteStorePersistsProviderAPIFormatAndOpenAIExtraParams(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	provider := testProvider("provider-openai", "OpenAI", "https://openai.example.com/v1", "token", true)
	provider.APIFormat = APIFormatOpenAIChat
	disabled := false
	provider.ClaudeCodeCompatHint = &disabled
	provider.OpenAIExtraParams = map[string]any{
		"allowed_openai_params": []any{"thinking", "context_management"},
		"litellm_settings": map[string]any{
			"drop_params": true,
		},
	}
	cfg := DefaultConfig()
	cfg.Providers = []Provider{provider}
	cfg.ActiveProviderID = provider.ID

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := loaded.GetProviderByID(provider.ID)
	if got == nil {
		t.Fatal("provider missing after load")
	}
	if got.APIFormat != APIFormatOpenAIChat {
		t.Fatalf("APIFormat = %q, want %q", got.APIFormat, APIFormatOpenAIChat)
	}
	if got.ClaudeCodeCompatHint == nil || *got.ClaudeCodeCompatHint {
		t.Fatalf("ClaudeCodeCompatHint = %#v, want explicit false", got.ClaudeCodeCompatHint)
	}
	settings, ok := got.OpenAIExtraParams["litellm_settings"].(map[string]any)
	if !ok {
		t.Fatalf("litellm_settings = %#v", got.OpenAIExtraParams["litellm_settings"])
	}
	if settings["drop_params"] != true {
		t.Fatalf("drop_params = %#v, want true", settings["drop_params"])
	}
}

func TestSQLiteStorePersistsStripUnknownContentBlocks(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	provider := testProvider("provider-kimi", "Kimi", "https://api.moonshot.cn/anthropic", "token", true)
	provider.APIFormat = APIFormatAnthropic
	provider.StripUnknownContentBlocks = true

	cfg := DefaultConfig()
	cfg.Providers = []Provider{provider}
	cfg.ActiveProviderID = provider.ID

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := loaded.GetProviderByID(provider.ID)
	if got == nil {
		t.Fatal("provider missing after load")
	}
	if !got.StripUnknownContentBlocks {
		t.Fatalf("StripUnknownContentBlocks = false, want true")
	}

	// 测试默认值（未设置时应为 false）
	provider2 := testProvider("provider-glm", "GLM", "https://open.bigmodel.cn/api/anthropic", "token", true)
	cfg2 := DefaultConfig()
	cfg2.Providers = []Provider{provider2}
	if err := store.Save(cfg2); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded2, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got2 := loaded2.GetProviderByID(provider2.ID)
	if got2 == nil {
		t.Fatal("provider2 missing after load")
	}
	if got2.StripUnknownContentBlocks {
		t.Fatalf("StripUnknownContentBlocks = true, want false by default")
	}
}

func TestSQLiteStorePersistsRateLimitFields(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	provider := testProvider("provider-rl", "RateLimit", "https://api.example.com/v1", "token", true)
	provider.RateLimitQueueEnabled = true
	provider.MaxConcurrentRequests = 8
	provider.MaxQueueSize = 32
	provider.QueueTimeoutMS = 30000
	provider.Retry429Enabled = true
	provider.Retry429MaxAttempts = 3
	provider.Retry429InitialDelayMS = 500
	provider.Retry429MaxDelayMS = 8000

	cfg := DefaultConfig()
	cfg.Providers = []Provider{provider}
	cfg.ActiveProviderID = provider.ID

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := loaded.GetProviderByID(provider.ID)
	if got == nil {
		t.Fatal("provider missing after load")
	}
	if !got.RateLimitQueueEnabled {
		t.Fatal("RateLimitQueueEnabled = false, want true")
	}
	if got.MaxConcurrentRequests != 8 {
		t.Fatalf("MaxConcurrentRequests = %d, want 8", got.MaxConcurrentRequests)
	}
	if got.MaxQueueSize != 32 {
		t.Fatalf("MaxQueueSize = %d, want 32", got.MaxQueueSize)
	}
	if got.QueueTimeoutMS != 30000 {
		t.Fatalf("QueueTimeoutMS = %d, want 30000", got.QueueTimeoutMS)
	}
	if !got.Retry429Enabled {
		t.Fatal("Retry429Enabled = false, want true")
	}
	if got.Retry429MaxAttempts != 3 {
		t.Fatalf("Retry429MaxAttempts = %d, want 3", got.Retry429MaxAttempts)
	}
	if got.Retry429InitialDelayMS != 500 {
		t.Fatalf("Retry429InitialDelayMS = %d, want 500", got.Retry429InitialDelayMS)
	}
	if got.Retry429MaxDelayMS != 8000 {
		t.Fatalf("Retry429MaxDelayMS = %d, want 8000", got.Retry429MaxDelayMS)
	}

	// Verify disabled provider also round-trips correctly
	provider2 := testProvider("provider-rl-off", "NoLimit", "https://api2.example.com/v1", "token2", true)
	provider2.RateLimitQueueEnabled = false
	provider2.Retry429Enabled = false
	cfg2 := DefaultConfig()
	cfg2.Providers = []Provider{provider, provider2}
	cfg2.ActiveProviderID = provider.ID
	if err := store.Save(cfg2); err != nil {
		t.Fatalf("Save() cfg2 error = %v", err)
	}
	loaded2, err := store.Load()
	if err != nil {
		t.Fatalf("Load() cfg2 error = %v", err)
	}
	got2 := loaded2.GetProviderByID(provider2.ID)
	if got2 == nil {
		t.Fatal("provider2 missing after load")
	}
	if got2.RateLimitQueueEnabled {
		t.Fatal("provider2 RateLimitQueueEnabled = true, want false")
	}
	if got2.Retry429Enabled {
		t.Fatal("provider2 Retry429Enabled = true, want false")
	}
}

func TestSQLiteStorePersistsAdminThemeMode(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(tmpDir, "config.db"), filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

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

func TestSQLiteStoreAddsMultimodalColumnsToExistingProviderTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "proxy.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE providers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			api_url TEXT NOT NULL,
			api_token TEXT NOT NULL DEFAULT '',
			supports_thinking INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE provider_model_mappings (
			provider_id TEXT NOT NULL,
			source_model TEXT NOT NULL,
			target_model TEXT NOT NULL,
			PRIMARY KEY (provider_id, source_model)
		);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations(version, applied_at) VALUES (1, '2026-06-01T00:00:00Z');
		INSERT INTO providers(id, name, api_url, api_token, supports_thinking, enabled, created_at, updated_at)
		VALUES ('provider-a', 'Legacy', 'https://legacy.example.com/anthropic', 'token', 1, 1, '2026-06-01T00:00:00Z', '2026-06-01T00:00:00Z');
	`)
	if err != nil {
		db.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath, filepath.Join(dir, "missing-config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d", len(cfg.Providers))
	}
	if cfg.Providers[0].MultimodalSwitch || cfg.Providers[0].MultimodalModel != "" {
		t.Fatalf("legacy multimodal defaults = %#v", cfg.Providers[0])
	}
}

func TestSQLiteStoreLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "missing-config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.BackendURL != "https://open.bigmodel.cn/api/anthropic" {
		t.Fatalf("unexpected BackendURL: %s", cfg.BackendURL)
	}
	if cfg.ProxyPort != 443 {
		t.Fatalf("unexpected ProxyPort: %d", cfg.ProxyPort)
	}
	if cfg.AdminPort != 8442 {
		t.Fatalf("unexpected AdminPort: %d", cfg.AdminPort)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("unexpected DataDir: %s", cfg.DataDir)
	}
}

func TestSQLiteStoreMigratesLegacyJSONOnce(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	dbPath := filepath.Join(dir, "proxy.db")

	legacy := &Config{
		BackendURL:        "https://legacy.example.com/anthropic",
		ProxyPort:         1443,
		AdminPort:         18442,
		AdminPasswordHash: "legacy-hash",
		DataDir:           dir,
		ActiveProviderID:  "provider-a",
		AdminThemeMode:    ThemeModeDark,
		Providers: []Provider{
			testProvider("provider-a", "Legacy", "https://legacy-provider.example.com/anthropic", "legacy-token", true),
		},
	}
	if err := NewStore(jsonPath).Save(legacy); err != nil {
		t.Fatalf("save legacy json: %v", err)
	}
	before, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read legacy json before migration: %v", err)
	}

	store, err := NewSQLiteStore(dbPath, jsonPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertConfigEqual(t, legacy, loaded)

	backups, err := filepath.Glob(filepath.Join(dir, "config.json.bak-*"))
	if err != nil {
		t.Fatalf("glob backup: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected one backup, got %d", len(backups))
	}

	updated := *legacy
	updated.ActiveProviderID = ""
	if err := store.Save(&updated); err != nil {
		t.Fatalf("Save() after migration error = %v", err)
	}
	after, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read legacy json after sqlite save: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("legacy config.json changed after SQLite Save")
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	store, err = NewSQLiteStore(dbPath, jsonPath)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore() error = %v", err)
	}
	defer store.Close()
	backups, err = filepath.Glob(filepath.Join(dir, "config.json.bak-*"))
	if err != nil {
		t.Fatalf("glob backup after reopen: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected no additional backup after reopen, got %d backups", len(backups))
	}
}

func TestSQLiteStoreMigratesLegacyJSONWhenDBFileExistsWithoutSchema(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	dbPath := filepath.Join(dir, "proxy.db")

	legacy := &Config{
		BackendURL:       "https://legacy-empty-db.example.com/anthropic",
		ProxyPort:        1443,
		AdminPort:        18442,
		DataDir:          dir,
		ActiveProviderID: "provider-a",
		Providers: []Provider{
			testProvider("provider-a", "Legacy Empty DB", "https://legacy-empty-db-provider.example.com/anthropic", "legacy-token", true),
		},
	}
	if err := NewStore(jsonPath).Save(legacy); err != nil {
		t.Fatalf("save legacy json: %v", err)
	}
	file, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("create empty db file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close empty db file: %v", err)
	}

	store, err := NewSQLiteStore(dbPath, jsonPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertConfigEqual(t, legacy, loaded)
}

func TestSQLiteStoreRetriesLegacyJSONMigrationAfterInitialFailure(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	dbPath := filepath.Join(dir, "proxy.db")

	if err := os.WriteFile(jsonPath, []byte(`{"providers":`), 0644); err != nil {
		t.Fatalf("write invalid legacy json: %v", err)
	}
	if _, err := NewSQLiteStore(dbPath, jsonPath); err == nil {
		t.Fatal("expected initial migration to fail")
	}

	legacy := &Config{
		BackendURL:       "https://retry.example.com/anthropic",
		ProxyPort:        1443,
		AdminPort:        18442,
		DataDir:          dir,
		ActiveProviderID: "provider-a",
		Providers: []Provider{
			testProvider("provider-a", "Retry", "https://retry-provider.example.com/anthropic", "retry-token", true),
		},
	}
	if err := NewStore(jsonPath).Save(legacy); err != nil {
		t.Fatalf("write valid legacy json: %v", err)
	}

	store, err := NewSQLiteStore(dbPath, jsonPath)
	if err != nil {
		t.Fatalf("retry NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertConfigEqual(t, legacy, loaded)
}

func TestSQLiteStorePreservesActiveProviderSemantics(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	cfg := &Config{
		BackendURL:       "https://fallback.example.com/anthropic",
		ProxyPort:        443,
		AdminPort:        8442,
		DataDir:          dir,
		ActiveProviderID: "provider-b",
		Providers: []Provider{
			testProvider("provider-a", "A", "https://a.example.com/anthropic", "token-a", true),
			testProvider("provider-b", "B", "https://b.example.com/anthropic", "token-b", true),
		},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.GetActiveProvider(); got == nil || got.ID != "provider-b" {
		t.Fatalf("expected provider-b active, got %#v", got)
	}

	loaded.ActiveProviderID = "missing-provider"
	if err := store.Save(loaded); err != nil {
		t.Fatalf("Save() with missing active provider error = %v", err)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.GetActiveProvider(); got == nil || got.ID != "provider-a" {
		t.Fatalf("expected fallback to provider-a, got %#v", got)
	}
}

func TestSQLiteStoreSaveRollsBackOnMappingFailure(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "proxy.db")
	store, err := NewSQLiteStore(dbPath, filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	original := &Config{
		BackendURL:       "https://original.example.com/anthropic",
		ProxyPort:        443,
		AdminPort:        8442,
		DataDir:          dir,
		ActiveProviderID: "provider-a",
		Providers: []Provider{
			testProvider("provider-a", "Original", "https://original-provider.example.com/anthropic", "original-token", true),
		},
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("initial Save() error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite for trigger: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_provider_model_mapping_insert BEFORE INSERT ON provider_model_mappings BEGIN SELECT RAISE(ABORT, 'forced mapping failure'); END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	changed := &Config{
		BackendURL:       "https://changed.example.com/anthropic",
		ProxyPort:        1443,
		AdminPort:        18442,
		DataDir:          dir,
		ActiveProviderID: "provider-b",
		Providers: []Provider{
			testProvider("provider-b", "Changed", "https://changed-provider.example.com/anthropic", "changed-token", true),
		},
	}
	if err := store.Save(changed); err == nil {
		t.Fatal("expected Save() to fail")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_provider_model_mapping_insert`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertConfigEqual(t, original, loaded)
}

func TestSQLiteStorePersistsQuotaQueryConfig(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	provider := testProvider("provider-quota", "Quota", "https://quota.example.com/anthropic", "token", true)
	provider.QuotaQuery = &providerquota.ProviderQuotaConfig{
		Enabled:                  true,
		TemplateType:             providerquota.TemplateNewAPI,
		TimeoutSeconds:           15,
		AutoQueryIntervalMinutes: 10,
		BaseURL:                  "https://panel.example.com",
		AccessToken:              "secret-at",
		UserID:                   "user-1",
	}
	cfg := DefaultConfig()
	cfg.Providers = []Provider{provider}
	cfg.ActiveProviderID = provider.ID

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := loaded.GetProviderByID(provider.ID)
	if got == nil {
		t.Fatal("provider missing after load")
	}
	if got.QuotaQuery == nil {
		t.Fatal("QuotaQuery is nil after load")
	}
	if got.QuotaQuery.TemplateType != providerquota.TemplateNewAPI {
		t.Errorf("TemplateType = %q, want %q", got.QuotaQuery.TemplateType, providerquota.TemplateNewAPI)
	}
	if got.QuotaQuery.AccessToken != "secret-at" {
		t.Errorf("AccessToken = %q, want secret-at", got.QuotaQuery.AccessToken)
	}
	if got.QuotaQuery.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", got.QuotaQuery.UserID)
	}
	if got.QuotaQuery.TimeoutSeconds != 15 {
		t.Errorf("TimeoutSeconds = %d, want 15", got.QuotaQuery.TimeoutSeconds)
	}
	if got.QuotaQuery.AutoQueryIntervalMinutes != 10 {
		t.Errorf("AutoQueryIntervalMinutes = %d, want 10", got.QuotaQuery.AutoQueryIntervalMinutes)
	}

	// Verify nil QuotaQuery round-trips as nil.
	provider2 := testProvider("provider-no-quota", "NoQuota", "https://noquota.example.com/anthropic", "token2", true)
	cfg2 := DefaultConfig()
	cfg2.Providers = []Provider{provider, provider2}
	cfg2.ActiveProviderID = provider.ID
	if err := store.Save(cfg2); err != nil {
		t.Fatalf("Save() cfg2 error = %v", err)
	}
	loaded2, err := store.Load()
	if err != nil {
		t.Fatalf("Load() cfg2 error = %v", err)
	}
	got2 := loaded2.GetProviderByID(provider2.ID)
	if got2 == nil {
		t.Fatal("provider2 missing after load")
	}
	if got2.QuotaQuery != nil {
		t.Errorf("provider2 QuotaQuery = %v, want nil", got2.QuotaQuery)
	}
}

func TestSQLiteStoreQuotaQuerySecretsNotInPublicResponse(t *testing.T) {
	// Verify that the public config builder strips all secrets.
	cfg := &providerquota.ProviderQuotaConfig{
		Enabled:          true,
		TemplateType:     providerquota.TemplateNewAPI,
		APIKey:           "secret-key",
		AccessToken:      "secret-at",
		SecretAccessKey:  "secret-sk",
		AccessKeyID:      "AKLT1234",
		BaseURL:          "https://example.com",
		TimeoutSeconds:   10,
		AutoQueryIntervalMinutes: 5,
	}
	pub := providerquota.ToPublicConfig(cfg)
	if pub.APIKeyConfigured != true {
		t.Error("APIKeyConfigured should be true")
	}
	if pub.AccessTokenConfigured != true {
		t.Error("AccessTokenConfigured should be true")
	}
	if pub.SecretAccessKeyConfigured != true {
		t.Error("SecretAccessKeyConfigured should be true")
	}
	if pub.AccessKeyID != "****" {
		t.Errorf("AccessKeyID = %q, want **** (masked)", pub.AccessKeyID)
	}
}

func TestSQLiteStoreSnapshotTableCreated(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	// Verify that the provider_quota_snapshots table exists.
	var tableName string
	err = store.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='provider_quota_snapshots'`).Scan(&tableName)
	if err != nil {
		t.Fatalf("provider_quota_snapshots table not found: %v", err)
	}
	if tableName != "provider_quota_snapshots" {
		t.Errorf("table name = %q, want provider_quota_snapshots", tableName)
	}
}
