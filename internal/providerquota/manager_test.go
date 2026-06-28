package providerquota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestManagerDedupDistinguishesDraftAndProduction verifies that a concurrent
// draft (test) query and a production query for the same provider are NOT
// deduplicated against each other — they must execute independently. This
// prevents a test query from receiving a production snapshot (or vice versa).
func TestManagerDedupDistinguishesDraftAndProduction(t *testing.T) {
	var queryCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryCount.Add(1)
		// Delay so the two calls overlap in time.
		time.Sleep(50 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{"balance": 100})
	}))
	defer srv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	insertTestProvider(t, db, "p1")
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {
				ID:       "p1",
				Enabled:  true,
				APIURL:   srv.URL,
				APIToken: "prod-token",
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

	// Launch a draft (test) query and a production query concurrently.
	var wg sync.WaitGroup
	wg.Add(2)

	var draftResult, prodResult *ProviderQuotaResult
	go func() {
		defer wg.Done()
		r, _ := mgr.Query(context.Background(), "p1", QueryOptions{
			Draft: &ProviderQuotaConfig{
				Enabled:      true,
				TemplateType: TemplateGeneral,
				BaseURL:      srv.URL,
				APIKey:       "draft-token",
				Script: `({
					request: { url: "{{baseUrl}}/balance", method: "GET" },
					extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
				})`,
				TimeoutSeconds: 10,
			},
		})
		draftResult = r
	}()
	go func() {
		defer wg.Done()
		r, _ := mgr.Query(context.Background(), "p1", QueryOptions{})
		prodResult = r
	}()
	wg.Wait()

	// Both queries must have executed independently (2 upstream requests),
	// not shared a single deduplicated result.
	if got := queryCount.Load(); got != 2 {
		t.Errorf("upstream requests = %d, want 2 (draft and prod must be independent)", got)
	}
	if draftResult == nil || !draftResult.Success {
		t.Errorf("draft result not successful: %+v", draftResult)
	}
	if prodResult == nil || !prodResult.Success {
		t.Errorf("prod result not successful: %+v", prodResult)
	}

	// The production query must have persisted a snapshot; the draft must not.
	snap, err := store.Get("p1")
	if err != nil {
		t.Fatalf("store Get: %v", err)
	}
	if snap == nil {
		t.Error("production query should have persisted a snapshot")
	}
}

