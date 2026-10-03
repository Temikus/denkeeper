package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// stubDecisionProvider answers every asked question from a fixed response and
// records what it was sent.
type stubDecisionProvider struct {
	resp  *DecisionResponse
	err   error
	delay time.Duration
	calls int
	last  DecisionRequest
}

func (s *stubDecisionProvider) Decide(ctx context.Context, req DecisionRequest) (*DecisionResponse, error) {
	s.calls++
	s.last = req
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func noulQuestions() map[string]Question {
	return map[string]Question{
		"safe": {Type: QuestionNoul, Instructions: "Are `tool.arguments` safe?"},
	}
}

func noulResponse(p, cost float64) *DecisionResponse {
	return &DecisionResponse{
		Model:   "typesafe/jev-1.13",
		Answers: map[string]Answer{"safe": {Type: QuestionNoul, Noul: p}},
		Usage:   TokenUsage{Prompt: 100, Completion: 3, Total: 103},
		CostUSD: cost,
	}
}

func newTestDecider(p DecisionProvider, ct *CostTracker) *Decider {
	return NewDecider(DeciderConfig{
		Name:           "jev",
		Provider:       "openrouter",
		Model:          "typesafe/jev-1.13",
		Timeout:        time.Second,
		MaxInputTokens: 30000,
	}, p, ct)
}

func TestValidateQuestions_Valid(t *testing.T) {
	qs := map[string]Question{
		"n": {Type: QuestionNoul, Instructions: "x"},
		"n2": {Type: QuestionNoul, Instructions: "x", Choices: map[string]string{
			"true": "yes", "false": "no",
		}},
		"c": {Type: QuestionChoice, Instructions: "x", Choices: map[string]string{"a": "A", "b": "B"}},
		"s": {Type: QuestionScore, Instructions: "x", Levels: []string{"bad", "ok", "good"}},
	}
	if err := ValidateQuestions(qs); err != nil {
		t.Fatalf("ValidateQuestions: %v", err)
	}
}

func assertRejected(t *testing.T, what string, q Question) {
	t.Helper()
	if err := ValidateQuestions(map[string]Question{"q": q}); err == nil {
		t.Errorf("%s: expected error, got nil", what)
	}
}

func TestValidateQuestions_RejectsMissingFields(t *testing.T) {
	if err := ValidateQuestions(map[string]Question{}); err == nil {
		t.Error("empty question set: expected error")
	}
	if err := ValidateQuestions(map[string]Question{"": {Type: QuestionNoul, Instructions: "x"}}); err == nil {
		t.Error("empty id: expected error")
	}
	assertRejected(t, "blank instructions", Question{Type: QuestionNoul, Instructions: "  "})
	assertRejected(t, "unknown type", Question{Type: "rank", Instructions: "x"})
}

func TestValidateQuestions_RejectsBadChoice(t *testing.T) {
	many := make(map[string]string, 256)
	for i := range 256 {
		many[strings.Repeat("x", i+1)] = "d"
	}
	assertRejected(t, "one option", Question{Type: QuestionChoice, Instructions: "x", Choices: map[string]string{"a": "A"}})
	assertRejected(t, "256 options", Question{Type: QuestionChoice, Instructions: "x", Choices: many})
	assertRejected(t, "levels on choice", Question{Type: QuestionChoice, Instructions: "x",
		Choices: map[string]string{"a": "A", "b": "B"}, Levels: []string{"l", "m"}})
}

func TestValidateQuestions_RejectsBadScore(t *testing.T) {
	assertRejected(t, "one level", Question{Type: QuestionScore, Instructions: "x", Levels: []string{"l"}})
	assertRejected(t, "eleven levels", Question{Type: QuestionScore, Instructions: "x", Levels: make([]string, 11)})
	assertRejected(t, "choices on score", Question{Type: QuestionScore, Instructions: "x",
		Levels: []string{"l", "m"}, Choices: map[string]string{"a": "A"}})
}

func TestValidateQuestions_RejectsBadNoul(t *testing.T) {
	assertRejected(t, "non-boolean criteria key", Question{Type: QuestionNoul, Instructions: "x", Choices: map[string]string{"maybe": "?"}})
	assertRejected(t, "levels on noul", Question{Type: QuestionNoul, Instructions: "x", Levels: []string{"l", "m"}})
}

func TestDecider_Decide_RecordsCostAgainstSession(t *testing.T) {
	ct := NewCostTracker(SessionLimits{}, nil)
	prov := &stubDecisionProvider{resp: noulResponse(0.98, 0.0002)}
	d := newTestDecider(prov, ct)

	resp, err := d.Decide(context.Background(), "decider:jev:default:c1", map[string]any{"tool": "x"}, noulQuestions())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got := resp.Answers["safe"].Noul; got != 0.98 {
		t.Errorf("noul = %v, want 0.98", got)
	}
	if prov.last.Model != "typesafe/jev-1.13" {
		t.Errorf("model sent = %q", prov.last.Model)
	}
	if got := ct.SessionCost("decider:jev:default:c1"); got != 0.0002 {
		t.Errorf("session cost = %v, want 0.0002", got)
	}
	st := ct.AllSessionStats()["decider:jev:default:c1"]
	if st.InputTokens != 100 || st.OutputTokens != 3 {
		t.Errorf("tokens = %d/%d, want 100/3", st.InputTokens, st.OutputTokens)
	}
	if st.PricingSources["provider"] != 1 {
		t.Errorf("pricing sources = %v, want provider:1", st.PricingSources)
	}
}

func TestDecider_Decide_TooLargeNeverCallsProvider(t *testing.T) {
	prov := &stubDecisionProvider{resp: noulResponse(0.9, 0)}
	d := NewDecider(DeciderConfig{Name: "jev", Provider: "openrouter", Model: "m", MaxInputTokens: 50}, prov, nil)

	_, err := d.Decide(context.Background(), "s", strings.Repeat("a", 1000), noulQuestions())
	if !errors.Is(err, ErrDecisionTooLarge) {
		t.Fatalf("err = %v, want ErrDecisionTooLarge", err)
	}
	if prov.calls != 0 {
		t.Errorf("provider called %d times, want 0", prov.calls)
	}
}

func TestDecider_Decide_HardLimitRefuses(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 0.001}, nil)
	ct.Record("s", 0.01)
	prov := &stubDecisionProvider{resp: noulResponse(0.9, 0)}
	d := newTestDecider(prov, ct)

	_, err := d.Decide(context.Background(), "s", "state", noulQuestions())
	if !errors.Is(err, ErrHardLimitExceeded) {
		t.Fatalf("err = %v, want ErrHardLimitExceeded", err)
	}
	if prov.calls != 0 {
		t.Errorf("provider called %d times, want 0", prov.calls)
	}
}

