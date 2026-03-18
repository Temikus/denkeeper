package llm

import "context"

// Provider defines the interface for LLM backends.
type Provider interface {
	ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	Name() string
	HealthCheck(ctx context.Context) error
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature *float64
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Content      string
	TokensUsed   TokenUsage
	Model        string
	FinishReason string
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