// TestDraftQueryFallsBackToCardCredentials verifies that a draft (test) query
// with empty BaseURL/APIKey falls back to the provider card's APIURL/APIToken.
// This is required so first-time Token Plan / Official Balance tests work
// without the user re-entering the card credentials.
func TestDraftQueryFallsBackToCardCredentials(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"balance": 42})
	}))
	defer srv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	insertTestProvider(t, db, "p1")
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {
				ID:       "p1",
				Enabled:  true,
				APIURL:   srv.URL,
				APIToken: "card-secret-token",
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

	// Draft has NO BaseURL and NO APIKey — must fall back to the card.
	result, err := mgr.Query(context.Background(), "p1", QueryOptions{
		Draft: &ProviderQuotaConfig{
			Enabled:      true,
			TemplateType: TemplateGeneral,
			Script: `({
				request: { url: "{{baseUrl}}/balance", method: "GET", headers: { "Authorization": "Bearer {{apiKey}}" } },
				extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
			})`,
			TimeoutSeconds: 10,
			// BaseURL and APIKey intentionally empty.
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("draft query failed: %s - %s", result.ErrorCode, result.ErrorMessage)
	}
	if gotAuth != "Bearer card-secret-token" {
		t.Errorf("Authorization = %q, want 'Bearer card-secret-token' (card fallback)", gotAuth)
	}
}

// TestDraftQueriesNotDeduplicatedByBaseURL verifies that two concurrent draft
// queries with the SAME BaseURL but DIFFERENT credentials/scripts are NOT
// deduplicated against each other — each must execute independently. Sharing a
// result would leak the wrong credentials' response across test runs.
func TestDraftQueriesNotDeduplicatedByBaseURL(t *testing.T) {
	var seenAuths sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		bal := 0.0
		if auth == "Bearer cred-A" {
			bal = 10
		} else if auth == "Bearer cred-B" {
			bal = 20
		}
		seenAuths.Store(auth, true)
		// Small delay so the two drafts overlap.
		time.Sleep(30 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{"balance": bal})
	}))
	defer srv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	insertTestProvider(t, db, "p1")
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {ID: "p1", Enabled: true, APIURL: srv.URL, APIToken: "card-tok"},
		},
	}
	mgr := NewManager(store, configGet, 4)

	script := `({
		request: { url: "{{baseUrl}}/balance", method: "GET", headers: { "Authorization": "Bearer {{apiKey}}" } },
		extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
	})`
	makeDraft := func(key string) *ProviderQuotaConfig {
		return &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateGeneral,
			BaseURL: srv.URL, APIKey: key, Script: script, TimeoutSeconds: 10,
		}
	}

	var wg sync.WaitGroup
	var resA, resB *ProviderQuotaResult
	wg.Add(2)
	go func() {
		defer wg.Done()
		r, _ := mgr.Query(context.Background(), "p1", QueryOptions{Draft: makeDraft("cred-A")})
		resA = r
	}()
	go func() {
		defer wg.Done()
		r, _ := mgr.Query(context.Background(), "p1", QueryOptions{Draft: makeDraft("cred-B")})
		resB = r
	}()
	wg.Wait()

	// Both credentials must have been used (two independent upstream requests).
	_, hasA := seenAuths.Load("Bearer cred-A")
	_, hasB := seenAuths.Load("Bearer cred-B")
	if !hasA || !hasB {
		t.Errorf("expected both cred-A and cred-B to hit upstream; hasA=%v hasB=%v", hasA, hasB)
	}
	// Results must reflect the respective credentials, not a shared one.
	if resA == nil || resB == nil {
		t.Fatalf("results nil: A=%v B=%v", resA, resB)
	}
	if len(resA.Balances) == 0 || len(resB.Balances) == 0 {
		t.Fatalf("missing balances")
	}
	a := *resA.Balances[0].Remaining
	b := *resB.Balances[0].Remaining
	if a == b {
		t.Errorf("draft results identical (remaining=%v), expected distinct per credential", a)
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

// TestSchedulerAppliesJitter verifies that scanAndQuery spreads scheduled
// queries using per-provider jitter rather than firing all at once.
func TestSchedulerAppliesJitter(t *testing.T) {
	var mu sync.Mutex
	var requestTimes []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"balance": 1})
	}))
	defer srv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)

	script := `({
		request: { url: "{{baseUrl}}/balance", method: "GET" },
		extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
	})`
	qq := func() *ProviderQuotaConfig {
		return &ProviderQuotaConfig{
			Enabled:                  true,
			TemplateType:             TemplateGeneral,
			Script:                   script,
			TimeoutSeconds:           10,
			AutoQueryIntervalMinutes: 5,
		}
	}
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"a": {ID: "a", Enabled: true, APIURL: srv.URL, APIToken: "tok", QuotaQuery: qq()},
			"b": {ID: "b", Enabled: true, APIURL: srv.URL, APIToken: "tok", QuotaQuery: qq()},
			"c": {ID: "c", Enabled: true, APIURL: srv.URL, APIToken: "tok", QuotaQuery: qq()},
		},
	}

	mgr := NewManager(store, configGet, 4)
	// Override jitter: a=0, b=80ms, c=160ms — distinct, increasing.
	mgr.jitterFn = func(id string) time.Duration {
		switch id {
		case "a":
			return 0
		case "b":
			return 80 * time.Millisecond
		case "c":
			return 160 * time.Millisecond
		}
		return 0
	}

	start := time.Now()
	mgr.scanAndQuery(context.Background(), true)

	// Wait for all three upstream requests.
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(requestTimes)
		mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requestTimes) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(requestTimes))
	}

	// Compute offsets from scan start.
	offsets := make([]time.Duration, len(requestTimes))
	for i, ts := range requestTimes {
		offsets[i] = ts.Sub(start)
	}
	minOff, maxOff := offsets[0], offsets[0]
	for _, o := range offsets {
		if o < minOff {
			minOff = o
		}
		if o > maxOff {
			maxOff = o
		}
	}

	// With jitter 0/80/160ms, the spread (max-min) must be substantial —
	// far more than the near-simultaneous (<20ms) spread without jitter.
	spread := maxOff - minOff
	if spread < 100*time.Millisecond {
		t.Errorf("jitter not applied: request spread = %v (want >= 100ms)", spread)
	}
	// The latest request must be near the largest jitter (160ms).
	if maxOff < 130*time.Millisecond {
		t.Errorf("latest request too early: %v (want >= 130ms)", maxOff)
	}
}

