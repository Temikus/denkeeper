package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// registerHumanOnlyTool registers an in-process MCP session advertising
// tool_add, the way the per-agent config-MCP server is registered, and counts
// how often the tool really runs.
func registerHumanOnlyTool(t *testing.T, e *Engine) *atomic.Int64 {
	t.Helper()
	var runs atomic.Int64
	srv := mcp.NewServer(&mcp.Implementation{Name: "config-default", Version: "v1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "tool_add", Description: "add a tool"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ toolAddArgs) (*mcp.CallToolResult, any, error) {
			runs.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Done"}}}, nil, nil
		})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("connecting server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "denkeeper-test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	if err := e.tools.RegisterSession(context.Background(), "config-default", session); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	return &runs
}

type toolAddArgs struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

func newHumanOnlyEngine(t *testing.T, tier string) (*Engine, *approval.Manager, *atomic.Int64) {
	t.Helper()
	e, _ := newCacheTestEngine(t, config.ToolConfig{}, tier)
	runs := registerHumanOnlyTool(t, e)
	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mgr := approval.NewManager(store, testLogger())
	e.approvals = mgr
	return e, mgr, runs
}

var toolAddCall = llm.ToolCall{ID: "c1", Type: "function", Function: llm.FunctionCall{
	Name: "tool_add", Arguments: `{"name":"x","command":"/usr/bin/x"}`,
}}

// answerApprovals resolves every pending approval event with approve and
// records the events it saw.
func answerApprovals(t *testing.T, mgr *approval.Manager, approve bool) (ChatEventFunc, func() []ChatEvent) {
	t.Helper()
	var mu sync.Mutex
	var events []ChatEvent
	return func(ev ChatEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
			if ev.Type == "tool_approval" && ev.ApprovalID != "" && ev.ApprovalStatus == "" {
				go func() { _, _ = mgr.Resolve(context.Background(), ev.ApprovalID, approve, "test-operator") }()
			}
		}, func() []ChatEvent {
			mu.Lock()
			defer mu.Unlock()
			return append([]ChatEvent(nil), events...)
		}
}

func TestHumanOnlyTool_AutonomousTierStillAsksOperator(t *testing.T) {
	e, mgr, runs := newHumanOnlyEngine(t, "autonomous")
	onEvent, seen := answerApprovals(t, mgr, true)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, record, _ := e.executeToolCall(ctx, toolAddCall, 1, "conv:1", false, turnRun{grant: grantAll}, onEvent)

	if record.Outcome != "ok" || runs.Load() != 1 {
		t.Fatalf("outcome = %q, runs = %d; want an approved call to run once", record.Outcome, runs.Load())
	}
	var asked *ChatEvent
	for _, ev := range seen() {
		if ev.Type == "tool_approval" && ev.ApprovalID != "" {
			asked = &ev
			break
		}
	}
	if asked == nil {
		t.Fatal("autonomous tool_add ran without an approval request")
	}
	if !asked.ApprovalHumanOnly {
		t.Error("approval event not marked human-only, so the UIs would offer 15 min / Always")
	}
	req, err := mgr.Get(ctx, asked.ApprovalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if req.Kind != approval.ActionKindInstallTool {
		t.Errorf("approval kind = %q, want %q so no auto-approve path acts on it", req.Kind, approval.ActionKindInstallTool)
	}
}

func TestHumanOnlyTool_DeniedCallDoesNotRun(t *testing.T) {
	e, mgr, runs := newHumanOnlyEngine(t, "autonomous")
	onEvent, _ := answerApprovals(t, mgr, false)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, record, _ := e.executeToolCall(ctx, toolAddCall, 1, "conv:1", false, turnRun{grant: grantAll}, onEvent)

	if runs.Load() != 0 {
		t.Fatalf("denied tool_add ran %d times", runs.Load())
	}
	if record.Outcome != outcomeDenied || !strings.Contains(result, "denied") {
		t.Errorf("outcome = %q, result = %q; want a denial", record.Outcome, result)
	}
}

// Rules of every scope are ignored: tool_add is never auto-approved.
func TestHumanOnlyTool_IgnoresAutoApproveRules(t *testing.T) {
	e, mgr, runs := newHumanOnlyEngine(t, "supervised")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mgr.SetConfigRules(ctx, map[string][]string{"default": {"tool_add"}})
	if _, err := mgr.AddPermanentRule(ctx, "default", "tool_add", "test"); err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}
	mgr.AddSessionRule(ctx, "default", "tool_add", "conv:1", "test")
	onEvent, seen := answerApprovals(t, mgr, false)

	_, record, _ := e.executeToolCall(ctx, toolAddCall, 1, "conv:1", true, turnRun{grant: grantAll}, onEvent)

	if runs.Load() != 0 || record.Outcome != outcomeDenied {
		t.Fatalf("outcome = %q, runs = %d; a rule answered a human-only call", record.Outcome, runs.Load())
	}
	for _, ev := range seen() {
		if ev.ApprovalStatus == "auto_approved" {
			t.Fatalf("tool_add was auto-approved (scope %q)", ev.ApprovalScope)
		}
	}
}

func TestHumanOnlyTool_NoAdapterFailsClosed(t *testing.T) {
	e, _, runs := newHumanOnlyEngine(t, "autonomous")
	result, record, _ := e.executeToolCall(context.Background(), toolAddCall, 1, "sched:x", false, turnRun{grant: grantAll}, nil)
	if runs.Load() != 0 {
		t.Fatalf("tool_add ran with nobody to approve it")
	}
	if record.Outcome != outcomeDenied || !strings.Contains(result, "operator") {
		t.Errorf("outcome = %q, result = %q; want a denial naming the operator", record.Outcome, result)
	}
}

func TestHumanOnlyTool_NoApprovalManagerFailsClosed(t *testing.T) {
	e, _, runs := newHumanOnlyEngine(t, "autonomous")
	e.approvals = nil
	_, record, _ := e.executeToolCall(context.Background(), toolAddCall, 1, "conv:1", false, turnRun{grant: grantAll}, func(ChatEvent) {})
	if runs.Load() != 0 || record.Outcome != outcomeDenied {
		t.Fatalf("outcome = %q, runs = %d; want a denial without an approval manager", record.Outcome, runs.Load())
	}
}

// The always callback on a human-only request approves once and creates no
// rule, so the next tool_add is asked about again.
func TestHumanOnlyTool_AlwaysCallbackCreatesNoRule(t *testing.T) {
	e, mgr, runs := newHumanOnlyEngine(t, "autonomous")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	onEvent := func(ev ChatEvent) {
		if ev.Type == "tool_approval" && ev.ApprovalCallback != "" {
			go func() { _, _ = mgr.ResolveByCallback(ctx, ev.ApprovalCallback+":approve_always", "telegram") }()
		}
	}
	if _, record, _ := e.executeToolCall(ctx, toolAddCall, 1, "conv:1", false, turnRun{grant: grantAll}, onEvent); record.Outcome != "ok" {
		t.Fatalf("outcome = %q, want ok", record.Outcome)
	}
	rules, err := mgr.ListAutoApproveRules(ctx, "default")
	if err != nil {
		t.Fatalf("ListAutoApproveRules: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules = %+v, want none", rules)
	}
	if runs.Load() != 1 {
		t.Errorf("runs = %d, want 1", runs.Load())
	}
}
