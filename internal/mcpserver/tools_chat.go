package mcpserver

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type chatInput struct {
	Message        string `json:"message" jsonschema:"The message to send to the agent"`
	Agent          string `json:"agent,omitempty" jsonschema:"Agent name to route to. Omit to use the default agent."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Conversation ID for session continuity. Omit to auto-generate."`
}

func (s *Server) registerChatTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "chat",
		Description: "Send a message to a Denkeeper agent and receive a text response. " +
			"The agent processes the message through its LLM with persona, skills, and tools. " +
			"Long-running calls emit progress notifications for tool executions. " +
			"Requires 'chat' scope.",
	}, s.handleChat)
}

func (s *Server) handleChat(ctx context.Context, req *mcp.CallToolRequest, input chatInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "chat"); err != nil {
		return err, nil, nil
	}

	if s.deps.Dispatcher.IsPanicked() {
		return toolError("system is in panic state — all processing paused"), nil, nil
	}

	e := s.resolveEngine(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	convID := input.ConversationID
	if convID == "" {
		convID = fmt.Sprintf("mcp:%s:%d", keyNameFromCtx(ctx), time.Now().UnixNano())
	}

	apiCfg := config.APIConfig{MCPServer: s.cfg}
	chatTimeout := apiCfg.MCPServerChatTimeout()
	ctx, cancel := context.WithTimeout(ctx, chatTimeout)
	defer cancel()

	msg := adapter.IncomingMessage{
		Adapter:        "mcp",
		ExternalID:     keyNameFromCtx(ctx),
		Text:           input.Message,
		ConversationID: convID,
		Timestamp:      time.Now(),
	}

	var progress atomic.Int64
	onEvent := func(ev agent.ChatEvent) {
		if req.Session == nil {
			return
		}
		var message string
		switch ev.Type {
		case "thinking":
			message = "Agent is thinking..."
		case "tool_start":
			progress.Add(1)
			message = fmt.Sprintf("Calling tool: %s", ev.Tool)
		case "tool_end":
			progress.Add(1)
			message = fmt.Sprintf("Tool %s completed", ev.Tool)
		case "tool_approval":
			message = fmt.Sprintf("Waiting for tool approval: %s", ev.Tool)
		case "usage":
			message = fmt.Sprintf("Response complete (%d tokens)", ev.Tokens)
		default:
			return
		}
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			Progress: float64(progress.Load()),
			Message:  message,
		})
	}

	text, err := e.ChatWithEvents(ctx, msg, onEvent)
	if err != nil {
		return toolError("chat failed: " + err.Error()), nil, nil
	}

	return toolText(text), nil, nil
}
