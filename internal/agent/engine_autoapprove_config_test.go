package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// TestEngine_ConfigAutoApprove_ExecutesAndAttributesScope pins the end-to-end
// behaviour of a TOML-declared auto-approve rule on a supervised agent: the
// tool executes with no approval row, the tool_approval event carries the
// machine-readable scope, and the auto-approval lands in the audit log.
func TestEngine_ConfigAutoApprove_ExecutesAndAttributesScope(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{}, "supervised")

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	t.Cleanup(func() { _ = approvalStore.Close() })
	mgr := approval.NewManager(approvalStore, testLogger())
	mgr.SetConfigRules(context.Background(), map[string][]string{"default": {"lookup"}})
	e.approvals = mgr

	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{Content: "Found it.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	router := llm.NewRouter("mock", "test-model", llm.NewCostTracker(llm.SessionLimits{}, nil))
	router.RegisterProvider(provider)
	e.router = router

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var events []ChatEvent
	result, err := e.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-config-approve",
		UserID:     "user-1",
		Text:       "look it up",
		Timestamp:  time.Now(),
	}, func(ev ChatEvent) { events = append(events, ev) })
	if err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}
	if result != "Found it." {
		t.Errorf("result = %q, want %q", result, "Found it.")
	}
	if got := execCount.Load(); got != 1 {
		t.Errorf("tool executed %d times, want 1", got)
	}

	// No approval row was ever created — the chain was skipped, not resolved.
	all, err := mgr.List(ctx, "")
	if err != nil {
		t.Fatalf("listing approvals: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("approval rows = %d, want 0 (config rules skip submission entirely)", len(all))
	}

	var approvalEvent *ChatEvent
	for i := range events {
		if events[i].Type == "tool_approval" {
			approvalEvent = &events[i]
			break
		}
	}
	if approvalEvent == nil {
		t.Fatal("no tool_approval event emitted")
	}
	if approvalEvent.ApprovalStatus != "auto_approved" {
		t.Errorf("ApprovalStatus = %q, want auto_approved", approvalEvent.ApprovalStatus)
	}
	if approvalEvent.ApprovalScope != string(approval.ScopeConfig) {
		t.Errorf("ApprovalScope = %q, want %q", approvalEvent.ApprovalScope, approval.ScopeConfig)
	}
	if !strings.Contains(approvalEvent.Text, "config") {
		t.Errorf("Text = %q, want the scope named in the human-readable text", approvalEvent.Text)
	}

	var autoApprove *audit.Event
	for i := range auditor.events {
		if auditor.events[i].Category == audit.CategoryApproval && auditor.events[i].Action == "auto_approve" {
			autoApprove = &auditor.events[i]
			break
		}
	}
	if autoApprove == nil {
		t.Fatal("no approval/auto_approve audit event emitted")
	}
	if autoApprove.Summary != "lookup" {
		t.Errorf("audit Summary = %q, want lookup", autoApprove.Summary)
	}
	if autoApprove.Status != audit.StatusOK {
		t.Errorf("audit Status = %v, want ok", autoApprove.Status)
	}
	if !strings.Contains(autoApprove.Detail, `"scope":"config"`) {
		t.Errorf("audit Detail = %q, want scope config", autoApprove.Detail)
	}
	if !strings.Contains(autoApprove.Detail, `"tool":"lookup"`) {
		t.Errorf("audit Detail = %q, want the tool name", autoApprove.Detail)
	}
}

// TestEngine_AutoApprove_AuditsEveryScope pins that the audit event is emitted
// for permanent rules too — it closes an observability gap that predates the
// config scope, so it must not be config-only.
func TestEngine_AutoApprove_AuditsEveryScope(t *testing.T) {
	e, _ := newCacheTestEngine(t, config.ToolConfig{}, "supervised")

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	t.Cleanup(func() { _ = approvalStore.Close() })
	mgr := approval.NewManager(approvalStore, testLogger())
	if _, err := mgr.AddPermanentRule(context.Background(), "default", "lookup", "test"); err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}
	e.approvals = mgr

	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	tc := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}}
	var events []ChatEvent
	outcome := e.resolveSupervisedApproval(context.Background(), tc, 1, "conv:1",
		func(ev ChatEvent) { events = append(events, ev) })
	if outcome.denied {
		t.Fatal("permanent rule should auto-approve")
	}

	if len(events) != 1 || events[0].ApprovalScope != string(approval.ScopePermanent) {
		t.Errorf("events = %+v, want one event with scope permanent", events)
	}

	found := false
	for _, ev := range auditor.events {
		if ev.Category == audit.CategoryApproval && ev.Action == "auto_approve" &&
			strings.Contains(ev.Detail, `"scope":"permanent"`) {
			found = true
		}
	}
	if !found {
		t.Error("no approval/auto_approve audit event for a permanent-scoped auto-approval")
	}
}
