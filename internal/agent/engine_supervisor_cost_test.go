package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

// supervisorCostHarness builds a supervised engine plus an "argus" supervisor
// sharing one cost tracker, and returns both engines and the tracker.
type supervisorCostHarness struct {
	engine   *Engine
	tracker  *llm.CostTracker
	auditor  *collectingAuditor
	teardown func()
}

// newSupervisorCostHarness wires a real engine, supervisor and cost tracker so
// the limit checks under test run through llm.Router rather than a stub.
// primaryResponses drives the reviewed agent; supervisorResponses drives the
// reviewer.
func newSupervisorCostHarness(t *testing.T, limits llm.SessionLimits, primaryResponses, supervisorResponses []*llm.ChatResponse) *supervisorCostHarness {
	t.Helper()

	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}

	tracker := llm.NewCostTracker(limits, nil)

	router := llm.NewRouter("mock", "test-model", tracker)
	router.RegisterProvider(&sequentialProvider{responses: primaryResponses})

	supRouter := llm.NewRouter("mock", "sup-model", tracker)
	supRouter.RegisterProvider(&sequentialProvider{responses: supervisorResponses})

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "", nil, tool.NewManager(testLogger()), mgr, testLogger())
	// The budget fall-through parks on human approval; keep the wait short so
	// the test doesn't sit out the 5m default.
	engine.SetApprovalConfig(50*time.Millisecond, 0)

	supPerms, _ := security.NewPermissionEngine("autonomous")
	// Named "argus", not "supervisor": a supervisor whose name matches the
	// session-key prefix would hide the attribution bug this exercises.
	supEngine := NewEngine("argus", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	auditor := &collectingAuditor{}
	engine.SetAuditor(auditor)

	return &supervisorCostHarness{
		engine:  engine,
		tracker: tracker,
		auditor: auditor,
		teardown: func() {
			_ = store.Close()
			_ = approvalStore.Close()
		},
	}
}

