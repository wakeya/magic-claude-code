package providerquota

import (
	"context"
	"log"
	"sync"
	"time"
)

// ProviderConfigGetter retrieves provider configuration by ID.
type ProviderConfigGetter interface {
	GetProviderByID(id string) *ProviderConfig
	ListEnabledProviders() []ProviderConfig
}

// ProviderConfig is the subset of provider config needed by the Manager.
type ProviderConfig struct {
	ID         string
	Enabled    bool
	APIURL     string
	APIToken   string
	QuotaQuery *ProviderQuotaConfig
}

// Manager coordinates quota queries with caching, deduplication, and scheduling.
type Manager struct {
	store     *SnapshotStore
	configGet ProviderConfigGetter

	// Per-provider in-flight deduplication.
	inFlight   map[string]*inflightEntry
	inFlightMu sync.Mutex

	// Global concurrency limiter.
	semaphore chan struct{}

	// Scheduler control.
	scanTicker *time.Ticker
	stopCh     chan struct{}
	done       chan struct{}

	// jitterFn returns the per-provider startup jitter applied before a
	// scheduled query fires. Defaults to jitterForProvider (deterministic
	// 0–30s hash); overridable for tests.
	jitterFn func(providerID string) time.Duration
}

type inflightEntry struct {
	done   chan struct{}
	result *ProviderQuotaResult
	err    error
}

// NewManager creates a Manager.
// maxConcurrency caps the number of simultaneous upstream queries.
func NewManager(store *SnapshotStore, configGet ProviderConfigGetter, maxConcurrency int) *Manager {
	if maxConcurrency <= 0 {
		maxConcurrency = 4
	}
	return &Manager{
		store:      store,
		configGet:  configGet,
		inFlight:   make(map[string]*inflightEntry),
		semaphore:  make(chan struct{}, maxConcurrency),
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
		jitterFn:   jitterForProvider,
	}
}

// QueryOptions configures a single query.
type QueryOptions struct {
	// Force bypasses the cache and always queries upstream.
	Force bool
	// Draft, if non-nil, is an unsaved config used instead of the stored one.
	Draft *ProviderQuotaConfig
}

