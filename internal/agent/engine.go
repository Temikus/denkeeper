package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
)

const maxContextMessages = 50
const maxToolRounds = 10

const memUpdateOpen = "[MEMORY_UPDATE]"
const memUpdateClose = "[/MEMORY_UPDATE]"

const userUpdateOpen = "[USER_UPDATE]"
const userUpdateClose = "[/USER_UPDATE]"

const skillCreateOpen = "[SKILL_CREATE]"
const skillCreateClose = "[/SKILL_CREATE]"

const scheduleAddOpen = "[SCHEDULE_ADD]"
const scheduleAddClose = "[/SCHEDULE_ADD]"

// SendFunc is a callback for sending a response back to the originating adapter.
// The Dispatcher sets this when constructing each Engine.
type SendFunc func(ctx context.Context, msg adapter.OutgoingMessage) error

// Engine is the core agent orchestrator. Each named agent gets its own Engine
// instance with its own persona, skills, permissions, and LLM router.
type Engine struct {
	name           string // agent name (e.g. "default", "work-assistant")
	router         *llm.Router
	memory         MemoryStore
	sendFunc       SendFunc // sends responses back via the originating adapter
	permissions    *security.PermissionEngine
	persona        *persona.Persona // nil = use fallbackPrompt
	fallbackPrompt string           // used when persona is nil
	skillsMu       sync.RWMutex
	skills         []skill.Skill     // filtered per-message based on triggers
	tools          *tool.Manager     // nil = no tools available
	approvals      *approval.Manager // nil = directives requiring approval are silently stripped

	// Extension fields wired in after construction via SetSkillDirs / SetScheduler.
	agentSkillsDir  string               // where to write new agent skill files
	globalSkillsDir string               // base global skills dir (for merge on reload)
	sched           *scheduler.Scheduler // nil = SCHEDULE_ADD directives silently stripped

	// lastPendingApproval holds the approval request from the most recent
	// chatWithApproval call, used by HandleMessage to attach inline buttons.
	lastPendingApproval *approval.Request

	logger *slog.Logger
}

func NewEngine(
	name string,
	router *llm.Router,
	memory MemoryStore,
	sendFunc SendFunc,
	permissions *security.PermissionEngine,
	p *persona.Persona,
	fallbackPrompt string,
	skills []skill.Skill,
	tools *tool.Manager,
	approvals *approval.Manager,
	logger *slog.Logger,
) *Engine {
	return &Engine{
		name:           name,
		router:         router,
		memory:         memory,
		sendFunc:       sendFunc,
		permissions:    permissions,
		persona:        p,
		fallbackPrompt: fallbackPrompt,
		skills:         skills,
		tools:          tools,
		approvals:      approvals,
		logger:         logger.With("agent", name),
	}
}

// SetSkillDirs configures the directories used for skill creation and hot-reload.
// agentSkillsDir is where new skill files are written; globalSkillsDir is the
// shared skills directory merged on top of agent-specific skills.
// Call this after NewEngine, before the engine starts handling messages.
func (e *Engine) SetSkillDirs(agentSkillsDir, globalSkillsDir string) {
	e.agentSkillsDir = agentSkillsDir
	e.globalSkillsDir = globalSkillsDir
}

// SetScheduler provides a Scheduler reference so the engine can register new
// schedules at runtime via SCHEDULE_ADD directives. Call this after the
// Scheduler is initialized.
func (e *Engine) SetScheduler(sched *scheduler.Scheduler) {
	e.sched = sched
}

// Name returns the agent's name.
func (e *Engine) Name() string { return e.name }

// PermissionTier returns the agent's default permission tier.
func (e *Engine) PermissionTier() string { return e.permissions.Tier() }

// ModelName returns the agent's default LLM model.
func (e *Engine) ModelName() string { return e.router.DefaultModel() }

// Skills returns the agent's loaded skills (global + agent-specific, merged).
func (e *Engine) Skills() []skill.Skill {
	e.skillsMu.RLock()
	defer e.skillsMu.RUnlock()
	return e.skills
}

// AppendSkill appends a new skill to the engine's in-memory skill list.
func (e *Engine) AppendSkill(s skill.Skill) {
	e.skillsMu.Lock()
	defer e.skillsMu.Unlock()
	e.skills = append(e.skills, s)
}

// HasTools returns true if the agent has MCP tools configured.
func (e *Engine) HasTools() bool { return e.tools != nil }

