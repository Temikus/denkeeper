// Package anthropic implements the llm.Provider interface against the
// Anthropic Messages API (https://docs.anthropic.com/en/api/messages).
// It uses raw HTTP calls, consistent with the other provider implementations.
package anthropic

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

var tracer = otel.Tracer("denkeeper.llm.anthropic")

const (
	defaultBaseURL   = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
	messagesEndpoint = "/v1/messages"
)

// Client implements llm.Provider against the Anthropic Messages API.
type Client struct {
	name    string
	apiKey  string
	baseURL string
	http    *http.Client
}

// New creates a Client with the given API key and default base URL.
func New(apiKey string) *Client {
	return &Client{
		name:    "anthropic",
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    http.DefaultClient,
	}
}

// NewFull creates a named Client with a custom base URL.
func NewFull(name, apiKey, baseURL string) *Client {
	if name == "" {
		name = "anthropic"
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		name:    name,
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    http.DefaultClient,
	}
}

// NewWithHTTPClient creates a Client with a custom HTTP client (for testing).
func NewWithHTTPClient(apiKey, baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		name:    "anthropic",
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    httpClient,
	}
}

func (c *Client) Name() string { return c.name }

// SupportsStreaming implements llm.StreamingProvider.
func (c *Client) SupportsStreaming() bool { return true }

func (c *Client) HealthCheck(ctx context.Context) error {
	// Anthropic has no dedicated health endpoint; a models list call serves as a proxy.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return fmt.Errorf("creating health check request: %w", err)
	}
	c.setHeaders(req)
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

// ListModels returns available model IDs from the Anthropic API.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	c.setHeaders(req)

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

	apiReq, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.request.body.size", len(body)))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+messagesEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(httpReq)

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
		var apiErr apiErrorResponse
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, &llm.LLMError{StatusCode: resp.StatusCode, Message: apiErr.Error.Message}
		}
		return nil, &llm.LLMError{StatusCode: resp.StatusCode, Message: string(respBody)}
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return c.parseResponse(&apiResp)
}

// chatCompletionStream handles Anthropic's SSE streaming format.
func (c *Client) chatCompletionStream(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	apiReq, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}
	apiReq.Stream = true

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling stream request: %w", err)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.request.body.size", len(body)))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+messagesEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating stream request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending stream request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		var apiErr apiErrorResponse
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, &llm.LLMError{StatusCode: resp.StatusCode, Message: apiErr.Error.Message}
		}
		return nil, &llm.LLMError{StatusCode: resp.StatusCode, Message: string(respBody)}
	}

	return c.readStreamResponse(resp.Body, req.OnStream)
}

// anthropicStreamAccumulator tracks state while reading an Anthropic SSE stream.
type anthropicStreamAccumulator struct {
	out        llm.ChatResponse
	contentBuf strings.Builder
	usage      apiUsage
	tools      map[int]*anthropicToolAccum
	onStream   llm.StreamCallback
}

type anthropicToolAccum struct {
	ID      string
	Name    string
	ArgsBuf strings.Builder
}

func (a *anthropicStreamAccumulator) handleBlockDelta(data string) {
	var delta struct {
		Index int `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text,omitempty"`
			Thinking    string `json:"thinking,omitempty"`
			PartialJSON string `json:"partial_json,omitempty"`
		} `json:"delta"`
	}
	if json.Unmarshal([]byte(data), &delta) != nil {
		return
	}
	switch delta.Delta.Type {
	case "text_delta":
		a.contentBuf.WriteString(delta.Delta.Text)
		a.onStream(llm.StreamChunk{ContentDelta: delta.Delta.Text})
	case "thinking_delta":
		a.onStream(llm.StreamChunk{ThinkingDelta: delta.Delta.Thinking})
	case "input_json_delta":
		if acc, ok := a.tools[delta.Index]; ok {
			acc.ArgsBuf.WriteString(delta.Delta.PartialJSON)
		}
	}
}

func (a *anthropicStreamAccumulator) finish() *llm.ChatResponse {
	a.out.Content = a.contentBuf.String()
	a.out.TokensUsed = llm.TokenUsage{
		Prompt:       a.usage.InputTokens,
		Completion:   a.usage.OutputTokens,
		CachedPrompt: a.usage.CacheReadInputTokens,
		Total:        a.usage.InputTokens + a.usage.OutputTokens + a.usage.CacheReadInputTokens,
	}
	for i := 0; i < len(a.tools); i++ {
		acc, ok := a.tools[i]
		if !ok {
			continue
		}
		a.out.ToolCalls = append(a.out.ToolCalls, llm.ToolCall{
			ID:   acc.ID,
			Type: "function",
			Function: llm.FunctionCall{
				Name:      acc.Name,
				Arguments: acc.ArgsBuf.String(),
			},
		})
	}
	return &a.out
}

