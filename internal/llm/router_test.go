package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm/pricing"
)

// ---------------------------------------------------------------------------
// Base mock types
// ---------------------------------------------------------------------------

type mockProvider struct {
	name      string
	response  *ChatResponse
	err       error
	healthErr error
}

func (m *mockProvider) ChatCompletion(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return m.response, m.err
}

func (m *mockProvider) Name() string                        { return m.name }
func (m *mockProvider) HealthCheck(_ context.Context) error { return m.healthErr }

// capturingMockProvider records the model from each ChatCompletion call.
type capturingMockProvider struct {
	mockProvider
	lastModel string
}

func (c *capturingMockProvider) ChatCompletion(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	c.lastModel = req.Model
	return c.mockProvider.ChatCompletion(context.Background(), req)
}

// statefulMockProvider fails for the first failCount calls, then succeeds.
type statefulMockProvider struct {
	name        string
	failCount   int
	failErr     error
	successResp *ChatResponse
	calls       int
}

func (s *statefulMockProvider) Name() string                        { return s.name }
func (s *statefulMockProvider) HealthCheck(_ context.Context) error { return nil }
func (s *statefulMockProvider) ChatCompletion(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	s.calls++
	if s.calls <= s.failCount {
		return nil, s.failErr
	}
	return s.successResp, nil
}

// countingMockProvider tracks ChatCompletion invocations.
type countingMockProvider struct {
	mockProvider
	calls int
}

func (c *countingMockProvider) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	c.calls++
	return c.mockProvider.ChatCompletion(ctx, req)
}

// ---------------------------------------------------------------------------
// Existing tests (regression)
// ---------------------------------------------------------------------------

func TestRouter_Complete(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
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
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("nonexistent", "model", ct)

	_, err := r.Complete(context.Background(), "s1", nil)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRouter_BudgetExceeded(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 0.001}, nil) // very low budget
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{
		name:     "mock",
		response: &ChatResponse{Content: "Hello!", TokensUsed: TokenUsage{Total: 10}},
	})

	ct.Record("s1", 1.0) // pre-fill past budget

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "Hi"}})
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
	if !strings.Contains(err.Error(), "exceeded hard cost limit") {
		t.Errorf("error = %q, want it to mention exceeded hard cost limit", err.Error())
	}
	if !errors.Is(err, ErrHardLimitExceeded) {
		t.Errorf("expected ErrHardLimitExceeded sentinel, got %v", err)
	}
}

func TestRouter_ProviderError(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{name: "mock", err: fmt.Errorf("connection refused")})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "Hi"}})
	if err == nil {
		t.Fatal("expected error from provider")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q, want it to wrap 'connection refused'", err.Error())
	}
}

func TestRouter_CostTracking(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{
		name:     "mock",
		response: &ChatResponse{Content: "Hi", TokensUsed: TokenUsage{Total: 1000}},
	})

	if ct.SessionCost("s1") != 0 {
		t.Fatal("expected zero cost before completion")
	}

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "Hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ct.SessionCost("s1") <= 0 {
		t.Errorf("expected positive session cost, got %f", ct.SessionCost("s1"))
	}
}

func TestRouter_HealthCheck_AllHealthy(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{name: "mock1"})
	r.RegisterProvider(&mockProvider{name: "mock2"})

	if err := r.HealthCheck(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouter_HealthCheck_OneUnhealthy(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "test-model", ct)
	r.RegisterProvider(&mockProvider{name: "healthy"})
	r.RegisterProvider(&mockProvider{name: "unhealthy", healthErr: fmt.Errorf("service down")})

	err := r.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected health check error")
	}
	if !strings.Contains(err.Error(), "unhealthy") {
		t.Errorf("error = %q, want it to include provider name 'unhealthy'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Fallback: error trigger
// ---------------------------------------------------------------------------

func TestRouter_Fallback_ErrorTrigger_SwitchProvider_Success(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "unavailable"}})
	r.RegisterProvider(&mockProvider{name: "fallback", response: &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 10}}})
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "fallback"}})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q, want ok", resp.Content)
	}
}

