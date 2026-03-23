package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// FallbackRule describes a single fallback step the router will attempt.
type FallbackRule struct {
	Trigger    string  // "error" | "rate_limit" | "low_funds"
	Action     string  // "switch_provider" | "switch_model" | "wait_and_retry"
	Provider   string  // provider name — for switch_provider
	Model      string  // model name — for switch_model; optional for switch_provider
	Threshold  float64 // remaining credit threshold in USD — for low_funds
	MaxRetries int     // number of retry attempts — for wait_and_retry
	Backoff    string  // "exponential" | "constant" — for wait_and_retry
}

type balanceCacheEntry struct {
	balance   float64
	fetchedAt time.Time
}

const balanceCacheTTL = 5 * time.Minute

// Router selects the appropriate LLM provider for a request.
type Router struct {
	providers       map[string]Provider
	defaultProvider string
	defaultModel    string
	costTracker     *CostTracker
	fallbacks       []FallbackRule
	tools           []ToolDef
	balanceCache    map[string]balanceCacheEntry
	mu              sync.Mutex // protects balanceCache
}

// DefaultModel returns the router's default model name.
func (r *Router) DefaultModel() string { return r.defaultModel }

func NewRouter(defaultProvider, defaultModel string, costTracker *CostTracker) *Router {
	return &Router{
		providers:       make(map[string]Provider),
		defaultProvider: defaultProvider,
		defaultModel:    defaultModel,
		costTracker:     costTracker,
		balanceCache:    make(map[string]balanceCacheEntry),
	}
}

func (r *Router) RegisterProvider(p Provider) {
	r.providers[p.Name()] = p
}

// SetFallbacks configures the ordered list of fallback rules.
func (r *Router) SetFallbacks(rules []FallbackRule) {
	r.fallbacks = rules
}

// SetTools configures the tool definitions passed to every LLM request.
func (r *Router) SetTools(tools []ToolDef) {
	r.tools = tools
}

