package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"magic-claude-code/internal/config"
	"magic-claude-code/internal/providerquota"
)

// handleProviderQuotaRoutes dispatches /api/providers/{id}/usage/* routes.
// Must be called from handleProviderRoutes before the generic provider handler.
func (s *Server) handleProviderQuotaRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// /api/providers/{id}/usage/test
	if strings.HasSuffix(path, "/usage/test") {
		s.handleProviderUsageTest(w, r)
		return
	}
	// /api/providers/{id}/usage/query
	if strings.HasSuffix(path, "/usage/query") {
		s.handleProviderUsageQuery(w, r)
		return
	}
	// /api/providers/{id}/usage (GET or PUT)
	if strings.HasSuffix(path, "/usage") {
		s.handleProviderUsage(w, r)
		return
	}
}

// handleProviderBatchUsage returns all provider quota snapshots.
// Route: GET /api/providers/usage
func (s *Server) handleProviderBatchUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if s.quotaManager == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"snapshots": map[string]any{}})
		return
	}

	snapshots, err := s.quotaManager.GetAllSnapshots()
	if err != nil {
		http.Error(w, `{"error": "failed to load snapshots"}`, http.StatusInternalServerError)
		return
	}

	// Build sanitized response.
	result := make(map[string]any)
	for id, snap := range snapshots {
		result[id] = sanitizeSnapshot(snap)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"snapshots": result})
}

// handleProviderUsage handles GET/PUT for /api/providers/{id}/usage.
func (s *Server) handleProviderUsage(w http.ResponseWriter, r *http.Request) {
	// Extract provider ID.
	path := strings.TrimSuffix(r.URL.Path, "/usage")
	id := strings.TrimPrefix(path, "/api/providers/")

	switch r.Method {
	case http.MethodGet:
		s.getProviderUsage(w, r, id)
	case http.MethodPut:
		s.updateProviderUsage(w, r, id)
	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// getProviderUsage returns public config and latest snapshots.
func (s *Server) getProviderUsage(w http.ResponseWriter, _ *http.Request, id string) {
	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	publicConfig := providerquota.ToPublicConfig(provider.QuotaQuery)

	// Detect token-plan provider / official-balance provider / MiMo from the
	// card API URL so the frontend can show the right fields and warnings
	// before the user saves anything.
	detectedTokenPlan, isMiMo := providerquota.DetectTokenPlanProvider(provider.APIURL)
	detectedBalance := providerquota.DetectBalanceProvider(provider.APIURL)

	var snapDTO *providerquota.SanitizedSnapshot
	if s.quotaManager != nil {
		snap, err := s.quotaManager.GetSnapshot(id)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to load quota snapshot"})
			return
		}
		if snap != nil {
			dto := sanitizeSnapshot(snap)
			snapDTO = &dto
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"config":              publicConfig,
		"snapshot":            snapDTO,
		"detected_token_plan": detectedTokenPlan,
		"detected_balance":    detectedBalance,
		"is_mimo":             isMiMo,
	})
}

// updateProviderUsage saves quota configuration.
func (s *Server) updateProviderUsage(w http.ResponseWriter, r *http.Request, id string) {
	var req providerQuotaUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}
	if err := validateProviderQuotaSecretPatches(req); err != nil {
		jsonErr, _ := json.Marshal(map[string]string{"error": err.Error()})
		http.Error(w, string(jsonErr), http.StatusBadRequest)
		return
	}

	// 只读加载用于请求级校验（ValidateForCard 依赖 provider 的 APIURL/APIToken/旧 QuotaQuery）。
	// 真正的写入手走下面的原子 Update，避免与代理自动故障切换并发写互相覆盖。
	checkCfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := checkCfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	// Build or update the quota config.
	newCfg := applyQuotaUpdate(provider.QuotaQuery, req, provider.APIURL)
	if err := newCfg.ValidateForCard(provider.APIURL, provider.APIToken); err != nil {
		jsonErr, _ := json.Marshal(map[string]string{"error": err.Error()})
		http.Error(w, string(jsonErr), http.StatusBadRequest)
		return
	}

	// Check if this is a material config change (not just interval).
	material := isMaterialQuotaChange(provider.QuotaQuery, newCfg)

	// 原子写入：在锁内重新查找 provider（防止并发删除）并写入新 QuotaQuery。
	if _, err := s.configStore.Update(func(cfg *config.Config) error {
		p := cfg.GetProviderByID(id)
		if p == nil {
			return errAdminProviderNotFound
		}
		p.QuotaQuery = newCfg
		p.UpdatedAt = time.Now()
		return nil
	}); err != nil {
		writeConfigUpdateError(w, err)
		return
	}

	// Delete stale snapshots after material changes. Disabled configs always
	// retry cleanup so a previous delete failure cannot strand a snapshot.
	if (material || !newCfg.Enabled) && s.quotaManager != nil {
		if err := s.quotaManager.DeleteSnapshot(id); err != nil {
			http.Error(w, `{"error": "config saved but failed to clear quota snapshot"}`, http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"config":  providerquota.ToPublicConfig(newCfg),
	})
}

// handleProviderUsageTest runs a test query with unsaved draft config.
func (s *Server) handleProviderUsageTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimSuffix(r.URL.Path, "/usage/test")
	id := strings.TrimPrefix(path, "/api/providers/")

	var req providerQuotaUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}
	if err := validateProviderQuotaSecretPatches(req); err != nil {
		jsonErr, _ := json.Marshal(map[string]string{"error": err.Error()})
		http.Error(w, string(jsonErr), http.StatusBadRequest)
		return
	}

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	// Build draft config.
	draft := applyQuotaUpdate(provider.QuotaQuery, req, provider.APIURL)
	if err := draft.ValidateForCard(provider.APIURL, provider.APIToken); err != nil {
		jsonErr, _ := json.Marshal(map[string]string{"error": err.Error()})
		http.Error(w, string(jsonErr), http.StatusBadRequest)
		return
	}

	if s.quotaManager == nil {
		http.Error(w, `{"error": "quota manager not available"}`, http.StatusInternalServerError)
		return
	}

	// Run test query (Draft mode - no persistence).
	result, err := s.quotaManager.Query(r.Context(), id, providerquota.QueryOptions{
		Draft: draft,
		Force: true,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": result.Success,
		"result":  result,
	})
}

