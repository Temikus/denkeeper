package configmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pelletier/go-toml/v2"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/skill"
)

// registerTools registers all Config MCP tools. Each optional dependency is
// nil-guarded here so registration helpers stay unconditional.
//
//nolint:gocyclo // dispatcher with one branch per optional dep; not reducible without hurting readability.
func (s *Server) registerTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "skill_create",
		Description: "Create a new skill file for this agent. In supervised mode the tool call requires operator approval.",
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

	if s.deps.GetSkill != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "skill_get",
			Description: "Return the full details of a skill including its body content.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Name of the skill to retrieve"}
				},
				"required": ["name"]
			}`),
		}, s.handleSkillGet)
	}

	if s.deps.UpdateSkill != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "skill_update",
			Description: "Update an existing skill's content. In supervised mode the tool call requires operator approval.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name":        {"type": "string",  "description": "Name of the skill to update (must already exist)"},
					"new_name":    {"type": "string",  "description": "Rename the skill to this name (omit to keep current name)"},
					"description":{"type": "string",  "description": "New description (omit to keep current)"},
					"version":     {"type": "string",  "description": "New version (omit to keep current)"},
					"triggers":    {"type": "array", "items": {"type": "string"}, "description": "New triggers (omit to keep current)"},
					"body":        {"type": "string",  "description": "New markdown body (omit to keep current)"}
				},
				"required": ["name"]
			}`),
		}, s.handleSkillUpdate)
	}

	if s.deps.GetSkill != nil && s.deps.UpdateSkill != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "skill_patch",
			Description: "Find-and-replace in a skill's body. The old_string must match exactly once. More surgical than skill_update for small edits.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name":       {"type": "string", "description": "Name of the skill to patch"},
					"old_string": {"type": "string", "description": "Exact text to find (must match exactly once)"},
					"new_string": {"type": "string", "description": "Replacement text (empty string deletes the match)"}
				},
				"required": ["name", "old_string", "new_string"]
			}`),
		}, s.handleSkillPatch)
	}

	if s.deps.GetSkill != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "skill_read_file",
			Description: "Read a sub-file from a subdirectory-form skill. Only files under references/, templates/, or scripts/ are accessible.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"skill":     {"type": "string", "description": "Name of the skill"},
					"file_path": {"type": "string", "description": "Relative path within the skill directory (e.g. references/oauth.md)"}
				},
				"required": ["skill", "file_path"]
			}`),
		}, s.handleSkillReadFile)
	}

	if s.deps.GetSkill != nil && s.deps.UpdateSkill != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "skill_write_file",
			Description: "Write a sub-file in a subdirectory-form skill. Creates parent directories if needed. Only references/, templates/, or scripts/ are writable. Flat-file skills are auto-converted to subdirectory form.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"skill":     {"type": "string", "description": "Name of the skill"},
					"file_path": {"type": "string", "description": "Relative path within the skill directory (e.g. templates/greeting.txt)"},
					"content":   {"type": "string", "description": "File content to write"}
				},
				"required": ["skill", "file_path", "content"]
			}`),
		}, s.handleSkillWriteFile)
	}

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "schedule_add",
		Description: "Register a new recurring schedule for this agent. In supervised mode the tool call requires operator approval.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name":         {"type": "string",  "description": "Unique schedule identifier"},
				"schedule":     {"type": "string",  "description": "Timing expression: @daily, @every 5m, or 5-field cron"},
				"skill":        {"type": "string",  "description": "Skill name to invoke when the schedule fires"},
				"channel":      {"type": "string",  "description": "Delivery channel in adapter:externalID format (e.g. telegram:387956986, discord:1234567890). Use the channel from your Session Context."},
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

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "skill_delete",
		Description: "Delete an existing skill by name. Removes it from memory and deletes the skill file. In supervised mode the tool call requires operator approval.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "Name of the skill to delete"}
			},
			"required": ["name"]
		}`),
	}, s.handleSkillDelete)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "schedule_delete",
		Description: "Delete an existing schedule by name. Removes it from the scheduler and from the config file. In supervised mode the tool call requires operator approval.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "Name of the schedule to delete"}
			},
			"required": ["name"]
		}`),
	}, s.handleScheduleDelete)

	// Tool & plugin management tools.
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "tool_list",
		Description: "List all MCP tools currently available to you, grouped by server.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}, s.handleToolList)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "tool_add",
		Description: "Add a new MCP tool server. Supports stdio (local subprocess) and sse (remote HTTP) transports.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name":                 {"type": "string",  "description": "Unique name for the tool (used as [tools.<name>] in config)"},
				"transport":            {"type": "string",  "enum": ["stdio", "sse"], "description": "Transport type: stdio (default) for local subprocess, sse for remote HTTP"},
				"command":              {"type": "string",  "description": "Path to the MCP server binary (required for stdio)"},
				"url":                  {"type": "string",  "description": "Remote MCP server URL (required for sse)"},
				"args":                 {"type": "array", "items": {"type": "string"}, "description": "Command-line arguments (stdio only)"},
				"env":                  {"type": "object", "additionalProperties": {"type": "string"}, "description": "Environment variables"},
				"headers":              {"type": "object", "additionalProperties": {"type": "string"}, "description": "HTTP headers for sse transport (supports ${VAR} placeholders)"},
				"request_timeout_secs": {"type": "integer", "description": "Per-server request timeout override in seconds"}
			},
			"required": ["name"]
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
		Name:        "tool_restart",
		Description: "Restart a crashed or disabled MCP tool server. Resets health state and re-connects.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "Name of the tool server to restart"}
			},
			"required": ["name"]
		}`),
	}, s.handleToolRestart)

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
				"channel":      {"type": "string",  "description": "New delivery channel in adapter:externalID format (e.g. telegram:387956986). Use the channel from your Session Context."},
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
								"trigger":     {"type": "string",  "enum": ["error", "rate_limit", "cost_limit", "low_funds"], "description": "low_funds is deprecated and auto-migrates to cost_limit/soft"},
								"action":      {"type": "string",  "enum": ["switch_provider", "switch_model", "wait_and_retry"]},
								"provider":    {"type": "string",  "description": "Target provider (for switch_provider, or scoping switch_model)"},
								"model":       {"type": "string",  "description": "Target model (for switch_model)"},
								"scope":       {"type": "string",  "enum": ["soft", "hard"], "description": "Cost limit scope (for cost_limit)"},
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
		s.registerCostTools()
	}

	// KV store tools (registered only when a KVStore is provided).
	if s.deps.KVStore != nil {
		s.registerKVTools()
	}

	// Browser profile tools (registered only when browser automation is enabled).
	if s.deps.BrowserProfiles != nil {
		s.registerBrowserTools()
	}

	// Persona tools (registered only when persona callbacks are provided).
	if s.deps.GetPersonaSection != nil && s.deps.SavePersonaSection != nil {
		s.registerPersonaTools()
	}

	// Channel tools (registered only when channel access is available).
	if s.deps.GetChannels != nil {
		s.registerChannelTools()
	}

	// Session search (registered only when SearchMessages is provided).
	if s.deps.SearchMessages != nil {
		s.registerSearchTools()
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

	payload := BuildSkillPayload(input.Name, input.Description, version, input.Triggers, input.Body)

	deps := s.deps
	applyFn := approval.ActionFunc(func(_ context.Context, p string) error {
		if err := ApplySkillCreate(deps.AgentSkillsDir, deps.AppendSkill, deps.Logger, p); err != nil {
			return err
		}
		if deps.SetSkillOrigin != nil {
			deps.SetSkillOrigin(deps.AgentName, input.Name, "agent")
		}
		return nil
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindCreateSkill,
		"Create new skill: "+input.Name, payload, applyFn, false)
}

func (s *Server) handleSkillList(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	type skillSummary struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Version     string   `json:"version"`
		Triggers    []string `json:"triggers"`
		SubFiles    []string `json:"sub_files,omitempty"`
	}

	skills := s.deps.GetSkills()
	summaries := make([]skillSummary, len(skills))
	for i, sk := range skills {
		summaries[i] = skillSummary{
			Name:        sk.Name,
			Description: sk.Description,
			Version:     sk.Version,
			Triggers:    sk.Triggers,
			SubFiles:    sk.SubFileNames,
		}
	}

	data, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return toolError("marshaling skills: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}

func (s *Server) handleSkillGet(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}

	sk, ok := s.deps.GetSkill(input.Name)
	if !ok {
		return toolError(fmt.Sprintf("skill %q not found", input.Name)), nil
	}

	if s.deps.BumpSkillView != nil {
		s.deps.BumpSkillView(s.deps.AgentName, input.Name)
	}

	type skillDetail struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Version     string   `json:"version"`
		Triggers    []string `json:"triggers"`
		Body        string   `json:"body"`
		SubFiles    []string `json:"sub_files,omitempty"`
	}

	data, err := json.MarshalIndent(skillDetail{
		Name:        sk.Name,
		Description: sk.Description,
		Version:     sk.Version,
		Triggers:    sk.Triggers,
		Body:        sk.Body,
		SubFiles:    sk.SubFileNames,
	}, "", "  ")
	if err != nil {
		return toolError("marshaling skill: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}

// MergeSkillFields merges optional update fields with existing skill values and
// returns the payload built with the given name.
func MergeSkillFields(name string, existing skill.Skill, desc, ver *string, triggers []string, body *string) string {
	description := existing.Description
	if desc != nil {
		description = *desc
	}
	version := existing.Version
	if ver != nil {
		version = *ver
	}
	trig := existing.Triggers
	if triggers != nil {
		trig = triggers
	}
	b := existing.Body
	if body != nil {
		b = *body
	}
	return BuildSkillPayload(name, description, version, trig, b)
}

func (s *Server) handleSkillUpdate(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.AgentSkillsDir == "" {
		return toolError("skill_update is not available: no agent skills directory configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("skill_update is not available in restricted mode"), nil
	}

	var input struct {
		Name        string   `json:"name"`
		NewName     *string  `json:"new_name"`
		Description *string  `json:"description"`
		Version     *string  `json:"version"`
		Triggers    []string `json:"triggers"`
		Body        *string  `json:"body"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}

	existing, ok := s.deps.GetSkill(input.Name)
	if !ok {
		return toolError(fmt.Sprintf("skill %q not found", input.Name)), nil
	}

	effectiveName, isRename := resolveSkillRename(input.Name, input.NewName)
	if isRename {
		if _, exists := s.deps.GetSkill(effectiveName); exists {
			return toolError(fmt.Sprintf("skill %q already exists", effectiveName)), nil
		}
	}

	payload := MergeSkillFields(effectiveName, existing, input.Description, input.Version, input.Triggers, input.Body)
	applyFn, summary := s.buildSkillUpdateAction(input.Name, effectiveName, isRename, payload)

	return applyOrSubmit(ctx, s.deps, approval.ActionKindUpdateSkill,
		summary, payload, applyFn, false)
}

func resolveSkillRename(name string, newName *string) (effectiveName string, isRename bool) {
	if newName != nil && strings.TrimSpace(*newName) != "" && *newName != name {
		return strings.TrimSpace(*newName), true
	}
	return name, false
}

func (s *Server) buildSkillUpdateAction(oldName, effectiveName string, isRename bool, _ string) (approval.ActionFunc, string) {
	deps := s.deps
	if isRename {
		fn := approval.ActionFunc(func(_ context.Context, p string) error {
			if err := ApplySkillRename(deps.AgentSkillsDir, deps.RemoveSkill, deps.AppendSkill, deps.Logger, oldName, p); err != nil {
				return err
			}
			if deps.BumpSkillPatch != nil {
				deps.BumpSkillPatch(deps.AgentName, effectiveName)
			}
			if deps.NudgeReset != nil {
				deps.NudgeReset("skill")
			}
			return nil
		})
		return fn, fmt.Sprintf("Rename skill: %s → %s", oldName, effectiveName)
	}
	fn := approval.ActionFunc(func(_ context.Context, p string) error {
		if err := ApplySkillUpdate(deps.AgentSkillsDir, deps.UpdateSkill, deps.Logger, oldName, p); err != nil {
			return err
		}
		if deps.BumpSkillPatch != nil {
			deps.BumpSkillPatch(deps.AgentName, oldName)
		}
		if deps.NudgeReset != nil {
			deps.NudgeReset("skill")
		}
		return nil
	})
	return fn, "Update skill: " + oldName
}

func (s *Server) handleSkillPatch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.AgentSkillsDir == "" {
		return toolError("skill_patch is not available: no agent skills directory configured"), nil
	}
	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("skill_patch is not available in restricted mode"), nil
	}

	var input struct {
		Name      string `json:"name"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}
	if input.OldString == "" {
		return toolError("old_string is required"), nil
	}

	sk, ok := s.deps.GetSkill(input.Name)
	if !ok {
		return toolError(fmt.Sprintf("skill %q not found", input.Name)), nil
	}

	if s.deps.IsSkillPinned != nil {
		pinned, err := s.deps.IsSkillPinned(input.Name)
		if err == nil && pinned {
			return toolError(fmt.Sprintf("skill %q is pinned and cannot be patched", input.Name)), nil
		}
	}

	count := strings.Count(sk.Body, input.OldString)
	if count == 0 {
		return toolError("old_string not found in skill body"), nil
	}
	if count > 1 {
		return toolError(fmt.Sprintf("old_string matches %d times; must match exactly once", count)), nil
	}

	newBody := strings.Replace(sk.Body, input.OldString, input.NewString, 1)
	payload := BuildSkillPayload(input.Name, sk.Description, sk.Version, sk.Triggers, newBody)

	deps := s.deps
	skillName := input.Name
	applyFn := approval.ActionFunc(func(_ context.Context, p string) error {
		if err := ApplySkillUpdate(deps.AgentSkillsDir, deps.UpdateSkill, deps.Logger, skillName, p); err != nil {
			return err
		}
		if deps.BumpSkillPatch != nil {
			deps.BumpSkillPatch(deps.AgentName, skillName)
		}
		if deps.NudgeReset != nil {
			deps.NudgeReset("skill")
		}
		return nil
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindUpdateSkill,
		"Patch skill: "+input.Name, payload, applyFn, false)
}

func (s *Server) handleSkillReadFile(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Skill    string `json:"skill"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Skill) == "" {
		return toolError("skill is required"), nil
	}
	if strings.TrimSpace(input.FilePath) == "" {
		return toolError("file_path is required"), nil
	}

	sk, ok := s.deps.GetSkill(input.Skill)
	if !ok {
		return toolError(fmt.Sprintf("skill %q not found", input.Skill)), nil
	}
	if sk.Dir == "" {
		return toolError(fmt.Sprintf("skill %q is a flat-file skill; sub-files are only available for subdirectory-form skills", input.Skill)), nil
	}

	resolved, err := skill.ValidateSubpath(sk.Dir, input.FilePath)
	if err != nil {
		return toolError(err.Error()), nil
	}

	data, err := os.ReadFile(resolved) // #nosec G304 -- path validated by ValidateSubpath
	if err != nil {
		if os.IsNotExist(err) {
			return toolError(fmt.Sprintf("file %q not found in skill %q", input.FilePath, input.Skill)), nil
		}
		return toolError(fmt.Sprintf("reading file: %v", err)), nil
	}

	if s.deps.BumpSkillView != nil {
		s.deps.BumpSkillView(s.deps.AgentName, input.Skill)
	}

	return toolText(string(data)), nil
}

func (s *Server) handleSkillWriteFile(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.AgentSkillsDir == "" {
		return toolError("skill_write_file is not available: no agent skills directory configured"), nil
	}
	if s.deps.PermissionTier() == "restricted" {
		return toolError("skill_write_file is not available in restricted mode"), nil
	}

	var input struct {
		Skill    string `json:"skill"`
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Skill) == "" {
		return toolError("skill is required"), nil
	}
	if strings.TrimSpace(input.FilePath) == "" {
		return toolError("file_path is required"), nil
	}

	sk, ok := s.deps.GetSkill(input.Skill)
	if !ok {
		return toolError(fmt.Sprintf("skill %q not found", input.Skill)), nil
	}

	if s.deps.IsSkillPinned != nil {
		pinned, err := s.deps.IsSkillPinned(input.Skill)
		if err == nil && pinned {
			return toolError(fmt.Sprintf("skill %q is pinned and cannot be modified", input.Skill)), nil
		}
	}

	return s.writeSkillFile(ctx, &sk, input.FilePath, input.Content)
}

func (s *Server) writeSkillFile(ctx context.Context, sk *skill.Skill, filePath, content string) (*mcp.CallToolResult, error) {
	if sk.Dir == "" {
		if err := s.convertFlatToSubdir(sk); err != nil {
			return toolError(fmt.Sprintf("auto-converting to subdirectory form: %v", err)), nil
		}
	}

	resolved, err := skill.ValidateSubpath(sk.Dir, filePath)
	if err != nil {
		return toolError(err.Error()), nil
	}

	parentDir := filepath.Dir(resolved)
	if err := os.MkdirAll(parentDir, 0o750); err != nil {
		return toolError(fmt.Sprintf("creating directory: %v", err)), nil
	}

	tmpPath := resolved + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil { // #nosec G306
		return toolError(fmt.Sprintf("writing file: %v", err)), nil
	}
	if err := os.Rename(tmpPath, resolved); err != nil {
		_ = os.Remove(tmpPath)
		return toolError(fmt.Sprintf("renaming temp file: %v", err)), nil
	}

	sk.SubFileNames = skill.ScanSubFiles(sk.Dir)
	s.deps.UpdateSkill(sk.Name, *sk)

	if s.deps.BumpSkillPatch != nil {
		s.deps.BumpSkillPatch(s.deps.AgentName, sk.Name)
	}

	resp, _ := json.Marshal(map[string]any{
		"ok":        true,
		"file_path": filePath,
		"sub_files": sk.SubFileNames,
	})
	return toolText(string(resp)), nil
}

func (s *Server) convertFlatToSubdir(sk *skill.Skill) error {
	newDir := filepath.Join(s.deps.AgentSkillsDir, sk.Name)
	if err := os.MkdirAll(newDir, 0o750); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}

	oldPath := filepath.Join(s.deps.AgentSkillsDir, sk.Name+".md")
	newPath := filepath.Join(newDir, "SKILL.md")

	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("moving skill file: %w", err)
		}
	}

	sk.Dir = newDir
	sk.Source = newPath
	return nil
}

type scheduleAddInput struct {
	Name        string   `json:"name"`
	Schedule    string   `json:"schedule"`
	Skill       string   `json:"skill"`
	Channel     string   `json:"channel"`
	SessionMode string   `json:"session_mode"`
	SessionTier string   `json:"session_tier"`
	Tags        []string `json:"tags"`
	Enabled     *bool    `json:"enabled"`
}

func parseScheduleAddInput(args json.RawMessage) (scheduleAddInput, string) {
	var input scheduleAddInput
	if err := json.Unmarshal(args, &input); err != nil {
		return input, "invalid arguments: " + err.Error()
	}
	if strings.TrimSpace(input.Name) == "" {
		return input, "name is required"
	}
	if strings.TrimSpace(input.Schedule) == "" {
		return input, "schedule is required"
	}
	if strings.TrimSpace(input.Channel) == "" {
		return input, "channel is required"
	}
	if err := scheduler.ValidateExpr(input.Schedule); err != nil {
		return input, "invalid schedule expression: " + err.Error()
	}
	if config.IsChannelRef(input.Channel) {
		if _, ok := config.ParseChannelRef(input.Channel); !ok {
			return input, fmt.Sprintf("channel %q is an invalid channel reference (use \"@channelname\")", input.Channel)
		}
	} else if _, _, ok := config.ParseChannel(input.Channel); !ok {
		return input, fmt.Sprintf("channel %q is not in adapter:externalID or @channelname format", input.Channel)
	}
	return input, ""
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

	input, errMsg := parseScheduleAddInput(req.Params.Arguments)
	if errMsg != "" {
		return toolError(errMsg), nil
	}

	if input.Skill != "" && s.deps.GetSkill != nil {
		if _, ok := s.deps.GetSkill(input.Skill); !ok {
			return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Skill, s.deps.AgentName)), nil
		}
	}

	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	sessionMode := input.SessionMode
	if sessionMode == "" {
		sessionMode = "isolated"
	}

	payload, err := BuildSchedulePayload(input.Name, input.Schedule, input.Skill,
		input.Channel, sessionMode, input.SessionTier, input.Tags, enabled)
	if err != nil {
		return toolError("building schedule payload: " + err.Error()), nil
	}

	cfg := scheduler.Config{
		Name:        input.Name,
		Type:        string(scheduler.ScheduleTypeAgent),
		Schedule:    input.Schedule,
		Skill:       input.Skill,
		Agent:       s.deps.AgentName,
		SessionTier: input.SessionTier,
		SessionMode: sessionMode,
		Channel:     input.Channel,
		Tags:        input.Tags,
		Enabled:     enabled,
	}

	schedRef := s.deps.Sched
	handleMsg := s.deps.HandleMessage
	logger := s.deps.Logger
	chResolver := s.deps.ChannelResolver

	configPath := s.deps.ConfigPath
	agentName := s.deps.AgentName

	applyFn := approval.ActionFunc(func(_ context.Context, _ string) error {
		if err := schedRef.RegisterAndStart(cfg, BuildScheduleJob(cfg, handleMsg, logger, chResolver, BuildScheduleJobOpts{Auditor: s.deps.Auditor})); err != nil {
			return err
		}
		if configPath != "" {
			return config.AddScheduleToConfig(configPath, cfg.Name, cfg.Schedule,
				cfg.Skill, cfg.Channel, cfg.SessionMode, cfg.SessionTier,
				agentName, cfg.Tags, cfg.Enabled)
		}
		return nil
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindModifySchedule,
		"Add schedule: "+input.Name+" ("+input.Schedule+")", payload, applyFn, false)
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
		Agent       string `json:"agent,omitempty"`
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
			Agent:       e.Agent,
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

// ScheduleUpdateInput holds the parsed arguments for schedule_update.
// ScheduleUpdateInput holds the parsed arguments for schedule_update.
// Exported so the REST API can reuse it.
type ScheduleUpdateInput struct {
	Name        string   `json:"name"`
	Schedule    *string  `json:"schedule"`
	Skill       *string  `json:"skill"`
	Channel     *string  `json:"channel"`
	SessionMode *string  `json:"session_mode"`
	SessionTier *string  `json:"session_tier"`
	Agent       *string  `json:"agent"`
	Tags        []string `json:"tags"`
	Enabled     *bool    `json:"enabled"`
}

// MergeScheduleUpdate applies partial updates from input onto an existing entry
// and returns the merged scheduler.Config plus the channel parts. Returns an
// error string if validation fails.
func MergeScheduleUpdate(existing scheduler.Entry, input ScheduleUpdateInput) (scheduler.Config, string) {
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
	if config.IsChannelRef(channel) {
		if _, ok := config.ParseChannelRef(channel); !ok {
			return scheduler.Config{}, fmt.Sprintf("channel %q is an invalid channel reference (use \"@channelname\")", channel)
		}
	} else if _, _, ok := config.ParseChannel(channel); !ok {
		return scheduler.Config{}, fmt.Sprintf("channel %q is not in adapter:externalID or @channelname format", channel)
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

	agentName := existing.Agent
	if input.Agent != nil {
		agentName = *input.Agent
	}

	return scheduler.Config{
		Name:        input.Name,
		Type:        string(scheduler.ScheduleTypeAgent),
		Schedule:    expr,
		Skill:       skill,
		Agent:       agentName,
		SessionTier: sessionTier,
		SessionMode: sessionMode,
		Channel:     channel,
		Tags:        tags,
		Enabled:     enabled,
	}, ""
}

// resolveScheduleHandler returns the message handler for the given agent name.
// If the agent matches this server's own agent, the local handler is returned.
// An empty agentName is treated as "self" (no reassignment).
// Otherwise it resolves via ResolveAgentHandler. Returns a non-empty error
// string on failure.
func (s *Server) resolveScheduleHandler(agentName string) (func(context.Context, adapter.IncomingMessage) error, string) {
	if agentName == s.deps.AgentName || agentName == "" {
		return s.deps.HandleMessage, ""
	}
	if s.deps.ResolveAgentHandler == nil {
		return nil, "cross-agent schedule reassignment is not supported"
	}
	h := s.deps.ResolveAgentHandler(agentName)
	if h == nil {
		return nil, fmt.Sprintf("agent %q not found", agentName)
	}
	return h, ""
}

func (s *Server) requireScheduleWrite() *mcp.CallToolResult {
	if s.deps.Sched == nil {
		return toolError("schedule_update is not available: no scheduler configured")
	}
	if s.deps.HandleMessage == nil {
		return toolError("schedule_update is not available: no message handler configured")
	}
	if s.deps.PermissionTier() == "restricted" {
		return toolError("schedule_update is not available in restricted mode")
	}
	return nil
}

func (s *Server) handleScheduleUpdate(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if r := s.requireScheduleWrite(); r != nil {
		return r, nil
	}

	var input ScheduleUpdateInput
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

	cfg, errMsg := MergeScheduleUpdate(existing, input)
	if errMsg != "" {
		return toolError(errMsg), nil
	}

	if cfg.Skill != "" && s.deps.GetSkill != nil {
		if _, ok := s.deps.GetSkill(cfg.Skill); !ok {
			return toolError(fmt.Sprintf("skill %q not found on agent %q", cfg.Skill, s.deps.AgentName)), nil
		}
	}

	handleMsg, resolveErr := s.resolveScheduleHandler(cfg.Agent)
	if resolveErr != "" {
		return toolError(resolveErr), nil
	}

	payload, err := BuildSchedulePayload(cfg.Name, cfg.Schedule, cfg.Skill,
		cfg.Channel, cfg.SessionMode, cfg.SessionTier, cfg.Tags, cfg.Enabled)
	if err != nil {
		return toolError("building schedule payload: " + err.Error()), nil
	}

	schedRef := s.deps.Sched
	logger := s.deps.Logger
	chResolver := s.deps.ChannelResolver
	configPath := s.deps.ConfigPath
	agentName := cfg.Agent

	applyFn := approval.ActionFunc(func(_ context.Context, _ string) error {
		if err := schedRef.Unregister(input.Name); err != nil {
			return fmt.Errorf("unregistering old schedule: %w", err)
		}
		if err := schedRef.RegisterAndStart(cfg, BuildScheduleJob(cfg, handleMsg, logger, chResolver, BuildScheduleJobOpts{Auditor: s.deps.Auditor})); err != nil {
			return err
		}
		if configPath != "" {
			return config.UpdateScheduleInConfig(configPath, cfg.Name, cfg.Schedule,
				cfg.Skill, cfg.Channel, cfg.SessionMode, cfg.SessionTier,
				agentName, cfg.Tags, cfg.Enabled)
		}
		return nil
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindModifySchedule,
		"Update schedule: "+input.Name, payload, applyFn, false)
}

func (s *Server) handleSkillDelete(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.AgentSkillsDir == "" {
		return toolError("skill_delete is not available: no agent skills directory configured"), nil
	}
	if s.deps.RemoveSkill == nil {
		return toolError("skill_delete is not available: no skill removal configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("skill_delete is not available in restricted mode"), nil
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

	if s.deps.GetSkill != nil {
		if _, ok := s.deps.GetSkill(input.Name); !ok {
			return toolError(fmt.Sprintf("skill %q not found", input.Name)), nil
		}
	}

	deps := s.deps
	applyFn := approval.ActionFunc(func(_ context.Context, _ string) error {
		if !deps.RemoveSkill(input.Name) {
			return fmt.Errorf("skill %q not found", input.Name)
		}
		filename := filepath.Join(deps.AgentSkillsDir, input.Name+".md")
		if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
			deps.Logger.Info("skill removed from memory but file deletion failed", "name", input.Name, "error", err)
		}
		deps.Logger.Info("skill deleted via config MCP", "name", input.Name)
		return nil
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindDeleteSkill,
		"Delete skill: "+input.Name, input.Name, applyFn, false)
}

func (s *Server) handleScheduleDelete(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.Sched == nil {
		return toolError("schedule_delete is not available: no scheduler configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("schedule_delete is not available in restricted mode"), nil
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

	if _, ok := s.deps.Sched.GetEntry(input.Name); !ok {
		return toolError(fmt.Sprintf("schedule %q not found", input.Name)), nil
	}

	schedRef := s.deps.Sched
	configPath := s.deps.ConfigPath
	logger := s.deps.Logger

	applyFn := approval.ActionFunc(func(_ context.Context, _ string) error {
		if err := schedRef.Unregister(input.Name); err != nil {
			return fmt.Errorf("unregistering schedule: %w", err)
		}
		if configPath != "" {
			if err := config.RemoveScheduleFromConfig(configPath, input.Name); err != nil {
				logger.Error("schedule deleted but config persistence failed", "name", input.Name, "error", err)
			}
		}
		logger.Info("schedule deleted via config MCP", "name", input.Name)
		return nil
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindModifySchedule,
		"Delete schedule: "+input.Name, input.Name, applyFn, false)
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

	// Validate + auto-migrate legacy low_funds rules into cost_limit/soft.
	for i := range input.Rules {
		if input.Rules[i].Trigger == "low_funds" {
			input.Rules[i].Trigger = "cost_limit"
			if input.Rules[i].Scope == "" {
				input.Rules[i].Scope = "soft"
			}
		}
		r := input.Rules[i]
		switch r.Trigger {
		case "error", "rate_limit", "cost_limit":
		default:
			return toolError(fmt.Sprintf("rules[%d]: trigger must be error, rate_limit, or cost_limit", i)), nil
		}
		switch r.Action {
		case "switch_provider", "switch_model", "wait_and_retry":
		default:
			return toolError(fmt.Sprintf("rules[%d]: action must be switch_provider, switch_model, or wait_and_retry", i)), nil
		}
		if r.Trigger == "cost_limit" {
			switch r.Scope {
			case "soft", "hard":
			default:
				return toolError(fmt.Sprintf("rules[%d]: cost_limit requires scope of soft or hard", i)), nil
			}
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
		summary, string(payload), applyFn, false)
}

func (s *Server) registerCostTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "get_cost_summary",
		Description: "Return current cost tracking data: global cost, per-session costs, budget limit, and per-tool/per-skill usage stats (call counts, error counts, average duration). Optional 'days' restricts the tool/skill stats to the last N days.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {"days": {"type": "integer", "description": "Restrict per-tool/per-skill stats to the last N days (0 or absent = all time)"}}}`),
	}, s.handleGetCostSummary)
}

func (s *Server) handleGetCostSummary(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Days int `json:"days"`
	}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
			return toolError("invalid arguments: " + err.Error()), nil
		}
	}

	data := s.deps.CostSummary()
	if s.deps.TelemetrySummary != nil {
		var since *time.Time
		if input.Days > 0 {
			t := time.Now().AddDate(0, 0, -input.Days)
			since = &t
		}
		summary, err := s.deps.TelemetrySummary(ctx, since)
		if err != nil {
			// Cost data is still useful on its own — report the failure inline.
			data.TelemetryError = err.Error()
		} else {
			data.ByTool = summary.ByTool
			data.BySkill = summary.BySkill
		}
	}

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
		Name               string            `json:"name"`
		Command            string            `json:"command"`
		Args               []string          `json:"args"`
		Env                map[string]string `json:"env"`
		Transport          string            `json:"transport"`
		URL                string            `json:"url"`
		Headers            map[string]string `json:"headers"`
		RequestTimeoutSecs int               `json:"request_timeout_secs"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Name) == "" {
		return toolError("name is required"), nil
	}

	cfg := config.ToolConfig{
		Command:            input.Command,
		Args:               input.Args,
		Env:                input.Env,
		Transport:          input.Transport,
		URL:                input.URL,
		Headers:            input.Headers,
		RequestTimeoutSecs: input.RequestTimeoutSecs,
	}

	lm := s.deps.LifecycleMgr
	identifier := input.Command
	if input.URL != "" {
		identifier = input.URL
	}
	summary := fmt.Sprintf("Install tool: %s (%s)", input.Name, identifier)

	payload, _ := toml.Marshal(map[string]any{
		"name":      input.Name,
		"transport": input.Transport,
		"command":   input.Command,
		"url":       input.URL,
		"args":      input.Args,
		"env":       input.Env,
	})

	applyFn := approval.ActionFunc(func(ctx context.Context, _ string) error {
		return lm.AddTool(ctx, input.Name, cfg)
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindInstallTool,
		summary, strings.TrimSpace(string(payload)), applyFn, false)
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
		summary, input.Name, applyFn, false)
}

func (s *Server) handleToolRestart(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.LifecycleMgr == nil {
		return toolError("tool_restart is not available: no lifecycle manager configured"), nil
	}

	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("tool_restart is not available in restricted mode"), nil
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
	summary := "Restart tool: " + input.Name

	applyFn := approval.ActionFunc(func(ctx context.Context, _ string) error {
		return lm.RestartTool(ctx, input.Name)
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindInstallTool,
		summary, input.Name, applyFn, false)
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
		summary, strings.TrimSpace(string(payload)), applyFn, false)
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
		summary, input.Name, applyFn, false)
}

// --------------------------------------------------------------------------
// Shared helpers
// --------------------------------------------------------------------------

// applyOrSubmit executes the action function directly and returns the result.
//
// Design invariant: Config MCP tools are always called through the Engine's
// tool execution path (engine.executeToolCall → tools.Execute). In supervised
// mode, the Engine obtains operator approval via a ChatEvent + inline keyboard
// BEFORE invoking the tool. Config MCP must not submit its own approval
// because (a) the operator has already approved the tool call, and (b) Config
// MCP has no mechanism to emit a ChatEvent, so a second approval request would
// never be surfaced and would silently time out.
//
// Individual handlers guard against restricted mode before reaching this point.
//
// The kind and forceApproval parameters are retained for call-site compatibility
// but are unused.
func applyOrSubmit(
	ctx context.Context,
	_ Deps,
	_ approval.ActionKind,
	summary string,
	payload string,
	fn approval.ActionFunc,
	_ bool,
) (*mcp.CallToolResult, error) {
	if err := fn(ctx, payload); err != nil {
		return toolError(fmt.Sprintf("action failed: %v", err)), nil
	}
	return toolText("Done: " + summary), nil
}

// ApplySkillCreate writes the skill file to disk and appends it to the
// in-memory skill list.
func ApplySkillCreate(agentSkillsDir string, appendSkill func(skill.Skill), logger interface {
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

// ApplySkillUpdate writes the updated skill file to disk and replaces it in
// the in-memory skill list.
func ApplySkillUpdate(agentSkillsDir string, updateSkill func(string, skill.Skill) bool, logger interface {
	Info(string, ...any)
}, name string, payload string) error {
	s, err := skill.ParseFile("(runtime)", []byte(payload))
	if err != nil {
		return fmt.Errorf("parsing skill: %w", err)
	}

	filename := filepath.Join(agentSkillsDir, name+".md")
	tmp := filename + ".tmp"
	if err := os.WriteFile(tmp, []byte(payload+"\n"), 0600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing skill file: %w", err)
	}
	if err := os.Rename(tmp, filename); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("committing skill file: %w", err)
	}

	updateSkill(name, *s)
	logger.Info("skill updated via config MCP", "name", name, "file", filename)
	return nil
}

// ApplySkillRename writes the skill under its new name, removes the old file,
// and updates the in-memory skill list (remove old + append new).
func ApplySkillRename(agentSkillsDir string, removeSkill func(string) bool, appendSkill func(skill.Skill), logger interface {
	Info(string, ...any)
}, oldName string, payload string) error {
	s, err := skill.ParseFile("(runtime)", []byte(payload))
	if err != nil {
		return fmt.Errorf("parsing skill: %w", err)
	}

	// Write new file first (crash-safe: worst case both files exist).
	newFilename := filepath.Join(agentSkillsDir, s.Name+".md")
	tmp := newFilename + ".tmp"
	if err := os.WriteFile(tmp, []byte(payload+"\n"), 0600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing skill file: %w", err)
	}
	if err := os.Rename(tmp, newFilename); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("committing skill file: %w", err)
	}

	// Remove old file.
	oldFilename := filepath.Join(agentSkillsDir, oldName+".md")
	if err := os.Remove(oldFilename); err != nil && !os.IsNotExist(err) {
		logger.Info("old skill file removal failed (new file written successfully)", "old", oldName, "new", s.Name, "error", err)
	}

	// Update in-memory: remove old, append new.
	removeSkill(oldName)
	appendSkill(*s)
	logger.Info("skill renamed via config MCP", "old", oldName, "new", s.Name, "file", newFilename)
	return nil
}

// BuildSkillPayload constructs the canonical +++ frontmatter + body format.
func BuildSkillPayload(name, description, version string, triggers []string, body string) string {
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

// BuildSchedulePayload marshals the schedule config to TOML for storage as
// approval payload.
func BuildSchedulePayload(name, schedule, skillName, channel, sessionMode, sessionTier string, tags []string, enabled bool) (string, error) {
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

// ChannelResolveResult holds the result of resolving a named channel reference.
type ChannelResolveResult struct {
	ConversationID string
	Bindings       []agent.AdapterBinding
	Broadcast      bool
}

// ChannelResolver looks up a channel by name and returns the resolution result.
// Returns nil if the channel is not found or has no usable bindings.
type ChannelResolver func(name string) *ChannelResolveResult

// BuildScheduleJobOpts holds parameters for BuildScheduleJob.
type BuildScheduleJobOpts struct {
	// Auditor emits audit events for broadcast delivery outcomes. May be nil.
	Auditor audit.Emitter
}

// BuildScheduleJob returns a JobFunc that dispatches a message when the
// schedule fires. Used by both schedule_add and schedule_update.
func BuildScheduleJob(cfg scheduler.Config, handleMsg func(context.Context, adapter.IncomingMessage) error, logger *slog.Logger, resolve ChannelResolver, opts BuildScheduleJobOpts) scheduler.JobFunc {
	var conversationID string
	var targets []agent.AdapterBinding
	var broadcast bool

	if channelName, isRef := config.ParseChannelRef(cfg.Channel); isRef {
		if resolve == nil {
			logger.Error("schedule references channel but no resolver configured", "name", cfg.Name, "channel", cfg.Channel)
		} else if result := resolve(channelName); result == nil {
			logger.Error("schedule references unknown channel", "name", cfg.Name, "channel", cfg.Channel)
		} else {
			conversationID = result.ConversationID
			targets = result.Bindings
			broadcast = result.Broadcast
		}
	} else {
		a, eid, _ := config.ParseChannel(cfg.Channel)
		if a != "" {
			targets = []agent.AdapterBinding{{Adapter: a, ExternalID: eid}}
		}
	}

	text := "[Scheduled trigger: " + cfg.Name + "]"
	if cfg.Skill != "" {
		text = "[Scheduled: " + cfg.Skill + "]"
	}

	return func(entry scheduler.Entry) {
		var failed, succeeded int
		var lastErr string
		for _, target := range targets {
			msg := adapter.IncomingMessage{
				Adapter:        target.Adapter,
				ExternalID:     target.ExternalID,
				ConversationID: conversationID,
				UserName:       "scheduler",
				Text:           text,
				SkillName:      cfg.Skill,
				SessionTier:    cfg.SessionTier,
			}
			if entry.SessionMode == "isolated" {
				msg.ConversationID = fmt.Sprintf("sched:%s:%d", entry.Name, entry.LastRun.UnixNano())
			}
			if err := handleMsg(context.Background(), msg); err != nil {
				failed++
				lastErr = err.Error()
				logger.Error("scheduled job failed", "name", entry.Name, "target", target.Adapter+":"+target.ExternalID, "error", err)
			} else {
				succeeded++
			}
		}
		EmitBroadcastFailure(context.Background(), opts.Auditor, broadcast, entry.Name, cfg.Channel, conversationID, succeeded, failed, lastErr)
	}
}

// EmitBroadcastFailure emits an audit event when broadcast delivery has
// failures. Safe to call with nil auditor or broadcast=false (no-op).
func EmitBroadcastFailure(ctx context.Context, auditor audit.Emitter, broadcast bool, scheduleName, channel, conversationID string, succeeded, failed int, lastErr string) {
	if !broadcast || failed == 0 || auditor == nil {
		return
	}
	summary := fmt.Sprintf("Schedule %s broadcast: %d/%d targets failed", scheduleName, failed, failed+succeeded)
	if succeeded > 0 {
		summary = fmt.Sprintf("Schedule %s broadcast: %d/%d delivered, %d failed", scheduleName, succeeded, failed+succeeded, failed)
	}
	auditor.Emit(ctx, audit.Event{
		Category:       audit.CategorySchedule,
		Action:         "broadcast_partial_failure",
		Summary:        summary,
		Detail:         fmt.Sprintf(`{"name":%q,"channel":%q,"succeeded":%d,"failed":%d,"last_error":%q}`, scheduleName, channel, succeeded, failed, lastErr),
		Status:         audit.StatusError,
		Source:         "scheduler",
		ConversationID: conversationID,
	})
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

func (s *Server) registerSearchTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "session_search",
		Description: `Search across all past conversations for content matching a query. Returns relevant excerpts with conversation IDs you can follow up on. Use for "did we discuss X last week?" — separate from your live conversation context.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "FTS5 search expression. Use plain words for AND, OR for either, quoted phrases for exact match, NEAR(a b, 5) for proximity. Quote tokens containing hyphens (e.g. \"2026-05-16\") — bare hyphens are FTS5 NOT operators."},
				"limit": {"type": "integer", "description": "Max results (default 20, max 50)"}
			},
			"required": ["query"]
		}`),
	}, s.handleSessionSearch)
}

func (s *Server) handleSessionSearch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(input.Query) == "" {
		return toolError("query is required"), nil
	}

	hits, err := s.deps.SearchMessages(ctx, input.Query, input.Limit, s.deps.AgentName)
	if err != nil {
		s.deps.Logger.Error("session_search failed", "query", input.Query, "error", err)
		return toolError("search failed: " + err.Error()), nil
	}

	if len(hits) == 0 {
		return toolText("No results found for: " + input.Query), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d result(s) for: %s\n\n", len(hits), input.Query)
	for i, h := range hits {
		fmt.Fprintf(&sb, "%d. [%s] %s (%s)\n   %s\n\n",
			i+1, h.ConversationID, h.Role, h.CreatedAt.Format("2006-01-02 15:04"), h.Snippet)
	}
	return toolText(sb.String()), nil
}