// Query performs a quota query for the given provider.
// Production queries (manual refresh + scheduler) are deduplicated per provider
// so concurrent callers share one upstream result. Draft/test queries bypass
// singleflight entirely: each test run executes independently with its own
// credentials/script, so two different drafts never share a result.
func (m *Manager) Query(ctx context.Context, providerID string, opts QueryOptions) (*ProviderQuotaResult, error) {
	// Draft queries are never deduplicated — they carry ephemeral test config
	// (credentials, scripts) that may differ even for the same provider+BaseURL.
	if opts.Draft != nil {
		return m.executeQuery(ctx, providerID, opts)
	}

	dedupKey := "prod:" + providerID

	// Check if there's already an in-flight request.
	m.inFlightMu.Lock()
	entry, exists := m.inFlight[dedupKey]
	if exists {
		m.inFlightMu.Unlock()
		// Wait for the existing request.
		select {
		case <-entry.done:
			return entry.result, entry.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Create new in-flight entry.
	entry = &inflightEntry{done: make(chan struct{})}
	m.inFlight[dedupKey] = entry
	m.inFlightMu.Unlock()

	// Execute the query.
	result, err := m.executeQuery(ctx, providerID, opts)

	// Store the result.
	entry.result = result
	entry.err = err
	close(entry.done)

	// Clean up.
	m.inFlightMu.Lock()
	delete(m.inFlight, dedupKey)
	m.inFlightMu.Unlock()

	return result, err
}

func (m *Manager) executeQuery(ctx context.Context, providerID string, opts QueryOptions) (*ProviderQuotaResult, error) {
	// Always load the provider card to get APIURL/APIToken as fallback.
	// This is essential for Token Plan / Official Balance first-time tests,
	// where the draft does not carry the card's URL/token.
	var cardAPIURL, cardAPIToken string
	if m.configGet != nil {
		if provider := m.configGet.GetProviderByID(providerID); provider != nil {
			cardAPIURL = provider.APIURL
			cardAPIToken = provider.APIToken
		}
	}

	// Resolve the effective quota config: use draft if provided, else the
	// stored provider config.
	quotaCfg := opts.Draft
	if quotaCfg == nil {
		if card := m.configGet; card != nil {
			provider := card.GetProviderByID(providerID)
			if provider == nil {
				return errorResult("not_configured", "provider not found", time.Now()), nil
			}
			if !provider.Enabled {
				return errorResult("not_configured", "provider is disabled", time.Now()), nil
			}
			quotaCfg = provider.QuotaQuery
		}
	}

	if quotaCfg == nil || !quotaCfg.Enabled {
		return errorResult("not_configured", "quota query not configured", time.Now()), nil
	}

	// Effective credentials: draft/provider overrides take precedence, but fall
	// back to the card's APIURL/APIToken when the override is empty. Secrets
	// from the card are used in-memory only and never persisted (drafts and
	// fallbacks skip SaveUpsert).
	effectiveBaseURL := quotaCfg.BaseURL
	if effectiveBaseURL == "" {
		effectiveBaseURL = cardAPIURL
	}
	effectiveToken := cardAPIToken
	if quotaCfg.APIKey != "" {
		effectiveToken = quotaCfg.APIKey
	}
	if quotaCfg.AccessToken != "" {
		effectiveToken = quotaCfg.AccessToken
	}

	// apiURL is used by Token Plan / Official Balance host detection.
	apiURL := effectiveBaseURL

	timeout := time.Duration(quotaCfg.TimeoutSeconds) * time.Second

	// Acquire concurrency semaphore.
	select {
	case m.semaphore <- struct{}{}:
		defer func() { <-m.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	start := time.Now()
	var result *ProviderQuotaResult

	switch quotaCfg.TemplateType {
	case TemplateCustom, TemplateGeneral:
		script := quotaCfg.Script
		if script == "" {
			script = defaultScript(quotaCfg.TemplateType)
		}
		exec := NewScriptExecutor(timeout)
		placeholders := map[string]string{
			"baseUrl":     effectiveBaseURL,
			"apiKey":      effectiveToken,
			"accessToken": effectiveToken,
			"userId":      quotaCfg.UserID,
		}
		r, err := exec.ExecuteScript(ctx, script, placeholders, effectiveBaseURL)
		if err != nil {
			return nil, err
		}
		r.ProviderID = providerID
		r.TemplateType = quotaCfg.TemplateType
		result = r

	case TemplateNewAPI:
		exec := NewScriptExecutor(timeout)
		placeholders := map[string]string{
			"baseUrl":     effectiveBaseURL,
			"accessToken": effectiveToken,
			"userId":      quotaCfg.UserID,
		}
		r, err := exec.ExecuteScript(ctx, defaultNewAPIScript(), placeholders, effectiveBaseURL)
		if err != nil {
			return nil, err
		}
		r.ProviderID = providerID
		r.TemplateType = TemplateNewAPI
		result = r

	case TemplateTokenPlan:
		provider, isMiMo := DetectTokenPlanProvider(apiURL)
		if isMiMo {
			return &ProviderQuotaResult{
				ProviderID:   providerID,
				TemplateType: TemplateTokenPlan,
				Success:      false,
				ErrorCode:    "unsupported_provider",
				ErrorMessage: "Xiaomi MiMo does not currently have a stable API-key quota endpoint",
				QueriedAt:    time.Now(),
				DurationMS:   time.Since(start).Milliseconds(),
			}, nil
		}
		if provider == "" {
			provider = quotaCfg.CodingPlanProvider
		}
		adapter := NewTokenPlanAdapter(timeout)
		result = adapter.Query(ctx, provider, quotaCfg, effectiveToken)
		result.ProviderID = providerID
		result.TemplateType = TemplateTokenPlan

	case TemplateOfficialBalance:
		provider := DetectBalanceProvider(apiURL)
		if provider == "" {
			return errorResult("unsupported_provider", "no official balance adapter for this host", start), nil
		}
		adapter := NewBalanceAdapter(timeout)
		result = adapter.Query(ctx, provider, effectiveToken)
		result.ProviderID = providerID
		result.TemplateType = TemplateOfficialBalance

	default:
		return errorResult("invalid_config", "unknown template type: "+quotaCfg.TemplateType, start), nil
	}

	// Persist the result.
	if m.store != nil && !opts.Force {
		// Force means test/draft query; don't persist.
		// Actually, if Draft is set, don't persist either.
		if opts.Draft == nil {
			if err := m.store.SaveUpsert(providerID, result); err != nil {
				log.Printf("providerquota: failed to save snapshot for %s: %v", providerID, err)
			}
		}
	}

	return result, nil
}

// Start begins the automatic query scheduler.
func (m *Manager) Start(ctx context.Context) {
	m.scanTicker = time.NewTicker(30 * time.Second)
	go m.run(ctx)
}

// run is the scheduler loop.
func (m *Manager) run(ctx context.Context) {
	defer close(m.done)

	// First scan applies startup jitter to spread the initial fan-out.
	m.scanAndQuery(ctx, true)

	for {
		select {
		case <-m.scanTicker.C:
			m.scanAndQuery(ctx, false)
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// scanAndQuery checks which providers are due and triggers queries.
// applyJitter spreads the startup fan-out; subsequent periodic scans fire
// immediately so a due provider is not delayed by up to 30s on every tick.
func (m *Manager) scanAndQuery(ctx context.Context, applyJitter bool) {
	if m.configGet == nil {
		return
	}

	providers := m.configGet.ListEnabledProviders()
	now := time.Now()

	for _, p := range providers {
		if p.QuotaQuery == nil || !p.QuotaQuery.Enabled {
			continue
		}
		if p.QuotaQuery.AutoQueryIntervalMinutes <= 0 {
			continue
		}

		// Check if due.
		snap, err := m.store.Get(p.ID)
		if err != nil {
			log.Printf("providerquota: failed to get snapshot for %s: %v", p.ID, err)
			continue
		}

		due := false
		if snap == nil {
			due = true
		} else {
			interval := time.Duration(p.QuotaQuery.AutoQueryIntervalMinutes) * time.Minute
			if now.Sub(snap.QueriedAt) >= interval {
				due = true
			}
		}

		if !due {
			continue
		}

		// Fire async query. Startup jitter spreads the initial fan-out and is
		// only applied on the first scan; periodic scans fire immediately.
		providerID := p.ID
		interval := time.Duration(p.QuotaQuery.AutoQueryIntervalMinutes) * time.Minute
		go func() {
			if applyJitter && m.jitterFn != nil {
				jitter := m.jitterFn(providerID)
				if jitter > 0 {
					select {
					case <-time.After(jitter):
					case <-ctx.Done():
						return
					}
				}
			}
			// Re-check due after the jitter wait — a manual refresh or a
			// concurrent scan may have already populated the snapshot, making
			// this scheduled query redundant.
			if snap, err := m.store.Get(providerID); err == nil && snap != nil {
				if time.Since(snap.QueriedAt) < interval {
					return
				}
			}
			_, _ = m.Query(ctx, providerID, QueryOptions{})
		}()
	}
}

// Stop gracefully stops the scheduler.
func (m *Manager) Stop() {
	if m.scanTicker != nil {
		m.scanTicker.Stop()
	}
	close(m.stopCh)
	<-m.done
}

// GetSnapshot returns the cached snapshot for a provider.
func (m *Manager) GetSnapshot(providerID string) (*QuotaSnapshot, error) {
	return m.store.Get(providerID)
}

// GetAllSnapshots returns all cached snapshots.
func (m *Manager) GetAllSnapshots() (map[string]*QuotaSnapshot, error) {
	return m.store.GetAll()
}

// DeleteSnapshot removes the snapshot for a provider.
func (m *Manager) DeleteSnapshot(providerID string) error {
	return m.store.Delete(providerID)
}

// defaultScript returns the default script for general or custom templates.
func defaultScript(templateType string) string {
	return `({
		request: {
			url: "{{baseUrl}}/user/balance",
			method: "GET",
			headers: {
				"Authorization": "Bearer {{apiKey}}",
				"Accept": "application/json"
			}
		},
		extractor: function (response) {
			return {
				remaining: response.balance,
				unit: "USD"
			};
		}
	})`
}

// defaultNewAPIScript returns the default NewAPI script.
func defaultNewAPIScript() string {
	return `({
		request: {
			url: "{{baseUrl}}/api/user/self",
			method: "GET",
			headers: {
				"Authorization": "Bearer {{accessToken}}",
				"New-Api-User": "{{userId}}",
				"Content-Type": "application/json"
			}
		},
		extractor: function (response) {
			if (response.success === false) {
				return {
					__error_code: "upstream_business_error",
					__error_message: response.message || "NewAPI business error"
				};
			}
			var data = response.data || {};
			var planName = data.group || "Default";
			var quota = (data.quota || 0) / 500000;
			var usedQuota = (data.used_quota || 0) / 500000;
			return {
				planName: planName,
				remaining: quota,
				used: usedQuota,
				total: quota + usedQuota,
				unit: "USD"
			};
		}
	})`
}

// GenerateStartupJitter returns a deterministic jitter duration based on provider ID.
func GenerateStartupJitter(providerID string) time.Duration {
	return jitterForProvider(providerID)
}

// jitterForProvider computes a deterministic 0-30s jitter from the provider ID.
func jitterForProvider(providerID string) time.Duration {
	h := []byte(providerID)
	var sum uint32
	for _, b := range h {
		sum = sum*31 + uint32(b)
	}
	return time.Duration(sum%31) * time.Second
}