// handleProviderUsageQuery runs a manual production query.
func (s *Server) handleProviderUsageQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimSuffix(r.URL.Path, "/usage/query")
	id := strings.TrimPrefix(path, "/api/providers/")

	cfg, err := s.configStore.Load()
	if err != nil {
		http.Error(w, `{"error": "failed to load config"}`, http.StatusInternalServerError)
		return
	}

	provider := cfg.GetProviderByID(id)
	if provider == nil {
		http.Error(w, `{"error": "provider not found"}`, http.StatusNotFound)
		return
	}

	if provider.QuotaQuery == nil || !provider.QuotaQuery.Enabled {
		http.Error(w, `{"error": "quota query not configured"}`, http.StatusBadRequest)
		return
	}

	if !provider.Enabled {
		http.Error(w, `{"error": "provider is disabled"}`, http.StatusBadRequest)
		return
	}

	if s.quotaManager == nil {
		http.Error(w, `{"error": "quota manager not available"}`, http.StatusInternalServerError)
		return
	}

	result, err := s.quotaManager.Query(r.Context(), id, providerquota.QueryOptions{})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": result.Success,
		"result":  result,
	})
}

// --- Request/Response DTOs ---

type providerQuotaUpdateRequest struct {
	Enabled                  *bool   `json:"enabled"`
	TemplateType             *string `json:"template_type"`
	TimeoutSeconds           *int    `json:"timeout_seconds"`
	AutoQueryIntervalMinutes *int    `json:"auto_query_interval_minutes"`
	Script                   *string `json:"script"`
	BaseURL                  *string `json:"base_url"`
	ScriptAPIKey             *string `json:"script_api_key"`
	ZenMuxBaseURL            *string `json:"zenmux_base_url"`
	ZenMuxAPIKey             *string `json:"zenmux_api_key"`
	APIKey                   *string `json:"api_key"` // backward-compatible client input
	AccessToken              *string `json:"access_token"`
	UserID                   *string `json:"user_id"`
	CodingPlanProvider       *string `json:"coding_plan_provider"`
	AccessKeyID              *string `json:"access_key_id"`
	SecretAccessKey          *string `json:"secret_access_key"`
	ClearScriptAPIKey        bool    `json:"clear_script_api_key"`
	ClearZenMuxAPIKey        bool    `json:"clear_zenmux_api_key"`
	ClearAPIKey              bool    `json:"clear_api_key"` // backward-compatible client input
	ClearAccessToken         bool    `json:"clear_access_token"`
	ClearSecretAccessKey     bool    `json:"clear_secret_access_key"`
}

