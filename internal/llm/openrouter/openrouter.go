package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/Temikus/denkeeper/internal/llm"
)

var tracer = otel.Tracer("denkeeper.llm.openrouter")

const (
	defaultBaseURL  = "https://openrouter.ai/api/v1"
	modelDetailsTTL = 5 * time.Minute
)

type modelDetailsCache struct {
	models    []llm.ModelInfo
	fetchedAt time.Time
}

type Client struct {
	name    string
	apiKey  string
	baseURL string
	http    *http.Client

	reasoningEnabled   *bool
	reasoningEffort    string
	reasoningMaxTokens int
	reasoningExclude   *bool

	providerOrder          []string
	providerAllowFallbacks *bool

	// Sticky provider routing: after a successful response, prefer the upstream
	// provider that served it for stickyTTL, so the upstream's automatic prompt
	// caching keeps hitting instead of being scattered across providers. Reset
	// on errors that implicate the upstream (429/5xx/network), not on caller
	// cancellation or 4xx client errors. Disabled when stickyTTL <= 0.
	stickyTTL      time.Duration
	stickyMu       sync.Mutex
	stickyProvider string
	stickyExpiry   time.Time
	now            func() time.Time // injectable clock; nil → time.Now

	detailsMu    sync.Mutex
	detailsCache *modelDetailsCache
}

// clock returns the current time, honoring an injected clock for tests.
func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func New(apiKey string) *Client {
	return &Client{
		name:    "openrouter",
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    http.DefaultClient,
	}
}

// NewFull creates a named client.
func NewFull(name, apiKey string) *Client {
	if name == "" {
		name = "openrouter"
	}
	return &Client{
		name:    name,
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    http.DefaultClient,
	}
}

// NewWithHTTPClient creates a client with a custom HTTP client (for testing).
func NewWithHTTPClient(apiKey, baseURL string, httpClient *http.Client) *Client {
	return &Client{
		name:    "openrouter",
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    httpClient,
	}
}

// SetReasoning configures the reasoning parameter for OpenRouter requests.
func (c *Client) SetReasoning(enabled *bool, effort string, maxTokens int, exclude *bool) {
	c.reasoningEnabled = enabled
	c.reasoningEffort = effort
	c.reasoningMaxTokens = maxTokens
	c.reasoningExclude = exclude
}

// buildReasoningParam constructs the reasoning parameter for the request
// based on client config. Returns nil when reasoning is not configured.
func (c *Client) buildReasoningParam() *reasoningParam {
	// Nothing configured — don't send the parameter.
	if c.reasoningEnabled == nil && c.reasoningEffort == "" && c.reasoningMaxTokens == 0 {
		return nil
	}
	// Explicitly disabled.
	if c.reasoningEnabled != nil && !*c.reasoningEnabled {
		return nil
	}
	p := &reasoningParam{}
	if c.reasoningEffort != "" {
		p.Effort = c.reasoningEffort
	} else if c.reasoningMaxTokens > 0 {
		p.MaxTokens = c.reasoningMaxTokens
	} else {
		// Enabled without effort/max_tokens — send enabled: true.
		enabled := true
		p.Enabled = &enabled
	}
	if c.reasoningExclude != nil && *c.reasoningExclude {
		p.Exclude = c.reasoningExclude
	}
	return p
}

// SetProviderRouting configures OpenRouter upstream provider routing. order is
// an explicit preference list of provider slugs/names (e.g. "moonshotai") that
// always wins when set; allowFallbacks (when non-nil) controls whether
// OpenRouter may fall back to providers outside that list. stickyTTL (> 0)
// enables sticky routing: after a success the served provider is preferred for
// that duration so the upstream's automatic prompt caching keeps hitting
// instead of being scattered across providers. Errors reset the preference.
func (c *Client) SetProviderRouting(order []string, allowFallbacks *bool, stickyTTL time.Duration) {
	c.providerOrder = order
	c.providerAllowFallbacks = allowFallbacks
	c.stickyTTL = stickyTTL
}

