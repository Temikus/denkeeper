package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// validateCostFields checks that cost limit fields are non-negative.
func validateCostFields(soft, hard, rate *float64) string {
	if soft != nil && *soft < 0 {
		return "cost_limit_soft must be >= 0"
	}
	if hard != nil && *hard < 0 {
		return "cost_limit_hard must be >= 0"
	}
	if rate != nil && *rate < 0 {
		return "default_rate_per_1k_tokens must be >= 0"
	}
	return ""
}

// modelPricesToMap converts ModelPriceConfig map to a generic map for TOML persistence.
func modelPricesToMap(prices map[string]config.ModelPriceConfig) map[string]any {
	mp := make(map[string]any, len(prices))
	for model, price := range prices {
		mp[model] = map[string]any{
			"input":        price.InputPerMTok,
			"output":       price.OutputPerMTok,
			"cached_input": price.CachedInputPerMTok,
		}
	}
	return mp
}

// costChangesMap builds a TOML-compatible changes map from cost fields.
func costChangesMap(soft, hard, rate *float64, prices map[string]config.ModelPriceConfig) map[string]any {
	changes := make(map[string]any)
	if soft != nil {
		changes["cost_limit_soft"] = *soft
	}
	if hard != nil {
		changes["cost_limit_hard"] = *hard
	}
	if rate != nil {
		changes["default_rate_per_1k_tokens"] = *rate
	}
	if len(prices) > 0 {
		changes["model_prices"] = modelPricesToMap(prices)
	}
	return changes
}

// costPersistChanges builds a TOML changes map for cost fields, including explicit null clearing.
// A nil value signals UpdateLLMProviderInstanceConfig to delete the key from TOML.
func costPersistChanges(soft, hard, rate *float64, prices map[string]config.ModelPriceConfig, nulls nullCostFields) map[string]any {
	changes := costChangesMap(soft, hard, rate, prices)
	if nulls.Soft {
		changes["cost_limit_soft"] = nil
	}
	if nulls.Hard {
		changes["cost_limit_hard"] = nil
	}
	if nulls.Rate {
		changes["default_rate_per_1k_tokens"] = nil
	}
	return changes
}

// validateBaseURL returns an error message if the URL is invalid, or "" if ok.
func validateBaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "invalid base_url: must be a valid URL with scheme and host"
	}
	return ""
}

// validateProviderAuth checks the auth mode for a provider. typ is the provider
// type and token is the OAuth token (empty string when not supplied). Returns an
// error message string, or "" if valid. An empty auth means the default api_key.
func validateProviderAuth(auth, typ, token string) string {
	if !config.ValidAuthMode(auth) {
		return "invalid auth: must be one of: api_key, oauth"
	}
	if auth == config.AuthModeOAuth {
		if typ != "anthropic" {
			return "auth \"oauth\" is only supported for anthropic-type providers"
		}
		if token == "" {
			return "oauth_token is required when auth is \"oauth\" (generate one with `claude setup-token`)"
		}
	}
	return ""
}

// nullCostFields tracks which cost fields were explicitly set to null in the JSON body.
type nullCostFields struct {
	Soft bool
	Hard bool
	Rate bool
}

// detectNullCostFields inspects raw JSON to identify cost fields explicitly set to null.
func detectNullCostFields(body []byte) nullCostFields {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nullCostFields{}
	}
	return nullCostFields{
		Soft: string(raw["cost_limit_soft"]) == "null",
		Hard: string(raw["cost_limit_hard"]) == "null",
		Rate: string(raw["default_rate_per_1k_tokens"]) == "null",
	}
}

// syncProviderCostTracker updates the live CostTracker with new provider limits.
func (s *Server) syncProviderCostTracker(name string, soft, hard *float64) {
	if soft == nil && hard == nil {
		return
	}
	limits := llm.SessionLimits{}
	if soft != nil {
		limits.Soft = *soft
	}
	if hard != nil {
		limits.Hard = *hard
	}
	s.deps.CostTracker.SetProviderLimits(name, limits)
}

