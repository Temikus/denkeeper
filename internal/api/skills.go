package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skilleffect"
)

// ---------------------------------------------------------------------------
// Skill CRUD handlers
// ---------------------------------------------------------------------------

// skillMaxBytes returns the configured per-skill size cap, or 0 (no limit) when
// no config is wired (e.g. in tests).
func (s *Server) skillMaxBytes() int {
	cfg := s.appConfig()
	if cfg == nil {
		return 0
	}
	return cfg.Skills.MaxBytes
}

// skillWriter returns a writer that journals each mutation before performing
// it, so a skill change made over REST can be reverted. A memory store without
// the revision methods — or none at all — yields an untracked passthrough that
// behaves exactly like the direct write helpers it replaced.
//
// Actor is "user": REST is the operator's surface, as opposed to the agent
// editing its own skills through config MCP.
func (s *Server) skillWriter(agentName string, e *agent.Engine) *skilleffect.Binding {
	store, _ := s.deps.Memory.(skilleffect.Store)
	return skilleffect.New(store, agentName, s.logger).Bind(e, agent.SkillActorUser, s.skillMaxBytes())
}

// handleGetSkill godoc
// @Summary Get a skill by name
// @Description Returns a single skill's full definition including name, description, version, triggers, body, and owning agent. max_tool_rounds and requires_tools are present only when the skill declares them.
// @Tags skills
// @Produce json
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param name path string true "Skill name"
// @Success 200 {object} map[string]any "Skill details"
// @Failure 404 {object} map[string]string "Agent or skill not found"
// @Router /skills/{agent}/{name} [get]
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

	resp := map[string]any{
		"name":        sk.Name,
		"description": sk.Description,
		"version":     sk.Version,
		"triggers":    sk.Triggers,
		"body":        sk.Body,
		"agent":       agentName,
	}
	if len(sk.SubFileNames) > 0 {
		resp["sub_files"] = sk.SubFileNames
	}
	// Present only on skills that declare them, where a read-modify-write must
	// carry them back.
	if sk.MaxToolRounds > 0 {
		resp["max_tool_rounds"] = sk.MaxToolRounds
	}
	if len(sk.Requires.Tools) > 0 {
		resp["requires_tools"] = sk.Requires.Tools
	}
	writeJSON(w, http.StatusOK, resp)
}

type skillCreateInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Triggers    []string `json:"triggers"`
	Body        string   `json:"body"`
	// MaxToolRounds optionally caps tool-call ROUNDS (not calls) for turns this
	// skill drives. 0 = no cap; it only lowers the agent's budget.
	MaxToolRounds int `json:"max_tool_rounds"`
	// RequiresTools declares the tool names this skill depends on (frontmatter
	// [requires] tools).
	RequiresTools []string `json:"requires_tools"`
}

// handleCreateSkill godoc
// @Summary Create a new skill
// @Description Creates a skill for the specified agent. Writes the skill file to the agent's persona directory and registers it in memory.
// @Tags skills
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param body body skillCreateInput true "Skill definition"
// @Success 201 {object} map[string]string "Skill created"
// @Failure 400 {object} map[string]string "Invalid input"
// @Failure 404 {object} map[string]string "Agent not found"
// @Failure 500 {object} map[string]string "Creation failed"
// @Failure 503 {object} map[string]string "Skill management unavailable"
// @Router /skills/{agent} [post]
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

	payload := configmcp.BuildSkillPayload(input.Name, input.Description, version, input.Triggers, input.Body, input.MaxToolRounds, input.RequiresTools)

	if err := s.skillWriter(agentName, e).Create(r.Context(), payload); err != nil {
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
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Version     *string  `json:"version"`
	Triggers    []string `json:"triggers"`
	Body        *string  `json:"body"`
	// MaxToolRounds sets the tool-call ROUND cap; 0 removes it. Omitted keeps
	// whatever the skill already carries.
	MaxToolRounds *int `json:"max_tool_rounds"`
	// RequiresTools replaces the declared tool set; [] clears it. Omitted keeps
	// whatever the skill already carries.
	RequiresTools []string `json:"requires_tools"`
}

// handleUpdateSkill godoc
// @Summary Update or rename a skill
// @Description Updates a skill's fields for the specified agent. If the name field differs from the path parameter, the skill is renamed (old file removed, new file created). Returns 409 if the new name conflicts with an existing skill.
// @Tags skills
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param name path string true "Current skill name"
// @Param body body skillUpdateInput true "Fields to update (all optional; omit to keep current value)"
// @Success 200 {object} map[string]string "Skill updated"
// @Failure 400 {object} map[string]string "Invalid input"
// @Failure 404 {object} map[string]string "Agent or skill not found"
// @Failure 409 {object} map[string]string "New name conflicts with existing skill"
// @Failure 500 {object} map[string]string "Update failed"
// @Failure 503 {object} map[string]string "Skill management unavailable"
// @Router /skills/{agent}/{name} [put]
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

	// Determine effective name (rename or keep).
	newName := skillName
	isRename := false
	if input.Name != nil && strings.TrimSpace(*input.Name) != "" && *input.Name != skillName {
		newName = strings.TrimSpace(*input.Name)
		isRename = true
		// Check for conflicts with existing skills.
		if _, exists := e.GetSkill(newName); exists {
			writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("skill %q already exists", newName)})
			return
		}
	}

	payload := configmcp.MergeSkillFields(newName, existing, input.Description, input.Version, input.Triggers, input.Body, input.MaxToolRounds, input.RequiresTools)

	writer := s.skillWriter(agentName, e)
	if isRename {
		if err := writer.Rename(r.Context(), skillName, payload); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("renaming skill: %v", err)})
			return
		}
		s.logger.Info("skill renamed via API", "agent", agentName, "old", skillName, "new", newName)
	} else {
		if err := writer.Update(r.Context(), skillName, payload); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("updating skill: %v", err)})
			return
		}
		s.logger.Info("skill updated via API", "agent", agentName, "name", skillName)
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"name":   newName,
		"agent":  agentName,
		"status": "updated",
	})
}

// handleDeleteSkill godoc
// @Summary Delete a skill
// @Description Removes a skill from the specified agent. Deletes both the in-memory registration and the skill file on disk.
// @Tags skills
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param name path string true "Skill name"
// @Success 204 "Skill deleted"
// @Failure 404 {object} map[string]string "Agent or skill not found"
// @Failure 500 {object} map[string]string "File deletion failed"
// @Failure 503 {object} map[string]string "Skill management unavailable"
// @Router /skills/{agent}/{name} [delete]
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

	if _, ok := e.GetSkill(skillName); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("skill %q not found", skillName)})
		return
	}

	// Disk-first: the writer removes the file before mutating memory, so a real
	// IO error leaves the skill intact in memory and on the next reload
	// (matching create/update, which return 500 and leave state unchanged on
	// persist failure).
	if err := s.skillWriter(agentName, e).Delete(r.Context(), skillName); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("deleting skill file: %v", err)})
		return
	}

	s.logger.Info("skill deleted via API", "agent", agentName, "name", skillName)
	w.WriteHeader(http.StatusNoContent)
}
