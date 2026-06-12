package config

import (
	"database/sql"
	"encoding/json"
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
const legacyJSONMigratedAtKey = "legacy_json_migrated_at"

type SQLiteStore struct {
	mu             sync.Mutex
	db             *sql.DB
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

	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)

	store := &SQLiteStore{
		db:             db,
		legacyJSONPath: legacyJSONPath,
	}
	if err := store.init(dbExisted); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

func (s *SQLiteStore) init(dbExisted bool) error {
	schemaInitialized, err := s.hasSchemaVersion()
	if err != nil {
		return err
	}
	if !dbExisted {
		schemaInitialized = false
	}
	if err := s.migrateSchema(); err != nil {
		return err
	}
	shouldMigrate, err := s.shouldMigrateLegacyJSON(schemaInitialized)
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
			multimodal_switch INTEGER NOT NULL DEFAULT 0,
			multimodal_model TEXT NOT NULL DEFAULT '',
			claude_code_compat_hint INTEGER,
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
	if err := s.ensureProviderColumns(); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
		sqliteSchemaVersion,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) ensureProviderColumns() error {
	columns := map[string]string{
		"multimodal_switch":       `ALTER TABLE providers ADD COLUMN multimodal_switch INTEGER NOT NULL DEFAULT 0`,
		"multimodal_model":        `ALTER TABLE providers ADD COLUMN multimodal_model TEXT NOT NULL DEFAULT ''`,
		"api_format":              `ALTER TABLE providers ADD COLUMN api_format TEXT NOT NULL DEFAULT 'anthropic'`,
		"openai_extra_params":     `ALTER TABLE providers ADD COLUMN openai_extra_params TEXT NOT NULL DEFAULT '{}'`,
		"claude_code_compat_hint": `ALTER TABLE providers ADD COLUMN claude_code_compat_hint INTEGER`,
	}
	rows, err := s.db.Query(`PRAGMA table_info(providers)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for name, stmt := range columns {
		if _, ok := existing[name]; !ok {
			if _, err := s.db.Exec(stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteStore) hasSchemaVersion() (bool, error) {
	var tableName string
	err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, sqliteSchemaVersion).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

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
	cfg.AdminThemeMode = NormalizeThemeMode(settings["admin_theme_mode"])

	providers, err := s.loadProviders()
	if err != nil {
		return nil, err
	}
	cfg.Providers = providers
	return cfg, nil
}

func (s *SQLiteStore) Save(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(cfg)
}

func (s *SQLiteStore) save(cfg *Config) error {
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

func (s *SQLiteStore) loadProviders() ([]Provider, error) {
	rows, err := s.db.Query(`SELECT id, name, api_url, api_token, api_format, openai_extra_params, supports_thinking, multimodal_switch, multimodal_model, claude_code_compat_hint, enabled, created_at, updated_at FROM providers ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []Provider
	for rows.Next() {
		var p Provider
		var supportsThinking, multimodalSwitch, enabled int
		var claudeCodeCompatHint sql.NullBool
		var openAIExtraParams, createdAt, updatedAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.APIURL, &p.APIToken, &p.APIFormat, &openAIExtraParams, &supportsThinking, &multimodalSwitch, &p.MultimodalModel, &claudeCodeCompatHint, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.normalizeDefaults()
		params, err := decodeOpenAIExtraParams(openAIExtraParams)
		if err != nil {
			return nil, fmt.Errorf("decode provider %s openai_extra_params: %w", p.ID, err)
		}
		p.OpenAIExtraParams = params
		p.SupportsThinking = supportsThinking == 1
		p.MultimodalSwitch = multimodalSwitch == 1
		if claudeCodeCompatHint.Valid {
			p.ClaudeCodeCompatHint = &claudeCodeCompatHint.Bool
		}
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

func saveSettings(tx *sql.Tx, cfg *Config) error {
	settings := map[string]string{
		"backend_url":         cfg.BackendURL,
		"proxy_port":          strconv.Itoa(cfg.ProxyPort),
		"admin_port":          strconv.Itoa(cfg.AdminPort),
		"admin_password_hash": cfg.AdminPasswordHash,
		"data_dir":            cfg.DataDir,
		"active_provider_id":  cfg.ActiveProviderID,
		"admin_theme_mode":    NormalizeThemeMode(cfg.AdminThemeMode),
	}
	for key, value := range settings {
		if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}
	return nil
}

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

func upsertProvider(tx *sql.Tx, provider Provider) error {
	provider.normalizeDefaults()
	createdAt := provider.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := provider.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	openAIExtraParams, err := encodeOpenAIExtraParams(provider.OpenAIExtraParams)
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		`INSERT INTO providers(id, name, api_url, api_token, api_format, openai_extra_params, supports_thinking, multimodal_switch, multimodal_model, claude_code_compat_hint, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		 name = excluded.name,
		 api_url = excluded.api_url,
		 api_token = excluded.api_token,
		 api_format = excluded.api_format,
		 openai_extra_params = excluded.openai_extra_params,
		 supports_thinking = excluded.supports_thinking,
		 multimodal_switch = excluded.multimodal_switch,
		 multimodal_model = excluded.multimodal_model,
		 claude_code_compat_hint = excluded.claude_code_compat_hint,
		 enabled = excluded.enabled,
		 created_at = excluded.created_at,
		 updated_at = excluded.updated_at`,
		provider.ID,
		provider.Name,
		provider.APIURL,
		provider.APIToken,
		provider.APIFormat,
		openAIExtraParams,
		boolToInt(provider.SupportsThinking),
		boolToInt(provider.MultimodalSwitch),
		provider.MultimodalModel,
		nullableBool(provider.ClaudeCodeCompatHint),
		boolToInt(provider.Enabled),
		createdAt.UTC().Format(time.RFC3339Nano),
		updatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func encodeOpenAIExtraParams(params map[string]any) (string, error) {
	if params == nil {
		return "{}", nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("encode openai_extra_params: %w", err)
	}
	return string(data), nil
}

func decodeOpenAIExtraParams(value string) (map[string]any, error) {
	if value == "" {
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(value), &params); err != nil {
		return nil, err
	}
	if len(params) == 0 {
		return nil, nil
	}
	return params, nil
}

func (s *SQLiteStore) shouldMigrateLegacyJSON(schemaInitialized bool) (bool, error) {
	if s.legacyJSONPath == "" {
		return false, nil
	}
	if _, err := os.Stat(s.legacyJSONPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !schemaInitialized {
		return true, nil
	}
	var migratedAt string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, legacyJSONMigratedAtKey).Scan(&migratedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

func (s *SQLiteStore) migrateLegacyJSON() error {
	cfg, err := NewStore(s.legacyJSONPath).Load()
	if err != nil {
		return err
	}
	if err := s.save(cfg); err != nil {
		return err
	}
	if err := backupFile(s.legacyJSONPath); err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		legacyJSONMigratedAtKey,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
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

func parseSQLiteTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return boolToInt(*value)
}