func TestRouter_Fallback_ErrorTrigger_SwitchProvider_FallbackAlsoFails(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.RegisterProvider(&mockProvider{name: "fallback", err: &LLMError{StatusCode: 502, Message: "also down"}})
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "fallback"}})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestRouter_Fallback_ErrorTrigger_SwitchProvider_WithModelOverride(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "expensive-model", ct)
	cap := &capturingMockProvider{}
	cap.name = "secondary"
	cap.response = &ChatResponse{Content: "fallback response", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.RegisterProvider(cap)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "error", Action: "switch_provider", Provider: "secondary", Model: "cheap-model"},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "fallback response" {
		t.Errorf("content = %q, want fallback response", resp.Content)
	}
	if cap.lastModel != "cheap-model" {
		t.Errorf("model = %q, want cheap-model", cap.lastModel)
	}
}

func TestRouter_Fallback_ErrorTrigger_UnregisteredProvider(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "nonexistent"}})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error when fallback provider is not registered")
	}
}

func TestRouter_Fallback_ErrorTrigger_NonRetryableSkipped(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 401, Message: "unauthorized"}})
	r.RegisterProvider(&mockProvider{name: "fallback", response: &ChatResponse{Content: "should not reach", TokensUsed: TokenUsage{Total: 5}}})
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "fallback"}})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for non-retryable 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want it to mention 401", err.Error())
	}
}

func TestRouter_Fallback_MultipleErrorRules_FirstSucceeds(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	second := &countingMockProvider{
		mockProvider: mockProvider{name: "second", response: &ChatResponse{Content: "second", TokensUsed: TokenUsage{Total: 5}}},
	}
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.RegisterProvider(&mockProvider{name: "first", response: &ChatResponse{Content: "first", TokensUsed: TokenUsage{Total: 5}}})
	r.RegisterProvider(second)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "error", Action: "switch_provider", Provider: "first"},
		{Trigger: "error", Action: "switch_provider", Provider: "second"},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "first" {
		t.Errorf("content = %q, want first", resp.Content)
	}
	if second.calls != 0 {
		t.Errorf("second fallback called %d times, want 0", second.calls)
	}
}

func TestRouter_Fallback_MultipleErrorRules_FirstFailsSecondSucceeds(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.RegisterProvider(&mockProvider{name: "first", err: &LLMError{StatusCode: 502, Message: "also down"}})
	r.RegisterProvider(&mockProvider{name: "second", response: &ChatResponse{Content: "second wins", TokensUsed: TokenUsage{Total: 5}}})
	r.SetFallbacks([]FallbackRule{
		{Trigger: "error", Action: "switch_provider", Provider: "first"},
		{Trigger: "error", Action: "switch_provider", Provider: "second"},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "second wins" {
		t.Errorf("content = %q, want second wins", resp.Content)
	}
}

func TestRouter_Fallback_CostRecordedAfterFallback(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.RegisterProvider(&mockProvider{name: "fallback", response: &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 1000}}})
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "fallback"}})

	if ct.SessionCost("s1") != 0 {
		t.Fatal("expected zero cost before completion")
	}

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ct.SessionCost("s1") <= 0 {
		t.Error("expected non-zero session cost after fallback completion")
	}
}

// ---------------------------------------------------------------------------
// Fallback: rate_limit trigger
// ---------------------------------------------------------------------------

func TestRouter_Fallback_RateLimit_WaitAndRetry_Success(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	// Fail first call with 429, succeed on second
	stateful := &statefulMockProvider{
		name:        "primary",
		failCount:   1,
		failErr:     &LLMError{StatusCode: 429, Message: "rate limited"},
		successResp: &ChatResponse{Content: "retried ok", TokensUsed: TokenUsage{Total: 5}},
	}
	r.RegisterProvider(stateful)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "rate_limit", Action: "wait_and_retry", MaxRetries: 2, Backoff: "constant"},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "retried ok" {
		t.Errorf("content = %q, want retried ok", resp.Content)
	}
}

