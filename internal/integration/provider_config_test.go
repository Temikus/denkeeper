//go:build integration

package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

// ---------------------------------------------------------------------------
// Provider create & delete (Phase 14d)
// ---------------------------------------------------------------------------

// providerCrudHarness creates a harness with ConfigPath and an initial
// provider so provider CRUD endpoints can persist to TOML.
func providerCrudHarness(t *testing.T) *Harness {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	initialConfig := `
[llm]
default_provider = "mock-existing"

[[llm.providers]]
name = "mock-existing"
type = "openai"
api_key = "sk-test"
`
	if err := os.WriteFile(cfgPath, []byte(initialConfig), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	h := NewHarness(t, &HarnessOpts{
		Agents:     []agentSetup{{Name: "default", Tier: "supervised"}},
		ConfigPath: cfgPath,
	})
	// Populate the in-memory provider list so the handler sees them.
	h.Config().LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "mock-existing", Type: "openai", APIKey: "sk-test"},
	}
	h.Config().LLM.DefaultProvider = "mock-existing"
	return h
}

func TestProviderCreate_Basic(t *testing.T) {
	h := providerCrudHarness(t)

	body := map[string]any{
		"name":     "my-ollama",
		"type":     "ollama",
		"base_url": "http://localhost:11434",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]string
	DecodeJSON(t, rec, &result)
	if result["name"] != "my-ollama" {
		t.Fatalf("expected name=my-ollama, got %v", result["name"])
	}

	// Verify it appears in the provider list.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/llm/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after create: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var listResp map[string]any
	DecodeJSON(t, rec, &listResp)
	providers := listResp["providers"].([]any)
	found := false
	for _, p := range providers {
		pm := p.(map[string]any)
		if pm["name"] == "my-ollama" {
			found = true
			if pm["type"] != "ollama" {
				t.Errorf("expected type=ollama, got %v", pm["type"])
			}
			break
		}
	}
	if !found {
		t.Error("my-ollama not found in provider list after create")
	}
}

func TestProviderCreate_DuplicateName(t *testing.T) {
	h := providerCrudHarness(t)

	body := map[string]any{"name": "mock-existing", "type": "openai"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProviderCreate_InvalidName(t *testing.T) {
	h := providerCrudHarness(t)

	body := map[string]any{"name": "INVALID NAME", "type": "openai"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid name, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProviderCreate_InvalidType(t *testing.T) {
	h := providerCrudHarness(t)

	body := map[string]any{"name": "bad-type", "type": "unknown"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid type, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProviderCreate_MissingName(t *testing.T) {
	h := providerCrudHarness(t)

	body := map[string]any{"type": "openai"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProviderCreate_PersistsToTOML(t *testing.T) {
	h := providerCrudHarness(t)

	body := map[string]any{
		"name": "toml-provider",
		"type": "anthropic",
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	content, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if !strings.Contains(string(content), "toml-provider") {
		t.Errorf("config file missing new provider; content:\n%s", content)
	}
}

func TestProviderDelete_Basic(t *testing.T) {
	h := providerCrudHarness(t)

	// Create a provider first (not the default one, so it can be deleted).
	body := map[string]any{"name": "delete-me", "type": "ollama"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Delete it.
	rec = h.Do(h.AuthedRequest("DELETE", "/api/v1/llm/providers/delete-me", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify it's gone from the list.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/llm/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after delete: expected 200, got %d", rec.Code)
	}
	var listResp map[string]any
	DecodeJSON(t, rec, &listResp)
	for _, p := range listResp["providers"].([]any) {
		pm := p.(map[string]any)
		if pm["name"] == "delete-me" {
			t.Error("delete-me should be gone from provider list")
		}
	}
}

func TestProviderDelete_NotFound(t *testing.T) {
	h := providerCrudHarness(t)

	rec := h.Do(h.AuthedRequest("DELETE", "/api/v1/llm/providers/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProviderDelete_InUseByDefaultProvider(t *testing.T) {
	h := providerCrudHarness(t)

	// mock-existing is the default_provider — cannot delete.
	rec := h.Do(h.AuthedRequest("DELETE", "/api/v1/llm/providers/mock-existing", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for default_provider reference, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["error"], "in use") {
		t.Errorf("error should mention provider is in use: %s", resp["error"])
	}
}

func TestProviderDelete_InUseByAgent(t *testing.T) {
	h := providerCrudHarness(t)

	// Create a standalone provider.
	body := map[string]any{"name": "agent-bound", "type": "anthropic"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create provider: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Simulate an agent referencing this provider.
	h.Config().Agents = append(h.Config().Agents, config.AgentInstanceConfig{
		Name:        "bound-agent",
		LLMProvider: "agent-bound",
	})

	// Attempt to delete — should be rejected.
	rec = h.Do(h.AuthedRequest("DELETE", "/api/v1/llm/providers/agent-bound", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for agent reference, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestProviderDelete_RemovedFromTOML(t *testing.T) {
	h := providerCrudHarness(t)

	// Create a provider.
	body := map[string]any{"name": "toml-delete", "type": "ollama"}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify it's in the TOML.
	content, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if !strings.Contains(string(content), "toml-delete") {
		t.Fatalf("provider should be in TOML after create; content:\n%s", content)
	}

	// Delete it.
	rec = h.Do(h.AuthedRequest("DELETE", "/api/v1/llm/providers/toml-delete", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify it's gone from the TOML.
	content, err = os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading config after delete: %v", err)
	}
	if strings.Contains(string(content), "toml-delete") {
		t.Errorf("provider should be removed from TOML after delete; content:\n%s", content)
	}
}

// ---------------------------------------------------------------------------
// CostTracker sync (bug fix)
// ---------------------------------------------------------------------------

func TestLLMConfig_PatchCostLimits_SyncsCostTracker(t *testing.T) {
	h := providerCrudHarness(t)

	// Verify initial default limits (harness sets Hard: 10.0).
	initial := h.CostTracker.DefaultLimits()
	if initial.Hard != 10.0 {
		t.Fatalf("initial hard limit = %f, want 10.0", initial.Hard)
	}

	// PATCH cost limits via API.
	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/config",
		map[string]any{"cost_limit_soft": 3.0, "cost_limit_hard": 7.0}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify CostTracker default limits were updated.
	updated := h.CostTracker.DefaultLimits()
	if updated.Soft != 3.0 {
		t.Errorf("soft limit = %f, want 3.0", updated.Soft)
	}
	if updated.Hard != 7.0 {
		t.Errorf("hard limit = %f, want 7.0", updated.Hard)
	}
}
