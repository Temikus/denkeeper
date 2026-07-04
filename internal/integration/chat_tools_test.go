//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

// chatToolHarness creates a harness with the test MCP server's echo tool wired
// into the engine, so the tool-call loop can execute tools.
func chatToolHarness(t *testing.T, responses []*llm.ChatResponse) *Harness {
	t.Helper()

	// Start test MCP server and register it with a tool manager.
	ts := startTestMCPServer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolMgr := tool.NewManager(logger)

	err := toolMgr.RegisterServer(context.Background(), "echo-tool", config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("registering test MCP server: %v", err)
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

func TestChat_ToolCallSSE(t *testing.T) {
	// Response 1: LLM requests a tool call.
	// Response 2: LLM returns final text after receiving tool result.
	h := chatToolHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "echo",
						Arguments: `{"input":"hello"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "The echo returned: hello",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	req := h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "please call echo with hello",
	})
	req.Header.Set("Accept", "text/event-stream")
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	// Verify SSE stream contains the key events.
	if !strings.Contains(body, `"type":"tool_start"`) {
		t.Error("SSE stream missing tool_start event")
	}
	if !strings.Contains(body, `"type":"tool_end"`) {
		t.Error("SSE stream missing tool_end event")
	}
	if !strings.Contains(body, `"type":"content"`) {
		t.Error("SSE stream missing content event")
	}
	if !strings.Contains(body, "The echo returned: hello") {
		t.Error("SSE stream missing final response text")
	}

	// Verify mock LLM was called twice (initial + after tool result).
	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestChat_ToolCallJSON(t *testing.T) {
	h := chatToolHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "echo",
						Arguments: `{"input":"world"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "The echo returned: world",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "please call echo with world",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["response"], "The echo returned: world") {
		t.Errorf("response = %q, want to contain 'The echo returned: world'", resp["response"])
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestChat_ToolError(t *testing.T) {
	// Mock LLM calls a tool that doesn't exist.
	h := chatToolHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_err",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "nonexistent_tool",
						Arguments: `{}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "I encountered an error with the tool.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "call a nonexistent tool",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the LLM received the error in its second call.
	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}

	// The second LLM request should contain a tool result message with the error.
	lastReq := h.MockLLM.LastRequest()
	foundToolMsg := false
	for _, msg := range lastReq.Messages {
		if msg.Role == "tool" {
			foundToolMsg = true
			break
		}
	}
	if !foundToolMsg {
		t.Error("expected a tool result message in the second LLM request")
	}
}

// TestChat_RepeatedToolCalls_WrapUp reproduces the 2026-07-04 heartbeat
// failure shape: the model calls the same tool with identical arguments
// three times in a row. Instead of delivering a blank interruption marker,
// the engine must run a tools-stripped wrap-up completion and deliver its
// summary (plus the early-end honesty marker).
func TestChat_RepeatedToolCalls_WrapUp(t *testing.T) {
	sameCall := llm.ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: llm.FunctionCall{
			Name:      "echo",
			Arguments: `{"input":"again"}`,
		},
	}
	repeat := func() *llm.ChatResponse {
		return &llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{sameCall},
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
		}
	}
	h := chatToolHarness(t, []*llm.ChatResponse{
		repeat(), repeat(), repeat(),
		{
			Content:      "Summary: echo returned 'again' twice; nothing further to do.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "check my tasks",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Summary: echo returned 'again' twice") {
		t.Errorf("response missing wrap-up summary, got: %s", body)
	}
	if !strings.Contains(body, "turn ended early") {
		t.Errorf("response missing early-end marker, got: %s", body)
	}
	if strings.Contains(body, "[Interrupted after") {
		t.Errorf("response contains the old interruption marker, got: %s", body)
	}

	// Initial + 2 round completions + wrap-up = 4 LLM calls.
	if h.MockLLM.CallCount() != 4 {
		t.Errorf("expected 4 LLM calls, got %d", h.MockLLM.CallCount())
	}

	// The wrap-up request must carry no tool definitions and must end with
	// the engine's wrap-up instruction.
	lastReq := h.MockLLM.LastRequest()
	if len(lastReq.Tools) != 0 {
		t.Errorf("wrap-up request carries %d tool definitions, want 0", len(lastReq.Tools))
	}
	lastMsg := lastReq.Messages[len(lastReq.Messages)-1]
	if lastMsg.Role != "user" || !strings.Contains(lastMsg.Content, "tool loop stopped") {
		t.Errorf("last message = [%s] %q, want the wrap-up instruction", lastMsg.Role, lastMsg.Content)
	}
}
