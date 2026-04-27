package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Provider defines the interface for LLM backends.
type Provider interface {
	ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	Name() string
	HealthCheck(ctx context.Context) error
}

// StreamChunk carries a single incremental piece of a streaming response.
type StreamChunk struct {
	ContentDelta  string // incremental text content
	ThinkingDelta string // incremental thinking/reasoning content
}

// StreamCallback is invoked for each chunk during a streaming LLM call.
// It is called synchronously from the provider's HTTP response reader.
type StreamCallback func(chunk StreamChunk)

// StreamingProvider is an optional interface. Providers that implement it
// honour the OnStream callback field on ChatRequest.
type StreamingProvider interface {
	Provider
	SupportsStreaming() bool
}

// ModelLister is an optional interface implemented by providers that can
// enumerate their available models.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// ModelDetailLister is an optional interface for providers that can return
// enriched model metadata (pricing, capabilities). Providers that implement
// this are preferred over the static heuristic in Router.ListModelDetails.
type ModelDetailLister interface {
	ListModelDetails(ctx context.Context) ([]ModelInfo, error)
}

// LLMError is returned by providers for API-level failures.
// Use errors.As to unwrap from wrapped errors.
type LLMError struct {
	StatusCode int
	Message    string
}

func (e *LLMError) Error() string {
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Message)
}

// Retryable reports whether the error is worth retrying.
// Non-retryable: 400 (bad request), 401 (auth), 402 (payment), 404 (not found), 422 (unprocessable).
// Retryable: 429 (rate limit), 5xx (server errors), and any unrecognised status.
func (e *LLMError) Retryable() bool {
	switch e.StatusCode {
	case 400, 401, 402, 404, 422:
		return false
	default:
		return true
	}
}

// isRetryable returns true if err is worth retrying.
// Network/context errors are always considered retryable.
// LLMErrors are retryable based on their status code.
func isRetryable(err error) bool {
	var llmErr *LLMError
	if errors.As(err, &llmErr) {
		return llmErr.Retryable()
	}
	return true // network error or unknown — assume retryable
}

// isRateLimit returns true if err is specifically a 429 rate-limit error.
func isRateLimit(err error) bool {
	var llmErr *LLMError
	return errors.As(err, &llmErr) && llmErr.StatusCode == 429
}

type ChatRequest struct {
	Model             string
	Messages          []Message
	MaxTokens         int
	Temperature       *float64
	Tools             []ToolDef
	OnStream          StreamCallback // if non-nil, provider streams chunks via this callback
	StreamIdleTimeout time.Duration  // if > 0, cancel the SSE stream if no data arrives within this duration
}

type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"` // reasoning/thinking from previous turn
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type ChatResponse struct {
	Content         string
	ThinkingContent string // accumulated thinking/reasoning content (if model supports it)
	ToolCalls       []ToolCall
	TokensUsed      TokenUsage
	Model           string
	FinishReason    string
	CostUSD         float64 // provider-reported or estimated cost in USD
}

// ToolCall represents a tool invocation requested by the LLM (OpenAI format).
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall is the function name and JSON-encoded arguments within a ToolCall.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a tool available for the LLM to call (OpenAI format).
type ToolDef struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef describes the function signature within a ToolDef.
type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema object
}

type TokenUsage struct {
	Prompt       int
	Completion   int
	CachedPrompt int // tokens served from cache (Anthropic cache_read, OpenAI cached_tokens)
	Total        int
}

type ProviderMetadata struct {
	Name    string
	BaseURL string
	Models  []string
}

// ModelInfo holds enriched metadata about an available LLM model.
type ModelInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Provider      string   `json:"provider"`
	InputPerMTok  *float64 `json:"input_per_mtok"`  // nil = pricing unknown
	OutputPerMTok *float64 `json:"output_per_mtok"` // nil = pricing unknown
	SupportsTools bool     `json:"supports_tools"`
	WeeklyTokens  int64    `json:"weekly_tokens"` // 0 = unknown; used for popularity sort
}