// buildProviderParam constructs the provider routing parameter for the request.
// Explicit config order wins; otherwise an active sticky preference is applied
// (always with fallbacks, so a now-unavailable provider degrades gracefully).
// Returns nil when nothing is configured (the field is then omitted entirely).
func (c *Client) buildProviderParam() *providerParam {
	if len(c.providerOrder) > 0 {
		return &providerParam{Order: c.providerOrder, AllowFallbacks: c.providerAllowFallbacks}
	}
	if c.stickyTTL > 0 {
		c.stickyMu.Lock()
		prov, exp := c.stickyProvider, c.stickyExpiry
		c.stickyMu.Unlock()
		if prov != "" && c.clock().Before(exp) {
			allow := true
			return &providerParam{Order: []string{prov}, AllowFallbacks: &allow}
		}
	}
	if c.providerAllowFallbacks != nil {
		return &providerParam{AllowFallbacks: c.providerAllowFallbacks}
	}
	return nil
}

// recordProvider remembers the provider that served a successful response,
// extending the sticky window. No-op when stickiness is off or name is empty.
func (c *Client) recordProvider(name string) {
	if c.stickyTTL <= 0 || name == "" {
		return
	}
	c.stickyMu.Lock()
	c.stickyProvider = name
	c.stickyExpiry = c.clock().Add(c.stickyTTL)
	c.stickyMu.Unlock()
}

// resetSticky clears the sticky preference so the next request lets OpenRouter
// route freshly. Called on errors that implicate the upstream provider
// (429/5xx/network), not on caller cancellation or 4xx client errors.
func (c *Client) resetSticky() {
	c.stickyMu.Lock()
	c.stickyProvider = ""
	c.stickyExpiry = time.Time{}
	c.stickyMu.Unlock()
}

func (c *Client) Name() string { return c.name }

// SupportsStreaming implements llm.StreamingProvider.
func (c *Client) SupportsStreaming() bool { return true }

func (c *Client) ChatCompletion(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	ctx, span := tracer.Start(ctx, "llm.provider.call", trace.WithAttributes(
		attribute.String("gen_ai.system", c.Name()),
		attribute.String("gen_ai.request.model", req.Model),
	))
	defer func() { span.End() }()

	resp, err := c.chatCompletionInner(ctx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		var llmErr *llm.LLMError
		if errors.As(err, &llmErr) {
			span.SetAttributes(attribute.Int("http.status_code", llmErr.StatusCode))
		}
		return nil, err
	}
	span.SetAttributes(attribute.String("gen_ai.response.model", resp.Model))
	return resp, nil
}

