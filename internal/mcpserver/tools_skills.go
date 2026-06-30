package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Temikus/denkeeper/internal/configmcp"
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
	Version     string   `json:"version,omitempty" jsonschema:"Skill version (e.g. 1.0.0)"`
	Triggers    []string `json:"triggers,omitempty" jsonschema:"Trigger keywords"`
	Body        string   `json:"body" jsonschema:"Skill content/instructions"`
}

type skillUpdateInput struct {
	Agent       string   `json:"agent" jsonschema:"Agent name"`
	Name        string   `json:"name" jsonschema:"Skill name to update"`
	NewName     *string  `json:"new_name,omitempty" jsonschema:"New skill name (rename)"`
	Description *string  `json:"description,omitempty" jsonschema:"New description"`
	Version     *string  `json:"version,omitempty" jsonschema:"New version (e.g. 1.0.0)"`
	Triggers    []string `json:"triggers,omitempty" jsonschema:"New triggers"`
	Body        *string  `json:"body,omitempty" jsonschema:"New content"`
}

type skillDeleteInput struct {
	Agent string `json:"agent" jsonschema:"Agent name"`
	Name  string `json:"name" jsonschema:"Skill name to delete"`
}

// validSkillName rejects names that would escape the skills directory once
// joined into a filesystem path. Skill names are used directly to build
// "<skillsDir>/<name>.md", so a name containing path separators or ".." could
// write to or delete files outside the agent's skills directory.
func validSkillName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid skill name %q", name)
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("skill name %q must not contain path separators or %q", name, "..")
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid skill name %q", name)
	}
	return nil
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
	if err := validSkillName(input.Name); err != nil {
		return toolError(err.Error()), nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		return toolError("skill management is not available for this agent"), nil, nil
	}

	if _, exists := e.GetSkill(input.Name); exists {
		return toolError(fmt.Sprintf("skill %q already exists", input.Name)), nil, nil
	}

	payload := configmcp.BuildSkillPayload(input.Name, input.Description, input.Version, input.Triggers, input.Body)
	if err := configmcp.ApplySkillCreate(skillsDir, e.AppendSkill, s.deps.Logger, payload); err != nil {
		return toolError("creating skill: " + err.Error()), nil, nil
	}

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

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		return toolError("skill management is not available for this agent"), nil, nil
	}

	if err := validSkillName(input.Name); err != nil {
		return toolError(err.Error()), nil, nil
	}

	existing, found := e.GetSkill(input.Name)
	if !found {
		return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Name, input.Agent)), nil, nil
	}

	// Determine effective name (rename or keep).
	newName := input.Name
	isRename := false
	if input.NewName != nil && strings.TrimSpace(*input.NewName) != "" && *input.NewName != input.Name {
		newName = strings.TrimSpace(*input.NewName)
		if err := validSkillName(newName); err != nil {
			return toolError(err.Error()), nil, nil
		}
		isRename = true
		if _, exists := e.GetSkill(newName); exists {
			return toolError(fmt.Sprintf("skill %q already exists", newName)), nil, nil
		}
	}

	payload := configmcp.MergeSkillFields(newName, existing, input.Description, input.Version, input.Triggers, input.Body)

	if isRename {
		if err := configmcp.ApplySkillRename(skillsDir, e.RemoveSkill, e.AppendSkill, s.deps.Logger, input.Name, payload); err != nil {
			return toolError("renaming skill: " + err.Error()), nil, nil
		}
		return toolText("skill renamed: " + input.Name + " → " + newName), nil, nil
	}

	if err := configmcp.ApplySkillUpdate(skillsDir, e.UpdateSkill, s.deps.Logger, input.Name, payload); err != nil {
		return toolError("updating skill: " + err.Error()), nil, nil
	}

	return toolText("skill updated: " + input.Name), nil, nil
}

func (s *Server) handleSkillDelete(ctx context.Context, _ *mcp.CallToolRequest, input skillDeleteInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:write"); err != nil {
		return err, nil, nil
	}

	if err := validSkillName(input.Name); err != nil {
		return toolError(err.Error()), nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		return toolError("skill management is not available for this agent"), nil, nil
	}

	if !e.RemoveSkill(input.Name) {
		return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Name, input.Agent)), nil, nil
	}

	if err := configmcp.RemoveSkillFile(skillsDir, input.Name); err != nil {
		s.deps.Logger.Error("skill removed from memory but file deletion failed", "name", input.Name, "error", err)
		return toolError("deleting skill file: " + err.Error()), nil, nil
	}

	return toolText("skill deleted: " + input.Name), nil, nil
}
