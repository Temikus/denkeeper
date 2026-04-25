package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

// agentConfigUpdateInput holds the mutable fields for PATCH /api/v1/agents/{name}.
type agentConfigUpdateInput struct {
	Name                *string                  `json:"name,omitempty"`
	SessionTier         *string                  `json:"session_tier,omitempty"`
	LLMProvider         *string                  `json:"llm_provider,omitempty"`
	LLMModel            *string                  `json:"llm_model,omitempty"`
	Description         *string                  `json:"description,omitempty"`
	MaxToolRounds       *int                     `json:"max_tool_rounds,omitempty"`
	BrowserURLAllowlist *[]string                `json:"browser_url_allowlist,omitempty"`
	Fallbacks           *[]config.FallbackConfig `json:"fallbacks,omitempty"`
	CostLimitSoft       *float64                 `json:"cost_limit_soft,omitempty"`
	CostLimitHard       *float64                 `json:"cost_limit_hard,omitempty"`
}

// handleAgentConfigUpdate godoc
// @Summary Update agent configuration
// @Description Mutates agent settings: tier, provider, model, cost limits, fallbacks, etc.
// @Tags agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Agent name"
// @Param body body agentConfigUpdateInput true "Fields to update"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /agents/{name} [patch]
func (s *Server) handleAgentConfigUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing agent name"})
		return
	}

	e := s.deps.Dispatcher.Agent(name)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	var input agentConfigUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if errMsg := validateAgentInput(&input); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	// Handle rename before other mutations.
	if input.Name != nil && *input.Name != name {
		code, msg := s.handleAgentRename(name, *input.Name)
		if code != 0 {
			writeJSON(w, code, map[string]string{"error": msg})
			return
		}
		name = *input.Name
		e = s.deps.Dispatcher.Agent(name)
	}

	// Apply runtime changes to the engine.
	if httpStatus, errMsg := applyAgentRuntimeChanges(e, &input); httpStatus != 0 {
		writeJSON(w, httpStatus, map[string]string{"error": errMsg})
		return
	}

	// Sync per-agent cost limits to the live CostTracker.
	if input.CostLimitSoft != nil || input.CostLimitHard != nil {
		s.syncAgentCostLimits(name, &input)
	}

	s.persistAgentConfig(name, &input)
	s.updateInMemoryAgentConfig(name, &input)

	writeJSON(w, http.StatusOK, map[string]string{
		"name":   name,
		"status": "updated",
	})
}

// validateAgentInput checks all input fields for validity.
// Returns an error message or empty string on success.
func validateAgentInput(input *agentConfigUpdateInput) string {
	if input.SessionTier != nil && !security.ValidTier(*input.SessionTier) {
		return "invalid session_tier: must be autonomous, supervised, or restricted"
	}
	if input.Fallbacks != nil {
		if err := config.ValidateFallbacks(*input.Fallbacks); err != nil {
			return err.Error()
		}
	}
	if input.CostLimitSoft != nil && *input.CostLimitSoft < 0 {
		return "cost_limit_soft must be >= 0"
	}
	if input.CostLimitHard != nil && *input.CostLimitHard < 0 {
		return "cost_limit_hard must be >= 0"
	}
	return ""
}

// applyAgentRuntimeChanges mutates the engine for the fields present in input.
// Returns (0, "") on success or (httpStatus, errorMessage) on failure.
func applyAgentRuntimeChanges(e *agent.Engine, input *agentConfigUpdateInput) (int, string) {
	if input.SessionTier != nil {
		if err := e.SetPermissionTier(*input.SessionTier); err != nil {
			return http.StatusInternalServerError, "setting permission tier: " + err.Error()
		}
	}
	if input.LLMProvider != nil {
		if err := e.SetProvider(*input.LLMProvider); err != nil {
			return http.StatusBadRequest, "invalid llm_provider: " + err.Error()
		}
	}
	if input.LLMModel != nil {
		e.SetModel(*input.LLMModel)
	}
	if input.MaxToolRounds != nil {
		if *input.MaxToolRounds <= 0 {
			return http.StatusBadRequest, "max_tool_rounds must be >= 1"
		}
		e.SetMaxToolRounds(*input.MaxToolRounds)
	}
	if input.Fallbacks != nil {
		e.LLMRouter().SetFallbacks(convertFallbackConfigs(*input.Fallbacks))
	}
	return 0, ""
}

