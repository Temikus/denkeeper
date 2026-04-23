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

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

func channelHarness(t *testing.T, channels []*agent.Channel) *Harness {
	t.Helper()
	return NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "work-agent", Tier: "autonomous", Adapters: []string{"telegram"}},
			{Name: "personal-agent", Tier: "autonomous", Adapters: []string{"telegram"}},
		},
		Channels: channels,
	})
}

func TestChannels_ListEmpty(t *testing.T) {
	h := NewHarness(t, nil)
	rec := h.Do(h.AuthedRequest("GET", "/api/v1/channels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result []map[string]any
	DecodeJSON(t, rec, &result)
	if len(result) != 0 {
		t.Fatalf("expected empty list, got %d items", len(result))
	}
}

func TestChannels_ListWithChannels(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "work", AgentName: "work-agent", Adapters: []string{"telegram"}},
		{Name: "personal", AgentName: "personal-agent", Adapters: []string{"discord"}},
	})

	rec := h.Do(h.AuthedRequest("GET", "/api/v1/channels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result []map[string]any
	DecodeJSON(t, rec, &result)
	if len(result) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(result))
	}

	// Verify both channels are present by name.
	names := map[string]bool{}
	for _, ch := range result {
		names[ch["name"].(string)] = true
	}
	if !names["work"] || !names["personal"] {
		t.Fatalf("expected work and personal channels, got %v", names)
	}
}

