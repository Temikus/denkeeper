// Package llmfactory builds llm.Provider clients from config. It sits outside
// package llm because llm cannot import its provider subpackages.
package llmfactory

import (
	"fmt"
	"net/http"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	anthropicllm "github.com/Temikus/denkeeper/internal/llm/anthropic"
	"github.com/Temikus/denkeeper/internal/llm/ollama"
	openaillm "github.com/Temikus/denkeeper/internal/llm/openai"
	"github.com/Temikus/denkeeper/internal/llm/openrouter"
)

// New builds the client for one [[llm.providers]] instance. or carries the
// global [llm.openrouter] reasoning and routing knobs, applied to openrouter
// instances. A non-nil hc replaces the client's HTTP client.
func New(pc config.ProviderInstanceConfig, or config.OpenRouterConfig, hc *http.Client) (llm.Provider, error) {
	switch pc.Type {
	case "anthropic":
		c := anthropicllm.NewFull(pc.Name, pc.APIKey, pc.BaseURL)
		setHTTP(c, hc)
		return c, nil
	case "openai":
		c := openaillm.NewFull(pc.Name, pc.APIKey, pc.BaseURL, pc.Organization)
		setHTTP(c, hc)
		return c, nil
	case "openrouter":
		c := openrouter.NewFull(pc.Name, pc.APIKey)
		r := &or.Reasoning
		c.SetReasoning(r.Enabled, r.Effort, r.MaxTokens, r.Exclude)
		c.SetProviderRouting(or.ProviderOrder, or.ProviderAllowFallbacks, or.ResolveStickyTTL())
		setHTTP(c, hc)
		return c, nil
	case "ollama":
		c := ollama.NewFull(pc.Name, pc.BaseURL)
		setHTTP(c, hc)
		return c, nil
	default:
		return nil, fmt.Errorf("unknown provider type %q", pc.Type)
	}
}

func setHTTP(c interface{ SetHTTPClient(*http.Client) }, hc *http.Client) {
	if hc != nil {
		c.SetHTTPClient(hc)
	}
}
