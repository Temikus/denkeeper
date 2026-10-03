//go:build integration

package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wizardHarness starts from a blank config file, the first-run state the web
// setup wizard is built for.
func wizardHarness(t *testing.T, live bool) *Harness {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "denkeeper.toml")
	if err := os.WriteFile(cfgPath, nil, 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return NewHarness(t, &HarnessOpts{
		Agents:           []agentSetup{{Name: "default", Tier: "supervised"}},
		ConfigPath:       cfgPath,
		WithAgentFactory: true,
		LiveProviders:    live,
	})
}

func TestWizard_CreateProviderThenAgent_ChatWithoutRestart(t *testing.T) {
	h := wizardHarness(t, true)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/providers", map[string]any{
		"name": "anthropic", "type": "anthropic", "api_key": "sk-ant-test",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create provider: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "restart_required") {
		t.Fatalf("provider create asked for a restart: %s", rec.Body)
	}

	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/agents", map[string]any{
		"name": "assistant", "llm_provider": "anthropic", "llm_model": "claude-sonnet-5-5", "session_tier": "supervised",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create agent: %d %s", rec.Code, rec.Body)
	}

	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"agent": "assistant", "message": "hello",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "Hello from mock!") {
		t.Errorf("chat body = %s, want the mock reply", rec.Body)
	}
}

func TestWizard_NoProviderRuntime_AgentCannotChat(t *testing.T) {
	// The bug this flow fixes: without a live runtime the provider exists only
	// on disk, so the new agent's first message fails until a restart.
	h := wizardHarness(t, false)
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/providers", map[string]any{
		"name": "anthropic", "type": "anthropic", "api_key": "sk-ant-test",
	}))
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/llm/providers", map[string]any{
		"name": "spare", "type": "ollama",
	}))
	if !strings.Contains(rec.Body.String(), `"restart_required":true`) {
		t.Errorf("create without runtime = %s, want restart_required true", rec.Body)
	}
}
