package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

type tierToolArgs struct {
	Value string `json:"value"`
}

// newRestrictedTestEngine builds a restricted-tier engine whose tool manager
// hosts a "read_thing" declared read-only by the operator and a "write_thing"
// nobody classified. Both execute if they are reached, so a test can tell a
// refusal from an execution by the record alone.
func newRestrictedTestEngine(t *testing.T, tier string) *Engine {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "tier-server", Version: "v1"}, nil)
	exec := func(_ context.Context, _ *mcp.CallToolRequest, _ tierToolArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ran the tool"}},
		}, nil, nil
	}
	mcp.AddTool(server, &mcp.Tool{Name: "read_thing", Description: "reads"}, exec)
	mcp.AddTool(server, &mcp.Tool{Name: "write_thing", Description: "writes"}, exec)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	toolMgr := tool.NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	if err := toolMgr.RegisterServer(context.Background(), "tier-tool", config.ToolConfig{
		Transport: "sse", URL: ts.URL, AllowLoopback: true,
		IdempotentTools: []string{"read_thing"},
	}); err != nil {
		t.Fatalf("RegisterServer: %v", err)
	}
	t.Cleanup(func() { _ = toolMgr.Close() })

	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "done", FinishReason: "stop"}})

	permissions, err := security.NewPermissionEngine(tier)
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	return NewEngine("default", router, store, nil, permissions, nil, "test", nil, toolMgr, nil, testLogger())
}

// toolUsage returns the per-tool telemetry row for name.
func toolUsage(t *testing.T, summary *TelemetrySummary, name string) ToolUsageSummary {
	t.Helper()
	for _, u := range summary.ByTool {
		if u.ToolName == name {
			return u
		}
	}
	t.Fatalf("no telemetry row for tool %q in %+v", name, summary.ByTool)
	return ToolUsageSummary{}
}

func TestGrantFor_RestrictedTierGetsReadOnlyTools(t *testing.T) {
	for tier, want := range map[string]toolGrant{
		"autonomous": grantAll,
		"supervised": grantAll,
		"restricted": grantReadOnly,
	} {
		perms, err := security.NewPermissionEngine(tier)
		if err != nil {
			t.Fatalf("NewPermissionEngine(%q): %v", tier, err)
		}
		if got := grantFor(perms); got != want {
			t.Errorf("grantFor(%q) = %d, want %d", tier, got, want)
		}
	}
}

func TestGrantFor_NoTierPermissionsGrantsNothing(t *testing.T) {
	if got := grantFor(security.NewDenyAll()); got != grantNone {
		t.Errorf("grantFor(deny-all) = %d, want grantNone", got)
	}
}

func TestToolGrant_ReadOnlyWithoutClassifierAdmitsNothing(t *testing.T) {
	// No tool manager means nothing is classified, so the read-only grant must
	// refuse rather than fall through to "unknown, allow".
	if grantReadOnly.allows("anything", nil) {
		t.Error("grantReadOnly admitted a call with no classifier wired")
	}
}

func TestExecuteToolCallDeduped_RestrictedTier_RunsReadOnlyTool(t *testing.T) {
	e := newRestrictedTestEngine(t, "restricted")
	result, record := e.executeToolCallDeduped(context.Background(), llm.ToolCall{
		ID: "c1", Function: llm.FunctionCall{Name: "read_thing", Arguments: `{"value":"x"}`},
	}, 1, "conv:1", false, turnRun{grant: grantReadOnly}, nil, newTurnToolState())

	if record.Outcome != "ok" || !record.Success {
		t.Fatalf("record = {Outcome:%q Success:%v}, want a real execution", record.Outcome, record.Success)
	}
	if !strings.Contains(result, "ran the tool") {
		t.Errorf("result = %q, want the tool's own output", result)
	}
}

func TestExecuteToolCallDeduped_RestrictedTier_DeniesUnclassifiedTool(t *testing.T) {
	e := newRestrictedTestEngine(t, "restricted")
	result, record := e.executeToolCallDeduped(context.Background(), llm.ToolCall{
		ID: "c1", Function: llm.FunctionCall{Name: "write_thing", Arguments: `{"value":"x"}`},
	}, 1, "conv:1", false, turnRun{grant: grantReadOnly}, nil, newTurnToolState())

	if record.Outcome != "denied" || record.Success {
		t.Fatalf("record = {Outcome:%q Success:%v}, want a denial", record.Outcome, record.Success)
	}
	if strings.Contains(result, "ran the tool") {
		t.Fatalf("result = %q — the write ran", result)
	}
	if !strings.Contains(result, "read-only tools only") {
		t.Errorf("result = %q, want the reason the model can act on", result)
	}
}

func TestExecuteToolCallDeduped_SupervisedTier_RunsUnclassifiedTool(t *testing.T) {
	// The read-only classification gates only the restricted tier; a tier
	// holding use_tools is unaffected by it.
	e := newRestrictedTestEngine(t, "supervised")
	_, record := e.executeToolCallDeduped(context.Background(), llm.ToolCall{
		ID: "c1", Function: llm.FunctionCall{Name: "write_thing", Arguments: `{"value":"x"}`},
	}, 1, "conv:1", false, turnRun{grant: grantAll}, nil, newTurnToolState())

	if record.Outcome != "ok" {
		t.Errorf("Outcome = %q, want ok", record.Outcome)
	}
}

func TestExecuteToolCallDeduped_DeniedByGrant_NeverReachesApproval(t *testing.T) {
	// A restricted agent has no approval chain to fall back on, so a denial
	// must be final and must not wait on one.
	e := newRestrictedTestEngine(t, "restricted")
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	var events []ChatEvent
	_, record := e.executeToolCallDeduped(context.Background(), llm.ToolCall{
		ID: "c1", Function: llm.FunctionCall{Name: "write_thing", Arguments: `{"value":"x"}`},
	}, 2, "conv:1", true, turnRun{grant: grantReadOnly}, func(ev ChatEvent) { events = append(events, ev) }, newTurnToolState())

	if record.ErrorMsg != "denied (read-only tier)" {
		t.Errorf("ErrorMsg = %q, want the tier denial to be distinguishable from an operator denial", record.ErrorMsg)
	}
	for _, ev := range events {
		if ev.Type == "tool_approval" {
			t.Errorf("unexpected tool_approval event: %+v", ev)
		}
	}
	var denied bool
	for _, ev := range auditor.events {
		if ev.Action == "denied" && ev.Summary == "write_thing" {
			denied = true
		}
	}
	if !denied {
		t.Error("no denied audit event emitted for the refused call")
	}
}
