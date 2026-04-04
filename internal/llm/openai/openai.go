package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/llm"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Client implements llm.Provider for the OpenAI Chat Completions API.
// Compatible with any OpenAI-format endpoint (OpenAI, Azure OpenAI, vLLM, LiteLLM).
type Client struct {
	apiKey       string
	baseURL      string
	organization string
	http         *http.Client
}

// New creates a client with the default OpenAI base URL.
func New(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    http.DefaultClient,
	}
}

// NewWithBaseURL creates a client with a custom base URL (for Azure, vLLM, etc.).
func NewWithBaseURL(apiKey, baseURL string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    http.DefaultClient,
	}
}

// NewWithHTTPClient creates a client with a custom HTTP client (for testing).
func NewWithHTTPClient(apiKey, baseURL, organization string, httpClient *http.Client) *Client {
	return &Client{
		apiKey:       apiKey,
		baseURL:      baseURL,
		organization: organization,
		http:         httpClient,
	}
}

func (c *Client) Name() string { return "openai" }

func (c *Client) ChatCompletion(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

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

func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return err
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
		return err
	}
	m.Role = w.Role
	m.ToolCalls = w.ToolCalls
	m.ToolCallID = w.ToolCallID
	if len(w.RawContent) == 0 {
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
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(w.RawContent, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
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
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
