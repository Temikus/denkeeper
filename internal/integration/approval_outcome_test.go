//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/llm"
)

// A supervisor that errors sends the call to a human; nobody answers. The
// call must read back as supervisor_error in telemetry, and the agent's own
// audit view must show the supervisor event with its cause (issue #433).
func TestApprovalOutcome_SupervisorErrorThenTimeout(t *testing.T) {
	toolCall := &llm.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function",
			Function: llm.FunctionCall{Name: "echo", Arguments: `{"input":"x"}`}}},
		TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
		Model:      "test-model",
	}
	final := &llm.ChatResponse{Content: "could not run it", FinishReason: "stop",
		TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15}, Model: "test-model"}
	// Call 0 is the agent, call 1 the supervisor (fails), call 2 the agent again.
	h := supervisorHarness(t, []*llm.ChatResponse{toolCall, final, final})
	h.MockLLM.errors = []error{nil, errors.New("supervisor upstream down")}
	h.Dispatcher.Agent("default").SetApprovalConfig(100*time.Millisecond, 0)

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{"message": "call echo"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body.String())
	}

	rec = h.Do(h.AuthedRequest("GET", "/api/v1/telemetry/summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("telemetry summary: %d %s", rec.Code, rec.Body.String())
	}
	var summary struct {
		ByTool []struct {
			ToolName             string `json:"tool_name"`
			DenialCount          int    `json:"denial_count"`
			ApprovalTimeoutCount int    `json:"approval_timeout_count"`
			SupervisorErrorCount int    `json:"supervisor_error_count"`
		} `json:"by_tool"`
	}
	DecodeJSON(t, rec, &summary)
	var found bool
	for _, row := range summary.ByTool {
		if row.ToolName != "echo" {
			continue
		}
		found = true
		if row.SupervisorErrorCount != 1 || row.DenialCount != 0 || row.ApprovalTimeoutCount != 0 {
			t.Errorf("echo supervisor_error/denial/approval_timeout = %d/%d/%d, want 1/0/0",
				row.SupervisorErrorCount, row.DenialCount, row.ApprovalTimeoutCount)
		}
	}
	if !found {
		t.Fatalf("no echo row in by_tool: %+v", summary.ByTool)
	}

	h.FlushAudit(t)
	session := connectAuditView(t, h, "default")
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "approval_audit", Arguments: map[string]any{"category": "supervisor", "status": "error"},
	})
	if err != nil || res.IsError {
		t.Fatalf("approval_audit: err=%v result=%+v", err, res)
	}
	var view struct {
		Events []struct {
			Agent  string         `json:"agent"`
			Detail map[string]any `json:"detail"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &view); err != nil {
		t.Fatalf("decode approval_audit: %v", err)
	}
	if len(view.Events) != 1 || view.Events[0].Detail["cause"] != "provider_error" || view.Events[0].Detail["tool"] != "echo" {
		t.Errorf("events = %+v, want one supervisor error for echo with cause provider_error", view.Events)
	}

	// The supervisor's own view does not see reviews of another agent.
	other := connectAuditView(t, h, "guard")
	res, err = other.CallTool(context.Background(), &mcp.CallToolParams{Name: "approval_audit", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("approval_audit (guard): err=%v result=%+v", err, res)
	}
	var otherView struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &otherView); err != nil {
		t.Fatalf("decode approval_audit (guard): %v", err)
	}
	if otherView.Total != 0 {
		t.Errorf("guard sees %d events, want 0: they belong to default", otherView.Total)
	}
}

// connectAuditView serves agentName's approval_audit over the harness store.
func connectAuditView(t *testing.T, h *Harness, agentName string) *mcp.ClientSession {
	t.Helper()
	srv := configmcp.New(configmcp.Deps{AgentName: agentName, AuditStore: h.AuditStore, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := srv.Connect(context.Background())
	if err != nil {
		t.Fatalf("config MCP connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
