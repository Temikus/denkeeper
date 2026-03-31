// Package websearch provides a pluggable web search interface with multiple
// provider backends (DuckDuckGo, Tavily, etc.).
package websearch

import (
	"context"
	"fmt"

	"github.com/Temikus/denkeeper/internal/config"
)

// Result represents a single search result.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Provider performs web searches and returns structured results.
type Provider interface {
	Search(ctx context.Context, query string, maxResults int) ([]Result, error)
	Name() string
}

// NewProvider creates a search provider based on config.
func NewProvider(cfg config.WebSearchConfig) (Provider, error) {
	switch cfg.Provider {
	case "duckduckgo":
		return NewDuckDuckGo(), nil
	case "tavily":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("tavily provider requires api_key")
		}
		return NewTavily(cfg.APIKey), nil
	default:
		return nil, fmt.Errorf("unsupported search provider: %q", cfg.Provider)
	}
}