func (c *Client) chatCompletionInner(ctx context.Context, req llm.ChatRequest) (out *llm.ChatResponse, retErr error) {
	if req.OnStream != nil {
		return c.chatCompletionStream(ctx, req)
	}
	// Sticky routing: clear the preference only when the error implicates the
	// upstream (429/5xx/network) so the next call routes freshly; a success
	// records the served provider below. Caller cancellation and 4xx client
	// errors leave the pin intact — the provider is healthy, so discarding its
	// warm cache affinity would needlessly scatter subsequent requests.
	defer func() {
		if retErr != nil && llm.IsRetryable(retErr) {
			c.resetSticky()
		}
	}()

	body := apiRequest{
		Model:     req.Model,
		Messages:  make([]apiMessage, len(req.Messages)),
		Tools:     req.Tools,
		Reasoning: c.buildReasoningParam(),
		Provider:  c.buildProviderParam(),
	}
	if req.MaxTokens > 0 {
		body.MaxTokens = &req.MaxTokens
	}
	if req.Temperature != nil {
		body.Temperature = req.Temperature
	}

	for i, m := range req.Messages {
		body.Messages[i] = apiMessage{
			Role:             m.Role,
			Content:          m.Content,
			ReasoningContent: m.ReasoningContent,
			ToolCalls:        m.ToolCalls,
			ToolCallID:       m.ToolCallID,
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.request.body.size", len(jsonBody)))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/Temikus/denkeeper")
	httpReq.Header.Set("X-Title", "Denkeeper")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.response.body.size", len(respBody)))

	if resp.StatusCode != http.StatusOK {
		return nil, &llm.LLMError{StatusCode: resp.StatusCode, Message: string(respBody)}
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	c.recordProvider(apiResp.Provider)
	return buildChatResponse(&apiResp), nil
}

// chatCompletionStream handles the streaming path using the shared OAI helper.
func (c *Client) chatCompletionStream(ctx context.Context, req llm.ChatRequest) (out *llm.ChatResponse, retErr error) {
	// Sticky routing: clear the preference only when the error implicates the
	// upstream (429/5xx/network, including a mid-stream drop); success records
	// the served provider (captured from the stream below). Caller cancellation
	// and 4xx client errors leave the pin intact.
	defer func() {
		if retErr != nil && llm.IsRetryable(retErr) {
			c.resetSticky()
		}
	}()

	body := apiStreamRequest{
		Model:         req.Model,
		Messages:      make([]apiMessage, len(req.Messages)),
		Tools:         req.Tools,
		Reasoning:     c.buildReasoningParam(),
		Provider:      c.buildProviderParam(),
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	if req.MaxTokens > 0 {
		body.MaxTokens = &req.MaxTokens
	}
	if req.Temperature != nil {
		body.Temperature = req.Temperature
	}
	for i, m := range req.Messages {
		body.Messages[i] = apiMessage{
			Role:             m.Role,
			Content:          m.Content,
			ReasoningContent: m.ReasoningContent,
			ToolCalls:        m.ToolCalls,
			ToolCallID:       m.ToolCallID,
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling stream request: %w", err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.request.body.size", len(jsonBody)))

	// Use a cancellable context so the idle timeout reader can kill the
	// connection if the provider stalls. This does NOT set a fixed deadline
	// on the entire request — it only fires when no data arrives for the
	// configured idle period (see IdleTimeoutReader).
	streamCtx, streamCancel := context.WithCancelCause(ctx)
	defer streamCancel(nil)

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/Temikus/denkeeper")
	httpReq.Header.Set("X-Title", "Denkeeper")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending stream request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		return nil, &llm.LLMError{StatusCode: resp.StatusCode, Message: string(respBody)}
	}

	idle := llm.StreamIdleConfigFor(streamCtx, req.StreamIdleTimeout, streamCancel)
	result, err := llm.ReadOAIStream(resp.Body, req.OnStream, idle)
	if err != nil {
		return nil, fmt.Errorf("reading stream: %w", err)
	}

	chatResp := &llm.ChatResponse{
		Content:         result.Content,
		ThinkingContent: result.ReasoningContent,
		ToolCalls:       result.ToolCalls,
		Model:           result.Model,
		FinishReason:    result.FinishReason,
	}
	if result.Usage != nil {
		var cachedPrompt int
		if result.Usage.PromptTokensDetails != nil {
			cachedPrompt = result.Usage.PromptTokensDetails.CachedTokens
		}
		chatResp.TokensUsed = llm.TokenUsage{
			Prompt:       result.Usage.PromptTokens - cachedPrompt,
			Completion:   result.Usage.CompletionTokens,
			CachedPrompt: cachedPrompt,
			Total:        result.Usage.TotalTokens,
		}
		chatResp.CostUSD = result.Usage.Cost
	}
	c.recordProvider(result.Provider)
	return chatResp, nil
}

// buildChatResponse converts the API response into a ChatResponse and logs
// warnings when the model returns empty content despite generating tokens.
func buildChatResponse(apiResp *apiResponse) *llm.ChatResponse {
	choice := apiResp.Choices[0]
	content := choice.Message.Content

	if content == "" && choice.Message.ReasoningContent != "" {
		slog.Warn("openrouter: model returned empty content with reasoning_content",
			"model", apiResp.Model,
			"reasoning_len", len(choice.Message.ReasoningContent),
			"finish_reason", choice.FinishReason,
			"completion_tokens", apiResp.Usage.CompletionTokens,
		)
	} else if content == "" && apiResp.Usage.CompletionTokens > 0 && choice.FinishReason == "stop" {
		slog.Warn("openrouter: model returned empty content despite generating tokens",
			"model", apiResp.Model,
			"finish_reason", choice.FinishReason,
			"completion_tokens", apiResp.Usage.CompletionTokens,
		)
	}

	var cachedPrompt int
	if apiResp.Usage.PromptTokensDetails != nil {
		cachedPrompt = apiResp.Usage.PromptTokensDetails.CachedTokens
	}

	return &llm.ChatResponse{
		Content:         content,
		ThinkingContent: choice.Message.ReasoningContent,
		ToolCalls:       choice.Message.ToolCalls,
		Model:           apiResp.Model,
		FinishReason:    choice.FinishReason,
		TokensUsed: llm.TokenUsage{
			// OpenRouter follows the OpenAI format: cached tokens are a
			// subset of prompt_tokens, so split them out.
			Prompt:       apiResp.Usage.PromptTokens - cachedPrompt,
			Completion:   apiResp.Usage.CompletionTokens,
			CachedPrompt: cachedPrompt,
			Total:        apiResp.Usage.TotalTokens,
		},
		CostUSD: apiResp.Usage.Cost,
	}
}

// ListModels returns available model IDs from the OpenRouter API.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing models returned status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing models response: %w", err)
	}

	models := make([]string, len(result.Data))
	for i, m := range result.Data {
		models[i] = m.ID
	}
	return models, nil
}

