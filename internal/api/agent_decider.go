package api

import (
	"fmt"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// deciderUpdate is a validated supervisor-decider change, ready to apply to
// the live engine.
type deciderUpdate struct {
	bind   *llm.Decider // non-nil: wire this decider
	unbind bool
	retune bool // mode or thresholds changed on the wired decider
	stage  agent.DeciderStageConfig
}

func (in *agentConfigUpdateInput) touchesDecider() bool {
	return in.SupervisorDecider != nil || in.SupervisorDeciderMode != nil ||
		in.SupervisorDeciderApproveAt != nil || in.SupervisorDeciderDenyAt != nil
}

func applyDeciderFields(ac *config.AgentInstanceConfig, input *agentConfigUpdateInput) {
	if input.SupervisorDecider != nil {
		// A bind or clear starts from defaults: a stale enforce mode left by
		// an earlier decider must not come back with a new name.
		ac.SupervisorDecider = *input.SupervisorDecider
		ac.SupervisorDeciderMode = ""
		ac.SupervisorDeciderApproveAt = 0
		ac.SupervisorDeciderDenyAt = 0
	}
	if input.SupervisorDeciderMode != nil {
		ac.SupervisorDeciderMode = *input.SupervisorDeciderMode
	}
	if input.SupervisorDeciderApproveAt != nil {
		ac.SupervisorDeciderApproveAt = *input.SupervisorDeciderApproveAt
	}
	if input.SupervisorDeciderDenyAt != nil {
		ac.SupervisorDeciderDenyAt = *input.SupervisorDeciderDenyAt
	}
	// A zero threshold or empty mode means "default", as in the TOML.
	config.ApplySupervisorDeciderDefaults(ac)
}

// planDeciderUpdate validates the request's decider fields merged over the
// agent's stored config. A session_tier change is validated too: leaving the
// supervised tier with a decider set would write a config that fails to load.
// It changes nothing, so a rejected request leaves the engine untouched.
func (s *Server) planDeciderUpdate(name string, input *agentConfigUpdateInput) (deciderUpdate, error) {
	var up deciderUpdate
	if !input.touchesDecider() && input.SessionTier == nil {
		return up, nil
	}
	cfg := s.appConfig()
	if cfg == nil {
		if input.touchesDecider() {
			return up, fmt.Errorf("supervisor_decider needs a config file")
		}
		return up, nil
	}
	merged := config.AgentInstanceConfig{Name: name}
	for _, ac := range cfg.Agents {
		if ac.Name == name {
			merged = ac
			break
		}
	}
	if input.SessionTier != nil {
		merged.SessionTier = *input.SessionTier
	}
	applyDeciderFields(&merged, input)
	if err := config.ValidateSupervisorDecider(cfg, merged); err != nil {
		return up, err
	}
	if !input.touchesDecider() {
		return up, nil
	}

	up.stage = agent.DeciderStageConfig{
		Mode:      merged.SupervisorDeciderMode,
		ApproveAt: merged.SupervisorDeciderApproveAt,
		DenyAt:    merged.SupervisorDeciderDenyAt,
	}
	switch {
	case merged.SupervisorDecider == "":
		up.unbind = input.SupervisorDecider != nil
	case input.SupervisorDecider != nil:
		d, err := s.startedDecider(cfg, merged.SupervisorDecider)
		if err != nil {
			return up, err
		}
		up.bind = d
	default:
		up.retune = true
	}
	return up, nil
}

// startedDecider returns the decider client built at startup for name, or an
// error when the running client no longer matches the config (clients are not
// rebuilt on reload).
func (s *Server) startedDecider(cfg *config.Config, name string) (*llm.Decider, error) {
	d := s.deps.Deciders[name]
	for _, dc := range cfg.LLM.Deciders {
		if dc.Name == name && d != nil && d.Matches(dc.Provider, dc.Model) {
			return d, nil
		}
	}
	return nil, fmt.Errorf("decider %q was added or changed since startup; restart denkeeper to use it", name)
}

func (up deciderUpdate) apply(e *agent.Engine) {
	switch {
	case up.bind != nil:
		e.SetSupervisorDecider(up.bind, up.stage)
	case up.unbind:
		e.SetSupervisorDecider(nil, agent.DeciderStageConfig{})
	case up.retune:
		e.SetSupervisorDeciderConfig(up.stage)
	}
}

func addDeciderConfigChanges(changes map[string]any, input *agentConfigUpdateInput) {
	if input.SupervisorDecider != nil {
		// Same reset as applyDeciderFields, so the TOML cannot keep stale tuning.
		changes["supervisor_decider"] = *input.SupervisorDecider
		changes["supervisor_decider_mode"] = ""
		changes["supervisor_decider_approve_at"] = 0.0
		changes["supervisor_decider_deny_at"] = 0.0
	}
	if input.SupervisorDeciderMode != nil {
		changes["supervisor_decider_mode"] = *input.SupervisorDeciderMode
	}
	if input.SupervisorDeciderApproveAt != nil {
		changes["supervisor_decider_approve_at"] = *input.SupervisorDeciderApproveAt
	}
	if input.SupervisorDeciderDenyAt != nil {
		changes["supervisor_decider_deny_at"] = *input.SupervisorDeciderDenyAt
	}
}

// addDeciderDetail adds the decider fields to an agent detail response.
func addDeciderDetail(resp map[string]any, ac config.AgentInstanceConfig) {
	if ac.SupervisorDecider == "" {
		return
	}
	resp["supervisor_decider"] = ac.SupervisorDecider
	resp["supervisor_decider_mode"] = ac.SupervisorDeciderMode
	resp["supervisor_decider_approve_at"] = ac.SupervisorDeciderApproveAt
	resp["supervisor_decider_deny_at"] = ac.SupervisorDeciderDenyAt
}
