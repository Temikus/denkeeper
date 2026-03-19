package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/foxbox/internal/llm"
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
