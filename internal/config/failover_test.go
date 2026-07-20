package config

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// TestResolveRouteExposedModelIsNotDefaultRouted verifies that a request
// hitting an enabled ExposedModel.ID is routed to that provider with
// DefaultRouted=false. Exposed-model routing is pinned (the /model session
// override) and MUST never participate in automatic failover.
func TestResolveRouteExposedModelIsNotDefaultRouted(t *testing.T) {
	cfg := &Config{Providers: []Provider{
		{ID: "a", Name: "A", Enabled: true, ExposedModels: []ExposedModel{
			{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"},
		}},
	}}

	r := cfg.ResolveRoute("glm-4.6")

	if r.Provider == nil || r.Provider.ID != "a" {
		t.Fatalf("expected provider a, got %+v", r.Provider)
	}
	if r.BackendModel != "glm-4.6" {
		t.Fatalf("expected backend model glm-4.6, got %q", r.BackendModel)
	}
	if r.DefaultRouted {
		t.Fatalf("exposed-model route must NOT be default-routed (failover must skip it)")
	}
	if r.ExposedLabel != "GLM-4.6" {
		t.Fatalf("expected exposed label GLM-4.6, got %q", r.ExposedLabel)
	}
}

// TestResolveRouteActiveFallbackIsDefaultRouted verifies that the
// ActiveProviderID fallback path is marked DefaultRouted=true so the proxy
// knows it may fail this request over to another default candidate.
func TestResolveRouteActiveFallbackIsDefaultRouted(t *testing.T) {
	cfg := &Config{
		ActiveProviderID: "a",
		Providers: []Provider{
			{ID: "a", Name: "A", Enabled: true, ModelMappings: map[string]string{
				"claude-opus-4-8": "glm-5.2",
			}},
		},
	}

	r := cfg.ResolveRoute("claude-opus-4-8")

	if r.Provider == nil || r.Provider.ID != "a" {
		t.Fatalf("expected active provider a, got %+v", r.Provider)
	}
	if r.BackendModel != "glm-5.2" {
		t.Fatalf("expected mapped backend glm-5.2, got %q", r.BackendModel)
	}
	if !r.DefaultRouted {
		t.Fatalf("active fallback MUST be default-routed (failover-eligible)")
	}
	if r.ExposedLabel != "" {
		t.Fatalf("active fallback must have empty ExposedLabel, got %q", r.ExposedLabel)
	}
}

// TestResolveRouteSkipsDisabledExposedModel preserves the existing fallback
// behavior: a disabled provider's exposed model is ignored and the active
// fallback is used (DefaultRouted=true).
func TestResolveRouteSkipsDisabledExposedModel(t *testing.T) {
	cfg := &Config{ActiveProviderID: "active", Providers: []Provider{
		{ID: "disabled", Name: "Disabled", Enabled: false, ExposedModels: []ExposedModel{
			{ID: "glm-4.6", Label: "GLM-4.6", BackendModel: "glm-4.6"},
		}},
		{ID: "active", Name: "Active", Enabled: true},
	}}

	r := cfg.ResolveRoute("glm-4.6")

	if r.Provider == nil || r.Provider.ID != "active" {
		t.Fatalf("expected fallback to active provider, got %+v", r.Provider)
	}
	if !r.DefaultRouted {
		t.Fatalf("fallback to active must be default-routed")
	}
	if r.ExposedLabel != "" {
		t.Fatalf("disabled-exposed fallback must have empty ExposedLabel, got %q", r.ExposedLabel)
	}
}

// TestResolveRouteWithoutActiveProvider preserves the nil-provider fallback:
// no active provider yields Provider=nil, the original model, and
// DefaultRouted=false (nothing to fail over).
func TestResolveRouteWithoutActiveProvider(t *testing.T) {
	cfg := &Config{}

	r := cfg.ResolveRoute("anything")

	if r.Provider != nil {
		t.Fatalf("expected nil provider, got %+v", r.Provider)
	}
	if r.BackendModel != "anything" {
		t.Fatalf("expected original model, got %q", r.BackendModel)
	}
	if r.DefaultRouted {
		t.Fatalf("nil-provider route must not be default-routed")
	}
	if r.ExposedLabel != "" {
		t.Fatalf("no-active route must have empty ExposedLabel, got %q", r.ExposedLabel)
	}
}

// TestResolveModelStillWrapsResolveRoute guarantees the legacy ResolveModel
// signature keeps returning the same provider/model pair as ResolveRoute so
// existing callers and tests stay green.
func TestResolveModelStillWrapsResolveRoute(t *testing.T) {
	cfg := &Config{ActiveProviderID: "a", Providers: []Provider{
		{ID: "a", Name: "A", Enabled: true, ModelMappings: map[string]string{
			"claude-opus-4-8": "glm-5.2",
		}},
	}}

	p, backend := cfg.ResolveModel("claude-opus-4-8")
	r := cfg.ResolveRoute("claude-opus-4-8")

	if p == nil || p.ID != r.Provider.ID {
		t.Fatalf("ResolveModel provider %v != ResolveRoute provider %v", p, r.Provider)
	}
	if backend != r.BackendModel {
		t.Fatalf("ResolveModel backend %q != ResolveRoute backend %q", backend, r.BackendModel)
	}
}

// TestAutoFailoverEnabledJSONRoundTrip verifies the JSON store persists the
// new toggle directly.
func TestAutoFailoverEnabledJSONRoundTrip(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "config.json"))

	cfg := DefaultConfig()
	cfg.AutoFailoverEnabled = true
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.AutoFailoverEnabled {
		t.Fatalf("AutoFailoverEnabled = false after JSON round-trip, want true")
	}
}

