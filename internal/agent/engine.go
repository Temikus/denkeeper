package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agentctx"
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

const defaultMaxContextMessages = 50
const maxToolRounds = 10
const toolExecTimeout = 30 * time.Second
const defaultApprovalTimeout = 5 * time.Minute
const maxConversationIDLen = 256

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
	approvals      *approval.Manager // nil = supervised tool calls execute immediately

	// maxContextMessages limits conversation history sent to the LLM.
	maxContextMessages int

	// Approval configuration (set via SetApprovalConfig after construction).
	approvalTimeout time.Duration // default 5m
	approvalRetries int           // default 0 (no retries)

	// Extension fields wired in after construction via SetSkillDirs / SetScheduler.
	agentSkillsDir  string               // where to write new agent skill files
	globalSkillsDir string               // base global skills dir (for merge on reload)
	sched           *scheduler.Scheduler // nil = scheduling disabled

	// adapterCtx stores the current message's adapter routing info so that
	// in-process MCP servers (configmcp) can retrieve it. The MCP JSON-RPC
	// boundary prevents context.Context propagation, so we bridge via this
	// field. Protected by adapterCtxMu; set at the start of each message.
	adapterCtxMu sync.RWMutex
	adapterCtx   adapterRouting

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
		name:               name,
		router:             router,
		memory:             memory,
		sendFunc:           sendFunc,
		permissions:        permissions,
		persona:            p,
		fallbackPrompt:     fallbackPrompt,
		skills:             skills,
		tools:              tools,
		approvals:          approvals,
		maxContextMessages: defaultMaxContextMessages,
		approvalTimeout:    defaultApprovalTimeout,
		logger:             logger.With("agent", name),
		tracer:             tracer,
		mMessages:          msgs,
		mSessions:          sessions,
		mChatDur:           chatDur,
		mToolCalls:         toolCalls,
	}
}

// SetMaxContextMessages overrides the default context message limit.
// Call this after NewEngine, before the engine starts handling messages.
func (e *Engine) SetMaxContextMessages(n int) {
	if n > 0 {
		e.maxContextMessages = n
	}
}

