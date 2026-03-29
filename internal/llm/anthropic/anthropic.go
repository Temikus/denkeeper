// Package anthropic implements the llm.Provider interface against the
// Anthropic Messages API (https://docs.anthropic.com/en/api/messages).
// It uses raw HTTP calls, consistent with the other provider implementations.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Temikus/denkeeper/internal/llm"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
	messagesEndpoint = "/v1/messages"
)

// Client implements llm.Provider against the Anthropic Messages API.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// New creates a Client with the given API key and default base URL.
func New(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    http.DefaultClient,
	}
}

// NewWithHTTPClient creates a Client with a custom HTTP client (for testing).
func NewWithHTTPClient(apiKey, baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    httpClient,
	}
}

func (c *Client) Name() string { return "anthropic" }

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

func (c *Client) ChatCompletion(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	apiReq, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

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

// parseResponse converts an Anthropic API response into an llm.ChatResponse.
func (c *Client) parseResponse(r *apiResponse) (*llm.ChatResponse, error) {
	out := &llm.ChatResponse{
		Model:        r.Model,
		FinishReason: r.StopReason,
		TokensUsed: llm.TokenUsage{
			Prompt:     r.Usage.InputTokens,
			Completion: r.Usage.OutputTokens,
			Total:      r.Usage.InputTokens + r.Usage.OutputTokens,
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
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type apiErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
