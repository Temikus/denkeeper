//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/skill"
)

func TestMultiAgent_DefaultAgentUsed(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
			{Name: "work", Tier: "supervised", Adapters: []string{"discord"}},
		},
	})

	// Chat without specifying agent — should route to "default".
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "hello",
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["response"] != "Hello from mock!" {
		t.Errorf("response = %v", resp["response"])
	}
}

func TestMultiAgent_SpecificAgentRouting(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
			{Name: "work", Tier: "autonomous", Adapters: []string{"discord"}},
		},
	})

	// Chat with explicit agent name.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "hello",
		"agent":   "work",
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestMultiAgent_ListsAllAgents(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}, Description: "Main agent"},
			{Name: "work", Tier: "autonomous", Adapters: []string{"discord"}, Description: "Work agent"},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/agents", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var agents []map[string]any
	DecodeJSON(t, rec, &agents)
	if len(agents) != 2 {
		t.Fatalf("agents count = %d, want 2", len(agents))
	}

	names := map[string]bool{}
	for _, a := range agents {
		names[a["name"].(string)] = true
	}
	if !names["default"] || !names["work"] {
		t.Errorf("expected both default and work agents, got %v", names)
	}
}

func TestMultiAgent_AgentDetail(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{
				Name:     "default",
				Tier:     "autonomous",
				Adapters: []string{"telegram"},
				Skills: []skill.Skill{
					{Name: "test-skill", Description: "A test skill"},
				},
			},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/agents/default", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var detail map[string]any
	DecodeJSON(t, rec, &detail)
	if detail["name"] != "default" {
		t.Errorf("name = %v, want default", detail["name"])
	}
	if detail["permission_tier"] != "autonomous" {
		t.Errorf("permission_tier = %v, want autonomous", detail["permission_tier"])
	}
}

func TestMultiAgent_CostTracking(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{
			{Content: "response", TokensUsed: llm.TokenUsage{Prompt: 100, Completion: 50, Total: 150}, Model: "test-model", FinishReason: "stop"},
		},
	})

	// Make a chat request to generate some cost.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message":    "hello",
		"session_id": "cost-test-session",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d", rec.Code)
	}

	// Check costs endpoint.
	costRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/costs", nil))
	if costRec.Code != http.StatusOK {
		t.Fatalf("costs status = %d", costRec.Code)
	}

	var costs map[string]any
	DecodeJSON(t, costRec, &costs)
	if costs["session_count"] == nil {
		t.Error("expected session_count in costs response")
	}
}
