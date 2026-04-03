package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Temikus/denkeeper/internal/configmcp"
)

// ---------------------------------------------------------------------------
// Skill CRUD handlers
// ---------------------------------------------------------------------------

func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("agent")
	skillName := r.PathValue("name")

	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	sk, ok := e.GetSkill(skillName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("skill %q not found", skillName)})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":        sk.Name,
		"description": sk.Description,
		"version":     sk.Version,
		"triggers":    sk.Triggers,
		"body":        sk.Body,
		"agent":       agentName,
	})
}

type skillCreateInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Triggers    []string `json:"triggers"`
	Body        string   `json:"body"`
}

func (s *Server) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("agent")

	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "skill management is not available for this agent",
		})
		return
	}

	var input skillCreateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if strings.TrimSpace(input.Body) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body is required"})
		return
	}

	version := input.Version
	if version == "" {
		version = "1.0.0"
	}

	payload := configmcp.BuildSkillPayload(input.Name, input.Description, version, input.Triggers, input.Body)

	if err := configmcp.ApplySkillCreate(skillsDir, e.AppendSkill, s.logger, payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("creating skill: %v", err)})
		return
	}

	s.logger.Info("skill created via API", "agent", agentName, "name", input.Name)
	writeJSON(w, http.StatusCreated, map[string]string{
		"name":   input.Name,
		"agent":  agentName,
		"status": "created",
	})
}

type skillUpdateInput struct {
	Description *string  `json:"description"`
	Version     *string  `json:"version"`
	Triggers    []string `json:"triggers"`
	Body        *string  `json:"body"`
}

func (s *Server) handleUpdateSkill(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("agent")
	skillName := r.PathValue("name")

	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "skill management is not available for this agent",
		})
		return
	}

	existing, ok := e.GetSkill(skillName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("skill %q not found", skillName)})
		return
	}

	var input skillUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	// Merge with existing values.
	description := existing.Description
	if input.Description != nil {
		description = *input.Description
	}
	version := existing.Version
	if input.Version != nil {
		version = *input.Version
	}
	triggers := existing.Triggers
	if input.Triggers != nil {
		triggers = input.Triggers
	}
	body := existing.Body
	if input.Body != nil {
		body = *input.Body
	}

	payload := configmcp.BuildSkillPayload(skillName, description, version, triggers, body)

	if err := configmcp.ApplySkillUpdate(skillsDir, e.UpdateSkill, s.logger, skillName, payload); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("updating skill: %v", err)})
		return
	}

	s.logger.Info("skill updated via API", "agent", agentName, "name", skillName)
	writeJSON(w, http.StatusOK, map[string]string{
		"name":   skillName,
		"agent":  agentName,
		"status": "updated",
	})
}

func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("agent")
	skillName := r.PathValue("name")

	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "skill management is not available for this agent",
		})
		return
	}

	if !e.RemoveSkill(skillName) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("skill %q not found", skillName)})
		return
	}

	filename := filepath.Join(skillsDir, skillName+".md")
	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) { // #nosec G703 -- skill file path from persona_dir config
		s.logger.Error("skill removed from memory but file deletion failed", "name", skillName, "error", err)
	}

	s.logger.Info("skill deleted via API", "agent", agentName, "name", skillName)
	w.WriteHeader(http.StatusNoContent)
}