// ListModelDetails returns enriched model metadata from the OpenRouter API,
// including pricing, tool support, and weekly usage for popularity sorting.
// Results are cached for 5 minutes to avoid repeated heavy API calls.
func (c *Client) ListModelDetails(ctx context.Context) ([]llm.ModelInfo, error) {
	c.detailsMu.Lock()
	if c.detailsCache != nil && time.Since(c.detailsCache.fetchedAt) < modelDetailsTTL {
		cached := c.detailsCache.models
		c.detailsMu.Unlock()
		return cached, nil
	}
	c.detailsMu.Unlock()

	models, err := c.fetchModelDetails(ctx)
	if err != nil {
		return nil, err
	}

	c.detailsMu.Lock()
	c.detailsCache = &modelDetailsCache{models: models, fetchedAt: time.Now()}
	c.detailsMu.Unlock()

	return models, nil
}

// fetchModelDetails does the actual work of fetching from /models and /find.
func (c *Client) fetchModelDetails(ctx context.Context) ([]llm.ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing model details: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing model details returned status %d", resp.StatusCode)
	}

	var result struct {
		Data []modelsEntry `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing model details response: %w", err)
	}

	// Fetch weekly analytics + permaslug mappings (best-effort).
	permaslugs, analytics := c.fetchFrontendData(ctx)

	models := make([]llm.ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		info := llm.ModelInfo{
			ID:            m.ID,
			Name:          m.Name,
			Provider:      "openrouter",
			SupportsTools: m.supportsTools(),
		}
		if inp, err := m.Pricing.inputPerMTok(); err == nil {
			info.InputPerMTok = &inp
		}
		if out, err := m.Pricing.outputPerMTok(); err == nil {
			info.OutputPerMTok = &out
		}
		// Analytics is keyed by permaslug, not slug. Look up via mapping.
		// Variant IDs (e.g. "model:free") are pre-registered in the map.
		if pslug, ok := permaslugs[m.ID]; ok {
			if a, ok := analytics[pslug]; ok {
				info.WeeklyTokens = a.TotalPromptTokens + a.TotalCompletionTokens
			}
		}
		models = append(models, info)
	}
	return models, nil
}

const frontendBaseURL = "https://openrouter.ai/api/frontend"

const frontendTimeout = 10 * time.Second

// fetchFrontendData fetches model permaslug mappings and weekly analytics from
// the public OpenRouter frontend API. Returns empty maps on any error.
func (c *Client) fetchFrontendData(ctx context.Context) (permaslugs map[string]string, analytics map[string]analyticsEntry) {
	ctx, cancel := context.WithTimeout(ctx, frontendTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, frontendBaseURL+"/models/find", nil)
	if err != nil {
		slog.Debug("openrouter: failed to build frontend request", "error", err)
		return nil, nil
	}

	resp, err := c.http.Do(req)
	if err != nil {
		slog.Debug("openrouter: failed to fetch frontend data", "error", err)
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		slog.Debug("openrouter: frontend API returned non-200", "status", resp.StatusCode)
		return nil, nil
	}

	var result struct {
		Data struct {
			Models    []frontendModel           `json:"models"`
			Analytics map[string]analyticsEntry `json:"analytics"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Debug("openrouter: failed to parse frontend data", "error", err)
		return nil, nil
	}

	// Collect the set of variant suffixes from analytics keys (e.g. "free", "thinking").
	variantSuffixes := make(map[string]bool)
	for _, entry := range result.Data.Analytics {
		if entry.Variant != "" && entry.Variant != "standard" {
			variantSuffixes[entry.Variant] = true
		}
	}

	// Build slug → permaslug mapping. Also register variant forms
	// (e.g. "slug:free" → same permaslug) so that variant model IDs
	// from /api/v1/models resolve correctly.
	permaslugs = make(map[string]string, len(result.Data.Models)*2)
	for _, m := range result.Data.Models {
		permaslugs[m.Slug] = m.Permaslug
		for suffix := range variantSuffixes {
			permaslugs[m.Slug+":"+suffix] = m.Permaslug
		}
	}

	// Aggregate analytics across variants using the model_permaslug field.
	aggregated := make(map[string]analyticsEntry, len(result.Data.Analytics))
	for _, entry := range result.Data.Analytics {
		agg := aggregated[entry.ModelPermaslug]
		agg.TotalPromptTokens += entry.TotalPromptTokens
		agg.TotalCompletionTokens += entry.TotalCompletionTokens
		aggregated[entry.ModelPermaslug] = agg
	}
	return permaslugs, aggregated
}

