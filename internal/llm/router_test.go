package llm

import (
	"context"
	"fmt"
	"strings"
	"testing"
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

// balanceMockProvider extends mockProvider with BalanceProvider support.
type balanceMockProvider struct {
	mockProvider
	balance    float64
	balanceErr error
}

func (b *balanceMockProvider) FundsRemaining(_ context.Context) (float64, error) {
	return b.balance, b.balanceErr
}

// balanceCapturingProvider records last model AND implements BalanceProvider.
type balanceCapturingProvider struct {
	mockProvider
	balance    float64
	balanceErr error
	lastModel  string
}

func (b *balanceCapturingProvider) ChatCompletion(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	b.lastModel = req.Model
	return b.mockProvider.ChatCompletion(context.Background(), req)
}

func (b *balanceCapturingProvider) FundsRemaining(_ context.Context) (float64, error) {
	return b.balance, b.balanceErr
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

// countingBalanceMockProvider counts FundsRemaining calls.
type countingBalanceMockProvider struct {
	mockProvider
	balance      float64
	balanceCalls int
}

func (c *countingBalanceMockProvider) FundsRemaining(_ context.Context) (float64, error) {
	c.balanceCalls++
	return c.balance, nil
}

// ---------------------------------------------------------------------------
// Existing tests (regression)
// ---------------------------------------------------------------------------

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
		name:     "mock",
		response: &ChatResponse{Content: "Hello!", TokensUsed: TokenUsage{Total: 10}},
	})

	ct.Record("s1", 1.0) // pre-fill past budget

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "Hi"}})
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "model", ct)
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "nonexistent"}})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected error when fallback provider is not registered")
	}
}

func TestRouter_Fallback_ErrorTrigger_NonRetryableSkipped(t *testing.T) {
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
	ct := NewCostTracker(10.0)
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
// Fallback: low_funds trigger
// ---------------------------------------------------------------------------

func TestRouter_Fallback_LowFunds_SwitchModel_BelowThreshold(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "expensive-model", ct)
	bp := &balanceCapturingProvider{balance: 10.0} // above threshold — no switch
	bp.name = "primary"
	bp.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(bp)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "low_funds", Action: "switch_model", Model: "cheap-model", Threshold: 5.0},
	})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bp.lastModel != "expensive-model" {
		t.Errorf("model = %q, want expensive-model (balance above threshold)", bp.lastModel)
	}
}

func TestRouter_Fallback_LowFunds_SwitchModel_AboveThreshold(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "expensive-model", ct)
	bp := &balanceCapturingProvider{balance: 2.0} // below threshold
	bp.name = "primary"
	bp.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(bp)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "low_funds", Action: "switch_model", Model: "cheap-model", Threshold: 5.0},
	})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bp.lastModel != "cheap-model" {
		t.Errorf("model = %q, want cheap-model (balance below threshold)", bp.lastModel)
	}
}

func TestRouter_Fallback_LowFunds_Unlimited(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "expensive-model", ct)
	bp := &balanceCapturingProvider{balance: -1} // unlimited
	bp.name = "primary"
	bp.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(bp)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "low_funds", Action: "switch_model", Model: "cheap-model", Threshold: 5.0},
	})

	_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bp.lastModel != "expensive-model" {
		t.Errorf("model = %q, want expensive-model (unlimited balance)", bp.lastModel)
	}
}

func TestRouter_Fallback_LowFunds_SwitchProvider(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "model", ct)
	primary := &balanceMockProvider{balance: 1.0} // below threshold
	primary.name = "primary"
	primary.response = &ChatResponse{Content: "primary", TokensUsed: TokenUsage{Total: 5}}
	secondary := &mockProvider{name: "secondary", response: &ChatResponse{Content: "secondary", TokensUsed: TokenUsage{Total: 5}}}
	r.RegisterProvider(primary)
	r.RegisterProvider(secondary)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "low_funds", Action: "switch_provider", Provider: "secondary", Threshold: 5.0},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "secondary" {
		t.Errorf("content = %q, want secondary (provider switched due to low funds)", resp.Content)
	}
}

func TestRouter_Fallback_LowFunds_UnregisteredProvider(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "model", ct)
	bp := &balanceMockProvider{balance: 1.0} // below threshold
	bp.name = "primary"
	bp.response = &ChatResponse{Content: "primary", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(bp)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "low_funds", Action: "switch_provider", Provider: "nonexistent", Threshold: 5.0},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "primary" {
		t.Errorf("content = %q, want primary (unregistered fallback skipped)", resp.Content)
	}
}

func TestRouter_Fallback_LowFunds_BalanceFetchError(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "model", ct)
	bp := &balanceMockProvider{
		balance:    0,
		balanceErr: fmt.Errorf("API unreachable"),
	}
	bp.name = "primary"
	bp.response = &ChatResponse{Content: "primary", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(bp)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "low_funds", Action: "switch_model", Model: "cheap-model", Threshold: 5.0},
	})

	// Balance fetch error — rule is skipped, primary is used
	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "primary" {
		t.Errorf("content = %q, want primary (balance error skipped rule)", resp.Content)
	}
}

func TestRouter_Fallback_LowFunds_NoBalanceProvider(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "model", ct)
	// Regular mockProvider does NOT implement BalanceProvider
	r.RegisterProvider(&mockProvider{
		name:     "primary",
		response: &ChatResponse{Content: "primary", TokensUsed: TokenUsage{Total: 5}},
	})
	r.SetFallbacks([]FallbackRule{
		{Trigger: "low_funds", Action: "switch_model", Model: "cheap-model", Threshold: 5.0},
	})

	resp, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "primary" {
		t.Errorf("content = %q, want primary (provider lacks BalanceProvider)", resp.Content)
	}
}

func TestRouter_Fallback_BalanceCached(t *testing.T) {
	ct := NewCostTracker(10.0)
	r := NewRouter("primary", "model", ct)
	counting := &countingBalanceMockProvider{balance: 10.0} // above threshold — no switch
	counting.name = "primary"
	counting.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(counting)
	r.SetFallbacks([]FallbackRule{
		{Trigger: "low_funds", Action: "switch_model", Model: "cheap-model", Threshold: 5.0},
	})

	// Two calls — FundsRemaining should only be called once (TTL cache)
	for range 2 {
		_, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if counting.balanceCalls != 1 {
		t.Errorf("FundsRemaining called %d times, want 1 (cached)", counting.balanceCalls)
	}
}
