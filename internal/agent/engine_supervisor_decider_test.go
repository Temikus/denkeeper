package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agentctx"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

// fakeDecisionProvider answers every question with p, or fails with err.
type fakeDecisionProvider struct {
	p     float64
	cost  float64
	err   error
	delay time.Duration
	calls int
	last  llm.DecisionRequest
}

func (f *fakeDecisionProvider) Decide(ctx context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	f.calls++
	f.last = req
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	answers := make(map[string]llm.Answer, len(req.Questions))
	for id := range req.Questions {
		answers[id] = llm.Answer{Type: llm.QuestionNoul, Noul: f.p}
	}
	return &llm.DecisionResponse{Model: req.Model, Answers: answers, Usage: llm.TokenUsage{Prompt: 50, Completion: 3}, CostUSD: f.cost}, nil
}

// wireDecider puts a real llm.Decider over prov in front of h's engine,
// billed to h's tracker.
func wireDecider(h *supervisorCostHarness, prov llm.DecisionProvider, cfg llm.DeciderConfig) {
	if cfg.Name == "" {
		cfg = llm.DeciderConfig{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13", Timeout: time.Second, MaxInputTokens: 30000}
	}
	d := llm.NewDecider(cfg, prov, h.tracker)
	h.engine.SetSupervisorDecider(d, DeciderStageConfig{Mode: "shadow", ApproveAt: 0.95, DenyAt: 0.05})
}

// deciderAudit returns the decoded detail of the single decider audit event.
func deciderAudit(t *testing.T, h *supervisorCostHarness) (audit.Event, map[string]any) {
	t.Helper()
	var found []audit.Event
	for _, ev := range h.auditor.events {
		if ev.Category == audit.CategorySupervisor && strings.HasPrefix(ev.Source, "decider:") {
			found = append(found, ev)
		}
	}
	if len(found) != 1 {
		t.Fatalf("decider audit events = %d, want 1 (all: %+v)", len(found), h.auditor.events)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(found[0].Detail), &detail); err != nil {
		t.Fatalf("decider audit detail not JSON: %v", err)
	}
	return found[0], detail
}

func supervisorSays(verdict string) []*llm.ChatResponse {
	return []*llm.ChatResponse{{Content: verdict, TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"}}
}

func TestSupervisorDecider_ShadowDenyDoesNotBlockSupervisorApproval(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("APPROVE: fine"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{p: 0.01, cost: 0.0001}, llm.DeciderConfig{})

	statuses := approvalStatuses(h.chat(t, "default:test:c1", "c1"))
	if len(statuses) != 1 || statuses[0] != "supervisor_approved" {
		t.Fatalf("statuses = %v, want [supervisor_approved]: shadow must not change the outcome", statuses)
	}
	ev, detail := deciderAudit(t, h)
	if ev.Source != "decider:jev" || ev.Status != audit.StatusOK {
		t.Errorf("event source/status = %q/%q", ev.Source, ev.Status)
	}
	if detail["decision"] != "shadow" || detail["would_decide"] != "DENY" || detail["stage"] != "decider" {
		t.Errorf("detail = %v, want decision=shadow would_decide=DENY", detail)
	}
	if answers, ok := detail["answers"].(map[string]any); !ok || len(answers) != 3 {
		t.Errorf("answers = %v, want 3", detail["answers"])
	}
}

func TestSupervisorDecider_ShadowApproveDoesNotOverrideSupervisorDenial(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("DENY: no"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{p: 0.99}, llm.DeciderConfig{})

	statuses := approvalStatuses(h.chat(t, "default:test:c1", "c1"))
	if len(statuses) != 1 || statuses[0] != "supervisor_denied" {
		t.Fatalf("statuses = %v, want [supervisor_denied]", statuses)
	}
	if _, detail := deciderAudit(t, h); detail["would_decide"] != "APPROVE" {
		t.Errorf("would_decide = %v, want APPROVE", detail["would_decide"])
	}
}

func TestSupervisorDecider_WithoutSupervisorFallsToHuman(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)
	wireDecider(h, &fakeDecisionProvider{p: 0.99}, llm.DeciderConfig{})

	events := h.chat(t, "default:test:c1", "c1")
	var human bool
	for _, ev := range events {
		if ev.Type != "tool_approval" {
			continue
		}
		if strings.HasPrefix(ev.ApprovalStatus, "supervisor_") || ev.ApprovalStatus == "auto_approved" {
			t.Errorf("unexpected status %q: a shadow approve must not approve", ev.ApprovalStatus)
		}
		if ev.ApprovalID != "" {
			human = true
		}
	}
	if !human {
		t.Errorf("no human approval request surfaced; statuses = %v", approvalStatuses(events))
	}
	deciderAudit(t, h)
}

// assertDeciderFailureFallsThrough runs one supervised call with a failing
// decider (seed pre-spends its session) and checks the supervisor still
// decides and the failure is audited with cause.
func assertDeciderFailureFallsThrough(t *testing.T, prov *fakeDecisionProvider, cfg llm.DeciderConfig, seed float64, cause string, wantCalls int) {
	t.Helper()
	h := newSupervisorCostHarness(t, llm.SessionLimits{Hard: 1.0}, toolCallThenDone(), supervisorSays("APPROVE: fine"))
	defer h.teardown()
	wireDecider(h, prov, cfg)
	if seed > 0 {
		h.tracker.Record(deciderSessionKey("jev", "default", "default:test:c1"), seed)
	}

	statuses := approvalStatuses(h.chat(t, "default:test:c1", "c1"))
	if len(statuses) != 1 || statuses[0] != "supervisor_approved" {
		t.Errorf("statuses = %v, want [supervisor_approved]", statuses)
	}
	ev, detail := deciderAudit(t, h)
	if ev.Status != audit.StatusError || detail["decision"] != "error" || detail["cause"] != cause {
		t.Errorf("status=%q decision=%v cause=%v, want error/%s", ev.Status, detail["decision"], detail["cause"], cause)
	}
	if prov.calls != wantCalls {
		t.Errorf("provider calls = %d, want %d", prov.calls, wantCalls)
	}
}

func TestSupervisorDecider_ProviderErrorFallsThrough(t *testing.T) {
	prov := &fakeDecisionProvider{err: &llm.LLMError{StatusCode: 503, Message: "down"}}
	assertDeciderFailureFallsThrough(t, prov, llm.DeciderConfig{}, 0, "provider_error", 1)
}

func TestSupervisorDecider_TimeoutFallsThrough(t *testing.T) {
	prov := &fakeDecisionProvider{p: 0.99, delay: time.Second}
	cfg := llm.DeciderConfig{Name: "jev", Provider: "or", Model: "m", Timeout: 20 * time.Millisecond}
	assertDeciderFailureFallsThrough(t, prov, cfg, 0, "timeout", 1)
}

func TestSupervisorDecider_TooLargeFallsThroughWithoutCalling(t *testing.T) {
	cfg := llm.DeciderConfig{Name: "jev", Provider: "or", Model: "m", MaxInputTokens: 10}
	assertDeciderFailureFallsThrough(t, &fakeDecisionProvider{p: 0.99}, cfg, 0, "too_large", 0)
}

func TestSupervisorDecider_CostLimitFallsThroughWithoutCalling(t *testing.T) {
	assertDeciderFailureFallsThrough(t, &fakeDecisionProvider{p: 0.99}, llm.DeciderConfig{}, 5.0, "cost_limit", 0)
}

func TestSupervisorDecider_SpendBilledToReviewedAgent(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("APPROVE: fine"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{p: 0.5, cost: 0.0003}, llm.DeciderConfig{})

	h.chat(t, "default:test:c1", "c1")

	key := deciderSessionKey("jev", "default", "default:test:c1")
	if got := h.tracker.SessionCost(key); got != 0.0003 {
		t.Errorf("session %q cost = %v, want 0.0003", key, got)
	}
	for _, a := range h.tracker.AgentCosts() {
		if a.Agent == "decider" {
			t.Errorf("spend attributed to phantom agent %q", a.Agent)
		}
	}
}

func TestSupervisorDecider_StateEmbedsArgumentsAsJSON(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("APPROVE: fine"))
	defer h.teardown()
	prov := &fakeDecisionProvider{p: 0.99}
	wireDecider(h, prov, llm.DeciderConfig{})

	h.chat(t, "default:test:c1", "c1")

	raw, err := json.Marshal(prov.last.State)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	var state struct {
		Agent string `json:"agent"`
		Tool  struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"tool"`
		UserRequest string `json:"user_request"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("state %s: %v", raw, err)
	}
	if state.Agent != "default" || state.Tool.Name != "web_search" || state.Tool.Arguments["query"] != "x" {
		t.Errorf("state = %s", raw)
	}
	if state.UserRequest != "search" {
		t.Errorf("user_request = %q, want %q", state.UserRequest, "search")
	}
	if len(prov.last.Questions) != 3 {
		t.Errorf("questions = %d, want 3", len(prov.last.Questions))
	}
}

func TestRawJSONOrString_NonJSONArgumentsBecomeString(t *testing.T) {
	if got := string(rawJSONOrString(`not json`)); got != `"not json"` {
		t.Errorf("rawJSONOrString = %s", got)
	}
	if got := string(rawJSONOrString(`{"a":1}`)); got != `{"a":1}` {
		t.Errorf("rawJSONOrString = %s", got)
	}
}

func allAnswers(p float64) map[string]llm.Answer {
	return map[string]llm.Answer{
		deciderQAligned:  {Noul: p},
		deciderQSafeArgs: {Noul: p},
		deciderQScoped:   {Noul: p},
	}
}

func TestDecideSupervisorOutcome_ApproveAtBoundary(t *testing.T) {
	if got, _ := decideSupervisorOutcome(allAnswers(0.95), 0.95, 0.05); got != supervisorApprove {
		t.Errorf("all at approve_at = %s, want APPROVE", got)
	}
}

func TestDecideSupervisorOutcome_JustBelowApproveEscalates(t *testing.T) {
	a := allAnswers(0.99)
	a[deciderQScoped] = llm.Answer{Noul: 0.94}
	got, reason := decideSupervisorOutcome(a, 0.95, 0.05)
	if got != supervisorEscalate || !strings.Contains(reason, "scoped") {
		t.Errorf("= %s %q, want ESCALATE naming scoped", got, reason)
	}
}

func TestDecideSupervisorOutcome_DenyAtBoundary(t *testing.T) {
	a := allAnswers(0.99)
	a[deciderQSafeArgs] = llm.Answer{Noul: 0.05}
	got, reason := decideSupervisorOutcome(a, 0.95, 0.05)
	if got != supervisorDeny {
		t.Fatalf("one answer at deny_at = %s, want DENY", got)
	}
	if reason != "decider: arguments flagged as unsafe (p=0.05)" {
		t.Errorf("reason = %q", reason)
	}
}

func TestDecideSupervisorOutcome_JustAboveDenyEscalates(t *testing.T) {
	if got, _ := decideSupervisorOutcome(allAnswers(0.06), 0.95, 0.05); got != supervisorEscalate {
		t.Errorf("= %s, want ESCALATE", got)
	}
}

func TestDecideSupervisorOutcome_MissingAnswerEscalates(t *testing.T) {
	a := allAnswers(0.99)
	delete(a, deciderQAligned)
	if got, _ := decideSupervisorOutcome(a, 0.95, 0.05); got != supervisorEscalate {
		t.Errorf("= %s, want ESCALATE", got)
	}
}

func TestSupervisorDeciderQuestions_ScheduledSkillUsesSkillPurpose(t *testing.T) {
	qs := supervisorDeciderQuestions(&agentctx.SkillSummary{Name: "digest", IsScheduled: true})
	if err := llm.ValidateQuestions(qs); err != nil {
		t.Fatalf("questions invalid: %v", err)
	}
	for _, id := range []string{deciderQAligned, deciderQScoped} {
		if in := qs[id].Instructions; !strings.Contains(in, "`skill.description`") || strings.Contains(in, "`user_request`") {
			t.Errorf("%s instructions = %q, want skill.description only", id, in)
		}
	}
	if in := supervisorDeciderQuestions(nil)[deciderQAligned].Instructions; !strings.Contains(in, "`user_request`") {
		t.Errorf("interactive aligned instructions = %q, want user_request", in)
	}
}

func TestSetSupervisorDeciderConfig_RetunesWiredDeciderOnly(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, nil, nil)
	defer h.teardown()

	h.engine.SetSupervisorDeciderConfig(DeciderStageConfig{Mode: "shadow", ApproveAt: 0.9, DenyAt: 0.1})
	if h.engine.supervisorDecider.Load() != nil {
		t.Fatal("re-tuning must not wire a decider")
	}

	wireDecider(h, &fakeDecisionProvider{}, llm.DeciderConfig{})
	h.engine.SetSupervisorDeciderConfig(DeciderStageConfig{Mode: "shadow", ApproveAt: 0.9, DenyAt: 0.1})
	if got := h.engine.supervisorDecider.Load().cfg.ApproveAt; got != 0.9 {
		t.Errorf("approve_at = %v, want 0.9", got)
	}

	h.engine.SetSupervisorDecider(nil, DeciderStageConfig{})
	if h.engine.supervisorDecider.Load() != nil {
		t.Error("nil decider must unwire")
	}
}

func TestSupervisorErrorCause_TooLarge(t *testing.T) {
	err := errors.Join(errors.New("decider \"jev\""), llm.ErrDecisionTooLarge)
	if got := supervisorErrorCause(err); got != "too_large" {
		t.Errorf("supervisorErrorCause = %q, want too_large", got)
	}
}
