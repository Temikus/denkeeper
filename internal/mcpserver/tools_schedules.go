package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type scheduleListInput struct{}

type scheduleCreateInput struct {
	Name        string   `json:"name" jsonschema:"Schedule name"`
	Schedule    string   `json:"schedule" jsonschema:"Cron expression (e.g. '*/5 * * * *')"`
	Agent       string   `json:"agent,omitempty" jsonschema:"Agent name to run the schedule on. Omit for default agent."`
	Skill       string   `json:"skill,omitempty" jsonschema:"Skill to trigger when the schedule fires"`
	Channel     string   `json:"channel" jsonschema:"Target channel in adapter:externalID or @channelname format"`
	SessionMode string   `json:"session_mode,omitempty" jsonschema:"Session mode: isolated (default) or shared"`
	SessionTier string   `json:"session_tier,omitempty" jsonschema:"Permission tier override for this schedule"`
	Tags        []string `json:"tags,omitempty" jsonschema:"Tags for categorization"`
	Enabled     *bool    `json:"enabled,omitempty" jsonschema:"Whether the schedule is enabled (default true)"`
}

type scheduleUpdateInput struct {
	Name        string   `json:"name" jsonschema:"Name of the schedule to update"`
	Schedule    *string  `json:"schedule,omitempty" jsonschema:"New cron expression"`
	Skill       *string  `json:"skill,omitempty" jsonschema:"New skill name"`
	Channel     *string  `json:"channel,omitempty" jsonschema:"New target channel"`
	SessionMode *string  `json:"session_mode,omitempty" jsonschema:"New session mode"`
	SessionTier *string  `json:"session_tier,omitempty" jsonschema:"New permission tier"`
	Agent       *string  `json:"agent,omitempty" jsonschema:"New agent name to run the schedule"`
	Tags        []string `json:"tags,omitempty" jsonschema:"New tags (replaces existing)"`
	Enabled     *bool    `json:"enabled,omitempty" jsonschema:"Enable or disable"`
}

type scheduleDeleteInput struct {
	Name string `json:"name" jsonschema:"Name of the schedule to delete"`
}

func (s *Server) registerScheduleTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "schedule_list",
		Description: "List all schedules with name, cron expression, skill, agent, status, " +
			"and last/next run times. Requires 'schedules:read' scope.",
	}, s.handleScheduleList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "schedule_create",
		Description: "Create a new schedule. Registers it with the scheduler and persists to TOML. " +
			"Requires 'schedules:write' scope.",
	}, s.handleScheduleCreate)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "schedule_update",
		Description: "Update an existing schedule. Supports partial updates — only supplied fields change. " +
			"Requires 'schedules:write' scope.",
	}, s.handleScheduleUpdate)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "schedule_delete",
		Description: "Delete a schedule by name. Unregisters from scheduler and removes from TOML. " +
			"Requires 'schedules:write' scope.",
	}, s.handleScheduleDelete)
}

func (s *Server) handleScheduleList(ctx context.Context, _ *mcp.CallToolRequest, _ scheduleListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "schedules:read"); err != nil {
		return err, nil, nil
	}

	if s.deps.Scheduler == nil {
		return toolError("scheduler not available"), nil, nil
	}

	type schedInfo struct {
		Name    string `json:"name"`
		Expr    string `json:"expression"`
		Skill   string `json:"skill,omitempty"`
		Agent   string `json:"agent,omitempty"`
		Channel string `json:"channel,omitempty"`
		Enabled bool   `json:"enabled"`
		LastRun string `json:"last_run,omitempty"`
		NextRun string `json:"next_run,omitempty"`
	}

	entries := s.deps.Scheduler.AgentEntries()
	result := make([]schedInfo, len(entries))
	for i, e := range entries {
		si := schedInfo{
			Name:    e.Name,
			Expr:    e.Expr,
			Skill:   e.Skill,
			Agent:   e.Agent,
			Channel: e.Channel,
			Enabled: e.Enabled,
		}
		if !e.LastRun.IsZero() {
			si.LastRun = e.LastRun.Format("2006-01-02T15:04:05Z07:00")
		}
		if !e.NextRun.IsZero() {
			si.NextRun = e.NextRun.Format("2006-01-02T15:04:05Z07:00")
		}
		result[i] = si
	}

	r, err := toolJSON(result)
	return r, nil, err
}

func (s *Server) handleScheduleCreate(ctx context.Context, _ *mcp.CallToolRequest, input scheduleCreateInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "schedules:write"); err != nil {
		return err, nil, nil
	}
	if s.deps.Scheduler == nil {
		return toolError("scheduler not available"), nil, nil
	}

	if errMsg := validateScheduleCreateInput(input); errMsg != "" {
		return toolError(errMsg), nil, nil
	}

	agentName := input.Agent
	e := s.resolveEngine(agentName)
	if e == nil {
		return toolError("agent not found: " + agentName), nil, nil
	}
	if agentName == "" {
		agentName = e.Name()
	}

	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	sessionMode := input.SessionMode
	if sessionMode == "" {
		sessionMode = "isolated"
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

	job := configmcp.BuildScheduleJob(cfg, e.HandleMessage, s.deps.Logger,
		s.deps.ChannelResolver, configmcp.BuildScheduleJobOpts{Auditor: s.deps.Auditor})

	if err := s.deps.Scheduler.RegisterAndStart(cfg, job); err != nil {
		return toolError("registering schedule: " + err.Error()), nil, nil
	}

	if s.deps.ConfigPath != "" {
		if err := config.AddScheduleToConfig(s.deps.ConfigPath, cfg.Name, cfg.Schedule,
			cfg.Skill, cfg.Channel, cfg.SessionMode, cfg.SessionTier,
			agentName, cfg.Tags, cfg.Enabled); err != nil {
			s.deps.Logger.Error("schedule created but config persistence failed",
				"name", cfg.Name, "error", err)
		}
	}

	r, jsonErr := toolJSON(map[string]string{
		"status": "created",
		"name":   cfg.Name,
	})
	return r, nil, jsonErr
}

