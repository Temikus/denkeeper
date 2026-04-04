package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Temikus/denkeeper/internal/llm/pricing"
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
	toolSource      func() []ToolDef      // dynamic tool resolution; nil = no tools
	pricing         *pricing.Registry      // model pricing lookup; nil = legacy fallback
	balanceCache    map[string]balanceCacheEntry
	mu              sync.Mutex // protects balanceCache

	// OTel instrumentation (global no-ops when OTel is disabled).
	tracer    trace.Tracer
	mDuration metric.Float64Histogram
	mTokens   metric.Int64Counter
	mCost     metric.Float64Counter
	mErrors   metric.Int64Counter
}

// DefaultModel returns the router's default model name.
func (r *Router) DefaultModel() string { return r.defaultModel }

// ListModels queries all registered providers that implement ModelLister and
// returns a de-duplicated sorted list of available model names.
func (r *Router) ListModels(ctx context.Context) []string {
	seen := make(map[string]bool)
	for _, p := range r.providers {
		lister, ok := p.(ModelLister)
		if !ok {
			continue
		}
		models, err := lister.ListModels(ctx)
		if err != nil {
			slog.Warn("listing models from provider failed", "provider", p.Name(), "error", err)
			continue
		}
		for _, m := range models {
			seen[m] = true
		}
	}
	result := make([]string, 0, len(seen))
	for m := range seen {
		result = append(result, m)
	}
	// Sort for deterministic output.
	sort.Strings(result)
	return result
}

// SetDefaultModel changes the router's default model for subsequent requests.
func (r *Router) SetDefaultModel(model string) { r.defaultModel = model }

func NewRouter(defaultProvider, defaultModel string, costTracker *CostTracker) *Router {
	meter := otel.Meter("denkeeper.llm")
	tracer := otel.Tracer("denkeeper.llm")

	dur, _ := meter.Float64Histogram("denkeeper.llm.duration",
		metric.WithDescription("LLM call latency in seconds"),
		metric.WithUnit("s"))
	tok, _ := meter.Int64Counter("denkeeper.llm.tokens",
		metric.WithDescription("LLM tokens consumed"))
	cost, _ := meter.Float64Counter("denkeeper.llm.cost",
		metric.WithDescription("LLM cost in USD"),
		metric.WithUnit("USD"))
	errs, _ := meter.Int64Counter("denkeeper.llm.errors",
		metric.WithDescription("LLM call errors"))

	return &Router{
		providers:       make(map[string]Provider),
		defaultProvider: defaultProvider,
		defaultModel:    defaultModel,
		costTracker:     costTracker,
		balanceCache:    make(map[string]balanceCacheEntry),
		tracer:          tracer,
		mDuration:       dur,
		mTokens:         tok,
		mCost:           cost,
		mErrors:         errs,
	}
}

func (r *Router) RegisterProvider(p Provider) {
	r.providers[p.Name()] = p
}

// SetFallbacks configures the ordered list of fallback rules.
func (r *Router) SetFallbacks(rules []FallbackRule) {
	r.fallbacks = rules
}

// SetPricing configures the model pricing registry used by TokenCost.
func (r *Router) SetPricing(reg *pricing.Registry) {
	r.pricing = reg
}

// SetTools configures a dynamic tool definition source. The function is
// called on every LLM request so that tools added at runtime are visible
// immediately.
func (r *Router) SetTools(source func() []ToolDef) {
	r.toolSource = source
}

// currentTools returns the current tool definitions, or nil if no source is set.
func (r *Router) currentTools() []ToolDef {
	if r.toolSource == nil {
		return nil
	}
	return r.toolSource()
}

