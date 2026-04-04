package configmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/approval"
)

// registerPersonaTools adds the persona_get and persona_update MCP tools.
// Called from registerTools when persona callbacks are available.
func (s *Server) registerPersonaTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "persona_get",
		Description: "Read a persona section (soul, user, or memory). Returns the content and editability flags.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"section": {"type": "string", "description": "The persona section to read: soul, user, or memory"}
			},
			"required": ["section"]
		}`),
	}, s.handlePersonaGet)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "persona_update",
		Description: "Update a persona section (soul, user, or memory). Replaces the entire section content. Soul and user updates require approval in supervised mode.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"section": {"type": "string", "description": "The persona section to update: soul, user, or memory"},
				"content": {"type": "string", "description": "The new content for the section (replaces existing content entirely)"}
			},
			"required": ["section", "content"]
		}`),
	}, s.handlePersonaUpdate)
}

func (s *Server) handlePersonaGet(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Section string `json:"section"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	section := strings.TrimSpace(strings.ToLower(input.Section))
	if section != "soul" && section != "user" && section != "memory" {
		return toolError(fmt.Sprintf("unknown section %q, must be one of: soul, user, memory", input.Section)), nil
	}

	content, editable, agentMutable, ok := s.deps.GetPersonaSection(section)
	if !ok {
		return toolError(fmt.Sprintf("section %q not available", section)), nil
	}

	resp, _ := json.Marshal(map[string]any{
		"section":       section,
		"content":       content,
		"editable":      editable,
		"agent_mutable": agentMutable,
	})
	return toolText(string(resp)), nil
}

func (s *Server) handlePersonaUpdate(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Section string `json:"section"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	section := strings.TrimSpace(strings.ToLower(input.Section))
	if section != "soul" && section != "user" && section != "memory" {
		return toolError(fmt.Sprintf("unknown section %q, must be one of: soul, user, memory", input.Section)), nil
	}
	if strings.TrimSpace(input.Content) == "" {
		return toolError("content is required"), nil
	}

	saveFn := s.deps.SavePersonaSection
	applyFn := func(_ context.Context, payload string) error {
		return saveFn(section, payload)
	}

	// Memory writes directly (all tiers), matching existing MEMORY_UPDATE behavior.
	if section == "memory" {
		if err := saveFn(section, input.Content); err != nil {
			return toolError(fmt.Sprintf("persona_update failed: %v", err)), nil
		}
		return toolText(`{"ok": true}`), nil
	}

	// Soul and user require tier checks.
	var kind approval.ActionKind
	switch section {
	case "soul":
		kind = approval.ActionKindSoulUpdate
	case "user":
		kind = approval.ActionKindUserUpdate
	}
	summary := fmt.Sprintf("Update persona section: %s", section)

	return applyOrSubmit(ctx, s.deps, kind, summary, input.Content, applyFn)
}
