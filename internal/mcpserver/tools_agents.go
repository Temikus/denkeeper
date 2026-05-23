package mcpserver

import (
	"context"

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
			"LLM provider, model, and skill count. Requires 'agents:read' scope.",
	}, s.handleAgentList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "agent_info",
		Description: "Get detailed information for a single agent including skills, " +
			"persona sections, and channel bindings. Requires 'agents:read' scope.",
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

	r, err := toolJSON(info)
	return r, nil, err
}
