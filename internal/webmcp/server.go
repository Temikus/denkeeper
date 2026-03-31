// Package webmcp provides an in-process MCP server that exposes web search
// and URL fetching as tools callable by agents. It follows the same pattern
// as internal/configmcp: no subprocess is spawned, the server runs in-process
// using mcp.NewInMemoryTransports.
package webmcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/webfetch"
	"github.com/Temikus/denkeeper/internal/websearch"
)

// Deps holds the runtime dependencies injected into the Web MCP server.
type Deps struct {
	// SearchProvider performs web searches. If nil, web_search tool is not registered.
	SearchProvider websearch.Provider

	// Fetcher retrieves URL content. If nil, web_fetch tool is not registered.
	Fetcher webfetch.Fetcher

	// PermissionTier returns the current effective tier for the agent.
	PermissionTier func() string

	Logger *slog.Logger
}

// Server is the in-process Web MCP server for a single agent.
type Server struct {
	mcpServer *mcp.Server
	deps      Deps
}

// New constructs and wires the Web MCP server. Tools are registered
// immediately; the server does not begin serving until Connect is called.
func New(deps Deps) *Server {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}

	s := &Server{
		mcpServer: mcp.NewServer(&mcp.Implementation{
			Name:    "denkeeper-web",
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
		return nil, fmt.Errorf("web MCP server connect: %w", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "denkeeper",
		Version: "v1.0.0",
	}, nil)

	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("web MCP client connect: %w", err)
	}

	return session, nil
}