func TestRouter_Fallback_RateLimit_WaitAndRetry_Exhausted(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 429, Message: "rate limited"}})
	r.SetFallbacks([]FallbackRule{
		{Trigger: "rate_limit", Action: "wait_and_retry", MaxRetries: 2, Backoff: "constant"},
	})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestRouter_Fallback_RateLimit_SwitchProvider(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 429, Message: "rate limited"}})
	r.RegisterProvider(&mockProvider{name: "fallback", response: &ChatResponse{Content: "from fallback", TokensUsed: TokenUsage{Total: 5}}})
	r.SetFallbacks([]FallbackRule{
		{Trigger: "rate_limit", Action: "switch_provider", Provider: "fallback"},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from fallback" {
		t.Errorf("content = %q, want from fallback", resp.Content)
	}
}

func TestRouter_Fallback_RateLimit_NotFiredOn5xx(t *testing.T) {
	// A 503 should not trigger the rate_limit rule — it should trigger the error rule.
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "service unavailable"}})
	r.RegisterProvider(&mockProvider{name: "error-fallback", response: &ChatResponse{Content: "error fallback", TokensUsed: TokenUsage{Total: 5}}})
	r.SetFallbacks([]FallbackRule{
		{Trigger: "rate_limit", Action: "switch_provider", Provider: "error-fallback"}, // must NOT fire
		{Trigger: "error", Action: "switch_provider", Provider: "error-fallback"},      // must fire
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "error fallback" {
		t.Errorf("content = %q, want error fallback", resp.Content)
	}
}

// ---------------------------------------------------------------------------
// Fallback: cost_limit trigger
// ---------------------------------------------------------------------------

func TestRouter_Fallback_CostLimit_Soft_NoFireWhenUnderLimit(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Soft: 5.0, Hard: 10.0}, nil)
	r := NewRouter("primary", "expensive-model", ct)
	cap := &capturingMockProvider{}
	cap.name = "primary"
	cap.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(cap)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "cost_limit", Scope: "soft", Action: "switch_model", Model: "cheap-model"},
	})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.lastModel != "expensive-model" {
		t.Errorf("model = %q, want expensive-model (under soft limit)", cap.lastModel)
	}
}

func TestRouter_Fallback_CostLimit_Soft_SwitchesModel(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Soft: 1.0, Hard: 10.0}, nil)
	r := NewRouter("primary", "expensive-model", ct)
	cap := &capturingMockProvider{}
	cap.name = "primary"
	cap.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(cap)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "cost_limit", Scope: "soft", Action: "switch_model", Model: "cheap-model"},
	})

	ct.Record("s1", 2.0) // push over soft limit

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.lastModel != "cheap-model" {
		t.Errorf("model = %q, want cheap-model (over soft limit)", cap.lastModel)
	}
}

func TestRouter_Fallback_CostLimit_Soft_SwitchesProvider(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Soft: 1.0, Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", response: &ChatResponse{Content: "primary", TokensUsed: TokenUsage{Total: 5}}})
	r.RegisterProvider(&mockProvider{name: "secondary", response: &ChatResponse{Content: "secondary", TokensUsed: TokenUsage{Total: 5}}})
	r.SetFallbacks([]FallbackRule{
		{Trigger: "cost_limit", Scope: "soft", Action: "switch_provider", Provider: "secondary"},
	})

	ct.Record("s1", 2.0)

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "secondary" {
		t.Errorf("content = %q, want secondary (over soft limit)", resp.Content)
	}
}