// persistCreateProviderCosts persists cost fields to TOML and syncs the live CostTracker.
// Returns an error if TOML persistence fails (CostTracker is still synced).
func (s *Server) persistCreateProviderCosts(name string, soft, hard, rate *float64, prices map[string]config.ModelPriceConfig) error {
	changes := costChangesMap(soft, hard, rate, prices)
	var persistErr error
	if len(changes) > 0 {
		if err := config.UpdateLLMProviderInstanceConfig(s.deps.ConfigPath, name, changes); err != nil {
			s.logger.Warn("failed to persist provider cost config", "provider", name, "error", err)
			persistErr = err
		}
	}
	s.syncProviderCostTracker(name, soft, hard)
	return persistErr
}

// providerInfo is the JSON shape returned by GET /api/v1/llm/providers.
type providerInfo struct {
	Name                  string                             `json:"name"`
	Type                  string                             `json:"type"`
	Enabled               bool                               `json:"enabled"`
	APIKeySet             bool                               `json:"api_key_set"`
	Auth                  string                             `json:"auth"`
	OAuthTokenSet         bool                               `json:"oauth_token_set"`
	BaseURL               string                             `json:"base_url,omitempty"`
	Organization          string                             `json:"organization,omitempty"`
	Reasoning             *config.OpenRouterReasoningCfg     `json:"reasoning,omitempty"`
	CostLimitSoft         *float64                           `json:"cost_limit_soft,omitempty"`
	CostLimitHard         *float64                           `json:"cost_limit_hard,omitempty"`
	DefaultRatePerKTokens *float64                           `json:"default_rate_per_1k_tokens,omitempty"`
	ModelPrices           map[string]config.ModelPriceConfig `json:"model_prices,omitempty"`
}

type llmProvidersResponse struct {
	DefaultProvider string         `json:"default_provider"`
	DefaultModel    string         `json:"default_model"`
	CostLimitSoft   float64        `json:"cost_limit_soft"`
	CostLimitHard   float64        `json:"cost_limit_hard"`
	Providers       []providerInfo `json:"providers"`
}

// handleGetLLMProviders godoc
// @Summary List LLM providers
// @Description Returns all configured LLM providers with their settings
// @Tags providers
// @Produce json
// @Security BearerAuth
// @Success 200 {object} llmProvidersResponse
// @Router /llm/providers [get]
func (s *Server) handleGetLLMProviders(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.Config.LLM

	providers := make([]providerInfo, 0, len(cfg.Providers))
	for _, pc := range cfg.Providers {
		auth := pc.Auth
		if auth == "" {
			auth = config.AuthModeAPIKey
		}
		enabled := pc.APIKey != "" || pc.Type == "ollama"
		if pc.IsOAuth() {
			enabled = pc.OAuthToken != ""
		}
		pi := providerInfo{
			Name:          pc.Name,
			Type:          pc.Type,
			Enabled:       enabled,
			APIKeySet:     pc.APIKey != "",
			Auth:          auth,
			OAuthTokenSet: pc.OAuthToken != "",
			BaseURL:       pc.BaseURL,
			Organization:  pc.Organization,
		}
		if pc.Type == "openrouter" {
			r := cfg.OpenRouter.Reasoning
			pi.Reasoning = &r
		}
		pi.CostLimitSoft = pc.CostLimitSoft
		pi.CostLimitHard = pc.CostLimitHard
		pi.DefaultRatePerKTokens = pc.DefaultRatePerKTokens
		if len(pc.ModelPrices) > 0 {
			pi.ModelPrices = pc.ModelPrices
		}
		providers = append(providers, pi)
	}

	writeJSON(w, http.StatusOK, llmProvidersResponse{
		DefaultProvider: cfg.DefaultProvider,
		DefaultModel:    cfg.DefaultModel,
		CostLimitSoft:   cfg.CostLimitSoft,
		CostLimitHard:   cfg.CostLimitHard,
		Providers:       providers,
	})
}

