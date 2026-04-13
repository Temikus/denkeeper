package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/Temikus/denkeeper/internal/llm"
)

var tracer = otel.Tracer("denkeeper.llm.openai")

const defaultBaseURL = "https://api.openai.com/v1"

// Client implements llm.Provider for the OpenAI Chat Completions API.
// Compatible with any OpenAI-format endpoint (OpenAI, Azure OpenAI, vLLM, LiteLLM).
type Client struct {
	name         string
	apiKey       string
	baseURL      string
	organization string
	http         *http.Client
}

// New creates a client with the default OpenAI base URL.
func New(apiKey string) *Client {
	return &Client{
		name:    "openai",
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    http.DefaultClient,
	}
}

// NewWithBaseURL creates a client with a custom base URL (for Azure, vLLM, etc.).
func NewWithBaseURL(apiKey, baseURL string) *Client {
	return &Client{
		name:    "openai",
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    http.DefaultClient,
	}
}

// NewFull creates a named client with all options.
func NewFull(name, apiKey, baseURL, organization string) *Client {
	if name == "" {
		name = "openai"
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		name:         name,
		apiKey:       apiKey,
		baseURL:      baseURL,
		organization: organization,
		http:         http.DefaultClient,
	}
}

// NewWithHTTPClient creates a client with a custom HTTP client (for testing).
func NewWithHTTPClient(apiKey, baseURL, organization string, httpClient *http.Client) *Client {
	return &Client{
		name:         "openai",
		apiKey:       apiKey,
		baseURL:      baseURL,
		organization: organization,
		http:         httpClient,
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.organization != "" {
		httpReq.Header.Set("OpenAI-Organization", c.organization)
	}

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

	var cachedPrompt int
	if apiResp.Usage.PromptTokensDetails != nil {
		cachedPrompt = apiResp.Usage.PromptTokensDetails.CachedTokens
	}
	// OpenAI includes cached tokens in PromptTokens; split them out.
	promptNonCached := apiResp.Usage.PromptTokens - cachedPrompt

	return &llm.ChatResponse{
		Content:      apiResp.Choices[0].Message.Content,
		ToolCalls:    apiResp.Choices[0].Message.ToolCalls,
		Model:        apiResp.Model,
		FinishReason: apiResp.Choices[0].FinishReason,
		TokensUsed: llm.TokenUsage{
			Prompt:       promptNonCached,
			Completion:   apiResp.Usage.CompletionTokens,
			CachedPrompt: cachedPrompt,
			Total:        apiResp.Usage.TotalTokens,
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.organization != "" {
		httpReq.Header.Set("OpenAI-Organization", c.organization)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending stream request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		return nil, &llm.LLMError{StatusCode: resp.StatusCode, Message: string(respBody)}
	}

	result, err := llm.ReadOAIStream(resp.Body, req.OnStream)
	if err != nil {
		return nil, fmt.Errorf("reading stream: %w", err)
	}

	chatResp := &llm.ChatResponse{
		Content:      result.Content,
		ToolCalls:    result.ToolCalls,
		Model:        result.Model,
		FinishReason: result.FinishReason,
	}
	if result.Usage != nil {
		var cachedPrompt int
		if result.Usage.PromptTokensDetails != nil {
			cachedPrompt = result.Usage.PromptTokensDetails.CachedTokens
		}
		promptNonCached := result.Usage.PromptTokens - cachedPrompt
		chatResp.TokensUsed = llm.TokenUsage{
			Prompt:       promptNonCached,
			Completion:   result.Usage.CompletionTokens,
			CachedPrompt: cachedPrompt,
			Total:        result.Usage.TotalTokens,
		}
	}
	return chatResp, nil
}

// ListModels returns available model IDs from the OpenAI-compatible API.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.organization != "" {
		req.Header.Set("OpenAI-Organization", c.organization)
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
	Model       string        `json:"model"`
	Messages    []apiMessage  `json:"messages"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Tools       []llm.ToolDef `json:"tools,omitempty"`
}

// apiMessage handles both outgoing requests (content as string) and incoming
// responses where some models return content as an array of content blocks
// instead of a plain string.
type apiMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"-"` // handled by MarshalJSON/UnmarshalJSON
	ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

func (m apiMessage) MarshalJSON() ([]byte, error) {
	type wire struct {
		Role       string         `json:"role"`
		Content    string         `json:"content"`
		ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string         `json:"tool_call_id,omitempty"`
	}
	return json.Marshal(wire(m))
}

func (m *apiMessage) UnmarshalJSON(data []byte) error {
	type wire struct {
		Role       string          `json:"role"`
		RawContent json.RawMessage `json:"content"`
		ToolCalls  []llm.ToolCall  `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("unmarshaling openai message: %w", err)
	}
	m.Role = w.Role
	m.ToolCalls = w.ToolCalls
	m.ToolCallID = w.ToolCallID
	if len(w.RawContent) == 0 || string(w.RawContent) == "null" {
		return nil
	}
	// Try plain string first (standard case).
	var s string
	if err := json.Unmarshal(w.RawContent, &s); err == nil {
		m.Content = s
		return nil
	}
	// Fall back to array of content blocks (some model-specific response formats).
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
			default:
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
	PromptTokens        int                `json:"prompt_tokens"`
	CompletionTokens    int                `json:"completion_tokens"`
	TotalTokens         int                `json:"total_tokens"`
	PromptTokensDetails *promptTokenDetail `json:"prompt_tokens_details,omitempty"`
}

type promptTokenDetail struct {
	CachedTokens int `json:"cached_tokens"`
}

// Streaming request type — extends apiRequest with stream fields.
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
