package llm

import (
	"context"
	"testing"
)

type mockProvider struct {
	name     string
	response *ChatResponse
	err      error
}

func (m *mockProvider) ChatCompletion(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return m.response, m.err
}

func (m *mockProvider) Name() string                          { return m.name }
func (m *mockProvider) HealthCheck(_ context.Context) error   { return nil }

func TestRouter_Complete(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("mock", "test-model", ct)

	r.RegisterProvider(&mockProvider{
		name: "mock",
		response: &ChatResponse{
			Content:      "Hello!",
			TokensUsed:   TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello!" {
		t.Errorf("content = %q, want Hello!", resp.Content)
	}
	if ct.SessionCost("s1") == 0 {
		t.Error("expected non-zero session cost")
	}
}

func TestRouter_UnknownProvider(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("nonexistent", "model", ct)

	_, err := r.Complete(context.Background(), "s1", nil)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}
