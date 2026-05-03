package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func testOnboardingServer(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	return &Server{
		cfg:    cfg.API,
		deps:   Deps{Config: cfg},
		logger: testLogger(),
	}
}

func TestHandleOnboarding_AllIncomplete(t *testing.T) {
	cfg := &config.Config{}
	s := testOnboardingServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding", nil)
	rec := httptest.NewRecorder()
	s.handleOnboarding(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp onboardingResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if !resp.ShowOnboarding {
		t.Error("expected show_onboarding=true when nothing is configured")
	}
	if resp.Dismissed {
		t.Error("expected dismissed=false")
	}
	for _, step := range resp.Steps {
		if step.Done {
			t.Errorf("expected step %q to be incomplete", step.ID)
		}
	}
}

func TestHandleOnboarding_AllComplete(t *testing.T) {
	cfg := &config.Config{
		Agents: []config.AgentInstanceConfig{
			{
				Name:     "test-agent",
				Adapters: []string{"telegram"},
			},
		},
		LLM: config.LLMConfig{
			DefaultProvider: "anthropic",
		},
	}
	// Auth: set password hash on the server
	s := testOnboardingServer(t, cfg)
	s.passwordHash = "some-hash"

	// Skill check won't find files (no directory), but we can verify the other 4 steps.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding", nil)
	rec := httptest.NewRecorder()
	s.handleOnboarding(rec, req)

	var resp onboardingResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	// Check individual steps (skill may be false since no real directory)
	stepMap := map[string]bool{}
	for _, step := range resp.Steps {
		stepMap[step.ID] = step.Done
	}
	if !stepMap["auth"] {
		t.Error("expected auth step done")
	}
	if !stepMap["agent"] {
		t.Error("expected agent step done")
	}
	if !stepMap["adapter"] {
		t.Error("expected adapter step done")
	}
	if !stepMap["provider"] {
		t.Error("expected provider step done")
	}
}

func TestHandleOnboarding_Dismissed(t *testing.T) {
	cfg := &config.Config{
		API: config.APIConfig{
			OnboardingDismissed: true,
		},
	}
	s := testOnboardingServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding", nil)
	rec := httptest.NewRecorder()
	s.handleOnboarding(rec, req)

	var resp onboardingResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp.ShowOnboarding {
		t.Error("expected show_onboarding=false when dismissed")
	}
	if !resp.Dismissed {
		t.Error("expected dismissed=true")
	}
}

func TestHandleOnboarding_IncludesWizardCompleted(t *testing.T) {
	cfg := &config.Config{
		API: config.APIConfig{WizardCompleted: true},
	}
	s := testOnboardingServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding", nil)
	rec := httptest.NewRecorder()
	s.handleOnboarding(rec, req)

	var resp onboardingResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.WizardCompleted {
		t.Error("expected wizard_completed=true in response")
	}
}

func TestHandleOnboarding_WizardCompletedDefaultFalse(t *testing.T) {
	cfg := &config.Config{}
	s := testOnboardingServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding", nil)
	rec := httptest.NewRecorder()
	s.handleOnboarding(rec, req)

	var resp onboardingResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp.WizardCompleted {
		t.Error("expected wizard_completed=false by default")
	}
}

func TestHandleWizardComplete(t *testing.T) {
	dir := t.TempDir()
	cfgPath := dir + "/config.toml"
	if err := os.WriteFile(cfgPath, []byte("[api]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	s := &Server{
		cfg:    cfg.API,
		deps:   Deps{Config: cfg, ConfigPath: cfgPath},
		logger: testLogger(),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/wizard-complete", nil)
	rec := httptest.NewRecorder()
	s.handleWizardComplete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if !cfg.API.WizardCompleted {
		t.Error("expected in-memory WizardCompleted=true after POST")
	}

	// Verify the flag was persisted to TOML.
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "wizard_completed") {
		t.Error("expected wizard_completed persisted to TOML config")
	}
}

func TestHandleOnboarding_PartialSetup(t *testing.T) {
	cfg := &config.Config{
		Agents: []config.AgentInstanceConfig{
			{Name: "agent1"},
		},
		LLM: config.LLMConfig{
			DefaultProvider: "openrouter",
		},
	}
	s := testOnboardingServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/onboarding", nil)
	rec := httptest.NewRecorder()
	s.handleOnboarding(rec, req)

	var resp onboardingResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if !resp.ShowOnboarding {
		t.Error("expected show_onboarding=true for partial setup")
	}

	stepMap := map[string]bool{}
	for _, step := range resp.Steps {
		stepMap[step.ID] = step.Done
	}
	if stepMap["auth"] {
		t.Error("expected auth step incomplete (no password, no OIDC, no keys)")
	}
	if !stepMap["agent"] {
		t.Error("expected agent step done")
	}
	if stepMap["adapter"] {
		t.Error("expected adapter step incomplete (no adapters configured)")
	}
	if !stepMap["provider"] {
		t.Error("expected provider step done")
	}
}
