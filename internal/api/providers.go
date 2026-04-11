package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"github.com/Temikus/denkeeper/internal/tool"
)

// knownProviders is the ordered list of LLM provider names we expose in the UI.
var knownProviders = []string{"anthropic", "openrouter", "openai", "ollama"}

// providerInfo is the JSON shape returned by GET /api/v1/llm/providers.
type providerInfo struct {
	Name         string `json:"name"`
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

	providers := []providerInfo{
		{
			Name:      "anthropic",
			Enabled:   cfg.Anthropic.APIKey != "",
			APIKeySet: cfg.Anthropic.APIKey != "",
			BaseURL:   cfg.Anthropic.BaseURL,
		},
		{
			Name:      "openrouter",
			Enabled:   cfg.OpenRouter.APIKey != "",
			APIKeySet: cfg.OpenRouter.APIKey != "",
		},
		{
			Name:         "openai",
			Enabled:      cfg.OpenAI.APIKey != "",
			APIKeySet:    cfg.OpenAI.APIKey != "",
			BaseURL:      cfg.OpenAI.BaseURL,
			Organization: cfg.OpenAI.Organization,
		},
		{
			Name:    "ollama",
			Enabled: true, // Ollama is always enabled (local, no API key).
			BaseURL: cfg.Ollama.BaseURL,
		},
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
	if !slices.Contains(knownProviders, name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "unknown provider: must be one of anthropic, openrouter, openai, ollama",
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

	// Organization is only valid for OpenAI.
	if input.Organization != nil && name != "openai" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "organization is only supported for the openai provider",
		})
		return
	}

	// Apply to in-memory config and persist.
	s.applyLLMProviderUpdate(name, &input)
	s.persistLLMProvider(name, &input)

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) applyLLMProviderUpdate(name string, input *providerUpdateInput) {
	switch name {
	case "anthropic":
		if input.APIKey != nil {
			s.deps.Config.LLM.Anthropic.APIKey = *input.APIKey
		}
		if input.BaseURL != nil {
			s.deps.Config.LLM.Anthropic.BaseURL = *input.BaseURL
		}
	case "openrouter":
		if input.APIKey != nil {
			s.deps.Config.LLM.OpenRouter.APIKey = *input.APIKey
		}
	case "openai":
		if input.APIKey != nil {
			s.deps.Config.LLM.OpenAI.APIKey = *input.APIKey
		}
		if input.BaseURL != nil {
			s.deps.Config.LLM.OpenAI.BaseURL = *input.BaseURL
		}
		if input.Organization != nil {
			s.deps.Config.LLM.OpenAI.Organization = *input.Organization
		}
	case "ollama":
		if input.BaseURL != nil {
			s.deps.Config.LLM.Ollama.BaseURL = *input.BaseURL
		}
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
		if err := tool.UpdateLLMProviderConfig(s.deps.ConfigPath, name, changes); err != nil {
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
		if !slices.Contains(knownProviders, *input.DefaultProvider) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid default_provider: must be one of anthropic, openrouter, openai, ollama",
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