// ToolNames returns the names of all registered MCP tools for this agent.
// Returns nil if the agent has no tools configured.
func (e *Engine) ToolNames() []string {
	if e.tools == nil {
		return nil
	}
	return e.tools.ToolNames()
}

// PersonaDir returns the directory the agent's persona was loaded from.
// Returns an empty string if no persona is configured.
func (e *Engine) PersonaDir() string {
	if e.persona == nil {
		return ""
	}
	return e.persona.Dir()
}

// PersonaSections returns which persona sections are loaded (soul/user/memory).
// Returns nil if no persona is configured.
func (e *Engine) PersonaSections() map[string]bool {
	if e.persona == nil {
		return nil
	}
	return map[string]bool{
		"soul":   e.persona.Soul != "",
		"user":   e.persona.User != "",
		"memory": e.persona.Memory != "",
	}
}

// buildSystemPrompt assembles the current system prompt from the persona (if set)
// or the fallback string, appending trigger-matched skill instructions and the
// memory update directive when the engine has write_memory permission.
func (e *Engine) buildSystemPrompt(perms *security.PermissionEngine, msg adapter.IncomingMessage) string {
	var base string
	if e.persona != nil {
		base = e.persona.SystemPrompt()
		if inst := e.persona.MemoryUpdateInstruction(); inst != "" && perms.CanExecute("write_memory") {
			base += "\n\n" + inst
		}
		if inst := e.persona.UserUpdateInstruction(perms.Tier()); inst != "" {
			base += "\n\n" + inst
		}
		if e.agentSkillsDir != "" && perms.Tier() != "restricted" {
			base += "\n\n" + skillCreateInstruction(perms.Tier())
		}
		if e.sched != nil && perms.Tier() != "restricted" {
			base += "\n\n" + scheduleAddInstruction(perms.Tier())
		}
	} else {
		base = e.fallbackPrompt
	}

	e.skillsMu.RLock()
	skills := e.skills
	e.skillsMu.RUnlock()

	matched := skill.MatchSkills(skills, skill.MatchContext{
		MessageText: msg.Text,
		SkillName:   msg.SkillName,
	})
	if suffix := skill.BuildPromptSection(matched); suffix != "" {
		return base + "\n\n" + suffix
	}
	return base
}

// skillCreateInstruction returns the system prompt fragment that tells the
// agent how to propose a new skill via the [SKILL_CREATE] directive.
func skillCreateInstruction(tier string) string {
	var modeNote string
	if tier == "autonomous" {
		modeNote = "In autonomous mode, the skill will be created immediately."
	} else {
		modeNote = "In supervised mode, the skill will be presented for your approval before being created."
	}
	return `## Creating New Skills

If the user asks you to learn a new skill or you identify a repeatable capability worth formalising, you can propose creating a skill file. Include a skill creation block at the end of your response:

[SKILL_CREATE]
+++
name = "skill-name"
description = "One-line description of what this skill does."
version = "1.0.0"
triggers = ["command:skill-name"]
+++

# Skill Name

Instructions and context for this skill...
[/SKILL_CREATE]

` + modeNote + ` Only propose a new skill when it is genuinely reusable. Omit entirely when not needed.`
}

// scheduleAddInstruction returns the system prompt fragment that tells the
// agent how to propose a new schedule entry via the [SCHEDULE_ADD] directive.
func scheduleAddInstruction(tier string) string {
	var modeNote string
	if tier == "autonomous" {
		modeNote = "In autonomous mode, the schedule will be registered immediately."
	} else {
		modeNote = "In supervised mode, the schedule will be presented for your approval before being registered."
	}
	return `## Adding Schedules

If the user asks you to run a task on a recurring basis, you can propose adding a schedule. Include a schedule block at the end of your response:

[SCHEDULE_ADD]
name = "unique-schedule-name"
schedule = "@daily"
skill = "skill-name"
channel = "telegram:CHAT_ID"
session_mode = "isolated"
[/SCHEDULE_ADD]

Supported schedule expressions: @hourly, @daily, @weekly, @monthly, @every 5m, or 5-field cron (e.g. "0 8 * * 1-5").
The channel must match an existing adapter channel (e.g. "telegram:123456789").
` + modeNote + ` Only propose a schedule when the user explicitly asks for recurring automation. Omit entirely when not needed.`
}

