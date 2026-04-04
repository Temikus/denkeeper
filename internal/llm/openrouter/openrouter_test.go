package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

func TestChatCompletion_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or wrong Authorization header")
		}

		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("model = %q, want test-model", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "Hello" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}

		resp := apiResponse{
			ID:    "chatcmpl-123",
			Model: "test-model",
			Choices: []apiChoice{
				{
					Message:      apiMessage{Role: "assistant", Content: "Hi there!"},
					FinishReason: "stop",
				},
			},
			Usage: apiUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("test-key", server.URL, server.Client())

	resp, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "test-model",
		Messages: []llm.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Content != "Hi there!" {
		t.Errorf("content = %q, want Hi there!", resp.Content)
	}
	if resp.TokensUsed.Total != 15 {
		t.Errorf("total tokens = %d, want 15", resp.TokensUsed.Total)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.FinishReason)
	}
}

// TestChatCompletion_ArrayContent verifies that models returning content as an
// array of content blocks (e.g. moonshotai/kimi-k2.5) are handled correctly.
func TestChatCompletion_ArrayContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return content as an array of blocks, as kimi-k2.5 does.
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-1",
			"model": "moonshotai/kimi-k2.5",
			"choices": [{
				"message": {
					"role": "assistant",
					"content": [{"type": "text", "text": "Hello from kimi!"}]
				},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	resp, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "moonshotai/kimi-k2.5",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello from kimi!" {
		t.Errorf("content = %q, want Hello from kimi!", resp.Content)
	}
}

func TestChatCompletion_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("bad-key", server.URL, server.Client())

	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *llm.LLMError, got %T: %v", err, err)
	}
	if llmErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", llmErr.StatusCode, http.StatusUnauthorized)
	}
}

func TestChatCompletion_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"m","choices":[],"usage":{"total_tokens":0}}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "m",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestChatCompletion_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "m",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestChatCompletion_MaxTokensAndTemperature(t *testing.T) {
	var receivedReq apiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedReq); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		resp := apiResponse{
			ID:    "chatcmpl-1",
			Model: "test-model",
			Choices: []apiChoice{
				{Message: apiMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
			},
			Usage: apiUsage{TotalTokens: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	temp := 0.7
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:       "test-model",
		Messages:    []llm.Message{{Role: "user", Content: "Hi"}},
		MaxTokens:   512,
		Temperature: &temp,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedReq.MaxTokens == nil || *receivedReq.MaxTokens != 512 {
		t.Errorf("max_tokens not sent correctly, got %v", receivedReq.MaxTokens)
	}
	if receivedReq.Temperature == nil || *receivedReq.Temperature != 0.7 {
		t.Errorf("temperature not sent correctly, got %v", receivedReq.Temperature)
	}
}

func TestHealthCheck_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s, want /models", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHealthCheck_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	if err := client.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestName(t *testing.T) {
	c := New("key")
	if c.Name() != "openrouter" {
		t.Errorf("name = %q, want openrouter", c.Name())
	}
}

func TestFundsRemaining_WithLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/key" {
			t.Errorf("path = %s, want /key", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"limit_remaining":42.50}}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	balance, err := client.FundsRemaining(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance != 42.50 {
		t.Errorf("balance = %f, want 42.50", balance)
	}
}

func TestFundsRemaining_Unlimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"limit_remaining":null}}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	balance, err := client.FundsRemaining(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance != -1 {
		t.Errorf("balance = %f, want -1 (unlimited)", balance)
	}
}

func TestChatCompletion_ToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}

		// Verify tools were sent in the request.
		if len(req.Tools) != 1 {
			t.Fatalf("expected 1 tool in request, got %d", len(req.Tools))
		}
		if req.Tools[0].Function.Name != "get_weather" {
			t.Errorf("tool name = %q, want get_weather", req.Tools[0].Function.Name)
		}

		resp := apiResponse{
			ID:    "chatcmpl-tc",
			Model: "test-model",
			Choices: []apiChoice{
				{
					Message: apiMessage{
						Role: "assistant",
						ToolCalls: []llm.ToolCall{
							{
								ID:   "call_123",
								Type: "function",
								Function: llm.FunctionCall{
									Name:      "get_weather",
									Arguments: `{"city":"London"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
			Usage: apiUsage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("test-key", server.URL, server.Client())

	resp, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "test-model",
		Messages: []llm.Message{{Role: "user", Content: "What's the weather?"}},
		Tools: []llm.ToolDef{
			{
				Type: "function",
				Function: llm.FunctionDef{
					Name:        "get_weather",
					Description: "Get current weather",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call_123" {
		t.Errorf("tool call ID = %q, want call_123", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool call function = %q, want get_weather", resp.ToolCalls[0].Function.Name)
	}
	if resp.ToolCalls[0].Function.Arguments != `{"city":"London"}` {
		t.Errorf("tool call args = %q, want {\"city\":\"London\"}", resp.ToolCalls[0].Function.Arguments)
	}
}

func TestChatCompletion_ToolCallPassesHistory(t *testing.T) {
	// Verify that tool_call_id and tool_calls in messages are passed through to the API.
	var receivedReq apiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedReq)
		resp := apiResponse{
			ID:    "chatcmpl-1",
			Model: "m",
			Choices: []apiChoice{
				{Message: apiMessage{Role: "assistant", Content: "The weather is sunny."}, FinishReason: "stop"},
			},
			Usage: apiUsage{TotalTokens: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("key", server.URL, server.Client())
	_, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model: "m",
		Messages: []llm.Message{
			{Role: "user", Content: "weather?"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{
				{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "weather", Arguments: "{}"}},
			}},
			{Role: "tool", Content: "Sunny, 22C", ToolCallID: "call_1"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(receivedReq.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(receivedReq.Messages))
	}
	if len(receivedReq.Messages[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call in assistant message, got %d", len(receivedReq.Messages[1].ToolCalls))
	}
	if receivedReq.Messages[2].ToolCallID != "call_1" {
		t.Errorf("tool_call_id = %q, want call_1", receivedReq.Messages[2].ToolCallID)
	}
}

func TestFundsRemaining_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("bad-key", server.URL, server.Client())
	_, err := client.FundsRemaining(context.Background())
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestChatCompletion_CostUSD_FromUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := apiResponse{
			ID:    "chatcmpl-cost",
			Model: "anthropic/claude-sonnet-4-5",
			Choices: []apiChoice{
				{Message: apiMessage{Role: "assistant", Content: "hi"}, FinishReason: "stop"},
			},
			Usage: apiUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
				Cost:             0.00045,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewWithHTTPClient("test-key", server.URL, server.Client())
	resp, err := client.ChatCompletion(context.Background(), llm.ChatRequest{
		Model:    "anthropic/claude-sonnet-4-5",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.CostUSD != 0.00045 {
		t.Errorf("CostUSD = %f, want 0.00045", resp.CostUSD)
	}
}
