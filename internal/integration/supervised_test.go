//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

// ---------------------------------------------------------------------------
// Supervised tool approval flow (Phase 4 of E2E test plan)
// ---------------------------------------------------------------------------

// supervisedHarness creates a harness with a supervised agent and the echo
// tool wired in, for testing the approval flow via direct chat (no channels).
func supervisedHarness(t *testing.T, responses []*llm.ChatResponse) *Harness {
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

	return NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised"},
		},
		ToolManager: toolMgr,
		Responses:   responses,
	})
}

func supervisedToolCallResponses() []*llm.ChatResponse {
	return []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "echo",
						Arguments: `{"input":"supervised-test"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "Tool returned: supervised-test",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	}
}

// approvalWorker polls for pending approvals and resolves them.
func approvalWorker(ctx context.Context, h *Harness, approve bool) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		rec := h.Do(h.AuthedRequest("GET", "/api/v1/approvals?status=pending", nil))
		if rec.Code != http.StatusOK {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		var pending []map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&pending)
		for _, appr := range pending {
			id, _ := appr["id"].(string)
			if id == "" {
				continue
			}
			action := "approve"
			if !approve {
				action = "deny"
			}
			h.Do(h.AuthedRequest("POST", "/api/v1/approvals/"+id+"/"+action, nil))
		}
		if len(pending) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSupervised_ToolCallApproved(t *testing.T) {
	h := supervisedHarness(t, supervisedToolCallResponses())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go approvalWorker(ctx, h, true)

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat",
		map[string]string{"message": "please call echo"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if !strings.Contains(chatResp["response"], "supervised-test") {
		t.Fatalf("expected response to contain 'supervised-test', got: %s", chatResp["response"])
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestSupervised_ToolCallDenied(t *testing.T) {
	h := supervisedHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "echo",
						Arguments: `{"input":"denied-test"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "The tool call was denied by the operator.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go approvalWorker(ctx, h, false)

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat",
		map[string]string{"message": "please call echo"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if chatResp["response"] == "" {
		t.Fatal("expected non-empty response after tool denial")
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestSupervised_AutoApprovePermanent(t *testing.T) {
	h := supervisedHarness(t, supervisedToolCallResponses())

	// Create a permanent auto-approve rule for the echo tool.
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/auto-approve", map[string]any{
		"agent": "default",
		"tool":  "echo",
		"scope": "permanent",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating auto-approve rule: %d %s", rec.Code, rec.Body.String())
	}

	// Chat — tool should execute immediately without manual approval.
	chatRec := h.Do(h.AuthedRequest("POST", "/api/v1/chat",
		map[string]string{"message": "please call echo"}))
	if chatRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", chatRec.Code, chatRec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, chatRec, &chatResp)
	if !strings.Contains(chatResp["response"], "supervised-test") {
		t.Fatalf("expected response to contain 'supervised-test', got: %s", chatResp["response"])
	}

	// No pending approvals should remain.
	listRec := h.Do(h.AuthedRequest("GET", "/api/v1/approvals?status=pending", nil))
	var pending []map[string]any
	_ = json.NewDecoder(listRec.Body).Decode(&pending)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending approvals, got %d", len(pending))
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestSupervised_AutoApproveSession(t *testing.T) {
	h := supervisedHarness(t, supervisedToolCallResponses())

	// Use an explicit session_id so the conversation_id is predictable.
	const sessionID = "default:api:auto-session"

	// Create a session-scoped auto-approve rule matching that conversation.
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/auto-approve", map[string]any{
		"agent":           "default",
		"tool":            "echo",
		"scope":           "session",
		"conversation_id": sessionID,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating auto-approve rule: %d %s", rec.Code, rec.Body.String())
	}

	// Chat with explicit session_id — tool should execute immediately.
	chatRec := h.Do(h.AuthedRequest("POST", "/api/v1/chat",
		map[string]string{"message": "please call echo", "session_id": sessionID}))
	if chatRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", chatRec.Code, chatRec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, chatRec, &chatResp)
	if !strings.Contains(chatResp["response"], "supervised-test") {
		t.Fatalf("expected response to contain 'supervised-test', got: %s", chatResp["response"])
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}
