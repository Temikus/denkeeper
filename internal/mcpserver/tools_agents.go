package mcpserver

import (
	"context"
	"sort"

	"github.com/Temikus/denkeeper/internal/agent"
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
			"LLM provider, model, skill count, and supervisor (when one is configured). " +
			"Requires 'agents:read' scope.",
	}, s.handleAgentList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "agent_info",
		Description: "Get detailed information for a single agent including skills, " +
			"persona sections, channel bindings, and the supervising agent that reviews " +
			"its tool calls (when one is configured). Requires 'agents:read' scope.",
	}, s.handleAgentInfo)
}

func (s *Server) handleAgentList(ctx context.Context, _ *mcp.CallToolRequest, _ agentListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "agents:read"); err != nil {
		return err, nil, nil
	}

	type agentSummary struct {
		Name           string `json:"name"`
		DisplayName    string `json:"display_name"`
		PermissionTier string `json:"permission_tier"`
		Provider       string `json:"provider"`
		Model          string `json:"model"`
		SkillCount     int    `json:"skill_count"`
		Supervisor     string `json:"supervisor,omitempty"`
	}

	names := s.deps.Dispatcher.Agents()
	agents := make([]agentSummary, 0, len(names))
	for _, name := range names {
		e := s.deps.Dispatcher.Agent(name)
		if e == nil {
			continue
		}
		agents = append(agents, agentSummary{
			Name:           e.Name(),
			DisplayName:    e.DisplayName(),
			PermissionTier: e.PermissionTier(),
			Provider:       e.ProviderName(),
			Model:          e.ModelName(),
			SkillCount:     len(e.Skills()),
			Supervisor:     supervisorName(e),
		})
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
	// Supervisor, persona sections, and channel bindings are omitted rather
	// than reported as empty when the agent has none, so their presence in the
	// payload is itself the signal.
	if sup := supervisorName(e); sup != "" {
		info["supervisor"] = sup
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
