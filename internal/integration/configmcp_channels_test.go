//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
)

// configMCPChannelHarness creates a harness with Config MCP channel tools
// wired through the chat tool-call loop.
func configMCPChannelHarness(t *testing.T, responses []*llm.ChatResponse) *Harness {
	t.Helper()
	return NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: []string{"api"}},
		},
		Channels: []*agent.Channel{
			{Name: "work", AgentName: "default", Adapters: []string{"api"}},
			{Name: "personal", AgentName: "default", Adapters: []string{"api:user1"}},
		},
		WithConfigMCP: true,
		Responses:     responses,
	})
}

func TestConfigMCP_ChannelList(t *testing.T) {
	h := configMCPChannelHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "channel_list",
						Arguments: `{}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "Here are your channels: work and personal.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	req := h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "list my channels",
	})
	req.Header.Set("Accept", "text/event-stream")
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	if !strings.Contains(body, `"type":"tool_start"`) {
		t.Error("SSE stream missing tool_start event")
	}
	if !strings.Contains(body, `"type":"tool_end"`) {
		t.Error("SSE stream missing tool_end event")
	}
	if !strings.Contains(body, "Here are your channels") {
		t.Error("SSE stream missing final response text")
	}

	// Verify the LLM's second call received tool results containing channel names.
	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestConfigMCP_ChannelSwitch(t *testing.T) {
	h := configMCPChannelHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "channel_switch",
						Arguments: `{"adapter_key":"api:user1","channel_name":"work"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "Switched to work channel.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	req := h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "switch api:user1 to work channel",
	})
	req.Header.Set("Accept", "text/event-stream")
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Switched to work channel") {
		t.Error("SSE stream missing final response text")
	}

	// Verify the active channel was actually changed on the dispatcher.
	activeKeys := h.Dispatcher.ActiveChannelsForChannel("work")
	found := false
	for _, k := range activeKeys {
		if k == "api:user1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected api:user1 to be active on work channel, got %v", activeKeys)
	}
}

func TestConfigMCP_ChannelInfo(t *testing.T) {
	h := configMCPChannelHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "channel_info",
						Arguments: `{"channel_name":"work"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "The work channel uses conversation ID chan:work.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	req := h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "tell me about the work channel",
	})
	req.Header.Set("Accept", "text/event-stream")
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"tool_end"`) {
		t.Error("SSE stream missing tool_end event")
	}
	if !strings.Contains(body, "chan:work") {
		t.Error("SSE stream missing conversation ID in final response")
	}
}
