package webfetch

import (
	"context"
	"log/slog"
	"strings"
)

// ChainFetcher tries the primary fetcher first. If the result content looks
// empty (common with JS-rendered pages), it falls back to an enhanced fetcher.
type ChainFetcher struct {
	primary  Fetcher
	fallback Fetcher // nil if no enhanced fetcher configured
	logger   *slog.Logger
}

// NewChainFetcher creates a fetcher that chains primary → fallback.
// If fallback is nil, it behaves identically to the primary fetcher.
func NewChainFetcher(primary, fallback Fetcher, logger *slog.Logger) *ChainFetcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &ChainFetcher{primary: primary, fallback: fallback, logger: logger}
}

// Name returns the fetcher identifier.
func (c *ChainFetcher) Name() string { return "chain" }

// Fetch tries the primary fetcher; falls back to the enhanced fetcher if the
// primary result appears empty or too short (likely a JS-rendered page).
func (c *ChainFetcher) Fetch(ctx context.Context, url string) (*FetchResult, error) {
	result, err := c.primary.Fetch(ctx, url)
	if err != nil {
		if c.fallback == nil {
			return nil, err
		}
		c.logger.Debug("primary fetch failed, trying fallback",
			"url", url, "primary", c.primary.Name(), "fallback", c.fallback.Name(), "error", err)
		return c.fallback.Fetch(ctx, url)
	}

	// If content looks empty, try fallback.
	if c.fallback != nil && looksEmpty(result.Content) {
		c.logger.Debug("primary fetch returned empty content, trying fallback",
			"url", url, "primary", c.primary.Name(), "fallback", c.fallback.Name())
		fbResult, fbErr := c.fallback.Fetch(ctx, url)
		if fbErr != nil {
			// Fallback failed; return the primary result (even if sparse).
			c.logger.Debug("fallback fetch also failed", "url", url, "error", fbErr)
			return result, nil
		}
		return fbResult, nil
	}

	return result, nil
}

// looksEmpty returns true if content has very little meaningful text,
// suggesting the page relies on JavaScript rendering.
func looksEmpty(content string) bool {
	trimmed := strings.TrimSpace(content)
	// Less than 100 chars of content is likely a JS-only page.
	return len(trimmed) < 100
}
