package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/tool"
)

type onboardingStep struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Done  bool   `json:"done"`
}

type onboardingResponse struct {
	ShowOnboarding  bool             `json:"show_onboarding"`
	Steps           []onboardingStep `json:"steps"`
	Dismissed       bool             `json:"dismissed"`
	WizardCompleted bool             `json:"wizard_completed"`
}

// handleOnboarding godoc
// @Summary      Get onboarding status
// @Description  Returns the onboarding checklist with five setup milestones (auth, agent, adapter, provider, skill) and whether the onboarding card should be displayed.
// @Tags         onboarding
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  onboardingResponse
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires admin scope"
// @Router       /onboarding [get]
func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	cfg := s.deps.Config
	dismissed := cfg.API.OnboardingDismissed

	steps := s.buildOnboardingSteps(r)

	allDone := true
	for _, step := range steps {
		if !step.Done {
			allDone = false
			break
		}
	}

	writeJSON(w, http.StatusOK, onboardingResponse{
		ShowOnboarding:  !dismissed && !allDone,
		Steps:           steps,
		Dismissed:       dismissed,
		WizardCompleted: cfg.API.WizardCompleted,
	})
}

func (s *Server) buildOnboardingSteps(r *http.Request) []onboardingStep {
	cfg := s.deps.Config

	// auth: password OR OIDC configured OR API key exists
	authDone := s.passwordHash != "" || s.oidcProvider != nil
	if !authDone && s.deps.KeyStore != nil {
		hasKey, err := s.deps.KeyStore.HasActiveKey(r.Context())
		if err == nil && hasKey {
			authDone = true
		}
	}

	// agent: at least one agent configured
	agentDone := len(cfg.Agents) > 0

	// adapter: any agent has a non-empty adapters slice
	adapterDone := false
	for _, a := range cfg.Agents {
		if len(a.Adapters) > 0 {
			adapterDone = true
			break
		}
	}

	// provider: default LLM provider is set
	providerDone := cfg.LLM.DefaultProvider != ""

	// skill: at least one .md file in any agent's skills directory
	skillDone := hasSkillFiles(cfg)

	return []onboardingStep{
		{ID: "auth", Label: "Set up authentication", Done: authDone},
		{ID: "agent", Label: "Configure an agent", Done: agentDone},
		{ID: "adapter", Label: "Connect a chat adapter", Done: adapterDone},
		{ID: "provider", Label: "Add an LLM provider", Done: providerDone},
		{ID: "skill", Label: "Create a skill file", Done: skillDone},
	}
}

func hasSkillFiles(cfg *config.Config) bool {
	dirs := map[string]struct{}{}
	// Collect unique skill directories.
	if cfg.Agent.SkillsDir != "" {
		dirs[cfg.Agent.SkillsDir] = struct{}{}
	}
	for _, a := range cfg.Agents {
		if a.SkillsDir != "" {
			dirs[a.SkillsDir] = struct{}{}
		}
	}
	for dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
				return true
			}
		}
	}
	return false
}

// handleOnboardingDismiss godoc
// @Summary      Dismiss onboarding
// @Description  Persists onboarding_dismissed=true to the TOML config so the onboarding card is permanently hidden.
// @Tags         onboarding
// @Produce      json
// @Security     BearerAuth
// @Success      204  "Onboarding dismissed"
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires admin scope"
// @Failure      500  {object}  map[string]string  "Failed to persist"
// @Router       /onboarding/dismiss [post]
func (s *Server) handleOnboardingDismiss(w http.ResponseWriter, r *http.Request) {
	if err := tool.UpdateAPIConfig(s.deps.ConfigPath, map[string]any{
		"onboarding_dismissed": true,
	}); err != nil {
		s.logger.Error("persisting onboarding dismiss", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist"})
		return
	}
	s.deps.Config.API.OnboardingDismissed = true
	w.WriteHeader(http.StatusNoContent)
}

// handleWizardComplete godoc
// @Summary      Mark setup wizard complete
// @Description  Persists wizard_completed=true to the TOML config so the post-auth setup wizard is not shown again.
// @Tags         onboarding
// @Produce      json
// @Security     BearerAuth
// @Success      204  "Wizard marked complete"
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires admin scope"
// @Failure      500  {object}  map[string]string  "Failed to persist"
// @Router       /onboarding/wizard-complete [post]
func (s *Server) handleWizardComplete(w http.ResponseWriter, r *http.Request) {
	if err := tool.UpdateAPIConfig(s.deps.ConfigPath, map[string]any{
		"wizard_completed": true,
	}); err != nil {
		s.logger.Error("persisting wizard complete", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist"})
		return
	}
	s.deps.Config.API.WizardCompleted = true
	w.WriteHeader(http.StatusNoContent)
}
