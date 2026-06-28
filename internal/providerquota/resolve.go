package providerquota

import (
	"errors"
	"fmt"
)

// ErrProviderMismatch is returned when an explicitly selected token-plan
// provider conflicts with the provider detected from the card API URL.
// It must surface before any network request so a card's credentials are
// never sent to a different provider's endpoint.
var ErrProviderMismatch = errors.New("provider_mismatch")

// ErrMissingCredentials is returned when a template/provider requires a
// dedicated credential that has not been configured.
var ErrMissingCredentials = errors.New("missing_credentials")

// queryPlan is the fully-resolved, validated description of a single quota
// query. It binds the template, provider, endpoint, and credential together so
// the dispatch layer never has to re-derive them (and cannot mix them up).
type queryPlan struct {
	template    string
	provider    string // token_plan / official_balance provider key
	scriptURL   string // base URL for script placeholders (general/custom/newapi)
	adapterURL  string // URL passed to the token_plan adapter (card URL or zenmux URL)
	token       string // Bearer token
	userID      string // newapi
	accessKeyID string // volcengine
	secretKey   string // volcengine
	isMiMo      bool
}

// NormalizeForTemplate clears quota-config fields that are inapplicable to the
// current template_type + coding_plan_provider. It is the backend safety
// boundary that prevents stale secrets from a previous configuration (e.g. a
// leftover ZenMux APIKey after switching to Kimi) from persisting and later
// leaking via a different credential route.
//
// It must run after partial updates are applied and respects the secret-patch
// "keep current applicable secret" semantics: only fields that are NOT
// applicable to the current template/provider are cleared.
func NormalizeForTemplate(c *ProviderQuotaConfig) {
	if c == nil {
		return
	}
	isTokenPlan := c.TemplateType == TemplateTokenPlan
	isScriptBased := c.TemplateType == TemplateGeneral || c.TemplateType == TemplateCustom

	// coding_plan_provider only applies to token_plan.
	if !isTokenPlan {
		c.CodingPlanProvider = ""
	}

	// Determine the effective token-plan provider (explicit or detected is not
	// known here without the card URL; use the stored value as-is).
	provider := c.CodingPlanProvider

	// BaseURL applies to: general, custom, newapi, and token_plan+zenmux.
	baseURLApplies := isScriptBased ||
		c.TemplateType == TemplateNewAPI ||
		(isTokenPlan && provider == "zenmux")
	if !baseURLApplies {
		c.BaseURL = ""
	}

	// APIKey applies to: general, custom, and token_plan+zenmux.
	apiKeyApplies := isScriptBased || (isTokenPlan && provider == "zenmux")
	if !apiKeyApplies {
		c.APIKey = ""
	}

	// AccessToken and UserID apply only to newapi.
	if c.TemplateType != TemplateNewAPI {
		c.AccessToken = ""
		c.UserID = ""
	}

	// AccessKeyID/SecretAccessKey apply only to token_plan+volcengine.
	if !(isTokenPlan && provider == "volcengine") {
		c.AccessKeyID = ""
		c.SecretAccessKey = ""
	}
}

// ResolveTokenPlanProvider binds an explicit token-plan provider selection to
// the provider detected from the card API URL. It is the single decision point
// that prevents a card's credentials from being routed to a mismatched provider.
//
// Resolution rules (per spec "自动检测或显式匹配"):
//  1. A MiMo card URL always returns unsupported — explicit selection cannot
//     bypass the deferral.
//  2. No explicit provider → use auto-detection from the card URL.
//  3. Explicit provider + detectable card URL → the two must agree, otherwise
//     ErrProviderMismatch.
//  4. Explicit provider + undetectable card URL → use the explicit provider.
//
// cardAPIURL is the provider card's API URL (never a quota BaseURL override).
func ResolveTokenPlanProvider(cardAPIURL, explicitProvider string) (provider string, isMiMo bool, err error) {
	detected, isMiMo := DetectTokenPlanProvider(cardAPIURL)
	if isMiMo {
		// MiMo deferral is absolute — no explicit provider can override it.
		return "", true, nil
	}
	if explicitProvider == "" {
		return detected, false, nil
	}
	if detected != "" && detected != explicitProvider {
		return "", false, ErrProviderMismatch
	}
	return explicitProvider, false, nil
}