// TestSchedulerPeriodicScanNoJitter verifies that subsequent (non-startup)
// scans fire due queries immediately without jitter, so periodic refresh is
// not delayed by up to 30s on every tick.
func TestSchedulerPeriodicScanNoJitter(t *testing.T) {
	var mu sync.Mutex
	var requestTimes []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"balance": 1})
	}))
	defer srv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	insertTestProvider(t, db, "p1")
	insertTestProvider(t, db, "p2")
	script := `({
		request: { url: "{{baseUrl}}/balance", method: "GET" },
		extractor: function(r) { return { remaining: r.balance, unit: "USD" }; }
	})`
	qq := func() *ProviderQuotaConfig {
		return &ProviderQuotaConfig{Enabled: true, TemplateType: TemplateGeneral, Script: script, TimeoutSeconds: 10, AutoQueryIntervalMinutes: 5}
	}
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {ID: "p1", Enabled: true, APIURL: srv.URL, APIToken: "tok", QuotaQuery: qq()},
			"p2": {ID: "p2", Enabled: true, APIURL: srv.URL, APIToken: "tok", QuotaQuery: qq()},
		},
	}

	mgr := NewManager(store, configGet, 4)
	// Large jitter that would clearly delay queries if applied.
	mgr.jitterFn = func(id string) time.Duration { return 5 * time.Second }

	// Non-startup scan (applyJitter=false) must fire immediately.
	start := time.Now()
	mgr.scanAndQuery(context.Background(), false)

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(requestTimes)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	if len(requestTimes) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requestTimes))
	}
	// With jitter disabled, both must fire well under the 5s jitter delay.
	if elapsed > 1*time.Second {
		t.Errorf("periodic scan delayed by jitter: elapsed = %v (want < 1s)", elapsed)
	}
}

