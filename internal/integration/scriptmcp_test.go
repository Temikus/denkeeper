//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scriptmcp"
	"github.com/Temikus/denkeeper/internal/tool"
)

// scriptToolHarness wires the in-process scriptmcp run_javascript tool into a
// tool manager and returns a harness driven by the given mock LLM responses.
func scriptToolHarness(t *testing.T, responses []*llm.ChatResponse) *Harness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolMgr := tool.NewManager(logger)

	srv := scriptmcp.New(scriptmcp.Deps{
		Enabled:        true,
		Timeout:        2 * time.Second,
		MaxOutputChars: 16000,
		MaxInputBytes:  262144,
		PermissionTier: func() string { return "autonomous" },
		Logger:         logger,
	})
	session, err := srv.Connect(context.Background())
	if err != nil {
		t.Fatalf("connecting script MCP: %v", err)
	}
	if err := toolMgr.RegisterSession(context.Background(), "script-default", session); err != nil {
		t.Fatalf("registering script MCP session: %v", err)
	}
	t.Cleanup(func() { _ = toolMgr.Close() })

	return NewHarness(t, &HarnessOpts{
		Responses: responses,
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: []string{"api"}},
		},
		ToolManager: toolMgr,
	})
}

func TestChat_RunJavaScriptReachesAdapter(t *testing.T) {
	// Response 1: LLM calls run_javascript to format a digest deterministically.
	// Response 2: LLM relays the tool's result as its reply.
	h := scriptToolHarness(t, []*llm.ChatResponse{
		{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_js",
					Type: "function",
					Function: llm.FunctionCall{
						Name: "run_javascript",
						Arguments: `{"code":"return input.items.map(function(x,i){return (i+1)+'. '+x;}).join('\\n');",` +
							`"input":{"items":["alpha","beta"]}}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "1. alpha\n2. beta",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "format the items",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["response"], "1. alpha\n2. beta") {
		t.Errorf("response = %q, want formatted list", resp["response"])
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}

	// The tool result fed back to the LLM should carry the deterministic output.
	lastReq := h.MockLLM.LastRequest()
	foundToolMsg := false
	for _, msg := range lastReq.Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "1. alpha") {
			foundToolMsg = true
			break
		}
	}
	if !foundToolMsg {
		t.Error("expected tool result message containing the formatted output")
	}
}