func validateProviderQuotaSecretPatches(req providerQuotaUpdateRequest) error {
	patches := []struct {
		name  string
		value *string
		clear bool
	}{
		{name: "script_api_key", value: req.ScriptAPIKey, clear: req.ClearScriptAPIKey},
		{name: "zenmux_api_key", value: req.ZenMuxAPIKey, clear: req.ClearZenMuxAPIKey},
		{name: "access_token", value: req.AccessToken, clear: req.ClearAccessToken},
		{name: "secret_access_key", value: req.SecretAccessKey, clear: req.ClearSecretAccessKey},
	}
	for _, patch := range patches {
		if patch.clear && patch.value != nil && *patch.value != "" {
			return fmt.Errorf("%s cannot be replaced and cleared in the same request", patch.name)
		}
	}
	return nil
}

// applyQuotaUpdate applies partial updates to a quota config.
// cardAPIURL is the provider card's API URL, used by NormalizeForTemplate to
// resolve the effective token-plan provider for auto-detected configs.
func applyQuotaUpdate(existing *providerquota.ProviderQuotaConfig, req providerQuotaUpdateRequest, cardAPIURL string) *providerquota.ProviderQuotaConfig {
	c := &providerquota.ProviderQuotaConfig{}
	if existing != nil {
		cp := *existing
		c = &cp
	}
	providerquota.MigrateLegacyCredentials(c, cardAPIURL)

	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	if req.TemplateType != nil {
		c.TemplateType = *req.TemplateType
	}
	if req.TimeoutSeconds != nil {
		c.TimeoutSeconds = *req.TimeoutSeconds
	}
	if req.AutoQueryIntervalMinutes != nil {
		c.AutoQueryIntervalMinutes = *req.AutoQueryIntervalMinutes
	}
	if req.Script != nil {
		c.Script = *req.Script
	}
	if req.BaseURL != nil {
		c.BaseURL = *req.BaseURL
	}
	if req.ZenMuxBaseURL != nil {
		c.ZenMuxBaseURL = *req.ZenMuxBaseURL
	}
	if req.UserID != nil {
		c.UserID = *req.UserID
	}
	if req.CodingPlanProvider != nil {
		c.CodingPlanProvider = *req.CodingPlanProvider
	}
	if req.AccessKeyID != nil {
		c.AccessKeyID = *req.AccessKeyID
	}

	// Purpose-specific secret patch semantics.
	applySecretPatch(&c.ScriptAPIKey, req.ScriptAPIKey, req.ClearScriptAPIKey)
	applySecretPatch(&c.ZenMuxAPIKey, req.ZenMuxAPIKey, req.ClearZenMuxAPIKey)
	applySecretPatch(&c.AccessToken, req.AccessToken, req.ClearAccessToken)
	applySecretPatch(&c.SecretAccessKey, req.SecretAccessKey, req.ClearSecretAccessKey)

	// Backward-compatible api_key/base_url requests are routed once according
	// to the new effective purpose. New purpose-specific fields take priority.
	legacyPurpose := quotaAPIKeyPurpose(c, cardAPIURL)
	if legacyPurpose == "zenmux" && req.ZenMuxBaseURL == nil && req.BaseURL != nil {
		c.ZenMuxBaseURL = *req.BaseURL
		c.BaseURL = ""
	}
	if req.APIKey != nil || req.ClearAPIKey {
		switch legacyPurpose {
		case "script":
			if req.ScriptAPIKey == nil && !req.ClearScriptAPIKey {
				applySecretPatch(&c.ScriptAPIKey, req.APIKey, req.ClearAPIKey)
			}
		case "zenmux":
			if req.ZenMuxAPIKey == nil && !req.ClearZenMuxAPIKey {
				applySecretPatch(&c.ZenMuxAPIKey, req.APIKey, req.ClearAPIKey)
			}
		}
	}

	// Backend safety boundary: clear fields inapplicable to the new
	// template/provider so stale secrets from a previous configuration cannot
	// persist and later leak via a different credential route. This runs after
	// the partial update + secret patch, so applicable secrets are retained.
	// cardAPIURL resolves the effective provider for auto-detected configs;
	providerquota.NormalizeForTemplate(c, cardAPIURL, existing)

	return c
}