// providerUpdateInput holds the mutable fields for PATCH /api/v1/llm/providers/{name}.
type providerUpdateInput struct {
	APIKey                *string                            `json:"api_key,omitempty"`
	Auth                  *string                            `json:"auth,omitempty"`
	OAuthToken            *string                            `json:"oauth_token,omitempty"`
	BaseURL               *string                            `json:"base_url,omitempty"`
	Organization          *string                            `json:"organization,omitempty"`
	Reasoning             *config.OpenRouterReasoningCfg     `json:"reasoning,omitempty"`
	CostLimitSoft         *float64                           `json:"cost_limit_soft,omitempty"`
	CostLimitHard         *float64                           `json:"cost_limit_hard,omitempty"`
	DefaultRatePerKTokens *float64                           `json:"default_rate_per_1k_tokens,omitempty"`
	ModelPrices           map[string]config.ModelPriceConfig `json:"model_prices,omitempty"`
}

// handlePatchLLMProvider godoc
// @Summary Update LLM provider
// @Description Updates a provider instance's configuration (API key, base URL, etc.)
// @Tags providers
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Provider name"
// @Param body body providerUpdateInput true "Fields to update"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /llm/providers/{name} [patch]
func (s *Server) handlePatchLLMProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pc := s.findProviderInstance(name)
	if pc == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "unknown provider: " + name,
		})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reading body: " + err.Error()})
		return
	}

	var input providerUpdateInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Detect explicitly null cost fields (distinct from absent).
	nullCosts := detectNullCostFields(body)

	if input.BaseURL != nil {
		if msg := validateBaseURL(*input.BaseURL); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
	}

	// Organization is only valid for OpenAI-type providers.
	if input.Organization != nil && pc.Type != "openai" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "organization is only supported for openai-type providers",
		})
		return
	}

	// Reasoning is only valid for OpenRouter-type providers.
	if input.Reasoning != nil {
		if pc.Type != "openrouter" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "reasoning is only supported for openrouter-type providers",
			})
			return
		}
		if err := config.ValidateOpenRouterReasoning(input.Reasoning); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid reasoning config: " + err.Error(),
			})
			return
		}
	}

	if msg := validateCostFields(input.CostLimitSoft, input.CostLimitHard, input.DefaultRatePerKTokens); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	// Validate the auth mode against the resulting state (auth/token may be set
	// in this patch or already present on the provider).
	if msg := validatePatchedProviderAuth(pc, &input); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	// Apply to in-memory config and persist.
	s.applyLLMProviderUpdate(name, &input, nullCosts)
	s.persistLLMProvider(name, &input, nullCosts)

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// providerTestInput optionally overrides saved credentials for a connection
// test, so a freshly-pasted token can be verified before it is persisted. All
// fields are optional; absent fields fall back to the saved provider config.
type providerTestInput struct {
	Auth       *string `json:"auth,omitempty"`
	APIKey     *string `json:"api_key,omitempty"`
	OAuthToken *string `json:"oauth_token,omitempty"`
	BaseURL    *string `json:"base_url,omitempty"`
}

// handleTestLLMProvider godoc
// @Summary Test LLM provider connection
// @Description Verifies provider credentials by listing models. Accepts optional credential overrides to test before saving.
// @Tags providers
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Provider name"
// @Param body body providerTestInput false "Optional credential overrides"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]any
// @Router /llm/providers/{name}/test [post]
func (s *Server) handleTestLLMProvider(w http.ResponseWriter, r *http.Request) {
	if s.deps.ProviderFactory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "provider testing not available"})
		return
	}

	name := r.PathValue("name")
	pc := s.findProviderInstance(name)
	if pc == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown provider: " + name})
		return
	}

	// Apply optional overrides onto a copy so the test can validate unsaved creds.
	test := *pc
	if body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(body) > 0 {
		var in providerTestInput
		if err := json.Unmarshal(body, &in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		applyProviderTestOverrides(&test, &in)
	}

	if msg := validateProviderAuth(test.Auth, test.Type, test.OAuthToken); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	provider := s.deps.ProviderFactory(test)
	if provider == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not construct provider client"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	ok, models, err := testProviderConnection(ctx, provider)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": providerTestError(err)})
		return
	}

	resp := map[string]any{"ok": true}
	if models >= 0 {
		resp["models"] = models
	}
	writeJSON(w, http.StatusOK, resp)
}

