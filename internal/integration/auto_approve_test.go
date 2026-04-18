//go:build integration

package integration

import (
	"net/http"
	"testing"
)

func TestAutoApprove_ListEmpty(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/auto-approve", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var rules []map[string]any
	DecodeJSON(t, rec, &rules)
	if len(rules) != 0 {
		t.Errorf("rules count = %d, want 0", len(rules))
	}
}

func TestAutoApprove_CreatePermanentAndList(t *testing.T) {
	h := NewHarness(t, nil)

	// Create a permanent auto-approve rule.
	createRec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"agent": "default",
		"tool":  "web_search",
	}))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}

	var createResp map[string]any
	DecodeJSON(t, createRec, &createResp)
	if createResp["scope"] != "permanent" {
		t.Errorf("scope = %v, want permanent", createResp["scope"])
	}
	ruleID, ok := createResp["id"].(string)
	if !ok || ruleID == "" {
		t.Fatal("expected non-empty rule id")
	}

	// List — should include the rule.
	listRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/auto-approve", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRec.Code)
	}

	var rules []map[string]any
	DecodeJSON(t, listRec, &rules)
	if len(rules) != 1 {
		t.Fatalf("rules count = %d, want 1", len(rules))
	}
	if rules[0]["tool_name"] != "web_search" {
		t.Errorf("tool = %v, want web_search", rules[0]["tool_name"])
	}
}

func TestAutoApprove_CreateSessionRule(t *testing.T) {
	h := NewHarness(t, nil)

	createRec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"agent":           "default",
		"tool":            "browser_navigate",
		"scope":           "session",
		"conversation_id": "conv-42",
	}))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body: %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, createRec, &resp)
	if resp["scope"] != "session" {
		t.Errorf("scope = %v, want session", resp["scope"])
	}
	if resp["status"] != "created" {
		t.Errorf("status = %v, want created", resp["status"])
	}
}

func TestAutoApprove_SessionRuleMissingConversation(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"agent": "default",
		"tool":  "browser_navigate",
		"scope": "session",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAutoApprove_CreateMissingFields(t *testing.T) {
	h := NewHarness(t, nil)

	// Missing agent.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"tool": "web_search",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing agent: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	// Missing tool.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"agent": "default",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing tool: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAutoApprove_InvalidScope(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"agent": "default",
		"tool":  "web_search",
		"scope": "invalid",
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAutoApprove_DeleteExisting(t *testing.T) {
	h := NewHarness(t, nil)

	// Create a rule.
	createRec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"agent": "default",
		"tool":  "web_fetch",
	}))
	var createResp map[string]any
	DecodeJSON(t, createRec, &createResp)
	ruleID := createResp["id"].(string)

	// Delete it.
	delRec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/auto-approve/"+ruleID, nil))
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d; body: %s", delRec.Code, http.StatusOK, delRec.Body.String())
	}

	var delResp map[string]string
	DecodeJSON(t, delRec, &delResp)
	if delResp["status"] != "deleted" {
		t.Errorf("status = %v, want deleted", delResp["status"])
	}

	// List — should be empty.
	listRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/auto-approve", nil))
	var rules []map[string]any
	DecodeJSON(t, listRec, &rules)
	if len(rules) != 0 {
		t.Errorf("rules after delete = %d, want 0", len(rules))
	}
}

func TestAutoApprove_DeleteNotFound(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/auto-approve/nonexistent-id", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAutoApprove_FilterByAgent(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
			{Name: "work", Tier: "supervised", Adapters: []string{"discord"}},
		},
	})

	// Create rules for different agents.
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"agent": "default",
		"tool":  "web_search",
	}))
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/auto-approve", map[string]any{
		"agent": "work",
		"tool":  "code_review",
	}))

	// Filter by agent.
	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/auto-approve?agent=work", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var rules []map[string]any
	DecodeJSON(t, rec, &rules)
	if len(rules) != 1 {
		t.Fatalf("rules count = %d, want 1", len(rules))
	}
	if rules[0]["tool_name"] != "code_review" {
		t.Errorf("tool = %v, want code_review", rules[0]["tool_name"])
	}
}
