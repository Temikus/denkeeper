//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Step detection tests — verify each onboarding milestone independently
// ---------------------------------------------------------------------------

func TestOnboarding_StepDetection_AuthViaPassword(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		PasswordHash: bcryptHashFor(t, "test-pass"),
	})

	resp := getOnboarding(t, h)
	stepDone := stepMap(resp.Steps)

	if !stepDone["auth"] {
		t.Error("auth step should be done when password hash is set")
	}
}

func TestOnboarding_StepDetection_AuthViaAPIKey(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{WithKeyStore: true})

	// Before creating a key — auth should be incomplete.
	resp := getOnboarding(t, h)
	if stepMap(resp.Steps)["auth"] {
		t.Error("auth step should be incomplete before any key exists")
	}

	// Create an API key via the REST endpoint.
	createTestKey(t, h, "onboarding-key", []string{"admin"})

	// Now auth should be detected as done.
	resp = getOnboarding(t, h)
	if !stepMap(resp.Steps)["auth"] {
		t.Error("auth step should be done after creating an API key")
	}
}

func TestOnboarding_StepDetection_AgentConfigured(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "my-agent", Tier: "autonomous"},
		},
	})

	resp := getOnboarding(t, h)
	if !stepMap(resp.Steps)["agent"] {
		t.Error("agent step should be done when at least one agent is configured")
	}
}

func TestOnboarding_StepDetection_AgentMissing(t *testing.T) {
	// Use an explicit agent setup but clear the config's Agents slice before
	// querying. Safe because the onboarding handler reads deps.Config.Agents
	// at request time — the dispatcher/engines are already wired and unaffected.
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{{Name: "placeholder", Tier: "autonomous"}},
	})
	h.Config().Agents = nil

	resp := getOnboarding(t, h)
	if stepMap(resp.Steps)["agent"] {
		t.Error("agent step should be incomplete when no agents configured")
	}
}

func TestOnboarding_StepDetection_AdapterConnected(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: []string{"telegram"}},
		},
	})

	resp := getOnboarding(t, h)
	if !stepMap(resp.Steps)["adapter"] {
		t.Error("adapter step should be done when an agent has adapters")
	}
}

func TestOnboarding_StepDetection_AdapterMissing(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: nil},
		},
	})

	resp := getOnboarding(t, h)
	if stepMap(resp.Steps)["adapter"] {
		t.Error("adapter step should be incomplete when no agent has adapters")
	}
}

func TestOnboarding_StepDetection_ProviderSet(t *testing.T) {
	h := NewHarness(t, nil)
	h.Config().LLM.DefaultProvider = "anthropic"

	resp := getOnboarding(t, h)
	if !stepMap(resp.Steps)["provider"] {
		t.Error("provider step should be done when default_provider is set")
	}
}

func TestOnboarding_StepDetection_ProviderMissing(t *testing.T) {
	h := NewHarness(t, nil)
	h.Config().LLM.DefaultProvider = ""

	resp := getOnboarding(t, h)
	if stepMap(resp.Steps)["provider"] {
		t.Error("provider step should be incomplete when default_provider is empty")
	}
}

func TestOnboarding_StepDetection_SkillFileExists(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "greet.md"), []byte("# Greet\nSay hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous"},
		},
	})
	h.Config().Agents[0].SkillsDir = skillsDir

	resp := getOnboarding(t, h)
	if !stepMap(resp.Steps)["skill"] {
		t.Error("skill step should be done when a .md file exists in the skills directory")
	}
}

func TestOnboarding_StepDetection_SkillDirEmpty(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous"},
		},
	})
	h.Config().Agents[0].SkillsDir = skillsDir

	resp := getOnboarding(t, h)
	if stepMap(resp.Steps)["skill"] {
		t.Error("skill step should be incomplete when skills dir has no .md files")
	}
}

