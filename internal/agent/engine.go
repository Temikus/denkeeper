package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
)

const maxContextMessages = 50

// Engine is the core agent orchestrator.
type Engine struct {
	router       *llm.Router
	memory       MemoryStore
	adapters     []adapter.Adapter
	permissions  *security.PermissionEngine
	systemPrompt string
	incoming     chan adapter.IncomingMessage
	logger       *slog.Logger
}

func NewEngine(
	router *llm.Router,
	memory MemoryStore,
	adapters []adapter.Adapter,
	permissions *security.PermissionEngine,
	systemPrompt string,
	logger *slog.Logger,
) *Engine {
	return &Engine{
		router:       router,
		memory:       memory,
		adapters:     adapters,
		permissions:  permissions,
		systemPrompt: systemPrompt,
		incoming:     make(chan adapter.IncomingMessage, 64),
		logger:       logger,
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
	if !e.permissions.CanExecute("chat") {
		return fmt.Errorf("chat action not permitted")
	}

	e.logger.Info("received message", "adapter", msg.Adapter, "user", msg.UserName, "text_len", len(msg.Text))

	// Get or create conversation
	convID, err := e.memory.GetOrCreateConversation(ctx, msg.Adapter, msg.ExternalID)
	if err != nil {
		return fmt.Errorf("getting conversation: %w", err)
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
	llmMessages = append(llmMessages, llm.Message{Role: "system", Content: e.systemPrompt})
	for _, h := range history {
		llmMessages = append(llmMessages, llm.Message{Role: h.Role, Content: h.Content})
	}

	// Call LLM
	resp, err := e.router.Complete(ctx, convID, llmMessages)
	if err != nil {
		return fmt.Errorf("LLM completion: %w", err)
	}

	// Store assistant response
	if err := e.memory.AddMessage(ctx, convID, StoredMessage{
		Role:       "assistant",
		Content:    resp.Content,
		TokensUsed: resp.TokensUsed.Total,
	}); err != nil {
		return fmt.Errorf("storing assistant message: %w", err)
	}

	// Send response back via the appropriate adapter
	for _, a := range e.adapters {
		if a.Name() == msg.Adapter {
			if err := a.Send(ctx, adapter.OutgoingMessage{
				ExternalID: msg.ExternalID,
				Text:       resp.Content,
			}); err != nil {
				return fmt.Errorf("sending response: %w", err)
			}
			break
		}
	}

	e.logger.Info("response sent", "adapter", msg.Adapter, "tokens", resp.TokensUsed.Total)
	return nil
}
