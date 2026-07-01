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

type outcomeToolArgs struct {
	Value string `json:"value"`
}

func okOutcomeTool(_ context.Context, _ *mcp.CallToolRequest, _ outcomeToolArgs) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "did the thing"}},
	}, nil, nil
}

func rejectOutcomeTool(_ context.Context, _ *mcp.CallToolRequest, _ outcomeToolArgs) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: "invalid arguments"}},
	}, nil, nil
}

// newOutcomeTestEngine builds an autonomous engine whose tool manager exposes
// an "ok_tool" (succeeds) and a "reject_tool" (returns IsError) backed by a
// real in-process MCP server over loopback HTTP.
func newOutcomeTestEngine(t *testing.T) *Engine {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "outcome-server", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ok_tool", Description: "ok"}, okOutcomeTool)
	mcp.AddTool(server, &mcp.Tool{Name: "reject_tool", Description: "rejects"}, rejectOutcomeTool)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	toolMgr := tool.NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	if err := toolMgr.RegisterServer(context.Background(), "outcome-tool", config.ToolConfig{
		Transport: "sse", URL: ts.URL, AllowLoopback: true,
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
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{}})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	return NewEngine("default", router, store, nil, permissions, nil, "test", nil, toolMgr, nil, testLogger())
}

func TestExecuteToolCall_OutcomeOk(t *testing.T) {
	e := newOutcomeTestEngine(t)
	_, record := e.executeToolCall(context.Background(), llm.ToolCall{
		Function: llm.FunctionCall{Name: "ok_tool", Arguments: `{"value":"x"}`},
	}, 1, "conv:1", false, nil)

	if !record.Success {
		t.Errorf("Success = false, want true")
	}
	if record.Outcome != "ok" {
		t.Errorf("Outcome = %q, want ok", record.Outcome)
	}
}

func TestExecuteToolCall_OutcomeRejected(t *testing.T) {
	e := newOutcomeTestEngine(t)
	_, record := e.executeToolCall(context.Background(), llm.ToolCall{
		Function: llm.FunctionCall{Name: "reject_tool", Arguments: `{"value":"x"}`},
	}, 1, "conv:1", false, nil)

	if record.Success {
		t.Errorf("Success = true, want false")
	}
	if record.Outcome != "rejected" {
		t.Errorf("Outcome = %q, want rejected (healthy tool, bad args)", record.Outcome)
	}
}

func TestExecuteToolCall_OutcomeFailed(t *testing.T) {
	e := newOutcomeTestEngine(t)
	// Unknown tool => manager returns a plain error (not a RejectionError).
	_, record := e.executeToolCall(context.Background(), llm.ToolCall{
		Function: llm.FunctionCall{Name: "no_such_tool", Arguments: `{}`},
	}, 1, "conv:1", false, nil)

	if record.Success {
		t.Errorf("Success = true, want false")
	}
	if record.Outcome != "failed" {
		t.Errorf("Outcome = %q, want failed (transport/exec failure)", record.Outcome)
	}
}

func TestExecuteToolCallDeduped_OutcomeDenied(t *testing.T) {
	e := newOutcomeTestEngine(t)
	tc := llm.ToolCall{Function: llm.FunctionCall{Name: "ok_tool", Arguments: `{"value":"x"}`}}
	denialKey := tc.Function.Name + "\x00" + tc.Function.Arguments
	deniedCalls := map[string]string{denialKey: "Tool call was denied by the operator."}

	_, record := e.executeToolCallDeduped(context.Background(), tc, 2, "conv:1", false, nil, deniedCalls)
	if record.Success {
		t.Errorf("Success = true, want false")
	}
	if record.Outcome != "denied" {
		t.Errorf("Outcome = %q, want denied", record.Outcome)
	}
}

func TestToolBudgetHint(t *testing.T) {
	if got, want := toolBudgetHint(50, 1), "\n\n[engine: 49 of 50 tool-call rounds remaining this turn]"; got != want {
		t.Errorf("toolBudgetHint(50, 1) = %q, want %q", got, want)
	}
	// Final round reports zero remaining, not a negative number.
	if got := toolBudgetHint(10, 10); !strings.Contains(got, "0 of 10") {
		t.Errorf("toolBudgetHint(10, 10) = %q, want to contain %q", got, "0 of 10")
	}
	// Clamp guards against an over-count (should never go negative).
	if got := toolBudgetHint(5, 8); !strings.Contains(got, "0 of 5") {
		t.Errorf("toolBudgetHint(5, 8) = %q, want to contain %q", got, "0 of 5")
	}
}

// TestExecuteToolRounds_AppendsBudgetHint verifies the tool-call loop annotates
// the final tool result of a round with an authoritative remaining-rounds hint,
// so the model reads its budget instead of counting calls by hand.
func TestExecuteToolRounds_AppendsBudgetHint(t *testing.T) {
	e := newOutcomeTestEngine(t)
	perms, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	resp := &llm.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "ok_tool", Arguments: `{"value":"x"}`}},
		},
	}
	_, msgs, _, err := e.executeToolRounds(context.Background(), "conv:hint", perms, resp, nil, nil)
	if err != nil {
		t.Fatalf("executeToolRounds: %v", err)
	}

	var toolMsg string
	for _, m := range msgs {
		if m.Role == "tool" {
			toolMsg = m.Content
		}
	}
	if !strings.Contains(toolMsg, "did the thing") {
		t.Errorf("tool message missing tool result: %q", toolMsg)
	}
	// Default cap is 50; after round 1, 49 remain.
	if !strings.Contains(toolMsg, "49 of 50 tool-call rounds remaining this turn") {
		t.Errorf("tool message missing budget hint: %q", toolMsg)
	}
}
