// Package configmcp provides an in-process MCP server that exposes denkeeper's
// own configuration as tools callable by the agent. This allows an agent to
// create skills, list skills, add schedules, and list schedules without relying
// on text-directive extraction from LLM responses.
//
// The server runs in-process using mcp.NewInMemoryTransports so no subprocess
// is spawned and latency is negligible. Approval for Config MCP tool calls is
// handled by the Engine's supervised tool-call flow, not by Config MCP itself.
package configmcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/browser"
	"github.com/Temikus/denkeeper/internal/kv"
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

	// MaxSkillBytes caps the size of a persisted skill file. 0 or negative
	// disables the check. Sourced from [skills] max_bytes.
	MaxSkillBytes int

	// GetSkills returns the agent's current in-memory skill list.
	GetSkills func() []skill.Skill

	// AppendSkill adds a skill to the agent's in-memory skill list.
	AppendSkill func(skill.Skill)

	// GetSkill returns a single skill by name and true, or zero value and false.
	// If nil, skill_get is disabled.
	GetSkill func(string) (skill.Skill, bool)

	// UpdateSkill replaces an existing skill by name. Returns false if not found.
	// If nil, skill_update is disabled.
	UpdateSkill func(string, skill.Skill) bool

	// RemoveSkill removes a skill by name. Returns false if not found.
	// Required for skill rename support.
	RemoveSkill func(string) bool

	// Sched is the shared scheduler instance. If nil, schedule_add is disabled.
	Sched *scheduler.Scheduler

	// HandleMessage is invoked by scheduled jobs to dispatch a message to the
	// agent. Typically the engine's HandleMessage method. If nil, schedule_add
	// is disabled.
	HandleMessage func(ctx context.Context, msg adapter.IncomingMessage) error

	// ResolveAgentHandler resolves another agent's HandleMessage by name.
	// Used by schedule_update when the agent field is changed. Returns nil
	// if the agent is not found. If nil, cross-agent schedule reassignment
	// is not supported via Config MCP.
	ResolveAgentHandler func(name string) func(context.Context, adapter.IncomingMessage) error

	// AgentLocation resolves the effective timezone for a target agent's
	// scheduled-message headers (agent override > global). May be nil or
	// return nil; the scheduler's location is used as the fallback.
	AgentLocation func(name string) *time.Location

	// PermissionTier returns the current effective tier for the agent
	// ("autonomous", "supervised", or "restricted").
	PermissionTier func() string

	// LifecycleMgr is the shared tool/plugin lifecycle manager. If nil,
	// tool_add/tool_remove/plugin_add/plugin_remove are disabled.
	LifecycleMgr *tool.LifecycleManager

	// KVStore is the per-agent key-value store. If nil, kv_* tools are disabled.
	KVStore kv.Store

	// CostSummary returns a snapshot of cost tracking data. If nil,
	// get_cost_summary is disabled.
	CostSummary func() CostSummaryData

	// TelemetrySummary returns aggregated per-tool/per-skill telemetry,
	// optionally restricted to events after since. If nil, get_cost_summary
	// returns cost data only.
	TelemetrySummary func(ctx context.Context, since *time.Time) (*agent.TelemetrySummary, error)

	// SetFallbacks replaces the LLM router's fallback rule list. If nil,
	// set_fallback is disabled.
	SetFallbacks func(rules []FallbackRuleInput)

	// BrowserProfiles is the shared browser profile service. If nil,
	// browser_profile_* tools are disabled.
	BrowserProfiles *browser.ProfileService

	// GetPersonaSection returns (content, editable, agentMutable, ok) for a
	// persona section. If nil, persona_get is disabled.
	GetPersonaSection func(section string) (string, bool, bool, bool)

	// SavePersonaSection writes content to a persona section. If nil,
	// persona_update is disabled.
	SavePersonaSection func(section, content string) error

	// AppendMemoryEntry adds a new entry to MEMORY.md separated by "---".
	// If nil, persona_memory_manage append is disabled.
	AppendMemoryEntry func(entry string) error

	// RemoveMemoryEntry removes a memory entry by heading from MEMORY.md.
	// If nil, persona_memory_manage remove is disabled.
	RemoveMemoryEntry func(heading string) error

	// ConfigPath is the path to the TOML config file. When non-empty,
	// schedule mutations are persisted to disk so they survive restarts.
	ConfigPath string

	// ChannelResolver resolves @channelname references in schedule channels.
	// If nil, @channelname is not supported for schedules created via Config MCP.
	ChannelResolver ChannelResolver

	// GetChannels returns all configured channels. Nil → channel tools not registered.
	GetChannels func() map[string]*agent.Channel

	// SetActiveChannel switches active channel for an adapter key.
	// Nil → channel_switch not registered.
	SetActiveChannel func(ctx context.Context, adapterKey, channelName string) error

	// ActiveChannelsForChannel returns adapter keys currently active on a channel.
	ActiveChannelsForChannel func(channelName string) []string

	// Auditor emits audit events. If nil, broadcast delivery audit is disabled.
	Auditor audit.Emitter

	// IsSkillPinned checks if a skill is pinned (curator-immune). Nil = not pinned.
	IsSkillPinned func(name string) (bool, error)

	// BumpSkillView records a skill view in telemetry. Best-effort; nil = no-op.
	BumpSkillView func(agent, skill string)
	// BumpSkillPatch records a skill patch in telemetry. Best-effort; nil = no-op.
	BumpSkillPatch func(agent, skill string)
	// SetSkillOrigin marks a skill's provenance. Best-effort; nil = no-op.
	SetSkillOrigin func(agent, skill, origin string)

	// SearchMessages searches across conversations for content matching a query.
	// Nil → session_search tool not registered.
	SearchMessages func(ctx context.Context, query string, limit int, agentFilter string) ([]agent.MessageSearchHit, error)

	// NudgeReset resets nudge counters after an agent self-write.
	// kind: "memory" or "skill". The callee resolves convID from engine adapter context.
	NudgeReset func(kind string)

	Logger *slog.Logger
}

