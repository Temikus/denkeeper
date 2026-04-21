package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/Temikus/denkeeper/internal/llm"
)

var tracer = otel.Tracer("denkeeper.llm.ollama")

const defaultBaseURL = "http://localhost:11434"

// Client implements llm.Provider using Ollama's OpenAI-compatible API.
// No API key is required; Ollama runs locally.
type Client struct {
	name    string
	baseURL string
	http    *http.Client
}

// New creates a Client pointing at baseURL. If baseURL is empty the default
// (http://localhost:11434) is used.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		name:    "ollama",
		baseURL: baseURL,
		http:    http.DefaultClient,
	}
}

// NewFull creates a named Client pointing at baseURL.
func NewFull(name, baseURL string) *Client {
	if name == "" {
		name = "ollama"
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		name:    name,
		baseURL: baseURL,
		http:    http.DefaultClient,
	}
}

// NewWithHTTPClient creates a Client with a custom HTTP client (for testing).
func NewWithHTTPClient(baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		name:    "ollama",
		baseURL: baseURL,
		http:    httpClient,
	}
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

func (c *Client) chatCompletionInner(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if req.OnStream != nil {
		return c.chatCompletionStream(ctx, req)
	}

	body := apiRequest{
		Model:    req.Model,
		Messages: make([]apiMessage, len(req.Messages)),
		Tools:    req.Tools,
	}
	if req.MaxTokens > 0 {
		body.MaxTokens = &req.MaxTokens
	}
	if req.Temperature != nil {
		body.Temperature = req.Temperature
	}

	for i, m := range req.Messages {
		body.Messages[i] = apiMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.request.body.size", len(jsonBody)))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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

	return &llm.ChatResponse{
		Content:      apiResp.Choices[0].Message.Content,
		ToolCalls:    apiResp.Choices[0].Message.ToolCalls,
		Model:        apiResp.Model,
		FinishReason: apiResp.Choices[0].FinishReason,
		TokensUsed: llm.TokenUsage{
			Prompt:     apiResp.Usage.PromptTokens,
			Completion: apiResp.Usage.CompletionTokens,
			Total:      apiResp.Usage.TotalTokens,
		},
	}, nil
}

// chatCompletionStream handles the streaming path using the shared OAI helper.
func (c *Client) chatCompletionStream(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	body := apiStreamRequest{
		Model:         req.Model,
		Messages:      make([]apiMessage, len(req.Messages)),
		Tools:         req.Tools,
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
			Role: m.Role, Content: m.Content,
			ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID,
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling stream request: %w", err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.request.body.size", len(jsonBody)))

	streamCtx, streamCancel := context.WithCancelCause(ctx)
	defer streamCancel(nil)

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

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
		chatResp.TokensUsed = llm.TokenUsage{
			Prompt:     result.Usage.PromptTokens,
			Completion: result.Usage.CompletionTokens,
			Total:      result.Usage.TotalTokens,
		}
	}
	return chatResp, nil
}

// ListModels returns available model names from the Ollama API.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("creating tags request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing models returned status %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing tags response: %w", err)
	}

	models := make([]string, len(result.Models))
	for i, m := range result.Models {
		models[i] = m.Name
	}
	return models, nil
}

// HealthCheck hits GET /api/tags which Ollama exposes on its native API.
func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("creating health check request: %w", err)
	}

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
	Model       string        `json:"model"`
	Messages    []apiMessage  `json:"messages"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Tools       []llm.ToolDef `json:"tools,omitempty"`
}

type apiMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type apiResponse struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Choices []apiChoice `json:"choices"`
	Usage   apiUsage    `json:"usage"`
}

type apiChoice struct {
	Message      apiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type apiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Streaming request type.
type apiStreamRequest struct {
	Model         string         `json:"model"`
	Messages      []apiMessage   `json:"messages"`
	MaxTokens     *int           `json:"max_tokens,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	Tools         []llm.ToolDef  `json:"tools,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}
