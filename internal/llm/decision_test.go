package llm

import (
	"context"
	"errors"
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

func TestValidateQuestions_Rejects(t *testing.T) {
	manyChoices := make(map[string]string, 256)
	for i := range 256 {
		manyChoices[strings.Repeat("x", i+1)] = "d"
	}
	cases := map[string]map[string]Question{
		"empty set":          {},
		"empty id":           {"": {Type: QuestionNoul, Instructions: "x"}},
		"no instructions":    {"q": {Type: QuestionNoul, Instructions: "  "}},
		"unknown type":       {"q": {Type: "rank", Instructions: "x"}},
		"one choice":         {"q": {Type: QuestionChoice, Instructions: "x", Choices: map[string]string{"a": "A"}}},
		"256 choices":        {"q": {Type: QuestionChoice, Instructions: "x", Choices: manyChoices}},
		"choice with levels": {"q": {Type: QuestionChoice, Instructions: "x", Choices: map[string]string{"a": "A", "b": "B"}, Levels: []string{"l", "m"}}},
		"one level":          {"q": {Type: QuestionScore, Instructions: "x", Levels: []string{"l"}}},
		"eleven levels":      {"q": {Type: QuestionScore, Instructions: "x", Levels: make([]string, 11)}},
		"score with choices": {"q": {Type: QuestionScore, Instructions: "x", Levels: []string{"l", "m"}, Choices: map[string]string{"a": "A"}}},
		"noul bad key":       {"q": {Type: QuestionNoul, Instructions: "x", Choices: map[string]string{"maybe": "?"}}},
		"noul with levels":   {"q": {Type: QuestionNoul, Instructions: "x", Levels: []string{"l", "m"}}},
	}
	for name, qs := range cases {
		if err := ValidateQuestions(qs); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
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
