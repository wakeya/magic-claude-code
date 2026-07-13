package config

import (
	"path/filepath"
	"testing"
)

func seedProviders(t *testing.T, store *SQLiteStore, ids ...string) {
	t.Helper()
	_, err := store.Update(func(cfg *Config) error {
		cfg.Providers = make([]Provider, 0, len(ids))
		for _, id := range ids {
			cfg.Providers = append(cfg.Providers, Provider{
				ID: id, Name: id, APIURL: "https://" + id, APIFormat: APIFormatAnthropic, Enabled: true,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func providerIDs(ps []Provider) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

// TestSQLiteProviderOrderRoundTrip 验证 Update 重排 Providers 后，重新 Load 顺序不变。
func TestSQLiteProviderOrderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.db")
	store, err := NewSQLiteStore(path, "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	seedProviders(t, store, "a", "b", "c")

	// 拖拽式重排：[a, b, c] -> [c, a, b]
	if _, err := store.Update(func(cfg *Config) error {
		cfg.Providers = []Provider{(*cfg.GetProviderByID("c")), (*cfg.GetProviderByID("a")), (*cfg.GetProviderByID("b"))}
		return nil
	}); err != nil {
		t.Fatalf("reorder update: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := providerIDs(loaded.Providers)
	want := []string{"c", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestSQLiteProviderOrderSurvivesReopen 验证关闭并重开 store 后顺序仍稳定（sort_order 持久化）。
func TestSQLiteProviderOrderSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.db")
	store, err := NewSQLiteStore(path, "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	seedProviders(t, store, "a", "b", "c", "d")
	if _, err := store.Update(func(cfg *Config) error {
		cfg.Providers = []Provider{
			(*cfg.GetProviderByID("d")), (*cfg.GetProviderByID("a")), (*cfg.GetProviderByID("c")), (*cfg.GetProviderByID("b")),
		}
		return nil
	}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	store.Close()

	store2, err := NewSQLiteStore(path, "")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()
	loaded, err := store2.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := providerIDs(loaded.Providers)
	want := []string{"d", "a", "c", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order after reopen = %v, want %v", got, want)
		}
	}
}

// TestSQLiteProviderOrderOldDBFallsBackToCreatedAt 验证老 DB（所有 sort_order=0）
// 初始顺序仍由 created_at ASC, id ASC 决定，不被破坏。
func TestSQLiteProviderOrderOldDBFallsBackToCreatedAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.db")
	store, err := NewSQLiteStore(path, "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	// 按顺序种入；created_at 单调递增（Update 用 time.Now）。
	seedProviders(t, store, "first", "second", "third")
	// 人为把所有 sort_order 抹回 0，模拟老 DB。
	if _, err := store.DB().Exec(`UPDATE providers SET sort_order = 0`); err != nil {
		t.Fatalf("reset sort_order: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := providerIDs(loaded.Providers)
	// 全 0 时退回 created_at ASC → 种入顺序。
	want := []string{"first", "second", "third"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("old-DB order = %v, want %v (created_at fallback)", got, want)
		}
	}
}