func quotaAPIKeyPurpose(c *providerquota.ProviderQuotaConfig, cardAPIURL string) string {
	if c == nil {
		return ""
	}
	if c.TemplateType == providerquota.TemplateGeneral || c.TemplateType == providerquota.TemplateCustom {
		return "script"
	}
	if c.TemplateType != providerquota.TemplateTokenPlan {
		return ""
	}
	provider, _, err := providerquota.ResolveTokenPlanProvider(cardAPIURL, c.CodingPlanProvider)
	if err == nil && provider == "zenmux" {
		return "zenmux"
	}
	return ""
}

// applySecretPatch applies secret field update semantics:
// - Missing/empty value + clear=false: keep existing (do nothing)
// - clear=true: clear the field
// - Non-empty value: replace
func applySecretPatch(field *string, value *string, clear bool) {
	if clear {
		*field = ""
		return
	}
	if value != nil && *value != "" {
		*field = *value
		return
	}
	// Missing or empty value: keep existing (no-op).
}

// isMaterialQuotaChange returns true if the config change should invalidate snapshots.
func isMaterialQuotaChange(old, new *providerquota.ProviderQuotaConfig) bool {
	if old == nil && new == nil {
		return false
	}
	if old == nil || new == nil {
		return true
	}
	return old.Enabled != new.Enabled ||
		old.TemplateType != new.TemplateType ||
		old.Script != new.Script ||
		old.BaseURL != new.BaseURL ||
		old.ScriptAPIKey != new.ScriptAPIKey ||
		old.ZenMuxBaseURL != new.ZenMuxBaseURL ||
		old.ZenMuxAPIKey != new.ZenMuxAPIKey ||
		old.AccessToken != new.AccessToken ||
		old.UserID != new.UserID ||
		old.CodingPlanProvider != new.CodingPlanProvider ||
		old.AccessKeyID != new.AccessKeyID ||
		old.SecretAccessKey != new.SecretAccessKey
}

// sanitizeSnapshot converts a QuotaSnapshot to a SanitizedSnapshot for the API.
func sanitizeSnapshot(snap *providerquota.QuotaSnapshot) providerquota.SanitizedSnapshot {
	dto := providerquota.SanitizedSnapshot{
		ProviderID:     snap.ProviderID,
		Result:         snap.Result,
		LastSuccess:    snap.LastSuccess,
		QueriedAt:      snap.QueriedAt,
		UpdatedAt:      snap.UpdatedAt,
		HasLastSuccess: snap.LastSuccess != nil,
		IsStale:        snap.Result != nil && !snap.Result.Success && snap.LastSuccess != nil,
	}
	return dto
}