// applyProviderTestOverrides mutates pc with any non-nil fields from in.
func applyProviderTestOverrides(pc *config.ProviderInstanceConfig, in *providerTestInput) {
	if in.Auth != nil {
		pc.Auth = *in.Auth
	}
	if in.APIKey != nil {
		pc.APIKey = *in.APIKey
	}
	if in.OAuthToken != nil {
		pc.OAuthToken = *in.OAuthToken
	}
	if in.BaseURL != nil {
		pc.BaseURL = *in.BaseURL
	}
}

// testProviderConnection probes a provider. It prefers ListModels (which both
// validates credentials and yields a count); otherwise it falls back to
// HealthCheck. The returned count is -1 when only a health check was performed.
func testProviderConnection(ctx context.Context, provider llm.Provider) (ok bool, models int, err error) {
	if lister, isLister := provider.(llm.ModelLister); isLister {
		list, lerr := lister.ListModels(ctx)
		if lerr != nil {
			return false, 0, lerr
		}
		return true, len(list), nil
	}
	if herr := provider.HealthCheck(ctx); herr != nil {
		return false, 0, herr
	}
	return true, -1, nil
}

// providerTestError renders a user-facing message for a failed connection test,
// surfacing the upstream API status/message when available.
func providerTestError(err error) string {
	if err == nil {
		return "connection failed"
	}
	var llmErr *llm.LLMError
	if errors.As(err, &llmErr) {
		return llmErr.Message
	}
	return err.Error()
}

// validatePatchedProviderAuth validates the auth mode that would result from
// applying a PATCH to an existing provider, accounting for fields not present in
// the patch (which retain their current values).
func validatePatchedProviderAuth(pc *config.ProviderInstanceConfig, input *providerUpdateInput) string {
	auth := pc.Auth
	if input.Auth != nil {
		auth = *input.Auth
	}
	token := pc.OAuthToken
	if input.OAuthToken != nil {
		token = *input.OAuthToken
	}
	return validateProviderAuth(auth, pc.Type, token)
}

// findProviderInstance returns a pointer to the named provider in config, or nil.
func (s *Server) findProviderInstance(name string) *config.ProviderInstanceConfig {
	for i := range s.deps.Config.LLM.Providers {
		if s.deps.Config.LLM.Providers[i].Name == name {
			return &s.deps.Config.LLM.Providers[i]
		}
	}
	return nil
}

func (s *Server) applyLLMProviderUpdate(name string, input *providerUpdateInput, nulls nullCostFields) {
	pc := s.findProviderInstance(name)
	if pc == nil {
		return
	}
	if input.APIKey != nil {
		pc.APIKey = *input.APIKey
	}
	if input.Auth != nil {
		pc.Auth = *input.Auth
	}
	if input.OAuthToken != nil {
		pc.OAuthToken = *input.OAuthToken
	}
	if input.BaseURL != nil {
		pc.BaseURL = *input.BaseURL
	}
	if input.Organization != nil {
		pc.Organization = *input.Organization
	}

	if input.Reasoning != nil && pc.Type == "openrouter" {
		s.deps.Config.LLM.OpenRouter.Reasoning = *input.Reasoning
	}

	applyProviderCostUpdate(pc, input, nulls)

	s.syncProviderCostTracker(name, pc.CostLimitSoft, pc.CostLimitHard)

	// Keep legacy structs in sync for backward compat.
	s.syncLegacyProviderConfig(name, pc)
}

// applyProviderCostUpdate applies cost-limit and pricing fields from input to
// pc, honoring explicit-null requests in nulls.
func applyProviderCostUpdate(pc *config.ProviderInstanceConfig, input *providerUpdateInput, nulls nullCostFields) {
	if nulls.Soft {
		pc.CostLimitSoft = nil
	} else if input.CostLimitSoft != nil {
		pc.CostLimitSoft = input.CostLimitSoft
	}
	if nulls.Hard {
		pc.CostLimitHard = nil
	} else if input.CostLimitHard != nil {
		pc.CostLimitHard = input.CostLimitHard
	}
	if nulls.Rate {
		pc.DefaultRatePerKTokens = nil
	} else if input.DefaultRatePerKTokens != nil {
		pc.DefaultRatePerKTokens = input.DefaultRatePerKTokens
	}
	if input.ModelPrices != nil {
		pc.ModelPrices = input.ModelPrices
	}
}

