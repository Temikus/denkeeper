//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/llm/openrouter"
)

// deciderFlowHarness starts with a supervised agent and an openrouter
// provider created through the API, so the in-memory config and the TOML file
// agree before any decider is added.
func deciderFlowHarness(t *testing.T) (*Harness, string) {
	t.Helper()
	return deciderFlowHarnessWith(t, noopDecisionProvider{})
}

func deciderFlowHarnessWith(t *testing.T, dp llm.DecisionProvider) (*Harness, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	initial := `
[telegram]
token = "test"
allowed_users = [1]

[[agents]]
name = "default"
persona_dir = "` + filepath.Join(dir, "persona") + `"
adapters = ["telegram"]
session_tier = "supervised"
`
	if err := os.WriteFile(cfgPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	h := NewHarness(t, &HarnessOpts{
		Agents:        []agentSetup{{Name: "default", Tier: "supervised"}},
		ConfigPath:    cfgPath,
		LiveProviders: true,
		LiveDeciders:  &LiveDecidersOpts{ProviderNames: []string{"or"}, Provider: dp},
	})
	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/providers",
		map[string]any{"name": "or", "type": "openrouter", "api_key": "sk-or-test"})), http.StatusCreated)
	return h, cfgPath
}

func mustStatus(t *testing.T, rec interface {
	Result() *http.Response
}, want int) {
	t.Helper()
	if res := rec.Result(); res.StatusCode != want {
		var body map[string]any
		_ = json.NewDecoder(res.Body).Decode(&body)
		t.Fatalf("status = %d, want %d; body %v", res.StatusCode, want, body)
	}
}

func TestDeciderCreate_BindsToAgentWithoutRestart(t *testing.T) {
	h, cfgPath := deciderFlowHarness(t)

	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/deciders",
		map[string]any{"name": "jev", "provider": "or", "model": "typesafe/jev-1.13"})), http.StatusCreated)
	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default",
		map[string]any{"supervisor_decider": "jev"})), http.StatusOK)

	if d := h.Dispatcher.Agent("default").SupervisorDecider(); d == nil || d.Name() != "jev" {
		t.Fatalf("live decider = %v, want jev", d)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("the persisted config does not load: %v", err)
	}
	if len(cfg.LLM.Deciders) != 1 || cfg.Agents[0].SupervisorDecider != "jev" {
		t.Errorf("loaded deciders %+v, agent decider %q", cfg.LLM.Deciders, cfg.Agents[0].SupervisorDecider)
	}
}

func TestDeciderPatch_RebindsTheAgentToTheNewModel(t *testing.T) {
	h, _ := deciderFlowHarness(t)
	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/deciders",
		map[string]any{"name": "jev", "provider": "or", "model": "typesafe/jev-1.13"})), http.StatusCreated)
	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default",
		map[string]any{"supervisor_decider": "jev"})), http.StatusOK)

	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/llm/deciders/jev",
		map[string]any{"model": "typesafe/jev-2"})), http.StatusOK)

	if d := h.Dispatcher.Agent("default").SupervisorDecider(); d == nil || d.Model() != "typesafe/jev-2" {
		t.Errorf("live decider = %v, want jev on typesafe/jev-2", d)
	}
}

func TestDeciderDelete_InUse409ThenRemovable(t *testing.T) {
	h, cfgPath := deciderFlowHarness(t)
	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/deciders",
		map[string]any{"name": "jev", "provider": "or", "model": "typesafe/jev-1.13"})), http.StatusCreated)
	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default",
		map[string]any{"supervisor_decider": "jev"})), http.StatusOK)

	mustStatus(t, h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/llm/deciders/jev", nil)), http.StatusConflict)

	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/agents/default",
		map[string]any{"supervisor_decider": ""})), http.StatusOK)
	mustStatus(t, h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/llm/deciders/jev", nil)), http.StatusNoContent)

	if d := h.Dispatcher.Agent("default").SupervisorDecider(); d != nil {
		t.Errorf("decider %q still bound after delete", d.Name())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("the persisted config does not load: %v", err)
	}
	if len(cfg.LLM.Deciders) != 0 {
		t.Errorf("deciders = %+v, want none", cfg.LLM.Deciders)
	}
}

func TestDeciderTest_ReachesTheLiveProvider(t *testing.T) {
	h, _ := deciderFlowHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/deciders/test",
		map[string]any{"provider": "or", "model": "typesafe/jev-1.13"}))

	mustStatus(t, rec, http.StatusOK)
	var resp struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	// noopDecisionProvider answers no questions, so the decider reports the
	// missing answer: proof the call reached the provider.
	if resp.Status != "error" || !strings.Contains(resp.Message, "no answer") {
		t.Errorf("status = %q (%s), want the missing-answer error", resp.Status, resp.Message)
	}
}

// TestDeciderTest_Live creates a decider through the API and tests it against
// the real decisions API. Skipped without OPENROUTER_API_KEY.
func TestDeciderTest_Live(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	h, _ := deciderFlowHarnessWith(t, openrouter.New(key))
	mustStatus(t, h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/deciders",
		map[string]any{"name": "jev", "provider": "or", "model": "typesafe/jev-1.13"})), http.StatusCreated)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/deciders/test", map[string]any{"name": "jev"}))

	mustStatus(t, rec, http.StatusOK)
	var resp struct {
		Status    string `json:"status"`
		Message   string `json:"message"`
		LatencyMs int64  `json:"latency_ms"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Status != "ok" {
		t.Fatalf("status = %q (%s), want ok from the live decisions API", resp.Status, resp.Message)
	}
	t.Logf("live decider answered in %dms", resp.LatencyMs)
}
