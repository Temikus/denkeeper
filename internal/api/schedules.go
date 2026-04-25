package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
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

// channelResolver returns a ChannelResolver that looks up channels from the
// dispatcher. For broadcast channels, all specific bindings are returned.
func (s *Server) channelResolver() configmcp.ChannelResolver {
	return func(name string) *configmcp.ChannelResolveResult {
		channels := s.deps.Dispatcher.Channels()
		if channels == nil {
			return nil
		}
		ch, found := channels[name]
		if !found {
			return nil
		}
		if ch.IsBroadcast() {
			bindings := ch.ResolveAllBindings()
			if len(bindings) == 0 {
				return nil
			}
			return &configmcp.ChannelResolveResult{
				ConversationID: ch.ConversationID(),
				Bindings:       bindings,
				Broadcast:      true,
			}
		}
		adapter, eid, wildcard, ok := ch.ResolveBinding()
		if !ok || wildcard {
			return nil
		}
		return &configmcp.ChannelResolveResult{
			ConversationID: ch.ConversationID(),
			Bindings:       []agent.AdapterBinding{{Adapter: adapter, ExternalID: eid}},
		}
	}
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
	if config.IsChannelRef(input.Channel) {
		if _, ok := config.ParseChannelRef(input.Channel); !ok {
			return fmt.Sprintf("channel %q is an invalid channel reference (use \"@channelname\")", input.Channel)
		}
	} else {
		colonIdx := strings.IndexByte(input.Channel, ':')
		if colonIdx <= 0 || colonIdx == len(input.Channel)-1 {
			return fmt.Sprintf("channel %q is not in adapter:externalID or @channelname format", input.Channel)
		}
	}
	return ""
}

// handleCreateSchedule godoc
// @Summary Create a new schedule
// @Description Creates a scheduled job bound to a channel and optionally a skill. Validates the cron expression, channel reference, and agent/skill existence. Persists the schedule to the TOML config file.
// @Tags schedules
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body scheduleCreateInput true "Schedule configuration"
// @Success 201 {object} map[string]string "Created schedule with name, schedule expression, and status"
// @Failure 400 {object} map[string]string "Validation error (missing fields, invalid cron, unknown skill)"
// @Failure 404 {object} map[string]string "Referenced agent not found"
// @Failure 409 {object} map[string]string "Schedule name already exists"
// @Failure 503 {object} map[string]string "Schedule management not available"
// @Router /schedules [post]
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

	if input.Skill != "" {
		if _, ok := e.GetSkill(input.Skill); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("skill %q not found on agent %q", input.Skill, agentName),
			})
			return
		}
	}

	cfg := scheduler.Config{
		Name:        input.Name,
		Type:        string(scheduler.ScheduleTypeAgent),
		Schedule:    input.Schedule,
		Skill:       input.Skill,
		Agent:       agentName,
		SessionTier: input.SessionTier,
		SessionMode: sessionMode,
		Channel:     input.Channel,
		Tags:        input.Tags,
		Enabled:     enabled,
	}

	job := configmcp.BuildScheduleJob(cfg, e.HandleMessage, s.logger, s.channelResolver(), configmcp.BuildScheduleJobOpts{Auditor: s.deps.Auditor})
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

// handleUpdateSchedule godoc
// @Summary Update an existing schedule
// @Description Partially updates a schedule's configuration. Only provided fields are applied; omitted fields retain their current values. Re-registers the schedule with the scheduler and persists changes to the TOML config file.
// @Tags schedules
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Schedule name"
// @Param body body configmcp.ScheduleUpdateInput true "Fields to update (all optional)"
// @Success 200 {object} map[string]string "Updated schedule with name, schedule expression, and status"
// @Failure 400 {object} map[string]string "Invalid JSON, invalid cron expression, or unknown skill"
// @Failure 404 {object} map[string]string "Schedule or agent not found"
// @Failure 500 {object} map[string]string "Internal error during re-registration"
// @Failure 503 {object} map[string]string "Schedule management not available"
// @Router /schedules/{name} [patch]
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

	agentName := cfg.Agent
	if agentName == "" {
		agentName = "default"
	}
	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	if cfg.Skill != "" {
		if _, ok := e.GetSkill(cfg.Skill); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("skill %q not found on agent %q", cfg.Skill, agentName),
			})
			return
		}
	}

	if err := s.deps.Scheduler.Unregister(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("unregistering schedule: %v", err)})
		return
	}

	job := configmcp.BuildScheduleJob(cfg, e.HandleMessage, s.logger, s.channelResolver(), configmcp.BuildScheduleJobOpts{Auditor: s.deps.Auditor})
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

// handleDeleteSchedule godoc
// @Summary Delete a schedule
// @Description Unregisters a schedule from the scheduler and removes it from the TOML config file.
// @Tags schedules
// @Security BearerAuth
// @Param name path string true "Schedule name"
// @Success 204 "Schedule deleted"
// @Failure 404 {object} map[string]string "Schedule not found"
// @Failure 503 {object} map[string]string "Schedule management not available"
// @Router /schedules/{name} [delete]
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
