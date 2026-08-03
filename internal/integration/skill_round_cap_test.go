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
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
	"github.com/Temikus/denkeeper/internal/tool"
)

// skillRoundCapResponses returns three tool-call rounds followed by a text
// response. A capped run stops early and spends the text response on the
// wrap-up; an uncapped run consumes all three rounds and finishes on it.
func skillRoundCapResponses() []*llm.ChatResponse {
	round := func(id string) *llm.ChatResponse {
		return &llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:       id,
				Type:     "function",
				Function: llm.FunctionCall{Name: "echo", Arguments: `{"input":"` + id + `"}`},
			}},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		}
	}
	return []*llm.ChatResponse{
		round("call_1"), round("call_2"), round("call_3"),
		{
			Content:      "Summary: did what fit in the budget.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	}
}

// skillRoundCapHarness wires the test MCP echo tool plus a single
// command-triggered skill, so a "/capped ..." message is an explicitly-invoked
// turn that resolveToolBudget can attribute.
func skillRoundCapHarness(t *testing.T, skills []skill.Skill) *Harness {
	t.Helper()

	ts := startTestMCPServer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolMgr := tool.NewManager(logger)
	if err := toolMgr.RegisterServer(context.Background(), "echo-tool", config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}); err != nil {
		t.Fatalf("registering test MCP server: %v", err)
	}
	t.Cleanup(func() { _ = toolMgr.Close() })

	return NewHarness(t, &HarnessOpts{
		Responses: skillRoundCapResponses(),
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: []string{"api"}, Skills: skills},
		},
		ToolManager: toolMgr,
	})
}

// A command-invoked skill carrying max_tool_rounds = 2 must stop the loop at
// two rounds and land on the wrap-up round, even though the agent's own budget
// (50) would have allowed all three the model asked for.
func TestChat_SkillRoundCap_WrapsUp(t *testing.T) {
	h := skillRoundCapHarness(t, []skill.Skill{
		skilltest.NewWithRoundCap("capped", "Capped skill", []string{"command:capped"}, "Do the capped work.", 2),
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message":    "/capped go",
		"session_id": "skill-round-cap",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["response"], "Summary: did what fit in the budget.") {
		t.Errorf("response = %q, want the wrap-up summary", resp["response"])
	}
	if !strings.Contains(resp["response"], "turn ended early") {
		t.Errorf("response = %q, want the early-end marker", resp["response"])
	}
	if strings.Contains(resp["response"], "[Interrupted after") {
		t.Errorf("response = %q, want a wrap-up, not the interruption marker", resp["response"])
	}

	// Initial + 2 round completions + wrap-up.
	if h.MockLLM.CallCount() != 4 {
		t.Errorf("expected 4 LLM calls, got %d", h.MockLLM.CallCount())
	}

	// The model saw exactly 2 tool rounds, and the wrap-up carried no tools so
	// it could not have asked for a third.
	lastReq := h.MockLLM.LastRequest()
	if len(lastReq.Tools) != 0 {
		t.Errorf("wrap-up request carries %d tool definitions, want 0", len(lastReq.Tools))
	}
	var toolCallMsgs int
	for _, m := range lastReq.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			toolCallMsgs++
		}
	}
	if toolCallMsgs != 2 {
		t.Errorf("wrap-up request carries %d assistant tool_calls messages, want 2", toolCallMsgs)
	}

	// The budget hint the model read must name the skill and its numbers.
	var hint string
	for _, m := range lastReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "[engine:") {
			hint = m.Content
		}
	}
	if !strings.Contains(hint, "(skill cap: capped)") {
		t.Errorf("tool result hint = %q, want it to name the binding skill cap", hint)
	}
}

// Control: the same responses without a skill cap run every round the model
// asks for, proving the cap is skill-scoped rather than a global change.
func TestChat_NoSkillRoundCap_RunsAllRounds(t *testing.T) {
	h := skillRoundCapHarness(t, []skill.Skill{
		skilltest.New("capped", "Uncapped skill", []string{"command:capped"}, "Do the work."),
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message":    "/capped go",
		"session_id": "skill-round-cap-off",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["response"], "Summary: did what fit in the budget.") {
		t.Errorf("response = %q, want the final text", resp["response"])
	}
	if strings.Contains(resp["response"], "turn ended early") {
		t.Errorf("response = %q, want no early-end marker without a skill cap", resp["response"])
	}

	lastReq := h.MockLLM.LastRequest()
	var toolCallMsgs int
	for _, m := range lastReq.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			toolCallMsgs++
		}
	}
	if toolCallMsgs != 3 {
		t.Errorf("final request carries %d assistant tool_calls messages, want 3", toolCallMsgs)
	}
	for _, m := range lastReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "skill cap:") {
			t.Errorf("uncapped turn leaked a skill-cap hint: %q", m.Content)
		}
	}
}