func (r *Router) Complete(ctx context.Context, sessionID string, messages []Message) (*ChatResponse, error) {
	provider, ok := r.providers[r.defaultProvider]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", r.defaultProvider)
	}

	if r.costTracker.ExceedsBudget(sessionID) {
		return nil, fmt.Errorf("session %q exceeded cost budget", sessionID)
	}

	ctx, span := r.tracer.Start(ctx, "llm.complete",
		trace.WithAttributes(
			attribute.String("llm.provider", r.defaultProvider),
			attribute.String("llm.model", r.defaultModel),
			attribute.String("session.id", sessionID),
		))
	defer span.End()
	start := time.Now()

	attrs := metric.WithAttributes(
		attribute.String("provider", r.defaultProvider),
		attribute.String("model", r.defaultModel),
	)

	// 1. Apply low_funds fallbacks pre-call (first matching rule wins).
	activeProvider, activeModel := r.applyLowFundsFallback(ctx, sessionID, provider)

	// 2. Make the primary call.
	currentTools := r.currentTools()
	req := ChatRequest{Model: activeModel, Messages: messages, Tools: currentTools}
	resp, err := activeProvider.ChatCompletion(ctx, req)
	if err == nil {
		slog.Debug("llm completion",
			"provider", r.defaultProvider,
			"model", activeModel,
			"finish_reason", resp.FinishReason,
			"content_len", len(resp.Content),
			"tool_calls", len(resp.ToolCalls),
			"tokens_total", resp.TokensUsed.Total,
		)
		cost, source := TokenCost(resp, r.pricing)
		r.recordOTelSuccess(start, resp, cost, source, attrs)
		r.costTracker.RecordWithTokens(sessionID, cost, resp.TokensUsed.Prompt, resp.TokensUsed.Completion)
		if source == "unknown" {
			slog.Warn("no pricing data for model", "model", resp.Model)
		}
		return resp, nil
	}

	// 3. Non-retryable errors skip all fallbacks immediately.
	if !isRetryable(err) {
		r.mErrors.Add(ctx, 1, attrs)
		span.RecordError(err)
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	// 4. Apply error/rate_limit fallbacks in declaration order.
	resp, err = r.applyErrorFallbacks(ctx, sessionID, activeProvider, activeModel, messages, currentTools, err)
	if err != nil {
		r.mErrors.Add(ctx, 1, attrs)
		span.RecordError(err)
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	cost, source := TokenCost(resp, r.pricing)
	r.recordOTelSuccess(start, resp, cost, source, attrs)
	r.costTracker.RecordWithTokens(sessionID, cost, resp.TokensUsed.Prompt, resp.TokensUsed.Completion)
	if source == "unknown" {
		slog.Warn("no pricing data for model", "model", resp.Model)
	}
	return resp, nil
}

// recordOTelSuccess records duration, token, and cost metrics for a successful LLM call.
func (r *Router) recordOTelSuccess(start time.Time, resp *ChatResponse, cost float64, pricingSource string, attrs metric.MeasurementOption) {
	ctx := context.Background()
	r.mDuration.Record(ctx, time.Since(start).Seconds(), attrs)
	r.mTokens.Add(ctx, int64(resp.TokensUsed.Prompt), attrs,
		metric.WithAttributes(attribute.String("direction", "prompt")))
	r.mTokens.Add(ctx, int64(resp.TokensUsed.Completion), attrs,
		metric.WithAttributes(attribute.String("direction", "completion")))
	if resp.TokensUsed.CachedPrompt > 0 {
		r.mTokens.Add(ctx, int64(resp.TokensUsed.CachedPrompt), attrs,
			metric.WithAttributes(attribute.String("direction", "cached_prompt")))
	}
	if cost > 0 {
		r.mCost.Add(ctx, cost, attrs,
			metric.WithAttributes(attribute.String("pricing_source", pricingSource)))
	}
}

// applyLowFundsFallback checks low_funds rules and returns the provider/model
// to use for the primary call. First matching rule wins.
func (r *Router) applyLowFundsFallback(ctx context.Context, sessionID string, provider Provider) (Provider, string) {
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
			continue
		}
		slog.Info("low_funds fallback triggered",
			"session", sessionID, "provider", activeProvider.Name(),
			"balance", balance, "threshold", rule.Threshold, "action", rule.Action)
		switch rule.Action {
		case "switch_model":
			activeModel = rule.Model
		case "switch_provider":
			if fp, ok := r.providers[rule.Provider]; ok {
				activeProvider = fp
			} else {
				slog.Warn("low_funds fallback provider not registered, skipping", "provider", rule.Provider)
				continue
			}
		}
		break
	}
	return activeProvider, activeModel
}

