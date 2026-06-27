package providerquota

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create required tables.
	_, err = db.Exec(`
		CREATE TABLE providers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			api_url TEXT NOT NULL,
			api_token TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE provider_quota_snapshots (
			provider_id TEXT PRIMARY KEY,
			result_json TEXT NOT NULL,
			last_success_json TEXT,
			queried_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	// Insert a test provider.
	_, err = db.Exec(`INSERT INTO providers(id, name, api_url, api_token, enabled, created_at, updated_at) VALUES ('test-p', 'Test', 'https://test.example.com', 'tok', 1, ?, ?)`,
		time.Now().Format(time.RFC3339Nano), time.Now().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("insert provider: %v", err)
	}

	return db
}

func TestSnapshotStoreSaveAndGet(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)

	result := &ProviderQuotaResult{
		ProviderID:   "test-p",
		TemplateType: TemplateGeneral,
		Success:      true,
		Balances: []BalanceItem{
			{Remaining: floatPtr(12.34), Unit: "USD"},
		},
		QueriedAt:  time.Now(),
		DurationMS: 150,
	}

	if err := store.SaveUpsert("test-p", result); err != nil {
		t.Fatalf("SaveUpsert: %v", err)
	}

	snap, err := store.Get("test-p")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.ProviderID != "test-p" {
		t.Errorf("provider_id = %q, want test-p", snap.ProviderID)
	}
	if !snap.Result.Success {
		t.Error("expected success")
	}
	if snap.LastSuccess == nil {
		t.Error("expected last_success to be set for success result")
	}
}

func TestSnapshotStoreFailurePreservesSuccess(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)

	// Save a success.
	successResult := &ProviderQuotaResult{
		ProviderID: "test-p",
		Success:    true,
		Balances:   []BalanceItem{{Remaining: floatPtr(10), Unit: "USD"}},
		QueriedAt:  time.Now(),
		DurationMS: 100,
	}
	if err := store.SaveUpsert("test-p", successResult); err != nil {
		t.Fatalf("SaveUpsert success: %v", err)
	}

	// Save a failure.
	failResult := &ProviderQuotaResult{
		ProviderID:   "test-p",
		Success:      false,
		ErrorCode:    "invalid_credentials",
		ErrorMessage: "HTTP 401",
		QueriedAt:    time.Now(),
		DurationMS:   50,
	}
	if err := store.SaveUpsert("test-p", failResult); err != nil {
		t.Fatalf("SaveUpsert failure: %v", err)
	}

	snap, err := store.Get("test-p")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if snap.Result.Success {
		t.Error("latest result should be failure")
	}
	if snap.LastSuccess == nil {
		t.Fatal("last_success should be preserved after failure")
	}
	if !snap.LastSuccess.Success {
		t.Error("last_success should be success")
	}
}

func TestSnapshotStoreDelete(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)

	result := &ProviderQuotaResult{
		ProviderID: "test-p",
		Success:    true,
		Balances:   []BalanceItem{{Remaining: floatPtr(10), Unit: "USD"}},
		QueriedAt:  time.Now(),
		DurationMS: 100,
	}
	store.SaveUpsert("test-p", result)

	if err := store.Delete("test-p"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	snap, err := store.Get("test-p")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if snap != nil {
		t.Error("expected nil after delete")
	}
}

func TestSnapshotStoreGetAll(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)

	result := &ProviderQuotaResult{
		ProviderID: "test-p",
		Success:    true,
		Balances:   []BalanceItem{{Remaining: floatPtr(10), Unit: "USD"}},
		QueriedAt:  time.Now(),
		DurationMS: 100,
	}
	store.SaveUpsert("test-p", result)

	all, err := store.GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(all))
	}
	if all["test-p"] == nil {
		t.Error("expected test-p in all")
	}
}

func TestSnapshotStoreGetNonExistent(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)

	snap, err := store.Get("nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if snap != nil {
		t.Error("expected nil for non-existent provider")
	}
}

func init() {
	// Silence log output in tests.
	_ = os.DevNull
}
