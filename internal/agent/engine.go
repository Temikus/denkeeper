package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const maxContextMessages = 50
const maxToolRounds = 10
const toolExecTimeout = 30 * time.Second
const approvalTimeout = 5 * time.Minute
const maxConversationIDLen = 256

const memUpdateOpen = "[MEMORY_UPDATE]"
const memUpdateClose = "[/MEMORY_UPDATE]"

const userUpdateOpen = "[USER_UPDATE]"
const userUpdateClose = "[/USER_UPDATE]"

const soulUpdateOpen = "[SOUL_UPDATE]"
const soulUpdateClose = "[/SOUL_UPDATE]"

const identityUpdateOpen = "[IDENTITY_UPDATE]"
const identityUpdateClose = "[/IDENTITY_UPDATE]"

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

	logger *slog.Logger

	// OTel instrumentation (global no-ops when OTel is disabled).
	tracer     trace.Tracer
	mMessages  metric.Int64Counter
	mSessions  metric.Int64UpDownCounter
	mChatDur   metric.Float64Histogram
	mToolCalls metric.Int64Counter
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
	meter := otel.Meter("denkeeper.agent")
	tracer := otel.Tracer("denkeeper.agent")
	msgs, _ := meter.Int64Counter("denkeeper.messages",
		metric.WithDescription("Messages processed"))
	sessions, _ := meter.Int64UpDownCounter("denkeeper.sessions.active",
		metric.WithDescription("Active chat sessions"))
	chatDur, _ := meter.Float64Histogram("denkeeper.chat.duration",
		metric.WithDescription("Chat pipeline latency in seconds"),
		metric.WithUnit("s"))
	toolCalls, _ := meter.Int64Counter("denkeeper.tool_calls",
		metric.WithDescription("Tool calls executed"))

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
		tracer:         tracer,
		mMessages:      msgs,
		mSessions:      sessions,
		mChatDur:       chatDur,
		mToolCalls:     toolCalls,
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

// SetPermissionTier replaces the engine's permission engine with one for the new tier.
func (e *Engine) SetPermissionTier(tier string) error {
	newPerms, err := security.NewPermissionEngine(tier)
	if err != nil {
		return err
	}
	e.permissions = newPerms
	return nil
}

// ListModels returns available LLM models from all registered providers.
func (e *Engine) ListModels(ctx context.Context) []string {
	return e.router.ListModels(ctx)
}

// SetModel changes the engine's default LLM model.
func (e *Engine) SetModel(model string) {
	e.router.SetDefaultModel(model)
}

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

// RemoveSkill removes a skill by name from the engine's in-memory skill list.
// Returns false if the skill was not found.
func (e *Engine) RemoveSkill(name string) bool {
	e.skillsMu.Lock()
	defer e.skillsMu.Unlock()
	for i, s := range e.skills {
		if s.Name == name {
			e.skills = append(e.skills[:i], e.skills[i+1:]...)
			return true
		}
	}
	return false
}

// UpdateSkill replaces an existing skill by name in the engine's in-memory
// skill list. Returns false if the skill was not found.
func (e *Engine) UpdateSkill(name string, updated skill.Skill) bool {
	e.skillsMu.Lock()
	defer e.skillsMu.Unlock()
	for i, s := range e.skills {
		if s.Name == name {
			e.skills[i] = updated
			return true
		}
	}
	return false
}

// GetSkill returns a skill by name and true, or a zero value and false if not found.
func (e *Engine) GetSkill(name string) (skill.Skill, bool) {
	e.skillsMu.RLock()
	defer e.skillsMu.RUnlock()
	for _, s := range e.skills {
		if s.Name == name {
			return s, true
		}
	}
	return skill.Skill{}, false
}

// SkillsDir returns the directory where agent-specific skill files are stored.
func (e *Engine) SkillsDir() string { return e.agentSkillsDir }

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
	return e.persona.Sections()
}

// PersonaSection returns the content, editability, and agent-mutability of a persona section.
// Returns ("", false, false, false) if no persona is configured or section is unknown.
func (e *Engine) PersonaSection(section string) (content string, editable bool, agentMutable bool, ok bool) {
	if e.persona == nil {
		return "", false, false, false
	}
	return e.persona.GetSection(section)
}