// applyErrorFallbacks iterates error/rate_limit fallback rules after a failed
// primary call. Returns the successful response or the last error encountered.
func (r *Router) applyErrorFallbacks(ctx context.Context, sessionID string, activeProvider Provider, activeModel string, messages []Message, tools []ToolDef, lastErr error) (*ChatResponse, error) {
	var resp *ChatResponse
	err := lastErr

	for _, rule := range r.fallbacks {
		if rule.Trigger == "low_funds" {
			continue
		}
		if !r.fallbackMatchesError(rule, err) {
			continue
		}
		slog.Info("fallback triggered",
			"session", sessionID, "trigger", rule.Trigger, "action", rule.Action, "error", err)

		resp, err = r.executeFallbackAction(ctx, rule, activeProvider, activeModel, messages, tools)
		if err == nil {
			return resp, nil
		}
		slog.Warn("fallback also failed, trying next", "session", sessionID, "error", err)
	}
	return nil, err
}

// fallbackMatchesError checks whether a fallback rule applies to the given error.
func (r *Router) fallbackMatchesError(rule FallbackRule, err error) bool {
	switch rule.Trigger {
	case "rate_limit":
		return isRateLimit(err)
	case "error":
		return !isRateLimit(err) && isRetryable(err)
	}
	return false
}

// executeFallbackAction performs a single fallback action and returns the result.
func (r *Router) executeFallbackAction(ctx context.Context, rule FallbackRule, activeProvider Provider, activeModel string, messages []Message, tools []ToolDef) (*ChatResponse, error) {
	switch rule.Action {
	case "wait_and_retry":
		var resp *ChatResponse
		var callErr error
		req := ChatRequest{Model: activeModel, Messages: messages, Tools: tools}
		retryErr := doWaitAndRetry(ctx, rule.MaxRetries, rule.Backoff, func() error {
			resp, callErr = activeProvider.ChatCompletion(ctx, req)
			return callErr
		})
		return resp, retryErr

	case "switch_provider":
		fp, ok := r.providers[rule.Provider]
		if !ok {
			slog.Warn("fallback provider not registered, skipping", "provider", rule.Provider)
			return nil, fmt.Errorf("fallback provider %q not registered", rule.Provider)
		}
		model := activeModel
		if rule.Model != "" {
			model = rule.Model
		}
		return fp.ChatCompletion(ctx, ChatRequest{Model: model, Messages: messages, Tools: tools})

	case "switch_model":
		return activeProvider.ChatCompletion(ctx, ChatRequest{Model: rule.Model, Messages: messages, Tools: tools})
	}

	return nil, fmt.Errorf("unknown fallback action %q", rule.Action)
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

// TokenCost returns the USD cost for a chat response using the pricing registry.
// Priority: provider-reported cost > registry lookup > fallback rate > $0.
// Also returns a pricing source string ("provider"/"registry"/"fallback"/"unknown").
func TokenCost(resp *ChatResponse, reg *pricing.Registry) (float64, string) {
	if reg != nil {
		return reg.Cost(
			resp.Model,
			resp.TokensUsed.Prompt,
			resp.TokensUsed.Completion,
			resp.TokensUsed.CachedPrompt,
			resp.CostUSD,
		)
	}
	// Legacy fallback when no registry is configured.
	if resp.CostUSD > 0 {
		return resp.CostUSD, "provider"
	}
	return float64(resp.TokensUsed.Total) / 1000.0 * 0.01, "fallback"
}
