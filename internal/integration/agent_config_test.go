//go:build integration

package integration

import (
	"net/http"
	"testing"
)

func TestAgentConfig_UpdateSessionTier(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default", map[string]any{
		"session_tier": "autonomous",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if resp["status"] != "updated" {
		t.Errorf("status = %v, want updated", resp["status"])
	}

	// Verify via agent detail endpoint.
	detailRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/agents/default", nil))
	var detail map[string]any
	DecodeJSON(t, detailRec, &detail)
	if detail["permission_tier"] != "autonomous" {
		t.Errorf("permission_tier = %v, want autonomous", detail["permission_tier"])
	}
}

func TestAgentConfig_UpdateInvalidTier(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default", map[string]any{
		"session_tier": "invalid-tier",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAgentConfig_UpdateModel(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default", map[string]any{
		"llm_model": "gpt-4o",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestAgentConfig_UpdateDescription(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default", map[string]any{
		"description": "Updated agent description",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAgentConfig_RenameAgent(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
			{Name: "work", Tier: "autonomous", Adapters: []string{"discord"}},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/work", map[string]any{
		"name": "personal",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Old name should be gone.
	oldRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/agents/work", nil))
	if oldRec.Code != http.StatusNotFound {
		t.Errorf("old name status = %d, want %d", oldRec.Code, http.StatusNotFound)
	}

	// New name should exist.
	newRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/agents/personal", nil))
	if newRec.Code != http.StatusOK {
		t.Errorf("new name status = %d, want %d", newRec.Code, http.StatusOK)
	}
}

func TestAgentConfig_RenameDefault_Fails(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default", map[string]any{
		"name": "renamed",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAgentConfig_RenameToInvalidName(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
			{Name: "work", Tier: "autonomous", Adapters: []string{"discord"}},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/work", map[string]any{
		"name": "INVALID NAME!",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAgentConfig_RenameToDuplicate(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
			{Name: "work", Tier: "autonomous", Adapters: []string{"discord"}},
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/work", map[string]any{
		"name": "default",
	}))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestAgentConfig_NotFound(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/nonexistent", map[string]any{
		"session_tier": "autonomous",
	}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
