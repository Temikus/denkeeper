package llm

import (
	"context"
	"fmt"
)

// Router selects the appropriate LLM provider for a request.
type Router struct {
	providers map[string]Provider
	defaultProvider string
	defaultModel    string
	costTracker     *CostTracker
}

func NewRouter(defaultProvider, defaultModel string, costTracker *CostTracker) *Router {
	return &Router{
		providers:       make(map[string]Provider),
		defaultProvider: defaultProvider,
		defaultModel:    defaultModel,
		costTracker:     costTracker,
	}
}

func (r *Router) RegisterProvider(p Provider) {
	r.providers[p.Name()] = p
}

func (r *Router) Complete(ctx context.Context, sessionID string, messages []Message) (*ChatResponse, error) {
	provider, ok := r.providers[r.defaultProvider]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", r.defaultProvider)
	}

	if r.costTracker.ExceedsBudget(sessionID) {
		return nil, fmt.Errorf("session %q exceeded cost budget", sessionID)
	}

	req := ChatRequest{
		Model:    r.defaultModel,
		Messages: messages,
	}

	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	// Estimate cost (rough: $0.01 per 1K tokens as placeholder)
	cost := float64(resp.TokensUsed.Total) / 1000.0 * 0.01
	r.costTracker.Record(sessionID, cost)

	return resp, nil
}

func (r *Router) HealthCheck(ctx context.Context) error {
	for name, p := range r.providers {
		if err := p.HealthCheck(ctx); err != nil {
			return fmt.Errorf("provider %q health check failed: %w", name, err)
		}
	}
	return nil
}
