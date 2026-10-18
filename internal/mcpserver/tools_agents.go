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

type agentUpdateInput struct {
	Agent           string `json:"agent" jsonschema:"Agent name to update"`
	MaxOutputTokens *int   `json:"max_output_tokens,omitempty" jsonschema:"Output-token cap per completion. 0 inherits llm.max_output_tokens. Above the model's output limit the provider rejects every request"`
	MaxToolRounds   *int   `json:"max_tool_rounds,omitempty" jsonschema:"Tool-call rounds per turn, at least 1"`
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

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "agent_update",
		Description: "Change an agent's max_output_tokens and/or max_tool_rounds. " +
			"Takes effect on the next LLM call and is saved to the config file. " +
			"Omitted fields are left unchanged. Requires 'agents:write' scope.",
	}, s.handleAgentUpdate)
}

func (s *Server) handleAgentUpdate(ctx context.Context, _ *mcp.CallToolRequest, input agentUpdateInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "agents:write"); err != nil {
		return err, nil, nil
	}
	if msg := validateAgentUpdate(input); msg != "" {
		return toolError(msg), nil, nil
	}
	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	s.applyAgentUpdate(e, input)
	persisted := s.persistAgentUpdate(input)

	r, err := toolJSON(map[string]any{
		"status":            "updated",
		"agent":             e.Name(),
		"max_output_tokens": e.MaxOutputTokens(),
		"max_tool_rounds":   e.MaxToolRounds(),
		"persisted":         persisted,
	})
	return r, nil, err
}

// validateAgentUpdate mirrors the REST PATCH /agents/{name} rules.
func validateAgentUpdate(input agentUpdateInput) string {
	if input.MaxOutputTokens == nil && input.MaxToolRounds == nil {
		return "nothing to update: set max_output_tokens and/or max_tool_rounds"
	}
	if input.MaxOutputTokens != nil && *input.MaxOutputTokens < 0 {
		return "max_output_tokens must be >= 0 (0 = inherit llm.max_output_tokens)"
	}
	if input.MaxToolRounds != nil && *input.MaxToolRounds <= 0 {
		return "max_tool_rounds must be >= 1"
	}
	return ""
}

// applyAgentUpdate changes the live engine and the in-memory config.
func (s *Server) applyAgentUpdate(e *agent.Engine, input agentUpdateInput) {
	if input.MaxOutputTokens != nil {
		global := 0
		if s.deps.Config != nil {
			global = s.deps.Config.Get().LLM.MaxOutputTokens
		}
		ac := config.AgentInstanceConfig{MaxOutputTokens: *input.MaxOutputTokens}
		e.SetMaxOutputTokens(ac.EffectiveMaxOutputTokens(global))
	}
	if input.MaxToolRounds != nil {
		e.SetMaxToolRounds(*input.MaxToolRounds)
	}
	if s.deps.Config == nil {
		return
	}
	s.deps.Config.Update(func(c *config.Config) {
		for i := range c.Agents {
			if c.Agents[i].Name != input.Agent {
				continue
			}
			if input.MaxOutputTokens != nil {
				c.Agents[i].MaxOutputTokens = *input.MaxOutputTokens
			}
			if input.MaxToolRounds != nil {
				c.Agents[i].MaxToolRounds = *input.MaxToolRounds
			}
			return
		}
	})
}

// persistAgentUpdate writes the change to TOML. It reports false when there
// is no config file or the write failed; the live change stands either way.
func (s *Server) persistAgentUpdate(input agentUpdateInput) bool {
	if s.deps.ConfigPath == "" {
		return false
	}
	changes := map[string]any{}
	if input.MaxOutputTokens != nil {
		changes["max_output_tokens"] = *input.MaxOutputTokens
	}
	if input.MaxToolRounds != nil {
		changes["max_tool_rounds"] = *input.MaxToolRounds
	}
	if err := config.UpdateAgentInConfig(s.deps.ConfigPath, input.Agent, changes); err != nil {
		s.deps.Logger.Error("agent updated but config persistence failed", "agent", input.Agent, "error", err)
		return false
	}
	return true
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
		"max_tool_rounds": e.MaxToolRounds(),
		"skills":          si,
	}
	// Omitted at 0, the provider default, like the fields below.
	if n := e.MaxOutputTokens(); n > 0 {
		info["max_output_tokens"] = n
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
