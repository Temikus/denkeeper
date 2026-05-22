package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type toolListInput struct{}

type toolHealthInput struct {
	Name string `json:"name" jsonschema:"Tool server name"`
}

type toolRestartInput struct {
	Name string `json:"name" jsonschema:"Tool server name to restart"`
}

func (s *Server) registerToolMgmtTools() {
	if s.deps.LifecycleMgr == nil {
		return
	}

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "tool_list",
		Description: "List MCP tool servers with health status, tool names, and connection info. " +
			"Requires 'tools:read' scope.",
	}, s.handleToolList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "tool_health",
		Description: "Get health details for a specific MCP tool server. " +
			"Requires 'tools:read' scope.",
	}, s.handleToolHealth)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "tool_restart",
		Description: "Restart a crashed or errored MCP tool server. " +
			"Requires 'tools:write' scope.",
	}, s.handleToolRestart)
}

func (s *Server) handleToolList(ctx context.Context, _ *mcp.CallToolRequest, _ toolListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "tools:read"); err != nil {
		return err, nil, nil
	}

	statuses := s.deps.LifecycleMgr.ListTools()
	r, err := toolJSON(statuses)
	return r, nil, err
}

func (s *Server) handleToolHealth(ctx context.Context, _ *mcp.CallToolRequest, input toolHealthInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "tools:read"); err != nil {
		return err, nil, nil
	}

	for _, st := range s.deps.LifecycleMgr.ListTools() {
		if st.Name == input.Name {
			r, err := toolJSON(st)
			return r, nil, err
		}
	}
	return toolError("tool server not found: " + input.Name), nil, nil
}

func (s *Server) handleToolRestart(ctx context.Context, _ *mcp.CallToolRequest, input toolRestartInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "tools:write"); err != nil {
		return err, nil, nil
	}

	if err := s.deps.LifecycleMgr.RestartTool(ctx, input.Name); err != nil {
		return toolError("restart failed: " + err.Error()), nil, nil
	}
	return toolText("tool server restarted: " + input.Name), nil, nil
}
