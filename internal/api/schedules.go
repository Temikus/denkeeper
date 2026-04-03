package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/tool"
)

// ---------------------------------------------------------------------------
// Schedule CRUD handlers
// ---------------------------------------------------------------------------

type scheduleCreateInput struct {
	Name        string   `json:"name"`
	Schedule    string   `json:"schedule"`
	Skill       string   `json:"skill"`
	Channel     string   `json:"channel"`
	SessionMode string   `json:"session_mode"`
	SessionTier string   `json:"session_tier"`
	Agent       string   `json:"agent"`
	Tags        []string `json:"tags"`
	Enabled     *bool    `json:"enabled"`
}

// validateScheduleInput checks required fields and format of a schedule create request.
func validateScheduleInput(input scheduleCreateInput) string {
	if strings.TrimSpace(input.Name) == "" {
		return "name is required"
	}
	if strings.TrimSpace(input.Schedule) == "" {
		return "schedule is required"
	}
	if strings.TrimSpace(input.Channel) == "" {
		return "channel is required"
	}
	if err := scheduler.ValidateExpr(input.Schedule); err != nil {
		return "invalid schedule expression: " + err.Error()
	}
	colonIdx := strings.IndexByte(input.Channel, ':')
	if colonIdx <= 0 || colonIdx == len(input.Channel)-1 {
		return fmt.Sprintf("channel %q is not in adapter:externalID format", input.Channel)
	}
	return ""
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scheduler == nil || s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "schedule management is not available",
		})
		return
	}

	var input scheduleCreateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if errMsg := validateScheduleInput(input); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	// Defaults.
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	sessionMode := input.SessionMode
	if sessionMode == "" {
		sessionMode = "isolated"
	}
	agentName := input.Agent
	if agentName == "" {
		agentName = "default"
	}

	// Look up the engine so we can wire the schedule job.
	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	cfg := scheduler.Config{
		Name:        input.Name,
		Type:        string(scheduler.ScheduleTypeAgent),
		Schedule:    input.Schedule,
		Skill:       input.Skill,
		SessionTier: input.SessionTier,
		SessionMode: sessionMode,
		Channel:     input.Channel,
		Tags:        input.Tags,
		Enabled:     enabled,
	}

	job := configmcp.BuildScheduleJob(cfg, e.HandleMessage, s.logger)
	if err := s.deps.Scheduler.RegisterAndStart(cfg, job); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	if err := tool.AddScheduleToConfig(s.deps.ConfigPath, cfg.Name, cfg.Schedule,
		cfg.Skill, cfg.Channel, cfg.SessionMode, cfg.SessionTier, agentName,
		cfg.Tags, cfg.Enabled); err != nil {
		s.logger.Error("schedule created but config persistence failed", "name", cfg.Name, "error", err)
	}

	s.logger.Info("schedule created via API", "name", cfg.Name)
	writeJSON(w, http.StatusCreated, map[string]string{
		"name":     cfg.Name,
		"schedule": cfg.Schedule,
		"status":   "created",
	})
}

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scheduler == nil || s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "schedule management is not available",
		})
		return
	}

	name := r.PathValue("name")
	existing, ok := s.deps.Scheduler.GetEntry(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("schedule %q not found", name)})
		return
	}

	var input configmcp.ScheduleUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	input.Name = name

	cfg, errMsg := configmcp.MergeScheduleUpdate(existing, input)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	// Determine the agent for job wiring. The schedule doesn't store agent
	// directly, so we fall back to "default".
	agentName := "default"
	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "default agent not found"})
		return
	}

	if err := s.deps.Scheduler.Unregister(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("unregistering schedule: %v", err)})
		return
	}

	job := configmcp.BuildScheduleJob(cfg, e.HandleMessage, s.logger)
	if err := s.deps.Scheduler.RegisterAndStart(cfg, job); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("re-registering schedule: %v", err)})
		return
	}

	if err := tool.UpdateScheduleInConfig(s.deps.ConfigPath, cfg.Name, cfg.Schedule,
		cfg.Skill, cfg.Channel, cfg.SessionMode, cfg.SessionTier, agentName,
		cfg.Tags, cfg.Enabled); err != nil {
		s.logger.Error("schedule updated but config persistence failed", "name", cfg.Name, "error", err)
	}

	s.logger.Info("schedule updated via API", "name", cfg.Name)
	writeJSON(w, http.StatusOK, map[string]string{
		"name":     cfg.Name,
		"schedule": cfg.Schedule,
		"status":   "updated",
	})
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scheduler == nil || s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "schedule management is not available",
		})
		return
	}

	name := r.PathValue("name")
	if err := s.deps.Scheduler.Unregister(name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("schedule %q not found", name)})
		return
	}

	if err := tool.RemoveScheduleFromConfig(s.deps.ConfigPath, name); err != nil {
		s.logger.Error("schedule deleted but config persistence failed", "name", name, "error", err)
	}

	s.logger.Info("schedule deleted via API", "name", name)
	w.WriteHeader(http.StatusNoContent)
}