type frontendModel struct {
	Slug      string `json:"slug"`
	Permaslug string `json:"permaslug"`
}

type analyticsEntry struct {
	ModelPermaslug        string `json:"model_permaslug"`
	Variant               string `json:"variant"`
	TotalCompletionTokens int64  `json:"total_completion_tokens"`
	TotalPromptTokens     int64  `json:"total_prompt_tokens"`
}

// modelsEntry represents a single model from the OpenRouter /models response.
type modelsEntry struct {
	ID                  string        `json:"id"`
	Name                string        `json:"name"`
	Pricing             modelsPricing `json:"pricing"`
	SupportedParameters []string      `json:"supported_parameters"`
}

// supportsTools checks if "tools" is in the model's supported parameters.
func (m *modelsEntry) supportsTools() bool {
	for _, p := range m.SupportedParameters {
		if p == "tools" {
			return true
		}
	}
	return false
}

// modelsPricing holds per-token pricing strings from the OpenRouter API.
type modelsPricing struct {
	Prompt     string `json:"prompt"`     // cost per token (USD)
	Completion string `json:"completion"` // cost per token (USD)
}

// inputPerMTok converts the per-token prompt price to per-million-token.
func (p modelsPricing) inputPerMTok() (float64, error) {
	var perToken float64
	if _, err := fmt.Sscanf(p.Prompt, "%f", &perToken); err != nil {
		return 0, err
	}
	return perToken * 1_000_000, nil
}

// outputPerMTok converts the per-token completion price to per-million-token.
func (p modelsPricing) outputPerMTok() (float64, error) {
	var perToken float64
	if _, err := fmt.Sscanf(p.Completion, "%f", &perToken); err != nil {
		return 0, err
	}
	return perToken * 1_000_000, nil
}

func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("creating health check request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	return nil
}

// API types (OpenAI-compatible format)

