package configmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pelletier/go-toml/v2"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/skill"
)

// registerTools adds all four Config MCP tools to the server.
func (s *Server) registerTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "skill_create",
		Description: "Create a new skill file for this agent. In supervised mode the creation is submitted for user approval.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name":        {"type": "string",  "description": "Unique skill slug (e.g. send-daily-report)"},
				"description":{"type": "string",  "description": "One-line description of what this skill does"},
				"version":     {"type": "string",  "description": "Semver string, e.g. 1.0.0"},
				"triggers":    {"type": "array", "items": {"type": "string"}, "description": "Trigger strings, e.g. [\"command:skill-name\"]"},
				"body":        {"type": "string",  "description": "Markdown body — the skill instructions"}
			},
			"required": ["name", "body"]
		}`),
	}, s.handleSkillCreate)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "skill_list",
		Description: "Return the list of skills currently loaded for this agent.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}, s.handleSkillList)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "schedule_add",
		Description: "Register a new recurring schedule for this agent. In supervised mode it is submitted for user approval.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name":         {"type": "string",  "description": "Unique schedule identifier"},
				"schedule":     {"type": "string",  "description": "Timing expression: @daily, @every 5m, or 5-field cron"},
				"skill":        {"type": "string",  "description": "Skill name to invoke when the schedule fires"},
				"channel":      {"type": "string",  "description": "Delivery channel in adapter:externalID format"},
				"session_mode": {"type": "string",  "description": "shared or isolated (default: isolated)"},
				"session_tier": {"type": "string",  "description": "Permission tier override for this schedule"},
				"tags":         {"type": "array", "items": {"type": "string"}, "description": "Freeform labels"},
				"enabled":      {"type": "boolean", "description": "Whether to start immediately (default: true)"}
			},
			"required": ["name", "schedule", "channel"]
		}`),
	}, s.handleScheduleAdd)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "schedule_list",
		Description: "Return all registered agent schedules.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}, s.handleScheduleList)

	// Tool & plugin management tools.
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "tool_list",
		Description: "List all MCP tools currently available to you, grouped by server.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}, s.handleToolList)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "tool_add",
		Description: "Add a new MCP tool server. The server process will be started immediately.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name":    {"type": "string",  "description": "Unique name for the tool (used as [tools.<name>] in config)"},
				"command": {"type": "string",  "description": "Path to the MCP server binary"},
				"args":    {"type": "array", "items": {"type": "string"}, "description": "Command-line arguments"},
				"env":     {"type": "object", "additionalProperties": {"type": "string"}, "description": "Environment variables"}
			},
			"required": ["name", "command"]
		}`),
	}, s.handleToolAdd)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "tool_remove",
		Description: "Remove an MCP tool server. The server process will be stopped immediately.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "Name of the tool to remove"}
			},
			"required": ["name"]
		}`),
	}, s.handleToolRemove)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "plugin_list",
		Description: "List all plugins and their status.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}, s.handlePluginList)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "plugin_add",
		Description: "Add a new plugin. Subprocess plugins execute directly; Docker plugins run in a container.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name":         {"type": "string"},
				"type":         {"type": "string", "enum": ["subprocess", "docker"]},
				"command":      {"type": "string", "description": "Binary path (subprocess) or entrypoint override (docker)"},
				"image":        {"type": "string", "description": "Docker image (required for docker type)"},
				"args":         {"type": "array", "items": {"type": "string"}},
				"env":          {"type": "object", "additionalProperties": {"type": "string"}},
				"capabilities": {"type": "array", "items": {"type": "string"}, "description": "e.g. ['tools']"},
				"memory_limit": {"type": "string", "description": "Docker memory limit (e.g. '256m')"},
				"cpu_limit":    {"type": "string", "description": "Docker CPU limit (e.g. '0.5')"},
				"network":      {"type": "string", "description": "Docker network mode (default: 'none')"}
			},
			"required": ["name", "type"]
		}`),
	}, s.handlePluginAdd)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "plugin_remove",
		Description: "Remove a plugin. The process or container will be stopped.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "Name of the plugin to remove"}
			},
			"required": ["name"]
		}`),
	}, s.handlePluginRemove)

	// Schedule update tool.
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "schedule_update",
		Description: "Update an existing schedule's properties. Only provided fields are changed; omitted fields keep their current values.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name":         {"type": "string",  "description": "Name of the schedule to update"},
				"schedule":     {"type": "string",  "description": "New timing expression"},
				"skill":        {"type": "string",  "description": "New skill name"},
				"channel":      {"type": "string",  "description": "New delivery channel (adapter:externalID)"},
				"session_mode": {"type": "string",  "description": "shared or isolated"},
				"session_tier": {"type": "string",  "description": "Permission tier override"},
				"tags":         {"type": "array", "items": {"type": "string"}, "description": "New tag list (replaces existing)"},
				"enabled":      {"type": "boolean", "description": "Enable or disable the schedule"}
			},
			"required": ["name"]
		}`),
	}, s.handleScheduleUpdate)

	// Fallback configuration tool.
	if s.deps.SetFallbacks != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "set_fallback",
			Description: "Replace the LLM router's fallback rules. Pass an empty array to clear all rules.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"rules": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"trigger":     {"type": "string",  "enum": ["error", "rate_limit", "low_funds"]},
								"action":      {"type": "string",  "enum": ["switch_provider", "switch_model", "wait_and_retry"]},
								"provider":    {"type": "string",  "description": "Target provider (for switch_provider)"},
								"model":       {"type": "string",  "description": "Target model (for switch_model)"},
								"threshold":   {"type": "number",  "description": "USD remaining (for low_funds)"},
								"max_retries": {"type": "integer", "description": "Retry count (for wait_and_retry)"},
								"backoff":     {"type": "string",  "enum": ["exponential", "constant"]}
							},
							"required": ["trigger", "action"]
						},
						"description": "Ordered list of fallback rules"
					}
				},
				"required": ["rules"]
			}`),
		}, s.handleSetFallback)
	}

	// Cost summary tool.
	if s.deps.CostSummary != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "get_cost_summary",
			Description: "Return current cost tracking data: global cost, per-session costs, and budget limit.",
			InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		}, s.handleGetCostSummary)
	}

	// KV store tools (registered only when a KVStore is provided).
	if s.deps.KVStore != nil {
		s.registerKVTools()
	}
}

