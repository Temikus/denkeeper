package api

import (
	"net/http"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/persona"
)

// wizardStatus is the setup wizard's progress, derived from config and the
// running agents rather than browser storage, so the wizard resumes at the
// right step from any browser and the dashboard can show "Setup 2 of 4".
type wizardStatus struct {
	Completed bool `json:"completed"`
	Skipped   bool `json:"skipped"`
	// Agent is the primary agent: the first one no other agent uses as its
	// supervisor. Empty until one exists.
	Agent     string       `json:"agent,omitempty"`
	Steps     []wizardStep `json:"steps"`
	DoneCount int          `json:"done_count"`
	Total     int          `json:"total"`
	// RestartRequired is true when a chat app is saved but its adapter is
	// not running yet; adapters only start at boot.
	RestartRequired bool          `json:"restart_required"`
	Restart         restartStatus `json:"restart"`
}

// restartStatus tells the UI whether POST /server/restart will bring the
// server back on its own (a process manager is watching) or just stop it.
type restartStatus struct {
	Available bool `json:"available"`
	Managed   bool `json:"managed"`
}

// wizardStep is one of provider, agent, persona, chat_app.
type wizardStep struct {
	ID       string           `json:"id"`
	Done     bool             `json:"done"`
	Optional bool             `json:"optional,omitempty"`
	Detail   wizardStepDetail `json:"detail"`
}

// wizardStepDetail carries what the wizard's summary rail shows for a step.
// Only the fields relevant to that step are set.
type wizardStepDetail struct {
	Name              string `json:"name,omitempty"`
	Type              string `json:"type,omitempty"`
	Model             string `json:"model,omitempty"`
	Tier              string `json:"tier,omitempty"`
	Supervisor        string `json:"supervisor,omitempty"`
	SupervisorModel   string `json:"supervisor_model,omitempty"`
	SupervisorTimeout string `json:"supervisor_timeout,omitempty"`
	DisplayName       string `json:"display_name,omitempty"`
	Emoji             string `json:"emoji,omitempty"`
	Theme             string `json:"theme,omitempty"`
	Running           bool   `json:"running,omitempty"`
}

func (s *Server) buildWizardStatus(cfg *config.Config) wizardStatus {
	primary := primaryAgent(cfg)
	steps := []wizardStep{
		providerWizardStep(cfg),
		agentWizardStep(cfg, primary),
		s.personaWizardStep(primary),
		s.chatAppWizardStep(primary),
	}
	ws := wizardStatus{
		Completed: cfg.API.WizardCompleted,
		Skipped:   cfg.API.WizardSkipped,
		Steps:     steps,
		Total:     len(steps),
		Restart:   restartStatus{Available: s.deps.RestartFunc != nil, Managed: s.deps.RestartManaged},
	}
	if primary != nil {
		ws.Agent = primary.Name
	}
	for _, st := range steps {
		if st.Done {
			ws.DoneCount++
		}
	}
	ws.RestartRequired = s.chatAppAwaitingRestart(cfg)
	return ws
}

// primaryAgent returns the first agent that is not another agent's
// supervisor, or nil.
func primaryAgent(cfg *config.Config) *config.AgentInstanceConfig {
	supervisors := make(map[string]bool, len(cfg.Agents))
	for _, a := range cfg.Agents {
		if a.Supervisor != "" {
			supervisors[a.Supervisor] = true
		}
	}
	for i := range cfg.Agents {
		if !supervisors[cfg.Agents[i].Name] {
			return &cfg.Agents[i]
		}
	}
	return nil
}

// usableProvider returns the default provider if it can serve requests,
// else the first instance that can: one with a key, or an Ollama instance.
func usableProvider(cfg *config.Config) *config.ProviderInstanceConfig {
	usable := func(p *config.ProviderInstanceConfig) bool { return p.APIKey != "" || p.Type == "ollama" }
	if p := findProviderIn(cfg, cfg.LLM.DefaultProvider); p != nil && usable(p) {
		return p
	}
	for i := range cfg.LLM.Providers {
		if usable(&cfg.LLM.Providers[i]) {
			return &cfg.LLM.Providers[i]
		}
	}
	return nil
}

