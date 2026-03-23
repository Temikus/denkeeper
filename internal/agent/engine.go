package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
)

const maxContextMessages = 50
const maxToolRounds = 10

const memUpdateOpen = "[MEMORY_UPDATE]"
const memUpdateClose = "[/MEMORY_UPDATE]"

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
	skills         []skill.Skill    // filtered per-message based on triggers
	tools          *tool.Manager    // nil = no tools available
	logger         *slog.Logger
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
		logger:         logger.With("agent", name),
	}
}

// Name returns the agent's name.
func (e *Engine) Name() string { return e.name }

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
	} else {
		base = e.fallbackPrompt
	}
	matched := skill.MatchSkills(e.skills, skill.MatchContext{
		MessageText: msg.Text,
		SkillName:   msg.SkillName,
	})
	if suffix := skill.BuildPromptSection(matched); suffix != "" {
		return base + "\n\n" + suffix
	}
	return base
}

// extractMemoryUpdate parses and removes a [MEMORY_UPDATE]...[/MEMORY_UPDATE]
// block from text. Returns the cleaned text and the extracted content (empty if
// no block was found or the block was malformed).
func extractMemoryUpdate(text string) (cleaned, memUpdate string) {
	start := strings.Index(text, memUpdateOpen)
	if start == -1 {
		return text, ""
	}
	rest := text[start+len(memUpdateOpen):]
	end := strings.Index(rest, memUpdateClose)
	if end == -1 {
		return text, ""
	}
	memUpdate = strings.TrimSpace(rest[:end])
	after := rest[end+len(memUpdateClose):]
	cleaned = strings.TrimSpace(text[:start] + after)
	return cleaned, memUpdate
}

// HandleMessage processes a single incoming message through the full pipeline:
// permission check → conversation lookup → LLM call → tool loop → response.
func (e *Engine) HandleMessage(ctx context.Context, msg adapter.IncomingMessage) error {
	// Resolve effective permissions: per-message override or global default.
	perms := e.permissions
	if msg.SessionTier != "" {
		if !security.ValidTier(msg.SessionTier) {
			e.logger.Warn("ignoring invalid session tier, using global",
				"session_tier", msg.SessionTier,
				"global_tier", e.permissions.Tier(),
			)
		} else if msg.SessionTier != e.permissions.Tier() {
			override, err := security.NewPermissionEngine(msg.SessionTier)
			if err != nil {
				e.logger.Warn("failed to create override permissions", "error", err)
			} else {
				perms = override
				e.logger.Info("using per-schedule permission tier",
					"tier", msg.SessionTier,
					"global_tier", e.permissions.Tier(),
				)
			}
		}
	}

	if !perms.CanExecute("chat") {
		return fmt.Errorf("chat action not permitted")
	}

	e.logger.Info("received message", "adapter", msg.Adapter, "user", msg.UserName, "text_len", len(msg.Text))

	// Get or create conversation — use explicit ID if provided (isolated sessions),
	// otherwise derive it from the agent name, adapter, and external chat ID.
	var convID string
	if msg.ConversationID != "" {
		if err := e.memory.GetOrCreateConversationByID(ctx, msg.ConversationID); err != nil {
			return fmt.Errorf("getting conversation: %w", err)
		}
		convID = msg.ConversationID
	} else {
		var err error
		// Namespace conversations by agent name so each agent has its own history.
		convID, err = e.memory.GetOrCreateConversation(ctx, e.name+":"+msg.Adapter, msg.ExternalID)
		if err != nil {
			return fmt.Errorf("getting conversation: %w", err)
		}
	}

	// Store incoming message
	if err := e.memory.AddMessage(ctx, convID, StoredMessage{
		Role:    "user",
		Content: msg.Text,
	}); err != nil {
		return fmt.Errorf("storing user message: %w", err)
	}

	// Load conversation history
	history, err := e.memory.GetMessages(ctx, convID, maxContextMessages)
	if err != nil {
		return fmt.Errorf("loading history: %w", err)
	}

	// Build LLM messages: system prompt + history
	llmMessages := make([]llm.Message, 0, len(history)+1)
	llmMessages = append(llmMessages, llm.Message{Role: "system", Content: e.buildSystemPrompt(perms, msg)})
	for _, h := range history {
		llmMessages = append(llmMessages, llm.Message{Role: h.Role, Content: h.Content})
	}

	// Call LLM
	resp, err := e.router.Complete(ctx, convID, llmMessages)
	if err != nil {
		return fmt.Errorf("LLM completion: %w", err)
	}

	// Tool-call loop: keep calling tools until the LLM produces a text response.
	for round := 0; resp.FinishReason == "tool_calls" && len(resp.ToolCalls) > 0; round++ {
		if round >= maxToolRounds {
			return fmt.Errorf("exceeded maximum tool call rounds (%d)", maxToolRounds)
		}
		if e.tools == nil {
			return fmt.Errorf("LLM requested tool calls but no tool manager configured")
		}
		if !perms.CanExecute("use_tools") {
			return fmt.Errorf("tool execution not permitted under %q tier", perms.Tier())
		}

		// Append the assistant's tool-call message to history.
		llmMessages = append(llmMessages, llm.Message{
			Role:      "assistant",
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call serially, append results.
		for _, tc := range resp.ToolCalls {
			e.logger.Info("executing tool", "tool", tc.Function.Name, "round", round+1)
			result, execErr := e.tools.Execute(ctx, tc)
			if execErr != nil {
				e.logger.Warn("tool execution failed", "tool", tc.Function.Name, "error", execErr)
				result = fmt.Sprintf("Tool error: %v", execErr)
			}
			llmMessages = append(llmMessages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}

		// Call LLM again with tool results.
		resp, err = e.router.Complete(ctx, convID, llmMessages)
		if err != nil {
			return fmt.Errorf("LLM completion (tool round %d): %w", round+1, err)
		}
	}

	// Extract and strip any memory update directive before storing/sending.
	responseText, memUpdate := extractMemoryUpdate(resp.Content)

	// Persist memory update if present and permitted.
	if memUpdate != "" && e.persona != nil && perms.CanExecute("write_memory") {
		if err := e.persona.UpdateMemory(memUpdate); err != nil {
			e.logger.Warn("failed to persist memory update", "error", err)
		} else {
			e.logger.Info("memory updated", "bytes", len(memUpdate))
		}
	}

	// Store assistant response (without the memory directive).
	if err := e.memory.AddMessage(ctx, convID, StoredMessage{
		Role:       "assistant",
		Content:    responseText,
		TokensUsed: resp.TokensUsed.Total,
	}); err != nil {
		return fmt.Errorf("storing assistant message: %w", err)
	}

	// Send response back via the originating adapter.
	if e.sendFunc != nil {
		if err := e.sendFunc(ctx, adapter.OutgoingMessage{
			ExternalID: msg.ExternalID,
			Text:       responseText,
			IsVoice:    msg.IsVoice,
		}); err != nil {
			return fmt.Errorf("sending response: %w", err)
		}
	}

	e.logger.Info("response sent", "adapter", msg.Adapter, "tokens", resp.TokensUsed.Total)
	return nil
}