func TestRouter_Fallback_CostLimit_Hard_PreemptsHardLimitError(t *testing.T) {
	// A scope=hard rule pointing at a free provider must swap before the
	// hard-limit guard fires.
	ct := NewCostTracker(SessionLimits{Hard: 1.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", response: &ChatResponse{Content: "primary", TokensUsed: TokenUsage{Total: 5}}})
	r.RegisterProvider(&mockProvider{name: "free", response: &ChatResponse{Content: "free", TokensUsed: TokenUsage{Total: 5}}})
	r.SetFallbacks([]FallbackRule{
		{Trigger: "cost_limit", Scope: "hard", Action: "switch_provider", Provider: "free"},
	})

	ct.Record("s1", 2.0) // over hard limit

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "free" {
		t.Errorf("content = %q, want free (hard limit fallback fired)", resp.Content)
	}
}

func TestRouter_Fallback_CostLimit_Hard_NoRule_StillErrors(t *testing.T) {
	// Without a matching cost_limit rule, the hard-limit guard still fires.
	ct := NewCostTracker(SessionLimits{Hard: 1.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", response: &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}})

	ct.Record("s1", 2.0)

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if !errors.Is(err, ErrHardLimitExceeded) {
		t.Fatalf("expected ErrHardLimitExceeded, got %v", err)
	}
}

func TestRouter_Fallback_CostLimit_UnregisteredProvider(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Soft: 1.0, Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{
		name:     "primary",
		response: &ChatResponse{Content: "primary", TokensUsed: TokenUsage{Total: 5}},
	})
	r.SetFallbacks([]FallbackRule{
		{Trigger: "cost_limit", Scope: "soft", Action: "switch_provider", Provider: "nonexistent"},
	})

	ct.Record("s1", 2.0)

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "primary" {
		t.Errorf("content = %q, want primary (unregistered fallback skipped)", resp.Content)
	}
}

// toolCapturingProvider records the tools from each ChatCompletion call.
type toolCapturingProvider struct {
	mockProvider
	lastTools []ToolDef
}

func (tc *toolCapturingProvider) ChatCompletion(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	tc.lastTools = req.Tools
	return tc.mockProvider.ChatCompletion(context.Background(), req)
}

func TestRouter_SetTools_DynamicResolution(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "model", ct)
	cap := &toolCapturingProvider{}
	cap.name = "mock"
	cap.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(cap)

	// Initially no tools.
	tools := []ToolDef{}
	r.SetTools(func() []ToolDef { return tools })

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cap.lastTools) != 0 {
		t.Errorf("first call tools count = %d, want 0", len(cap.lastTools))
	}

	// Add a tool at "runtime".
	tools = append(tools, ToolDef{
		Type:     "function",
		Function: FunctionDef{Name: "new_tool", Description: "a runtime tool"},
	})

	_, err = r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi again"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cap.lastTools) != 1 {
		t.Fatalf("second call tools count = %d, want 1", len(cap.lastTools))
	}
	if cap.lastTools[0].Function.Name != "new_tool" {
		t.Errorf("tool name = %q, want new_tool", cap.lastTools[0].Function.Name)
	}
}

func TestRouter_SetTools_NilSource(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "model", ct)
	cap := &toolCapturingProvider{}
	cap.name = "mock"
	cap.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(cap)

	// No SetTools call — toolSource is nil.
	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cap.lastTools != nil {
		t.Errorf("tools = %v, want nil when no source set", cap.lastTools)
	}
}

func TestRouter_SetDefaultModel(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "original-model", ct)
	r.RegisterProvider(&mockProvider{
		name:     "mock",
		response: &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 1}},
	})

	if r.DefaultModel() != "original-model" {
		t.Fatalf("initial model = %q, want original-model", r.DefaultModel())
	}

	r.SetDefaultModel("updated-model")
	if r.DefaultModel() != "updated-model" {
		t.Errorf("model after set = %q, want updated-model", r.DefaultModel())
	}
}

func TestRouter_SetDefaultProvider(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "m1", ct)
	r.RegisterProvider(&mockProvider{name: "mock"})
	r.RegisterProvider(&mockProvider{name: "other"})

	if r.DefaultProvider() != "mock" {
		t.Fatalf("initial provider = %q, want mock", r.DefaultProvider())
	}

	if err := r.SetDefaultProvider("other"); err != nil {
		t.Fatalf("SetDefaultProvider(other) = %v, want nil", err)
	}
	if r.DefaultProvider() != "other" {
		t.Errorf("provider after set = %q, want other", r.DefaultProvider())
	}
}