// syncLegacyProviderConfig mirrors changes from a ProviderInstanceConfig back
// to the old-style config struct fields so existing code paths that read the
// legacy fields stay correct.
func (s *Server) syncLegacyProviderConfig(name string, pc *config.ProviderInstanceConfig) {
	switch name {
	case "anthropic":
		s.deps.Config.LLM.Anthropic.APIKey = pc.APIKey
		s.deps.Config.LLM.Anthropic.BaseURL = pc.BaseURL
		s.deps.Config.LLM.Anthropic.Auth = pc.Auth
		s.deps.Config.LLM.Anthropic.OAuthToken = pc.OAuthToken
	case "openrouter":
		s.deps.Config.LLM.OpenRouter.APIKey = pc.APIKey
	case "openai":
		s.deps.Config.LLM.OpenAI.APIKey = pc.APIKey
		s.deps.Config.LLM.OpenAI.BaseURL = pc.BaseURL
		s.deps.Config.LLM.OpenAI.Organization = pc.Organization
	case "ollama":
		s.deps.Config.LLM.Ollama.BaseURL = pc.BaseURL
	}
}

func (s *Server) persistLLMProvider(name string, input *providerUpdateInput, nulls nullCostFields) {
	if s.deps.ConfigPath == "" {
		return
	}

	changes := make(map[string]any)
	if input.APIKey != nil {
		changes["api_key"] = *input.APIKey
	}
	if input.Auth != nil {
		// "api_key" is the default; persist it as a deletion so the key is
		// omitted from TOML (backward-compat), and write "oauth" explicitly.
		if *input.Auth == config.AuthModeOAuth {
			changes["auth"] = config.AuthModeOAuth
		} else {
			changes["auth"] = nil
		}
	}
	if input.OAuthToken != nil {
		if *input.OAuthToken == "" {
			changes["oauth_token"] = nil
		} else {
			changes["oauth_token"] = *input.OAuthToken
		}
	}
	if input.BaseURL != nil {
		changes["base_url"] = *input.BaseURL
	}
	if input.Organization != nil {
		changes["organization"] = *input.Organization
	}
	for k, v := range costPersistChanges(input.CostLimitSoft, input.CostLimitHard, input.DefaultRatePerKTokens, input.ModelPrices, nulls) {
		changes[k] = v
	}

	if len(changes) > 0 {
		if err := config.UpdateLLMProviderInstanceConfig(s.deps.ConfigPath, name, changes); err != nil {
			s.logger.Warn("failed to persist LLM provider config", "provider", name, "error", err)
		}
	}

	// Reasoning config lives in [llm.openrouter.reasoning], not in [[llm.providers]].
	if input.Reasoning != nil {
		s.persistProviderReasoning(input.Reasoning)
	}
}

// persistProviderReasoning writes OpenRouter reasoning config to disk. Reasoning
// lives in [llm.openrouter.reasoning], not in [[llm.providers]].
func (s *Server) persistProviderReasoning(reasoning *config.OpenRouterReasoningCfg) {
	r := make(map[string]any)
	if reasoning.Enabled != nil {
		r["enabled"] = *reasoning.Enabled
	}
	if reasoning.Effort != "" {
		r["effort"] = reasoning.Effort
	}
	if reasoning.MaxTokens > 0 {
		r["max_tokens"] = reasoning.MaxTokens
	}
	if reasoning.Exclude != nil {
		r["exclude"] = *reasoning.Exclude
	}
	if err := config.UpdateLLMProviderConfig(s.deps.ConfigPath, "openrouter", map[string]any{"reasoning": r}); err != nil {
		s.logger.Warn("failed to persist OpenRouter reasoning config", "error", err)
	}
}

// llmConfigUpdateInput holds the mutable fields for PATCH /api/v1/llm/config.
type llmConfigUpdateInput struct {
	DefaultProvider *string  `json:"default_provider,omitempty"`
	DefaultModel    *string  `json:"default_model,omitempty"`
	CostLimitSoft   *float64 `json:"cost_limit_soft,omitempty"`
	CostLimitHard   *float64 `json:"cost_limit_hard,omitempty"`
}