// TestManagerStopTerminatesScheduler verifies Stop() stops the ticker and
// returns (closing the done channel), so callers can shut down cleanly.
func TestManagerStopTerminatesScheduler(t *testing.T) {
	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	mgr := NewManager(store, nil, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	// Stop must return promptly (not block forever).
	done := make(chan struct{})
	go func() {
		mgr.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Manager.Stop() did not return within 3s")
	}
}

// TestTokenPlanExplicitProviderNotOverridden verifies that an explicit
// CodingPlanProvider takes precedence over auto-detection, and that a stale
// quota BaseURL (e.g. a leftover ZenMux URL) does NOT redirect the query.
// Selecting Kimi with a stale ZenMux BaseURL must still hit Kimi.
func TestTokenPlanExplicitProviderNotOverridden(t *testing.T) {
	// Mock that stands in for api.kimi.com.
	kimiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"limits":[{"name":"coding","detail":{"limit":1000,"remaining":800,"resetTime":1719500000000}}]}`))
	}))
	defer kimiSrv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	insertTestProvider(t, db, "p1")
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {
				ID:       "p1",
				Enabled:  true,
				APIURL:   "https://api.kimi.com/coding/v1", // card URL → Kimi
				APIToken: "kimi-token",
			},
		},
	}
	mgr := NewManager(store, configGet, 4)
	// Route all adapter HTTP traffic to the Kimi mock.
	mgr.adapterHTTPClient = &http.Client{
		Transport: &urlRewriteTransport{original: "api.kimi.com", replaced: kimiSrv.URL, inner: http.DefaultTransport},
		Timeout:   5 * time.Second,
	}

	// Draft explicitly selects Kimi but carries a STALE ZenMux BaseURL.
	result, err := mgr.Query(context.Background(), "p1", QueryOptions{
		Draft: &ProviderQuotaConfig{
			Enabled:            true,
			TemplateType:       TemplateTokenPlan,
			CodingPlanProvider: "kimi",
			BaseURL:            "https://quota.zenmux.example/v1", // stale
			TimeoutSeconds:     10,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("explicit Kimi selection should query Kimi, not the stale ZenMux URL; got %s - %s",
			result.ErrorCode, result.ErrorMessage)
	}
}

// TestVolcengineRegionFromCardURLManager verifies the full Manager → adapter
// path derives the Volcengine region from the provider card URL (not the empty
// quota BaseURL). A Shanghai card must sign Region=cn-shanghai.
func TestVolcengineRegionFromCardURLManager(t *testing.T) {
	var capturedQuery string
	// Mock standing in for open.volcengineapi.com.
	volcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		// Return an empty AFP result so the adapter produces a business fallback.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"Result": {}, "ResponseMetadata": {}}`))
	}))
	defer volcSrv.Close()

	db := setupTestDB(t)
	store := NewSnapshotStore(db)
	insertTestProvider(t, db, "p1")
	configGet := &mockConfigGet{
		providers: map[string]ProviderConfig{
			"p1": {
				ID:       "p1",
				Enabled:  true,
				APIURL:   "https://ark.cn-shanghai.volces.com/api/v3",
				APIToken: "unused",
			},
		},
	}
	mgr := NewManager(store, configGet, 4)
	mgr.adapterHTTPClient = &http.Client{
		Transport: &urlRewriteTransport{original: "open.volcengineapi.com", replaced: volcSrv.URL, inner: http.DefaultTransport},
		Timeout:   5 * time.Second,
	}

	_, err := mgr.Query(context.Background(), "p1", QueryOptions{
		Draft: &ProviderQuotaConfig{
			Enabled:            true,
			TemplateType:       TemplateTokenPlan,
			CodingPlanProvider: "volcengine",
			AccessKeyID:        "AKLT-test",
			SecretAccessKey:    "secret-key",
			TimeoutSeconds:     10,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The signed query is captured regardless of result success.
	if !strings.Contains(capturedQuery, "Region=cn-shanghai") {
		t.Errorf("expected Region=cn-shanghai in signed query; got %q", capturedQuery)
	}
}

// TestManagerTokenPlanProviderMismatch verifies that an explicit token-plan
// provider conflicting with the card URL is rejected BEFORE any network
// request, so a card's credentials are never sent to a different provider.
func TestManagerTokenPlanProviderMismatch(t *testing.T) {
	tests := []struct {
		name           string
		cardAPIURL     string
		explicit       string
		wantErrContain string
	}{
		{"MiniMax card + explicit Kimi", "https://api.minimaxi.com/v1/chat", "kimi", "provider_mismatch"},
		{"Kimi card + explicit MiniMax", "https://api.kimi.com/coding/v1", "minimax_cn", "provider_mismatch"},
		{"MiMo card + explicit Kimi", "https://token-plan-cn.xiaomimimo.com/v1", "kimi", "unsupported_provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqCount int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&reqCount, 1)
				json.NewEncoder(w).Encode(map[string]any{"balance": 1})
			}))
			defer srv.Close()

			db := setupTestDB(t)
			store := NewSnapshotStore(db)
			insertTestProvider(t, db, "p1")
			configGet := &mockConfigGet{
				providers: map[string]ProviderConfig{
					"p1": {ID: "p1", Enabled: true, APIURL: tt.cardAPIURL, APIToken: "card-secret"},
				},
			}
			mgr := NewManager(store, configGet, 4)
			mgr.adapterHTTPClient = &http.Client{
				Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport},
				Timeout:   5 * time.Second,
			}

			result, err := mgr.Query(context.Background(), "p1", QueryOptions{
				Draft: &ProviderQuotaConfig{
					Enabled:            true,
					TemplateType:       TemplateTokenPlan,
					CodingPlanProvider: tt.explicit,
					TimeoutSeconds:     10,
				},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Success {
				t.Error("expected failure for provider mismatch")
			}
			if !strings.Contains(result.ErrorCode, tt.wantErrContain) {
				t.Errorf("ErrorCode = %q, want to contain %q", result.ErrorCode, tt.wantErrContain)
			}
			if got := atomic.LoadInt32(&reqCount); got != 0 {
				t.Errorf("upstream requests = %d, want 0 (mismatch must fail before network)", got)
			}
		})
	}
}