func providerWizardStep(cfg *config.Config) wizardStep {
	st := wizardStep{ID: "provider"}
	if p := usableProvider(cfg); p != nil {
		st.Done = true
		st.Detail = wizardStepDetail{Name: p.Name, Type: p.Type}
	}
	return st
}

func agentWizardStep(cfg *config.Config, primary *config.AgentInstanceConfig) wizardStep {
	st := wizardStep{ID: "agent"}
	if primary == nil {
		return st
	}
	st.Done = true
	st.Detail = wizardStepDetail{
		Name:       primary.Name,
		Model:      primary.LLMModel,
		Tier:       primary.SessionTier,
		Supervisor: primary.Supervisor,
	}
	if st.Detail.Model == "" {
		st.Detail.Model = cfg.LLM.DefaultModel
	}
	if primary.Supervisor != "" {
		st.Detail.SupervisorTimeout = primary.SupervisorTimeout
		for _, a := range cfg.Agents {
			if a.Name == primary.Supervisor {
				st.Detail.SupervisorModel = a.LLMModel
			}
		}
	}
	return st
}

func (s *Server) personaWizardStep(primary *config.AgentInstanceConfig) wizardStep {
	st := wizardStep{ID: "persona"}
	if primary == nil || s.deps.Dispatcher == nil {
		return st
	}
	e := s.deps.Dispatcher.Agent(primary.Name)
	if e == nil {
		return st
	}
	content, _, _, _ := e.PersonaSection("identity")
	id, err := persona.ParseIdentity(content)
	if err != nil || id.Name == "" {
		return st
	}
	st.Done = true
	st.Detail = wizardStepDetail{DisplayName: id.Name, Emoji: id.Emoji, Theme: id.Theme}
	return st
}

func (s *Server) chatAppWizardStep(primary *config.AgentInstanceConfig) wizardStep {
	st := wizardStep{ID: "chat_app", Optional: true}
	if primary == nil || len(primary.Adapters) == 0 {
		return st
	}
	typ, _, _ := config.ParseChannel(primary.Adapters[0])
	if typ == "" {
		typ = primary.Adapters[0]
	}
	st.Done = true
	st.Detail = wizardStepDetail{Type: typ, Running: s.deps.Dispatcher != nil && s.deps.Dispatcher.HasAdapter(typ)}
	return st
}

// chatAppAwaitingRestart reports whether a configured chat adapter is not
// running in this process.
func (s *Server) chatAppAwaitingRestart(cfg *config.Config) bool {
	if s.deps.Dispatcher == nil {
		return false
	}
	return (cfg.Telegram.Token != "" && !s.deps.Dispatcher.HasAdapter("telegram")) ||
		(cfg.Discord.Token != "" && !s.deps.Dispatcher.HasAdapter("discord"))
}

// handleWizardSkip godoc
// @Summary      Leave the setup wizard for later
// @Description  Persists wizard_completed=true and wizard_skipped=true, so the wizard stops opening on login but the dashboard keeps offering to resume it.
// @Tags         onboarding
// @Produce      json
// @Security     BearerAuth
// @Success      204  "Wizard skipped"
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires admin scope"
// @Failure      500  {object}  map[string]string  "Failed to persist"
// @Router       /onboarding/wizard-skip [post]
func (s *Server) handleWizardSkip(w http.ResponseWriter, _ *http.Request) {
	if err := config.UpdateAPIConfig(s.deps.ConfigPath, map[string]any{
		"wizard_completed": true,
		"wizard_skipped":   true,
	}); err != nil {
		s.logger.Error("persisting wizard skip", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist"})
		return
	}
	s.deps.Config.Update(func(c *config.Config) {
		c.API.WizardCompleted = true
		c.API.WizardSkipped = true
	})
	w.WriteHeader(http.StatusNoContent)
}
