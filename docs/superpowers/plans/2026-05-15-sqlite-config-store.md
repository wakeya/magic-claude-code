# SQLite Config Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 provider 和全局配置从 `data/config.json` 迁移到 `<dataDir>/proxy.db`，同时保持现有 `ConfigStore` 接口、前端 API 和 provider 切换语义不变。

**Architecture:** 新增 `internal/config/SQLiteStore` 实现现有 `ConfigStore` 接口，启动时通过 `NewSQLiteStore(dbPath, legacyJSONPath)` 初始化 schema 并执行一次性 JSON 导入。JSON store 保留为迁移来源和测试兼容，服务启动后 SQLite 是唯一写入目标。

**Tech Stack:** Go `database/sql`、pure Go SQLite driver `modernc.org/sqlite`、现有 `config.ConfigStore` 接口、现有 Go 单元测试。

**Spec:** [2026-05-15-sqlite-config-store-design.md](../specs/2026-05-15-sqlite-config-store-design.md)

---

## File Map

| 文件 | 动作 | 责任 |
|------|------|------|
| `internal/config/sqlite_store.go` | Create | SQLite schema、迁移、Load/Save 实现、JSON 导入和备份 |
| `internal/config/sqlite_store_test.go` | Create | SQLite store TDD 覆盖基础读写、默认值、迁移、事务、provider 切换语义 |
| `cmd/server/main.go` | Modify | 将启动配置 store 从 JSON 切换到 SQLite |
| `go.mod` / `go.sum` | Modify | 增加 `modernc.org/sqlite` 依赖 |
| `internal/config/store.go` | Keep | 不改接口；JSON store 保留为 legacy migration source |
| `Dockerfile` | Keep | 不改；依赖必须兼容 `CGO_ENABLED=0` |

---

## Task 1: Add SQLite Dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Add pure Go SQLite driver**

Run:

```bash
go get modernc.org/sqlite@latest
```

Expected: `go.mod` contains `modernc.org/sqlite`, and `go.sum` is updated.

- [ ] **Step 2: Verify dependency resolves**

Run:

```bash
go list -m modernc.org/sqlite
```

Expected: prints a selected `modernc.org/sqlite` version.

- [ ] **Step 3: Commit dependency change**

```bash
git add go.mod go.sum
git commit -m "chore: add sqlite driver dependency"
```

---

## Task 2: Write SQLite Store Tests First

**Files:**
- Create: `internal/config/sqlite_store_test.go`

- [ ] **Step 1: Create test file with helper assertions**

Add `internal/config/sqlite_store_test.go`:

```go
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
```

- [ ] **Step 2: Add basic Save/Load test**

Append:

```go
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
```

- [ ] **Step 3: Add default config test**

Append:

```go
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
```

- [ ] **Step 4: Add JSON migration test**

Append:

```go
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
}
```

- [ ] **Step 5: Add provider switching and fallback semantics test**

Append:

```go
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
```

- [ ] **Step 6: Add rollback-on-failed-save test**

Append:

```go
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
```

- [ ] **Step 7: Run tests and confirm they fail before implementation**

Run:

```bash
env GOCACHE=/tmp/go-build go test ./internal/config -run SQLiteStore -count=1
```

Expected: FAIL because `NewSQLiteStore` and `SQLiteStore.Close` are not defined.

---

## Task 3: Implement SQLiteStore Skeleton and Schema

**Files:**
- Create: `internal/config/sqlite_store.go`

- [ ] **Step 1: Create SQLiteStore type and constructor**

Add `internal/config/sqlite_store.go`:

```go
package config

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteSchemaVersion = 1

type SQLiteStore struct {
	mu             sync.Mutex
	db             *sql.DB
	dbPath         string
	legacyJSONPath string
}

var _ ConfigStore = (*SQLiteStore)(nil)

func NewSQLiteStore(dbPath string, legacyJSONPath string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}

	_, statErr := os.Stat(dbPath)
	dbExisted := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	s := &SQLiteStore{db: db, dbPath: dbPath, legacyJSONPath: legacyJSONPath}
	if err := s.init(dbExisted); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
```

- [ ] **Step 2: Add schema migration**

Append:

```go
func (s *SQLiteStore) init(dbExisted bool) error {
	if _, err := s.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return err
	}
	if err := s.migrateSchema(); err != nil {
		return err
	}
	shouldMigrate, err := s.shouldMigrateLegacyJSON(dbExisted)
	if err != nil {
		return err
	}
	if shouldMigrate {
		return s.migrateLegacyJSON()
	}
	return nil
}

func (s *SQLiteStore) migrateSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS providers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			api_url TEXT NOT NULL,
			api_token TEXT NOT NULL DEFAULT '',
			supports_thinking INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS provider_model_mappings (
			provider_id TEXT NOT NULL,
			source_model TEXT NOT NULL,
			target_model TEXT NOT NULL,
			PRIMARY KEY (provider_id, source_model),
			FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
		sqliteSchemaVersion,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}
```

- [ ] **Step 3: Run skeleton tests**

Run:

```bash
env GOCACHE=/tmp/go-build go test ./internal/config -run SQLiteStore -count=1
```

Expected: still FAIL because `Load`, `Save`, and legacy migration helpers are missing.

---

## Task 4: Implement Load

**Files:**
- Modify: `internal/config/sqlite_store.go`

- [ ] **Step 1: Add Load and read helpers**

Append:

```go
func (s *SQLiteStore) Load() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := DefaultConfig()

	settings, err := s.loadSettings()
	if err != nil {
		return nil, err
	}
	if v := settings["backend_url"]; v != "" {
		cfg.BackendURL = v
	}
	if v := settings["proxy_port"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ProxyPort = n
		}
	}
	if v := settings["admin_port"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.AdminPort = n
		}
	}
	cfg.AdminPasswordHash = settings["admin_password_hash"]
	if v := settings["data_dir"]; v != "" {
		cfg.DataDir = v
	}
	cfg.ActiveProviderID = settings["active_provider_id"]

	providers, err := s.loadProviders()
	if err != nil {
		return nil, err
	}
	cfg.Providers = providers
	return cfg, nil
}

func (s *SQLiteStore) loadSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		settings[key] = value
	}
	return settings, rows.Err()
}
```

- [ ] **Step 2: Add provider read helper**

Append:

