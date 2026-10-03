// Package decidemcp provides an in-process MCP server exposing a `decide` tool:
// the agent puts typed questions about a JSON state to a decision model and
// gets probabilities back. It follows the same pattern as internal/scriptmcp:
// no subprocess, the server runs in-process via mcp.NewInMemoryTransports.
package decidemcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps holds the runtime dependencies injected into the Decide MCP server.
type Deps struct {
	// Decider answers the questions. A nil Decider registers no tool.
	Decider *llm.Decider
	// AgentName is the agent the decider spend is billed to.
	AgentName string
	// Now stamps the billing session key. Defaults to time.Now.
	Now func() time.Time

	Logger *slog.Logger
}

// Server is the in-process Decide MCP server for a single agent.
type Server struct {
	mcpServer *mcp.Server
	deps      Deps
}

// New constructs and wires the Decide MCP server. The decide tool is
// registered immediately (when a decider is set); the server does not begin
// serving until Connect is called.
func New(deps Deps) *Server {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	s := &Server{
		mcpServer: mcp.NewServer(&mcp.Implementation{
			Name:    "denkeeper-decide",
			Version: "v1.0.0",
		}, nil),
		deps: deps,
	}
	s.registerTools()
	return s
}

// Connect starts the in-process server goroutine and returns a
// *mcp.ClientSession ready to be passed to tool.Manager.RegisterSession.
func (s *Server) Connect(ctx context.Context) (*mcp.ClientSession, error) {
	t1, t2 := mcp.NewInMemoryTransports()

	if _, err := s.mcpServer.Connect(ctx, t1, nil); err != nil {
		return nil, fmt.Errorf("decide MCP server connect: %w", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "denkeeper",
		Version: "v1.0.0",
	}, nil)

	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("decide MCP client connect: %w", err)
	}

	return session, nil
}
