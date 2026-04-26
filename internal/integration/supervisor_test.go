//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

// ---------------------------------------------------------------------------
// Supervisor agent integration tests
// ---------------------------------------------------------------------------

// supervisorHarness creates a harness with a supervised "default" agent, an
// autonomous "guard" agent acting as supervisor, and the echo tool wired in.
// The supervisor is linked after harness creation since the harness doesn't
// natively support the supervisor field.
func supervisorHarness(t *testing.T, responses []*llm.ChatResponse) *Harness {
	t.Helper()

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

	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised"},
			{Name: "guard", Tier: "autonomous"},
		},
		ToolManager: toolMgr,
		Responses:   responses,
	})

	// Wire the supervisor relationship.
	defaultAgent := h.Dispatcher.Agent("default")
	guardAgent := h.Dispatcher.Agent("guard")
	defaultAgent.SetSupervisor(guardAgent)

	return h
}

func TestSupervisor_Approve(t *testing.T) {
	// Response sequence:
	// 1. Primary agent LLM returns a tool call.
	// 2. Supervisor LLM returns APPROVE.
	// 3. Primary agent LLM returns final text after tool result.
	h := supervisorHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "echo",
						Arguments: `{"input":"supervisor-approved"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "APPROVE: tool call aligns with user request",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 5, Completion: 5, Total: 10},
			Model:        "test-model",
		},
		{
			Content:      "Tool returned: supervisor-approved",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat",
		map[string]string{"message": "please call echo"}))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if !strings.Contains(chatResp["response"], "supervisor-approved") {
		t.Fatalf("expected response to contain 'supervisor-approved', got: %s", chatResp["response"])
	}

	// 3 LLM calls: primary (tool_calls) + supervisor (APPROVE) + primary (final).
	if h.MockLLM.CallCount() != 3 {
		t.Errorf("expected 3 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestSupervisor_Deny(t *testing.T) {
	// Response sequence:
	// 1. Primary agent LLM returns a tool call.
	// 2. Supervisor LLM returns DENY.
	// 3. Primary agent LLM returns text acknowledging the denial.
	h := supervisorHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "echo",
						Arguments: `{"input":"should-be-denied"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "DENY: arguments look suspicious",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 5, Completion: 5, Total: 10},
			Model:        "test-model",
		},
		{
			Content:      "The tool call was denied by the supervisor.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat",
		map[string]string{"message": "call echo with something bad"}))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	// The final response should reflect the supervisor denial (LLM sees the denial reason).
	if !strings.Contains(chatResp["response"], "denied") {
		t.Fatalf("expected response to mention denial, got: %s", chatResp["response"])
	}

	// 3 LLM calls: primary (tool_calls) + supervisor (DENY) + primary (final).
	if h.MockLLM.CallCount() != 3 {
		t.Errorf("expected 3 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestSupervisor_Escalate(t *testing.T) {
	// Response sequence:
	// 1. Primary agent LLM returns a tool call.
	// 2. Supervisor LLM returns ESCALATE.
	// 3. Human approves via API (approvalWorker).
	// 4. Primary agent LLM returns final text after tool result.
	h := supervisorHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "echo",
						Arguments: `{"input":"escalated"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "ESCALATE: unusual pattern, needs human review",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 5, Completion: 5, Total: 10},
			Model:        "test-model",
		},
		{
			Content:      "Tool returned: escalated",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Auto-approve when the supervisor escalates to human.
	go approvalWorker(ctx, h, true)

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat",
		map[string]string{"message": "call echo with something unusual"}))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if !strings.Contains(chatResp["response"], "escalated") {
		t.Fatalf("expected response to contain 'escalated', got: %s", chatResp["response"])
	}

	// 3 LLM calls: primary (tool_calls) + supervisor (ESCALATE) + primary (final).
	if h.MockLLM.CallCount() != 3 {
		t.Errorf("expected 3 LLM calls, got %d", h.MockLLM.CallCount())
	}
}
