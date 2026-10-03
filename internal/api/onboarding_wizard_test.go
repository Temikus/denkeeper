package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func getWizard(t *testing.T, s *Server) wizardStatus {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleOnboarding(rec, httptest.NewRequest(http.MethodGet, "/api/v1/onboarding", nil))
	var resp onboardingResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp.Wizard
}

func wizardStepByID(ws wizardStatus, id string) wizardStep {
	for _, st := range ws.Steps {
		if st.ID == id {
			return st
		}
	}
	return wizardStep{}
}

func TestHandleOnboarding_WizardSteps_Fresh(t *testing.T) {
	ws := getWizard(t, testOnboardingServer(t, &config.Config{}))

	if ws.Total != 4 || ws.DoneCount != 0 || ws.Agent != "" {
		t.Errorf("wizard = %+v, want 0 of 4 and no agent", ws)
	}
	if !wizardStepByID(ws, "chat_app").Optional {
		t.Error("chat_app step should be marked optional")
	}
}

func TestHandleOnboarding_WizardSteps_ProviderAndAgent(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			DefaultProvider: "anthropic",
			Providers:       []config.ProviderInstanceConfig{{Name: "anthropic", Type: "anthropic", APIKey: "k"}},
		},
		Agents: []config.AgentInstanceConfig{
			// The supervisor is listed first; it must not count as the primary agent.
			{Name: "supervisor", LLMModel: "claude-haiku-4-5"},
			{Name: "assistant", LLMModel: "claude-sonnet-5-5", SessionTier: "supervised", Supervisor: "supervisor", SupervisorTimeout: "30s"},
		},
	}
	ws := getWizard(t, testOnboardingServer(t, cfg))

	if ws.Agent != "assistant" || ws.DoneCount != 2 {
		t.Fatalf("wizard = %+v, want agent assistant and 2 done", ws)
	}
	p := wizardStepByID(ws, "provider").Detail
	if p.Name != "anthropic" || p.Type != "anthropic" {
		t.Errorf("provider detail = %+v", p)
	}
	a := wizardStepByID(ws, "agent").Detail
	if a.Model != "claude-sonnet-5-5" || a.Tier != "supervised" || a.SupervisorModel != "claude-haiku-4-5" || a.SupervisorTimeout != "30s" {
		t.Errorf("agent detail = %+v", a)
	}
}

func TestHandleOnboarding_WizardSteps_PersonaFromIdentity(t *testing.T) {
	deps := testDepsWithPersona(t)
	if err := deps.Dispatcher.Agent("default").SavePersonaSection("identity", "---\nname: Den\nemoji: \"🦊\"\ntheme: warm\n---\n"); err != nil {
		t.Fatal(err)
	}
	s := &Server{deps: deps, logger: testLogger()}

	st := wizardStepByID(getWizard(t, s), "persona")
	if !st.Done || st.Detail.DisplayName != "Den" || st.Detail.Emoji != "🦊" || st.Detail.Theme != "warm" {
		t.Errorf("persona step = %+v", st)
	}
}

func TestHandleOnboarding_WizardSteps_ChatAppAwaitsRestart(t *testing.T) {
	deps := testDepsWithPersona(t) // dispatcher with no running adapters
	deps.Config = config.NewHolder(&config.Config{
		Telegram: config.TelegramConfig{Token: "1:abc", AllowedUsers: []int64{1}},
		Agents:   []config.AgentInstanceConfig{{Name: "default", Adapters: []string{"telegram"}}},
	})
	deps.RestartFunc = func() error { return nil }
	deps.RestartManaged = true
	s := &Server{deps: deps, logger: testLogger()}

	ws := getWizard(t, s)
	st := wizardStepByID(ws, "chat_app")
	if !st.Done || st.Detail.Type != "telegram" || st.Detail.Running {
		t.Errorf("chat_app step = %+v, want done, telegram, not running", st)
	}
	if !ws.RestartRequired || !ws.Restart.Available || !ws.Restart.Managed {
		t.Errorf("wizard = %+v, want restart required, available and managed", ws)
	}
}

func onboardingServerWithFile(t *testing.T) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "denkeeper.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := testOnboardingServer(t, &config.Config{})
	s.deps.ConfigPath = path
	return s, path
}

func TestWizardSkip_PersistsBothFlags(t *testing.T) {
	s, path := onboardingServerWithFile(t)

	rec := httptest.NewRecorder()
	s.handleWizardSkip(rec, httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/wizard-skip", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	if api := s.appConfig().API; !api.WizardCompleted || !api.WizardSkipped {
		t.Errorf("in-memory flags = completed %v skipped %v, want both true", api.WizardCompleted, api.WizardSkipped)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "wizard_skipped = true") || !strings.Contains(string(data), "wizard_completed = true") {
		t.Errorf("TOML missing flags:\n%s", data)
	}
}

func TestWizardComplete_ClearsSkipped(t *testing.T) {
	s, _ := onboardingServerWithFile(t)
	s.handleWizardSkip(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))

	s.handleWizardComplete(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))

	if api := s.appConfig().API; !api.WizardCompleted || api.WizardSkipped {
		t.Errorf("flags = completed %v skipped %v, want completed and not skipped", api.WizardCompleted, api.WizardSkipped)
	}
}
