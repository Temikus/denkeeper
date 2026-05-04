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

// ---------------------------------------------------------------------------
// Per-provider cost fields (Phase 4)
// ---------------------------------------------------------------------------

func TestProviderPatch_CostFields_RoundTrip(t *testing.T) {
	h := providerCrudHarness(t)

	// PATCH cost fields onto the existing provider.
	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{
			"cost_limit_soft":            5.0,
			"cost_limit_hard":            10.0,
			"default_rate_per_1k_tokens": 0.02,
			"model_prices": map[string]any{
				"gpt-4o": map[string]any{
					"input": 2.5, "output": 10.0, "cached_input": 1.25,
				},
			},
		}))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", rec.Code, rec.Body.String())
	}

	// GET and verify round-trip.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/llm/providers", nil))
	var body map[string]any
	DecodeJSON(t, rec, &body)

	providers := body["providers"].([]any)
	p := providers[0].(map[string]any)
	if p["cost_limit_soft"] != 5.0 {
		t.Errorf("cost_limit_soft = %v, want 5.0", p["cost_limit_soft"])
	}
	if p["cost_limit_hard"] != 10.0 {
		t.Errorf("cost_limit_hard = %v, want 10.0", p["cost_limit_hard"])
	}
	if p["default_rate_per_1k_tokens"] != 0.02 {
		t.Errorf("default_rate_per_1k_tokens = %v, want 0.02", p["default_rate_per_1k_tokens"])
	}
	mp, ok := p["model_prices"].(map[string]any)
	if !ok {
		t.Fatalf("model_prices missing or wrong type: %v", p["model_prices"])
	}
	gpt4o, ok := mp["gpt-4o"].(map[string]any)
	if !ok {
		t.Fatalf("model_prices[gpt-4o] missing: %v", mp)
	}
	if gpt4o["input"] != 2.5 {
		t.Errorf("gpt-4o input = %v, want 2.5", gpt4o["input"])
	}
}

func TestProviderPatch_CostFields_SyncsCostTracker(t *testing.T) {
	h := providerCrudHarness(t)

	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"cost_limit_soft": 2.0, "cost_limit_hard": 8.0}))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", rec.Code, rec.Body.String())
	}

	limits := h.CostTracker.ProviderLimits("mock-existing")
	if limits.Soft != 2.0 {
		t.Errorf("provider soft limit = %f, want 2.0", limits.Soft)
	}
	if limits.Hard != 8.0 {
		t.Errorf("provider hard limit = %f, want 8.0", limits.Hard)
	}
}

func TestProviderPatch_NegativeCostFields_Rejected(t *testing.T) {
	h := providerCrudHarness(t)

	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"cost_limit_soft": -1.0}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative cost_limit_soft, got %d", rec.Code)
	}

	rec = h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"cost_limit_hard": -1.0}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative cost_limit_hard, got %d", rec.Code)
	}

	rec = h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"default_rate_per_1k_tokens": -0.01}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative default_rate_per_1k_tokens, got %d", rec.Code)
	}
}

func TestProviderPatch_ExplicitNull_ClearsLimit(t *testing.T) {
	h := providerCrudHarness(t)

	// Set a cost limit first.
	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"cost_limit_soft": 5.0, "cost_limit_hard": 10.0}))
	if rec.Code != http.StatusOK {
		t.Fatalf("set: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Clear by sending explicit null (nil in Go map serializes as JSON null).
	rec = h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"cost_limit_soft": nil, "cost_limit_hard": nil}))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// GET should omit the fields (explicit null clears to nil pointer).
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/llm/providers", nil))
	var body map[string]any
	DecodeJSON(t, rec, &body)

	providers := body["providers"].([]any)
	p := providers[0].(map[string]any)
	if _, exists := p["cost_limit_soft"]; exists {
		t.Errorf("cost_limit_soft should be omitted after clearing, got %v", p["cost_limit_soft"])
	}
	if _, exists := p["cost_limit_hard"]; exists {
		t.Errorf("cost_limit_hard should be omitted after clearing, got %v", p["cost_limit_hard"])
	}
}

func TestProviderPatch_ClearCostFields_SurvivesReload(t *testing.T) {
	h := providerCrudHarness(t)

	// Set cost limits.
	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"cost_limit_soft": 5.0, "cost_limit_hard": 10.0}))
	if rec.Code != http.StatusOK {
		t.Fatalf("set: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Clear by sending explicit null.
	rec = h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"cost_limit_soft": nil, "cost_limit_hard": nil}))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Reload config from TOML (simulates restart).
	cfg, err := config.Load(h.ConfigPath())
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	for _, pc := range cfg.LLM.Providers {
		if pc.Name == "mock-existing" {
			if pc.CostLimitSoft != nil {
				t.Errorf("cost_limit_soft should be nil after reload, got %v", *pc.CostLimitSoft)
			}
			if pc.CostLimitHard != nil {
				t.Errorf("cost_limit_hard should be nil after reload, got %v", *pc.CostLimitHard)
			}
			return
		}
	}
	t.Fatal("mock-existing not found in reloaded config")
}