// TestManagerCredentialsBoundPerTemplate verifies that each template/provider
// uses ONLY its designated credential and ignores stale secrets left over from
// a different template. The card token must never leak to a mismatched route.
func TestManagerCredentialsBoundPerTemplate(t *testing.T) {
	t.Run("Kimi ignores stale ZenMux APIKey and AccessToken", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"limits":[{"name":"coding","detail":{"limit":1000,"remaining":800,"resetTime":1719500000000}}]}`))
		}))
		defer srv.Close()

		db := setupTestDB(t)
		store := NewSnapshotStore(db)
		insertTestProvider(t, db, "p1")
		configGet := &mockConfigGet{
			providers: map[string]ProviderConfig{
				"p1": {ID: "p1", Enabled: true, APIURL: "https://api.kimi.com/coding/v1", APIToken: "kimi-card-secret"},
			},
		}
		mgr := NewManager(store, configGet, 4)
		mgr.adapterHTTPClient = &http.Client{
			Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport},
			Timeout:   5 * time.Second,
		}

		// Draft carries stale purpose-specific secrets that must be ignored.
		_, err := mgr.Query(context.Background(), "p1", QueryOptions{
			Draft: &ProviderQuotaConfig{
				Enabled: true, TemplateType: TemplateTokenPlan,
				CodingPlanProvider: "kimi",
				ScriptAPIKey:       "stale-script-key",
				ZenMuxAPIKey:       "stale-zenmux-key",
				AccessToken:        "stale-newapi-token",
				TimeoutSeconds:     10,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuth != "Bearer kimi-card-secret" {
			t.Errorf("Authorization = %q, want 'Bearer kimi-card-secret' (card token, not stale secrets)", gotAuth)
		}
	})

	t.Run("Official Balance ignores stale separated keys and AccessToken", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"balance_infos":[{"currency":"CNY","total_balance":10}],"is_available":true}`))
		}))
		defer srv.Close()

		db := setupTestDB(t)
		store := NewSnapshotStore(db)
		insertTestProvider(t, db, "p1")
		configGet := &mockConfigGet{
			providers: map[string]ProviderConfig{
				"p1": {ID: "p1", Enabled: true, APIURL: "https://api.deepseek.com/v1", APIToken: "deepseek-card-secret"},
			},
		}
		mgr := NewManager(store, configGet, 4)
		mgr.adapterHTTPClient = &http.Client{
			Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport},
			Timeout:   5 * time.Second,
		}

		_, err := mgr.Query(context.Background(), "p1", QueryOptions{
			Draft: &ProviderQuotaConfig{
				Enabled: true, TemplateType: TemplateOfficialBalance,
				ScriptAPIKey:   "stale-general-key",
				ZenMuxAPIKey:   "stale-zenmux-key",
				AccessToken:    "stale-newapi-token",
				TimeoutSeconds: 10,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuth != "Bearer deepseek-card-secret" {
			t.Errorf("Authorization = %q, want 'Bearer deepseek-card-secret' (card token)", gotAuth)
		}
	})

	t.Run("ZenMux without override uses complete card fallback pair", func(t *testing.T) {
		var reqCount int32
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&reqCount, 1)
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"data":{"quota_5_hour":{"usage_percentage":0.1,"max_value_usd":100}}}`))
		}))
		defer srv.Close()

		db := setupTestDB(t)
		store := NewSnapshotStore(db)
		insertTestProvider(t, db, "p1")
		configGet := &mockConfigGet{
			providers: map[string]ProviderConfig{
				"p1": {ID: "p1", Enabled: true, APIURL: "https://zenmux.example.com/v1", APIToken: "card-must-not-leak"},
			},
		}
		mgr := NewManager(store, configGet, 4)
		mgr.adapterHTTPClient = &http.Client{
			Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport},
			Timeout:   5 * time.Second,
		}

		result, err := mgr.Query(context.Background(), "p1", QueryOptions{
			Draft: &ProviderQuotaConfig{
				Enabled: true, TemplateType: TemplateTokenPlan,
				CodingPlanProvider: "zenmux",
				TimeoutSeconds:     10,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("fallback query failed: %+v", result)
		}
		if gotAuth != "Bearer card-must-not-leak" {
			t.Errorf("Authorization = %q, want card fallback token", gotAuth)
		}
		if got := atomic.LoadInt32(&reqCount); got != 1 {
			t.Errorf("upstream requests = %d, want 1", got)
		}
	})

	t.Run("ZenMux half override fails before network", func(t *testing.T) {
		var reqCount int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&reqCount, 1)
		}))
		defer srv.Close()

		db := setupTestDB(t)
		store := NewSnapshotStore(db)
		insertTestProvider(t, db, "p1")
		configGet := &mockConfigGet{providers: map[string]ProviderConfig{
			"p1": {ID: "p1", Enabled: true, APIURL: "https://zenmux.example.com/v1", APIToken: "card-token"},
		}}
		mgr := NewManager(store, configGet, 4)
		mgr.adapterHTTPClient = &http.Client{Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport}}

		result, err := mgr.Query(context.Background(), "p1", QueryOptions{Draft: &ProviderQuotaConfig{
			Enabled: true, TemplateType: TemplateTokenPlan,
			CodingPlanProvider: "zenmux",
			ZenMuxBaseURL:      "https://quota.zenmux.example/v1",
			TimeoutSeconds:     10,
		}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ErrorCode != "missing_credentials" {
			t.Fatalf("ErrorCode = %q, want missing_credentials", result.ErrorCode)
		}
		if got := atomic.LoadInt32(&reqCount); got != 0 {
			t.Fatalf("upstream requests = %d, want 0", got)
		}
	})

	t.Run("ZenMux with dedicated APIKey sends only that key", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"data":{"quota_5_hour":{"usage_percentage":0.1,"max_value_usd":100}}}`))
		}))
		defer srv.Close()

		db := setupTestDB(t)
		store := NewSnapshotStore(db)
		insertTestProvider(t, db, "p1")
		configGet := &mockConfigGet{
			providers: map[string]ProviderConfig{
				"p1": {ID: "p1", Enabled: true, APIURL: "https://zenmux.example.com/v1", APIToken: "card-must-not-leak"},
			},
		}
		mgr := NewManager(store, configGet, 4)
		mgr.adapterHTTPClient = &http.Client{
			Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport},
			Timeout:   5 * time.Second,
		}

		_, err := mgr.Query(context.Background(), "p1", QueryOptions{
			Draft: &ProviderQuotaConfig{
				Enabled: true, TemplateType: TemplateTokenPlan,
				CodingPlanProvider: "zenmux",
				ZenMuxBaseURL:      "https://quota.zenmux.example/v1",
				ZenMuxAPIKey:       "zenmux-dedicated-key",
				ScriptAPIKey:       "script-must-not-leak",
				TimeoutSeconds:     10,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuth != "Bearer zenmux-dedicated-key" {
			t.Errorf("Authorization = %q, want 'Bearer zenmux-dedicated-key'", gotAuth)
		}
	})

	t.Run("NewAPI uses only AccessToken", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"data":{"quota":500000,"used_quota":0,"group":"default"}}`))
		}))
		defer srv.Close()

		db := setupTestDB(t)
		store := NewSnapshotStore(db)
		insertTestProvider(t, db, "p1")
		configGet := &mockConfigGet{
			providers: map[string]ProviderConfig{
				"p1": {ID: "p1", Enabled: true, APIURL: "https://panel.example.com", APIToken: "card-must-not-leak"},
			},
		}
		mgr := NewManager(store, configGet, 4)
		mgr.adapterHTTPClient = &http.Client{
			Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport},
			Timeout:   5 * time.Second,
		}

		_, err := mgr.Query(context.Background(), "p1", QueryOptions{
			Draft: &ProviderQuotaConfig{
				Enabled: true, TemplateType: TemplateNewAPI,
				BaseURL:        "https://panel.example.com",
				AccessToken:    "newapi-access-token",
				APIKey:         "stale-general-key",
				UserID:         "u1",
				TimeoutSeconds: 10,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuth != "Bearer newapi-access-token" {
			t.Errorf("Authorization = %q, want 'Bearer newapi-access-token'", gotAuth)
		}
	})

	t.Run("General APIKey override beats card token", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			json.NewEncoder(w).Encode(map[string]any{"balance": 5})
		}))
		defer srv.Close()

		db := setupTestDB(t)
		store := NewSnapshotStore(db)
		insertTestProvider(t, db, "p1")
		configGet := &mockConfigGet{
			providers: map[string]ProviderConfig{
				"p1": {ID: "p1", Enabled: true, APIURL: srv.URL, APIToken: "card-token"},
			},
		}
		mgr := NewManager(store, configGet, 4)
		mgr.adapterHTTPClient = &http.Client{
			Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport},
			Timeout:   5 * time.Second,
		}

		_, err := mgr.Query(context.Background(), "p1", QueryOptions{
			Draft: &ProviderQuotaConfig{
				Enabled: true, TemplateType: TemplateGeneral,
				BaseURL: srv.URL, APIKey: "general-override-key",
				Script:         `({request:{url:"{{baseUrl}}/b",method:"GET",headers:{"Authorization":"Bearer {{apiKey}}"}},extractor:function(r){return{remaining:r.balance};}})`,
				TimeoutSeconds: 10,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuth != "Bearer general-override-key" {
			t.Errorf("Authorization = %q, want 'Bearer general-override-key'", gotAuth)
		}
	})
}

// TestManagerTokenPlanMatchingProviderRequests verifies that a matching
// (explicit == detected) provider and an undetectable card with an explicit
// provider both proceed to a normal upstream request.
func TestManagerTokenPlanMatchingProviderRequests(t *testing.T) {
	tests := []struct {
		name       string
		cardAPIURL string
		explicit   string
	}{
		{"matching Kimi", "https://api.kimi.com/coding/v1", "kimi"},
		{"undetectable card + explicit Kimi", "https://custom-gateway.example.com/v1", "kimi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reqCount int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&reqCount, 1)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"limits":[{"name":"coding","detail":{"limit":1000,"remaining":800,"resetTime":1719500000000}}]}`))
			}))
			defer srv.Close()

			db := setupTestDB(t)
			store := NewSnapshotStore(db)
			insertTestProvider(t, db, "p1")
			configGet := &mockConfigGet{
				providers: map[string]ProviderConfig{
					"p1": {ID: "p1", Enabled: true, APIURL: tt.cardAPIURL, APIToken: "kimi-card-secret"},
				},
			}
			mgr := NewManager(store, configGet, 4)
			mgr.adapterHTTPClient = &http.Client{
				Transport: &urlRewriteTransport{replaced: srv.URL, inner: http.DefaultTransport},
				Timeout:   5 * time.Second,
			}

			result, err := mgr.Query(context.Background(), "p1", QueryOptions{
				Draft: &ProviderQuotaConfig{
					Enabled:            true,
					TemplateType:       TemplateTokenPlan,
					CodingPlanProvider: tt.explicit,
					TimeoutSeconds:     10,
				},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.Success {
				t.Errorf("expected success, got %s - %s", result.ErrorCode, result.ErrorMessage)
			}
			if got := atomic.LoadInt32(&reqCount); got != 1 {
				t.Errorf("upstream requests = %d, want 1", got)
			}
		})
	}
}
