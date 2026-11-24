package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// newHighRiskEngine is a supervised engine with a permanent rule on
// schedule_update and no supervisor, so a call the rule skips goes to a human.
func newHighRiskEngine(t *testing.T) (*Engine, *approval.Manager) {
	t.Helper()
	e, _ := newCacheTestEngine(t, config.ToolConfig{}, "supervised")
	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mgr := approval.NewManager(store, testLogger())
	if _, err := mgr.AddPermanentRule(context.Background(), "default", "schedule_update", "test"); err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}
	e.approvals = mgr
	e.SetApprovalConfig(5*time.Second, 0)
	return e, mgr
}

func scheduleUpdateCall(args string) llm.ToolCall {
	return llm.ToolCall{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "schedule_update", Arguments: args}}
}

func TestEngine_HighRiskCallSkipsRuleAndCarriesRisk(t *testing.T) {
	e, mgr := newHighRiskEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	onEvent, seen := answerApprovals(t, mgr, false)

	e.resolveSupervisedApproval(ctx, scheduleUpdateCall(`{"name":"nightly","session_tier":"autonomous"}`), 1, "conv:1", turnRun{}, onEvent)

	var pending *ChatEvent
	for _, ev := range seen() {
		if ev.Type != "tool_approval" {
			continue
		}
		if ev.ApprovalStatus == "auto_approved" {
			t.Fatalf("high-risk call auto-approved (scope %q)", ev.ApprovalScope)
		}
		if ev.ApprovalID != "" && ev.ApprovalStatus == "" {
			pending = &ev
		}
	}
	if pending == nil {
		t.Fatal("no pending tool_approval event")
	}
	if pending.ApprovalRisk == nil || pending.ApprovalRisk.Flagged[0].Name != "session_tier" {
		t.Errorf("ApprovalRisk = %+v, want session_tier flagged", pending.ApprovalRisk)
	}
}

func TestEngine_HarmlessCallStillAutoApprovedWithNoRiskKey(t *testing.T) {
	e, _ := newHighRiskEngine(t)
	var events []ChatEvent
	outcome := e.resolveSupervisedApproval(context.Background(), scheduleUpdateCall(`{"name":"nightly","schedule":"@daily"}`),
		1, "conv:1", turnRun{}, func(ev ChatEvent) { events = append(events, ev) })

	if outcome.denied || len(events) != 1 || events[0].ApprovalStatus != "auto_approved" {
		t.Fatalf("outcome denied=%v, events %+v; want one auto_approved event", outcome.denied, events)
	}
	raw, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "approval_risk") {
		t.Errorf("event JSON carries approval_risk: %s", raw)
	}
}