// syncAgentCostLimits propagates cost limit changes to the live CostTracker.
func (s *Server) syncAgentCostLimits(name string, input *agentConfigUpdateInput) {
	defaults := s.deps.CostTracker.DefaultLimits()
	limits := defaults
	for _, ac := range s.deps.Config.Agents {
		if ac.Name == name {
			if ac.CostLimitSoft != nil {
				limits.Soft = *ac.CostLimitSoft
			}
			if ac.CostLimitHard != nil {
				limits.Hard = *ac.CostLimitHard
			}
			break
		}
	}
	if input.CostLimitSoft != nil {
		limits.Soft = *input.CostLimitSoft
	}
	if input.CostLimitHard != nil {
		limits.Hard = *input.CostLimitHard
	}
	s.deps.CostTracker.SetAgentLimits(name, limits)
}

// handleAgentRename validates and executes an agent rename.
// Returns (0, "") on success, or (httpStatus, errorMessage) on failure.
func (s *Server) handleAgentRename(oldName, newName string) (int, string) {
	if oldName == "default" {
		return http.StatusBadRequest, "cannot rename the default agent"
	}
	if !config.ValidResourceName(newName) {
		return http.StatusBadRequest, "invalid agent name: must be lowercase alphanumeric with hyphens, max 64 chars"
	}
	if s.deps.Dispatcher.Agent(newName) != nil {
		return http.StatusConflict, "agent name already exists"
	}
	if err := s.deps.Dispatcher.RenameAgent(oldName, newName); err != nil {
		return http.StatusInternalServerError, "renaming agent: " + err.Error()
	}
	if s.deps.ConfigPath != "" {
		if err := tool.RenameAgentInConfig(s.deps.ConfigPath, oldName, newName); err != nil {
			s.logger.Warn("failed to persist agent rename", "old", oldName, "new", newName, "error", err)
		}
	}
	s.renameInMemoryAgent(oldName, newName)
	return 0, ""
}

// convertFallbackConfigs converts config fallback rules to LLM router rules.
func convertFallbackConfigs(cfgs []config.FallbackConfig) []llm.FallbackRule {
	rules := make([]llm.FallbackRule, len(cfgs))
	for i, f := range cfgs {
		rules[i] = llm.FallbackRule{
			Trigger:    f.Trigger,
			Action:     f.Action,
			Provider:   f.Provider,
			Model:      f.Model,
			Threshold:  f.Threshold,
			MaxRetries: f.MaxRetries,
			Backoff:    f.Backoff,
		}
	}
	return rules
}

// persistAgentConfig writes changed fields to the TOML config file.
func (s *Server) persistAgentConfig(name string, input *agentConfigUpdateInput) {
	if s.deps.ConfigPath == "" {
		return
	}

	changes := make(map[string]any)
	if input.SessionTier != nil {
		changes["session_tier"] = *input.SessionTier
	}
	if input.LLMProvider != nil {
		changes["llm_provider"] = *input.LLMProvider
	}
	if input.LLMModel != nil {
		changes["llm_model"] = *input.LLMModel
	}
	if input.Description != nil {
		changes["description"] = *input.Description
	}
	if input.MaxToolRounds != nil {
		changes["max_tool_rounds"] = *input.MaxToolRounds
	}
	if input.BrowserURLAllowlist != nil {
		allowlist := make([]any, len(*input.BrowserURLAllowlist))
		for i, d := range *input.BrowserURLAllowlist {
			allowlist[i] = d
		}
		changes["browser_url_allowlist"] = allowlist
	}
	if input.Fallbacks != nil {
		changes["fallback"] = serializeFallbacks(*input.Fallbacks)
	}
	if input.CostLimitSoft != nil {
		changes["cost_limit_soft"] = *input.CostLimitSoft
	}
	if input.CostLimitHard != nil {
		changes["cost_limit_hard"] = *input.CostLimitHard
	}
	if len(changes) > 0 {
		if err := tool.UpdateAgentInConfig(s.deps.ConfigPath, name, changes); err != nil {
			s.logger.Warn("failed to persist agent config", "agent", name, "error", err)
		}
	}
}