// SetApprovalConfig configures the approval timeout and retry count.
// Call this after NewEngine, before the engine starts handling messages.
func (e *Engine) SetApprovalConfig(timeout time.Duration, retries int) {
	if timeout > 0 {
		e.approvalTimeout = timeout
	}
	e.approvalRetries = retries
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

// SetName updates the agent's name (used during rename).
func (e *Engine) SetName(name string) { e.name = name }

// DisplayName returns a human-friendly name derived from the agent's identity
// persona (if available), falling back to the agent ID.
func (e *Engine) DisplayName() string {
	if e.persona != nil {
		if id := e.persona.GetIdentity(); id != nil && id.Name != "" {
			if id.Emoji != "" {
				return id.Emoji + " " + id.Name
			}
			return id.Name
		}
	}
	return e.name
}

// PermissionTier returns the agent's default permission tier.
func (e *Engine) PermissionTier() string { return e.permissions.Tier() }

// ProviderName returns the agent's default LLM provider.
func (e *Engine) ProviderName() string { return e.router.DefaultProvider() }

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

// ListModelDetails returns enriched model metadata from all registered providers.
func (e *Engine) ListModelDetails(ctx context.Context, providerFilter string) []llm.ModelInfo {
	return e.router.ListModelDetails(ctx, providerFilter)
}

// LLMRouter returns the engine's LLM router for runtime configuration updates.
func (e *Engine) LLMRouter() *llm.Router { return e.router }

// SetModel changes the engine's default LLM model.
func (e *Engine) SetModel(model string) {
	e.router.SetDefaultModel(model)
}

// SetProvider changes the engine's default LLM provider.
func (e *Engine) SetProvider(provider string) error {
	return e.router.SetDefaultProvider(provider)
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

// AppendMemoryEntry adds a new entry to the persona's MEMORY.md.
// Returns an error if no persona is configured.
func (e *Engine) AppendMemoryEntry(entry string) error {
	if e.persona == nil {
		return fmt.Errorf("no persona configured for agent %q", e.name)
	}
	return e.persona.AppendMemoryEntry(entry)
}

// RemoveMemoryEntry removes a memory entry by heading from the persona's MEMORY.md.
// Returns an error if no persona is configured.
func (e *Engine) RemoveMemoryEntry(heading string) error {
	if e.persona == nil {
		return fmt.Errorf("no persona configured for agent %q", e.name)
	}
	return e.persona.RemoveMemoryEntry(heading)
}

// buildSystemPromptResult holds the system prompt and the skills that were
// matched for this message (used for telemetry persistence).
type buildSystemPromptResult struct {
	prompt        string
	matchedSkills []skill.Skill
}

// buildSystemPrompt assembles the current system prompt from the persona (if set)
// or the fallback string, appending trigger-matched skill instructions.
// Persona management (memory, soul, identity, user) is handled via MCP tools
// whose descriptions guide the agent on when and how to use them.
func (e *Engine) buildSystemPrompt(_ *security.PermissionEngine, msg adapter.IncomingMessage) buildSystemPromptResult {
	var base string
	if e.persona != nil {
		base = e.persona.SystemPrompt()
		base += `

## Persona Management

You have tools to manage your persona sections. Use them proactively:

- **Memory** (persona_memory_manage): When important context emerges that you should remember across sessions — user preferences, key facts, project context — append a memory entry. Prefer append over full replacement.
- **User** (persona_update): When the user shares persistent personal information (name, background, routines, preferences), update the user section.
- **Soul** (persona_update): If your core personality or values should genuinely evolve based on experience, update your soul. Do this rarely and thoughtfully.
- **Identity** (persona_update): If your name, emoji, or theme should change, update identity metadata.

User/soul/identity updates may require approval depending on your permission tier. Memory writes are always direct.`
	} else {
		base = e.fallbackPrompt
	}

	// Inject session context so the agent knows its current delivery channel.
	if msg.Adapter != "" && msg.ExternalID != "" {
		base += fmt.Sprintf(`

## Session Context

You are currently connected via the %q adapter. Your delivery channel is: %s:%s
When creating or updating schedules, use this channel value unless the user specifies otherwise.`,
			msg.Adapter, msg.Adapter, msg.ExternalID)
	}

	e.skillsMu.RLock()
	skills := e.skills
	e.skillsMu.RUnlock()

	matched := skill.MatchSkills(skills, skill.MatchContext{
		MessageText: msg.Text,
		SkillName:   msg.SkillName,
	})
	if suffix := skill.BuildPromptSection(matched); suffix != "" {
		return buildSystemPromptResult{prompt: base + "\n\n" + suffix, matchedSkills: matched}
	}
	return buildSystemPromptResult{prompt: base, matchedSkills: matched}
}

// persistTelemetry writes tool calls, skill usages, and conversation stats
// after an assistant message is stored. Errors are logged but not propagated —
// telemetry failures must not break the chat pipeline.
func (e *Engine) persistTelemetry(ctx context.Context, convID string, userMsgID, assistMsgID int64, assistMsg StoredMessage, toolRecords []ToolCallRecord, matched []skill.Skill, msg adapter.IncomingMessage) {
	store, ok := e.memory.(TelemetryStore)
	if !ok {
		return
	}

	// Persist tool call records.
	toolErrors := 0
	for _, r := range toolRecords {
		if !r.Success {
			toolErrors++
		}
	}
	if err := store.AddToolCalls(ctx, convID, assistMsgID, toolRecords); err != nil {
		e.logger.Warn("failed to persist tool calls", "error", err, "conversation", convID)
	}

	// Persist skill usages (matched skills passed from buildSystemPrompt).
	if len(matched) > 0 {
		records := make([]SkillUsageRecord, len(matched))
		for i, s := range matched {
			records[i] = SkillUsageRecord{
				SkillName: s.Name,
				MatchType: classifySkillMatch(s, msg),
			}
		}
		if err := store.AddSkillUsages(ctx, convID, userMsgID, records); err != nil {
			e.logger.Warn("failed to persist skill usages", "error", err, "conversation", convID)
		}
	}

	// Update conversation stats.
	if err := store.UpdateConversationStats(ctx, convID, assistMsg, len(toolRecords), toolErrors); err != nil {
		e.logger.Warn("failed to update conversation stats", "error", err, "conversation", convID)
	}
}

// classifySkillMatch determines the match type for a skill.
func classifySkillMatch(s skill.Skill, msg adapter.IncomingMessage) string {
	if msg.SkillName != "" && msg.SkillName == s.Name {
		return "schedule"
	}
	if len(s.Triggers) == 0 {
		return "always"
	}
	return "command"
}

// ChatEvent describes an intermediate pipeline event streamed to SSE clients.
type ChatEvent struct {
	Type     string  `json:"type"`                  // "tool_start", "tool_end", "thinking", "usage", "tool_approval", "content_delta", "thinking_delta"
	Tool     string  `json:"tool,omitempty"`        // tool name
	ToolID   string  `json:"tool_id,omitempty"`     // unique tool call ID (from LLM response)
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
		trace.WithAttributes(agentAttr, adapterAttr,
			attribute.String("agent.permission_tier", perms.Tier()),
		))
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
	span.SetAttributes(attribute.String("conversation.id", convID))

	userMsgID, err := e.memory.AddMessage(ctx, convID, StoredMessage{
		Role:    "user",
		Content: msg.Text,
	})
	if err != nil {
		return "", nil, fmt.Errorf("storing user message: %w", err)
	}

	history, err := e.memory.GetMessages(ctx, convID, e.maxContextMessages)
	if err != nil {
		return "", nil, fmt.Errorf("loading history: %w", err)
	}
	truncated := len(history) >= e.maxContextMessages
	if truncated {
		e.logger.Warn("conversation history truncated to context limit",
			"conversation_id", convID, "limit", e.maxContextMessages)
	}

	sysResult := e.buildSystemPrompt(perms, msg)

	llmMessages := make([]llm.Message, 0, len(history)+2)
	llmMessages = append(llmMessages, llm.Message{Role: "system", Content: sysResult.prompt})
	if truncated {
		llmMessages = append(llmMessages, llm.Message{
			Role:    "system",
			Content: fmt.Sprintf("[Conversation history truncated — only the most recent %d messages are shown. Earlier messages have been omitted. Do not assume context from before this point.]", e.maxContextMessages),
		})
	}
	for _, h := range history {
		llmMessages = append(llmMessages, llm.Message{Role: h.Role, Content: h.Content})
	}

	// Store adapter routing info in context for tool approval submissions,
	// and in the engine struct for in-process MCP servers (configmcp) that
	// can't receive context values across the JSON-RPC boundary.
	ctx = agentctx.WithAdapter(ctx, msg.Adapter)
	ctx = agentctx.WithExternalID(ctx, msg.ExternalID)
	ctx = agentctx.WithConversationID(ctx, convID)
	e.setAdapterContext(msg.Adapter, msg.ExternalID, convID)

	// Wrap onEvent to accumulate content_delta text. If the context is
	// cancelled mid-stream, savePartialResponse uses the accumulated content
	// to keep the conversation history consistent.
	var streamedContent strings.Builder
	wrappedEvent := wrapEventForPartialCapture(onEvent, &streamedContent)

	resp, _, toolRecords, err := e.runLLMWithTools(ctx, convID, perms, msg, llmMessages, wrappedEvent)
	if err != nil {
		e.savePartialResponse(ctx, convID, streamedContent.String())
		return "", nil, err
	}

	if onEvent != nil {
		onEvent(ChatEvent{
			Type:    "usage",
			Tokens:  resp.TokensUsed.Total,
			CostUSD: resp.CostUSD,
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

	responseText := sanitizeStaleDirectives(resp.Content, e.logger)
	var pendingApproval *approval.Request

	if responseText == "" {
		e.logger.Warn("llm returned empty response",
			"finish_reason", resp.FinishReason,
			"conversation", convID,
		)
	}

	// Use a background context for storing the assistant response so it
	// persists even if the caller's context was cancelled between the LLM
	// returning and this point (e.g. WebSocket disconnect during directive
	// processing).
	saveCtx := ctx
	if ctx.Err() != nil {
		var saveCancel context.CancelFunc
		saveCtx, saveCancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer saveCancel()
	}
	assistMsg := StoredMessage{
		Role:             "assistant",
		Content:          responseText,
		TokensUsed:       resp.TokensUsed.Total,
		Cost:             resp.CostUSD,
		Model:            resp.Model,
		Provider:         e.router.DefaultProvider(),
		TokensPrompt:     resp.TokensUsed.Prompt,
		TokensCompletion: resp.TokensUsed.Completion,
		TokensCached:     resp.TokensUsed.CachedPrompt,
	}
	assistMsgID, err := e.memory.AddMessage(saveCtx, convID, assistMsg)
	if err != nil {
		return "", nil, fmt.Errorf("storing assistant message: %w", err)
	}

	// Persist telemetry data (tool calls, skill usages, stats).
	e.persistTelemetry(saveCtx, convID, userMsgID, assistMsgID, assistMsg, toolRecords, sysResult.matchedSkills, msg)

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

// wrapEventForPartialCapture wraps onEvent to accumulate content_delta text
// into buf. Returns onEvent unchanged when it is nil.
func wrapEventForPartialCapture(onEvent ChatEventFunc, buf *strings.Builder) ChatEventFunc {
	if onEvent == nil {
		return nil
	}
	return func(evt ChatEvent) {
		if evt.Type == "content_delta" {
			buf.WriteString(evt.Text)
		}
		onEvent(evt)
	}
}

// savePartialResponse persists partial streamed content when the caller's
// context was cancelled (e.g. client disconnect) and some content was already
// streamed. It uses a fresh background context so storage succeeds even though
// the original context is done.
func (e *Engine) savePartialResponse(ctx context.Context, convID, content string) {
	if ctx.Err() == nil || content == "" {
		return
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := e.memory.AddMessage(saveCtx, convID, StoredMessage{
		Role:    "assistant",
		Content: content,
	}); err != nil {
		e.logger.Warn("failed to save partial response after disconnect",
			"error", err, "conversation", convID, "partial_len", len(content))
	} else {
		e.logger.Info("saved partial response after disconnect",
			"conversation", convID, "partial_len", len(content))
	}
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
// LLM produces a text response. Returns the response, messages, collected
// tool call records for persistence, and any error.
func (e *Engine) runLLMWithTools(ctx context.Context, convID string, perms *security.PermissionEngine, msg adapter.IncomingMessage, llmMessages []llm.Message, onEvent ChatEventFunc) (*llm.ChatResponse, []llm.Message, []ToolCallRecord, error) {
	if onEvent != nil {
		onEvent(ChatEvent{Type: "thinking"})
	}

	resp, err := e.router.CompleteStream(ctx, convID, llmMessages, streamCallbackFor(onEvent))
	if err != nil {
		return nil, llmMessages, nil, fmt.Errorf("LLM completion: %w", err)
	}

	// Validate tool execution preconditions before entering the loop.
	if resp.FinishReason == "tool_calls" && len(resp.ToolCalls) > 0 {
		if e.tools == nil {
			return nil, llmMessages, nil, fmt.Errorf("LLM requested tool calls but no tool manager configured")
		}
		if !perms.CanExecute("use_tools") {
			return nil, llmMessages, nil, fmt.Errorf("tool execution not permitted under %q tier", perms.Tier())
		}
	}

	resp, llmMessages, toolRecords, err := e.executeToolRounds(ctx, convID, perms, resp, llmMessages, onEvent)
	if err != nil {
		return nil, llmMessages, nil, err
	}

	return resp, llmMessages, toolRecords, nil
}

// executeToolRounds runs the tool-call loop, accumulating tokens/cost across
// all rounds. Returns the final response, messages, collected tool call records,
// and any error. If the model returns empty content after completing tool rounds,
// it attempts to recover by using intermediate content or nudging the model.
func (e *Engine) executeToolRounds(ctx context.Context, convID string, perms *security.PermissionEngine, resp *llm.ChatResponse, llmMessages []llm.Message, onEvent ChatEventFunc) (*llm.ChatResponse, []llm.Message, []ToolCallRecord, error) {
	var totalUsage llm.TokenUsage
	var totalCost float64
	totalUsage.Prompt += resp.TokensUsed.Prompt
	totalUsage.Completion += resp.TokensUsed.Completion
	totalUsage.Total += resp.TokensUsed.Total
	totalCost += resp.CostUSD

	supervised := perms.Tier() == "supervised" && e.approvals != nil
	parentSpan := trace.SpanFromContext(ctx)
	var toolRounds int
	var toolRecords []ToolCallRecord
	var accumulatedContent strings.Builder
	for round := 0; resp.FinishReason == "tool_calls" && len(resp.ToolCalls) > 0; round++ {
		toolRounds++
		if round >= maxToolRounds {
			return nil, llmMessages, toolRecords, fmt.Errorf("exceeded maximum tool call rounds (%d)", maxToolRounds)
		}

		recordToolRoundEvent(parentSpan, round+1, resp.ToolCalls)

		// Preserve any text content the model produced alongside tool calls.
		if resp.Content != "" {
			accumulatedContent.WriteString(resp.Content)
		}

		llmMessages = append(llmMessages, llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			e.mToolCalls.Add(ctx, 1, metric.WithAttributes(
				attribute.String("agent", e.name),
				attribute.String("tool_name", tc.Function.Name)))
			result, record := e.executeToolCall(ctx, tc, round+1, convID, supervised, onEvent)
			toolRecords = append(toolRecords, record)
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
			return nil, llmMessages, toolRecords, fmt.Errorf("LLM completion (tool round %d): %w", round+1, err)
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
		// Hard limits are enforced by the router before each LLM call.
		if e.router.CostTracker().ExceedsSoftLimit(convID) {
			if onEvent != nil {
				onEvent(ChatEvent{Type: "cost_limit", Text: "Session approaching cost limit — pausing tool use."})
			}
			e.logger.Warn("soft cost limit reached, breaking tool loop", "conversation", convID)
			break
		}
	}

	if toolRounds > 0 {
		parentSpan.SetAttributes(attribute.Int("agent.tool_rounds", toolRounds))
	}

	// If the model returned empty content after tool rounds, try to recover.
	if resp.Content == "" && toolRounds > 0 {
		var err error
		resp, llmMessages, err = e.recoverEmptyToolResponse(ctx, convID, resp, llmMessages, accumulatedContent.String())
		if err != nil {
			return nil, llmMessages, toolRecords, err
		}
		totalUsage.Prompt += resp.TokensUsed.Prompt
		totalUsage.Completion += resp.TokensUsed.Completion
		totalUsage.Total += resp.TokensUsed.Total
		totalCost += resp.CostUSD
	}

	// Replace per-round usage with accumulated totals.
	resp.TokensUsed = totalUsage
	resp.CostUSD = totalCost
	return resp, llmMessages, toolRecords, nil
}

// recordToolRoundEvent adds a span event for a tool-call round.
func recordToolRoundEvent(span trace.Span, round int, toolCalls []llm.ToolCall) {
	names := make([]string, len(toolCalls))
	for i, tc := range toolCalls {
		names[i] = tc.Function.Name
	}
	span.AddEvent("tool_call_round", trace.WithAttributes(
		attribute.Int("round", round),
		attribute.Int("tool_call_count", len(toolCalls)),
		attribute.StringSlice("tool_names", names),
	))
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
// Returns the tool result string and a ToolCallRecord for persistence.
func (e *Engine) executeToolCall(ctx context.Context, tc llm.ToolCall, round int, convID string, supervised bool, onEvent ChatEventFunc) (string, ToolCallRecord) {
	record := ToolCallRecord{
		ToolName: tc.Function.Name,
		Round:    round,
		Success:  true,
	}
	if e.tools != nil {
		record.ServerName = e.tools.ToolServer(tc.Function.Name)
	}

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
				record.Success = false
				record.ErrorMsg = "denied"
				return result, record
			}
		}
	}

	if onEvent != nil {
		onEvent(ChatEvent{Type: "tool_start", Tool: tc.Function.Name, ToolID: tc.ID, Round: round})
	}

	toolStart := time.Now()
	e.logger.Info("executing tool", "tool", tc.Function.Name, "round", round)
	toolCtx, toolCancel := context.WithTimeout(ctx, toolExecTimeout)
	defer toolCancel()
	result, execErr := e.tools.Execute(toolCtx, tc)
	toolDur := time.Since(toolStart)
	record.DurationMs = toolDur.Milliseconds()

	if execErr != nil && toolCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		execErr = fmt.Errorf("tool execution timed out after %s", toolExecTimeout)
		result = execErr.Error()
	}

	if execErr != nil {
		e.logger.Warn("tool execution failed", "tool", tc.Function.Name, "round", round,
			"duration_ms", toolDur.Milliseconds(), "error", execErr)
		result = fmt.Sprintf("Tool error: %v", execErr)
		record.Success = false
		record.ErrorMsg = execErr.Error()
	} else {
		e.logger.Info("tool execution complete", "tool", tc.Function.Name, "round", round,
			"duration_ms", toolDur.Milliseconds(), "result_len", len(result))
	}

	if onEvent != nil {
		evt := ChatEvent{Type: "tool_end", Tool: tc.Function.Name, ToolID: tc.ID, Round: round, Duration: toolDur.Milliseconds()}
		if execErr != nil {
			evt.Error = execErr.Error()
		}
		onEvent(evt)
	}
	return result, record
}

// awaitToolApproval submits a tool call for approval and blocks until the
// operator approves or denies it. Emits a "tool_approval" ChatEvent so the
// adapter can render inline buttons. On timeout, retries up to
// e.approvalRetries times before giving up. Returns the result string and
// whether the tool was approved.
func (e *Engine) awaitToolApproval(ctx context.Context, tc llm.ToolCall, round int, convID string, onEvent ChatEventFunc) (string, bool) {
	adapterName := agentctx.Adapter(ctx)
	externalID := agentctx.ExternalID(ctx)
	noOp := func(_ context.Context, _ string) error { return nil }

	maxAttempts := 1 + e.approvalRetries
	var prevReqID string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Expire the previous timed-out approval so stale buttons don't linger.
		if prevReqID != "" {
			if _, err := e.approvals.Resolve(ctx, prevReqID, false, "timeout-retry"); err != nil {
				e.logger.Debug("failed to expire previous approval on retry", "id", prevReqID, "error", err)
			}
		}

		summary := fmt.Sprintf("Execute tool %q with args: %s", tc.Function.Name, tc.Function.Arguments)
		if attempt > 1 {
			summary = fmt.Sprintf("[retry %d/%d] %s", attempt-1, e.approvalRetries, summary)
		}

		e.logger.Info("submitting tool call for approval",
			"tool", tc.Function.Name, "round", round,
			"conversation", convID, "attempt", attempt)

		req, err := e.approvals.Submit(
			ctx, e.name, approval.ActionKindToolCall, summary,
			tc.Function.Arguments, externalID, adapterName, convID, noOp,
		)
		if err != nil {
			e.logger.Warn("tool approval submit failed", "tool", tc.Function.Name, "error", err)
			return fmt.Sprintf("Tool call approval failed: %v", err), false
		}

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

		approvalCtx, approvalCancel := context.WithTimeout(ctx, e.approvalTimeout)
		status := e.approvals.WaitForResolution(approvalCtx, req.ID)
		timedOut := approvalCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
		approvalCancel()

		if timedOut {
			prevReqID = req.ID
			if attempt < maxAttempts {
				e.logger.Warn("tool approval timed out, retrying",
					"tool", tc.Function.Name, "id", req.ID,
					"attempt", attempt, "max_attempts", maxAttempts)
				continue
			}
			e.logger.Warn("tool approval timed out",
				"tool", tc.Function.Name, "id", req.ID,
				"timeout", e.approvalTimeout)
			return "Tool approval timed out — no response from operator.", false
		}

		if status == approval.StatusApproved {
			e.logger.Info("tool call approved", "tool", tc.Function.Name, "id", req.ID)
			return "", true
		}

		e.logger.Info("tool call denied", "tool", tc.Function.Name, "id", req.ID)
		return "Tool call was denied by the operator.", false
	}
	return "Tool approval timed out — no response from operator.", false
}

// adapterRouting stores adapter routing info for the current message.
// This lives on the Engine (not solely in context) because MCP tool
// handlers receive a JSON-RPC context that cannot carry Go values.
type adapterRouting struct {
	adapter        string
	externalID     string
	conversationID string
}

// setAdapterContext stores the current message's adapter routing info.
func (e *Engine) setAdapterContext(adapter, externalID, conversationID string) {
	e.adapterCtxMu.Lock()
	e.adapterCtx = adapterRouting{
		adapter:        adapter,
		externalID:     externalID,
		conversationID: conversationID,
	}
	e.adapterCtxMu.Unlock()
}

// AdapterContext returns the adapter routing info for the current in-flight
// message. Designed to be wired into configmcp.Deps.AdapterContext so that
// in-process MCP servers can populate approval requests with routing info.
func (e *Engine) AdapterContext() (adapterName, externalID, conversationID string) {
	e.adapterCtxMu.RLock()
	defer e.adapterCtxMu.RUnlock()
	return e.adapterCtx.adapter, e.adapterCtx.externalID, e.adapterCtx.conversationID
}

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

// staleDirectiveTags lists the open/close tag pairs for directives that were
// removed in favour of MCP tools. If the LLM still produces them (from cached
// conversation context), sanitizeStaleDirectives strips them so the user doesn't
// see raw tags. This is temporary — remove after a few releases.
var staleDirectiveTags = [][2]string{
	{"[MEMORY_UPDATE]", "[/MEMORY_UPDATE]"},
	{"[USER_UPDATE]", "[/USER_UPDATE]"},
	{"[SOUL_UPDATE]", "[/SOUL_UPDATE]"},
	{"[IDENTITY_UPDATE]", "[/IDENTITY_UPDATE]"},
	{"[SKILL_CREATE]", "[/SKILL_CREATE]"},
	{"[SCHEDULE_ADD]", "[/SCHEDULE_ADD]"},
}

// sanitizeStaleDirectives strips any leftover [TAG]...[/TAG] blocks from the
// LLM response text. These tags were part of the old directive system and may
// still appear if the LLM has cached conversation context. The content inside
// the tags is discarded (not processed) — MCP tools are the sole mechanism now.
// The full payload is logged at Warn level so operators can see what was lost.
func sanitizeStaleDirectives(text string, logger *slog.Logger) string {
	for _, pair := range staleDirectiveTags {
		openTag, closeTag := pair[0], pair[1]
		for {
			start := strings.Index(text, openTag)
			if start == -1 {
				break
			}
			rest := text[start+len(openTag):]
			end := strings.Index(rest, closeTag)
			if end == -1 {
				break
			}
			payload := strings.TrimSpace(rest[:end])
			// Truncate logged payload to avoid flooding logs.
			logPayload := payload
			if len(logPayload) > 500 {
				logPayload = logPayload[:500] + "...(truncated)"
			}
			logger.Warn("stripped stale directive from response — content discarded, use MCP tools instead",
				"tag", openTag,
				"payload_len", len(payload),
				"payload", logPayload,
			)
			text = strings.TrimSpace(text[:start] + rest[end+len(closeTag):])
		}
	}
	return text
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
