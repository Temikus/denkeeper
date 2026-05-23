package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type skillListInput struct {
	Agent string `json:"agent" jsonschema:"Agent name"`
}

type skillGetInput struct {
	Agent string `json:"agent" jsonschema:"Agent name"`
	Name  string `json:"name" jsonschema:"Skill name"`
}

type skillCreateInput struct {
	Agent       string   `json:"agent" jsonschema:"Agent name"`
	Name        string   `json:"name" jsonschema:"Skill name"`
	Description string   `json:"description,omitempty" jsonschema:"Skill description"`
	Triggers    []string `json:"triggers,omitempty" jsonschema:"Trigger keywords"`
	Body        string   `json:"body" jsonschema:"Skill content/instructions"`
}

type skillUpdateInput struct {
	Agent       string   `json:"agent" jsonschema:"Agent name"`
	Name        string   `json:"name" jsonschema:"Skill name to update"`
	Description *string  `json:"description,omitempty" jsonschema:"New description"`
	Triggers    []string `json:"triggers,omitempty" jsonschema:"New triggers"`
	Body        *string  `json:"body,omitempty" jsonschema:"New content"`
}

type skillDeleteInput struct {
	Agent string `json:"agent" jsonschema:"Agent name"`
	Name  string `json:"name" jsonschema:"Skill name to delete"`
}

func (s *Server) registerSkillTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_list",
		Description: "List all skills for an agent. Returns name, description, and triggers. " +
			"Requires 'skills:read' scope.",
	}, s.handleSkillList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_get",
		Description: "Get full details of a skill including its body content. " +
			"Requires 'skills:read' scope.",
	}, s.handleSkillGet)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_create",
		Description: "Create a new skill for an agent. Requires name and body at minimum. " +
			"Requires 'skills:write' scope.",
	}, s.handleSkillCreate)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_update",
		Description: "Update an existing skill. Only provided fields are changed. " +
			"Requires 'skills:write' scope.",
	}, s.handleSkillUpdate)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "skill_delete",
		Description: "Delete a skill by name. Requires 'skills:write' scope.",
	}, s.handleSkillDelete)
}

func (s *Server) handleSkillList(ctx context.Context, _ *mcp.CallToolRequest, input skillListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:read"); err != nil {
		return err, nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	type skillSummary struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Triggers    []string `json:"triggers,omitempty"`
	}

	skills := e.Skills()
	result := make([]skillSummary, len(skills))
	for i, sk := range skills {
		result[i] = skillSummary{Name: sk.Name, Description: sk.Description, Triggers: sk.Triggers}
	}

	r, err := toolJSON(result)
	return r, nil, err
}

func (s *Server) handleSkillGet(ctx context.Context, _ *mcp.CallToolRequest, input skillGetInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:read"); err != nil {
		return err, nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	sk, found := e.GetSkill(input.Name)
	if !found {
		return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Name, input.Agent)), nil, nil
	}

	r, err := toolJSON(sk)
	return r, nil, err
}

func (s *Server) handleSkillCreate(ctx context.Context, _ *mcp.CallToolRequest, input skillCreateInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:write"); err != nil {
		return err, nil, nil
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Body) == "" {
		return toolError("name and body are required"), nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	if _, exists := e.GetSkill(input.Name); exists {
		return toolError(fmt.Sprintf("skill %q already exists", input.Name)), nil, nil
	}

	sk := skill.Skill{
		Name:        input.Name,
		Description: input.Description,
		Triggers:    input.Triggers,
		Body:        input.Body,
	}
	e.AppendSkill(sk)

	return toolText("skill created: " + input.Name), nil, nil
}

func (s *Server) handleSkillUpdate(ctx context.Context, _ *mcp.CallToolRequest, input skillUpdateInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:write"); err != nil {
		return err, nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	existing, found := e.GetSkill(input.Name)
	if !found {
		return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Name, input.Agent)), nil, nil
	}

	if input.Description != nil {
		existing.Description = *input.Description
	}
	if input.Triggers != nil {
		existing.Triggers = input.Triggers
	}
	if input.Body != nil {
		existing.Body = *input.Body
	}

	if !e.UpdateSkill(input.Name, existing) {
		return toolError("update failed"), nil, nil
	}

	return toolText("skill updated: " + input.Name), nil, nil
}

func (s *Server) handleSkillDelete(ctx context.Context, _ *mcp.CallToolRequest, input skillDeleteInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:write"); err != nil {
		return err, nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	if !e.RemoveSkill(input.Name) {
		return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Name, input.Agent)), nil, nil
	}

	return toolText("skill deleted: " + input.Name), nil, nil
}
