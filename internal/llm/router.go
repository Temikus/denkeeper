package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Temikus/denkeeper/internal/llm/pricing"
)

// FallbackRule describes a single fallback step the router will attempt.
type FallbackRule struct {
	Trigger    string // "error" | "rate_limit" | "cost_limit"
	Action     string // "switch_provider" | "switch_model" | "wait_and_retry"
	Provider   string // provider name — for switch_provider
	Model      string // model name — for switch_model; optional for switch_provider
	Scope      string // "soft" | "hard" — for cost_limit (which agent limit triggers)
	MaxRetries int    // number of retry attempts — for wait_and_retry
	Backoff    string // "exponential" | "constant" — for wait_and_retry
}

// Router selects the appropriate LLM provider for a request.
type Router struct {
	providers         map[string]Provider
	defaultProvider   string
	defaultModel      string
	costTracker       *CostTracker
	fallbacks         []FallbackRule
	toolSource        func() []ToolDef  // dynamic tool resolution; nil = no tools
	pricing           *pricing.Registry // model pricing lookup; nil = legacy fallback
	streamIdleTimeout time.Duration     // idle timeout for LLM SSE streams; 0 = disabled

	// OTel instrumentation (global no-ops when OTel is disabled).
	tracer    trace.Tracer
	mDuration metric.Float64Histogram
	mTokens   metric.Int64Counter
	mCost     metric.Float64Counter
	mErrors   metric.Int64Counter
}

// DefaultProvider returns the router's default provider name.
func (r *Router) DefaultProvider() string { return r.defaultProvider }

// DefaultModel returns the router's default model name.
func (r *Router) DefaultModel() string { return r.defaultModel }

// CostTracker returns the router's cost tracker for soft-limit checks.
func (r *Router) CostTracker() *CostTracker { return r.costTracker }

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

