package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
)

// toolOutcomes returns web_search's outcome counts from the persisted
// telemetry summary, so each test checks what an agent would read back.
func toolOutcomes(t *testing.T, h *supervisorCostHarness) ToolUsageSummary {
	t.Helper()
	summary, err := h.store.GetTelemetrySummary(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("GetTelemetrySummary: %v", err)
	}
	for _, row := range summary.ByTool {
		if row.ToolName == "web_search" {
			return row
		}
	}
	t.Fatalf("no web_search row in by_tool: %+v", summary.ByTool)
	return ToolUsageSummary{}
}

type outcomeCounts struct{ denied, timeout, supErr int }

func assertOutcomes(t *testing.T, got ToolUsageSummary, want outcomeCounts) {
	t.Helper()
	if got.DenialCount != want.denied || got.ApprovalTimeoutCount != want.timeout || got.SupervisorErrorCount != want.supErr {
		t.Errorf("denial/approval_timeout/supervisor_error = %d/%d/%d, want %d/%d/%d",
			got.DenialCount, got.ApprovalTimeoutCount, got.SupervisorErrorCount,
			want.denied, want.timeout, want.supErr)
	}
}

// lastToolMessage returns the tool result the model read after the call.
func lastToolMessage(h *supervisorCostHarness) string {
	var out string
	for _, m := range h.primary.requests[len(h.primary.requests)-1].Messages {
		if m.Role == "tool" {
			out = m.Content
		}
	}
	return out
}

// chatAndDeny runs one chat and denies the first human approval it raises.
func chatAndDeny(t *testing.T, h *supervisorCostHarness) {
	t.Helper()
	h.engine.SetApprovalConfig(5*time.Second, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	onEvent := func(evt ChatEvent) {
		if evt.Type != "tool_approval" || evt.ApprovalID == "" {
			return
		}
		go func(id string) {
			// The engine starts waiting right after this event returns.
			time.Sleep(100 * time.Millisecond)
			_, _ = h.approvals.Resolve(ctx, id, false, "test-operator")
		}(evt.ApprovalID)
	}
	msg := adapter.IncomingMessage{
		Adapter: "test", ExternalID: "c1", ConversationID: "default:test:c1",
		UserID: "u", UserName: "t", Text: "search", Timestamp: time.Now(),
	}
	if _, err := h.engine.ChatWithEvents(ctx, msg, onEvent); err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}
}

// The #433 case: the supervisor fails, the call goes to a human, nobody
// answers. That is a broken reviewer, not a refusal.
func TestApprovalOutcome_SupervisorErrorThenTimeoutIsSupervisorError(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()

	statuses := approvalStatuses(h.chat(t, "default:test:c1", "c1"))
	if !slicesContains(statuses, "supervisor_error") {
		t.Fatalf("statuses = %v, want a supervisor_error", statuses)
	}
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{supErr: 1})
	if msg := lastToolMessage(h); !strings.Contains(msg, "Supervisor review failed") || !strings.Contains(msg, "timed out") {
		t.Errorf("tool result = %q, want it to name the supervisor failure and the timeout", msg)
	}
}

func TestApprovalOutcome_TimeoutWithoutSupervisorIsApprovalTimeout(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)

	h.chat(t, "default:test:c1", "c1")
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{timeout: 1})
}

// A supervisor that worked and escalated did its job; only the human was absent.
func TestApprovalOutcome_SupervisorEscalateThenTimeoutIsApprovalTimeout(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("ESCALATE: unsure"))
	defer h.teardown()

	h.chat(t, "default:test:c1", "c1")
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{timeout: 1})
}

func TestApprovalOutcome_SupervisorDenyIsDenied(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("DENY: no"))
	defer h.teardown()

	h.chat(t, "default:test:c1", "c1")
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{denied: 1})
}

func TestApprovalOutcome_HumanDenyIsDenied(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)

	chatAndDeny(t, h)
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{denied: 1})
}

// A human who answers after a supervisor error decided the call; the error
// does not override that.
func TestApprovalOutcome_HumanDenyAfterSupervisorErrorIsDenied(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()

	chatAndDeny(t, h)
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{denied: 1})
}

func TestApprovalOutcome_DeciderEnforceDenyIsDenied(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("APPROVE: fine"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{p: 0.01}, llm.DeciderConfig{})
	enforce(h)

	h.chat(t, "default:test:c1", "c1")
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{denied: 1})
}

// With no supervisor, an enforcing decider is the only reviewer, so its
// failure is what sent the call to a human.
func TestApprovalOutcome_DeciderErrorWithoutSupervisorThenTimeoutIsSupervisorError(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)
	wireDecider(h, &fakeDecisionProvider{err: &llm.LLMError{StatusCode: 503, Message: "down"}}, llm.DeciderConfig{})
	enforce(h)

	h.chat(t, "default:test:c1", "c1")
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{supErr: 1})
	if msg := lastToolMessage(h); !strings.HasPrefix(msg, "Decider review failed") {
		t.Errorf("tool result = %q, want it to name the decider failure", msg)
	}
}

// The supervisor ran after the failed decider and chose to escalate, so the
// timeout is the human's, not the decider's.
func TestApprovalOutcome_DeciderErrorThenSupervisorEscalateThenTimeoutIsApprovalTimeout(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), supervisorSays("ESCALATE: unsure"))
	defer h.teardown()
	wireDecider(h, &fakeDecisionProvider{err: &llm.LLMError{StatusCode: 503, Message: "down"}}, llm.DeciderConfig{})
	enforce(h)

	h.chat(t, "default:test:c1", "c1")
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{timeout: 1})
}

// A shadow decider never affects the outcome, including when it fails.
func TestApprovalOutcome_ShadowDeciderErrorThenTimeoutIsApprovalTimeout(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)
	wireDecider(h, &fakeDecisionProvider{err: &llm.LLMError{StatusCode: 503, Message: "down"}}, llm.DeciderConfig{})

	h.chat(t, "default:test:c1", "c1")
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{timeout: 1})
}

// A repeat auto-denied by the per-turn dedup keeps the original outcome, or
// one unanswered call would count once as a timeout and once as a denial.
func TestApprovalOutcome_RepeatOfTimedOutCallKeepsOutcome(t *testing.T) {
	primary := toolCallThenDone()
	primary = append([]*llm.ChatResponse{primary[0]}, primary...)
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, primary, nil)
	defer h.teardown()
	h.engine.SetSupervisor(nil)

	statuses := approvalStatuses(h.chat(t, "default:test:c1", "c1"))
	if !slicesContains(statuses, "auto_denied") {
		t.Fatalf("statuses = %v, want the repeat auto-denied", statuses)
	}
	assertOutcomes(t, toolOutcomes(t, h), outcomeCounts{timeout: 2})
}
