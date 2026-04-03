package api

import (
	"encoding/json"
	"net/http"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

// agentConfigUpdateInput holds the mutable fields for PATCH /api/v1/agents/{name}.
type agentConfigUpdateInput struct {
	SessionTier         *string   `json:"session_tier,omitempty"`
	LLMModel            *string   `json:"llm_model,omitempty"`
	Description         *string   `json:"description,omitempty"`
	BrowserURLAllowlist *[]string `json:"browser_url_allowlist,omitempty"`
}

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

	if input.SessionTier != nil && !security.ValidTier(*input.SessionTier) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid session_tier: must be autonomous, supervised, or restricted",
		})
		return
	}

	// Apply runtime changes to the engine.
	if input.SessionTier != nil {
		if err := e.SetPermissionTier(*input.SessionTier); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "setting permission tier: " + err.Error()})
			return
		}
	}
	if input.LLMModel != nil {
		e.SetModel(*input.LLMModel)
	}

	s.persistAgentConfig(name, &input)
	s.updateInMemoryAgentConfig(name, &input)

	writeJSON(w, http.StatusOK, map[string]string{
		"name":   name,
		"status": "updated",
	})
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
	if input.LLMModel != nil {
		changes["llm_model"] = *input.LLMModel
	}
	if input.Description != nil {
		changes["description"] = *input.Description
	}
	if input.BrowserURLAllowlist != nil {
		allowlist := make([]any, len(*input.BrowserURLAllowlist))
		for i, d := range *input.BrowserURLAllowlist {
			allowlist[i] = d
		}
		changes["browser_url_allowlist"] = allowlist
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

func applyAgentFields(ac *config.AgentInstanceConfig, input *agentConfigUpdateInput) {
	if input.SessionTier != nil {
		ac.SessionTier = *input.SessionTier
	}
	if input.LLMModel != nil {
		ac.LLMModel = *input.LLMModel
	}
	if input.Description != nil {
		ac.Description = *input.Description
	}
	if input.BrowserURLAllowlist != nil {
		ac.BrowserURLAllowlist = *input.BrowserURLAllowlist
	}
}