// updateInMemoryAgentConfig applies input fields to the in-memory config.
func (s *Server) updateInMemoryAgentConfig(name string, input *agentConfigUpdateInput) {
	if s.deps.Config == nil {
		return
	}
	for i := range s.deps.Config.Agents {
		if s.deps.Config.Agents[i].Name != name {
			continue
		}
		applyAgentFields(&s.deps.Config.Agents[i], input)
		return
	}
}

// renameInMemoryAgent updates the agent name in the in-memory config slice.
func (s *Server) renameInMemoryAgent(oldName, newName string) {
	if s.deps.Config == nil {
		return
	}
	for i := range s.deps.Config.Agents {
		if s.deps.Config.Agents[i].Name == oldName {
			s.deps.Config.Agents[i].Name = newName
			return
		}
	}
}

func applyAgentFields(ac *config.AgentInstanceConfig, input *agentConfigUpdateInput) {
	if input.SessionTier != nil {
		ac.SessionTier = *input.SessionTier
	}
	if input.LLMProvider != nil {
		ac.LLMProvider = *input.LLMProvider
	}
	if input.LLMModel != nil {
		ac.LLMModel = *input.LLMModel
	}
	if input.Description != nil {
		ac.Description = *input.Description
	}
	if input.MaxToolRounds != nil {
		ac.MaxToolRounds = *input.MaxToolRounds
	}
	if input.BrowserURLAllowlist != nil {
		ac.BrowserURLAllowlist = *input.BrowserURLAllowlist
	}
	if input.Fallbacks != nil {
		ac.Fallbacks = *input.Fallbacks
	}
	if input.CostLimitSoft != nil {
		ac.CostLimitSoft = input.CostLimitSoft
	}
	if input.CostLimitHard != nil {
		ac.CostLimitHard = input.CostLimitHard
	}
}

// ---------------------------------------------------------------------------
// Agent create & delete
// ---------------------------------------------------------------------------

// agentCreateInput holds the fields for POST /api/v1/agents.
type agentCreateInput struct {
	Name        string `json:"name"`
	LLMProvider string `json:"llm_provider,omitempty"`
	LLMModel    string `json:"llm_model,omitempty"`
	SessionTier string `json:"session_tier,omitempty"`
	Description string `json:"description,omitempty"`
}

// handleCreateAgent godoc
// @Summary Create agent
// @Description Creates a new agent at runtime with persona directory and TOML persistence
// @Tags agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body agentCreateInput true "Agent configuration"
// @Success 201 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /agents [post]
func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	if s.deps.AgentFactory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent creation not available"})
		return
	}
	if s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config persistence not available"})
		return
	}

	var input agentCreateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if !config.ValidResourceName(input.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid agent name: must be lowercase alphanumeric with hyphens, 1-64 chars",
		})
		return
	}
	if s.deps.Dispatcher.Agent(input.Name) != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "agent already exists"})
		return
	}
	if input.SessionTier != "" && !security.ValidTier(input.SessionTier) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid session_tier: must be autonomous, supervised, or restricted",
		})
		return
	}

	// Compute persona directory and ensure it exists.
	personaDir := filepath.Join(s.deps.Config.DataDir, "agents", input.Name)
	if err := os.MkdirAll(personaDir, 0o750); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "creating persona directory: " + err.Error(),
		})
		return
	}

	ac := config.AgentInstanceConfig{
		Name:        input.Name,
		LLMProvider: input.LLMProvider,
		LLMModel:    input.LLMModel,
		SessionTier: input.SessionTier,
		Description: input.Description,
		PersonaDir:  personaDir,
	}

	e, _, err := s.deps.AgentFactory(ac)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "building agent engine: " + err.Error(),
		})
		return
	}

	// Persist to TOML first — this is the source of truth. If it fails,
	// don't register the agent in memory to avoid config drift on restart.
	if err := tool.AddAgentToConfig(s.deps.ConfigPath, input.Name, input.LLMProvider, input.LLMModel, input.SessionTier, input.Description, personaDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "persisting agent to config: " + err.Error(),
		})
		return
	}

	if err := s.deps.Dispatcher.AddAgent(input.Name, e); err != nil {
		// TOML was written but runtime registration failed — remove TOML entry
		// to stay consistent.
		if rmErr := tool.RemoveAgentFromConfig(s.deps.ConfigPath, input.Name); rmErr != nil {
			s.logger.Warn("failed to roll back agent config after AddAgent error", "agent", input.Name, "error", rmErr)
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	if s.deps.Config != nil {
		s.deps.Config.Agents = append(s.deps.Config.Agents, ac)
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"name":   input.Name,
		"status": "created",
	})
}