func TestOnboarding_StepDetection_SkillNonMDIgnored(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a non-markdown file — should not count.
	if err := os.WriteFile(filepath.Join(skillsDir, "notes.txt"), []byte("not a skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous"},
		},
	})
	h.Config().Agents[0].SkillsDir = skillsDir

	resp := getOnboarding(t, h)
	if stepMap(resp.Steps)["skill"] {
		t.Error("skill step should be incomplete when only non-.md files exist")
	}
}

// ---------------------------------------------------------------------------
// show_onboarding logic
// ---------------------------------------------------------------------------

func TestOnboarding_ShowOnboarding_FalseWhenAllDone(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "test.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewHarness(t, &HarnessOpts{
		PasswordHash: bcryptHashFor(t, "pass"),
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: []string{"discord"}},
		},
	})
	h.Config().LLM.DefaultProvider = "openrouter"
	h.Config().Agents[0].SkillsDir = skillsDir

	resp := getOnboarding(t, h)
	if resp.ShowOnboarding {
		t.Error("show_onboarding should be false when all steps are complete")
	}
}

func TestOnboarding_ShowOnboarding_TrueWhenPartiallyDone(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		PasswordHash: bcryptHashFor(t, "pass"),
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: []string{"telegram"}},
		},
	})
	// provider is empty, skills dir doesn't exist

	resp := getOnboarding(t, h)
	if !resp.ShowOnboarding {
		t.Error("show_onboarding should be true when some steps are incomplete")
	}
}

func TestOnboarding_ShowOnboarding_FalseWhenDismissedEvenIfIncomplete(t *testing.T) {
	h := NewHarness(t, nil)
	h.Config().API.OnboardingDismissed = true

	resp := getOnboarding(t, h)
	if resp.ShowOnboarding {
		t.Error("show_onboarding should be false when dismissed, even with incomplete steps")
	}
	if !resp.Dismissed {
		t.Error("dismissed should be true")
	}
}

// ---------------------------------------------------------------------------
// Dismiss endpoint — TOML persistence
// ---------------------------------------------------------------------------

func TestOnboarding_Dismiss_PersistsToTOML(t *testing.T) {
	cfgPath := tempConfigPath(t)
	h := NewHarness(t, &HarnessOpts{ConfigPath: cfgPath})

	// Pre-check: not dismissed.
	resp := getOnboarding(t, h)
	if resp.Dismissed {
		t.Fatal("precondition: dismissed should be false initially")
	}

	// Dismiss.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/dismiss", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("dismiss: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// In-memory config updated.
	resp = getOnboarding(t, h)
	if !resp.Dismissed {
		t.Error("dismissed should be true after POST")
	}
	if resp.ShowOnboarding {
		t.Error("show_onboarding should be false after dismissal")
	}

	// TOML on disk contains the flag.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "onboarding_dismissed = true") {
		t.Errorf("TOML should contain onboarding_dismissed = true, got:\n%s", string(data))
	}
}

func TestOnboarding_Dismiss_Idempotent(t *testing.T) {
	cfgPath := tempConfigPath(t)
	h := NewHarness(t, &HarnessOpts{ConfigPath: cfgPath})

	// Dismiss twice — second call should succeed without error.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/dismiss", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first dismiss: status = %d", rec.Code)
	}

	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/dismiss", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second dismiss: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Wizard-complete endpoint — TOML persistence
// ---------------------------------------------------------------------------

func TestOnboarding_WizardComplete_PersistsToTOML(t *testing.T) {
	cfgPath := tempConfigPath(t)
	h := NewHarness(t, &HarnessOpts{ConfigPath: cfgPath})

	// Pre-check: wizard not completed.
	resp := getOnboarding(t, h)
	if resp.WizardCompleted {
		t.Fatal("precondition: wizard_completed should be false initially")
	}

	// Mark wizard complete.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/wizard-complete", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("wizard-complete: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// In-memory config updated.
	resp = getOnboarding(t, h)
	if !resp.WizardCompleted {
		t.Error("wizard_completed should be true after POST")
	}

	// TOML on disk contains the flag.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "wizard_completed = true") {
		t.Errorf("TOML should contain wizard_completed = true, got:\n%s", string(data))
	}
}

