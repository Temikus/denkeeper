package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
)

// shadowsOf returns the Shadow of every tool_approval event, by status.
func shadowsOf(events []ChatEvent) map[string]*ShadowVerdict {
	out := map[string]*ShadowVerdict{}
	for _, ev := range events {
		if ev.Type == "tool_approval" {
			status := ev.ApprovalStatus
			if status == "" {
				status = "pending"
			}
			out[status] = ev.Shadow
		}
	}
	return out
}

func TestShadowEvent_SupervisorApprovalCarriesDeciderDeny(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("APPROVE: fine"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{p: 0.01}, llm.DeciderConfig{})

	s := shadowsOf(h.chat(t, "default:test:c1", "c1"))["supervisor_approved"]
	if s == nil {
		t.Fatal("supervisor_approved carries no shadow")
	}
	if s.Agent != "default" || s.Decider != "jev" || s.Verdict != "DENY" || s.Lowest == "" || s.Score == nil || *s.Score > 0.05 {
		t.Fatalf("shadow = %+v", *s)
	}
}

func TestShadowEvent_EscalationAndPendingCarryTheSameShadow(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("ESCALATE: unsure"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{p: 0.99}, llm.DeciderConfig{})

	shadows := shadowsOf(h.chat(t, "default:test:c1", "c1"))
	esc, pending := shadows["supervisor_escalated"], shadows["pending"]
	if esc == nil || pending == nil {
		t.Fatalf("shadows = %v, want both escalated and pending", shadows)
	}
	if !reflect.DeepEqual(esc, pending) || esc.Verdict != "APPROVE" {
		t.Fatalf("escalated %+v, pending %+v", *esc, *pending)
	}
}

func TestShadowEvent_PendingWithoutSupervisor(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)
	wireDecider(h, &fakeDecisionProvider{p: 0.99}, llm.DeciderConfig{})

	if s := shadowsOf(h.chat(t, "default:test:c1", "c1"))["pending"]; s == nil || s.Verdict != "APPROVE" {
		t.Fatalf("pending shadow = %v", s)
	}
}

func TestShadowEvent_NoneInEnforceMode(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("APPROVE: fine"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{p: 0.5}, llm.DeciderConfig{})
	h.engine.SetSupervisorDeciderConfig(DeciderStageConfig{Mode: DeciderModeEnforce, ApproveAt: 0.95, DenyAt: 0.05})

	for status, s := range shadowsOf(h.chat(t, "default:test:c1", "c1")) {
		if s != nil {
			t.Errorf("%s carries a shadow in enforce mode", status)
		}
	}
}

func TestShadowEvent_NoneWhenDeciderErrors(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("APPROVE: fine"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{err: errors.New("boom")}, llm.DeciderConfig{})

	for status, s := range shadowsOf(h.chat(t, "default:test:c1", "c1")) {
		if s != nil {
			t.Errorf("%s carries a shadow after a decider error", status)
		}
	}
}

// llm.Decider rejects a response with a missing answer, so this cannot reach
// a live turn; newShadowVerdict still must not report a score for one.
func TestNewShadowVerdict_MissingAnswerHasNoScore(t *testing.T) {
	answers := map[string]llm.Answer{}
	for _, id := range deciderQuestionIDs {
		if id != deciderQOverreach {
			answers[id] = llm.Answer{Type: llm.QuestionNoul, Noul: DeciderAnswerForScore(id, 0.99)}
		}
	}
	decision, _ := decideSupervisorOutcome(answers, 0.95, 0.05)
	s := newShadowVerdict("default", "jev", decision, answers)
	if s.Verdict != "ESCALATE" || s.Lowest != "" || s.Score != nil {
		t.Fatalf("shadow = %+v", *s)
	}
}

func TestShadowEvent_HumanDenialCarriesNone(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)
	h.engine.SetApprovalConfig(5*time.Second, 0)
	wireDecider(h, &fakeDecisionProvider{p: 0.99}, llm.DeciderConfig{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var events []ChatEvent
	onEvent := func(ev ChatEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		if ev.Type == "tool_approval" && ev.ApprovalID != "" {
			go func(id string) {
				time.Sleep(100 * time.Millisecond) // the engine waits right after this event
				_, _ = h.approvals.Resolve(ctx, id, false, "test-operator")
			}(ev.ApprovalID)
		}
	}
	msg := adapter.IncomingMessage{Adapter: "test", ExternalID: "c1", ConversationID: "default:test:c1", UserID: "u", UserName: "t", Text: "search", Timestamp: time.Now()}
	if _, err := h.engine.ChatWithEvents(ctx, msg, onEvent); err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	shadows := shadowsOf(events)
	if shadows["pending"] == nil {
		t.Fatalf("pending carries no shadow; statuses = %v", approvalStatuses(events))
	}
	denied, ok := shadows["denied"]
	if !ok {
		t.Fatalf("no denied event; statuses = %v", approvalStatuses(events))
	}
	if denied != nil {
		t.Error("the human's denied event carries a shadow")
	}
}

func TestShadowEvent_NilHandlerStillDeniedAtOnce(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)
	h.engine.SetApprovalConfig(5*time.Second, 0)
	wireDecider(h, &fakeDecisionProvider{p: 0.99}, llm.DeciderConfig{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msg := adapter.IncomingMessage{Adapter: "test", ExternalID: "c1", ConversationID: "default:test:c1", UserID: "u", UserName: "t", Text: "search", Timestamp: time.Now()}
	start := time.Now()
	if _, err := h.engine.ChatWithEvents(ctx, msg, nil); err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("turn took %v: the call waited for an approval nobody can see", elapsed)
	}
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{denied: 1})
}

func TestShadowEvent_OmittedFromJSONWhenNil(t *testing.T) {
	b, err := json.Marshal(ChatEvent{Type: "tool_approval", ApprovalStatus: "supervisor_approved"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "shadow") {
		t.Fatalf("event without a shadow verdict has a shadow key: %s", b)
	}
}
