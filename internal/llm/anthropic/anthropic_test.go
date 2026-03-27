package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

func TestChatCompletion_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s, want /v1/messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q, want test-key", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("anthropic-version header missing")
		}

		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if req.Model != "claude-sonnet-4-6" {
			t.Errorf("model = %q, want claude-sonnet-4-6", req.Model)
		}

		resp := apiResponse{
			Model:      "claude-sonnet-4-6",
			StopReason: "end_turn",
			Content:    []contentBlock{{Type: "text", Text: "Hello, how can I help?"}},
			Usage:      apiUsage{InputTokens: 10, OutputTokens: 8},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("test-key", server.URL, server.Client())
	resp, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []llm.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "Hello, how can I help?" {
		t.Errorf("content = %q, want Hello, how can I help?", resp.Content)
	}
	if resp.FinishReason != "end_turn" {
		t.Errorf("finish_reason = %q, want end_turn", resp.FinishReason)
	}
	if resp.TokensUsed.Total != 18 {
		t.Errorf("total tokens = %d, want 18", resp.TokensUsed.Total)
	}
	if resp.TokensUsed.Prompt != 10 {
		t.Errorf("prompt tokens = %d, want 10", resp.TokensUsed.Prompt)
	}
}

func TestChatCompletion_SystemPrompt(t *testing.T) {
	var gotReq apiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		resp := apiResponse{
			Content: []contentBlock{{Type: "text", Text: "ok"}},
			Usage:   apiUsage{InputTokens: 5, OutputTokens: 1},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model: "claude-haiku-4-5",
		Messages: []llm.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// System message should be extracted to top-level field.
	if gotReq.System != "You are a helpful assistant." {
		t.Errorf("system = %q, want 'You are a helpful assistant.'", gotReq.System)
	}
	// Only the user message should remain in messages.
	if len(gotReq.Messages) != 1 || gotReq.Messages[0].Role != "user" {
		t.Errorf("unexpected messages: %+v", gotReq.Messages)
	}
}

func TestChatCompletion_ToolUseResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := apiResponse{
			StopReason: "tool_use",
			Content: []contentBlock{
				{
					Type:  "tool_use",
					ID:    "toolu_01",
					Name:  "get_weather",
					Input: map[string]any{"location": "London"},
				},
			},
			Usage: apiUsage{InputTokens: 20, OutputTokens: 10},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	resp, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []llm.Message{
			{Role: "user", Content: "What's the weather in London?"},
		},
		Tools: []llm.ToolDef{{
			Type: "function",
			Function: llm.FunctionDef{
				Name:       "get_weather",
				Parameters: map[string]any{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_01" {
		t.Errorf("tool call ID = %q, want toolu_01", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", tc.Function.Name)
	}
	if tc.Type != "function" {
		t.Errorf("type = %q, want function", tc.Type)
	}
	// Arguments should be JSON-encoded input map.
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("parsing tool arguments: %v", err)
	}
	if args["location"] != "London" {
		t.Errorf("location = %v, want London", args["location"])
	}
}

func TestChatCompletion_ToolResult(t *testing.T) {
	var gotReq apiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		resp := apiResponse{
			Content: []contentBlock{{Type: "text", Text: "The weather is sunny."}},
			Usage:   apiUsage{InputTokens: 30, OutputTokens: 6},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []llm.Message{
			{Role: "user", Content: "What's the weather?"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []llm.ToolCall{{
					ID:   "toolu_01",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "get_weather",
						Arguments: `{"location":"London"}`,
					},
				}},
			},
			{Role: "tool", ToolCallID: "toolu_01", Content: "Sunny, 22°C"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The tool-result message should be a user message with tool_result block.
	if len(gotReq.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(gotReq.Messages))
	}
	toolResultMsg := gotReq.Messages[2]
	if toolResultMsg.Role != "user" {
		t.Errorf("tool result role = %q, want user", toolResultMsg.Role)
	}
	if len(toolResultMsg.Content) != 1 || toolResultMsg.Content[0].Type != "tool_result" {
		t.Errorf("unexpected tool result content: %+v", toolResultMsg.Content)
	}
	if toolResultMsg.Content[0].ToolUseID != "toolu_01" {
		t.Errorf("tool_use_id = %q, want toolu_01", toolResultMsg.Content[0].ToolUseID)
	}
}

func TestChatCompletion_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"Invalid API key"}}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("bad-key", server.URL, server.Client())
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []llm.Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected LLMError, got %T: %v", err, err)
	}
	if llmErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("status code = %d, want 401", llmErr.StatusCode)
	}
	if llmErr.Message != "Invalid API key" {
		t.Errorf("message = %q, want 'Invalid API key'", llmErr.Message)
	}
}

func TestChatCompletion_RateLimitError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"Rate limited"}}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []llm.Message{{Role: "user", Content: "Hello"}},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected LLMError, got %T: %v", err, err)
	}
	if !llmErr.Retryable() {
		t.Error("expected 429 to be retryable")
	}
}

func TestChatCompletion_MaxTokensDefault(t *testing.T) {
	var gotReq apiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		resp := apiResponse{
			Content: []contentBlock{{Type: "text", Text: "ok"}},
			Usage:   apiUsage{InputTokens: 5, OutputTokens: 1},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	// MaxTokens = 0 in request should default to 4096.
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []llm.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotReq.MaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want 4096", gotReq.MaxTokens)
	}
}

func TestName(t *testing.T) {
	c := New("key")
	if c.Name() != "anthropic" {
		t.Errorf("Name() = %q, want anthropic", c.Name())
	}
}

func TestChatCompletion_TextAndToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := apiResponse{
			StopReason: "tool_use",
			Content: []contentBlock{
				{Type: "text", Text: "Let me check the weather."},
				{
					Type:  "tool_use",
					ID:    "toolu_02",
					Name:  "get_weather",
					Input: map[string]any{"city": "Paris"},
				},
			},
			Usage: apiUsage{InputTokens: 15, OutputTokens: 12},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	resp, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []llm.Message{{Role: "user", Content: "Weather in Paris?"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both text and tool call should be present.
	if resp.Content != "Let me check the weather." {
		t.Errorf("content = %q, want 'Let me check the weather.'", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Errorf("tool calls = %d, want 1", len(resp.ToolCalls))
	}
}