func TestProviderCreate_WithCostFields(t *testing.T) {
	h := providerCrudHarness(t)

	body := map[string]any{
		"name":            "cost-provider",
		"type":            "ollama",
		"base_url":        "http://localhost:11434",
		"cost_limit_soft": 1.0,
		"cost_limit_hard": 5.0,
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify in-memory config has cost fields.
	rec = h.Do(h.AuthedRequest("GET", "/api/v1/llm/providers", nil))
	var resp map[string]any
	DecodeJSON(t, rec, &resp)

	providers := resp["providers"].([]any)
	var found bool
	for _, p := range providers {
		pm := p.(map[string]any)
		if pm["name"] == "cost-provider" {
			if pm["cost_limit_soft"] != 1.0 {
				t.Errorf("cost_limit_soft = %v, want 1.0", pm["cost_limit_soft"])
			}
			if pm["cost_limit_hard"] != 5.0 {
				t.Errorf("cost_limit_hard = %v, want 5.0", pm["cost_limit_hard"])
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("cost-provider not found in GET response")
	}

	// Verify CostTracker was synced.
	limits := h.CostTracker.ProviderLimits("cost-provider")
	if limits.Soft != 1.0 {
		t.Errorf("CostTracker soft = %f, want 1.0", limits.Soft)
	}
	if limits.Hard != 5.0 {
		t.Errorf("CostTracker hard = %f, want 5.0", limits.Hard)
	}
}

func TestProviderCreate_CostFields_PersistToTOML(t *testing.T) {
	h := providerCrudHarness(t)

	body := map[string]any{
		"name":                       "cost-toml",
		"type":                       "ollama",
		"base_url":                   "http://localhost:11434",
		"cost_limit_soft":            2.5,
		"cost_limit_hard":            7.0,
		"default_rate_per_1k_tokens": 0.05,
		"model_prices": map[string]any{
			"llama3": map[string]any{
				"input": 1.0, "output": 3.0, "cached_input": 0.5,
			},
		},
	}
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/llm/providers", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	content, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	tomlStr := string(content)

	if !strings.Contains(tomlStr, "cost-toml") {
		t.Fatalf("provider not in TOML; content:\n%s", tomlStr)
	}
	if !strings.Contains(tomlStr, "cost_limit_soft") {
		t.Errorf("cost_limit_soft missing from TOML; content:\n%s", tomlStr)
	}
	if !strings.Contains(tomlStr, "cost_limit_hard") {
		t.Errorf("cost_limit_hard missing from TOML; content:\n%s", tomlStr)
	}
	if !strings.Contains(tomlStr, "default_rate_per_1k_tokens") {
		t.Errorf("default_rate_per_1k_tokens missing from TOML; content:\n%s", tomlStr)
	}
	if !strings.Contains(tomlStr, "llama3") {
		t.Errorf("model_prices.llama3 missing from TOML; content:\n%s", tomlStr)
	}

	// Verify the TOML parses correctly and values survive the round-trip.
	cfg, err := config.Load(h.ConfigPath())
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	var found bool
	for _, pc := range cfg.LLM.Providers {
		if pc.Name == "cost-toml" {
			found = true
			if pc.CostLimitSoft == nil || *pc.CostLimitSoft != 2.5 {
				t.Errorf("parsed cost_limit_soft = %v, want 2.5", pc.CostLimitSoft)
			}
			if pc.CostLimitHard == nil || *pc.CostLimitHard != 7.0 {
				t.Errorf("parsed cost_limit_hard = %v, want 7.0", pc.CostLimitHard)
			}
			if pc.DefaultRatePerKTokens == nil || *pc.DefaultRatePerKTokens != 0.05 {
				t.Errorf("parsed default_rate_per_1k_tokens = %v, want 0.05", pc.DefaultRatePerKTokens)
			}
			if pc.ModelPrices == nil {
				t.Fatal("parsed model_prices is nil")
			}
			if mp, ok := pc.ModelPrices["llama3"]; !ok {
				t.Error("model_prices[llama3] missing after reload")
			} else {
				if mp.InputPerMTok != 1.0 {
					t.Errorf("llama3 input = %f, want 1.0", mp.InputPerMTok)
				}
				if mp.OutputPerMTok != 3.0 {
					t.Errorf("llama3 output = %f, want 3.0", mp.OutputPerMTok)
				}
				if mp.CachedInputPerMTok != 0.5 {
					t.Errorf("llama3 cached_input = %f, want 0.5", mp.CachedInputPerMTok)
				}
			}
			break
		}
	}
	if !found {
		t.Error("cost-toml provider not found after config.Load")
	}
}

func TestCosts_PerProviderArray(t *testing.T) {
	h := providerCrudHarness(t)

	rec := h.Do(h.AuthedRequest("GET", "/api/v1/costs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	DecodeJSON(t, rec, &body)

	// per_provider key must exist (may be empty array with no messages).
	pp, ok := body["per_provider"]
	if !ok {
		t.Fatal("per_provider key missing from /api/v1/costs response")
	}
	arr, ok := pp.([]any)
	if !ok {
		t.Fatalf("per_provider is not an array: %T", pp)
	}
	// No messages seeded, so should be empty.
	if len(arr) != 0 {
		t.Errorf("per_provider should be empty with no messages, got %d entries", len(arr))
	}
}
