package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// handleGetPersona godoc
// @Summary      Get persona section
// @Description  Returns the content, editability, and agent-mutability of a persona section (identity, soul, user, or memory) for the specified agent.
// @Tags         persona
// @Produce      json
// @Security     BearerAuth
// @Param        name     path      string  true  "Agent name"
// @Param        section  path      string  true  "Persona section name (identity, soul, user, memory)"
// @Success      200  {object}  map[string]interface{}  "section, content, editable, agent_mutable"
// @Failure      400  {object}  map[string]string       "Unknown section"
// @Failure      401  {object}  map[string]string       "Unauthorized"
// @Failure      403  {object}  map[string]string       "Forbidden — requires agents:read scope"
// @Failure      404  {object}  map[string]string       "Agent not found"
// @Router       /agents/{name}/persona/{section} [get]
func (s *Server) handleGetPersona(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	section := r.PathValue("section")

	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	content, editable, agentMutable, ok := e.PersonaSection(section)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("unknown section %q, must be one of: identity, soul, user, memory", section),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"section":       section,
		"content":       content,
		"editable":      editable,
		"agent_mutable": agentMutable,
	})
}

// handleUpdatePersona godoc
// @Summary      Update persona section
// @Description  Writes new content to a persona section (identity, soul, user, or memory) for the specified agent. The section must be editable.
// @Tags         persona
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        name     path      string             true  "Agent name"
// @Param        section  path      string             true  "Persona section name (identity, soul, user, memory)"
// @Param        body     body      object{content=string}  true  "New section content"
// @Success      200  {object}  map[string]string  "agent, section, status"
// @Failure      400  {object}  map[string]string  "Unknown section or invalid JSON"
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires agents:write scope"
// @Failure      404  {object}  map[string]string  "Agent not found"
// @Failure      500  {object}  map[string]string  "Save failed"
// @Router       /agents/{name}/persona/{section} [put]
func (s *Server) handleUpdatePersona(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	section := r.PathValue("section")

	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	// Validate section name before parsing body.
	if _, _, _, ok := e.PersonaSection(section); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("unknown section %q, must be one of: identity, soul, user, memory", section),
		})
		return
	}

	var input struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if err := e.SavePersonaSection(section, input.Content); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("saving persona section: %v", err)})
		return
	}

	s.logger.Info("persona section updated via API", "agent", agentName, "section", section)
	writeJSON(w, http.StatusOK, map[string]string{
		"agent":   agentName,
		"section": section,
		"status":  "updated",
	})
}
