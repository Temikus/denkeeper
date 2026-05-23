package mcpserver

import (
	"context"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sessionListInput struct {
	Agent  string `json:"agent,omitempty" jsonschema:"Filter by agent name"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max results (default 50)"`
	Offset int    `json:"offset,omitempty" jsonschema:"Pagination offset"`
}

type sessionMessagesInput struct {
	ConversationID string `json:"conversation_id" jsonschema:"Conversation ID to retrieve messages for"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Max messages to return (default 50)"`
}

type sessionSearchInput struct {
	Query string `json:"query" jsonschema:"Full-text search query"`
	Limit int    `json:"limit,omitempty" jsonschema:"Max results (default 20)"`
	Agent string `json:"agent,omitempty" jsonschema:"Filter by agent name"`
}

type sessionClearInput struct {
	ConversationID string `json:"conversation_id" jsonschema:"Conversation ID to clear"`
	Agent          string `json:"agent,omitempty" jsonschema:"Agent name (used to resolve engine)"`
}

type sessionCompactInput struct {
	ConversationID string `json:"conversation_id" jsonschema:"Conversation ID to compact"`
	Agent          string `json:"agent,omitempty" jsonschema:"Agent name (used to resolve engine)"`
}

func (s *Server) registerSessionTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "session_list",
		Description: "List conversations with optional agent filter and pagination. " +
			"Returns conversation IDs, adapter, message count, and creation time. " +
			"Requires 'sessions:read' scope.",
	}, s.handleSessionList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "session_messages",
		Description: "Get messages for a conversation by ID. Returns role, text, and timestamp " +
			"for each message. Requires 'sessions:read' scope.",
	}, s.handleSessionMessages)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "session_search",
		Description: "Full-text search across all conversation messages. Returns matching excerpts " +
			"with conversation IDs. Requires 'sessions:read' scope.",
	}, s.handleSessionSearch)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "session_clear",
		Description: "Clear all messages in a session while keeping the conversation row. " +
			"Requires 'sessions:write' scope.",
	}, s.handleSessionClear)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "session_compact",
		Description: "Compact a session into an LLM-generated summary, replacing all messages " +
			"with a single summary message. Requires 'sessions:write' scope.",
	}, s.handleSessionCompact)
}

func (s *Server) handleSessionList(ctx context.Context, _ *mcp.CallToolRequest, input sessionListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "sessions:read"); err != nil {
		return err, nil, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}

	convs, total, err := s.deps.Memory.ListConversations(ctx, agent.SessionListOpts{
		Limit:  limit,
		Offset: input.Offset,
		Agent:  input.Agent,
	})
	if err != nil {
		return toolError("listing sessions: " + err.Error()), nil, nil
	}

	r, err := toolJSON(map[string]any{"conversations": convs, "total": total})
	return r, nil, err
}

func (s *Server) handleSessionMessages(ctx context.Context, _ *mcp.CallToolRequest, input sessionMessagesInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "sessions:read"); err != nil {
		return err, nil, nil
	}
	if strings.TrimSpace(input.ConversationID) == "" {
		return toolError("conversation_id is required"), nil, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}

	msgs, err := s.deps.Memory.GetMessages(ctx, input.ConversationID, limit)
	if err != nil {
		return toolError("getting messages: " + err.Error()), nil, nil
	}

	r, jsonErr := toolJSON(msgs)
	return r, nil, jsonErr
}

func (s *Server) handleSessionSearch(ctx context.Context, _ *mcp.CallToolRequest, input sessionSearchInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "sessions:read"); err != nil {
		return err, nil, nil
	}
	if strings.TrimSpace(input.Query) == "" {
		return toolError("query is required"), nil, nil
	}

	ts, ok := s.deps.Memory.(agent.TelemetryStore)
	if !ok {
		return toolError("search not available"), nil, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}

	hits, err := ts.SearchMessages(ctx, input.Query, limit, input.Agent)
	if err != nil {
		return toolError("search failed: " + err.Error()), nil, nil
	}

	r, jsonErr := toolJSON(hits)
	return r, nil, jsonErr
}

func (s *Server) handleSessionClear(ctx context.Context, _ *mcp.CallToolRequest, input sessionClearInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "sessions:write"); err != nil {
		return err, nil, nil
	}
	if strings.TrimSpace(input.ConversationID) == "" {
		return toolError("conversation_id is required"), nil, nil
	}

	e := s.resolveEngine(input.Agent)
	if e == nil {
		return toolError("no agent found"), nil, nil
	}

	if err := e.ClearSession(ctx, input.ConversationID); err != nil {
		return toolError("clear failed: " + err.Error()), nil, nil
	}
	return toolText("session cleared"), nil, nil
}

func (s *Server) handleSessionCompact(ctx context.Context, _ *mcp.CallToolRequest, input sessionCompactInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "sessions:write"); err != nil {
		return err, nil, nil
	}
	if strings.TrimSpace(input.ConversationID) == "" {
		return toolError("conversation_id is required"), nil, nil
	}

	e := s.resolveEngine(input.Agent)
	if e == nil {
		return toolError("no agent found"), nil, nil
	}

	summary, err := e.CompactSession(ctx, input.ConversationID)
	if err != nil {
		return toolError("compact failed: " + err.Error()), nil, nil
	}

	r, jsonErr := toolJSON(map[string]string{"summary": summary})
	return r, nil, jsonErr
}

func (s *Server) resolveEngine(agentName string) *agent.Engine {
	if agentName != "" {
		return s.deps.Dispatcher.Agent(agentName)
	}
	return s.deps.Dispatcher.FallbackAgent()
}
