package mcpserver

import (
	"context"
	"fmt"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type personaGetInput struct {
	Agent   string `json:"agent" jsonschema:"Agent name"`
	Section string `json:"section" jsonschema:"Persona section: identity, soul, user or memory"`
}

type personaUpdateInput struct {
	Agent   string `json:"agent" jsonschema:"Agent name"`
	Section string `json:"section" jsonschema:"Persona section: identity, soul, user or memory"`
	Content string `json:"content" jsonschema:"The whole new section text. It replaces the section; for identity send the whole file, frontmatter included"`
}

func (s *Server) registerPersonaTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "persona_get",
		Description: "Read one persona section of an agent: identity, soul, user or memory. " +
			"Returns the content and whether the section is editable and agent-mutable. " +
			"agent_info lists which sections have content. Requires 'agents:read' scope.",
	}, s.handlePersonaGet)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "persona_update",
		Description: "Replace one persona section of an agent (identity, soul, user or memory) " +
			"with new text. This replaces the whole section, so read it with persona_get first " +
			"and send the full new text. soul cannot be empty. Takes effect on the agent's next turn. " +
			"Requires 'agents:write' scope.",
	}, s.handlePersonaUpdate)
}

// personaTarget resolves the agent and checks the section the way REST's
// persona handlers do, in the same order. A non-nil result is the tool error.
func (s *Server) personaTarget(name, section string) (*agent.Engine, *mcp.CallToolResult) {
	e := s.deps.Dispatcher.Agent(name)
	if e == nil {
		return nil, toolError("agent not found: " + name)
	}
	if _, _, _, ok := e.PersonaSection(section); !ok {
		return nil, toolError(fmt.Sprintf("unknown section %q, must be one of: identity, soul, user, memory", section))
	}
	return e, nil
}

func (s *Server) handlePersonaGet(ctx context.Context, _ *mcp.CallToolRequest, input personaGetInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "agents:read"); err != nil {
		return err, nil, nil
	}
	e, failed := s.personaTarget(input.Agent, input.Section)
	if failed != nil {
		return failed, nil, nil
	}
	content, editable, agentMutable, _ := e.PersonaSection(input.Section)
	r, err := toolJSON(map[string]any{
		"agent":         input.Agent,
		"section":       input.Section,
		"content":       content,
		"editable":      editable,
		"agent_mutable": agentMutable,
	})
	return r, nil, err
}

func (s *Server) handlePersonaUpdate(ctx context.Context, _ *mcp.CallToolRequest, input personaUpdateInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "agents:write"); err != nil {
		return err, nil, nil
	}
	e, failed := s.personaTarget(input.Agent, input.Section)
	if failed != nil {
		return failed, nil, nil
	}
	if err := e.SavePersonaSection(input.Section, input.Content); err != nil {
		return toolError(fmt.Sprintf("saving persona section: %v", err)), nil, nil
	}
	s.deps.Logger.Info("persona section updated via MCP", "agent", input.Agent, "section", input.Section)
	r, err := toolJSON(map[string]string{
		"agent":   input.Agent,
		"section": input.Section,
		"status":  "updated",
	})
	return r, nil, err
}