func TestDecider_Decide_InvalidQuestionsNeverCallProvider(t *testing.T) {
	prov := &stubDecisionProvider{resp: noulResponse(0.9, 0)}
	d := newTestDecider(prov, nil)

	_, err := d.Decide(context.Background(), "s", "state", map[string]Question{"q": {Type: QuestionNoul}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if prov.calls != 0 {
		t.Errorf("provider called %d times, want 0", prov.calls)
	}
}

func TestDecider_Decide_Timeout(t *testing.T) {
	prov := &stubDecisionProvider{resp: noulResponse(0.9, 0), delay: time.Second}
	d := NewDecider(DeciderConfig{Name: "jev", Provider: "openrouter", Model: "m", Timeout: 20 * time.Millisecond, MaxInputTokens: 30000}, prov, nil)

	_, err := d.Decide(context.Background(), "s", "state", noulQuestions())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestDecider_Decide_MissingAnswerIsError(t *testing.T) {
	ct := NewCostTracker(SessionLimits{}, nil)
	prov := &stubDecisionProvider{resp: &DecisionResponse{Answers: map[string]Answer{}, CostUSD: 0.0001}}
	d := newTestDecider(prov, ct)

	_, err := d.Decide(context.Background(), "s", "state", noulQuestions())
	if err == nil || !strings.Contains(err.Error(), `"safe"`) {
		t.Fatalf("err = %v, want missing-answer error naming the question", err)
	}
	// The call happened and was billed even though the answer is unusable.
	if got := ct.SessionCost("s"); got != 0.0001 {
		t.Errorf("session cost = %v, want 0.0001", got)
	}
}

func TestDecider_Decide_ProviderErrorPropagates(t *testing.T) {
	prov := &stubDecisionProvider{err: &LLMError{StatusCode: 503, Message: "down"}}
	d := newTestDecider(prov, nil)

	_, err := d.Decide(context.Background(), "s", "state", noulQuestions())
	var llmErr *LLMError
	if !errors.As(err, &llmErr) || llmErr.StatusCode != 503 {
		t.Fatalf("err = %v, want LLMError 503", err)
	}
}

func TestDecider_WithTimeout_ClonesWithoutTouchingTheOriginal(t *testing.T) {
	prov := &stubDecisionProvider{resp: noulResponse(0.9, 0), delay: 50 * time.Millisecond}
	short := NewDecider(DeciderConfig{Name: "jev", Provider: "openrouter", Model: "m", Timeout: 5 * time.Millisecond}, prov, nil)
	long := short.WithTimeout(time.Second)

	if _, err := long.Decide(context.Background(), "s", "state", noulQuestions()); err != nil {
		t.Fatalf("clone with a longer timeout: %v", err)
	}
	if _, err := short.Decide(context.Background(), "s", "state", noulQuestions()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("original err = %v, want DeadlineExceeded: WithTimeout must not mutate it", err)
	}
	if long.Name() != "jev" || long.Model() != "m" || long.Provider() != "openrouter" {
		t.Errorf("clone lost its identity: %q %q %q", long.Name(), long.Model(), long.Provider())
	}
}

// Causes are classified with errors.Is and callers hand the provider error
// back wrapped, so each mapping is checked through a wrap — an unwrapped-only
// match would record nothing in production.

func TestDecisionErrorCause_HardLimit(t *testing.T) {
	err := fmt.Errorf("session %q exceeded hard cost limit: %w", "supervisor:default:c", ErrHardLimitExceeded)
	if got := DecisionErrorCause(err); got != "cost_limit" {
		t.Errorf("DecisionErrorCause = %q, want cost_limit", got)
	}
}

func TestDecisionErrorCause_Timeout(t *testing.T) {
	err := fmt.Errorf("chat completion: %w", context.DeadlineExceeded)
	if got := DecisionErrorCause(err); got != "timeout" {
		t.Errorf("DecisionErrorCause = %q, want timeout", got)
	}
}

func TestDecisionErrorCause_TooLarge(t *testing.T) {
	err := errors.Join(errors.New("decider \"jev\""), ErrDecisionTooLarge)
	if got := DecisionErrorCause(err); got != "too_large" {
		t.Errorf("DecisionErrorCause = %q, want too_large", got)
	}
}

func TestDecisionErrorCause_ProviderError(t *testing.T) {
	err := fmt.Errorf("chat completion: %w", errors.New("502 bad gateway"))
	if got := DecisionErrorCause(err); got != "provider_error" {
		t.Errorf("DecisionErrorCause = %q, want provider_error", got)
	}
}

// Only the hard limit refuses a call; a soft limit warns and the call still
// runs, so it must not be reported as the reason a call failed.
func TestDecisionErrorCause_SoftLimitIsNotACostRefusal(t *testing.T) {
	err := fmt.Errorf("chat completion: %w", ErrSoftLimitExceeded)
	if got := DecisionErrorCause(err); got != "provider_error" {
		t.Errorf("DecisionErrorCause = %q, want provider_error", got)
	}
}
