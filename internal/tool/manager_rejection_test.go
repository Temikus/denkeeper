package tool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// rejectingTool is an MCP tool handler that returns an application-level error
// result (IsError=true) without a transport error — simulating a healthy tool
// rejecting bad arguments.
func rejectingTool(_ context.Context, _ *mcp.CallToolRequest, _ sayHiParams) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: "invalid arguments: missing required field 'cron'"},
		},
	}, nil, nil
}

// startRejectingServer starts an httptest MCP server whose single "schedule_update"
// tool always returns IsError=true.
func startRejectingServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "reject-server", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "schedule_update", Description: "update a schedule"}, rejectingTool)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestExecute_IsErrorResultReturnsRejectionError(t *testing.T) {
	ts := startRejectingServer(t)

	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	cfg := config.ToolConfig{Transport: "sse", URL: ts.URL, AllowLoopback: true}
	if err := m.RegisterServer(context.Background(), "reject-tool", cfg); err != nil {
		t.Fatalf("RegisterServer failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	text, err := m.Execute(context.Background(), llm.ToolCall{
		Function: llm.FunctionCall{Name: "schedule_update", Arguments: `{"name":"x"}`},
	})
	if err == nil {
		t.Fatal("Execute() should return an error for an IsError result")
	}

	var re *RejectionError
	if !errors.As(err, &re) {
		t.Fatalf("error should be a *RejectionError, got %T: %v", err, err)
	}
	if re.Tool != "schedule_update" {
		t.Errorf("RejectionError.Tool = %q, want schedule_update", re.Tool)
	}
	if re.Text == "" {
		t.Error("RejectionError.Text should carry the tool error text")
	}
	// The result text is still returned so it can be fed back to the LLM.
	if text == "" {
		t.Error("Execute() should still return the tool result text alongside the rejection error")
	}
}

func TestExecute_SessionNilIsNotRejectionError(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{
		name:    "test-server",
		session: nil, // not connected — a transport/exec failure, not a rejection
		cfg:     config.ToolConfig{Command: "test"},
	}
	m.servers["test-server"] = sc
	m.toolMap = map[string]*serverConn{"some_tool": sc}

	_, err := m.Execute(context.Background(), llm.ToolCall{
		Function: llm.FunctionCall{Name: "some_tool"},
	})
	if err == nil {
		t.Fatal("Execute() should error when session is nil")
	}
	var re *RejectionError
	if errors.As(err, &re) {
		t.Fatalf("session-nil failure must NOT be a *RejectionError, got %v", err)
	}
}

func TestRejectionError_ErrorString(t *testing.T) {
	re := &RejectionError{Tool: "do_thing", Text: "bad arg"}
	got := re.Error()
	want := `tool "do_thing" returned error: bad arg`
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
