package config

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if len(got.Providers) != len(want.Providers) {
		t.Fatalf("Providers length mismatch: want %d, got %d", len(want.Providers), len(got.Providers))
	}
	for i := range want.Providers {
		wp := want.Providers[i]
		gp := got.Providers[i]
		if gp.ID != wp.ID || gp.Name != wp.Name || gp.APIURL != wp.APIURL || gp.APIToken != wp.APIToken || gp.SupportsThinking != wp.SupportsThinking || gp.Enabled != wp.Enabled {
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
