package configmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/persona"
)

// memoryOpDocs documents each persona_memory_manage operation. Keyed by the
// operation name so the tool description can be assembled from whichever
// operations this server can actually perform.
var memoryOpDocs = map[string]string{
	"append":  `- "append": Add a new entry to memory (separated by ---). Use when you learn something worth remembering.`,
	"remove":  `- "remove": Remove an entry by its ## heading. Use to clean up outdated information.`,
	"replace": `- "replace": Replace all memory content (same as persona_update for memory). Use only when restructuring the entire memory.`,
}

// memoryOps returns the persona_memory_manage operations backed by a live
// dependency, in a stable order. The advertised schema is the capability
// statement: an operation whose dep is absent would always error, so it is
// never offered. Engines wired read-mostly (e.g. the post-turn reviewer) get
// an append-only tool this way.
func (s *Server) memoryOps() []string {
	var ops []string
	if s.deps.AppendMemoryEntry != nil {
		ops = append(ops, "append")
	}
	if s.deps.RemoveMemoryEntry != nil {
		ops = append(ops, "remove")
	}
	if s.deps.SavePersonaSection != nil {
		ops = append(ops, "replace")
	}
	return ops
}

// memoryManageTool builds the persona_memory_manage tool definition for the
// given operation set. Built rather than hardcoded so the enum, the parameter
// list, and the prose stay in sync with actual capability.
func memoryManageTool(ops []string) *mcp.Tool {
	docs := make([]string, 0, len(ops))
	for _, op := range ops {
		docs = append(docs, memoryOpDocs[op])
	}

	props := map[string]any{
		"operation": map[string]any{
			"type":        "string",
			"enum":        ops,
			"description": "The operation to perform. Available: " + strings.Join(ops, ", ") + ".",
		},
	}

	var contentUses []string
	if slices.Contains(ops, "append") {
		contentUses = append(contentUses, "For append: the new entry text.")
	}
	if slices.Contains(ops, "replace") {
		contentUses = append(contentUses, "For replace: the complete new memory content.")
	}
	if len(contentUses) > 0 {
		props["content"] = map[string]any{
			"type":        "string",
			"description": strings.Join(contentUses, " "),
		}
	}
	if slices.Contains(ops, "remove") {
		props["heading"] = map[string]any{
			"type":        "string",
			"description": "For remove: the heading (without ##) of the entry to remove.",
		}
	}

	schema, err := json.Marshal(map[string]any{
		"type":       "object",
		"properties": props,
		"required":   []string{"operation"},
	})
	if err != nil {
		// Unreachable: the map holds only strings, slices, and nested maps.
		panic(fmt.Sprintf("configmcp: marshalling persona_memory_manage schema: %v", err))
	}

	return &mcp.Tool{
		Name: "persona_memory_manage",
		Description: "Manage individual memory entries. Preferred over persona_update for incremental memory changes.\n\nOperations:\n" +
			strings.Join(docs, "\n") +
			"\n\nMemory writes are always direct (no approval needed, all permission tiers).",
		InputSchema: json.RawMessage(schema),
	}
}

// registerPersonaTools adds the persona MCP tools. Called from registerTools
// when GetPersonaSection is available. persona_update and the individual
// persona_memory_manage operations are gated separately on their own write
// deps, so a read-mostly Deps yields a read-mostly persona surface.
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

	// Whole-section replacement needs the section writer. Without it,
	// soul/identity/user are unreachable — the capability boundary the
	// post-turn reviewer relies on.
	if s.deps.SavePersonaSection != nil {
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
	}

	if ops := s.memoryOps(); len(ops) > 0 {
		s.mcpServer.AddTool(memoryManageTool(ops), s.handlePersonaMemoryManage)
	}
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

	return applyOrSubmit(ctx, s.deps, kind, summary, input.Content, applyFn, false)
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
		return s.handleMemoryAppend(input.Content)
	case "remove":
		return s.handleMemoryRemove(input.Heading)
	case "replace":
		return s.handleMemoryReplace(input.Content)
	default:
		return toolError(fmt.Sprintf("unknown operation %q, must be one of: %s",
			input.Operation, strings.Join(s.memoryOps(), ", "))), nil
	}
}

// memoryPruneHint returns advice for a full memory, naming only the pruning
// operations this server can perform. Empty when it can perform none.
func (s *Server) memoryPruneHint() string {
	switch {
	case s.deps.RemoveMemoryEntry != nil && s.deps.SavePersonaSection != nil:
		return "Use persona_memory_manage operation=remove to drop entries first, or operation=replace to consolidate."
	case s.deps.RemoveMemoryEntry != nil:
		return "Use persona_memory_manage operation=remove to drop entries first."
	case s.deps.SavePersonaSection != nil:
		return "Use persona_memory_manage operation=replace to consolidate."
	default:
		return ""
	}
}

func (s *Server) handleMemoryAppend(content string) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(content) == "" {
		return toolError("content is required for append operation"), nil
	}
	if s.deps.AppendMemoryEntry == nil {
		return toolError("append not available: no persona configured"), nil
	}
	if err := s.deps.AppendMemoryEntry(content); err != nil {
		var memFull *persona.MemoryFullError
		if errors.As(err, &memFull) {
			// Only point at pruning operations this server actually offers —
			// an append-only engine cannot act on that advice.
			hint := "Memory is full and this agent cannot prune it; report the condition in your response."
			if prune := s.memoryPruneHint(); prune != "" {
				hint = prune
			}
			resp, _ := json.Marshal(map[string]any{
				"success":         false,
				"error":           memFull.Error(),
				"current_entries": memFull.CurrentEntries,
				"usage":           fmt.Sprintf("%d/%d", memFull.Current, memFull.Limit),
				"hint":            hint,
			})
			return toolText(string(resp)), nil
		}
		return toolError(fmt.Sprintf("append failed: %v", err)), nil
	}
	if s.deps.NudgeReset != nil {
		s.deps.NudgeReset("memory")
	}
	return toolText(`{"ok": true, "operation": "append"}`), nil
}

func (s *Server) handleMemoryRemove(heading string) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(heading) == "" {
		return toolError("heading is required for remove operation"), nil
	}
	if s.deps.RemoveMemoryEntry == nil {
		return toolError("remove not available: no persona configured"), nil
	}
	if err := s.deps.RemoveMemoryEntry(heading); err != nil {
		return toolError(fmt.Sprintf("remove failed: %v", err)), nil
	}
	if s.deps.NudgeReset != nil {
		s.deps.NudgeReset("memory")
	}
	return toolText(`{"ok": true, "operation": "remove"}`), nil
}

func (s *Server) handleMemoryReplace(content string) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(content) == "" {
		return toolError("content is required for replace operation"), nil
	}
	if s.deps.SavePersonaSection == nil {
		return toolError("replace not available: no persona configured"), nil
	}
	if err := s.deps.SavePersonaSection("memory", content); err != nil {
		return toolError(fmt.Sprintf("replace failed: %v", err)), nil
	}
	return toolText(`{"ok": true, "operation": "replace"}`), nil
}