// extractDirective is a generic extractor for [TAG]...[/TAG] blocks.
// Returns the cleaned text (block removed) and the extracted content.
// Returns the original text and empty string if the block is absent or malformed.
func extractDirective(text, openTag, closeTag string) (cleaned, payload string) {
	start := strings.Index(text, openTag)
	if start == -1 {
		return text, ""
	}
	rest := text[start+len(openTag):]
	end := strings.Index(rest, closeTag)
	if end == -1 {
		return text, ""
	}
	payload = strings.TrimSpace(rest[:end])
	after := rest[end+len(closeTag):]
	cleaned = strings.TrimSpace(text[:start] + after)
	return cleaned, payload
}

// extractMemoryUpdate parses and removes a [MEMORY_UPDATE]...[/MEMORY_UPDATE]
// block from text. Returns the cleaned text and the extracted content (empty if
// no block was found or the block was malformed).
func extractMemoryUpdate(text string) (cleaned, memUpdate string) {
	return extractDirective(text, memUpdateOpen, memUpdateClose)
}

// extractUserUpdate parses and removes a [USER_UPDATE]...[/USER_UPDATE] block
// from text. Returns the cleaned text and the proposed USER.md content
// (empty if no block was found or the block was malformed).
func extractUserUpdate(text string) (cleaned, userUpdate string) {
	return extractDirective(text, userUpdateOpen, userUpdateClose)
}

// extractSkillCreate parses and removes a [SKILL_CREATE]...[/SKILL_CREATE] block.
func extractSkillCreate(text string) (cleaned, payload string) {
	return extractDirective(text, skillCreateOpen, skillCreateClose)
}

// extractScheduleAdd parses and removes a [SCHEDULE_ADD]...[/SCHEDULE_ADD] block.
func extractScheduleAdd(text string) (cleaned, payload string) {
	return extractDirective(text, scheduleAddOpen, scheduleAddClose)
}

// applySkillCreate writes the skill file to the agent's skills directory and
// appends the parsed skill to the engine's in-memory skill list.
func (e *Engine) applySkillCreate(payload string) error {
	if e.agentSkillsDir == "" {
		return fmt.Errorf("no agent skills directory configured")
	}
	s, err := skill.ParseFile("(runtime)", []byte(payload))
	if err != nil {
		return fmt.Errorf("parsing skill: %w", err)
	}
	if s.Name == "" {
		return fmt.Errorf("skill name is required")
	}

	if err := os.MkdirAll(e.agentSkillsDir, 0755); err != nil {
		return fmt.Errorf("creating skills directory: %w", err)
	}

	filename := filepath.Join(e.agentSkillsDir, s.Name+".md")
	tmp := filename + ".tmp"
	if err := os.WriteFile(tmp, []byte(payload+"\n"), 0644); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing skill file: %w", err)
	}
	if err := os.Rename(tmp, filename); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("committing skill file: %w", err)
	}

	e.AppendSkill(*s)
	e.logger.Info("skill created", "name", s.Name, "file", filename)
	return nil
}

// scheduleAddConfig is the TOML structure for a SCHEDULE_ADD payload.
type scheduleAddConfig struct {
	Name        string   `toml:"name"`
	Schedule    string   `toml:"schedule"`
	Skill       string   `toml:"skill"`
	Channel     string   `toml:"channel"`
	SessionMode string   `toml:"session_mode"`
	SessionTier string   `toml:"session_tier"`
	Tags        []string `toml:"tags"`
	Enabled     *bool    `toml:"enabled"` // defaults to true if omitted
}