// toolCallThenDone is one round-trip of the reviewed agent: request a tool,
// then wrap up once the approval chain resolves.
func toolCallThenDone() []*llm.ChatResponse {
	return []*llm.ChatResponse{
		{
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"x"}`}},
			},
			TokensUsed:   llm.TokenUsage{Total: 10},
			FinishReason: "tool_calls",
		},
		{Content: "done", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
	}
}

func (h *supervisorCostHarness) chat(t *testing.T, convID, externalID string) []ChatEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var events []ChatEvent
	if _, err := h.engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:        "test",
		ExternalID:     externalID,
		ConversationID: convID,
		UserID:         "u",
		UserName:       "t",
		Text:           "search",
		Timestamp:      time.Now(),
	}, func(evt ChatEvent) { events = append(events, evt) }); err != nil {
		t.Fatalf("ChatWithEvents(%s): %v", convID, err)
	}
	return events
}

func approvalStatuses(events []ChatEvent) []string {
	var out []string
	for _, evt := range events {
		if evt.Type == "tool_approval" {
			out = append(out, evt.ApprovalStatus)
		}
	}
	return out
}

func findApprovalEvent(events []ChatEvent, status string) *ChatEvent {
	for i := range events {
		if events[i].Type == "tool_approval" && events[i].ApprovalStatus == status {
			return &events[i]
		}
	}
	return nil
}

// A supervisor session that has spent its budget must not take every other
// conversation down with it. Before the per-conversation key, the supervisor
// billed every review for the process lifetime to one session ID that nothing
// ever reset, so the first conversation to exhaust the limit wedged the
// supervisor until restart and all later tool calls fell through to a human.
func TestSupervisorReview_SpentBudgetDoesNotWedgeOtherConversations(t *testing.T) {
	primary := append(toolCallThenDone(), toolCallThenDone()...)
	h := newSupervisorCostHarness(t, llm.SessionLimits{Hard: 1.0}, primary, []*llm.ChatResponse{
		{Content: "APPROVE: fine", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
	})
	defer h.teardown()

	// Exhaust the supervisor budget for conversation A only.
	spent := h.tracker.Record(supervisorSessionKey("default", "default:test:conv-a"), 5.0)
	if spent {
		t.Fatal("seeded spend did not exceed the hard limit; harness limit is wrong")
	}

	aStatuses := approvalStatuses(h.chat(t, "default:test:conv-a", "conv-a"))
	if !slicesContains(aStatuses, "supervisor_error") {
		t.Fatalf("conversation A statuses = %v, want a supervisor_error (budget spent)", aStatuses)
	}

	bEvents := h.chat(t, "default:test:conv-b", "conv-b")
	if ev := findApprovalEvent(bEvents, "supervisor_approved"); ev == nil {
		t.Errorf("conversation B statuses = %v, want supervisor_approved: conversation A's spend must not wedge B",
			approvalStatuses(bEvents))
	}
}

// Supervisor spend must land on the supervisor's own agent. The session key
// starts with "supervisor:", which agentFromSessionID would otherwise read as
// an agent literally named "supervisor" — hiding the spend from the real agent
// and skipping its configured limits.
func TestSupervisorReview_SpendAttributedToSupervisorAgent(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallThenDone(), []*llm.ChatResponse{
		{Content: "APPROVE: fine", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
	})
	defer h.teardown()

	h.chat(t, "default:test:conv-attr", "conv-attr")

	var sawArgus bool
	for _, a := range h.tracker.AgentCosts() {
		if a.Agent == "supervisor" {
			t.Errorf("cost attributed to phantom agent %q; want the supervisor's own name", a.Agent)
		}
		if a.Agent == "argus" {
			sawArgus = true
		}
	}
	if !sawArgus {
		t.Errorf("no cost entry for the supervisor agent %q; got %+v", "argus", h.tracker.AgentCosts())
	}
}

// A supervisor refused for budget is not a supervisor that is down. Both fall
// through to a human, so the operator needs the reason to be legible in the
// message and filterable in the audit detail.
func TestSupervisorReview_CostLimitErrorNamesBudget(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{Hard: 1.0}, toolCallThenDone(), []*llm.ChatResponse{
		{Content: "APPROVE: fine", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
	})
	defer h.teardown()

	h.tracker.Record(supervisorSessionKey("default", "default:test:conv-budget"), 5.0)

	events := h.chat(t, "default:test:conv-budget", "conv-budget")

	ev := findApprovalEvent(events, "supervisor_error")
	if ev == nil {
		t.Fatalf("statuses = %v, want supervisor_error", approvalStatuses(events))
	}
	if !strings.Contains(strings.ToLower(ev.Text), "cost limit") {
		t.Errorf("event text = %q, want it to name the cost limit rather than a generic outage", ev.Text)
	}

	var detail map[string]any
	for i := range h.auditor.events {
		e := &h.auditor.events[i]
		if e.Category != audit.CategorySupervisor || e.Status != audit.StatusError {
			continue
		}
		if err := json.Unmarshal([]byte(e.Detail), &detail); err != nil {
			t.Fatalf("audit detail not JSON: %v", err)
		}
		break
	}
	if detail == nil {
		t.Fatal("no supervisor audit event with status=error emitted")
	}
	if detail["cause"] != "cost_limit" {
		t.Errorf("audit detail cause = %v, want \"cost_limit\"", detail["cause"])
	}
}

// Causes are classified with errors.Is and Router.Complete hands the provider
// error back wrapped ("chat completion: %w"), so each mapping is checked
// through a wrap — an unwrapped-only match would record nothing in production.

func TestSupervisorErrorCause_HardLimit(t *testing.T) {
	err := fmt.Errorf("session %q exceeded hard cost limit: %w", "supervisor:default:c", llm.ErrHardLimitExceeded)
	if got := supervisorErrorCause(err); got != "cost_limit" {
		t.Errorf("supervisorErrorCause = %q, want %q", got, "cost_limit")
	}
}

func TestSupervisorErrorCause_Timeout(t *testing.T) {
	err := fmt.Errorf("chat completion: %w", context.DeadlineExceeded)
	if got := supervisorErrorCause(err); got != "timeout" {
		t.Errorf("supervisorErrorCause = %q, want %q", got, "timeout")
	}
}

func TestSupervisorErrorCause_ProviderError(t *testing.T) {
	err := fmt.Errorf("chat completion: %w", errors.New("502 bad gateway"))
	if got := supervisorErrorCause(err); got != "provider_error" {
		t.Errorf("supervisorErrorCause = %q, want %q", got, "provider_error")
	}
}

// Only the hard limit refuses a call; a soft limit warns and the review still
// runs, so it must not be reported as the reason a review failed.
func TestSupervisorErrorCause_SoftLimitIsNotACostRefusal(t *testing.T) {
	err := fmt.Errorf("chat completion: %w", llm.ErrSoftLimitExceeded)
	if got := supervisorErrorCause(err); got != "provider_error" {
		t.Errorf("supervisorErrorCause = %q, want %q", got, "provider_error")
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
