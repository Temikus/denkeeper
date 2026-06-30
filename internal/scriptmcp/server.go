// Package scriptmcp provides an in-process MCP server exposing a sandboxed
// JavaScript execution tool for deterministic data transformation/formatting.
// It follows the same pattern as internal/webmcp and internal/configmcp: no
// subprocess is spawned; the server runs in-process via mcp.NewInMemoryTransports.
package scriptmcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps holds the runtime dependencies injected into the Script MCP server.
type Deps struct {
	// Enabled gates tool registration. When false, no tool is registered.
	Enabled bool
	// Timeout is the wall-clock limit for a single snippet. Default applied by caller.
	Timeout time.Duration
	// MaxOutputChars caps the returned result length.
	MaxOutputChars int
	// MaxInputBytes caps the accepted input payload size.
	MaxInputBytes int
	// Sem bounds simultaneous VM executions. It is shared across all per-agent
	// Script servers so the cap is process-global, protecting the shared goja
	// heap. A nil semaphore means unbounded concurrency. Use NewSemaphore.
	Sem chan struct{}
	// AgentSem additionally bounds simultaneous VM executions for this one agent,
	// so a single agent can't monopolize the global pool. Not shared between
	// agents. A nil semaphore means no per-agent limit. Use NewSemaphore.
	AgentSem chan struct{}
	// PermissionTier returns the current effective tier for the agent.
	PermissionTier func() string

	Logger *slog.Logger
}

// NewSemaphore returns a concurrency semaphore bounding simultaneous VM
// executions to n. A non-positive n returns nil, meaning unbounded. Construct
// one per process and share it across every agent's Deps so the cap is global.
func NewSemaphore(n int) chan struct{} {
	if n <= 0 {
		return nil
	}
	return make(chan struct{}, n)
}

// Server is the in-process Script MCP server for a single agent.
type Server struct {
	mcpServer *mcp.Server
	deps      Deps
}

// New constructs and wires the Script MCP server. The run_javascript tool is
// registered immediately (when enabled); the server does not begin serving
// until Connect is called.
func New(deps Deps) *Server {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	s := &Server{
		mcpServer: mcp.NewServer(&mcp.Implementation{
			Name:    "denkeeper-script",
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
		return nil, fmt.Errorf("script MCP server connect: %w", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "denkeeper",
		Version: "v1.0.0",
	}, nil)

	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("script MCP client connect: %w", err)
	}

	return session, nil
}
