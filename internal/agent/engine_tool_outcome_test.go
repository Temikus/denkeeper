package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
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