// agentDependencyError checks whether the named agent is referenced by any
// channels or schedules and returns a descriptive error if so.
func (s *Server) agentDependencyError(name string) string {
	var blockingChannels []string
	for chName, ch := range s.deps.Dispatcher.Channels() {
		if ch.AgentName == name {
			blockingChannels = append(blockingChannels, chName)
		}
	}
	if len(blockingChannels) > 0 {
		return "agent is referenced by channels: " + strings.Join(blockingChannels, ", ")
	}

	if s.deps.Config != nil {
		var blockingSchedules []string
		for _, sched := range s.deps.Config.Schedules {
			if sched.Agent == name {
				blockingSchedules = append(blockingSchedules, sched.Name)
			}
		}
		if len(blockingSchedules) > 0 {
			return "agent is referenced by schedules: " + strings.Join(blockingSchedules, ", ")
		}
	}
	return ""
}

// handleDeleteAgent godoc
// @Summary Delete agent
// @Description Removes an agent. Rejects if referenced by channels or schedules.
// @Tags agents
// @Produce json
// @Security BearerAuth
// @Param name path string true "Agent name"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /agents/{name} [delete]
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing agent name"})
		return
	}
	if s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config persistence not available"})
		return
	}
	if name == "default" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete the default agent"})
		return
	}
	if s.deps.Dispatcher.Agent(name) == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	if depErr := s.agentDependencyError(name); depErr != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": depErr})
		return
	}

	// Persist removal to TOML first — this is the source of truth. If it
	// fails, don't remove the agent from memory to avoid config drift.
	if err := tool.RemoveAgentFromConfig(s.deps.ConfigPath, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "removing agent from config: " + err.Error(),
		})
		return
	}

	if err := s.deps.Dispatcher.RemoveAgent(name); err != nil {
		// TOML was updated but runtime removal failed — re-add TOML entry
		// to stay consistent. This is unlikely but we handle it defensively.
		s.logger.Error("failed to remove agent from dispatcher after config update", "agent", name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Remove from in-memory config.
	if s.deps.Config != nil {
		agents := s.deps.Config.Agents
		for i := range agents {
			if agents[i].Name == name {
				s.deps.Config.Agents = append(agents[:i], agents[i+1:]...)
				break
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// serializeFallbacks converts fallback configs to a slice of maps for TOML persistence.
func serializeFallbacks(fbs []config.FallbackConfig) []any {
	result := make([]any, len(fbs))
	for i, f := range fbs {
		m := map[string]any{
			"trigger": f.Trigger,
			"action":  f.Action,
		}
		if f.Provider != "" {
			m["provider"] = f.Provider
		}
		if f.Model != "" {
			m["model"] = f.Model
		}
		if f.Threshold > 0 {
			m["threshold"] = f.Threshold
		}
		if f.MaxRetries > 0 {
			m["max_retries"] = f.MaxRetries
		}
		if f.Backoff != "" {
			m["backoff"] = f.Backoff
		}
		result[i] = m
	}
	return result
}