// readStreamResponse parses the Anthropic SSE event stream and calls onStream
// for each content/thinking delta.
func (c *Client) readStreamResponse(body io.Reader, onStream llm.StreamCallback) (*llm.ChatResponse, error) {
	scanner := llm.NewSSEScanner(body)
	acc := &anthropicStreamAccumulator{
		tools:    make(map[int]*anthropicToolAccum),
		onStream: onStream,
	}

	for scanner.Next() {
		evt := scanner.Event()
		switch evt.Type {
		case "message_start":
			var msg struct {
				Message struct {
					Model string   `json:"model"`
					Usage apiUsage `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(evt.Data), &msg) == nil {
				acc.out.Model = msg.Message.Model
				acc.usage.InputTokens = msg.Message.Usage.InputTokens
				acc.usage.CacheReadInputTokens = msg.Message.Usage.CacheReadInputTokens
			}

		case "content_block_start":
			var block struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id,omitempty"`
					Name string `json:"name,omitempty"`
				} `json:"content_block"`
			}
			if json.Unmarshal([]byte(evt.Data), &block) == nil && block.ContentBlock.Type == "tool_use" {
				acc.tools[block.Index] = &anthropicToolAccum{
					ID:   block.ContentBlock.ID,
					Name: block.ContentBlock.Name,
				}
			}

		case "content_block_delta":
			acc.handleBlockDelta(evt.Data)

		case "message_delta":
			var md struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(evt.Data), &md) == nil {
				acc.out.FinishReason = normalizeStopReason(md.Delta.StopReason)
				acc.usage.OutputTokens = md.Usage.OutputTokens
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading Anthropic stream: %w", err)
	}

	return acc.finish(), nil
}

// setHeaders attaches the required Anthropic authentication and versioning headers.
func (c *Client) setHeaders(r *http.Request) {
	r.Header.Set("x-api-key", c.apiKey)
	r.Header.Set("anthropic-version", anthropicVersion)
	r.Header.Set("content-type", "application/json")
}

// buildRequest converts an llm.ChatRequest into the Anthropic API wire format.
// Anthropic separates the system prompt from the message list and has a
// distinct content block format for tool calls and tool results.
func (c *Client) buildRequest(req llm.ChatRequest) (*apiRequest, error) {
	out := &apiRequest{
		Model:     req.Model,
		MaxTokens: 4096,
	}
	if req.MaxTokens > 0 {
		out.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		out.Temperature = req.Temperature
	}

	// Map llm.ToolDef → Anthropic tool format.
	if len(req.Tools) > 0 {
		out.Tools = make([]apiTool, len(req.Tools))
		for i, t := range req.Tools {
			out.Tools[i] = apiTool{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: t.Function.Parameters,
			}
		}
	}

	// Split system message from conversation messages.
	// Anthropic takes the system prompt as a top-level field, not a message.
	for _, m := range req.Messages {
		if m.Role == "system" {
			out.System = m.Content
			continue
		}

		msg, err := convertMessage(m)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, msg)
	}

	return out, nil
}

// convertMessage translates an llm.Message into an Anthropic apiMessage.
// Roles: "user" and "assistant" are passed through. "tool" (OpenAI role for
// tool results) becomes role "user" with a tool_result content block.
func convertMessage(m llm.Message) (apiMessage, error) {
	switch m.Role {
	case "user":
		return apiMessage{Role: "user", Content: []contentBlock{{Type: "text", Text: m.Content}}}, nil

	case "assistant":
		var blocks []contentBlock
		if m.Content != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			var input map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					return apiMessage{}, fmt.Errorf("parsing tool call arguments: %w", err)
				}
			}
			blocks = append(blocks, contentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			})
		}
		return apiMessage{Role: "assistant", Content: blocks}, nil

	case "tool":
		// OpenAI-style tool result → Anthropic tool_result content block.
		return apiMessage{
			Role: "user",
			Content: []contentBlock{{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			}},
		}, nil

	default:
		return apiMessage{}, fmt.Errorf("unsupported message role %q", m.Role)
	}
}

// normalizeStopReason maps Anthropic stop reasons to the canonical values
// expected by the engine's tool-call loop ("tool_calls", "stop").
func normalizeStopReason(reason string) string {
	switch reason {
	case "tool_use":
		return "tool_calls"
	case "end_turn":
		return "stop"
	default:
		return reason
	}
}

// parseResponse converts an Anthropic API response into an llm.ChatResponse.
func (c *Client) parseResponse(r *apiResponse) (*llm.ChatResponse, error) {
	out := &llm.ChatResponse{
		Model:        r.Model,
		FinishReason: normalizeStopReason(r.StopReason),
		TokensUsed: llm.TokenUsage{
			Prompt:       r.Usage.InputTokens,
			Completion:   r.Usage.OutputTokens,
			CachedPrompt: r.Usage.CacheReadInputTokens,
			Total:        r.Usage.InputTokens + r.Usage.OutputTokens + r.Usage.CacheReadInputTokens,
		},
	}

	for _, block := range r.Content {
		switch block.Type {
		case "text":
			out.Content += block.Text
		case "tool_use":
			argsJSON, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("marshaling tool input: %w", err)
			}
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: llm.FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	return out, nil
}

// ─── Wire types ──────────────────────────────────────────────────────────────

type apiRequest struct {
	Model       string       `json:"model"`
	MaxTokens   int          `json:"max_tokens"`
	System      string       `json:"system,omitempty"`
	Messages    []apiMessage `json:"messages"`
	Tools       []apiTool    `json:"tools,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
}

type apiMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	// Shared
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type apiTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type apiResponse struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Content    []contentBlock `json:"content"`
	Usage      apiUsage       `json:"usage"`
}

type apiUsage struct {
	InputTokens            int `json:"input_tokens"`
	OutputTokens           int `json:"output_tokens"`
	CacheReadInputTokens   int `json:"cache_read_input_tokens"`
	CacheCreationInputToks int `json:"cache_creation_input_tokens"`
}

type apiErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