// handlePatchLLMConfig godoc
// @Summary Update global LLM configuration
// @Description Updates default provider, model, and cost limits. Syncs limits to live CostTracker.
// @Tags providers
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body llmConfigUpdateInput true "Fields to update"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Router /llm/config [patch]
func (s *Server) handlePatchLLMConfig(w http.ResponseWriter, r *http.Request) {
	var input llmConfigUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if input.DefaultProvider != nil && *input.DefaultProvider != "" {
		if !s.deps.Config.LLM.HasProvider(*input.DefaultProvider) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid default_provider: " + *input.DefaultProvider + " is not a configured provider",
			})
			return
		}
	}

	if input.CostLimitSoft != nil && *input.CostLimitSoft < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cost_limit_soft must be >= 0"})
		return
	}
	if input.CostLimitHard != nil && *input.CostLimitHard < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cost_limit_hard must be >= 0"})
		return
	}

	// Apply to in-memory config.
	if input.DefaultProvider != nil {
		s.deps.Config.LLM.DefaultProvider = *input.DefaultProvider
	}
	if input.DefaultModel != nil {
		s.deps.Config.LLM.DefaultModel = *input.DefaultModel
	}
	if input.CostLimitSoft != nil {
		s.deps.Config.LLM.CostLimitSoft = *input.CostLimitSoft
	}
	if input.CostLimitHard != nil {
		s.deps.Config.LLM.CostLimitHard = *input.CostLimitHard
	}

	// Sync the live CostTracker so new sessions use updated limits.
	if input.CostLimitSoft != nil || input.CostLimitHard != nil {
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Sunset", "2026-08-01")
		s.deps.CostTracker.SetDefaultLimits(llm.SessionLimits{
			Soft: s.deps.Config.LLM.CostLimitSoft,
			Hard: s.deps.Config.LLM.CostLimitHard,
		})
	}

	// Persist to TOML.
	s.persistLLMConfig(&input)

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) persistLLMConfig(input *llmConfigUpdateInput) {
	if s.deps.ConfigPath == "" {
		return
	}

	changes := make(map[string]any)
	if input.DefaultProvider != nil {
		changes["default_provider"] = *input.DefaultProvider
	}
	if input.DefaultModel != nil {
		changes["default_model"] = *input.DefaultModel
	}
	if input.CostLimitSoft != nil {
		changes["cost_limit_soft"] = *input.CostLimitSoft
	}
	if input.CostLimitHard != nil {
		changes["cost_limit_hard"] = *input.CostLimitHard
	}
	if len(changes) > 0 {
		if err := config.UpdateLLMConfig(s.deps.ConfigPath, changes); err != nil {
			s.logger.Warn("failed to persist LLM config", "error", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Provider create & delete
// ---------------------------------------------------------------------------

// providerCreateInput holds the fields for POST /api/v1/llm/providers.
type providerCreateInput struct {
	Name                  string                             `json:"name"`
	Type                  string                             `json:"type"`
	APIKey                string                             `json:"api_key,omitempty"`
	Auth                  string                             `json:"auth,omitempty"`
	OAuthToken            string                             `json:"oauth_token,omitempty"`
	BaseURL               string                             `json:"base_url,omitempty"`
	Organization          string                             `json:"organization,omitempty"`
	CostLimitSoft         *float64                           `json:"cost_limit_soft,omitempty"`
	CostLimitHard         *float64                           `json:"cost_limit_hard,omitempty"`
	DefaultRatePerKTokens *float64                           `json:"default_rate_per_1k_tokens,omitempty"`
	ModelPrices           map[string]config.ModelPriceConfig `json:"model_prices,omitempty"`
}

// handleCreateLLMProvider godoc
// @Summary Create LLM provider
// @Description Creates a new named provider instance and persists to TOML
// @Tags providers
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body providerCreateInput true "Provider configuration"
// @Success 201 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /llm/providers [post]
func (s *Server) handleCreateLLMProvider(w http.ResponseWriter, r *http.Request) {
	if s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "config persistence not available",
		})
		return
	}

	var input providerCreateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON: " + err.Error(),
		})
		return
	}

	if !config.ValidResourceName(input.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid provider name: must be lowercase alphanumeric with hyphens, 1-64 chars",
		})
		return
	}

	if !config.ValidProviderType(input.Type) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid provider type: must be one of: anthropic, openai, openrouter, ollama",
		})
		return
	}

	if s.deps.Config.LLM.HasProvider(input.Name) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "provider already exists: " + input.Name,
		})
		return
	}

	if msg := validateBaseURL(input.BaseURL); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	if input.Organization != "" && input.Type != "openai" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "organization is only supported for openai-type providers",
		})
		return
	}

	if msg := validateProviderAuth(input.Auth, input.Type, input.OAuthToken); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	if msg := validateCostFields(input.CostLimitSoft, input.CostLimitHard, input.DefaultRatePerKTokens); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	// Persist to TOML — source of truth.
	if err := config.AddLLMProviderToConfig(s.deps.ConfigPath, config.ProviderInstanceConfig{
		Name:         input.Name,
		Type:         input.Type,
		APIKey:       input.APIKey,
		BaseURL:      input.BaseURL,
		Organization: input.Organization,
		Auth:         input.Auth,
		OAuthToken:   input.OAuthToken,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "persisting provider to config: " + err.Error(),
		})
		return
	}

	costPersistErr := s.persistCreateProviderCosts(input.Name, input.CostLimitSoft, input.CostLimitHard, input.DefaultRatePerKTokens, input.ModelPrices)

	// Update in-memory config.
	s.deps.Config.LLM.Providers = append(s.deps.Config.LLM.Providers, config.ProviderInstanceConfig{
		Name:                  input.Name,
		Type:                  input.Type,
		APIKey:                input.APIKey,
		Auth:                  input.Auth,
		OAuthToken:            input.OAuthToken,
		BaseURL:               input.BaseURL,
		Organization:          input.Organization,
		CostLimitSoft:         input.CostLimitSoft,
		CostLimitHard:         input.CostLimitHard,
		DefaultRatePerKTokens: input.DefaultRatePerKTokens,
		ModelPrices:           input.ModelPrices,
	})

	if s.deps.Auditor != nil {
		s.deps.Auditor.Emit(r.Context(), audit.Event{
			Category: audit.CategoryConfig,
			Action:   "create_provider",
			Summary:  "Created LLM provider " + input.Name + " (type: " + input.Type + ")",
			Status:   audit.StatusOK,
			Source:   "api",
		})
	}

	resp := map[string]string{
		"name":   input.Name,
		"status": "created",
	}
	if costPersistErr != nil {
		resp["warning"] = "cost fields not persisted to config: " + costPersistErr.Error()
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleDeleteLLMProvider godoc
// @Summary Delete LLM provider
// @Description Removes a provider instance. Rejects if referenced by agents or default_provider.
// @Tags providers
// @Produce json
// @Security BearerAuth
// @Param name path string true "Provider name"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /llm/providers/{name} [delete]
func (s *Server) handleDeleteLLMProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing provider name"})
		return
	}
	if s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "config persistence not available",
		})
		return
	}

	if !s.deps.Config.LLM.HasProvider(name) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "provider not found: " + name,
		})
		return
	}

	// Check dependencies (default_provider, agent llm_provider, fallbacks).
	if config.IsProviderReferenced(s.deps.Config, name) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "provider is in use: referenced as default_provider, by an agent, or by a fallback rule",
		})
		return
	}

	// Persist removal to TOML — source of truth.
	if err := config.RemoveLLMProviderFromConfig(s.deps.ConfigPath, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "removing provider from config: " + err.Error(),
		})
		return
	}

	// Remove from in-memory config.
	providers := s.deps.Config.LLM.Providers
	for i := range providers {
		if providers[i].Name == name {
			s.deps.Config.LLM.Providers = append(providers[:i], providers[i+1:]...)
			break
		}
	}

	if s.deps.Auditor != nil {
		s.deps.Auditor.Emit(r.Context(), audit.Event{
			Category: audit.CategoryConfig,
			Action:   "delete_provider",
			Summary:  "Deleted LLM provider " + name,
			Status:   audit.StatusOK,
			Source:   "api",
		})
	}

	w.WriteHeader(http.StatusNoContent)
}
