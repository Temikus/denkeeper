//go:build integration

package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	toml "github.com/pelletier/go-toml/v2"
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

func TestAgentConfig_RenameDefault_Succeeds(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default", map[string]any{
		"name": "renamed",
	}))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
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

func TestAgentConfig_UpdateMaxToolRounds(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default", map[string]any{
		"max_tool_rounds": 25,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Verify via agent detail endpoint.
	detailRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/agents/default", nil))
	var detail map[string]any
	DecodeJSON(t, detailRec, &detail)
	if int(detail["max_tool_rounds"].(float64)) != 25 {
		t.Errorf("max_tool_rounds = %v, want 25", detail["max_tool_rounds"])
	}
}

func TestAgentConfig_UpdateMaxToolRounds_Invalid(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default", map[string]any{
		"max_tool_rounds": 0,
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
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

// ---------------------------------------------------------------------------
// Agent create & delete (Phase 14c)
// ---------------------------------------------------------------------------

// agentCrudHarness creates a harness with ConfigPath, AgentFactory, and a
// DataDir so agent CRUD endpoints can build engines and persist to TOML.
func agentCrudHarness(t *testing.T) *Harness {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
		},
		ConfigPath:       cfgPath,
		WithAgentFactory: true,
	})
	// Set DataDir on the config so persona directories are created correctly.
	h.Config().DataDir = dir
	return h
}