// applyScheduleAdd parses the TOML payload and registers a new schedule entry
// with the scheduler. The job function dispatches directly through the engine.
func (e *Engine) applyScheduleAdd(payload string) error {
	if e.sched == nil {
		return fmt.Errorf("no scheduler configured")
	}

	var cfg scheduleAddConfig
	if err := toml.Unmarshal([]byte(payload), &cfg); err != nil {
		return fmt.Errorf("parsing schedule config: %w", err)
	}
	if cfg.Name == "" {
		return fmt.Errorf("schedule name is required")
	}
	if cfg.Schedule == "" {
		return fmt.Errorf("schedule expression is required")
	}
	if cfg.Channel == "" {
		return fmt.Errorf("schedule channel is required")
	}
	if err := scheduler.ValidateExpr(cfg.Schedule); err != nil {
		return fmt.Errorf("invalid schedule expression: %w", err)
	}

	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}

	sessionMode := cfg.SessionMode
	if sessionMode == "" {
		sessionMode = "isolated"
	}

	// Split "adapter:externalID" channel into its parts.
	colonIdx := strings.IndexByte(cfg.Channel, ':')
	if colonIdx <= 0 || colonIdx == len(cfg.Channel)-1 {
		return fmt.Errorf("channel %q is not in adapter:externalID format", cfg.Channel)
	}
	adapterName := cfg.Channel[:colonIdx]
	externalID := cfg.Channel[colonIdx+1:]

	skillName := cfg.Skill
	text := "[Scheduled trigger: " + cfg.Name + "]"
	if skillName != "" {
		text = "[Scheduled: " + skillName + "]"
	}

	engineRef := e // capture for closure
	baseMsg := adapter.IncomingMessage{
		Adapter:     adapterName,
		ExternalID:  externalID,
		UserName:    "scheduler",
		Text:        text,
		SkillName:   skillName,
		SessionTier: cfg.SessionTier,
	}

	jobFunc := func(entry scheduler.Entry) {
		msg := baseMsg
		if entry.SessionMode == "isolated" {
			msg.ConversationID = fmt.Sprintf("sched:%s:%d", entry.Name, entry.LastRun.UnixNano())
		}
		if err := engineRef.HandleMessage(context.Background(), msg); err != nil {
			engineRef.logger.Error("scheduled job failed", "name", entry.Name, "error", err)
		}
	}

	return e.sched.RegisterAndStart(scheduler.Config{
		Name:        cfg.Name,
		Type:        string(scheduler.ScheduleTypeAgent),
		Schedule:    cfg.Schedule,
		Skill:       cfg.Skill,
		SessionTier: cfg.SessionTier,
		SessionMode: sessionMode,
		Channel:     cfg.Channel,
		Tags:        cfg.Tags,
		Enabled:     enabled,
	}, jobFunc)
}

// Chat processes a single incoming message through the full pipeline and
// returns the response text. It does not call the sendFunc — use this when
// the caller wants to receive the reply directly (e.g. the REST API).
// Any pending approval request is accessible via GET /api/v1/approvals.
func (e *Engine) Chat(ctx context.Context, msg adapter.IncomingMessage) (string, error) {
	text, _, err := e.chatWithApproval(ctx, msg)
	return text, err
}

// directiveSpec describes a single extracted directive that may need autonomous
// execution or supervised approval.
type directiveSpec struct {
	payload     string
	kind        approval.ActionKind
	description string
	pendingMsg  string
	logLabel    string
	externalID  string
	adapter     string
	convID      string
	applyFn     func(ctx context.Context, payload string) error
}

// processDirective handles the autonomous/supervised/restricted logic for a
// single directive. It returns a non-nil approval request when a supervised
// submission succeeds, and the (possibly appended) response text.
func (e *Engine) processDirective(ctx context.Context, perms *security.PermissionEngine, spec directiveSpec, responseText string) (*approval.Request, string) {
	switch perms.Tier() {
	case "autonomous":
		if err := spec.applyFn(ctx, spec.payload); err != nil {
			e.logger.Warn(spec.logLabel+" failed", "error", err)
		} else {
			e.logger.Info(spec.logLabel+" applied directly", "bytes", len(spec.payload))
		}
	case "supervised":
		if e.approvals != nil {
			req, submitErr := e.approvals.Submit(
				ctx,
				e.name,
				spec.kind,
				spec.description,
				spec.payload,
				spec.externalID,
				spec.adapter,
				spec.convID,
				spec.applyFn,
			)
			if submitErr != nil {
				e.logger.Warn(spec.logLabel+" approval submit failed", "error", submitErr)
			} else {
				e.logger.Info(spec.logLabel+" approval submitted", "id", req.ID)
				return req, responseText + spec.pendingMsg
			}
		}
		// restricted: silently drop
	}
	return nil, responseText
}

