// Package configmcp provides an in-process MCP server that exposes denkeeper's
// own configuration as tools callable by the agent. This allows an agent to
// create skills, list skills, add schedules, and list schedules without relying
// on text-directive extraction from LLM responses.
//
// The server runs in-process using mcp.NewInMemoryTransports so no subprocess
// is spawned, approval manager references are shared directly, and latency is
// negligible.
package configmcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
)

// Deps holds the runtime dependencies injected into the Config MCP server.
// All fields are required unless noted.
type Deps struct {
	// AgentName is used as the agent label in approval requests.
	AgentName string

	// AgentSkillsDir is the directory where new skill files are written.
	// If empty, skill_create is disabled.
	AgentSkillsDir string

	// GetSkills returns the agent's current in-memory skill list.
	GetSkills func() []skill.Skill

	// AppendSkill adds a skill to the agent's in-memory skill list.
	AppendSkill func(skill.Skill)

	// Sched is the shared scheduler instance. If nil, schedule_add is disabled.
	Sched *scheduler.Scheduler

	// HandleMessage is invoked by scheduled jobs to dispatch a message to the
	// agent. Typically the engine's HandleMessage method. If nil, schedule_add
	// is disabled.
	HandleMessage func(ctx context.Context, msg adapter.IncomingMessage) error

	// Approvals is the shared approval manager. If nil, supervised mutations are
	// executed immediately (same behaviour as autonomous tier).
	Approvals *approval.Manager

	// PermissionTier returns the current effective tier for the agent
	// ("autonomous", "supervised", or "restricted").
	PermissionTier func() string

	// LifecycleMgr is the shared tool/plugin lifecycle manager. If nil,
	// tool_add/tool_remove/plugin_add/plugin_remove are disabled.
	LifecycleMgr *tool.LifecycleManager

	Logger *slog.Logger
}

// Server is the in-process Config MCP server for a single agent.
// Construct with New, then call Connect to obtain a *mcp.ClientSession that
// can be registered into a tool.Manager.
type Server struct {
	mcpServer *mcp.Server
	deps      Deps
}

// New constructs and wires the Config MCP server. Tools are registered
// immediately; the server does not begin serving until Connect is called.
func New(deps Deps) *Server {
	s := &Server{
		mcpServer: mcp.NewServer(&mcp.Implementation{
			Name:    "denkeeper-config",
			Version: "v1.0.0",
		}, nil),
		deps: deps,
	}
	s.registerTools()
	return s
}

// Connect starts the in-process server goroutine and returns a
// *mcp.ClientSession ready to be passed to tool.Manager.RegisterSession.
// The server runs until ctx is cancelled.
func (s *Server) Connect(ctx context.Context) (*mcp.ClientSession, error) {
	t1, t2 := mcp.NewInMemoryTransports()

	// Server must connect first; it drives the MCP initialisation handshake.
	if _, err := s.mcpServer.Connect(ctx, t1, nil); err != nil {
		return nil, fmt.Errorf("config MCP server connect: %w", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "denkeeper",
		Version: "v1.0.0",
	}, nil)

	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("config MCP client connect: %w", err)
	}

	return session, nil
}
