package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type mockProvider struct {
	name      string
	response  *ChatResponse
	err       error
	healthErr error
}

func (m *mockProvider) ChatCompletion(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return m.response, m.err
}

func (m *mockProvider) Name() string                       { return m.name }
func (m *mockProvider) HealthCheck(_ context.Context) error { return m.healthErr }

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

func TestRouter_BudgetExceeded(t *testing.T) {
	ct := NewCostTracker(0.001) // very low budget
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{
		name: "mock",
		response: &ChatResponse{
			Content:    "Hello!",
			TokensUsed: TokenUsage{Total: 10},
		},
	})

	// Pre-fill cost to exceed budget
	ct.Record("s1", 1.0)

	_, err := r.Complete(context.Background(), "s1", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
	if !strings.Contains(err.Error(), "exceeded cost budget") {
		t.Errorf("error = %q, want it to mention exceeded cost budget", err.Error())
	}
}

func TestRouter_ProviderError(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{
		name: "mock",
		err:  fmt.Errorf("connection refused"),
	})

	_, err := r.Complete(context.Background(), "s1", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err == nil {
		t.Fatal("expected error from provider")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q, want it to wrap 'connection refused'", err.Error())
	}
}

func TestRouter_CostTracking(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{
		name: "mock",
		response: &ChatResponse{
			Content:    "Hi",
			TokensUsed: TokenUsage{Total: 1000}, // 1000 tokens = $0.01
		},
	})

	if ct.SessionCost("s1") != 0 {
		t.Fatal("expected zero cost before completion")
	}

	_, err := r.Complete(context.Background(), "s1", []Message{
		{Role: "user", Content: "Hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cost := ct.SessionCost("s1")
	if cost <= 0 {
		t.Errorf("expected positive session cost after completion, got %f", cost)
	}
}

func TestRouter_HealthCheck_AllHealthy(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{name: "mock1"})
	r.RegisterProvider(&mockProvider{name: "mock2"})

	if err := r.HealthCheck(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouter_HealthCheck_OneUnhealthy(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{name: "healthy"})
	r.RegisterProvider(&mockProvider{
		name:      "unhealthy",
		healthErr: fmt.Errorf("service down"),
	})

	err := r.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected health check error")
	}
	if !strings.Contains(err.Error(), "unhealthy") {
		t.Errorf("error = %q, want it to include provider name 'unhealthy'", err.Error())
	}
}
