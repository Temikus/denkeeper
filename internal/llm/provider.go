package llm

import (
	"context"
	"errors"
	"fmt"
)

// Provider defines the interface for LLM backends.
type Provider interface {
	ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	Name() string
	HealthCheck(ctx context.Context) error
}

// BalanceProvider is an optional interface implemented by providers that can
// report their remaining credit balance. Returns -1 if the balance is unlimited.
type BalanceProvider interface {
	FundsRemaining(ctx context.Context) (float64, error)
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
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature *float64
	Tools       []ToolDef
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	TokensUsed   TokenUsage
	Model        string
	FinishReason string
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
	Prompt     int
	Completion int
	Total      int
}

type ProviderMetadata struct {
	Name       string
	BaseURL    string
	Models     []string
}
