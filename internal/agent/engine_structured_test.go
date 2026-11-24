package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

// structuredMarker is a value only the structured content carries, so a test
// can tell whether it leaked anywhere the text alone should be.
const structuredMarker = "only-in-structured-7f3a"

// newStructuredEngine builds an autonomous engine whose in-process tool
// "lookup" returns res verbatim, and records its audit events.
func newStructuredEngine(t *testing.T, res *mcp.CallToolResult, provider llm.Provider) (*Engine, *collectingAuditor) {
	t.Helper()
	mgr := tool.NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	t.Cleanup(func() { _ = mgr.Close() })

	srv := mcp.NewServer(&mcp.Implementation{Name: "structured", Version: "v1"}, nil)
	srv.AddTool(&mcp.Tool{Name: "lookup", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return res, nil })
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), serverT, nil); err != nil {
		t.Fatalf("connecting server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "denkeeper-test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	if err := mgr.RegisterSession(context.Background(), "structured", session); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	e := newSatisfactionEngine(t, mgr, nil, provider)
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)
	return e, auditor
}

func lookupThenStop() []*llm.ChatResponse {
	return []*llm.ChatResponse{
		toolCallResponse("c1", "lookup", `{}`),
		{Content: "done", FinishReason: "stop", Model: "test-model"},
	}
}

func runStructuredTurn(t *testing.T, e *Engine) {
	t.Helper()
	if _, err := e.Chat(context.Background(), adapter.IncomingMessage{
		Adapter: "api", ExternalID: "u1", Text: "look it up",
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

// executeAuditDetail returns the decoded detail of the one tool-call execute
// event, and its raw text.
func executeAuditDetail(t *testing.T, a *collectingAuditor) (map[string]any, string) {
	t.Helper()
	for _, ev := range a.events {
		if ev.Action == "execute" && ev.Summary == "lookup" {
			var detail map[string]any
			if err := json.Unmarshal([]byte(ev.Detail), &detail); err != nil {
				t.Fatalf("decoding audit detail: %v (%s)", err, ev.Detail)
			}
			return detail, ev.Detail
		}
	}
	t.Fatalf("no execute audit event for lookup in %+v", a.events)
	return nil, ""
}

func TestAuditToolExecution_RecordsStructuredSizeNotValue(t *testing.T) {
	structured := map[string]any{"id": structuredMarker}
	e, auditor := newStructuredEngine(t, &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: "found it"}},
		StructuredContent: structured,
	}, &sequentialProvider{responses: lookupThenStop()})

	runStructuredTurn(t, e)

	detail, raw := executeAuditDetail(t, auditor)
	want, _ := json.Marshal(structured)
	if got, ok := detail["structured_bytes"].(float64); !ok || int(got) != len(want) {
		t.Errorf("structured_bytes = %v, want %d", detail["structured_bytes"], len(want))
	}
	if detail["result"] != "found it" {
		t.Errorf("result = %v, want the text", detail["result"])
	}
	if strings.Contains(raw, structuredMarker) {
		t.Errorf("audit detail carries the structured value: %s", raw)
	}
}

func TestAuditToolExecution_TextOnlyHasNoStructuredBytes(t *testing.T) {
	e, auditor := newStructuredEngine(t, &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "found it"}},
	}, &sequentialProvider{responses: lookupThenStop()})

	runStructuredTurn(t, e)

	detail, _ := executeAuditDetail(t, auditor)
	if _, ok := detail["structured_bytes"]; ok {
		t.Errorf("structured_bytes present for a text-only result: %v", detail)
	}
	for _, key := range []string{"tool", "server", "round", "arguments", "result"} {
		if _, ok := detail[key]; !ok {
			t.Errorf("detail is missing %q: %v", key, detail)
		}
	}
	if len(detail) != 5 {
		t.Errorf("detail has %d keys, want the 5 it carries today: %v", len(detail), detail)
	}
}

func TestExecuteToolCall_ModelReadsTheTextAlone(t *testing.T) {
	provider := &capturingProvider{responses: lookupThenStop()}
	e, _ := newStructuredEngine(t, &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: "found it"}},
		StructuredContent: map[string]any{"id": structuredMarker},
	}, provider)

	runStructuredTurn(t, e)

	if len(provider.requests) < 2 {
		t.Fatalf("provider received %d requests, want 2", len(provider.requests))
	}
	var toolMsg string
	for _, m := range provider.requests[1].Messages {
		if m.Role == "tool" && m.ToolCallID == "c1" {
			toolMsg = m.Content
		}
	}
	// The round's budget hint follows the result, so match the prefix.
	if !strings.HasPrefix(toolMsg, "found it") {
		t.Errorf("tool message = %q, want it to start with the text", toolMsg)
	}
	if strings.Contains(toolMsg, structuredMarker) {
		t.Errorf("tool message carries the structured value: %q", toolMsg)
	}
}