// ListModelDetails queries all providers and returns enriched model metadata
// including pricing and tool support. Providers implementing ModelDetailLister
// supply authoritative data; others fall back to the pricing registry and a
// static tool-support heuristic. When providerFilter is non-empty only the
// matching provider is queried, which avoids expensive remote calls.
func (r *Router) ListModelDetails(ctx context.Context, providerFilter string) []ModelInfo {
	seen := make(map[string]bool)
	var result []ModelInfo

	for _, p := range r.providers {
		if providerFilter != "" && p.Name() != providerFilter {
			continue
		}
		// Prefer rich metadata from providers that support it.
		if dl, ok := p.(ModelDetailLister); ok {
			models, err := dl.ListModelDetails(ctx)
			if err != nil {
				slog.Warn("listing model details from provider failed", "provider", p.Name(), "error", err)
				continue
			}
			for _, m := range models {
				if seen[m.ID] {
					continue
				}
				seen[m.ID] = true
				result = append(result, m)
			}
			continue
		}

		// Fall back to basic model listing with static enrichment.
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
			if seen[m] {
				continue
			}
			seen[m] = true
			info := ModelInfo{
				ID:            m,
				Name:          modelDisplayName(m),
				Provider:      p.Name(),
				SupportsTools: modelSupportsTools(m),
			}
			if r.pricing != nil {
				if price, ok := r.pricing.Lookup(m); ok {
					info.InputPerMTok = &price.InputPerMTok
					info.OutputPerMTok = &price.OutputPerMTok
				}
			}
			result = append(result, info)
		}
	}

	// Sort by popularity (weekly tokens descending), alphabetical tiebreaker.
	sort.Slice(result, func(i, j int) bool {
		if result[i].WeeklyTokens != result[j].WeeklyTokens {
			return result[i].WeeklyTokens > result[j].WeeklyTokens
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// modelDisplayName derives a human-friendly name from a model ID.
// It strips the provider prefix (e.g. "anthropic/claude-3-opus" → "claude-3-opus")
// and replaces hyphens with spaces, then title-cases.
func modelDisplayName(id string) string {
	name := id
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.ReplaceAll(name, "-", " ")
	// Title-case each word.
	words := strings.Fields(name)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// modelSupportsTools returns true if the model is known to support tool/function calling.
func modelSupportsTools(id string) bool {
	toolPrefixes := []string{
		"claude-3", "claude-sonnet-4", "claude-opus-4",
		"anthropic/claude-3", "anthropic/claude-sonnet-4", "anthropic/claude-opus-4",
		"gpt-4", "gpt-3.5-turbo",
		"openai/gpt-4", "openai/gpt-3.5-turbo",
		"o1", "o3", "o4",
		"openai/o1", "openai/o3", "openai/o4",
		"gemini-", "google/gemini-",
		"mistralai/mistral-large", "mistralai/mistral-medium", "mistralai/mistral-small",
	}
	for _, prefix := range toolPrefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

// SetDefaultModel changes the router's default model for subsequent requests.
func (r *Router) SetDefaultModel(model string) { r.defaultModel = model }

// SetDefaultProvider changes the router's default provider for subsequent requests.
// Returns an error if the provider is not registered.
func (r *Router) SetDefaultProvider(provider string) error {
	if _, ok := r.providers[provider]; !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}
	r.defaultProvider = provider
	return nil
}

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

// SetStreamIdleTimeout configures the idle timeout applied to LLM SSE streams.
// If no data arrives within this duration, the stream is cancelled. Zero disables.
func (r *Router) SetStreamIdleTimeout(d time.Duration) {
	r.streamIdleTimeout = d
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
	return r.completeInternal(ctx, sessionID, messages, nil)
}

// CompleteStream is like Complete but enables real-time streaming of content
// chunks via the onStream callback. If the active provider does not support
// streaming, it falls back to the non-streaming path transparently.
func (r *Router) CompleteStream(ctx context.Context, sessionID string, messages []Message, onStream StreamCallback) (*ChatResponse, error) {
	return r.completeInternal(ctx, sessionID, messages, onStream)
}

func (r *Router) completeInternal(ctx context.Context, sessionID string, messages []Message, onStream StreamCallback) (*ChatResponse, error) {
	provider, ok := r.providers[r.defaultProvider]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", r.defaultProvider)
	}

	ctx, span := r.tracer.Start(ctx, "llm.complete",
		trace.WithAttributes(
			attribute.String("gen_ai.system", r.defaultProvider),
			attribute.String("gen_ai.request.model", r.defaultModel),
			attribute.String("session.id", sessionID),
			attribute.Int("gen_ai.request.message_count", len(messages)),
		))
	defer span.End()
	start := time.Now()

	attrs := metric.WithAttributes(
		attribute.String("provider", r.defaultProvider),
		attribute.String("model", r.defaultModel),
	)

	// 1. Apply cost_limit fallbacks pre-call so a scope=hard rule pointing at a
	//    free provider can swap before the hard-limit guard fires.
	activeProvider, activeModel := r.applyCostLimitFallback(sessionID, provider)

	// 2. Hard-limit guard: if no fallback rerouted us off the default provider
	//    and the session is over the hard limit, refuse the call.
	if activeProvider == provider && r.costTracker.ExceedsHardLimit(sessionID) {
		return nil, fmt.Errorf("session %q exceeded hard cost limit: %w", sessionID, ErrHardLimitExceeded)
	}

	// 3. Make the primary call — enable streaming if the provider supports it.
	currentTools := r.currentTools()
	req := ChatRequest{Model: activeModel, Messages: messages, Tools: currentTools, StreamIdleTimeout: r.streamIdleTimeout}
	if onStream != nil {
		if sp, ok := activeProvider.(StreamingProvider); ok && sp.SupportsStreaming() {
			req.OnStream = onStream
		}
	}
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
		cost, source := TokenCost(resp, r.pricing, activeProvider.Name())
		r.recordOTelSuccess(start, resp, cost, source, attrs)
		r.setSpanResponseAttrs(span, resp, cost)
		r.costTracker.RecordWithProvider(sessionID, activeProvider.Name(), cost, resp.TokensUsed.Prompt, resp.TokensUsed.Completion, source)
		if source == "unknown" {
			slog.Warn("no pricing data for model", "model", resp.Model)
		}
		return resp, nil
	}

	// 4. Non-retryable errors skip all fallbacks immediately. A dead caller
	// context also skips them regardless of the error's shape — any retry
	// against an expired/cancelled context fails instantly.
	if ctx.Err() != nil || !isRetryable(err) {
		r.mErrors.Add(ctx, 1, attrs)
		span.RecordError(err)
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	// 5. Apply error/rate_limit fallbacks in declaration order.
	var resolvedProvider string
	resp, resolvedProvider, err = r.applyErrorFallbacks(ctx, sessionID, activeProvider, activeModel, messages, currentTools, onStream, err)
	if err != nil {
		r.mErrors.Add(ctx, 1, attrs)
		span.RecordError(err)
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	cost, source := TokenCost(resp, r.pricing, resolvedProvider)
	r.recordOTelSuccess(start, resp, cost, source, attrs)
	r.setSpanResponseAttrs(span, resp, cost)
	r.costTracker.RecordWithProvider(sessionID, resolvedProvider, cost, resp.TokensUsed.Prompt, resp.TokensUsed.Completion, source)
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

// setSpanResponseAttrs adds GenAI semantic convention attributes to the span
// after a successful LLM completion.
func (r *Router) setSpanResponseAttrs(span trace.Span, resp *ChatResponse, cost float64) {
	span.SetAttributes(
		attribute.String("gen_ai.response.model", resp.Model),
		attribute.String("gen_ai.response.finish_reasons", resp.FinishReason),
		attribute.Int("gen_ai.usage.input_tokens", resp.TokensUsed.Prompt),
		attribute.Int("gen_ai.usage.output_tokens", resp.TokensUsed.Completion),
	)
	if resp.TokensUsed.CachedPrompt > 0 {
		span.SetAttributes(attribute.Int("gen_ai.usage.cached_tokens", resp.TokensUsed.CachedPrompt))
	}
	if cost > 0 {
		span.SetAttributes(attribute.Float64("gen_ai.usage.cost", cost))
	}
	if len(resp.ToolCalls) > 0 {
		span.SetAttributes(attribute.Int("gen_ai.response.tool_calls", len(resp.ToolCalls)))
	}
}

// applyCostLimitFallback checks cost_limit rules against the agent's
// configured soft/hard limits in CostTracker and returns the provider/model
// to use for the primary call. First matching rule wins. Per-rule Scope
// selects which limit (soft or hard) gates the swap; an empty scope is
// treated as "soft" for backward compatibility with auto-migrated rules.
func (r *Router) applyCostLimitFallback(sessionID string, provider Provider) (Provider, string) {
	activeProvider := provider
	activeModel := r.defaultModel

	for _, rule := range r.fallbacks {
		if rule.Trigger != "cost_limit" {
			continue
		}
		var exceeded bool
		switch rule.Scope {
		case "hard":
			exceeded = r.costTracker.ExceedsHardLimit(sessionID)
		case "soft", "":
			exceeded = r.costTracker.ExceedsSoftLimit(sessionID)
		default:
			slog.Warn("cost_limit fallback has unknown scope, skipping", "scope", rule.Scope)
			continue
		}
		if !exceeded {
			continue
		}
		slog.Info("cost_limit fallback triggered",
			"session", sessionID, "provider", activeProvider.Name(),
			"scope", rule.Scope, "action", rule.Action)
		switch rule.Action {
		case "switch_model":
			activeModel = rule.Model
		case "switch_provider":
			fp, ok := r.providers[rule.Provider]
			if !ok {
				slog.Warn("cost_limit fallback provider not registered, skipping", "provider", rule.Provider)
				continue
			}
			activeProvider = fp
			if rule.Model != "" {
				activeModel = rule.Model
			}
		}
		break
	}
	return activeProvider, activeModel
}

// applyErrorFallbacks iterates error/rate_limit fallback rules after a failed
// primary call. Returns the successful response, the name of the provider that
// served it, or the last error encountered.
func (r *Router) applyErrorFallbacks(ctx context.Context, sessionID string, activeProvider Provider, activeModel string, messages []Message, tools []ToolDef, onStream StreamCallback, lastErr error) (*ChatResponse, string, error) {
	var resp *ChatResponse
	err := lastErr

	for _, rule := range r.fallbacks {
		if rule.Trigger == "cost_limit" {
			continue
		}
		if !r.fallbackMatchesError(rule, err) {
			continue
		}
		slog.Info("fallback triggered",
			"session", sessionID, "trigger", rule.Trigger, "action", rule.Action, "error", err)

		var providerName string
		resp, providerName, err = r.executeFallbackAction(ctx, rule, activeProvider, activeModel, messages, tools, onStream)
		if err == nil {
			return resp, providerName, nil
		}
		slog.Warn("fallback also failed, trying next", "session", sessionID, "error", err)
	}
	return nil, "", err
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

// executeFallbackAction performs a single fallback action and returns the
// result along with the name of the provider that served it.
func (r *Router) executeFallbackAction(ctx context.Context, rule FallbackRule, activeProvider Provider, activeModel string, messages []Message, tools []ToolDef, onStream StreamCallback) (*ChatResponse, string, error) {
	// buildReq creates a ChatRequest and enables streaming if the provider supports it.
	buildReq := func(p Provider, model string) ChatRequest {
		req := ChatRequest{Model: model, Messages: messages, Tools: tools}
		if onStream != nil {
			if sp, ok := p.(StreamingProvider); ok && sp.SupportsStreaming() {
				req.OnStream = onStream
			}
		}
		return req
	}

	switch rule.Action {
	case "wait_and_retry":
		var resp *ChatResponse
		var callErr error
		req := buildReq(activeProvider, activeModel)
		retryErr := doWaitAndRetry(ctx, rule.MaxRetries, rule.Backoff, func() error {
			resp, callErr = activeProvider.ChatCompletion(ctx, req)
			return callErr
		})
		return resp, activeProvider.Name(), retryErr

	case "switch_provider":
		fp, ok := r.providers[rule.Provider]
		if !ok {
			slog.Warn("fallback provider not registered, skipping", "provider", rule.Provider)
			return nil, "", fmt.Errorf("fallback provider %q not registered", rule.Provider)
		}
		model := activeModel
		if rule.Model != "" {
			model = rule.Model
		}
		resp, err := fp.ChatCompletion(ctx, buildReq(fp, model))
		return resp, fp.Name(), err

	case "switch_model":
		resp, err := activeProvider.ChatCompletion(ctx, buildReq(activeProvider, rule.Model))
		return resp, activeProvider.Name(), err
	}

	return nil, "", fmt.Errorf("unknown fallback action %q", rule.Action)
}

func (r *Router) HealthCheck(ctx context.Context) error {
	for name, p := range r.providers {
		if err := p.HealthCheck(ctx); err != nil {
			return fmt.Errorf("provider %q health check failed: %w", name, err)
		}
	}
	return nil
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
	return fmt.Errorf("rate limit retries exhausted (%d attempts): %w", maxRetries, err)
}

// TokenCost returns the USD cost for a chat response using the pricing registry.
// providerName selects the per-provider override layer (empty string skips it).
// Priority: provider-reported cost > registry lookup > fallback rate > $0.
// Also returns a pricing source string ("provider"/"registry"/"fallback"/"unknown").
func TokenCost(resp *ChatResponse, reg *pricing.Registry, providerName string) (float64, string) {
	if reg != nil {
		return reg.Cost(
			providerName,
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