```go
func (s *SQLiteStore) loadProviders() ([]Provider, error) {
	rows, err := s.db.Query(`SELECT id, name, api_url, api_token, supports_thinking, enabled, created_at, updated_at FROM providers ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []Provider
	for rows.Next() {
		var p Provider
		var supportsThinking, enabled int
		var createdAt, updatedAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.APIURL, &p.APIToken, &supportsThinking, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.SupportsThinking = supportsThinking == 1
		p.Enabled = enabled == 1
		p.CreatedAt = parseSQLiteTime(createdAt)
		p.UpdatedAt = parseSQLiteTime(updatedAt)
		p.ModelMappings = make(map[string]string)
		providers = append(providers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range providers {
		mappings, err := s.loadModelMappings(providers[i].ID)
		if err != nil {
			return nil, err
		}
		providers[i].ModelMappings = mappings
	}
	return providers, nil
}

func (s *SQLiteStore) loadModelMappings(providerID string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT source_model, target_model FROM provider_model_mappings WHERE provider_id = ? ORDER BY source_model ASC`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mappings := make(map[string]string)
	for rows.Next() {
		var source, target string
		if err := rows.Scan(&source, &target); err != nil {
			return nil, err
		}
		mappings[source] = target
	}
	return mappings, rows.Err()
}

func parseSQLiteTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
```

- [ ] **Step 3: Run Load tests**

Run:

```bash
env GOCACHE=/tmp/go-build go test ./internal/config -run 'TestSQLiteStoreLoadDefaults' -count=1
```

Expected: PASS after `Load()` exists and schema creates empty tables.

---

## Task 5: Implement Save Transaction

**Files:**
- Modify: `internal/config/sqlite_store.go`

- [ ] **Step 1: Add Save method**

Append:

```go
func (s *SQLiteStore) Save(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := saveSettings(tx, cfg); err != nil {
		return err
	}
	if err := saveProviders(tx, cfg.Providers); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [ ] **Step 2: Add settings writer**

Append:

```go
func saveSettings(tx *sql.Tx, cfg *Config) error {
	settings := map[string]string{
		"backend_url":         cfg.BackendURL,
		"proxy_port":          strconv.Itoa(cfg.ProxyPort),
		"admin_port":          strconv.Itoa(cfg.AdminPort),
		"admin_password_hash": cfg.AdminPasswordHash,
		"data_dir":            cfg.DataDir,
		"active_provider_id":  cfg.ActiveProviderID,
	}
	for key, value := range settings {
		if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 3: Add provider writer**

Append:

```go
func saveProviders(tx *sql.Tx, providers []Provider) error {
	keep := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		keep[provider.ID] = struct{}{}
	}

	existingRows, err := tx.Query(`SELECT id FROM providers`)
	if err != nil {
		return err
	}
	var existing []string
	for existingRows.Next() {
		var id string
		if err := existingRows.Scan(&id); err != nil {
			existingRows.Close()
			return err
		}
		existing = append(existing, id)
	}
	if err := existingRows.Err(); err != nil {
		existingRows.Close()
		return err
	}
	existingRows.Close()

	for _, id := range existing {
		if _, ok := keep[id]; !ok {
			if _, err := tx.Exec(`DELETE FROM providers WHERE id = ?`, id); err != nil {
				return err
			}
		}
	}

	for _, provider := range providers {
		if err := upsertProvider(tx, provider); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM provider_model_mappings WHERE provider_id = ?`, provider.ID); err != nil {
			return err
		}
		for source, target := range provider.ModelMappings {
			if _, err := tx.Exec(`INSERT INTO provider_model_mappings(provider_id, source_model, target_model) VALUES (?, ?, ?)`, provider.ID, source, target); err != nil {
				return err
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Add provider upsert helper**

Append:

```go
func upsertProvider(tx *sql.Tx, provider Provider) error {
	createdAt := provider.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := provider.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	_, err := tx.Exec(
		`INSERT INTO providers(id, name, api_url, api_token, supports_thinking, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		 name = excluded.name,
		 api_url = excluded.api_url,
		 api_token = excluded.api_token,
		 supports_thinking = excluded.supports_thinking,
		 enabled = excluded.enabled,
		 created_at = excluded.created_at,
		 updated_at = excluded.updated_at`,
		provider.ID,
		provider.Name,
		provider.APIURL,
		provider.APIToken,
		boolToInt(provider.SupportsThinking),
		boolToInt(provider.Enabled),
		createdAt.UTC().Format(time.RFC3339Nano),
		updatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
```

- [ ] **Step 5: Run Save/Load and rollback tests**

Run:

```bash
env GOCACHE=/tmp/go-build go test ./internal/config -run 'TestSQLiteStore(SaveAndLoad|PreservesActiveProviderSemantics|SaveRollsBackOnMappingFailure)' -count=1
```

Expected: PASS.

---

## Task 6: Implement Legacy JSON Migration

**Files:**
- Modify: `internal/config/sqlite_store.go`

- [ ] **Step 1: Add migration decision helper**

Append:

```go
func (s *SQLiteStore) shouldMigrateLegacyJSON(dbExisted bool) (bool, error) {
	if dbExisted {
		return false, nil
	}
	if s.legacyJSONPath == "" {
		return false, nil
	}
	if _, err := os.Stat(s.legacyJSONPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
```

- [ ] **Step 2: Add migration and backup implementation**

Append:

```go
func (s *SQLiteStore) migrateLegacyJSON() error {
	cfg, err := NewStore(s.legacyJSONPath).Load()
	if err != nil {
		return err
	}
	if err := s.Save(cfg); err != nil {
		return err
	}
	return backupFile(s.legacyJSONPath)
}

func backupFile(path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	backupPath := path + ".bak-" + time.Now().UTC().Format("20060102150405")
	dst, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}
```

- [ ] **Step 3: Remove unused imports or add missing imports**

Run:

```bash
gofmt -w internal/config/sqlite_store.go internal/config/sqlite_store_test.go
```

Expected: files format cleanly with no unused imports.

- [ ] **Step 4: Run migration tests**

Run:

```bash
env GOCACHE=/tmp/go-build go test ./internal/config -run 'TestSQLiteStoreMigratesLegacyJSONOnce' -count=1
```

Expected: PASS, including backup creation and JSON non-mutation after SQLite `Save()`.

---

## Task 7: Switch Server Startup to SQLiteStore

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Replace store initialization**

Change:

```go
configPath := filepath.Join(*dataDir, "config.json")
configStore := config.NewStore(configPath)
cfg, err := configStore.Load()
if err != nil {
	log.Fatalf("Failed to load config: %v", err)
}
```

To:

```go
configPath := filepath.Join(*dataDir, "config.json")
dbPath := filepath.Join(*dataDir, "proxy.db")
configStore, err := config.NewSQLiteStore(dbPath, configPath)
if err != nil {
	log.Fatalf("Failed to initialize config store: %v", err)
}
defer configStore.Close()

cfg, err := configStore.Load()
if err != nil {
	log.Fatalf("Failed to load config: %v", err)
}
```

- [ ] **Step 2: Keep admin config path unchanged**

Verify this block still passes `configPath`:

```go
adminServer := admin.NewServer(&admin.AdminConfig{
	Password:   *adminPassword,
	CertFile:   certManager.GetServerCertPath(),
	KeyFile:    filepath.Join(*dataDir, "server.key"),
	ConfigPath: configPath,
}, configStore, proxyServer)
```

Expected: compile succeeds and admin code still receives the legacy JSON path for existing display/compat fields.

- [ ] **Step 3: Format main**

Run:

```bash
gofmt -w cmd/server/main.go
```

Expected: no output.

---

## Task 8: Run Focused Tests and Full Test Suite

**Files:**
- Test: `internal/config/sqlite_store_test.go`
- Test: all packages

- [ ] **Step 1: Run config package tests**

Run:

```bash
env GOCACHE=/tmp/go-build go test ./internal/config -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full Go tests**

Run:

```bash
env GOCACHE=/tmp/go-build go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit SQLite store implementation**

```bash
git add internal/config/sqlite_store.go internal/config/sqlite_store_test.go cmd/server/main.go go.mod go.sum
git commit -m "feat: migrate config store to sqlite"
```

---

## Task 9: Manual Migration Verification

**Files:**
- Runtime data: `data/config.json`
- Runtime data: `data/proxy.db`

- [ ] **Step 1: Build and start container**

Run:

```bash
docker compose up -d --build
```

Expected: service starts successfully.

- [ ] **Step 2: Confirm SQLite database exists**

Run:

```bash
test -f data/proxy.db && echo "proxy.db exists"
```

Expected:

```text
proxy.db exists
```

- [ ] **Step 3: Confirm JSON backup exists when legacy JSON existed**

Run:

```bash
ls data/config.json data/config.json.bak-* 2>/dev/null
```

Expected: `data/config.json` exists; at least one `data/config.json.bak-*` exists if this was the first migration from JSON.

- [ ] **Step 4: Confirm provider switching still works**

Manual:

1. Open admin page.
2. Click a non-active provider's “设为当前”.
3. Send a new Claude Code request.
4. Check container logs.

Expected log pattern:

```text
Model mapping: <source model> -> <target model> (provider: <new provider name>)
```

- [ ] **Step 5: Confirm JSON is no longer mutated**

Run before and after a provider switch:

```bash
sha256sum data/config.json
```

Expected: hash does not change after provider switch; SQLite is the active write target.

---

## Task 10: Final Verification and Handoff

**Files:**
- Validate: `docs/superpowers/validate/2026-05-15-sqlite-config-store-verification.md`

- [ ] **Step 1: Complete validation checklist**

Open:

```text
docs/superpowers/validate/2026-05-15-sqlite-config-store-verification.md
```

Mark each passed item with `[x]`.

- [ ] **Step 2: Confirm no unintended scope expansion**

Run:

```bash
git diff --stat HEAD
```

Expected: changes are limited to SQLite config store implementation, dependency files, and planned docs. No usage statistics code is included in this phase.

- [ ] **Step 3: Commit validation result if the checklist is updated**

```bash
git add docs/superpowers/validate/2026-05-15-sqlite-config-store-verification.md
git commit -m "docs: add sqlite config store verification results"
```
