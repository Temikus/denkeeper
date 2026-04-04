package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/llm"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    http.DefaultClient,
	}
}

// NewWithHTTPClient creates a client with a custom HTTP client (for testing).
func NewWithHTTPClient(apiKey, baseURL string, httpClient *http.Client) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    httpClient,
	}
}

func (c *Client) Name() string { return "openrouter" }

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
	httpReq.Header.Set("HTTP-Referer", "https://github.com/Temikus/denkeeper")
	httpReq.Header.Set("X-Title", "Denkeeper")

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

	return buildChatResponse(&apiResp), nil
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

	return &llm.ChatResponse{
		Content:      content,
		ToolCalls:    choice.Message.ToolCalls,
		Model:        apiResp.Model,
		FinishReason: choice.FinishReason,
		TokensUsed: llm.TokenUsage{
			Prompt:     apiResp.Usage.PromptTokens,
			Completion: apiResp.Usage.CompletionTokens,
			Total:      apiResp.Usage.TotalTokens,
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

// FundsRemaining implements llm.BalanceProvider by querying GET /api/v1/key.
// Returns -1 if the account has no credit limit (unlimited).
func (c *Client) FundsRemaining(ctx context.Context) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/key", nil)
	if err != nil {
		return 0, fmt.Errorf("building key info request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching key info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("reading key info response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("key info API error (status %d): %s", resp.StatusCode, string(body))
	}

	var kr keyResponse
	if err := json.Unmarshal(body, &kr); err != nil {
		return 0, fmt.Errorf("decoding key info response: %w", err)
	}

	if kr.Data.LimitRemaining == nil {
		return -1, nil // unlimited
	}
	return *kr.Data.LimitRemaining, nil
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
		ReasoningContent string          `json:"reasoning_content,omitempty"`
		ToolCalls        []llm.ToolCall  `json:"tool_calls,omitempty"`
		ToolCallID       string          `json:"tool_call_id,omitempty"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Role = w.Role
	m.ToolCalls = w.ToolCalls
	m.ToolCallID = w.ToolCallID
	m.ReasoningContent = w.ReasoningContent
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
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost"` // real cost reported by OpenRouter
}

type keyResponse struct {
	Data struct {
		LimitRemaining *float64 `json:"limit_remaining"` // null means unlimited
	} `json:"data"`
}