func TestAgentCreate_Basic(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{
		"name":         "helper",
		"llm_provider": "mock",
		"llm_model":    "test-model",
		"session_tier": "autonomous",
		"description":  "A helper agent",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]string
	DecodeJSON(t, rec, &result)
	if result["name"] != "helper" {
		t.Fatalf("expected name=helper, got %v", result["name"])
	}

	// Verify the agent appears in the list.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/agents/helper", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get after create: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentCreate_DuplicateName(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{"name": "default"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentCreate_InvalidName(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{"name": "INVALID NAME"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid name, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentCreate_InvalidTier(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{
		"name":         "bad-tier",
		"session_tier": "invalid",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid tier, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentCreate_MissingName(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{"description": "no name"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentCreate_PersistsToTOML(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{
		"name":         "toml-agent",
		"session_tier": "autonomous",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	content, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if !strings.Contains(string(content), "toml-agent") {
		t.Errorf("config file missing new agent; content:\n%s", content)
	}
}

func TestAgentDelete_Basic(t *testing.T) {
	h := agentCrudHarness(t)

	// Create an agent first.
	body := map[string]any{"name": "delete-me"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Delete it.
	rec = h.Do(h.AuthedRequest("DELETE", "/api/v1/agents/delete-me", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify it's gone.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/agents/delete-me", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: expected 404, got %d", rec.Code)
	}
}

func TestAgentDelete_LastAgent(t *testing.T) {
	h := agentCrudHarness(t)

	rec := h.Do(h.AuthedRequest("DELETE", "/api/v1/agents/default", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for last agent, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["error"], "last agent") {
		t.Errorf("error should mention last agent: %s", resp["error"])
	}
}

func TestAgentDelete_NotFound(t *testing.T) {
	h := agentCrudHarness(t)

	rec := h.Do(h.AuthedRequest("DELETE", "/api/v1/agents/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentDelete_ChannelReference(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised", Adapters: []string{"telegram"}},
			{Name: "helper", Tier: "autonomous", Adapters: []string{"telegram"}},
		},
		Channels: []*agent.Channel{
			{Name: "work", AgentName: "helper", Adapters: []string{"telegram"}},
		},
		ConfigPath:       filepath.Join(t.TempDir(), "denkeeper.toml"),
		WithAgentFactory: true,
	})
	// Create the config file so endpoints don't 503.
	_ = os.WriteFile(h.ConfigPath(), []byte(""), 0o644)

	rec := h.Do(h.AuthedRequest("DELETE", "/api/v1/agents/helper", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for channel reference, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["error"], "work") {
		t.Errorf("error should mention blocking channel 'work': %s", resp["error"])
	}
}

func TestAgentDelete_PersonaPreserved(t *testing.T) {
	h := agentCrudHarness(t)

	// Create an agent (which creates persona dir).
	body := map[string]any{"name": "persona-test"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify persona dir was created.
	personaDir := filepath.Join(h.Config().DataDir, "agents", "persona-test")
	if _, err := os.Stat(personaDir); os.IsNotExist(err) {
		t.Fatalf("persona directory should exist after create")
	}

	// Delete the agent.
	rec = h.Do(h.AuthedRequest("DELETE", "/api/v1/agents/persona-test", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Persona dir should still exist (per PRD: does NOT delete persona files).
	if _, err := os.Stat(personaDir); os.IsNotExist(err) {
		t.Fatal("persona directory should be preserved after agent delete")
	}
}

func TestAgentCreate_SurvivesConfigReload(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{
		"name":         "reload-test",
		"llm_model":    "test-model",
		"session_tier": "autonomous",
		"description":  "Survives reload",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Read and parse the TOML file independently to verify the agent entry
	// would survive a restart (config.Parse finds it with correct fields).
	data, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}

	// Unmarshal just the [[agents]] array from the TOML to verify the entry
	// would survive a restart. We use raw TOML unmarshal (not config.Parse)
	// because the test config has no provider credentials for full validation.
	type agentEntry struct {
		Name        string `toml:"name"`
		LLMModel    string `toml:"llm_model"`
		SessionTier string `toml:"session_tier"`
		Description string `toml:"description"`
		PersonaDir  string `toml:"persona_dir"`
	}
	var raw struct {
		Agents []agentEntry `toml:"agents"`
	}
	if err = toml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshaling TOML: %v", err)
	}

	var found *agentEntry
	for i := range raw.Agents {
		if raw.Agents[i].Name == "reload-test" {
			found = &raw.Agents[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("agent 'reload-test' not found in parsed config; TOML content:\n%s", data)
	}
	if found.LLMModel != "test-model" {
		t.Errorf("model = %q, want 'test-model'", found.LLMModel)
	}
	if found.SessionTier != "autonomous" {
		t.Errorf("tier = %q, want 'autonomous'", found.SessionTier)
	}
	if found.Description != "Survives reload" {
		t.Errorf("description = %q, want 'Survives reload'", found.Description)
	}
	if found.PersonaDir == "" {
		t.Error("persona_dir should be set in persisted config")
	}
}

// ---------------------------------------------------------------------------
// Per-agent cost limits (Phase 14e)
// ---------------------------------------------------------------------------

func TestAgentConfig_UpdateCostLimits(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{{Name: "default", Tier: "supervised"}},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default",
		map[string]any{"cost_limit_soft": 2.5, "cost_limit_hard": 5.0}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify via agent detail endpoint.
	detailRec := h.Do(h.AuthedRequest("GET", "/api/v1/agents/default", nil))
	var detail map[string]any
	DecodeJSON(t, detailRec, &detail)
	if detail["cost_limit_soft"] != 2.5 {
		t.Errorf("cost_limit_soft = %v, want 2.5", detail["cost_limit_soft"])
	}
	if detail["cost_limit_hard"] != 5.0 {
		t.Errorf("cost_limit_hard = %v, want 5.0", detail["cost_limit_hard"])
	}
}

func TestAgentConfig_UpdateCostLimits_Negative(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{{Name: "default", Tier: "supervised"}},
	})

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default",
		map[string]any{"cost_limit_soft": -1.0}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentConfig_UpdateCostLimits_PersistsToTOML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	initialConfig := `
[[agents]]
name = "default"
session_tier = "supervised"
`
	if err := os.WriteFile(cfgPath, []byte(initialConfig), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	h := NewHarness(t, &HarnessOpts{
		Agents:     []agentSetup{{Name: "default", Tier: "supervised"}},
		ConfigPath: cfgPath,
	})

	rec := h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default",
		map[string]any{"cost_limit_soft": 1.5, "cost_limit_hard": 3.0}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}

	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if !strings.Contains(string(content), "cost_limit_soft") {
		t.Errorf("cost_limit_soft not found in TOML:\n%s", content)
	}
	if !strings.Contains(string(content), "cost_limit_hard") {
		t.Errorf("cost_limit_hard not found in TOML:\n%s", content)
	}
}

// ---------------------------------------------------------------------------
// Companion supervisor creation (wizard flow)
// ---------------------------------------------------------------------------

func TestAgentCreate_WithCompanionSupervisor(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{
		"name":         "worker",
		"llm_provider": "mock",
		"llm_model":    "test-model",
		"session_tier": "supervised",
		"create_supervisor": map[string]any{
			"name":             "overseer",
			"llm_model":        "test-model",
			"timeout":          "45s",
			"context_messages": 3,
		},
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]string
	DecodeJSON(t, rec, &result)
	if result["name"] != "worker" {
		t.Errorf("name = %q, want worker", result["name"])
	}
	if result["supervisor"] != "overseer" {
		t.Errorf("supervisor = %q, want overseer", result["supervisor"])
	}

	// Both agents should exist.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/agents/worker", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("worker not found: %d", rec.Code)
	}
	var workerDetail map[string]any
	DecodeJSON(t, rec, &workerDetail)
	if workerDetail["supervisor"] != "overseer" {
		t.Errorf("worker.supervisor = %v, want overseer", workerDetail["supervisor"])
	}

	rec = h.Do(h.AuthedRequest("GET", "/api/v1/agents/overseer", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("overseer not found: %d", rec.Code)
	}
	var supDetail map[string]any
	DecodeJSON(t, rec, &supDetail)
	if supDetail["permission_tier"] != "autonomous" {
		t.Errorf("overseer tier = %v, want autonomous", supDetail["permission_tier"])
	}
}

func TestAgentCreate_WithCompanionSupervisor_DefaultName(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{
		"name":              "bot",
		"session_tier":      "supervised",
		"create_supervisor": map[string]any{},
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]string
	DecodeJSON(t, rec, &result)
	if result["supervisor"] != "supervisor" {
		t.Errorf("supervisor = %q, want supervisor", result["supervisor"])
	}
}

func TestAgentCreate_WithCompanionSupervisor_ConflictingName(t *testing.T) {
	h := agentCrudHarness(t)

	// First create an agent that will conflict with the supervisor name.
	body := map[string]any{"name": "overseer"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Now try to create an agent with a companion supervisor named "overseer".
	// The main agent is created first, then companion creation fails and rolls back.
	body = map[string]any{
		"name":         "worker",
		"session_tier": "supervised",
		"create_supervisor": map[string]any{
			"name": "overseer",
		},
	}
	rec = h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for conflicting supervisor name, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["error"], "already exists") {
		t.Errorf("error should mention already exists: %s", resp["error"])
	}

	// Main agent should have been rolled back.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/agents/worker", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("worker should be rolled back, got %d", rec.Code)
	}
}

func TestAgentCreate_WithCompanionSupervisor_RollbackOnlyAgent(t *testing.T) {
	// Start with NO pre-existing agents except the one we create. This exercises
	// the edge case where rollbackAgent is called on the only agent in the
	// dispatcher (the last-agent guard). The rollback should still clean up
	// config and in-memory state even if dispatcher removal is blocked.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "existing", Tier: "supervised", Adapters: []string{"telegram"}},
		},
		ConfigPath:       cfgPath,
		WithAgentFactory: true,
	})
	h.Config().DataDir = dir

	// Create "existing-supervisor" first so it conflicts.
	body := map[string]any{"name": "existing-supervisor"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Now create a new supervised agent whose companion supervisor conflicts.
	body = map[string]any{
		"name":         "new-agent",
		"session_tier": "supervised",
		"create_supervisor": map[string]any{
			"name": "existing-supervisor",
		},
	}
	rec = h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// The main agent should be rolled back from both runtime and config.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/agents/new-agent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("new-agent should be rolled back, got %d", rec.Code)
	}

	// Config file should not contain the rolled-back agent.
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if strings.Contains(string(content), "new-agent") {
		t.Errorf("config should not contain rolled-back agent; content:\n%s", content)
	}
}

func TestAgentCreate_WithCompanionSupervisor_WrongTier_Ignored(t *testing.T) {
	h := agentCrudHarness(t)

	// When tier is not "supervised", create_supervisor is silently ignored.
	body := map[string]any{
		"name":              "worker",
		"session_tier":      "autonomous",
		"create_supervisor": map[string]any{"name": "sup"},
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]string
	DecodeJSON(t, rec, &result)
	if _, hasSup := result["supervisor"]; hasSup {
		t.Errorf("expected no supervisor field for non-supervised agent, got %q", result["supervisor"])
	}

	// Supervisor agent should not exist.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/agents/sup", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("supervisor should not exist, got %d", rec.Code)
	}
}

func TestAgentCreate_WithCompanionSupervisor_PersistsToTOML(t *testing.T) {
	h := agentCrudHarness(t)

	body := map[string]any{
		"name":         "toml-worker",
		"session_tier": "supervised",
		"create_supervisor": map[string]any{
			"name": "toml-sup",
		},
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/agents", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	content, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "toml-worker") {
		t.Errorf("config missing worker agent:\n%s", s)
	}
	if !strings.Contains(s, "toml-sup") {
		t.Errorf("config missing supervisor agent:\n%s", s)
	}
}