// SavePersonaSection writes content to the named persona section.
// Returns an error if no persona is configured.
func (e *Engine) SavePersonaSection(section, content string) error {
	if e.persona == nil {
		return fmt.Errorf("no persona configured for agent %q", e.name)
	}
	return e.persona.Save(section, content)
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
		if inst := e.persona.SoulUpdateInstruction(perms.Tier()); inst != "" {
			base += "\n\n" + inst
		}
		if inst := e.persona.IdentityUpdateInstruction(perms.Tier()); inst != "" {
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

// extractSoulUpdate parses and removes a [SOUL_UPDATE]...[/SOUL_UPDATE] block
// from text. Returns the cleaned text and the proposed SOUL.md content
// (empty if no block was found or the block was malformed).
func extractSoulUpdate(text string) (cleaned, soulUpdate string) {
	return extractDirective(text, soulUpdateOpen, soulUpdateClose)
}

// extractIdentityUpdate parses and removes an [IDENTITY_UPDATE]...[/IDENTITY_UPDATE]
// block from text. Returns the cleaned text and the proposed IDENTITY.md content
// (empty if no block was found or the block was malformed).
func extractIdentityUpdate(text string) (cleaned, identityUpdate string) {
	return extractDirective(text, identityUpdateOpen, identityUpdateClose)
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

	if err := os.MkdirAll(e.agentSkillsDir, 0750); err != nil {
		return fmt.Errorf("creating skills directory: %w", err)
	}

	filename := filepath.Join(e.agentSkillsDir, s.Name+".md")
	tmp := filename + ".tmp"
	if err := os.WriteFile(tmp, []byte(payload+"\n"), 0600); err != nil {
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
		// Derive from the scheduler's lifecycle context so jobs stop on shutdown.
		jobCtx, jobCancel := context.WithTimeout(engineRef.sched.Context(), 10*time.Minute)
		defer jobCancel()
		if err := engineRef.HandleMessage(jobCtx, msg); err != nil {
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

// ChatEvent describes an intermediate pipeline event streamed to SSE clients.
type ChatEvent struct {
	Type     string  `json:"type"`                  // "tool_start", "tool_end", "thinking", "usage", "tool_approval", "content_delta", "thinking_delta"
	Tool     string  `json:"tool,omitempty"`        // tool name
	Round    int     `json:"round,omitempty"`       // 1-based tool round
	Duration int64   `json:"duration_ms,omitempty"` // tool execution time
	Error    string  `json:"error,omitempty"`       // tool error (if any)
	Text     string  `json:"text,omitempty"`        // human-readable status message / content delta
	Tokens   int     `json:"tokens,omitempty"`      // total tokens used (usage event)
	CostUSD  float64 `json:"cost_usd,omitempty"`    // estimated cost in USD (usage event)

	// ApprovalID and ApprovalCallback are set on "tool_approval" events so
	// the adapter can render inline approve/deny buttons.
	ApprovalID       string `json:"approval_id,omitempty"`
	ApprovalCallback string `json:"approval_callback,omitempty"` // "appr:{id}" prefix

	// ApprovalStatus distinguishes pending approvals from auto-approved ones.
	// Values: "" (pending, needs user action), "auto_approved" (rule matched).
	ApprovalStatus string `json:"approval_status,omitempty"`
}

// ChatEventFunc is called for each intermediate pipeline event.
type ChatEventFunc func(ChatEvent)

// Chat processes a single incoming message through the full pipeline and
// returns the response text. It does not call the sendFunc — use this when
// the caller wants to receive the reply directly (e.g. the REST API).
// Any pending approval request is accessible via GET /api/v1/approvals.
func (e *Engine) Chat(ctx context.Context, msg adapter.IncomingMessage) (string, error) {
	text, _, err := e.chatWithApproval(ctx, msg, nil)
	return text, err
}

// ChatWithEvents is like Chat but calls onEvent for intermediate status events
// (tool calls, etc.) that can be streamed to the client in real time.
func (e *Engine) ChatWithEvents(ctx context.Context, msg adapter.IncomingMessage, onEvent ChatEventFunc) (string, error) {
	text, _, err := e.chatWithApproval(ctx, msg, onEvent)
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
				return nil, responseText + "\n\n(Failed to submit " + spec.logLabel + " for approval.)"
			}
			e.logger.Info(spec.logLabel+" approval submitted", "id", req.ID)
			return req, responseText + spec.pendingMsg
		}
		// No approval manager — treat as restricted and silently drop.
	}
	return nil, responseText
}

// chatWithApproval is the internal full-pipeline implementation. It returns
// both the response text and any approval request that was created during this
// call (nil if none). HandleMessage uses this to attach inline keyboard buttons.
func (e *Engine) chatWithApproval(ctx context.Context, msg adapter.IncomingMessage, onEvent ChatEventFunc) (string, *approval.Request, error) {
	perms := e.resolvePermissions(msg)
	if !perms.CanExecute("chat") {
		return "", nil, fmt.Errorf("chat action not permitted")
	}

	agentAttr := attribute.String("agent", e.name)
	adapterAttr := attribute.String("adapter", msg.Adapter)
	e.mMessages.Add(ctx, 1, metric.WithAttributes(agentAttr, adapterAttr))
	e.mSessions.Add(ctx, 1, metric.WithAttributes(agentAttr))
	defer e.mSessions.Add(ctx, -1, metric.WithAttributes(agentAttr))

	ctx, span := e.tracer.Start(ctx, "agent.chat",
		trace.WithAttributes(agentAttr, adapterAttr))
	defer span.End()
	chatStart := time.Now()
	defer func() {
		e.mChatDur.Record(ctx, time.Since(chatStart).Seconds(), metric.WithAttributes(agentAttr))
	}()

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

	// Store adapter routing info in context for tool approval submissions.
	ctx = context.WithValue(ctx, ctxKeyAdapter, msg.Adapter)
	ctx = context.WithValue(ctx, ctxKeyExternalID, msg.ExternalID)

	resp, _, err := e.runLLMWithTools(ctx, convID, perms, msg, llmMessages, onEvent)
	if err != nil {
		return "", nil, err
	}

	if onEvent != nil {
		cost, _ := llm.TokenCost(resp, nil)
		onEvent(ChatEvent{
			Type:    "usage",
			Tokens:  resp.TokensUsed.Total,
			CostUSD: cost,
		})
	}

	e.logger.Info("llm response received",
		"adapter", msg.Adapter,
		"finish_reason", resp.FinishReason,
		"model", resp.Model,
		"content_len", len(resp.Content),
		"tool_calls", len(resp.ToolCalls),
		"tokens_prompt", resp.TokensUsed.Prompt,
		"tokens_completion", resp.TokensUsed.Completion,
		"tokens_total", resp.TokensUsed.Total,
	)

	responseText, pendingApproval := e.processResponseDirectives(ctx, resp, perms, msg, convID)

	if responseText == "" && resp.Content != "" {
		e.logger.Warn("response emptied by directive extraction",
			"raw_content_len", len(resp.Content),
			"finish_reason", resp.FinishReason,
			"conversation", convID,
		)
	} else if responseText == "" {
		e.logger.Warn("llm returned empty response",
			"finish_reason", resp.FinishReason,
			"conversation", convID,
		)
	}

	if err := e.memory.AddMessage(ctx, convID, StoredMessage{
		Role:       "assistant",
		Content:    responseText,
		TokensUsed: resp.TokensUsed.Total,
	}); err != nil {
		return "", nil, fmt.Errorf("storing assistant message: %w", err)
	}

	e.logger.Info("chat complete",
		"adapter", msg.Adapter,
		"response_len", len(responseText),
		"tokens", resp.TokensUsed.Total,
		"finish_reason", resp.FinishReason,
		"model", resp.Model,
		"conversation", convID,
	)
	return responseText, pendingApproval, nil
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
		if err := validateConversationID(msg.ConversationID); err != nil {
			return "", err
		}
		if err := e.memory.GetOrCreateConversationByID(ctx, msg.ConversationID, msg.Adapter, msg.ExternalID); err != nil {
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

// streamCallbackFor returns an llm.StreamCallback that emits content_delta
// and thinking_delta ChatEvents. Returns nil if onEvent is nil.
func streamCallbackFor(onEvent ChatEventFunc) llm.StreamCallback {
	if onEvent == nil {
		return nil
	}
	return func(chunk llm.StreamChunk) {
		if chunk.ContentDelta != "" {
			onEvent(ChatEvent{Type: "content_delta", Text: chunk.ContentDelta})
		}
		if chunk.ThinkingDelta != "" {
			onEvent(ChatEvent{Type: "thinking_delta", Text: chunk.ThinkingDelta})
		}
	}
}

// runLLMWithTools makes the LLM call and runs the tool-call loop until the
// LLM produces a text response. If onEvent is non-nil, it is called for each
// tool execution start/end so the caller can stream status to the client.
func (e *Engine) runLLMWithTools(ctx context.Context, convID string, perms *security.PermissionEngine, msg adapter.IncomingMessage, llmMessages []llm.Message, onEvent ChatEventFunc) (*llm.ChatResponse, []llm.Message, error) {
	if onEvent != nil {
		onEvent(ChatEvent{Type: "thinking"})
	}

	resp, err := e.router.CompleteStream(ctx, convID, llmMessages, streamCallbackFor(onEvent))
	if err != nil {
		return nil, llmMessages, fmt.Errorf("LLM completion: %w", err)
	}

	// Validate tool execution preconditions before entering the loop.
	if resp.FinishReason == "tool_calls" && len(resp.ToolCalls) > 0 {
		if e.tools == nil {
			return nil, llmMessages, fmt.Errorf("LLM requested tool calls but no tool manager configured")
		}
		if !perms.CanExecute("use_tools") {
			return nil, llmMessages, fmt.Errorf("tool execution not permitted under %q tier", perms.Tier())
		}
	}

	resp, llmMessages, err = e.executeToolRounds(ctx, convID, perms, resp, llmMessages, onEvent)
	if err != nil {
		return nil, llmMessages, err
	}

	return resp, llmMessages, nil
}

// executeToolRounds runs the tool-call loop, accumulating tokens/cost across
// all rounds. If the model returns empty content after completing tool rounds,
// it attempts to recover by using intermediate content or nudging the model.
func (e *Engine) executeToolRounds(ctx context.Context, convID string, perms *security.PermissionEngine, resp *llm.ChatResponse, llmMessages []llm.Message, onEvent ChatEventFunc) (*llm.ChatResponse, []llm.Message, error) {
	var totalUsage llm.TokenUsage
	var totalCost float64
	totalUsage.Prompt += resp.TokensUsed.Prompt
	totalUsage.Completion += resp.TokensUsed.Completion
	totalUsage.Total += resp.TokensUsed.Total
	totalCost += resp.CostUSD

	supervised := perms.Tier() == "supervised" && e.approvals != nil
	var toolRounds int
	var accumulatedContent strings.Builder
	for round := 0; resp.FinishReason == "tool_calls" && len(resp.ToolCalls) > 0; round++ {
		toolRounds++
		if round >= maxToolRounds {
			return nil, llmMessages, fmt.Errorf("exceeded maximum tool call rounds (%d)", maxToolRounds)
		}

		// Preserve any text content the model produced alongside tool calls.
		if resp.Content != "" {
			accumulatedContent.WriteString(resp.Content)
		}

		llmMessages = append(llmMessages, llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			e.mToolCalls.Add(ctx, 1, metric.WithAttributes(
				attribute.String("agent", e.name),
				attribute.String("tool_name", tc.Function.Name)))
			result := e.executeToolCall(ctx, tc, round+1, convID, supervised, onEvent)
			llmMessages = append(llmMessages, llm.Message{
				Role: "tool", Content: result, ToolCallID: tc.ID,
			})
		}

		if onEvent != nil {
			onEvent(ChatEvent{Type: "thinking", Round: round + 1, Text: "Processing tool results..."})
		}

		var err error
		resp, err = e.router.CompleteStream(ctx, convID, llmMessages, streamCallbackFor(onEvent))
		if err != nil {
			return nil, llmMessages, fmt.Errorf("LLM completion (tool round %d): %w", round+1, err)
		}

		totalUsage.Prompt += resp.TokensUsed.Prompt
		totalUsage.Completion += resp.TokensUsed.Completion
		totalUsage.Total += resp.TokensUsed.Total
		totalCost += resp.CostUSD

		e.logger.Info("tool round complete",
			"round", round+1,
			"finish_reason", resp.FinishReason,
			"content_len", len(resp.Content),
			"tool_calls_next", len(resp.ToolCalls),
			"tokens_total", resp.TokensUsed.Total,
		)

		// Check soft cost limit between tool rounds — allows the model to
		// produce a final response but prevents further tool calls.
		if e.router.CostTracker().ExceedsSoftLimit(convID) {
			if onEvent != nil {
				onEvent(ChatEvent{Type: "cost_limit", Text: "Session approaching cost limit — pausing tool use."})
			}
			e.logger.Warn("soft cost limit reached, breaking tool loop", "conversation", convID)
			break
		}
	}

	// If the model returned empty content after tool rounds, try to recover.
	if resp.Content == "" && toolRounds > 0 {
		var err error
		resp, llmMessages, err = e.recoverEmptyToolResponse(ctx, convID, resp, llmMessages, accumulatedContent.String())
		if err != nil {
			return nil, llmMessages, err
		}
		totalUsage.Prompt += resp.TokensUsed.Prompt
		totalUsage.Completion += resp.TokensUsed.Completion
		totalUsage.Total += resp.TokensUsed.Total
		totalCost += resp.CostUSD
	}

	// Replace per-round usage with accumulated totals.
	resp.TokensUsed = totalUsage
	resp.CostUSD = totalCost
	return resp, llmMessages, nil
}

// recoverEmptyToolResponse attempts to recover when the LLM returns empty
// content after tool rounds. It first checks for accumulated intermediate
// content, then falls back to nudging the model for a response.
func (e *Engine) recoverEmptyToolResponse(ctx context.Context, convID string, resp *llm.ChatResponse, llmMessages []llm.Message, accumulated string) (*llm.ChatResponse, []llm.Message, error) {
	if accumulated != "" {
		e.logger.Info("using accumulated content from intermediate tool rounds",
			"accumulated_len", len(accumulated))
		resp.Content = accumulated
		return resp, llmMessages, nil
	}
	e.logger.Warn("empty response after tool rounds, retrying with nudge",
		"finish_reason", resp.FinishReason)
	llmMessages = append(llmMessages, llm.Message{
		Role:    "user",
		Content: "Please provide your response based on the tool results above.",
	})
	nudgeResp, err := e.router.Complete(ctx, convID, llmMessages)
	if err != nil {
		return nil, llmMessages, fmt.Errorf("LLM completion (nudge retry): %w", err)
	}
	return nudgeResp, llmMessages, nil
}

// executeToolCall handles one tool call: optionally awaiting approval (supervised),
// then executing it and emitting tool_start/tool_end ChatEvents.
// Returns the tool result string to be fed back to the LLM.
func (e *Engine) executeToolCall(ctx context.Context, tc llm.ToolCall, round int, convID string, supervised bool, onEvent ChatEventFunc) string {
	// Supervised tier: check auto-approve rules first, then request human approval.
	if supervised {
		if autoApproved, scope := e.approvals.ShouldAutoApprove(ctx, e.name, tc.Function.Name, convID); autoApproved {
			e.logger.Info("tool auto-approved", "tool", tc.Function.Name, "scope", scope)
			if onEvent != nil {
				onEvent(ChatEvent{
					Type:           "tool_approval",
					Tool:           tc.Function.Name,
					Round:          round,
					Text:           fmt.Sprintf("Auto-approved (%s)", scope),
					ApprovalStatus: "auto_approved",
				})
			}
		} else {
			result, approved := e.awaitToolApproval(ctx, tc, round, convID, onEvent)
			if !approved {
				return result
			}
		}
	}

	if onEvent != nil {
		onEvent(ChatEvent{Type: "tool_start", Tool: tc.Function.Name, Round: round})
	}

	toolStart := time.Now()
	e.logger.Info("executing tool", "tool", tc.Function.Name, "round", round)
	toolCtx, toolCancel := context.WithTimeout(ctx, toolExecTimeout)
	defer toolCancel()
	result, execErr := e.tools.Execute(toolCtx, tc)
	toolDur := time.Since(toolStart)

	if execErr != nil && toolCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		execErr = fmt.Errorf("tool execution timed out after %s", toolExecTimeout)
		result = execErr.Error()
	}

	if execErr != nil {
		e.logger.Warn("tool execution failed", "tool", tc.Function.Name, "round", round,
			"duration_ms", toolDur.Milliseconds(), "error", execErr)
		result = fmt.Sprintf("Tool error: %v", execErr)
	} else {
		e.logger.Info("tool execution complete", "tool", tc.Function.Name, "round", round,
			"duration_ms", toolDur.Milliseconds(), "result_len", len(result))
	}

	if onEvent != nil {
		evt := ChatEvent{Type: "tool_end", Tool: tc.Function.Name, Round: round, Duration: toolDur.Milliseconds()}
		if execErr != nil {
			evt.Error = execErr.Error()
		}
		onEvent(evt)
	}
	return result
}

// awaitToolApproval submits a tool call for approval and blocks until the
// operator approves or denies it. Emits a "tool_approval" ChatEvent so the
// adapter can render inline buttons. Returns the result string and whether
// the tool was approved.
func (e *Engine) awaitToolApproval(ctx context.Context, tc llm.ToolCall, round int, convID string, onEvent ChatEventFunc) (string, bool) {
	summary := fmt.Sprintf("Execute tool %q with args: %s", tc.Function.Name, tc.Function.Arguments)

	// Extract adapter routing from context — stored by chatWithApproval.
	adapterName, _ := ctx.Value(ctxKeyAdapter).(string)
	externalID, _ := ctx.Value(ctxKeyExternalID).(string)

	e.logger.Info("submitting tool call for approval",
		"tool", tc.Function.Name, "round", round, "conversation", convID)

	// Use Submit (non-blocking) so we can emit the event before blocking.
	noOp := func(_ context.Context, _ string) error { return nil }
	req, err := e.approvals.Submit(
		ctx, e.name, approval.ActionKindToolCall, summary,
		tc.Function.Arguments, externalID, adapterName, convID, noOp,
	)
	if err != nil {
		e.logger.Warn("tool approval submit failed", "tool", tc.Function.Name, "error", err)
		return fmt.Sprintf("Tool call approval failed: %v", err), false
	}

	// Emit event so the adapter can send the approval prompt to the user.
	if onEvent != nil {
		onEvent(ChatEvent{
			Type:             "tool_approval",
			Tool:             tc.Function.Name,
			Round:            round,
			Text:             summary,
			ApprovalID:       req.ID,
			ApprovalCallback: req.CallbackData,
		})
	}

	// Block until the approval is resolved by the operator or the timeout expires.
	approvalCtx, approvalCancel := context.WithTimeout(ctx, approvalTimeout)
	defer approvalCancel()
	status := e.approvals.WaitForResolution(approvalCtx, req.ID)

	if approvalCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		e.logger.Warn("tool approval timed out", "tool", tc.Function.Name, "id", req.ID,
			"timeout", approvalTimeout)
		return "Tool approval timed out — no response from operator.", false
	}

	if status == approval.StatusApproved {
		e.logger.Info("tool call approved", "tool", tc.Function.Name, "id", req.ID)
		return "", true
	}

	e.logger.Info("tool call denied", "tool", tc.Function.Name, "id", req.ID)
	return "Tool call was denied by the operator.", false
}

// Context keys for adapter routing info passed through the pipeline.
type ctxKey string

const (
	ctxKeyAdapter    ctxKey = "adapter"
	ctxKeyExternalID ctxKey = "external_id"
)

// validateConversationID checks that a client-supplied conversation ID is
// reasonable: non-empty, within length limits, and contains only safe characters.
func validateConversationID(id string) error {
	if len(id) > maxConversationIDLen {
		return fmt.Errorf("conversation ID exceeds maximum length of %d", maxConversationIDLen)
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("conversation ID contains invalid control character")
		}
	}
	return nil
}

// applyMemoryUpdate persists a memory update extracted from the LLM response.
func (e *Engine) applyMemoryUpdate(memUpdate string, perms *security.PermissionEngine) {
	if memUpdate == "" || e.persona == nil || !perms.CanExecute("write_memory") {
		return
	}
	if err := e.persona.UpdateMemory(memUpdate); err != nil {
		e.logger.Warn("failed to persist memory update", "error", err)
	} else {
		e.logger.Info("memory updated", "bytes", len(memUpdate))
	}
}

// processPersonaSection handles a single persona-file directive (user/soul/identity).
// It is a no-op when payload is empty, a prior approval is already pending, or the
// persona has no write path. This helper keeps processResponseDirectives within the
// cyclomatic complexity limit.
func (e *Engine) processPersonaSection(ctx context.Context, perms *security.PermissionEngine, msg adapter.IncomingMessage, convID, payload, section string, kind approval.ActionKind, description, pendingMsg, logLabel string, responseText string, pendingApproval *approval.Request) (string, *approval.Request) {
	if payload == "" || pendingApproval != nil || e.persona == nil || e.persona.Dir() == "" {
		return responseText, pendingApproval
	}
	personaRef := e.persona
	pending, text := e.processDirective(ctx, perms, directiveSpec{
		payload: payload, kind: kind,
		description: description, pendingMsg: pendingMsg,
		logLabel: logLabel, externalID: msg.ExternalID,
		adapter: msg.Adapter, convID: convID,
		applyFn: func(_ context.Context, p string) error { return personaRef.Save(section, p) },
	}, responseText)
	return text, pending
}

// processResponseDirectives extracts memory updates, user updates, soul updates,
// skill creation, and schedule addition directives from the LLM response. Returns
// the cleaned response text and a pending approval request (if any).
func (e *Engine) processResponseDirectives(ctx context.Context, resp *llm.ChatResponse, perms *security.PermissionEngine, msg adapter.IncomingMessage, convID string) (string, *approval.Request) {
	responseText, memUpdate := extractMemoryUpdate(resp.Content)
	e.applyMemoryUpdate(memUpdate, perms)

	responseText, userUpdate := extractUserUpdate(responseText)
	responseText, soulUpdate := extractSoulUpdate(responseText)
	responseText, identityUpdate := extractIdentityUpdate(responseText)
	responseText, skillPayload := extractSkillCreate(responseText)
	responseText, schedPayload := extractScheduleAdd(responseText)

	var pendingApproval *approval.Request

	responseText, pendingApproval = e.processPersonaSection(ctx, perms, msg, convID, userUpdate, "user",
		approval.ActionKindUserUpdate, "Update user profile (USER.md)",
		"\n\n_Proposed user profile update is pending your approval._", "user update",
		responseText, pendingApproval)

	responseText, pendingApproval = e.processPersonaSection(ctx, perms, msg, convID, soulUpdate, "soul",
		approval.ActionKindSoulUpdate, "Update soul identity (SOUL.md)",
		"\n\n_Proposed soul update is pending your approval._", "soul update",
		responseText, pendingApproval)

	responseText, pendingApproval = e.processPersonaSection(ctx, perms, msg, convID, identityUpdate, "identity",
		approval.ActionKindIdentityUpdate, "Update identity metadata (IDENTITY.md)",
		"\n\n_Proposed identity update is pending your approval._", "identity update",
		responseText, pendingApproval)

	if skillPayload != "" && pendingApproval == nil {
		pendingApproval, responseText = e.processDirective(ctx, perms, directiveSpec{
			payload: skillPayload, kind: approval.ActionKindCreateSkill,
			description: "Create new skill",
			pendingMsg:  "\n\n_Proposed skill creation is pending your approval._",
			logLabel:    "skill create", externalID: msg.ExternalID,
			adapter: msg.Adapter, convID: convID,
			applyFn: func(_ context.Context, payload string) error { return e.applySkillCreate(payload) },
		}, responseText)
	}

	if schedPayload != "" && pendingApproval == nil {
		pendingApproval, responseText = e.processDirective(ctx, perms, directiveSpec{
			payload: schedPayload, kind: approval.ActionKindModifySchedule,
			description: "Add new schedule",
			pendingMsg:  "\n\n_Proposed schedule addition is pending your approval._",
			logLabel:    "schedule add", externalID: msg.ExternalID,
			adapter: msg.Adapter, convID: convID,
			applyFn: func(_ context.Context, payload string) error { return e.applyScheduleAdd(payload) },
		}, responseText)
	}

	return responseText, pendingApproval
}

// HandleMessage processes a single incoming message and sends the response
// back via the adapter's SendFunc. It delegates to HandleMessageWithEvents
// with a nil event callback.
func (e *Engine) HandleMessage(ctx context.Context, msg adapter.IncomingMessage) error {
	return e.HandleMessageWithEvents(ctx, msg, nil)
}

// HandleMessageWithEvents is like HandleMessage but calls onEvent for
// intermediate pipeline events (thinking, tool calls, usage). The Dispatcher
// uses this to refresh adapter typing indicators during processing.
func (e *Engine) HandleMessageWithEvents(ctx context.Context, msg adapter.IncomingMessage, onEvent ChatEventFunc) error {
	responseText, pendingApproval, err := e.chatWithApproval(ctx, msg, onEvent)
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