type apiRequest struct {
	Model       string          `json:"model"`
	Messages    []apiMessage    `json:"messages"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Tools       []llm.ToolDef   `json:"tools,omitempty"`
	Reasoning   *reasoningParam `json:"reasoning,omitempty"`
	Provider    *providerParam  `json:"provider,omitempty"`
}

type reasoningParam struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   *bool  `json:"exclude,omitempty"`
}

// providerParam is OpenRouter's upstream provider routing preference.
// See https://openrouter.ai/docs/features/provider-routing
type providerParam struct {
	Order          []string `json:"order,omitempty"`
	AllowFallbacks *bool    `json:"allow_fallbacks,omitempty"`
}

// apiMessage handles both outgoing requests (content as string) and incoming
// responses where some models (e.g. moonshotai/kimi-k2.5) return content as
// an array of content blocks instead of a plain string.
type apiMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"-"` // handled by MarshalJSON/UnmarshalJSON
	ReasoningContent string         `json:"-"` // reasoning/thinking from the model (populated by UnmarshalJSON)
	ToolCalls        []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

func (m apiMessage) MarshalJSON() ([]byte, error) {
	type wire struct {
		Role             string         `json:"role"`
		Content          string         `json:"content"`
		ReasoningContent string         `json:"reasoning_content,omitempty"`
		ToolCalls        []llm.ToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string         `json:"tool_call_id,omitempty"`
	}
	return json.Marshal(wire(m))
}

func (m *apiMessage) UnmarshalJSON(data []byte) error {
	type wire struct {
		Role             string          `json:"role"`
		RawContent       json.RawMessage `json:"content"`
		Reasoning        string          `json:"reasoning,omitempty"`
		ReasoningContent string          `json:"reasoning_content,omitempty"`
		ToolCalls        []llm.ToolCall  `json:"tool_calls,omitempty"`
		ToolCallID       string          `json:"tool_call_id,omitempty"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("unmarshaling openrouter message: %w", err)
	}
	m.Role = w.Role
	m.ToolCalls = w.ToolCalls
	m.ToolCallID = w.ToolCallID
	// OpenRouter returns reasoning in either `reasoning_content` or `reasoning`.
	m.ReasoningContent = w.ReasoningContent
	if m.ReasoningContent == "" {
		m.ReasoningContent = w.Reasoning
	}
	if len(w.RawContent) == 0 || string(w.RawContent) == "null" {
		return nil
	}
	// Try plain string first (standard case).
	var s string
	if err := json.Unmarshal(w.RawContent, &s); err == nil {
		m.Content = s
		return nil
	}
	// Fall back to array of content blocks (e.g. moonshotai/kimi-k2.5).
	var blocks []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(w.RawContent, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			switch b.Type {
			case "text":
				sb.WriteString(b.Text)
			case "thinking", "reasoning":
				// Capture thinking/reasoning blocks as fallback content.
				if m.ReasoningContent == "" && b.Text != "" {
					m.ReasoningContent = b.Text
				}
			default:
				// Unknown block type — extract any text it carries.
				if b.Text != "" {
					sb.WriteString(b.Text)
				} else if b.Content != "" {
					sb.WriteString(b.Content)
				}
			}
		}
		m.Content = sb.String()
	}
	return nil
}

type apiResponse struct {
	ID       string      `json:"id"`
	Model    string      `json:"model"`
	Provider string      `json:"provider"`
	Choices  []apiChoice `json:"choices"`
	Usage    apiUsage    `json:"usage"`
}

type apiChoice struct {
	Message      apiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type apiUsage struct {
	PromptTokens        int                       `json:"prompt_tokens"`
	CompletionTokens    int                       `json:"completion_tokens"`
	TotalTokens         int                       `json:"total_tokens"`
	PromptTokensDetails *llm.OAIPromptTokenDetail `json:"prompt_tokens_details,omitempty"`
	Cost                float64                   `json:"cost"` // real cost reported by OpenRouter
}

// Streaming request type.
type apiStreamRequest struct {
	Model         string          `json:"model"`
	Messages      []apiMessage    `json:"messages"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	Tools         []llm.ToolDef   `json:"tools,omitempty"`
	Reasoning     *reasoningParam `json:"reasoning,omitempty"`
	Provider      *providerParam  `json:"provider,omitempty"`
	Stream        bool            `json:"stream"`
	StreamOptions *streamOptions  `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}