func TestOnboarding_WizardComplete_Idempotent(t *testing.T) {
	cfgPath := tempConfigPath(t)
	h := NewHarness(t, &HarnessOpts{ConfigPath: cfgPath})

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/wizard-complete", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first: status = %d", rec.Code)
	}

	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/wizard-complete", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("second: status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Auth enforcement
// ---------------------------------------------------------------------------

func TestOnboarding_RequiresAuth(t *testing.T) {
	h := NewHarness(t, nil)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/onboarding"},
		{http.MethodPost, "/api/v1/onboarding/dismiss"},
		{http.MethodPost, "/api/v1/onboarding/wizard-complete"},
	}

	for _, ep := range endpoints {
		req := newUnauthenticatedRequest(ep.method, ep.path)
		rec := h.Do(req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401, got %d", ep.method, ep.path, rec.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// Full lifecycle flow
// ---------------------------------------------------------------------------

func TestOnboarding_FullLifecycle(t *testing.T) {
	cfgPath := tempConfigPath(t)
	skillsDir := filepath.Join(filepath.Dir(cfgPath), "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	h := NewHarness(t, &HarnessOpts{
		ConfigPath:   cfgPath,
		PasswordHash: bcryptHashFor(t, "lifecycle-pass"),
		Agents: []agentSetup{
			{Name: "assistant", Tier: "supervised", Adapters: []string{"telegram"}},
		},
	})
	h.Config().Agents[0].SkillsDir = skillsDir

	// Phase 1: partial setup — auth + agent + adapter done; provider + skill missing.
	resp := getOnboarding(t, h)
	if !resp.ShowOnboarding {
		t.Fatal("phase 1: show_onboarding should be true (provider/skill missing)")
	}
	steps := stepMap(resp.Steps)
	if !steps["auth"] || !steps["agent"] || !steps["adapter"] {
		t.Errorf("phase 1: auth/agent/adapter should be done, got %v", steps)
	}
	if steps["provider"] || steps["skill"] {
		t.Errorf("phase 1: provider/skill should be incomplete, got %v", steps)
	}
	if resp.WizardCompleted {
		t.Error("phase 1: wizard_completed should be false")
	}

	// Phase 2: add provider.
	h.Config().LLM.DefaultProvider = "openrouter"
	resp = getOnboarding(t, h)
	steps = stepMap(resp.Steps)
	if !steps["provider"] {
		t.Error("phase 2: provider step should now be done")
	}
	if !resp.ShowOnboarding {
		t.Error("phase 2: show_onboarding should still be true (skill missing)")
	}

	// Phase 3: add a skill file.
	if err := os.WriteFile(filepath.Join(skillsDir, "weather.md"), []byte("# Weather\nGet weather"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp = getOnboarding(t, h)
	steps = stepMap(resp.Steps)
	if !steps["skill"] {
		t.Error("phase 3: skill step should now be done")
	}
	if resp.ShowOnboarding {
		t.Error("phase 3: show_onboarding should be false (all steps complete)")
	}

	// Phase 4: mark wizard complete.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/wizard-complete", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("phase 4: wizard-complete status = %d", rec.Code)
	}
	resp = getOnboarding(t, h)
	if !resp.WizardCompleted {
		t.Error("phase 4: wizard_completed should be true")
	}

	// Phase 5: dismiss.
	rec = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/onboarding/dismiss", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("phase 5: dismiss status = %d", rec.Code)
	}
	resp = getOnboarding(t, h)
	if !resp.Dismissed {
		t.Error("phase 5: dismissed should be true")
	}
	if resp.ShowOnboarding {
		t.Error("phase 5: show_onboarding should remain false")
	}

	// Verify both flags persisted to TOML.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "onboarding_dismissed = true") {
		t.Error("TOML missing onboarding_dismissed")
	}
	if !strings.Contains(string(data), "wizard_completed = true") {
		t.Error("TOML missing wizard_completed")
	}
}