// CostSummaryData holds the data returned by the get_cost_summary tool.
type CostSummaryData struct {
	// GlobalCost and SessionCosts come from the in-memory CostTracker and
	// reflect spend since the last restart only — they drive live budget
	// enforcement. For persistent lifetime spend, use LifetimeCost/ByModel.
	GlobalCost    float64            `json:"global_cost"`
	MaxPerSession float64            `json:"max_per_session"`
	SessionCosts  map[string]float64 `json:"session_costs"`

	// LifetimeCost is the persistent all-time spend (sum of ByModel costs).
	// Unlike GlobalCost it survives restarts. It is populated only for an
	// all-time query; when the 'days' filter is set the bounded figure goes to
	// WindowCost instead, so lifetime_cost never carries a windowed number.
	LifetimeCost float64 `json:"lifetime_cost,omitempty"`
	// WindowCost is the persistent spend over the last WindowDays days, set only
	// when the 'days' filter is provided. Like LifetimeCost it survives restarts,
	// but it is a bounded window — use it (not GlobalCost) for restart-proof
	// week-over-week trend comparisons. LifetimeCost and WindowCost are mutually
	// exclusive: one carries the all-time total, the other the windowed total.
	WindowCost float64 `json:"window_cost,omitempty"`
	// WindowDays echoes the 'days' filter that produced WindowCost.
	WindowDays int `json:"window_days,omitempty"`
	// ByModel, ByTool and BySkill are populated from TelemetrySummary when
	// available and reflect persistent storage (global across agents).
	ByModel []agent.ModelCostSummary  `json:"by_model,omitempty"`
	ByTool  []agent.ToolUsageSummary  `json:"by_tool,omitempty"`
	BySkill []agent.SkillUsageSummary `json:"by_skill,omitempty"`
	// ByToolSkill breaks tool reliability down per owning (skill, version) so a
	// skill's tool behaviour can be compared across versions.
	ByToolSkill []agent.ToolSkillUsageSummary `json:"by_tool_skill,omitempty"`
	// TelemetryError is set when the telemetry lookup failed; cost fields
	// above are still valid.
	TelemetryError string `json:"telemetry_error,omitempty"`
}

// FallbackRuleInput describes a single fallback rule as provided by the agent.
type FallbackRuleInput struct {
	Trigger    string `json:"trigger"`
	Action     string `json:"action"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Scope      string `json:"scope,omitempty"`
	MaxRetries int    `json:"max_retries,omitempty"`
	Backoff    string `json:"backoff,omitempty"`
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