func (r *Router) Complete(ctx context.Context, sessionID string, messages []Message) (*ChatResponse, error) {
	provider, ok := r.providers[r.defaultProvider]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", r.defaultProvider)
	}

	if r.costTracker.ExceedsBudget(sessionID) {
		return nil, fmt.Errorf("session %q exceeded cost budget", sessionID)
	}

	// 1. Apply low_funds fallbacks pre-call (first matching rule wins).
	activeProvider := provider
	activeModel := r.defaultModel

	for _, rule := range r.fallbacks {
		if rule.Trigger != "low_funds" {
			continue
		}
		balance, err := r.cachedBalance(ctx, activeProvider)
		if err != nil {
			slog.Warn("could not fetch provider balance, skipping low_funds rule",
				"provider", activeProvider.Name(), "error", err)
			continue
		}
		if balance == -1 || balance >= rule.Threshold {
			continue // unlimited or sufficient funds
		}
		slog.Info("low_funds fallback triggered",
			"session", sessionID,
			"provider", activeProvider.Name(),
			"balance", balance,
			"threshold", rule.Threshold,
			"action", rule.Action,
		)
		switch rule.Action {
		case "switch_model":
			activeModel = rule.Model
		case "switch_provider":
			fp, fpOK := r.providers[rule.Provider]
			if !fpOK {
				slog.Warn("low_funds fallback provider not registered, skipping",
					"provider", rule.Provider)
				continue
			}
			activeProvider = fp
		}
		break // first matching low_funds rule wins
	}

	// 2. Make the primary call.
	req := ChatRequest{Model: activeModel, Messages: messages, Tools: r.tools}
	resp, err := activeProvider.ChatCompletion(ctx, req)
	if err == nil {
		r.costTracker.Record(sessionID, tokenCost(resp))
		return resp, nil
	}

	// 3. Non-retryable errors skip all fallbacks immediately.
	if !isRetryable(err) {
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	// 4. Apply error/rate_limit fallbacks in declaration order.
	for _, rule := range r.fallbacks {
		switch rule.Trigger {
		case "low_funds":
			continue // pre-call only

		case "rate_limit":
			if !isRateLimit(err) {
				continue // not a 429
			}
			slog.Info("rate_limit fallback triggered",
				"session", sessionID, "action", rule.Action, "error", err)
			switch rule.Action {
			case "wait_and_retry":
				err = doWaitAndRetry(ctx, rule.MaxRetries, rule.Backoff, func() error {
					resp, err = activeProvider.ChatCompletion(ctx, req)
					return err
				})
			case "switch_provider":
				fp, fpOK := r.providers[rule.Provider]
				if !fpOK {
					slog.Warn("rate_limit fallback provider not registered, skipping",
						"provider", rule.Provider)
					continue
				}
				fbModel := activeModel
				if rule.Model != "" {
					fbModel = rule.Model
				}
				resp, err = fp.ChatCompletion(ctx, ChatRequest{Model: fbModel, Messages: messages, Tools: r.tools})
			case "switch_model":
				resp, err = activeProvider.ChatCompletion(ctx,
					ChatRequest{Model: rule.Model, Messages: messages, Tools: r.tools})
			}

		case "error":
			if isRateLimit(err) {
				continue // rate_limit rules handle 429
			}
			if !isRetryable(err) {
				continue
			}
			slog.Info("error fallback triggered",
				"session", sessionID, "action", rule.Action, "original_error", err)
			switch rule.Action {
			case "switch_provider":
				fp, fpOK := r.providers[rule.Provider]
				if !fpOK {
					slog.Warn("error fallback provider not registered, skipping",
						"provider", rule.Provider)
					continue
				}
				fbModel := activeModel
				if rule.Model != "" {
					fbModel = rule.Model
				}
				resp, err = fp.ChatCompletion(ctx, ChatRequest{Model: fbModel, Messages: messages, Tools: r.tools})
			case "switch_model":
				resp, err = activeProvider.ChatCompletion(ctx,
					ChatRequest{Model: rule.Model, Messages: messages, Tools: r.tools})
			}
		}

		if err == nil {
			break // fallback succeeded
		}
		slog.Warn("fallback also failed, trying next", "session", sessionID, "error", err)
	}

	if err != nil {
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	r.costTracker.Record(sessionID, tokenCost(resp))
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

// cachedBalance returns the provider's remaining funds using a TTL cache.
// Returns -1 if the provider doesn't implement BalanceProvider or balance is unlimited.
func (r *Router) cachedBalance(ctx context.Context, p Provider) (float64, error) {
	bp, ok := p.(BalanceProvider)
	if !ok {
		return -1, nil // provider doesn't support balance queries
	}

	r.mu.Lock()
	entry, exists := r.balanceCache[p.Name()]
	r.mu.Unlock()

	if exists && time.Since(entry.fetchedAt) < balanceCacheTTL {
		return entry.balance, nil
	}

	balance, err := bp.FundsRemaining(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetching balance for %q: %w", p.Name(), err)
	}

	r.mu.Lock()
	r.balanceCache[p.Name()] = balanceCacheEntry{balance: balance, fetchedAt: time.Now()}
	r.mu.Unlock()

	return balance, nil
}

// doWaitAndRetry retries fn with backoff for up to maxRetries attempts.
// Stops early if fn returns a non-rate-limit error.
// Respects context cancellation between attempts.
func doWaitAndRetry(ctx context.Context, maxRetries int, backoff string, fn func() error) error {
	var err error
	for attempt := range maxRetries {
		var delay time.Duration
		if backoff == "constant" {
			delay = time.Second
		} else { // exponential (default)
			delay = time.Duration(1<<uint(attempt)) * time.Second
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
		if attempt > 0 {
			slog.Info("rate_limit: waiting before retry", "attempt", attempt, "delay", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		err = fn()
		if err == nil || !isRateLimit(err) {
			return err // success, or a different error type — stop retrying
		}
	}
	return err
}

// tokenCost returns the estimated USD cost for a chat response.
func tokenCost(resp *ChatResponse) float64 {
	return float64(resp.TokensUsed.Total) / 1000.0 * 0.01
}
