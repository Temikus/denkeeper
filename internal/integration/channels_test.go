//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
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

func TestChannels_ScopeEnforcement(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"chat"}, // no channels:read scope
	})

	rec := h.Do(h.AuthedRequest("GET", "/api/v1/channels", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without channels:read scope, got %d", rec.Code)
	}
}