// chatWithApproval is the internal full-pipeline implementation. It returns
// both the response text and any approval request that was created during this
// call (nil if none). HandleMessage uses this to attach inline keyboard buttons.
func (e *Engine) chatWithApproval(ctx context.Context, msg adapter.IncomingMessage) (string, *approval.Request, error) {
	perms := e.resolvePermissions(msg)
	if !perms.CanExecute("chat") {
		return "", nil, fmt.Errorf("chat action not permitted")
	}

	e.logger.Info("received message", "adapter", msg.Adapter, "user", msg.UserName, "text_len", len(msg.Text))

	convID, err := e.resolveConversation(ctx, msg)
	if err != nil {
		return "", nil, err
	}

	if err := e.memory.AddMessage(ctx, convID, StoredMessage{
		Role:    "user",
		Content: msg.Text,
	}); err != nil {
		return "", nil, fmt.Errorf("storing user message: %w", err)
	}

	history, err := e.memory.GetMessages(ctx, convID, maxContextMessages)
	if err != nil {
		return "", nil, fmt.Errorf("loading history: %w", err)
	}

	llmMessages := make([]llm.Message, 0, len(history)+1)
	llmMessages = append(llmMessages, llm.Message{Role: "system", Content: e.buildSystemPrompt(perms, msg)})
	for _, h := range history {
		llmMessages = append(llmMessages, llm.Message{Role: h.Role, Content: h.Content})
	}

	resp, _, err := e.runLLMWithTools(ctx, convID, perms, llmMessages)
	if err != nil {
		return "", nil, err
	}

	responseText := e.processResponseDirectives(ctx, resp, perms, msg, convID)

	if err := e.memory.AddMessage(ctx, convID, StoredMessage{
		Role:       "assistant",
		Content:    responseText,
		TokensUsed: resp.TokensUsed.Total,
	}); err != nil {
		return "", nil, fmt.Errorf("storing assistant message: %w", err)
	}

	e.logger.Info("chat complete", "adapter", msg.Adapter, "tokens", resp.TokensUsed.Total)
	return responseText, e.lastPendingApproval, nil
}

// resolvePermissions returns the effective permission engine for the message,
// considering per-schedule tier overrides.
func (e *Engine) resolvePermissions(msg adapter.IncomingMessage) *security.PermissionEngine {
	if msg.SessionTier == "" {
		return e.permissions
	}
	if !security.ValidTier(msg.SessionTier) {
		e.logger.Warn("ignoring invalid session tier, using global",
			"session_tier", msg.SessionTier, "global_tier", e.permissions.Tier())
		return e.permissions
	}
	if msg.SessionTier == e.permissions.Tier() {
		return e.permissions
	}
	override, err := security.NewPermissionEngine(msg.SessionTier)
	if err != nil {
		e.logger.Warn("failed to create override permissions", "error", err)
		return e.permissions
	}
	e.logger.Info("using per-schedule permission tier",
		"tier", msg.SessionTier, "global_tier", e.permissions.Tier())
	return override
}

// resolveConversation returns the conversation ID for the message, creating
// the conversation if necessary.
func (e *Engine) resolveConversation(ctx context.Context, msg adapter.IncomingMessage) (string, error) {
	if msg.ConversationID != "" {
		if err := e.memory.GetOrCreateConversationByID(ctx, msg.ConversationID); err != nil {
			return "", fmt.Errorf("getting conversation: %w", err)
		}
		return msg.ConversationID, nil
	}
	convID, err := e.memory.GetOrCreateConversation(ctx, e.name+":"+msg.Adapter, msg.ExternalID)
	if err != nil {
		return "", fmt.Errorf("getting conversation: %w", err)
	}
	return convID, nil
}

