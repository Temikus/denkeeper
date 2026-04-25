package api

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

// providerInfo is the JSON shape returned by GET /api/v1/llm/providers.
type providerInfo struct {
	Name         string                         `json:"name"`
	Type         string                         `json:"type"`
	Enabled      bool                           `json:"enabled"`
	APIKeySet    bool                           `json:"api_key_set"`
	BaseURL      string                         `json:"base_url,omitempty"`
	Organization string                         `json:"organization,omitempty"`
	Reasoning    *config.OpenRouterReasoningCfg `json:"reasoning,omitempty"`
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
		pi := providerInfo{
			Name:         pc.Name,
			Type:         pc.Type,
			Enabled:      pc.APIKey != "" || pc.Type == "ollama",
			APIKeySet:    pc.APIKey != "",
			BaseURL:      pc.BaseURL,
			Organization: pc.Organization,
		}
		if pc.Type == "openrouter" {
			r := cfg.OpenRouter.Reasoning
			pi.Reasoning = &r
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
	APIKey       *string                        `json:"api_key,omitempty"`
	BaseURL      *string                        `json:"base_url,omitempty"`
	Organization *string                        `json:"organization,omitempty"`
	Reasoning    *config.OpenRouterReasoningCfg `json:"reasoning,omitempty"`
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

	var input providerUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Validate base_url if provided.
	if input.BaseURL != nil && *input.BaseURL != "" {
		u, err := url.Parse(*input.BaseURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid base_url: must be a valid URL with scheme and host",
			})
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

	// Apply to in-memory config and persist.
	s.applyLLMProviderUpdate(name, &input)
	s.persistLLMProvider(name, &input)

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
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

func (s *Server) applyLLMProviderUpdate(name string, input *providerUpdateInput) {
	pc := s.findProviderInstance(name)
	if pc == nil {
		return
	}
	if input.APIKey != nil {
		pc.APIKey = *input.APIKey
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

	// Keep legacy structs in sync for backward compat.
	s.syncLegacyProviderConfig(name, pc)
}

// syncLegacyProviderConfig mirrors changes from a ProviderInstanceConfig back
// to the old-style config struct fields so existing code paths that read the
// legacy fields stay correct.
func (s *Server) syncLegacyProviderConfig(name string, pc *config.ProviderInstanceConfig) {
	switch name {
	case "anthropic":
		s.deps.Config.LLM.Anthropic.APIKey = pc.APIKey
		s.deps.Config.LLM.Anthropic.BaseURL = pc.BaseURL
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

func (s *Server) persistLLMProvider(name string, input *providerUpdateInput) {
	if s.deps.ConfigPath == "" {
		return
	}

	changes := make(map[string]any)
	if input.APIKey != nil {
		changes["api_key"] = *input.APIKey
	}
	if input.BaseURL != nil {
		changes["base_url"] = *input.BaseURL
	}
	if input.Organization != nil {
		changes["organization"] = *input.Organization
	}
	if len(changes) > 0 {
		if err := tool.UpdateLLMProviderInstanceConfig(s.deps.ConfigPath, name, changes); err != nil {
			s.logger.Warn("failed to persist LLM provider config", "provider", name, "error", err)
		}
	}

	// Reasoning config lives in [llm.openrouter.reasoning], not in [[llm.providers]].
	if input.Reasoning != nil {
		r := make(map[string]any)
		if input.Reasoning.Enabled != nil {
			r["enabled"] = *input.Reasoning.Enabled
		}
		if input.Reasoning.Effort != "" {
			r["effort"] = input.Reasoning.Effort
		}
		if input.Reasoning.MaxTokens > 0 {
			r["max_tokens"] = input.Reasoning.MaxTokens
		}
		if input.Reasoning.Exclude != nil {
			r["exclude"] = *input.Reasoning.Exclude
		}
		if err := tool.UpdateLLMProviderConfig(s.deps.ConfigPath, "openrouter", map[string]any{"reasoning": r}); err != nil {
			s.logger.Warn("failed to persist OpenRouter reasoning config", "error", err)
		}
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
		if err := tool.UpdateLLMConfig(s.deps.ConfigPath, changes); err != nil {
			s.logger.Warn("failed to persist LLM config", "error", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Provider create & delete
// ---------------------------------------------------------------------------

// providerCreateInput holds the fields for POST /api/v1/llm/providers.
type providerCreateInput struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	APIKey       string `json:"api_key,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	Organization string `json:"organization,omitempty"`
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

	if input.BaseURL != "" {
		u, err := url.Parse(input.BaseURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid base_url: must be a valid URL with scheme and host",
			})
			return
		}
	}

	if input.Organization != "" && input.Type != "openai" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "organization is only supported for openai-type providers",
		})
		return
	}

	// Persist to TOML — source of truth.
	if err := tool.AddLLMProviderToConfig(
		s.deps.ConfigPath, input.Name, input.Type,
		input.APIKey, input.BaseURL, input.Organization,
	); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "persisting provider to config: " + err.Error(),
		})
		return
	}

	// Update in-memory config.
	s.deps.Config.LLM.Providers = append(s.deps.Config.LLM.Providers, config.ProviderInstanceConfig{
		Name:         input.Name,
		Type:         input.Type,
		APIKey:       input.APIKey,
		BaseURL:      input.BaseURL,
		Organization: input.Organization,
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

	writeJSON(w, http.StatusCreated, map[string]string{
		"name":   input.Name,
		"status": "created",
	})
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
	if err := tool.RemoveLLMProviderFromConfig(s.deps.ConfigPath, name); err != nil {
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