// --------------------------------------------------------------------------
// Handlers
// --------------------------------------------------------------------------

func (s *Server) handleSkillCreate(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.AgentSkillsDir == "" {
		return toolError("skill_create is not available: no agent skills directory configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("skill_create is not available in restricted mode"), nil
	}

	var input struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Version     string   `json:"version"`
		Triggers    []string `json:"triggers"`
		Body        string   `json:"body"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}
	if strings.TrimSpace(input.Body) == "" {
		return toolError("body is required"), nil
	}

	version := input.Version
	if version == "" {
		version = "1.0.0"
	}

	payload := buildSkillPayload(input.Name, input.Description, version, input.Triggers, input.Body)

	deps := s.deps
	applyFn := approval.ActionFunc(func(_ context.Context, p string) error {
		return applySkillCreate(deps.AgentSkillsDir, deps.AppendSkill, deps.Logger, p)
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindCreateSkill,
		"Create new skill: "+input.Name, payload, applyFn)
}

func (s *Server) handleSkillList(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	type skillSummary struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Version     string   `json:"version"`
		Triggers    []string `json:"triggers"`
	}

	skills := s.deps.GetSkills()
	summaries := make([]skillSummary, len(skills))
	for i, sk := range skills {
		summaries[i] = skillSummary{
			Name:        sk.Name,
			Description: sk.Description,
			Version:     sk.Version,
			Triggers:    sk.Triggers,
		}
	}

	data, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return toolError("marshaling skills: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}

func (s *Server) handleScheduleAdd(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.Sched == nil {
		return toolError("schedule_add is not available: no scheduler configured"), nil
	}
	if s.deps.HandleMessage == nil {
		return toolError("schedule_add is not available: no message handler configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("schedule_add is not available in restricted mode"), nil
	}

	var input struct {
		Name        string   `json:"name"`
		Schedule    string   `json:"schedule"`
		Skill       string   `json:"skill"`
		Channel     string   `json:"channel"`
		SessionMode string   `json:"session_mode"`
		SessionTier string   `json:"session_tier"`
		Tags        []string `json:"tags"`
		Enabled     *bool    `json:"enabled"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}
	if strings.TrimSpace(input.Schedule) == "" {
		return toolError("schedule is required"), nil
	}
	if strings.TrimSpace(input.Channel) == "" {
		return toolError("channel is required"), nil
	}
	if err := scheduler.ValidateExpr(input.Schedule); err != nil {
		return toolError("invalid schedule expression: " + err.Error()), nil
	}

	colonIdx := strings.IndexByte(input.Channel, ':')
	if colonIdx <= 0 || colonIdx == len(input.Channel)-1 {
		return toolError(fmt.Sprintf("channel %q is not in adapter:externalID format", input.Channel)), nil
	}

	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	sessionMode := input.SessionMode
	if sessionMode == "" {
		sessionMode = "isolated"
	}

	payload, err := buildSchedulePayload(input.Name, input.Schedule, input.Skill,
		input.Channel, sessionMode, input.SessionTier, input.Tags, enabled)
	if err != nil {
		return toolError("building schedule payload: " + err.Error()), nil
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

	schedRef := s.deps.Sched
	handleMsg := s.deps.HandleMessage
	logger := s.deps.Logger

	applyFn := approval.ActionFunc(func(_ context.Context, _ string) error {
		return schedRef.RegisterAndStart(cfg, buildScheduleJob(cfg, handleMsg, logger))
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindModifySchedule,
		"Add schedule: "+input.Name+" ("+input.Schedule+")", payload, applyFn)
}

func (s *Server) handleScheduleList(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.Sched == nil {
		return toolText("[]"), nil
	}

	type entrySummary struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Schedule    string `json:"schedule"`
		Skill       string `json:"skill,omitempty"`
		Channel     string `json:"channel,omitempty"`
		SessionMode string `json:"session_mode,omitempty"`
		Enabled     bool   `json:"enabled"`
	}

	entries := s.deps.Sched.AgentEntries()
	summaries := make([]entrySummary, len(entries))
	for i, e := range entries {
		summaries[i] = entrySummary{
			Name:        e.Name,
			Type:        string(e.Type),
			Schedule:    e.Expr,
			Skill:       e.Skill,
			Channel:     e.Channel,
			SessionMode: e.SessionMode,
			Enabled:     e.Enabled,
		}
	}

	data, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return toolError("marshaling schedules: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}

// scheduleUpdateInput holds the parsed arguments for schedule_update.
type scheduleUpdateInput struct {
	Name        string   `json:"name"`
	Schedule    *string  `json:"schedule"`
	Skill       *string  `json:"skill"`
	Channel     *string  `json:"channel"`
	SessionMode *string  `json:"session_mode"`
	SessionTier *string  `json:"session_tier"`
	Tags        []string `json:"tags"`
	Enabled     *bool    `json:"enabled"`
}

// mergeScheduleUpdate applies partial updates from input onto an existing entry
// and returns the merged scheduler.Config plus the channel parts. Returns an
// error string if validation fails.
func mergeScheduleUpdate(existing scheduler.Entry, input scheduleUpdateInput) (scheduler.Config, string) {
	expr := existing.Expr
	if input.Schedule != nil {
		expr = *input.Schedule
	}
	if err := scheduler.ValidateExpr(expr); err != nil {
		return scheduler.Config{}, "invalid schedule expression: " + err.Error()
	}
	skill := existing.Skill
	if input.Skill != nil {
		skill = *input.Skill
	}
	channel := existing.Channel
	if input.Channel != nil {
		channel = *input.Channel
	}
	colonIdx := strings.IndexByte(channel, ':')
	if colonIdx <= 0 || colonIdx == len(channel)-1 {
		return scheduler.Config{}, fmt.Sprintf("channel %q is not in adapter:externalID format", channel)
	}
	sessionMode := existing.SessionMode
	if input.SessionMode != nil {
		sessionMode = *input.SessionMode
	}
	sessionTier := existing.SessionTier
	if input.SessionTier != nil {
		sessionTier = *input.SessionTier
	}
	enabled := existing.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	tags := existing.Tags
	if input.Tags != nil {
		tags = input.Tags
	}

	return scheduler.Config{
		Name:        input.Name,
		Type:        string(scheduler.ScheduleTypeAgent),
		Schedule:    expr,
		Skill:       skill,
		SessionTier: sessionTier,
		SessionMode: sessionMode,
		Channel:     channel,
		Tags:        tags,
		Enabled:     enabled,
	}, ""
}

func (s *Server) handleScheduleUpdate(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.Sched == nil {
		return toolError("schedule_update is not available: no scheduler configured"), nil
	}
	if s.deps.HandleMessage == nil {
		return toolError("schedule_update is not available: no message handler configured"), nil
	}
	if s.deps.PermissionTier() == "restricted" {
		return toolError("schedule_update is not available in restricted mode"), nil
	}

	var input scheduleUpdateInput
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}

	existing, ok := s.deps.Sched.GetEntry(input.Name)
	if !ok {
		return toolError(fmt.Sprintf("schedule %q not found", input.Name)), nil
	}

	cfg, errMsg := mergeScheduleUpdate(existing, input)
	if errMsg != "" {
		return toolError(errMsg), nil
	}

	payload, err := buildSchedulePayload(cfg.Name, cfg.Schedule, cfg.Skill,
		cfg.Channel, cfg.SessionMode, cfg.SessionTier, cfg.Tags, cfg.Enabled)
	if err != nil {
		return toolError("building schedule payload: " + err.Error()), nil
	}

	schedRef := s.deps.Sched
	handleMsg := s.deps.HandleMessage
	logger := s.deps.Logger

	applyFn := approval.ActionFunc(func(_ context.Context, _ string) error {
		if err := schedRef.Unregister(input.Name); err != nil {
			return fmt.Errorf("unregistering old schedule: %w", err)
		}
		return schedRef.RegisterAndStart(cfg, buildScheduleJob(cfg, handleMsg, logger))
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindModifySchedule,
		"Update schedule: "+input.Name, payload, applyFn)
}

func (s *Server) handleSetFallback(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("set_fallback is not available in restricted mode"), nil
	}

	var input struct {
		Rules []FallbackRuleInput `json:"rules"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}

	// Validate rules.
	for i, r := range input.Rules {
		switch r.Trigger {
		case "error", "rate_limit", "low_funds":
		default:
			return toolError(fmt.Sprintf("rules[%d]: trigger must be error, rate_limit, or low_funds", i)), nil
		}
		switch r.Action {
		case "switch_provider", "switch_model", "wait_and_retry":
		default:
			return toolError(fmt.Sprintf("rules[%d]: action must be switch_provider, switch_model, or wait_and_retry", i)), nil
		}
	}

	payload, _ := json.Marshal(input.Rules)

	setFn := s.deps.SetFallbacks
	rules := input.Rules
	applyFn := approval.ActionFunc(func(_ context.Context, _ string) error {
		setFn(rules)
		return nil
	})

	summary := fmt.Sprintf("Set %d fallback rule(s)", len(input.Rules))
	return applyOrSubmit(ctx, s.deps, approval.ActionKindModifyConfig,
		summary, string(payload), applyFn)
}

func (s *Server) handleGetCostSummary(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	data := s.deps.CostSummary()
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return toolError("marshaling cost summary: " + err.Error()), nil
	}
	return toolText(string(out)), nil
}

// --------------------------------------------------------------------------
// Tool & plugin handlers
// --------------------------------------------------------------------------

func (s *Server) handleToolList(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.LifecycleMgr == nil {
		return toolText("[]"), nil
	}

	tools := s.deps.LifecycleMgr.ListTools()
	data, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		return toolError("marshaling tools: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}

func (s *Server) handleToolAdd(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.LifecycleMgr == nil {
		return toolError("tool_add is not available: no lifecycle manager configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("tool_add is not available in restricted mode"), nil
	}

	var input struct {
		Name    string            `json:"name"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}
	if strings.TrimSpace(input.Command) == "" {
		return toolError("command is required"), nil
	}

	cfg := config.ToolConfig{
		Command: input.Command,
		Args:    input.Args,
		Env:     input.Env,
	}

	lm := s.deps.LifecycleMgr
	summary := fmt.Sprintf("Install tool: %s (%s)", input.Name, input.Command)

	payload, _ := toml.Marshal(map[string]any{
		"name":    input.Name,
		"command": input.Command,
		"args":    input.Args,
		"env":     input.Env,
	})

	applyFn := approval.ActionFunc(func(ctx context.Context, _ string) error {
		return lm.AddTool(ctx, input.Name, cfg)
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindInstallTool,
		summary, strings.TrimSpace(string(payload)), applyFn)
}

func (s *Server) handleToolRemove(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.LifecycleMgr == nil {
		return toolError("tool_remove is not available: no lifecycle manager configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("tool_remove is not available in restricted mode"), nil
	}

	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}

	lm := s.deps.LifecycleMgr
	summary := "Remove tool: " + input.Name

	applyFn := approval.ActionFunc(func(ctx context.Context, _ string) error {
		return lm.RemoveTool(ctx, input.Name)
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindInstallTool,
		summary, input.Name, applyFn)
}

func (s *Server) handlePluginList(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.LifecycleMgr == nil {
		return toolText("[]"), nil
	}

	plugins := s.deps.LifecycleMgr.ListPlugins()
	data, err := json.MarshalIndent(plugins, "", "  ")
	if err != nil {
		return toolError("marshaling plugins: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}

func (s *Server) handlePluginAdd(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.LifecycleMgr == nil {
		return toolError("plugin_add is not available: no lifecycle manager configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("plugin_add is not available in restricted mode"), nil
	}

	var input struct {
		Name         string            `json:"name"`
		Type         string            `json:"type"`
		Command      string            `json:"command"`
		Image        string            `json:"image"`
		Args         []string          `json:"args"`
		Env          map[string]string `json:"env"`
		Capabilities []string          `json:"capabilities"`
		MemoryLimit  string            `json:"memory_limit"`
		CPULimit     string            `json:"cpu_limit"`
		Network      string            `json:"network"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}
	if input.Type != "subprocess" && input.Type != "docker" {
		return toolError("type must be \"subprocess\" or \"docker\""), nil
	}

	cfg := config.PluginConfig{
		Type:         input.Type,
		Command:      input.Command,
		Image:        input.Image,
		Args:         input.Args,
		Env:          input.Env,
		Capabilities: input.Capabilities,
		MemoryLimit:  input.MemoryLimit,
		CPULimit:     input.CPULimit,
		Network:      input.Network,
	}

	lm := s.deps.LifecycleMgr
	summary := fmt.Sprintf("Install plugin: %s (%s)", input.Name, input.Type)

	payload, _ := toml.Marshal(map[string]any{
		"name": input.Name,
		"type": input.Type,
	})

	applyFn := approval.ActionFunc(func(ctx context.Context, _ string) error {
		return lm.AddPlugin(ctx, input.Name, cfg)
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindInstallTool,
		summary, strings.TrimSpace(string(payload)), applyFn)
}

func (s *Server) handlePluginRemove(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.LifecycleMgr == nil {
		return toolError("plugin_remove is not available: no lifecycle manager configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("plugin_remove is not available in restricted mode"), nil
	}

	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}

	lm := s.deps.LifecycleMgr
	summary := "Remove plugin: " + input.Name

	applyFn := approval.ActionFunc(func(ctx context.Context, _ string) error {
		return lm.RemovePlugin(ctx, input.Name)
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindInstallTool,
		summary, input.Name, applyFn)
}

// --------------------------------------------------------------------------
// Shared helpers
// --------------------------------------------------------------------------

// applyOrSubmit either executes fn immediately (autonomous tier) or submits
// it to the approval manager (supervised tier). Returns a text tool result.
func applyOrSubmit(
	ctx context.Context,
	deps Deps,
	kind approval.ActionKind,
	summary string,
	payload string,
	fn approval.ActionFunc,
) (*mcp.CallToolResult, error) {
	switch deps.PermissionTier() {
	case "autonomous":
		if err := fn(ctx, payload); err != nil {
			return toolError(fmt.Sprintf("action failed: %v", err)), nil
		}
		return toolText("Done: " + summary), nil

	case "supervised":
		if deps.Approvals == nil {
			// No manager wired — fall back to immediate execution.
			if err := fn(ctx, payload); err != nil {
				return toolError(fmt.Sprintf("action failed: %v", err)), nil
			}
			return toolText("Done: " + summary), nil
		}
		_, submitErr := deps.Approvals.Submit(
			ctx,
			deps.AgentName,
			kind,
			summary,
			payload,
			"", // externalID — unknown at tool-call time; approval can be resolved via API
			"", // adapterName
			"", // conversationID
			fn,
		)
		if submitErr != nil {
			return toolError(fmt.Sprintf("approval submit failed: %v", submitErr)), nil
		}
		return toolText("Submitted for approval: " + summary), nil

	default:
		return toolError("action not permitted in current permission tier"), nil
	}
}

// applySkillCreate writes the skill file to disk and appends it to the
// in-memory skill list.
func applySkillCreate(agentSkillsDir string, appendSkill func(skill.Skill), logger interface {
	Info(string, ...any)
}, payload string) error {
	s, err := skill.ParseFile("(runtime)", []byte(payload))
	if err != nil {
		return fmt.Errorf("parsing skill: %w", err)
	}
	if s.Name == "" {
		return fmt.Errorf("skill name is required")
	}

	if err := os.MkdirAll(agentSkillsDir, 0750); err != nil {
		return fmt.Errorf("creating skills directory: %w", err)
	}

	filename := filepath.Join(agentSkillsDir, s.Name+".md")
	tmp := filename + ".tmp"
	if err := os.WriteFile(tmp, []byte(payload+"\n"), 0600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing skill file: %w", err)
	}
	if err := os.Rename(tmp, filename); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("committing skill file: %w", err)
	}

	appendSkill(*s)
	logger.Info("skill created via config MCP", "name", s.Name, "file", filename)
	return nil
}

// buildSkillPayload constructs the canonical +++ frontmatter + body format.
func buildSkillPayload(name, description, version string, triggers []string, body string) string {
	type fm struct {
		Name        string   `toml:"name"`
		Description string   `toml:"description,omitempty"`
		Version     string   `toml:"version,omitempty"`
		Triggers    []string `toml:"triggers,omitempty"`
	}
	data, _ := toml.Marshal(fm{
		Name:        name,
		Description: description,
		Version:     version,
		Triggers:    triggers,
	})
	return "+++\n" + strings.TrimSpace(string(data)) + "\n+++\n\n" + strings.TrimSpace(body)
}

// buildSchedulePayload marshals the schedule config to TOML for storage as
// approval payload.
func buildSchedulePayload(name, schedule, skillName, channel, sessionMode, sessionTier string, tags []string, enabled bool) (string, error) {
	type payload struct {
		Name        string   `toml:"name"`
		Schedule    string   `toml:"schedule"`
		Skill       string   `toml:"skill,omitempty"`
		Channel     string   `toml:"channel"`
		SessionMode string   `toml:"session_mode,omitempty"`
		SessionTier string   `toml:"session_tier,omitempty"`
		Tags        []string `toml:"tags,omitempty"`
		Enabled     bool     `toml:"enabled"`
	}
	data, err := toml.Marshal(payload{
		Name:        name,
		Schedule:    schedule,
		Skill:       skillName,
		Channel:     channel,
		SessionMode: sessionMode,
		SessionTier: sessionTier,
		Tags:        tags,
		Enabled:     enabled,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// buildScheduleJob returns a JobFunc that dispatches a message when the
// schedule fires. Used by both schedule_add and schedule_update.
func buildScheduleJob(cfg scheduler.Config, handleMsg func(context.Context, adapter.IncomingMessage) error, logger *slog.Logger) scheduler.JobFunc {
	colonIdx := strings.IndexByte(cfg.Channel, ':')
	adapterName := cfg.Channel[:colonIdx]
	externalID := cfg.Channel[colonIdx+1:]

	text := "[Scheduled trigger: " + cfg.Name + "]"
	if cfg.Skill != "" {
		text = "[Scheduled: " + cfg.Skill + "]"
	}

	baseMsg := adapter.IncomingMessage{
		Adapter:     adapterName,
		ExternalID:  externalID,
		UserName:    "scheduler",
		Text:        text,
		SkillName:   cfg.Skill,
		SessionTier: cfg.SessionTier,
	}

	return func(entry scheduler.Entry) {
		msg := baseMsg
		if entry.SessionMode == "isolated" {
			msg.ConversationID = fmt.Sprintf("sched:%s:%d", entry.Name, entry.LastRun.UnixNano())
		}
		if err := handleMsg(context.Background(), msg); err != nil {
			logger.Error("scheduled job failed", "name", entry.Name, "error", err)
		}
	}
}

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}
