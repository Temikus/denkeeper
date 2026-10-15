package configmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/task"
	"github.com/Temikus/denkeeper/internal/tasktools"
)

// registerTaskTools adds the shared task tools. The caller is always this
// agent. Tier enforcement happens in the engine: task_list and task_get are
// classified read-only, so restricted agents can read but not write.
func (s *Server) registerTaskTools() {
	actor := task.AgentID(s.deps.AgentName)
	tasktools.Register(s.mcpServer, s.deps.Tasks, func(context.Context, bool) (string, *mcp.CallToolResult) {
		return actor, nil
	})
}
