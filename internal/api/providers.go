package api

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/tool"
)

// providerInfo is the JSON shape returned by GET /api/v1/llm/providers.
type providerInfo struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	APIKeySet    bool   `json:"api_key_set"`
	BaseURL      string `json:"base_url,omitempty"`
	Organization string `json:"organization,omitempty"`
}

type llmProvidersResponse struct {
	DefaultProvider string         `json:"default_provider"`
	DefaultModel    string         `json:"default_model"`
	CostLimitSoft   float64        `json:"cost_limit_soft"`
	CostLimitHard   float64        `json:"cost_limit_hard"`
	Providers       []providerInfo `json:"providers"`
}

func (s *Server) handleGetLLMProviders(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.Config.LLM

	providers := make([]providerInfo, 0, len(cfg.Providers))
	for _, pc := range cfg.Providers {
		providers = append(providers, providerInfo{
			Name:         pc.Name,
			Type:         pc.Type,
			Enabled:      pc.APIKey != "" || pc.Type == "ollama",
			APIKeySet:    pc.APIKey != "",
			BaseURL:      pc.BaseURL,
			Organization: pc.Organization,
		})
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
	APIKey       *string `json:"api_key,omitempty"`
	BaseURL      *string `json:"base_url,omitempty"`
	Organization *string `json:"organization,omitempty"`
}

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
}

// llmConfigUpdateInput holds the mutable fields for PATCH /api/v1/llm/config.
type llmConfigUpdateInput struct {
	DefaultProvider *string  `json:"default_provider,omitempty"`
	DefaultModel    *string  `json:"default_model,omitempty"`
	CostLimitSoft   *float64 `json:"cost_limit_soft,omitempty"`
	CostLimitHard   *float64 `json:"cost_limit_hard,omitempty"`
}

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
