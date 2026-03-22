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
	"github.com/Temikus/denkeeper/internal/tool"
)

const maxContextMessages = 50
const maxToolRounds = 10

const memUpdateOpen = "[MEMORY_UPDATE]"
const memUpdateClose = "[/MEMORY_UPDATE]"

// Engine is the core agent orchestrator.
type Engine struct {
	router         *llm.Router
	memory         MemoryStore
	adapters       []adapter.Adapter
	permissions    *security.PermissionEngine
	persona        *persona.Persona // nil = use fallbackPrompt
	fallbackPrompt string           // used when persona is nil
	promptSuffix   string           // appended after persona/fallback (e.g. skills)
	tools          *tool.Manager    // nil = no tools available
	incoming       chan adapter.IncomingMessage
	logger         *slog.Logger
}

func NewEngine(
	router *llm.Router,
	memory MemoryStore,
	adapters []adapter.Adapter,
	permissions *security.PermissionEngine,
	p *persona.Persona,
	fallbackPrompt string,
	promptSuffix string,
	tools *tool.Manager,
	logger *slog.Logger,
) *Engine {
	return &Engine{
		router:         router,
		memory:         memory,
		adapters:       adapters,
		permissions:    permissions,
		persona:        p,
		fallbackPrompt: fallbackPrompt,
		promptSuffix:   promptSuffix,
		tools:          tools,
		incoming:       make(chan adapter.IncomingMessage, 64),
		logger:         logger,
	}
}

// buildSystemPrompt assembles the current system prompt from the persona (if set)
// or the fallback string, appending any skill instructions and the memory update
// directive when the engine has write_memory permission.
func (e *Engine) buildSystemPrompt(perms *security.PermissionEngine) string {
	var base string
	if e.persona != nil {
		base = e.persona.SystemPrompt()
		if inst := e.persona.MemoryUpdateInstruction(); inst != "" && perms.CanExecute("write_memory") {
			base += "\n\n" + inst
		}
	} else {
		base = e.fallbackPrompt
	}
	if e.promptSuffix != "" {
		return base + "\n\n" + e.promptSuffix
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

// Dispatch injects a pre-built message into the engine's incoming queue.
// It is used by the scheduler to trigger agent runs without an active adapter
// session. Blocks until the message is accepted or ctx is cancelled.
func (e *Engine) Dispatch(ctx context.Context, msg adapter.IncomingMessage) error {
	select {
	case e.incoming <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run starts the engine and blocks until the context is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	// Start all adapters
	for _, a := range e.adapters {
		a := a
		go func() {
			if err := a.Start(ctx, e.incoming); err != nil && ctx.Err() == nil {
				e.logger.Error("adapter stopped with error", "adapter", a.Name(), "error", err)
			}
		}()
	}

	e.logger.Info("engine started", "adapters", len(e.adapters))

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("engine shutting down")
			return ctx.Err()
		case msg := <-e.incoming:
			if err := e.handleMessage(ctx, msg); err != nil {
				e.logger.Error("handling message", "error", err, "adapter", msg.Adapter, "user", msg.UserName)
			}
		}
	}
}

func (e *Engine) handleMessage(ctx context.Context, msg adapter.IncomingMessage) error {
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
	// otherwise derive it from the adapter and external chat ID.
	var convID string
	if msg.ConversationID != "" {
		if err := e.memory.GetOrCreateConversationByID(ctx, msg.ConversationID); err != nil {
			return fmt.Errorf("getting conversation: %w", err)
		}
		convID = msg.ConversationID
	} else {
		var err error
		convID, err = e.memory.GetOrCreateConversation(ctx, msg.Adapter, msg.ExternalID)
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
	llmMessages = append(llmMessages, llm.Message{Role: "system", Content: e.buildSystemPrompt(perms)})
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

	// Send response back via the appropriate adapter
	for _, a := range e.adapters {
		if a.Name() == msg.Adapter {
			if err := a.Send(ctx, adapter.OutgoingMessage{
				ExternalID: msg.ExternalID,
				Text:       responseText,
			}); err != nil {
				return fmt.Errorf("sending response: %w", err)
			}
			break
		}
	}

	e.logger.Info("response sent", "adapter", msg.Adapter, "tokens", resp.TokensUsed.Total)
	return nil
}
