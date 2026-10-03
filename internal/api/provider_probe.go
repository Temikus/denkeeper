package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/llm/llmfactory"
)

// probeTimeout bounds one key test end to end.
const probeTimeout = 10 * time.Second

// providerTestInput is the body of POST /api/v1/llm/providers/test. With
// name set and api_key empty, the stored instance's key and URL are tested.
type providerTestInput struct {
	Type         string `json:"type"`
	APIKey       string `json:"api_key,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	Organization string `json:"organization,omitempty"`
	Name         string `json:"name,omitempty"`
}

// providerTestResponse classifies a key test. Status is ok, rejected (the
// upstream refused the key), unreachable (no answer) or error.
type providerTestResponse struct {
	Status     string   `json:"status"`
	Message    string   `json:"message"`
	ModelCount int      `json:"model_count"`
	Models     []string `json:"models,omitempty"`
}

var providerLabels = map[string]string{
	"anthropic":  "Anthropic",
	"openai":     "OpenAI",
	"openrouter": "OpenRouter",
	"ollama":     "Ollama",
}

// probeHTTPClient refuses redirects: Go re-sends custom headers such as
// Anthropic's x-api-key across hosts, so following one could leak the key.
func probeHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// handleTestLLMProvider godoc
// @Summary Test LLM provider credentials
// @Description Checks a key and base URL against the provider without saving anything, and lists the models it can see. Always answers 200 with a status: an upstream refusal is a result, not an auth failure of this request. The key is never logged or echoed.
// @Tags providers
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body providerTestInput true "Provider to test"
// @Success 200 {object} providerTestResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /llm/providers/test [post]
func (s *Server) handleTestLLMProvider(w http.ResponseWriter, r *http.Request) {
	var input providerTestInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	pc, status, msg := s.resolveProbeTarget(&input)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}

	p, err := llmfactory.New(pc, config.OpenRouterConfig{}, probeHTTPClient())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	resp := probeProvider(ctx, p, pc)
	s.logger.Info("provider key test", "type", pc.Type, "status", resp.Status)
	writeJSON(w, http.StatusOK, resp)
}

// resolveProbeTarget validates input and fills in a stored instance's
// credentials when input names one. A non-zero status means reject.
func (s *Server) resolveProbeTarget(input *providerTestInput) (config.ProviderInstanceConfig, int, string) {
	pc := config.ProviderInstanceConfig{
		Name: "probe", Type: input.Type, APIKey: input.APIKey,
		BaseURL: input.BaseURL, Organization: input.Organization,
	}
	if input.Name != "" && input.APIKey == "" {
		stored := s.findProviderInstance(input.Name)
		if stored == nil {
			return pc, http.StatusNotFound, "provider not found: " + input.Name
		}
		pc.Type, pc.APIKey = stored.Type, stored.APIKey
		if pc.BaseURL == "" {
			pc.BaseURL = stored.BaseURL
		}
		if pc.Organization == "" {
			pc.Organization = stored.Organization
		}
	}
	if !config.ValidProviderType(pc.Type) {
		return pc, http.StatusBadRequest, "invalid provider type: must be one of: anthropic, openai, openrouter, ollama"
	}
	if msg := validateBaseURL(pc.BaseURL); msg != "" {
		return pc, http.StatusBadRequest, msg
	}
	if pc.Organization != "" && pc.Type != "openai" {
		return pc, http.StatusBadRequest, "organization is only supported for openai-type providers"
	}
	if pc.Type != "ollama" && pc.APIKey == "" {
		return pc, http.StatusBadRequest, "api_key is required for " + pc.Type
	}
	return pc, 0, ""
}

// probeProvider checks credentials (where listing models cannot prove them)
// and then lists models.
func probeProvider(ctx context.Context, p llm.Provider, pc config.ProviderInstanceConfig) providerTestResponse {
	if cc, ok := p.(llm.CredentialChecker); ok {
		if err := cc.CheckCredentials(ctx); err != nil {
			return classifyProbeErr(ctx, err, pc)
		}
	}
	lister, ok := p.(llm.ModelLister)
	if !ok {
		return providerTestResponse{Status: "ok", Message: "Connected."}
	}
	models, err := lister.ListModels(ctx)
	if err != nil {
		return classifyProbeErr(ctx, err, pc)
	}
	sort.Strings(models)
	resp := providerTestResponse{Status: "ok", ModelCount: len(models), Models: models}
	switch {
	case len(models) == 0 && pc.Type == "ollama":
		resp.Message = "Ollama is running but has no models yet. Run `ollama pull llama3.2` and test again."
	case pc.Type == "ollama":
		resp.Message = fmt.Sprintf("Ollama is running. %d models available.", len(models))
	default:
		resp.Message = fmt.Sprintf("Key works. %d models available.", len(models))
	}
	return resp
}

// classifyProbeErr turns a probe failure into a user-facing result. It never
// includes the upstream body or the request URL, which could carry secrets.
func classifyProbeErr(ctx context.Context, err error, pc config.ProviderInstanceConfig) providerTestResponse {
	label := providerLabels[pc.Type]
	var le *llm.LLMError
	if errors.As(err, &le) {
		switch le.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return providerTestResponse{Status: "rejected", Message: label + " says this key is not valid. Check you copied all of it."}
		case http.StatusNotFound:
			return providerTestResponse{Status: "error", Message: "That base URL doesn't look like the " + label + " API."}
		case http.StatusTooManyRequests:
			return providerTestResponse{Status: "error", Message: label + " is rate limiting this key. Try again in a minute."}
		default:
			return providerTestResponse{Status: "error", Message: fmt.Sprintf("%s answered with status %d.", label, le.StatusCode)}
		}
	}
	if pc.Type == "ollama" {
		return providerTestResponse{Status: "unreachable", Message: "Nothing answered here. Is Ollama running? Try `ollama serve`."}
	}
	if ctx.Err() != nil {
		return providerTestResponse{Status: "unreachable", Message: "No answer from " + label + " within 10 seconds."}
	}
	return providerTestResponse{Status: "unreachable", Message: "Couldn't reach " + label + ". Check the base URL and your network."}
}
