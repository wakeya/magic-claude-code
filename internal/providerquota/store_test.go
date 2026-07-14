package providerquota

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	// DSN mirrors production (internal/config/sqlite_store.go) so the test DB
	// runs under WAL + busy_timeout. Without these, concurrent read/write
	// (e.g. scanAndQuery's sequential Get vs. an async SaveUpsert) hits
	// database-wide rollback-journal locks and returns SQLITE_BUSY immediately.
	dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
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

// insertTestProvider adds a provider row so FK constraints on
// provider_quota_snapshots are satisfied during Manager tests.
func insertTestProvider(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	now := time.Now().Format(time.RFC3339Nano)
	_, err := db.Exec(`INSERT OR IGNORE INTO providers(id, name, api_url, api_token, enabled, created_at, updated_at) VALUES (?, ?, 'https://test.example.com', 'tok', 1, ?, ?)`,
		id, id, now, now)
	if err != nil {
		t.Fatalf("insert provider %s: %v", id, err)
	}
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

// TestSnapshotStoreConcurrentReadWriteNoBusy verifies that the WAL + busy_timeout
// pragma configuration (mirrored from production) prevents SQLITE_BUSY under
// concurrent readers and writers. This is a regression guard for the scheduler
// flaky where scanAndQuery's sequential Get raced an async SaveUpsert under the
// default rollback-journal mode and returned "database is locked" immediately.
//
// If someone reverts setupTestDB's DSN to drop WAL/busy_timeout, this test
// becomes flaky under rollback mode — which is the intended signal.
func TestSnapshotStoreConcurrentReadWriteNoBusy(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)

	// test-p is inserted by setupTestDB; add more provider rows so writers can
	// target distinct IDs while satisfying the provider FK.
	providerIDs := []string{"test-p"}
	for i := 0; i < 4; i++ {
		insertTestProvider(t, db, fmt.Sprintf("p%d", i))
		providerIDs = append(providerIDs, fmt.Sprintf("p%d", i))
	}

	var wg sync.WaitGroup
	var busy atomic.Int64
	var firstMu sync.Mutex
	var firstErr error // first non-BUSY store error; surfaced after wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	isBusy := func(err error) bool {
		if err == nil {
			return false
		}
		msg := err.Error()
		return strings.Contains(msg, "locked") || strings.Contains(msg, "busy")
	}

	// recordNonBusy keeps the first non-BUSY store error so unrelated storage
	// regressions cannot pass silently under this test's BUSY-only lens.
	recordNonBusy := func(err error) {
		firstMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		firstMu.Unlock()
	}

	// Writers: SaveUpsert on each provider in a tight loop.
	for i := range providerIDs {
		id := providerIDs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r := &ProviderQuotaResult{
					ProviderID:   id,
					Success:      true,
					Balances:     []BalanceItem{{Remaining: floatPtr(float64(j)), Unit: "USD"}},
					QueriedAt:    time.Now(),
					DurationMS:   int64(j),
				}
				if err := store.SaveUpsert(id, r); err != nil {
					if isBusy(err) {
						busy.Add(1)
					} else {
						recordNonBusy(err)
					}
				}
				if ctx.Err() != nil {
					return
				}
			}
		}()
	}

	// Readers: GetAll in a tight loop, contending with writers for the DB.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if _, err := store.GetAll(); err != nil {
					if isBusy(err) {
						busy.Add(1)
					} else {
						recordNonBusy(err)
					}
				}
				if ctx.Err() != nil {
					return
				}
			}
		}()
	}

	wg.Wait()
	if got := busy.Load(); got != 0 {
		t.Fatalf("encountered %d SQLITE_BUSY errors under WAL+busy_timeout (expected 0)", got)
	}
	if firstErr != nil {
		t.Fatalf("non-BUSY store error surfaced (would have been silently ignored before): %v", firstErr)
	}
}
