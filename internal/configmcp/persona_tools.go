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
		Name: "persona_get",
		Description: `Read a persona section. Returns the content and flags indicating whether the section is user-editable and agent-mutable.

Sections:
- "identity": Name, emoji, theme (YAML frontmatter + markdown body). Defines how you present yourself.
- "soul": Your core personality, values, and behavioral guidelines. This is who you are.
- "user": Profile of the person you're talking to — preferences, background, routines.
- "memory": Running notes and context you want to remember across conversations.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"section": {"type": "string", "enum": ["identity", "soul", "user", "memory"], "description": "The persona section to read"}
			},
			"required": ["section"]
		}`),
	}, s.handlePersonaGet)

	s.mcpServer.AddTool(&mcp.Tool{
		Name: "persona_update",
		Description: `Replace an entire persona section. For incremental memory changes, prefer persona_memory_manage instead.

- "memory": Running notes. Writes directly (no approval, all tiers). Prefer persona_memory_manage append for adding entries.
- "user": User profile. Update when the user shares persistent personal info. Requires approval in supervised/restricted mode.
- "soul": Core personality. Update only when you have genuine reason to evolve. Requires approval in supervised/restricted mode.
- "identity": Name/emoji/theme metadata. Requires approval in supervised/restricted mode.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"section": {"type": "string", "enum": ["identity", "soul", "user", "memory"], "description": "The persona section to update"},
				"content": {"type": "string", "description": "The new content (replaces existing content entirely)"}
			},
			"required": ["section", "content"]
		}`),
	}, s.handlePersonaUpdate)

	s.mcpServer.AddTool(&mcp.Tool{
		Name: "persona_memory_manage",
		Description: `Manage individual memory entries. Preferred over persona_update for incremental memory changes.

Operations:
- "append": Add a new entry to memory (separated by ---). Use when you learn something worth remembering.
- "remove": Remove an entry by its ## heading. Use to clean up outdated information.
- "replace": Replace all memory content (same as persona_update for memory). Use only when restructuring the entire memory.

Memory writes are always direct (no approval needed, all permission tiers).`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"operation": {"type": "string", "enum": ["append", "remove", "replace"], "description": "The operation: append (add entry), remove (delete by heading), or replace (full replacement)"},
				"content": {"type": "string", "description": "For append: the new entry text. For replace: the complete new memory content."},
				"heading": {"type": "string", "description": "For remove: the heading (without ##) of the entry to remove."}
			},
			"required": ["operation"]
		}`),
	}, s.handlePersonaMemoryManage)
}

func (s *Server) handlePersonaGet(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Section string `json:"section"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	section := strings.TrimSpace(strings.ToLower(input.Section))
	if section != "identity" && section != "soul" && section != "user" && section != "memory" {
		return toolError(fmt.Sprintf("unknown section %q, must be one of: identity, soul, user, memory", input.Section)), nil
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
	if section != "identity" && section != "soul" && section != "user" && section != "memory" {
		return toolError(fmt.Sprintf("unknown section %q, must be one of: identity, soul, user, memory", input.Section)), nil
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

	// Identity, soul, and user require tier checks.
	var kind approval.ActionKind
	switch section {
	case "identity":
		kind = approval.ActionKindIdentityUpdate
	case "soul":
		kind = approval.ActionKindSoulUpdate
	case "user":
		kind = approval.ActionKindUserUpdate
	}
	summary := fmt.Sprintf("Update persona section: %s", section)

	return applyOrSubmit(ctx, s.deps, kind, summary, input.Content, applyFn)
}

func (s *Server) handlePersonaMemoryManage(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Operation string `json:"operation"`
		Content   string `json:"content"`
		Heading   string `json:"heading"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}

	getPersona := s.deps.GetPersonaSection
	if _, _, _, ok := getPersona("memory"); !ok {
		return toolError("memory section not available"), nil
	}

	switch input.Operation {
	case "append":
		if strings.TrimSpace(input.Content) == "" {
			return toolError("content is required for append operation"), nil
		}
		if s.deps.AppendMemoryEntry == nil {
			return toolError("append not available: no persona configured"), nil
		}
		if err := s.deps.AppendMemoryEntry(input.Content); err != nil {
			return toolError(fmt.Sprintf("append failed: %v", err)), nil
		}
		return toolText(`{"ok": true, "operation": "append"}`), nil

	case "remove":
		if strings.TrimSpace(input.Heading) == "" {
			return toolError("heading is required for remove operation"), nil
		}
		if s.deps.RemoveMemoryEntry == nil {
			return toolError("remove not available: no persona configured"), nil
		}
		if err := s.deps.RemoveMemoryEntry(input.Heading); err != nil {
			return toolError(fmt.Sprintf("remove failed: %v", err)), nil
		}
		return toolText(`{"ok": true, "operation": "remove"}`), nil

	case "replace":
		if strings.TrimSpace(input.Content) == "" {
			return toolError("content is required for replace operation"), nil
		}
		if s.deps.SavePersonaSection == nil {
			return toolError("replace not available: no persona configured"), nil
		}
		if err := s.deps.SavePersonaSection("memory", input.Content); err != nil {
			return toolError(fmt.Sprintf("replace failed: %v", err)), nil
		}
		return toolText(`{"ok": true, "operation": "replace"}`), nil

	default:
		return toolError(fmt.Sprintf("unknown operation %q, must be one of: append, remove, replace", input.Operation)), nil
	}
}
