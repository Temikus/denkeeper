package configmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pelletier/go-toml/v2"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
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

	schedRef := s.deps.Sched
	handleMsg := s.deps.HandleMessage

	adapterName := input.Channel[:colonIdx]
	externalID := input.Channel[colonIdx+1:]

	text := "[Scheduled trigger: " + input.Name + "]"
	if input.Skill != "" {
		text = "[Scheduled: " + input.Skill + "]"
	}

	baseMsg := adapter.IncomingMessage{
		Adapter:     adapterName,
		ExternalID:  externalID,
		UserName:    "scheduler",
		Text:        text,
		SkillName:   input.Skill,
		SessionTier: input.SessionTier,
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

	logger := s.deps.Logger
	applyFn := approval.ActionFunc(func(_ context.Context, _ string) error {
		return schedRef.RegisterAndStart(cfg, func(entry scheduler.Entry) {
			msg := baseMsg
			if entry.SessionMode == "isolated" {
				msg.ConversationID = fmt.Sprintf("sched:%s:%d", entry.Name, entry.LastRun.UnixNano())
			}
			if err := handleMsg(context.Background(), msg); err != nil {
				logger.Error("scheduled job failed", "name", entry.Name, "error", err)
			}
		})
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

	if err := os.MkdirAll(agentSkillsDir, 0755); err != nil {
		return fmt.Errorf("creating skills directory: %w", err)
	}

	filename := filepath.Join(agentSkillsDir, s.Name+".md")
	tmp := filename + ".tmp"
	if err := os.WriteFile(tmp, []byte(payload+"\n"), 0644); err != nil {
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
