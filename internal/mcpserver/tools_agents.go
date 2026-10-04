package mcpserver

import (
	"context"
	"sort"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentListInput struct{}

type agentInfoInput struct {
	Agent string `json:"agent" jsonschema:"Agent name to get info for"`
}

func (s *Server) registerAgentTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "agent_list",
		Description: "List all configured agents with name, display name, permission tier, " +
			"LLM provider, model, skill count, and supervisor and supervisor_decider " +
			"(each when one is configured). " +
			"Requires 'agents:read' scope.",
	}, s.handleAgentList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "agent_info",
		Description: "Get detailed information for a single agent including skills, " +
			"persona sections, channel bindings, the supervising agent that reviews " +
			"its tool calls, and the decision model that screens those calls first " +
			"(name, model, mode, approve_at, deny_at), each when configured. " +
			"Requires 'agents:read' scope.",
	}, s.handleAgentInfo)
}

func (s *Server) handleAgentList(ctx context.Context, _ *mcp.CallToolRequest, _ agentListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "agents:read"); err != nil {
		return err, nil, nil
	}

	type agentSummary struct {
		Name              string `json:"name"`
		DisplayName       string `json:"display_name"`
		PermissionTier    string `json:"permission_tier"`
		Provider          string `json:"provider"`
		Model             string `json:"model"`
		SkillCount        int    `json:"skill_count"`
		Supervisor        string `json:"supervisor,omitempty"`
		SupervisorDecider string `json:"supervisor_decider,omitempty"`
	}

	names := s.deps.Dispatcher.Agents()
	agents := make([]agentSummary, 0, len(names))
	for _, name := range names {
		e := s.deps.Dispatcher.Agent(name)
		if e == nil {
			continue
		}
		summary := agentSummary{
			Name:           e.Name(),
			DisplayName:    e.DisplayName(),
			PermissionTier: e.PermissionTier(),
			Provider:       e.ProviderName(),
			Model:          e.ModelName(),
			SkillCount:     len(e.Skills()),
			Supervisor:     supervisorName(e),
		}
		if d := e.SupervisorDecider(); d != nil {
			summary.SupervisorDecider = d.Name()
		}
		agents = append(agents, summary)
	}

	r, err := toolJSON(agents)
	return r, nil, err
}

func (s *Server) handleAgentInfo(ctx context.Context, _ *mcp.CallToolRequest, input agentInfoInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "agents:read"); err != nil {
		return err, nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	type skillInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	skills := e.Skills()
	si := make([]skillInfo, len(skills))
	for i, sk := range skills {
		si[i] = skillInfo{Name: sk.Name, Description: sk.Description}
	}

	info := map[string]any{
		"name":            e.Name(),
		"display_name":    e.DisplayName(),
		"permission_tier": e.PermissionTier(),
		"provider":        e.ProviderName(),
		"model":           e.ModelName(),
		"skills":          si,
	}
	// Supervisor, decider, persona sections, and channel bindings are omitted
	// rather than reported as empty when the agent has none, so their presence
	// in the payload is itself the signal.
	if sup := supervisorName(e); sup != "" {
		info["supervisor"] = sup
	}
	if d := supervisorDeciderInfo(e); d != nil {
		info["supervisor_decider"] = d
	}
	if sections := e.PersonaSections(); sections != nil {
		info["persona_sections"] = sections
	}
	if channels := agentChannels(s.deps.Dispatcher, e.Name()); len(channels) > 0 {
		info["channels"] = channels
	}

	r, err := toolJSON(info)
	return r, nil, err
}

// supervisorName returns the name of the engine supervising e, or "" when the
// agent has no supervisor. The wiring on the live engine is authoritative —
// config is only its input.
func supervisorName(e *agent.Engine) string {
	sup := e.Supervisor()
	if sup == nil {
		return ""
	}
	return sup.Name()
}

// deciderInfo describes the decision model screening an agent's tool calls
// ahead of its supervisor.
type deciderInfo struct {
	Name      string  `json:"name"`
	Model     string  `json:"model"`
	Mode      string  `json:"mode"`
	ApproveAt float64 `json:"approve_at"`
	DenyAt    float64 `json:"deny_at"`
}

// supervisorDeciderInfo returns the decider wired to e, or nil when none is.
// Like supervisorName it reads live wiring, so a reload or PATCH shows at once.
func supervisorDeciderInfo(e *agent.Engine) *deciderInfo {
	d := e.SupervisorDecider()
	cfg, ok := e.SupervisorDeciderConfig()
	if d == nil || !ok {
		return nil
	}
	// The engine acts only on "enforce"; any other mode runs as shadow.
	mode := config.DeciderModeShadow
	if cfg.Mode == agent.DeciderModeEnforce {
		mode = agent.DeciderModeEnforce
	}
	return &deciderInfo{Name: d.Name(), Model: d.Model(), Mode: mode, ApproveAt: cfg.ApproveAt, DenyAt: cfg.DenyAt}
}

// channelBinding describes a channel routed to an agent.
type channelBinding struct {
	Name     string   `json:"name"`
	Adapters []string `json:"adapters,omitempty"`
	Delivery string   `json:"delivery,omitempty"`
	Implicit bool     `json:"implicit"`
}

// agentChannels returns the channels routed to the named agent, sorted by
// channel name for stable output. Returns nil when channels are not configured.
func agentChannels(d *agent.Dispatcher, name string) []channelBinding {
	if d == nil {
		return nil
	}
	var out []channelBinding
	for _, ch := range d.Channels() {
		if ch == nil || ch.AgentName != name {
			continue
		}
		out = append(out, channelBinding{
			Name:     ch.Name,
			Adapters: ch.Adapters,
			Delivery: ch.Delivery,
			Implicit: ch.Implicit,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