// resolveQueryPlan produces a validated queryPlan for the given config and card
// credentials. Every credential decision is template/provider-specific:
//
//   - general/custom: quota APIKey override, else card APIToken.
//   - newapi: only AccessToken (never APIKey or card APIToken).
//   - token_plan/zenmux: only the dedicated APIKey (BaseURL+APIKey required,
//     NO card-token fallback).
//   - token_plan/kimi,zhipu,minimax: only the card APIToken (ignore stale
//     APIKey/AccessToken).
//   - token_plan/volcengine: only AK/SK.
//   - official_balance: only the card APIToken.
//
// All validation (mismatch, missing credentials) fails BEFORE any network
// request. The card APIToken is never sent to a ZenMux URL or a different
// provider's endpoint.
func resolveQueryPlan(cfg *ProviderQuotaConfig, cardAPIURL, cardAPIToken string) (*queryPlan, error) {
	if cfg == nil {
		return nil, errors.New("not_configured")
	}
	switch cfg.TemplateType {
	case TemplateGeneral, TemplateCustom:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = cardAPIURL
		}
		token := cardAPIToken
		if cfg.APIKey != "" {
			token = cfg.APIKey
		}
		return &queryPlan{template: cfg.TemplateType, scriptURL: baseURL, token: token}, nil

	case TemplateNewAPI:
		if cfg.AccessToken == "" {
			return nil, fmt.Errorf("%w: newapi requires access_token", ErrMissingCredentials)
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = cardAPIURL
		}
		return &queryPlan{template: TemplateNewAPI, scriptURL: baseURL, token: cfg.AccessToken, userID: cfg.UserID}, nil

	case TemplateTokenPlan:
		provider, isMiMo, err := ResolveTokenPlanProvider(cardAPIURL, cfg.CodingPlanProvider)
		if err != nil {
			return nil, err
		}
		if isMiMo {
			return &queryPlan{template: TemplateTokenPlan, isMiMo: true}, nil
		}
		switch provider {
		case "zenmux":
			if cfg.BaseURL == "" || cfg.APIKey == "" {
				return nil, fmt.Errorf("%w: zenmux requires base_url and api_key", ErrMissingCredentials)
			}
			return &queryPlan{template: TemplateTokenPlan, provider: "zenmux", adapterURL: cfg.BaseURL, token: cfg.APIKey}, nil
		case "volcengine":
			if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
				return nil, fmt.Errorf("%w: volcengine requires access_key_id and secret_access_key", ErrMissingCredentials)
			}
			// adapterURL carries the card URL so the adapter can derive the region.
			return &queryPlan{template: TemplateTokenPlan, provider: "volcengine", adapterURL: cardAPIURL, accessKeyID: cfg.AccessKeyID, secretKey: cfg.SecretAccessKey}, nil
		case "":
			return nil, errors.New("unsupported_provider: could not detect token plan provider; set coding_plan_provider explicitly")
		default: // kimi, zhipu, minimax
			return &queryPlan{template: TemplateTokenPlan, provider: provider, adapterURL: cardAPIURL, token: cardAPIToken}, nil
		}

	case TemplateOfficialBalance:
		provider := DetectBalanceProvider(cardAPIURL)
		if provider == "" {
			return nil, errors.New("unsupported_provider: no official balance adapter for this host")
		}
		return &queryPlan{template: TemplateOfficialBalance, provider: provider, token: cardAPIToken}, nil
	}
	return nil, fmt.Errorf("invalid_config: unknown template type %q", cfg.TemplateType)
}