func (s *Server) handleScheduleUpdate(ctx context.Context, _ *mcp.CallToolRequest, input scheduleUpdateInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "schedules:write"); err != nil {
		return err, nil, nil
	}
	if s.deps.Scheduler == nil {
		return toolError("scheduler not available"), nil, nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil, nil
	}

	existing, ok := s.deps.Scheduler.GetEntry(input.Name)
	if !ok {
		return toolError(fmt.Sprintf("schedule %q not found", input.Name)), nil, nil
	}

	merged, errMsg := configmcp.MergeScheduleUpdate(existing, configmcp.ScheduleUpdateInput{
		Name:        input.Name,
		Schedule:    input.Schedule,
		Skill:       input.Skill,
		Channel:     input.Channel,
		SessionMode: input.SessionMode,
		SessionTier: input.SessionTier,
		Agent:       input.Agent,
		Tags:        input.Tags,
		Enabled:     input.Enabled,
	})
	if errMsg != "" {
		return toolError(errMsg), nil, nil
	}

	e := s.resolveEngine(merged.Agent)
	if e == nil {
		return toolError("agent not found: " + merged.Agent), nil, nil
	}

	if err := s.deps.Scheduler.Unregister(input.Name); err != nil {
		return toolError("unregistering old schedule: " + err.Error()), nil, nil
	}

	job := configmcp.BuildScheduleJob(merged, e.HandleMessage, s.deps.Logger,
		s.deps.ChannelResolver, configmcp.BuildScheduleJobOpts{Auditor: s.deps.Auditor})

	if err := s.deps.Scheduler.RegisterAndStart(merged, job); err != nil {
		if rbErr := s.rollbackSchedule(existing, e); rbErr != nil {
			return toolError(fmt.Sprintf("update failed: %v; rollback also failed: %v — schedule %q may be missing", err, rbErr, input.Name)), nil, nil
		}
		return toolError("registering updated schedule (rolled back): " + err.Error()), nil, nil
	}

	if s.deps.ConfigPath != "" {
		if err := config.UpdateScheduleInConfig(s.deps.ConfigPath, merged.Name, merged.Schedule,
			merged.Skill, merged.Channel, merged.SessionMode, merged.SessionTier,
			merged.Agent, merged.Tags, merged.Enabled); err != nil {
			s.deps.Logger.Error("schedule updated but config persistence failed",
				"name", merged.Name, "error", err)
		}
	}

	r, jsonErr := toolJSON(map[string]string{
		"status": "updated",
		"name":   merged.Name,
	})
	return r, nil, jsonErr
}

func (s *Server) handleScheduleDelete(ctx context.Context, _ *mcp.CallToolRequest, input scheduleDeleteInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "schedules:write"); err != nil {
		return err, nil, nil
	}
	if s.deps.Scheduler == nil {
		return toolError("scheduler not available"), nil, nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil, nil
	}

	if _, ok := s.deps.Scheduler.GetEntry(input.Name); !ok {
		return toolError(fmt.Sprintf("schedule %q not found", input.Name)), nil, nil
	}

	if err := s.deps.Scheduler.Unregister(input.Name); err != nil {
		return toolError("unregistering schedule: " + err.Error()), nil, nil
	}

	if s.deps.ConfigPath != "" {
		if err := config.RemoveScheduleFromConfig(s.deps.ConfigPath, input.Name); err != nil {
			s.deps.Logger.Error("schedule deleted but config persistence failed",
				"name", input.Name, "error", err)
		}
	}

	return toolText("schedule deleted: " + input.Name), nil, nil
}

func validateScheduleCreateInput(input scheduleCreateInput) string {
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
			return fmt.Sprintf("channel %q is an invalid channel reference", input.Channel)
		}
	} else if _, _, ok := config.ParseChannel(input.Channel); !ok {
		return fmt.Sprintf("channel %q is not in adapter:externalID or @channelname format", input.Channel)
	}
	return ""
}

func (s *Server) rollbackSchedule(entry scheduler.Entry, e *agent.Engine) error {
	oldCfg := scheduler.Config{
		Name:        entry.Name,
		Type:        string(entry.Type),
		Schedule:    entry.Expr,
		Skill:       entry.Skill,
		Agent:       entry.Agent,
		SessionTier: entry.SessionTier,
		SessionMode: entry.SessionMode,
		Channel:     entry.Channel,
		Tags:        entry.Tags,
		Enabled:     entry.Enabled,
	}
	oldJob := configmcp.BuildScheduleJob(oldCfg, e.HandleMessage, s.deps.Logger,
		s.deps.ChannelResolver, configmcp.BuildScheduleJobOpts{Auditor: s.deps.Auditor})
	if err := s.deps.Scheduler.RegisterAndStart(oldCfg, oldJob); err != nil {
		s.deps.Logger.Error("failed to rollback schedule after update failure",
			"name", entry.Name, "error", err)
		return err
	}
	return nil
}
