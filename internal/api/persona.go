package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// handleGetPersona returns the content and editability of a persona section.
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
			"error": fmt.Sprintf("unknown section %q, must be one of: soul, user, memory", section),
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

// handleUpdatePersona writes new content to a persona section.
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
			"error": fmt.Sprintf("unknown section %q, must be one of: soul, user, memory", section),
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