// TestAutoFailoverEnabledDefaultsFalse verifies a fresh default config and a
// brand-new SQLite DB both report the toggle disabled until explicitly set.
func TestAutoFailoverEnabledDefaultsFalse(t *testing.T) {
	if DefaultConfig().AutoFailoverEnabled {
		t.Fatalf("DefaultConfig AutoFailoverEnabled must be false")
	}
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AutoFailoverEnabled {
		t.Fatalf("fresh SQLite DB AutoFailoverEnabled must be false")
	}
}

// TestAutoFailoverEnabledSQLiteRoundTrip verifies the SQLite store persists the
// toggle as a 0/1 setting and reloads it.
func TestAutoFailoverEnabledSQLiteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded.AutoFailoverEnabled = true
	if err := store.Save(loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if !reloaded.AutoFailoverEnabled {
		t.Fatalf("AutoFailoverEnabled = false after SQLite round-trip, want true")
	}

	// An old DB missing the setting must default to false.
	if _, err := store.DB().Exec(`DELETE FROM settings WHERE key = 'auto_failover_enabled'`); err != nil {
		t.Fatalf("delete setting: %v", err)
	}
	old, err := store.Load()
	if err != nil {
		t.Fatalf("Load old DB: %v", err)
	}
	if old.AutoFailoverEnabled {
		t.Fatalf("old DB without setting must default to false")
	}
}

// TestAtomicConfigUpdatePreservesConcurrentActiveProviderAndFailoverSetting
// verifies that the atomic Update path serializes concurrent read-modify-write
// cycles so the failover toggle and an active-provider change never clobber
// each other. This is the core correctness guarantee for auto-failover running
// concurrently with admin edits.
func TestAtomicConfigUpdatePreservesConcurrentActiveProviderAndFailoverSetting(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	if _, err := store.Update(func(cfg *Config) error {
		cfg.Providers = []Provider{
			{ID: "p1", Name: "P1", APIURL: "https://p1.example", APIFormat: APIFormatAnthropic, Enabled: true},
			{ID: "p2", Name: "P2", APIURL: "https://p2.example", APIFormat: APIFormatAnthropic, Enabled: true},
		}
		return nil
	}); err != nil {
		t.Fatalf("seed Update: %v", err)
	}

	const goroutines = 60
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = store.Update(func(cfg *Config) error {
				cfg.ActiveProviderID = "p2"
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = store.Update(func(cfg *Config) error {
				cfg.AutoFailoverEnabled = true
				return nil
			})
		}()
	}
	wg.Wait()

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	if loaded.ActiveProviderID != "p2" {
		t.Fatalf("ActiveProviderID = %q, want p2", loaded.ActiveProviderID)
	}
	if !loaded.AutoFailoverEnabled {
		t.Fatalf("AutoFailoverEnabled = false, want true (clobbered by concurrent active update)")
	}
}

// TestAtomicUpdateValidationRollsBack verifies that a validation failure inside
// Update prevents the change from persisting and returns a ValidationError the
// caller can map to HTTP 400.
func TestAtomicUpdateValidationRollsBack(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "proxy.db"), "")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	// Attempt an invalid mutation: empty providers + empty backend_url.
	_, err = store.Update(func(cfg *Config) error {
		cfg.Providers = nil
		cfg.BackendURL = ""
		return nil
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}

	// State must be unchanged (DefaultConfig backend_url retained).
	loaded, _ := store.Load()
	if loaded.BackendURL == "" {
		t.Fatal("invalid Update must not persist (BackendURL emptied)")
	}
}