func TestRouter_SetDefaultProvider_UnknownReturnsError(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("mock", "m1", ct)
	r.RegisterProvider(&mockProvider{name: "mock"})

	err := r.SetDefaultProvider("nonexistent")
	if err == nil {
		t.Fatal("SetDefaultProvider(nonexistent) = nil, want error")
	}
	if r.DefaultProvider() != "mock" {
		t.Errorf("provider should remain mock, got %q", r.DefaultProvider())
	}
}

func TestTokenCost_PrefersProviderCost(t *testing.T) {
	resp := &ChatResponse{
		CostUSD:    0.00123,
		TokensUsed: TokenUsage{Total: 1000},
	}
	got, source := TokenCost(resp, nil, "")
	if got != 0.00123 {
		t.Errorf("TokenCost with CostUSD = %f, want 0.00123", got)
	}
	if source != "provider" {
		t.Errorf("source = %q, want %q", source, "provider")
	}
}

func TestTokenCost_LegacyFallbackEstimate(t *testing.T) {
	resp := &ChatResponse{
		CostUSD:    0,
		TokensUsed: TokenUsage{Total: 1000},
	}
	want := float64(1000) / 1000.0 * 0.01
	got, source := TokenCost(resp, nil, "")
	if got != want {
		t.Errorf("TokenCost fallback = %f, want %f", got, want)
	}
	if source != "fallback" {
		t.Errorf("source = %q, want %q", source, "fallback")
	}
}

func TestTokenCost_UsesRegistry(t *testing.T) {
	reg := pricing.NewEmpty()
	reg.RegisterPrefix("claude-sonnet-4", pricing.ModelPrice{
		InputPerMTok: 3.0, OutputPerMTok: 15.0, CachedInputPerMTok: 0.30,
	})

	resp := &ChatResponse{
		Model:      "claude-sonnet-4-20250514",
		TokensUsed: TokenUsage{Prompt: 1000, Completion: 500, CachedPrompt: 200, Total: 1700},
	}
	got, source := TokenCost(resp, reg, "")
	want := 1000.0/1_000_000*3.0 + 500.0/1_000_000*15.0 + 200.0/1_000_000*0.30
	if diff := got - want; diff > 1e-12 || diff < -1e-12 {
		t.Errorf("TokenCost = %e, want %e", got, want)
	}
	if source != "registry" {
		t.Errorf("source = %q, want %q", source, "registry")
	}
}

// ---------------------------------------------------------------------------
// Streaming tests
// ---------------------------------------------------------------------------

// mockStreamingProvider wraps mockProvider and implements StreamingProvider.
type mockStreamingProvider struct {
	mockProvider
	chunks []StreamChunk
}

func (m *mockStreamingProvider) SupportsStreaming() bool { return true }

func (m *mockStreamingProvider) ChatCompletion(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.OnStream != nil {
		for _, c := range m.chunks {
			req.OnStream(c)
		}
	}
	return m.response, m.err
}

func newStreamingRouter(p Provider, name, model string) *Router {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter(name, model, ct)
	r.RegisterProvider(p)
	return r
}

func TestRouter_CompleteStream_CallsCallback(t *testing.T) {
	provider := &mockStreamingProvider{
		mockProvider: mockProvider{
			name:     "primary",
			response: &ChatResponse{Content: "hello world", FinishReason: "stop"},
		},
		chunks: []StreamChunk{
			{ContentDelta: "hello"},
			{ContentDelta: " world"},
		},
	}

	r := newStreamingRouter(provider, "primary", "model-1")
	var received []string
	resp, err := r.CompleteStream(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}}, func(c StreamChunk) {
		if c.ContentDelta != "" {
			received = append(received, c.ContentDelta)
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello world" {
		t.Errorf("content = %q, want hello world", resp.Content)
	}
	if len(received) != 2 || received[0] != "hello" || received[1] != " world" {
		t.Errorf("chunks = %v", received)
	}
}

func TestRouter_CompleteStream_NonStreamingProviderNoCallback(t *testing.T) {
	// mockProvider does NOT implement StreamingProvider — callback should never fire.
	provider := &mockProvider{
		name:     "primary",
		response: &ChatResponse{Content: "full response", FinishReason: "stop"},
	}

	r := newStreamingRouter(provider, "primary", "model-1")
	var called bool
	resp, err := r.CompleteStream(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}}, func(StreamChunk) {
		called = true
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "full response" {
		t.Errorf("content = %q, want full response", resp.Content)
	}
	if called {
		t.Error("callback should not be called for non-streaming provider")
	}
}