// ---------------------------------------------------------------------------
// Multi-agent scenarios
// ---------------------------------------------------------------------------

func TestOnboarding_MultiAgent_AdapterOnSecondAgent(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "brain", Tier: "autonomous", Adapters: nil},
			{Name: "chat", Tier: "supervised", Adapters: []string{"discord"}},
		},
	})

	resp := getOnboarding(t, h)
	if !stepMap(resp.Steps)["adapter"] {
		t.Error("adapter step should be done when any agent has adapters")
	}
}

func TestOnboarding_MultiAgent_SkillInSecondAgentDir(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "agent2-skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "hello.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "primary", Tier: "autonomous"},
			{Name: "secondary", Tier: "autonomous"},
		},
	})
	// Only the second agent has a skills dir with a .md file.
	h.Config().Agents[1].SkillsDir = skillsDir

	resp := getOnboarding(t, h)
	if !stepMap(resp.Steps)["skill"] {
		t.Error("skill step should be done when any agent's skills dir has .md files")
	}
}

// ---------------------------------------------------------------------------
// Step ordering and defaults
// ---------------------------------------------------------------------------

func TestOnboarding_StepOrdering(t *testing.T) {
	h := NewHarness(t, nil)

	resp := getOnboarding(t, h)
	wantOrder := []string{"auth", "agent", "adapter", "provider", "skill"}

	if len(resp.Steps) != len(wantOrder) {
		t.Fatalf("expected %d steps, got %d", len(wantOrder), len(resp.Steps))
	}
	for i, step := range resp.Steps {
		if step.ID != wantOrder[i] {
			t.Errorf("step[%d].id = %q, want %q", i, step.ID, wantOrder[i])
		}
		if step.Label == "" {
			t.Errorf("step[%d] (%s) has empty label", i, step.ID)
		}
	}
}

func TestOnboarding_FreshHarnessDefaults(t *testing.T) {
	// Default harness: one "default" agent with telegram adapter, no password,
	// no provider, no skills dir. Asserts the exact Done state for each step.
	h := NewHarness(t, nil)

	resp := getOnboarding(t, h)

	want := map[string]bool{
		"auth":     false,
		"agent":    true,
		"adapter":  true,
		"provider": false,
		"skill":    false,
	}

	for _, step := range resp.Steps {
		if step.Done != want[step.ID] {
			t.Errorf("step %q: done = %v, want %v", step.ID, step.Done, want[step.ID])
		}
	}

	if !resp.ShowOnboarding {
		t.Error("show_onboarding should be true for fresh harness (incomplete steps)")
	}
	if resp.Dismissed {
		t.Error("dismissed should be false for fresh harness")
	}
	if resp.WizardCompleted {
		t.Error("wizard_completed should be false for fresh harness")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type onboardingResp struct {
	ShowOnboarding  bool             `json:"show_onboarding"`
	Dismissed       bool             `json:"dismissed"`
	WizardCompleted bool             `json:"wizard_completed"`
	Steps           []onboardingStep `json:"steps"`
}

type onboardingStep struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Done  bool   `json:"done"`
}

func getOnboarding(t *testing.T, h *Harness) onboardingResp {
	t.Helper()
	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/onboarding", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/onboarding: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp onboardingResp
	DecodeJSON(t, rec, &resp)
	return resp
}

func stepMap(steps []onboardingStep) map[string]bool {
	m := make(map[string]bool, len(steps))
	for _, s := range steps {
		m[s.ID] = s.Done
	}
	return m
}

func newUnauthenticatedRequest(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

// tempConfigPath creates a minimal TOML config file in a temp directory and
// returns its path. The parent directory is t.TempDir(), so callers can derive
// sibling paths via filepath.Dir(cfgPath).
func tempConfigPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("[api]\nlisten = \":0\"\n"), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return cfgPath
}