// runLLMWithTools makes the LLM call and runs the tool-call loop until the
// LLM produces a text response.
func (e *Engine) runLLMWithTools(ctx context.Context, convID string, perms *security.PermissionEngine, llmMessages []llm.Message) (*llm.ChatResponse, []llm.Message, error) {
	resp, err := e.router.Complete(ctx, convID, llmMessages)
	if err != nil {
		return nil, llmMessages, fmt.Errorf("LLM completion: %w", err)
	}

	for round := 0; resp.FinishReason == "tool_calls" && len(resp.ToolCalls) > 0; round++ {
		if round >= maxToolRounds {
			return nil, llmMessages, fmt.Errorf("exceeded maximum tool call rounds (%d)", maxToolRounds)
		}
		if e.tools == nil {
			return nil, llmMessages, fmt.Errorf("LLM requested tool calls but no tool manager configured")
		}
		if !perms.CanExecute("use_tools") {
			return nil, llmMessages, fmt.Errorf("tool execution not permitted under %q tier", perms.Tier())
		}

		llmMessages = append(llmMessages, llm.Message{Role: "assistant", ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			e.logger.Info("executing tool", "tool", tc.Function.Name, "round", round+1)
			result, execErr := e.tools.Execute(ctx, tc)
			if execErr != nil {
				e.logger.Warn("tool execution failed", "tool", tc.Function.Name, "error", execErr)
				result = fmt.Sprintf("Tool error: %v", execErr)
			}
			llmMessages = append(llmMessages, llm.Message{
				Role: "tool", Content: result, ToolCallID: tc.ID,
			})
		}

		resp, err = e.router.Complete(ctx, convID, llmMessages)
		if err != nil {
			return nil, llmMessages, fmt.Errorf("LLM completion (tool round %d): %w", round+1, err)
		}
	}
	return resp, llmMessages, nil
}

// processResponseDirectives extracts memory updates, user updates, skill
// creation, and schedule addition directives from the LLM response. Returns
// the cleaned response text. Sets e.lastPendingApproval if an approval was
// created.
func (e *Engine) processResponseDirectives(ctx context.Context, resp *llm.ChatResponse, perms *security.PermissionEngine, msg adapter.IncomingMessage, convID string) string {
	responseText, memUpdate := extractMemoryUpdate(resp.Content)
	if memUpdate != "" && e.persona != nil && perms.CanExecute("write_memory") {
		if err := e.persona.UpdateMemory(memUpdate); err != nil {
			e.logger.Warn("failed to persist memory update", "error", err)
		} else {
			e.logger.Info("memory updated", "bytes", len(memUpdate))
		}
	}

	responseText, userUpdate := extractUserUpdate(responseText)
	responseText, skillPayload := extractSkillCreate(responseText)
	responseText, schedPayload := extractScheduleAdd(responseText)

	e.lastPendingApproval = nil

	if userUpdate != "" && e.persona != nil && e.persona.Dir() != "" {
		personaRef := e.persona
		e.lastPendingApproval, responseText = e.processDirective(ctx, perms, directiveSpec{
			payload: userUpdate, kind: approval.ActionKindUserUpdate,
			description: "Update user profile (USER.md)",
			pendingMsg: "\n\n_Proposed user profile update is pending your approval._",
			logLabel: "user update", externalID: msg.ExternalID,
			adapter: msg.Adapter, convID: convID,
			applyFn: func(_ context.Context, payload string) error { return personaRef.Save("user", payload) },
		}, responseText)
	}

	if skillPayload != "" && e.lastPendingApproval == nil {
		e.lastPendingApproval, responseText = e.processDirective(ctx, perms, directiveSpec{
			payload: skillPayload, kind: approval.ActionKindCreateSkill,
			description: "Create new skill",
			pendingMsg: "\n\n_Proposed skill creation is pending your approval._",
			logLabel: "skill create", externalID: msg.ExternalID,
			adapter: msg.Adapter, convID: convID,
			applyFn: func(_ context.Context, payload string) error { return e.applySkillCreate(payload) },
		}, responseText)
	}

	if schedPayload != "" && e.lastPendingApproval == nil {
		e.lastPendingApproval, responseText = e.processDirective(ctx, perms, directiveSpec{
			payload: schedPayload, kind: approval.ActionKindModifySchedule,
			description: "Add new schedule",
			pendingMsg: "\n\n_Proposed schedule addition is pending your approval._",
			logLabel: "schedule add", externalID: msg.ExternalID,
			adapter: msg.Adapter, convID: convID,
			applyFn: func(_ context.Context, payload string) error { return e.applyScheduleAdd(payload) },
		}, responseText)
	}

	return responseText
}

// HandleMessage processes a single incoming message and sends the response
// back via the adapter's SendFunc. It delegates the full pipeline to
// chatWithApproval so it can attach inline keyboard buttons when an approval
// was created during this call.
func (e *Engine) HandleMessage(ctx context.Context, msg adapter.IncomingMessage) error {
	responseText, pendingApproval, err := e.chatWithApproval(ctx, msg)
	if err != nil {
		return err
	}

	if e.sendFunc != nil {
		out := adapter.OutgoingMessage{
			Adapter:    msg.Adapter,
			ExternalID: msg.ExternalID,
			Text:       responseText,
			IsVoice:    msg.IsVoice,
		}
		if pendingApproval != nil {
			out.Buttons = []adapter.KeyboardButton{
				{Label: "✅ Approve", CallbackData: pendingApproval.CallbackData + ":approve"},
				{Label: "❌ Deny", CallbackData: pendingApproval.CallbackData + ":deny"},
			}
		}
		if err := e.sendFunc(ctx, out); err != nil {
			return fmt.Errorf("sending response: %w", err)
		}
	}

	e.logger.Info("response sent", "adapter", msg.Adapter)
	return nil
}