func TestChannels_GetByName(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "work", AgentName: "work-agent", Adapters: []string{"telegram"}},
	})

	rec := h.Do(h.AuthedRequest("GET", "/api/v1/channels/work", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	DecodeJSON(t, rec, &result)
	if result["name"] != "work" {
		t.Fatalf("expected name=work, got %v", result["name"])
	}
	if result["agent"] != "work-agent" {
		t.Fatalf("expected agent=work-agent, got %v", result["agent"])
	}
	if result["conversation_id"] != "chan:work" {
		t.Fatalf("expected conversation_id=chan:work, got %v", result["conversation_id"])
	}
}

func TestChannels_GetNotFound(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "work", AgentName: "work-agent", Adapters: []string{"telegram"}},
	})

	rec := h.Do(h.AuthedRequest("GET", "/api/v1/channels/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestChannels_ActivateAndDeactivate(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "work", AgentName: "work-agent", Adapters: []string{"telegram"}},
	})

	// Activate.
	body := map[string]string{"adapter_key": "telegram:12345"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/channels/work/activate", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("activate: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var activateResult map[string]string
	DecodeJSON(t, rec, &activateResult)
	if activateResult["status"] != "activated" {
		t.Fatalf("expected status=activated, got %v", activateResult["status"])
	}

	// Verify the channel now shows the active adapter key.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/channels/work", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get after activate: expected 200, got %d", rec.Code)
	}
	var detail map[string]any
	DecodeJSON(t, rec, &detail)
	keys := detail["active_adapter_keys"].([]any)
	if len(keys) != 1 || keys[0] != "telegram:12345" {
		t.Fatalf("expected [telegram:12345], got %v", keys)
	}

	// Deactivate.
	rec = h.Do(h.AuthedRequest("DELETE", "/api/v1/channels/work/activate", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify cleared.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/channels/work", nil))
	DecodeJSON(t, rec, &detail)
	keys = detail["active_adapter_keys"].([]any)
	if len(keys) != 0 {
		t.Fatalf("expected empty active keys after deactivate, got %v", keys)
	}
}

func TestChannels_ActivateUnknownChannel(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "work", AgentName: "work-agent", Adapters: []string{"telegram"}},
	})

	body := map[string]string{"adapter_key": "telegram:12345"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/channels/nonexistent/activate", body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChannels_DeactivateWrongChannel(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "work", AgentName: "work-agent", Adapters: []string{"telegram"}},
		{Name: "personal", AgentName: "personal-agent", Adapters: []string{"discord"}},
	})

	// Activate on "work".
	body := map[string]string{"adapter_key": "telegram:12345"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/channels/work/activate", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("activate: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Try to deactivate from "personal" — should fail with 409 since the key
	// is active on "work", not "personal".
	rec = h.Do(h.AuthedRequest("DELETE", "/api/v1/channels/personal/activate", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("deactivate wrong channel: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the key is still active on "work".
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/channels/work", nil))
	var detail map[string]any
	DecodeJSON(t, rec, &detail)
	keys := detail["active_adapter_keys"].([]any)
	if len(keys) != 1 || keys[0] != "telegram:12345" {
		t.Fatalf("expected key still active on work, got %v", keys)
	}
}

func TestChannels_DeactivateNotActive(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "work", AgentName: "work-agent", Adapters: []string{"telegram"}},
	})

	// Try to deactivate a key that was never activated — should return 409.
	body := map[string]string{"adapter_key": "telegram:99999"}
	rec := h.Do(h.AuthedRequest("DELETE", "/api/v1/channels/work/activate", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("deactivate not active: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChannels_ChatWithChannel(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "work", AgentName: "work-agent", Adapters: []string{"telegram"}},
	})

	// Send a chat message routed through the "work" channel.
	chatBody := map[string]string{
		"message": "hello via channel",
		"channel": "work",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", chatBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if chatResp["response"] == "" {
		t.Fatal("expected non-empty response")
	}

	// Verify the conversation was stored under the channel's conversation ID.
	ctx := context.Background()
	convos, err := h.Memory.ListConversations(ctx)
	if err != nil {
		t.Fatalf("listing conversations: %v", err)
	}
	found := false
	for _, c := range convos {
		if c.ID == "chan:work" {
			found = true
			break
		}
	}
	if !found {
		ids := make([]string, len(convos))
		for i, c := range convos {
			ids[i] = c.ID
		}
		t.Fatalf("expected conversation with ID 'chan:work', found: %v", ids)
	}
}

func TestChannels_ChatEphemeralChannel(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "scratch", AgentName: "work-agent", Adapters: []string{"telegram"}, SessionMode: "ephemeral"},
	})

	// Send two chat messages through the ephemeral channel.
	chatBody := map[string]string{
		"message": "first ephemeral message",
		"channel": "scratch",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", chatBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("first chat: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	chatBody["message"] = "second ephemeral message"
	rec = h.Do(h.AuthedRequest("POST", "/api/v1/chat", chatBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("second chat: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify two distinct conversations were created, neither equal to the
	// persistent "chan:scratch".
	ctx := context.Background()
	convos, err := h.Memory.ListConversations(ctx)
	if err != nil {
		t.Fatalf("listing conversations: %v", err)
	}
	if len(convos) != 2 {
		ids := make([]string, len(convos))
		for i, c := range convos {
			ids[i] = c.ID
		}
		t.Fatalf("expected 2 ephemeral conversations, got %d: %v", len(convos), ids)
	}

	for _, c := range convos {
		if c.ID == "chan:scratch" {
			t.Fatal("ephemeral channel should not create persistent conversation ID 'chan:scratch'")
		}
		if !strings.HasPrefix(c.ID, "chan:scratch:") {
			t.Fatalf("expected ephemeral conversation ID starting with 'chan:scratch:', got %q", c.ID)
		}
	}

	if convos[0].ID == convos[1].ID {
		t.Fatalf("expected two distinct conversation IDs, both are %q", convos[0].ID)
	}
}

func TestChannels_SessionModeInResponse(t *testing.T) {
	h := channelHarness(t, []*agent.Channel{
		{Name: "persistent-ch", AgentName: "work-agent", Adapters: []string{"telegram"}},
		{Name: "ephemeral-ch", AgentName: "work-agent", Adapters: []string{"telegram"}, SessionMode: "ephemeral"},
	})

	// List: verify session_mode appears on ephemeral channel.
	rec := h.Do(h.AuthedRequest("GET", "/api/v1/channels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var list []map[string]any
	DecodeJSON(t, rec, &list)

	for _, ch := range list {
		name := ch["name"].(string)
		if name == "ephemeral-ch" {
			if ch["session_mode"] != "ephemeral" {
				t.Fatalf("expected session_mode=ephemeral for ephemeral-ch, got %v", ch["session_mode"])
			}
		}
		if name == "persistent-ch" {
			// Persistent channels with empty SessionMode should omit the field (omitempty).
			if mode, ok := ch["session_mode"]; ok && mode != "" {
				t.Fatalf("expected no session_mode for persistent-ch, got %v", mode)
			}
		}
	}

	// Get by name: verify session_mode on ephemeral channel.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/channels/ephemeral-ch", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var detail map[string]any
	DecodeJSON(t, rec, &detail)
	if detail["session_mode"] != "ephemeral" {
		t.Fatalf("expected session_mode=ephemeral, got %v", detail["session_mode"])
	}
}

func TestChannels_ScopeEnforcement(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"chat"}, // no channels:read scope
	})

	rec := h.Do(h.AuthedRequest("GET", "/api/v1/channels", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without channels:read scope, got %d", rec.Code)
	}
}

// supervisedChannelHarness creates a harness with a supervised agent, channel
// routing, and the test MCP echo tool wired into the engine.
func supervisedChannelHarness(t *testing.T, responses []*llm.ChatResponse) *Harness {
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
			{Name: "supervised-agent", Tier: "supervised", Adapters: []string{"telegram"}},
		},
		Channels: []*agent.Channel{
			{Name: "review", AgentName: "supervised-agent", Adapters: []string{"telegram"}},
		},
		ToolManager: toolMgr,
		Responses:   responses,
	})
}

