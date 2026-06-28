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

// NormalizeForTemplate clears fields whose meaning is unambiguous and limited
// to another template. Script and ZenMux override fields are intentionally kept
// independently so switching templates cannot reinterpret or destroy them.
func NormalizeForTemplate(c *ProviderQuotaConfig, cardAPIURL string, _ *ProviderQuotaConfig) {
	if c == nil {
		return
	}
	MigrateLegacyCredentials(c, cardAPIURL)
	isTokenPlan := c.TemplateType == TemplateTokenPlan
	isScriptBased := c.TemplateType == TemplateGeneral || c.TemplateType == TemplateCustom

	// coding_plan_provider only applies to token_plan.
	if !isTokenPlan {
		c.CodingPlanProvider = ""
	}

	// Effective provider: explicit selection, else auto-detected from card URL.
	provider := c.CodingPlanProvider
	if provider == "" && isTokenPlan {
		provider, _ = DetectTokenPlanProvider(cardAPIURL)
	}

	// BaseURL is the generic script/NewAPI URL. ZenMux has ZenMuxBaseURL.
	baseURLApplies := isScriptBased || c.TemplateType == TemplateNewAPI
	if !baseURLApplies {
		c.BaseURL = ""
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

// resolveZenMuxCredentials resolves one complete endpoint/credential pair.
// Overrides are atomic: a partial override is invalid and is never combined
// with one half of the provider card's credentials.
func resolveZenMuxCredentials(cfg *ProviderQuotaConfig, cardAPIURL, cardAPIToken string) (string, string, error) {
	if cfg == nil {
		return "", "", fmt.Errorf("%w: zenmux configuration is missing", ErrMissingCredentials)
	}
	hasOverrideURL := cfg.ZenMuxBaseURL != ""
	hasOverrideKey := cfg.ZenMuxAPIKey != ""
	if hasOverrideURL != hasOverrideKey {
		return "", "", fmt.Errorf("%w: zenmux override requires both zenmux_base_url and zenmux_api_key", ErrMissingCredentials)
	}
	if hasOverrideURL {
		return cfg.ZenMuxBaseURL, cfg.ZenMuxAPIKey, nil
	}
	if cardAPIURL == "" || cardAPIToken == "" {
		return "", "", fmt.Errorf("%w: zenmux requires a complete override or provider card URL and token", ErrMissingCredentials)
	}
	return cardAPIURL, cardAPIToken, nil
}

// ValidateForCard validates template requirements that depend on the provider
// card's URL/token and therefore cannot be checked by Validate alone.
func (c *ProviderQuotaConfig) ValidateForCard(cardAPIURL, cardAPIToken string) error {
	if c == nil {
		return nil
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if !c.Enabled || c.TemplateType != TemplateTokenPlan {
		return nil
	}

	provider, isMiMo, err := ResolveTokenPlanProvider(cardAPIURL, c.CodingPlanProvider)
	if err != nil {
		return err
	}
	if isMiMo {
		return nil
	}
	switch provider {
	case "zenmux":
		_, _, err := resolveZenMuxCredentials(c, cardAPIURL, cardAPIToken)
		return err
	case "volcengine":
		if c.AccessKeyID == "" || c.SecretAccessKey == "" {
			return fmt.Errorf("%w: volcengine requires access_key_id and secret_access_key", ErrMissingCredentials)
		}
	case "":
		return errors.New("unsupported_provider: could not detect token plan provider; set coding_plan_provider explicitly")
	default:
		if cardAPIToken == "" {
			return fmt.Errorf("%w: token plan provider requires the provider card token", ErrMissingCredentials)
		}
	}
	return nil
}

// resolveQueryPlan produces a validated queryPlan for the given config and card
// credentials. Every credential decision is template/provider-specific:
//
//   - general/custom: ScriptAPIKey override, else card APIToken.
//   - newapi: only AccessToken (never script/ZenMux keys or card APIToken).
//   - token_plan/zenmux: complete ZenMux override pair, else complete card pair.
//   - token_plan/kimi,zhipu,minimax: only the card APIToken (ignore stale
//     separated keys/AccessToken).
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
	MigrateLegacyCredentials(cfg, cardAPIURL)
	if err := cfg.ValidateForCard(cardAPIURL, cardAPIToken); err != nil {
		return nil, err
	}
	switch cfg.TemplateType {
	case TemplateGeneral, TemplateCustom:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = cardAPIURL
		}
		token := cardAPIToken
		if cfg.ScriptAPIKey != "" {
			token = cfg.ScriptAPIKey
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
			endpoint, token, err := resolveZenMuxCredentials(cfg, cardAPIURL, cardAPIToken)
			if err != nil {
				return nil, err
			}
			return &queryPlan{template: TemplateTokenPlan, provider: "zenmux", adapterURL: endpoint, token: token}, nil
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