func TestRouter_CompleteStream_NilCallbackWorksLikeComplete(t *testing.T) {
	provider := &mockStreamingProvider{
		mockProvider: mockProvider{
			name:     "primary",
			response: &ChatResponse{Content: "response", FinishReason: "stop"},
		},
	}

	r := newStreamingRouter(provider, "primary", "model-1")
	resp, err := r.CompleteStream(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "response" {
		t.Errorf("content = %q, want response", resp.Content)
	}
}

// ---------------------------------------------------------------------------
// ListModelDetails
// ---------------------------------------------------------------------------

// listingMockProvider implements ModelLister (but not ModelDetailLister) for testing.
type listingMockProvider struct {
	mockProvider
	models []string
}

func (l *listingMockProvider) ListModels(_ context.Context) ([]string, error) {
	return l.models, nil
}

// modelDetailsMockProvider implements ModelDetailLister for testing.
type modelDetailsMockProvider struct {
	mockProvider
	models []ModelInfo
	err    error
}

func (m *modelDetailsMockProvider) ListModelDetails(_ context.Context) ([]ModelInfo, error) {
	return m.models, m.err
}

func TestRouter_ListModelDetails_UsesDetailLister(t *testing.T) {
	ct := NewCostTracker(SessionLimits{}, nil)
	r := NewRouter("p1", "m", ct)
	inp, out := 3.0, 15.0
	p := &modelDetailsMockProvider{
		mockProvider: mockProvider{name: "p1", response: &ChatResponse{}},
		models: []ModelInfo{
			{ID: "anthropic/claude-3-opus", Name: "Claude 3 Opus", Provider: "p1", InputPerMTok: &inp, OutputPerMTok: &out, SupportsTools: true, WeeklyTokens: 1000},
			{ID: "anthropic/claude-3-haiku", Name: "Claude 3 Haiku", Provider: "p1", InputPerMTok: &inp, OutputPerMTok: &out, SupportsTools: true, WeeklyTokens: 500},
		},
	}
	r.RegisterProvider(p)

	got := r.ListModelDetails(context.Background(), "")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Should be sorted by WeeklyTokens descending.
	if got[0].ID != "anthropic/claude-3-opus" {
		t.Errorf("got[0].ID = %q, want claude-3-opus (highest tokens)", got[0].ID)
	}
	if !got[0].SupportsTools {
		t.Error("expected SupportsTools = true")
	}
}

func TestRouter_ListModelDetails_FallbackToStaticEnrichment(t *testing.T) {
	ct := NewCostTracker(SessionLimits{}, nil)
	r := NewRouter("p1", "m", ct)
	// Provider implements ModelLister but not ModelDetailLister.
	p := &listingMockProvider{
		mockProvider: mockProvider{name: "p1", response: &ChatResponse{}},
		models:       []string{"anthropic/claude-3-opus", "meta-llama/llama-3.1-8b"},
	}
	r.RegisterProvider(p)
	reg := pricing.New()
	r.SetPricing(reg)

	got := r.ListModelDetails(context.Background(), "")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// claude-3-opus should have tool support and pricing.
	var opus ModelInfo
	for _, m := range got {
		if m.ID == "anthropic/claude-3-opus" {
			opus = m
		}
	}
	if !opus.SupportsTools {
		t.Error("claude-3-opus: expected SupportsTools = true")
	}
	if opus.InputPerMTok == nil {
		t.Error("claude-3-opus: expected non-nil InputPerMTok from registry")
	}
}

func TestRouter_ListModelDetails_DetailListerErrorFallsThrough(t *testing.T) {
	ct := NewCostTracker(SessionLimits{}, nil)
	r := NewRouter("p1", "m", ct)
	p := &modelDetailsMockProvider{
		mockProvider: mockProvider{name: "p1", response: &ChatResponse{}},
		err:          fmt.Errorf("provider down"),
	}
	r.RegisterProvider(p)

	// Should return empty rather than panic.
	got := r.ListModelDetails(context.Background(), "")
	if got == nil {
		got = []ModelInfo{} // nil is also acceptable
	}
	if len(got) != 0 {
		t.Errorf("expected empty result on error, got %d", len(got))
	}
}

func TestModelDisplayName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"anthropic/claude-3-opus", "Claude 3 Opus"},
		{"openai/gpt-4o", "Gpt 4o"},
		{"meta-llama/llama-3.1-8b", "Llama 3.1 8b"},
		{"simple", "Simple"},
	}
	for _, c := range cases {
		got := modelDisplayName(c.in)
		if got != c.want {
			t.Errorf("modelDisplayName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModelSupportsTools(t *testing.T) {
	yes := []string{
		"anthropic/claude-3-opus",
		"anthropic/claude-sonnet-4-20250514",
		"openai/gpt-4o",
		"google/gemini-2.5-flash",
	}
	no := []string{
		"meta-llama/llama-3.1-8b",
		"unknown/model-x",
	}
	for _, id := range yes {
		if !modelSupportsTools(id) {
			t.Errorf("modelSupportsTools(%q) = false, want true", id)
		}
	}
	for _, id := range no {
		if modelSupportsTools(id) {
			t.Errorf("modelSupportsTools(%q) = true, want false", id)
		}
	}
}

// ---------------------------------------------------------------------------
// Retry classification: context errors
// ---------------------------------------------------------------------------

func TestIsRetryable_ContextErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"bare canceled", context.Canceled, false},
		{"bare deadline", context.DeadlineExceeded, false},
		{"wrapped canceled", fmt.Errorf("sending request: %w", context.Canceled), false},
		{"wrapped deadline", fmt.Errorf("sending stream request: %w", context.DeadlineExceeded), false},
		{"wrapped idle timeout", fmt.Errorf("reading stream: %w", ErrStreamIdleTimeout), true},
		{"llm 503", &LLMError{StatusCode: 503, Message: "down"}, true},
		{"llm 401", &LLMError{StatusCode: 401, Message: "unauthorized"}, false},
		{"plain network error", errors.New("connection refused"), true},
	}
	for _, c := range cases {
		if got := IsRetryable(c.err); got != c.want {
			t.Errorf("%s: IsRetryable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRouter_Fallback_ContextErrorSkipsFallbacks(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	fallback := &countingMockProvider{
		mockProvider: mockProvider{name: "fallback", response: &ChatResponse{Content: "should not reach", TokensUsed: TokenUsage{Total: 5}}},
	}
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: fmt.Errorf("sending stream request: %w", context.DeadlineExceeded)})
	r.RegisterProvider(fallback)
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "fallback"}})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for context deadline")
	}
	if fallback.calls != 0 {
		t.Errorf("fallback invoked %d times, want 0", fallback.calls)
	}
}

func TestRouter_Fallback_DeadContextSkipsFallbacks(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	fallback := &countingMockProvider{
		mockProvider: mockProvider{name: "fallback", response: &ChatResponse{Content: "should not reach", TokensUsed: TokenUsage{Total: 5}}},
	}
	r := NewRouter("primary", "model", ct)
	// Retryable error shape, but the caller's context is already cancelled —
	// the liveness guard must skip fallbacks regardless of error shape.
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.RegisterProvider(fallback)
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "fallback"}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Complete(ctx, "s1", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error with dead context")
	}
	if fallback.calls != 0 {
		t.Errorf("fallback invoked %d times, want 0", fallback.calls)
	}
}

func TestRouter_Fallback_IdleTimeoutStillTriggersFallback(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: fmt.Errorf("reading stream: %w", ErrStreamIdleTimeout)})
	r.RegisterProvider(&mockProvider{name: "fallback", response: &ChatResponse{Content: "fallback ok", TokensUsed: TokenUsage{Total: 5}}})
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "fallback"}})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "fallback ok" {
		t.Errorf("content = %q, want fallback ok", resp.Content)
	}
}