func TestChannels_SupervisedApprovalFlow(t *testing.T) {
	h := supervisedChannelHarness(t, []*llm.ChatResponse{
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
	})

	// Launch goroutine that polls for pending approvals and approves them.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() {
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
				if id != "" {
					h.Do(h.AuthedRequest("POST", "/api/v1/approvals/"+id+"/approve", nil))
				}
			}
			if len(pending) > 0 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	// Send chat through the channel — blocks until approval + tool execution.
	chatBody := map[string]string{
		"message": "please call echo",
		"channel": "review",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", chatBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if !strings.Contains(chatResp["response"], "supervised-test") {
		t.Fatalf("expected response to contain 'supervised-test', got: %s", chatResp["response"])
	}

	// Verify conversation stored under channel's conversation ID.
	convos, err := h.Memory.ListConversations(ctx)
	if err != nil {
		t.Fatalf("listing conversations: %v", err)
	}
	found := false
	for _, c := range convos {
		if c.ID == "chan:review" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected conversation with ID 'chan:review'")
	}

	// Verify the mock LLM was called twice (initial + after tool result).
	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestChannels_SupervisedDenialFlow(t *testing.T) {
	h := supervisedChannelHarness(t, []*llm.ChatResponse{
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

	// Launch goroutine that polls for pending approvals and denies them.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() {
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
				if id != "" {
					h.Do(h.AuthedRequest("POST", "/api/v1/approvals/"+id+"/deny", nil))
				}
			}
			if len(pending) > 0 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	// Send chat through the channel — blocks until denial + LLM fallback.
	chatBody := map[string]string{
		"message": "please call echo",
		"channel": "review",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", chatBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if chatResp["response"] == "" {
		t.Fatal("expected non-empty response after tool denial")
	}

	// Verify conversation stored under channel's conversation ID.
	convos, err := h.Memory.ListConversations(ctx)
	if err != nil {
		t.Fatalf("listing conversations: %v", err)
	}
	found := false
	for _, c := range convos {
		if c.ID == "chan:review" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected conversation with ID 'chan:review'")
	}

	// Verify the mock LLM was called twice (initial + after denial message).
	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}
