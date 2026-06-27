package providerquota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockConfigGet implements ProviderConfigGetter for testing.
type mockConfigGet struct {
	providers map[string]ProviderConfig
}

func (m *mockConfigGet) GetProviderByID(id string) *ProviderConfig {
	p, ok := m.providers[id]
	if !ok {
		return nil
	}
	return &p
}

func (m *mockConfigGet) ListEnabledProviders() []ProviderConfig {
	var result []ProviderConfig
	for _, p := range m.providers {
		if p.Enabled {
			result = append(result, p)
		}
	}
	return result
}

func TestManagerDeduplicatesConcurrentQueries(t *testing.T) {
	var queryCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryCount.Add(1)
		// Small delay to ensure concurrent requests overlap.
		time.Sleep(50 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{"balance": 100})
	}))
	defer srv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {
				ID:       "p1",
				Enabled:  true,
				APIURL:   srv.URL,
				APIToken: "test-token",
				QuotaQuery: &ProviderQuotaConfig{
					Enabled:      true,
					TemplateType: TemplateGeneral,
					Script: `({
						request: { url: "{{baseUrl}}/balance", method: "GET", headers: { "Authorization": "Bearer {{apiKey}}" } },
						extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
					})`,
					TimeoutSeconds: 10,
				},
			},
		},
	}

	mgr := NewManager(store, configGet, 4)

	// Launch 10 concurrent queries for the same provider.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mgr.Query(context.Background(), "p1", QueryOptions{})
		}()
	}
	wg.Wait()

	// Should only have made 1 upstream request (deduplication).
	count := queryCount.Load()
	if count != 1 {
		t.Errorf("upstream requests = %d, want 1 (deduplication)", count)
	}
}

func TestManagerRespectsConcurrencyLimit(t *testing.T) {
	var maxConcurrent atomic.Int32
	var currentConcurrent atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := currentConcurrent.Add(1)
		// Track max concurrent.
		for {
			old := maxConcurrent.Load()
			if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
		currentConcurrent.Add(-1)
		json.NewEncoder(w).Encode(map[string]any{"balance": 1})
	}))
	defer srv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)

	// Create 10 different providers.
	providers := make(map[string]ProviderConfig)
	for i := 0; i < 10; i++ {
		id := string(rune('a'+i)) + "-provider"
		providers[id] = ProviderConfig{
			ID:       id,
			Enabled:  true,
			APIURL:   srv.URL,
			APIToken: "tok",
			QuotaQuery: &ProviderQuotaConfig{
				Enabled:      true,
				TemplateType: TemplateGeneral,
				Script: `({
					request: { url: "{{baseUrl}}/balance", method: "GET" },
					extractor: function(r) { return { remaining: 1, unit: "USD" }; }
				})`,
				TimeoutSeconds: 10,
			},
		}
	}

	configGet := &mockConfigGet{providers: providers}
	mgr := NewManager(store, configGet, 4) // Max 4 concurrent.

	var wg sync.WaitGroup
	for id := range providers {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			_, _ = mgr.Query(context.Background(), pid, QueryOptions{})
		}(id)
	}
	wg.Wait()

	if maxConcurrent.Load() > 4 {
		t.Errorf("max concurrent = %d, want <= 4", maxConcurrent.Load())
	}
}

func TestManagerNotConfiguredResult(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {ID: "p1", Enabled: true, QuotaQuery: nil},
		},
	}
	mgr := NewManager(store, configGet, 4)

	result, err := mgr.Query(context.Background(), "p1", QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for unconfigured provider")
	}
	if result.ErrorCode != "not_configured" {
		t.Errorf("error_code = %q, want not_configured", result.ErrorCode)
	}
}

func TestManagerDisabledProvider(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {ID: "p1", Enabled: false},
		},
	}
	mgr := NewManager(store, configGet, 4)

	result, err := mgr.Query(context.Background(), "p1", QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for disabled provider")
	}
}

func TestManagerTestQueryDoesNotPersist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"balance": 100})
	}))
	defer srv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {
				ID:       "p1",
				Enabled:  true,
				APIURL:   srv.URL,
				APIToken: "tok",
				QuotaQuery: &ProviderQuotaConfig{
					Enabled:      true,
					TemplateType: TemplateGeneral,
					Script: `({
						request: { url: "{{baseUrl}}/balance", method: "GET" },
						extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
					})`,
					TimeoutSeconds: 10,
				},
			},
		},
	}
	mgr := NewManager(store, configGet, 4)

	// Test query with Draft (should not persist).
	result, err := mgr.Query(context.Background(), "p1", QueryOptions{
		Draft: &ProviderQuotaConfig{
			Enabled:      true,
			TemplateType: TemplateGeneral,
			BaseURL:      srv.URL,
			APIKey:       "tok",
			Script: `({
				request: { url: "{{baseUrl}}/balance", method: "GET" },
				extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
			})`,
			TimeoutSeconds: 10,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("test query failed: %s", result.ErrorMessage)
	}

	// Snapshot should not exist.
	snap, err := store.Get("p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if snap != nil {
		t.Error("test query should not create a snapshot")
	}
}

func TestJitterForProvider(t *testing.T) {
	j1 := jitterForProvider("provider-1234-5678")
	j2 := jitterForProvider("provider-1234-5678")
	if j1 != j2 {
		t.Errorf("jitter not deterministic: %v != %v", j1, j2)
	}
	if j1 < 0 || j1 > 30*time.Second {
		t.Errorf("jitter out of range: %v", j1)
	}
}
