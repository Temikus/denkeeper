// Package mcpserver exposes Denkeeper as an HTTP-facing MCP server at
// /api/v1/mcp. External MCP clients (Claude Code, other AI tools) connect
// via Streamable HTTP or SSE transport to interact with agents, skills,
// schedules, and other Denkeeper state.
//
// This differs from configmcp and webmcp which are in-process MCP servers
// using in-memory transports for per-agent tool access.
package mcpserver

import (
	"log/slog"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/tool"
)

// ScheduleManager is the subset of scheduler.Scheduler used by the MCP
// server. Using an interface allows tests to inject failures.
type ScheduleManager interface {
	AgentEntries() []scheduler.Entry
	EntriesByAgent(agent string) []scheduler.Entry
	RegisterAndStart(cfg scheduler.Config, job scheduler.JobFunc) error
	GetEntry(name string) (scheduler.Entry, bool)
	Unregister(name string) error
}

// Deps holds the application dependencies the MCP server needs.
type Deps struct {
	Dispatcher      *agent.Dispatcher
	Scheduler       ScheduleManager
	CostTracker     *llm.CostTracker
	Memory          agent.MemoryStore
	Config          *config.Config
	Approvals       *approval.Manager
	LifecycleMgr    *tool.LifecycleManager
	KeyStore        *api.KeyStore
	TOMLKeys        []config.APIKeyConfig
	KVStore         kv.Store
	ChannelResolver configmcp.ChannelResolver
	Auditor         audit.Emitter
	AuditStore      audit.Store
	ConfigPath      string
	Version         string
	Logger          *slog.Logger
}
