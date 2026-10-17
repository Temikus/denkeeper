package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/task"
	"github.com/Temikus/denkeeper/internal/tasktools"
)

// registerTaskTools adds the shared task tools. An external caller acts as
// the member named after its API key ("ext:<key name>"), even with an admin
// key: the MCP server is the agent surface, and the operator acts over REST.
func (s *Server) registerTaskTools() {
	if s.deps.Tasks == nil {
		return
	}
	tasktools.Register(s.mcpServer, s.deps.Tasks, func(ctx context.Context, write bool) (string, *mcp.CallToolResult) {
		scope := "tasks:read"
		if write {
			scope = "tasks:write"
		}
		if denied := requireScope(ctx, scope); denied != nil {
			return "", denied
		}
		name := keyNameFromCtx(ctx)
		if name == "" {
			return "", toolError("task tools need a named API key: the key's name is who you are on tasks")
		}
		return task.ExternalID(name), nil
	})
}
